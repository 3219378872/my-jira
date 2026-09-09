package workitems_test

import (
	"context"
	"encoding/json"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"my-jira/apps/api/internal/documents"
	"my-jira/apps/api/internal/planning"
	"my-jira/apps/api/internal/support/testutil"
	"my-jira/apps/api/internal/workitems"
)

func TestAdvancedFiltersAndGroupedPagination(t *testing.T) {
	f := testutil.New(t)
	r := f.Router(workitems.Register, planning.Register)
	base := f.Prefix() + "/issues"
	call := func(actor uuid.UUID, method, path string, body any, status int) map[string]any {
		return testutil.Request(t, r, actor, method, path, body, status)
	}
	create := func(name, priority string, assignees []uuid.UUID) map[string]any {
		return testutil.Object(t, call(f.OwnerID, "POST", base, map[string]any{"name": name, "priority": priority, "assignee_ids": assignees, "start_date": "2026-09-01", "target_date": "2026-09-30"}, 201))
	}
	a := create("Alpha", "high", []uuid.UUID{f.OwnerID, f.MemberID})
	b := create("Beta", "high", []uuid.UUID{f.OwnerID})
	create("Gamma", "low", []uuid.UUID{})
	f.Issue(t, f.OtherProjectID, f.OtherStateID, f.OwnerID, "Hidden", 1)
	filter := map[string]any{"and": []any{map[string]any{"or": []any{map[string]any{"field": "name", "op": "contains", "value": "Alpha"}, map[string]any{"field": "priority", "value": "low"}}}, map[string]any{"not": map[string]any{"field": "assignee_id", "op": "is_empty"}}}}
	raw, _ := json.Marshal(filter)
	got := testutil.Array(t, call(f.OwnerID, "GET", base+"?filter="+url.QueryEscape(string(raw)), nil, 200))
	if len(got) != 1 || got[0].(map[string]any)["id"] != a["id"] {
		t.Fatalf("boolean filter returned incorrect items: %#v", got)
	}
	for query, want := range map[string]int{"priority=high,low": 3, "assignee_id=none": 1, "assignee_id=me": 2, "state_group=unstarted&target_date_after=2026-09-15": 3, "project_id=" + f.OtherProjectID.String(): 0} {
		if got := testutil.Array(t, call(f.OwnerID, "GET", base+"?"+query, nil, 200)); len(got) != want {
			t.Fatalf("%s returned %d, want %d", query, len(got), want)
		}
	}
	filter = map[string]any{"field": "assignee_id", "op": "all", "value": []uuid.UUID{f.OwnerID, f.MemberID}}
	raw, _ = json.Marshal(filter)
	if got := testutil.Array(t, call(f.OwnerID, "GET", base+"?filter="+url.QueryEscape(string(raw)), nil, 200)); len(got) != 1 {
		t.Fatal("all-assignee match must require both associations")
	}
	filter = map[string]any{"field": "assignee_id", "op": "not_in", "value": []uuid.UUID{f.OwnerID}}
	raw, _ = json.Marshal(filter)
	if got := testutil.Array(t, call(f.OwnerID, "GET", base+"?filter="+url.QueryEscape(string(raw)), nil, 200)); len(got) != 1 {
		t.Fatal("negative relation filter did not include unassigned item")
	}
	call(f.OwnerID, "GET", base+"?filter="+url.QueryEscape(`{"field":"name);DROP TABLE users;--","value":"x"}`), nil, 400)
	call(f.OwnerID, "GET", base+"?state_group=unknown", nil, 400)
	call(f.OwnerID, "GET", base+"?cursor=bad", nil, 400)
	first := call(f.OwnerID, "GET", base+"?limit=2&order_by=name", nil, 200)
	cursor := first["pagination"].(map[string]any)["next_cursor"].(string)
	if next := testutil.Array(t, call(f.OwnerID, "GET", base+"?limit=2&order_by=name&cursor="+cursor, nil, 200)); len(next) != 1 || next[0].(map[string]any)["name"] != "Gamma" {
		t.Fatal("ordinary pagination repeated or skipped a stable item")
	}
	grouped := testutil.Array(t, call(f.OwnerID, "GET", base+"?group_by=priority&show_empty=true&limit=1&order_by=name", nil, 200))
	if len(grouped) != 5 {
		t.Fatalf("empty priority groups missing: %d", len(grouped))
	}
	var high map[string]any
	for _, entry := range grouped {
		g := entry.(map[string]any)
		if g["key"] == "high" {
			high = g
		}
	}
	if high == nil || high["total"] != float64(2) || high["items"].([]any)[0].(map[string]any)["id"] != a["id"] {
		t.Fatalf("wrong high group: %#v", high)
	}
	cursor = high["pagination"].(map[string]any)["next_cursor"].(string)
	nextGroup := testutil.Array(t, call(f.OwnerID, "GET", base+"?group_by=priority&group_key=high&limit=1&order_by=name&cursor="+cursor, nil, 200))
	if len(nextGroup) != 1 || nextGroup[0].(map[string]any)["items"].([]any)[0].(map[string]any)["id"] != b["id"] {
		t.Fatal("group pagination did not advance within its group")
	}
	subgroups := testutil.Array(t, call(f.OwnerID, "GET", base+"?group_by=priority&sub_group_by=assignee_id&group_key=high", nil, 200))
	parent := subgroups[0].(map[string]any)
	if parent["total"] != float64(2) || len(parent["groups"].([]any)) != 2 {
		t.Fatalf("subgroup total double-counted multi-assignee item: %#v", parent)
	}
	mention := map[string]any{"type": "doc", "content": []any{map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "mention", "attrs": map[string]any{"id": f.MemberID.String(), "label": "Member"}}}}}}
	call(f.OwnerID, "PATCH", base+"/"+a["id"].(string), map[string]any{"version": 1, "description_json": mention}, 200)
	if got := testutil.Array(t, call(f.MemberID, "GET", base+"?mention_id=me", nil, 200)); len(got) != 1 || got[0].(map[string]any)["id"] != a["id"] {
		t.Fatal("structured mention filter failed")
	}
	call(f.GuestID, "GET", base+"?deleted=true", nil, 403)
}

func TestEstimateLifecycleAndCycleDateChecks(t *testing.T) {
	f := testutil.New(t)
	r := f.Router(workitems.Register, planning.Register)
	p := f.Prefix()
	base := p + "/issues"
	call := func(actor uuid.UUID, method, path string, body any, status int) map[string]any {
		return testutil.Request(t, r, actor, method, path, body, status)
	}
	createScheme := func(kind string, points []map[string]any) map[string]any {
		return testutil.Object(t, call(f.OwnerID, "POST", p+"/estimates", map[string]any{"name": kind, "kind": kind, "points": points}, 201))
	}
	scheme := createScheme("points", []map[string]any{{"label": "One", "numeric_value": 1}, {"label": "Three", "numeric_value": 3}})
	sid := scheme["id"].(string)
	point := scheme["points"].([]any)[0].(map[string]any)["id"].(string)
	call(f.MemberID, "PATCH", p+"/estimate-settings", map[string]any{"estimate_id": sid}, 403)
	call(f.OwnerID, "PATCH", p+"/estimate-settings", map[string]any{"estimate_id": sid}, 200)
	issue := testutil.Object(t, call(f.OwnerID, "POST", base, map[string]any{"name": "Estimated", "estimate_point_id": point}, 201))
	iid := issue["id"].(string)
	if issue["estimate"] != float64(1) || issue["estimate_point_detail"].(map[string]any)["label"] != "One" {
		t.Fatal("point selection was not persisted/projected")
	}
	call(f.OwnerID, "PATCH", base+"/"+iid, map[string]any{"version": 1, "estimate": 99}, 400)
	call(f.OwnerID, "PATCH", p+"/estimates/"+sid+"/points/"+point, map[string]any{"numeric_value": 2, "label": "Two"}, 200)
	issue = testutil.Object(t, call(f.OwnerID, "GET", base+"/"+iid, nil, 200))
	if issue["estimate"] != float64(2) || issue["version"] != float64(2) {
		t.Fatal("point value update did not update linked issue and version")
	}
	category := createScheme("categories", []map[string]any{{"label": "Small"}, {"label": "Large"}})
	cid := category["id"].(string)
	small := category["points"].([]any)[0].(map[string]any)["id"].(string)
	large := category["points"].([]any)[1].(map[string]any)["id"].(string)
	call(f.OwnerID, "PATCH", base+"/"+iid, map[string]any{"version": 2, "estimate_point_id": f.OtherStateID}, 400)
	call(f.OwnerID, "PATCH", p+"/estimate-settings", map[string]any{"estimate_id": cid}, 200)
	issue = testutil.Object(t, call(f.OwnerID, "GET", base+"/"+iid, nil, 200))
	if issue["estimate_point_id"] != point || issue["estimate"] != float64(2) || issue["version"] != float64(2) {
		t.Fatal("scheme switch altered an existing assignment")
	}
	call(f.OwnerID, "PATCH", base+"/"+iid, map[string]any{"version": 2, "estimate_point_id": small}, 200)
	call(f.OwnerID, "DELETE", p+"/estimates/"+cid+"/points/"+small+"?replacement_id="+point, nil, 400)
	call(f.OwnerID, "DELETE", p+"/estimates/"+cid+"/points/"+small+"?replacement_id="+large, nil, 204)
	issue = testutil.Object(t, call(f.OwnerID, "GET", base+"/"+iid, nil, 200))
	if issue["estimate_point_id"] != large || issue["estimate"] != nil {
		t.Fatal("category deletion did not reassign to replacement")
	}
	call(f.OwnerID, "DELETE", p+"/estimates/"+cid, nil, 204)
	issue = testutil.Object(t, call(f.OwnerID, "GET", base+"/"+iid, nil, 200))
	if issue["estimate_point_id"] != nil {
		t.Fatal("deleting active scheme retained point reference")
	}
	if settings := testutil.Object(t, call(f.OwnerID, "GET", p+"/estimate-settings", nil, 200)); settings["estimate_id"] != nil {
		t.Fatal("active scheme pointer was not cleared")
	}
	call(f.OwnerID, "PATCH", p+"/estimate-settings", map[string]any{"estimate_id": sid}, 200)
	issue = testutil.Object(t, call(f.OwnerID, "PATCH", base+"/"+iid, map[string]any{"version": issue["version"], "estimate_point_id": point}, 200))
	start := make(chan struct{})
	results := make(chan int, 2)
	go concurrentPatch(r, f.OwnerID, base+"/"+iid, map[string]any{"version": issue["version"], "estimate_point_id": point}, start, results)
	go concurrentRequest(r, f.OwnerID, "DELETE", p+"/estimates/"+sid, nil, start, results)
	close(start)
	for range 2 {
		if code := <-results; code != 200 && code != 204 && code != 409 {
			t.Fatalf("concurrent estimate deletion/write returned %d", code)
		}
	}
	issue = testutil.Object(t, call(f.OwnerID, "GET", base+"/"+iid, nil, 200))
	if issue["estimate_point_id"] != nil || issue["estimate"] != nil {
		t.Fatal("concurrent deletion retained a stale estimate assignment")
	}
	cycle := testutil.Object(t, call(f.OwnerID, "POST", p+"/cycles", map[string]any{"name": "September", "start_date": "2026-09-01", "end_date": "2026-09-10"}, 201))
	cycleID := cycle["id"].(string)
	check := func(body map[string]any) map[string]any {
		return testutil.Object(t, call(f.OwnerID, "POST", p+"/cycles/check-dates", body, 200))
	}
	if result := check(map[string]any{"start_date": "2026-09-10", "end_date": "2026-09-11"}); result["available"] != false || len(result["conflicts"].([]any)) != 1 {
		t.Fatal("cycle endpoint missed inclusive boundary overlap")
	}
	if result := check(map[string]any{"start_date": "2026-09-01", "end_date": "2026-09-10", "exclude_cycle_id": cycleID}); result["available"] != true {
		t.Fatal("cycle editing did not exclude itself")
	}
	if result := check(map[string]any{"start_date": "2026-09-11", "end_date": "2026-09-20"}); result["available"] != true {
		t.Fatal("nonoverlapping dates were rejected")
	}
	call(f.OwnerID, "POST", p+"/cycles/check-dates", map[string]any{"start_date": "2026-09-01", "end_date": "2026-09-10", "exclude_cycle_id": f.OtherProjectID}, 404)
	call(f.OwnerID, "POST", p+"/cycles", map[string]any{"name": "Incomplete", "start_date": "2026-09-01"}, 400)
}

func TestProjectAutomationAndBoundedHistoryCleanup(t *testing.T) {
	f := testutil.New(t)
	r := f.Router(workitems.Register, planning.Register, documents.Register)
	p := f.Prefix()
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	call := func(actor uuid.UUID, method, path string, body any, status int) map[string]any {
		return testutil.Request(t, r, actor, method, path, body, status)
	}
	done, cancelled := uuid.New(), uuid.New()
	for id, group := range map[uuid.UUID]string{done: "completed", cancelled: "cancelled"} {
		f.Exec(t, "INSERT INTO states(id,workspace_id,project_id,name,group_name) VALUES($1,$2,$3,$4,$4)", id, f.WorkspaceID, f.ProjectID, group)
	}
	archive := f.Issue(t, f.ProjectID, done, f.OwnerID, "Old complete", 1)
	closeID := f.Issue(t, f.ProjectID, f.StateID, f.OwnerID, "Old active", 2)
	recent := f.Issue(t, f.ProjectID, f.StateID, f.OwnerID, "Recent", 3)
	draft := f.Issue(t, f.ProjectID, f.StateID, f.OwnerID, "Draft", 4)
	inCycle := f.Issue(t, f.ProjectID, f.StateID, f.OwnerID, "Future cycle", 5)
	inModule := f.Issue(t, f.ProjectID, f.StateID, f.OwnerID, "Future module", 6)
	intake := f.Issue(t, f.ProjectID, f.StateID, f.GuestID, "Pending intake", 7)
	f.Exec(t, "UPDATE work_items SET updated_at=$2 WHERE project_id=$1", f.ProjectID, now.AddDate(0, 0, -45))
	f.Exec(t, "UPDATE work_items SET updated_at=$2 WHERE id=$1", recent, now)
	f.Exec(t, "UPDATE work_items SET is_draft=true WHERE id=$1", draft)
	cy := testutil.Object(t, call(f.OwnerID, "POST", p+"/cycles", map[string]any{"name": "Active", "start_date": "2026-09-01", "end_date": "2026-09-20"}, 201))
	mo := testutil.Object(t, call(f.OwnerID, "POST", p+"/modules", map[string]any{"name": "Current", "target_date": "2026-09-20"}, 201))
	call(f.OwnerID, "POST", p+"/cycles/"+cy["id"].(string)+"/items", map[string]any{"work_item_ids": []uuid.UUID{inCycle}}, 200)
	call(f.OwnerID, "POST", p+"/modules/"+mo["id"].(string)+"/items", map[string]any{"work_item_ids": []uuid.UUID{inModule}}, 200)
	f.Exec(t, "INSERT INTO intake_items(id,workspace_id,project_id,work_item_id,submitted_by) VALUES($1,$2,$3,$4,$5)", uuid.New(), f.WorkspaceID, f.ProjectID, intake, f.GuestID)
	call(f.MemberID, "PATCH", p+"/automation", map[string]any{"close_after_months": 1}, 403)
	call(f.OwnerID, "PATCH", p+"/automation", map[string]any{"close_after_months": 1, "close_state_id": f.OtherStateID}, 400)
	call(f.OwnerID, "PATCH", p+"/automation", map[string]any{"close_after_months": 1, "archive_after_months": 1, "close_state_id": cancelled}, 200)
	result, err := workitems.RunMaintenance(context.Background(), f.Deps, now, f.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Archived != 1 || result.Closed != 1 {
		t.Fatalf("maintenance affected incorrect set: %#v", result)
	}
	if again, err := workitems.RunMaintenance(context.Background(), f.Deps, now, f.ProjectID); err != nil || again.Archived != 0 || again.Closed != 0 {
		t.Fatalf("maintenance was not idempotent: %#v %v", again, err)
	}
	for _, id := range []uuid.UUID{recent, draft, inCycle, inModule, intake} {
		var state uuid.UUID
		if err := f.DB.SQL.QueryRowContext(context.Background(), "SELECT state_id FROM work_items WHERE id=$1", id).Scan(&state); err != nil || state != f.StateID {
			t.Fatalf("ineligible item changed: %s %v", id, err)
		}
	}
	var archivedAt *time.Time
	var state uuid.UUID
	var version int
	if err := f.DB.SQL.QueryRowContext(context.Background(), "SELECT archived_at FROM work_items WHERE id=$1", archive).Scan(&archivedAt); err != nil || archivedAt == nil {
		t.Fatal("archive was not persisted")
	}
	if err := f.DB.SQL.QueryRowContext(context.Background(), "SELECT state_id,version FROM work_items WHERE id=$1", closeID).Scan(&state, &version); err != nil || state != cancelled || version != 2 {
		t.Fatal("closure did not persist correct state/version")
	}
	page := testutil.Object(t, call(f.OwnerID, "POST", p+"/pages", map[string]any{"name": "History"}, 201))
	pageID := page["id"].(string)
	for v := 2; v <= 25; v++ {
		f.Exec(t, "INSERT INTO page_versions(id,workspace_id,page_id,saved_by,version,name,content_json,content_html) VALUES($1,$2,$3,$4,$5,'History','{}','<p>snapshot</p>')", uuid.New(), f.WorkspaceID, pageID, f.OwnerID, v)
	}
	for v := 3; v <= 25; v++ {
		f.Exec(t, "INSERT INTO work_item_versions(id,workspace_id,project_id,work_item_id,saved_by,version,snapshot) VALUES($1,$2,$3,$4,$5,$6,'{}')", uuid.New(), f.WorkspaceID, f.ProjectID, closeID, f.OwnerID, v)
	}
	cleaned, err := workitems.CleanupHistory(context.Background(), f.Deps, now)
	if err != nil {
		t.Fatal(err)
	}
	if cleaned["page_versions"] != 5 || cleaned["work_item_versions"] != 4 {
		t.Fatalf("unexpected retained version counts: %#v", cleaned)
	}
	var count, min int
	if err := f.DB.SQL.QueryRowContext(context.Background(), "SELECT count(*),min(version) FROM page_versions WHERE page_id=$1", pageID).Scan(&count, &min); err != nil || count != 20 || min != 6 {
		t.Fatalf("cleanup did not retain newest twenty: %d %d %v", count, min, err)
	}
}
