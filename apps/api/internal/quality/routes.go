package quality

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"my-jira/apps/api/internal/platform"
	"my-jira/apps/api/internal/platform/apperror"
	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/httpapi"
	"my-jira/apps/api/internal/platform/identity"
	"my-jira/apps/api/internal/platform/jobs"
	"my-jira/apps/api/internal/support/data"
	"my-jira/apps/api/internal/workitems"
)

type handler struct {
	d platform.Dependencies
	p *provider
}

func Register(r *gin.RouterGroup, d platform.Dependencies) {
	RegisterWithConfig(r, d, EnvironmentConfig())
}
func RegisterWithConfig(r *gin.RouterGroup, d platform.Dependencies, config Config) {
	h := newHandler(d, config)
	p := r.Group("/workspaces/:workspaceID/projects/:projectID")
	p.GET("/github", h.github)
	p.POST("/github/connect", h.connect)
	p.POST("/github/bindings", h.bind)
	p.DELETE("/github/bindings/:bindingID", h.unbind)
	p.POST("/github/bindings/:bindingID/sync", h.sync)
	p.GET("/quality/reports", h.reports)
	p.GET("/quality/reports/:reportID", h.report)
}
func RegisterPublic(r *gin.RouterGroup, d platform.Dependencies) {
	RegisterPublicWithConfig(r, d, EnvironmentConfig())
}
func RegisterPublicWithConfig(r *gin.RouterGroup, d platform.Dependencies, config Config) {
	h := newHandler(d, config)
	r.POST("/github/webhook", h.webhook)
	r.GET("/github/callback", h.callback)
}
func newHandler(d platform.Dependencies, config Config) *handler {
	if d.Jobs == nil {
		d.Jobs = jobs.Outbox{}
	}
	return &handler{d, newProvider(config)}
}

func (h *handler) scope(c *gin.Context, role identity.Role) (identity.Scope, error) {
	a, e := httpapi.Actor(c)
	if e != nil {
		return identity.Scope{}, e
	}
	if a.TokenWorkspaceID != uuid.Nil {
		return identity.Scope{}, apperror.Forbidden()
	}
	w, e := httpapi.UUIDParam(c, "workspaceID")
	if e != nil {
		return identity.Scope{}, e
	}
	p, e := httpapi.UUIDParam(c, "projectID")
	if e != nil {
		return identity.Scope{}, e
	}
	return data.PageProjectScope(c.Request.Context(), h.d.DB.SQL, a, w, p, role)
}
func lockProject(ctx context.Context, q database.DBTX, projectID uuid.UUID) error {
	var id uuid.UUID
	return q.QueryRowContext(ctx, `SELECT id FROM projects WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, projectID).Scan(&id)
}
func (h *handler) current(ctx context.Context, q database.DBTX, s identity.Scope, role identity.Role) error {
	if e := lockProject(ctx, q, s.ProjectID); e != nil {
		return e
	}
	if _, e := workitems.CurrentScope(ctx, q, s.Actor, s.WorkspaceID, s.ProjectID, role); e != nil {
		return e
	}
	_, e := data.PageProjectScope(ctx, q, s.Actor, s.WorkspaceID, s.ProjectID, role)
	return e
}

func queryJSON(ctx context.Context, q database.DBTX, statement string, args ...any) ([]json.RawMessage, error) {
	rows, e := q.QueryContext(ctx, statement, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	result := []json.RawMessage{}
	for rows.Next() {
		var raw []byte
		if e = rows.Scan(&raw); e != nil {
			return nil, e
		}
		result = append(result, json.RawMessage(raw))
	}
	return result, rows.Err()
}

func (h *handler) github(c *gin.Context) {
	s, e := h.scope(c, identity.Member)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	bindings, e := queryJSON(c.Request.Context(), h.d.DB.SQL, `SELECT to_jsonb(b) FROM github_bindings b WHERE workspace_id=$1 AND project_id=$2 ORDER BY created_at,id`, s.WorkspaceID, s.ProjectID)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	grants, e := queryJSON(c.Request.Context(), h.d.DB.SQL, `SELECT jsonb_build_object('installation_id',g.installation_id,'repositories',g.repositories,'expires_at',g.expires_at) FROM github_installation_grants g JOIN github_installations i USING(installation_id) WHERE g.workspace_id=$1 AND g.project_id=$2 AND g.actor_id=$3 AND g.expires_at>now() AND i.revoked_at IS NULL ORDER BY g.created_at DESC`, s.WorkspaceID, s.ProjectID, s.Actor.UserID)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	httpapi.JSON(c, 200, gin.H{"configured": h.p.config.configured(), "install_url": "", "bindings": bindings, "grants": grants, "required_permissions": gin.H{"contents": "read", "pull_requests": "read", "actions": "read", "checks": "read"}})
}

func (h *handler) connect(c *gin.Context) {
	s, e := h.scope(c, identity.Admin)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	if !h.p.config.configured() {
		httpapi.Fail(c, apperror.New(503, "github_not_configured", "The independent GitHub App is not configured"))
		return
	}
	raw := make([]byte, 32)
	if _, e = rand.Read(raw); e != nil {
		httpapi.Fail(c, e)
		return
	}
	// The complete opaque state is hashed in the database. Including the
	// initiating session identity lets the callback recheck a logout without
	// persisting an OAuth token or introducing another session store.
	state := base64.RawURLEncoding.EncodeToString(raw) + "." + s.Actor.SessionID.String()
	hash := sha256.Sum256([]byte(state))
	expires := time.Now().Add(10 * time.Minute)
	e = h.d.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if e := h.current(c.Request.Context(), q, s, identity.Admin); e != nil {
			return e
		}
		_, e := q.ExecContext(c.Request.Context(), `INSERT INTO github_connect_states(state_hash,workspace_id,project_id,actor_id,expires_at) VALUES($1,$2,$3,$4,$5)`, hex.EncodeToString(hash[:]), s.WorkspaceID, s.ProjectID, s.Actor.UserID, expires)
		return e
	})
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	httpapi.JSON(c, 201, gin.H{"install_url": h.p.config.WebBase + "/apps/" + url.PathEscape(h.p.config.AppSlug) + "/installations/new?state=" + url.QueryEscape(state), "expires_at": expires})
}

func (h *handler) callback(c *gin.Context) {
	state, code := c.Query("state"), c.Query("code")
	parts := strings.Split(state, ".")
	if len(parts) != 2 && len(parts) != 3 {
		httpapi.Fail(c, apperror.Invalid("A valid installation state is required"))
		return
	}
	if code != "" && len(parts) != 3 {
		httpapi.Fail(c, apperror.Invalid("The OAuth callback must use the setup authorization state"))
		return
	}
	sessionID, sessionErr := uuid.Parse(parts[1])
	installationText := c.Query("installation_id")
	if len(parts) == 3 {
		if installationText != "" && installationText != parts[2] {
			httpapi.Fail(c, apperror.Invalid("Installation state does not match the callback"))
			return
		}
		installationText = parts[2]
	}
	installationID, e := strconv.ParseInt(installationText, 10, 64)
	if !h.p.config.configured() || e != nil || sessionErr != nil || installationID <= 0 || len(state) < 32 || len(state) > 200 || len(code) > 500 {
		httpapi.Fail(c, apperror.Invalid("A valid GitHub App installation authorization callback is required"))
		return
	}
	hash := sha256.Sum256([]byte(strings.Join(parts[:2], ".")))
	stateHash := hex.EncodeToString(hash[:])
	var s identity.Scope
	s.Actor.SessionID = sessionID
	e = h.d.DB.SQL.QueryRowContext(c.Request.Context(), `SELECT workspace_id,project_id,actor_id FROM github_connect_states WHERE state_hash=$1 AND expires_at>now() AND consumed_at IS NULL`, stateHash).Scan(&s.WorkspaceID, &s.ProjectID, &s.Actor.UserID)
	if e != nil {
		httpapi.Fail(c, apperror.Invalid("The installation authorization state is expired or has already been used"))
		return
	}
	if code == "" {
		if len(parts) != 2 {
			httpapi.Fail(c, apperror.Invalid("The OAuth callback must include its authorization code"))
			return
		}
		if e = h.d.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error { return h.current(c.Request.Context(), q, s, identity.Admin) }); e != nil {
			httpapi.Fail(c, e)
			return
		}
		// GitHub's setup URL receives installation_id; the OAuth callback receives
		// code/state. Bridge those two documented phases without trusting the ID.
		oauthState := state + "." + strconv.FormatInt(installationID, 10)
		challenge := sha256.Sum256([]byte(h.p.pkceVerifier(oauthState)))
		values := url.Values{"client_id": {h.p.config.ClientID}, "state": {oauthState}, "code_challenge": {base64.RawURLEncoding.EncodeToString(challenge[:])}, "code_challenge_method": {"S256"}}
		if h.p.config.PublicURL != "" {
			values.Set("redirect_uri", h.p.config.PublicURL+"/api/v1/github/callback")
		}
		c.Redirect(http.StatusSeeOther, h.p.config.WebBase+"/login/oauth/authorize?"+values.Encode())
		return
	}
	// Consume before exchanging a code: concurrent callback retries cannot replay
	// authorization or replace the project attached to the original browser state.
	e = h.d.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if e := h.current(c.Request.Context(), q, s, identity.Admin); e != nil {
			return e
		}
		res, e := q.ExecContext(c.Request.Context(), `UPDATE github_connect_states SET consumed_at=now() WHERE state_hash=$1 AND expires_at>now() AND consumed_at IS NULL`, stateHash)
		if e != nil {
			return e
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return apperror.Conflict("The installation authorization state was already consumed")
		}
		return nil
	})
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	repositories, account, e := h.p.userInstallation(c.Request.Context(), code, installationID, h.p.pkceVerifier(state))
	if e != nil {
		httpapi.Fail(c, apperror.New(502, "github_authorization_failed", "GitHub could not verify repository administration for this installation"))
		return
	}
	repos, _ := json.Marshal(repositories)
	e = h.d.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if e := h.current(c.Request.Context(), q, s, identity.Admin); e != nil {
			return e
		}
		// A tombstone from a deleted/suspended installation is never cleared by a
		// late callback. Reinstalling creates a new installation ID.
		_, e := q.ExecContext(c.Request.Context(), `INSERT INTO github_installations(installation_id,account_login) VALUES($1,$2) ON CONFLICT(installation_id) DO UPDATE SET account_login=EXCLUDED.account_login,updated_at=now()`, installationID, account)
		if e != nil {
			return e
		}
		var revoked sql.NullTime
		if e = q.QueryRowContext(c.Request.Context(), `SELECT revoked_at FROM github_installations WHERE installation_id=$1 FOR UPDATE`, installationID).Scan(&revoked); e != nil {
			return e
		}
		if revoked.Valid {
			return apperror.Forbidden()
		}
		_, e = q.ExecContext(c.Request.Context(), `INSERT INTO github_installation_grants(id,workspace_id,project_id,actor_id,installation_id,repositories,expires_at) VALUES($1,$2,$3,$4,$5,$6::jsonb,now()+interval '15 minutes')`, uuid.New(), s.WorkspaceID, s.ProjectID, s.Actor.UserID, installationID, string(repos))
		return e
	})
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	var slug string
	if e = h.d.DB.SQL.QueryRowContext(c.Request.Context(), `SELECT slug FROM workspaces WHERE id=$1`, s.WorkspaceID).Scan(&slug); e != nil {
		httpapi.Fail(c, e)
		return
	}
	c.Redirect(http.StatusSeeOther, fmt.Sprintf("%s/w/%s/projects/%s/automation?tab=quality&github=connected", h.p.config.PublicURL, url.PathEscape(slug), s.ProjectID))
}

func (h *handler) bind(c *gin.Context) {
	s, e := h.scope(c, identity.Admin)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	body, e := httpapi.Bind[struct {
		InstallationID int64  `json:"installation_id"`
		Repository     string `json:"repository"`
	}](c)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	if body.InstallationID <= 0 || !repoPattern.MatchString(body.Repository) {
		httpapi.Fail(c, apperror.Invalid("A verified installation and owner/repository are required"))
		return
	}
	var repositoryID int64
	e = h.d.DB.SQL.QueryRowContext(c.Request.Context(), `SELECT (r->>'id')::bigint FROM github_installation_grants g CROSS JOIN LATERAL jsonb_array_elements(g.repositories) r WHERE g.workspace_id=$1 AND g.project_id=$2 AND g.actor_id=$3 AND g.installation_id=$4 AND g.expires_at>now() AND lower(r->>'full_name')=lower($5) ORDER BY g.created_at DESC LIMIT 1`, s.WorkspaceID, s.ProjectID, s.Actor.UserID, body.InstallationID, body.Repository).Scan(&repositoryID)
	if e != nil {
		httpapi.Fail(c, apperror.Forbidden())
		return
	}
	// Verify selected-repository access is still live, using a token scoped only
	// to this repository. The grant itself is rechecked in the commit transaction.
	token, e := h.p.installationToken(c.Request.Context(), body.InstallationID, repositoryID)
	if e != nil {
		httpapi.Fail(c, apperror.New(502, "github_access_failed", "GitHub repository access could not be verified"))
		return
	}
	var repo Repository
	_, e = h.p.api(c.Request.Context(), "GET", "/repos/"+body.Repository, token, nil, &repo)
	if e != nil || repo.ID != repositoryID {
		httpapi.Fail(c, apperror.Forbidden())
		return
	}
	var raw []byte
	e = h.d.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if e := h.current(c.Request.Context(), q, s, identity.Admin); e != nil {
			return e
		}
		var revoked sql.NullTime
		if e := q.QueryRowContext(c.Request.Context(), `SELECT revoked_at FROM github_installations WHERE installation_id=$1 FOR UPDATE`, body.InstallationID).Scan(&revoked); e != nil {
			return e
		}
		if revoked.Valid {
			return apperror.Forbidden()
		}
		var grant bool
		if e := q.QueryRowContext(c.Request.Context(), `SELECT EXISTS(SELECT 1 FROM github_installation_grants g CROSS JOIN LATERAL jsonb_array_elements(g.repositories) r WHERE g.workspace_id=$1 AND g.project_id=$2 AND g.actor_id=$3 AND g.installation_id=$4 AND g.expires_at>now() AND (r->>'id')::bigint=$5)`, s.WorkspaceID, s.ProjectID, s.Actor.UserID, body.InstallationID, repositoryID).Scan(&grant); e != nil {
			return e
		}
		if !grant {
			return apperror.Forbidden()
		}
		return q.QueryRowContext(c.Request.Context(), `INSERT INTO github_bindings(id,workspace_id,project_id,installation_id,repository_id,repository,created_by) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(project_id,repository_id) DO UPDATE SET installation_id=EXCLUDED.installation_id,repository=EXCLUDED.repository,active=true,created_by=EXCLUDED.created_by,sync_status='idle',last_error='',next_retry_at=NULL,updated_at=now() RETURNING to_jsonb(github_bindings)`, uuid.New(), s.WorkspaceID, s.ProjectID, body.InstallationID, repositoryID, repo.FullName, s.Actor.UserID).Scan(&raw)
	})
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	httpapi.JSON(c, 201, json.RawMessage(raw))
}

func (h *handler) unbind(c *gin.Context) {
	s, e := h.scope(c, identity.Admin)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	id, e := httpapi.UUIDParam(c, "bindingID")
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	e = h.d.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if e := h.current(c.Request.Context(), q, s, identity.Admin); e != nil {
			return e
		}
		res, e := q.ExecContext(c.Request.Context(), `UPDATE github_bindings SET active=false,sync_status='disabled',updated_at=now() WHERE id=$1 AND workspace_id=$2 AND project_id=$3`, id, s.WorkspaceID, s.ProjectID)
		if e != nil {
			return e
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return apperror.NotFound()
		}
		return nil
	})
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	c.Status(204)
}

func (h *handler) sync(c *gin.Context) {
	s, e := h.scope(c, identity.Admin)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	id, e := httpapi.UUIDParam(c, "bindingID")
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	source, e := httpapi.Bind[Source](c)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	if source.CommitSHA != "" && !shaPattern.MatchString(source.CommitSHA) || source.PullRequest < 0 || source.RunID < 0 || source.RunAttempt < 0 || source.RunAttempt > 0 && source.RunID == 0 || !source.EventTime.IsZero() {
		httpapi.Fail(c, apperror.Invalid("Supply an immutable commit SHA, PR number, or Actions run and attempt"))
		return
	}
	requestID := uuid.New()
	e = h.d.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if e := h.current(c.Request.Context(), q, s, identity.Admin); e != nil {
			return e
		}
		if e := authorizeBinding(c.Request.Context(), q, id, s.WorkspaceID, s.ProjectID, true); e != nil {
			return e
		}
		return h.enqueue(c.Request.Context(), q, requestID, s.WorkspaceID, s.ProjectID, id, source)
	})
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	httpapi.JSON(c, 202, gin.H{"id": requestID, "status": "queued"})
}

func (h *handler) enqueue(ctx context.Context, q database.DBTX, id, wid, pid, bid uuid.UUID, source Source) error {
	raw, _ := json.Marshal(source)
	_, e := q.ExecContext(ctx, `INSERT INTO github_sync_requests(id,workspace_id,project_id,binding_id,source) VALUES($1,$2,$3,$4,$5::jsonb) ON CONFLICT(id) DO NOTHING`, id, wid, pid, bid, string(raw))
	if e != nil {
		return e
	}
	if _, e = q.ExecContext(ctx, `UPDATE github_bindings SET sync_status='queued',updated_at=now() WHERE id=$1 AND active`, bid); e != nil {
		return e
	}
	return h.d.Jobs.Publish(ctx, q, "quality.sync", map[string]any{"request_id": id}, "quality.sync:"+id.String())
}

func authorizeBinding(ctx context.Context, q database.DBTX, id, wid, pid uuid.UUID, lock bool) error {
	statement := `SELECT b.active,i.revoked_at FROM github_bindings b JOIN github_installations i USING(installation_id) WHERE b.id=$1 AND b.workspace_id=$2 AND b.project_id=$3`
	if lock {
		var revoked sql.NullTime
		if e := q.QueryRowContext(ctx, `SELECT i.revoked_at FROM github_installations i WHERE i.installation_id=(SELECT installation_id FROM github_bindings WHERE id=$1 AND workspace_id=$2 AND project_id=$3) FOR UPDATE`, id, wid, pid).Scan(&revoked); e != nil {
			return e
		}
		if revoked.Valid {
			return errRevoked
		}
		statement += " FOR UPDATE OF b"
	}
	var active bool
	var revoked sql.NullTime
	e := q.QueryRowContext(ctx, statement, id, wid, pid).Scan(&active, &revoked)
	if e != nil {
		return e
	}
	if !active || revoked.Valid {
		return errRevoked
	}
	return nil
}

func (h *handler) reports(c *gin.Context) {
	s, e := h.scope(c, identity.Member)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	reports := []json.RawMessage{}
	e = h.d.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if _, e := data.PageProjectScope(c.Request.Context(), q, s.Actor, s.WorkspaceID, s.ProjectID, identity.Member); e != nil {
			return e
		}
		candidates, e := queryJSON(c.Request.Context(), q, `SELECT (to_jsonb(r)-'evidence'-'findings')||jsonb_build_object('is_latest',b.latest_report_id=r.id) FROM quality_reports r JOIN github_bindings b ON b.id=r.binding_id WHERE r.workspace_id=$1 AND r.project_id=$2 ORDER BY r.source_at DESC,r.created_at DESC,r.id LIMIT 100`, s.WorkspaceID, s.ProjectID)
		if e != nil {
			return e
		}
		for _, raw := range candidates {
			var r struct {
				ID uuid.UUID `json:"id"`
			}
			if e = json.Unmarshal(raw, &r); e != nil {
				return e
			}
			if e = validateReportSources(c.Request.Context(), q, r.ID, s.WorkspaceID, s.ProjectID, false); e == nil {
				reports = append(reports, raw)
			} else {
				var api *apperror.Error
				if !errors.As(e, &api) {
					return e
				}
			}
		}
		return nil
	})
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	httpapi.JSON(c, 200, reports)
}
func (h *handler) report(c *gin.Context) {
	s, e := h.scope(c, identity.Member)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	id, e := httpapi.UUIDParam(c, "reportID")
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	var raw []byte
	e = h.d.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if _, e := data.PageProjectScope(c.Request.Context(), q, s.Actor, s.WorkspaceID, s.ProjectID, identity.Member); e != nil {
			return e
		}
		if e := validateReportSources(c.Request.Context(), q, id, s.WorkspaceID, s.ProjectID, false); e != nil {
			return e
		}
		return q.QueryRowContext(c.Request.Context(), `SELECT to_jsonb(r)||jsonb_build_object('actions',COALESCE((SELECT jsonb_agg(to_jsonb(a)) FROM quality_repair_actions a WHERE a.report_id=r.id),'[]'::jsonb)) FROM quality_reports r WHERE id=$1 AND workspace_id=$2 AND project_id=$3`, id, s.WorkspaceID, s.ProjectID).Scan(&raw)
	})
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	httpapi.JSON(c, 200, json.RawMessage(raw))
}

func verifySignature(secret, signature string, body []byte) bool {
	if secret == "" || !strings.HasPrefix(signature, "sha256=") {
		return false
	}
	supplied, e := hex.DecodeString(strings.TrimPrefix(signature, "sha256="))
	if e != nil || len(supplied) != sha256.Size {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(supplied, mac.Sum(nil))
}

type webhookPayload struct {
	Action       string `json:"action"`
	Installation struct {
		ID int64 `json:"id"`
	} `json:"installation"`
	Repository struct {
		ID int64 `json:"id"`
	} `json:"repository"`
	RepositoriesRemoved []struct {
		ID int64 `json:"id"`
	} `json:"repositories_removed"`
	After      string `json:"after"`
	Deleted    bool   `json:"deleted"`
	HeadCommit struct {
		Timestamp time.Time `json:"timestamp"`
	} `json:"head_commit"`
	PullRequest struct {
		Number int64 `json:"number"`
		Head   struct {
			SHA string `json:"sha"`
		} `json:"head"`
		UpdatedAt time.Time `json:"updated_at"`
	} `json:"pull_request"`
	WorkflowRun workflowRun `json:"workflow_run"`
}

func (h *handler) webhook(c *gin.Context) {
	body, e := limitedRead(c.Request.Body, 2<<20)
	if e != nil {
		httpapi.Fail(c, apperror.Invalid("GitHub event exceeds the size limit"))
		return
	}
	if !verifySignature(h.p.config.WebhookSecret, c.GetHeader("X-Hub-Signature-256"), body) {
		httpapi.Fail(c, apperror.Unauthorized())
		return
	}
	delivery := c.GetHeader("X-GitHub-Delivery")
	if parsed, e := uuid.Parse(delivery); e != nil {
		httpapi.Fail(c, apperror.Invalid("A GitHub delivery identifier is required"))
		return
	} else {
		delivery = parsed.String()
	}
	event := c.GetHeader("X-GitHub-Event")
	if len(event) > 100 {
		httpapi.Fail(c, apperror.Invalid("Invalid GitHub event"))
		return
	}
	var payload webhookPayload
	if json.Unmarshal(body, &payload) != nil {
		httpapi.Fail(c, apperror.Invalid("Invalid GitHub event payload"))
		return
	}
	if event == "ping" {
		httpapi.JSON(c, 200, gin.H{"accepted": true})
		return
	}
	if payload.Installation.ID <= 0 {
		httpapi.Fail(c, apperror.Invalid("Installation-bound GitHub event required"))
		return
	}
	hash := sha256.Sum256(body)
	duplicate := false
	e = h.d.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		res, e := q.ExecContext(c.Request.Context(), `INSERT INTO github_deliveries(delivery_id,installation_id,event_type,payload_hash) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, delivery, payload.Installation.ID, event, hex.EncodeToString(hash[:]))
		if e != nil {
			return e
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			var oldHash string
			if e = q.QueryRowContext(c.Request.Context(), `SELECT payload_hash FROM github_deliveries WHERE delivery_id=$1`, delivery).Scan(&oldHash); e != nil {
				if errors.Is(e, sql.ErrNoRows) {
					duplicate = true
					return nil
				}
				return e
			}
			if oldHash != hex.EncodeToString(hash[:]) {
				return apperror.Conflict("The delivery identifier was already used for a different payload")
			}
			duplicate = true
			return nil
		}
		bindings, e := loadInstallationBindings(c.Request.Context(), q, payload.Installation.ID)
		if e != nil {
			return e
		}
		projects := map[uuid.UUID]bool{}
		for _, b := range bindings {
			projects[b.ProjectID] = true
		}
		ids := []string{}
		for id := range projects {
			ids = append(ids, id.String())
		}
		sort.Strings(ids)
		for _, id := range ids {
			if e = lockProject(c.Request.Context(), q, uuid.MustParse(id)); e != nil {
				return e
			}
		}
		if event == "installation" && (payload.Action == "deleted" || payload.Action == "suspend") {
			_, e = q.ExecContext(c.Request.Context(), `INSERT INTO github_installations(installation_id,revoked_at) VALUES($1,now()) ON CONFLICT(installation_id) DO UPDATE SET revoked_at=COALESCE(github_installations.revoked_at,now()),updated_at=now()`, payload.Installation.ID)
			if e != nil {
				return e
			}
			_, e = q.ExecContext(c.Request.Context(), `UPDATE github_bindings SET active=false,sync_status='revoked',last_error='Installation authorization revoked',updated_at=now() WHERE installation_id=$1`, payload.Installation.ID)
			return e
		}
		if event == "installation_repositories" && payload.Action == "removed" {
			for _, repo := range payload.RepositoriesRemoved {
				if _, e = q.ExecContext(c.Request.Context(), `UPDATE github_bindings SET active=false,sync_status='revoked',last_error='Repository removed from installation',updated_at=now() WHERE installation_id=$1 AND repository_id=$2`, payload.Installation.ID, repo.ID); e != nil {
					return e
				}
			}
			return nil
		}
		var source Source
		valid := true
		switch event {
		case "push":
			source = Source{CommitSHA: payload.After, EventTime: payload.HeadCommit.Timestamp}
			valid = !payload.Deleted && shaPattern.MatchString(source.CommitSHA)
		case "pull_request":
			source = Source{CommitSHA: payload.PullRequest.Head.SHA, PullRequest: payload.PullRequest.Number, EventTime: payload.PullRequest.UpdatedAt}
			valid = payload.Action == "opened" || payload.Action == "synchronize" || payload.Action == "reopened" || payload.Action == "edited"
		case "workflow_run":
			source = Source{CommitSHA: payload.WorkflowRun.HeadSHA, RunID: payload.WorkflowRun.ID, RunAttempt: payload.WorkflowRun.RunAttempt, EventTime: payload.WorkflowRun.UpdatedAt}
			valid = payload.Action == "completed"
		default:
			valid = false
		}
		if !valid {
			return nil
		}
		for _, b := range bindings {
			if b.RepositoryID != payload.Repository.ID {
				continue
			}
			if e = authorizeBinding(c.Request.Context(), q, b.ID, b.WorkspaceID, b.ProjectID, true); errors.Is(e, errRevoked) {
				continue
			} else if e != nil {
				return e
			}
			requestID := uuid.NewSHA1(uuid.NameSpaceOID, []byte("quality:"+delivery+":"+b.ID.String()))
			if e = h.enqueue(c.Request.Context(), q, requestID, b.WorkspaceID, b.ProjectID, b.ID, source); e != nil {
				return e
			}
		}
		return nil
	})
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	httpapi.JSON(c, 202, gin.H{"accepted": true, "duplicate": duplicate})
}

type binding struct {
	ID, WorkspaceID, ProjectID, CreatedBy uuid.UUID
	InstallationID, RepositoryID          int64
	Repository                            string
}

func loadInstallationBindings(ctx context.Context, q database.DBTX, installationID int64) ([]binding, error) {
	rows, e := q.QueryContext(ctx, `SELECT id,workspace_id,project_id,created_by,installation_id,repository_id,repository FROM github_bindings WHERE installation_id=$1 AND active ORDER BY project_id,id`, installationID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	result := []binding{}
	for rows.Next() {
		var b binding
		if e = rows.Scan(&b.ID, &b.WorkspaceID, &b.ProjectID, &b.CreatedBy, &b.InstallationID, &b.RepositoryID, &b.Repository); e != nil {
			return nil, e
		}
		result = append(result, b)
	}
	return result, rows.Err()
}
