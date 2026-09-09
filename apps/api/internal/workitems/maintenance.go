package workitems

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"my-jira/apps/api/internal/platform"
	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/httpapi"
	"my-jira/apps/api/internal/platform/identity"
	"my-jira/apps/api/internal/platform/jobs"
	"my-jira/apps/api/internal/support/data"
)

type AutomationSettings struct {
	ArchiveAfterMonths int        `json:"archive_after_months"`
	CloseAfterMonths   int        `json:"close_after_months"`
	CloseStateID       *uuid.UUID `json:"close_state_id"`
}

type MaintenanceResult struct {
	Archived int `json:"archived"`
	Closed   int `json:"closed"`
}

func (h *handler) automation(c *gin.Context) {
	scope, err := h.scope(c, identity.Guest)
	if err != nil {
		respondError(c, err)
		return
	}
	var raw []byte
	err = h.deps.DB.SQL.QueryRowContext(c.Request.Context(), "SELECT COALESCE(settings->'automation','{}') FROM projects WHERE id=$1", scope.ProjectID).Scan(&raw)
	var settings AutomationSettings
	if err == nil {
		err = json.Unmarshal(raw, &settings)
	}
	if err != nil {
		respondError(c, err)
		return
	}
	httpapi.JSON(c, 200, settings)
}

func validateAutomation(ctx context.Context, q database.DBTX, scope identity.Scope, settings AutomationSettings) (uuid.UUID, string, error) {
	if settings.ArchiveAfterMonths < 0 || settings.ArchiveAfterMonths > 12 || settings.CloseAfterMonths < 0 || settings.CloseAfterMonths > 12 {
		return uuid.Nil, "", invalid("Automation months must be between 0 and 12; zero disables that action")
	}
	if settings.CloseStateID == nil && settings.CloseAfterMonths == 0 {
		return uuid.Nil, "", nil
	}
	var state uuid.UUID
	var group string
	var err error
	if settings.CloseStateID != nil {
		err = q.QueryRowContext(ctx, "SELECT id,group_name FROM states WHERE id=$1 AND workspace_id=$2 AND project_id=$3 AND deleted_at IS NULL", settings.CloseStateID, scope.WorkspaceID, scope.ProjectID).Scan(&state, &group)
	} else {
		err = q.QueryRowContext(ctx, "SELECT id,group_name FROM states WHERE workspace_id=$1 AND project_id=$2 AND group_name='cancelled' AND deleted_at IS NULL ORDER BY position,id LIMIT 1", scope.WorkspaceID, scope.ProjectID).Scan(&state, &group)
	}
	if err == sql.ErrNoRows {
		return uuid.Nil, "", invalid("Choose a completed or cancelled state in this project")
	}
	if err != nil {
		return uuid.Nil, "", err
	}
	if group != "completed" && group != "cancelled" {
		return uuid.Nil, "", invalid("Automatic closing requires a completed or cancelled state")
	}
	return state, group, nil
}

func (h *handler) configureAutomation(c *gin.Context) {
	scope, err := h.scope(c, identity.Admin)
	if err != nil {
		respondError(c, err)
		return
	}
	input, err := data.Bind(c, "archive_after_months", "close_after_months", "close_state_id")
	if err != nil {
		respondError(c, err)
		return
	}
	var result AutomationSettings
	err = h.deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if _, err := q.ExecContext(c.Request.Context(), "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", "automation:"+scope.ProjectID.String()); err != nil {
			return err
		}
		var raw []byte
		if err := q.QueryRowContext(c.Request.Context(), "SELECT COALESCE(settings->'automation','{}') FROM projects WHERE id=$1 AND workspace_id=$2 AND deleted_at IS NULL FOR UPDATE", scope.ProjectID, scope.WorkspaceID).Scan(&raw); err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &result); err != nil {
			return err
		}
		for field, value := range input {
			switch field {
			case "archive_after_months":
				if string(value) == "null" || json.Unmarshal(value, &result.ArchiveAfterMonths) != nil {
					return invalid("Invalid archive_after_months")
				}
			case "close_after_months":
				if string(value) == "null" || json.Unmarshal(value, &result.CloseAfterMonths) != nil {
					return invalid("Invalid close_after_months")
				}
			case "close_state_id":
				if json.Unmarshal(value, &result.CloseStateID) != nil {
					return invalid("Invalid close_state_id")
				}
			}
		}
		if _, _, err := validateAutomation(c.Request.Context(), q, scope, result); err != nil {
			return err
		}
		raw, err := json.Marshal(result)
		if err != nil {
			return err
		}
		_, err = q.ExecContext(c.Request.Context(), "UPDATE projects SET settings=jsonb_set(settings,'{automation}',$2::jsonb),updated_at=now() WHERE id=$1", scope.ProjectID, string(raw))
		return err
	})
	if err != nil {
		respondError(c, err)
		return
	}
	httpapi.JSON(c, 200, result)
}

func (h *handler) runAutomation(c *gin.Context) {
	scope, err := h.scope(c, identity.Admin)
	if err != nil {
		respondError(c, err)
		return
	}
	result, err := runProjectAutomation(c.Request.Context(), h.deps, scope, time.Now().UTC())
	if err != nil {
		respondError(c, err)
		return
	}
	httpapi.JSON(c, 200, result)
}

const automationEligible = ` AND w.deleted_at IS NULL AND w.archived_at IS NULL AND NOT w.is_draft
 AND NOT EXISTS(SELECT 1 FROM cycle_items ci JOIN cycles cy ON cy.id=ci.cycle_id AND cy.deleted_at IS NULL WHERE ci.work_item_id=w.id AND ci.deleted_at IS NULL AND (cy.end_date IS NULL OR cy.end_date>=$4::date))
 AND NOT EXISTS(SELECT 1 FROM module_items mi JOIN modules m ON m.id=mi.module_id AND m.deleted_at IS NULL WHERE mi.work_item_id=w.id AND mi.deleted_at IS NULL AND (m.target_date IS NULL OR m.target_date>=$4::date))
 AND NOT EXISTS(SELECT 1 FROM intake_items it WHERE it.work_item_id=w.id AND it.deleted_at IS NULL AND it.status IN('pending','snoozed'))`

func runProjectAutomation(ctx context.Context, deps platform.Dependencies, scope identity.Scope, now time.Time) (MaintenanceResult, error) {
	result := MaintenanceResult{}
	for _, action := range []string{"archived", "closed"} {
		for {
			changed := 0
			err := deps.DB.WithinTx(ctx, func(q database.DBTX) error {
				if _, err := q.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", "automation:"+scope.ProjectID.String()); err != nil {
					return err
				}
				var raw []byte
				if err := q.QueryRowContext(ctx, "SELECT COALESCE(settings->'automation','{}') FROM projects WHERE id=$1 AND workspace_id=$2 AND deleted_at IS NULL AND archived_at IS NULL", scope.ProjectID, scope.WorkspaceID).Scan(&raw); err != nil {
					if err == sql.ErrNoRows {
						return nil
					}
					return err
				}
				var settings AutomationSettings
				if err := json.Unmarshal(raw, &settings); err != nil {
					return err
				}
				state, group, err := validateAutomation(ctx, q, scope, settings)
				if err != nil {
					return err
				}
				months := settings.ArchiveAfterMonths
				stateFilter := "s.group_name IN('completed','cancelled')"
				if action == "closed" {
					months = settings.CloseAfterMonths
					stateFilter = "s.group_name IN('backlog','unstarted','started')"
				}
				if months == 0 {
					return nil
				}
				rows, err := q.QueryContext(ctx, "SELECT w.id,to_jsonb(w)-'description_binary' FROM work_items w JOIN states s ON s.id=w.state_id WHERE w.workspace_id=$1 AND w.project_id=$2 AND w.updated_at<=$3 AND "+stateFilter+automationEligible+" ORDER BY w.id LIMIT 500 FOR UPDATE OF w SKIP LOCKED", scope.WorkspaceID, scope.ProjectID, now.AddDate(0, 0, -months*30), now)
				if err != nil {
					return err
				}
				type item struct {
					id     uuid.UUID
					before json.RawMessage
				}
				items := []item{}
				for rows.Next() {
					var x item
					if err = rows.Scan(&x.id, &x.before); err != nil {
						rows.Close()
						return err
					}
					items = append(items, x)
				}
				err = rows.Err()
				rows.Close()
				if err != nil {
					return err
				}
				for _, item := range items {
					var after []byte
					if action == "archived" {
						err = q.QueryRowContext(ctx, "UPDATE work_items SET archived_at=$2,updated_at=$2,updated_by=$3,version=version+1 WHERE id=$1 RETURNING to_jsonb(work_items)-'description_binary'", item.id, now, scope.Actor.UserID).Scan(&after)
					} else {
						var completed any
						if group == "completed" {
							completed = now
						}
						err = q.QueryRowContext(ctx, "UPDATE work_items SET state_id=$2,completed_at=$3,updated_at=$4,updated_by=$5,version=version+1 WHERE id=$1 RETURNING to_jsonb(work_items)-'description_binary'", item.id, state, completed, now, scope.Actor.UserID).Scan(&after)
					}
					if err != nil {
						return err
					}
					if err = data.RecordIssue(ctx, deps, q, scope, item.id, "automation."+action, item.before, json.RawMessage(after), true); err != nil {
						return err
					}
				}
				changed = len(items)
				return nil
			})
			if err != nil {
				return result, err
			}
			if action == "archived" {
				result.Archived += changed
			} else {
				result.Closed += changed
			}
			if changed < 500 {
				break
			}
		}
	}
	return result, nil
}

// RunMaintenance operates only on active projects. Passing an explicit project
// ID is useful for isolated operational checks and deterministic regression tests.
func RunMaintenance(ctx context.Context, deps platform.Dependencies, now time.Time, projectID uuid.UUID) (MaintenanceResult, error) {
	if _, err := deps.DB.SQL.ExecContext(ctx, `UPDATE intake_items SET status='pending',snoozed_until=NULL,updated_at=$1 WHERE status='snoozed' AND snoozed_until<=$1 AND deleted_at IS NULL AND ($2::uuid IS NULL OR project_id=$2)`, now, nullableID(projectID)); err != nil {
		return MaintenanceResult{}, err
	}
	rows, err := deps.DB.SQL.QueryContext(ctx, `SELECT p.id,p.workspace_id,w.owner_id FROM projects p JOIN workspaces w ON w.id=p.workspace_id AND w.deleted_at IS NULL WHERE p.deleted_at IS NULL AND p.archived_at IS NULL AND ($1::uuid IS NULL OR p.id=$1) AND p.settings ? 'automation' ORDER BY p.id`, nullableID(projectID))
	if err != nil {
		return MaintenanceResult{}, err
	}
	scopes := []identity.Scope{}
	for rows.Next() {
		var scope identity.Scope
		scope.Role = identity.Admin
		if err = rows.Scan(&scope.ProjectID, &scope.WorkspaceID, &scope.Actor.UserID); err != nil {
			rows.Close()
			return MaintenanceResult{}, err
		}
		scopes = append(scopes, scope)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return MaintenanceResult{}, err
	}
	total := MaintenanceResult{}
	for _, scope := range scopes {
		result, err := runProjectAutomation(ctx, deps, scope, now)
		total.Archived += result.Archived
		total.Closed += result.Closed
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

func nullableID(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}

// CleanupHistory retains the newest twenty snapshots for each document/item.
// Old webhook attempts are eligible only after delivery or retry exhaustion.
func CleanupHistory(ctx context.Context, deps platform.Dependencies, now time.Time) (map[string]int64, error) {
	result := map[string]int64{}
	queries := map[string]string{
		"page_versions":      `SELECT id FROM (SELECT id,row_number() OVER(PARTITION BY page_id ORDER BY version DESC,created_at DESC,id DESC) n FROM page_versions) ranked WHERE n>20 LIMIT 500`,
		"work_item_versions": `SELECT id FROM (SELECT id,row_number() OVER(PARTITION BY work_item_id ORDER BY version DESC,created_at DESC,id DESC) n FROM work_item_versions) ranked WHERE n>20 LIMIT 500`,
		"webhook_deliveries": `SELECT id FROM webhook_deliveries WHERE created_at<$1 AND (delivered_at IS NOT NULL OR attempts>=11) ORDER BY created_at,id LIMIT 500`,
	}
	for table, selection := range queries {
		for {
			args := []any{}
			if table == "webhook_deliveries" {
				args = append(args, now.AddDate(0, 0, -14))
			}
			res, err := deps.DB.SQL.ExecContext(ctx, "DELETE FROM "+table+" WHERE id IN("+selection+")", args...)
			if err != nil {
				return result, err
			}
			count, err := res.RowsAffected()
			if err != nil {
				return result, err
			}
			result[table] += count
			if count < 500 {
				break
			}
		}
	}
	return result, nil
}

func RegisterJobs(mux *asynq.ServeMux, deps platform.Dependencies) {
	mux.HandleFunc("maintenance.run", func(ctx context.Context, task *asynq.Task) error {
		var envelope jobs.Envelope
		if err := json.Unmarshal(task.Payload(), &envelope); err != nil {
			return fmt.Errorf("invalid maintenance envelope: %w", asynq.SkipRetry)
		}
		now := time.Now().UTC()
		if _, err := RunMaintenance(ctx, deps, now, uuid.Nil); err != nil {
			return err
		}
		_, err := CleanupHistory(ctx, deps, now)
		return err
	})
}

func RunScheduler(ctx context.Context, deps platform.Dependencies) {
	if deps.Jobs == nil {
		return
	}
	enqueue := func() {
		now := time.Now().UTC()
		key := "maintenance:" + now.Format("2006-01-02T15")
		if err := deps.Jobs.Publish(ctx, deps.DB.SQL, "maintenance.run", map[string]any{"scheduled_at": now}, key); err != nil {
			slog.Error("maintenance scheduling failed", "error", err)
		}
	}
	enqueue()
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			enqueue()
		}
	}
}
