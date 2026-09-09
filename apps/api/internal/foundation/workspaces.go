package foundation

import (
	"database/sql"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"my-jira/apps/api/internal/platform/apperror"
	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/httpapi"
	"my-jira/apps/api/internal/platform/identity"
)

func (s *Server) workspaces(c *gin.Context) {
	a, _ := httpapi.Actor(c)
	value, err := queryList(c.Request.Context(), s.Deps.DB.SQL, `SELECT to_jsonb(w)||jsonb_build_object('role',m.role) FROM workspaces w JOIN workspace_members m ON m.workspace_id=w.id WHERE m.user_id=$1 AND m.is_active AND m.deleted_at IS NULL AND w.deleted_at IS NULL AND ($2::uuid='00000000-0000-0000-0000-000000000000' OR w.id=$2) ORDER BY w.created_at`, a.UserID, a.TokenWorkspaceID)
	reply(c, 200, value, err)
}
func (s *Server) workspace(c *gin.Context) {
	scope, err := s.scope(c, false, identity.Guest)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	value, err := queryObject(c.Request.Context(), s.Deps.DB.SQL, `SELECT to_jsonb(w)||jsonb_build_object('role',$2::int) FROM workspaces w WHERE id=$1 AND deleted_at IS NULL`, scope.WorkspaceID, scope.Role)
	reply(c, 200, value, err)
}
func (s *Server) createWorkspace(c *gin.Context) {
	a, _ := httpapi.Actor(c)
	var allowed bool
	if err := s.Deps.DB.SQL.QueryRowContext(c.Request.Context(), `SELECT COALESCE((settings->>'allow_workspace_creation')::bool,true) FROM instances WHERE singleton`).Scan(&allowed); err != nil {
		httpapi.Fail(c, err)
		return
	}
	if !allowed && !a.IsAdmin {
		httpapi.Fail(c, apperror.Forbidden())
		return
	}
	input, err := httpapi.Bind[struct {
		Name        string `json:"name"`
		Slug        string `json:"slug"`
		Timezone    string `json:"timezone"`
		Description string `json:"description"`
	}](c)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Slug = strings.ToLower(strings.TrimSpace(input.Slug))
	if input.Timezone == "" {
		input.Timezone = "UTC"
	}
	if !validName(input.Name) || len(input.Slug) > 48 || !slugPattern.MatchString(input.Slug) || !validTimezone(input.Timezone) || len(input.Description) > 10000 {
		httpapi.Fail(c, apperror.Invalid("Provide a workspace name, a valid slug, and a valid timezone"))
		return
	}
	id := uuid.New()
	var value map[string]any
	err = s.Deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if _, err := q.ExecContext(c.Request.Context(), `INSERT INTO workspaces(id,name,slug,owner_id,timezone,description) VALUES($1,$2,$3,$4,$5,$6)`, id, input.Name, input.Slug, a.UserID, input.Timezone, input.Description); err != nil {
			return err
		}
		if _, err := q.ExecContext(c.Request.Context(), `INSERT INTO workspace_members(workspace_id,user_id,role) VALUES($1,$2,20)`, id, a.UserID); err != nil {
			return err
		}
		var err error
		value, err = queryObject(c.Request.Context(), q, `SELECT to_jsonb(w)||jsonb_build_object('role',20) FROM workspaces w WHERE id=$1`, id)
		return err
	})
	reply(c, 201, value, err)
}
func (s *Server) updateWorkspace(c *gin.Context) {
	scope, err := s.scope(c, false, identity.Admin)
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
		v, ok := value.(string)
		if !ok {
			httpapi.Fail(c, apperror.Invalid("Invalid "+key))
			return
		}
		switch key {
		case "name":
			v = strings.TrimSpace(v)
			if !validName(v) {
				httpapi.Fail(c, apperror.Invalid("A workspace name is required"))
				return
			}
		case "slug":
			v = strings.ToLower(strings.TrimSpace(v))
			if len(v) > 48 || !slugPattern.MatchString(v) {
				httpapi.Fail(c, apperror.Invalid("Invalid workspace slug"))
				return
			}
		case "timezone":
			if !validTimezone(v) {
				httpapi.Fail(c, apperror.Invalid("Invalid timezone"))
				return
			}
		case "description", "logo_url":
			if len(v) > 10000 {
				httpapi.Fail(c, apperror.Invalid("Value is too long"))
				return
			}
		default:
			httpapi.Fail(c, apperror.Invalid("Unsupported workspace field: "+key))
			return
		}
		values[key] = v
	}
	if err = patch(c.Request.Context(), s.Deps.DB.SQL, "workspaces", scope.WorkspaceID, "", nil, values); err != nil {
		httpapi.Fail(c, err)
		return
	}
	s.workspace(c)
}
func (s *Server) deleteWorkspace(c *gin.Context) {
	scope, err := s.scope(c, false, identity.Admin)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	err = affected(s.Deps.DB.SQL.ExecContext(c.Request.Context(), `UPDATE workspaces SET deleted_at=now(),updated_at=now() WHERE id=$1 AND deleted_at IS NULL`, scope.WorkspaceID))
	noContent(c, err)
}

const memberProjection = `jsonb_build_object('id',m.id,'user_id',m.user_id,'workspace_id',m.workspace_id,'role',m.role,'is_active',m.is_active,'email',u.email,'display_name',u.display_name,'avatar_url',u.avatar_url,'created_at',m.created_at)`

func (s *Server) workspaceMembers(c *gin.Context) {
	scope, err := s.scope(c, false, identity.Guest)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	value, err := queryList(c.Request.Context(), s.Deps.DB.SQL, `SELECT `+memberProjection+` FROM workspace_members m JOIN users u ON u.id=m.user_id WHERE m.workspace_id=$1 AND m.is_active AND m.deleted_at IS NULL AND u.is_active AND u.deleted_at IS NULL ORDER BY m.role DESC,u.display_name`, scope.WorkspaceID)
	reply(c, 200, value, err)
}
func (s *Server) addWorkspaceMember(c *gin.Context) {
	scope, err := s.scope(c, false, identity.Admin)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	input, err := httpapi.Bind[struct {
		Email string        `json:"email"`
		Role  identity.Role `json:"role"`
	}](c)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	if input.Role == 0 {
		input.Role = identity.Member
	}
	if !validEmail(input.Email) || !identity.ValidRole(input.Role) {
		httpapi.Fail(c, apperror.Invalid("A valid email and role are required"))
		return
	}
	var userID uuid.UUID
	err = s.Deps.DB.SQL.QueryRowContext(c.Request.Context(), `SELECT id FROM users WHERE email=$1 AND is_active AND deleted_at IS NULL`, input.Email).Scan(&userID)
	if err == sql.ErrNoRows {
		httpapi.Fail(c, apperror.New(404, "user_not_found", "Invite this email address to create an account first"))
		return
	}
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	var value map[string]any
	err = s.Deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if _, err := q.ExecContext(c.Request.Context(), `SELECT id FROM workspaces WHERE id=$1 FOR UPDATE`, scope.WorkspaceID); err != nil {
			return err
		}
		if _, err := (&identity.SQLPolicy{DB: q}).Workspace(c.Request.Context(), scope.Actor, scope.WorkspaceID, identity.Admin); err != nil {
			return err
		}
		var memberID uuid.UUID
		if err := q.QueryRowContext(c.Request.Context(), `INSERT INTO workspace_members(workspace_id,user_id,role) VALUES($1,$2,$3) RETURNING id`, scope.WorkspaceID, userID, input.Role).Scan(&memberID); err != nil {
			return err
		}
		var err error
		value, err = queryObject(c.Request.Context(), q, `SELECT `+memberProjection+` FROM workspace_members m JOIN users u ON u.id=m.user_id WHERE m.id=$1`, memberID)
		return err
	})
	reply(c, 201, value, err)
}
func (s *Server) updateWorkspaceMember(c *gin.Context) { s.mutateWorkspaceMember(c, false) }
func (s *Server) removeWorkspaceMember(c *gin.Context) { s.mutateWorkspaceMember(c, true) }
func (s *Server) mutateWorkspaceMember(c *gin.Context, remove bool) {
	self := remove && c.Param("memberID") == ""
	minimum := identity.Admin
	if self {
		minimum = identity.Guest
	}
	scope, err := s.scope(c, false, minimum)
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
		if _, err := (&identity.SQLPolicy{DB: q}).Workspace(c.Request.Context(), scope.Actor, scope.WorkspaceID, minimum); err != nil {
			return err
		}
		if self {
			if err := q.QueryRowContext(c.Request.Context(), `SELECT id FROM workspace_members WHERE workspace_id=$1 AND user_id=$2 AND is_active AND deleted_at IS NULL`, scope.WorkspaceID, scope.Actor.UserID).Scan(&id); err != nil {
				return err
			}
		}
		var userID uuid.UUID
		var oldRole identity.Role
		err := q.QueryRowContext(c.Request.Context(), `SELECT user_id,role FROM workspace_members WHERE id=$1 AND workspace_id=$2 AND deleted_at IS NULL AND is_active FOR UPDATE`, id, scope.WorkspaceID).Scan(&userID, &oldRole)
		if err == sql.ErrNoRows {
			return apperror.NotFound()
		}
		if err != nil {
			return err
		}
		if oldRole == identity.Admin && (remove || role != identity.Admin) {
			var count int
			if err = q.QueryRowContext(c.Request.Context(), `SELECT count(*) FROM workspace_members WHERE workspace_id=$1 AND role=20 AND is_active AND deleted_at IS NULL`, scope.WorkspaceID).Scan(&count); err != nil {
				return err
			}
			if count <= 1 {
				return apperror.Conflict("A workspace must retain at least one admin")
			}
		}
		if remove || role == identity.Guest {
			var orphan bool
			err = q.QueryRowContext(c.Request.Context(), `SELECT EXISTS(SELECT 1 FROM project_members m JOIN projects p ON p.id=m.project_id WHERE m.workspace_id=$1 AND m.user_id=$2 AND m.role=20 AND m.is_active AND m.deleted_at IS NULL AND p.deleted_at IS NULL AND NOT EXISTS(SELECT 1 FROM project_members other JOIN workspace_members wm ON wm.workspace_id=other.workspace_id AND wm.user_id=other.user_id JOIN users u ON u.id=other.user_id WHERE other.project_id=m.project_id AND other.user_id<>$2 AND other.role=20 AND other.is_active AND other.deleted_at IS NULL AND wm.role>=15 AND wm.is_active AND wm.deleted_at IS NULL AND u.is_active AND u.deleted_at IS NULL))`, scope.WorkspaceID, userID).Scan(&orphan)
			if err != nil {
				return err
			}
			if orphan {
				return apperror.Conflict("Assign another project admin before removing or making this member a guest")
			}
		}
		if remove {
			if _, err = q.ExecContext(c.Request.Context(), `UPDATE workspace_members SET deleted_at=now(),is_active=false,updated_at=now() WHERE id=$1`, id); err != nil {
				return err
			}
			if _, err = q.ExecContext(c.Request.Context(), `UPDATE project_members SET deleted_at=now(),is_active=false,updated_at=now() WHERE workspace_id=$1 AND user_id=$2 AND deleted_at IS NULL`, scope.WorkspaceID, userID); err != nil {
				return err
			}
		} else {
			if _, err = q.ExecContext(c.Request.Context(), `UPDATE workspace_members SET role=$2,updated_at=now() WHERE id=$1`, id, role); err != nil {
				return err
			}
		}
		if remove || role != identity.Admin {
			if _, err = q.ExecContext(c.Request.Context(), `UPDATE workspaces SET owner_id=(SELECT user_id FROM workspace_members WHERE workspace_id=$1 AND role=20 AND is_active AND deleted_at IS NULL ORDER BY created_at LIMIT 1),updated_at=now() WHERE id=$1 AND owner_id=$2`, scope.WorkspaceID, userID); err != nil {
				return err
			}
		}
		if !remove {
			value, err = queryObject(c.Request.Context(), q, `SELECT `+memberProjection+` FROM workspace_members m JOIN users u ON u.id=m.user_id WHERE m.id=$1`, id)
		}
		return err
	})
	if remove {
		noContent(c, err)
	} else {
		reply(c, 200, value, err)
	}
}

func (s *Server) invitations(c *gin.Context) {
	scope, err := s.scope(c, false, identity.Admin)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	value, err := queryList(c.Request.Context(), s.Deps.DB.SQL, `SELECT to_jsonb(i)-'token_hash' FROM invitations i WHERE workspace_id=$1 AND deleted_at IS NULL ORDER BY created_at DESC`, scope.WorkspaceID)
	reply(c, 200, value, err)
}
func (s *Server) createInvitation(c *gin.Context) {
	scope, err := s.scope(c, false, identity.Admin)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	input, err := httpapi.Bind[struct {
		Email string        `json:"email"`
		Role  identity.Role `json:"role"`
	}](c)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	if input.Role == 0 {
		input.Role = identity.Member
	}
	if !validEmail(input.Email) || !identity.ValidRole(input.Role) {
		httpapi.Fail(c, apperror.Invalid("A valid email and role are required"))
		return
	}
	token, err := randomToken()
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	id := uuid.New()
	var value map[string]any
	err = s.Deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		var member bool
		if err := q.QueryRowContext(c.Request.Context(), `SELECT EXISTS(SELECT 1 FROM workspace_members m JOIN users u ON u.id=m.user_id WHERE m.workspace_id=$1 AND u.email=$2 AND m.deleted_at IS NULL AND m.is_active)`, scope.WorkspaceID, input.Email).Scan(&member); err != nil {
			return err
		}
		if member {
			return apperror.Conflict("This user is already a workspace member")
		}
		if _, err := q.ExecContext(c.Request.Context(), `UPDATE invitations SET revoked_at=now(),updated_at=now() WHERE workspace_id=$1 AND email=$2 AND accepted_at IS NULL AND revoked_at IS NULL`, scope.WorkspaceID, input.Email); err != nil {
			return err
		}
		if _, err := q.ExecContext(c.Request.Context(), `INSERT INTO invitations(id,workspace_id,email,role,token_hash,invited_by,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, id, scope.WorkspaceID, input.Email, input.Role, hashToken(token), scope.Actor.UserID, time.Now().Add(7*24*time.Hour)); err != nil {
			return err
		}
		link := strings.TrimRight(strings.Split(s.Config.AppOrigin, ",")[0], "/") + "/invitations/" + token
		if err := s.Deps.Jobs.Publish(c.Request.Context(), q, "email.send", map[string]any{"to": input.Email, "subject": "You have been invited to a workspace", "text": "Sign in with this email address, then accept the invitation: " + link}, "invitation:"+id.String()); err != nil {
			return err
		}
		var err error
		value, err = queryObject(c.Request.Context(), q, `SELECT to_jsonb(i)-'token_hash' FROM invitations i WHERE id=$1`, id)
		return err
	})
	reply(c, 201, value, err)
}
func (s *Server) revokeInvitation(c *gin.Context) {
	scope, err := s.scope(c, false, identity.Admin)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	id, err := httpapi.UUIDParam(c, "invitationID")
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	err = affected(s.Deps.DB.SQL.ExecContext(c.Request.Context(), `UPDATE invitations SET revoked_at=now(),updated_at=now() WHERE id=$1 AND workspace_id=$2 AND deleted_at IS NULL AND accepted_at IS NULL`, id, scope.WorkspaceID))
	noContent(c, err)
}
func (s *Server) acceptInvitation(c *gin.Context) {
	a, _ := httpapi.Actor(c)
	var expectedWorkspaceID uuid.UUID
	if c.Param("workspaceID") != "" {
		var err error
		expectedWorkspaceID, err = httpapi.UUIDParam(c, "workspaceID")
		if err != nil {
			httpapi.Fail(c, err)
			return
		}
		if a.TokenWorkspaceID != uuid.Nil && a.TokenWorkspaceID != expectedWorkspaceID {
			httpapi.Fail(c, apperror.NotFound())
			return
		}
	}
	input, err := httpapi.Bind[struct {
		Token string `json:"token"`
	}](c)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	if len(input.Token) != 43 {
		httpapi.Fail(c, apperror.Invalid("Invalid invitation token"))
		return
	}
	var value map[string]any
	err = s.Deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		var id, workspaceID uuid.UUID
		var role int
		var email string
		err := q.QueryRowContext(c.Request.Context(), `SELECT i.id,i.workspace_id,i.role,i.email FROM invitations i JOIN workspaces w ON w.id=i.workspace_id WHERE i.token_hash=$1 AND i.expires_at>now() AND i.revoked_at IS NULL AND i.accepted_at IS NULL AND i.deleted_at IS NULL AND w.deleted_at IS NULL FOR UPDATE OF i`, hashToken(input.Token)).Scan(&id, &workspaceID, &role, &email)
		if err == sql.ErrNoRows {
			return apperror.NotFound()
		}
		if err != nil {
			return err
		}
		if expectedWorkspaceID != uuid.Nil && expectedWorkspaceID != workspaceID {
			return apperror.NotFound()
		}
		if _, err = q.ExecContext(c.Request.Context(), `SELECT id FROM workspaces WHERE id=$1 FOR UPDATE`, workspaceID); err != nil {
			return err
		}
		var userEmail string
		if err = q.QueryRowContext(c.Request.Context(), `SELECT email FROM users WHERE id=$1`, a.UserID).Scan(&userEmail); err != nil {
			return err
		}
		if userEmail != email {
			return apperror.Forbidden()
		}
		if _, err = q.ExecContext(c.Request.Context(), `INSERT INTO workspace_members(workspace_id,user_id,role) VALUES($1,$2,$3) ON CONFLICT(workspace_id,user_id) WHERE deleted_at IS NULL DO NOTHING`, workspaceID, a.UserID, role); err != nil {
			return err
		}
		if _, err = q.ExecContext(c.Request.Context(), `UPDATE invitations SET accepted_at=now(),updated_at=now() WHERE id=$1`, id); err != nil {
			return err
		}
		value, err = queryObject(c.Request.Context(), q, `SELECT to_jsonb(w)||jsonb_build_object('role',m.role) FROM workspaces w JOIN workspace_members m ON m.workspace_id=w.id WHERE w.id=$1 AND m.user_id=$2 AND m.deleted_at IS NULL`, workspaceID, a.UserID)
		return err
	})
	reply(c, 200, value, err)
}
