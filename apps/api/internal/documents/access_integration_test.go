package documents_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"my-jira/apps/api/internal/documents"
	"my-jira/apps/api/internal/support"
	"my-jira/apps/api/internal/support/testutil"
)

func TestPublicProjectPagesStillRequireActiveProjectMembership(t *testing.T) {
	f := testutil.New(t)
	r := f.Router(documents.Register, support.Register)
	page := testutil.Object(t, testutil.Request(t, r, f.OwnerID, "POST", f.Prefix()+"/pages", map[string]any{"name": "Membership protected specification"}, 201))
	id := page["id"].(string)
	base := f.Prefix() + "/pages/" + id
	versions := testutil.Array(t, testutil.Request(t, r, f.OwnerID, "GET", base+"/versions", nil, 200))
	versionID := versions[0].(map[string]any)["id"].(string)
	testutil.Request(t, r, f.MemberID, "POST", f.WorkspacePrefix()+"/favorites", map[string]any{"entity_type": "page", "entity_id": id}, 201)
	notification := uuid.New()
	f.Exec(t, "INSERT INTO notifications(id,workspace_id,project_id,user_id,entity_type,entity_id,title) VALUES($1,$2,$3,$4,'page',$5,'Protected specification')", notification, f.WorkspaceID, f.ProjectID, f.MemberID, id)
	f.Exec(t, "UPDATE projects SET network='public' WHERE id=$1", f.ProjectID)
	f.Exec(t, "UPDATE project_members SET is_active=false WHERE project_id=$1 AND user_id=$2", f.ProjectID, f.MemberID)
	for _, suffix := range []string{"", "/content", "/versions", "/versions/" + versionID, "/resources", "/comments"} {
		testutil.Request(t, r, f.MemberID, "GET", base+suffix, nil, 404)
	}
	testutil.Request(t, r, f.MemberID, "GET", f.Prefix()+"/pages", nil, 404)
	testutil.Request(t, r, f.MemberID, "GET", f.Prefix()+"/pages/summary", nil, 404)
	testutil.Request(t, r, f.MemberID, "POST", f.Prefix()+"/pages", map[string]any{"name": "Unauthorized creation"}, 404)
	testutil.Request(t, r, f.MemberID, "PATCH", base, map[string]any{"name": "Unauthorized rename", "version": 1}, 404)
	search := testutil.Object(t, testutil.Request(t, r, f.MemberID, "GET", f.WorkspacePrefix()+"/search?q=specification", nil, 200))
	if len(search["pages"].([]any)) != 0 {
		t.Fatal("search bypassed explicit page project membership")
	}
	for _, resource := range []string{"favorites", "notifications"} {
		if list := testutil.Array(t, testutil.Request(t, r, f.MemberID, "GET", f.WorkspacePrefix()+"/"+resource, nil, 200)); len(list) != 0 {
			t.Fatalf("%s bypassed explicit page membership", resource)
		}
	}
	testutil.Request(t, r, f.MemberID, "PATCH", f.WorkspacePrefix()+"/notifications/"+notification.String(), map[string]any{"read": true}, 404)
}

func TestDocumentWriteWaitsForRoleChangeAndUsesCommittedRole(t *testing.T) {
	f := testutil.New(t)
	r := f.Router(documents.Register)
	page := testutil.Object(t, testutil.Request(t, r, f.OwnerID, "POST", f.Prefix()+"/pages", map[string]any{"name": "Original document"}, 201))
	base := f.Prefix() + "/pages/" + page["id"].(string)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx, err := f.DB.SQL.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var locker int
	if err = tx.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&locker); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, "UPDATE project_members SET role=5 WHERE project_id=$1 AND user_id=$2", f.ProjectID, f.MemberID); err != nil {
		t.Fatal(err)
	}
	finished := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest("PATCH", base, strings.NewReader(`{"name":"Forbidden after demotion","version":1}`)).WithContext(ctx)
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
		t.Fatal("document authorization did not wait for the role update")
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case response := <-finished:
		if response.Code != 404 {
			t.Fatalf("demoted request status %d: %s", response.Code, response.Body.String())
		}
	case <-ctx.Done():
		t.Fatal("document request did not resume after role commit")
	}
	current := testutil.Object(t, testutil.Request(t, r, f.OwnerID, "GET", base, nil, 200))
	if current["name"] != "Original document" || current["version"] != float64(1) {
		t.Fatal("demoted user changed document content")
	}
}

func TestPagePrivacyRequiresOwnerAndRetainsActiveOwnerRights(t *testing.T) {
	f := testutil.New(t)
	r := f.Router(documents.Register)
	page := testutil.Object(t, testutil.Request(t, r, f.MemberID, "POST", f.Prefix()+"/pages", map[string]any{"name": "Member owned page"}, 201))
	base := f.Prefix() + "/pages/" + page["id"].(string)
	testutil.Request(t, r, f.OwnerID, "PATCH", base, map[string]any{"is_private": true, "version": 1}, 403)
	current := testutil.Object(t, testutil.Request(t, r, f.MemberID, "GET", base, nil, 200))
	if current["is_private"] != false || current["version"] != float64(1) {
		t.Fatal("non-owner admin changed page privacy or version")
	}
	f.Exec(t, "UPDATE project_members SET role=5 WHERE project_id=$1 AND user_id=$2", f.ProjectID, f.MemberID)
	f.Exec(t, "UPDATE workspace_members SET role=5 WHERE workspace_id=$1 AND user_id=$2", f.WorkspaceID, f.MemberID)
	content := testutil.Object(t, testutil.Request(t, r, f.MemberID, "GET", base+"/content", nil, 200))
	if content["can_edit"] != true {
		t.Fatal("active page owner lost their content edit rights after role change")
	}
	testutil.Request(t, r, f.MemberID, "PATCH", base, map[string]any{"name": "Still owned", "is_private": true, "version": 1}, 200)
	testutil.Request(t, r, f.OwnerID, "GET", base, nil, 404)
	f.Exec(t, "UPDATE project_members SET is_active=false WHERE project_id=$1 AND user_id=$2", f.ProjectID, f.MemberID)
	testutil.Request(t, r, f.MemberID, "PATCH", base, map[string]any{"is_private": false, "version": 2}, 404)
	testutil.Request(t, r, f.MemberID, "GET", base+"/content", nil, 404)
}

func TestGuestReadSettingScopesEveryPageReadSurface(t *testing.T) {
	f := testutil.New(t)
	r := f.Router(documents.Register, support.Register)
	public := testutil.Object(t, testutil.Request(t, r, f.OwnerID, "POST", f.Prefix()+"/pages", map[string]any{"name": "Guest setting public page"}, 201))
	private := testutil.Object(t, testutil.Request(t, r, f.OwnerID, "POST", f.Prefix()+"/pages", map[string]any{"name": "Guest setting private page", "is_private": true}, 201))
	owned := testutil.Object(t, testutil.Request(t, r, f.OwnerID, "POST", f.Prefix()+"/pages", map[string]any{"name": "Guest setting owned page", "is_private": true}, 201))
	f.Exec(t, "UPDATE pages SET owner_id=$2 WHERE id=$1", owned["id"], f.GuestID)
	base := f.Prefix() + "/pages/" + public["id"].(string)
	versions := testutil.Array(t, testutil.Request(t, r, f.OwnerID, "GET", base+"/versions", nil, 200))
	versionID := versions[0].(map[string]any)["id"].(string)
	for _, enabled := range []bool{false, true, false} {
		f.Exec(t, "UPDATE projects SET guest_can_view_all=$2 WHERE id=$1", f.ProjectID, enabled)
		status, count := 404, 1
		if enabled {
			status, count = 200, 2
		}
		for _, suffix := range []string{"", "/content", "/versions", "/versions/" + versionID, "/resources", "/comments"} {
			testutil.Request(t, r, f.GuestID, "GET", base+suffix, nil, status)
		}
		if pages := testutil.Array(t, testutil.Request(t, r, f.GuestID, "GET", f.Prefix()+"/pages", nil, 200)); len(pages) != count {
			t.Fatalf("guest setting %t did not scope the page collection: %#v", enabled, pages)
		}
		if summary := testutil.Object(t, testutil.Request(t, r, f.GuestID, "GET", f.Prefix()+"/pages/summary", nil, 200)); summary["total"] != float64(count) || summary["private_pages"] != float64(1) {
			t.Fatalf("guest setting %t did not scope page summary: %#v", enabled, summary)
		}
		search := testutil.Object(t, testutil.Request(t, r, f.GuestID, "GET", f.WorkspacePrefix()+"/search?q=Guest%20setting", nil, 200))
		if len(search["pages"].([]any)) != count {
			t.Fatalf("guest setting %t did not scope page search: %#v", enabled, search)
		}
		testutil.Request(t, r, f.GuestID, "GET", f.Prefix()+"/pages/"+private["id"].(string), nil, 404)
		testutil.Request(t, r, f.GuestID, "GET", f.Prefix()+"/pages/"+owned["id"].(string), nil, 200)
		if enabled {
			content := testutil.Object(t, testutil.Request(t, r, f.GuestID, "GET", base+"/content", nil, 200))
			if content["can_edit"] != false {
				t.Fatal("read setting elevated a guest to content editor")
			}
			testutil.Request(t, r, f.GuestID, "PATCH", base, map[string]any{"name": "Unauthorized", "version": 1}, 403)
			testutil.Request(t, r, f.GuestID, "POST", f.Prefix()+"/pages", map[string]any{"name": "Unauthorized new page"}, 403)
			testutil.Request(t, r, f.GuestID, "POST", f.WorkspacePrefix()+"/favorites", map[string]any{"entity_type": "page", "entity_id": public["id"]}, 201)
		}
		favorites := testutil.Array(t, testutil.Request(t, r, f.GuestID, "GET", f.WorkspacePrefix()+"/favorites", nil, 200))
		if len(favorites) != count-1 {
			t.Fatalf("guest setting %t did not recheck page favorites", enabled)
		}
	}
	f.Exec(t, "UPDATE projects SET guest_can_view_all=true,network='public' WHERE id=$1", f.ProjectID)
	f.Exec(t, "UPDATE project_members SET is_active=false WHERE project_id=$1 AND user_id=$2", f.ProjectID, f.GuestID)
	testutil.Request(t, r, f.GuestID, "GET", base, nil, 404)
	testutil.Request(t, r, f.GuestID, "GET", f.Prefix()+"/pages/"+owned["id"].(string), nil, 404)
}
