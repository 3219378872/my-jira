package workitems

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"my-jira/apps/api/internal/platform"
	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/httpapi"
	"my-jira/apps/api/internal/platform/identity"
	"my-jira/apps/api/internal/support/data"
)

type handler struct{ deps platform.Dependencies }

type problem struct {
	status        int
	code, message string
}

func (e problem) Error() string     { return e.message }
func invalid(message string) error  { return problem{400, "validation_failed", message} }
func absent() error                 { return problem{404, "not_found", "Work item was not found"} }
func conflict(message string) error { return problem{409, "conflict", message} }
func respondError(c *gin.Context, err error) {
	var p problem
	if errors.As(err, &p) {
		c.JSON(p.status, gin.H{"error": gin.H{"code": p.code, "message": p.message}})
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		respondError(c, absent())
		return
	}
	httpapi.Fail(c, err)
}

func Register(r *gin.RouterGroup, deps platform.Dependencies) {
	h := &handler{deps: deps}
	p := r.Group("/workspaces/:workspaceID/projects/:projectID/issues")
	p.GET("", h.list)
	p.POST("", h.create)
	p.PATCH("/bulk", h.bulk)
	p.DELETE("/bulk", h.bulk)
	p.GET("/:issueID", h.detail)
	p.PATCH("/:issueID", h.update)
	p.DELETE("/:issueID", h.remove)
	p.POST("/:issueID/duplicate", h.duplicate)
	p.POST("/:issueID/move", h.move)
	p.GET("/:issueID/activities", h.activities)
	p.GET("/:issueID/versions", h.versions)
	p.GET("/:issueID/comments", h.comments)
	p.POST("/:issueID/comments", h.addComment)
	p.PATCH("/:issueID/comments/:commentID", h.updateComment)
	p.DELETE("/:issueID/comments/:commentID", h.deleteComment)
	p.GET("/:issueID/links", h.links)
	p.POST("/:issueID/links", h.addLink)
	p.DELETE("/:issueID/links/:linkID", h.deleteLink)
	p.GET("/:issueID/relations", h.relations)
	p.POST("/:issueID/relations", h.addRelation)
	p.DELETE("/:issueID/relations/:relationID", h.deleteRelation)
	p.GET("/:issueID/subscribers", h.subscribers)
	p.POST("/:issueID/subscribers", h.subscribe)
	p.DELETE("/:issueID/subscribers", h.unsubscribe)
	p.DELETE("/:issueID/subscribers/:userID", h.unsubscribe)
	p.GET("/:issueID/reactions", h.reactions)
	p.POST("/:issueID/reactions", h.addReaction)
	p.DELETE("/:issueID/reactions/:reactionID", h.deleteReaction)
	r.GET("/workspaces/:workspaceID/issues", h.workspaceList)
	r.GET("/workspaces/:workspaceID/issues/lookup/:identifier", h.lookup)
	project := r.Group("/workspaces/:workspaceID/projects/:projectID")
	project.GET("/automation", h.automation)
	project.PATCH("/automation", h.configureAutomation)
	project.POST("/automation/run", h.runAutomation)
	project.GET("/intake", h.listIntake)
	project.POST("/intake", h.createIntake)
	project.GET("/intake/:intakeID", h.detailIntake)
	project.PATCH("/intake/:intakeID", h.editIntake)
	project.DELETE("/intake/:intakeID", h.deleteIntake)
	project.POST("/intake/:intakeID/resolve", h.resolveIntake)
	project.GET("/intake/:intakeID/versions", h.intakeVersions)
}

func (h *handler) scope(c *gin.Context, role identity.Role) (identity.Scope, error) {
	a, e := httpapi.Actor(c)
	if e != nil {
		return identity.Scope{}, e
	}
	w, e := uuid.Parse(c.Param("workspaceID"))
	if e != nil {
		return identity.Scope{}, invalid("Invalid workspace identifier")
	}
	p, e := uuid.Parse(c.Param("projectID"))
	if e != nil {
		return identity.Scope{}, invalid("Invalid project identifier")
	}
	return h.deps.Policy.Project(c.Request.Context(), a, w, p, role)
}

const issueSelect = `SELECT (to_jsonb(w)-'description_binary'-'deleted_at') || jsonb_build_object(
 'assignee_ids', COALESCE((SELECT jsonb_agg(a.user_id ORDER BY a.user_id) FROM work_item_assignees a WHERE a.work_item_id=w.id AND a.deleted_at IS NULL),'[]'::jsonb),
 'assignee_details',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',u.id,'display_name',u.display_name,'avatar_url',u.avatar_url)) FROM work_item_assignees a JOIN users u ON u.id=a.user_id WHERE a.work_item_id=w.id AND a.deleted_at IS NULL AND u.deleted_at IS NULL),'[]'::jsonb),
 'label_ids',COALESCE((SELECT jsonb_agg(l.label_id ORDER BY l.label_id) FROM work_item_labels l WHERE l.work_item_id=w.id AND l.deleted_at IS NULL),'[]'::jsonb),
 'label_details',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',l.id,'name',l.name,'color',l.color)) FROM work_item_labels a JOIN labels l ON l.id=a.label_id WHERE a.work_item_id=w.id AND a.deleted_at IS NULL AND l.deleted_at IS NULL),'[]'::jsonb),
 'cycle_id',(SELECT a.cycle_id FROM cycle_items a JOIN cycles cy ON cy.id=a.cycle_id WHERE a.work_item_id=w.id AND a.deleted_at IS NULL AND cy.deleted_at IS NULL LIMIT 1),
 'dependency_ids',COALESCE((SELECT jsonb_agg(d.id ORDER BY d.id) FROM work_item_relations r JOIN work_items d ON d.id=r.source_id AND d.workspace_id=w.workspace_id AND d.project_id=w.project_id WHERE r.target_id=w.id AND r.relation_type='blocks' AND r.deleted_at IS NULL AND d.deleted_at IS NULL),'[]'::jsonb),
 'module_ids',COALESCE((SELECT jsonb_agg(a.module_id) FROM module_items a JOIN modules m ON m.id=a.module_id WHERE a.work_item_id=w.id AND a.deleted_at IS NULL AND m.deleted_at IS NULL),'[]'::jsonb),
 'state_detail',(SELECT (to_jsonb(s)-'deleted_at') || jsonb_build_object('group',s.group_name) FROM states s WHERE s.id=w.state_id),
 'project_detail',(SELECT jsonb_build_object('id',p.id,'name',p.name,'identifier',p.identifier,'icon',p.icon,'color',p.color) FROM projects p WHERE p.id=w.project_id),
 'estimate_point_detail',(SELECT to_jsonb(ep)-'deleted_at' FROM estimate_points ep WHERE ep.id=w.estimate_point_id AND ep.deleted_at IS NULL),
 'sub_item_count',(SELECT count(*) FROM work_items ch WHERE ch.parent_id=w.id AND ch.deleted_at IS NULL),
 'comment_count',(SELECT count(*) FROM comments cm WHERE cm.work_item_id=w.id AND cm.deleted_at IS NULL),
 'attachment_count',(SELECT count(*) FROM file_assets f WHERE f.work_item_id=w.id AND f.deleted_at IS NULL AND f.upload_status='completed')
 ) FROM work_items w `

func issueSelectFor(actorParameter string) string {
	access := ` AND (ch.created_by=` + actorParameter + ` OR EXISTS(SELECT 1 FROM projects gp WHERE gp.id=ch.project_id AND gp.guest_can_view_all) OR EXISTS(SELECT 1 FROM workspace_members wm LEFT JOIN project_members pm ON pm.workspace_id=wm.workspace_id AND pm.project_id=ch.project_id AND pm.user_id=wm.user_id AND pm.is_active AND pm.deleted_at IS NULL WHERE wm.workspace_id=ch.workspace_id AND wm.user_id=` + actorParameter + ` AND wm.is_active AND wm.deleted_at IS NULL AND CASE WHEN wm.role IN(5,20) THEN wm.role ELSE COALESCE(pm.role,wm.role) END>=15))`
	query := strings.Replace(issueSelect, "ch.deleted_at IS NULL)", "ch.deleted_at IS NULL"+access+")", 1)
	dependencyAccess := strings.ReplaceAll(access, "ch.", "d.")
	return strings.Replace(query, "d.deleted_at IS NULL)", "d.deleted_at IS NULL"+dependencyAccess+")", 1)
}

func bindInput[T any](c *gin.Context, target *T) error {
	value, err := httpapi.Bind[T](c)
	if err == nil {
		*target = value
	}
	return err
}

func loadIssue(c *gin.Context, q database.DBTX, s identity.Scope, id uuid.UUID, lock bool) (map[string]any, error) {
	if lock {
		var createdBy uuid.UUID
		if e := q.QueryRowContext(c.Request.Context(), `SELECT created_by FROM work_items WHERE id=$1 AND workspace_id=$2 AND project_id=$3 AND deleted_at IS NULL FOR UPDATE`, id, s.WorkspaceID, s.ProjectID).Scan(&createdBy); e != nil {
			return nil, e
		}
		if s.Role < identity.Member && !s.GuestCanViewAll && createdBy != s.Actor.UserID {
			return nil, absent()
		}
	}
	var raw []byte
	if e := q.QueryRowContext(c.Request.Context(), issueSelectFor("$2")+` WHERE w.id=$1 AND w.workspace_id=$3 AND w.project_id=$4 AND w.deleted_at IS NULL`, id, s.Actor.UserID, s.WorkspaceID, s.ProjectID).Scan(&raw); e != nil {
		return nil, e
	}
	var out map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	e := decoder.Decode(&out)
	if e == nil && s.Role < identity.Member && !s.GuestCanViewAll && out["created_by"] != s.Actor.UserID.String() {
		return nil, absent()
	}
	return out, e
}

func recordVersion(item map[string]any) int64 {
	if number, ok := item["version"].(json.Number); ok {
		value, _ := number.Int64()
		return value
	}
	if number, ok := item["version"].(float64); ok {
		return int64(number)
	}
	return 0
}

func (h *handler) issueScope(c *gin.Context, role identity.Role) (identity.Scope, uuid.UUID, map[string]any, error) {
	s, e := h.scope(c, role)
	if e != nil {
		return s, uuid.Nil, nil, e
	}
	id, e := uuid.Parse(c.Param("issueID"))
	if e != nil {
		return s, id, nil, invalid("Invalid work item identifier")
	}
	x, e := loadIssue(c, h.deps.DB.SQL, s, id, false)
	return s, id, x, e
}

func (h *handler) detail(c *gin.Context) {
	_, _, item, e := h.issueScope(c, identity.Guest)
	if e != nil {
		respondError(c, e)
		return
	}
	httpapi.JSON(c, 200, item)
}

func (h *handler) list(c *gin.Context) {
	minimum := identity.Guest
	if c.Query("deleted") == "true" || c.Query("deleted") == "all" {
		minimum = identity.Member
	}
	s, e := h.scope(c, minimum)
	if e != nil {
		respondError(c, e)
		return
	}
	where := ` WHERE w.workspace_id=$1 AND w.project_id=$2`
	if c.Query("deleted") == "true" {
		where += " AND w.deleted_at IS NOT NULL"
	} else if c.Query("deleted") != "all" {
		where += " AND w.deleted_at IS NULL"
	}
	args := []any{s.WorkspaceID, s.ProjectID}
	if s.Role < identity.Member && !s.GuestCanViewAll {
		where += " AND w.created_by=$3"
		args = append(args, s.Actor.UserID)
	}
	h.respondList(c, where, args)
}

func (h *handler) workspaceList(c *gin.Context) {
	a, e := httpapi.Actor(c)
	if e != nil {
		respondError(c, e)
		return
	}
	wid, e := uuid.Parse(c.Param("workspaceID"))
	if e != nil {
		respondError(c, invalid("Invalid workspace identifier"))
		return
	}
	minimum := identity.Guest
	if c.Query("deleted") == "true" || c.Query("deleted") == "all" {
		minimum = identity.Member
	}
	if _, e = h.deps.Policy.Workspace(c.Request.Context(), a, wid, minimum); e != nil {
		respondError(c, e)
		return
	}
	const projectRole = `CASE WHEN wm.role IN(5,20) THEN wm.role ELSE COALESCE(pm.role,wm.role) END`
	projectAccess := `(` + projectRole + `>=15 OR p.guest_can_view_all OR w.created_by=$2)`
	if minimum >= identity.Member {
		// A workspace member can still be a guest in a particular project.
		// Lifecycle collections follow the same project role as project lists.
		projectAccess = projectRole + `>=15`
	}
	where := ` WHERE w.workspace_id=$1 AND w.deleted_at IS NULL AND EXISTS (SELECT 1 FROM projects p JOIN workspace_members wm ON wm.workspace_id=p.workspace_id AND wm.user_id=$2 AND wm.is_active AND wm.deleted_at IS NULL LEFT JOIN project_members pm ON pm.project_id=p.id AND pm.user_id=$2 AND pm.is_active AND pm.deleted_at IS NULL WHERE p.id=w.project_id AND p.deleted_at IS NULL AND (pm.id IS NOT NULL OR p.network='public') AND ` + projectAccess + `)`
	if c.Query("deleted") == "true" {
		where = strings.Replace(where, "w.deleted_at IS NULL", "w.deleted_at IS NOT NULL", 1)
	} else if c.Query("deleted") == "all" {
		where = strings.Replace(where, " AND w.deleted_at IS NULL", "", 1)
	}
	if c.Query("project_archived") != "all" {
		if c.Query("project_archived") == "true" {
			where += " AND EXISTS(SELECT 1 FROM projects ap WHERE ap.id=w.project_id AND ap.archived_at IS NOT NULL)"
		} else {
			where += " AND EXISTS(SELECT 1 FROM projects ap WHERE ap.id=w.project_id AND ap.archived_at IS NULL)"
		}
	}
	h.respondList(c, where, []any{wid, a.UserID})
}

func (h *handler) respondList(c *gin.Context, where string, args []any) {
	where, args, e := advancedQuery(c, where, args)
	if e != nil {
		respondError(c, e)
		return
	}
	limit := 100
	if v := c.Query("limit"); v != "" {
		limit, e = strconv.Atoi(v)
		if e != nil || limit < 1 || limit > 500 {
			respondError(c, invalid("Limit must be between 1 and 500"))
			return
		}
	}
	offset, e := pageOffset(c.Query("cursor"))
	if e != nil {
		respondError(c, e)
		return
	}
	ordering, e := issueOrdering(c)
	if e != nil {
		respondError(c, e)
		return
	}
	if c.Query("group_by") != "" {
		h.groupedList(c, where, args, limit, offset, ordering)
		return
	}
	if c.Query("sub_group_by") != "" {
		respondError(c, invalid("sub_group_by requires group_by"))
		return
	}
	var total int64
	if e = h.deps.DB.SQL.QueryRowContext(c.Request.Context(), "SELECT count(*) FROM work_items w "+where, args...).Scan(&total); e != nil {
		respondError(c, e)
		return
	}
	n := len(args)
	actor, e := httpapi.Actor(c)
	if e != nil {
		respondError(c, e)
		return
	}
	args = append(args, limit, offset, actor.UserID)
	rows, e := h.deps.DB.SQL.QueryContext(c.Request.Context(), issueSelectFor(fmt.Sprintf("$%d", n+3))+where+" ORDER BY "+ordering+fmt.Sprintf(" LIMIT $%d OFFSET $%d", n+1, n+2), args...)
	if e != nil {
		respondError(c, e)
		return
	}
	defer rows.Close()
	data := []json.RawMessage{}
	for rows.Next() {
		var raw []byte
		if e = rows.Scan(&raw); e != nil {
			respondError(c, e)
			return
		}
		data = append(data, json.RawMessage(raw))
	}
	if e = rows.Err(); e != nil {
		respondError(c, e)
		return
	}
	var next *string
	hasMore := int64(offset+len(data)) < total
	if hasMore {
		v := base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(offset + len(data))))
		next = &v
	}
	c.JSON(http.StatusOK, gin.H{"data": data, "pagination": gin.H{"total": total, "next_cursor": next, "has_more": hasMore}})
}

var htmlPolicy = data.EditorHTMLPolicy()

func cleanHTML(value string) string { return htmlPolicy.Sanitize(value) }
func validPriority(v string) bool {
	return v == "none" || v == "low" || v == "medium" || v == "high" || v == "urgent"
}
func bindMap(c *gin.Context) (map[string]json.RawMessage, error) {
	b, e := httpapi.Bind[map[string]json.RawMessage](c)
	if e != nil || b == nil {
		return nil, invalid("Expected a JSON object")
	}
	return b, nil
}
func stringField(raw json.RawMessage) (string, error) {
	var s string
	if string(raw) == "null" {
		return "", invalid("Expected a string")
	}
	if e := json.Unmarshal(raw, &s); e != nil {
		return "", invalid("Expected a string")
	}
	return s, nil
}
func uuidValue(raw json.RawMessage, nullable bool) (any, error) {
	if string(raw) == "null" && nullable {
		return nil, nil
	}
	s, e := stringField(raw)
	if e != nil {
		return nil, e
	}
	id, e := uuid.Parse(s)
	if e != nil || id == uuid.Nil {
		return nil, invalid("Invalid related identifier")
	}
	return id, nil
}
func uuidArray(raw json.RawMessage) ([]uuid.UUID, error) {
	var input []string
	if string(raw) == "null" {
		return nil, invalid("Expected an array of identifiers")
	}
	if e := json.Unmarshal(raw, &input); e != nil {
		return nil, invalid("Expected an array of identifiers")
	}
	if len(input) > 200 {
		return nil, invalid("Too many related identifiers")
	}
	out := []uuid.UUID{}
	seen := map[uuid.UUID]bool{}
	for _, s := range input {
		id, e := uuid.Parse(s)
		if e != nil || id == uuid.Nil {
			return nil, invalid("Invalid related identifier")
		}
		if !seen[id] {
			out = append(out, id)
			seen[id] = true
		}
	}
	return out, nil
}

func execJSON(c *gin.Context, q database.DBTX, query string, args ...any) ([]json.RawMessage, error) {
	rows, e := q.QueryContext(c.Request.Context(), query, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []json.RawMessage{}
	for rows.Next() {
		var b []byte
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
