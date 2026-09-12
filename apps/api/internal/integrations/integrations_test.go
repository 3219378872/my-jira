package integrations

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/minio/minio-go/v7"
	"my-jira/apps/api/internal/platform/jobs"
	"my-jira/apps/api/internal/platform/objectstore"
	"my-jira/apps/api/internal/support/testutil"
	"my-jira/apps/api/internal/workitems"
)

func TestPublicSitesKeepInternalCommentsPrivate(t *testing.T) {
	f := testutil.New(t)
	router := f.Router(Register)
	issue := f.Issue(t, f.ProjectID, f.StateID, f.OwnerID, "Published item", 1)
	hidden := f.Issue(t, f.OtherProjectID, f.OtherStateID, f.OwnerID, "Hidden item", 1)
	call := func(actor uuid.UUID, method, path string, body any, want int) map[string]any {
		return testutil.Request(t, router, actor, method, path, body, want)
	}
	call(f.MemberID, "PUT", f.Prefix()+"/site", map[string]any{"slug": "public-board", "title": "Public"}, 403)
	call(f.OwnerID, "PUT", f.Prefix()+"/site", map[string]any{"slug": "public-board", "title": "Public", "is_enabled": true, "comments_enabled": true, "votes_enabled": true, "intake_enabled": true}, 200)
	f.Exec(t, `INSERT INTO comments(id,workspace_id,project_id,work_item_id,author_id,body_html,is_public)VALUES($1,$2,$3,$4,$5,'internal secret',false)`, uuid.New(), f.WorkspaceID, f.ProjectID, issue, f.OwnerID)
	call(f.OutsiderID, "POST", "/api/v1/public/public-board/issues/"+issue.String()+"/comments", map[string]any{"body_html": "<p>Public feedback</p><script>attack()</script>"}, 201)
	public := gin.New()
	RegisterPublic(public.Group("/api/v1"), f.Deps)
	get := func(path string, want int) map[string]any {
		rec := httptest.NewRecorder()
		public.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != want {
			t.Fatalf("public %s: %d %s", path, rec.Code, rec.Body.String())
		}
		out := map[string]any{}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out
	}
	comments := testutil.Array(t, get("/api/v1/public/public-board/issues/"+issue.String()+"/comments", 200))
	if len(comments) != 1 || comments[0].(map[string]any)["body_html"] != "<p>Public feedback</p>" {
		t.Fatalf("internal or unsafe content leaked: %#v", comments)
	}
	get("/api/v1/public/public-board/issues/"+hidden.String(), 404)
	for i := 0; i < 2; i++ {
		vote := testutil.Object(t, call(f.OutsiderID, "POST", "/api/v1/public/public-board/issues/"+issue.String()+"/vote", map[string]any{}, 200))
		if vote["vote_count"] != float64(1) {
			t.Fatal("Repeated vote was not idempotent")
		}
	}
	call(f.OutsiderID, "POST", "/api/v1/public/public-board/intake", map[string]any{"name": "Suggestion", "description_html": "<p>New idea</p>"}, 201)
	if rows := testutil.Array(t, get("/api/v1/public/public-board/issues", 200)); len(rows) != 1 {
		t.Fatal("Unaccepted intake leaked into public list")
	}
	call(f.OwnerID, "PUT", f.Prefix()+"/site", map[string]any{"slug": "public-board", "title": "Public", "is_enabled": false}, 200)
	get("/api/v1/public/public-board", 404)
}

func TestCredentialStorageAndNotificationIdempotency(t *testing.T) {
	f := testutil.New(t)
	router := f.Router(Register)
	issue := f.Issue(t, f.ProjectID, f.StateID, f.OwnerID, "Assigned work", 1)
	response := testutil.Object(t, testutil.Request(t, router, f.MemberID, "POST", f.WorkspacePrefix()+"/api-tokens", map[string]any{"name": "Automation"}, 201))
	token := response["token"].(string)
	var hash string
	if e := f.DB.SQL.QueryRow(`SELECT token_hash FROM api_tokens WHERE id=$1`, response["id"]).Scan(&hash); e != nil {
		t.Fatal(e)
	}
	if hash == token || len(hash) != 64 {
		t.Fatal("Raw API credential was persisted")
	}
	listed := testutil.Array(t, testutil.Request(t, router, f.MemberID, "GET", f.WorkspacePrefix()+"/api-tokens", nil, 200))
	if _, ok := listed[0].(map[string]any)["token_hash"]; ok {
		t.Fatal("Credential hash exposed in list")
	}
	testutil.Request(t, router, f.GuestID, "POST", f.WorkspacePrefix()+"/api-tokens", map[string]any{"name": "Forbidden"}, 403)
	f.Exec(t, `INSERT INTO work_item_assignees(id,workspace_id,project_id,work_item_id,user_id) VALUES($1,$2,$3,$4,$5)`, uuid.New(), f.WorkspaceID, f.ProjectID, issue, f.MemberID)
	payload, _ := json.Marshal(map[string]any{"event_id": uuid.New(), "workspace_id": f.WorkspaceID, "project_id": f.ProjectID, "work_item_id": issue, "actor_id": f.OwnerID, "action": "updated"})
	raw, _ := json.Marshal(jobs.Envelope{EventID: uuid.New(), Payload: payload})
	task := asynq.NewTask("work_item.changed", raw)
	h := &handler{f.Deps}
	for i := 0; i < 2; i++ {
		if e := h.changedJob(context.Background(), task); e != nil {
			t.Fatal(e)
		}
	}
	var count int
	if e := f.DB.SQL.QueryRow(`SELECT count(*) FROM notifications WHERE user_id=$1`, f.MemberID).Scan(&count); e != nil || count != 1 {
		t.Fatalf("Repeated delivery duplicated notification: %d %v", count, e)
	}
	f.Exec(t, `UPDATE project_members SET is_active=false WHERE project_id=$1 AND user_id=$2`, f.ProjectID, f.MemberID)
	raw, _ = json.Marshal(jobs.Envelope{EventID: uuid.New(), Payload: payload})
	if e := h.changedJob(context.Background(), asynq.NewTask("work_item.changed", raw)); e != nil {
		t.Fatal(e)
	}
	if e := f.DB.SQL.QueryRow(`SELECT count(*) FROM notifications WHERE user_id=$1`, f.MemberID).Scan(&count); e != nil || count != 1 {
		t.Fatal("Notification was sent after access revocation")
	}
}

func TestMentionPreferencesAndPublicReactions(t *testing.T) {
	f := testutil.New(t)
	router := f.Router(Register, RegisterPublic, workitems.Register)
	mention := map[string]any{"type": "mention", "attrs": map[string]any{"id": f.MemberID, "label": "Teammate"}}
	created := testutil.Object(t, testutil.Request(t, router, f.OwnerID, "POST", f.Prefix()+"/issues", map[string]any{"name": "Mentioned work", "description_json": map[string]any{"type": "doc", "content": []any{map[string]any{"type": "paragraph", "content": []any{mention}}}}}, 201))
	id := created["id"].(string)
	var event uuid.UUID
	var payload []byte
	if e := f.DB.SQL.QueryRow(`SELECT id,payload FROM outbox_events WHERE topic='work_item.changed' AND payload->>'work_item_id'=$1 ORDER BY created_at DESC LIMIT 1`, id).Scan(&event, &payload); e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(jobs.Envelope{EventID: event, Payload: payload})
	h := &handler{f.Deps}
	if e := h.changedJob(context.Background(), asynq.NewTask("work_item.changed", raw)); e != nil {
		t.Fatal(e)
	}
	var count int
	if e := f.DB.SQL.QueryRow(`SELECT count(*) FROM notifications WHERE user_id=$1 AND data->'reasons' ? 'mentions'`, f.MemberID).Scan(&count); e != nil || count != 1 {
		t.Fatalf("mention notification missing: %d %v", count, e)
	}
	if e := f.DB.SQL.QueryRow(`SELECT count(*) FROM outbox_events WHERE topic='email.send' AND deduplication_key LIKE 'notification-email:%'`).Scan(&count); e != nil || count != 1 {
		t.Fatalf("notification email outbox missing: %d %v", count, e)
	}
	f.Exec(t, `INSERT INTO preferences(id,workspace_id,user_id,scope,value)VALUES($1,$2,$3,'notifications','{"mentions":false}')`, uuid.New(), f.WorkspaceID, f.MemberID)
	raw, _ = json.Marshal(jobs.Envelope{EventID: uuid.New(), Payload: payload})
	if e := h.changedJob(context.Background(), asynq.NewTask("work_item.changed", raw)); e != nil {
		t.Fatal(e)
	}
	if e := f.DB.SQL.QueryRow(`SELECT count(*) FROM notifications WHERE user_id=$1`, f.MemberID).Scan(&count); e != nil || count != 1 {
		t.Fatal("mention opt-out was ignored")
	}
	testutil.Request(t, router, f.OwnerID, "PUT", f.Prefix()+"/site", map[string]any{"slug": "reactions-board", "title": "Feedback", "is_enabled": true, "reactions_enabled": true, "comments_enabled": true, "votes_enabled": true}, 200)
	base := "/api/v1/public/reactions-board/issues/" + id
	for i := 0; i < 2; i++ {
		reactions := testutil.Array(t, testutil.Request(t, router, f.OutsiderID, "POST", base+"/reactions", map[string]any{"emoji": "👍"}, 200))
		if reactions[0].(map[string]any)["count"] != float64(1) || reactions[0].(map[string]any)["reacted"] != true {
			t.Fatal("public reaction was not idempotent")
		}
	}
	testutil.Request(t, router, f.OutsiderID, "POST", base+"/vote", map[string]any{}, 200)
	if testutil.Object(t, testutil.Request(t, router, f.OutsiderID, "GET", base, nil, 200))["voted"] != true {
		t.Fatal("persisted vote not reflected for current actor")
	}
	if testutil.Object(t, testutil.Request(t, router, f.OwnerID, "GET", base, nil, 200))["voted"] != false {
		t.Fatal("other actor vote status leaked")
	}
	comment := testutil.Object(t, testutil.Request(t, router, f.OutsiderID, "POST", base+"/comments", map[string]any{"body_html": "<p>Public comment</p>"}, 201))
	testutil.Request(t, router, f.MemberID, "PATCH", base+"/comments/"+comment["id"].(string), map[string]any{"body_html": "other edit"}, 404)
	testutil.Request(t, router, f.OutsiderID, "PATCH", base+"/comments/"+comment["id"].(string), map[string]any{"body_html": "<p>Edited public comment</p>"}, 200)
	testutil.Request(t, router, f.OutsiderID, "DELETE", base+"/reactions", map[string]any{"emoji": "👍"}, 200)
	if len(testutil.Array(t, testutil.Request(t, router, f.OutsiderID, "GET", base+"/reactions", nil, 200))) != 0 {
		t.Fatal("reaction removal failed")
	}
}

func TestExportFilterAndRevocation(t *testing.T) {
	testutil.ObjectStoreEnvironment(t)
	f := testutil.New(t)
	router := f.Router(Register)
	f.Issue(t, f.ProjectID, f.StateID, f.OwnerID, "Include this", 1)
	f.Issue(t, f.ProjectID, f.StateID, f.OwnerID, "Exclude this", 2)
	f.Issue(t, f.OtherProjectID, f.OtherStateID, f.OwnerID, "Private secret", 1)
	response := testutil.Object(t, testutil.Request(t, router, f.MemberID, "POST", f.WorkspacePrefix()+"/exports", map[string]any{"format": "json", "filters": map[string]any{"search": "Include"}}, 202))
	id := response["id"].(string)
	payload, _ := json.Marshal(map[string]any{"export_id": id})
	raw, _ := json.Marshal(jobs.Envelope{EventID: uuid.New(), Payload: payload})
	if e := (&handler{f.Deps}).exportJob(context.Background(), asynq.NewTask("export.generate", raw)); e != nil {
		t.Fatal(e)
	}
	store, bucket, e := objectstore.Load(context.Background(), f.DB.SQL)
	if e != nil {
		t.Fatal(e)
	}
	var key string
	if e = f.DB.SQL.QueryRow(`SELECT object_key FROM exports WHERE id=$1`, id).Scan(&key); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		_ = store.RemoveObject(context.Background(), bucket, key, minio.RemoveObjectOptions{})
		_ = store.RemoveBucket(context.Background(), bucket)
	})
	object, e := store.GetObject(context.Background(), bucket, key, minio.GetObjectOptions{})
	if e != nil {
		t.Fatal(e)
	}
	data, e := io.ReadAll(object)
	_ = object.Close()
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Contains(data, []byte("Include this")) || bytes.Contains(data, []byte("Exclude this")) || bytes.Contains(data, []byte("Private secret")) {
		t.Fatalf("Export filtering failed: %s", data)
	}
	f.Exec(t, `UPDATE project_members SET is_active=false WHERE project_id=$1 AND user_id=$2`, f.ProjectID, f.MemberID)
	testutil.Request(t, router, f.MemberID, "GET", f.WorkspacePrefix()+"/exports/"+id+"/download", nil, 403)
}

func TestWebhookAddressAndSpreadsheetBoundaries(t *testing.T) {
	for _, value := range []string{"127.0.0.1", "::1", "10.0.0.1", "192.168.1.5", "169.254.169.254", "100.100.100.100", "::ffff:127.0.0.1", "0.0.0.0", "224.0.0.1"} {
		if publicIP(net.ParseIP(value)) {
			t.Fatalf("Accepted internal destination %s", value)
		}
	}
	if !publicIP(net.ParseIP("8.8.8.8")) {
		t.Fatal("Public destination rejected")
	}
	x, e := xlsx([][]string{{"标题", "Content"}, {"=HYPERLINK(\"bad\")", "<&>中文"}})
	if e != nil {
		t.Fatal(e)
	}
	archive, e := zip.NewReader(bytes.NewReader(x), int64(len(x)))
	if e != nil {
		t.Fatal(e)
	}
	for _, file := range archive.File {
		if file.Name == "xl/worksheets/sheet1.xml" {
			r, e := file.Open()
			if e != nil {
				t.Fatal(e)
			}
			content, _ := io.ReadAll(r)
			_ = r.Close()
			if strings.Contains(string(content), "<f>") || !strings.Contains(string(content), "&lt;&amp;&gt;中文") {
				t.Fatalf("Spreadsheet formula/text boundary failed: %s", content)
			}
		}
	}
}
