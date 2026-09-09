package workitems

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"my-jira/apps/api/internal/platform/httpapi"
	"my-jira/apps/api/internal/platform/identity"
)

var workItemIdentifier = regexp.MustCompile(`^([A-Z][A-Z0-9]{0,11})-([1-9][0-9]{0,18})$`)

func (h *handler) lookup(c *gin.Context) {
	actor, err := httpapi.Actor(c)
	if err != nil {
		respondError(c, err)
		return
	}
	wid, err := httpapi.UUIDParam(c, "workspaceID")
	if err != nil {
		respondError(c, err)
		return
	}
	if _, err = h.deps.Policy.Workspace(c.Request.Context(), actor, wid, identity.Guest); err != nil {
		respondError(c, err)
		return
	}
	parts := workItemIdentifier.FindStringSubmatch(strings.ToUpper(strings.TrimSpace(c.Param("identifier"))))
	if parts == nil {
		respondError(c, invalid("Use a work-item identifier such as APP-123"))
		return
	}
	sequence, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		respondError(c, invalid("Invalid work-item sequence"))
		return
	}
	projectState := c.DefaultQuery("project_archived", "false")
	if projectState != "false" && projectState != "true" && projectState != "all" {
		respondError(c, invalid("project_archived must be true, false, or all"))
		return
	}
	projectFilter := ""
	if projectState == "false" {
		projectFilter = " AND p.archived_at IS NULL"
	} else if projectState == "true" {
		projectFilter = " AND p.archived_at IS NOT NULL"
	}
	where := `w.workspace_id=$1 AND w.sequence_id=$4 AND w.deleted_at IS NULL AND EXISTS(SELECT 1 FROM projects p JOIN workspace_members wm ON wm.workspace_id=p.workspace_id AND wm.user_id=$2 AND wm.is_active AND wm.deleted_at IS NULL LEFT JOIN project_members pm ON pm.project_id=p.id AND pm.user_id=$2 AND pm.is_active AND pm.deleted_at IS NULL WHERE p.id=w.project_id AND p.workspace_id=$1 AND p.identifier=$3 AND p.deleted_at IS NULL AND (p.network='public' OR pm.id IS NOT NULL) AND (CASE WHEN wm.role IN(5,20) THEN wm.role ELSE COALESCE(pm.role,wm.role) END>=15 OR p.guest_can_view_all OR w.created_by=$2)` + projectFilter + `)`
	where, args, err := CompileQuery(c, where, []any{wid, actor.UserID, parts[1], sequence})
	if err != nil {
		respondError(c, err)
		return
	}
	var result json.RawMessage
	err = h.deps.DB.SQL.QueryRowContext(c.Request.Context(), issueSelectFor("$2")+" WHERE "+where, args...).Scan(&result)
	if err != nil {
		respondError(c, err)
		return
	}
	httpapi.JSON(c, 200, result)
}
