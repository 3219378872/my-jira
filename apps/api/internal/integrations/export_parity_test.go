package integrations

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"my-jira/apps/api/internal/platform/objectstore"
	"my-jira/apps/api/internal/support/testutil"
	"my-jira/apps/api/internal/workitems"
)

func TestExportsUseListFiltersAcrossFormats(t *testing.T) {
	testutil.ObjectStoreEnvironment(t)
	f := testutil.New(t)
	router := f.Router(Register, workitems.Register)
	store, bucket, err := objectstore.Load(context.Background(), f.DB.SQL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for object := range store.ListObjects(context.Background(), bucket, minio.ListObjectsOptions{Recursive: true}) {
			if object.Err == nil {
				_ = store.RemoveObject(context.Background(), bucket, object.Key, minio.RemoveObjectOptions{})
			}
		}
		_ = store.RemoveBucket(context.Background(), bucket)
	})
	ids := []uuid.UUID{}
	for index, name := range []string{"Eligible high", "Eligible urgent", "Different priority", "Draft", "Archived", "Pending intake", "Deleted"} {
		ids = append(ids, f.Issue(t, f.ProjectID, f.StateID, f.OwnerID, name, index+1))
	}
	f.Exec(t, `UPDATE work_items SET priority='high',target_date='2026-09-15' WHERE project_id=$1`, f.ProjectID)
	f.Exec(t, `UPDATE work_items SET priority='urgent' WHERE id=$1`, ids[1])
	f.Exec(t, `UPDATE work_items SET priority='low' WHERE id=$1`, ids[2])
	f.Exec(t, `UPDATE work_items SET is_draft=true WHERE id=$1`, ids[3])
	f.Exec(t, `UPDATE work_items SET archived_at=now() WHERE id=$1`, ids[4])
	f.Exec(t, `UPDATE work_items SET deleted_at=now() WHERE id=$1`, ids[6])
	f.Exec(t, `INSERT INTO intake_items(id,workspace_id,project_id,work_item_id,submitted_by,status)VALUES($1,$2,$3,$4,$5,'pending')`, uuid.New(), f.WorkspaceID, f.ProjectID, ids[5], f.OwnerID)
	for _, id := range ids {
		f.Exec(t, `INSERT INTO work_item_subscribers(id,workspace_id,project_id,work_item_id,user_id)VALUES($1,$2,$3,$4,$5)`, uuid.New(), f.WorkspaceID, f.ProjectID, id, f.MemberID)
	}
	complex := map[string]any{"and": []any{map[string]any{"field": "name", "op": "contains", "value": "Eligible"}, map[string]any{"not": map[string]any{"field": "estimate", "op": "gt", "value": 10}}}}
	filters := map[string]any{"priority": []any{"high", "urgent"}, "subscriber_id": "me", "target_date_after": "2026-09-01", "assignee_id": []any{nil}, "filter": complex}
	raw, _ := json.Marshal(complex)
	query := url.Values{"priority": []string{"high,urgent"}, "subscriber_id": []string{"me"}, "target_date_after": []string{"2026-09-01"}, "assignee_id": []string{"null"}, "filter": []string{string(raw)}}
	listed := testutil.Array(t, testutil.Request(t, router, f.MemberID, "GET", f.Prefix()+"/issues?"+query.Encode(), nil, 200))
	if len(listed) != 2 {
		t.Fatalf("list fixture: %#v", listed)
	}
	for _, format := range []string{"json", "csv", "xlsx"} {
		exported := testutil.Object(t, testutil.Request(t, router, f.MemberID, "POST", f.WorkspacePrefix()+"/exports", map[string]any{"format": format, "filters": filters}, 202))
		if err := (&handler{f.Deps}).exportJob(context.Background(), eventTask(t, "export.generate", map[string]any{"export_id": exported["id"]})); err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", f.WorkspacePrefix()+"/exports/"+exported["id"].(string)+"/download", nil)
		req.Header.Set("X-Test-Actor", f.MemberID.String())
		router.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("download %s: %d %s", format, rec.Code, rec.Body)
		}
		content := rec.Body.String()
		if format == "xlsx" {
			archive, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
			if err != nil {
				t.Fatal(err)
			}
			for _, file := range archive.File {
				if file.Name == "xl/worksheets/sheet1.xml" {
					r, _ := file.Open()
					data, _ := io.ReadAll(r)
					_ = r.Close()
					content = string(data)
				}
			}
		}
		for index, id := range ids {
			if strings.Contains(content, id.String()) != (index < 2) {
				t.Fatalf("%s differs from list for item %d", format, index)
			}
		}
	}
	for _, invalid := range []map[string]any{{"unknown": "silently ignored"}, {"filter": map[string]any{"field": "password_hash", "value": "bad"}}, {"priority": map[string]any{"bad": true}}} {
		testutil.Request(t, router, f.MemberID, "POST", f.WorkspacePrefix()+"/exports", map[string]any{"format": "json", "filters": invalid}, 400)
	}
	testutil.Request(t, router, f.MemberID, "POST", f.WorkspacePrefix()+"/exports", map[string]any{"format": "json", "filters": map[string]any{"deleted": true}}, 403)
}
