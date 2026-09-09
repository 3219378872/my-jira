package integrations

import (
	"context"
	"encoding/json"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/httpapi"
	"my-jira/apps/api/internal/platform/identity"
)

func validWebhookEvent(event string) bool {
	switch event {
	case "*", "work_item.changed", "project.changed", "comment.changed", "cycle.changed", "cycle.items_changed", "module.changed", "module.items_changed":
		return true
	}
	return false
}

func (h *handler) rotateWebhookSecret(c *gin.Context) {
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
	value, _, e := secret("whsec_")
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	var updated uuid.UUID
	e = h.d.DB.SQL.QueryRowContext(c.Request.Context(), `UPDATE webhooks SET secret=$3,updated_at=now() WHERE id=$1 AND workspace_id=$2 AND deleted_at IS NULL RETURNING id`, id, s.WorkspaceID, value).Scan(&updated)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	httpapi.JSON(c, 200, gin.H{"id": updated, "secret": value})
}

type entityEvent struct {
	EventID     uuid.UUID       `json:"event_id"`
	WorkspaceID uuid.UUID       `json:"workspace_id"`
	ProjectID   uuid.UUID       `json:"project_id"`
	EntityID    uuid.UUID       `json:"entity_id"`
	Event       string          `json:"event"`
	Action      string          `json:"action"`
	Data        json.RawMessage `json:"data"`
}

func (h *handler) canDeliver(ctx context.Context, owner uuid.UUID, event entityEvent) bool {
	if _, e := h.d.Policy.Workspace(ctx, identity.Actor{UserID: owner}, event.WorkspaceID, identity.Admin); e != nil {
		return false
	}
	if event.ProjectID == uuid.Nil {
		_, e := h.d.Policy.Workspace(ctx, identity.Actor{UserID: owner}, event.WorkspaceID, identity.Admin)
		return e == nil
	}
	if _, e := h.d.Policy.Project(ctx, identity.Actor{UserID: owner}, event.WorkspaceID, event.ProjectID, identity.Member); e == nil {
		return true
	}
	if event.Event != "project.changed" || event.Action != "deleted" {
		return false
	}
	// Soft deletion removes a project from normal reads without revoking its
	// active memberships. Previously revoked memberships must not qualify.
	if _, e := h.d.Policy.Workspace(ctx, identity.Actor{UserID: owner}, event.WorkspaceID, identity.Admin); e != nil {
		return false
	}
	var member bool
	e := h.d.DB.SQL.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM projects p WHERE p.id=$1 AND p.workspace_id=$2 AND p.deleted_at IS NOT NULL AND (p.network='public' OR EXISTS(SELECT 1 FROM project_members m WHERE m.project_id=p.id AND m.workspace_id=p.workspace_id AND m.user_id=$3 AND m.is_active AND m.deleted_at IS NULL)))`, event.ProjectID, event.WorkspaceID, owner).Scan(&member)
	return e == nil && member
}

func (h *handler) entityChangedJob(ctx context.Context, t *asynq.Task) error {
	var event entityEvent
	_, e := decodeTask(t, &event)
	if e != nil {
		return e
	}
	if !validWebhookEvent(event.Event) || event.Event == "*" {
		return asynq.SkipRetry
	}
	return h.queueEntityHooks(ctx, event)
}

func (h *handler) queueEntityHooks(ctx context.Context, event entityEvent) error {
	rows, e := h.d.DB.SQL.QueryContext(ctx, `SELECT id,created_by FROM webhooks WHERE workspace_id=$1 AND is_active AND deleted_at IS NULL AND (events ? '*' OR events ? $2)`, event.WorkspaceID, event.Event)
	if e != nil {
		return e
	}
	type hook struct{ id, owner uuid.UUID }
	hooks := []hook{}
	for rows.Next() {
		var item hook
		if e = rows.Scan(&item.id, &item.owner); e != nil {
			rows.Close()
			return e
		}
		hooks = append(hooks, item)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	return h.d.DB.WithinTx(ctx, func(q database.DBTX) error {
		for _, hook := range hooks {
			if !h.canDeliver(ctx, hook.owner, event) {
				continue
			}
			payload := map[string]any{"webhook_id": hook.id, "event_id": event.EventID, "event": event.Event, "action": event.Action, "project_id": event.ProjectID, "data": event.Data}
			if e := h.d.Jobs.Publish(ctx, q, "webhook.deliver", payload, "webhook:"+hook.id.String()+":"+event.EventID.String()+":"+event.Event); e != nil {
				return e
			}
		}
		return nil
	})
}
