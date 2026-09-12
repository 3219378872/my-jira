package automation

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"my-jira/apps/api/internal/platform/apperror"
	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/identity"
	"my-jira/apps/api/internal/support/data"
	"my-jira/apps/api/internal/support/testutil"
)

func reconcileProjects(t *testing.T) (*testutil.Fixture, uuid.UUID, uuid.UUID) {
	t.Helper()
	f := testutil.New(t)
	invalid := uuid.MustParse("00000000-0000-0000-0000-000000000010")
	valid := uuid.MustParse("00000000-0000-0000-0000-000000000020")
	p := DefaultPolicy()
	p.Enabled, p.MinIntervalSeconds, p.MaxRounds = true, 0, 1
	p.AllowedKinds = []string{"forecast"}
	for _, id := range []uuid.UUID{invalid, valid} {
		f.Exec(t, `INSERT INTO projects(id,workspace_id,name,identifier,network) VALUES($1,$2,$3,$3,'private')`, id, f.WorkspaceID, "RECONCILE"+id.String()[34:])
		f.Exec(t, `INSERT INTO project_members(id,workspace_id,project_id,user_id,role) VALUES($1,$2,$3,$4,20)`, uuid.New(), f.WorkspaceID, id, f.OwnerID)
		f.Exec(t, `INSERT INTO automation_policies(project_id,workspace_id,enabled,authorized_by,config) VALUES($1,$2,true,$3,$4::jsonb)`, id, f.WorkspaceID, f.OwnerID, string(raw(p)))
		f.Exec(t, `SELECT planning_emit_event($1,$2,'test.configuration',$2)`, f.WorkspaceID, id)
	}
	return f, invalid, valid
}

func TestReconcileSkipsUnavailablePoliciesAndQueuesLaterProject(t *testing.T) {
	for _, kind := range []string{"deleted_project", "removed_admin", "demoted_admin"} {
		t.Run(kind, func(t *testing.T) {
			f, invalid, valid := reconcileProjects(t)
			switch kind {
			case "deleted_project":
				f.Exec(t, `UPDATE projects SET deleted_at=now() WHERE id=$1`, invalid)
			case "removed_admin":
				f.Exec(t, `UPDATE project_members SET is_active=false WHERE project_id=$1 AND user_id=$2`, invalid, f.OwnerID)
			case "demoted_admin":
				f.Exec(t, `UPDATE project_members SET role=15 WHERE project_id=$1 AND user_id=$2`, invalid, f.OwnerID)
			}
			if err := Reconcile(context.Background(), f.Deps); err != nil {
				t.Fatalf("unavailable policy stopped reconciliation: %v", err)
			}
			var invalidRuns, validRuns, queued int
			if err := f.DB.SQL.QueryRow(`SELECT count(*) FILTER(WHERE project_id=$1),count(*) FILTER(WHERE project_id=$2),count(*) FILTER(WHERE project_id=$2 AND status='queued') FROM automation_runs WHERE project_id IN ($1,$2)`, invalid, valid).Scan(&invalidRuns, &validRuns, &queued); err != nil {
				t.Fatal(err)
			}
			if invalidRuns != 0 || validRuns != 1 || queued != 1 {
				t.Fatalf("stale policy affected another project: invalid=%d valid=%d queued=%d", invalidRuns, validRuns, queued)
			}
			if err := Reconcile(context.Background(), f.Deps); err != nil {
				t.Fatalf("the unchanged stale policy failed the next tick: %v", err)
			}
		})
	}
}

func TestReconcileReportsInvalidConfigurationAndStillProcessesLaterProject(t *testing.T) {
	f, invalid, valid := reconcileProjects(t)
	f.Exec(t, `UPDATE projects SET timezone='Invalid/ProjectZone' WHERE id=$1`, invalid)
	err := Reconcile(context.Background(), f.Deps)
	var problem *data.Error
	if !errors.As(err, &problem) || problem.Status != 400 {
		t.Fatalf("non-scope validation failure was swallowed: %v", err)
	}
	var queued int
	if err := f.DB.SQL.QueryRow(`SELECT count(*) FROM automation_runs WHERE project_id=$1 AND status='queued'`, valid).Scan(&queued); err != nil || queued != 1 {
		t.Fatalf("one invalid configuration starved the valid project: queued=%d error=%v", queued, err)
	}
}

func TestReconcileRetainsDatabaseErrorsFromAuthorizationQueries(t *testing.T) {
	f, _, _ := reconcileProjects(t)
	// Rename only this fixture's owned relation to reproduce an operational
	// database failure at the project membership authorization query.
	f.Exec(t, `ALTER TABLE project_members RENAME TO unavailable_project_members`)
	err := Reconcile(context.Background(), f.Deps)
	var databaseError *pgconn.PgError
	var scopeError *apperror.Error
	if !errors.As(err, &databaseError) || databaseError.Code != "42P01" || errors.As(err, &scopeError) {
		t.Fatalf("database failure was classified as a stale grant: %v", err)
	}
}

func TestInsertRunRetainsDatabaseErrorsFromAuthorizationQueries(t *testing.T) {
	f, _, valid := reconcileProjects(t)
	f.Exec(t, `ALTER TABLE project_members RENAME TO unavailable_project_members`)
	err := f.DB.WithinTx(context.Background(), func(q database.DBTX) error {
		_, err := NewService(f.Deps).insertRun(context.Background(), q, identity.Scope{
			Actor: identity.Actor{UserID: f.OwnerID}, WorkspaceID: f.WorkspaceID, ProjectID: valid,
		}, Request{Kind: "forecast", IdempotencyKey: "database-failure"}, false)
		return err
	})
	var databaseError *pgconn.PgError
	var conflict *data.Error
	if !errors.As(err, &databaseError) || databaseError.Code != "42P01" || errors.As(err, &conflict) {
		t.Fatalf("enqueue discarded the original authorization query failure: %v", err)
	}
}
