package requirements

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"my-jira/apps/api/internal/platform/httpapi"
	"my-jira/apps/api/internal/platform/identity"
)

type Event struct {
	Revision int64      `json:"revision"`
	Kind     string     `json:"kind"`
	EntityID *uuid.UUID `json:"entity_id,omitempty"`
}

func (h *handler) currentStreamScope(ctx context.Context, s identity.Scope) (identity.Scope, error) {
	current, err := h.deps.Policy.Project(ctx, s.Actor, s.WorkspaceID, s.ProjectID, identity.Guest)
	if err != nil {
		return current, err
	}
	if s.Actor.SessionID != uuid.Nil {
		var valid bool
		if err = h.deps.DB.SQL.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sessions WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL AND expires_at>now() AND deleted_at IS NULL)`, s.Actor.SessionID, s.Actor.UserID).Scan(&valid); err != nil {
			return current, err
		}
		if !valid {
			return current, httpapi.NewError(401, "session_expired", "The session is no longer active")
		}
	}
	return current, nil
}

// Replay contains no stored entity payloads. IDs are included only for current
// visible work items; other domains require their own source-aware fetch.
func (s *Service) Replay(ctx context.Context, scope identity.Scope, cursor int64, limit int) ([]Event, int64, bool, error) {
	if limit < 1 || limit > 500 {
		limit = 200
	}
	var current, minimum int64
	err := s.deps.DB.SQL.QueryRowContext(ctx, `SELECT COALESCE((SELECT revision FROM project_revisions WHERE project_id=$1),0),COALESCE((SELECT min(revision) FROM project_events WHERE project_id=$1),0)`, scope.ProjectID).Scan(&current, &minimum)
	if err != nil {
		return nil, 0, false, err
	}
	if cursor < 0 || cursor > current || (minimum > 0 && cursor < minimum-1) || (minimum == 0 && cursor < current) {
		return []Event{}, current, true, nil
	}
	rows, err := s.deps.DB.SQL.QueryContext(ctx, `SELECT e.revision,CASE WHEN e.kind='authorization.changed' OR e.kind LIKE '%.visibility_changed' THEN 'authorization.changed' WHEN w.id IS NOT NULL THEN e.kind ELSE 'project.changed' END,CASE WHEN w.id IS NOT NULL THEN w.id ELSE NULL END FROM project_events e LEFT JOIN work_items w ON w.id=e.entity_id AND w.project_id=e.project_id AND w.workspace_id=e.workspace_id AND w.deleted_at IS NULL AND ($4 OR w.created_by=$3) WHERE e.workspace_id=$1 AND e.project_id=$2 AND e.revision>$5 ORDER BY e.revision LIMIT $6`, scope.WorkspaceID, scope.ProjectID, scope.Actor.UserID, scope.Role >= identity.Member || scope.GuestCanViewAll, cursor, limit)
	if err != nil {
		return nil, current, false, err
	}
	defer rows.Close()
	events := []Event{}
	for rows.Next() {
		var event Event
		if err = rows.Scan(&event.Revision, &event.Kind, &event.EntityID); err != nil {
			return nil, current, false, err
		}
		events = append(events, event)
	}
	return events, current, false, rows.Err()
}

func (h *handler) events(c *gin.Context) {
	s, err := h.projectScope(c, identity.Guest, false)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	// External API tokens are short HTTP credentials; browser streams recheck
	// their durable session rather than silently extending a revoked token.
	if s.Actor.TokenWorkspaceID != uuid.Nil {
		httpapi.Fail(c, httpapi.NewError(403, "browser_session_required", "Realtime subscriptions require a browser session"))
		return
	}
	s, err = h.currentStreamScope(c.Request.Context(), s)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	text := c.GetHeader("Last-Event-ID")
	if text == "" {
		text = c.DefaultQuery("cursor", "0")
	}
	cursor, err := strconv.ParseInt(text, 10, 64)
	if err != nil || cursor < 0 {
		httpapi.Fail(c, httpapi.NewError(400, "validation_failed", "The event cursor must be a nonnegative integer"))
		return
	}
	batch, current, expired, err := h.service.Replay(c.Request.Context(), s, cursor, 200)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache, no-store")
	c.Header("X-Accel-Buffering", "no")
	c.Header("Connection", "keep-alive")
	send := func(name string, id int64, value any) bool {
		raw, _ := json.Marshal(value)
		if id > 0 {
			if _, err := fmt.Fprintf(c.Writer, "id: %d\n", id); err != nil {
				return false
			}
		}
		if _, err := fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", name, raw); err != nil {
			return false
		}
		c.Writer.Flush()
		return true
	}
	if expired {
		send("cursor_expired", 0, gin.H{"revision": current, "resnapshot": true})
		return
	}
	if _, err = fmt.Fprint(c.Writer, ": connected\n\n"); err != nil {
		return
	}
	c.Writer.Flush()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	heartbeats := 0
	for {
		fresh, err := h.currentStreamScope(c.Request.Context(), s)
		if err != nil || fresh.Role != s.Role || fresh.GuestCanViewAll != s.GuestCanViewAll {
			send("authorization_changed", 0, gin.H{"clear": true})
			return
		}
		for _, event := range batch {
			if event.Kind == "authorization.changed" {
				send("authorization_changed", event.Revision, gin.H{"clear": true, "revision": event.Revision, "reconnect": true})
				return
			}
			if !send("change", event.Revision, event) {
				return
			}
			cursor = event.Revision
		}
		if len(batch) == 200 {
			batch, _, expired, err = h.service.Replay(c.Request.Context(), s, cursor, 200)
		} else {
			select {
			case <-c.Request.Context().Done():
				return
			case <-ticker.C:
			}
			heartbeats++
			if heartbeats%15 == 0 {
				if _, err = fmt.Fprint(c.Writer, ": heartbeat\n\n"); err != nil {
					return
				}
				c.Writer.Flush()
			}
			batch, current, expired, err = h.service.Replay(c.Request.Context(), s, cursor, 200)
		}
		if err != nil {
			send("retry", 0, gin.H{"reconnect": true})
			return
		}
		if expired {
			send("cursor_expired", 0, gin.H{"revision": current, "resnapshot": true})
			return
		}
	}
}
