package workitems

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/httpapi"
	"my-jira/apps/api/internal/platform/identity"
)

func (h *handler) intakeScope(c *gin.Context, q database.DBTX, lock bool) (identity.Scope, uuid.UUID, map[string]any, error) {
	s, e := h.scope(c, identity.Guest)
	if e != nil {
		return s, uuid.Nil, nil, e
	}
	id, e := httpapi.UUIDParam(c, "intakeID")
	if e != nil {
		return s, id, nil, e
	}
	var raw []byte
	query := `SELECT to_jsonb(i)-'deleted_at' FROM intake_items i WHERE id=$1 AND project_id=$2 AND workspace_id=$3 AND deleted_at IS NULL`
	if lock {
		query += " FOR UPDATE"
	}
	e = q.QueryRowContext(c.Request.Context(), query, id, s.ProjectID, s.WorkspaceID).Scan(&raw)
	if e != nil {
		return s, id, nil, e
	}
	var result map[string]any
	if e = json.Unmarshal(raw, &result); e != nil {
		return s, id, nil, e
	}
	itemID, e := uuid.Parse(result["work_item_id"].(string))
	if e != nil {
		return s, id, nil, e
	}
	item, e := loadIssue(c, q, s, itemID, lock)
	if e != nil {
		return s, id, nil, e
	}
	result["work_item"] = item
	return s, id, result, nil
}

func (h *handler) listIntake(c *gin.Context) {
	s, e := h.scope(c, identity.Guest)
	if e != nil {
		respondError(c, e)
		return
	}
	args := []any{s.WorkspaceID, s.ProjectID}
	where := `i.workspace_id=$1 AND i.project_id=$2 AND i.deleted_at IS NULL AND w.deleted_at IS NULL`
	if s.Role < identity.Member && !s.GuestCanViewAll {
		args = append(args, s.Actor.UserID)
		where += fmt.Sprintf(" AND w.created_by=$%d", len(args))
	}
	if status := c.Query("status"); status != "" {
		switch status {
		case "pending", "accepted", "rejected", "duplicate", "snoozed":
		default:
			respondError(c, invalid("Unknown intake status"))
			return
		}
		args = append(args, status)
		where += fmt.Sprintf(" AND i.status=$%d", len(args))
	}
	if search := strings.TrimSpace(c.Query("search")); search != "" {
		args = append(args, "%"+search+"%")
		where += fmt.Sprintf(" AND w.name ILIKE $%d", len(args))
	}
	data, e := execJSON(c, h.deps.DB.SQL, `SELECT (to_jsonb(i)-'deleted_at')||jsonb_build_object('work_item',to_jsonb(w)-'description_binary'-'deleted_at') FROM intake_items i JOIN work_items w ON w.id=i.work_item_id WHERE `+where+` ORDER BY i.created_at DESC,i.id`, args...)
	if e != nil {
		respondError(c, e)
		return
	}
	httpapi.JSON(c, 200, data)
}

func (h *handler) createIntake(c *gin.Context) {
	s, e := h.scope(c, identity.Guest)
	if e != nil {
		respondError(c, e)
		return
	}
	input, e := bindMap(c)
	if e != nil {
		respondError(c, e)
		return
	}
	input["is_draft"] = json.RawMessage("true")
	var result json.RawMessage
	e = h.deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		item, e := h.createItem(c, q, s, input)
		if e != nil {
			return e
		}
		id := uuid.New()
		var raw []byte
		e = q.QueryRowContext(c.Request.Context(), `INSERT INTO intake_items(id,workspace_id,project_id,work_item_id,status,source,submitted_by)VALUES($1,$2,$3,$4,'pending','app',$5) RETURNING to_jsonb(intake_items)-'deleted_at'`, id, s.WorkspaceID, s.ProjectID, item["id"], s.Actor.UserID).Scan(&raw)
		if e != nil {
			return e
		}
		var data map[string]any
		_ = json.Unmarshal(raw, &data)
		data["work_item"] = item
		result, e = json.Marshal(data)
		return e
	})
	if e != nil {
		respondError(c, e)
		return
	}
	httpapi.JSON(c, 201, result)
}

func (h *handler) detailIntake(c *gin.Context) {
	_, _, item, e := h.intakeScope(c, h.deps.DB.SQL, false)
	if e != nil {
		respondError(c, e)
		return
	}
	httpapi.JSON(c, 200, item)
}

func (h *handler) editIntake(c *gin.Context) {
	input, e := bindMap(c)
	if e != nil {
		respondError(c, e)
		return
	}
	var result map[string]any
	e = h.deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		s, _, intake, e := h.intakeScope(c, q, true)
		if e != nil {
			return e
		}
		item := intake["work_item"].(map[string]any)
		if s.Role < identity.Admin && item["created_by"] != s.Actor.UserID.String() {
			return httpapi.NewError(403, "forbidden", "Only the author or an administrator can edit an intake item")
		}
		if s.Role < identity.Member {
			for field := range input {
				if field != "name" && field != "description_html" && field != "description_json" && field != "version" {
					return httpapi.NewError(403, "forbidden", "Guests may edit only the title and description")
				}
			}
		}
		if _, exists := input["is_draft"]; exists {
			return invalid("Use intake review to accept a submission")
		}
		id := uuid.MustParse(item["id"].(string))
		updated, e := h.updateItem(c, q, s, id, input, true)
		if e != nil {
			return e
		}
		intake["work_item"] = updated
		result = intake
		return nil
	})
	if e != nil {
		respondError(c, e)
		return
	}
	httpapi.JSON(c, 200, result)
}

func (h *handler) deleteIntake(c *gin.Context) {
	e := h.deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		s, id, intake, e := h.intakeScope(c, q, true)
		if e != nil {
			return e
		}
		item := intake["work_item"].(map[string]any)
		if s.Role < identity.Admin && item["created_by"] != s.Actor.UserID.String() {
			return httpapi.NewError(403, "forbidden", "Only the author or an administrator can delete an intake item")
		}
		if e = h.deleteItem(c, q, s, uuid.MustParse(item["id"].(string))); e != nil {
			return e
		}
		_, e = q.ExecContext(c.Request.Context(), `UPDATE intake_items SET deleted_at=now(),updated_at=now() WHERE id=$1`, id)
		return e
	})
	if e != nil {
		respondError(c, e)
		return
	}
	c.Status(204)
}

func (h *handler) resolveIntake(c *gin.Context) {
	body, e := httpapi.Bind[struct {
		Status       string     `json:"status"`
		DuplicateOf  *uuid.UUID `json:"duplicate_of"`
		SnoozedUntil *time.Time `json:"snoozed_until"`
		StateID      *uuid.UUID `json:"state_id"`
		Version      *int64     `json:"version"`
	}](c)
	if e != nil {
		respondError(c, e)
		return
	}
	switch body.Status {
	case "pending", "accepted", "rejected", "duplicate", "snoozed":
	default:
		respondError(c, invalid("Unknown intake status"))
		return
	}
	if body.Status == "snoozed" && (body.SnoozedUntil == nil || !body.SnoozedUntil.After(time.Now())) {
		respondError(c, invalid("Choose a future snooze time"))
		return
	}
	if body.Status != "snoozed" {
		body.SnoozedUntil = nil
	}
	if body.Status != "duplicate" {
		body.DuplicateOf = nil
	}
	var result map[string]any
	e = h.deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		s, id, intake, e := h.intakeScope(c, q, true)
		if e != nil {
			return e
		}
		if s.Role < identity.Admin {
			return httpapi.NewError(403, "forbidden", "An administrator must review intake submissions")
		}
		item := intake["work_item"].(map[string]any)
		itemID := uuid.MustParse(item["id"].(string))
		previousStatus := intake["status"]
		if body.Version != nil && *body.Version != recordVersion(item) {
			return conflict("The submission changed. Reload it before reviewing")
		}
		if body.Status == "duplicate" {
			if body.DuplicateOf == nil || *body.DuplicateOf == itemID {
				return invalid("Choose a different original work item")
			}
			if _, e = loadIssue(c, q, s, *body.DuplicateOf, false); e != nil {
				return invalid("Original work item is outside this project")
			}
		}
		if _, e = q.ExecContext(c.Request.Context(), `UPDATE intake_items SET status=$2,duplicate_of=$3,snoozed_until=$4,updated_at=now() WHERE id=$1`, id, body.Status, body.DuplicateOf, body.SnoozedUntil); e != nil {
			return e
		}
		changes := map[string]json.RawMessage{"is_draft": json.RawMessage("true")}
		if body.Status == "accepted" {
			changes["is_draft"] = json.RawMessage("false")
		}
		if body.StateID != nil {
			raw, _ := json.Marshal(body.StateID)
			changes["state_id"] = raw
		}
		updated, e := h.updateItem(c, q, s, itemID, changes, false)
		if e != nil {
			return e
		}
		intake["status"] = body.Status
		intake["duplicate_of"] = body.DuplicateOf
		intake["snoozed_until"] = body.SnoozedUntil
		intake["work_item"] = updated
		result = intake
		return h.record(c, q, s, itemID, "intake_reviewed", map[string]any{"status": previousStatus}, map[string]any{"status": body.Status})
	})
	if e != nil {
		respondError(c, e)
		return
	}
	httpapi.JSON(c, 200, result)
}

func (h *handler) intakeVersions(c *gin.Context) {
	s, _, intake, e := h.intakeScope(c, h.deps.DB.SQL, false)
	if e != nil {
		respondError(c, e)
		return
	}
	item := intake["work_item"].(map[string]any)
	rows, e := execJSON(c, h.deps.DB.SQL, `SELECT to_jsonb(v)-'deleted_at' FROM work_item_versions v WHERE work_item_id=$1 AND workspace_id=$2 AND project_id=$3 AND deleted_at IS NULL ORDER BY version DESC`, item["id"], s.WorkspaceID, s.ProjectID)
	if e != nil {
		respondError(c, e)
		return
	}
	httpapi.JSON(c, 200, rows)
}
