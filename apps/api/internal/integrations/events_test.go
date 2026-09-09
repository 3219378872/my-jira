package integrations

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"my-jira/apps/api/internal/platform/jobs"
	"my-jira/apps/api/internal/support/testutil"
	"my-jira/apps/api/internal/workitems"
)

func eventTask(t *testing.T, topic string, payload any) *asynq.Task {
	t.Helper()
	content, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(jobs.Envelope{EventID: uuid.New(), Payload: content})
	if err != nil {
		t.Fatal(err)
	}
	return asynq.NewTask(topic, raw)
}

func TestWebhookCommentEventsAndScope(t *testing.T) {
	f := testutil.New(t)
	router := f.Router(Register, workitems.Register)
	issue := f.Issue(t, f.ProjectID, f.StateID, f.OwnerID, "Webhook discussion", 1)
	for _, events := range []string{`["*"]`, `["comment.changed"]`} {
		f.Exec(t, `INSERT INTO webhooks(id,workspace_id,created_by,url,secret,events)VALUES($1,$2,$3,'https://example.test/hooks','test-secret',$4::jsonb)`, uuid.New(), f.WorkspaceID, f.OwnerID, events)
	}
	h := &handler{f.Deps}
	base := f.Prefix() + "/issues/" + issue.String() + "/comments"
	comment := testutil.Object(t, testutil.Request(t, router, f.OwnerID, "POST", base, map[string]any{"body_html": "<p>Original</p>"}, 201))
	for index, action := range []string{"created", "updated", "deleted"} {
		if index == 1 {
			testutil.Request(t, router, f.OwnerID, "PATCH", base+"/"+comment["id"].(string), map[string]any{"body_html": "<p>Revised</p>"}, 200)
		}
		if index == 2 {
			testutil.Request(t, router, f.OwnerID, "DELETE", base+"/"+comment["id"].(string), nil, 204)
		}
		var payload json.RawMessage
		if err := f.DB.SQL.QueryRow(`SELECT payload FROM outbox_events WHERE topic='work_item.changed' ORDER BY created_at DESC LIMIT 1`).Scan(&payload); err != nil {
			t.Fatal(err)
		}
		for repeat := 0; repeat < 2; repeat++ {
			if err := h.changedJob(context.Background(), eventTask(t, "work_item.changed", payload)); err != nil {
				t.Fatal(err)
			}
		}
		var count int
		if err := f.DB.SQL.QueryRow(`SELECT count(*) FROM outbox_events WHERE topic='webhook.deliver' AND payload->>'event'='comment.changed' AND payload->>'action'=$1 AND payload->'data'->>'id'=$2 AND jsonb_typeof(payload->'data')='object'`, action, comment["id"]).Scan(&count); err != nil || count != 2 {
			t.Fatalf("comment %s event DTO/dedup: count=%d err=%v", action, count, err)
		}
	}
	var deliveries int
	if err := f.DB.SQL.QueryRow(`SELECT count(*) FROM outbox_events WHERE topic='webhook.deliver'`).Scan(&deliveries); err != nil || deliveries != 9 {
		t.Fatalf("generic and comment event subscriptions collided: %d %v", deliveries, err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	defer server.Close()
	f.Exec(t, `UPDATE webhooks SET url=$1`, server.URL)
	rows, err := f.DB.SQL.Query(`SELECT payload FROM outbox_events WHERE topic='webhook.deliver'`)
	if err != nil {
		t.Fatal(err)
	}
	var payloads []json.RawMessage
	for rows.Next() {
		var payload json.RawMessage
		if err := rows.Scan(&payload); err != nil {
			t.Fatal(err)
		}
		payloads = append(payloads, payload)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	for _, payload := range payloads {
		if err := h.sendWebhook(context.Background(), eventTask(t, "webhook.deliver", payload), server.Client(), func(_ context.Context, target string) error {
			if target != server.URL {
				return fmt.Errorf("unexpected test destination")
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.DB.SQL.QueryRow(`SELECT count(*) FROM webhook_deliveries WHERE delivered_at IS NOT NULL`).Scan(&deliveries); err != nil || deliveries != 9 {
		t.Fatalf("event identities collided at persistence: %d %v", deliveries, err)
	}
	event := entityEvent{WorkspaceID: f.WorkspaceID, ProjectID: f.ProjectID, Event: "project.changed", Action: "deleted"}
	f.Exec(t, `UPDATE projects SET deleted_at=now() WHERE id=$1`, f.ProjectID)
	if !h.canDeliver(context.Background(), f.OwnerID, event) {
		t.Fatal("active admin lost deletion event")
	}
	f.Exec(t, `UPDATE project_members SET is_active=false WHERE project_id=$1 AND user_id=$2`, f.ProjectID, f.OwnerID)
	if h.canDeliver(context.Background(), f.OwnerID, event) {
		t.Fatal("revoked private-project membership received deletion event")
	}
}

func TestWebhookRetryRotationSignatureAndRevocation(t *testing.T) {
	f := testutil.New(t)
	h := &handler{f.Deps}
	router := f.Router(Register)
	hook, eventID := uuid.New(), uuid.New()
	signingSecret := "initial-secret"
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		raw, _ := io.ReadAll(r.Body)
		mac := hmac.New(sha256.New, []byte(signingSecret))
		_, _ = mac.Write(raw)
		if r.Header.Get("X-MyJira-Signature") != "sha256="+hex.EncodeToString(mac.Sum(nil)) {
			t.Error("signature did not use the current secret")
		}
		if r.Header.Get("Idempotency-Key") != eventID.String()+":module.changed" {
			t.Error("event idempotency key missing")
		}
		var data map[string]any
		if err := json.Unmarshal(raw, &data); err != nil || data["action"] != "updated" || data["event"] != "module.changed" {
			t.Errorf("event contract: %s", raw)
		}
		if calls == 1 {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(204)
	}))
	defer server.Close()
	f.Exec(t, `INSERT INTO webhooks(id,workspace_id,created_by,url,secret,events)VALUES($1,$2,$3,$4,$5,'["module.changed"]')`, hook, f.WorkspaceID, f.OwnerID, server.URL, signingSecret)
	task := eventTask(t, "webhook.deliver", map[string]any{"webhook_id": hook, "event_id": eventID, "event": "module.changed", "action": "updated", "project_id": f.ProjectID, "data": map[string]any{"id": uuid.New(), "name": "Planning"}})
	validate := func(_ context.Context, target string) error {
		if target != server.URL {
			return fmt.Errorf("unexpected test destination")
		}
		return nil
	}
	if err := h.sendWebhook(context.Background(), task, server.Client(), validate); err == nil {
		t.Fatal("failed delivery did not request retry")
	}
	testutil.Request(t, router, f.MemberID, "POST", f.WorkspacePrefix()+"/webhooks/"+hook.String()+"/rotate-secret", nil, 403)
	rotation := testutil.Object(t, testutil.Request(t, router, f.OwnerID, "POST", f.WorkspacePrefix()+"/webhooks/"+hook.String()+"/rotate-secret", nil, 200))
	signingSecret = rotation["secret"].(string)
	if !strings.HasPrefix(signingSecret, "whsec_") {
		t.Fatal("rotation returned invalid secret")
	}
	for repeat := 0; repeat < 2; repeat++ {
		if err := h.sendWebhook(context.Background(), task, server.Client(), validate); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Fatalf("delivered task repeated HTTP: %d", calls)
	}
	var attempts int
	if err := f.DB.SQL.QueryRow(`SELECT attempts FROM webhook_deliveries WHERE webhook_id=$1 AND event_id=$2 AND delivered_at IS NOT NULL`, hook, eventID).Scan(&attempts); err != nil || attempts != 2 {
		t.Fatalf("retry log: %d %v", attempts, err)
	}
	f.Exec(t, `UPDATE workspace_members SET role=15 WHERE workspace_id=$1 AND user_id=$2`, f.WorkspaceID, f.OwnerID)
	task = eventTask(t, "webhook.deliver", map[string]any{"webhook_id": hook, "event_id": uuid.New(), "event": "module.changed", "action": "updated", "project_id": f.ProjectID, "data": map[string]any{"id": uuid.New()}})
	if err := h.sendWebhook(context.Background(), task, server.Client(), validate); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("queued webhook delivered after administrator access was revoked")
	}
}
