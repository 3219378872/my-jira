package data

import (
	"context"
	"database/sql"

	"github.com/google/uuid"
	"my-jira/apps/api/internal/platform/apperror"
	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/identity"
)

// PageProjectScope requires explicit active project membership for project
// pages, even when the project is public. With a transaction q, the shared
// locks keep member, project, workspace and account changes ordered against
// document operations until commit. A nil project selects workspace pages.
func PageProjectScope(ctx context.Context, q database.DBTX, actor identity.Actor, workspaceID, projectID uuid.UUID, minimum identity.Role) (identity.Scope, error) {
	scope := identity.Scope{Actor: actor, WorkspaceID: workspaceID, ProjectID: projectID}
	if actor.UserID == uuid.Nil {
		return scope, apperror.Unauthorized()
	}
	if actor.TokenWorkspaceID != uuid.Nil && actor.TokenWorkspaceID != workspaceID {
		return scope, apperror.NotFound()
	}
	var workspaceRole, projectRole identity.Role
	query := `SELECT wm.role FROM workspace_members wm JOIN workspaces w ON w.id=wm.workspace_id JOIN users u ON u.id=wm.user_id WHERE wm.workspace_id=$1 AND wm.user_id=$2 AND wm.is_active AND wm.deleted_at IS NULL AND w.deleted_at IS NULL AND u.is_active AND u.deleted_at IS NULL FOR SHARE OF wm,w,u`
	args := []any{workspaceID, actor.UserID}
	var err error
	if projectID == uuid.Nil {
		err = q.QueryRowContext(ctx, query, args...).Scan(&workspaceRole)
		projectRole = workspaceRole
	} else {
		query = `SELECT wm.role,pm.role,p.guest_can_view_all FROM workspace_members wm JOIN workspaces w ON w.id=wm.workspace_id JOIN users u ON u.id=wm.user_id JOIN projects p ON p.workspace_id=w.id AND p.id=$3 JOIN project_members pm ON pm.project_id=p.id AND pm.workspace_id=w.id AND pm.user_id=u.id WHERE wm.workspace_id=$1 AND wm.user_id=$2 AND wm.is_active AND wm.deleted_at IS NULL AND w.deleted_at IS NULL AND u.is_active AND u.deleted_at IS NULL AND p.deleted_at IS NULL AND pm.is_active AND pm.deleted_at IS NULL FOR SHARE OF wm,w,u,p,pm`
		err = q.QueryRowContext(ctx, query, workspaceID, actor.UserID, projectID).Scan(&workspaceRole, &projectRole, &scope.GuestCanViewAll)
	}
	if err == sql.ErrNoRows {
		return scope, apperror.NotFound()
	}
	if err != nil {
		return scope, err
	}
	scope.Role = projectRole
	if workspaceRole == identity.Guest {
		scope.Role = identity.Guest
	}
	if scope.Role < minimum {
		return scope, apperror.Forbidden()
	}
	return scope, nil
}

// VisiblePage is a SQL predicate for current page access. alias and parameter
// expressions are source controlled; no request text should be interpolated.
func VisiblePage(alias, workspaceParameter, actorParameter string) string {
	return alias + ".workspace_id=" + workspaceParameter + " AND " + alias + ".deleted_at IS NULL AND (NOT " + alias + ".is_private OR " + alias + ".owner_id=" + actorParameter + ") AND EXISTS(SELECT 1 FROM workspace_members pwm JOIN workspaces pws ON pws.id=pwm.workspace_id AND pws.deleted_at IS NULL JOIN users pu ON pu.id=pwm.user_id AND pu.is_active AND pu.deleted_at IS NULL WHERE pwm.workspace_id=" + workspaceParameter + " AND pwm.user_id=" + actorParameter + " AND pwm.is_active AND pwm.deleted_at IS NULL) AND (" + alias + ".project_id IS NULL OR EXISTS(SELECT 1 FROM project_members ppm JOIN projects ppr ON ppr.id=ppm.project_id AND ppr.deleted_at IS NULL JOIN workspace_members pgwm ON pgwm.workspace_id=ppm.workspace_id AND pgwm.user_id=ppm.user_id AND pgwm.is_active AND pgwm.deleted_at IS NULL WHERE ppm.workspace_id=" + workspaceParameter + " AND ppm.project_id=" + alias + ".project_id AND ppm.user_id=" + actorParameter + " AND ppm.is_active AND ppm.deleted_at IS NULL AND ((pgwm.role<>5 AND ppm.role>=15) OR ppr.guest_can_view_all OR " + alias + ".owner_id=" + actorParameter + ")))"
}
