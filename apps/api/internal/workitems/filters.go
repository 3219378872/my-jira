package workitems

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"my-jira/apps/api/internal/platform/httpapi"
)

type filterNode struct {
	And   []filterNode    `json:"and,omitempty"`
	Or    []filterNode    `json:"or,omitempty"`
	Not   *filterNode     `json:"not,omitempty"`
	Field string          `json:"field,omitempty"`
	Op    string          `json:"op,omitempty"`
	Value json.RawMessage `json:"value,omitempty"`
}

type filterBuilder struct {
	args  []any
	actor uuid.UUID
	nodes int
}

func (b *filterBuilder) parameter(value any) string {
	b.args = append(b.args, value)
	return fmt.Sprintf("$%d", len(b.args))
}

type queryField struct{ expression, kind, relation string }

var queryFields = map[string]queryField{
	"id": {"w.id", "uuid", ""}, "project_id": {"w.project_id", "uuid", ""},
	"state_id": {"w.state_id", "uuid", ""}, "state_group": {"(SELECT group_name FROM states WHERE id=w.state_id)", "state_group", ""},
	"parent_id": {"w.parent_id", "uuid", ""}, "created_by": {"w.created_by", "uuid", ""},
	"requirement_type": {"w.requirement_type", "text", ""}, "activity_id": {"w.activity_id", "uuid", ""},
	"estimated_minutes": {"w.estimated_minutes", "number", ""}, "remaining_minutes": {"w.remaining_minutes", "number", ""},
	"planning_locked": {"w.planning_locked", "bool", ""},
	"priority":        {"w.priority", "priority", ""}, "name": {"w.name", "text", ""},
	"sequence_id": {"w.sequence_id", "number", ""}, "estimate": {"w.estimate", "number", ""},
	"estimate_point_id": {"w.estimate_point_id", "uuid", ""},
	"start_date":        {"w.start_date", "date", ""}, "target_date": {"w.target_date", "date", ""},
	"created_at": {"w.created_at::date", "date", ""}, "updated_at": {"w.updated_at::date", "date", ""},
	"completed_at": {"w.completed_at::date", "date", ""}, "is_draft": {"w.is_draft", "bool", ""},
	"assignee_id":   {"r.user_id", "uuid", "work_item_assignees"},
	"label_id":      {"r.label_id", "uuid", "work_item_labels"},
	"cycle_id":      {"r.cycle_id", "uuid", "cycle_items"},
	"module_id":     {"r.module_id", "uuid", "module_items"},
	"subscriber_id": {"r.user_id", "uuid", "work_item_subscribers"},
	"mention_id":    {"", "uuid", "mentions"},
}

func (b *filterBuilder) node(n filterNode, depth int) (string, error) {
	b.nodes++
	if depth > 6 || b.nodes > 80 {
		return "", invalid("The filter is too complex")
	}
	variants := 0
	for _, yes := range []bool{n.And != nil, n.Or != nil, n.Not != nil, n.Field != ""} {
		if yes {
			variants++
		}
	}
	if variants != 1 {
		return "", invalid("Each filter needs exactly one of and, or, not, or field")
	}
	if n.And != nil || n.Or != nil {
		children, join := n.And, " AND "
		if n.Or != nil {
			children, join = n.Or, " OR "
		}
		if len(children) == 0 || len(children) > 40 {
			return "", invalid("A filter group needs between 1 and 40 conditions")
		}
		parts := make([]string, 0, len(children))
		for _, child := range children {
			part, err := b.node(child, depth+1)
			if err != nil {
				return "", err
			}
			parts = append(parts, "("+part+")")
		}
		return strings.Join(parts, join), nil
	}
	if n.Not != nil {
		part, err := b.node(*n.Not, depth+1)
		return "NOT COALESCE((" + part + "),false)", err
	}
	f, ok := queryFields[n.Field]
	if !ok {
		return "", invalid("Unknown filter field: " + n.Field)
	}
	op := n.Op
	if op == "" {
		op = "eq"
	}
	if op == "is_empty" || op == "not_empty" {
		if f.relation == "mentions" {
			return "", invalid("Mention filters require a user identifier")
		}
		part := f.expression + " IS NULL"
		if f.relation != "" {
			part = "NOT " + relationExists(f, "true")
		}
		if op == "not_empty" {
			part = "NOT (" + part + ")"
		}
		return part, nil
	}
	if op == "eq" || op == "ne" || op == "in" || op == "not_in" || op == "all" {
		var values []json.RawMessage
		if op == "in" || op == "not_in" || op == "all" {
			if err := json.Unmarshal(n.Value, &values); err != nil || len(values) == 0 || len(values) > 200 {
				return "", invalid("An inclusion filter needs 1 to 200 values")
			}
		} else {
			values = []json.RawMessage{n.Value}
		}
		parts := make([]string, 0, len(values))
		for _, value := range values {
			if string(value) == "null" {
				if f.relation == "mentions" {
					return "", invalid("Mention filters require a user identifier")
				}
				part := f.expression + " IS NULL"
				if f.relation != "" {
					part = "NOT " + relationExists(f, "true")
				}
				parts = append(parts, part)
				continue
			}
			parsed, err := b.value(f.kind, value)
			if err != nil {
				return "", err
			}
			parameter := b.parameter(parsed)
			part := f.expression + "=" + parameter
			if f.relation == "mentions" {
				part = mentionExists(parameter)
			} else if f.relation != "" {
				part = relationExists(f, part)
			}
			parts = append(parts, "("+part+")")
		}
		join := " OR "
		if op == "all" {
			join = " AND "
		}
		part := "(" + strings.Join(parts, join) + ")"
		if op == "ne" || op == "not_in" {
			part = "NOT COALESCE(" + part + ",false)"
		}
		return part, nil
	}
	if op == "contains" || op == "starts_with" {
		if f.kind != "text" {
			return "", invalid("Text matching requires a text field")
		}
		value, err := b.value("text", n.Value)
		if err != nil {
			return "", err
		}
		text := strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(value.(string)) + "%"
		if op == "contains" {
			text = "%" + text
		}
		return f.expression + " ILIKE " + b.parameter(text), nil
	}
	comparison, ok := map[string]string{"gt": ">", "gte": ">=", "lt": "<", "lte": "<="}[op]
	if !ok || (f.kind != "date" && f.kind != "number") {
		return "", invalid("Unsupported filter operator")
	}
	value, err := b.value(f.kind, n.Value)
	if err != nil {
		return "", err
	}
	return f.expression + comparison + b.parameter(value), nil
}

func relationExists(f queryField, condition string) string {
	return "EXISTS(SELECT 1 FROM " + f.relation + " r WHERE r.work_item_id=w.id AND r.workspace_id=w.workspace_id AND r.project_id=w.project_id AND r.deleted_at IS NULL AND " + condition + ")"
}

func mentionExists(parameter string) string {
	match := `jsonb_path_exists(%s, '$.** ? (@.type == "mention" && @.attrs.id == $uid)',jsonb_build_object('uid',` + parameter + `::text))`
	return "(" + fmt.Sprintf(match, "w.description_json") + " OR EXISTS(SELECT 1 FROM comments mc WHERE mc.work_item_id=w.id AND mc.deleted_at IS NULL AND " + fmt.Sprintf(match, "mc.body_json") + "))"
}

func (b *filterBuilder) value(kind string, raw json.RawMessage) (any, error) {
	if kind == "bool" {
		var value bool
		if string(raw) == "null" || json.Unmarshal(raw, &value) != nil {
			return nil, invalid("Expected a boolean filter value")
		}
		return value, nil
	}
	if kind == "number" {
		var value json.Number
		if json.Unmarshal(raw, &value) != nil {
			return nil, invalid("Expected a number filter value")
		}
		number, err := strconv.ParseFloat(string(value), 64)
		if err != nil || number < -1e15 || number > 1e15 {
			return nil, invalid("Invalid numeric filter value")
		}
		return number, nil
	}
	if kind == "date" && len(raw) > 0 && raw[0] == '{' {
		var relative struct {
			RelativeDays *int `json:"relative_days"`
		}
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&relative) != nil || relative.RelativeDays == nil || *relative.RelativeDays < -36500 || *relative.RelativeDays > 36500 {
			return nil, invalid("Relative dates need relative_days between -36500 and 36500")
		}
		return time.Now().UTC().AddDate(0, 0, *relative.RelativeDays).Format("2006-01-02"), nil
	}
	value, err := stringField(raw)
	if err != nil || len(value) > 1000 {
		return nil, invalid("Invalid filter value")
	}
	switch kind {
	case "uuid":
		if value == "me" {
			return b.actor, nil
		}
		id, err := uuid.Parse(value)
		if err != nil || id == uuid.Nil {
			return nil, invalid("Invalid filter identifier")
		}
		return id, nil
	case "date":
		if _, err := time.Parse("2006-01-02", value); err != nil {
			return nil, invalid("Dates must use YYYY-MM-DD")
		}
	case "priority":
		if !validPriority(value) {
			return nil, invalid("Invalid priority filter")
		}
	case "state_group":
		if !strings.Contains("|backlog|unstarted|started|completed|cancelled|", "|"+value+"|") {
			return nil, invalid("Invalid state group")
		}
	}
	return value, nil
}

func advancedQuery(c *gin.Context, where string, args []any) (string, []any, error) {
	actor, err := httpapi.Actor(c)
	if err != nil {
		return "", nil, err
	}
	b := filterBuilder{args: args, actor: actor.UserID}
	appendNode := func(node filterNode) error {
		part, err := b.node(node, 0)
		if err == nil {
			where += " AND (" + part + ")"
		}
		return err
	}
	for key, f := range queryFields {
		if key == "name" || key == "is_draft" {
			continue
		}
		if text, ok := c.GetQuery(key); ok {
			if text == "" {
				return "", nil, invalid("Filter values cannot be empty")
			}
			var values []any
			for _, part := range strings.Split(text, ",") {
				part = strings.TrimSpace(part)
				if part == "null" || (part == "none" && f.kind != "priority") {
					values = append(values, nil)
				} else if f.kind == "number" {
					num, e := strconv.ParseFloat(part, 64)
					if e != nil {
						return "", nil, invalid("Invalid numeric filter")
					}
					values = append(values, num)
				} else {
					values = append(values, part)
				}
			}
			raw, _ := json.Marshal(values)
			if err := appendNode(filterNode{Field: key, Op: "in", Value: raw}); err != nil {
				return "", nil, err
			}
		}
		if f.kind == "date" {
			for suffix, op := range map[string]string{"_before": "lte", "_after": "gte"} {
				if text := c.Query(key + suffix); text != "" {
					raw, _ := json.Marshal(text)
					if err := appendNode(filterNode{Field: key, Op: op, Value: raw}); err != nil {
						return "", nil, err
					}
				}
			}
		}
	}
	if search := strings.TrimSpace(c.Query("search")); search != "" {
		if len(search) > 1000 {
			return "", nil, invalid("Search is too long")
		}
		parameter := b.parameter("%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(search) + "%")
		where += " AND (w.name ILIKE " + parameter + " OR concat((SELECT identifier FROM projects WHERE id=w.project_id),'-',w.sequence_id) ILIKE " + parameter + ")"
	}
	for key, column := range map[string]string{"archived": "w.archived_at IS NOT NULL", "draft": "w.is_draft", "deleted": "w.deleted_at IS NOT NULL"} {
		value := c.DefaultQuery(key, "false")
		if value != "all" && value != "true" && value != "false" {
			return "", nil, invalid(key + " must be true, false, or all")
		}
		if key == "deleted" {
			continue
		} // Base scope selects deleted rows only when explicitly requested.
		if value == "true" {
			where += " AND (" + column + ")"
		} else if value == "false" {
			where += " AND NOT (" + column + ")"
		}
	}
	if value := c.Query("include_subitems"); value != "" {
		if value != "true" && value != "false" {
			return "", nil, invalid("include_subitems must be a boolean")
		}
		if value == "false" {
			where += " AND w.parent_id IS NULL"
		}
	}
	if value := c.Query("scheduled"); value != "" {
		if value != "true" && value != "false" {
			return "", nil, invalid("scheduled must be a boolean")
		}
		if value == "true" {
			where += " AND w.start_date IS NOT NULL AND w.target_date IS NOT NULL"
		} else {
			where += " AND (w.start_date IS NULL OR w.target_date IS NULL)"
		}
	}
	if text := c.Query("filter"); text != "" {
		if len(text) > 32768 {
			return "", nil, invalid("Filter is too large")
		}
		decoder := json.NewDecoder(strings.NewReader(text))
		decoder.DisallowUnknownFields()
		var node filterNode
		if err := decoder.Decode(&node); err != nil {
			return "", nil, invalid("Invalid filter JSON")
		}
		var trailing any
		if decoder.Decode(&trailing) != io.EOF {
			return "", nil, invalid("Only one filter object is allowed")
		}
		if err := appendNode(node); err != nil {
			return "", nil, err
		}
	}
	// Pending and rejected intake entries have their own collection.
	if value := c.Query("intake_status"); value != "" {
		parts := []string{}
		for _, status := range strings.Split(value, ",") {
			if !strings.Contains("|pending|accepted|rejected|duplicate|snoozed|", "|"+status+"|") {
				return "", nil, invalid("Invalid intake status")
			}
			parts = append(parts, b.parameter(status))
		}
		where += " AND EXISTS(SELECT 1 FROM intake_items it WHERE it.work_item_id=w.id AND it.deleted_at IS NULL AND it.status IN(" + strings.Join(parts, ",") + "))"
	} else {
		where += " AND NOT EXISTS(SELECT 1 FROM intake_items it WHERE it.work_item_id=w.id AND it.deleted_at IS NULL AND it.status<>'accepted')"
	}
	return where, b.args, nil
}
