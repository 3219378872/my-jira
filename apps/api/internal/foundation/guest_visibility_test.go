package foundation

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"my-jira/apps/api/internal/platform/identity"
)

func TestProjectGuestVisibilityIsCurrentAndDoesNotGrantWriteRole(t *testing.T) {
	f := newFixture(t)
	owner, guest := f.client(), f.client()
	owner.account("visibility-owner@example.test", true)
	guestUser := guest.account("visibility-guest@example.test", false)
	w := owner.must("POST", "/workspaces", map[string]any{"name": "Visibility", "slug": "guest-visibility"}, 201)
	base := "/workspaces/" + w["id"].(string)
	owner.must("POST", base+"/members", map[string]any{"email": "visibility-guest@example.test", "role": 5}, 201)
	p := owner.must("POST", base+"/projects", map[string]any{"name": "Private", "identifier": "GUE", "network": "private"}, 201)
	path := base + "/projects/" + p["id"].(string)
	if p["guest_can_view_all"] != false {
		t.Fatal("guest visibility did not default to false")
	}
	owner.must("PATCH", path, map[string]any{"guest_can_view_all": true}, 200)
	guest.must("GET", path, nil, 404)
	owner.must("POST", path+"/members", map[string]any{"user_id": guestUser["id"], "role": 20}, 400)
	owner.must("POST", path+"/members", map[string]any{"user_id": guestUser["id"], "role": 5}, 201)
	view := guest.must("GET", path, nil, 200)
	if view["guest_can_view_all"] != true || view["role"] != float64(5) {
		t.Fatal("guest visibility incorrectly elevated the actor role")
	}
	guest.must("PATCH", path, map[string]any{"guest_can_view_all": false}, 403)
	owner.must("PATCH", path, map[string]any{"guest_can_view_all": "true"}, 400)
	policy := identity.SQLPolicy{DB: f.db.SQL}
	actor := identity.Actor{UserID: uuid.MustParse(guestUser["id"].(string))}
	check := func(want bool) {
		t.Helper()
		scope, err := policy.Project(context.Background(), actor, uuid.MustParse(w["id"].(string)), uuid.MustParse(p["id"].(string)), identity.Guest)
		if err != nil || scope.Role != identity.Guest || scope.GuestCanViewAll != want {
			t.Fatalf("current project policy: %#v %v", scope, err)
		}
		if _, err := policy.Project(context.Background(), actor, scope.WorkspaceID, scope.ProjectID, identity.Member); err == nil {
			t.Fatal("read visibility granted Member mutation permission")
		}
	}
	check(true)
	owner.must("PATCH", path, map[string]any{"guest_can_view_all": false}, 200)
	check(false)
}
