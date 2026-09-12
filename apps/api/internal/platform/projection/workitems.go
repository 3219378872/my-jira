// Package projection selects fields from already-authorized API representations.
// It never queries an entity or widens the domain handler's visibility.
package projection

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"my-jira/apps/api/internal/platform/httpapi"
)

const maxResponseBytes = 16 << 20

var issueFields = strings.Fields(`id workspace_id project_id state_id parent_id created_by updated_by
	name description_html description_json priority sequence_id position start_date target_date
	completed_at archived_at is_draft estimate estimate_point_id type_name version created_at updated_at
	assignee_ids label_ids cycle_id module_ids sub_item_count comment_count attachment_count
	requirement_type story_role story_goal story_benefit acceptance_criteria activity_id map_position
	estimated_minutes remaining_minutes required_skills allocation_weights planning_locked dependency_ids`)

type expansion struct {
	property string
	fields   []string
	array    bool
}

var expansions = map[string]expansion{
	"state":          {"state_detail", strings.Fields("id workspace_id project_id name color group position is_default created_at updated_at"), false},
	"project":        {"project_detail", strings.Fields("id name identifier icon color"), false},
	"assignees":      {"assignee_details", strings.Fields("id display_name avatar_url"), true},
	"labels":         {"label_details", strings.Fields("id name color"), true},
	"estimate_point": {"estimate_point_detail", strings.Fields("id workspace_id project_id estimate_id label numeric_value position created_at updated_at"), false},
}

// FieldNames and ExpansionNames let the API contract use the runtime allowlists.
// Returned slices are copies and cannot change the middleware's policy.
func FieldNames() []string { return append([]string(nil), issueFields...) }
func ExpansionNames() []string {
	names := make([]string, 0, len(expansions))
	for name := range expansions {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func Supports(path string) bool {
	switch path {
	case "/api/v1/workspaces/:workspaceID/issues",
		"/api/v1/workspaces/:workspaceID/issues/lookup/:identifier",
		"/api/v1/workspaces/:workspaceID/projects/:projectID/issues",
		"/api/v1/workspaces/:workspaceID/projects/:projectID/issues/:issueID":
		return true
	}
	return false
}

// WorkItems wraps only opted-in work-item reads. The handler completes normal
// authorization and filtering before query validation or field selection. Error
// responses are preserved, including errors for revoked or cross-tenant access.
func WorkItems() gin.HandlerFunc {
	return func(c *gin.Context) {
		values := c.Request.URL.Query()
		if c.Request.Method != http.MethodGet || !Supports(c.FullPath()) || (!values.Has("fields") && !values.Has("expand")) {
			c.Next()
			return
		}
		original := c.Writer
		captured := &responseBuffer{ResponseWriter: original, status: http.StatusOK, size: -1}
		c.Writer = captured
		defer func() { c.Writer = original }()
		c.Next()
		c.Writer = original
		original.Header().Del("Content-Length")
		if captured.status < 200 || captured.status >= 300 {
			original.WriteHeader(captured.status)
			_, _ = original.Write(captured.body.Bytes())
			return
		}
		if captured.tooLarge {
			httpapi.Fail(c, httpapi.NewError(413, "projection_too_large", "Use a smaller list limit before selecting response fields"))
			return
		}
		selection, err := parseSelection(c.Request.URL.RawQuery)
		if err != nil {
			httpapi.Fail(c, httpapi.NewError(400, "validation_failed", err.Error()))
			return
		}
		projected, err := selection.response(captured.body.Bytes())
		if err != nil {
			httpapi.Fail(c, httpapi.NewError(500, "projection_failed", "The authorized response could not be projected"))
			return
		}
		c.Data(captured.status, "application/json; charset=utf-8", projected)
	}
}

// Buffering is bounded because large descriptions can dominate collection reads.
// It is used only for JSON GET handlers, never downloads or streamed responses.
type responseBuffer struct {
	gin.ResponseWriter
	body     bytes.Buffer
	status   int
	size     int
	tooLarge bool
}

func (w *responseBuffer) WriteHeader(status int) {
	if w.size < 0 {
		w.status = status
	}
}
func (w *responseBuffer) WriteHeaderNow() {
	if w.size < 0 {
		w.size = 0
	}
}
func (w *responseBuffer) Write(value []byte) (int, error) {
	w.WriteHeaderNow()
	w.size += len(value)
	if w.tooLarge || w.body.Len()+len(value) > maxResponseBytes {
		w.tooLarge = true
		return len(value), nil
	}
	return w.body.Write(value)
}
func (w *responseBuffer) WriteString(value string) (int, error) { return w.Write([]byte(value)) }
func (w *responseBuffer) Status() int                           { return w.status }
func (w *responseBuffer) Size() int                             { return w.size }
func (w *responseBuffer) Written() bool                         { return w.size >= 0 }
func (w *responseBuffer) Flush()                                { w.WriteHeaderNow() }

type selection struct{ fields, expand []string }

func parseSelection(rawQuery string) (selection, error) {
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return selection{}, errors.New("Invalid response field query")
	}
	result := selection{fields: FieldNames()}
	if values.Has("fields") {
		result.fields, err = names(values["fields"], issueFields, "fields")
		if err != nil {
			return selection{}, err
		}
	}
	if values.Has("expand") {
		if len(values["expand"]) == 1 && values.Get("expand") == "none" {
			return result, nil
		}
		result.expand, err = names(values["expand"], ExpansionNames(), "expand")
		if err != nil {
			return selection{}, err
		}
	}
	return result, nil
}

func names(values, allowed []string, parameter string) ([]string, error) {
	if len(values) != 1 || len(values[0]) > 2048 || strings.TrimSpace(values[0]) == "" {
		return nil, errors.New(parameter + " must be one non-empty comma-separated value of at most 2048 bytes")
	}
	choices := map[string]bool{}
	for _, name := range allowed {
		choices[name] = true
	}
	names := strings.Split(values[0], ",")
	if len(names) > 64 {
		return nil, errors.New(parameter + " accepts at most 64 names")
	}
	result, seen := []string{}, map[string]bool{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if !choices[name] {
			return nil, errors.New("Unsupported " + parameter + " name: " + name)
		}
		if !seen[name] {
			result = append(result, name)
			seen[name] = true
		}
	}
	return result, nil
}

func (s selection) response(body []byte) ([]byte, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil || envelope["data"] == nil {
		return nil, errors.New("invalid JSON envelope")
	}
	var data []byte
	var err error
	if envelope["group_by"] != nil {
		data, err = s.groups(envelope["data"], 0)
	} else if bytes.HasPrefix(bytes.TrimSpace(envelope["data"]), []byte("[")) {
		data, err = s.items(envelope["data"])
	} else {
		data, err = s.item(envelope["data"])
	}
	if err != nil {
		return nil, err
	}
	result := keep(envelope, []string{"pagination", "group_by", "sub_group_by", "total_items"})
	result["data"] = data
	return json.Marshal(result)
}

func (s selection) groups(raw json.RawMessage, depth int) ([]byte, error) {
	if depth > 3 {
		return nil, errors.New("unexpected group depth")
	}
	var groups []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &groups); err != nil {
		return nil, err
	}
	result := make([]map[string]json.RawMessage, 0, len(groups))
	for _, group := range groups {
		projected := keep(group, []string{"key", "label", "total", "pagination"})
		if value := group["items"]; value != nil {
			items, err := s.items(value)
			if err != nil {
				return nil, err
			}
			projected["items"] = items
		}
		if value := group["groups"]; value != nil {
			children, err := s.groups(value, depth+1)
			if err != nil {
				return nil, err
			}
			projected["groups"] = children
		}
		result = append(result, projected)
	}
	return json.Marshal(result)
}

func (s selection) items(raw json.RawMessage) ([]byte, error) {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, err
	}
	result := make([]json.RawMessage, 0, len(items))
	for _, item := range items {
		projected, err := s.item(item)
		if err != nil {
			return nil, err
		}
		result = append(result, projected)
	}
	return json.Marshal(result)
}

func (s selection) item(raw json.RawMessage) ([]byte, error) {
	var item map[string]json.RawMessage
	if err := json.Unmarshal(raw, &item); err != nil || item["id"] == nil {
		return nil, errors.New("invalid work item")
	}
	result := keep(item, s.fields)
	result["id"] = item["id"]
	for _, name := range s.expand {
		definition := expansions[name]
		value := item[definition.property]
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) || value == nil {
			result[definition.property] = json.RawMessage("null")
			if definition.array {
				result[definition.property] = json.RawMessage("[]")
			}
			continue
		}
		if definition.array {
			var entries []map[string]json.RawMessage
			if err := json.Unmarshal(value, &entries); err != nil {
				return nil, err
			}
			selected := make([]map[string]json.RawMessage, 0, len(entries))
			for _, entry := range entries {
				selected = append(selected, keep(entry, definition.fields))
			}
			result[definition.property], _ = json.Marshal(selected)
		} else {
			var entry map[string]json.RawMessage
			if err := json.Unmarshal(value, &entry); err != nil {
				return nil, err
			}
			result[definition.property], _ = json.Marshal(keep(entry, definition.fields))
		}
	}
	return json.Marshal(result)
}

func keep(source map[string]json.RawMessage, fields []string) map[string]json.RawMessage {
	result := make(map[string]json.RawMessage, len(fields))
	for _, field := range fields {
		if value, ok := source[field]; ok {
			result[field] = value
		}
	}
	return result
}
