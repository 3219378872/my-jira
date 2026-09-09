package workitems_test

import (
	"net/url"
	"reflect"
	"sort"
	"testing"

	"github.com/google/uuid"
	"my-jira/apps/api/internal/support/testutil"
	"my-jira/apps/api/internal/workitems"
)

func groupKeys(t *testing.T, groups []any) []string {
	t.Helper()
	keys := make([]string, 0, len(groups))
	for _, raw := range groups {
		keys = append(keys, raw.(map[string]any)["key"].(string))
	}
	return keys
}

func wantGroupKeys(t *testing.T, groups []any, want []string) {
	t.Helper()
	if got := groupKeys(t, groups); !reflect.DeepEqual(got, want) {
		t.Fatalf("group order %v, want %v", got, want)
	}
}

func TestGroupBusinessOrderIncludesEmptyGroupsBeforePagination(t *testing.T) {
	f := testutil.New(t)
	r := f.Router(workitems.Register)
	base := f.Prefix() + "/issues"
	first := uuid.MustParse("ffffffff-ffff-4fff-8fff-fffffffffff1")
	empty := uuid.MustParse("eeeeeeee-eeee-4eee-8eee-eeeeeeeeeee2")
	last := uuid.MustParse("00000000-0000-4000-8000-000000000003")
	for _, state := range []struct {
		id       uuid.UUID
		group    string
		position int
	}{{first, "started", 100}, {empty, "backlog", 200}, {last, "completed", 300}} {
		f.Exec(t, `INSERT INTO states(id,workspace_id,project_id,name,group_name,position) VALUES($1,$2,$3,$4,$4,$5)`, state.id, f.WorkspaceID, f.ProjectID, state.group, state.position)
	}
	f.Exec(t, `UPDATE states SET position=400 WHERE id=$1`, f.StateID)
	for i, input := range []struct {
		state    uuid.UUID
		priority string
	}{{first, "urgent"}, {last, "low"}, {f.StateID, "medium"}, {f.StateID, "none"}} {
		id := f.Issue(t, f.ProjectID, input.state, f.OwnerID, input.priority, i+1)
		f.Exec(t, `UPDATE work_items SET priority=$2 WHERE id=$1`, id, input.priority)
	}
	read := func(query string) map[string]any {
		t.Helper()
		return testutil.Request(t, r, f.OwnerID, "GET", base+"?"+query, nil, 200)
	}
	for _, test := range []struct {
		field string
		want  []string
	}{
		{"priority", []string{"urgent", "high", "medium", "low", "none"}},
		{"state_group", []string{"backlog", "unstarted", "started", "completed", "cancelled"}},
		{"state_id", []string{first.String(), empty.String(), last.String(), f.StateID.String()}},
	} {
		t.Run(test.field, func(t *testing.T) {
			cursor := ""
			combined := []any{}
			for page := 0; page < 4; page++ {
				response := read("group_by=" + test.field + "&show_empty=true&group_limit=2&group_cursor=" + url.QueryEscape(cursor))
				groups := testutil.Array(t, response)
				combined = append(combined, groups...)
				pagination := response["pagination"].(map[string]any)
				if pagination["total"] != float64(len(test.want)) {
					t.Fatalf("group count lost empty groups: %#v", pagination)
				}
				if pagination["has_more"] == false {
					break
				}
				cursor = pagination["next_cursor"].(string)
			}
			wantGroupKeys(t, combined, test.want)
		})
	}
	wantGroupKeys(t, testutil.Array(t, read("group_by=priority")), []string{"urgent", "medium", "low", "none"})
	nested := testutil.Array(t, read("group_by=state_group&sub_group_by=priority&show_empty=true&group_limit=2"))
	wantGroupKeys(t, nested, []string{"backlog", "unstarted"})
	for _, raw := range nested {
		parent := raw.(map[string]any)
		children := parent["groups"].([]any)
		wantGroupKeys(t, children, []string{"urgent", "high", "medium", "low", "none"})
		if parent["key"] == "backlog" {
			if parent["total"] != float64(0) {
				t.Fatal("empty parent gained items")
			}
			for _, raw := range children {
				child := raw.(map[string]any)
				if child["total"] != float64(0) || child["pagination"].(map[string]any)["has_more"] != false {
					t.Fatalf("empty child pagination: %#v", child)
				}
			}
		}
	}
	nested = testutil.Array(t, read("group_by=state_group&sub_group_by=priority&group_key=completed&sub_group_key=low&show_empty=true"))
	wantGroupKeys(t, nested, []string{"completed"})
	wantGroupKeys(t, nested[0].(map[string]any)["groups"].([]any), []string{"low"})
}

func TestGroupCatalogUsesBusinessPositionsAndStableDisplayNames(t *testing.T) {
	f := testutil.New(t)
	r := f.Router(workitems.Register)
	base := f.Prefix() + "/issues"
	a := uuid.MustParse("ffffffff-ffff-4fff-8fff-fffffffffff1")
	b := uuid.MustParse("00000000-0000-4000-8000-000000000002")
	c := uuid.MustParse("77777777-7777-4777-8777-777777777773")
	items := []uuid.UUID{}
	for i, author := range []uuid.UUID{f.OwnerID, f.MemberID, f.GuestID, f.OwnerID} {
		items = append(items, f.Issue(t, f.ProjectID, f.StateID, author, "Catalog group", i+1))
	}
	f.Exec(t, `UPDATE users SET display_name='Zulu' WHERE id=$1`, f.OwnerID)
	f.Exec(t, `UPDATE users SET display_name='alpha' WHERE id=$1`, f.MemberID)
	f.Exec(t, `UPDATE users SET display_name='Alpha' WHERE id=$1`, f.GuestID)
	estimate := uuid.New()
	f.Exec(t, `INSERT INTO estimates(id,workspace_id,project_id,name,kind) VALUES($1,$2,$3,'Category','categories')`, estimate, f.WorkspaceID, f.ProjectID)
	for i, input := range []struct {
		id            uuid.UUID
		name, created string
		position      int
		author        uuid.UUID
	}{{a, "Zulu", "2026-09-01T00:00:00Z", 10, f.OwnerID}, {b, "alpha", "2026-08-01T00:00:00Z", 20, f.MemberID}, {c, "Beta", "2026-09-02T00:00:00Z", 10, f.GuestID}} {
		f.Exec(t, `INSERT INTO cycles(id,workspace_id,project_id,owner_id,name,position,created_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, input.id, f.WorkspaceID, f.ProjectID, f.OwnerID, input.name, input.position, input.created)
		f.Exec(t, `INSERT INTO cycle_items(id,workspace_id,project_id,cycle_id,work_item_id) VALUES($1,$2,$3,$4,$5)`, uuid.New(), f.WorkspaceID, f.ProjectID, input.id, items[i])
		f.Exec(t, `INSERT INTO modules(id,workspace_id,project_id,name,position,created_at) VALUES($1,$2,$3,$4,$5,$6)`, input.id, f.WorkspaceID, f.ProjectID, input.name, input.position, input.created)
		f.Exec(t, `INSERT INTO module_items(id,workspace_id,project_id,module_id,work_item_id) VALUES($1,$2,$3,$4,$5)`, uuid.New(), f.WorkspaceID, f.ProjectID, input.id, items[i])
		f.Exec(t, `INSERT INTO estimate_points(id,workspace_id,project_id,estimate_id,label,position) VALUES($1,$2,$3,$4,$5,$6)`, input.id, f.WorkspaceID, f.ProjectID, estimate, input.name, (3-i)*10)
		f.Exec(t, `UPDATE work_items SET estimate_point_id=$2 WHERE id=$1`, items[i], input.id)
		f.Exec(t, `INSERT INTO labels(id,workspace_id,project_id,name,position) VALUES($1,$2,$3,$4,$5)`, input.id, f.WorkspaceID, f.ProjectID, input.name, input.position)
		f.Exec(t, `INSERT INTO work_item_labels(id,workspace_id,project_id,work_item_id,label_id) VALUES($1,$2,$3,$4,$5)`, uuid.New(), f.WorkspaceID, f.ProjectID, items[i], input.id)
		f.Exec(t, `INSERT INTO work_item_assignees(id,workspace_id,project_id,work_item_id,user_id) VALUES($1,$2,$3,$4,$5)`, uuid.New(), f.WorkspaceID, f.ProjectID, items[i], input.author)
	}
	users := []string{f.MemberID.String(), f.GuestID.String()}
	sort.Strings(users)
	users = append(users, f.OwnerID.String(), "none")
	for _, test := range []struct {
		field string
		want  []string
	}{
		{"cycle_id", []string{a.String(), c.String(), b.String(), "none"}},
		{"module_id", []string{a.String(), c.String(), b.String(), "none"}},
		{"estimate_point_id", []string{c.String(), b.String(), a.String(), "none"}},
		{"label_id", []string{b.String(), c.String(), a.String(), "none"}},
		{"assignee_id", users},
		{"created_by", users},
	} {
		t.Run(test.field, func(t *testing.T) {
			response := testutil.Request(t, r, f.OwnerID, "GET", base+"?group_by="+test.field+"&show_empty=true&group_limit=2", nil, 200)
			groups := testutil.Array(t, response)
			cursor := response["pagination"].(map[string]any)["next_cursor"].(string)
			next := testutil.Request(t, r, f.OwnerID, "GET", base+"?group_by="+test.field+"&show_empty=true&group_limit=2&group_cursor="+url.QueryEscape(cursor), nil, 200)
			groups = append(groups, testutil.Array(t, next)...)
			wantGroupKeys(t, groups, test.want)
			for _, raw := range groups {
				group := raw.(map[string]any)
				if group["key"] != "none" && group["label"] == group["key"] {
					t.Fatalf("catalog label was not resolved: %#v", group)
				}
			}
		})
	}
}

func TestWorkspaceProjectStateGroupsUseOnlyTheirAuthorizedCatalogs(t *testing.T) {
	f := testutil.New(t)
	r := f.Router(workitems.Register)
	base := f.WorkspacePrefix() + "/issues?group_by=project_id&sub_group_by=state_id&show_empty=true"
	f.Exec(t, `UPDATE projects SET name='Zulu' WHERE id=$1`, f.ProjectID)
	f.Exec(t, `UPDATE projects SET name='alpha' WHERE id=$1`, f.OtherProjectID)
	f.Exec(t, `UPDATE states SET position=50 WHERE id IN($1,$2)`, f.StateID, f.OtherStateID)
	emptyMain, emptyOther := uuid.New(), uuid.New()
	for project, state := range map[uuid.UUID]uuid.UUID{f.ProjectID: emptyMain, f.OtherProjectID: emptyOther} {
		f.Exec(t, `INSERT INTO states(id,workspace_id,project_id,name,group_name,position) VALUES($1,$2,$3,'Empty first','backlog',10)`, state, f.WorkspaceID, project)
	}
	f.Issue(t, f.ProjectID, f.StateID, f.OwnerID, "Main item", 1)
	f.Issue(t, f.ProjectID, f.StateID, f.GuestID, "Guest item", 2)
	f.Issue(t, f.OtherProjectID, f.OtherStateID, f.OwnerID, "Other item", 1)
	hiddenProject, hiddenState := f.Project(t, "NOACCESS")
	f.Issue(t, hiddenProject, hiddenState, f.OwnerID, "Hidden even from workspace admin", 1)
	first := testutil.Request(t, r, f.OwnerID, "GET", base+"&group_limit=1", nil, 200)
	groups := testutil.Array(t, first)
	wantGroupKeys(t, groups, []string{f.OtherProjectID.String()})
	wantGroupKeys(t, groups[0].(map[string]any)["groups"].([]any), []string{emptyOther.String(), f.OtherStateID.String()})
	cursor := first["pagination"].(map[string]any)["next_cursor"].(string)
	second := testutil.Request(t, r, f.OwnerID, "GET", base+"&group_limit=1&group_cursor="+url.QueryEscape(cursor), nil, 200)
	groups = testutil.Array(t, second)
	wantGroupKeys(t, groups, []string{f.ProjectID.String()})
	wantGroupKeys(t, groups[0].(map[string]any)["groups"].([]any), []string{emptyMain.String(), f.StateID.String()})
	for _, actor := range []uuid.UUID{f.MemberID, f.GuestID} {
		groups := testutil.Array(t, testutil.Request(t, r, actor, "GET", base, nil, 200))
		wantGroupKeys(t, groups, []string{f.ProjectID.String(), "none"})
		wantGroupKeys(t, groups[0].(map[string]any)["groups"].([]any), []string{emptyMain.String(), f.StateID.String()})
		if actor == f.GuestID && groups[0].(map[string]any)["total"] != float64(1) {
			t.Fatal("empty-state catalog expanded Guest item visibility")
		}
	}
	f.Exec(t, `UPDATE project_members SET is_active=false WHERE project_id=$1 AND user_id=$2`, f.ProjectID, f.MemberID)
	groups = testutil.Array(t, testutil.Request(t, r, f.MemberID, "GET", base, nil, 200))
	wantGroupKeys(t, groups, []string{"none"})
	if groups[0].(map[string]any)["groups"] != nil {
		t.Fatal("revoked project left workflow states in an empty parent")
	}
}
