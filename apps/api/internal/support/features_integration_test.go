package support_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"testing"

	"github.com/google/uuid"

	"my-jira/apps/api/internal/documents"
	"my-jira/apps/api/internal/planning"
	"my-jira/apps/api/internal/support"
	"my-jira/apps/api/internal/support/testutil"
)

func TestPlanningDocumentsAndPersonalResources(t *testing.T) {
	f := testutil.New(t)
	r := f.Router(planning.Register, documents.Register, support.Register)
	p, w := f.Prefix(), f.WorkspacePrefix()
	call := func(actor uuid.UUID, method, path string, input any, status int) map[string]any {
		return testutil.Request(t, r, actor, method, path, input, status)
	}
	ownerItem := f.Issue(t, f.ProjectID, f.StateID, f.OwnerID, "Visible owner task", 1)
	guestItem := f.Issue(t, f.ProjectID, f.StateID, f.GuestID, "Visible guest task", 2)
	hiddenItem := f.Issue(t, f.OtherProjectID, f.OtherStateID, f.OwnerID, "Hidden task", 1)
	f.Exec(t, "UPDATE project_members SET role=20 WHERE project_id=$1 AND user_id=$2", f.ProjectID, f.GuestID)
	cycle1 := testutil.Object(t, call(f.OwnerID, "POST", p+"/cycles", map[string]any{"name": "Cycle one", "start_date": "2026-09-01", "end_date": "2026-09-09"}, 201))["id"].(string)
	cycle2 := testutil.Object(t, call(f.OwnerID, "POST", p+"/cycles", map[string]any{"name": "Cycle two"}, 201))["id"].(string)
	call(f.OwnerID, "POST", p+"/cycles/"+cycle1+"/items", map[string]any{"work_item_ids": []uuid.UUID{ownerItem, hiddenItem}}, 404)
	if items := testutil.Array(t, call(f.OwnerID, "GET", p+"/cycles/"+cycle1+"/items", nil, 200)); len(items) != 0 {
		t.Fatal("cross-project bulk association was not rolled back")
	}
	call(f.OwnerID, "POST", p+"/cycles/"+cycle1+"/items", map[string]any{"work_item_ids": []uuid.UUID{ownerItem, guestItem}}, 200)
	if items := testutil.Array(t, call(f.GuestID, "GET", p+"/cycles/"+cycle1+"/items", nil, 200)); len(items) != 1 || items[0].(map[string]any)["id"] != guestItem.String() {
		t.Fatal("guest saw another creator's cycle work item")
	}
	progress := testutil.Object(t, call(f.GuestID, "GET", p+"/cycles/"+cycle1+"/progress", nil, 200))
	if progress["total"] != float64(1) {
		t.Fatalf("guest progress leaked aggregate: %#v", progress)
	}
	call(f.OwnerID, "POST", p+"/cycles/"+cycle2+"/items", map[string]any{"work_item_ids": []uuid.UUID{ownerItem}}, 200)
	call(f.OwnerID, "POST", p+"/cycles/"+cycle1+"/transfer", map[string]any{"target_cycle_id": cycle2, "work_item_ids": []uuid.UUID{guestItem, ownerItem}}, 400)
	items := testutil.Array(t, call(f.OwnerID, "GET", p+"/cycles/"+cycle1+"/items", nil, 200))
	if len(items) != 1 || items[0].(map[string]any)["id"] != guestItem.String() {
		t.Fatal("failed cycle transfer did not roll back all associations")
	}
	call(f.OwnerID, "POST", p+"/modules", map[string]any{"name": "Bad module", "member_ids": []uuid.UUID{f.OutsiderID}}, 400)
	module := testutil.Object(t, call(f.OwnerID, "POST", p+"/modules", map[string]any{"name": "Delivery", "member_ids": []uuid.UUID{f.MemberID}, "status": "backlog"}, 201))
	if len(module["member_ids"].([]any)) != 1 {
		t.Fatal("module members were not persisted")
	}
	call(f.MemberID, "DELETE", p+"/cycles/"+cycle1, nil, 403)
	view := testutil.Object(t, call(f.MemberID, "POST", p+"/views", map[string]any{"name": "Personal view", "is_private": true, "filters": map[string]any{"priority": "high"}}, 201))
	call(f.OwnerID, "GET", p+"/views/"+view["id"].(string), nil, 404)

	content := func(text string, version any) map[string]any {
		return map[string]any{"content_html": "<p>" + text + "</p>", "content_json": map[string]any{"type": "doc", "content": []any{map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": text}}}}}, "version": version}
	}
	initial := content("safe", 1)
	initial["name"], initial["content_html"], initial["content_binary"] = "Secret specification", "<script>bad()</script><p>safe</p>", base64.StdEncoding.EncodeToString([]byte{0, 4, 255, 8, 42})
	call(f.OwnerID, "POST", p+"/pages", initial, 403)
	delete(initial, "content_binary")
	doc := testutil.Object(t, call(f.OwnerID, "POST", p+"/pages", initial, 201))
	docID := doc["id"].(string)
	docPath := p + "/pages/" + docID
	call(f.GuestID, "GET", docPath+"/content", nil, 404)
	f.Exec(t, "UPDATE projects SET guest_can_view_all=true WHERE id=$1", f.ProjectID)
	guestContent := testutil.Object(t, call(f.GuestID, "GET", docPath+"/content", nil, 200))
	if guestContent["can_edit"] != false || guestContent["owner_id"] != f.OwnerID.String() || guestContent["name"] != "Secret specification" {
		t.Fatal("collaboration authorization did not describe guest read-only access")
	}
	f.Exec(t, "UPDATE projects SET guest_can_view_all=false WHERE id=$1", f.ProjectID)
	if doc["content_html"] != "<p>safe</p>" {
		t.Fatal("HTML was not sanitized")
	}
	if doc["content_binary"] != "" {
		t.Fatal("ordinary REST creation should not carry unvalidated binary")
	}
	call(f.MemberID, "POST", w+"/favorites", map[string]any{"entity_type": "page", "entity_id": docID}, 201)
	call(f.MemberID, "PUT", docPath+"/content", map[string]any{"content_html": "<p>partial</p>", "version": 1}, 400)
	updated := testutil.Object(t, call(f.MemberID, "PUT", docPath+"/content", content("edited", 1), 200))
	if updated["version"] != float64(2) {
		t.Fatal("version was not incremented")
	}
	if updated["content_binary"] != "" {
		t.Fatal("REST replacement retained obsolete CRDT content")
	}
	call(f.OwnerID, "PUT", docPath+"/content", content("lost write", 1), 409)
	call(f.MemberID, "PATCH", docPath, map[string]any{"is_private": true}, 403)
	locked := testutil.Object(t, call(f.OwnerID, "PATCH", docPath, map[string]any{"is_locked": true}, 200))
	call(f.OwnerID, "PUT", docPath+"/content", content("blocked", locked["version"]), 409)
	call(f.OwnerID, "PATCH", docPath, map[string]any{"is_locked": false}, 200)
	versions := testutil.Array(t, call(f.OwnerID, "GET", docPath+"/versions", nil, 200))
	if len(versions) != 4 {
		t.Fatalf("expected four preserved versions, got %d", len(versions))
	}
	first := versions[len(versions)-1].(map[string]any)
	restored := testutil.Object(t, call(f.OwnerID, "POST", docPath+"/versions/"+first["id"].(string)+"/restore", map[string]any{"version": 4}, 200))
	if restored["content_html"] != "<p>safe</p>" || restored["version"] != float64(5) {
		t.Fatal("restore failed to create a new version")
	}
	comment := testutil.Object(t, call(f.MemberID, "POST", docPath+"/comments", map[string]any{"body_html": "<p>Review</p>"}, 201))
	call(f.GuestID, "PATCH", docPath+"/comments/"+comment["id"].(string), map[string]any{"body_html": "Changed"}, 404)
	call(f.OwnerID, "PATCH", docPath, map[string]any{"is_private": true}, 200)
	call(f.MemberID, "GET", docPath, nil, 404)
	call(f.GuestID, "GET", docPath+"/content", nil, 404)
	if favorites := testutil.Array(t, call(f.MemberID, "GET", w+"/favorites", nil, 200)); len(favorites) != 0 {
		t.Fatal("favorite revealed page after visibility changed")
	}
	search := testutil.Object(t, call(f.MemberID, "GET", w+"/search?q=Secret", nil, 200))
	if len(search["pages"].([]any)) != 0 {
		t.Fatal("private page leaked through search")
	}
	stats := testutil.Object(t, call(f.GuestID, "GET", w+"/analytics", nil, 200))
	if stats["total"] != float64(1) {
		t.Fatalf("guest statistics leaked tasks: %#v", stats)
	}
	guestSearch := testutil.Object(t, call(f.GuestID, "GET", w+"/search?q=Visible", nil, 200))
	if issues := guestSearch["issues"].([]any); len(issues) != 1 || issues[0].(map[string]any)["id"] != guestItem.String() {
		t.Fatal("workspace guest search was elevated by project role")
	}
	f.Exec(t, "UPDATE projects SET guest_can_view_all=true WHERE id=$1", f.ProjectID)
	if stats := testutil.Object(t, call(f.GuestID, "GET", w+"/analytics", nil, 200)); stats["total"] != float64(2) {
		t.Fatal("guest read setting was not applied to workspace analysis")
	}
	if profile := testutil.Object(t, call(f.GuestID, "GET", w+"/profiles/"+f.OwnerID.String()+"/stats", nil, 200)); profile["created"] != float64(1) {
		t.Fatal("guest read setting was not applied to member profile statistics")
	}
	if search := testutil.Object(t, call(f.GuestID, "GET", w+"/search?q=Visible", nil, 200)); len(search["issues"].([]any)) != 2 {
		t.Fatal("guest read setting was not applied to workspace search")
	}
	call(f.GuestID, "POST", w+"/favorites", map[string]any{"entity_type": "issue", "entity_id": ownerItem}, 201)
	if favorites := testutil.Array(t, call(f.GuestID, "GET", w+"/favorites", nil, 200)); len(favorites) != 1 {
		t.Fatal("guest could not resolve favorite for a readable item")
	}
	f.Exec(t, "UPDATE projects SET guest_can_view_all=false WHERE id=$1", f.ProjectID)
	if favorites := testutil.Array(t, call(f.GuestID, "GET", w+"/favorites", nil, 200)); len(favorites) != 0 {
		t.Fatal("favorite ignored the disabled guest read setting")
	}

	sticky := testutil.Object(t, call(f.MemberID, "POST", w+"/stickies", map[string]any{"title": "Personal note", "content_html": "<p>Memo</p>"}, 201))
	call(f.GuestID, "GET", w+"/stickies/"+sticky["id"].(string), nil, 404)
	call(f.MemberID, "PUT", w+"/preferences/home", map[string]any{"value": map[string]any{"first": true}}, 200)
	pref := testutil.Object(t, call(f.MemberID, "PATCH", w+"/preferences/home", map[string]any{"value": map[string]any{"second": true}}, 200))
	if len(pref["value"].(map[string]any)) != 2 {
		t.Fatal("preference merge discarded an existing field")
	}
	nid := uuid.New()
	f.Exec(t, "INSERT INTO notifications(id,workspace_id,project_id,user_id,entity_type,entity_id,title) VALUES($1,$2,$3,$4,'issue',$5,'Assigned')", nid, f.WorkspaceID, f.ProjectID, f.MemberID, ownerItem)
	call(f.GuestID, "PATCH", w+"/notifications/"+nid.String(), map[string]any{"read": true}, 404)
	notification := testutil.Object(t, call(f.MemberID, "PATCH", w+"/notifications/"+nid.String(), map[string]any{"read": true, "archived": true}, 200))
	if notification["read_at"] == nil || notification["archived_at"] == nil {
		t.Fatal("notification state was not saved")
	}
	call(f.MemberID, "POST", w+"/favorites", map[string]any{"entity_type": "cycle", "entity_id": cycle1}, 201)
	f.Exec(t, "UPDATE project_members SET is_active=false WHERE project_id=$1 AND user_id=$2", f.ProjectID, f.MemberID)
	if favorites := testutil.Array(t, call(f.MemberID, "GET", w+"/favorites", nil, 200)); len(favorites) != 0 {
		t.Fatal("favorite survived revoked project access")
	}
	var count int
	if err := f.DB.SQL.QueryRowContext(context.Background(), "SELECT count(*) FROM cycle_items WHERE work_item_id=$1 AND deleted_at IS NULL", ownerItem).Scan(&count); err != nil || count != 1 {
		t.Fatalf("one-cycle invariant failed: %d %v", count, err)
	}
	t.Log(fmt.Sprintf("validated persisted planning, document history, visibility changes and personal resources in %s", f.WorkspaceID))
}

func TestNotificationsRecheckEntityAccess(t *testing.T) {
	f := testutil.New(t)
	r := f.Router(documents.Register, support.Register)
	w := f.WorkspacePrefix()
	call := func(method, path string, input any, status int) map[string]any {
		return testutil.Request(t, r, f.MemberID, method, path, input, status)
	}
	item := f.Issue(t, f.ProjectID, f.StateID, f.OwnerID, "Original membership", 1)
	page := testutil.Object(t, testutil.Request(t, r, f.OwnerID, "POST", f.Prefix()+"/pages", map[string]any{"name": "Public specification"}, 201))
	issueNotification, pageNotification := uuid.New(), uuid.New()
	for _, entry := range []struct {
		id   uuid.UUID
		kind string
		item any
	}{{issueNotification, "work_item", item}, {pageNotification, "page", page["id"]}} {
		f.Exec(t, "INSERT INTO notifications(id,workspace_id,project_id,user_id,entity_type,entity_id,title) VALUES($1,$2,$3,$4,$5,$6,'Visible when created')", entry.id, f.WorkspaceID, f.ProjectID, f.MemberID, entry.kind, entry.item)
	}
	silentNotification := uuid.New()
	f.Exec(t, "INSERT INTO notifications(id,workspace_id,project_id,user_id,entity_type,entity_id,title,data) VALUES($1,$2,$3,$4,'page',$5,'Email only','{\"silent\":true}')", silentNotification, f.WorkspaceID, f.ProjectID, f.MemberID, page["id"])
	call("PATCH", w+"/notifications/"+silentNotification.String(), map[string]any{"read": true}, 404)
	call("PATCH", w+"/preferences/notifications", map[string]any{"value": map[string]any{"email": "yes"}}, 400)
	call("PATCH", w+"/preferences/notifications", map[string]any{"value": map[string]any{"email": false, "mentions": true}}, 200)
	if got := testutil.Array(t, call("GET", w+"/notifications", nil, 200)); len(got) != 2 {
		t.Fatal("member could not see authorized notifications")
	}
	f.Exec(t, "UPDATE workspace_members SET role=5 WHERE workspace_id=$1 AND user_id=$2", f.WorkspaceID, f.MemberID)
	got := testutil.Array(t, call("GET", w+"/notifications", nil, 200))
	if len(got) != 0 {
		t.Fatal("notification retained issue or page content after guest demotion")
	}
	call("PATCH", w+"/notifications/"+issueNotification.String(), map[string]any{"read": true}, 404)
	if count := testutil.Object(t, call("GET", w+"/notifications/unread-count", nil, 200))["count"]; count != float64(0) {
		t.Fatalf("unread count includes hidden entities: %v", count)
	}
	f.Exec(t, "UPDATE projects SET guest_can_view_all=true WHERE id=$1", f.ProjectID)
	if got := testutil.Array(t, call("GET", w+"/notifications", nil, 200)); len(got) != 2 {
		t.Fatal("guest read setting did not expose currently readable notifications")
	}
	testutil.Request(t, r, f.OwnerID, "PATCH", f.Prefix()+"/pages/"+page["id"].(string), map[string]any{"is_private": true}, 200)
	if got := testutil.Array(t, call("GET", w+"/notifications", nil, 200)); len(got) != 1 || got[0].(map[string]any)["entity_type"] != "work_item" {
		t.Fatal("notification retained content after page privacy changed")
	}
	call("PATCH", w+"/notifications/"+pageNotification.String(), map[string]any{"read": true}, 404)
	f.Exec(t, "UPDATE projects SET guest_can_view_all=false WHERE id=$1", f.ProjectID)
	if changed := testutil.Object(t, call("POST", w+"/notifications/mark-all-read", nil, 200))["updated"]; changed != float64(0) {
		t.Fatalf("bulk notification action exposed inaccessible count: %v", changed)
	}
}

func TestNotificationReasonPaginationAndFilteredMarkRead(t *testing.T) {
	f := testutil.New(t)
	r := f.Router(support.Register)
	w := f.WorkspacePrefix()
	item := f.Issue(t, f.ProjectID, f.StateID, f.OwnerID, "Notifications", 1)
	for _, reason := range []string{"mentions", "assigned", "created"} {
		f.Exec(t, "INSERT INTO notifications(id,workspace_id,project_id,user_id,entity_type,entity_id,title,data) VALUES($1,$2,$3,$4,'work_item',$5,$6::text,jsonb_build_object('reasons',jsonb_build_array($6::text)))", uuid.New(), f.WorkspaceID, f.ProjectID, f.MemberID, item, reason)
	}
	response := testutil.Request(t, r, f.MemberID, "GET", w+"/notifications?reason=mentions,assigned&limit=1", nil, 200)
	if len(testutil.Array(t, response)) != 1 || response["pagination"].(map[string]any)["total"] != float64(2) || response["pagination"].(map[string]any)["has_more"] != true {
		t.Fatalf("notification pagination lost matching results: %#v", response)
	}
	marked := testutil.Object(t, testutil.Request(t, r, f.MemberID, "POST", w+"/notifications/mark-all-read?reason=mentions", nil, 200))
	if marked["updated"] != float64(1) {
		t.Fatal("filtered mark-read changed unrelated reasons")
	}
	if list := testutil.Array(t, testutil.Request(t, r, f.MemberID, "GET", w+"/notifications?unread=true", nil, 200)); len(list) != 2 {
		t.Fatal("filtered mark-read changed unrelated notifications")
	}
	testutil.Request(t, r, f.MemberID, "GET", w+"/notifications?reason=bogus", nil, 400)
	testutil.Request(t, r, f.MemberID, "GET", w+"/notifications?read=true&unread=true", nil, 400)
}
