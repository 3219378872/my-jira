package foundation

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"my-jira/apps/api/internal/platform/apperror"
	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/httpapi"
	"my-jira/apps/api/internal/platform/identity"
)

func (s *Server) workspaceAvailability(c *gin.Context) {
	actor, _ := httpapi.Actor(c)
	if actor.TokenWorkspaceID != uuid.Nil {
		httpapi.Fail(c, apperror.Forbidden())
		return
	}
	slug := strings.ToLower(strings.TrimSpace(c.Query("slug")))
	if len(slug) > 48 || !slugPattern.MatchString(slug) {
		httpapi.Fail(c, apperror.Invalid("Provide a valid workspace slug"))
		return
	}
	var exists bool
	err := s.Deps.DB.SQL.QueryRowContext(c.Request.Context(), `SELECT EXISTS(SELECT 1 FROM workspaces WHERE slug=$1 AND deleted_at IS NULL)`, slug).Scan(&exists)
	reply(c, 200, gin.H{"slug": slug, "available": !exists}, err)
}

func (s *Server) projectAvailability(c *gin.Context) {
	scope, err := s.scope(c, false, identity.Member)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	identifier := strings.ToUpper(strings.TrimSpace(c.Query("identifier")))
	if !identifierPattern.MatchString(identifier) {
		httpapi.Fail(c, apperror.Invalid("Provide a valid project identifier"))
		return
	}
	var exists bool
	err = s.Deps.DB.SQL.QueryRowContext(c.Request.Context(), `SELECT EXISTS(SELECT 1 FROM projects WHERE workspace_id=$1 AND identifier=$2 AND deleted_at IS NULL)`, scope.WorkspaceID, identifier).Scan(&exists)
	reply(c, 200, gin.H{"identifier": identifier, "available": !exists}, err)
}

type visitedLocation struct {
	WorkspaceID *uuid.UUID `json:"workspace_id"`
	ProjectID   *uuid.UUID `json:"project_id"`
}

func unavailableLocation(err error) bool {
	var typed *apperror.Error
	return errors.As(err, &typed) && (typed.Status == 404 || typed.Status == 403)
}

func (s *Server) lastVisited(c *gin.Context) {
	actor, _ := httpapi.Actor(c)
	var raw []byte
	if err := s.Deps.DB.SQL.QueryRowContext(c.Request.Context(), `SELECT COALESCE(preferences->'_last_visited','{}'::jsonb) FROM users WHERE id=$1`, actor.UserID).Scan(&raw); err != nil {
		httpapi.Fail(c, err)
		return
	}
	var value visitedLocation
	if err := json.Unmarshal(raw, &value); err != nil {
		httpapi.Fail(c, err)
		return
	}
	if value.WorkspaceID != nil {
		if _, err := s.Deps.Policy.Workspace(c.Request.Context(), actor, *value.WorkspaceID, identity.Guest); err != nil {
			if !unavailableLocation(err) {
				httpapi.Fail(c, err)
				return
			}
			value = visitedLocation{}
		}
	}
	if value.WorkspaceID == nil {
		value.ProjectID = nil
	} else if value.ProjectID != nil {
		if _, err := s.Deps.Policy.Project(c.Request.Context(), actor, *value.WorkspaceID, *value.ProjectID, identity.Guest); err != nil {
			if !unavailableLocation(err) {
				httpapi.Fail(c, err)
				return
			}
			value.ProjectID = nil
		}
	}
	httpapi.JSON(c, 200, value)
}

func (s *Server) updateLastVisited(c *gin.Context) {
	actor, _ := httpapi.Actor(c)
	value, err := httpapi.Bind[visitedLocation](c)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	if value.WorkspaceID == nil && value.ProjectID != nil {
		httpapi.Fail(c, apperror.Invalid("A workspace is required when saving a project"))
		return
	}
	if value.WorkspaceID != nil {
		if value.ProjectID == nil {
			_, err = s.Deps.Policy.Workspace(c.Request.Context(), actor, *value.WorkspaceID, identity.Guest)
		} else {
			_, err = s.Deps.Policy.Project(c.Request.Context(), actor, *value.WorkspaceID, *value.ProjectID, identity.Guest)
		}
		if err != nil {
			httpapi.Fail(c, err)
			return
		}
	}
	raw, _ := json.Marshal(value)
	err = affected(s.Deps.DB.SQL.ExecContext(c.Request.Context(), `UPDATE users SET preferences=jsonb_set(preferences,'{_last_visited}',$2::jsonb,true),updated_at=now() WHERE id=$1 AND deleted_at IS NULL`, actor.UserID, string(raw)))
	reply(c, 200, value, err)
}

func (s *Server) joinProject(c *gin.Context) {
	scope, err := s.scope(c, true, identity.Guest)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	var value map[string]any
	err = s.Deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if _, err := q.ExecContext(c.Request.Context(), `SELECT id FROM workspaces WHERE id=$1 FOR UPDATE`, scope.WorkspaceID); err != nil {
			return err
		}
		member, err := (&identity.SQLPolicy{DB: q}).Workspace(c.Request.Context(), scope.Actor, scope.WorkspaceID, identity.Guest)
		if err != nil {
			return err
		}
		var network string
		if err = q.QueryRowContext(c.Request.Context(), `SELECT network FROM projects WHERE id=$1 AND workspace_id=$2 AND deleted_at IS NULL FOR UPDATE`, scope.ProjectID, scope.WorkspaceID).Scan(&network); err != nil {
			return err
		}
		if network != "public" {
			return apperror.Invalid("Private projects require an administrator to add members")
		}
		role := identity.Member
		if member.Role == identity.Guest {
			role = identity.Guest
		}
		if _, err = q.ExecContext(c.Request.Context(), `INSERT INTO project_members(workspace_id,project_id,user_id,role) VALUES($1,$2,$3,$4) ON CONFLICT(project_id,user_id) WHERE deleted_at IS NULL DO UPDATE SET is_active=true,updated_at=now()`, scope.WorkspaceID, scope.ProjectID, scope.Actor.UserID, role); err != nil {
			return err
		}
		value, err = queryObject(c.Request.Context(), q, `SELECT `+memberProjection+`||jsonb_build_object('project_id',m.project_id) FROM project_members m JOIN users u ON u.id=m.user_id WHERE m.project_id=$1 AND m.user_id=$2 AND m.deleted_at IS NULL`, scope.ProjectID, scope.Actor.UserID)
		return err
	})
	reply(c, 200, value, err)
}
