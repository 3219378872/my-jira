package resources

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"my-jira/apps/api/internal/platform/identity"
	"my-jira/apps/api/internal/support/testutil"
)

func config(version int) map[string]any {
	return map[string]any{"version": version, "skills": []string{" Go ", "sql", "go"}, "weekday_minutes": []any{0, 480, 480, 480, 480, 480, 0}, "project_minutes_per_day": 360, "exceptions": map[string]any{"2026-09-15": 0}}
}

func TestResourceConfigurationPermissionsVersionsAndAudit(t *testing.T) {
	f := testutil.New(t)
	r := f.Router(Register)
	path := f.Prefix() + "/resources"
	initial := testutil.Object(t, testutil.Request(t, r, f.OwnerID, "GET", path, nil, 200))
	if len(initial["members"].([]any)) != 3 {
		t.Fatalf("Current members: %#v", initial)
	}
	first := initial["members"].([]any)[0].(map[string]any)
	if first["version"] != float64(0) || first["project_minutes_per_day"] != nil {
		t.Fatal("Unconfigured capacity must be unknown")
	}
	memberPath := path + "/members/" + f.MemberID.String()
	testutil.Request(t, r, f.MemberID, "PUT", memberPath, config(0), 403)
	testutil.Request(t, r, f.GuestID, "PUT", memberPath, config(0), 403)
	testutil.Request(t, r, f.OutsiderID, "GET", path, nil, 404)
	testutil.Request(t, r, f.OwnerID, "PUT", path+"/members/"+f.OutsiderID.String(), config(0), 404)
	saved := testutil.Object(t, testutil.Request(t, r, f.OwnerID, "PUT", memberPath, config(0), 200))
	if saved["version"] != float64(1) || len(saved["skills"].([]any)) != 2 {
		t.Fatalf("Persisted config: %#v", saved)
	}
	testutil.Request(t, r, f.OwnerID, "PUT", memberPath, config(0), 409)
	testutil.Request(t, r, f.OwnerID, "PUT", memberPath, config(1), 200)
	bad := config(2)
	bad["weekday_minutes"] = []int{480}
	testutil.Request(t, r, f.OwnerID, "PUT", memberPath, bad, 400)
	testutil.Request(t, r, f.OwnerID, "PATCH", path, map[string]any{"timezone": "Mars/Nowhere"}, 400)
	testutil.Request(t, r, f.OwnerID, "PATCH", path, map[string]any{"timezone": "Asia/Shanghai"}, 200)
	var history, events, outbox int
	if err := f.DB.SQL.QueryRowContext(context.Background(), `SELECT (SELECT count(*) FROM project_resource_history WHERE project_id=$1),(SELECT count(*) FROM project_events WHERE project_id=$1 AND kind='resources.updated'),(SELECT count(*) FROM outbox_events WHERE payload->>'event'='resources.changed' AND payload->>'project_id'=$1::text)`, f.ProjectID).Scan(&history, &events, &outbox); err != nil {
		t.Fatal(err)
	}
	if history != 3 || events != 3 || outbox != 3 {
		t.Fatalf("Atomic audit/events: history=%d events=%d outbox=%d", history, events, outbox)
	}
}

func TestResourceRequirementsGatePreservesConfigurationAndInternalSnapshot(t *testing.T) {
	f := testutil.New(t)
	r := f.Router(Register)
	path := f.Prefix() + "/resources"
	memberPath := path + "/members/" + f.MemberID.String()
	testutil.Request(t, r, f.OwnerID, "PUT", memberPath, config(0), 200)
	itemID := f.Issue(t, f.ProjectID, f.StateID, f.OwnerID, "Internal automation input", 1)
	f.Exec(t, `UPDATE projects SET settings=jsonb_set(settings,'{requirements_enabled}','null'::jsonb) WHERE id=$1`, f.ProjectID)
	testutil.Request(t, r, f.OwnerID, "GET", path, nil, 200)
	f.Exec(t, `UPDATE projects SET settings=jsonb_set(settings,'{requirements_enabled}','false'::jsonb) WHERE id=$1`, f.ProjectID)
	for _, request := range []struct {
		method, path string
		body         any
	}{
		{"GET", path, nil},
		{"GET", path + "/load?start_date=2026-09-14&end_date=2026-09-18", nil},
		{"POST", path + "/schedule", map[string]any{"start_date": "2026-09-14", "end_date": "2026-09-18"}},
		{"PATCH", path, map[string]any{"timezone": "Asia/Shanghai"}},
		{"PUT", memberPath, config(1)},
	} {
		response := testutil.Request(t, r, f.OwnerID, request.method, request.path, request.body, 403)
		if response["error"].(map[string]any)["code"] != "requirements_disabled" {
			t.Fatalf("Wrong rollout gate error: %#v", response)
		}
	}
	// Authorization still precedes the flag, and disabling one project does not
	// disable another project's resources.
	testutil.Request(t, r, f.OutsiderID, "GET", path, nil, 404)
	otherPath := f.WorkspacePrefix() + "/projects/" + f.OtherProjectID.String() + "/resources"
	testutil.Request(t, r, f.OwnerID, "GET", otherPath, nil, 200)
	scope, err := f.Deps.Policy.Project(context.Background(), identity.Actor{UserID: f.OwnerID}, f.WorkspaceID, f.ProjectID, identity.Guest)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := NewService(f.Deps).Snapshot(context.Background(), f.DB.SQL, scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Members) != 3 || len(snapshot.Tasks) != 1 || snapshot.Tasks[0].ID != itemID {
		t.Fatal("Disabled views prevented the independent internal resource snapshot")
	}
	var history int
	var timezone string
	if err = f.DB.SQL.QueryRowContext(context.Background(), `SELECT timezone,(SELECT count(*) FROM project_resource_history WHERE project_id=$1) FROM projects WHERE id=$1`, f.ProjectID).Scan(&timezone, &history); err != nil {
		t.Fatal(err)
	}
	if timezone != "UTC" || history != 1 {
		t.Fatalf("Disabled resource writes changed state: timezone=%s history=%d", timezone, history)
	}
	f.Exec(t, `UPDATE projects SET settings=jsonb_set(settings,'{requirements_enabled}','true'::jsonb) WHERE id=$1`, f.ProjectID)
	result := testutil.Object(t, testutil.Request(t, r, f.OwnerID, "GET", path, nil, 200))
	for _, raw := range result["members"].([]any) {
		member := raw.(map[string]any)
		if member["member_id"] == f.MemberID.String() {
			if member["version"] != float64(1) || member["project_minutes_per_day"] != float64(360) {
				t.Fatalf("Re-enabling changed persisted resource configuration: %#v", member)
			}
			return
		}
	}
	t.Fatal("Re-enabled resource configuration is missing")
}

func TestResourceWritesRecheckRequirementsGateAfterGraphLock(t *testing.T) {
	for _, operation := range []string{"member", "timezone"} {
		t.Run(operation, func(t *testing.T) {
			f := testutil.New(t)
			r := f.Router(Register)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			tx, err := f.DB.SQL.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, f.ProjectID.String()); err != nil {
				t.Fatal(err)
			}
			method, path := "PUT", f.Prefix()+"/resources/members/"+f.MemberID.String()
			var payload any = config(0)
			if operation == "timezone" {
				method, path = "PATCH", f.Prefix()+"/resources"
				payload = map[string]any{"timezone": "Asia/Shanghai"}
			}
			body, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(method, path, bytes.NewReader(body)).WithContext(ctx)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Test-Actor", f.OwnerID.String())
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				response := httptest.NewRecorder()
				r.ServeHTTP(response, req)
				done <- response
			}()
			// Observe the actual advisory wait so the flag changes only after the
			// request has passed its initial HTTP authorization and feature gate.
			for {
				var waiting bool
				err = f.DB.SQL.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND NOT granted AND classid::bigint=((hashtextextended($1,0)>>32)&4294967295::bigint) AND objid::bigint=(hashtextextended($1,0)&4294967295::bigint) AND objsubid=1)`, f.ProjectID.String()).Scan(&waiting)
				if err != nil {
					t.Fatal(err)
				}
				if waiting {
					break
				}
				select {
				case response := <-done:
					t.Fatalf("Resource request did not wait for graph lock: %d %s", response.Code, response.Body.String())
				case <-ctx.Done():
					t.Fatal("Resource request did not reach graph lock")
				case <-time.After(10 * time.Millisecond):
				}
			}
			if _, err = tx.ExecContext(ctx, `UPDATE projects SET settings=jsonb_set(settings,'{requirements_enabled}','false'::jsonb) WHERE id=$1`, f.ProjectID); err != nil {
				t.Fatal(err)
			}
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			select {
			case response := <-done:
				var envelope map[string]any
				if err = json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
					t.Fatal(err)
				}
				if response.Code != 403 || envelope["error"].(map[string]any)["code"] != "requirements_disabled" {
					t.Fatalf("Queued resource write bypassed disabled requirements: %d %s", response.Code, response.Body.String())
				}
			case <-ctx.Done():
				t.Fatal("Resource request failed to finish after graph lock release")
			}
			var resources, history, events int
			var timezone string
			if err = f.DB.SQL.QueryRowContext(ctx, `SELECT timezone,(SELECT count(*) FROM project_resources WHERE project_id=$1),(SELECT count(*) FROM project_resource_history WHERE project_id=$1),(SELECT count(*) FROM project_events WHERE project_id=$1 AND kind='resources.updated') FROM projects WHERE id=$1`, f.ProjectID).Scan(&timezone, &resources, &history, &events); err != nil {
				t.Fatal(err)
			}
			if timezone != "UTC" || resources != 0 || history != 0 || events != 0 {
				t.Fatalf("Blocked resource write persisted state: timezone=%s resources=%d history=%d events=%d", timezone, resources, history, events)
			}
		})
	}
}

func TestResourceProjectionFullSetGuestScopeAndMemberRemoval(t *testing.T) {
	f := testutil.New(t)
	r := f.Router(Register)
	path := f.Prefix() + "/resources"
	cfg := config(0)
	cfg["project_minutes_per_day"] = 480
	cfg["exceptions"] = map[string]any{}
	testutil.Request(t, r, f.OwnerID, "PUT", path+"/members/"+f.MemberID.String(), cfg, 200)
	for i := 1; i <= 3; i++ {
		creator := f.OwnerID
		if i == 3 {
			creator = f.GuestID
		}
		id := f.Issue(t, f.ProjectID, f.StateID, creator, "Scheduled work", i)
		f.Exec(t, `UPDATE work_items SET requirement_type='task',estimated_minutes=300,remaining_minutes=300,start_date='2026-09-14',target_date='2026-09-14' WHERE id=$1`, id)
		f.Exec(t, `INSERT INTO work_item_assignees(id,workspace_id,project_id,work_item_id,user_id)VALUES($1,$2,$3,$4,$5)`, uuid.New(), f.WorkspaceID, f.ProjectID, id, f.MemberID)
	}
	other := f.Issue(t, f.OtherProjectID, f.OtherStateID, f.OwnerID, "Hidden other project", 1)
	f.Exec(t, `UPDATE work_items SET remaining_minutes=9999,start_date='2026-09-14',target_date='2026-09-14' WHERE id=$1`, other)
	url := path + "/load?start_date=2026-09-14&end_date=2026-09-14&member_id=" + f.MemberID.String()
	load := testutil.Object(t, testutil.Request(t, r, f.MemberID, "GET", url, nil, 200))
	member := load["members"].([]any)[0].(map[string]any)
	day := member["days"].([]any)[0].(map[string]any)
	if day["allocated_minutes"] != float64(900) || day["over_capacity"] != true || len(day["task_ids"].([]any)) != 3 {
		t.Fatalf("Complete project aggregation: %#v", day)
	}
	guest := testutil.Object(t, testutil.Request(t, r, f.GuestID, "GET", url, nil, 200))
	guestDay := guest["members"].([]any)[0].(map[string]any)["days"].([]any)[0].(map[string]any)
	if guestDay["allocated_minutes"] != float64(300) || len(guest["tasks"].([]any)) != 1 {
		t.Fatalf("Guest leaked other creators: %#v", guest)
	}
	filtered := testutil.Object(t, testutil.Request(t, r, f.MemberID, "GET", url+"&cycle_id="+uuid.NewString(), nil, 200))
	if len(filtered["tasks"].([]any)) != 0 || filtered["members"].([]any)[0].(map[string]any)["days"].([]any)[0].(map[string]any)["allocated_minutes"] != float64(900) {
		t.Fatal("Task filter hid overbooking")
	}
	if filtered["members"].([]any)[0].(map[string]any)["days"].([]any)[0].(map[string]any)["selected_minutes"] != float64(0) {
		t.Fatal("Filtered workload subtotal did not change")
	}
	backlog := testutil.Object(t, testutil.Request(t, r, f.MemberID, "GET", url+"&cycle_id=backlog", nil, 200))
	if len(backlog["tasks"].([]any)) != 3 {
		t.Fatal("Backlog cycle filter omitted unassigned execution Cycles")
	}
	f.Exec(t, `UPDATE project_members SET is_active=false WHERE project_id=$1 AND user_id=$2`, f.ProjectID, f.MemberID)
	testutil.Request(t, r, f.MemberID, "GET", url, nil, 404)
	removed := testutil.Object(t, testutil.Request(t, r, f.OwnerID, "GET", path+"/load?start_date=2026-09-14&end_date=2026-09-14", nil, 200))
	found := false
	for _, conflict := range removed["conflicts"].([]any) {
		if conflict.(map[string]any)["type"] == "member_removed" {
			found = true
		}
	}
	if !found {
		t.Fatal("Removed assignment was treated as known zero workload")
	}
}

func TestResourceWriteRollsBackAuditAndEventOnOutboxFailure(t *testing.T) {
	f := testutil.New(t)
	r := f.Router(Register)
	f.Exec(t, `CREATE FUNCTION resource_test_reject_outbox() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.payload->>'event'='resources.changed' THEN RAISE EXCEPTION 'fixture rejects outbox'; END IF; RETURN NEW; END $$`)
	f.Exec(t, `CREATE TRIGGER resource_test_reject BEFORE INSERT ON outbox_events FOR EACH ROW EXECUTE FUNCTION resource_test_reject_outbox()`)
	testutil.Request(t, r, f.OwnerID, "PUT", f.Prefix()+"/resources/members/"+f.MemberID.String(), config(0), 500)
	var resources, history, events int
	if err := f.DB.SQL.QueryRowContext(context.Background(), `SELECT (SELECT count(*) FROM project_resources),(SELECT count(*) FROM project_resource_history),(SELECT count(*) FROM project_events WHERE kind='resources.updated')`).Scan(&resources, &history, &events); err != nil {
		t.Fatal(err)
	}
	if resources != 0 || history != 0 || events != 0 {
		t.Fatalf("Partial failed transaction: %d/%d/%d", resources, history, events)
	}
}

type signalPolicy struct {
	identity.Policy
	once    sync.Once
	checked chan struct{}
}

func (p *signalPolicy) Project(ctx context.Context, actor identity.Actor, wid, pid uuid.UUID, minimum identity.Role) (identity.Scope, error) {
	scope, err := p.Policy.Project(ctx, actor, wid, pid, minimum)
	p.once.Do(func() { close(p.checked) })
	return scope, err
}

func rawRequest(r *gin.Engine, actor uuid.UUID, path string, payload []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest("PUT", path, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-Actor", actor.String())
	response := httptest.NewRecorder()
	r.ServeHTTP(response, req)
	return response
}

func TestResourceWriteRechecksAuthorizationAfterWaitingForGraphLock(t *testing.T) {
	f := testutil.New(t)
	signals := &signalPolicy{Policy: f.Deps.Policy, checked: make(chan struct{})}
	f.Deps.Policy = signals
	r := f.Router(Register)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tx, err := f.DB.SQL.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, f.ProjectID.String()); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(config(0))
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- rawRequest(r, f.OwnerID, f.Prefix()+"/resources/members/"+f.MemberID.String(), payload)
	}()
	select {
	case <-signals.checked:
	case <-ctx.Done():
		t.Fatal("Initial authorization did not finish")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE workspace_members SET role=5 WHERE workspace_id=$1 AND user_id=$2`, f.WorkspaceID, f.OwnerID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case response := <-done:
		if response.Code != 403 {
			t.Fatalf("Stale Admin wrote after downgrade: %d %s", response.Code, response.Body.String())
		}
	case <-ctx.Done():
		t.Fatal("Blocked resource update failed to finish")
	}
	var count int
	if err = f.DB.SQL.QueryRowContext(ctx, `SELECT count(*) FROM project_resources`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("Downgraded actor wrote resource configuration")
	}
}

func TestConcurrentResourceWritersUseVersionCompare(t *testing.T) {
	f := testutil.New(t)
	r := f.Router(Register)
	payload, _ := json.Marshal(config(0))
	responses := make(chan *httptest.ResponseRecorder, 2)
	for i := 0; i < 2; i++ {
		go func() {
			responses <- rawRequest(r, f.OwnerID, f.Prefix()+"/resources/members/"+f.MemberID.String(), payload)
		}()
	}
	codes := map[int]int{}
	for i := 0; i < 2; i++ {
		select {
		case response := <-responses:
			codes[response.Code]++
		case <-time.After(10 * time.Second):
			t.Fatal("Concurrent resource writer timed out")
		}
	}
	if codes[200] != 1 || codes[409] != 1 {
		t.Fatalf("Concurrent versions were not protected: %v", codes)
	}
}

func TestResourceSnapshotDerivesStoryCycleCommitmentAndScheduleHTTP(t *testing.T) {
	f := testutil.New(t)
	r := f.Router(Register)
	path := f.Prefix() + "/resources"
	cfg := config(0)
	cfg["exceptions"] = map[string]any{}
	testutil.Request(t, r, f.OwnerID, "PUT", path+"/members/"+f.MemberID.String(), cfg, 200)
	story := f.Issue(t, f.ProjectID, f.StateID, f.OwnerID, "Story commitment", 1)
	f.Exec(t, `UPDATE work_items SET requirement_type='story' WHERE id=$1`, story)
	child := f.Issue(t, f.ProjectID, f.StateID, f.OwnerID, "Execution", 2)
	f.Exec(t, `UPDATE work_items SET requirement_type='task',parent_id=$2,remaining_minutes=360,required_skills='["go"]'::jsonb WHERE id=$1`, child, story)
	cycle := uuid.New()
	f.Exec(t, `INSERT INTO cycles(id,workspace_id,project_id,name,start_date,end_date,owner_id)VALUES($1,$2,$3,'Commitment','2026-09-15','2026-09-16',$4)`, cycle, f.WorkspaceID, f.ProjectID, f.OwnerID)
	f.Exec(t, `INSERT INTO cycle_items(id,workspace_id,project_id,cycle_id,work_item_id)VALUES($1,$2,$3,$4,$5)`, uuid.New(), f.WorkspaceID, f.ProjectID, cycle, story)
	proposal := testutil.Object(t, testutil.Request(t, r, f.OwnerID, "POST", path+"/schedule", map[string]any{"start_date": "2026-09-14", "end_date": "2026-09-18"}, 200))
	if proposal["feasible"] != true || proposal["input_revision"].(float64) <= 0 {
		t.Fatalf("Proposal: %#v", proposal)
	}
	assignment := proposal["assignments"].([]any)[0].(map[string]any)
	if assignment["start_date"] != "2026-09-15" || assignment["id"] != child.String() {
		t.Fatalf("Story Cycle commitment was ignored: %#v", assignment)
	}
	load := testutil.Object(t, testutil.Request(t, r, f.OwnerID, "GET", path+"/load?start_date=2026-09-14&end_date=2026-09-18&commitment_cycle_id="+cycle.String(), nil, 200))
	if len(load["tasks"].([]any)) != 2 {
		t.Fatal("Story commitment filter omitted its independently unassigned execution task")
	}
	for _, raw := range load["tasks"].([]any) {
		row := raw.(map[string]any)
		if row["id"] == child.String() && (row["cycle_id"] != nil || row["commitment_cycle_id"] != cycle.String()) {
			t.Fatalf("Story commitment rewrote execution Cycle: %#v", row)
		}
	}
	prerequisite := f.Issue(t, f.ProjectID, f.StateID, f.OwnerID, "Archived unfinished prerequisite", 3)
	f.Exec(t, `UPDATE work_items SET archived_at=now() WHERE id=$1`, prerequisite)
	f.Exec(t, `INSERT INTO work_item_relations(id,workspace_id,project_id,source_id,target_id,relation_type)VALUES($1,$2,$3,$4,$5,'blocks')`, uuid.New(), f.WorkspaceID, f.ProjectID, prerequisite, child)
	blocked := testutil.Object(t, testutil.Request(t, r, f.OwnerID, "POST", path+"/schedule", map[string]any{"start_date": "2026-09-14", "end_date": "2026-09-18"}, 200))
	if blocked["feasible"] != false {
		t.Fatal("Archived unfinished prerequisite was ignored by scheduler")
	}
	var start *time.Time
	if err := f.DB.SQL.QueryRowContext(context.Background(), `SELECT start_date FROM work_items WHERE id=$1`, child).Scan(&start); err != nil {
		t.Fatal(err)
	}
	if start != nil {
		t.Fatal("Proposal endpoint directly changed work items")
	}
}
