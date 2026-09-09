package identity

import (
	"context"
	"database/sql"

	"github.com/google/uuid"
	"my-jira/apps/api/internal/platform/apperror"
	"my-jira/apps/api/internal/platform/database"
)

type Actor struct {
	UserID           uuid.UUID
	SessionID        uuid.UUID
	IsAdmin          bool
	TokenWorkspaceID uuid.UUID
}
type Role int

const (
	Guest  Role = 5
	Member Role = 15
	Admin  Role = 20
)

type Scope struct {
	Actor           Actor
	WorkspaceID     uuid.UUID
	ProjectID       uuid.UUID
	Role            Role
	GuestCanViewAll bool
}
type Policy interface {
	Workspace(context.Context, Actor, uuid.UUID, Role) (Scope, error)
	Project(context.Context, Actor, uuid.UUID, uuid.UUID, Role) (Scope, error)
}
type SQLPolicy struct{ DB database.DBTX }

func (p *SQLPolicy) Workspace(ctx context.Context, actor Actor, workspaceID uuid.UUID, minimum Role) (Scope, error) {
	scope := Scope{Actor: actor, WorkspaceID: workspaceID}
	if actor.UserID == uuid.Nil {
		return scope, apperror.Unauthorized()
	}
	if actor.TokenWorkspaceID != uuid.Nil && actor.TokenWorkspaceID != workspaceID {
		return scope, apperror.NotFound()
	}
	err := p.DB.QueryRowContext(ctx, `SELECT m.role FROM workspace_members m JOIN workspaces w ON w.id=m.workspace_id
		JOIN users u ON u.id=m.user_id WHERE m.workspace_id=$1 AND m.user_id=$2 AND m.is_active AND u.is_active
		AND m.deleted_at IS NULL AND w.deleted_at IS NULL AND u.deleted_at IS NULL`, workspaceID, actor.UserID).Scan(&scope.Role)
	if err == sql.ErrNoRows {
		return scope, apperror.NotFound()
	}
	if err != nil {
		return scope, err
	}
	if scope.Role < minimum {
		return scope, apperror.Forbidden()
	}
	return scope, nil
}

func (p *SQLPolicy) Project(ctx context.Context, actor Actor, workspaceID, projectID uuid.UUID, minimum Role) (Scope, error) {
	scope, err := p.Workspace(ctx, actor, workspaceID, Guest)
	if err != nil {
		return scope, err
	}
	scope.ProjectID = projectID
	var network string
	var role sql.NullInt64
	err = p.DB.QueryRowContext(ctx, `SELECT p.network,m.role,p.guest_can_view_all FROM projects p LEFT JOIN project_members m
		ON m.project_id=p.id AND m.user_id=$3 AND m.is_active AND m.deleted_at IS NULL
		WHERE p.id=$1 AND p.workspace_id=$2 AND p.deleted_at IS NULL`, projectID, workspaceID, actor.UserID).Scan(&network, &role, &scope.GuestCanViewAll)
	if err == sql.ErrNoRows {
		return scope, apperror.NotFound()
	}
	if err != nil {
		return scope, err
	}
	if !role.Valid && network != "public" {
		return scope, apperror.NotFound()
	}
	if role.Valid && scope.Role != Admin && scope.Role != Guest {
		scope.Role = Role(role.Int64)
	}
	if scope.Role < minimum {
		return scope, apperror.Forbidden()
	}
	return scope, nil
}

func ValidRole(role Role) bool { return role == Guest || role == Member || role == Admin }
