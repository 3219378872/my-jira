// Package quality reads explicitly authorized GitHub repositories and existing
// CI evidence. It never clones repositories or executes repository content.
package quality

import (
	"bytes"
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"my-jira/apps/api/internal/platform/apperror"
)

const maxJSONBytes = 8 << 20
const maxArchiveBytes = 16 << 20

var errRevoked = apperror.New(403, "github_revoked", "GitHub installation access has been revoked")
var errSource = errors.New("GitHub evidence source is missing or does not match the requested immutable revision")
var shaPattern = regexp.MustCompile(`^[a-fA-F0-9]{40}([a-fA-F0-9]{24})?$`)
var repoPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

type Config struct {
	AppID, PrivateKey, ClientID, ClientSecret, WebhookSecret, AppSlug string
	APIBase, WebBase, PublicURL                                       string
	HTTPClient                                                        *http.Client
}

func EnvironmentConfig() Config {
	return Config{AppID: os.Getenv("GITHUB_APP_ID"), PrivateKey: os.Getenv("GITHUB_APP_PRIVATE_KEY"), ClientID: os.Getenv("GITHUB_APP_CLIENT_ID"), ClientSecret: os.Getenv("GITHUB_APP_CLIENT_SECRET"), WebhookSecret: os.Getenv("GITHUB_WEBHOOK_SECRET"), AppSlug: os.Getenv("GITHUB_APP_SLUG"), APIBase: "https://api.github.com", WebBase: "https://github.com", PublicURL: strings.TrimRight(os.Getenv("APP_URL"), "/")}
}

func (c Config) configured() bool {
	return c.AppID != "" && c.PrivateKey != "" && c.ClientID != "" && c.ClientSecret != "" && c.WebhookSecret != "" && c.AppSlug != ""
}

type provider struct {
	config Config
	client *http.Client
}

func newProvider(c Config) *provider {
	if c.APIBase == "" {
		c.APIBase = "https://api.github.com"
	}
	if c.WebBase == "" {
		c.WebBase = "https://github.com"
	}
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 40 * time.Second}
	}
	copyClient := *client
	// Download redirects are handled explicitly with a separate credential-free
	// request. OAuth and API responses may never redirect bearer credentials.
	copyClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	return &provider{c, &copyClient}
}

type rateLimitError struct{ RetryAt time.Time }

func (e *rateLimitError) Error() string {
	return "GitHub rate limit reached; synchronization will retry"
}

type Repository struct {
	ID            int64  `json:"id"`
	FullName      string `json:"full_name"`
	DefaultBranch string `json:"default_branch"`
	Permissions   struct {
		Admin bool `json:"admin"`
	} `json:"permissions"`
}

type Source struct {
	CommitSHA   string    `json:"commit_sha,omitempty"`
	PullRequest int64     `json:"pull_request,omitempty"`
	RunID       int64     `json:"run_id,omitempty"`
	RunAttempt  int       `json:"run_attempt,omitempty"`
	EventTime   time.Time `json:"event_time,omitempty"`
}

type sourceFile struct {
	SHA      string `json:"sha"`
	Filename string `json:"filename"`
	Status   string `json:"status"`
	Patch    string `json:"patch,omitempty"`
}
type sourceExcerpt struct {
	Path          string `json:"path"`
	BlobSHA       string `json:"blob_sha,omitempty"`
	ContentSHA256 string `json:"content_sha256,omitempty"`
	StartLine     int    `json:"start_line,omitempty"`
	Text          string `json:"text,omitempty"`
	Status        string `json:"status"`
}
type workflowRun struct {
	ID           int64     `json:"id"`
	HeadSHA      string    `json:"head_sha"`
	RunAttempt   int       `json:"run_attempt"`
	Status       string    `json:"status"`
	Conclusion   string    `json:"conclusion"`
	UpdatedAt    time.Time `json:"updated_at"`
	PullRequests []struct {
		Number int64 `json:"number"`
	} `json:"pull_requests"`
}
type artifact struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Size        int64     `json:"size_in_bytes"`
	Expired     bool      `json:"expired"`
	Digest      string    `json:"digest"`
	UpdatedAt   time.Time `json:"updated_at"`
	WorkflowRun struct {
		ID      int64  `json:"id"`
		HeadSHA string `json:"head_sha"`
	} `json:"workflow_run"`
}
type artifactEvidence struct {
	ID       int64    `json:"id"`
	Name     string   `json:"name"`
	Revision string   `json:"revision"`
	Digest   string   `json:"sha256,omitempty"`
	Status   string   `json:"status"`
	Files    []string `json:"files,omitempty"`
}
type repositoryEvidence struct {
	Repository     string             `json:"repository"`
	CommitSHA      string             `json:"commit_sha"`
	PullRequest    int64              `json:"pull_request,omitempty"`
	PRBaseSHA      string             `json:"pr_base_sha,omitempty"`
	PRFiles        []sourceFile       `json:"pr_files,omitempty"`
	RunID          int64              `json:"run_id,omitempty"`
	RunAttempt     int                `json:"run_attempt,omitempty"`
	RunStatus      string             `json:"run_status"`
	RunConclusion  string             `json:"run_conclusion"`
	SourceAt       time.Time          `json:"source_at"`
	Message        string             `json:"message"`
	Files          []sourceFile       `json:"files"`
	SourceExcerpts []sourceExcerpt    `json:"source_excerpts"`
	Artifacts      []artifactEvidence `json:"artifacts"`
	Truncated      bool               `json:"truncated"`
	Reports        []reportFile       `json:"-"`
}

func (p *provider) appJWT() (string, error) {
	block, _ := pem.Decode([]byte(strings.ReplaceAll(p.config.PrivateKey, `\n`, "\n")))
	if block == nil {
		return "", errors.New("GitHub App private key is not configured correctly")
	}
	var key *rsa.PrivateKey
	if k, e := x509.ParsePKCS1PrivateKey(block.Bytes); e == nil {
		key = k
	} else if k, e := x509.ParsePKCS8PrivateKey(block.Bytes); e == nil {
		key, _ = k.(*rsa.PrivateKey)
	}
	if key == nil {
		return "", errors.New("GitHub App requires an RSA private key")
	}
	now := time.Now()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	payload, _ := json.Marshal(map[string]any{"iat": now.Add(-time.Minute).Unix(), "exp": now.Add(9 * time.Minute).Unix(), "iss": p.config.AppID})
	unsigned := header + "." + base64.RawURLEncoding.EncodeToString(payload)
	hash := sha256.Sum256([]byte(unsigned))
	signature, e := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hash[:])
	if e != nil {
		return "", errors.New("GitHub App signing failed")
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func limitedRead(r io.Reader, limit int64) ([]byte, error) {
	data, e := io.ReadAll(io.LimitReader(r, limit+1))
	if e != nil {
		return nil, errors.New("GitHub response could not be read")
	}
	if int64(len(data)) > limit {
		return nil, errors.New("GitHub response exceeded the configured size limit")
	}
	return data, nil
}

func (p *provider) request(ctx context.Context, method, endpoint, token string, body any, limit int64) ([]byte, http.Header, int, error) {
	var r io.Reader
	if body != nil {
		raw, e := json.Marshal(body)
		if e != nil {
			return nil, nil, 0, e
		}
		r = bytes.NewReader(raw)
	}
	req, e := http.NewRequestWithContext(ctx, method, endpoint, r)
	if e != nil {
		return nil, nil, 0, errors.New("Invalid GitHub request")
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if strings.HasSuffix(endpoint, "/login/oauth/access_token") {
		req.Header.Set("Accept", "application/json")
	}
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "my-jira-quality")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, e := p.client.Do(req)
	if e != nil {
		return nil, nil, 0, errors.New("GitHub request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode == 429 || resp.StatusCode == 403 && (resp.Header.Get("X-RateLimit-Remaining") == "0" || resp.Header.Get("Retry-After") != "") {
		retry := time.Now().Add(time.Minute)
		if seconds, e := strconv.ParseInt(resp.Header.Get("Retry-After"), 10, 64); e == nil && seconds > 0 {
			retry = time.Now().Add(time.Duration(seconds) * time.Second)
		}
		if unix, e := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64); e == nil && time.Unix(unix, 0).After(retry) {
			retry = time.Unix(unix, 0)
		}
		return nil, resp.Header, resp.StatusCode, &rateLimitError{retry}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return nil, resp.Header, resp.StatusCode, fmt.Errorf("GitHub returned status %d", resp.StatusCode)
	}
	data, e := limitedRead(resp.Body, limit)
	return data, resp.Header, resp.StatusCode, e
}

func (p *provider) api(ctx context.Context, method, endpoint, token string, body, into any) (http.Header, error) {
	data, headers, _, e := p.request(ctx, method, strings.TrimRight(p.config.APIBase, "/")+endpoint, token, body, maxJSONBytes)
	if e != nil {
		return headers, e
	}
	if into != nil && json.Unmarshal(data, into) != nil {
		return headers, errors.New("GitHub returned an invalid JSON response")
	}
	return headers, nil
}

func (p *provider) installationToken(ctx context.Context, installationID, repositoryID int64) (string, error) {
	jwt, e := p.appJWT()
	if e != nil {
		return "", e
	}
	var installation struct {
		SuspendedAt *time.Time `json:"suspended_at"`
	}
	data, _, status, e := p.request(ctx, "GET", fmt.Sprintf("%s/app/installations/%d", p.config.APIBase, installationID), jwt, nil, maxJSONBytes)
	if status == 404 || status == 401 {
		return "", errRevoked
	}
	if e != nil {
		return "", e
	}
	if json.Unmarshal(data, &installation) != nil {
		return "", errors.New("GitHub installation response is invalid")
	}
	if installation.SuspendedAt != nil {
		return "", errRevoked
	}
	body := map[string]any{"repository_ids": []int64{repositoryID}, "permissions": map[string]string{"contents": "read", "pull_requests": "read", "actions": "read", "checks": "read"}}
	var result struct {
		Token string `json:"token"`
	}
	_, e = p.api(ctx, "POST", fmt.Sprintf("/app/installations/%d/access_tokens", installationID), jwt, body, &result)
	if e != nil {
		return "", e
	}
	if result.Token == "" {
		return "", errors.New("GitHub installation token was not issued")
	}
	return result.Token, nil
}

// userInstallation verifies an installation using the independent GitHub App
// OAuth identity, rather than trusting an installation ID supplied by a browser.
// The user token is discarded after the short-lived repository grant is built.
func (p *provider) userInstallation(ctx context.Context, code string, installationID int64, verifier string) ([]Repository, string, error) {
	if verifier == "" {
		return nil, "", errors.New("GitHub App user authorization requires a PKCE verifier")
	}
	body := map[string]string{"client_id": p.config.ClientID, "client_secret": p.config.ClientSecret, "code": code, "code_verifier": verifier}
	if p.config.PublicURL != "" {
		body["redirect_uri"] = p.config.PublicURL + "/api/v1/github/callback"
	}
	data, _, _, e := p.request(ctx, "POST", p.config.WebBase+"/login/oauth/access_token", "", body, maxJSONBytes)
	if e != nil {
		return nil, "", e
	}
	var auth struct {
		AccessToken string `json:"access_token"`
	}
	if json.Unmarshal(data, &auth) != nil || auth.AccessToken == "" {
		return nil, "", errors.New("GitHub App user authorization failed")
	}
	// A valid user's repository response both proves installation membership and
	// supplies repository administration rights. No OAuth login token is reused.
	repos := []Repository{}
	for page := 1; page <= 100; page++ {
		var result struct {
			Repositories []Repository `json:"repositories"`
		}
		headers, e := p.api(ctx, "GET", fmt.Sprintf("/user/installations/%d/repositories?per_page=100&page=%d", installationID, page), auth.AccessToken, nil, &result)
		if e != nil {
			return nil, "", e
		}
		for _, repo := range result.Repositories {
			if repo.Permissions.Admin && repo.ID > 0 && repoPattern.MatchString(repo.FullName) {
				repos = append(repos, repo)
			}
		}
		if !hasNext(headers) {
			break
		}
		if page == 100 {
			return nil, "", errors.New("GitHub installation repository pagination exceeded the configured limit")
		}
	}
	if len(repos) == 0 {
		return nil, "", errors.New("The GitHub identity administers no repositories in this installation")
	}
	jwt, e := p.appJWT()
	if e != nil {
		return nil, "", e
	}
	var install struct {
		ID      int64 `json:"id"`
		Account struct {
			Login string `json:"login"`
		} `json:"account"`
		SuspendedAt *time.Time `json:"suspended_at"`
	}
	_, e = p.api(ctx, "GET", fmt.Sprintf("/app/installations/%d", installationID), jwt, nil, &install)
	if e != nil {
		return nil, "", e
	}
	if install.ID != installationID || install.SuspendedAt != nil {
		return nil, "", errRevoked
	}
	return repos, install.Account.Login, nil
}

func (p *provider) pkceVerifier(state string) string {
	mac := hmac.New(sha256.New, []byte(p.config.ClientSecret))
	mac.Write([]byte("github-app-pkce:" + state))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func hasNext(headers http.Header) bool { return strings.Contains(headers.Get("Link"), `rel="next"`) }

func (p *provider) readEvidence(ctx context.Context, installationID, repositoryID int64, repository string, source Source, authorize func() error) (repositoryEvidence, error) {
	result := repositoryEvidence{Repository: repository, Files: []sourceFile{}, Artifacts: []artifactEvidence{}, Reports: []reportFile{}, RunStatus: "missing", RunConclusion: "unknown"}
	if !repoPattern.MatchString(repository) {
		return result, errSource
	}
	if e := authorize(); e != nil {
		return result, e
	}
	token, e := p.installationToken(ctx, installationID, repositoryID)
	if e != nil {
		return result, e
	}
	read := func(path string, into any) (http.Header, error) {
		if e := authorize(); e != nil {
			return nil, e
		}
		return p.api(ctx, "GET", "/repos/"+repository+path, token, nil, into)
	}
	var repo Repository
	if _, e = read("", &repo); e != nil {
		return result, e
	}
	if repo.ID != repositoryID {
		return result, errSource
	}
	sha := source.CommitSHA
	var run workflowRun
	if source.RunID > 0 {
		path := fmt.Sprintf("/actions/runs/%d", source.RunID)
		if source.RunAttempt > 0 {
			path += fmt.Sprintf("/attempts/%d", source.RunAttempt)
		}
		if _, e = read(path, &run); e != nil {
			return result, e
		}
		if run.ID != source.RunID || source.RunAttempt > 0 && run.RunAttempt != source.RunAttempt {
			return result, errSource
		}
	} else if source.PullRequest == 0 {
		var runs struct {
			WorkflowRuns []workflowRun `json:"workflow_runs"`
		}
		path := "/actions/runs?status=completed&per_page=100"
		if sha != "" {
			path += "&head_sha=" + url.QueryEscape(sha)
		}
		if _, e = read(path, &runs); e != nil {
			return result, e
		}
		if len(runs.WorkflowRuns) > 0 {
			run = runs.WorkflowRuns[0]
		}
	}
	if source.PullRequest > 0 {
		var pull struct {
			Number int64  `json:"number"`
			Title  string `json:"title"`
			Body   string `json:"body"`
			Head   struct {
				SHA string `json:"sha"`
			} `json:"head"`
			Base struct {
				SHA string `json:"sha"`
			} `json:"base"`
			UpdatedAt time.Time `json:"updated_at"`
		}
		if _, e = read(fmt.Sprintf("/pulls/%d", source.PullRequest), &pull); e != nil {
			return result, e
		}
		if pull.Number != source.PullRequest || sha != "" && !strings.EqualFold(sha, pull.Head.SHA) {
			return result, errSource
		}
		sha = pull.Head.SHA
		result.PullRequest = pull.Number
		result.PRBaseSHA = pull.Base.SHA
		result.Message = pull.Title + "\n" + pull.Body
		result.SourceAt = pull.UpdatedAt
		if run.ID == 0 {
			var runs struct {
				WorkflowRuns []workflowRun `json:"workflow_runs"`
			}
			if _, e = read("/actions/runs?status=completed&per_page=100&head_sha="+url.QueryEscape(sha), &runs); e != nil {
				return result, e
			}
			if len(runs.WorkflowRuns) > 0 {
				run = runs.WorkflowRuns[0]
			}
		}
	}
	if run.ID > 0 {
		if run.RunAttempt < 1 {
			return result, errSource
		}
		if sha != "" && !strings.EqualFold(sha, run.HeadSHA) {
			return result, errSource
		}
		sha = run.HeadSHA
		result.RunID = run.ID
		result.RunAttempt = run.RunAttempt
		result.RunStatus = run.Status
		result.RunConclusion = run.Conclusion
		if run.UpdatedAt.After(result.SourceAt) {
			result.SourceAt = run.UpdatedAt
		}
		if result.PullRequest == 0 && len(run.PullRequests) == 1 {
			result.PullRequest = run.PullRequests[0].Number
		}
	}
	ref := sha
	if ref == "" {
		ref = repo.DefaultBranch
	}
	if ref == "" {
		return result, errSource
	}
	for page := 1; page <= 10; page++ {
		var commit struct {
			SHA    string `json:"sha"`
			Commit struct {
				Message   string `json:"message"`
				Committer struct {
					Date time.Time `json:"date"`
				} `json:"committer"`
			} `json:"commit"`
			Files []sourceFile `json:"files"`
		}
		headers, e := read("/commits/"+url.PathEscape(ref)+fmt.Sprintf("?per_page=100&page=%d", page), &commit)
		if e != nil {
			return result, e
		}
		if !shaPattern.MatchString(commit.SHA) || sha != "" && !strings.EqualFold(sha, commit.SHA) {
			return result, errSource
		}
		sha = commit.SHA
		ref = sha
		result.CommitSHA = sha
		if page == 1 {
			result.Message += "\n" + commit.Commit.Message
			if result.SourceAt.IsZero() {
				result.SourceAt = commit.Commit.Committer.Date
			}
		}
		for _, file := range commit.Files {
			if len(file.Patch) > 16000 {
				file.Patch = file.Patch[:16000]
				result.Truncated = true
			}
			result.Files = append(result.Files, file)
		}
		if !hasNext(headers) {
			break
		}
		if page == 10 {
			result.Truncated = true
		}
	}
	if result.SourceAt.IsZero() {
		result.SourceAt = time.Now().UTC()
	}
	if shaPattern.MatchString(result.PRBaseSHA) {
		var comparison struct {
			Files  []sourceFile `json:"files"`
			Status string       `json:"status"`
		}
		if _, e = read("/compare/"+result.PRBaseSHA+"..."+result.CommitSHA+"?per_page=100", &comparison); e != nil {
			return result, e
		}
		result.PRFiles = comparison.Files
		// GitHub's compare response caps changed files at 300. Preserve this gap
		// instead of claiming the available PR diff is the entire change.
		if len(result.PRFiles) >= 300 {
			result.Truncated = true
		}
		for i := range result.PRFiles {
			if len(result.PRFiles[i].Patch) > 16000 {
				result.PRFiles[i].Patch = result.PRFiles[i].Patch[:16000]
				result.Truncated = true
			}
		}
	}
	if result.RunID > 0 {
		// The run artifact endpoint is not attempt scoped. After a rerun we cannot
		// attribute its current artifacts to an older attempt, so refuse mixing.
		var latest workflowRun
		if _, e = read(fmt.Sprintf("/actions/runs/%d", run.ID), &latest); e != nil {
			return result, e
		}
		if latest.RunAttempt != run.RunAttempt {
			result.Artifacts = append(result.Artifacts, artifactEvidence{Name: "run artifacts", Status: "unknown_attempt"})
			return result, nil
		}
		artifactCount := 0
		var downloadedBytes, reportBytes int
		for page := 1; page <= 10; page++ {
			var data struct {
				Artifacts []artifact `json:"artifacts"`
			}
			headers, e := read(fmt.Sprintf("/actions/runs/%d/artifacts?per_page=100&page=%d", run.ID, page), &data)
			if e != nil {
				return result, e
			}
			for _, a := range data.Artifacts {
				artifactCount++
				if artifactCount > 100 {
					result.Truncated = true
					break
				}
				ae := artifactEvidence{ID: a.ID, Name: a.Name, Revision: a.UpdatedAt.UTC().Format(time.RFC3339Nano) + ":" + a.Digest, Status: "unknown"}
				if a.Expired {
					ae.Status = "missing"
				} else if downloadedBytes >= 64<<20 || reportBytes >= 32<<20 {
					ae.Status = "budget_exceeded"
					result.Truncated = true
				} else if a.Size > maxArchiveBytes {
					ae.Status = "too_large"
				} else if a.WorkflowRun.ID != 0 && (a.WorkflowRun.ID != run.ID || a.WorkflowRun.HeadSHA != sha) {
					ae.Status = "source_mismatch"
				} else {
					if e := authorize(); e != nil {
						return result, e
					}
					raw, e := p.downloadArtifact(ctx, repository, a.ID, token)
					if e != nil {
						var limit *rateLimitError
						if errors.As(e, &limit) {
							return result, e
						}
						ae.Status = "unavailable"
					} else {
						downloadedBytes += len(raw)
						digest := sha256.Sum256(raw)
						ae.Digest = hex.EncodeToString(digest[:])
						if a.Digest != "" && a.Digest != "sha256:"+ae.Digest {
							ae.Status = "digest_mismatch"
						} else if files, e := unpackReports(raw, a.ID, ae.Digest); e != nil {
							ae.Status = "invalid_archive"
						} else {
							ae.Status = "available"
							artifactReportBytes := 0
							for _, file := range files {
								artifactReportBytes += len(file.Data)
							}
							if reportBytes+artifactReportBytes > 32<<20 {
								ae.Status = "budget_exceeded"
								result.Truncated = true
								files = nil
							} else {
								reportBytes += artifactReportBytes
							}
							for _, file := range files {
								ae.Files = append(ae.Files, file.Name)
							}
							result.Reports = append(result.Reports, files...)
						}
					}
				}
				result.Artifacts = append(result.Artifacts, ae)
			}
			if !hasNext(headers) || artifactCount > 100 {
				break
			}
			if page == 10 {
				result.Truncated = true
			}
		}
		var confirmed workflowRun
		if _, e = read(fmt.Sprintf("/actions/runs/%d", run.ID), &confirmed); e != nil {
			return result, e
		}
		if confirmed.RunAttempt != run.RunAttempt || confirmed.HeadSHA != result.CommitSHA {
			result.Reports = nil
			for i := range result.Artifacts {
				result.Artifacts[i].Status = "unknown_attempt"
			}
			return result, nil
		}
	}
	paths := map[string]int{}
	for _, file := range result.Reports {
		if !(strings.HasSuffix(strings.ToLower(file.Name), ".sarif") || strings.HasSuffix(strings.ToLower(file.Name), ".sarif.json")) {
			continue
		}
		findings, e := readSARIF(file)
		if e != nil {
			continue
		}
		for _, finding := range findings {
			if finding.Actionable {
				paths[finding.Path] = finding.Line
			}
		}
	}
	filenames := make([]string, 0, len(paths))
	for filename := range paths {
		filenames = append(filenames, filename)
	}
	sort.Strings(filenames)
	for _, filename := range filenames {
		line := paths[filename]
		if len(result.SourceExcerpts) >= 20 {
			result.Truncated = true
			break
		}
		excerpt := sourceExcerpt{Path: filename, Status: "unknown"}
		if strings.ContainsAny(filename, ":\\?#") || strings.HasPrefix(filename, "/") || path.Clean(filename) != filename || filename == ".." || strings.HasPrefix(filename, "../") {
			result.SourceExcerpts = append(result.SourceExcerpts, excerpt)
			continue
		}
		var file struct {
			SHA      string `json:"sha"`
			Content  string `json:"content"`
			Encoding string `json:"encoding"`
			Size     int    `json:"size"`
			Type     string `json:"type"`
		}
		if _, e = read("/contents/"+url.PathEscape(filename)+"?ref="+result.CommitSHA, &file); e != nil {
			var limit *rateLimitError
			if errors.As(e, &limit) {
				return result, e
			}
			if errors.Is(e, errRevoked) {
				return result, e
			}
			result.SourceExcerpts = append(result.SourceExcerpts, excerpt)
			continue
		}
		if file.Encoding != "base64" || file.Size > 256<<10 || file.Type != "file" {
			result.SourceExcerpts = append(result.SourceExcerpts, excerpt)
			continue
		}
		content, e := base64.StdEncoding.DecodeString(strings.ReplaceAll(file.Content, "\n", ""))
		if e != nil || len(content) > 256<<10 {
			result.SourceExcerpts = append(result.SourceExcerpts, excerpt)
			continue
		}
		hash := sha256.Sum256(content)
		excerpt.BlobSHA = file.SHA
		excerpt.ContentSHA256 = hex.EncodeToString(hash[:])
		lines := strings.Split(string(content), "\n")
		start := max(line-6, 0)
		end := min(line+5, len(lines))
		if start >= end {
			result.SourceExcerpts = append(result.SourceExcerpts, excerpt)
			continue
		}
		excerpt.StartLine = start + 1
		excerpt.Text = strings.Join(lines[start:end], "\n")
		if len(excerpt.Text) > 16000 {
			excerpt.Text = excerpt.Text[:16000]
		}
		excerpt.Status = "available"
		result.SourceExcerpts = append(result.SourceExcerpts, excerpt)
	}
	return result, nil
}

func (p *provider) downloadArtifact(ctx context.Context, repository string, artifactID int64, token string) ([]byte, error) {
	endpoint := fmt.Sprintf("%s/repos/%s/actions/artifacts/%d/zip", p.config.APIBase, repository, artifactID)
	data, headers, status, e := p.request(ctx, "GET", endpoint, token, nil, maxArchiveBytes)
	if e != nil {
		return nil, e
	}
	if status < 300 {
		return data, nil
	}
	target, e := url.Parse(headers.Get("Location"))
	if e != nil || target.Scheme != "https" || target.User != nil {
		return nil, errors.New("GitHub artifact redirect was rejected")
	}
	host := strings.ToLower(target.Hostname())
	if !(strings.HasSuffix(host, ".blob.core.windows.net") || strings.HasSuffix(host, ".actions.githubusercontent.com") || strings.HasSuffix(host, ".githubusercontent.com")) {
		return nil, errors.New("GitHub artifact redirect host was rejected")
	}
	// Deliberately omit Authorization when retrieving an expiring signed URL.
	data, _, status, e = p.request(ctx, "GET", target.String(), "", nil, maxArchiveBytes)
	if e != nil {
		return nil, e
	}
	if status >= 300 {
		return nil, errors.New("GitHub artifact redirected unexpectedly")
	}
	return data, nil
}
