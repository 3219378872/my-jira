package foundation

import (
	"encoding/json"
	"testing"
)

func TestProjectEventsAreScopedAndTransactional(t *testing.T) {
	f := newFixture(t)
	owner := f.client()
	owner.account("owner@example.test", true)
	w := owner.must("POST", "/workspaces", map[string]any{"name": "Events", "slug": "events"}, 201)
	base := "/workspaces/" + w["id"].(string) + "/projects"
	p := owner.must("POST", base, map[string]any{"name": "Created", "identifier": "EVT"}, 201)
	path := base + "/" + p["id"].(string)
	owner.must("PATCH", path, map[string]any{"name": "Updated", "settings": map[string]any{"private_display_preference": "must not be in webhook DTO"}}, 200)
	var raw []byte
	if err := f.db.SQL.QueryRow(`SELECT payload FROM outbox_events WHERE topic='entity.changed' AND payload->>'action'='updated'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var event map[string]any
	if err := json.Unmarshal(raw, &event); err != nil {
		t.Fatal(err)
	}
	data := event["data"].(map[string]any)
	if event["workspace_id"] != w["id"] || event["project_id"] != p["id"] || event["entity_id"] != p["id"] || event["event"] != "project.changed" || data["name"] != "Updated" || data["settings"] != nil || data["role"] != nil {
		t.Fatalf("project event scope or public projection: %v", event)
	}
	if _, err := f.db.SQL.Exec(`CREATE FUNCTION reject_project_event() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.topic='entity.changed' THEN RAISE EXCEPTION 'isolated fixture rejects project event'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_project_event BEFORE INSERT ON outbox_events FOR EACH ROW EXECUTE FUNCTION reject_project_event()`); err != nil {
		t.Fatal(err)
	}
	owner.must("PATCH", path, map[string]any{"name": "Must roll back"}, 500)
	if owner.must("GET", path, nil, 200)["name"] != "Updated" {
		t.Fatal("project changed after its event failed to persist")
	}
	if _, err := f.db.SQL.Exec(`DROP TRIGGER reject_project_event ON outbox_events`); err != nil {
		t.Fatal(err)
	}
	owner.must("DELETE", path, nil, 204)
	var count int
	if err := f.db.SQL.QueryRow(`SELECT count(*) FROM outbox_events WHERE topic='entity.changed' AND payload->>'event'='project.changed'`).Scan(&count); err != nil || count != 3 {
		t.Fatalf("project event count=%d error=%v", count, err)
	}
}
