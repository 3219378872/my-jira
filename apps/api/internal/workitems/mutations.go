package workitems

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/httpapi"
	"my-jira/apps/api/internal/platform/identity"
	"my-jira/apps/api/internal/support/data"
)

func fields(input map[string]json.RawMessage) (map[string]any, error) {
	out := map[string]any{}
	for k, raw := range input {
		if value, handled, err := requirementField(k, raw); handled {
			if err != nil {
				return nil, err
			}
			out[k] = value
			continue
		}
		switch k {
		case "version", "assignee_ids", "label_ids", "cycle_id", "module_ids", "dependency_ids":
			continue
		case "name", "description_html", "type_name":
			v, e := stringField(raw)
			if e != nil {
				return nil, e
			}
			if k == "name" {
				v = strings.TrimSpace(v)
				if len(v) < 1 || len([]rune(v)) > 255 {
					return nil, invalid("Name must contain between 1 and 255 characters")
				}
			}
			if k == "description_html" {
				if len(v) > 1024*1024 {
					return nil, invalid("Description is too long")
				}
				v = cleanHTML(v)
			}
			out[k] = v
		case "description_json":
			if len(raw) > 1024*1024 {
				return nil, invalid("Document is too large")
			}
			var v map[string]any
			if e := json.Unmarshal(raw, &v); e != nil || v == nil {
				return nil, invalid("Invalid document JSON")
			}
			out[k] = string(raw)
		case "priority":
			v, e := stringField(raw)
			if e != nil || !validPriority(v) {
				return nil, invalid("Invalid priority")
			}
			out[k] = v
		case "state_id", "parent_id", "estimate_point_id", "activity_id":
			v, e := uuidValue(raw, k != "state_id")
			if e != nil {
				return nil, e
			}
			out[k] = v
		case "start_date", "target_date":
			if string(raw) == "null" || string(raw) == `""` {
				out[k] = nil
				continue
			}
			v, e := stringField(raw)
			if e != nil {
				return nil, e
			}
			if _, e = time.Parse("2006-01-02", v); e != nil {
				return nil, invalid("Dates must use YYYY-MM-DD")
			}
			out[k] = v
		case "archived_at":
			if string(raw) == "null" {
				out[k] = nil
				continue
			}
			v, e := stringField(raw)
			if e != nil {
				return nil, e
			}
			t, e := time.Parse(time.RFC3339, v)
			if e != nil {
				return nil, invalid("Invalid archive timestamp")
			}
			out[k] = t
		case "is_draft", "planning_locked":
			var v bool
			if e := json.Unmarshal(raw, &v); e != nil || string(raw) == "null" {
				return nil, invalid("Invalid draft flag")
			}
			out[k] = v
		case "estimate", "position", "map_position":
			if string(raw) == "null" && k == "estimate" {
				out[k] = nil
				continue
			}
			var v float64
			if e := json.Unmarshal(raw, &v); e != nil || string(raw) == "null" || math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v) > 1e15 || (k == "estimate" && v < 0) {
				return nil, invalid("Invalid " + k)
			}
			out[k] = v
		default:
			return nil, invalid("Unknown field: " + k)
		}
	}
	return out, nil
}

func lockGraphs(c *gin.Context, q database.DBTX, ids ...uuid.UUID) error {
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
	for i, id := range ids {
		if i > 0 && ids[i-1] == id {
			continue
		}
		if _, err := q.ExecContext(c.Request.Context(), `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, id.String()); err != nil {
			return err
		}
	}
	return nil
}

func validateDateRange(values map[string]any, existing map[string]any) error {
	date := func(key string) string {
		value, ok := values[key]
		if !ok {
			value = existing[key]
		}
		if value == nil {
			return ""
		}
		text, _ := value.(string)
		if len(text) >= 10 {
			return text[:10]
		}
		return text
	}
	start, target := date("start_date"), date("target_date")
	if start != "" && target != "" && start > target {
		return invalid("The target date cannot precede the start date")
	}
	return nil
}

func validateFields(c *gin.Context, q database.DBTX, s identity.Scope, id uuid.UUID, values map[string]any) error {
	ctx := c.Request.Context()
	point, pointSet := values["estimate_point_id"]
	numeric, numericSet := values["estimate"]
	if pointSet && numericSet {
		return invalid("Use either estimate_point_id or estimate")
	}
	if pointSet {
		if point == nil {
			values["estimate"] = nil
		} else {
			var number *float64
			if e := q.QueryRowContext(ctx, `SELECT ep.numeric_value FROM estimate_points ep JOIN estimates es ON es.id=ep.estimate_id AND es.deleted_at IS NULL WHERE ep.id=$1 AND ep.workspace_id=$2 AND ep.project_id=$3 AND ep.deleted_at IS NULL`, point, s.WorkspaceID, s.ProjectID).Scan(&number); e != nil {
				return invalid("Estimate point must belong to a live scheme in this project")
			}
			values["estimate"] = number
		}
	} else if numericSet {
		var enabled bool
		if e := q.QueryRowContext(ctx, "SELECT estimate_id IS NOT NULL FROM projects WHERE id=$1", s.ProjectID).Scan(&enabled); e != nil {
			return e
		}
		if enabled && numeric != nil {
			return invalid("Choose estimate_point_id from the active estimate scheme")
		}
		values["estimate_point_id"] = nil
	}
	if v, ok := values["state_id"]; ok {
		var group string
		if e := q.QueryRowContext(ctx, `SELECT group_name FROM states WHERE id=$1 AND workspace_id=$2 AND project_id=$3 AND deleted_at IS NULL`, v, s.WorkspaceID, s.ProjectID).Scan(&group); e != nil {
			return invalid("State does not belong to this project")
		}
		if group == "completed" {
			values["completed_at"] = time.Now().UTC()
		} else {
			values["completed_at"] = nil
		}
	}
	if v, ok := values["parent_id"]; ok && v != nil {
		parent := v.(uuid.UUID)
		if parent == id {
			return invalid("A work item cannot be its own parent")
		}
		if _, e := loadIssue(c, q, s, parent, false); e != nil {
			return invalid("Parent is not accessible in this project")
		}
		var cyclic bool
		e := q.QueryRowContext(ctx, `WITH RECURSIVE ancestors(id,parent_id,path) AS (SELECT id,parent_id,ARRAY[id] FROM work_items WHERE id=$1 UNION ALL SELECT w.id,w.parent_id,a.path||w.id FROM work_items w JOIN ancestors a ON w.id=a.parent_id WHERE NOT w.id=ANY(a.path)) SELECT EXISTS(SELECT 1 FROM ancestors WHERE id=$2)`, parent, id).Scan(&cyclic)
		if e != nil {
			return e
		}
		if cyclic {
			return invalid("This parent would create a cycle")
		}
	}
	return nil
}

func (h *handler) replaceRelations(c *gin.Context, q database.DBTX, s identity.Scope, id uuid.UUID, input map[string]json.RawMessage) error {
	ctx := c.Request.Context()
	for key, table := range map[string]string{"assignee_ids": "work_item_assignees", "label_ids": "work_item_labels", "module_ids": "module_items"} {
		raw, present := input[key]
		if !present {
			continue
		}
		ids, e := uuidArray(raw)
		if e != nil {
			return e
		}
		col := "user_id"
		if key == "label_ids" {
			col = "label_id"
		}
		if key == "module_ids" {
			col = "module_id"
		}
		for _, related := range ids {
			var valid bool
			switch key {
			case "assignee_ids":
				e = q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM workspace_members wm JOIN users u ON u.id=wm.user_id JOIN projects p ON p.workspace_id=wm.workspace_id AND p.id=$3 LEFT JOIN project_members pm ON pm.project_id=p.id AND pm.user_id=wm.user_id AND pm.is_active AND pm.deleted_at IS NULL WHERE wm.workspace_id=$1 AND wm.user_id=$2 AND wm.is_active AND wm.deleted_at IS NULL AND u.is_active AND u.deleted_at IS NULL AND (p.network='public' OR pm.id IS NOT NULL))`, s.WorkspaceID, related, s.ProjectID).Scan(&valid)
			case "label_ids":
				e = q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM labels WHERE id=$1 AND workspace_id=$2 AND (project_id=$3 OR project_id IS NULL) AND deleted_at IS NULL)`, related, s.WorkspaceID, s.ProjectID).Scan(&valid)
			case "module_ids":
				e = q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM modules WHERE id=$1 AND workspace_id=$2 AND project_id=$3 AND deleted_at IS NULL)`, related, s.WorkspaceID, s.ProjectID).Scan(&valid)
			}
			if e != nil {
				return e
			}
			if !valid {
				return invalid("Related resource is not accessible in this project")
			}
		}
		if _, e = q.ExecContext(ctx, "DELETE FROM "+table+" WHERE work_item_id=$1", id); e != nil {
			return e
		}
		for _, related := range ids {
			if _, e = q.ExecContext(ctx, "INSERT INTO "+table+" (id,workspace_id,project_id,work_item_id,"+col+") VALUES ($1,$2,$3,$4,$5)", uuid.New(), s.WorkspaceID, s.ProjectID, id, related); e != nil {
				return e
			}
		}
	}
	if raw, ok := input["cycle_id"]; ok {
		v, e := uuidValue(raw, true)
		if e != nil {
			return e
		}
		if v != nil {
			var valid bool
			e = q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM cycles WHERE id=$1 AND workspace_id=$2 AND project_id=$3 AND deleted_at IS NULL)`, v, s.WorkspaceID, s.ProjectID).Scan(&valid)
			if e != nil {
				return e
			}
			if !valid {
				return invalid("Cycle does not belong to this project")
			}
		}
		if _, e = q.ExecContext(ctx, `DELETE FROM cycle_items WHERE work_item_id=$1`, id); e != nil {
			return e
		}
		if v != nil {
			if _, e = q.ExecContext(ctx, `INSERT INTO cycle_items(id,workspace_id,project_id,work_item_id,cycle_id) VALUES($1,$2,$3,$4,$5)`, uuid.New(), s.WorkspaceID, s.ProjectID, id, v); e != nil {
				return e
			}
		}
	}
	return replaceDependencies(c, q, s, id, input)
}

func (h *handler) record(c *gin.Context, q database.DBTX, s identity.Scope, id uuid.UUID, action string, before, after any) error {
	return data.RecordIssue(c.Request.Context(), h.deps, q, s, id, action, before, after, action == "created" || action == "updated" || action == "moved")
}

func (h *handler) createItem(c *gin.Context, q database.DBTX, s identity.Scope, input map[string]json.RawMessage) (map[string]any, error) {
	if e := lockGraphs(c, q, s.ProjectID); e != nil {
		return nil, e
	}
	if e := data.LockEstimates(c.Request.Context(), q, s.ProjectID); e != nil {
		return nil, e
	}
	var accessErr error
	minimum := identity.Member
	if s.Role < identity.Member {
		minimum = identity.Guest
	} // Intake has its own guest submission contract.
	s, accessErr = CurrentScope(c.Request.Context(), q, s.Actor, s.WorkspaceID, s.ProjectID, minimum)
	if accessErr != nil {
		return nil, accessErr
	}
	if hasRequirementInput(input) {
		if err := RequireRequirements(c.Request.Context(), q, s.WorkspaceID, s.ProjectID); err != nil {
			return nil, err
		}
	}
	values, e := fields(input)
	if e != nil {
		return nil, e
	}
	if _, ok := values["name"]; !ok {
		return nil, invalid("Name is required")
	}
	if e := validateDateRange(values, nil); e != nil {
		return nil, e
	}
	id := uuid.New()
	if _, ok := values["state_id"]; !ok {
		var state uuid.UUID
		e = q.QueryRowContext(c.Request.Context(), `SELECT id FROM states WHERE project_id=$1 AND deleted_at IS NULL ORDER BY is_default DESC,position,id LIMIT 1`, s.ProjectID).Scan(&state)
		if e != nil {
			return nil, invalid("Project needs a workflow state")
		}
		values["state_id"] = state
	}
	if e = validateFields(c, q, s, id, values); e != nil {
		return nil, e
	}
	if e = validateRequirement(c, q, s, id, values, nil, input); e != nil {
		return nil, e
	}
	if _, set := values["remaining_minutes"]; !set {
		if v, ok := values["estimated_minutes"]; ok {
			values["remaining_minutes"] = v
		}
	}
	// A new execution task inherits the Story's commitment only at creation.
	if _, set := input["cycle_id"]; !set && mergedText(values, nil, "requirement_type") == "task" && mergedText(values, nil, "parent_id") != "" {
		var cycle *uuid.UUID
		if e = q.QueryRowContext(c.Request.Context(), `SELECT (SELECT ci.cycle_id FROM cycle_items ci JOIN cycles cy ON cy.id=ci.cycle_id AND cy.deleted_at IS NULL WHERE ci.work_item_id=$1 AND ci.deleted_at IS NULL LIMIT 1)`, values["parent_id"]).Scan(&cycle); e != nil {
			return nil, e
		}
		if cycle != nil {
			input = cloneInput(input)
			input["cycle_id"], _ = json.Marshal(cycle)
		}
	}
	var seq int64
	e = q.QueryRowContext(c.Request.Context(), `UPDATE projects SET next_sequence=next_sequence+1 WHERE id=$1 AND workspace_id=$2 AND deleted_at IS NULL RETURNING next_sequence-1`, s.ProjectID, s.WorkspaceID).Scan(&seq)
	if e != nil {
		return nil, e
	}
	if _, ok := values["position"]; !ok {
		values["position"] = float64(seq) * 1024
	}
	cols := []string{"id", "workspace_id", "project_id", "created_by", "updated_by", "sequence_id"}
	args := []any{id, s.WorkspaceID, s.ProjectID, s.Actor.UserID, s.Actor.UserID, seq}
	place := []string{"$1", "$2", "$3", "$4", "$5", "$6"}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		cols = append(cols, k)
		args = append(args, values[k])
		p := fmt.Sprintf("$%d", len(args))
		if jsonColumn(k) {
			p += "::jsonb"
		}
		place = append(place, p)
	}
	_, e = q.ExecContext(c.Request.Context(), "INSERT INTO work_items ("+strings.Join(cols, ",")+") VALUES ("+strings.Join(place, ",")+")", args...)
	if e != nil {
		return nil, e
	}
	if e = h.replaceRelations(c, q, s, id, input); e != nil {
		return nil, e
	}
	item, e := loadIssue(c, q, s, id, false)
	if e != nil {
		return nil, e
	}
	if e = h.record(c, q, s, id, "created", nil, item); e != nil {
		return nil, e
	}
	return item, nil
}

func (h *handler) create(c *gin.Context) {
	s, e := h.scope(c, identity.Member)
	if e != nil {
		respondError(c, e)
		return
	}
	input, e := bindMap(c)
	if e != nil {
		respondError(c, e)
		return
	}
	var item map[string]any
	e = h.deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error { var err error; item, err = h.createItem(c, q, s, input); return err })
	if e != nil {
		respondError(c, e)
		return
	}
	httpapi.JSON(c, 201, item)
}

func (h *handler) updateItem(c *gin.Context, q database.DBTX, s identity.Scope, id uuid.UUID, input map[string]json.RawMessage, requireVersion bool) (map[string]any, error) {
	if e := lockGraphs(c, q, s.ProjectID); e != nil {
		return nil, e
	}
	_, pointSet := input["estimate_point_id"]
	_, numericSet := input["estimate"]
	if pointSet || numericSet {
		if e := data.LockEstimates(c.Request.Context(), q, s.ProjectID); e != nil {
			return nil, e
		}
	}
	old, e := loadIssue(c, q, s, id, true)
	if e != nil {
		return nil, e
	}
	s, e = CurrentScope(c.Request.Context(), q, s.Actor, s.WorkspaceID, s.ProjectID, identity.Guest)
	if e != nil {
		return nil, e
	}
	if s.Role < identity.Member && !s.GuestCanViewAll && old["created_by"] != s.Actor.UserID.String() {
		return nil, absent()
	}
	if s.Role < identity.Member && old["created_by"] != s.Actor.UserID.String() {
		return nil, httpapi.NewError(403, "forbidden", "Guests may change only their own work items")
	}
	if e = EnsureWritableProject(c.Request.Context(), q, s.WorkspaceID, s.ProjectID); e != nil {
		return nil, e
	}
	if hasRequirementInput(input) {
		if err := RequireRequirements(c.Request.Context(), q, s.WorkspaceID, s.ProjectID); err != nil {
			return nil, err
		}
	}
	var intakeStatus string
	e = q.QueryRowContext(c.Request.Context(), `SELECT status FROM intake_items WHERE work_item_id=$1 AND deleted_at IS NULL`, id).Scan(&intakeStatus)
	if e != nil && e != sql.ErrNoRows {
		return nil, e
	}
	if intakeStatus != "" && intakeStatus != "accepted" {
		if s.Role < identity.Admin && old["created_by"] != s.Actor.UserID.String() {
			return nil, httpapi.NewError(403, "forbidden", "Only the author or an administrator can edit a submission")
		}
		if draft, exists := input["is_draft"]; exists && string(draft) == "false" {
			return nil, invalid("Use intake review to accept a submission")
		}
	}
	var version int64
	if raw, ok := input["version"]; ok {
		if e = json.Unmarshal(raw, &version); e != nil || version < 1 {
			return nil, invalid("Invalid version")
		}
		if recordVersion(old) != version {
			return nil, conflict("This work item changed. Reload it before saving")
		}
	} else if requireVersion {
		return nil, invalid("Version is required")
	}
	values, e := fields(input)
	if e != nil {
		return nil, e
	}
	if e := validateDateRange(values, old); e != nil {
		return nil, e
	}
	if e = validateFields(c, q, s, id, values); e != nil {
		return nil, e
	}
	if e = validateRequirement(c, q, s, id, values, old, input); e != nil {
		return nil, e
	}
	keys := []string{}
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	args := []any{id, s.Actor.UserID}
	sets := []string{"updated_at=now()", "updated_by=$2", "version=version+1"}
	for _, k := range keys {
		args = append(args, values[k])
		p := fmt.Sprintf("$%d", len(args))
		if jsonColumn(k) {
			p += "::jsonb"
		}
		sets = append(sets, k+"="+p)
	}
	if _, e = q.ExecContext(c.Request.Context(), "UPDATE work_items SET "+strings.Join(sets, ",")+" WHERE id=$1", args...); e != nil {
		return nil, e
	}
	if e = h.replaceRelations(c, q, s, id, input); e != nil {
		return nil, e
	}
	item, e := loadIssue(c, q, s, id, false)
	if e != nil {
		return nil, e
	}
	if e = h.record(c, q, s, id, "updated", old, item); e != nil {
		return nil, e
	}
	return item, nil
}

func (h *handler) update(c *gin.Context) {
	s, e := h.scope(c, identity.Guest)
	if e != nil {
		respondError(c, e)
		return
	}
	id, e := uuid.Parse(c.Param("issueID"))
	if e != nil {
		respondError(c, invalid("Invalid identifier"))
		return
	}
	input, e := bindMap(c)
	if e != nil {
		respondError(c, e)
		return
	}
	var item map[string]any
	e = h.deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		var err error
		item, err = h.updateItem(c, q, s, id, input, true)
		return err
	})
	if e != nil {
		respondError(c, e)
		return
	}
	httpapi.JSON(c, 200, item)
}

func (h *handler) deleteItem(c *gin.Context, q database.DBTX, s identity.Scope, id uuid.UUID) error {
	if e := lockGraphs(c, q, s.ProjectID); e != nil {
		return e
	}
	old, e := loadIssue(c, q, s, id, true)
	if e != nil {
		return e
	}
	s, e = CurrentScope(c.Request.Context(), q, s.Actor, s.WorkspaceID, s.ProjectID, identity.Guest)
	if e != nil {
		return e
	}
	if s.Role < identity.Member && !s.GuestCanViewAll && old["created_by"] != s.Actor.UserID.String() {
		return absent()
	}
	var activities bool
	if e = q.QueryRowContext(c.Request.Context(), `SELECT EXISTS(SELECT 1 FROM requirement_activities WHERE epic_id=$1 AND deleted_at IS NULL)`, id).Scan(&activities); e != nil {
		return e
	}
	if activities {
		return invalid("Move or delete this Epic's activities before deleting it")
	}
	if s.Role < identity.Member && old["created_by"] != s.Actor.UserID.String() {
		return httpapi.NewError(403, "forbidden", "Guests may delete only their own work items")
	}
	if e = EnsureWritableProject(c.Request.Context(), q, s.WorkspaceID, s.ProjectID); e != nil {
		return e
	}
	if _, e = q.ExecContext(c.Request.Context(), `UPDATE work_items SET deleted_at=now(),updated_at=now(),updated_by=$2,version=version+1 WHERE id=$1`, id, s.Actor.UserID); e != nil {
		return e
	}
	if _, e = q.ExecContext(c.Request.Context(), `UPDATE work_items SET parent_id=NULL,updated_at=now(),version=version+1 WHERE parent_id=$1 AND deleted_at IS NULL`, id); e != nil {
		return e
	}
	return h.record(c, q, s, id, "deleted", old, nil)
}

func (h *handler) remove(c *gin.Context) {
	s, e := h.scope(c, identity.Guest)
	if e != nil {
		respondError(c, e)
		return
	}
	id, e := uuid.Parse(c.Param("issueID"))
	if e != nil {
		respondError(c, invalid("Invalid identifier"))
		return
	}
	e = h.deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error { return h.deleteItem(c, q, s, id) })
	if e != nil {
		respondError(c, e)
		return
	}
	c.Status(204)
}

func (h *handler) bulk(c *gin.Context) {
	s, e := h.scope(c, identity.Member)
	if e != nil {
		respondError(c, e)
		return
	}
	var body struct {
		IDs      []uuid.UUID                `json:"ids"`
		Changes  map[string]json.RawMessage `json:"changes"`
		Versions map[string]int64           `json:"versions"`
	}
	if e = bindInput(c, &body); e != nil || len(body.IDs) < 1 || len(body.IDs) > 200 {
		respondError(c, invalid("Supply between 1 and 200 work item IDs"))
		return
	}
	sort.Slice(body.IDs, func(i, j int) bool { return body.IDs[i].String() < body.IDs[j].String() })
	seen := map[uuid.UUID]bool{}
	for _, id := range body.IDs {
		if seen[id] {
			respondError(c, invalid("Duplicate work item identifier"))
			return
		}
		seen[id] = true
	}
	items := []map[string]any{}
	e = h.deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		for _, id := range body.IDs {
			if c.Request.Method == "DELETE" {
				if e := h.deleteItem(c, q, s, id); e != nil {
					return e
				}
			} else {
				changes := map[string]json.RawMessage{}
				for k, v := range body.Changes {
					changes[k] = v
				}
				if v, ok := body.Versions[id.String()]; ok {
					changes["version"], _ = json.Marshal(v)
				}
				item, e := h.updateItem(c, q, s, id, changes, true)
				if e != nil {
					return e
				}
				items = append(items, item)
			}
		}
		return nil
	})
	if e != nil {
		respondError(c, e)
		return
	}
	if c.Request.Method == "DELETE" {
		c.Status(204)
	} else {
		httpapi.JSON(c, 200, items)
	}
}

func (h *handler) duplicate(c *gin.Context) {
	s, id, _, e := h.issueScope(c, identity.Member)
	if e != nil {
		respondError(c, e)
		return
	}
	var result map[string]any
	e = h.deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		item, e := loadIssue(c, q, s, id, false)
		if e != nil {
			return e
		}
		input := map[string]json.RawMessage{}
		copyFields := []string{"name", "description_html", "description_json", "state_id", "priority", "assignee_ids", "label_ids", "start_date", "target_date", "estimate", "module_ids", "cycle_id", "parent_id", "dependency_ids"}
		if hasRequirementData(item) {
			copyFields = append(copyFields, "requirement_type", "story_role", "story_goal", "story_benefit", "acceptance_criteria", "activity_id", "map_position", "estimated_minutes", "remaining_minutes", "required_skills", "allocation_weights")
		}
		for _, k := range copyFields {
			if v, ok := item[k]; ok {
				input[k], _ = json.Marshal(v)
			}
		}
		if point := item["estimate_point_id"]; point != nil {
			delete(input, "estimate")
			input["estimate_point_id"], _ = json.Marshal(point)
		}
		if weights, ok := item["allocation_weights"].([]any); ok && len(weights) == 0 {
			delete(input, "allocation_weights")
		}
		name := []rune(item["name"].(string))
		if len(name) > 248 {
			name = name[:248]
		}
		input["name"], _ = json.Marshal(string(name) + " (copy)")
		result, e = h.createItem(c, q, s, input)
		return e
	})
	if e != nil {
		respondError(c, e)
		return
	}
	httpapi.JSON(c, 201, result)
}

func (h *handler) move(c *gin.Context) {
	s, id, _, e := h.issueScope(c, identity.Member)
	if e != nil {
		respondError(c, e)
		return
	}
	var body struct {
		ProjectID uuid.UUID                  `json:"project_id"`
		StateID   uuid.UUID                  `json:"state_id"`
		Version   int64                      `json:"version"`
		Changes   map[string]json.RawMessage `json:"changes"`
	}
	if e = bindInput(c, &body); e != nil || body.ProjectID == uuid.Nil || body.ProjectID == s.ProjectID {
		respondError(c, invalid("Choose a different target project"))
		return
	}
	for key := range body.Changes {
		switch key {
		case "position", "priority", "assignee_ids", "label_ids", "cycle_id", "module_ids", "estimate_point_id":
		default:
			respondError(c, invalid("Unsupported move change: "+key))
			return
		}
	}
	target, e := h.deps.Policy.Project(c.Request.Context(), s.Actor, s.WorkspaceID, body.ProjectID, identity.Member)
	if e != nil {
		respondError(c, e)
		return
	}
	var result map[string]any
	e = h.deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if e := lockGraphs(c, q, s.ProjectID, target.ProjectID); e != nil {
			return e
		}
		projects := []uuid.UUID{s.ProjectID, target.ProjectID}
		sort.Slice(projects, func(i, j int) bool { return projects[i].String() < projects[j].String() })
		for _, pid := range projects {
			if e := data.LockEstimates(c.Request.Context(), q, pid); e != nil {
				return e
			}
		}
		// The request may have waited for another graph/estimate transaction.
		// Order current membership changes against this move, then authorize
		// again using the same transaction that will change both project scopes.
		// Foundation membership writes also lock the workspace, so this covers
		// creation of a restrictive explicit role in an otherwise public project.
		if _, e := q.ExecContext(c.Request.Context(), `SELECT w.id FROM workspaces w JOIN workspace_members wm ON wm.workspace_id=w.id JOIN users u ON u.id=wm.user_id WHERE w.id=$1 AND u.id=$2 FOR SHARE OF w,wm,u`, s.WorkspaceID, s.Actor.UserID); e != nil {
			return e
		}
		if _, e := q.ExecContext(c.Request.Context(), `SELECT id FROM projects WHERE id IN($1,$2) ORDER BY id FOR SHARE`, s.ProjectID, target.ProjectID); e != nil {
			return e
		}
		if _, e := q.ExecContext(c.Request.Context(), `SELECT id FROM project_members WHERE workspace_id=$1 AND user_id=$2 AND project_id IN($3,$4) ORDER BY project_id,id FOR SHARE`, s.WorkspaceID, s.Actor.UserID, s.ProjectID, target.ProjectID); e != nil {
			return e
		}
		var accessErr error
		s, accessErr = CurrentScope(c.Request.Context(), q, s.Actor, s.WorkspaceID, s.ProjectID, identity.Member)
		if accessErr != nil {
			return accessErr
		}
		target, accessErr = CurrentScope(c.Request.Context(), q, s.Actor, s.WorkspaceID, target.ProjectID, identity.Member)
		if accessErr != nil {
			return accessErr
		}
		old, e := loadIssue(c, q, s, id, true)
		if e != nil {
			return e
		}
		if body.Version != recordVersion(old) {
			return conflict("The work item changed before it could be moved")
		}
		var children int
		if e = q.QueryRowContext(c.Request.Context(), `SELECT count(*) FROM work_items WHERE parent_id=$1 AND deleted_at IS NULL`, id).Scan(&children); e != nil {
			return e
		}
		if children > 0 {
			return invalid("Move or detach sub-items before moving this work item")
		}
		if body.StateID == uuid.Nil {
			if e = q.QueryRowContext(c.Request.Context(), `SELECT id FROM states WHERE project_id=$1 AND deleted_at IS NULL ORDER BY is_default DESC,position,id LIMIT 1`, target.ProjectID).Scan(&body.StateID); e != nil {
				return e
			}
		}
		values, e := fields(body.Changes)
		if e != nil {
			return e
		}
		values["state_id"] = body.StateID
		if e = validateFields(c, q, target, id, values); e != nil {
			return e
		}
		var seq int64
		if e = q.QueryRowContext(c.Request.Context(), `UPDATE projects SET next_sequence=next_sequence+1 WHERE id=$1 RETURNING next_sequence-1`, target.ProjectID).Scan(&seq); e != nil {
			return e
		}
		for _, table := range []string{"work_item_assignees", "work_item_labels", "cycle_items", "module_items"} {
			if _, e = q.ExecContext(c.Request.Context(), "DELETE FROM "+table+" WHERE work_item_id=$1", id); e != nil {
				return e
			}
		}
		if _, e = q.ExecContext(c.Request.Context(), `UPDATE work_item_relations SET deleted_at=now(),updated_at=now(),project_id=CASE WHEN source_id=$1 THEN $2 ELSE project_id END WHERE source_id=$1 OR target_id=$1`, id, target.ProjectID); e != nil {
			return e
		}
		for _, table := range []string{"comments", "activities", "work_item_subscribers", "work_item_links", "reactions", "file_assets", "work_item_versions"} {
			if _, e = q.ExecContext(c.Request.Context(), "UPDATE "+table+" SET project_id=$2 WHERE work_item_id=$1", id, target.ProjectID); e != nil {
				return e
			}
		}
		if _, e = q.ExecContext(c.Request.Context(), `UPDATE reactions SET project_id=$2,updated_at=now() WHERE comment_id IN(SELECT id FROM comments WHERE work_item_id=$1)`, id, target.ProjectID); e != nil {
			return e
		}
		if _, e = q.ExecContext(c.Request.Context(), `UPDATE notifications SET project_id=$2,updated_at=now() WHERE entity_type IN('issue','work_item') AND entity_id=$1 AND workspace_id=$3`, id, target.ProjectID, s.WorkspaceID); e != nil {
			return e
		}
		for _, table := range []string{"intake_items", "public_votes"} {
			if _, e = q.ExecContext(c.Request.Context(), "UPDATE "+table+" SET deleted_at=COALESCE(deleted_at,now()),updated_at=now(),project_id=$2 WHERE work_item_id=$1", id, target.ProjectID); e != nil {
				return e
			}
		}
		if _, e = q.ExecContext(c.Request.Context(), `UPDATE work_item_subscribers sub SET deleted_at=now(),updated_at=now() WHERE sub.work_item_id=$1 AND sub.deleted_at IS NULL AND NOT EXISTS(SELECT 1 FROM workspace_members wm JOIN users u ON u.id=wm.user_id AND u.is_active AND u.deleted_at IS NULL JOIN projects p ON p.workspace_id=wm.workspace_id LEFT JOIN project_members pm ON pm.project_id=p.id AND pm.user_id=wm.user_id AND pm.is_active AND pm.deleted_at IS NULL WHERE wm.workspace_id=$2 AND wm.user_id=sub.user_id AND wm.is_active AND wm.deleted_at IS NULL AND p.id=$3 AND (p.network='public' OR pm.id IS NOT NULL))`, id, s.WorkspaceID, target.ProjectID); e != nil {
			return e
		}
		values["project_id"], values["sequence_id"], values["parent_id"], values["activity_id"], values["allocation_weights"] = target.ProjectID, seq, nil, nil, "[]"
		if _, selected := body.Changes["estimate_point_id"]; !selected {
			values["estimate_point_id"], values["estimate"] = nil, nil
		}
		keys := make([]string, 0, len(values))
		for key := range values {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		args := []any{id, s.Actor.UserID}
		sets := []string{"version=version+1", "updated_at=now()", "updated_by=$2"}
		for _, key := range keys {
			args = append(args, values[key])
			sets = append(sets, fmt.Sprintf("%s=$%d", key, len(args)))
		}
		if _, e = q.ExecContext(c.Request.Context(), "UPDATE work_items SET "+strings.Join(sets, ",")+" WHERE id=$1", args...); e != nil {
			return e
		}
		// Apply destination groups inside the move transaction. A failed drop
		// must also restore source associations, numbering, history and events.
		if e = h.replaceRelations(c, q, target, id, body.Changes); e != nil {
			return e
		}
		result, e = loadIssue(c, q, target, id, false)
		if e != nil {
			return e
		}
		return h.record(c, q, target, id, "moved", old, result)
	})
	if e != nil {
		respondError(c, e)
		return
	}
	httpapi.JSON(c, 200, result)
}

func (h *handler) activities(c *gin.Context) {
	s, id, _, e := h.issueScope(c, identity.Guest)
	if e != nil {
		respondError(c, e)
		return
	}
	data, e := execJSON(c, h.deps.DB.SQL, `SELECT (to_jsonb(a)-'deleted_at')||jsonb_build_object('actor',jsonb_build_object('id',u.id,'display_name',u.display_name,'avatar_url',u.avatar_url)) FROM activities a JOIN users u ON u.id=a.actor_id WHERE a.work_item_id=$1 AND a.workspace_id=$2 AND a.project_id=$3 AND a.deleted_at IS NULL ORDER BY a.created_at DESC,a.id LIMIT 200`, id, s.WorkspaceID, s.ProjectID)
	if e != nil {
		respondError(c, e)
		return
	}
	httpapi.JSON(c, 200, data)
}
func (h *handler) versions(c *gin.Context) {
	s, id, _, e := h.issueScope(c, identity.Guest)
	if e != nil {
		respondError(c, e)
		return
	}
	data, e := execJSON(c, h.deps.DB.SQL, `SELECT to_jsonb(v)-'deleted_at' FROM work_item_versions v WHERE work_item_id=$1 AND workspace_id=$2 AND project_id=$3 AND deleted_at IS NULL ORDER BY version DESC LIMIT 100`, id, s.WorkspaceID, s.ProjectID)
	if e != nil {
		respondError(c, e)
		return
	}
	httpapi.JSON(c, 200, data)
}
