package workitems

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"my-jira/apps/api/internal/platform/httpapi"
	"my-jira/apps/api/internal/platform/identity"
)

// CompileQuery applies the shared work-item filter grammar to a caller's
// already-authorized base query. The work_items table must use alias w.
func CompileQuery(c *gin.Context, where string, args []any) (string, []any, error) {
	query, values, err := advancedQuery(c, where, args)
	var issueError problem
	if errors.As(err, &issueError) {
		err = httpapi.NewError(issueError.status, issueError.code, issueError.message)
	}
	return query, values, err
}

// CompileFilters accepts the same filters as the list endpoint in a JSON body.
// Callers still own the base tenant, role and deleted-row scope.
func CompileFilters(ctx context.Context, actor identity.Actor, where string, args []any, filters map[string]any) (string, []any, error) {
	values := url.Values{}
	keys := make([]string, 0, len(filters))
	for key := range filters {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := filters[key]
		field, known := queryFields[key]
		known = known && key != "name" && key != "is_draft"
		if !known {
			for _, suffix := range []string{"_before", "_after"} {
				if strings.HasSuffix(key, suffix) {
					field, known = queryFields[strings.TrimSuffix(key, suffix)]
					known = known && field.kind == "date"
				}
			}
		}
		switch key {
		case "search", "archived", "draft", "deleted", "include_subitems", "scheduled", "intake_status":
			known = true
		case "filter":
			if text, ok := value.(string); ok {
				values.Set(key, text)
			} else {
				raw, err := json.Marshal(value)
				if err != nil {
					return "", nil, httpapi.NewError(400, "invalid_request", "Invalid filter JSON")
				}
				values.Set(key, string(raw))
			}
			continue
		}
		if !known {
			return "", nil, httpapi.NewError(400, "invalid_request", "Unknown filter: "+key)
		}
		scalar := func(item any) (string, error) {
			switch item := item.(type) {
			case nil:
				return "null", nil
			case string:
				return item, nil
			case bool, float64, json.Number, int:
				return fmt.Sprint(item), nil
			default:
				return "", httpapi.NewError(400, "invalid_request", "Invalid value for filter: "+key)
			}
		}
		var text string
		if items, ok := value.([]any); ok {
			if len(items) == 0 || len(items) > 200 || (field.kind == "" && key != "intake_status") {
				return "", nil, httpapi.NewError(400, "invalid_request", "Invalid filter values: "+key)
			}
			parts := make([]string, 0, len(items))
			for _, item := range items {
				part, err := scalar(item)
				if err != nil {
					return "", nil, err
				}
				parts = append(parts, part)
			}
			text = strings.Join(parts, ",")
		} else {
			var err error
			text, err = scalar(value)
			if err != nil {
				return "", nil, err
			}
		}
		values.Set(key, text)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "/?"+values.Encode(), nil)
	if err != nil {
		return "", nil, err
	}
	c := &gin.Context{Request: request}
	c.Set(httpapi.ActorKey, actor)
	return CompileQuery(c, where, args)
}
