package foundation

import (
	"database/sql"
	"math"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"my-jira/apps/api/internal/platform/apperror"
	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/httpapi"
	"my-jira/apps/api/internal/platform/identity"
	"my-jira/apps/api/internal/support/data"
)

func sqlNow() time.Time { return time.Now() }
func validGroup(group string) bool {
	return group == "backlog" || group == "unstarted" || group == "started" || group == "completed" || group == "cancelled"
}

const stateJSON = `(to_jsonb(s)-'group_name')||jsonb_build_object('group',s.group_name)`

func lockCatalogProject(c *gin.Context, q database.DBTX, scope identity.Scope) error {
	if _, err := q.ExecContext(c.Request.Context(), `SELECT id FROM workspaces WHERE id=$1 FOR UPDATE`, scope.WorkspaceID); err != nil {
		return err
	}
	if _, err := q.ExecContext(c.Request.Context(), `SELECT id FROM projects WHERE id=$1 FOR UPDATE`, scope.ProjectID); err != nil {
		return err
	}
	_, err := (&identity.SQLPolicy{DB: q}).Project(c.Request.Context(), scope.Actor, scope.WorkspaceID, scope.ProjectID, identity.Admin)
	return err
}

func (s *Server) states(c *gin.Context) {
	scope, err := s.scope(c, true, identity.Guest)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	value, err := queryList(c.Request.Context(), s.Deps.DB.SQL, `SELECT `+stateJSON+` FROM states s WHERE project_id=$1 AND workspace_id=$2 AND deleted_at IS NULL ORDER BY position,id`, scope.ProjectID, scope.WorkspaceID)
	reply(c, 200, value, err)
}
func (s *Server) createState(c *gin.Context) {
	scope, err := s.scope(c, true, identity.Admin)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	input, err := httpapi.Bind[struct {
		Name      string  `json:"name"`
		Color     string  `json:"color"`
		Group     string  `json:"group"`
		Position  float64 `json:"position"`
		IsDefault bool    `json:"is_default"`
	}](c)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Color == "" {
		input.Color = "#64748b"
	}
	if input.Group == "" {
		input.Group = "unstarted"
	}
	if !validName(input.Name) || !colorPattern.MatchString(input.Color) || !validGroup(input.Group) {
		httpapi.Fail(c, apperror.Invalid("Invalid state name, color, or group"))
		return
	}
	id := uuid.New()
	var value map[string]any
	err = s.Deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if err := lockCatalogProject(c, q, scope); err != nil {
			return err
		}
		if input.IsDefault {
			if _, err := q.ExecContext(c.Request.Context(), `UPDATE states SET is_default=false,updated_at=now() WHERE project_id=$1 AND deleted_at IS NULL`, scope.ProjectID); err != nil {
				return err
			}
		}
		if input.Position == 0 {
			if err := q.QueryRowContext(c.Request.Context(), `SELECT COALESCE(max(position),0)+65536 FROM states WHERE project_id=$1 AND deleted_at IS NULL`, scope.ProjectID).Scan(&input.Position); err != nil {
				return err
			}
		}
		if _, err := q.ExecContext(c.Request.Context(), `INSERT INTO states(id,workspace_id,project_id,name,color,group_name,position,is_default) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, id, scope.WorkspaceID, scope.ProjectID, input.Name, input.Color, input.Group, input.Position, input.IsDefault); err != nil {
			return err
		}
		var err error
		value, err = queryObject(c.Request.Context(), q, `SELECT `+stateJSON+` FROM states s WHERE id=$1`, id)
		return err
	})
	reply(c, 201, value, err)
}
func (s *Server) updateState(c *gin.Context) {
	scope, err := s.scope(c, true, identity.Admin)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	id, err := httpapi.UUIDParam(c, "stateID")
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	input, err := httpapi.Bind[map[string]any](c)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	values := map[string]any{}
	for key, value := range input {
		switch key {
		case "name", "color", "group":
			v, ok := value.(string)
			if !ok || (key == "name" && !validName(v)) || (key == "color" && !colorPattern.MatchString(v)) || (key == "group" && !validGroup(v)) {
				httpapi.Fail(c, apperror.Invalid("Invalid "+key))
				return
			}
			if key == "group" {
				key = "group_name"
			}
			values[key] = v
		case "position":
			v, ok := value.(float64)
			if !ok || math.IsNaN(v) || math.IsInf(v, 0) {
				httpapi.Fail(c, apperror.Invalid("Invalid position"))
				return
			}
			values[key] = v
		case "is_default":
			v, ok := value.(bool)
			if !ok {
				httpapi.Fail(c, apperror.Invalid("Invalid default state"))
				return
			}
			if !v {
				httpapi.Fail(c, apperror.Invalid("Choose a different default state instead"))
				return
			}
			values[key] = v
		default:
			httpapi.Fail(c, apperror.Invalid("Unsupported state field: "+key))
			return
		}
	}
	var value map[string]any
	err = s.Deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if err := lockCatalogProject(c, q, scope); err != nil {
			return err
		}
		var oldGroup string
		if err := q.QueryRowContext(c.Request.Context(), `SELECT group_name FROM states WHERE id=$1 AND workspace_id=$2 AND project_id=$3 AND deleted_at IS NULL FOR UPDATE`, id, scope.WorkspaceID, scope.ProjectID).Scan(&oldGroup); err != nil {
			return err
		}
		if values["is_default"] == true {
			if _, err := q.ExecContext(c.Request.Context(), `UPDATE states SET is_default=false,updated_at=now() WHERE project_id=$1 AND deleted_at IS NULL`, scope.ProjectID); err != nil {
				return err
			}
		}
		if err := patch(c.Request.Context(), q, "states", id, "AND project_id=$2 AND workspace_id=$3", []any{scope.ProjectID, scope.WorkspaceID}, values); err != nil {
			return err
		}
		if group, ok := values["group_name"].(string); ok && group != oldGroup {
			if err := s.propagateStateGroup(c, q, scope, id, oldGroup, group); err != nil {
				return err
			}
		}
		var err error
		value, err = queryObject(c.Request.Context(), q, `SELECT `+stateJSON+` FROM states s WHERE id=$1`, id)
		return err
	})
	reply(c, 200, value, err)
}

func (s *Server) propagateStateGroup(c *gin.Context, q database.DBTX, scope identity.Scope, stateID uuid.UUID, beforeGroup, afterGroup string) error {
	type changedItem struct {
		id        uuid.UUID
		completed sql.NullTime
	}
	rows, err := q.QueryContext(c.Request.Context(), `SELECT id,completed_at FROM work_items WHERE state_id=$1 AND workspace_id=$2 AND project_id=$3 AND deleted_at IS NULL ORDER BY id FOR UPDATE`, stateID, scope.WorkspaceID, scope.ProjectID)
	if err != nil {
		return err
	}
	items := []changedItem{}
	for rows.Next() {
		var item changedItem
		if err := rows.Scan(&item.id, &item.completed); err != nil {
			rows.Close()
			return err
		}
		items = append(items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	completed := func(value sql.NullTime) any {
		if value.Valid {
			return value.Time
		}
		return nil
	}
	for _, item := range items {
		var after sql.NullTime
		if err := q.QueryRowContext(c.Request.Context(), `UPDATE work_items SET completed_at=CASE WHEN $2='completed' THEN COALESCE(completed_at,now()) ELSE NULL END,version=version+1,updated_by=$3,updated_at=now() WHERE id=$1 RETURNING completed_at`, item.id, afterGroup, scope.Actor.UserID).Scan(&after); err != nil {
			return err
		}
		before := map[string]any{"state_id": stateID, "state_group": beforeGroup, "completed_at": completed(item.completed)}
		next := map[string]any{"state_id": stateID, "state_group": afterGroup, "completed_at": completed(after)}
		if err := data.RecordIssue(c.Request.Context(), s.Deps, q, scope, item.id, "state_group_changed", before, next, true); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) deleteState(c *gin.Context) {
	scope, err := s.scope(c, true, identity.Admin)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	id, err := httpapi.UUIDParam(c, "stateID")
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	err = s.Deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if err := lockCatalogProject(c, q, scope); err != nil {
			return err
		}
		var isDefault, inUse bool
		if err := q.QueryRowContext(c.Request.Context(), `SELECT is_default,EXISTS(SELECT 1 FROM work_items WHERE state_id=s.id AND deleted_at IS NULL) FROM states s WHERE s.id=$1 AND s.project_id=$2 AND s.workspace_id=$3 AND s.deleted_at IS NULL FOR UPDATE`, id, scope.ProjectID, scope.WorkspaceID).Scan(&isDefault, &inUse); err != nil {
			return err
		}
		if isDefault || inUse {
			return apperror.Conflict("Move work items and select another default before deleting this state")
		}
		return affected(q.ExecContext(c.Request.Context(), `UPDATE states SET deleted_at=now(),updated_at=now() WHERE id=$1`, id))
	})
	noContent(c, err)
}

func (s *Server) labels(c *gin.Context) {
	scope, err := s.scope(c, c.Param("projectID") != "", identity.Guest)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	query := `SELECT to_jsonb(l) FROM labels l WHERE l.workspace_id=$1 AND (l.project_id=$2::uuid OR l.project_id IS NULL) AND l.deleted_at IS NULL ORDER BY l.position,l.name,l.id`
	args := []any{scope.WorkspaceID, optionalProjectID(scope)}
	if scope.ProjectID == uuid.Nil {
		query = `SELECT to_jsonb(l) FROM labels l WHERE l.workspace_id=$1 AND l.deleted_at IS NULL AND (l.project_id IS NULL OR EXISTS(SELECT 1 FROM projects p WHERE p.id=l.project_id AND p.workspace_id=$1 AND p.deleted_at IS NULL AND p.archived_at IS NULL AND (p.network='public' OR EXISTS(SELECT 1 FROM project_members pm WHERE pm.project_id=p.id AND pm.workspace_id=$1 AND pm.user_id=$2 AND pm.is_active AND pm.deleted_at IS NULL)))) ORDER BY l.position,l.name,l.id`
		args = []any{scope.WorkspaceID, scope.Actor.UserID}
	}
	value, err := queryList(c.Request.Context(), s.Deps.DB.SQL, query, args...)
	reply(c, 200, value, err)
}

func optionalProjectID(scope identity.Scope) any {
	if scope.ProjectID == uuid.Nil {
		return nil
	}
	return scope.ProjectID
}

func lockLabelScope(c *gin.Context, q database.DBTX, scope identity.Scope) error {
	// One lock covers workspace and project hierarchies so concurrent parent
	// changes observe each other's committed ancestry before checking for cycles.
	if _, err := q.ExecContext(c.Request.Context(), `SELECT id FROM workspaces WHERE id=$1 FOR UPDATE`, scope.WorkspaceID); err != nil {
		return err
	}
	policy := &identity.SQLPolicy{DB: q}
	if scope.ProjectID == uuid.Nil {
		_, err := policy.Workspace(c.Request.Context(), scope.Actor, scope.WorkspaceID, identity.Member)
		return err
	}
	_, err := policy.Project(c.Request.Context(), scope.Actor, scope.WorkspaceID, scope.ProjectID, identity.Member)
	return err
}

type labelInput struct {
	Name        string     `json:"name"`
	Color       string     `json:"color"`
	Description string     `json:"description"`
	ParentID    *uuid.UUID `json:"parent_id"`
	Position    float64    `json:"position"`
}

func (input *labelInput) normalize() error {
	input.Name = strings.TrimSpace(input.Name)
	if input.Color == "" {
		input.Color = "#64748b"
	}
	if !validName(input.Name) || !colorPattern.MatchString(input.Color) || len(input.Description) > 10000 || math.IsNaN(input.Position) || math.IsInf(input.Position, 0) {
		return apperror.Invalid("Invalid label")
	}
	if input.Position == 0 {
		input.Position = 65536
	}
	return nil
}

func insertLabel(c *gin.Context, q database.DBTX, scope identity.Scope, input labelInput) (map[string]any, error) {
	if input.ParentID != nil {
		var exists bool
		if err := q.QueryRowContext(c.Request.Context(), `SELECT EXISTS(SELECT 1 FROM labels WHERE id=$1 AND project_id IS NOT DISTINCT FROM $2::uuid AND workspace_id=$3 AND deleted_at IS NULL)`, input.ParentID, optionalProjectID(scope), scope.WorkspaceID).Scan(&exists); err != nil {
			return nil, err
		}
		if !exists {
			return nil, apperror.Invalid("Parent label must belong to the same workspace or project label hierarchy")
		}
	}
	return queryObject(c.Request.Context(), q, `WITH inserted AS(INSERT INTO labels(workspace_id,project_id,name,color,description,parent_id,position) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING *)SELECT to_jsonb(inserted) FROM inserted`, scope.WorkspaceID, optionalProjectID(scope), input.Name, input.Color, input.Description, input.ParentID, input.Position)
}

func (s *Server) createLabel(c *gin.Context) {
	scope, err := s.scope(c, c.Param("projectID") != "", identity.Member)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	input, err := httpapi.Bind[labelInput](c)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	if err := input.normalize(); err != nil {
		httpapi.Fail(c, err)
		return
	}
	var value map[string]any
	err = s.Deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if err := lockLabelScope(c, q, scope); err != nil {
			return err
		}
		var err error
		value, err = insertLabel(c, q, scope, input)
		return err
	})
	reply(c, 201, value, err)
}

func (s *Server) bulkLabels(c *gin.Context) {
	scope, err := s.scope(c, c.Param("projectID") != "", identity.Member)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	input, err := httpapi.Bind[struct {
		Labels []labelInput `json:"labels"`
	}](c)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	if len(input.Labels) < 1 || len(input.Labels) > 100 {
		httpapi.Fail(c, apperror.Invalid("Create between one and 100 labels per batch"))
		return
	}
	for i := range input.Labels {
		if err := input.Labels[i].normalize(); err != nil {
			httpapi.Fail(c, err)
			return
		}
	}
	values := make([]map[string]any, 0, len(input.Labels))
	err = s.Deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if err := lockLabelScope(c, q, scope); err != nil {
			return err
		}
		for _, input := range input.Labels {
			value, err := insertLabel(c, q, scope, input)
			if err != nil {
				return err
			}
			values = append(values, value)
		}
		return nil
	})
	reply(c, 201, values, err)
}
func (s *Server) updateLabel(c *gin.Context) {
	scope, err := s.scope(c, c.Param("projectID") != "", identity.Member)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	id, err := httpapi.UUIDParam(c, "labelID")
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	input, err := httpapi.Bind[map[string]any](c)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	values := map[string]any{}
	for key, value := range input {
		switch key {
		case "name", "color", "description":
			v, ok := value.(string)
			if !ok || len(v) > 10000 || (key == "name" && !validName(v)) || (key == "color" && !colorPattern.MatchString(v)) {
				httpapi.Fail(c, apperror.Invalid("Invalid "+key))
				return
			}
			values[key] = v
		case "position":
			if _, ok := value.(float64); !ok {
				httpapi.Fail(c, apperror.Invalid("Invalid position"))
				return
			}
			values[key] = value
		case "parent_id":
			if value == nil {
				values[key] = nil
				break
			}
			v, ok := value.(string)
			if !ok {
				httpapi.Fail(c, apperror.Invalid("Invalid parent label"))
				return
			}
			parentID, err := uuid.Parse(v)
			if err != nil || parentID == id {
				httpapi.Fail(c, apperror.Invalid("Invalid parent label"))
				return
			}
			values[key] = parentID
		default:
			httpapi.Fail(c, apperror.Invalid("Unsupported label field: "+key))
			return
		}
	}
	var value map[string]any
	err = s.Deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if err := lockLabelScope(c, q, scope); err != nil {
			return err
		}
		if parentID, ok := values["parent_id"].(uuid.UUID); ok {
			var exists, cycle bool
			err := q.QueryRowContext(c.Request.Context(), `WITH RECURSIVE ancestors AS(SELECT id,parent_id FROM labels WHERE id=$1 AND project_id IS NOT DISTINCT FROM $2::uuid AND workspace_id=$3 AND deleted_at IS NULL UNION SELECT l.id,l.parent_id FROM labels l JOIN ancestors a ON l.id=a.parent_id WHERE l.workspace_id=$3 AND l.project_id IS NOT DISTINCT FROM $2::uuid AND l.deleted_at IS NULL)SELECT EXISTS(SELECT 1 FROM ancestors),EXISTS(SELECT 1 FROM ancestors WHERE id=$4)`, parentID, optionalProjectID(scope), scope.WorkspaceID, id).Scan(&exists, &cycle)
			if err != nil {
				return err
			}
			if !exists || cycle {
				return apperror.Invalid("Parent label must be in the same hierarchy and cannot form a cycle")
			}
		}
		if err := patch(c.Request.Context(), q, "labels", id, "AND project_id IS NOT DISTINCT FROM $2::uuid AND workspace_id=$3", []any{optionalProjectID(scope), scope.WorkspaceID}, values); err != nil {
			return err
		}
		var err error
		value, err = queryObject(c.Request.Context(), q, `SELECT to_jsonb(l) FROM labels l WHERE id=$1`, id)
		return err
	})
	reply(c, 200, value, err)
}
func (s *Server) deleteLabel(c *gin.Context) {
	scope, err := s.scope(c, c.Param("projectID") != "", identity.Member)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	id, err := httpapi.UUIDParam(c, "labelID")
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	err = s.Deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if err := lockLabelScope(c, q, scope); err != nil {
			return err
		}
		if err := affected(q.ExecContext(c.Request.Context(), `UPDATE labels SET deleted_at=now(),updated_at=now() WHERE id=$1 AND project_id IS NOT DISTINCT FROM $2::uuid AND workspace_id=$3 AND deleted_at IS NULL`, id, optionalProjectID(scope), scope.WorkspaceID)); err != nil {
			return err
		}
		if _, err := q.ExecContext(c.Request.Context(), `UPDATE work_item_labels SET deleted_at=now(),updated_at=now() WHERE label_id=$1 AND deleted_at IS NULL`, id); err != nil {
			return err
		}
		_, err := q.ExecContext(c.Request.Context(), `UPDATE labels SET parent_id=NULL,updated_at=now() WHERE parent_id=$1 AND deleted_at IS NULL`, id)
		return err
	})
	noContent(c, err)
}
