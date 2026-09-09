package workitems_test

import (
	"context"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"my-jira/apps/api/internal/planning"
	"my-jira/apps/api/internal/support/testutil"
	"my-jira/apps/api/internal/workitems"
)

func TestMoveDestinationGroupsAreAtomic(t *testing.T) {
	f := testutil.New(t)
	r := f.Router(workitems.Register, planning.Register)
	source, target := f.Prefix(), f.WorkspacePrefix()+"/projects/"+f.OtherProjectID.String()
	call := func(method, path string, input any, status int) map[string]any {
		return testutil.Request(t, r, f.OwnerID, method, path, input, status)
	}
	resource := func(prefix, kind string) string {
		return testutil.Object(t, call("POST", prefix+"/"+kind, map[string]any{"name": kind}, 201))["id"].(string)
	}
	sourceLabel, targetLabel := uuid.New(), uuid.New()
	f.Exec(t, "INSERT INTO labels(id,workspace_id,project_id,name) VALUES($1,$2,$3,'Source'),($4,$2,$5,'Target')", sourceLabel, f.WorkspaceID, f.ProjectID, targetLabel, f.OtherProjectID)
	sourceCycle, targetCycle := resource(source, "cycles"), resource(target, "cycles")
	sourceModule, targetModule := resource(source, "modules"), resource(target, "modules")
	scheme := testutil.Object(t, call("POST", target+"/estimates", map[string]any{"name": "Destination points", "kind": "points", "points": []map[string]any{{"label": "Three", "numeric_value": 3}}}, 201))
	point := scheme["points"].([]any)[0].(map[string]any)["id"].(string)
	f.Exec(t, "UPDATE states SET group_name='completed' WHERE id=$1", f.OtherStateID)
	item := testutil.Object(t, call("POST", source+"/issues", map[string]any{"name": "Atomic board drop", "priority": "low", "assignee_ids": []uuid.UUID{f.OwnerID, f.MemberID}, "label_ids": []uuid.UUID{sourceLabel}, "cycle_id": sourceCycle, "module_ids": []string{sourceModule}}, 201))
	id := item["id"].(string)
	path := source + "/issues/" + id
	assertUnchanged := func() {
		t.Helper()
		got := testutil.Object(t, call("GET", path, nil, 200))
		for _, key := range []string{"project_id", "state_id", "sequence_id", "version", "priority", "position", "assignee_ids", "label_ids", "cycle_id", "module_ids", "estimate_point_id"} {
			if !reflect.DeepEqual(got[key], item[key]) {
				t.Fatalf("failed drop changed %s: got %#v, want %#v", key, got[key], item[key])
			}
		}
		var next, moved int
		if err := f.DB.SQL.QueryRowContext(context.Background(), "SELECT next_sequence FROM projects WHERE id=$1", f.OtherProjectID).Scan(&next); err != nil || next != 1 {
			t.Fatalf("failed drop consumed target sequence: %d %v", next, err)
		}
		if err := f.DB.SQL.QueryRowContext(context.Background(), "SELECT count(*) FROM activities WHERE work_item_id=$1 AND action='moved'", id).Scan(&moved); err != nil || moved != 0 {
			t.Fatalf("failed drop created activity: %d %v", moved, err)
		}
	}
	for _, changes := range []map[string]any{
		{"label_ids": []uuid.UUID{sourceLabel}}, {"cycle_id": sourceCycle}, {"module_ids": []string{sourceModule}},
		{"assignee_ids": []uuid.UUID{f.MemberID}}, {"estimate_point_id": f.StateID}, {"priority": "invalid"},
		{"position": nil}, {"created_by": f.MemberID}, {"state_id": f.StateID},
	} {
		call("POST", path+"/move", map[string]any{"project_id": f.OtherProjectID, "version": 1, "changes": changes}, 400)
		assertUnchanged()
	}
	call("POST", path+"/move", map[string]any{"project_id": f.OtherProjectID, "version": 999, "changes": map[string]any{"priority": "urgent"}}, 409)
	assertUnchanged()
	changes := map[string]any{"priority": "urgent", "position": 42.5, "assignee_ids": []uuid.UUID{f.OwnerID}, "label_ids": []uuid.UUID{targetLabel}, "cycle_id": targetCycle, "module_ids": []string{targetModule}, "estimate_point_id": point}
	moved := testutil.Object(t, call("POST", path+"/move", map[string]any{"project_id": f.OtherProjectID, "state_id": f.OtherStateID, "version": 1, "changes": changes}, 200))
	for key, want := range map[string]any{"project_id": f.OtherProjectID.String(), "state_id": f.OtherStateID.String(), "version": float64(2), "sequence_id": float64(1), "priority": "urgent", "position": 42.5, "estimate_point_id": point, "estimate": float64(3), "cycle_id": targetCycle} {
		if !reflect.DeepEqual(moved[key], want) {
			t.Fatalf("committed drop has incorrect %s: got %#v, want %#v", key, moved[key], want)
		}
	}
	for key, want := range map[string][]any{"assignee_ids": {f.OwnerID.String()}, "label_ids": {targetLabel.String()}, "module_ids": {targetModule}} {
		if !reflect.DeepEqual(moved[key], want) {
			t.Fatalf("committed drop has incorrect %s: %#v", key, moved[key])
		}
	}
	if moved["completed_at"] == nil {
		t.Fatal("completed destination state did not set the completion date")
	}
	call("GET", path, nil, 404)
	call("GET", target+"/issues/"+id, nil, 200)
	var count int
	if err := f.DB.SQL.QueryRowContext(context.Background(), "SELECT count(*) FROM activities WHERE work_item_id=$1 AND action='moved'", id).Scan(&count); err != nil || count != 1 {
		t.Fatalf("expected one committed move event: %d %v", count, err)
	}
}

func TestMoveRechecksMembershipAfterWaitingForGraphLock(t *testing.T) {
	f := testutil.New(t)
	r := f.Router(workitems.Register)
	f.Exec(t, "INSERT INTO project_members(id,workspace_id,project_id,user_id,role) VALUES($1,$2,$3,$4,15)", uuid.New(), f.WorkspaceID, f.OtherProjectID, f.MemberID)
	id := f.Issue(t, f.ProjectID, f.StateID, f.MemberID, "Queued move", 1)
	base := f.Prefix() + "/issues/" + id.String()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	for _, revoke := range []string{
		"UPDATE project_members SET role=5 WHERE project_id=$1 AND user_id=$2",
		"UPDATE project_members SET is_active=false WHERE project_id=$1 AND user_id=$2",
	} {
		f.Exec(t, "UPDATE project_members SET role=15,is_active=true WHERE project_id=$1 AND user_id=$2", f.OtherProjectID, f.MemberID)
		tx, err := f.DB.SQL.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		var locker int
		if err = tx.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&locker); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", f.ProjectID.String()); err != nil {
			t.Fatal(err)
		}
		finished := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			req := httptest.NewRequest("POST", base+"/move", strings.NewReader(`{"project_id":"`+f.OtherProjectID.String()+`","version":1,"changes":{"priority":"urgent"}}`)).WithContext(ctx)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Test-Actor", f.MemberID.String())
			response := httptest.NewRecorder()
			r.ServeHTTP(response, req)
			finished <- response
		}()
		blocked := false
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if err = f.DB.SQL.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))", locker).Scan(&blocked); err != nil {
				t.Fatal(err)
			}
			if blocked {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		if !blocked {
			t.Fatal("move did not reach the owned graph lock")
		}
		f.Exec(t, revoke, f.OtherProjectID, f.MemberID)
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
		select {
		case response := <-finished:
			if response.Code != 403 && response.Code != 404 {
				t.Fatalf("revoked move status %d: %s", response.Code, response.Body.String())
			}
		case <-ctx.Done():
			t.Fatal("move did not resume after graph lock release")
		}
		current := testutil.Object(t, testutil.Request(t, r, f.OwnerID, "GET", base, nil, 200))
		if current["project_id"] != f.ProjectID.String() || current["version"] != float64(1) || current["priority"] != "none" {
			t.Fatal("revoked move changed source item or destination groups")
		}
	}
}
