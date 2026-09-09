package foundation

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"my-jira/apps/api/internal/platform"
	"my-jira/apps/api/internal/platform/apperror"
	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/httpapi"
	"my-jira/apps/api/internal/platform/identity"
)

type Config struct {
	AppOrigin       string
	CookieSecure    bool
	SessionDuration time.Duration
}
type Server struct {
	Deps   platform.Dependencies
	Config Config
}

func New(deps platform.Dependencies, config Config) *Server {
	if config.SessionDuration == 0 {
		config.SessionDuration = 30 * 24 * time.Hour
	}
	return &Server{Deps: deps, Config: config}
}

func (s *Server) RegisterPublic(r *gin.RouterGroup) {
	r.GET("/instance", s.instance)
	r.POST("/instance/setup", s.setup)
	r.GET("/auth/csrf", s.csrf)
	r.POST("/auth/register", s.register)
	r.POST("/auth/login", s.login)
	r.POST("/auth/forgot-password", s.forgotPassword)
	r.POST("/auth/reset-password", s.resetPassword)
	r.POST("/auth/magic/request", s.magicRequest)
	r.POST("/auth/magic/verify", s.magicVerify)
	r.GET("/auth/oauth/:provider", s.oauthStart)
	r.GET("/auth/oauth/:provider/callback", s.oauthCallback)
}
func (s *Server) Register(r *gin.RouterGroup) {
	r.PATCH("/instance", s.updateInstance)
	r.GET("/auth/me", s.me)
	r.PATCH("/auth/me", s.updateMe)
	r.GET("/auth/last-visited", s.lastVisited)
	r.PATCH("/auth/last-visited", s.updateLastVisited)
	r.POST("/auth/password", s.changePassword)
	r.POST("/auth/logout", s.logout)
	r.GET("/auth/sessions", s.sessions)
	r.DELETE("/auth/sessions/:sessionID", s.revokeSession)
	r.GET("/auth/accounts", s.accounts)
	r.DELETE("/auth/accounts/:accountID", s.unlinkAccount)
	r.POST("/auth/email-verification/request", s.requestEmailVerification)
	r.POST("/auth/email-verification/confirm", s.confirmEmailVerification)
	r.POST("/auth/email-change/request", s.requestEmailChange)
	r.POST("/auth/email-change/confirm", s.confirmEmailChange)
	r.GET("/admin/stats", s.adminStats)
	r.GET("/admin/users", s.adminUsers)
	r.PATCH("/admin/users/:userID", s.updateAdminUser)
	r.GET("/admin/workspaces", s.adminWorkspaces)
	r.GET("/admin/configuration", s.adminConfiguration)
	r.PATCH("/admin/configuration", s.updateAdminConfiguration)
	r.GET("/admin/services", s.adminServices)
	r.PATCH("/admin/services/:service", s.saveService)
	r.POST("/admin/email/test", s.testEmail)
	r.POST("/admin/storage/test", s.testStorage)
	r.GET("/workspaces", s.workspaces)
	r.POST("/workspaces", s.createWorkspace)
	r.GET("/workspaces/availability", s.workspaceAvailability)
	r.GET("/workspaces/:workspaceID", s.workspace)
	r.PATCH("/workspaces/:workspaceID", s.updateWorkspace)
	r.DELETE("/workspaces/:workspaceID", s.deleteWorkspace)
	r.POST("/workspaces/:workspaceID/leave", s.removeWorkspaceMember)
	r.POST("/workspaces/:workspaceID/join", s.acceptInvitation)
	r.GET("/workspaces/:workspaceID/members", s.workspaceMembers)
	r.POST("/workspaces/:workspaceID/members", s.addWorkspaceMember)
	r.PATCH("/workspaces/:workspaceID/members/:memberID", s.updateWorkspaceMember)
	r.DELETE("/workspaces/:workspaceID/members/:memberID", s.removeWorkspaceMember)
	r.GET("/workspaces/:workspaceID/invitations", s.invitations)
	r.POST("/workspaces/:workspaceID/invitations", s.createInvitation)
	r.DELETE("/workspaces/:workspaceID/invitations/:invitationID", s.revokeInvitation)
	r.POST("/invitations/accept", s.acceptInvitation)
	r.GET("/workspaces/:workspaceID/labels", s.labels)
	r.POST("/workspaces/:workspaceID/labels", s.createLabel)
	r.POST("/workspaces/:workspaceID/labels/bulk", s.bulkLabels)
	r.PATCH("/workspaces/:workspaceID/labels/:labelID", s.updateLabel)
	r.DELETE("/workspaces/:workspaceID/labels/:labelID", s.deleteLabel)
	p := r.Group("/workspaces/:workspaceID/projects")
	p.GET("", s.projects)
	p.POST("", s.createProject)
	p.GET("/availability", s.projectAvailability)
	p.GET("/:projectID", s.project)
	p.PATCH("/:projectID", s.updateProject)
	p.DELETE("/:projectID", s.deleteProject)
	p.POST("/:projectID/join", s.joinProject)
	p.POST("/:projectID/leave", s.removeProjectMember)
	p.GET("/:projectID/members", s.projectMembers)
	p.POST("/:projectID/members", s.addProjectMember)
	p.PATCH("/:projectID/members/:memberID", s.updateProjectMember)
	p.DELETE("/:projectID/members/:memberID", s.removeProjectMember)
	p.GET("/:projectID/states", s.states)
	p.POST("/:projectID/states", s.createState)
	p.PATCH("/:projectID/states/:stateID", s.updateState)
	p.DELETE("/:projectID/states/:stateID", s.deleteState)
	p.GET("/:projectID/labels", s.labels)
	p.POST("/:projectID/labels", s.createLabel)
	p.POST("/:projectID/labels/bulk", s.bulkLabels)
	p.PATCH("/:projectID/labels/:labelID", s.updateLabel)
	p.DELETE("/:projectID/labels/:labelID", s.deleteLabel)
}

func (s *Server) RequireAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, err := httpapi.Actor(c); err != nil {
			httpapi.Fail(c, err)
			return
		}
		c.Next()
	}
}
func (s *Server) scope(c *gin.Context, project bool, role identity.Role) (identity.Scope, error) {
	a, err := httpapi.Actor(c)
	if err != nil {
		return identity.Scope{}, err
	}
	w, err := httpapi.UUIDParam(c, "workspaceID")
	if err != nil {
		return identity.Scope{}, err
	}
	if project {
		p, err := httpapi.UUIDParam(c, "projectID")
		if err != nil {
			return identity.Scope{}, err
		}
		return s.Deps.Policy.Project(c.Request.Context(), a, w, p, role)
	}
	return s.Deps.Policy.Workspace(c.Request.Context(), a, w, role)
}

func queryObject(ctx context.Context, q database.DBTX, query string, args ...any) (map[string]any, error) {
	var raw []byte
	if err := q.QueryRowContext(ctx, query, args...).Scan(&raw); err != nil {
		if err == sql.ErrNoRows {
			return nil, apperror.NotFound()
		}
		return nil, err
	}
	var result map[string]any
	err := json.Unmarshal(raw, &result)
	return result, err
}
func queryList(ctx context.Context, q database.DBTX, query string, args ...any) ([]map[string]any, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []map[string]any{}
	for rows.Next() {
		var raw []byte
		var item map[string]any
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &item); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}
func reply(c *gin.Context, status int, value any, err error) {
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.JSON(c, status, value)
}
func affected(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return apperror.NotFound()
	}
	return nil
}
func validName(name string) bool     { return len(strings.TrimSpace(name)) > 0 && len(name) <= 255 }
func validTimezone(zone string) bool { _, err := time.LoadLocation(zone); return err == nil }

var slugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
var identifierPattern = regexp.MustCompile(`^[A-Z][A-Z0-9]{0,11}$`)
var colorPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func patch(ctx context.Context, q database.DBTX, table string, id uuid.UUID, scope string, args []any, values map[string]any) error {
	if len(values) == 0 {
		return apperror.Invalid("No changes were provided")
	}
	sets := []string{"updated_at=now()"}
	all := []any{id}
	all = append(all, args...)
	for key, value := range values {
		all = append(all, value)
		sets = append(sets, fmt.Sprintf("%s=$%d", key, len(all)))
	}
	return affected(q.ExecContext(ctx, fmt.Sprintf("UPDATE %s SET %s WHERE id=$1 AND deleted_at IS NULL %s", table, strings.Join(sets, ","), scope), all...))
}
func noContent(c *gin.Context, err error) {
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
