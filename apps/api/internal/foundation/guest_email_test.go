package foundation

import (
	"testing"

	"github.com/google/uuid"
	"my-jira/apps/api/internal/platform"
	"my-jira/apps/api/internal/platform/identity"
)

func TestGuestPageEmailUsesCurrentFlagPrivacyAndExplicitMembership(t *testing.T) {
	f := newFixture(t)
	owner, guest := f.client(), f.client()
	ownerUser := owner.account("page-owner@example.test", true)
	guestUser := guest.account("page-guest@example.test", false)
	w := owner.must("POST", "/workspaces", map[string]any{"name": "Email", "slug": "guest-email"}, 201)
	base := "/workspaces/" + w["id"].(string)
	owner.must("POST", base+"/members", map[string]any{"email": "page-guest@example.test", "role": 5}, 201)
	p := owner.must("POST", base+"/projects", map[string]any{"name": "Public", "identifier": "EML", "network": "public"}, 201)
	owner.must("POST", base+"/projects/"+p["id"].(string)+"/members", map[string]any{"user_id": guestUser["id"], "role": 5}, 201)
	pageID, notificationID := uuid.New(), uuid.New()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := f.db.SQL.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO pages(id,workspace_id,project_id,owner_id,name) VALUES($1,$2,$3,$4,'Shared project page')`, pageID, w["id"], p["id"], ownerUser["id"])
	exec(`INSERT INTO notifications(id,workspace_id,project_id,user_id,actor_id,entity_type,entity_id,title,data) VALUES($1,$2,$3,$4,$5,'page',$6,'Stale page','{"reasons":["mentions"]}')`, notificationID, w["id"], p["id"], guestUser["id"], ownerUser["id"], pageID)
	deps := platform.Dependencies{DB: f.db, Policy: &identity.SQLPolicy{DB: f.db.SQL}}
	check := func(allowed bool) {
		t.Helper()
		message, err := notificationEmail(t.Context(), deps, notificationID)
		if err != nil || (message != nil) != allowed {
			t.Fatalf("page email visibility allowed=%v: %v %v", allowed, message, err)
		}
	}
	check(false)
	exec(`UPDATE projects SET guest_can_view_all=true WHERE id=$1`, p["id"])
	check(true)
	exec(`UPDATE pages SET is_private=true WHERE id=$1`, pageID)
	check(false)
	exec(`UPDATE pages SET owner_id=$2 WHERE id=$1`, pageID, guestUser["id"])
	check(true)
	exec(`UPDATE project_members SET is_active=false WHERE project_id=$1 AND user_id=$2`, p["id"], guestUser["id"])
	check(false)
}
