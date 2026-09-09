package support

import (
	"encoding/json"
	"strings"

	"github.com/gin-gonic/gin"

	"my-jira/apps/api/internal/platform/httpapi"
	"my-jira/apps/api/internal/support/data"
)

func (s *service) search(c *gin.Context) {
	scope, err := s.scope(c)
	if err != nil {
		data.Fail(c, err)
		return
	}
	text := strings.TrimSpace(c.Query("q"))
	if len([]rune(text)) > 200 {
		data.Fail(c, data.Invalid("Search text must have at most 200 characters"))
		return
	}
	result := map[string][]json.RawMessage{"projects": {}, "issues": {}, "cycles": {}, "modules": {}, "pages": {}}
	if text == "" {
		httpapi.JSON(c, 200, result)
		return
	}
	args := []any{scope.WorkspaceID, scope.Actor.UserID, "%" + text + "%"}
	queries := map[string]string{
		"projects": "SELECT jsonb_build_object('id',p.id,'name',p.name,'identifier',p.identifier,'icon',p.icon,'color',p.color) FROM projects p WHERE p.workspace_id=$1 AND p.deleted_at IS NULL AND p.archived_at IS NULL AND (p.name ILIKE $3 OR p.identifier ILIKE $3) AND (p.network='public' OR EXISTS(SELECT 1 FROM project_members pm WHERE pm.project_id=p.id AND pm.user_id=$2 AND pm.is_active AND pm.deleted_at IS NULL)) ORDER BY p.name,p.id LIMIT 25",
		"issues":   "SELECT jsonb_build_object('id',wi.id,'name',wi.name,'project_id',wi.project_id,'sequence_id',wi.sequence_id,'priority',wi.priority,'state_id',wi.state_id,'identifier',p.identifier) FROM work_items wi JOIN projects p ON p.id=wi.project_id WHERE wi.workspace_id=$1 AND wi.deleted_at IS NULL AND wi.archived_at IS NULL AND NOT wi.is_draft AND (wi.name ILIKE $3 OR concat(p.identifier,'-',wi.sequence_id) ILIKE $3) AND " + visibleIssue("wi") + " ORDER BY wi.updated_at DESC,wi.id LIMIT 25",
		"cycles":   "SELECT jsonb_build_object('id',t.id,'name',t.name,'project_id',t.project_id,'start_date',t.start_date,'end_date',t.end_date) FROM cycles t WHERE t.workspace_id=$1 AND t.deleted_at IS NULL AND t.archived_at IS NULL AND t.name ILIKE $3 AND " + visibleProject("t") + " ORDER BY t.updated_at DESC,t.id LIMIT 25",
		"modules":  "SELECT jsonb_build_object('id',t.id,'name',t.name,'project_id',t.project_id,'status',t.status) FROM modules t WHERE t.workspace_id=$1 AND t.deleted_at IS NULL AND t.archived_at IS NULL AND t.name ILIKE $3 AND " + visibleProject("t") + " ORDER BY t.updated_at DESC,t.id LIMIT 25",
		"pages":    "SELECT jsonb_build_object('id',t.id,'name',t.name,'project_id',t.project_id,'icon',t.icon,'is_private',t.is_private) FROM pages t WHERE t.archived_at IS NULL AND t.name ILIKE $3 AND " + data.VisiblePage("t", "$1", "$2") + " ORDER BY t.updated_at DESC,t.id LIMIT 25",
	}
	for key, query := range queries {
		values, err := data.Many(c, s.deps.DB.SQL, query, args...)
		if err != nil {
			data.Fail(c, err)
			return
		}
		result[key] = values
	}
	httpapi.JSON(c, 200, result)
}
