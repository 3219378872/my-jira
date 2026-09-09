package integrations

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"my-jira/apps/api/internal/platform"
	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/httpapi"
	"my-jira/apps/api/internal/platform/identity"
	"my-jira/apps/api/internal/workitems"
)

type handler struct{ d platform.Dependencies }

func Register(r *gin.RouterGroup, d platform.Dependencies) {
	h := &handler{d}
	w := r.Group("/workspaces/:workspaceID")
	w.POST("/ai/text", h.aiText)
	w.GET("/images", h.images)
	w.GET("/api-tokens", h.tokens)
	w.POST("/api-tokens", h.createToken)
	w.DELETE("/api-tokens/:tokenID", h.deleteToken)
	w.GET("/webhooks", h.webhooks)
	w.POST("/webhooks", h.createWebhook)
	w.PATCH("/webhooks/:webhookID", h.updateWebhook)
	w.DELETE("/webhooks/:webhookID", h.deleteWebhook)
	w.GET("/webhooks/:webhookID/deliveries", h.deliveries)
	w.POST("/webhooks/:webhookID/test", h.testWebhook)
	w.POST("/webhooks/:webhookID/rotate-secret", h.rotateWebhookSecret)
	w.GET("/exports", h.exports)
	w.POST("/exports", h.createExport)
	w.GET("/exports/:exportID/download", h.downloadExport)
	p := w.Group("/projects/:projectID")
	p.GET("/site", h.site)
	p.PUT("/site", h.saveSite)
	r.POST("/public/:slug/issues/:issueID/comments", h.publicComment)
	r.PATCH("/public/:slug/issues/:issueID/comments/:commentID", h.updatePublicComment)
	r.DELETE("/public/:slug/issues/:issueID/comments/:commentID", h.updatePublicComment)
	r.POST("/public/:slug/issues/:issueID/reactions", h.publicReaction)
	r.DELETE("/public/:slug/issues/:issueID/reactions", h.publicReaction)
	r.POST("/public/:slug/issues/:issueID/vote", h.publicVote)
	r.DELETE("/public/:slug/issues/:issueID/vote", h.publicVote)
	r.POST("/public/:slug/intake", h.publicIntake)
}
func RegisterPublic(r *gin.RouterGroup, d platform.Dependencies) {
	h := &handler{d}
	r.GET("/public/:slug", h.publicSite)
	r.GET("/public/:slug/issues", h.publicIssues)
	r.GET("/public/:slug/issues/:issueID", h.publicIssue)
	r.GET("/public/:slug/issues/:issueID/comments", h.publicComments)
	r.GET("/public/:slug/issues/:issueID/reactions", h.publicReactions)
	r.GET("/public/:slug/issues/:issueID/attachments", h.publicAttachments)
	r.GET("/public/:slug/issues/:issueID/attachments/:assetID/download", h.publicAttachment)
}
func (h *handler) scope(c *gin.Context, role identity.Role) (identity.Scope, error) {
	a, e := httpapi.Actor(c)
	if e != nil {
		return identity.Scope{}, e
	}
	w, e := httpapi.UUIDParam(c, "workspaceID")
	if e != nil {
		return identity.Scope{}, e
	}
	if c.Param("projectID") != "" {
		p, e := httpapi.UUIDParam(c, "projectID")
		if e != nil {
			return identity.Scope{}, e
		}
		return h.d.Policy.Project(c.Request.Context(), a, w, p, role)
	}
	return h.d.Policy.Workspace(c.Request.Context(), a, w, role)
}
func fail(c *gin.Context, code int, message string) {
	name := map[int]string{400: "validation_failed", 401: "unauthorized", 403: "forbidden", 404: "not_found", 409: "conflict"}[code]
	if name == "" {
		name = "request_failed"
	}
	httpapi.Fail(c, httpapi.NewError(code, name, message))
}
func (h *handler) query(c *gin.Context, query string, args ...any) {
	rows, e := h.d.DB.SQL.QueryContext(c.Request.Context(), query, args...)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	defer rows.Close()
	data := []json.RawMessage{}
	for rows.Next() {
		var raw []byte
		if e = rows.Scan(&raw); e != nil {
			httpapi.Fail(c, e)
			return
		}
		data = append(data, raw)
	}
	if e = rows.Err(); e != nil {
		httpapi.Fail(c, e)
		return
	}
	httpapi.JSON(c, 200, data)
}
func secret(prefix string) (string, string, error) {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		return "", "", e
	}
	token := prefix + base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(token))
	return token, hex.EncodeToString(sum[:]), nil
}

func (h *handler) tokens(c *gin.Context) {
	s, e := h.scope(c, identity.Member)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	h.query(c, `SELECT to_jsonb(t)-'token_hash'-'deleted_at' FROM api_tokens t WHERE workspace_id=$1 AND user_id=$2 AND revoked_at IS NULL AND deleted_at IS NULL ORDER BY created_at DESC`, s.WorkspaceID, s.Actor.UserID)
}
func (h *handler) createToken(c *gin.Context) {
	s, e := h.scope(c, identity.Member)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	if s.Actor.TokenWorkspaceID != uuid.Nil {
		fail(c, 403, "API tokens cannot create new credentials")
		return
	}
	var body struct {
		Name      string     `json:"name"`
		ExpiresAt *time.Time `json:"expires_at"`
	}
	if e = c.ShouldBindJSON(&body); e != nil || strings.TrimSpace(body.Name) == "" || len(body.Name) > 100 {
		fail(c, 400, "A token name of up to 100 characters is required")
		return
	}
	if body.ExpiresAt != nil && body.ExpiresAt.Before(time.Now()) {
		fail(c, 400, "Expiration must be in the future")
		return
	}
	token, hash, e := secret("mjt_")
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	var raw []byte
	e = h.d.DB.SQL.QueryRowContext(c.Request.Context(), `INSERT INTO api_tokens(id,user_id,workspace_id,name,token_hash,prefix,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING to_jsonb(api_tokens)-'token_hash'-'deleted_at'`, uuid.New(), s.Actor.UserID, s.WorkspaceID, body.Name, hash, token[:12], body.ExpiresAt).Scan(&raw)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	var data map[string]any
	_ = json.Unmarshal(raw, &data)
	data["token"] = token
	httpapi.JSON(c, 201, data)
}
func (h *handler) deleteToken(c *gin.Context) {
	s, e := h.scope(c, identity.Member)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	id, e := httpapi.UUIDParam(c, "tokenID")
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	res, e := h.d.DB.SQL.ExecContext(c.Request.Context(), `UPDATE api_tokens SET revoked_at=now() WHERE id=$1 AND workspace_id=$2 AND user_id=$3 AND revoked_at IS NULL`, id, s.WorkspaceID, s.Actor.UserID)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		fail(c, 404, "Token not found")
		return
	}
	c.Status(204)
}

func (h *handler) webhooks(c *gin.Context) {
	s, e := h.scope(c, identity.Admin)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	h.query(c, `SELECT to_jsonb(w)-'secret'-'deleted_at' FROM webhooks w WHERE workspace_id=$1 AND deleted_at IS NULL ORDER BY created_at DESC`, s.WorkspaceID)
}

type webhookInput struct {
	URL      string   `json:"url"`
	Events   []string `json:"events"`
	IsActive *bool    `json:"is_active"`
}

func (h *handler) createWebhook(c *gin.Context) {
	s, e := h.scope(c, identity.Admin)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	var b webhookInput
	if e = c.ShouldBindJSON(&b); e != nil {
		fail(c, 400, "Invalid webhook configuration")
		return
	}
	if e = validateDestination(c.Request.Context(), b.URL); e != nil {
		fail(c, 400, e.Error())
		return
	}
	if len(b.Events) == 0 {
		b.Events = []string{"work_item.changed"}
	}
	for _, event := range b.Events {
		if !validWebhookEvent(event) {
			fail(c, 400, "Unknown webhook event")
			return
		}
	}
	value, _, e := secret("whsec_")
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	rawEvents, _ := json.Marshal(b.Events)
	active := true
	if b.IsActive != nil {
		active = *b.IsActive
	}
	var raw []byte
	e = h.d.DB.SQL.QueryRowContext(c.Request.Context(), `INSERT INTO webhooks(id,workspace_id,created_by,url,secret,events,is_active) VALUES($1,$2,$3,$4,$5,$6::jsonb,$7) RETURNING to_jsonb(webhooks)-'deleted_at'`, uuid.New(), s.WorkspaceID, s.Actor.UserID, b.URL, value, string(rawEvents), active).Scan(&raw)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	httpapi.JSON(c, 201, json.RawMessage(raw))
}
func (h *handler) updateWebhook(c *gin.Context) {
	s, e := h.scope(c, identity.Admin)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	id, e := httpapi.UUIDParam(c, "webhookID")
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	var b webhookInput
	if e = c.ShouldBindJSON(&b); e != nil {
		fail(c, 400, "Invalid webhook configuration")
		return
	}
	var oldEvents []byte
	var oldURL string
	var active bool
	e = h.d.DB.SQL.QueryRowContext(c.Request.Context(), `SELECT url,events,is_active FROM webhooks WHERE id=$1 AND workspace_id=$2 AND deleted_at IS NULL`, id, s.WorkspaceID).Scan(&oldURL, &oldEvents, &active)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	if b.URL == "" {
		b.URL = oldURL
	}
	if e = validateDestination(c.Request.Context(), b.URL); e != nil {
		fail(c, 400, e.Error())
		return
	}
	if b.IsActive != nil {
		active = *b.IsActive
	}
	if b.Events != nil {
		for _, event := range b.Events {
			if !validWebhookEvent(event) {
				fail(c, 400, "Unknown event")
				return
			}
		}
		oldEvents, _ = json.Marshal(b.Events)
	}
	var raw []byte
	e = h.d.DB.SQL.QueryRowContext(c.Request.Context(), `UPDATE webhooks SET url=$3,events=$4::jsonb,is_active=$5,updated_at=now() WHERE id=$1 AND workspace_id=$2 RETURNING to_jsonb(webhooks)-'secret'-'deleted_at'`, id, s.WorkspaceID, b.URL, string(oldEvents), active).Scan(&raw)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	httpapi.JSON(c, 200, json.RawMessage(raw))
}
func (h *handler) deleteWebhook(c *gin.Context) {
	s, e := h.scope(c, identity.Admin)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	id, e := httpapi.UUIDParam(c, "webhookID")
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	res, e := h.d.DB.SQL.ExecContext(c.Request.Context(), `UPDATE webhooks SET deleted_at=now(),is_active=false WHERE id=$1 AND workspace_id=$2 AND deleted_at IS NULL`, id, s.WorkspaceID)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		fail(c, 404, "Webhook not found")
		return
	}
	c.Status(204)
}
func (h *handler) deliveries(c *gin.Context) {
	s, e := h.scope(c, identity.Admin)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	id, e := httpapi.UUIDParam(c, "webhookID")
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	h.query(c, `SELECT to_jsonb(d)-'deleted_at' FROM webhook_deliveries d WHERE webhook_id=$1 AND workspace_id=$2 AND deleted_at IS NULL ORDER BY created_at DESC LIMIT 100`, id, s.WorkspaceID)
}
func (h *handler) testWebhook(c *gin.Context) {
	s, e := h.scope(c, identity.Admin)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	id, e := httpapi.UUIDParam(c, "webhookID")
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	event := uuid.New()
	e = h.d.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		var valid bool
		if e := q.QueryRowContext(c.Request.Context(), `SELECT EXISTS(SELECT 1 FROM webhooks WHERE id=$1 AND workspace_id=$2 AND deleted_at IS NULL)`, id, s.WorkspaceID).Scan(&valid); e != nil {
			return e
		}
		if !valid {
			return httpapi.NewError(404, "not_found", "Webhook not found")
		}
		return h.d.Jobs.Publish(c.Request.Context(), q, "webhook.deliver", map[string]any{"webhook_id": id, "event_id": event, "event": "webhook.test", "data": map[string]string{"message": "Webhook connection test"}}, event.String())
	})
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	httpapi.JSON(c, 202, gin.H{"event_id": event, "status": "queued"})
}

func (h *handler) exports(c *gin.Context) {
	s, e := h.scope(c, identity.Member)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	h.query(c, `SELECT to_jsonb(e)-'object_key'-'deleted_at' FROM exports e WHERE workspace_id=$1 AND requested_by=$2 AND deleted_at IS NULL ORDER BY created_at DESC LIMIT 100`, s.WorkspaceID, s.Actor.UserID)
}
func (h *handler) createExport(c *gin.Context) {
	s, e := h.scope(c, identity.Member)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	var body struct {
		Format    string         `json:"format"`
		ProjectID *uuid.UUID     `json:"project_id"`
		Filters   map[string]any `json:"filters"`
	}
	if e = c.ShouldBindJSON(&body); e != nil || (body.Format != "csv" && body.Format != "json" && body.Format != "xlsx") {
		fail(c, 400, "Choose csv, xlsx or json format")
		return
	}
	if body.ProjectID != nil {
		if _, e = h.d.Policy.Project(c.Request.Context(), s.Actor, s.WorkspaceID, *body.ProjectID, identity.Member); e != nil {
			httpapi.Fail(c, e)
			return
		}
	}
	if body.Filters == nil {
		body.Filters = map[string]any{}
	}
	if exportsDeleted(body.Filters) {
		if body.ProjectID != nil {
			_, e = h.d.Policy.Project(c.Request.Context(), s.Actor, s.WorkspaceID, *body.ProjectID, identity.Admin)
		} else {
			_, e = h.d.Policy.Workspace(c.Request.Context(), s.Actor, s.WorkspaceID, identity.Admin)
		}
		if e != nil {
			httpapi.Fail(c, e)
			return
		}
	}
	if _, _, e = workitems.CompileFilters(c.Request.Context(), s.Actor, "", nil, body.Filters); e != nil {
		fail(c, 400, e.Error())
		return
	}
	filters, _ := json.Marshal(body.Filters)
	id := uuid.New()
	e = h.d.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if _, e := q.ExecContext(c.Request.Context(), `INSERT INTO exports(id,workspace_id,project_id,requested_by,format,filters,status) VALUES($1,$2,$3,$4,$5,$6::jsonb,'queued')`, id, s.WorkspaceID, body.ProjectID, s.Actor.UserID, body.Format, string(filters)); e != nil {
			return e
		}
		return h.d.Jobs.Publish(c.Request.Context(), q, "export.generate", map[string]any{"export_id": id}, "export:"+id.String())
	})
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	httpapi.JSON(c, 202, gin.H{"id": id, "format": body.Format, "status": "queued", "download_url": fmt.Sprintf("/api/v1/workspaces/%s/exports/%s/download", s.WorkspaceID, id)})
}
