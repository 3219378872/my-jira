package requirements_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"my-jira/apps/api/internal/foundation"
	"my-jira/apps/api/internal/planning"
	"my-jira/apps/api/internal/platform"
	"my-jira/apps/api/internal/platform/httpapi"
	"my-jira/apps/api/internal/platform/identity"
	"my-jira/apps/api/internal/requirements"
	"my-jira/apps/api/internal/support/testutil"
	"my-jira/apps/api/internal/workitems"
)

func TestHierarchyStoryCommitmentActivityAndAtomicChanges(t *testing.T) {
	f := testutil.New(t)
	r := f.Router(requirements.Register, workitems.Register, planning.Register)
	base := f.Prefix()
	call := func(method, path string, body any, status int) map[string]any {
		return testutil.Request(t, r, f.OwnerID, method, base+path, body, status)
	}
	create := func(fields map[string]any) map[string]any {
		return testutil.Object(t, call("POST", "/issues", fields, 201))
	}
	epic := create(map[string]any{"name": "需求组织", "requirement_type": "epic"})
	activity := testutil.Object(t, call("POST", "/requirements/activities", map[string]any{"name": "安排工作", "epic_id": epic["id"]}, 201))
	a := testutil.Object(t, call("POST", "/cycles", map[string]any{"name": "S1", "start_date": "2026-09-14", "end_date": "2026-09-25"}, 201))
	b := testutil.Object(t, call("POST", "/cycles", map[string]any{"name": "S2", "start_date": "2026-09-21", "end_date": "2026-10-02"}, 201))
	story := create(map[string]any{"name": "分配负责人", "requirement_type": "story", "parent_id": epic["id"], "activity_id": activity["id"], "cycle_id": a["id"], "story_role": "团队成员", "story_goal": "分配负责人", "story_benefit": "明确责任", "acceptance_criteria": []string{"负责人保存后可恢复"}})
	task := create(map[string]any{"name": "实现选择器", "requirement_type": "task", "parent_id": story["id"], "start_date": "2026-09-14", "target_date": "2026-09-15", "estimated_minutes": 960, "assignee_ids": []uuid.UUID{f.OwnerID, f.MemberID}, "allocation_weights": []map[string]any{{"member_id": f.OwnerID, "weight": 75}, {"member_id": f.MemberID, "weight": 25}}})
	if task["cycle_id"] != a["id"] || task["remaining_minutes"] != float64(960) {
		t.Fatalf("Task initial inheritance/estimate missing: %#v", task)
	}
	call("PATCH", "/issues/"+story["id"].(string), map[string]any{"version": 1, "cycle_id": b["id"], "map_position": 2048}, 200)
	unchanged := testutil.Object(t, call("GET", "/issues/"+task["id"].(string), nil, 200))
	if unchanged["cycle_id"] != a["id"] || unchanged["start_date"] != "2026-09-14" {
		t.Fatalf("Story move rewrote execution: %#v", unchanged)
	}
	call("PATCH", "/issues/"+epic["id"].(string), map[string]any{"version": 1, "requirement_type": "task"}, 400)
	call("PATCH", "/issues/"+task["id"].(string), map[string]any{"version": 1, "parent_id": epic["id"]}, 400)
	call("POST", "/issues", map[string]any{"name": "Wrong activity", "requirement_type": "task", "activity_id": activity["id"]}, 400)
	call("POST", "/issues", map[string]any{"name": "Wrong allocation", "assignee_ids": []uuid.UUID{f.OwnerID}, "allocation_weights": []map[string]any{{"member_id": f.MemberID, "weight": 100}}}, 400)
	call("DELETE", "/requirements/activities/"+activity["id"].(string)+"?version=1", nil, 409)
	snapshot := testutil.Object(t, call("GET", "/requirements/snapshot", nil, 200))
	revision := snapshot["revision"]
	batch := map[string]any{"idempotency_key": "rollback", "expected_revision": revision, "commands": []map[string]any{{"operation": "update", "id": story["id"], "version": 2, "fields": map[string]any{"priority": "urgent"}}, {"operation": "update", "id": task["id"], "version": 999, "fields": map[string]any{"priority": "urgent"}}}}
	call("POST", "/planning/changesets", batch, 409)
	after := testutil.Object(t, call("GET", "/requirements/snapshot", nil, 200))
	if after["revision"] != revision {
		t.Fatalf("Failed batch committed event revisions %v -> %v", revision, after["revision"])
	}
	storyNow := testutil.Object(t, call("GET", "/issues/"+story["id"].(string), nil, 200))
	if storyNow["priority"] != "none" || storyNow["version"] != float64(2) {
		t.Fatal("Failed batch partially committed Story")
	}
	batch["idempotency_key"] = "execution-transfer"
	batch["commands"] = []map[string]any{{"operation": "update", "id": task["id"], "version": 1, "fields": map[string]any{"cycle_id": b["id"], "start_date": "2026-09-21", "target_date": "2026-09-22"}}}
	result := testutil.Object(t, call("POST", "/planning/changesets", batch, 200))
	replay := testutil.Object(t, call("POST", "/planning/changesets", batch, 200))
	if result["batch_id"] != replay["batch_id"] {
		t.Fatal("Replay applied another batch")
	}
	call("DELETE", "/requirements/activities/"+activity["id"].(string)+"?version=1&migrate_to=null", nil, 204)
	storyNow = testutil.Object(t, call("GET", "/issues/"+story["id"].(string), nil, 200))
	if storyNow["activity_id"] != nil || storyNow["parent_id"] != epic["id"] {
		t.Fatal("Activity migration lost Story or Epic")
	}
}

func TestChangesetCreationDependenciesReplayAndDeletion(t *testing.T) {
	f := testutil.New(t)
	r := f.Router(requirements.Register, workitems.Register)
	base := f.Prefix()
	path := base + "/planning/changesets"
	batch := map[string]any{"idempotency_key": "prd-v1", "commands": []map[string]any{{"operation": "create", "client_id": "epic", "fields": map[string]any{"name": "E", "requirement_type": "epic"}}, {"operation": "create", "client_id": "story", "fields": map[string]any{"name": "S", "requirement_type": "story", "parent_id": "$epic"}}, {"operation": "create", "client_id": "first", "fields": map[string]any{"name": "A", "requirement_type": "task", "parent_id": "$story"}}, {"operation": "create", "client_id": "second", "fields": map[string]any{"name": "B", "requirement_type": "task", "parent_id": "$story", "dependency_ids": []string{"$first"}}}}}
	result := testutil.Object(t, testutil.Request(t, r, f.OwnerID, "POST", path, batch, 200))
	replay := testutil.Object(t, testutil.Request(t, r, f.OwnerID, "POST", path, batch, 200))
	if result["batch_id"] != replay["batch_id"] {
		t.Fatal("Duplicate source created duplicate batch")
	}
	ids := result["created_ids"].(map[string]any)
	second := testutil.Object(t, testutil.Request(t, r, f.OwnerID, "GET", base+"/issues/"+ids["second"].(string), nil, 200))
	deps := second["dependency_ids"].([]any)
	if len(deps) != 1 || deps[0] != ids["first"] {
		t.Fatal("Shared command dependency mapping failed")
	}
	testutil.Request(t, r, f.OwnerID, "PATCH", base+"/issues/"+ids["first"].(string), map[string]any{"version": 1, "dependency_ids": []any{ids["second"]}}, 400)
	deletion := map[string]any{"idempotency_key": "delete-second", "commands": []map[string]any{{"operation": "delete", "id": ids["second"], "version": 1}}}
	deleted := testutil.Object(t, testutil.Request(t, r, f.OwnerID, "POST", path, deletion, 200))
	again := testutil.Object(t, testutil.Request(t, r, f.OwnerID, "POST", path, deletion, 200))
	if deleted["batch_id"] != again["batch_id"] {
		t.Fatal("Deleted batch cannot replay")
	}
	testutil.Request(t, r, f.GuestID, "POST", path, batch, 403)
	testutil.Request(t, r, f.OutsiderID, "GET", base+"/requirements/snapshot", nil, 404)
}

func TestSnapshotFullAuthorizedCollectionAndDurableHistory(t *testing.T) {
	f := testutil.New(t)
	r := f.Router(requirements.Register, workitems.Register)
	base := f.Prefix()
	f.Exec(t, `INSERT INTO work_items(id,workspace_id,project_id,state_id,created_by,updated_by,name,sequence_id) SELECT gen_random_uuid(),$1,$2,$3,$4,$4,'Visible '||n,n FROM generate_series(1,230)n`, f.WorkspaceID, f.ProjectID, f.StateID, f.OwnerID)
	guest := f.Issue(t, f.ProjectID, f.StateID, f.GuestID, "Guest own", 231)
	all := testutil.Object(t, testutil.Request(t, r, f.OwnerID, "GET", base+"/requirements/snapshot", nil, 200))
	if len(all["items"].([]any)) != 231 {
		t.Fatal("Snapshot paginated the authorized aggregate")
	}
	visible := testutil.Object(t, testutil.Request(t, r, f.GuestID, "GET", base+"/requirements/snapshot", nil, 200))
	if len(visible["items"].([]any)) != 1 {
		t.Fatal("Guest snapshot widened ownership scope")
	}
	before := int64(all["revision"].(float64))
	testutil.Request(t, r, f.GuestID, "PATCH", base+"/issues/"+guest.String(), map[string]any{"version": 1, "name": "Guest revision"}, 200)
	scope, _ := f.Deps.Policy.Project(context.Background(), identity.Actor{UserID: f.OwnerID}, f.WorkspaceID, f.ProjectID, identity.Guest)
	events, current, expired, err := requirements.NewService(f.Deps).Replay(context.Background(), scope, before, 200)
	if err != nil || expired || len(events) == 0 || current <= before {
		t.Fatalf("Snapshot-to-subscribe update lost: %v %v %v", events, expired, err)
	}
	var historicalGroup string
	if err = f.DB.SQL.QueryRowContext(context.Background(), `SELECT after_data->>'state_group' FROM work_item_history WHERE work_item_id=$1 AND action='update' ORDER BY revision DESC LIMIT 1`, guest).Scan(&historicalGroup); err != nil || historicalGroup != "unstarted" {
		t.Fatalf("Durable historical state missing: %q %v", historicalGroup, err)
	}
	f.Exec(t, `UPDATE states SET group_name='started' WHERE id=$1`, f.StateID)
	var beforeGroup, afterGroup string
	if err = f.DB.SQL.QueryRowContext(context.Background(), `SELECT before_data->>'state_group',after_data->>'state_group' FROM work_item_history WHERE work_item_id=$1 AND action='update' ORDER BY revision DESC LIMIT 1`, guest).Scan(&beforeGroup, &afterGroup); err != nil || beforeGroup != "unstarted" || afterGroup != "started" {
		t.Fatalf("Workflow edits lost historical group meanings: %s %s %v", beforeGroup, afterGroup, err)
	}
	f.Exec(t, `DELETE FROM project_events WHERE project_id=$1 AND revision<=$2`, f.ProjectID, current-1)
	_, _, expired, err = requirements.NewService(f.Deps).Replay(context.Background(), scope, 0, 200)
	if err != nil || !expired {
		t.Fatalf("Expired cursor not identified: %v %v", expired, err)
	}
}

func TestPermissionRevokedDuringProjectLockWait(t *testing.T) {
	f := testutil.New(t)
	r := f.Router(requirements.Register, workitems.Register)
	id := f.Issue(t, f.ProjectID, f.StateID, f.MemberID, "Locked", 1)
	tx, err := f.DB.SQL.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = workitems.LockProject(context.Background(), tx, f.ProjectID); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(r)
	defer func() { _ = tx.Rollback(); server.Close() }()
	done := make(chan int, 1)
	go func() {
		body := fmt.Sprintf(`{"idempotency_key":"wait","commands":[{"operation":"update","id":"%s","version":1,"fields":{"name":"Should fail"}}]}`, id)
		req, _ := http.NewRequest("POST", server.URL+f.Prefix()+"/planning/changesets", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Test-Actor", f.MemberID.String())
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			done <- 0
			return
		}
		defer res.Body.Close()
		done <- res.StatusCode
	}()
	deadline := time.Now().Add(4 * time.Second)
	waiting := false
	for time.Now().Before(deadline) {
		var count int
		if err = f.DB.SQL.QueryRowContext(context.Background(), `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event='advisory'`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count > 0 {
			waiting = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !waiting {
		t.Fatal("Mutation never waited for the project lock")
	}
	f.Exec(t, `UPDATE workspace_members SET is_active=false WHERE workspace_id=$1 AND user_id=$2`, f.WorkspaceID, f.MemberID)
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case status := <-done:
		if status != 404 {
			t.Fatalf("Revoked writer returned %d", status)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Writer did not finish after releasing lock")
	}
	var name string
	_ = f.DB.SQL.QueryRowContext(context.Background(), `SELECT name FROM work_items WHERE id=$1`, id).Scan(&name)
	if name != "Locked" {
		t.Fatal("Revoked writer committed")
	}
}

func TestSSERevocationWithoutContentMutation(t *testing.T) {
	f := testutil.New(t)
	r := f.Router(requirements.Register)
	server := httptest.NewServer(r)
	defer server.Close()
	var cursor int64
	if err := f.DB.SQL.QueryRowContext(context.Background(), `SELECT revision FROM project_revisions WHERE project_id=$1`, f.ProjectID).Scan(&cursor); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", server.URL+f.Prefix()+"/requirements/events?cursor="+fmt.Sprint(cursor), nil)
	req.Header.Set("X-Test-Actor", f.MemberID.String())
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatal(res.Status)
	}
	f.Exec(t, `UPDATE project_members SET role=5 WHERE workspace_id=$1 AND project_id=$2 AND user_id=$3`, f.WorkspaceID, f.ProjectID, f.MemberID)
	scanner := bufio.NewScanner(res.Body)
	found := false
	for scanner.Scan() {
		if scanner.Text() == "event: authorization_changed" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Idle revoked stream did not invalidate: %v", scanner.Err())
	}
}

func TestRequirementInputRejectsNonIntegerAndUnknownFields(t *testing.T) {
	f := testutil.New(t)
	r := f.Router(requirements.Register, workitems.Register)
	for _, fields := range []map[string]any{{"name": "Fraction", "estimated_minutes": 1.5}, {"name": "Unknown", "requirement_type": "Story"}, {"name": "Invalid criteria", "acceptance_criteria": []any{true}}, {"name": "Duplicate skill", "required_skills": []string{"Go", "go"}}, {"name": "Negative", "remaining_minutes": -1}} {
		testutil.Request(t, r, f.OwnerID, "POST", f.Prefix()+"/issues", fields, 400)
	}
}

func TestParentFingerprintRetainsLaterHumanLinkage(t *testing.T) {
	f := testutil.New(t)
	r := f.Router(requirements.Register, workitems.Register)
	parent := f.Issue(t, f.ProjectID, f.StateID, f.OwnerID, "Automatic parent", 1)
	fingerprint := func() string {
		value, err := workitems.MutationFingerprint(context.Background(), f.DB.SQL, f.WorkspaceID, f.ProjectID, parent)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	before := fingerprint()
	child := testutil.Object(t, testutil.Request(t, r, f.MemberID, "POST", f.Prefix()+"/issues", map[string]any{"name": "Later human task", "parent_id": parent}, 201))
	attached := fingerprint()
	if attached == before {
		t.Fatal("Later child did not protect the parent from undo")
	}
	testutil.Request(t, r, f.MemberID, "DELETE", f.Prefix()+"/issues/"+child["id"].(string), nil, 204)
	after := fingerprint()
	if after == before || after == attached {
		t.Fatal("Removing a later child erased durable parent linkage evidence")
	}
}

func TestRevokedSessionAndTokenCannotCommitAfterLockWait(t *testing.T) {
	for _, kind := range []string{"session", "token"} {
		t.Run(kind, func(t *testing.T) {
			f := testutil.New(t)
			actor := identity.Actor{UserID: f.MemberID}
			credential := uuid.New()
			if kind == "session" {
				actor.SessionID = credential
				f.Exec(t, `INSERT INTO sessions(id,user_id,token_hash,csrf_hash,expires_at) VALUES($1,$2,$3,'isolated-csrf',now()+interval '1 hour')`, credential, f.MemberID, uuid.NewString())
			} else {
				actor.TokenID = credential
				actor.TokenWorkspaceID = f.WorkspaceID
				f.Exec(t, `INSERT INTO api_tokens(id,user_id,workspace_id,name,token_hash,prefix) VALUES($1,$2,$3,'isolated-token',$4,'test')`, credential, f.MemberID, f.WorkspaceID, uuid.NewString())
			}
			service := requirements.NewService(f.Deps)
			created, err := service.Apply(context.Background(), actor, f.WorkspaceID, f.ProjectID, requirements.ChangeSet{IdempotencyKey: "before-revocation", Commands: []requirements.Command{{Operation: "create", Fields: map[string]json.RawMessage{"name": json.RawMessage(`"Before revoke"`)}}}})
			if err != nil {
				t.Fatal(err)
			}
			id := uuid.MustParse(created.Items[0]["id"].(string))
			tx, err := f.DB.SQL.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if err = workitems.LockProject(context.Background(), tx, f.ProjectID); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				_, err := service.Apply(context.Background(), actor, f.WorkspaceID, f.ProjectID, requirements.ChangeSet{IdempotencyKey: "after-revocation", Commands: []requirements.Command{{Operation: "update", ID: id, Version: 1, Fields: map[string]json.RawMessage{"name": json.RawMessage(`"After revoke"`)}}}})
				done <- err
			}()
			deadline := time.Now().Add(4 * time.Second)
			waiting := false
			for time.Now().Before(deadline) {
				var count int
				if err = f.DB.SQL.QueryRowContext(context.Background(), `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event='advisory'`).Scan(&count); err != nil {
					t.Fatal(err)
				}
				if count > 0 {
					waiting = true
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if !waiting {
				t.Fatal("Command did not wait for the project lock")
			}
			if kind == "session" {
				f.Exec(t, `UPDATE sessions SET revoked_at=now() WHERE id=$1`, credential)
			} else {
				f.Exec(t, `UPDATE api_tokens SET revoked_at=now() WHERE id=$1`, credential)
			}
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			select {
			case err = <-done:
				var denied *httpapi.Error
				if !errors.As(err, &denied) || denied.Status != 401 {
					t.Fatalf("Revoked %s result: %v", kind, err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Credential revocation check did not complete")
			}
			var name string
			if err = f.DB.SQL.QueryRowContext(context.Background(), `SELECT name FROM work_items WHERE id=$1`, id).Scan(&name); err != nil || name != "Before revoke" {
				t.Fatalf("Revoked credential committed: %q %v", name, err)
			}
		})
	}
}

func TestArchivedActivityPreservesExistingStoryEdits(t *testing.T) {
	f := testutil.New(t)
	r := f.Router(requirements.Register, workitems.Register)
	base := f.Prefix()
	activity := testutil.Object(t, testutil.Request(t, r, f.OwnerID, "POST", base+"/requirements/activities", map[string]any{"name": "Completed discovery"}, 201))
	story := testutil.Object(t, testutil.Request(t, r, f.OwnerID, "POST", base+"/issues", map[string]any{"name": "Existing Story", "requirement_type": "story", "activity_id": activity["id"]}, 201))
	testutil.Request(t, r, f.OwnerID, "PATCH", base+"/requirements/activities/"+activity["id"].(string), map[string]any{"version": 1, "archived_at": "2026-09-12T00:00:00Z"}, 200)
	testutil.Request(t, r, f.OwnerID, "PATCH", base+"/issues/"+story["id"].(string), map[string]any{"version": 1, "priority": "high"}, 200)
	testutil.Request(t, r, f.OwnerID, "POST", base+"/issues", map[string]any{"name": "New Story", "requirement_type": "story", "activity_id": activity["id"]}, 400)
}

func TestArchivedProjectHasReadOnlySnapshotAndRejectsCommands(t *testing.T) {
	f := testutil.New(t)
	r := f.Router(requirements.Register, workitems.Register)
	base := f.Prefix()
	id := f.Issue(t, f.ProjectID, f.StateID, f.OwnerID, "Preserved", 1)
	before := testutil.Object(t, testutil.Request(t, r, f.OwnerID, "GET", base+"/requirements/snapshot", nil, 200))
	cursor := int64(before["revision"].(float64))
	f.Exec(t, `UPDATE projects SET archived_at=now() WHERE id=$1`, f.ProjectID)
	snapshot := testutil.Object(t, testutil.Request(t, r, f.OwnerID, "GET", base+"/requirements/snapshot", nil, 200))
	permissions := snapshot["permissions"].(map[string]any)
	if permissions["can_edit"] != false || permissions["can_admin"] != false || len(snapshot["items"].([]any)) != 1 {
		t.Fatalf("Archived snapshot permissions or read failed: %#v", snapshot)
	}
	testutil.Request(t, r, f.OwnerID, "PATCH", base+"/issues/"+id.String(), map[string]any{"version": 1, "name": "Forbidden"}, 409)
	testutil.Request(t, r, f.OwnerID, "POST", base+"/planning/changesets", map[string]any{"idempotency_key": "archived", "commands": []map[string]any{{"operation": "update", "id": id, "version": 1, "fields": map[string]any{"name": "Forbidden"}}}}, 409)
	scope, _ := f.Deps.Policy.Project(context.Background(), identity.Actor{UserID: f.OwnerID}, f.WorkspaceID, f.ProjectID, identity.Guest)
	events, _, _, err := requirements.NewService(f.Deps).Replay(context.Background(), scope, cursor, 200)
	if err != nil {
		t.Fatal(err)
	}
	invalidated := false
	for _, event := range events {
		invalidated = invalidated || event.Kind == "authorization.changed"
	}
	if !invalidated {
		t.Fatal("Archive did not invalidate the old permission snapshot")
	}
	f.Exec(t, `UPDATE projects SET archived_at=NULL WHERE id=$1`, f.ProjectID)
	testutil.Request(t, r, f.OwnerID, "PATCH", base+"/issues/"+id.String(), map[string]any{"version": 1, "name": "Restored edit"}, 200)
}

func TestRequirementsRolloutGatePreservesBaseWorkAndMetadataStream(t *testing.T) {
	f := testutil.New(t)
	r := f.Router(requirements.Register, workitems.Register, func(g *gin.RouterGroup, deps platform.Dependencies) {
		foundation.New(deps, foundation.Config{}).Register(g)
	})
	base := f.Prefix()
	call := func(method, path string, body any, status int) map[string]any {
		return testutil.Request(t, r, f.OwnerID, method, base+path, body, status)
	}
	basic := testutil.Object(t, call("POST", "/issues", map[string]any{"name": "Basic work", "assignee_ids": []uuid.UUID{f.MemberID}}, 201))
	story := testutil.Object(t, call("POST", "/issues", map[string]any{"name": "Existing Story", "requirement_type": "story"}, 201))
	activity := testutil.Object(t, call("POST", "/requirements/activities", map[string]any{"name": "Existing activity"}, 201))
	initial := testutil.Object(t, call("GET", "/requirements/snapshot", nil, 200))
	cursor := int64(initial["revision"].(float64))
	testutil.Request(t, r, f.MemberID, "PATCH", base, map[string]any{"settings": map[string]any{"requirements_enabled": false}}, 403)
	call("PATCH", "", map[string]any{"settings": map[string]any{"requirements_enabled": "false"}}, 400)
	call("PATCH", "", map[string]any{"settings": map[string]any{"requirements_enabled": false, "existing_preference": "retained"}}, 200)
	for _, path := range []string{"/requirements/snapshot", "/requirements/activities", "/requirements/history"} {
		response := call("GET", path, nil, 403)
		if response["error"].(map[string]any)["code"] != "requirements_disabled" {
			t.Fatalf("Wrong disabled contract: %#v", response)
		}
	}
	call("POST", "/requirements/activities", map[string]any{"name": "Blocked"}, 403)
	call("PATCH", "/requirements/activities/"+activity["id"].(string), map[string]any{"version": 1, "name": "Blocked"}, 403)
	call("DELETE", "/requirements/activities/"+activity["id"].(string)+"?version=1", nil, 403)
	call("POST", "/planning/changesets", map[string]any{"idempotency_key": "disabled", "commands": []map[string]any{{"operation": "create", "fields": map[string]any{"name": "Blocked batch"}}}}, 403)
	call("POST", "/issues", map[string]any{"name": "Blocked Story", "requirement_type": "story"}, 403)
	call("PATCH", "/issues/"+story["id"].(string), map[string]any{"version": 1, "estimated_minutes": 60}, 403)
	call("PATCH", "/issues/"+story["id"].(string), map[string]any{"version": 1, "name": "Base edit on existing Story", "assignee_ids": []uuid.UUID{f.MemberID}}, 200)
	call("POST", "/issues", map[string]any{"name": "New basic work"}, 201)
	copy := testutil.Object(t, call("POST", "/issues/"+basic["id"].(string)+"/duplicate", nil, 201))
	if copy["requirement_type"] != nil || len(copy["assignee_ids"].([]any)) != 1 {
		t.Fatal("Disabled rollout changed base duplication semantics")
	}
	testutil.Request(t, r, f.OutsiderID, "GET", base+"/requirements/snapshot", nil, 404)
	scope, _ := f.Deps.Policy.Project(context.Background(), identity.Actor{UserID: f.OwnerID}, f.WorkspaceID, f.ProjectID, identity.Guest)
	events, current, _, err := requirements.NewService(f.Deps).Replay(context.Background(), scope, cursor, 200)
	if err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, event := range events {
		seen = seen || event.Kind == "authorization.changed"
	}
	if !seen {
		t.Fatal("Feature disable did not invalidate current caches")
	}
	server := httptest.NewServer(r)
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", server.URL+base+"/requirements/events?cursor="+fmt.Sprint(current), nil)
	req.Header.Set("X-Test-Actor", f.OwnerID.String())
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 {
		response.Body.Close()
		t.Fatalf("Disabled project lost metadata stream: %d", response.StatusCode)
	}
	response.Body.Close()
	cancel()
	call("PATCH", "", map[string]any{"settings": map[string]any{"requirements_enabled": nil}}, 200)
	restored := testutil.Object(t, call("GET", "/requirements/snapshot", nil, 200))
	if len(restored["activities"].([]any)) != 1 {
		t.Fatal("Re-enabling lost stored activity")
	}
	project := testutil.Object(t, call("GET", "", nil, 200))
	if project["settings"].(map[string]any)["existing_preference"] != "retained" {
		t.Fatal("Rollout toggle overwrote unrelated settings")
	}
}

func TestRequirementsDisabledWhileTypedWriteWaitsForLock(t *testing.T) {
	for _, surface := range []string{"issue", "changeset"} {
		t.Run(surface, func(t *testing.T) {
			f := testutil.New(t)
			r := f.Router(requirements.Register, workitems.Register)
			id := f.Issue(t, f.ProjectID, f.StateID, f.OwnerID, "Existing", 1)
			tx, err := f.DB.SQL.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if err = workitems.LockProject(context.Background(), tx, f.ProjectID); err != nil {
				t.Fatal(err)
			}
			path, method := f.Prefix()+"/issues/"+id.String(), "PATCH"
			body := `{"version":1,"estimated_minutes":60}`
			if surface == "changeset" {
				path, method = f.Prefix()+"/planning/changesets", "POST"
				body = fmt.Sprintf(`{"idempotency_key":"wait-toggle","commands":[{"operation":"update","id":"%s","version":1,"fields":{"estimated_minutes":60}}]}`, id)
			}
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				request := httptest.NewRequest(method, path, strings.NewReader(body))
				request.Header.Set("X-Test-Actor", f.OwnerID.String())
				request.Header.Set("Content-Type", "application/json")
				response := httptest.NewRecorder()
				r.ServeHTTP(response, request)
				done <- response
			}()
			deadline := time.Now().Add(4 * time.Second)
			waiting := false
			for time.Now().Before(deadline) {
				var count int
				if err = f.DB.SQL.QueryRowContext(context.Background(), `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event='advisory'`).Scan(&count); err != nil {
					t.Fatal(err)
				}
				if count > 0 {
					waiting = true
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if !waiting {
				t.Fatal("Typed write did not wait for project lock")
			}
			f.Exec(t, `UPDATE projects SET settings=settings||'{"requirements_enabled":false}'::jsonb WHERE id=$1`, f.ProjectID)
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			select {
			case response := <-done:
				if response.Code != 403 || !strings.Contains(response.Body.String(), "requirements_disabled") {
					t.Fatalf("Disabled typed write result: %d %s", response.Code, response.Body.String())
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Write did not finish after feature toggle")
			}
			var version int64
			var minutes *int
			if err = f.DB.SQL.QueryRowContext(context.Background(), `SELECT version,estimated_minutes FROM work_items WHERE id=$1`, id).Scan(&version, &minutes); err != nil || version != 1 || minutes != nil {
				t.Fatalf("Disabled typed write committed: %d %v %v", version, minutes, err)
			}
		})
	}
}
