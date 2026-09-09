package foundation

import (
	"encoding/json"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"my-jira/apps/api/internal/platform/apperror"
	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/httpapi"
)

func (s *Server) admin(c *gin.Context) bool {
	actor, err := httpapi.Actor(c)
	if err != nil {
		httpapi.Fail(c, err)
		return false
	}
	if !actor.IsAdmin || actor.TokenWorkspaceID != uuid.Nil {
		httpapi.Fail(c, apperror.Forbidden())
		return false
	}
	return true
}
func (s *Server) adminStats(c *gin.Context) {
	if !s.admin(c) {
		return
	}
	// These are existing-resource totals: archived projects/items/pages and drafts
	// still exist, while a deleted parent removes its children from the totals.
	// Attachment storage counts completed, nondeleted asset metadata under those
	// same live parent chains; it is not the physical bucket's retained byte size.
	value, err := queryObject(c.Request.Context(), s.Deps.DB.SQL, `SELECT jsonb_build_object(
		'users',(SELECT count(*) FROM users WHERE deleted_at IS NULL),
		'active_users',(SELECT count(*) FROM users WHERE is_active AND deleted_at IS NULL),
		'workspaces',(SELECT count(*) FROM workspaces WHERE deleted_at IS NULL),
		'projects',(SELECT count(*) FROM projects p JOIN workspaces w ON w.id=p.workspace_id AND w.deleted_at IS NULL WHERE p.deleted_at IS NULL),
		'work_items',(SELECT count(*) FROM work_items i JOIN projects p ON p.id=i.project_id AND p.workspace_id=i.workspace_id AND p.deleted_at IS NULL JOIN workspaces w ON w.id=i.workspace_id AND w.deleted_at IS NULL WHERE i.deleted_at IS NULL),
		'storage_bytes',(SELECT COALESCE(sum(f.size_bytes),0) FROM file_assets f WHERE f.upload_status='completed' AND f.deleted_at IS NULL
			AND (f.workspace_id IS NULL OR EXISTS(SELECT 1 FROM workspaces w WHERE w.id=f.workspace_id AND w.deleted_at IS NULL))
			AND (f.project_id IS NULL OR EXISTS(SELECT 1 FROM projects p WHERE p.id=f.project_id AND p.workspace_id=f.workspace_id AND p.deleted_at IS NULL))
			AND (f.work_item_id IS NULL OR EXISTS(SELECT 1 FROM work_items i WHERE i.id=f.work_item_id AND i.workspace_id=f.workspace_id AND i.project_id=f.project_id AND i.deleted_at IS NULL))
			AND (f.page_id IS NULL OR EXISTS(SELECT 1 FROM pages p WHERE p.id=f.page_id AND p.workspace_id=f.workspace_id AND p.project_id IS NOT DISTINCT FROM f.project_id AND p.deleted_at IS NULL))),
		'pending_jobs',(SELECT count(*) FROM outbox_events WHERE dispatched_at IS NULL),
		'active_sessions',(SELECT count(*) FROM sessions WHERE revoked_at IS NULL AND expires_at>now()))`)
	reply(c, 200, value, err)
}
func (s *Server) adminUsers(c *gin.Context) {
	if !s.admin(c) {
		return
	}
	search := strings.TrimSpace(c.Query("search"))
	if len(search) > 255 {
		httpapi.Fail(c, apperror.Invalid("Search is too long"))
		return
	}
	value, err := queryList(c.Request.Context(), s.Deps.DB.SQL, `SELECT `+userJSON+`||jsonb_build_object('is_active',u.is_active,'workspace_count',(SELECT count(*) FROM workspace_members m JOIN workspaces w ON w.id=m.workspace_id WHERE m.user_id=u.id AND m.is_active AND m.deleted_at IS NULL AND w.deleted_at IS NULL)) FROM users u WHERE u.deleted_at IS NULL AND ($1='' OR u.email ILIKE '%'||$1||'%' OR u.display_name ILIKE '%'||$1||'%') ORDER BY u.created_at DESC LIMIT 1000`, search)
	reply(c, 200, value, err)
}
func (s *Server) updateAdminUser(c *gin.Context) {
	if !s.admin(c) {
		return
	}
	id, err := httpapi.UUIDParam(c, "userID")
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	input, err := httpapi.Bind[struct {
		IsActive        *bool `json:"is_active"`
		IsInstanceAdmin *bool `json:"is_instance_admin"`
	}](c)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	if input.IsActive == nil && input.IsInstanceAdmin == nil {
		httpapi.Fail(c, apperror.Invalid("No changes were provided"))
		return
	}
	var value map[string]any
	err = s.Deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if _, err := q.ExecContext(c.Request.Context(), `SELECT id FROM instances WHERE singleton FOR UPDATE`); err != nil {
			return err
		}
		actor, _ := httpapi.Actor(c)
		var stillAdmin bool
		if err := q.QueryRowContext(c.Request.Context(), `SELECT is_instance_admin AND is_active AND deleted_at IS NULL FROM users WHERE id=$1`, actor.UserID).Scan(&stillAdmin); err != nil {
			return err
		}
		if !stillAdmin {
			return apperror.Forbidden()
		}
		var active, admin bool
		err := q.QueryRowContext(c.Request.Context(), `SELECT is_active,is_instance_admin FROM users WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, id).Scan(&active, &admin)
		if err != nil {
			return err
		}
		nextActive, nextAdmin := active, admin
		if input.IsActive != nil {
			nextActive = *input.IsActive
		}
		if input.IsInstanceAdmin != nil {
			nextAdmin = *input.IsInstanceAdmin
		}
		if active && admin && (!nextActive || !nextAdmin) {
			var count int
			if err = q.QueryRowContext(c.Request.Context(), `SELECT count(*) FROM users WHERE is_active AND is_instance_admin AND deleted_at IS NULL`).Scan(&count); err != nil {
				return err
			}
			if count <= 1 {
				return apperror.Conflict("The instance must retain at least one active admin")
			}
		}
		if _, err = q.ExecContext(c.Request.Context(), `UPDATE users SET is_active=$2,is_instance_admin=$3,updated_at=now() WHERE id=$1`, id, nextActive, nextAdmin); err != nil {
			return err
		}
		if !nextActive {
			if _, err = q.ExecContext(c.Request.Context(), `UPDATE sessions SET revoked_at=now(),updated_at=now() WHERE user_id=$1 AND revoked_at IS NULL`, id); err != nil {
				return err
			}
		}
		value, err = queryObject(c.Request.Context(), q, `SELECT `+userJSON+`||jsonb_build_object('is_active',u.is_active) FROM users u WHERE id=$1`, id)
		return err
	})
	reply(c, 200, value, err)
}
func (s *Server) adminWorkspaces(c *gin.Context) {
	if !s.admin(c) {
		return
	}
	value, err := queryList(c.Request.Context(), s.Deps.DB.SQL, `SELECT to_jsonb(w)||jsonb_build_object('member_count',(SELECT count(*) FROM workspace_members m WHERE m.workspace_id=w.id AND m.is_active AND m.deleted_at IS NULL),'project_count',(SELECT count(*) FROM projects p WHERE p.workspace_id=w.id AND p.deleted_at IS NULL)) FROM workspaces w WHERE w.deleted_at IS NULL ORDER BY w.created_at DESC LIMIT 1000`)
	reply(c, 200, value, err)
}
func (s *Server) adminConfiguration(c *gin.Context) {
	if !s.admin(c) {
		return
	}
	value, err := queryObject(c.Request.Context(), s.Deps.DB.SQL, `SELECT jsonb_build_object('name',name,'registration_enabled',registration_enabled,'settings',jsonb_build_object('allow_workspace_creation',COALESCE((settings->>'allow_workspace_creation')::bool,true),'magic_login_enabled',COALESCE((settings->>'magic_login_enabled')::bool,true))) FROM instances WHERE singleton AND deleted_at IS NULL`)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	value["configured_auth_methods"] = s.authMethods()
	value["email_configured"] = s.emailConfigured()
	httpapi.JSON(c, 200, value)
}
func (s *Server) updateAdminConfiguration(c *gin.Context) {
	if !s.admin(c) {
		return
	}
	input, err := httpapi.Bind[struct {
		Name                *string        `json:"name"`
		RegistrationEnabled *bool          `json:"registration_enabled"`
		Settings            map[string]any `json:"settings"`
	}](c)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	if input.Name != nil && !validName(*input.Name) {
		httpapi.Fail(c, apperror.Invalid("A valid instance name is required"))
		return
	}
	for key, value := range input.Settings {
		if key != "allow_workspace_creation" && key != "magic_login_enabled" {
			httpapi.Fail(c, apperror.Invalid("Unsupported setting: "+key))
			return
		}
		if _, ok := value.(bool); !ok {
			httpapi.Fail(c, apperror.Invalid("Settings values must be true or false"))
			return
		}
	}
	if input.Settings == nil {
		input.Settings = map[string]any{}
	}
	raw, _ := json.Marshal(input.Settings)
	_, err = s.Deps.DB.SQL.ExecContext(c.Request.Context(), `UPDATE instances SET name=COALESCE($1,name),registration_enabled=COALESCE($2,registration_enabled),settings=settings||$3::jsonb,updated_at=now() WHERE singleton`, input.Name, input.RegistrationEnabled, string(raw))
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	s.adminConfiguration(c)
}
