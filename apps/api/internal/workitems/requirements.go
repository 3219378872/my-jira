package workitems

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/httpapi"
	"my-jira/apps/api/internal/platform/identity"
)

// RequireRequirements is the common project rollout gate. Authorize the actor
// first, and for a mutation hold the project's row lock through commit.
// Browser SSE intentionally remains available for metadata-only re-enablement.
func RequireRequirements(ctx context.Context, q database.DBTX, wid, pid uuid.UUID) error {
	var enabled bool
	if err := q.QueryRowContext(ctx, `SELECT COALESCE(settings->'requirements_enabled','null'::jsonb)<>'false'::jsonb FROM projects WHERE id=$1 AND workspace_id=$2 AND deleted_at IS NULL`, pid, wid).Scan(&enabled); err != nil {
		return err
	}
	if !enabled {
		return httpapi.NewError(403, "requirements_disabled", "Requirements views are disabled for this project")
	}
	return nil
}

func hasRequirementInput(input map[string]json.RawMessage) bool {
	for key := range input {
		switch key {
		case "requirement_type", "story_role", "story_goal", "story_benefit", "acceptance_criteria", "activity_id", "map_position", "estimated_minutes", "remaining_minutes", "required_skills", "allocation_weights", "planning_locked":
			return true
		}
	}
	return false
}

func hasRequirementData(item map[string]any) bool {
	for _, key := range []string{"requirement_type", "activity_id", "estimated_minutes", "remaining_minutes"} {
		if item[key] != nil {
			return true
		}
	}
	for _, key := range []string{"story_role", "story_goal", "story_benefit"} {
		if text, ok := item[key].(string); ok && text != "" {
			return true
		}
	}
	for _, key := range []string{"required_skills", "acceptance_criteria"} {
		if values, ok := item[key].([]any); ok && len(values) > 0 {
			return true
		}
	}
	return false
}

type allocationWeight struct {
	MemberID uuid.UUID `json:"member_id"`
	Weight   int64     `json:"weight"`
}

func cloneInput(input map[string]json.RawMessage) map[string]json.RawMessage {
	out := make(map[string]json.RawMessage, len(input))
	for k, v := range input {
		out[k] = v
	}
	return out
}

func requirementField(key string, raw json.RawMessage) (any, bool, error) {
	switch key {
	case "requirement_type":
		if string(raw) == "null" {
			return nil, true, nil
		}
		var value string
		if json.Unmarshal(raw, &value) != nil || (value != "epic" && value != "story" && value != "task") {
			return nil, true, invalid("requirement_type must be epic, story, task or null")
		}
		return value, true, nil
	case "story_role", "story_goal", "story_benefit":
		var value string
		if string(raw) == "null" || json.Unmarshal(raw, &value) != nil || len([]rune(value)) > 10000 {
			return nil, true, invalid(key + " must be text of at most 10000 characters")
		}
		return strings.TrimSpace(value), true, nil
	case "acceptance_criteria", "required_skills":
		var values []string
		if string(raw) == "null" || json.Unmarshal(raw, &values) != nil || values == nil || len(values) > 100 {
			return nil, true, invalid(key + " must contain at most 100 strings")
		}
		seen := map[string]bool{}
		for i, value := range values {
			value = strings.TrimSpace(value)
			if value == "" || len([]rune(value)) > 4000 {
				return nil, true, invalid(key + " contains an empty or overly long value")
			}
			if key == "required_skills" {
				value = strings.ToLower(value)
				if seen[value] {
					return nil, true, invalid("Duplicate required skill")
				}
				seen[value] = true
			}
			values[i] = value
		}
		encoded, _ := json.Marshal(values)
		return string(encoded), true, nil
	case "estimated_minutes", "remaining_minutes":
		if string(raw) == "null" {
			return nil, true, nil
		}
		var value int64
		if json.Unmarshal(raw, &value) != nil || value < 0 || value > 52560000 {
			return nil, true, invalid(key + " must be integer minutes between 0 and 52560000")
		}
		return value, true, nil
	case "allocation_weights":
		var values []allocationWeight
		if string(raw) == "null" || json.Unmarshal(raw, &values) != nil || values == nil || len(values) > 200 {
			return nil, true, invalid("allocation_weights must be an array of member_id and weight")
		}
		seen := map[uuid.UUID]bool{}
		var total int64
		for _, v := range values {
			if v.MemberID == uuid.Nil || seen[v.MemberID] || v.Weight < 0 || v.Weight > 1000000 {
				return nil, true, invalid("Allocation weights require distinct members and nonnegative integer weights")
			}
			seen[v.MemberID] = true
			total += v.Weight
		}
		if len(values) > 0 && total == 0 {
			return nil, true, invalid("Allocation weights must have a positive total")
		}
		sort.Slice(values, func(i, j int) bool { return values[i].MemberID.String() < values[j].MemberID.String() })
		encoded, _ := json.Marshal(values)
		return string(encoded), true, nil
	}
	return nil, false, nil
}

func jsonColumn(key string) bool {
	return key == "description_json" || key == "acceptance_criteria" || key == "required_skills" || key == "allocation_weights"
}

func mergedText(values, old map[string]any, key string) string {
	v, ok := values[key]
	if !ok {
		v = old[key]
	}
	if v == nil {
		return ""
	}
	switch v := v.(type) {
	case string:
		return v
	case uuid.UUID:
		return v.String()
	}
	return ""
}

func validateRequirement(c *gin.Context, q database.DBTX, s identity.Scope, id uuid.UUID, values, old map[string]any, input map[string]json.RawMessage) error {
	ctx := c.Request.Context()
	kind := mergedText(values, old, "requirement_type")
	parent := mergedText(values, old, "parent_id")
	activity := mergedText(values, old, "activity_id")
	if kind == "epic" && parent != "" {
		return invalid("An Epic cannot have a parent")
	}
	if parent != "" && kind != "" {
		var parentKind *string
		if err := q.QueryRowContext(ctx, `SELECT requirement_type FROM work_items WHERE id=$1 AND workspace_id=$2 AND project_id=$3 AND deleted_at IS NULL AND (archived_at IS NULL OR $4)`, parent, s.WorkspaceID, s.ProjectID, parent == mergedText(nil, old, "parent_id")).Scan(&parentKind); err != nil {
			return invalid("Parent is not accessible in this project")
		}
		expected := "story"
		if kind == "story" {
			expected = "epic"
		}
		if parentKind == nil || *parentKind != expected {
			return invalid("A Story parent must be an Epic; a Task parent must be a Story")
		}
	}
	var invalidChildren bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM work_items WHERE parent_id=$1 AND deleted_at IS NULL AND requirement_type IS NOT NULL AND NOT (($2='epic' AND requirement_type='story') OR ($2='story' AND requirement_type='task')))`, id, kind).Scan(&invalidChildren); err != nil {
		return err
	}
	if invalidChildren {
		return invalid("Changing this type would invalidate its classified children")
	}
	if kind != "epic" {
		var hasActivities bool
		if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM requirement_activities WHERE epic_id=$1 AND deleted_at IS NULL)`, id).Scan(&hasActivities); err != nil {
			return err
		}
		if hasActivities {
			return invalid("Move this Epic's activities before changing its type")
		}
	}
	if activity != "" {
		if kind != "story" {
			return invalid("Only Stories can belong to a user activity")
		}
		var epic *uuid.UUID
		if err := q.QueryRowContext(ctx, `SELECT epic_id FROM requirement_activities WHERE id=$1 AND workspace_id=$2 AND project_id=$3 AND deleted_at IS NULL AND (archived_at IS NULL OR $4)`, activity, s.WorkspaceID, s.ProjectID, activity == mergedText(nil, old, "activity_id")).Scan(&epic); err != nil {
			return invalid("Activity is not active in this project")
		}
		if (epic == nil && parent != "") || (epic != nil && epic.String() != parent) {
			return invalid("Story and activity must belong to the same Epic")
		}
	}
	if raw, changed := input["assignee_ids"]; changed {
		ids, err := uuidArray(raw)
		if err != nil {
			return err
		}
		if _, explicit := input["allocation_weights"]; !explicit {
			weights := make([]allocationWeight, 0, len(ids))
			for _, uid := range ids {
				weights = append(weights, allocationWeight{uid, 1})
			}
			sort.Slice(weights, func(i, j int) bool { return weights[i].MemberID.String() < weights[j].MemberID.String() })
			encoded, _ := json.Marshal(weights)
			values["allocation_weights"] = string(encoded)
		}
	}
	if raw, present := values["allocation_weights"]; present {
		var weights []allocationWeight
		_ = json.Unmarshal([]byte(raw.(string)), &weights)
		var ids []uuid.UUID
		if raw, ok := input["assignee_ids"]; ok {
			var err error
			ids, err = uuidArray(raw)
			if err != nil {
				return err
			}
		} else {
			rows, err := q.QueryContext(ctx, `SELECT user_id FROM work_item_assignees WHERE work_item_id=$1 AND deleted_at IS NULL`, id)
			if err != nil {
				return err
			}
			for rows.Next() {
				var uid uuid.UUID
				if err = rows.Scan(&uid); err != nil {
					rows.Close()
					return err
				}
				ids = append(ids, uid)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
		}
		if len(weights) != len(ids) {
			return invalid("Allocation members must match all work-item assignees")
		}
		set := map[uuid.UUID]bool{}
		for _, uid := range ids {
			set[uid] = true
		}
		for _, weight := range weights {
			if !set[weight.MemberID] {
				return invalid("Allocation member is not an assignee")
			}
		}
	}
	return nil
}

func replaceDependencies(c *gin.Context, q database.DBTX, s identity.Scope, id uuid.UUID, input map[string]json.RawMessage) error {
	raw, ok := input["dependency_ids"]
	if !ok {
		return nil
	}
	if s.Role < identity.Member {
		return invalid("Only members can change dependencies")
	}
	ids, err := uuidArray(raw)
	if err != nil {
		return err
	}
	ctx := c.Request.Context()
	for _, parent := range ids {
		if parent == id {
			return invalid("A work item cannot block itself")
		}
		if _, err := loadIssue(c, q, s, parent, false); err != nil {
			return invalid("Dependency is not accessible in this project")
		}
	}
	if _, err = q.ExecContext(ctx, `DELETE FROM work_item_relations WHERE target_id=$1 AND relation_type='blocks'`, id); err != nil {
		return err
	}
	for _, source := range ids {
		var cycle bool
		if err = q.QueryRowContext(ctx, `WITH RECURSIVE reachable(id,path) AS (SELECT $1::uuid,ARRAY[$1::uuid] UNION ALL SELECT r.target_id,x.path||r.target_id FROM work_item_relations r JOIN reachable x ON r.source_id=x.id WHERE r.project_id=$3 AND r.relation_type='blocks' AND r.deleted_at IS NULL AND NOT r.target_id=ANY(x.path)) SELECT EXISTS(SELECT 1 FROM reachable WHERE id=$2)`, id, source, s.ProjectID).Scan(&cycle); err != nil {
			return err
		}
		if cycle {
			return invalid("This dependency would form a cycle")
		}
		if _, err = q.ExecContext(ctx, `INSERT INTO work_item_relations(id,workspace_id,project_id,source_id,target_id,relation_type) VALUES($1,$2,$3,$4,$5,'blocks')`, uuid.New(), s.WorkspaceID, s.ProjectID, source, id); err != nil {
			return err
		}
	}
	return nil
}
