package foundation

import (
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func TestAvailabilityNavigationAndProjectMetadata(t *testing.T) {
	f := newFixture(t)
	owner := f.client()
	owner.account("owner@example.test", true)
	member := f.client()
	memberUser := member.account("member@example.test", false)
	available := owner.must("GET", "/workspaces/availability?slug=ENGINEERING", nil, 200)
	if available["available"] != true || available["slug"] != "engineering" {
		t.Fatalf("normalized availability: %v", available)
	}
	w := owner.must("POST", "/workspaces", map[string]any{"name": "Engineering", "slug": "engineering"}, 201)
	base := "/workspaces/" + w["id"].(string)
	owner.must("GET", "/workspaces/availability?slug=invalid_slug", nil, 400)
	if owner.must("GET", "/workspaces/availability?slug=engineering", nil, 200)["available"] != false {
		t.Fatal("existing workspace reported available")
	}
	p := owner.must("POST", base+"/projects", map[string]any{"name": "Private", "identifier": "SEC", "network": "private", "features": map[string]any{"cycles": false}}, 201)
	projectPath := base + "/projects/" + p["id"].(string)
	if p["is_member"] != true || p["features"].(map[string]any)["cycles"] != false || p["features"].(map[string]any)["pages"] != true {
		t.Fatalf("project metadata defaults: %v", p)
	}
	owner.must("POST", base+"/members", map[string]any{"email": "member@example.test", "role": 15}, 201)
	owner.must("POST", projectPath+"/members", map[string]any{"user_id": memberUser["id"], "role": 15}, 201)
	if owner.must("GET", base+"/projects/availability?identifier=sec", nil, 200)["available"] != false {
		t.Fatal("existing project reported available")
	}
	if _, err := f.db.SQL.Exec(`UPDATE projects SET settings=settings||'{"automation":{"archive_after_months":3},"display":{"group":"state"}}'::jsonb WHERE id=$1`, p["id"]); err != nil {
		t.Fatal(err)
	}
	owner.must("PATCH", projectPath, map[string]any{"features": map[string]any{"modules": false}, "cover_image_url": "https://images.example.test/cover.png", "settings": map[string]any{"display": map[string]any{"sort": "priority"}}}, 200)
	updated := owner.must("GET", projectPath, nil, 200)
	features := updated["features"].(map[string]any)
	settings := updated["settings"].(map[string]any)
	if features["cycles"] != false || features["modules"] != false || updated["cover_image_url"] != "https://images.example.test/cover.png" || settings["automation"].(map[string]any)["archive_after_months"] != float64(3) || settings["display"].(map[string]any)["group"] != "state" || settings["display"].(map[string]any)["sort"] != "priority" {
		t.Fatalf("settings merge lost existing configuration: %v", updated)
	}
	owner.must("PATCH", projectPath, map[string]any{"settings": map[string]any{"automation": map[string]any{"archive_after_months": 99}}}, 400)
	owner.must("PATCH", projectPath, map[string]any{"features": map[string]any{"cycles": "false"}}, 400)
	owner.must("PATCH", projectPath, map[string]any{"cover_image_url": "javascript:alert(1)"}, 400)
	if owner.must("PATCH", projectPath, map[string]any{"cover_image_url": nil}, 200)["cover_image_url"] != "" {
		t.Fatal("cover image was not cleared")
	}
	member.must("PATCH", "/auth/last-visited", map[string]any{"workspace_id": w["id"], "project_id": p["id"]}, 200)
	member.must("PATCH", "/auth/me", map[string]any{"preferences": map[string]any{"theme": "dark", "_last_visited": map[string]any{"workspace_id": uuid.NewString()}}}, 200)
	location := member.must("GET", "/auth/last-visited", nil, 200)
	if location["workspace_id"] != w["id"] || location["project_id"] != p["id"] {
		t.Fatalf("profile preferences overwrote navigation: %v", location)
	}
	member.must("POST", projectPath+"/leave", nil, 204)
	location = member.must("GET", "/auth/last-visited", nil, 200)
	if location["workspace_id"] != w["id"] || location["project_id"] != nil {
		t.Fatalf("private project location survived membership revocation: %v", location)
	}
	member.must("PATCH", "/auth/last-visited", map[string]any{"workspace_id": w["id"], "project_id": p["id"]}, 404)
	member.must("POST", base+"/leave", nil, 204)
	location = member.must("GET", "/auth/last-visited", nil, 200)
	if location["workspace_id"] != nil || location["project_id"] != nil {
		t.Fatalf("workspace location survived membership revocation: %v", location)
	}
	member.must("GET", base+"/projects/availability?identifier=SEC", nil, 404)
}

func TestProjectJoinLeaveAndWorkspaceInvitationBinding(t *testing.T) {
	f := newFixture(t)
	owner := f.client()
	owner.account("owner@example.test", true)
	guest := f.client()
	guest.account("guest@example.test", false)
	w := owner.must("POST", "/workspaces", map[string]any{"name": "Team", "slug": "team"}, 201)
	base := "/workspaces/" + w["id"].(string)
	owner.must("POST", base+"/members", map[string]any{"email": "guest@example.test", "role": 5}, 201)
	public := owner.must("POST", base+"/projects", map[string]any{"name": "Public", "identifier": "PUB"}, 201)
	private := owner.must("POST", base+"/projects", map[string]any{"name": "Private", "identifier": "PRI", "network": "private"}, 201)
	publicPath := base + "/projects/" + public["id"].(string)
	privatePath := base + "/projects/" + private["id"].(string)
	if guest.must("GET", publicPath, nil, 200)["is_member"] != false {
		t.Fatal("public project access incorrectly implied membership")
	}
	joined := guest.must("POST", publicPath+"/join", nil, 200)
	again := guest.must("POST", publicPath+"/join", nil, 200)
	if joined["role"] != float64(5) || again["id"] != joined["id"] {
		t.Fatalf("join must preserve guest role and be idempotent: %v %v", joined, again)
	}
	guest.must("POST", privatePath+"/join", nil, 404)
	owner.must("POST", privatePath+"/leave", nil, 409)
	owner.must("POST", base+"/leave", nil, 409)
	guest.must("POST", publicPath+"/leave", nil, 204)
	if guest.must("GET", publicPath, nil, 200)["is_member"] != false {
		t.Fatal("leave did not clear explicit membership")
	}
	guest.must("POST", base+"/leave", nil, 204)
	token, err := randomToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.SQL.Exec(`INSERT INTO invitations(workspace_id,email,role,token_hash,invited_by,expires_at) VALUES($1,'guest@example.test',5,$2,(SELECT id FROM users WHERE email='owner@example.test'),now()+interval '1 day')`, w["id"], hashToken(token)); err != nil {
		t.Fatal(err)
	}
	guest.must("POST", "/workspaces/"+uuid.NewString()+"/join", map[string]any{"token": token}, 404)
	joinedWorkspace := guest.must("POST", base+"/join", map[string]any{"token": token}, 200)
	if joinedWorkspace["id"] != w["id"] || joinedWorkspace["role"] != float64(5) {
		t.Fatalf("bound invitation result: %v", joinedWorkspace)
	}
	guest.must("POST", base+"/join", map[string]any{"token": token}, 404)
}

func TestConcurrentWorkspaceAndProjectLeavesRetainProjectAdmin(t *testing.T) {
	f := newFixture(t)
	first := f.client()
	first.account("first@example.test", true)
	second := f.client()
	secondUser := second.account("second@example.test", false)
	w := first.must("POST", "/workspaces", map[string]any{"name": "Team", "slug": "team"}, 201)
	base := "/workspaces/" + w["id"].(string)
	secondMember := first.must("POST", base+"/members", map[string]any{"email": "second@example.test", "role": 20}, 201)
	p := first.must("POST", base+"/projects", map[string]any{"name": "Private", "identifier": "PRI", "network": "private"}, 201)
	projectPath := base + "/projects/" + p["id"].(string)
	first.must("POST", projectPath+"/members", map[string]any{"user_id": secondUser["id"], "role": 20}, 201)
	var wait sync.WaitGroup
	codes := make(chan int, 2)
	for i, client := range []*testClient{first, second} {
		path := projectPath + "/leave"
		if i == 1 {
			path = base + "/leave"
		}
		wait.Add(1)
		go func(c *testClient, path string) {
			defer wait.Done()
			code, _ := c.request("POST", path, nil)
			codes <- code
		}(client, path)
	}
	wait.Wait()
	close(codes)
	counts := map[int]int{}
	for code := range codes {
		counts[code]++
	}
	if counts[204] != 1 || counts[409] != 1 {
		t.Fatalf("concurrent leave results: %v", counts)
	}
	var count int
	if err := f.db.SQL.QueryRow(`SELECT count(*) FROM project_members m JOIN workspace_members wm ON wm.workspace_id=m.workspace_id AND wm.user_id=m.user_id WHERE m.project_id=$1 AND m.role=20 AND m.is_active AND m.deleted_at IS NULL AND wm.is_active AND wm.deleted_at IS NULL AND wm.role>=15`, p["id"]).Scan(&count); err != nil || count != 1 {
		t.Fatalf("active project admins=%d err=%v", count, err)
	}
	// If the project leave won, the remaining project admin cannot be
	// downgraded to a workspace guest and make the private project unmanaged.
	var secondActive bool
	if err := f.db.SQL.QueryRow(`SELECT is_active FROM workspace_members WHERE id=$1`, secondMember["id"]).Scan(&secondActive); err != nil {
		t.Fatal(err)
	}
	if secondActive {
		first.must("PATCH", base+"/members/"+secondMember["id"].(string), map[string]any{"role": 5}, 409)
	}
}

func TestWorkspaceLabelsScopeAndConcurrentHierarchy(t *testing.T) {
	f := newFixture(t)
	owner := f.client()
	owner.account("owner@example.test", true)
	w := owner.must("POST", "/workspaces", map[string]any{"name": "Team", "slug": "team"}, 201)
	base := "/workspaces/" + w["id"].(string)
	p := owner.must("POST", base+"/projects", map[string]any{"name": "Project", "identifier": "PRO"}, 201)
	projectPath := base + "/projects/" + p["id"].(string)
	a := owner.must("POST", base+"/labels", map[string]any{"name": "Global A"}, 201)
	b := owner.must("POST", base+"/labels", map[string]any{"name": "Global B"}, 201)
	if a["project_id"] != nil {
		t.Fatalf("workspace label incorrectly assigned to project: %v", a)
	}
	owner.must("POST", projectPath+"/labels", map[string]any{"name": "Local", "parent_id": a["id"]}, 400)
	local := owner.must("POST", projectPath+"/labels", map[string]any{"name": "Local"}, 201)
	owner.must("PATCH", projectPath+"/labels/"+a["id"].(string), map[string]any{"name": "Moved"}, 404)
	owner.must("PATCH", base+"/labels/"+local["id"].(string), map[string]any{"name": "Moved"}, 404)
	status, listed := owner.request("GET", projectPath+"/labels", nil)
	if status != 200 || len(listed["data"].([]any)) != 3 {
		t.Fatalf("project label list should include global labels: %v", listed)
	}
	var wait sync.WaitGroup
	codes := make(chan int, 2)
	for index, label := range []map[string]any{a, b} {
		parent := b["id"]
		if index == 1 {
			parent = a["id"]
		}
		wait.Add(1)
		go func(id, parent any) {
			defer wait.Done()
			code, _ := owner.request("PATCH", fmt.Sprintf("%s/labels/%s", base, id), map[string]any{"parent_id": parent})
			codes <- code
		}(label["id"], parent)
	}
	wait.Wait()
	close(codes)
	counts := map[int]int{}
	for code := range codes {
		counts[code]++
	}
	if counts[200] != 1 || counts[400] != 1 {
		t.Fatalf("concurrent hierarchy results: %v", counts)
	}
	var childID, parentID string
	if err := f.db.SQL.QueryRow(`SELECT id,parent_id FROM labels WHERE workspace_id=$1 AND project_id IS NULL AND parent_id IS NOT NULL`, w["id"]).Scan(&childID, &parentID); err != nil {
		t.Fatal(err)
	}
	owner.must("DELETE", base+"/labels/"+parentID, nil, 204)
	var parentCleared bool
	if err := f.db.SQL.QueryRow(`SELECT parent_id IS NULL FROM labels WHERE id=$1`, childID).Scan(&parentCleared); err != nil || !parentCleared {
		t.Fatalf("label deletion did not detach child: %v", err)
	}
}
