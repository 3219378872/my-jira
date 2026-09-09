package files

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"my-jira/apps/api/internal/platform/objectstore"
	"my-jira/apps/api/internal/support/testutil"
)

func storageFixture(t *testing.T) *testutil.Fixture {
	t.Helper()
	endpoint := os.Getenv("TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("TEST_S3_ENDPOINT required for owned object-store integration")
	}
	t.Setenv("S3_ENDPOINT", endpoint)
	t.Setenv("S3_BUCKET", "test-"+uuid.NewString())
	t.Setenv("S3_ACCESS_KEY", "myjira_local")
	t.Setenv("S3_SECRET_KEY", "myjira_local_storage")
	f := testutil.New(t)
	store, bucket, e := objectstore.Load(context.Background(), f.DB.SQL)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		for obj := range store.ListObjects(context.Background(), bucket, minio.ListObjectsOptions{Recursive: true}) {
			if obj.Err == nil {
				_ = store.RemoveObject(context.Background(), bucket, obj.Key, minio.RemoveObjectOptions{})
			}
		}
		_ = store.RemoveBucket(context.Background(), bucket)
	})
	return f
}

func TestAssetsPersistenceScopeCopyAndRecovery(t *testing.T) {
	f := storageFixture(t)
	router := f.Router(Register)
	issue := f.Issue(t, f.ProjectID, f.StateID, f.OwnerID, "Attachment target", 1)
	upload := func(actor uuid.UUID, path string, fields map[string]string, want int) map[string]any {
		var b bytes.Buffer
		writer := multipart.NewWriter(&b)
		part, e := writer.CreateFormFile("file", "notes.txt")
		if e != nil {
			t.Fatal(e)
		}
		_, _ = io.WriteString(part, "durable original bytes")
		for k, v := range fields {
			_ = writer.WriteField(k, v)
		}
		_ = writer.Close()
		r := httptest.NewRequest("POST", path, &b)
		r.Header.Set("Content-Type", writer.FormDataContentType())
		r.Header.Set("X-Test-Actor", actor.String())
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, r)
		if rec.Code != want {
			t.Fatalf("upload %d wanted %d: %s", rec.Code, want, rec.Body)
		}
		if want != 201 {
			return nil
		}
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return testutil.Object(t, out)
	}
	asset := upload(f.OwnerID, f.Prefix()+"/issues/"+issue.String()+"/attachments", nil, 201)
	id := asset["id"].(string)
	download := asset["download_url"].(string)
	if _, ok := asset["object_key"]; ok {
		t.Fatal("storage key exposed")
	}
	get := func(actor uuid.UUID, path string, want int) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("X-Test-Actor", actor.String())
		router.ServeHTTP(rec, r)
		if rec.Code != want {
			t.Fatalf("download %d wanted %d: %s", rec.Code, want, rec.Body)
		}
		return rec
	}
	if rec := get(f.MemberID, download, 200); rec.Body.String() != "durable original bytes" || !strings.HasPrefix(rec.Header().Get("Content-Disposition"), "attachment") {
		t.Fatal("bytes or disposition changed")
	}
	get(f.GuestID, download, 404)
	get(f.OutsiderID, download, 404)
	get(f.OwnerID, strings.Replace(download, f.ProjectID.String(), f.OtherProjectID.String(), 1), 404)
	upload(f.GuestID, f.Prefix()+"/assets", nil, 403)
	testutil.Request(t, router, f.MemberID, "DELETE", f.Prefix()+"/assets/"+id, nil, 403)
	testutil.Request(t, router, f.OwnerID, "DELETE", f.Prefix()+"/assets/"+id, nil, 204)
	get(f.OwnerID, download, 404)
	deleted := testutil.Array(t, testutil.Request(t, router, f.OwnerID, "GET", f.Prefix()+"/assets?deleted=true", nil, 200))
	if len(deleted) != 1 {
		t.Fatal("deleted asset missing from recovery list")
	}
	testutil.Request(t, router, f.OwnerID, "POST", f.Prefix()+"/assets/"+id+"/restore", map[string]any{}, 200)
	get(f.MemberID, download, 200)
	copy := testutil.Object(t, testutil.Request(t, router, f.MemberID, "POST", f.Prefix()+"/assets/"+id+"/copy", map[string]any{"work_item_id": issue}, 201))
	if rec := get(f.MemberID, copy["download_url"].(string), 200); rec.Body.String() != "durable original bytes" {
		t.Fatal("copy lost content")
	}
	f.Exec(t, `UPDATE projects SET guest_can_view_all=true WHERE id=$1`, f.ProjectID)
	get(f.GuestID, copy["download_url"].(string), 200)
	f.Exec(t, `UPDATE projects SET guest_can_view_all=false WHERE id=$1`, f.ProjectID)
	get(f.GuestID, copy["download_url"].(string), 404)
	personal := upload(f.OwnerID, "/api/v1/auth/assets", nil, 201)
	get(f.MemberID, personal["download_url"].(string), 404)
	page := uuid.New()
	f.Exec(t, `INSERT INTO pages(id,workspace_id,project_id,owner_id,name,is_private)VALUES($1,$2,$3,$4,'Private',true)`, page, f.WorkspaceID, f.ProjectID, f.OwnerID)
	upload(f.MemberID, f.Prefix()+"/assets", map[string]string{"page_id": page.String()}, 404)
	private := upload(f.OwnerID, f.Prefix()+"/assets", map[string]string{"page_id": page.String()}, 201)
	get(f.MemberID, private["download_url"].(string), 404)
	f.Exec(t, `UPDATE projects SET guest_can_view_all=true WHERE id=$1`, f.ProjectID)
	get(f.GuestID, private["download_url"].(string), 404)
	f.Exec(t, `UPDATE pages SET is_private=false WHERE id=$1`, page)
	get(f.GuestID, private["download_url"].(string), 200)
	f.Exec(t, `UPDATE projects SET guest_can_view_all=false WHERE id=$1`, f.ProjectID)
	get(f.GuestID, private["download_url"].(string), 404)
	f.Exec(t, `UPDATE pages SET is_private=true WHERE id=$1`, page)
	f.Exec(t, `UPDATE workspace_members SET role=5 WHERE workspace_id=$1 AND user_id=$2`, f.WorkspaceID, f.OwnerID)
	upload(f.OwnerID, f.Prefix()+"/assets", map[string]string{"page_id": page.String()}, 201)
	f.Exec(t, `UPDATE workspace_members SET role=20 WHERE workspace_id=$1 AND user_id=$2`, f.WorkspaceID, f.OwnerID)
	f.Exec(t, `UPDATE pages SET is_locked=true WHERE id=$1`, page)
	upload(f.OwnerID, f.Prefix()+"/assets", map[string]string{"page_id": page.String()}, 404)
	testutil.Request(t, router, f.OwnerID, "DELETE", f.Prefix()+"/assets/"+private["id"].(string), nil, 404)
	testutil.Request(t, router, f.OwnerID, "DELETE", f.Prefix()+"/assets/"+id, nil, 204)
	f.Exec(t, `UPDATE file_assets SET deleted_at=now()-interval '31 days' WHERE id=$1`, id)
	if e := Cleanup(context.Background(), f.Deps); e != nil {
		t.Fatal(e)
	}
	testutil.Request(t, router, f.OwnerID, "POST", f.Prefix()+"/assets/"+id+"/restore", map[string]any{}, 404)
	get(f.MemberID, copy["download_url"].(string), 200)
}
