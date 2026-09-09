package workitems_test

import (
	"testing"

	"github.com/google/uuid"
	"my-jira/apps/api/internal/support/testutil"
	"my-jira/apps/api/internal/workitems"
)

func TestManagedSubscriptionsEnforceRecipientAccess(t *testing.T) {
	f := testutil.New(t)
	r := f.Router(workitems.Register)
	ownerItem := f.Issue(t, f.ProjectID, f.StateID, f.OwnerID, "Owner item", 1)
	guestItem := f.Issue(t, f.ProjectID, f.StateID, f.GuestID, "Guest item", 2)
	base := f.Prefix() + "/issues/" + ownerItem.String() + "/subscribers"
	guestBase := f.Prefix() + "/issues/" + guestItem.String() + "/subscribers"
	call := func(actor uuid.UUID, method, path string, body any, status int) map[string]any {
		return testutil.Request(t, r, actor, method, path, body, status)
	}
	call(f.OwnerID, "POST", base, map[string]any{"user_id": f.MemberID}, 200)
	call(f.OwnerID, "POST", base, map[string]any{"user_id": f.MemberID}, 200)
	if rows := testutil.Array(t, call(f.MemberID, "GET", base, nil, 200)); len(rows) != 1 || rows[0].(map[string]any)["user_id"] != f.MemberID.String() {
		t.Fatal("managed subscription was missing or duplicated")
	}
	call(f.OwnerID, "POST", base, map[string]any{"user_id": f.OutsiderID}, 400)
	call(f.OwnerID, "POST", base, map[string]any{"user_id": f.GuestID}, 400)
	call(f.OwnerID, "POST", base, map[string]any{"user_id": uuid.Nil}, 400)
	call(f.GuestID, "POST", guestBase, map[string]any{"user_id": f.MemberID}, 403)
	call(f.MemberID, "POST", guestBase, map[string]any{"user_id": f.GuestID}, 200)
	call(f.GuestID, "DELETE", guestBase+"/"+f.MemberID.String(), nil, 403)
	call(f.GuestID, "DELETE", guestBase, nil, 204)
	if rows := testutil.Array(t, call(f.OwnerID, "GET", guestBase, nil, 200)); len(rows) != 0 {
		t.Fatal("self unsubscribe did not remove the managed subscription")
	}
	call(f.GuestID, "POST", guestBase, nil, 200)
	call(f.MemberID, "DELETE", guestBase+"/"+f.GuestID.String(), nil, 204)
	call(f.GuestID, "POST", guestBase, nil, 200)
	if rows := testutil.Array(t, call(f.OwnerID, "GET", guestBase, nil, 200)); len(rows) != 1 {
		t.Fatal("resubscribe after soft deletion failed")
	}
	f.Exec(t, "UPDATE project_members SET role=5 WHERE project_id=$1 AND user_id=$2", f.ProjectID, f.MemberID)
	if rows := testutil.Array(t, call(f.OwnerID, "GET", base, nil, 200)); len(rows) != 0 {
		t.Fatal("subscriber list retained a guest who cannot read this item")
	}
	call(f.OwnerID, "DELETE", base+"/"+f.MemberID.String(), nil, 204)
	f.Exec(t, "UPDATE project_members SET is_active=false WHERE project_id=$1 AND user_id=$2", f.ProjectID, f.MemberID)
	call(f.OwnerID, "POST", base, map[string]any{"user_id": f.MemberID}, 400)
}

func TestIdentifierLookupUsesListVisibilityAndLifecycle(t *testing.T) {
	f := testutil.New(t)
	r := f.Router(workitems.Register)
	owner := f.Issue(t, f.ProjectID, f.StateID, f.OwnerID, "Main visible", 1)
	guest := f.Issue(t, f.ProjectID, f.StateID, f.GuestID, "Guest visible", 2)
	f.Issue(t, f.OtherProjectID, f.OtherStateID, f.OwnerID, "Hidden item", 1)
	draft := f.Issue(t, f.ProjectID, f.StateID, f.OwnerID, "Draft item", 3)
	intake := f.Issue(t, f.ProjectID, f.StateID, f.OwnerID, "Pending intake", 4)
	archived := f.Issue(t, f.ProjectID, f.StateID, f.OwnerID, "Archived item", 5)
	deleted := f.Issue(t, f.ProjectID, f.StateID, f.OwnerID, "Deleted item", 6)
	f.Exec(t, "UPDATE work_items SET is_draft=true WHERE id=$1", draft)
	f.Exec(t, "UPDATE work_items SET archived_at=now() WHERE id=$1", archived)
	f.Exec(t, "UPDATE work_items SET deleted_at=now() WHERE id=$1", deleted)
	f.Exec(t, "INSERT INTO intake_items(id,workspace_id,project_id,work_item_id,submitted_by,status) VALUES($1,$2,$3,$4,$5,'pending')", uuid.New(), f.WorkspaceID, f.ProjectID, intake, f.OwnerID)
	base := f.WorkspacePrefix() + "/issues/lookup/"
	call := func(actor uuid.UUID, identifier string, status int) map[string]any {
		return testutil.Request(t, r, actor, "GET", base+identifier, nil, status)
	}
	detail := testutil.Object(t, call(f.MemberID, "main-1", 200))
	if detail["id"] != owner.String() || detail["project_detail"].(map[string]any)["identifier"] != "MAIN" || detail["assignee_ids"] == nil {
		t.Fatalf("lookup did not return the complete scoped DTO: %#v", detail)
	}
	call(f.MemberID, "HIDDEN-1", 404)
	call(f.GuestID, "MAIN-1", 404)
	if item := testutil.Object(t, call(f.GuestID, "MAIN-2", 200)); item["id"] != guest.String() {
		t.Fatal("guest could not resolve own work item")
	}
	for _, identifier := range []string{"MAIN-3", "MAIN-4", "MAIN-5", "MAIN-6"} {
		call(f.OwnerID, identifier, 404)
	}
	call(f.OwnerID, "MAIN-3?draft=true", 200)
	call(f.OwnerID, "MAIN-4?intake_status=pending", 200)
	call(f.OwnerID, "MAIN-5?archived=true", 200)
	call(f.OwnerID, "MAIN-6?deleted=true", 404)
	for _, identifier := range []string{"MAIN-0", "MAIN--1", "MAIN-9223372036854775808", "invalid"} {
		call(f.OwnerID, identifier, 400)
	}
	f.Exec(t, "UPDATE projects SET archived_at=now() WHERE id=$1", f.ProjectID)
	call(f.OwnerID, "MAIN-1", 404)
	call(f.OwnerID, "MAIN-1?project_archived=true", 200)
	f.Exec(t, "UPDATE projects SET archived_at=NULL,network='public' WHERE id=$1", f.ProjectID)
	f.Exec(t, "UPDATE project_members SET is_active=false WHERE project_id=$1 AND user_id=$2", f.ProjectID, f.MemberID)
	call(f.MemberID, "MAIN-1", 200)
	f.Exec(t, "UPDATE projects SET network='private' WHERE id=$1", f.ProjectID)
	call(f.MemberID, "MAIN-1", 404)
}

func TestGuestReadSettingControlsSubscriptionsAndIdentifierLookup(t *testing.T) {
	f := testutil.New(t)
	r := f.Router(workitems.Register)
	item := f.Issue(t, f.ProjectID, f.StateID, f.OwnerID, "Shared with guests", 1)
	base := f.Prefix() + "/issues/" + item.String() + "/subscribers"
	lookup := f.WorkspacePrefix() + "/issues/lookup/MAIN-1"
	testutil.Request(t, r, f.GuestID, "GET", lookup, nil, 404)
	testutil.Request(t, r, f.GuestID, "POST", base, nil, 404)
	f.Exec(t, "UPDATE projects SET guest_can_view_all=true WHERE id=$1", f.ProjectID)
	testutil.Request(t, r, f.GuestID, "GET", lookup, nil, 200)
	testutil.Request(t, r, f.GuestID, "POST", base, nil, 200)
	testutil.Request(t, r, f.GuestID, "POST", base, map[string]any{"user_id": f.MemberID}, 403)
	testutil.Request(t, r, f.MemberID, "POST", base, map[string]any{"user_id": f.GuestID}, 200)
	if rows := testutil.Array(t, testutil.Request(t, r, f.OwnerID, "GET", base, nil, 200)); len(rows) != 1 || rows[0].(map[string]any)["user_id"] != f.GuestID.String() {
		t.Fatal("readable guest subscriber was not listed exactly once")
	}
	testutil.Request(t, r, f.GuestID, "DELETE", base, nil, 204)
	testutil.Request(t, r, f.MemberID, "POST", base, map[string]any{"user_id": f.GuestID}, 200)
	f.Exec(t, "UPDATE projects SET guest_can_view_all=false WHERE id=$1", f.ProjectID)
	testutil.Request(t, r, f.GuestID, "GET", lookup, nil, 404)
	testutil.Request(t, r, f.GuestID, "GET", base, nil, 404)
	testutil.Request(t, r, f.MemberID, "POST", base, map[string]any{"user_id": f.GuestID}, 400)
	if rows := testutil.Array(t, testutil.Request(t, r, f.OwnerID, "GET", base, nil, 200)); len(rows) != 0 {
		t.Fatal("subscriber list retained a guest after read access was disabled")
	}
	f.Exec(t, "UPDATE projects SET guest_can_view_all=true WHERE id=$1", f.ProjectID)
	f.Exec(t, "UPDATE project_members SET is_active=false WHERE project_id=$1 AND user_id=$2", f.ProjectID, f.GuestID)
	testutil.Request(t, r, f.GuestID, "GET", lookup, nil, 404)
	testutil.Request(t, r, f.MemberID, "POST", base, map[string]any{"user_id": f.GuestID}, 400)
}
