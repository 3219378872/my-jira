package automation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"my-jira/apps/api/internal/platform"
	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/identity"
	"my-jira/apps/api/internal/support/data"
)

// RunScheduler reconciles durable facts after worker restarts. It never runs a
// model itself; every chosen stable input is persisted through the outbox.
func RunScheduler(ctx context.Context, d platform.Dependencies) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if e := Reconcile(ctx, d); e != nil {
				slog.Error("automation reconciliation failed", "error", e)
			}
		}
	}
}
func Reconcile(ctx context.Context, d platform.Dependencies) error {
	rows, e := d.DB.SQL.QueryContext(ctx, `SELECT workspace_id,project_id,authorized_by FROM automation_policies WHERE enabled ORDER BY project_id LIMIT 1000`)
	if e != nil {
		return e
	}
	scopes := []identity.Scope{}
	for rows.Next() {
		var s identity.Scope
		if e = rows.Scan(&s.WorkspaceID, &s.ProjectID, &s.Actor.UserID); e != nil {
			break
		}
		scopes = append(scopes, s)
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		return e
	}
	service := NewService(d)
	var failures []error
	for _, scope := range scopes {
		e = d.DB.WithinTx(ctx, func(q database.DBTX) error {
			if e := lockProject(ctx, q, scope.WorkspaceID, scope.ProjectID); e != nil {
				// A policy can outlive its soft-deleted project. This specific
				// lookup returning no rows is an unavailable scope, not a failure
				// that should prevent other projects from being reconciled.
				if errors.Is(e, sql.ErrNoRows) {
					return nil
				}
				return e
			}
			current, e := data.PageProjectScope(ctx, q, scope.Actor, scope.WorkspaceID, scope.ProjectID, identity.Admin)
			if e != nil {
				if unavailableAuthority(e) {
					return nil
				}
				return e
			}
			p, e := policyAt(ctx, q, scope.ProjectID, true)
			if e != nil {
				return e
			}
			if !p.Enabled {
				return nil
			}
			var busy bool
			if e = q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM automation_runs WHERE project_id=$1 AND (status IN ('queued','running') OR created_at>now()-make_interval(secs=>$2)))`, scope.ProjectID, p.MinIntervalSeconds).Scan(&busy); e != nil {
				return e
			}
			if busy {
				return nil
			}
			// Only configured source pages and pages explicitly processed previously
			// are watched. An administrator enabling planning does not classify every
			// ordinary project document as a PRD.
			if contains(p.AllowedKinds, "decompose") {
				var page uuid.UUID
				var version int64
				e = q.QueryRowContext(ctx, `SELECT p.id,p.version FROM pages p WHERE p.workspace_id=$1 AND p.project_id=$2 AND p.deleted_at IS NULL AND p.archived_at IS NULL AND NOT p.is_private AND (p.id::text IN (SELECT jsonb_array_elements_text($3::jsonb)) OR EXISTS(SELECT 1 FROM automation_runs r WHERE r.project_id=$2 AND r.kind='decompose' AND r.input->'source'->>'page_id'=p.id::text)) AND NOT EXISTS(SELECT 1 FROM automation_runs r WHERE r.project_id=$2 AND r.kind='decompose' AND r.input->'source'->>'page_id'=p.id::text AND (r.input->'source'->>'revision')::bigint=p.version) ORDER BY p.updated_at,p.id LIMIT 1`, scope.WorkspaceID, scope.ProjectID, string(raw(p.AllowedPageIDs))).Scan(&page, &version)
				if e == nil {
					_, e = service.insertRun(ctx, q, current, Request{Kind: "decompose", IdempotencyKey: fmt.Sprintf("page:%s:%d:policy:%d", page, version, p.Version), Source: Source{PageID: &page, Revision: version}, Cause: "source_revision"}, false)
					return e
				}
				if e != sql.ErrNoRows {
					return e
				}
			}
			var revision int64
			var cause string
			e = q.QueryRowContext(ctx, `SELECT revision,cause FROM project_events WHERE project_id=$1 ORDER BY revision DESC LIMIT 1`, scope.ProjectID).Scan(&revision, &cause)
			if e != nil && e != sql.ErrNoRows {
				return e
			}
			// AI-origin revisions continue only through the bounded causal follow-up
			// already queued by the applying run; they cannot restart at round zero.
			if e == nil && !strings.HasPrefix(cause, "automation:") {
				kind := "risk"
				if contains(p.AllowedKinds, "schedule") {
					kind = "schedule"
				} else if !contains(p.AllowedKinds, "risk") {
					kind = "forecast"
				}
				if contains(p.AllowedKinds, kind) {
					var attempted bool
					if e = q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM automation_runs WHERE project_id=$1 AND kind=$2 AND (input->>'project_revision')::bigint=$3 AND policy_version=$4)`, scope.ProjectID, kind, revision, p.Version).Scan(&attempted); e != nil {
						return e
					}
					if !attempted {
						_, e = service.insertRun(ctx, q, current, Request{Kind: kind, IdempotencyKey: fmt.Sprintf("revision:%d:%s:policy:%d", revision, kind, p.Version), Cause: "project_revision"}, false)
						return e
					}
				}
			}
			if contains(p.AllowedKinds, "efficiency") {
				var due bool
				if e = q.QueryRowContext(ctx, `SELECT NOT EXISTS(SELECT 1 FROM automation_runs WHERE project_id=$1 AND kind='efficiency' AND created_at>now()-interval '14 days') AND NOT EXISTS(SELECT 1 FROM automation_improvements WHERE project_id=$1 AND status IN ('proposed','observing'))`, scope.ProjectID).Scan(&due); e != nil {
					return e
				}
				if due {
					_, e = service.insertRun(ctx, q, current, Request{Kind: "efficiency", IdempotencyKey: "periodic-efficiency:" + time.Now().UTC().Format(time.DateOnly), Cause: "observation_window"}, false)
					return e
				}
			}
			return nil
		})
		if e != nil {
			failures = append(failures, fmt.Errorf("reconcile project %s: %w", scope.ProjectID, e))
			if ctx.Err() != nil {
				break
			}
		}
	}
	return errors.Join(failures...)
}
