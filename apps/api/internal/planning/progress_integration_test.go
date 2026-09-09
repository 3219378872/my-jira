package planning_test

import (
	"testing"

	"github.com/google/uuid"
	"my-jira/apps/api/internal/planning"
	"my-jira/apps/api/internal/support/testutil"
)

func TestCycleTransferFreezesScopedProgressAndEvents(t *testing.T) {
	f := testutil.New(t)
	r := f.Router(planning.Register)
	p := f.Prefix()
	call := func(actor uuid.UUID, method, path string, body any, status int) map[string]any {
		return testutil.Request(t, r, actor, method, path, body, status)
	}
	completedState, label := uuid.New(), uuid.New()
	f.Exec(t, "INSERT INTO states(id,workspace_id,project_id,name,group_name,color) VALUES($1,$2,$3,'Done','completed','#008800')", completedState, f.WorkspaceID, f.ProjectID)
	f.Exec(t, "INSERT INTO labels(id,workspace_id,project_id,name,color) VALUES($1,$2,$3,'Backend','#008888')", label, f.WorkspaceID, f.ProjectID)
	done := f.Issue(t, f.ProjectID, completedState, f.OwnerID, "Already delivered", 1)
	memberItem := f.Issue(t, f.ProjectID, f.StateID, f.OwnerID, "Owner pending", 2)
	guestItem := f.Issue(t, f.ProjectID, f.StateID, f.GuestID, "Guest pending", 3)
	f.Exec(t, "UPDATE work_items SET estimate=2,completed_at='2026-09-02' WHERE id=$1", done)
	f.Exec(t, "UPDATE work_items SET estimate=3 WHERE id IN($1,$2)", memberItem, guestItem)
	for _, id := range []uuid.UUID{done, memberItem, guestItem} {
		f.Exec(t, "INSERT INTO work_item_assignees(id,workspace_id,project_id,work_item_id,user_id) VALUES($1,$2,$3,$4,$5)", uuid.New(), f.WorkspaceID, f.ProjectID, id, f.MemberID)
		f.Exec(t, "INSERT INTO work_item_labels(id,workspace_id,project_id,work_item_id,label_id) VALUES($1,$2,$3,$4,$5)", uuid.New(), f.WorkspaceID, f.ProjectID, id, label)
	}
	source := testutil.Object(t, call(f.OwnerID, "POST", p+"/cycles", map[string]any{"name": "Finished iteration", "start_date": "2026-09-01", "end_date": "2026-09-09"}, 201))["id"].(string)
	target := testutil.Object(t, call(f.OwnerID, "POST", p+"/cycles", map[string]any{"name": "Next iteration"}, 201))["id"].(string)
	ended := testutil.Object(t, call(f.OwnerID, "POST", p+"/cycles", map[string]any{"name": "Old iteration", "start_date": "2000-01-01", "end_date": "2000-01-02"}, 201))["id"].(string)
	base := p + "/cycles/" + source
	call(f.OwnerID, "POST", base+"/items", map[string]any{"work_item_ids": []uuid.UUID{done, memberItem, guestItem}}, 200)
	progress := testutil.Object(t, call(f.OwnerID, "GET", base+"/progress", nil, 200))
	if progress["total"] != float64(3) || progress["estimate_total"] != float64(8) || progress["estimate_completed"] != float64(2) || len(progress["burndown"].([]any)) != 9 {
		t.Fatalf("live progress incorrect: %#v", progress)
	}
	for _, key := range []string{"assignees", "labels"} {
		rows := progress[key].([]any)
		if len(rows) != 1 || rows[0].(map[string]any)["count"] != float64(3) || rows[0].(map[string]any)["estimate_completed"] != float64(2) {
			t.Fatalf("%s distribution incorrect: %#v", key, rows)
		}
	}
	var beforeEvents int
	if err := f.DB.SQL.QueryRow("SELECT count(*) FROM outbox_events").Scan(&beforeEvents); err != nil {
		t.Fatal(err)
	}
	call(f.OwnerID, "POST", base+"/transfer", map[string]any{"target_cycle_id": target, "work_item_ids": []uuid.UUID{guestItem, done}}, 400)
	call(f.OwnerID, "POST", base+"/transfer", map[string]any{"target_cycle_id": ended}, 400)
	var afterEvents int
	var hasSnapshot bool
	if err := f.DB.SQL.QueryRow("SELECT progress_snapshot IS NOT NULL FROM cycles WHERE id=$1", source).Scan(&hasSnapshot); err != nil {
		t.Fatal(err)
	}
	if err := f.DB.SQL.QueryRow("SELECT count(*) FROM outbox_events").Scan(&afterEvents); err != nil {
		t.Fatal(err)
	}
	if hasSnapshot || afterEvents != beforeEvents {
		t.Fatal("failed transfer persisted a snapshot or event")
	}
	transferred := testutil.Object(t, call(f.OwnerID, "POST", base+"/transfer", map[string]any{"target_cycle_id": target}, 200))
	if len(transferred["work_item_ids"].([]any)) != 2 {
		t.Fatal("completed work item was transferred")
	}
	frozen := testutil.Object(t, call(f.OwnerID, "GET", base+"/progress", nil, 200))
	if frozen["is_snapshot"] != true || frozen["total"] != float64(3) || frozen["snapshot_at"] == nil {
		t.Fatalf("cycle did not retain pre-transfer statistics: %#v", frozen)
	}
	f.Exec(t, "UPDATE work_items SET state_id=$2,estimate=100,completed_at=now() WHERE id=$1", guestItem, completedState)
	guest := testutil.Object(t, call(f.GuestID, "GET", base+"/progress", nil, 200))
	if guest["total"] != float64(1) || guest["completed"] != float64(0) || guest["estimate_total"] != float64(3) {
		t.Fatalf("guest snapshot leaked other creators or changed with live work: %#v", guest)
	}
	module := testutil.Object(t, call(f.OwnerID, "POST", p+"/modules", map[string]any{"name": "Guest-visible delivery"}, 201))["id"].(string)
	call(f.OwnerID, "POST", p+"/modules/"+module+"/items", map[string]any{"work_item_ids": []uuid.UUID{done, memberItem, guestItem}}, 200)
	for _, enabled := range []bool{true, false} {
		f.Exec(t, "UPDATE projects SET guest_can_view_all=$2 WHERE id=$1", f.ProjectID, enabled)
		count := float64(1)
		if enabled {
			count = 3
		}
		for _, path := range []string{base + "/progress", p + "/modules/" + module + "/progress"} {
			if value := testutil.Object(t, call(f.GuestID, "GET", path, nil, 200)); value["total"] != count {
				t.Fatalf("guest read setting did not scope live and frozen progress: %#v", value)
			}
		}
		if value := testutil.Object(t, call(f.GuestID, "GET", p+"/modules/"+module, nil, 200)); value["work_item_count"] != count {
			t.Fatal("guest read setting did not scope planning counts")
		}
		if items := testutil.Array(t, call(f.GuestID, "GET", p+"/modules/"+module+"/items", nil, 200)); len(items) != int(count) {
			t.Fatal("guest read setting did not scope associated work items")
		}
	}
	live := testutil.Object(t, call(f.OwnerID, "GET", base+"/progress?live=true", nil, 200))
	if live["is_snapshot"] != false || live["total"] != float64(1) {
		t.Fatal("live progress did not reflect remaining completed item")
	}
	call(f.OwnerID, "POST", base+"/transfer", map[string]any{"target_cycle_id": target}, 200)
	if retried := testutil.Object(t, call(f.OwnerID, "GET", base+"/progress", nil, 200)); retried["total"] != float64(3) {
		t.Fatal("repeated transfer replaced the original progress snapshot")
	}
	cycle := testutil.Object(t, call(f.GuestID, "GET", base, nil, 200))
	if _, ok := cycle["progress_snapshot"]; ok {
		t.Fatal("raw snapshot bypassed creator visibility")
	}
	var version int
	if err := f.DB.SQL.QueryRow("SELECT version FROM work_items WHERE id=$1", memberItem).Scan(&version); err != nil || version != 4 {
		t.Fatalf("association changes did not advance optimistic version: %d %v", version, err)
	}
	var sourceEvents, targetEvents int
	if err := f.DB.SQL.QueryRow("SELECT count(*) FILTER(WHERE payload->>'entity_id'=$1),count(*) FILTER(WHERE payload->>'entity_id'=$2) FROM outbox_events WHERE topic='entity.changed' AND payload->>'event'='cycle.items_changed'", source, target).Scan(&sourceEvents, &targetEvents); err != nil || sourceEvents < 2 || targetEvents < 1 {
		t.Fatalf("cycle association events missing: %d %d %v", sourceEvents, targetEvents, err)
	}
}
