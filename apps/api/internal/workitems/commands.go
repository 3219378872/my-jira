package workitems

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"my-jira/apps/api/internal/platform"
	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/httpapi"
	"my-jira/apps/api/internal/platform/identity"
)

// Commands is the shared work-item domain entrypoint for HTTP and background
// automation. The caller owns the transaction; every mutation rechecks current
// authorization after waiting for domain/entity locks.
type Commands struct{ h handler }

func NewCommands(deps platform.Dependencies) *Commands { return &Commands{handler{deps}} }

func commandContext(ctx context.Context, actor identity.Actor) *gin.Context {
	r, _ := http.NewRequestWithContext(ctx, http.MethodPost, "/", nil)
	c := &gin.Context{Request: r}
	c.Set(httpapi.ActorKey, actor)
	return c
}

func commandError(err error) error {
	var p problem
	if errors.As(err, &p) {
		return httpapi.NewError(p.status, p.code, p.message)
	}
	return err
}

func (s *Commands) Create(ctx context.Context, q database.DBTX, scope identity.Scope, input map[string]json.RawMessage) (map[string]any, error) {
	// Intake invokes its separate submission command internally; generic work
	// creation always requires current membership, regardless of supplied scope.
	scope.Role = identity.Member
	item, err := s.h.createItem(commandContext(ctx, scope.Actor), q, scope, input)
	return item, commandError(err)
}

func (s *Commands) Update(ctx context.Context, q database.DBTX, scope identity.Scope, id uuid.UUID, input map[string]json.RawMessage, requireVersion bool) (map[string]any, error) {
	item, err := s.h.updateItem(commandContext(ctx, scope.Actor), q, scope, id, input, requireVersion)
	return item, commandError(err)
}

func (s *Commands) Delete(ctx context.Context, q database.DBTX, scope identity.Scope, id uuid.UUID) error {
	return commandError(s.h.deleteItem(commandContext(ctx, scope.Actor), q, scope, id))
}

func (s *Commands) Load(ctx context.Context, q database.DBTX, scope identity.Scope, id uuid.UUID, lock bool) (map[string]any, error) {
	item, err := loadIssue(commandContext(ctx, scope.Actor), q, scope, id, lock)
	return item, commandError(err)
}

// SelectSQL projects a work item already constrained by the caller's WHERE
// clause. ActorParameter additionally protects nested children/dependencies.
func SelectSQL(actorParameter string) string { return issueSelectFor(actorParameter) }

// LockProject uses the same graph lock as existing hierarchy and dependency
// writes. Acquire this before item locks and current membership rechecks.
func LockProject(ctx context.Context, q database.DBTX, projectID uuid.UUID) error {
	_, err := q.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, projectID.String())
	return err
}

// CurrentScope serializes with membership/config changes. Call after any lock
// wait that could otherwise leave the request's authorization stale.
func CurrentScope(ctx context.Context, q database.DBTX, actor identity.Actor, wid, pid uuid.UUID, role identity.Role) (identity.Scope, error) {
	if _, err := q.ExecContext(ctx, `SELECT w.id FROM workspaces w JOIN workspace_members wm ON wm.workspace_id=w.id JOIN users u ON u.id=wm.user_id WHERE w.id=$1 AND u.id=$2 FOR SHARE OF w,wm,u`, wid, actor.UserID); err != nil {
		return identity.Scope{}, err
	}
	if _, err := q.ExecContext(ctx, `SELECT id FROM projects WHERE id=$1 AND workspace_id=$2 FOR SHARE`, pid, wid); err != nil {
		return identity.Scope{}, err
	}
	if _, err := q.ExecContext(ctx, `SELECT id FROM project_members WHERE workspace_id=$1 AND project_id=$2 AND user_id=$3 FOR SHARE`, wid, pid, actor.UserID); err != nil {
		return identity.Scope{}, err
	}
	if actor.SessionID != uuid.Nil {
		var sessionID uuid.UUID
		err := q.QueryRowContext(ctx, `SELECT id FROM sessions WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL AND expires_at>clock_timestamp() AND deleted_at IS NULL FOR SHARE`, actor.SessionID, actor.UserID).Scan(&sessionID)
		if errors.Is(err, sql.ErrNoRows) {
			return identity.Scope{}, httpapi.NewError(401, "session_expired", "The session is no longer active")
		}
		if err != nil {
			return identity.Scope{}, err
		}
	}
	if actor.TokenWorkspaceID != uuid.Nil {
		var tokenID uuid.UUID
		err := q.QueryRowContext(ctx, `SELECT id FROM api_tokens WHERE id=$1 AND user_id=$2 AND workspace_id=$3 AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at>clock_timestamp()) AND deleted_at IS NULL FOR SHARE`, actor.TokenID, actor.UserID, wid).Scan(&tokenID)
		if errors.Is(err, sql.ErrNoRows) {
			return identity.Scope{}, httpapi.NewError(401, "token_expired", "The API token is no longer active")
		}
		if err != nil {
			return identity.Scope{}, err
		}
	}
	policy := identity.SQLPolicy{DB: q}
	s, err := policy.Project(ctx, actor, wid, pid, role)
	if err == nil && role >= identity.Member {
		err = EnsureWritableProject(ctx, q, wid, pid)
	}
	if err == nil {
		_, err = q.ExecContext(ctx, `SELECT set_config('myjira.change_actor',$1,true)`, actor.UserID.String())
	}
	return s, err
}

// EnsureWritableProject keeps the requirements mutation contract aligned with
// the application's existing archived-project read-only behavior.
func EnsureWritableProject(ctx context.Context, q database.DBTX, wid, pid uuid.UUID) error {
	var writable bool
	if err := q.QueryRowContext(ctx, `SELECT archived_at IS NULL FROM projects WHERE id=$1 AND workspace_id=$2 AND deleted_at IS NULL FOR SHARE`, pid, wid).Scan(&writable); err != nil {
		return err
	}
	if !writable {
		return httpapi.NewError(409, "project_archived", "Restore the project before changing its work")
	}
	return nil
}

// MutationFingerprint includes durable main-row and relation/comment facts;
// unlike the short version history, deletion of an added comment cannot erase
// evidence that a person edited the object after an automation batch.
func MutationFingerprint(ctx context.Context, q database.DBTX, wid, pid, id uuid.UUID) (string, error) {
	var raw []byte
	err := q.QueryRowContext(ctx, `SELECT jsonb_build_object('version',w.version,'deleted_at',w.deleted_at,'revision',COALESCE((SELECT max(h.revision) FROM work_item_history h WHERE h.workspace_id=$1 AND h.project_id=$2 AND h.work_item_id=w.id),0)) FROM work_items w WHERE w.workspace_id=$1 AND w.project_id=$2 AND w.id=$3`, wid, pid, id).Scan(&raw)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
