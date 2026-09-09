package foundation

import (
	"testing"

	"github.com/google/uuid"
)

func TestStateGroupChangesVersionItemsAndRollBackTogether(t *testing.T) {
	f := newFixture(t)
	owner := f.client()
	user := owner.account("state-owner@example.test", true)
	w := owner.must("POST", "/workspaces", map[string]any{"name": "States", "slug": "states"}, 201)
	project := owner.must("POST", "/workspaces/"+w["id"].(string)+"/projects", map[string]any{"name": "States", "identifier": "STA"}, 201)
	base := "/workspaces/" + w["id"].(string) + "/projects/" + project["id"].(string)
	state := owner.must("POST", base+"/states", map[string]any{"name": "Review", "group": "started"}, 201)
	for sequence := 1; sequence <= 2; sequence++ {
		if _, err := f.db.SQL.Exec(`INSERT INTO work_items(id,workspace_id,project_id,state_id,created_by,updated_by,name,sequence_id) VALUES($1,$2,$3,$4,$5,$5,'Affected item',$6)`, uuid.New(), w["id"], project["id"], state["id"], user["id"], sequence); err != nil {
			t.Fatal(err)
		}
	}
	path := base + "/states/" + state["id"].(string)
	check := func(version, eventCount int, complete bool) {
		t.Helper()
		var count int
		if err := f.db.SQL.QueryRow(`SELECT count(*) FROM work_items WHERE state_id=$1 AND version=$2 AND (completed_at IS NOT NULL)=$3 AND updated_by=$4`, state["id"], version, complete, user["id"]).Scan(&count); err != nil || count != 2 {
			t.Fatalf("affected work-item state: %d, %v", count, err)
		}
		for _, query := range []string{`SELECT count(*) FROM activities WHERE action='state_group_changed'`, `SELECT count(*) FROM work_item_versions`, `SELECT count(*) FROM outbox_events WHERE topic='work_item.changed' AND payload->>'action'='state_group_changed'`} {
			if err := f.db.SQL.QueryRow(query).Scan(&count); err != nil || count != eventCount {
				t.Fatalf("history/outbox count %d, want %d: %v", count, eventCount, err)
			}
		}
	}
	owner.must("PATCH", path, map[string]any{"group": "completed"}, 200)
	check(2, 2, true)
	owner.must("PATCH", path, map[string]any{"group": "completed", "color": "#334455"}, 200)
	check(2, 2, true)
	owner.must("PATCH", path, map[string]any{"group": "started"}, 200)
	check(3, 4, false)
	if _, err := f.db.SQL.Exec(`CREATE FUNCTION reject_state_event() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.topic='work_item.changed' THEN RAISE EXCEPTION 'isolated state propagation rollback'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_state_event BEFORE INSERT ON outbox_events FOR EACH ROW EXECUTE FUNCTION reject_state_event()`); err != nil {
		t.Fatal(err)
	}
	owner.must("PATCH", path, map[string]any{"group": "completed"}, 500)
	check(3, 4, false)
	var group string
	if err := f.db.SQL.QueryRow(`SELECT group_name FROM states WHERE id=$1`, state["id"]).Scan(&group); err != nil || group != "started" {
		t.Fatalf("state did not roll back with its items: %q %v", group, err)
	}
}

func TestBulkLabelsAreAtomicAndWorkspaceListsRespectProjectVisibility(t *testing.T) {
	f := newFixture(t)
	owner, member := f.client(), f.client()
	owner.account("label-owner@example.test", true)
	memberUser := member.account("label-member@example.test", false)
	w := owner.must("POST", "/workspaces", map[string]any{"name": "Labels", "slug": "bulk-labels"}, 201)
	base := "/workspaces/" + w["id"].(string)
	owner.must("POST", base+"/members", map[string]any{"email": "label-member@example.test", "role": 15}, 201)
	labels := func(client *testClient) []any {
		t.Helper()
		status, body := client.request("GET", base+"/labels", nil)
		if status != 200 {
			t.Fatal(body)
		}
		return body["data"].([]any)
	}
	bulk := base + "/labels/bulk"
	member.must("POST", bulk, map[string]any{"labels": []any{map[string]any{"name": "Global one"}, map[string]any{"name": "Global two"}}}, 201)
	if len(labels(member)) != 2 {
		t.Fatal("batch did not create both labels")
	}
	member.must("POST", bulk, map[string]any{"labels": []any{map[string]any{"name": "Must roll back"}, map[string]any{"name": "Global one"}}}, 409)
	member.must("POST", bulk, map[string]any{"labels": []any{}}, 400)
	if len(labels(member)) != 2 {
		t.Fatal("failed batch left a partial label")
	}
	project := owner.must("POST", base+"/projects", map[string]any{"name": "Private labels", "identifier": "LAB", "network": "private"}, 201)
	projectPath := base + "/projects/" + project["id"].(string)
	owner.must("POST", projectPath+"/labels/bulk", map[string]any{"labels": []any{map[string]any{"name": "Private one"}, map[string]any{"name": "Private two"}}}, 201)
	if len(labels(member)) != 2 || len(labels(owner)) != 4 {
		t.Fatal("workspace labels did not apply private-project visibility")
	}
	owner.must("POST", projectPath+"/members", map[string]any{"user_id": memberUser["id"], "role": 15}, 201)
	if len(labels(member)) != 4 {
		t.Fatal("newly accessible project labels were not aggregated")
	}
	globalID := labels(member)[0].(map[string]any)["id"].(string)
	owner.must("POST", projectPath+"/labels/bulk", map[string]any{"labels": []any{map[string]any{"name": "No partial"}, map[string]any{"name": "Wrong parent", "parent_id": globalID}}}, 400)
	if len(labels(owner)) != 4 {
		t.Fatal("invalid parent left a partial project label")
	}
	owner.must("PATCH", projectPath, map[string]any{"archived_at": "now"}, 200)
	if len(labels(member)) != 2 {
		t.Fatal("archived project labels remained in workspace aggregation")
	}
}
