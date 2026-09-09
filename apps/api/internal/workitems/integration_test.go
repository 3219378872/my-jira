package workitems_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"sort"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"my-jira/apps/api/internal/planning"
	"my-jira/apps/api/internal/support"
	"my-jira/apps/api/internal/support/testutil"
	"my-jira/apps/api/internal/workitems"
)

func TestWorkItemContractsAndRollback(t *testing.T) {
	f := testutil.New(t)
	r := f.Router(workitems.Register, planning.Register, support.Register)
	p, w := f.Prefix(), f.WorkspacePrefix()
	base := p + "/issues"
	call := func(actor uuid.UUID, method, path string, input any, status int) map[string]any {
		return testutil.Request(t, r, actor, method, path, input, status)
	}
	create := func(name string) map[string]any {
		return testutil.Object(t, call(f.OwnerID, "POST", base, map[string]any{"name": name, "description_html": "<script>unsafe()</script><p>safe</p>"}, 201))
	}
	first := create("First")
	firstID := first["id"].(string)
	if first["sequence_id"] != float64(1) || first["description_html"] != "<p>safe</p>" {
		t.Fatalf("database defaults or HTML sanitation failed: %#v", first)
	}
	call(f.GuestID, "POST", base, map[string]any{"name": "Cannot create directly"}, 403)
	hidden := f.Issue(t, f.OtherProjectID, f.OtherStateID, f.OwnerID, "Hidden task", 1)
	call(f.MemberID, "GET", base+"/"+hidden.String(), nil, 404)
	call(f.OwnerID, "POST", base, map[string]any{"name": "Invalid related scope", "state_id": f.OtherStateID}, 400)
	call(f.OwnerID, "POST", base, map[string]any{"name": "Invalid assignee", "assignee_ids": []uuid.UUID{f.OutsiderID}}, 400)
	second := create("Second")
	if second["sequence_id"] != float64(2) {
		t.Fatal("failed create consumed a project sequence outside the transaction")
	}
	secondID := second["id"].(string)
	call(f.OwnerID, "PATCH", base+"/"+firstID, map[string]any{"name": "Wrong date", "start_date": "2026-09-09", "target_date": "2026-09-01", "version": 1}, 400)
	call(f.OwnerID, "PATCH", base+"/"+firstID, map[string]any{"is_draft": nil, "version": 1}, 400)
	label := uuid.New()
	f.Exec(t, "INSERT INTO labels(id,workspace_id,project_id,name) VALUES($1,$2,$3,'API')", label, f.WorkspaceID, f.ProjectID)
	cycle := testutil.Object(t, call(f.OwnerID, "POST", p+"/cycles", map[string]any{"name": "Sprint"}, 201))
	module := testutil.Object(t, call(f.OwnerID, "POST", p+"/modules", map[string]any{"name": "Module"}, 201))
	changed := testutil.Object(t, call(f.OwnerID, "PATCH", base+"/"+firstID, map[string]any{"version": 1, "assignee_ids": []uuid.UUID{f.OwnerID, f.MemberID}, "label_ids": []uuid.UUID{label}, "module_ids": []string{module["id"].(string)}, "cycle_id": cycle["id"]}, 200))
	if len(changed["assignee_ids"].([]any)) != 2 || len(changed["label_ids"].([]any)) != 1 || len(changed["module_ids"].([]any)) != 1 || changed["cycle_id"] != cycle["id"] {
		t.Fatal("multi-relation update did not persist every association")
	}
	call(f.OwnerID, "PATCH", base+"/bulk", map[string]any{"ids": []string{firstID, secondID}, "changes": map[string]any{"priority": "urgent"}, "versions": map[string]int{firstID: 2, secondID: 999}}, 409)
	for _, id := range []string{firstID, secondID} {
		item := testutil.Object(t, call(f.OwnerID, "GET", base+"/"+id, nil, 200))
		if item["priority"] != "none" {
			t.Fatal("bulk version conflict partially committed other work items")
		}
	}
	guestItem := f.Issue(t, f.ProjectID, f.StateID, f.GuestID, "Guest parent", 3)
	child := create("Another author's child")
	call(f.OwnerID, "PATCH", base+"/"+child["id"].(string), map[string]any{"parent_id": guestItem, "version": 1}, 200)
	f.Exec(t, "UPDATE project_members SET role=20 WHERE project_id=$1 AND user_id=$2", f.ProjectID, f.GuestID)
	call(f.GuestID, "POST", base, map[string]any{"name": "Guest role remains capped"}, 403)
	call(f.OwnerID, "POST", base+"/"+guestItem.String()+"/relations", map[string]any{"target_id": firstID, "relation_type": "related"}, 201)
	if values := testutil.Array(t, call(f.GuestID, "GET", base+"/"+guestItem.String()+"/relations", nil, 200)); len(values) != 0 {
		t.Fatal("guest relation exposed another creator's title")
	}
	guestDetail := testutil.Object(t, call(f.GuestID, "GET", base+"/"+guestItem.String(), nil, 200))
	if guestDetail["sub_item_count"] != float64(0) {
		t.Fatal("guest detail leaked hidden child count")
	}
	guestList := testutil.Array(t, call(f.GuestID, "GET", base, nil, 200))
	if len(guestList) != 1 || guestList[0].(map[string]any)["sub_item_count"] != float64(0) {
		t.Fatal("guest list leaked hidden child count or another work item")
	}
	if workspaceList := testutil.Array(t, call(f.GuestID, "GET", w+"/issues", nil, 200)); len(workspaceList) != 1 || workspaceList[0].(map[string]any)["id"] != guestItem.String() {
		t.Fatal("workspace guest role was elevated by a project role")
	}
	call(f.GuestID, "PATCH", base+"/"+firstID, map[string]any{"name": "Hijack", "version": 2}, 404)
	call(f.OwnerID, "POST", base+"/"+firstID+"/links", map[string]any{"url": "javascript:alert(1)"}, 400)
	comment := testutil.Object(t, call(f.MemberID, "POST", base+"/"+firstID+"/comments", map[string]any{"body_html": "<p>Review</p>"}, 201))
	reaction := testutil.Object(t, call(f.MemberID, "POST", base+"/"+firstID+"/reactions", map[string]any{"emoji": "👍", "comment_id": comment["id"]}, 201))
	if reaction["work_item_id"] != nil || reaction["comment_id"] != comment["id"] {
		t.Fatal("comment reaction violated exclusive target contract")
	}
	call(f.MemberID, "POST", base+"/"+firstID+"/reactions", map[string]any{"emoji": "👍", "comment_id": comment["id"]}, 409)
	call(f.MemberID, "POST", base+"/"+firstID+"/subscribers", nil, 200)
	call(f.OwnerID, "POST", base+"/"+firstID+"/relations", map[string]any{"target_id": secondID, "relation_type": "blocks"}, 201)
	nid := uuid.New()
	f.Exec(t, "INSERT INTO notifications(id,workspace_id,project_id,user_id,entity_type,entity_id,title) VALUES($1,$2,$3,$4,'issue',$5,'Old scope')", nid, f.WorkspaceID, f.ProjectID, f.MemberID, firstID)
	f.Exec(t, "INSERT INTO intake_items(id,workspace_id,project_id,work_item_id,submitted_by) VALUES($1,$2,$3,$4,$5)", uuid.New(), f.WorkspaceID, f.ProjectID, firstID, f.MemberID)
	f.Exec(t, "INSERT INTO public_votes(id,workspace_id,project_id,work_item_id,user_id) VALUES($1,$2,$3,$4,$5)", uuid.New(), f.WorkspaceID, f.ProjectID, firstID, f.MemberID)
	call(f.MemberID, "POST", base+"/"+firstID+"/move", map[string]any{"project_id": f.OtherProjectID, "version": 2}, 404)
	moved := testutil.Object(t, call(f.OwnerID, "POST", base+"/"+firstID+"/move", map[string]any{"project_id": f.OtherProjectID, "version": 2}, 200))
	if moved["project_id"] != f.OtherProjectID.String() || moved["sequence_id"] != float64(2) {
		t.Fatal("move failed to assign target scope/sequence")
	}
	call(f.OwnerID, "GET", base+"/"+firstID, nil, 404)
	if notifications := testutil.Array(t, call(f.MemberID, "GET", w+"/notifications", nil, 200)); len(notifications) != 0 {
		t.Fatal("moved work item's notification leaked into source project")
	}
	var reactionProject uuid.UUID
	if err := f.DB.SQL.QueryRowContext(context.Background(), "SELECT project_id FROM reactions WHERE id=$1", reaction["id"]).Scan(&reactionProject); err != nil || reactionProject != f.OtherProjectID {
		t.Fatalf("comment reaction did not follow move: %v", err)
	}
	for _, table := range []string{"work_item_assignees", "work_item_labels", "cycle_items", "module_items", "work_item_subscribers", "intake_items", "public_votes"} {
		var count int
		if err := f.DB.SQL.QueryRowContext(context.Background(), "SELECT count(*) FROM "+table+" WHERE work_item_id=$1 AND deleted_at IS NULL", firstID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("move retained invalid %s association: %d %v", table, count, err)
		}
	}
}

func concurrentPatch(r *gin.Engine, actor uuid.UUID, path string, body map[string]any, start <-chan struct{}, result chan<- int) {
	concurrentRequest(r, actor, "PATCH", path, body, start, result)
}

func concurrentRequest(r *gin.Engine, actor uuid.UUID, method, path string, body map[string]any, start <-chan struct{}, result chan<- int) {
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-Actor", actor.String())
	recorder := httptest.NewRecorder()
	<-start
	r.ServeHTTP(recorder, req)
	result <- recorder.Code
}

func TestConcurrentHierarchyAndVersionWrites(t *testing.T) {
	f := testutil.New(t)
	r := f.Router(workitems.Register)
	base := f.Prefix() + "/issues"
	create := func(name string) map[string]any {
		return testutil.Object(t, testutil.Request(t, r, f.OwnerID, "POST", base, map[string]any{"name": name}, 201))
	}
	a, b := create("A"), create("B")
	start := make(chan struct{})
	result := make(chan int, 2)
	go concurrentPatch(r, f.OwnerID, base+"/"+a["id"].(string), map[string]any{"parent_id": b["id"], "version": 1}, start, result)
	go concurrentPatch(r, f.OwnerID, base+"/"+b["id"].(string), map[string]any{"parent_id": a["id"], "version": 1}, start, result)
	close(start)
	codes := []int{<-result, <-result}
	sort.Ints(codes)
	if codes[0] != 200 || codes[1] != 400 {
		t.Fatalf("concurrent parent assignments: %v; one must be rejected", codes)
	}
	var cycle bool
	if err := f.DB.SQL.QueryRowContext(context.Background(), "SELECT EXISTS(SELECT 1 FROM work_items x JOIN work_items y ON y.id=x.parent_id WHERE y.parent_id=x.id AND x.deleted_at IS NULL AND y.deleted_at IS NULL)").Scan(&cycle); err != nil || cycle {
		t.Fatalf("persisted hierarchy cycle: %v %v", cycle, err)
	}
	c := create("Concurrent description")
	start = make(chan struct{})
	result = make(chan int, 2)
	for _, name := range []string{"writer one", "writer two"} {
		go concurrentPatch(r, f.OwnerID, base+"/"+c["id"].(string), map[string]any{"name": name, "version": 1}, start, result)
	}
	close(start)
	codes = []int{<-result, <-result}
	sort.Ints(codes)
	if codes[0] != 200 || codes[1] != 409 {
		t.Fatalf("concurrent version writes: %v; stale writer must receive 409", codes)
	}
	final := testutil.Object(t, testutil.Request(t, r, f.OwnerID, "GET", base+"/"+c["id"].(string), nil, 200))
	if final["version"] != float64(2) {
		t.Fatal("concurrent edit applied more than once")
	}
}
