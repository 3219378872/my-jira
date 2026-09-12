package scenarios_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"my-jira/apps/api/internal/automation"
	"my-jira/apps/api/internal/documents"
	"my-jira/apps/api/internal/platform/httpapi"
	"my-jira/apps/api/internal/platform/identity"
	"my-jira/apps/api/internal/scenarios"
	"my-jira/apps/api/internal/support/testutil"
	"my-jira/apps/api/internal/workitems"
)

func story(t *testing.T, f *testutil.Fixture, name string, sequence int) uuid.UUID {
	t.Helper()
	id := f.Issue(t, f.ProjectID, f.StateID, f.OwnerID, name, sequence)
	f.Exec(t, `UPDATE work_items SET requirement_type='story' WHERE id=$1`, id)
	return id
}

func content(id uuid.UUID, sources ...string) map[string]any {
	a, b, call := uuid.NewString(), uuid.NewString(), uuid.NewString()
	return map[string]any{
		"story_id": id, "name": "Customer checks availability", "goal": "A customer can reserve available stock", "trigger": "The customer requests stock", "preconditions": "The catalog is available", "outcome": "The booking is confirmed", "system_boundary": "Inventory", "bind_story_name": true,
		"source_page_ids": append([]string{}, sources...),
		"participants":    []any{map[string]any{"id": a, "name": "Customer", "kind": "actor"}, map[string]any{"id": b, "name": "Inventory system", "kind": "system"}},
		"steps":           []any{map[string]any{"id": call, "kind": "call", "from_id": a, "to_id": b, "message": "Check availability"}, map[string]any{"id": uuid.NewString(), "kind": "return", "from_id": b, "to_id": a, "message": "Available", "return_of": call}},
		"relationships":   []any{},
	}
}

func rawRequest(r *gin.Engine, actor uuid.UUID, method, path string, input any) *httptest.ResponseRecorder {
	var body []byte
	if input != nil {
		body, _ = json.Marshal(input)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-Actor", actor.String())
	response := httptest.NewRecorder()
	r.ServeHTTP(response, req)
	return response
}

func svgRequest(t *testing.T, r *gin.Engine, actor uuid.UUID, path string, status int) string {
	t.Helper()
	response := rawRequest(r, actor, "GET", path, nil)
	if response.Code != status {
		t.Fatalf("GET %s: status=%d want=%d body=%s", path, response.Code, status, response.Body.String())
	}
	if status == 200 {
		if response.Header().Get("Content-Type") != "image/svg+xml; charset=utf-8" || response.Header().Get("Cache-Control") != "private, no-store" || !strings.Contains(response.Header().Get("Content-Security-Policy"), "script-src 'none'") {
			t.Fatalf("SVG export lacks appropriate type, no-store or script prohibition: %#v", response.Header())
		}
	}
	return response.Body.String()
}

func TestScenarioCRUDImmutableVersionsDynamicBindingAndAtomicConflicts(t *testing.T) {
	f := testutil.New(t)
	r := f.Router(scenarios.Register)
	storyID := story(t, f, "Reserve a book", 1)
	base := f.Prefix() + "/scenarios"
	created := testutil.Object(t, testutil.Request(t, r, f.MemberID, "POST", base, content(storyID), 201))
	id := created["id"].(string)
	path := base + "/" + id
	testutil.Request(t, r, f.GuestID, "POST", base, content(storyID), 403)
	testutil.Request(t, r, f.OutsiderID, "GET", path, nil, 404)
	testutil.Request(t, r, f.MemberID, "GET", strings.Replace(path, f.ProjectID.String(), f.OtherProjectID.String(), 1), nil, 404)
	if created["version"] != float64(1) || created["review_needed"] != false {
		t.Fatalf("Unexpected initial revision: %#v", created)
	}
	if created["source_story_version"] != float64(1) {
		t.Fatal("Scenario did not pin its source Story revision")
	}
	first := svgRequest(t, r, f.MemberID, path+"/sequence.svg?download=1", 200)
	if !strings.Contains(first, "Reserve a book") || !strings.Contains(first, "data-step-id") {
		t.Fatal("Initial diagram did not bind Story label or steps")
	}
	f.Exec(t, `UPDATE work_items SET name='Reserve a newly named book',version=version+1 WHERE id=$1`, storyID)
	renamed := testutil.Object(t, testutil.Request(t, r, f.MemberID, "GET", path, nil, 200))
	if renamed["version"] != float64(1) || renamed["review_needed"] != false || renamed["story"].(map[string]any)["name"] != "Reserve a newly named book" {
		t.Fatal("A dynamically bound label should refresh without changing authored steps or review status")
	}
	f.Exec(t, `UPDATE work_items SET acceptance_criteria='["Customer sees availability"]',version=version+1 WHERE id=$1`, storyID)
	review := testutil.Object(t, testutil.Request(t, r, f.MemberID, "GET", path, nil, 200))
	if review["version"] != float64(2) || review["review_needed"] != true {
		t.Fatal("Story acceptance change did not create a review-needed scenario revision")
	}
	if review["source_story_version"] != created["source_story_version"] {
		t.Fatal("Unreviewed Story changes replaced the original source revision")
	}
	testutil.Request(t, r, f.MemberID, "PATCH", path, map[string]any{"version": 1, "name": "Stale editor overwrite"}, 409)
	updated := testutil.Object(t, testutil.Request(t, r, f.MemberID, "PATCH", path, map[string]any{"version": 2, "name": "Reviewed booking", "review_needed": false}, 200))
	if updated["version"] != float64(3) || updated["review_needed"] != false {
		t.Fatal("Explicit review acknowledgement was not persisted")
	}
	if updated["source_story_version"] != float64(3) {
		t.Fatal("Explicit review did not capture the new source Story revision")
	}
	historical := testutil.Object(t, testutil.Request(t, r, f.MemberID, "GET", path+"/versions/1", nil, 200))
	if historical["name"] != "Customer checks availability" || historical["version"] != float64(1) || historical["story"].(map[string]any)["name"] != "Reserve a newly named book" {
		t.Fatal("History lost immutable authored content or current bound Story context")
	}
	beforeEvents := 0
	if err := f.DB.SQL.QueryRow(`SELECT count(*) FROM project_events WHERE project_id=$1`, f.ProjectID).Scan(&beforeEvents); err != nil {
		t.Fatal(err)
	}
	testutil.Request(t, r, f.MemberID, "PATCH", path, map[string]any{"version": 3, "source_page_ids": []string{uuid.NewString()}}, 404)
	afterEvents := 0
	if err := f.DB.SQL.QueryRow(`SELECT count(*) FROM project_events WHERE project_id=$1`, f.ProjectID).Scan(&afterEvents); err != nil {
		t.Fatal(err)
	}
	if beforeEvents != afterEvents {
		t.Fatal("Rejected source update leaked a committed planning event")
	}
	var wait sync.WaitGroup
	responses := make(chan *httptest.ResponseRecorder, 2)
	for _, name := range []string{"Concurrent one", "Concurrent two"} {
		wait.Add(1)
		go func(name string) {
			defer wait.Done()
			responses <- rawRequest(r, f.MemberID, "PATCH", path, map[string]any{"version": 3, "name": name})
		}(name)
	}
	wait.Wait()
	close(responses)
	statuses := map[int]int{}
	for response := range responses {
		statuses[response.Code]++
		if response.Code != 200 && response.Code != 409 {
			t.Fatalf("Unexpected concurrent write response %d: %s", response.Code, response.Body.String())
		}
	}
	if statuses[200] != 1 || statuses[409] != 1 {
		t.Fatalf("Concurrent writes did not protect the shared version: %#v", statuses)
	}
	versions := testutil.Array(t, testutil.Request(t, r, f.MemberID, "GET", path+"/versions", nil, 200))
	if len(versions) != 4 {
		t.Fatalf("Immutable history does not match committed revisions: %#v", versions)
	}
	copy := testutil.Object(t, testutil.Request(t, r, f.MemberID, "POST", path+"/copy", map[string]any{"version": 4, "name": "Alternative scenario"}, 201))
	if copy["id"] == created["id"] || copy["steps"].([]any)[0].(map[string]any)["id"] == created["steps"].([]any)[0].(map[string]any)["id"] {
		t.Fatal("A copy reused source object identities")
	}
	testutil.Request(t, r, f.MemberID, "DELETE", path+"?version=3", nil, 409)
	testutil.Request(t, r, f.MemberID, "DELETE", path+"?version=4", nil, 204)
	for _, suffix := range []string{"", "/versions", "/versions/1"} {
		testutil.Request(t, r, f.MemberID, "GET", path+suffix, nil, 404)
	}
	svgRequest(t, r, f.MemberID, path+"/sequence.svg?version=1", 404)
	if list := testutil.Array(t, testutil.Request(t, r, f.MemberID, "GET", base, nil, 200)); len(list) != 1 || list[0].(map[string]any)["id"] != copy["id"] {
		t.Fatal("Deleted scenario remained in the visible collection")
	}
}

func TestPrivateSourcesNeverWidenThroughHistoryReferenceRemovalCopyOrExport(t *testing.T) {
	f := testutil.New(t)
	r := f.Router(scenarios.Register, documents.Register)
	storyID := story(t, f, "Shared Story", 1)
	page := testutil.Object(t, testutil.Request(t, r, f.MemberID, "POST", f.Prefix()+"/pages", map[string]any{"name": "Private source", "is_private": true}, 201))
	base := f.Prefix() + "/scenarios"
	input := content(storyID, page["id"].(string))
	input["name"], input["goal"], input["bind_story_name"] = "PRIVATE-SCENARIO-CONTENT", "Secret derived requirement", false
	created := testutil.Object(t, testutil.Request(t, r, f.MemberID, "POST", base, input, 201))
	path := base + "/" + created["id"].(string)
	assertHidden := func(actor uuid.UUID, path string) {
		t.Helper()
		for _, suffix := range []string{"", "/versions", "/versions/1"} {
			testutil.Request(t, r, actor, "GET", path+suffix, nil, 404)
		}
		for _, suffix := range []string{"/sequence.svg", "/sequence.svg?version=1", "/use-case.svg", "/use-case.svg?version=1&download=1"} {
			svgRequest(t, r, actor, path+suffix, 404)
		}
	}
	assertHidden(f.OwnerID, path)
	testutil.Request(t, r, f.OwnerID, "POST", path+"/copy", map[string]any{"version": 1}, 404)
	if list := testutil.Array(t, testutil.Request(t, r, f.OwnerID, "GET", base, nil, 200)); len(list) != 0 {
		t.Fatal("An administrator bypassed private source intersection in a collection")
	}
	if aggregate := svgRequest(t, r, f.OwnerID, base+"/use-case.svg", 200); strings.Contains(aggregate, "PRIVATE-SCENARIO-CONTENT") || strings.Contains(aggregate, created["id"].(string)) {
		t.Fatal("Project diagram leaked a private scenario")
	}
	updated := testutil.Object(t, testutil.Request(t, r, f.MemberID, "PATCH", path, map[string]any{"version": 1, "source_page_ids": []string{}}, 200))
	if len(updated["source_page_ids"].([]any)) != 0 || len(updated["provenance_source_page_ids"].([]any)) != 1 {
		t.Fatal("Removing a source link did not retain derivation provenance")
	}
	assertHidden(f.OwnerID, path)
	copy := testutil.Object(t, testutil.Request(t, r, f.MemberID, "POST", path+"/copy", map[string]any{"version": 2}, 201))
	assertHidden(f.OwnerID, base+"/"+copy["id"].(string))
	// The current source owner can deliberately publish the source. Future reads
	// use that current authorization, including historical SVG and copied content.
	testutil.Request(t, r, f.MemberID, "PATCH", f.Prefix()+"/pages/"+page["id"].(string), map[string]any{"version": 1, "is_private": false}, 200)
	testutil.Request(t, r, f.OwnerID, "GET", path+"/versions/1", nil, 200)
	svgRequest(t, r, f.OwnerID, path+"/sequence.svg?version=1", 200)
	var visibilityChanges int
	if err := f.DB.SQL.QueryRow(`SELECT count(*) FROM project_events WHERE project_id=$1 AND kind='scenario.visibility_changed'`, f.ProjectID).Scan(&visibilityChanges); err != nil {
		t.Fatal(err)
	}
	if visibilityChanges != 1 {
		t.Fatal("Publishing a source did not invalidate scenario visibility independently of content")
	}
	testutil.Request(t, r, f.MemberID, "PATCH", f.Prefix()+"/pages/"+page["id"].(string), map[string]any{"version": 2, "content_html": "<p>Updated private-origin requirement</p>", "content_json": map[string]any{"type": "doc", "content": []any{map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": "Updated private-origin requirement"}}}}}}, 200)
	review := testutil.Object(t, testutil.Request(t, r, f.MemberID, "GET", path, nil, 200))
	if review["review_needed"] != true || review["source_page_versions"].(map[string]any)[page["id"].(string)] != float64(1) {
		t.Fatal("Source content update was not marked for review with original page revision retained")
	}
	f.Exec(t, `UPDATE project_members SET is_active=false WHERE project_id=$1 AND user_id=$2`, f.ProjectID, f.MemberID)
	assertHidden(f.MemberID, path)
	testutil.Request(t, r, f.MemberID, "GET", base, nil, 404)
}

func TestStoryProjectMoveTransfersScenariosWithCurrentSourceAuthorization(t *testing.T) {
	f := testutil.New(t)
	r := f.Router(scenarios.Register, documents.Register)
	storyID := story(t, f, "Movable Story", 1)
	page := testutil.Object(t, testutil.Request(t, r, f.OwnerID, "POST", f.Prefix()+"/pages", map[string]any{"name": "Original project source"}, 201))
	created := testutil.Object(t, testutil.Request(t, r, f.OwnerID, "POST", f.Prefix()+"/scenarios", content(storyID, page["id"].(string)), 201))
	oldPath := f.Prefix() + "/scenarios/" + created["id"].(string)
	f.Exec(t, `UPDATE work_items SET project_id=$2,state_id=$3,version=version+1 WHERE id=$1`, storyID, f.OtherProjectID, f.OtherStateID)
	newPath := strings.Replace(oldPath, f.ProjectID.String(), f.OtherProjectID.String(), 1)
	testutil.Request(t, r, f.OwnerID, "GET", oldPath, nil, 404)
	moved := testutil.Object(t, testutil.Request(t, r, f.OwnerID, "GET", newPath, nil, 200))
	if moved["project_id"] != f.OtherProjectID.String() || moved["version"] != float64(2) || len(moved["provenance_source_page_ids"].([]any)) != 1 {
		t.Fatal("Scenario move lost project, history or source provenance")
	}
	testutil.Request(t, r, f.OwnerID, "GET", newPath+"/versions/1", nil, 200)
	f.Exec(t, `UPDATE project_members SET is_active=false WHERE project_id=$1 AND user_id=$2`, f.ProjectID, f.OwnerID)
	// Access to the destination Story does not grant its earlier page source.
	testutil.Request(t, r, f.OwnerID, "GET", newPath+"/versions/1", nil, 404)
	svgRequest(t, r, f.OwnerID, newPath+"/sequence.svg?version=1", 404)
}

func TestSourceVisibilityIntersectionIncludesOtherProjectsAndGuestStoryOwnership(t *testing.T) {
	f := testutil.New(t)
	r := f.Router(scenarios.Register, documents.Register)
	storyID := story(t, f, "Restricted Story", 1)
	otherPages := fmt.Sprintf("/api/v1/workspaces/%s/projects/%s/pages", f.WorkspaceID, f.OtherProjectID)
	page := testutil.Object(t, testutil.Request(t, r, f.OwnerID, "POST", otherPages, map[string]any{"name": "Other project requirement"}, 201))
	base := f.Prefix() + "/scenarios"
	testutil.Request(t, r, f.MemberID, "POST", base, content(storyID, page["id"].(string)), 404)
	created := testutil.Object(t, testutil.Request(t, r, f.OwnerID, "POST", base, content(storyID, page["id"].(string)), 201))
	path := base + "/" + created["id"].(string)
	testutil.Request(t, r, f.MemberID, "GET", path, nil, 404)
	public := testutil.Object(t, testutil.Request(t, r, f.OwnerID, "POST", base, content(storyID), 201))
	publicPath := base + "/" + public["id"].(string)
	testutil.Request(t, r, f.GuestID, "GET", publicPath, nil, 404)
	f.Exec(t, `UPDATE projects SET guest_can_view_all=true WHERE id=$1`, f.ProjectID)
	testutil.Request(t, r, f.GuestID, "GET", publicPath, nil, 200)
	testutil.Request(t, r, f.GuestID, "GET", path, nil, 404)
	testutil.Request(t, r, f.GuestID, "PATCH", publicPath, map[string]any{"version": 1, "name": "Guest edit"}, 403)
	f.Exec(t, `UPDATE projects SET guest_can_view_all=false WHERE id=$1`, f.ProjectID)
	svgRequest(t, r, f.GuestID, publicPath+"/sequence.svg?version=1", 404)
	f.Exec(t, `UPDATE work_items SET archived_at=now() WHERE id=$1`, storyID)
	if list := testutil.Array(t, testutil.Request(t, r, f.OwnerID, "GET", base, nil, 200)); len(list) != 0 {
		t.Fatal("Archiving a primary Story did not remove its scenarios")
	}
}

func TestScenarioWriteWaitsForMembershipDemotion(t *testing.T) {
	f := testutil.New(t)
	r := f.Router(scenarios.Register)
	created := testutil.Object(t, testutil.Request(t, r, f.MemberID, "POST", f.Prefix()+"/scenarios", content(story(t, f, "Concurrent permissions", 1)), 201))
	path := f.Prefix() + "/scenarios/" + created["id"].(string)
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	tx, err := f.DB.SQL.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var locker int
	if err := tx.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&locker); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE project_members SET role=5 WHERE project_id=$1 AND user_id=$2`, f.ProjectID, f.MemberID); err != nil {
		t.Fatal(err)
	}
	finished := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		finished <- rawRequest(r, f.MemberID, "PATCH", path, map[string]any{"version": 1, "name": "Must not commit"})
	}()
	blocked := false
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := f.DB.SQL.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, locker).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("Scenario write did not wait for membership authorization to settle")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case response := <-finished:
		if response.Code != 403 {
			t.Fatalf("Demoted writer status %d: %s", response.Code, response.Body.String())
		}
	case <-ctx.Done():
		t.Fatal("Scenario request did not resume after demotion")
	}
	current := testutil.Object(t, testutil.Request(t, r, f.OwnerID, "GET", path, nil, 200))
	if current["name"] != created["name"] || current["version"] != float64(1) {
		t.Fatal("Demoted writer modified scenario content")
	}
}

func TestScenarioExportWaitsForPrivateSourceAndRechecksAddedSources(t *testing.T) {
	for _, change := range []string{"source_becomes_private", "private_source_is_added"} {
		t.Run(change, func(t *testing.T) {
			f := testutil.New(t)
			r := f.Router(scenarios.Register, documents.Register)
			storyID := story(t, f, "Concurrent source authorization", 1)
			page := testutil.Object(t, testutil.Request(t, r, f.MemberID, "POST", f.Prefix()+"/pages", map[string]any{"name": "Source with changing access", "is_private": change == "private_source_is_added"}, 201))
			input := content(storyID)
			if change == "source_becomes_private" {
				input["source_page_ids"] = []string{page["id"].(string)}
			}
			created := testutil.Object(t, testutil.Request(t, r, f.MemberID, "POST", f.Prefix()+"/scenarios", input, 201))
			path := f.Prefix() + "/scenarios/" + created["id"].(string)
			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
			defer cancel()
			tx, err := f.DB.SQL.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			var locker int
			if err := tx.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&locker); err != nil {
				t.Fatal(err)
			}
			if change == "source_becomes_private" {
				_, err = tx.ExecContext(ctx, `UPDATE pages SET is_private=true,version=version+1 WHERE id=$1`, page["id"])
			} else {
				_, err = tx.ExecContext(ctx, `UPDATE business_scenarios SET version=version+1,body=jsonb_set(body,'{source_page_ids}',jsonb_build_array($2::text)) WHERE id=$1`, created["id"], page["id"])
				if err == nil {
					_, err = tx.ExecContext(ctx, `INSERT INTO business_scenario_sources(scenario_id,workspace_id,page_id) VALUES($1,$2,$3)`, created["id"], f.WorkspaceID, page["id"])
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			finished := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				finished <- rawRequest(r, f.OwnerID, "GET", path+"/sequence.svg?version=1&download=1", nil)
			}()
			blocked := false
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				if err := f.DB.SQL.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, locker).Scan(&blocked); err != nil {
					t.Fatal(err)
				}
				if blocked {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			if !blocked {
				t.Fatal("Historical export did not wait for the uncommitted authorization-changing row")
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			select {
			case response := <-finished:
				if response.Code != 404 {
					t.Fatalf("Historical export bypassed new source visibility: %d %s", response.Code, response.Body.String())
				}
			case <-ctx.Done():
				t.Fatal("Historical export did not resume after source permission commit")
			}
		})
	}
}

func TestRelationshipsOnlyExposeCurrentlyVisibleTargetsInHistoryAndEdits(t *testing.T) {
	f := testutil.New(t)
	r := f.Router(scenarios.Register, documents.Register)
	storyID := story(t, f, "Shared use cases", 1)
	base := f.Prefix() + "/scenarios"
	page := testutil.Object(t, testutil.Request(t, r, f.OwnerID, "POST", f.Prefix()+"/pages", map[string]any{"name": "Target source"}, 201))
	primary := testutil.Object(t, testutil.Request(t, r, f.OwnerID, "POST", base, content(storyID), 201))
	targetInput := content(storyID, page["id"].(string))
	targetInput["name"], targetInput["bind_story_name"] = "Initially visible target", false
	target := testutil.Object(t, testutil.Request(t, r, f.OwnerID, "POST", base, targetInput, 201))
	path := base + "/" + primary["id"].(string)
	relationID := uuid.NewString()
	testutil.Request(t, r, f.MemberID, "PATCH", path, map[string]any{"version": 1, "relationships": []any{map[string]any{"id": relationID, "kind": "include", "from_id": primary["id"], "to_id": target["id"]}}}, 200)
	if svg := svgRequest(t, r, f.MemberID, base+"/use-case.svg", 200); !strings.Contains(svg, relationID) || !strings.Contains(svg, "Initially visible target") {
		t.Fatal("Explicit visible use-case relationship was not rendered")
	}
	testutil.Request(t, r, f.OwnerID, "PATCH", f.Prefix()+"/pages/"+page["id"].(string), map[string]any{"version": 1, "is_private": true}, 200)
	for _, suffix := range []string{"", "/versions/2"} {
		read := testutil.Object(t, testutil.Request(t, r, f.MemberID, "GET", path+suffix, nil, 200))
		if len(read["relationships"].([]any)) != 0 {
			t.Fatal("Current or historical JSON exposed a now-private relationship target")
		}
	}
	for _, suffix := range []string{"/use-case.svg", "/use-case.svg?version=2"} {
		if svg := svgRequest(t, r, f.MemberID, path+suffix, 200); strings.Contains(svg, relationID) || strings.Contains(svg, target["id"].(string)) || strings.Contains(svg, "Initially visible target") {
			t.Fatal("Current or historical SVG exposed a now-private relationship target")
		}
	}
	// Losing a target does not prevent editing unrelated primary content, and
	// that mutation's response must not re-expose the hidden stored relationship.
	changed := testutil.Object(t, testutil.Request(t, r, f.MemberID, "PATCH", path, map[string]any{"version": 2, "name": "Edited after target became private"}, 200))
	if len(changed["relationships"].([]any)) != 0 {
		t.Fatal("Mutation response exposed a hidden relationship target")
	}
}

func TestAggregateUseCaseFilterIntersectsVisibleStoriesAndPreservesExplicitEmpty(t *testing.T) {
	f := testutil.New(t)
	r := f.Router(scenarios.Register)
	firstStory, secondStory := story(t, f, "Included story", 1), story(t, f, "Filtered story", 2)
	base := f.Prefix() + "/scenarios"
	first := testutil.Object(t, testutil.Request(t, r, f.MemberID, "POST", base, content(firstStory), 201))
	second := testutil.Object(t, testutil.Request(t, r, f.MemberID, "POST", base, content(secondStory), 201))
	relationID := uuid.NewString()
	testutil.Request(t, r, f.MemberID, "PATCH", base+"/"+first["id"].(string), map[string]any{"version": 1, "relationships": []any{map[string]any{"id": relationID, "kind": "include", "from_id": first["id"], "to_id": second["id"]}}}, 200)
	filtered := svgRequest(t, r, f.MemberID, base+"/use-case.svg?story_ids="+firstStory.String(), 200)
	if !strings.Contains(filtered, first["id"].(string)) || strings.Contains(filtered, second["id"].(string)) || strings.Contains(filtered, relationID) {
		t.Fatal("Aggregate filter did not constrain both scenario and relationship endpoints")
	}
	empty := svgRequest(t, r, f.MemberID, base+"/use-case.svg?story_ids=", 200)
	if strings.Contains(empty, first["id"].(string)) || strings.Contains(empty, second["id"].(string)) {
		t.Fatal("An explicitly empty filter was treated as an unfiltered project")
	}
	all := svgRequest(t, r, f.MemberID, base+"/use-case.svg", 200)
	if !strings.Contains(all, first["id"].(string)) || !strings.Contains(all, second["id"].(string)) || !strings.Contains(all, relationID) {
		t.Fatal("Unfiltered aggregate lost visible scenarios or explicit relationships")
	}
	for _, query := range []string{"story_ids=not-a-uuid", "story_ids=" + uuid.Nil.String(), "story_ids=" + firstStory.String() + ",", "story_ids=&story_ids=" + firstStory.String()} {
		svgRequest(t, r, f.MemberID, base+"/use-case.svg?"+query, 400)
	}
	hidden := svgRequest(t, r, f.GuestID, base+"/use-case.svg?story_ids="+firstStory.String(), 200)
	if strings.Contains(hidden, first["id"].(string)) {
		t.Fatal("A client-provided Story filter bypassed current authorization")
	}
}

func TestLaterScenarioWorkProtectsStoryUndoEvenAfterScenarioDeletion(t *testing.T) {
	for _, removeScenario := range []bool{false, true} {
		t.Run(fmt.Sprintf("scenario_deleted_%t", removeScenario), func(t *testing.T) {
			f := testutil.New(t)
			r := f.Router(automation.Register, scenarios.Register)
			item := story(t, f, "Automatically generated Story", 1)
			fingerprint, err := workitems.MutationFingerprint(context.Background(), f.DB.SQL, f.WorkspaceID, f.ProjectID, item)
			if err != nil {
				t.Fatal(err)
			}
			runID := uuid.New()
			after, _ := json.Marshal([]automation.SavedItem{{ID: item, Version: 1, Fingerprint: fingerprint, Created: true}})
			f.Exec(t, `INSERT INTO automation_runs(id,workspace_id,project_id,requested_by,authorized_by,kind,status,idempotency_key,input_fingerprint,policy_version,input,algorithm_version,after_state) VALUES($1,$2,$3,$4,$4,'decompose','applied','scenario-undo','scenario-undo-fingerprint',1,'{}','scenario-test',$5::jsonb)`, runID, f.WorkspaceID, f.ProjectID, f.OwnerID, string(after))
			scenario := testutil.Object(t, testutil.Request(t, r, f.MemberID, "POST", f.Prefix()+"/scenarios", content(item), 201))
			if removeScenario {
				testutil.Request(t, r, f.MemberID, "DELETE", f.Prefix()+"/scenarios/"+scenario["id"].(string)+"?version=1", nil, 204)
			}
			testutil.Request(t, r, f.OwnerID, "POST", f.Prefix()+"/automation/runs/"+runID.String()+"/undo", map[string]any{}, 409)
			var deleted bool
			if err := f.DB.SQL.QueryRow(`SELECT deleted_at IS NOT NULL FROM work_items WHERE id=$1`, item).Scan(&deleted); err != nil || deleted {
				t.Fatalf("Later human scenario work was hidden by an automatic Story undo: %v %v", deleted, err)
			}
		})
	}
}

func TestScenarioSessionRevocationDuringDomainLockWait(t *testing.T) {
	f := testutil.New(t)
	created := testutil.Object(t, testutil.Request(t, f.Router(scenarios.Register), f.MemberID, "POST", f.Prefix()+"/scenarios", content(story(t, f, "Credential wait", 1)), 201))
	sessionID := uuid.New()
	f.Exec(t, `INSERT INTO sessions(id,user_id,token_hash,csrf_hash,expires_at) VALUES($1,$2,$3,'test-csrf',now()+interval '1 hour')`, sessionID, f.MemberID, uuid.NewString())
	r := gin.New()
	r.Use(func(c *gin.Context) {
		actorID, _ := uuid.Parse(c.GetHeader("X-Test-Actor"))
		actor := identity.Actor{UserID: actorID}
		if actorID == f.MemberID {
			actor.SessionID = sessionID
		}
		c.Set(httpapi.ActorKey, actor)
		c.Next()
	})
	scenarios.Register(r.Group("/api/v1"), f.Deps)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	tx, err := f.DB.SQL.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var locker int
	if err := tx.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&locker); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `SELECT id FROM business_scenarios WHERE id=$1 FOR UPDATE`, created["id"]); err != nil {
		t.Fatal(err)
	}
	path := f.Prefix() + "/scenarios/" + created["id"].(string)
	finished := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		finished <- rawRequest(r, f.MemberID, "PATCH", path, map[string]any{"version": 1, "name": "Revoked session write"})
	}()
	blocked := false
	until := time.Now().Add(3 * time.Second)
	for time.Now().Before(until) {
		if err := f.DB.SQL.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, locker).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("Scenario write did not wait for its domain row")
	}
	f.Exec(t, `UPDATE sessions SET revoked_at=now() WHERE id=$1`, sessionID)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case response := <-finished:
		if response.Code != 401 {
			t.Fatalf("Revoked waiting session status %d: %s", response.Code, response.Body.String())
		}
	case <-ctx.Done():
		t.Fatal("Scenario mutation did not resume after lock release")
	}
	current := testutil.Object(t, testutil.Request(t, r, f.OwnerID, "GET", path, nil, 200))
	if current["version"] != float64(1) || current["name"] != created["name"] {
		t.Fatal("Revoked session modified the scenario after its lock wait")
	}
}

func TestScenarioRequirementsFlagGatesEveryHTTPRoute(t *testing.T) {
	f := testutil.New(t)
	r := f.Router(scenarios.Register)
	storyID := story(t, f, "Rollout-controlled scenario", 1)
	base := f.Prefix() + "/scenarios"
	created := testutil.Object(t, testutil.Request(t, r, f.MemberID, "POST", base, content(storyID), 201))
	path := base + "/" + created["id"].(string)
	for _, settings := range []string{`{}`, `{"requirements_enabled":null}`, `{"requirements_enabled":true}`} {
		f.Exec(t, `UPDATE projects SET settings=$2::jsonb WHERE id=$1`, f.ProjectID, settings)
		testutil.Request(t, r, f.MemberID, "GET", path, nil, 200)
		svgRequest(t, r, f.MemberID, base+"/use-case.svg", 200)
	}
	f.Exec(t, `UPDATE projects SET settings='{"requirements_enabled":false}'::jsonb WHERE id=$1`, f.ProjectID)
	requests := []struct {
		name, method, path string
		body               any
	}{
		{"list", "GET", base, nil},
		{"create", "POST", base, content(storyID)},
		{"get", "GET", path, nil},
		{"update", "PATCH", path, map[string]any{"version": 1, "name": "Disabled edit"}},
		{"delete", "DELETE", path + "?version=1", nil},
		{"copy", "POST", path + "/copy", map[string]any{"version": 1}},
		{"versions", "GET", path + "/versions", nil},
		{"historical", "GET", path + "/versions/1", nil},
		{"project_diagram", "GET", base + "/use-case.svg?download=1", nil},
		{"sequence_diagram", "GET", path + "/sequence.svg?download=1", nil},
		{"historical_sequence", "GET", path + "/sequence.svg?version=1&download=1", nil},
		{"use_case_diagram", "GET", path + "/use-case.svg?download=1", nil},
		{"historical_use_case", "GET", path + "/use-case.svg?version=1&download=1", nil},
	}
	for _, request := range requests {
		t.Run(request.name, func(t *testing.T) {
			response := rawRequest(r, f.MemberID, request.method, request.path, request.body)
			if response.Code != 403 || !strings.Contains(response.Body.String(), `"requirements_disabled"`) {
				t.Fatalf("Disabled scenario route returned %d: %s", response.Code, response.Body.String())
			}
			if strings.Contains(response.Body.String(), created["name"].(string)) || strings.Contains(response.Body.String(), "<svg") {
				t.Fatal("Disabled scenario route disclosed content")
			}
			testutil.Request(t, r, f.OutsiderID, request.method, request.path, request.body, 404)
		})
	}
	var scenariosCount, versionsCount int
	if err := f.DB.SQL.QueryRow(`SELECT count(*) FROM business_scenarios WHERE project_id=$1`, f.ProjectID).Scan(&scenariosCount); err != nil {
		t.Fatal(err)
	}
	if err := f.DB.SQL.QueryRow(`SELECT count(*) FROM business_scenario_versions WHERE scenario_id=$1`, created["id"]).Scan(&versionsCount); err != nil {
		t.Fatal(err)
	}
	if scenariosCount != 1 || versionsCount != 1 {
		t.Fatalf("Disabled mutations changed scenario/history rows: scenarios=%d versions=%d", scenariosCount, versionsCount)
	}
	f.Exec(t, `UPDATE projects SET settings='{"requirements_enabled":true}'::jsonb WHERE id=$1`, f.ProjectID)
	current := testutil.Object(t, testutil.Request(t, r, f.MemberID, "GET", path, nil, 200))
	if current["version"] != float64(1) || current["name"] != created["name"] {
		t.Fatal("Rollout changes or disabled mutations changed existing scenario content")
	}
	testutil.Request(t, r, f.MemberID, "PATCH", path, map[string]any{"version": 1, "name": "Reenabled edit"}, 200)
}

func TestScenarioRequirementsDisableSettlesBeforeWaitingWriteAndExport(t *testing.T) {
	f := testutil.New(t)
	r := f.Router(scenarios.Register)
	created := testutil.Object(t, testutil.Request(t, r, f.MemberID, "POST", f.Prefix()+"/scenarios", content(story(t, f, "Rollout lock wait", 1)), 201))
	path := f.Prefix() + "/scenarios/" + created["id"].(string)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	tx, err := f.DB.SQL.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var locker int
	if err := tx.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&locker); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE projects SET settings='{"requirements_enabled":false}'::jsonb WHERE id=$1`, f.ProjectID); err != nil {
		t.Fatal(err)
	}
	finished := make(chan *httptest.ResponseRecorder, 2)
	go func() {
		finished <- rawRequest(r, f.MemberID, "PATCH", path, map[string]any{"version": 1, "name": "Must not commit"})
	}()
	go func() {
		finished <- rawRequest(r, f.MemberID, "GET", path+"/sequence.svg?version=1&download=1", nil)
	}()
	var waiting int
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := f.DB.SQL.QueryRowContext(ctx, `SELECT count(*) FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid))`, locker).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting == 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if waiting != 2 {
		t.Fatalf("Write and historical export did not both wait for the project setting: %d waiting", waiting)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		select {
		case response := <-finished:
			if response.Code != 403 || !strings.Contains(response.Body.String(), `"requirements_disabled"`) {
				t.Fatalf("Waiting scenario request returned %d: %s", response.Code, response.Body.String())
			}
		case <-ctx.Done():
			t.Fatal("Scenario request did not resume after the project setting committed")
		}
	}
	var name string
	var version int64
	if err := f.DB.SQL.QueryRow(`SELECT body->>'name',version FROM business_scenarios WHERE id=$1 AND deleted_at IS NULL`, created["id"]).Scan(&name, &version); err != nil {
		t.Fatal(err)
	}
	if version != 1 || name != created["name"] {
		t.Fatal("Waiting mutation changed the scenario after the project disabled requirements")
	}
}
