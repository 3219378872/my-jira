package foundation

import (
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"my-jira/apps/api/internal/platform/apperror"
	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/httpapi"
	"my-jira/apps/api/internal/platform/identity"
	"my-jira/apps/api/internal/platform/jobs"
	"my-jira/apps/api/internal/workitems"
)

func (s *Server) projects(c *gin.Context) {
	scope, err := s.scope(c, false, identity.Guest)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	value, err := queryList(c.Request.Context(), s.Deps.DB.SQL, `SELECT `+projectJSON+`||jsonb_build_object('role',CASE WHEN $3::int=5 THEN 5 WHEN $3::int=20 THEN 20 ELSE COALESCE(m.role,$3::int) END,'is_member',m.id IS NOT NULL) FROM projects p LEFT JOIN project_members m ON m.project_id=p.id AND m.user_id=$2 AND m.is_active AND m.deleted_at IS NULL WHERE p.workspace_id=$1 AND p.deleted_at IS NULL AND (p.network='public' OR m.id IS NOT NULL) ORDER BY p.created_at`, scope.WorkspaceID, scope.Actor.UserID, scope.Role)
	reply(c, 200, value, err)
}
func (s *Server) project(c *gin.Context) {
	scope, err := s.scope(c, true, identity.Guest)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	value, err := queryObject(c.Request.Context(), s.Deps.DB.SQL, `SELECT `+projectJSON+`||jsonb_build_object('role',$3::int,'is_member',EXISTS(SELECT 1 FROM project_members m WHERE m.project_id=p.id AND m.user_id=$4 AND m.is_active AND m.deleted_at IS NULL)) FROM projects p WHERE p.id=$1 AND p.workspace_id=$2 AND p.deleted_at IS NULL`, scope.ProjectID, scope.WorkspaceID, scope.Role, scope.Actor.UserID)
	reply(c, 200, value, err)
}
func (s *Server) createProject(c *gin.Context) {
	scope, err := s.scope(c, false, identity.Member)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	input, err := httpapi.Bind[struct {
		Name            string         `json:"name"`
		Identifier      string         `json:"identifier"`
		Description     string         `json:"description"`
		Network         string         `json:"network"`
		Icon            string         `json:"icon"`
		Color           string         `json:"color"`
		Features        map[string]any `json:"features"`
		CoverImageURL   string         `json:"cover_image_url"`
		GuestCanViewAll bool           `json:"guest_can_view_all"`
	}](c)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Identifier = strings.ToUpper(strings.TrimSpace(input.Identifier))
	if input.Network == "" {
		input.Network = "public"
	}
	if input.Color == "" {
		input.Color = "#6d5efc"
	}
	if !validName(input.Name) || !identifierPattern.MatchString(input.Identifier) || (input.Network != "public" && input.Network != "private") || !colorPattern.MatchString(input.Color) || len(input.Description) > 10000 || len(input.Icon) > 255 {
		httpapi.Fail(c, apperror.Invalid("Invalid project name, identifier, network, or color"))
		return
	}
	settings := map[string]any{"cover_image_url": input.CoverImageURL}
	if input.Features != nil {
		settings["features"] = input.Features
	}
	if err = validateProjectSettings(settings); err != nil {
		httpapi.Fail(c, err)
		return
	}
	rawSettings, _ := json.Marshal(settings)
	id := uuid.New()
	var value map[string]any
	err = s.Deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if _, err := q.ExecContext(c.Request.Context(), `SELECT id FROM workspaces WHERE id=$1 FOR UPDATE`, scope.WorkspaceID); err != nil {
			return err
		}
		if _, err := (&identity.SQLPolicy{DB: q}).Workspace(c.Request.Context(), scope.Actor, scope.WorkspaceID, identity.Member); err != nil {
			return err
		}
		_, err := q.ExecContext(c.Request.Context(), `INSERT INTO projects(id,workspace_id,name,identifier,description,network,icon,color,timezone,settings,guest_can_view_all) SELECT $1,$2,$3,$4,$5,$6,$7,$8,timezone,$9::jsonb,$10 FROM workspaces WHERE id=$2`, id, scope.WorkspaceID, input.Name, input.Identifier, input.Description, input.Network, input.Icon, input.Color, string(rawSettings), input.GuestCanViewAll)
		if err != nil {
			return err
		}
		if _, err = q.ExecContext(c.Request.Context(), `INSERT INTO project_members(workspace_id,project_id,user_id,role) VALUES($1,$2,$3,20)`, scope.WorkspaceID, id, scope.Actor.UserID); err != nil {
			return err
		}
		states := []struct{ name, group, color string }{{"Backlog", "backlog", "#94a3b8"}, {"Todo", "unstarted", "#64748b"}, {"In progress", "started", "#eab308"}, {"Done", "completed", "#22c55e"}, {"Cancelled", "cancelled", "#ef4444"}}
		for i, state := range states {
			if _, err = q.ExecContext(c.Request.Context(), `INSERT INTO states(workspace_id,project_id,name,color,group_name,position,is_default) VALUES($1,$2,$3,$4,$5,$6,$7)`, scope.WorkspaceID, id, state.name, state.color, state.group, float64(i+1)*65536, i == 0); err != nil {
				return err
			}
		}
		value, err = queryObject(c.Request.Context(), q, `SELECT `+projectJSON+`||jsonb_build_object('role',20,'is_member',true) FROM projects p WHERE id=$1`, id)
		if err != nil {
			return err
		}
		eventScope := scope
		eventScope.ProjectID = id
		return jobs.PublishEntityChange(c.Request.Context(), q, eventScope, "project.changed", id, "created", projectEventDTO(value))
	})
	reply(c, 201, value, err)
}
func (s *Server) updateProject(c *gin.Context) {
	scope, err := s.scope(c, true, identity.Admin)
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
	settingsChanges := map[string]any{}
	for key, value := range input {
		switch key {
		case "name", "identifier", "description", "network", "timezone", "icon", "color":
			v, ok := value.(string)
			if !ok {
				httpapi.Fail(c, apperror.Invalid("Invalid "+key))
				return
			}
			v = strings.TrimSpace(v)
			if key == "identifier" {
				v = strings.ToUpper(v)
			}
			if len(v) > 10000 || (key == "name" && !validName(v)) || (key == "identifier" && !identifierPattern.MatchString(v)) || (key == "network" && v != "private" && v != "public") || (key == "timezone" && !validTimezone(v)) || (key == "color" && !colorPattern.MatchString(v)) {
				httpapi.Fail(c, apperror.Invalid("Invalid "+key))
				return
			}
			values[key] = v
		case "settings":
			settings, ok := value.(map[string]any)
			if !ok {
				httpapi.Fail(c, apperror.Invalid("Settings must be an object"))
				return
			}
			if err = validateProjectSettings(settings); err != nil {
				httpapi.Fail(c, err)
				return
			}
			settingsChanges = settings
		case "features", "cover_image_url":
			// Merged after the generic settings object so top-level fields win.
		case "guest_can_view_all":
			flag, ok := value.(bool)
			if !ok {
				httpapi.Fail(c, apperror.Invalid("guest_can_view_all must be true or false"))
				return
			}
			values[key] = flag
		case "archived_at":
			if value == nil {
				values[key] = nil
			} else if value == "now" {
				values[key] = sqlNow()
			} else {
				httpapi.Fail(c, apperror.Invalid("archived_at must be null or 'now'"))
				return
			}
		case "lead_id", "default_assignee_id":
			if value == nil {
				values[key] = nil
				break
			}
			v, ok := value.(string)
			if !ok {
				httpapi.Fail(c, apperror.Invalid("Invalid "+key))
				return
			}
			id, err := uuid.Parse(v)
			if err != nil {
				httpapi.Fail(c, apperror.Invalid("Invalid "+key))
				return
			}
			var exists bool
			err = s.Deps.DB.SQL.QueryRowContext(c.Request.Context(), `SELECT EXISTS(SELECT 1 FROM workspace_members WHERE workspace_id=$1 AND user_id=$2 AND role>=15 AND is_active AND deleted_at IS NULL)`, scope.WorkspaceID, id).Scan(&exists)
			if err != nil {
				httpapi.Fail(c, err)
				return
			}
			if !exists {
				httpapi.Fail(c, apperror.Invalid("The selected user must be a workspace member"))
				return
			}
			values[key] = id
		default:
			httpapi.Fail(c, apperror.Invalid("Unsupported project field: "+key))
			return
		}
	}
	if features, ok := input["features"]; ok {
		settingsChanges["features"], err = validateProjectFeatures(features)
		if err != nil {
			httpapi.Fail(c, err)
			return
		}
	}
	if cover, ok := input["cover_image_url"]; ok {
		settingsChanges["cover_image_url"], err = validateProjectCover(cover)
		if err != nil {
			httpapi.Fail(c, err)
			return
		}
	}
	err = s.Deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if _, err := q.ExecContext(c.Request.Context(), `SELECT id FROM workspaces WHERE id=$1 FOR UPDATE`, scope.WorkspaceID); err != nil {
			return err
		}
		var raw []byte
		if err := q.QueryRowContext(c.Request.Context(), `SELECT settings FROM projects WHERE id=$1 AND workspace_id=$2 AND deleted_at IS NULL FOR UPDATE`, scope.ProjectID, scope.WorkspaceID).Scan(&raw); err != nil {
			return err
		}
		current, authErr := workitems.CurrentScope(c.Request.Context(), q, scope.Actor, scope.WorkspaceID, scope.ProjectID, identity.Guest)
		if authErr != nil {
			return authErr
		}
		if current.Role < identity.Admin {
			return apperror.Forbidden()
		}
		scope = current
		if len(settingsChanges) > 0 {
			settings := map[string]any{}
			if err := json.Unmarshal(raw, &settings); err != nil {
				return err
			}
			merged, err := json.Marshal(mergeJSONObjects(settings, settingsChanges))
			if err != nil {
				return err
			}
			values["settings"] = string(merged)
		}
		for _, key := range []string{"lead_id", "default_assignee_id"} {
			if userID, ok := values[key].(uuid.UUID); ok {
				if _, err := (&identity.SQLPolicy{DB: q}).Project(c.Request.Context(), identity.Actor{UserID: userID}, scope.WorkspaceID, scope.ProjectID, identity.Member); err != nil {
					if unavailableLocation(err) {
						return apperror.Invalid("The selected user must have member access to this project")
					}
					return err
				}
			}
		}
		if err := patch(c.Request.Context(), q, "projects", scope.ProjectID, "AND workspace_id=$2", []any{scope.WorkspaceID}, values); err != nil {
			return err
		}
		value, err := queryObject(c.Request.Context(), q, `SELECT `+projectJSON+` FROM projects p WHERE id=$1 AND workspace_id=$2`, scope.ProjectID, scope.WorkspaceID)
		if err != nil {
			return err
		}
		return jobs.PublishEntityChange(c.Request.Context(), q, scope, "project.changed", scope.ProjectID, "updated", projectEventDTO(value))
	})
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	s.project(c)
}
func (s *Server) deleteProject(c *gin.Context) {
	scope, err := s.scope(c, true, identity.Admin)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	err = s.Deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if _, err := q.ExecContext(c.Request.Context(), `SELECT id FROM workspaces WHERE id=$1 FOR UPDATE`, scope.WorkspaceID); err != nil {
			return err
		}
		if _, err := q.ExecContext(c.Request.Context(), `SELECT id FROM projects WHERE id=$1 FOR UPDATE`, scope.ProjectID); err != nil {
			return err
		}
		if _, err := (&identity.SQLPolicy{DB: q}).Project(c.Request.Context(), scope.Actor, scope.WorkspaceID, scope.ProjectID, identity.Admin); err != nil {
			return err
		}
		if err := affected(q.ExecContext(c.Request.Context(), `UPDATE projects SET deleted_at=now(),updated_at=now() WHERE id=$1 AND workspace_id=$2 AND deleted_at IS NULL`, scope.ProjectID, scope.WorkspaceID)); err != nil {
			return err
		}
		value, err := queryObject(c.Request.Context(), q, `SELECT `+projectJSON+` FROM projects p WHERE id=$1 AND workspace_id=$2`, scope.ProjectID, scope.WorkspaceID)
		if err != nil {
			return err
		}
		return jobs.PublishEntityChange(c.Request.Context(), q, scope, "project.changed", scope.ProjectID, "deleted", projectEventDTO(value))
	})
	noContent(c, err)
}

func (s *Server) projectMembers(c *gin.Context) {
	scope, err := s.scope(c, true, identity.Guest)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	value, err := queryList(c.Request.Context(), s.Deps.DB.SQL, `SELECT `+memberProjection+`||jsonb_build_object('project_id',m.project_id) FROM project_members m JOIN users u ON u.id=m.user_id JOIN workspace_members wm ON wm.workspace_id=m.workspace_id AND wm.user_id=m.user_id WHERE m.project_id=$1 AND m.workspace_id=$2 AND m.is_active AND m.deleted_at IS NULL AND wm.is_active AND wm.deleted_at IS NULL AND u.is_active AND u.deleted_at IS NULL ORDER BY m.role DESC,u.display_name`, scope.ProjectID, scope.WorkspaceID)
	reply(c, 200, value, err)
}
func (s *Server) addProjectMember(c *gin.Context) {
	scope, err := s.scope(c, true, identity.Admin)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	input, err := httpapi.Bind[struct {
		UserID uuid.UUID     `json:"user_id"`
		Role   identity.Role `json:"role"`
	}](c)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	if input.Role == 0 {
		input.Role = identity.Member
	}
	if input.UserID == uuid.Nil || !identity.ValidRole(input.Role) {
		httpapi.Fail(c, apperror.Invalid("A user and valid role are required"))
		return
	}
	var value map[string]any
	err = s.Deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if _, err := q.ExecContext(c.Request.Context(), `SELECT id FROM workspaces WHERE id=$1 FOR UPDATE`, scope.WorkspaceID); err != nil {
			return err
		}
		if _, err := q.ExecContext(c.Request.Context(), `SELECT id FROM projects WHERE id=$1 FOR UPDATE`, scope.ProjectID); err != nil {
			return err
		}
		if _, err := (&identity.SQLPolicy{DB: q}).Project(c.Request.Context(), scope.Actor, scope.WorkspaceID, scope.ProjectID, identity.Admin); err != nil {
			return err
		}
		var workspaceRole identity.Role
		err := q.QueryRowContext(c.Request.Context(), `SELECT m.role FROM workspace_members m JOIN users u ON u.id=m.user_id WHERE m.workspace_id=$1 AND m.user_id=$2 AND m.is_active AND m.deleted_at IS NULL AND u.is_active AND u.deleted_at IS NULL`, scope.WorkspaceID, input.UserID).Scan(&workspaceRole)
		if err == sql.ErrNoRows {
			return apperror.Invalid("The user must first join this workspace")
		}
		if err != nil {
			return err
		}
		if workspaceRole == identity.Guest && input.Role != identity.Guest {
			return apperror.Invalid("Workspace guests can only have a guest project role")
		}
		var id uuid.UUID
		if err = q.QueryRowContext(c.Request.Context(), `INSERT INTO project_members(workspace_id,project_id,user_id,role) VALUES($1,$2,$3,$4) RETURNING id`, scope.WorkspaceID, scope.ProjectID, input.UserID, input.Role).Scan(&id); err != nil {
			return err
		}
		value, err = queryObject(c.Request.Context(), q, `SELECT `+memberProjection+`||jsonb_build_object('project_id',m.project_id) FROM project_members m JOIN users u ON u.id=m.user_id WHERE m.id=$1`, id)
		return err
	})
	reply(c, 201, value, err)
}
func (s *Server) updateProjectMember(c *gin.Context) { s.mutateProjectMember(c, false) }
func (s *Server) removeProjectMember(c *gin.Context) { s.mutateProjectMember(c, true) }
func (s *Server) mutateProjectMember(c *gin.Context, remove bool) {
	self := remove && c.Param("memberID") == ""
	minimum := identity.Admin
	if self {
		minimum = identity.Guest
	}
	scope, err := s.scope(c, true, minimum)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	var id uuid.UUID
	if !self {
		id, err = httpapi.UUIDParam(c, "memberID")
		if err != nil {
			httpapi.Fail(c, err)
			return
		}
	}
	role := identity.Guest
	if !remove {
		input, err := httpapi.Bind[struct {
			Role identity.Role `json:"role"`
		}](c)
		if err != nil {
			httpapi.Fail(c, err)
			return
		}
		role = input.Role
		if !identity.ValidRole(role) {
			httpapi.Fail(c, apperror.Invalid("Invalid role"))
			return
		}
	}
	var value map[string]any
	err = s.Deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if _, err := q.ExecContext(c.Request.Context(), `SELECT id FROM workspaces WHERE id=$1 FOR UPDATE`, scope.WorkspaceID); err != nil {
			return err
		}
		if _, err := q.ExecContext(c.Request.Context(), `SELECT id FROM projects WHERE id=$1 FOR UPDATE`, scope.ProjectID); err != nil {
			return err
		}
		if _, err := (&identity.SQLPolicy{DB: q}).Project(c.Request.Context(), scope.Actor, scope.WorkspaceID, scope.ProjectID, minimum); err != nil {
			return err
		}
		if self {
			if err := q.QueryRowContext(c.Request.Context(), `SELECT id FROM project_members WHERE project_id=$1 AND user_id=$2 AND is_active AND deleted_at IS NULL`, scope.ProjectID, scope.Actor.UserID).Scan(&id); err != nil {
				return err
			}
		}
		var oldRole, workspaceRole identity.Role
		err := q.QueryRowContext(c.Request.Context(), `SELECT m.role,w.role FROM project_members m JOIN workspace_members w ON w.workspace_id=m.workspace_id AND w.user_id=m.user_id AND w.deleted_at IS NULL AND w.is_active WHERE m.id=$1 AND m.project_id=$2 AND m.workspace_id=$3 AND m.is_active AND m.deleted_at IS NULL FOR UPDATE OF m`, id, scope.ProjectID, scope.WorkspaceID).Scan(&oldRole, &workspaceRole)
		if err == sql.ErrNoRows {
			return apperror.NotFound()
		}
		if err != nil {
			return err
		}
		if !remove && workspaceRole == identity.Guest && role != identity.Guest {
			return apperror.Invalid("Workspace guests can only have a guest project role")
		}
		if oldRole == identity.Admin && (remove || role != identity.Admin) {
			var count int
			if err = q.QueryRowContext(c.Request.Context(), `SELECT count(*) FROM project_members m JOIN workspace_members wm ON wm.workspace_id=m.workspace_id AND wm.user_id=m.user_id JOIN users u ON u.id=m.user_id WHERE m.project_id=$1 AND m.role=20 AND m.is_active AND m.deleted_at IS NULL AND wm.role>=15 AND wm.is_active AND wm.deleted_at IS NULL AND u.is_active AND u.deleted_at IS NULL`, scope.ProjectID).Scan(&count); err != nil {
				return err
			}
			if count <= 1 {
				return apperror.Conflict("A project must retain at least one admin")
			}
		}
		if remove {
			_, err = q.ExecContext(c.Request.Context(), `UPDATE project_members SET is_active=false,deleted_at=now(),updated_at=now() WHERE id=$1`, id)
		} else {
			_, err = q.ExecContext(c.Request.Context(), `UPDATE project_members SET role=$2,updated_at=now() WHERE id=$1`, id, role)
		}
		if err != nil {
			return err
		}
		if !remove {
			value, err = queryObject(c.Request.Context(), q, `SELECT `+memberProjection+`||jsonb_build_object('project_id',m.project_id) FROM project_members m JOIN users u ON u.id=m.user_id WHERE m.id=$1`, id)
		}
		return err
	})
	if remove {
		noContent(c, err)
	} else {
		reply(c, 200, value, err)
	}
}
