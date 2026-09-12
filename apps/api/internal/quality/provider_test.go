package quality

import (
	"context"
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
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func providerConfig(t *testing.T, endpoint string) Config {
	t.Helper()
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	return Config{AppID: "91", PrivateKey: string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})), ClientID: "app-client", ClientSecret: "test-only-client-secret", WebhookSecret: "test-only-webhook-secret", AppSlug: "myjira-test", APIBase: endpoint, WebBase: endpoint}
}

func TestProviderInstallationScopePaginationAndImmutableArtifacts(t *testing.T) {
	sha := strings.Repeat("a", 40)
	archive := testArchive(t, map[string]string{"code.sarif": sarifDefect, "tests.xml": `<testsuite><testcase name="fail"><failure message="broken"/></testcase></testsuite>`})
	digest := sha256.Sum256(archive)
	var commitPages, artifactPages, authChecks atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/app/installations/5":
			fmt.Fprint(w, `{"id":5}`)
		case r.URL.Path == "/app/installations/5/access_tokens":
			var input struct {
				RepositoryIDs []int64           `json:"repository_ids"`
				Permissions   map[string]string `json:"permissions"`
			}
			if json.NewDecoder(r.Body).Decode(&input) != nil || len(input.RepositoryIDs) != 1 || input.RepositoryIDs[0] != 99 || input.Permissions["contents"] != "read" || input.Permissions["actions"] != "read" {
				t.Errorf("token permission scope wrong: %#v", input)
			}
			fmt.Fprint(w, `{"token":"installation-only-fixture-token"}`)
		case r.URL.Path == "/repos/org/repo":
			fmt.Fprint(w, `{"id":99,"full_name":"org/repo","default_branch":"main"}`)
		case r.URL.Path == "/repos/org/repo/actions/runs/42" || r.URL.Path == "/repos/org/repo/actions/runs/42/attempts/2":
			fmt.Fprintf(w, `{"id":42,"head_sha":%q,"run_attempt":2,"status":"completed","conclusion":"failure","updated_at":"2026-09-11T12:00:00Z"}`, sha)
		case strings.HasPrefix(r.URL.Path, "/repos/org/repo/commits/"):
			commitPages.Add(1)
			if r.URL.Query().Get("page") == "1" {
				w.Header().Set("Link", `<https://api.github.com/ignored>; rel="next"`)
			}
			fmt.Fprintf(w, `{"sha":%q,"commit":{"message":"Fix MAIN-1","committer":{"date":"2026-09-11T11:00:00Z"}},"files":[{"filename":"server/query.go","sha":"blob-version","patch":"+ changed"}]}`, sha)
		case r.URL.Path == "/repos/org/repo/actions/runs/42/artifacts":
			artifactPages.Add(1)
			if r.URL.Query().Get("page") == "1" {
				w.Header().Set("Link", `<https://evil.invalid/must-not-follow>; rel="next"`)
				fmt.Fprintf(w, `{"artifacts":[{"id":7,"name":"reports","size_in_bytes":%d,"digest":"sha256:%s","updated_at":"2026-09-11T12:00:00Z","workflow_run":{"id":42,"head_sha":%q}}]}`, len(archive), hex.EncodeToString(digest[:]), sha)
			} else {
				fmt.Fprint(w, `{"artifacts":[]}`)
			}
		case r.URL.Path == "/repos/org/repo/actions/artifacts/7/zip":
			w.Header().Set("Content-Type", "application/zip")
			_, _ = w.Write(archive)
		case r.URL.Path == "/repos/org/repo/contents/server/query.go":
			content := strings.Repeat("// context\n", 26) + "query(untrusted)\n"
			fmt.Fprintf(w, `{"sha":"blob","type":"file","size":%d,"encoding":"base64","content":%q}`, len(content), base64.StdEncoding.EncodeToString([]byte(content)))
		default:
			t.Errorf("unexpected provider request %s", r.URL.String())
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	p := newProvider(providerConfig(t, server.URL))
	evidence, e := p.readEvidence(context.Background(), 5, 99, "org/repo", Source{RunID: 42, RunAttempt: 2}, func() error { authChecks.Add(1); return nil })
	if e != nil {
		t.Fatal(e)
	}
	if evidence.CommitSHA != sha || evidence.RunID != 42 || evidence.RunAttempt != 2 || len(evidence.Reports) != 2 || evidence.Artifacts[0].Digest != hex.EncodeToString(digest[:]) {
		t.Fatalf("evidence mismatch: %#v", evidence)
	}
	if commitPages.Load() != 2 || artifactPages.Load() != 2 || authChecks.Load() < 8 {
		t.Fatalf("pagination or access rechecks missing: %d %d %d", commitPages.Load(), artifactPages.Load(), authChecks.Load())
	}
	dim, findings := analyze(evidence, nil)
	if dim.Code.Status != "failed" || dim.Tests.Status != "failed" || len(findings) < 2 {
		t.Fatalf("known provider artifact defects missing: %#v", dim)
	}
}

func TestProviderRevocationRateLimitAndRedirectBoundary(t *testing.T) {
	var mode atomic.Int32
	var reads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		switch mode.Load() {
		case 0:
			w.WriteHeader(404)
		case 1:
			w.Header().Set("Retry-After", "120")
			w.WriteHeader(429)
		case 2:
			w.Header().Set("Location", "https://evil.invalid/steal")
			w.WriteHeader(302)
		}
	}))
	defer server.Close()
	p := newProvider(providerConfig(t, server.URL))
	if _, e := p.installationToken(context.Background(), 5, 99); !errors.Is(e, errRevoked) {
		t.Fatalf("missing installation not revoked: %v", e)
	}
	mode.Store(1)
	_, e := p.installationToken(context.Background(), 5, 99)
	var limit *rateLimitError
	if !errors.As(e, &limit) || time.Until(limit.RetryAt) < 119*time.Second {
		t.Fatalf("rate limit ignored: %v", e)
	}
	mode.Store(2)
	before := reads.Load()
	if _, e = p.downloadArtifact(context.Background(), "org/repo", 5, "fixture-token"); e == nil {
		t.Fatal("credential redirect accepted")
	}
	if reads.Load() != before+1 {
		t.Fatal("unexpected redirect request")
	}
	before = reads.Load()
	_, e = p.readEvidence(context.Background(), 5, 99, "org/repo", Source{}, func() error { return errRevoked })
	if !errors.Is(e, errRevoked) || reads.Load() != before {
		t.Fatal("provider called after local revocation")
	}
}

func TestOAuthInstallationProofRequiresRepositoryAdministration(t *testing.T) {
	var userReads atomic.Int32
	var providerCalls atomic.Int32
	var verifier string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerCalls.Add(1)
		switch r.URL.Path {
		case "/login/oauth/access_token":
			if r.Header.Get("Accept") != "application/json" {
				t.Error("OAuth must request JSON")
			}
			var request map[string]string
			if e := json.NewDecoder(r.Body).Decode(&request); e != nil {
				t.Error(e)
			}
			if request["code_verifier"] != verifier || len(verifier) != 43 {
				t.Error("OAuth must prove the initiating PKCE challenge")
			}
			fmt.Fprint(w, `{"access_token":"separate-app-user-token"}`)
		case "/user/installations/5/repositories":
			if r.Header.Get("Authorization") != "Bearer separate-app-user-token" {
				t.Error("used wrong authorization surface")
			}
			userReads.Add(1)
			fmt.Fprint(w, `{"repositories":[{"id":99,"full_name":"org/repo","permissions":{"admin":true}},{"id":100,"full_name":"org/readonly","permissions":{"admin":false}}]}`)
		case "/app/installations/5":
			fmt.Fprint(w, `{"id":5,"account":{"login":"org"}}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	p := newProvider(providerConfig(t, server.URL))
	if _, _, e := p.userInstallation(context.Background(), "fixture-code", 5, ""); e == nil || providerCalls.Load() != 0 {
		t.Fatal("OAuth without a PKCE verifier reached the provider")
	}
	verifier = p.pkceVerifier("fixture-session-installation-state")
	repos, account, e := p.userInstallation(context.Background(), "fixture-code", 5, verifier)
	if e != nil || len(repos) != 1 || repos[0].ID != 99 || account != "org" || userReads.Load() != 1 {
		t.Fatalf("installation proof incorrect: %#v %s %v", repos, account, e)
	}
}

func TestHistoricalAttemptNeverConsumesLatestAttemptArtifacts(t *testing.T) {
	sha := strings.Repeat("a", 40)
	var archives atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/app/installations/5":
			fmt.Fprint(w, `{"id":5}`)
		case r.URL.Path == "/app/installations/5/access_tokens":
			fmt.Fprint(w, `{"token":"scoped-test-token"}`)
		case r.URL.Path == "/repos/org/repo":
			fmt.Fprint(w, `{"id":99,"full_name":"org/repo"}`)
		case strings.HasSuffix(r.URL.Path, "/attempts/1"):
			fmt.Fprintf(w, `{"id":42,"head_sha":%q,"run_attempt":1,"status":"completed","conclusion":"success","updated_at":"2026-09-10T12:00:00Z"}`, sha)
		case r.URL.Path == "/repos/org/repo/actions/runs/42":
			fmt.Fprintf(w, `{"id":42,"head_sha":%q,"run_attempt":2,"status":"completed","conclusion":"success"}`, sha)
		case strings.HasPrefix(r.URL.Path, "/repos/org/repo/commits/"):
			fmt.Fprintf(w, `{"sha":%q,"commit":{"message":"old attempt","committer":{"date":"2026-09-10T12:00:00Z"}},"files":[]}`, sha)
		case strings.Contains(r.URL.Path, "/artifacts"):
			archives.Add(1)
			fmt.Fprint(w, `{"artifacts":[]}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	p := newProvider(providerConfig(t, server.URL))
	evidence, e := p.readEvidence(context.Background(), 5, 99, "org/repo", Source{RunID: 42, RunAttempt: 1}, func() error { return nil })
	if e != nil {
		t.Fatal(e)
	}
	if archives.Load() != 0 || len(evidence.Artifacts) != 1 || evidence.Artifacts[0].Status != "unknown_attempt" {
		t.Fatalf("old run consumed current attempt artifacts: %#v", evidence)
	}
	dim, _ := analyze(evidence, nil)
	if dim.Tests.Status != "unknown" {
		t.Fatalf("unattributed attempt evidence was reported %q", dim.Tests.Status)
	}
	_, e = p.readEvidence(context.Background(), 5, 99, "org/repo", Source{CommitSHA: strings.Repeat("b", 40), RunID: 42, RunAttempt: 1}, func() error { return nil })
	if !errors.Is(e, errSource) {
		t.Fatalf("mismatched SHA and run were combined: %v", e)
	}
}
