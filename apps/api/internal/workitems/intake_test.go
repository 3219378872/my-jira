package workitems

import (
	"context"
	"testing"
	"time"

	"my-jira/apps/api/internal/support/testutil"
)

func TestIntakeGuestOwnershipReviewAndSnoozeLifecycle(t *testing.T) {
	f := testutil.New(t)
	router := f.Router(Register)
	created := testutil.Object(t, testutil.Request(t, router, f.GuestID, "POST", f.Prefix()+"/intake", map[string]any{"name": "Guest suggestion", "description_html": "<p>Original</p>", "description_json": map[string]any{"type": "doc", "content": []any{}}}, 201))
	id := created["id"].(string)
	item := created["work_item"].(map[string]any)
	itemID := item["id"].(string)
	if item["is_draft"] != true || created["status"] != "pending" {
		t.Fatal("intake did not start hidden and pending")
	}
	if rows := testutil.Array(t, testutil.Request(t, router, f.OwnerID, "GET", f.Prefix()+"/issues", nil, 200)); len(rows) != 0 {
		t.Fatal("pending intake leaked into regular collection")
	}
	testutil.Request(t, router, f.MemberID, "PATCH", f.Prefix()+"/intake/"+id, map[string]any{"version": 1, "name": "Other edit"}, 403)
	testutil.Request(t, router, f.GuestID, "PATCH", f.Prefix()+"/intake/"+id, map[string]any{"version": 1, "priority": "high"}, 403)
	testutil.Request(t, router, f.GuestID, "PATCH", f.Prefix()+"/intake/"+id, map[string]any{"version": 1, "name": "Revised suggestion"}, 200)
	testutil.Request(t, router, f.GuestID, "PATCH", f.Prefix()+"/intake/"+id, map[string]any{"version": 1, "name": "Stale edit"}, 409)
	testutil.Request(t, router, f.MemberID, "POST", f.Prefix()+"/intake/"+id+"/resolve", map[string]any{"status": "accepted"}, 403)
	testutil.Request(t, router, f.OwnerID, "PATCH", f.Prefix()+"/issues/"+itemID, map[string]any{"version": 2, "is_draft": false}, 400)
	testutil.Request(t, router, f.OwnerID, "POST", f.Prefix()+"/intake/"+id+"/resolve", map[string]any{"status": "snoozed", "snoozed_until": time.Now().Add(-time.Hour)}, 400)
	snooze := time.Now().Add(24 * time.Hour)
	testutil.Request(t, router, f.OwnerID, "POST", f.Prefix()+"/intake/"+id+"/resolve", map[string]any{"status": "snoozed", "snoozed_until": snooze, "version": 2}, 200)
	if _, e := RunMaintenance(context.Background(), f.Deps, snooze.Add(time.Second), f.ProjectID); e != nil {
		t.Fatal(e)
	}
	detail := testutil.Object(t, testutil.Request(t, router, f.GuestID, "GET", f.Prefix()+"/intake/"+id, nil, 200))
	if detail["status"] != "pending" || detail["snoozed_until"] != nil {
		t.Fatal("expired snooze did not return to pending")
	}
	accepted := testutil.Object(t, testutil.Request(t, router, f.OwnerID, "POST", f.Prefix()+"/intake/"+id+"/resolve", map[string]any{"status": "accepted"}, 200))
	if accepted["work_item"].(map[string]any)["is_draft"] != false {
		t.Fatal("accepted submission stayed hidden")
	}
	versions := testutil.Array(t, testutil.Request(t, router, f.GuestID, "GET", f.Prefix()+"/intake/"+id+"/versions", nil, 200))
	if len(versions) < 3 {
		t.Fatal("intake description/review versions were not recorded")
	}
	testutil.Request(t, router, f.OwnerID, "POST", f.Prefix()+"/intake/"+id+"/resolve", map[string]any{"status": "duplicate", "duplicate_of": itemID}, 400)
	testutil.Request(t, router, f.MemberID, "DELETE", f.Prefix()+"/intake/"+id, nil, 403)
	testutil.Request(t, router, f.GuestID, "DELETE", f.Prefix()+"/intake/"+id, nil, 204)
	testutil.Request(t, router, f.OwnerID, "GET", f.Prefix()+"/intake/"+id, nil, 404)
}
