package support

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/microcosm-cc/bluemonday"

	"my-jira/apps/api/internal/platform"
	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/httpapi"
	"my-jira/apps/api/internal/platform/identity"
	"my-jira/apps/api/internal/support/data"
)

type service struct{ deps platform.Dependencies }

func Register(r *gin.RouterGroup, deps platform.Dependencies) {
	s := &service{deps: deps}
	w := r.Group("/workspaces/:workspaceID")
	w.GET("/search", s.search)
	w.GET("/analytics", s.analytics)
	w.POST("/analytics/export", s.exportAnalysis)
	w.GET("/analyses", s.savedAnalyses)
	w.POST("/analyses", s.mutateAnalysis)
	w.GET("/analyses/:analysisID", s.savedAnalysis)
	w.PATCH("/analyses/:analysisID", s.mutateAnalysis)
	w.DELETE("/analyses/:analysisID", s.mutateAnalysis)
	w.GET("/analyses/:analysisID/run", s.savedAnalysis)
	w.GET("/profiles/:userID", s.profile)
	w.GET("/profiles/:userID/stats", s.profileStats)
	w.GET("/profiles/:userID/activities", s.profileActivities)
	w.GET("/profiles/:userID/activities/export", s.exportProfileActivities)
	w.GET("/notifications", s.notifications)
	w.GET("/notifications/unread-count", s.unread)
	w.POST("/notifications/mark-all-read", s.markAllRead)
	w.PATCH("/notifications/:notificationID", s.updateNotification)
	w.DELETE("/notifications/:notificationID", s.deleteNotification)
	w.GET("/favorites", s.favorites)
	w.POST("/favorites", s.addFavorite)
	w.PATCH("/favorites/:favoriteID", s.updateFavorite)
	w.DELETE("/favorites/:favoriteID", s.deleteFavorite)
	w.GET("/recent-visits", s.recents)
	w.POST("/recent-visits", s.visit)
	w.GET("/preferences", s.preferences)
	w.GET("/preferences/:key", s.preference)
	w.PATCH("/preferences/:key", s.savePreference)
	w.PUT("/preferences/:key", s.savePreference)
	w.GET("/stickies", s.stickies)
	w.POST("/stickies", s.createSticky)
	w.GET("/stickies/:stickyID", s.sticky)
	w.PATCH("/stickies/:stickyID", s.updateSticky)
	w.DELETE("/stickies/:stickyID", s.deleteSticky)
}

func (s *service) scope(c *gin.Context) (identity.Scope, error) {
	actor, err := httpapi.Actor(c)
	if err != nil {
		return identity.Scope{}, err
	}
	wid, err := httpapi.UUIDParam(c, "workspaceID")
	if err != nil {
		return identity.Scope{}, err
	}
	return s.deps.Policy.Workspace(c.Request.Context(), actor, wid, 5)
}

// visibleProject is safe to interpolate only with a source-code-controlled alias.
// $1 is workspace, $2 is the current user in every use.
func visibleProject(alias string) string {
	return "EXISTS(SELECT 1 FROM projects vp JOIN workspace_members wm ON wm.workspace_id=vp.workspace_id AND wm.user_id=$2 AND wm.is_active AND wm.deleted_at IS NULL LEFT JOIN project_members pm ON pm.project_id=vp.id AND pm.user_id=$2 AND pm.is_active AND pm.deleted_at IS NULL WHERE vp.id=" + alias + ".project_id AND vp.workspace_id=$1 AND vp.deleted_at IS NULL AND (vp.network='public' OR pm.id IS NOT NULL))"
}
func visibleIssue(alias string) string {
	return "EXISTS(SELECT 1 FROM projects vp JOIN workspace_members wm ON wm.workspace_id=vp.workspace_id AND wm.user_id=$2 AND wm.is_active AND wm.deleted_at IS NULL LEFT JOIN project_members pm ON pm.project_id=vp.id AND pm.user_id=$2 AND pm.is_active AND pm.deleted_at IS NULL WHERE vp.id=" + alias + ".project_id AND vp.workspace_id=$1 AND vp.deleted_at IS NULL AND vp.archived_at IS NULL AND (vp.network='public' OR pm.id IS NOT NULL) AND (CASE WHEN wm.role IN(5,20) THEN wm.role ELSE COALESCE(pm.role,wm.role) END>=15 OR vp.guest_can_view_all OR " + alias + ".created_by=$2))"
}

// Notifications retain content, so reading them must recheck the entity's
// current access after membership, project, or private-page changes.
func visibleNotification() string {
	return "COALESCE(n.data->>'silent','false')<>'true' AND (n.project_id IS NULL OR " + visibleProject("n") + ") AND CASE " +
		"WHEN n.entity_type IN('issue','work_item') THEN EXISTS(SELECT 1 FROM work_items ni WHERE ni.id=n.entity_id AND ni.workspace_id=$1 AND ni.deleted_at IS NULL AND " + visibleIssue("ni") + ") " +
		"WHEN n.entity_type='page' THEN EXISTS(SELECT 1 FROM pages np WHERE np.id=n.entity_id AND " + data.VisiblePage("np", "$1", "$2") + ") " +
		"WHEN n.entity_type='view' THEN EXISTS(SELECT 1 FROM saved_views nv WHERE nv.id=n.entity_id AND nv.workspace_id=$1 AND nv.deleted_at IS NULL AND (NOT nv.is_private OR nv.owner_id=$2) AND (nv.project_id IS NULL OR " + visibleProject("nv") + ")) " +
		"WHEN n.entity_type='cycle' THEN EXISTS(SELECT 1 FROM cycles nc WHERE nc.id=n.entity_id AND nc.workspace_id=$1 AND nc.deleted_at IS NULL AND " + visibleProject("nc") + ") " +
		"WHEN n.entity_type='module' THEN EXISTS(SELECT 1 FROM modules nm WHERE nm.id=n.entity_id AND nm.workspace_id=$1 AND nm.deleted_at IS NULL AND " + visibleProject("nm") + ") " +
		"ELSE true END"
}

func (s *service) notifications(c *gin.Context) {
	scope, err := s.scope(c)
	if err != nil {
		data.Fail(c, err)
		return
	}
	where, args, err := notificationWhere(c, scope)
	if err != nil {
		data.Fail(c, err)
		return
	}
	limit, offset := 100, 0
	for key, target := range map[string]*int{"limit": &limit, "offset": &offset} {
		if text := c.Query(key); text != "" {
			value, err := strconv.Atoi(text)
			if err != nil || value < 0 || value > 1000000 || (key == "limit" && (value < 1 || value > 500)) {
				data.Fail(c, data.Invalid("Invalid notification pagination"))
				return
			}
			*target = value
		}
	}
	var total int
	if err = s.deps.DB.SQL.QueryRowContext(c.Request.Context(), "SELECT count(*) FROM notifications n WHERE "+where, args...).Scan(&total); err != nil {
		data.Fail(c, err)
		return
	}
	result, err := data.Many(c, s.deps.DB.SQL, "SELECT to_jsonb(n)-'deleted_at' FROM notifications n WHERE "+where+fmt.Sprintf(" ORDER BY n.created_at DESC,n.id LIMIT %d OFFSET %d", limit, offset), args...)
	if err != nil {
		data.Fail(c, err)
		return
	}
	var next any
	if offset+len(result) < total {
		next = offset + len(result)
	}
	c.JSON(200, gin.H{"data": result, "pagination": gin.H{"total": total, "limit": limit, "offset": offset, "next_offset": next, "has_more": next != nil}})
}

func notificationWhere(c *gin.Context, scope identity.Scope) (string, []any, error) {
	where := "n.workspace_id=$1 AND n.user_id=$2 AND n.deleted_at IS NULL AND " + visibleNotification()
	args := []any{scope.WorkspaceID, scope.Actor.UserID}
	for _, key := range []string{"archived", "unread", "snoozed", "read"} {
		if value := c.Query(key); value != "" && value != "true" && value != "false" {
			return "", nil, data.Invalid(key + " must be a boolean")
		}
	}
	if c.Query("archived") == "true" {
		where += " AND n.archived_at IS NOT NULL"
	} else {
		where += " AND n.archived_at IS NULL"
	}
	if c.Query("unread") == "true" || c.Query("read") == "false" {
		if c.Query("read") == "true" {
			return "", nil, data.Invalid("Conflicting read filters")
		}
		where += " AND n.read_at IS NULL"
	} else if c.Query("read") == "true" {
		where += " AND n.read_at IS NOT NULL"
	}
	if c.Query("snoozed") == "true" {
		where += " AND n.snoozed_until>now()"
	} else {
		where += " AND (n.snoozed_until IS NULL OR n.snoozed_until<=now())"
	}
	if value := c.Query("reason"); value != "" {
		parts := []string{}
		for _, reason := range strings.Split(value, ",") {
			if reason != "mentions" && reason != "assigned" && reason != "created" && reason != "subscribed" {
				return "", nil, data.Invalid("Invalid notification reason")
			}
			args = append(args, reason)
			parts = append(parts, fmt.Sprintf("COALESCE(n.data->'reasons','[]'::jsonb) ? $%d", len(args)))
		}
		where += " AND (" + strings.Join(parts, " OR ") + ")"
	}
	if value := c.Query("project_id"); value != "" {
		id, err := uuid.Parse(value)
		if err != nil || id == uuid.Nil {
			return "", nil, data.Invalid("Invalid project_id")
		}
		args = append(args, id)
		where += fmt.Sprintf(" AND n.project_id=$%d", len(args))
	}
	return where, args, nil
}
func (s *service) unread(c *gin.Context) {
	scope, err := s.scope(c)
	if err != nil {
		data.Fail(c, err)
		return
	}
	var count int
	err = s.deps.DB.SQL.QueryRowContext(c.Request.Context(), "SELECT count(*) FROM notifications n WHERE n.workspace_id=$1 AND n.user_id=$2 AND n.deleted_at IS NULL AND n.read_at IS NULL AND n.archived_at IS NULL AND (n.snoozed_until IS NULL OR n.snoozed_until<=now()) AND "+visibleNotification(), scope.WorkspaceID, scope.Actor.UserID).Scan(&count)
	if err != nil {
		data.Fail(c, err)
		return
	}
	httpapi.JSON(c, 200, gin.H{"count": count})
}
func (s *service) markAllRead(c *gin.Context) {
	scope, err := s.scope(c)
	if err != nil {
		data.Fail(c, err)
		return
	}
	where, args, err := notificationWhere(c, scope)
	if err != nil {
		data.Fail(c, err)
		return
	}
	result, err := s.deps.DB.SQL.ExecContext(c.Request.Context(), "UPDATE notifications n SET read_at=now(),updated_at=now() WHERE n.read_at IS NULL AND "+where, args...)
	if err != nil {
		data.Fail(c, err)
		return
	}
	count, _ := result.RowsAffected()
	httpapi.JSON(c, 200, gin.H{"updated": count})
}
func (s *service) deleteNotification(c *gin.Context) { s.mutateNotification(c, true) }
func (s *service) updateNotification(c *gin.Context) { s.mutateNotification(c, false) }
func (s *service) mutateNotification(c *gin.Context, remove bool) {
	scope, err := s.scope(c)
	if err != nil {
		data.Fail(c, err)
		return
	}
	id, err := httpapi.UUIDParam(c, "notificationID")
	if err != nil {
		data.Fail(c, err)
		return
	}
	input := data.Object{}
	if !remove {
		input, err = data.Bind(c, "read", "archived", "snoozed_until")
		if err != nil {
			data.Fail(c, err)
			return
		}
	}
	args := []any{scope.WorkspaceID, scope.Actor.UserID, id}
	sets := []string{"updated_at=now()"}
	for key := range input {
		var value any
		column := key
		switch key {
		case "read", "archived":
			flag, err := input.Bool(key)
			if err != nil {
				data.Fail(c, err)
				return
			}
			if flag {
				value = time.Now().UTC()
			}
			column = key + "_at"
		case "snoozed_until":
			if string(input[key]) != "null" {
				text, err := input.String(key, true, 100)
				if err != nil {
					data.Fail(c, err)
					return
				}
				timestamp, err := time.Parse(time.RFC3339, text)
				if err != nil {
					data.Fail(c, data.Invalid("snoozed_until must be an RFC3339 timestamp or null"))
					return
				}
				value = timestamp
			}
		}
		args = append(args, value)
		sets = append(sets, fmt.Sprintf("%s=$%d", column, len(args)))
	}
	if remove {
		sets = append(sets, "deleted_at=now()")
	}
	query := "UPDATE notifications n SET " + strings.Join(sets, ",") + " WHERE n.workspace_id=$1 AND n.user_id=$2 AND n.id=$3 AND n.deleted_at IS NULL AND " + visibleNotification() + " RETURNING to_jsonb(n)-'deleted_at'"
	result, err := data.One(c, s.deps.DB.SQL, query, args...)
	if err != nil {
		data.Fail(c, err)
		return
	}
	if remove {
		c.Status(204)
		return
	}
	httpapi.JSON(c, 200, result)
}

func denied(err error) bool {
	if errors.Is(err, sql.ErrNoRows) {
		return true
	}
	var a *httpapi.Error
	if errors.As(err, &a) {
		return a.Status == 403 || a.Status == 404
	}
	var b *data.Error
	return errors.As(err, &b) && (b.Status == 403 || b.Status == 404)
}

func (s *service) entity(c *gin.Context, scope identity.Scope, kind string, id uuid.UUID) (json.RawMessage, error) {
	if kind == "project" {
		if _, err := s.deps.Policy.Project(c.Request.Context(), scope.Actor, scope.WorkspaceID, id, 5); err != nil {
			return nil, err
		}
		return data.One(c, s.deps.DB.SQL, "SELECT jsonb_build_object('id',id,'name',name,'identifier',identifier,'icon',icon,'color',color) FROM projects WHERE id=$1 AND workspace_id=$2 AND deleted_at IS NULL", id, scope.WorkspaceID)
	}
	tables := map[string]string{"issue": "work_items", "cycle": "cycles", "module": "modules", "view": "saved_views", "page": "pages"}
	table, ok := tables[kind]
	if !ok {
		return nil, data.Invalid("Unsupported entity_type")
	}
	var pid uuid.NullUUID
	var raw []byte
	where := "id=$1 AND workspace_id=$2 AND deleted_at IS NULL"
	args := []any{id, scope.WorkspaceID}
	if kind == "page" {
		where += " AND " + data.VisiblePage("t", "$2", "$3")
		args = append(args, scope.Actor.UserID)
	}
	err := s.deps.DB.SQL.QueryRowContext(c.Request.Context(), "SELECT project_id,to_jsonb(t)-'deleted_at'-'content_binary'-'description_binary'-'progress_snapshot' FROM "+table+" t WHERE "+where, args...).Scan(&pid, &raw)
	if err != nil {
		return nil, err
	}
	if pid.Valid {
		var err error
		if kind == "page" {
			scope, err = data.PageProjectScope(c.Request.Context(), s.deps.DB.SQL, scope.Actor, scope.WorkspaceID, pid.UUID, identity.Guest)
		} else {
			scope, err = s.deps.Policy.Project(c.Request.Context(), scope.Actor, scope.WorkspaceID, pid.UUID, 5)
		}
		if err != nil {
			return nil, err
		}
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	if (kind == "page" || kind == "view") && value["is_private"] == true && value["owner_id"] != scope.Actor.UserID.String() {
		return nil, data.Missing()
	}
	if kind == "issue" && scope.Role < 15 && !scope.GuestCanViewAll && value["created_by"] != scope.Actor.UserID.String() {
		return nil, data.Missing()
	}
	return json.RawMessage(raw), nil
}

func (s *service) favorites(c *gin.Context) { s.entityList(c, "favorites") }
func (s *service) recents(c *gin.Context)   { s.entityList(c, "recent_visits") }
func (s *service) entityList(c *gin.Context, table string) {
	scope, err := s.scope(c)
	if err != nil {
		data.Fail(c, err)
		return
	}
	order := "position,id"
	if table == "recent_visits" {
		order = "visited_at DESC,id"
	}
	records, err := data.Many(c, s.deps.DB.SQL, "SELECT to_jsonb(t)-'deleted_at' FROM "+table+" t WHERE workspace_id=$1 AND user_id=$2 AND deleted_at IS NULL ORDER BY "+order+" LIMIT 500", scope.WorkspaceID, scope.Actor.UserID)
	if err != nil {
		data.Fail(c, err)
		return
	}
	result := make([]map[string]any, 0, len(records))
	for _, raw := range records {
		var value map[string]any
		if err := json.Unmarshal(raw, &value); err != nil {
			data.Fail(c, err)
			return
		}
		kind, _ := value["entity_type"].(string)
		idText, _ := value["entity_id"].(string)
		id, err := uuid.Parse(idText)
		if err != nil {
			data.Fail(c, err)
			return
		}
		entity, err := s.entity(c, scope, kind, id)
		if err != nil {
			if denied(err) {
				continue
			}
			data.Fail(c, err)
			return
		}
		value["entity"] = entity
		result = append(result, value)
	}
	httpapi.JSON(c, 200, result)
}

func (s *service) addFavorite(c *gin.Context) { s.saveEntity(c, "favorites") }
func (s *service) visit(c *gin.Context)       { s.saveEntity(c, "recent_visits") }
func (s *service) saveEntity(c *gin.Context, table string) {
	scope, err := s.scope(c)
	if err != nil {
		data.Fail(c, err)
		return
	}
	input, err := data.Bind(c, "entity_type", "entity_id", "position")
	if err != nil {
		data.Fail(c, err)
		return
	}
	kind, err := input.String("entity_type", true, 30)
	if err != nil {
		data.Fail(c, err)
		return
	}
	value, err := input.UUID("entity_id", false)
	if err != nil {
		data.Fail(c, err)
		return
	}
	id := value.(uuid.UUID)
	if _, err := s.entity(c, scope, kind, id); err != nil {
		data.Fail(c, err)
		return
	}
	position, err := input.Number("position")
	if err != nil {
		data.Fail(c, err)
		return
	}
	var result json.RawMessage
	err = s.deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		key := table + scope.WorkspaceID.String() + scope.Actor.UserID.String() + kind + id.String()
		if _, err := q.ExecContext(c.Request.Context(), "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", key); err != nil {
			return err
		}
		var rowID uuid.UUID
		err := q.QueryRowContext(c.Request.Context(), "SELECT id FROM "+table+" WHERE workspace_id=$1 AND user_id=$2 AND entity_type=$3 AND entity_id=$4 AND deleted_at IS NULL", scope.WorkspaceID, scope.Actor.UserID, kind, id).Scan(&rowID)
		if errors.Is(err, sql.ErrNoRows) {
			rowID = uuid.New()
			if table == "favorites" {
				_, err = q.ExecContext(c.Request.Context(), "INSERT INTO favorites(id,workspace_id,user_id,entity_type,entity_id,position) VALUES($1,$2,$3,$4,$5,$6)", rowID, scope.WorkspaceID, scope.Actor.UserID, kind, id, position)
			} else {
				_, err = q.ExecContext(c.Request.Context(), "INSERT INTO recent_visits(id,workspace_id,user_id,entity_type,entity_id,visited_at) VALUES($1,$2,$3,$4,$5,now())", rowID, scope.WorkspaceID, scope.Actor.UserID, kind, id)
			}
		} else if err == nil {
			if table == "favorites" {
				_, err = q.ExecContext(c.Request.Context(), "UPDATE favorites SET position=$2,updated_at=now() WHERE id=$1", rowID, position)
			} else {
				_, err = q.ExecContext(c.Request.Context(), "UPDATE recent_visits SET visited_at=now(),updated_at=now() WHERE id=$1", rowID)
			}
		}
		if err != nil {
			return err
		}
		result, err = data.One(c, q, "SELECT to_jsonb(t)-'deleted_at' FROM "+table+" t WHERE id=$1", rowID)
		return err
	})
	if err != nil {
		data.Fail(c, err)
		return
	}
	httpapi.JSON(c, 201, result)
}

func (s *service) updateFavorite(c *gin.Context) {
	scope, err := s.scope(c)
	if err != nil {
		data.Fail(c, err)
		return
	}
	id, err := httpapi.UUIDParam(c, "favoriteID")
	if err != nil {
		data.Fail(c, err)
		return
	}
	input, err := data.Bind(c, "position")
	if err != nil {
		data.Fail(c, err)
		return
	}
	position, err := input.Number("position")
	if err != nil {
		data.Fail(c, err)
		return
	}
	result, err := data.One(c, s.deps.DB.SQL, "UPDATE favorites SET position=$4,updated_at=now() WHERE id=$1 AND workspace_id=$2 AND user_id=$3 AND deleted_at IS NULL RETURNING to_jsonb(favorites)-'deleted_at'", id, scope.WorkspaceID, scope.Actor.UserID, position)
	data.Send(c, result, err)
}
func (s *service) deleteFavorite(c *gin.Context) {
	scope, err := s.scope(c)
	if err != nil {
		data.Fail(c, err)
		return
	}
	id, err := httpapi.UUIDParam(c, "favoriteID")
	if err != nil {
		data.Fail(c, err)
		return
	}
	_, err = data.One(c, s.deps.DB.SQL, "UPDATE favorites SET deleted_at=now(),updated_at=now() WHERE id=$1 AND workspace_id=$2 AND user_id=$3 AND deleted_at IS NULL RETURNING to_jsonb(favorites)", id, scope.WorkspaceID, scope.Actor.UserID)
	if err != nil {
		data.Fail(c, err)
		return
	}
	c.Status(204)
}

func (s *service) preferences(c *gin.Context) {
	scope, err := s.scope(c)
	if err != nil {
		data.Fail(c, err)
		return
	}
	result, err := data.Many(c, s.deps.DB.SQL, "SELECT to_jsonb(p)-'deleted_at' FROM preferences p WHERE workspace_id=$1 AND user_id=$2 AND project_id IS NULL AND deleted_at IS NULL ORDER BY scope", scope.WorkspaceID, scope.Actor.UserID)
	data.Send(c, result, err)
}
func (s *service) preference(c *gin.Context) {
	scope, err := s.scope(c)
	if err != nil {
		data.Fail(c, err)
		return
	}
	key := strings.TrimSpace(c.Param("key"))
	if key == "" || len(key) > 100 {
		data.Fail(c, data.Invalid("Invalid preference key"))
		return
	}
	result, err := data.One(c, s.deps.DB.SQL, "SELECT to_jsonb(p)-'deleted_at' FROM preferences p WHERE workspace_id=$1 AND user_id=$2 AND project_id IS NULL AND scope=$3 AND deleted_at IS NULL", scope.WorkspaceID, scope.Actor.UserID, key)
	if errors.Is(err, sql.ErrNoRows) {
		httpapi.JSON(c, 200, gin.H{"scope": key, "value": gin.H{}})
		return
	}
	data.Send(c, result, err)
}
func (s *service) savePreference(c *gin.Context) {
	scope, err := s.scope(c)
	if err != nil {
		data.Fail(c, err)
		return
	}
	key := strings.TrimSpace(c.Param("key"))
	if key == "" || len(key) > 100 {
		data.Fail(c, data.Invalid("Invalid preference key"))
		return
	}
	input, err := data.Bind(c, "value")
	if err != nil {
		data.Fail(c, err)
		return
	}
	value, err := input.JSON("value")
	if err != nil {
		data.Fail(c, err)
		return
	}
	if key == "notifications" {
		var settings map[string]json.RawMessage
		if err := json.Unmarshal(input["value"], &settings); err != nil {
			data.Fail(c, data.Invalid("Invalid notification preferences"))
			return
		}
		allowed := map[string]bool{"in_app": true, "email": true, "mentions": true, "assigned": true, "subscribed": true, "created": true}
		for field, raw := range settings {
			var flag bool
			if !allowed[field] || string(raw) == "null" || json.Unmarshal(raw, &flag) != nil {
				data.Fail(c, data.Invalid("Notification preferences accept only boolean in_app, email, mentions, assigned, subscribed, and created"))
				return
			}
		}
	}
	var result json.RawMessage
	err = s.deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if _, err := q.ExecContext(c.Request.Context(), "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", "preference"+scope.WorkspaceID.String()+scope.Actor.UserID.String()+key); err != nil {
			return err
		}
		var id uuid.UUID
		err := q.QueryRowContext(c.Request.Context(), "SELECT id FROM preferences WHERE workspace_id=$1 AND user_id=$2 AND project_id IS NULL AND scope=$3 AND deleted_at IS NULL", scope.WorkspaceID, scope.Actor.UserID, key).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			id = uuid.New()
			_, err = q.ExecContext(c.Request.Context(), "INSERT INTO preferences(id,workspace_id,user_id,scope,value) VALUES($1,$2,$3,$4,$5::jsonb)", id, scope.WorkspaceID, scope.Actor.UserID, key, value)
		} else if err == nil {
			if c.Request.Method == "PATCH" {
				_, err = q.ExecContext(c.Request.Context(), "UPDATE preferences SET value=value || $2::jsonb,updated_at=now() WHERE id=$1", id, value)
			} else {
				_, err = q.ExecContext(c.Request.Context(), "UPDATE preferences SET value=$2::jsonb,updated_at=now() WHERE id=$1", id, value)
			}
		}
		if err != nil {
			return err
		}
		result, err = data.One(c, q, "SELECT to_jsonb(p)-'deleted_at' FROM preferences p WHERE id=$1", id)
		return err
	})
	data.Send(c, result, err)
}

var htmlPolicy = bluemonday.UGCPolicy()

func (s *service) stickies(c *gin.Context) {
	scope, err := s.scope(c)
	if err != nil {
		data.Fail(c, err)
		return
	}
	result, err := data.Many(c, s.deps.DB.SQL, "SELECT to_jsonb(t)-'deleted_at' FROM stickies t WHERE workspace_id=$1 AND user_id=$2 AND deleted_at IS NULL AND is_archived=$3 ORDER BY position,updated_at DESC,id", scope.WorkspaceID, scope.Actor.UserID, c.Query("archived") == "true")
	data.Send(c, result, err)
}
func (s *service) sticky(c *gin.Context) {
	scope, err := s.scope(c)
	if err != nil {
		data.Fail(c, err)
		return
	}
	id, err := httpapi.UUIDParam(c, "stickyID")
	if err != nil {
		data.Fail(c, err)
		return
	}
	result, err := data.One(c, s.deps.DB.SQL, "SELECT to_jsonb(t)-'deleted_at' FROM stickies t WHERE id=$1 AND workspace_id=$2 AND user_id=$3 AND deleted_at IS NULL", id, scope.WorkspaceID, scope.Actor.UserID)
	data.Send(c, result, err)
}
func (s *service) createSticky(c *gin.Context) { s.mutateSticky(c, true, false) }
func (s *service) updateSticky(c *gin.Context) { s.mutateSticky(c, false, false) }
func (s *service) deleteSticky(c *gin.Context) { s.mutateSticky(c, false, true) }
func (s *service) mutateSticky(c *gin.Context, creating, remove bool) {
	scope, err := s.scope(c)
	if err != nil {
		data.Fail(c, err)
		return
	}
	id := uuid.New()
	if !creating {
		id, err = httpapi.UUIDParam(c, "stickyID")
		if err != nil {
			data.Fail(c, err)
			return
		}
	}
	input := data.Object{}
	if !remove {
		input, err = data.Bind(c, "title", "content_html", "content_json", "color", "position", "is_archived")
		if err != nil {
			data.Fail(c, err)
			return
		}
	}
	columns := []string{}
	args := []any{id, scope.WorkspaceID, scope.Actor.UserID}
	sets := []string{"updated_at=now()"}
	for key := range input {
		var value any
		var err error
		switch key {
		case "title":
			value, err = input.String(key, false, 255)
		case "content_html":
			var text string
			text, err = input.String(key, false, 100000)
			value = htmlPolicy.Sanitize(text)
		case "content_json":
			value, err = input.JSON(key)
		case "color":
			value, err = input.String(key, true, 32)
		case "position":
			value, err = input.Number(key)
		case "is_archived":
			value, err = input.Bool(key)
		}
		if err != nil {
			data.Fail(c, err)
			return
		}
		columns = append(columns, key)
		args = append(args, value)
		sets = append(sets, fmt.Sprintf("%s=$%d", key, len(args)))
	}
	query := ""
	if creating {
		all := append([]string{"id", "workspace_id", "user_id"}, columns...)
		query = "INSERT INTO stickies(" + strings.Join(all, ",") + ") VALUES(" + data.Markers(1, len(args)) + ") RETURNING to_jsonb(stickies)-'deleted_at'"
	} else {
		if remove {
			sets = append(sets, "deleted_at=now()")
		}
		query = "UPDATE stickies SET " + strings.Join(sets, ",") + " WHERE id=$1 AND workspace_id=$2 AND user_id=$3 AND deleted_at IS NULL RETURNING to_jsonb(stickies)-'deleted_at'"
	}
	result, err := data.One(c, s.deps.DB.SQL, query, args...)
	if err != nil {
		data.Fail(c, err)
		return
	}
	if remove {
		c.Status(204)
		return
	}
	status := 200
	if creating {
		status = 201
	}
	httpapi.JSON(c, status, result)
}
