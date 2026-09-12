package automation

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"my-jira/apps/api/internal/documents"
	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/httpapi"
	"my-jira/apps/api/internal/platform/identity"
	"my-jira/apps/api/internal/support/testutil"
	"my-jira/apps/api/internal/workitems"
)

func automationFixture(t *testing.T) (*testutil.Fixture, *Service, identity.Scope, Policy) {
	f := testutil.New(t)
	service := NewService(f.Deps)
	scope := identity.Scope{WorkspaceID: f.WorkspaceID, ProjectID: f.ProjectID, Actor: identity.Actor{UserID: f.OwnerID}, Role: identity.Admin}
	p := DefaultPolicy()
	p.Enabled = true
	p.MinIntervalSeconds = 0
	p.MaxRounds = 1
	response := testutil.Request(t, f.Router(Register), f.OwnerID, "PUT", f.Prefix()+"/automation/policy", p, 200)
	object := testutil.Object(t, response)
	p.Version = int64(object["version"].(float64))
	p.AuthorizedBy = f.OwnerID
	return f, service, scope, p
}
func queue(t *testing.T, f *testutil.Fixture, b Request) uuid.UUID {
	t.Helper()
	response := testutil.Request(t, f.Router(Register), f.OwnerID, "POST", f.Prefix()+"/automation/runs", b, 202)
	return uuid.MustParse(testutil.Object(t, response)["id"].(string))
}
func TestRunSnapshotDeduplicationColdStartAndCancellation(t *testing.T) {
	f, s, _, p := automationFixture(t)
	f.Issue(t, f.ProjectID, f.StateID, f.OwnerID, "Historical fixture", 1)
	b := Request{Kind: "forecast", IdempotencyKey: "forecast-a"}
	id := queue(t, f, b)
	if repeat := queue(t, f, b); repeat != id {
		t.Fatal("idempotent replay created another run")
	}
	b.IdempotencyKey = "forecast-b"
	if repeat := queue(t, f, b); repeat != id {
		t.Fatal("same source input created another run")
	}
	if e := s.Run(context.Background(), id); e != nil {
		t.Fatal(e)
	}
	run, e := loadRun(context.Background(), f.DB.SQL, id, false)
	if e != nil {
		t.Fatal(e)
	}
	if run.Status != "completed" {
		t.Fatalf("cold start run status: %+v", run)
	}
	var status string
	if e = f.DB.SQL.QueryRow(`SELECT status FROM automation_forecasts WHERE run_id=$1`, id).Scan(&status); e != nil || status != "insufficient_data" {
		t.Fatalf("forecast evidence: %s %v", status, e)
	}
	p.Enabled = false
	testutil.Request(t, f.Router(Register), f.OwnerID, "PUT", f.Prefix()+"/automation/policy", p, 200)
	testutil.Request(t, f.Router(Register), f.OwnerID, "POST", f.Prefix()+"/automation/runs", Request{Kind: "risk", IdempotencyKey: "disabled"}, 409)
	testutil.Request(t, f.Router(Register), f.GuestID, "GET", f.Prefix()+"/automation/runs", nil, 403)
	testutil.Request(t, f.Router(Register), f.OutsiderID, "GET", f.Prefix()+"/automation/runs", nil, 404)
}
func TestPolicyDisableQueuedRunAndStaleInputDoNotWrite(t *testing.T) {
	f, s, _, p := automationFixture(t)
	item := f.Issue(t, f.ProjectID, f.StateID, f.OwnerID, "Mutable", 1)
	first := queue(t, f, Request{Kind: "efficiency", IdempotencyKey: "stale"})
	f.Exec(t, `UPDATE work_items SET name='Human changed',version=version+1 WHERE id=$1`, item)
	if e := s.Run(context.Background(), first); e != nil {
		t.Fatal(e)
	}
	run, e := loadRun(context.Background(), f.DB.SQL, first, false)
	if e != nil || run.Status != "blocked" {
		t.Fatalf("stale input applied: %+v %v", run, e)
	}
	second := queue(t, f, Request{Kind: "risk", IdempotencyKey: "disabled-queued"})
	p.Enabled = false
	testutil.Request(t, f.Router(Register), f.OwnerID, "PUT", f.Prefix()+"/automation/policy", p, 200)
	if e := s.Run(context.Background(), second); e != nil {
		t.Fatal(e)
	}
	run, e = loadRun(context.Background(), f.DB.SQL, second, false)
	if e != nil || run.Status != "cancelled" {
		t.Fatalf("policy disable did not cancel: %+v %v", run, e)
	}
	var count int
	_ = f.DB.SQL.QueryRow(`SELECT count(*) FROM work_items WHERE project_id=$1`, f.ProjectID).Scan(&count)
	if count != 1 {
		t.Fatal("blocked worker wrote a task")
	}
}
func TestExternalAtomicIdempotenceUndoAndDurableCommentProtection(t *testing.T) {
	f, s, scope, _ := automationFixture(t)
	ctx := context.Background()
	validate := func(context.Context, database.DBTX) error { return nil }
	first, e := ApplyExternal(ctx, f.Deps, scope, ExternalInput{Kind: "quality", IdempotencyKey: "fix-a", Title: "Repair", Description: "<p>Evidence-backed repair</p>", Evidence: map[string]any{"fixture": "known failure"}}, validate)
	if e != nil {
		t.Fatal(e)
	}
	if first.Status != "applied" || len(first.ItemIDs) != 1 {
		t.Fatalf("missing action: %+v", first)
	}
	repeated, e := ApplyExternal(ctx, f.Deps, scope, ExternalInput{Kind: "quality", IdempotencyKey: "fix-a", Title: "Repair", Description: "<p>Evidence-backed repair</p>", Evidence: map[string]any{"fixture": "known failure"}}, validate)
	if e != nil || repeated.RunID != first.RunID || len(repeated.ItemIDs) != 1 {
		t.Fatalf("external retry duplicated: %+v %v", repeated, e)
	}
	conflicts, e := s.Undo(ctx, scope, first.RunID)
	if e != nil || len(conflicts) > 0 {
		t.Fatalf("safe undo failed: %v %+v", e, conflicts)
	}
	var deleted bool
	_ = f.DB.SQL.QueryRow(`SELECT deleted_at IS NOT NULL FROM work_items WHERE id=$1`, first.ItemIDs[0]).Scan(&deleted)
	if !deleted {
		t.Fatal("undo did not remove its newly created task")
	}
	second, e := ApplyExternal(ctx, f.Deps, scope, ExternalInput{Kind: "quality", IdempotencyKey: "fix-b", Title: "Repair with discussion", Description: "<p>Repair</p>", Evidence: map[string]any{"fixture": "other failure"}}, validate)
	if e != nil {
		t.Fatal(e)
	}
	comment := uuid.New()
	f.Exec(t, `INSERT INTO comments(id,workspace_id,project_id,work_item_id,author_id,body_html,body_json) VALUES($1,$2,$3,$4,$5,'<p>Keep this discussion</p>','{}')`, comment, f.WorkspaceID, f.ProjectID, second.ItemIDs[0], f.MemberID)
	f.Exec(t, `UPDATE comments SET deleted_at=now() WHERE id=$1`, comment)
	conflicts, e = s.Undo(ctx, scope, second.RunID)
	if e != nil || len(conflicts) != 1 {
		t.Fatalf("deleted comment history did not protect undo: %+v %v", conflicts, e)
	}
	_ = f.DB.SQL.QueryRow(`SELECT deleted_at IS NOT NULL FROM work_items WHERE id=$1`, second.ItemIDs[0]).Scan(&deleted)
	if deleted {
		t.Fatal("conflicting undo partially deleted user work")
	}
}
func TestExternalCallbackFailureRollsBackTaskRunAndOutbox(t *testing.T) {
	f, _, scope, _ := automationFixture(t)
	marker := errors.New("installation revoked")
	_, e := ApplyExternal(context.Background(), f.Deps, scope, ExternalInput{Kind: "quality", IdempotencyKey: "revoked", Title: "Must not exist", Evidence: map[string]any{"fixture": "revoked"}}, func(context.Context, database.DBTX) error { return marker })
	if !errors.Is(e, marker) {
		t.Fatal(e)
	}
	_, e = ApplyExternal(context.Background(), f.Deps, scope, ExternalInput{Kind: "quality", IdempotencyKey: "rollback", Title: "Must roll back", Evidence: map[string]any{"fixture": "callback"}, OnApplied: func(context.Context, database.DBTX, []uuid.UUID) error { return marker }}, func(context.Context, database.DBTX) error { return nil })
	if !errors.Is(e, marker) {
		t.Fatal(e)
	}
	for _, table := range []string{"work_items", "automation_runs", "planning_change_batches"} {
		var count int
		if e = f.DB.SQL.QueryRow(`SELECT count(*) FROM `+table+` WHERE project_id=$1`, f.ProjectID).Scan(&count); e != nil || count != 0 {
			t.Fatalf("%s leaked a partial transaction: %d %v", table, count, e)
		}
	}
}
func TestExternalReportLimitRejectsSecondFindingWithoutPartialWrites(t *testing.T) {
	f, _, scope, p := automationFixture(t)
	p.MaxChanges = 1
	testutil.Request(t, f.Router(Register), f.OwnerID, "PUT", f.Prefix()+"/automation/policy", p, 200)
	bindingID, reportID := uuid.New(), uuid.New()
	f.Exec(t, `INSERT INTO github_installations(installation_id,account_login) VALUES(5,'test-org')`)
	f.Exec(t, `INSERT INTO github_bindings(id,workspace_id,project_id,installation_id,repository_id,repository,created_by) VALUES($1,$2,$3,5,99,'test-org/repo',$4)`, bindingID, f.WorkspaceID, f.ProjectID, f.OwnerID)
	f.Exec(t, `INSERT INTO quality_reports(id,workspace_id,project_id,binding_id,repository,commit_sha,source_revision,source_at,dimensions,findings,evidence) VALUES($1,$2,$3,$4,'test-org/repo',repeat('a',40),'two-findings',now(),'{}','[{"id":"first"},{"id":"second"}]','{"documents":[]}')`, reportID, f.WorkspaceID, f.ProjectID, bindingID)
	f.Exec(t, `UPDATE github_bindings SET latest_report_id=$2 WHERE id=$1`, bindingID, reportID)
	ctx := context.Background()
	linked := 0
	applyFinding := func(finding string) (ExternalResult, error) {
		return ApplyExternal(ctx, f.Deps, scope, ExternalInput{
			Kind: "quality", IdempotencyKey: "report-limit:" + finding, Title: "Repair " + finding,
			Evidence: map[string]any{"report_id": reportID, "finding_id": finding},
			OnApplied: func(ctx context.Context, q database.DBTX, ids []uuid.UUID) error {
				linked++
				_, err := q.ExecContext(ctx, `INSERT INTO quality_repair_actions(report_id,finding_id,item_ids,status) VALUES($1,$2,$3::jsonb,'applied')`, reportID, finding, string(raw(ids)))
				return err
			},
		}, func(ctx context.Context, q database.DBTX) error {
			return authorizeQualityReport(ctx, q, scope, reportID)
		})
	}
	first, err := applyFinding("first")
	if err != nil || first.Status != "applied" || len(first.ItemIDs) != 1 {
		t.Fatalf("first finding did not use its allowed slot: %+v %v", first, err)
	}
	type persistedState struct {
		Items, Runs, Batches, History, Events, Outbox, Links int
		NextSequence, Revision                               int64
	}
	readState := func() persistedState {
		t.Helper()
		var state persistedState
		err := f.DB.SQL.QueryRow(`SELECT
			(SELECT count(*) FROM work_items WHERE project_id=$1),
			(SELECT count(*) FROM automation_runs WHERE project_id=$1),
			(SELECT count(*) FROM planning_change_batches WHERE project_id=$1),
			(SELECT count(*) FROM work_item_history WHERE project_id=$1),
			(SELECT count(*) FROM project_events WHERE project_id=$1),
			(SELECT count(*) FROM outbox_events),
			(SELECT count(*) FROM quality_repair_actions WHERE report_id=$2),
			(SELECT next_sequence FROM projects WHERE id=$1),
			(SELECT revision FROM project_revisions WHERE project_id=$1)`, f.ProjectID, reportID).Scan(&state.Items, &state.Runs, &state.Batches, &state.History, &state.Events, &state.Outbox, &state.Links, &state.NextSequence, &state.Revision)
		if err != nil {
			t.Fatal(err)
		}
		return state
	}
	before := readState()
	if before.Items != 1 || before.Runs != 1 || before.Batches != 1 || before.Links != 1 {
		t.Fatalf("first action was not committed atomically: %+v", before)
	}
	_, err = applyFinding("second")
	var problem *httpapi.Error
	if !errors.As(err, &problem) || problem.Status != 409 || problem.Message != "This report reached the policy's maximum changes per batch" {
		t.Fatalf("second distinct finding bypassed max_changes=1: %v", err)
	}
	if after := readState(); after != before || linked != 1 {
		t.Fatalf("blocked finding left partial effects: before=%+v after=%+v callbacks=%d", before, after, linked)
	}
	repeated, err := applyFinding("first")
	if err != nil || repeated.RunID != first.RunID || len(repeated.ItemIDs) != 1 || repeated.ItemIDs[0] != first.ItemIDs[0] {
		t.Fatalf("idempotent replay was not retained at the cap: %+v %v", repeated, err)
	}
	if after := readState(); after != before || linked != 1 {
		t.Fatalf("idempotent replay consumed another report slot: before=%+v after=%+v callbacks=%d", before, after, linked)
	}
}
func TestMembershipRevokedWhileWaitingForProjectLock(t *testing.T) {
	f, _, scope, _ := automationFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx, e := f.DB.SQL.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	if e = workitems.LockProject(ctx, tx, f.ProjectID); e != nil {
		t.Fatal(e)
	}
	started := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		close(started)
		_, e := ApplyExternal(ctx, f.Deps, scope, ExternalInput{Kind: "quality", IdempotencyKey: "waiting", Title: "Unauthorized after wait", Evidence: map[string]any{"fixture": "wait"}}, func(context.Context, database.DBTX) error { return nil })
		result <- e
	}()
	<-started
	f.Exec(t, `UPDATE project_members SET is_active=false WHERE project_id=$1 AND user_id=$2`, f.ProjectID, f.OwnerID)
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	select {
	case e := <-result:
		if e == nil {
			t.Fatal("revoked authorization survived a lock wait")
		}
	case <-ctx.Done():
		t.Fatal("lock-wait authorization test timed out")
	}
	var count int
	_ = f.DB.SQL.QueryRow(`SELECT count(*) FROM work_items WHERE project_id=$1`, f.ProjectID).Scan(&count)
	if count != 0 {
		t.Fatal("revoked worker wrote after waiting")
	}
}
func TestConcurrentExternalIdempotence(t *testing.T) {
	f, _, scope, _ := automationFixture(t)
	var wg sync.WaitGroup
	results := make(chan ExternalResult, 2)
	errorsCh := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := ApplyExternal(context.Background(), f.Deps, scope, ExternalInput{Kind: "quality", IdempotencyKey: "concurrent", Title: "One task", Evidence: map[string]any{"fixture": "same"}}, func(context.Context, database.DBTX) error { return nil })
			results <- r
			errorsCh <- e
		}()
	}
	wg.Wait()
	close(results)
	close(errorsCh)
	for e := range errorsCh {
		if e != nil {
			t.Fatal(e)
		}
	}
	var id uuid.UUID
	for result := range results {
		if id != uuid.Nil && id != result.RunID {
			t.Fatal("concurrent retry generated two runs")
		}
		id = result.RunID
	}
	var count int
	_ = f.DB.SQL.QueryRow(`SELECT count(*) FROM work_items WHERE project_id=$1`, f.ProjectID).Scan(&count)
	if count != 1 {
		t.Fatal(count)
	}
}
func TestRunHistoricalInputNoLongerReadableAfterCrossProjectMove(t *testing.T) {
	f, _, _, _ := automationFixture(t)
	id := f.Issue(t, f.ProjectID, f.StateID, f.OwnerID, "Sensitive moved prose", 1)
	run := queue(t, f, Request{Kind: "forecast", IdempotencyKey: "before-move"})
	f.Exec(t, `UPDATE work_items SET project_id=$2,state_id=$3,version=version+1 WHERE id=$1`, id, f.OtherProjectID, f.OtherStateID)
	testutil.Request(t, f.Router(Register), f.MemberID, "GET", f.Prefix()+"/automation/runs/"+run.String(), nil, 404)
	list := testutil.Array(t, testutil.Request(t, f.Router(Register), f.MemberID, "GET", f.Prefix()+"/automation/runs", nil, 200))
	if len(list) != 0 {
		t.Fatal("inaccessible composite source remained in run list")
	}
}
func TestProtectedBatchRollbackBeforeFieldsAreChanged(t *testing.T) {
	f, s, scope, p := automationFixture(t)
	first := f.Issue(t, f.ProjectID, f.StateID, f.OwnerID, "First", 1)
	second := f.Issue(t, f.ProjectID, f.StateID, f.OwnerID, "Locked", 2)
	f.Exec(t, `UPDATE work_items SET planning_locked=true WHERE id=$1`, second)
	runID := queue(t, f, Request{Kind: "schedule", IdempotencyKey: "protected"})
	run, e := loadRun(context.Background(), f.DB.SQL, runID, false)
	if e != nil {
		t.Fatal(e)
	}
	proposal := Proposal{Commands: []ProposalCommand{{Operation: "update", ID: first, Version: 1, Fields: map[string]json.RawMessage{"name": raw("Changed")}}, {Operation: "update", ID: second, Version: 1, Fields: map[string]json.RawMessage{"name": raw("Bad")}}}}
	e = f.DB.WithinTx(context.Background(), func(q database.DBTX) error {
		if e := lockProject(context.Background(), q, f.WorkspaceID, f.ProjectID); e != nil {
			return e
		}
		return s.commitProposal(context.Background(), q, run, p, scope, proposal)
	})
	if e == nil {
		t.Fatal("protected batch applied")
	}
	var name string
	if e = f.DB.SQL.QueryRow(`SELECT name FROM work_items WHERE id=$1`, first).Scan(&name); e != nil || name != "First" {
		t.Fatalf("partial mutation survived: %s %v", name, e)
	}
}

func TestModelDecompositionIncrementalSourceMappingAndBudget(t *testing.T) {
	f, s, _, p := automationFixture(t)
	d, source := validDecomposition()
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "completed", "output": []any{map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": string(raw(d))}}}}})
	}))
	defer provider.Close()
	t.Setenv("AI_BASE_URL", provider.URL)
	t.Setenv("MINE_API_KEY", "isolated-model-fixture")
	id := queue(t, f, Request{Kind: "decompose", IdempotencyKey: "first-prd", Source: Source{Key: "fixture-prd", Text: source}})
	if e := s.Run(context.Background(), id); e != nil {
		failed, _ := loadRun(context.Background(), f.DB.SQL, id, false)
		t.Fatalf("%v; durable failure: %s", e, failed.Failure)
	}
	run, e := loadRun(context.Background(), f.DB.SQL, id, false)
	if e != nil || run.Status != "applied" {
		t.Fatalf("structured decomposition failed: %+v %v", run, e)
	}
	var count int
	_ = f.DB.SQL.QueryRow(`SELECT count(*) FROM automation_source_mappings WHERE project_id=$1`, f.ProjectID).Scan(&count)
	if count != 4 {
		t.Fatalf("source mappings missing: %d", count)
	}
	var story uuid.UUID
	_ = f.DB.SQL.QueryRow(`SELECT work_item_id FROM automation_source_mappings WHERE project_id=$1 AND entity_key='story'`, f.ProjectID).Scan(&story)
	f.Exec(t, `UPDATE work_items SET name='Human story decision',version=version+1 WHERE id=$1`, story)
	d.Entities[1].Title = "Model would replace human"
	increment := queue(t, f, Request{Kind: "decompose", IdempotencyKey: "incremental-prd", Source: Source{Key: "fixture-prd", Text: source + "\n补充要求"}})
	if e = s.Run(context.Background(), increment); e != nil {
		t.Fatal(e)
	}
	run, e = loadRun(context.Background(), f.DB.SQL, increment, false)
	if e != nil || run.Status != "partial" {
		t.Fatalf("manual conflict not surfaced: %+v %v", run, e)
	}
	var name string
	_ = f.DB.SQL.QueryRow(`SELECT name FROM work_items WHERE id=$1`, story).Scan(&name)
	if name != "Human story decision" {
		t.Fatal("incremental generation overwrote a human decision")
	}
	_ = f.DB.SQL.QueryRow(`SELECT count(*) FROM work_items WHERE project_id=$1 AND deleted_at IS NULL`, f.ProjectID).Scan(&count)
	if count != 4 {
		t.Fatal("incremental generation duplicated tasks")
	}
	p.BudgetCalls = 2
	testutil.Request(t, f.Router(Register), f.OwnerID, "PUT", f.Prefix()+"/automation/policy", p, 200)
	budget := queue(t, f, Request{Kind: "decompose", IdempotencyKey: "over-budget", Source: Source{Key: "fixture-prd", Text: source + "\n预算测试"}})
	if e = s.Run(context.Background(), budget); e != nil {
		t.Fatal(e)
	}
	run, _ = loadRun(context.Background(), f.DB.SQL, budget, false)
	if run.Status != "blocked" || calls.Load() != 2 {
		t.Fatalf("model budget did not block before provider call: %+v calls=%d", run, calls.Load())
	}
}
func TestModelCallHoldsNoBusinessTransactionAndStaleOutputPersists(t *testing.T) {
	f, s, _, _ := automationFixture(t)
	item := f.Issue(t, f.ProjectID, f.StateID, f.OwnerID, "Before model", 1)
	d, source := validDecomposition()
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if _, e := f.DB.SQL.ExecContext(ctx, `UPDATE work_items SET name='Changed during provider call',version=version+1 WHERE id=$1`, item); e != nil {
			t.Errorf("provider call held a business lock: %v", e)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "completed", "output": []any{map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": string(raw(d))}}}}})
	}))
	defer provider.Close()
	t.Setenv("AI_BASE_URL", provider.URL)
	t.Setenv("MINE_API_KEY", "isolated-model-fixture")
	id := queue(t, f, Request{Kind: "decompose", IdempotencyKey: "provider-stale", Source: Source{Key: "prd", Text: source}})
	if e := s.Run(context.Background(), id); e != nil {
		t.Fatal(e)
	}
	run, e := loadRun(context.Background(), f.DB.SQL, id, false)
	if e != nil || run.Status != "blocked" || len(run.Output) < 10 {
		t.Fatalf("stale output was applied or discarded: %+v %v", run, e)
	}
	var count int
	_ = f.DB.SQL.QueryRow(`SELECT count(*) FROM work_items WHERE project_id=$1`, f.ProjectID).Scan(&count)
	if count != 1 {
		t.Fatal("stale model proposal partially applied")
	}
}
func TestScheduleAppliesConservedDatesAndUndoRestoresNull(t *testing.T) {
	f, s, scope, _ := automationFixture(t)
	id := f.Issue(t, f.ProjectID, f.StateID, f.OwnerID, "Schedulable", 1)
	f.Exec(t, `UPDATE work_items SET remaining_minutes=480,estimated_minutes=480,required_skills='["go"]'::jsonb WHERE id=$1`, id)
	f.Exec(t, `INSERT INTO project_resources(workspace_id,project_id,member_id,skills,weekday_minutes,project_minutes_per_day,updated_by) VALUES($1,$2,$3,ARRAY['go'],'[0,480,480,480,480,480,0]',480,$3)`, f.WorkspaceID, f.ProjectID, f.OwnerID)
	runID := queue(t, f, Request{Kind: "schedule", IdempotencyKey: "schedule", StartDate: "2026-09-14", EndDate: "2026-09-18", TaskIDs: []uuid.UUID{id}})
	if e := s.Run(context.Background(), runID); e != nil {
		t.Fatal(e)
	}
	run, e := loadRun(context.Background(), f.DB.SQL, runID, false)
	if e != nil || run.Status != "applied" {
		t.Fatalf("schedule not applied: %+v %v", run, e)
	}
	var start, end *string
	_ = f.DB.SQL.QueryRow(`SELECT start_date::text,target_date::text FROM work_items WHERE id=$1`, id).Scan(&start, &end)
	if start == nil || *start != "2026-09-14" || end == nil || *end != *start {
		t.Fatalf("unexpected planned dates %v %v", start, end)
	}
	conflicts, e := s.Undo(context.Background(), scope, runID)
	if e != nil || len(conflicts) > 0 {
		t.Fatalf("schedule undo failed: %+v %v", conflicts, e)
	}
	_ = f.DB.SQL.QueryRow(`SELECT start_date::text,target_date::text FROM work_items WHERE id=$1`, id).Scan(&start, &end)
	if start != nil || end != nil {
		t.Fatal("undo failed to restore unknown schedule")
	}
}
func TestRiskLifecycleDeduplicatesAndResolvesOriginalCause(t *testing.T) {
	f, s, _, p := automationFixture(t)
	p.AllowedOperations = []string{"update"}
	testutil.Request(t, f.Router(Register), f.OwnerID, "PUT", f.Prefix()+"/automation/policy", p, 200)
	id := f.Issue(t, f.ProjectID, f.StateID, f.OwnerID, "Overdue task", 1)
	f.Exec(t, `UPDATE work_items SET target_date='2026-01-01' WHERE id=$1`, id)
	first := queue(t, f, Request{Kind: "risk", IdempotencyKey: "risk-first"})
	if e := s.Run(context.Background(), first); e != nil {
		t.Fatal(e)
	}
	var riskID uuid.UUID
	var version int64
	if e := f.DB.SQL.QueryRow(`SELECT id,version FROM automation_risks WHERE project_id=$1 AND type='overdue'`, f.ProjectID).Scan(&riskID, &version); e != nil {
		t.Fatal(e)
	}
	var notifications int
	_ = f.DB.SQL.QueryRow(`SELECT count(*) FROM notifications WHERE entity_id=$1`, riskID).Scan(&notifications)
	f.Exec(t, `UPDATE work_items SET name='Same delay cause',version=version+1 WHERE id=$1`, id)
	second := queue(t, f, Request{Kind: "risk", IdempotencyKey: "risk-second"})
	if e := s.Run(context.Background(), second); e != nil {
		t.Fatal(e)
	}
	var again int
	_ = f.DB.SQL.QueryRow(`SELECT count(*) FROM notifications WHERE entity_id=$1`, riskID).Scan(&again)
	if again != notifications {
		t.Fatal("persistent identical cause repeated notifications")
	}
	f.Exec(t, `UPDATE work_items SET target_date=NULL,version=version+1 WHERE id=$1`, id)
	third := queue(t, f, Request{Kind: "risk", IdempotencyKey: "risk-third"})
	if e := s.Run(context.Background(), third); e != nil {
		t.Fatal(e)
	}
	var status string
	_ = f.DB.SQL.QueryRow(`SELECT status FROM automation_risks WHERE id=$1`, riskID).Scan(&status)
	if status != "resolved" {
		t.Fatal("absent original cause did not resolve")
	}
}

func TestPageBecomesPrivateDuringProviderCallBlocksAtomicApply(t *testing.T) {
	f, s, _, _ := automationFixture(t)
	router := f.Router(Register, documents.Register)
	page := testutil.Object(t, testutil.Request(t, router, f.OwnerID, "POST", f.Prefix()+"/pages", map[string]any{"name": "PRD", "content_html": "<p>Reserve a book</p>", "content_json": map[string]any{"type": "doc", "content": []any{map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": "Reserve a book"}}}}}}, 201))
	pageID := uuid.MustParse(page["id"].(string))
	response := testutil.Object(t, testutil.Request(t, router, f.MemberID, "POST", f.Prefix()+"/automation/runs", Request{Kind: "decompose", IdempotencyKey: "privacy-during-provider", Source: Source{PageID: &pageID, Revision: 1}}, 202))
	runID := uuid.MustParse(response["id"].(string))
	started, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		model := Decomposition{Entities: []DecomposedEntity{{Key: "epic", Kind: "epic", Title: "Reservation", Source: Location{1, 1, "Reserve a book"}}}, Complete: true}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "completed", "output": []any{map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": string(raw(model))}}}}})
	}))
	defer provider.Close()
	t.Setenv("AI_BASE_URL", provider.URL)
	t.Setenv("MINE_API_KEY", "isolated-model-fixture")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx, runID) }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("provider was not called")
	}
	testutil.Request(t, router, f.OwnerID, "PATCH", f.Prefix()+"/pages/"+pageID.String(), map[string]any{"version": 1, "is_private": true}, 200)
	close(release)
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-ctx.Done():
		t.Fatal("worker did not finish after source privacy change")
	}
	run, e := loadRun(ctx, f.DB.SQL, runID, false)
	if e != nil || run.Status != "blocked" {
		t.Fatalf("source privacy did not block: %+v %v", run, e)
	}
	var count int
	_ = f.DB.SQL.QueryRow(`SELECT count(*) FROM work_items WHERE project_id=$1`, f.ProjectID).Scan(&count)
	if count != 0 {
		t.Fatal("private source broadened project work")
	}
	testutil.Request(t, router, f.MemberID, "GET", f.Prefix()+"/automation/runs/"+runID.String(), nil, 404)
}
func TestBoundedDecompositionScheduleRiskEfficiencyChain(t *testing.T) {
	f, s, _, p := automationFixture(t)
	p.MaxRounds = 4
	testutil.Request(t, f.Router(Register), f.OwnerID, "PUT", f.Prefix()+"/automation/policy", p, 200)
	f.Exec(t, `INSERT INTO project_resources(workspace_id,project_id,member_id,weekday_minutes,project_minutes_per_day,updated_by) VALUES($1,$2,$3,'[0,480,480,480,480,480,0]',480,$3)`, f.WorkspaceID, f.ProjectID, f.OwnerID)
	d, source := validDecomposition()
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "completed", "output": []any{map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": string(raw(d))}}}}})
	}))
	defer provider.Close()
	t.Setenv("AI_BASE_URL", provider.URL)
	t.Setenv("MINE_API_KEY", "isolated-model-fixture")
	_ = queue(t, f, Request{Kind: "decompose", IdempotencyKey: "closed-loop", Source: Source{Text: source, Key: "continuous-prd"}})
	for i := 0; i < 4; i++ {
		var id uuid.UUID
		if e := f.DB.SQL.QueryRow(`SELECT id FROM automation_runs WHERE project_id=$1 AND status='queued' ORDER BY created_at,id LIMIT 1`, f.ProjectID).Scan(&id); e != nil {
			t.Fatalf("missing chain step %d: %v", i, e)
		}
		if e := s.Run(context.Background(), id); e != nil {
			t.Fatalf("step%d: %v", i, e)
		}
	}
	var runs, pending, forecasts, improvements int
	_ = f.DB.SQL.QueryRow(`SELECT count(*),count(*) FILTER(WHERE status IN ('queued','running','blocked','failed')) FROM automation_runs WHERE project_id=$1`, f.ProjectID).Scan(&runs, &pending)
	_ = f.DB.SQL.QueryRow(`SELECT count(*) FROM automation_forecasts WHERE project_id=$1`, f.ProjectID).Scan(&forecasts)
	_ = f.DB.SQL.QueryRow(`SELECT count(*) FROM automation_improvements WHERE project_id=$1`, f.ProjectID).Scan(&improvements)
	if runs != 4 || pending != 0 || forecasts != 1 || improvements != 1 {
		t.Fatalf("causal chain incomplete or unbounded: runs=%d pending=%d forecast=%d improvements=%d", runs, pending, forecasts, improvements)
	}
	var scheduled int
	_ = f.DB.SQL.QueryRow(`SELECT count(*) FROM work_items WHERE project_id=$1 AND requirement_type='task' AND start_date IS NOT NULL AND target_date IS NOT NULL`, f.ProjectID).Scan(&scheduled)
	if scheduled != 2 {
		t.Fatalf("decomposed leaves not scheduled: %d", scheduled)
	}
}

func TestRejectedPrivateCompositeInputCannotEnqueueOrReachProvider(t *testing.T) {
	f, s, _, _ := automationFixture(t)
	router := f.Router(Register, documents.Register)
	page := testutil.Object(t, testutil.Request(t, router, f.OwnerID, "POST", f.Prefix()+"/pages", map[string]any{"name": "Source", "is_private": false}, 201))
	pageID := uuid.MustParse(page["id"].(string))
	item := f.Issue(t, f.ProjectID, f.StateID, f.OwnerID, "Derived source task", 1)
	origin := uuid.New()
	pageKey := "page:" + pageID.String()
	f.Exec(t, `INSERT INTO automation_runs(id,workspace_id,project_id,requested_by,authorized_by,kind,status,idempotency_key,input_fingerprint,policy_version,input,algorithm_version) VALUES($1,$2,$3,$4,$4,'decompose','applied','origin','origin',1,$5::jsonb,'test')`, origin, f.WorkspaceID, f.ProjectID, f.OwnerID, string(raw(Input{Source: Source{PageID: &pageID, Revision: 1, Key: pageKey, Text: "Private mapping quote"}})))
	f.Exec(t, `INSERT INTO automation_source_mappings(project_id,source_key,entity_key,work_item_id,run_id,source_revision,source_location,generated_fields,last_item_version) VALUES($1,$2,'mapped',$3,$4,'1','{"quote":"Private mapping quote","start_line":1,"end_line":1}','{"name":"Derived source task"}',1)`, f.ProjectID, pageKey, item, origin)
	response := testutil.Object(t, testutil.Request(t, router, f.MemberID, "POST", f.Prefix()+"/automation/runs", Request{Kind: "decompose", IdempotencyKey: "before-private-composite", Source: Source{Text: "A different PRD", Key: "independent-upload"}}, 202))
	queuedID := uuid.MustParse(response["id"].(string))
	testutil.Request(t, router, f.OwnerID, "PATCH", f.Prefix()+"/pages/"+pageID.String(), map[string]any{"version": 1, "is_private": true}, 200)
	testutil.Request(t, router, f.MemberID, "POST", f.Prefix()+"/automation/runs", Request{Kind: "decompose", IdempotencyKey: "spoof-private-key", Source: Source{Text: "Replace source", Key: pageKey}}, 400)
	testutil.Request(t, router, f.MemberID, "POST", f.Prefix()+"/automation/runs", Request{Kind: "decompose", IdempotencyKey: "unauthorized-upload", Source: Source{Text: "Different source", Key: "different-upload"}}, 404)
	testutil.Request(t, router, f.MemberID, "POST", f.Prefix()+"/automation/runs", Request{Kind: "decompose", IdempotencyKey: "unauthorized-default-upload", Source: Source{Text: "Different default source"}}, 404)
	var count int
	_ = f.DB.SQL.QueryRow(`SELECT count(*) FROM automation_runs WHERE project_id=$1 AND idempotency_key IN ('spoof-private-key','unauthorized-upload','unauthorized-default-upload')`, f.ProjectID).Scan(&count)
	if count != 0 {
		t.Fatal("rejected requester persisted an unauthorized durable input")
	}
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); http.Error(w, "must not be called", 500) }))
	defer provider.Close()
	t.Setenv("AI_BASE_URL", provider.URL)
	t.Setenv("MINE_API_KEY", "isolated-model-fixture")
	if e := s.Run(context.Background(), queuedID); e != nil {
		t.Fatal(e)
	}
	run, e := loadRun(context.Background(), f.DB.SQL, queuedID, false)
	if e != nil || run.Status != "blocked" || calls.Load() != 0 {
		t.Fatalf("policy admin acted as a confused deputy: %+v error=%v calls=%d", run, e, calls.Load())
	}
}
func TestInterruptedWorkerLeaseRetriesThenRecovers(t *testing.T) {
	f, s, _, _ := automationFixture(t)
	runID := queue(t, f, Request{Kind: "forecast", IdempotencyKey: "restart"})
	f.Exec(t, `UPDATE automation_runs SET status='running',lease_until=now()+interval '1 minute',attempts=1 WHERE id=$1`, runID)
	if e := s.Run(context.Background(), runID); !errors.Is(e, errLeaseActive) {
		t.Fatalf("active lease was acknowledged and lost: %v", e)
	}
	f.Exec(t, `UPDATE automation_runs SET lease_until=now()-interval '1 second' WHERE id=$1`, runID)
	if e := s.Run(context.Background(), runID); e != nil {
		t.Fatal(e)
	}
	run, e := loadRun(context.Background(), f.DB.SQL, runID, false)
	if e != nil || run.Status != "completed" || run.Attempts != 2 {
		t.Fatalf("expired lease did not resume: %+v %v", run, e)
	}
}
