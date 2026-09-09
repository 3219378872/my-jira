package foundation

import (
	"testing"

	"github.com/google/uuid"
)

func TestAdminStatsExcludeDeletedParentsAndRetainArchivedResources(t *testing.T) {
	f := newFixture(t)
	admin := f.client()
	user := admin.account("counts-admin@example.test", true)
	userID := user["id"].(string)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := f.db.SQL.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	createWorkspace := func(slug string) string {
		t.Helper()
		return admin.must("POST", "/workspaces", map[string]any{"name": slug, "slug": slug}, 201)["id"].(string)
	}
	createProject := func(workspaceID, identifier string) string {
		t.Helper()
		return admin.must("POST", "/workspaces/"+workspaceID+"/projects", map[string]any{"name": identifier, "identifier": identifier}, 201)["id"].(string)
	}
	workspace, removedWorkspace := createWorkspace("retained"), createWorkspace("removed")
	project := createProject(workspace, "LIVE")
	archivedProject := createProject(workspace, "ARCHIVED")
	removedProject := createProject(workspace, "REMOVED")
	childOfRemovedWorkspace := createProject(removedWorkspace, "CHILD")
	admin.must("PATCH", "/workspaces/"+workspace+"/projects/"+archivedProject, map[string]any{"archived_at": "now"}, 200)
	createItem := func(workspaceID, projectID string, sequence int) uuid.UUID {
		t.Helper()
		id := uuid.New()
		exec(`INSERT INTO work_items(id,workspace_id,project_id,state_id,created_by,updated_by,name,sequence_id) VALUES($1,$2,$3,(SELECT id FROM states WHERE project_id=$3 AND is_default AND deleted_at IS NULL),$4,$4,'Counted item',$5)`, id, workspaceID, projectID, userID, sequence)
		return id
	}
	item := createItem(workspace, project, 1)
	archivedItem := createItem(workspace, project, 2)
	draft := createItem(workspace, project, 3)
	deletedItem := createItem(workspace, project, 4)
	inArchivedProject := createItem(workspace, archivedProject, 1)
	inRemovedProject := createItem(workspace, removedProject, 1)
	inRemovedWorkspace := createItem(removedWorkspace, childOfRemovedWorkspace, 1)
	exec(`UPDATE work_items SET archived_at=now() WHERE id=$1`, archivedItem)
	exec(`UPDATE work_items SET is_draft=true WHERE id=$1`, draft)
	exec(`UPDATE work_items SET deleted_at=now() WHERE id=$1`, deletedItem)
	createPage := func(workspaceID string, projectID any) uuid.UUID {
		t.Helper()
		id := uuid.New()
		exec(`INSERT INTO pages(id,workspace_id,project_id,owner_id,name) VALUES($1,$2,$3,$4,'Counted page')`, id, workspaceID, projectID, userID)
		return id
	}
	page := createPage(workspace, project)
	archivedPage := createPage(workspace, project)
	workspacePage := createPage(workspace, nil)
	removedProjectPage := createPage(workspace, removedProject)
	removedWorkspacePage := createPage(removedWorkspace, nil)
	exec(`UPDATE pages SET archived_at=now() WHERE id=$1`, archivedPage)
	createAsset := func(workspaceID, projectID, itemID, pageID any, size int) uuid.UUID {
		t.Helper()
		id := uuid.New()
		// These are metadata fixtures for logical attachment totals, not claims
		// about bytes stored by an external object service.
		exec(`INSERT INTO file_assets(id,workspace_id,project_id,work_item_id,page_id,uploaded_by,filename,content_type,size_bytes,object_key,upload_status) VALUES($1,$2,$3,$4,$5,$6,'count.txt','text/plain',$7,$8,'completed')`, id, workspaceID, projectID, itemID, pageID, userID, size, "count-fixture/"+id.String())
		return id
	}
	createAsset(nil, nil, nil, nil, 11)
	createAsset(workspace, nil, nil, nil, 17)
	createAsset(workspace, project, nil, nil, 23)
	createAsset(workspace, project, item, nil, 31)
	createAsset(workspace, project, archivedItem, nil, 37)
	createAsset(workspace, project, draft, nil, 41)
	createAsset(workspace, project, nil, page, 43)
	createAsset(workspace, project, nil, archivedPage, 47)
	createAsset(workspace, nil, nil, workspacePage, 53)
	deletedAsset := createAsset(workspace, project, item, nil, 59)
	pendingAsset := createAsset(workspace, project, item, nil, 61)
	exec(`UPDATE file_assets SET deleted_at=now() WHERE id=$1`, deletedAsset)
	exec(`UPDATE file_assets SET upload_status='pending' WHERE id=$1`, pendingAsset)
	createAsset(workspace, archivedProject, inArchivedProject, nil, 73)
	createAsset(workspace, removedProject, inRemovedProject, nil, 79)
	createAsset(workspace, removedProject, nil, removedProjectPage, 83)
	createAsset(workspace, removedProject, nil, nil, 89)
	createAsset(removedWorkspace, nil, nil, nil, 97)
	createAsset(removedWorkspace, childOfRemovedWorkspace, nil, nil, 101)
	createAsset(removedWorkspace, childOfRemovedWorkspace, inRemovedWorkspace, nil, 103)
	createAsset(removedWorkspace, nil, nil, removedWorkspacePage, 107)
	const retainedBytes = 11 + 17 + 23 + 31 + 37 + 41 + 43 + 47 + 53 + 73
	const removedProjectBytes = 79 + 83 + 89
	const removedWorkspaceBytes = 97 + 101 + 103 + 107
	check := func(workspaces, projects, items, storage int) {
		t.Helper()
		stats := admin.must("GET", "/admin/stats", nil, 200)
		for _, count := range []struct {
			name string
			want int
		}{{"workspaces", workspaces}, {"projects", projects}, {"work_items", items}, {"storage_bytes", storage}} {
			if stats[count.name] != float64(count.want) {
				t.Fatalf("%s = %v, want %d after entity lifecycle change", count.name, stats[count.name], count.want)
			}
		}
	}
	check(2, 4, 6, retainedBytes+removedProjectBytes+removedWorkspaceBytes)
	admin.must("DELETE", "/workspaces/"+workspace+"/projects/"+removedProject, nil, 204)
	check(2, 3, 5, retainedBytes+removedWorkspaceBytes)
	admin.must("DELETE", "/workspaces/"+removedWorkspace, nil, 204)
	check(1, 2, 4, retainedBytes)
	var retainedChildRows int
	if err := f.db.SQL.QueryRow(`SELECT count(*) FROM work_items WHERE id IN($1,$2) AND deleted_at IS NULL`, inRemovedProject, inRemovedWorkspace).Scan(&retainedChildRows); err != nil || retainedChildRows != 2 {
		t.Fatalf("parent deletion fixture must retain both child rows: %d %v", retainedChildRows, err)
	}
	// Archiving preserves existing resources, including drafts and attachments.
	admin.must("PATCH", "/workspaces/"+workspace+"/projects/"+project, map[string]any{"archived_at": "now"}, 200)
	check(1, 2, 4, retainedBytes)
	// Deleting only an attachment's entity also removes its logical byte total.
	exec(`UPDATE pages SET deleted_at=now() WHERE id IN($1,$2)`, page, workspacePage)
	exec(`UPDATE work_items SET deleted_at=now() WHERE id=$1`, item)
	check(1, 2, 3, retainedBytes-43-53-31)
}
