package workitems_test

import (
	"testing"

	"github.com/google/uuid"
	"my-jira/apps/api/internal/support/testutil"
	"my-jira/apps/api/internal/workitems"
)

func TestWorkspaceLifecycleListsRespectProjectRole(t *testing.T) {
	f := testutil.New(t)
	r := f.Router(workitems.Register)
	liveOwn := f.Issue(t, f.ProjectID, f.StateID, f.MemberID, "My active item", 1)
	f.Issue(t, f.ProjectID, f.StateID, f.OwnerID, "Other active item", 2)
	deletedOwn := f.Issue(t, f.ProjectID, f.StateID, f.MemberID, "My deleted item", 3)
	deletedOther := f.Issue(t, f.ProjectID, f.StateID, f.OwnerID, "Other deleted item", 4)
	f.Exec(t, "UPDATE work_items SET deleted_at=now() WHERE id IN($1,$2)", deletedOwn, deletedOther)
	f.Exec(t, "UPDATE project_members SET role=5 WHERE project_id=$1 AND user_id=$2", f.ProjectID, f.MemberID)
	list := func(actor uuid.UUID, query string) []any {
		return testutil.Array(t, testutil.Request(t, r, actor, "GET", f.WorkspacePrefix()+"/issues"+query, nil, 200))
	}
	if items := list(f.MemberID, ""); len(items) != 1 || items[0].(map[string]any)["id"] != liveOwn.String() {
		t.Fatal("project guest must retain creator-only active reads")
	}
	for _, canViewAll := range []bool{false, true} {
		f.Exec(t, "UPDATE projects SET guest_can_view_all=$2 WHERE id=$1", f.ProjectID, canViewAll)
		for _, query := range []string{"?deleted=true", "?deleted=all"} {
			testutil.Request(t, r, f.MemberID, "GET", f.Prefix()+"/issues"+query, nil, 403)
			if items := list(f.MemberID, query); len(items) != 0 {
				t.Fatalf("workspace lifecycle collection included a guest project: %s, view all %v", query, canViewAll)
			}
		}
	}
	if items := list(f.MemberID, ""); len(items) != 2 {
		t.Fatal("view-all preference must still expand ordinary guest reads")
	}
	f.Exec(t, "UPDATE project_members SET role=15 WHERE project_id=$1 AND user_id=$2", f.ProjectID, f.MemberID)
	if items := list(f.MemberID, "?deleted=true"); len(items) != 2 {
		t.Fatal("project member did not receive the deleted collection")
	}
	if items := list(f.MemberID, "?deleted=all"); len(items) != 4 {
		t.Fatal("project member did not receive the complete lifecycle collection")
	}
}
