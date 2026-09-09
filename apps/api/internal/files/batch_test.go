package files

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"my-jira/apps/api/internal/support/testutil"
)

func batchUpload(t *testing.T, router *gin.Engine, actor uuid.UUID, path, text string, fields map[string]string) map[string]any {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("file", "batch-attachment.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(part, text); err != nil {
		t.Fatal(err)
	}
	for key, value := range fields {
		if err := form.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", path, &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	req.Header.Set("X-Test-Actor", actor.String())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != 201 {
		t.Fatalf("batch fixture upload: %d %s", rec.Code, rec.Body)
	}
	var result map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return testutil.Object(t, result)
}

func batchPage(t *testing.T, f *testutil.Fixture, project, owner uuid.UUID, private bool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	f.Exec(t, `INSERT INTO pages(id,workspace_id,project_id,owner_id,name,is_private) VALUES($1,$2,$3,$4,'Batch page',$5)`, id, f.WorkspaceID, nullable(project), owner, private)
	return id
}

func TestAssetBatchMultipleProjectsPagesAndRecovery(t *testing.T) {
	f := storageFixture(t)
	router := f.Router(Register)
	w, p := f.WorkspacePrefix(), f.Prefix()
	other := w + "/projects/" + f.OtherProjectID.String()
	f.Exec(t, `INSERT INTO project_members(id,workspace_id,project_id,user_id,role) VALUES($1,$2,$3,$4,15)`, uuid.New(), f.WorkspaceID, f.OtherProjectID, f.MemberID)
	issue := f.Issue(t, f.ProjectID, f.StateID, f.OwnerID, "Several attachments", 1)
	otherIssue := f.Issue(t, f.OtherProjectID, f.OtherStateID, f.OwnerID, "Second project", 1)
	empty := f.Issue(t, f.ProjectID, f.StateID, f.OwnerID, "No attachments", 2)
	page := batchPage(t, f, f.ProjectID, f.OwnerID, false)
	privatePage := batchPage(t, f, f.OtherProjectID, f.MemberID, true)
	workspacePage := batchPage(t, f, uuid.Nil, f.MemberID, false)
	expectedText := map[string]string{}
	expectedProject := map[string]uuid.UUID{}
	add := func(actor uuid.UUID, path, text string, fields map[string]string, project uuid.UUID) map[string]any {
		t.Helper()
		asset := batchUpload(t, router, actor, path, text, fields)
		id := asset["id"].(string)
		expectedText[id], expectedProject[id] = text, project
		return asset
	}
	add(f.OwnerID, p+"/assets", "first author's bytes", map[string]string{"work_item_id": issue.String()}, f.ProjectID)
	add(f.MemberID, p+"/assets", "second author's bytes", map[string]string{"work_item_id": issue.String()}, f.ProjectID)
	add(f.OwnerID, other+"/assets", "another project's bytes", map[string]string{"work_item_id": otherIssue.String()}, f.OtherProjectID)
	add(f.OwnerID, p+"/assets", "shared page bytes", map[string]string{"page_id": page.String()}, f.ProjectID)
	add(f.MemberID, other+"/assets", "owned private page bytes", map[string]string{"page_id": privatePage.String()}, f.OtherProjectID)
	add(f.MemberID, w+"/assets", "workspace page bytes", map[string]string{"page_id": workspacePage.String()}, uuid.Nil)
	removed := batchUpload(t, router, f.OwnerID, p+"/assets", "recoverable bytes", map[string]string{"work_item_id": issue.String()})
	testutil.Request(t, router, f.OwnerID, "DELETE", p+"/assets/"+removed["id"].(string), nil, 204)
	f.Exec(t, `INSERT INTO file_assets(id,workspace_id,project_id,work_item_id,uploaded_by,filename,content_type,size_bytes,object_key,upload_status) VALUES($1,$2,$3,$4,$5,'not-completed.txt','text/plain',1,$6,'pending')`, uuid.New(), f.WorkspaceID, f.ProjectID, issue, f.OwnerID, "pending/"+uuid.NewString())
	query := url.Values{
		"work_item_ids": {issue.String() + "," + otherIssue.String(), empty.String(), issue.String()},
		"page_ids":      {page.String(), privatePage.String() + "," + workspacePage.String()},
	}
	groups := testutil.Array(t, testutil.Request(t, router, f.MemberID, "GET", w+"/assets/batch?"+query.Encode(), nil, 200))
	wantOrder := []uuid.UUID{issue, otherIssue, empty, page, privatePage, workspacePage}
	wantCounts := []int{2, 1, 0, 1, 1, 1}
	if len(groups) != len(wantOrder) {
		t.Fatalf("batch deduplication/grouping: %#v", groups)
	}
	seen := map[string]bool{}
	for i, raw := range groups {
		group := raw.(map[string]any)
		kind := "work_item"
		if i >= 3 {
			kind = "page"
		}
		assets, ok := group["assets"].([]any)
		if group["entity_type"] != kind || group["entity_id"] != wantOrder[i].String() || !ok || len(assets) != wantCounts[i] {
			t.Fatalf("unexpected entity group %d: %#v", i, group)
		}
		for _, rawAsset := range assets {
			asset := rawAsset.(map[string]any)
			id := asset["id"].(string)
			if _, secret := asset["object_key"]; secret || expectedText[id] == "" || seen[id] || asset["deleted_at"] != nil {
				t.Fatalf("unexpected, repeated or hidden asset: %#v", asset)
			}
			seen[id] = true
			prefix := w
			if project := expectedProject[id]; project != uuid.Nil {
				prefix += "/projects/" + project.String()
			}
			download := prefix + "/assets/" + id + "/download"
			if asset["download_url"] != download {
				t.Fatalf("download URL lost the entity's actual project: %#v", asset)
			}
			req := httptest.NewRequest("GET", download, nil)
			req.Header.Set("X-Test-Actor", f.MemberID.String())
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != 200 || rec.Body.String() != expectedText[id] {
				t.Fatalf("actual object download: %d %s", rec.Code, rec.Body)
			}
		}
	}
	if len(seen) != len(expectedText) {
		t.Fatalf("only %d of %d actual attachments returned", len(seen), len(expectedText))
	}
	projectQuery := "?work_item_ids=" + issue.String() + "," + empty.String() + "&page_ids=" + page.String()
	testutil.Request(t, router, f.MemberID, "GET", p+"/assets/batch"+projectQuery, nil, 200)
	query.Set("deleted", "true")
	deleted := testutil.Array(t, testutil.Request(t, router, f.MemberID, "GET", w+"/assets/batch?"+query.Encode(), nil, 200))
	for i, raw := range deleted {
		assets := raw.(map[string]any)["assets"].([]any)
		if i == 0 {
			if len(assets) != 1 || assets[0].(map[string]any)["id"] != removed["id"] {
				t.Fatalf("recovery batch must contain only the deleted file: %#v", assets)
			}
		} else if len(assets) != 0 {
			t.Fatalf("live files mixed into deleted batch: %#v", assets)
		}
	}
	for _, invalid := range []string{
		p + "/assets/batch?work_item_ids=" + otherIssue.String(),
		p + "/assets/batch?page_ids=" + workspacePage.String(),
		w + "/assets/batch?work_item_ids=" + issue.String() + "," + uuid.NewString(),
	} {
		result := testutil.Request(t, router, f.MemberID, "GET", invalid, nil, 404)
		if result["data"] != nil {
			t.Fatal("failed batch returned a partial asset list")
		}
	}
	f.Exec(t, `UPDATE work_items SET deleted_at=now() WHERE id=$1`, empty)
	testutil.Request(t, router, f.MemberID, "GET", p+"/assets/batch?work_item_ids="+empty.String()+"&deleted=true", nil, 404)
	f.Exec(t, `UPDATE pages SET deleted_at=now() WHERE id=$1`, page)
	testutil.Request(t, router, f.MemberID, "GET", p+"/assets/batch?page_ids="+page.String(), nil, 404)
	testutil.Request(t, router, f.OutsiderID, "GET", w+"/assets/batch?work_item_ids="+issue.String(), nil, 404)
}

func TestAssetBatchGuestVisibilityPrivateOwnersAndRevocation(t *testing.T) {
	f := storageFixture(t)
	router := f.Router(Register)
	w, p := f.WorkspacePrefix(), f.Prefix()
	issue := f.Issue(t, f.ProjectID, f.StateID, f.OwnerID, "Owner item", 1)
	own := f.Issue(t, f.ProjectID, f.StateID, f.GuestID, "Guest item", 2)
	hidden := f.Issue(t, f.OtherProjectID, f.OtherStateID, f.OwnerID, "Hidden project item", 1)
	page := batchPage(t, f, f.ProjectID, f.OwnerID, false)
	privatePage := batchPage(t, f, f.ProjectID, f.OwnerID, true)
	ownPage := batchPage(t, f, f.ProjectID, f.GuestID, true)
	for _, input := range []struct {
		actor uuid.UUID
		field string
		id    uuid.UUID
	}{{f.OwnerID, "work_item_id", issue}, {f.GuestID, "work_item_id", own}, {f.OwnerID, "page_id", page}, {f.OwnerID, "page_id", privatePage}, {f.GuestID, "page_id", ownPage}} {
		batchUpload(t, router, input.actor, p+"/assets", "scoped bytes "+input.id.String(), map[string]string{input.field: input.id.String()})
	}
	read := func(actor uuid.UUID, prefix, query string, want int) []any {
		t.Helper()
		result := testutil.Request(t, router, actor, "GET", prefix+"/assets/batch?"+query, nil, want)
		if want != 200 {
			if result["data"] != nil || result["error"].(map[string]any)["code"] != "not_found" {
				t.Fatalf("failed batch disclosed an entity or partial data: %#v", result)
			}
			return nil
		}
		groups := testutil.Array(t, result)
		for _, group := range groups {
			if len(group.(map[string]any)["assets"].([]any)) != 1 {
				t.Fatalf("authorized entity lost its real attachment: %#v", group)
			}
		}
		return groups
	}
	ownQuery := "work_item_ids=" + own.String() + "&page_ids=" + ownPage.String()
	read(f.GuestID, w, ownQuery, 200)
	mixed := "work_item_ids=" + own.String() + "," + issue.String() + "&page_ids=" + ownPage.String() + "," + page.String()
	read(f.GuestID, w, mixed, 404)
	f.Exec(t, `UPDATE projects SET guest_can_view_all=true WHERE id=$1`, f.ProjectID)
	if groups := read(f.GuestID, w, mixed, 200); len(groups) != 4 {
		t.Fatalf("expanded Guest read returned %d groups", len(groups))
	}
	read(f.GuestID, p, mixed, 200)
	read(f.GuestID, w, mixed+","+privatePage.String(), 404)
	read(f.MemberID, w, "page_ids="+privatePage.String(), 404)
	read(f.OwnerID, w, "page_ids="+ownPage.String(), 404)
	read(f.OwnerID, w, "page_ids="+privatePage.String(), 200)
	f.Exec(t, `UPDATE pages SET is_locked=true,archived_at=now() WHERE id=$1`, ownPage)
	read(f.GuestID, w, ownQuery, 200)
	f.Exec(t, `UPDATE projects SET guest_can_view_all=false WHERE id=$1`, f.ProjectID)
	read(f.GuestID, w, mixed, 404)
	read(f.GuestID, w, ownQuery, 200)
	f.Exec(t, `UPDATE project_members SET role=5 WHERE project_id=$1 AND user_id=$2`, f.ProjectID, f.MemberID)
	read(f.MemberID, w, "work_item_ids="+issue.String(), 404)
	f.Exec(t, `UPDATE projects SET guest_can_view_all=true WHERE id IN($1,$2)`, f.ProjectID, f.OtherProjectID)
	read(f.MemberID, w, "work_item_ids="+issue.String()+"&page_ids="+page.String(), 200)
	read(f.GuestID, w, "work_item_ids="+own.String()+","+hidden.String(), 404)
	read(f.GuestID, w, "work_item_ids="+own.String()+","+uuid.NewString(), 404)
	f.Exec(t, `UPDATE projects SET network='public' WHERE id=$1`, f.ProjectID)
	f.Exec(t, `UPDATE project_members SET is_active=false WHERE project_id=$1 AND user_id=$2`, f.ProjectID, f.GuestID)
	read(f.GuestID, w, "work_item_ids="+own.String()+","+issue.String(), 200)
	read(f.GuestID, w, ownQuery, 404)
	f.Exec(t, `UPDATE workspace_members SET is_active=false WHERE workspace_id=$1 AND user_id=$2`, f.WorkspaceID, f.GuestID)
	read(f.GuestID, w, "work_item_ids="+own.String(), 404)
}

func TestAssetBatchQueryLimitsAndValidation(t *testing.T) {
	id, second := uuid.NewString(), uuid.NewString()
	for _, test := range []struct {
		name  string
		query url.Values
		want  int
	}{
		{"missing", url.Values{}, -1},
		{"empty", url.Values{"work_item_ids": {""}}, -1},
		{"malformed", url.Values{"page_ids": {"not-a-uuid"}}, -1},
		{"zero", url.Values{"work_item_ids": {uuid.Nil.String()}}, -1},
		{"trailing-comma", url.Values{"work_item_ids": {id + ","}}, -1},
		{"comma-and-repeated", url.Values{"work_item_ids": {id + "," + second, id}, "page_ids": {second}}, 3},
		{"hundred", url.Values{"work_item_ids": {strings.TrimSuffix(strings.Repeat(id+",", 100), ",")}}, 1},
		{"over-hundred", url.Values{"work_item_ids": {strings.TrimSuffix(strings.Repeat(id+",", 101), ",")}}, -1},
		{"combined-limit", url.Values{"work_item_ids": {strings.TrimSuffix(strings.Repeat(id+",", 50), ",")}, "page_ids": {strings.TrimSuffix(strings.Repeat(second+",", 51), ",")}}, -1},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := assetBatchTargets(test.query)
			if test.want < 0 {
				if err == nil {
					t.Fatal("invalid entity batch was accepted")
				}
			} else if err != nil || len(result) != test.want {
				t.Fatalf("parsed %d entities, wanted %d: %v", len(result), test.want, err)
			}
		})
	}
	f := testutil.New(t)
	router := f.Router(Register)
	for _, query := range []string{"", "work_item_ids=bad", "work_item_ids=" + id + "&deleted=all", "work_item_ids=" + id + "&deleted=true&deleted=false"} {
		testutil.Request(t, router, f.OwnerID, "GET", f.WorkspacePrefix()+"/assets/batch?"+query, nil, 400)
	}
}
