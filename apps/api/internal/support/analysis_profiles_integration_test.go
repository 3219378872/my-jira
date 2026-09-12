package support_test

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/minio/minio-go/v7"
	"my-jira/apps/api/internal/documents"
	"my-jira/apps/api/internal/integrations"
	"my-jira/apps/api/internal/planning"
	"my-jira/apps/api/internal/platform/jobs"
	"my-jira/apps/api/internal/platform/objectstore"
	"my-jira/apps/api/internal/support"
	"my-jira/apps/api/internal/support/testutil"
)

func TestAdvancedAnalysesProfilesAndSavedQueries(t *testing.T) {
	f := testutil.New(t)
	r := f.Router(support.Register)
	w := f.WorkspacePrefix()
	call := func(actor uuid.UUID, method, path string, body any, status int) map[string]any {
		return testutil.Request(t, r, actor, method, path, body, status)
	}
	first := f.Issue(t, f.ProjectID, f.StateID, f.OwnerID, "=Visible assigned", 1)
	second := f.Issue(t, f.ProjectID, f.StateID, f.MemberID, "Other visible", 2)
	secret := f.Issue(t, f.OtherProjectID, f.OtherStateID, f.OwnerID, "Private", 1)
	f.Exec(t, "UPDATE work_items SET estimate=3,priority='high',created_at='2026-09-05' WHERE id=$1", first)
	f.Exec(t, "UPDATE work_items SET estimate=8,priority='low',created_at='2026-08-05' WHERE id=$1", second)
	for _, id := range []uuid.UUID{first, secret} {
		pid := f.ProjectID
		if id == secret {
			pid = f.OtherProjectID
		}
		f.Exec(t, "INSERT INTO work_item_assignees(id,workspace_id,project_id,work_item_id,user_id) VALUES($1,$2,$3,$4,$5)", uuid.New(), f.WorkspaceID, pid, id, f.MemberID)
		f.Exec(t, "INSERT INTO activities(id,workspace_id,project_id,work_item_id,actor_id,action,old_value,new_value) VALUES($1,$2,$3,$4,$5,'updated','{}','{}')", uuid.New(), f.WorkspaceID, pid, id, f.MemberID)
	}
	f.Exec(t, "INSERT INTO activities(id,workspace_id,project_id,work_item_id,actor_id,action) VALUES($1,$2,$3,$4,$5,'commented')", uuid.New(), f.WorkspaceID, f.ProjectID, first, f.MemberID)
	f.Exec(t, "INSERT INTO work_item_subscribers(id,workspace_id,project_id,work_item_id,user_id) VALUES($1,$2,$3,$4,$5)", uuid.New(), f.WorkspaceID, f.ProjectID, first, f.MemberID)
	filter := url.QueryEscape(`{"or":[{"field":"priority","value":"high"},{"field":"priority","value":"urgent"}]}`)
	analysis := testutil.Object(t, call(f.MemberID, "GET", w+"/analytics?x_axis=assignee&segment=priority&metric=estimate&from=2026-09-01&to=2026-09-09&filter="+filter, nil, 200))
	if analysis["total"] != float64(1) || analysis["estimate"] != float64(3) {
		t.Fatalf("date/filter/permission scope failed: %#v", analysis)
	}
	points := analysis["distribution"].([]any)
	if len(points) != 1 || points[0].(map[string]any)["key"] != f.MemberID.String() || points[0].(map[string]any)["value"] != float64(3) {
		t.Fatalf("dimension/segment failed: %#v", points)
	}
	call(f.MemberID, "GET", w+"/analytics?filter="+url.QueryEscape(`{"field":"bogus","value":1}`), nil, 400)
	call(f.MemberID, "GET", w+"/analytics?x_axis=priority&segment=priority", nil, 400)
	call(f.MemberID, "GET", w+"/analytics?from=2026-10-01&to=2026-09-01", nil, 400)
	savedBody := map[string]any{"name": "Priority review", "query": map[string]string{"x_axis": "project", "priority": "high"}}
	call(f.MemberID, "POST", w+"/analyses", savedBody, 403)
	saved := testutil.Object(t, call(f.OwnerID, "POST", w+"/analyses", savedBody, 201))
	savedID := saved["id"].(string)
	if run := testutil.Object(t, call(f.MemberID, "GET", w+"/analyses/"+savedID+"/run", nil, 200)); run["total"] != float64(1) {
		t.Fatal("saved query did not use caller visibility")
	}
	call(f.OwnerID, "PATCH", w+"/analyses/"+savedID, map[string]any{"query": map[string]string{"metric": "bogus"}}, 400)
	profile := testutil.Object(t, call(f.MemberID, "GET", w+"/profiles/me", nil, 200))
	stats := profile["stats"].(map[string]any)
	if stats["created"] != float64(1) || stats["assigned"] != float64(1) || stats["pending"] != float64(1) || stats["subscribed"] != float64(1) {
		t.Fatalf("profile stats leaked or lost data: %#v", stats)
	}
	if _, ok := profile["user"].(map[string]any)["password_hash"]; ok {
		t.Fatal("profile exposed account secret")
	}
	call(f.MemberID, "GET", w+"/profiles/"+f.OutsiderID.String(), nil, 404)
	activities := testutil.Object(t, call(f.MemberID, "GET", w+"/profiles/me/activities", nil, 200))
	if activities["total"] != float64(1) {
		t.Fatalf("profile activities included hidden/comment events: %#v", activities)
	}
	call(f.MemberID, "GET", w+"/profiles/me/activities/export", nil, 400)
	req := httptest.NewRequest("GET", w+"/profiles/me/activities/export?from=2020-01-01&to=2100-01-01", nil)
	req.Header.Set("X-Test-Actor", f.MemberID.String())
	response := httptest.NewRecorder()
	r.ServeHTTP(response, req)
	if response.Code != 200 {
		t.Fatalf("activity CSV failed: %s", response.Body.String())
	}
	records, err := csv.NewReader(response.Body).ReadAll()
	if err != nil || len(records) != 2 || records[1][2] != "'=Visible assigned" {
		t.Fatalf("CSV data/formula protection failed: %#v %v", records, err)
	}
	f.Exec(t, "UPDATE project_members SET is_active=false WHERE project_id=$1 AND user_id=$2", f.ProjectID, f.MemberID)
	if run := testutil.Object(t, call(f.MemberID, "GET", w+"/analyses/"+savedID+"/run", nil, 200)); run["total"] != float64(0) {
		t.Fatal("saved analysis retained revoked project data")
	}
	if stats := testutil.Object(t, call(f.MemberID, "GET", w+"/profiles/me/stats", nil, 200)); stats["assigned"] != float64(0) {
		t.Fatal("profile retained revoked project data")
	}
}

func TestModuleLinksAndPageResources(t *testing.T) {
	f := testutil.New(t)
	r := f.Router(planning.Register, documents.Register)
	p := f.Prefix()
	call := func(actor uuid.UUID, method, path string, body any, status int) map[string]any {
		return testutil.Request(t, r, actor, method, path, body, status)
	}
	module := testutil.Object(t, call(f.OwnerID, "POST", p+"/modules", map[string]any{"name": "Resources"}, 201))
	base := p + "/modules/" + module["id"].(string) + "/links"
	call(f.GuestID, "POST", base, map[string]any{"url": "https://example.test"}, 403)
	call(f.MemberID, "POST", base, map[string]any{"url": "javascript:bad()"}, 400)
	link := testutil.Object(t, call(f.MemberID, "POST", base, map[string]any{"title": "Design", "url": "https://example.test/design"}, 201))
	call(f.OwnerID, "PATCH", base+"/"+link["id"].(string), map[string]any{"title": "Updated design"}, 200)
	if list := testutil.Array(t, call(f.GuestID, "GET", base, nil, 200)); len(list) != 1 || list[0].(map[string]any)["title"] != "Updated design" {
		t.Fatal("module links did not persist")
	}
	wrong := strings.Replace(base, module["id"].(string), uuid.NewString(), 1)
	call(f.MemberID, "DELETE", wrong+"/"+link["id"].(string), nil, 404)
	call(f.MemberID, "DELETE", base+"/"+link["id"].(string), nil, 204)
	body := map[string]any{"name": "Linked page", "content_html": `<h2>Overview</h2><p><a href="https://example.test/spec">Specification</a><img src="https://example.test/image.png" alt="Diagram"></p>`, "content_json": map[string]any{"type": "doc", "content": []any{map[string]any{"type": "heading", "attrs": map[string]any{"level": 2}, "content": []any{map[string]any{"type": "text", "text": "Overview"}}}}}}
	page := testutil.Object(t, call(f.OwnerID, "POST", p+"/pages", body, 201))
	pageID := page["id"].(string)
	asset := uuid.New()
	f.Exec(t, "INSERT INTO file_assets(id,workspace_id,project_id,page_id,uploaded_by,filename,content_type,size_bytes,object_key,upload_status) VALUES($1,$2,$3,$4,$5,'diagram.png','image/png',32,'private-key','completed')", asset, f.WorkspaceID, f.ProjectID, pageID, f.OwnerID)
	resources := testutil.Object(t, call(f.MemberID, "GET", p+"/pages/"+pageID+"/resources", nil, 200))
	if len(resources["links"].([]any)) != 2 || len(resources["headings"].([]any)) != 1 || len(resources["assets"].([]any)) != 1 {
		t.Fatalf("page resources incomplete: %#v", resources)
	}
	if _, ok := resources["assets"].([]any)[0].(map[string]any)["object_key"]; ok {
		t.Fatal("resources exposed object key")
	}
	call(f.OwnerID, "POST", p+"/pages", map[string]any{"name": "Private root", "is_private": true}, 201)
	call(f.OwnerID, "POST", p+"/pages", map[string]any{"name": "Child", "parent_id": pageID}, 201)
	if summary := testutil.Object(t, call(f.MemberID, "GET", p+"/pages/summary", nil, 200)); summary["total"] != float64(1) {
		t.Fatalf("root summary leaked private/child pages: %#v", summary)
	}
	call(f.OwnerID, "PATCH", p+"/pages/"+pageID, map[string]any{"is_private": true}, 200)
	call(f.MemberID, "GET", p+"/pages/"+pageID+"/resources", nil, 404)
}

func TestAnalysisReportPersistsAndRechecksDownload(t *testing.T) {
	testutil.ObjectStoreEnvironment(t)
	f := testutil.New(t)
	r := f.Router(support.Register, integrations.Register)
	f.Issue(t, f.ProjectID, f.StateID, f.OwnerID, "Visible", 1)
	f.Issue(t, f.OtherProjectID, f.OtherStateID, f.OwnerID, "Secret", 1)
	f.Exec(t, "UPDATE projects SET name='=Visible project' WHERE id=$1", f.ProjectID)
	created := testutil.Object(t, testutil.Request(t, r, f.MemberID, "POST", f.WorkspacePrefix()+"/analytics/export", map[string]any{"query": map[string]string{"x_axis": "project"}}, 202))
	id := created["id"].(string)
	mux := asynq.NewServeMux()
	support.RegisterJobs(mux, f.Deps)
	payload, _ := json.Marshal(map[string]any{"export_id": id})
	envelope, _ := json.Marshal(jobs.Envelope{EventID: uuid.New(), Payload: payload})
	task := asynq.NewTask("report.generate", envelope)
	completed := make(chan error, 2)
	for range 2 {
		go func() { completed <- mux.ProcessTask(context.Background(), task) }()
	}
	for range 2 {
		if err := <-completed; err != nil {
			t.Fatal(err)
		}
	}
	store, bucket, err := objectstore.Load(context.Background(), f.DB.SQL)
	if err != nil {
		t.Fatal(err)
	}
	var key, status string
	var scopeJSON []byte
	if err = f.DB.SQL.QueryRow("SELECT object_key,status,filters->'_scope_project_ids' FROM exports WHERE id=$1", id).Scan(&key, &status, &scopeJSON); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.RemoveObject(context.Background(), bucket, key, minio.RemoveObjectOptions{})
		_ = store.RemoveBucket(context.Background(), bucket)
	})
	if status != "completed" || !bytes.Contains(scopeJSON, []byte(f.ProjectID.String())) || bytes.Contains(scopeJSON, []byte(f.OtherProjectID.String())) {
		t.Fatalf("report status/scope incorrect: %s %s", status, scopeJSON)
	}
	object, err := store.GetObject(context.Background(), bucket, key, minio.GetObjectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	content, err := io.ReadAll(object)
	_ = object.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(content, []byte("'=Visible project")) || bytes.Contains(content, []byte("HIDDEN")) {
		t.Fatalf("report grouping/formula protection failed: %s", content)
	}
	if err = mux.ProcessTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	var mails int
	if err = f.DB.SQL.QueryRow("SELECT count(*) FROM outbox_events WHERE topic='email.report-ready' AND payload->>'export_id'=$1", id).Scan(&mails); err != nil || mails != 1 {
		t.Fatalf("report retry duplicated completion mail: %d %v", mails, err)
	}
	f.Exec(t, "UPDATE project_members SET is_active=false WHERE project_id=$1 AND user_id=$2", f.ProjectID, f.MemberID)
	testutil.Request(t, r, f.MemberID, "GET", created["download_url"].(string), nil, 403)
}
