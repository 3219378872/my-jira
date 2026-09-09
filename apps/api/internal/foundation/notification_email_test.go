package foundation

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"my-jira/apps/api/internal/platform"
	"my-jira/apps/api/internal/platform/identity"
	"my-jira/apps/api/internal/platform/jobs"
)

func TestNotificationEmailRechecksCurrentContentPermissionsAndPreferences(t *testing.T) {
	f := newFixture(t)
	owner := f.client()
	ownerUser := owner.account("owner@example.test", true)
	member := f.client()
	memberUser := member.account("member@example.test", false)
	w := owner.must("POST", "/workspaces", map[string]any{"name": "Team", "slug": "team"}, 201)
	base := "/workspaces/" + w["id"].(string)
	owner.must("POST", base+"/members", map[string]any{"email": "member@example.test", "role": 15}, 201)
	p := owner.must("POST", base+"/projects", map[string]any{"name": "Private", "identifier": "PRI", "network": "private"}, 201)
	owner.must("POST", base+"/projects/"+p["id"].(string)+"/members", map[string]any{"user_id": memberUser["id"], "role": 15}, 201)
	itemID, notificationID := uuid.New(), uuid.New()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := f.db.SQL.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO work_items(id,workspace_id,project_id,state_id,created_by,updated_by,name,sequence_id) SELECT $1,$2,$3,id,$4,$4,'Current title',1 FROM states WHERE project_id=$3 AND is_default`, itemID, w["id"], p["id"], ownerUser["id"])
	exec(`INSERT INTO notifications(id,workspace_id,project_id,user_id,actor_id,entity_type,entity_id,title,body,data) VALUES($1,$2,$3,$4,$5,'work_item',$6,'Stale sensitive title','Old sensitive body','{"reasons":["assigned"],"silent":true}')`, notificationID, w["id"], p["id"], memberUser["id"], ownerUser["id"], itemID)
	exec(`UPDATE users SET email='current@example.test' WHERE id=$1`, memberUser["id"])
	host, port, messages := mockSMTP(t)
	t.Setenv("SMTP_HOST", host)
	t.Setenv("SMTP_PORT", port)
	t.Setenv("SMTP_FROM", "sender@example.test")
	t.Setenv("SMTP_USER", "")
	t.Setenv("SMTP_PASSWORD", "")
	t.Setenv("SMTP_SECURE", "false")
	t.Setenv("APP_ORIGIN", "https://app.example.test")
	deps := platform.Dependencies{DB: f.db, Policy: &identity.SQLPolicy{DB: f.db.SQL}, Jobs: jobs.Outbox{}}
	mux := asynq.NewServeMux()
	RegisterJobs(mux, deps)
	run := func(wantSend bool) string {
		t.Helper()
		payload, _ := json.Marshal(map[string]any{"notification_id": notificationID, "to": "stale@example.test", "subject": "Injected stale title", "text": "Injected stale body"})
		raw, _ := json.Marshal(jobs.Envelope{EventID: uuid.New(), Payload: payload})
		if err := mux.ProcessTask(t.Context(), asynq.NewTask("email.send", raw)); err != nil {
			t.Fatal(err)
		}
		select {
		case message := <-messages:
			if !wantSend {
				t.Fatal("notification was sent after access or preference was revoked")
			}
			return message
		default:
			if wantSend {
				t.Fatal("notification did not reach the local SMTP server")
			}
			return ""
		}
	}
	message := run(true)
	if !strings.Contains(message, "To: current@example.test") || !strings.Contains(message, "Subject: [my-jira] PRI-1 Current title") || !strings.Contains(message, "https://app.example.test/w/team/projects/") || strings.Contains(message, "Stale") || strings.Contains(message, "Old sensitive") || strings.Contains(message, "Injected") {
		t.Fatalf("notification did not use current content and recipient: %s", message)
	}
	exec(`INSERT INTO preferences(workspace_id,user_id,scope,value) VALUES($1,$2,'notifications','{"email":false}')`, w["id"], memberUser["id"])
	run(false)
	exec(`UPDATE preferences SET value='{"email":true,"assigned":false,"mentions":false}' WHERE workspace_id=$1 AND user_id=$2 AND scope='notifications'`, w["id"], memberUser["id"])
	run(false)
	exec(`UPDATE notifications SET data='{"reasons":["assigned","mentions"],"silent":true}' WHERE id=$1`, notificationID)
	exec(`UPDATE preferences SET value='{"email":true,"in_app":false,"assigned":false,"mentions":true}' WHERE workspace_id=$1 AND user_id=$2 AND scope='notifications'`, w["id"], memberUser["id"])
	run(true)
	exec(`UPDATE workspace_members SET role=5 WHERE workspace_id=$1 AND user_id=$2`, w["id"], memberUser["id"])
	run(false)
	exec(`UPDATE projects SET guest_can_view_all=true WHERE id=$1`, p["id"])
	run(true)
	exec(`UPDATE projects SET guest_can_view_all=false WHERE id=$1`, p["id"])
	run(false)
	exec(`UPDATE work_items SET created_by=$2 WHERE id=$1`, itemID, memberUser["id"])
	run(true)
	exec(`UPDATE project_members SET is_active=false WHERE project_id=$1 AND user_id=$2`, p["id"], memberUser["id"])
	run(false)
	exec(`UPDATE project_members SET is_active=true WHERE project_id=$1 AND user_id=$2`, p["id"], memberUser["id"])
	exec(`UPDATE workspace_members SET is_active=false WHERE workspace_id=$1 AND user_id=$2`, w["id"], memberUser["id"])
	run(false)
	exec(`UPDATE workspace_members SET is_active=true WHERE workspace_id=$1 AND user_id=$2`, w["id"], memberUser["id"])
	exec(`UPDATE notifications SET actor_id=user_id WHERE id=$1`, notificationID)
	run(false)
	exec(`UPDATE notifications SET actor_id=$2 WHERE id=$1`, notificationID, ownerUser["id"])
	exec(`UPDATE work_items SET deleted_at=now() WHERE id=$1`, itemID)
	run(false)
}

func TestNotificationEmailRechecksPrivatePageAndViewOwnership(t *testing.T) {
	f := newFixture(t)
	owner := f.client()
	ownerUser := owner.account("owner@example.test", true)
	member := f.client()
	memberUser := member.account("member@example.test", false)
	w := owner.must("POST", "/workspaces", map[string]any{"name": "Team", "slug": "team"}, 201)
	owner.must("POST", "/workspaces/"+w["id"].(string)+"/members", map[string]any{"email": "member@example.test", "role": 15}, 201)
	deps := platform.Dependencies{DB: f.db, Policy: &identity.SQLPolicy{DB: f.db.SQL}}
	for _, kind := range []string{"page", "view"} {
		t.Run(kind, func(t *testing.T) {
			entityID, notificationID := uuid.New(), uuid.New()
			table := "pages"
			if kind == "view" {
				table = "saved_views"
			}
			if _, err := f.db.SQL.Exec(`INSERT INTO `+table+`(id,workspace_id,owner_id,name,is_private) VALUES($1,$2,$3,'Current private document',true)`, entityID, w["id"], ownerUser["id"]); err != nil {
				t.Fatal(err)
			}
			if _, err := f.db.SQL.Exec(`INSERT INTO notifications(id,workspace_id,user_id,actor_id,entity_type,entity_id,title,data) VALUES($1,$2,$3,$4,$5,$6,'Stale visible document','{"reasons":["mentions"]}')`, notificationID, w["id"], memberUser["id"], ownerUser["id"], kind, entityID); err != nil {
				t.Fatal(err)
			}
			if message, err := notificationEmail(t.Context(), deps, notificationID); err != nil || message != nil {
				t.Fatalf("private %s leaked to nonowner: %v %v", kind, message, err)
			}
			if _, err := f.db.SQL.Exec(`UPDATE `+table+` SET owner_id=$2 WHERE id=$1`, entityID, memberUser["id"]); err != nil {
				t.Fatal(err)
			}
			if message, err := notificationEmail(t.Context(), deps, notificationID); err != nil || message == nil || !strings.Contains(message.Subject, "Current private document") {
				t.Fatalf("private %s owner mail: %v %v", kind, message, err)
			}
		})
	}
}

func TestOrdinaryAuthenticationMailStillUsesExplicitPayload(t *testing.T) {
	f := newFixture(t)
	host, port, messages := mockSMTP(t)
	t.Setenv("SMTP_HOST", host)
	t.Setenv("SMTP_PORT", port)
	t.Setenv("SMTP_FROM", "sender@example.test")
	t.Setenv("SMTP_USER", "")
	t.Setenv("SMTP_PASSWORD", "")
	t.Setenv("SMTP_SECURE", "false")
	deps := platform.Dependencies{DB: f.db, Policy: &identity.SQLPolicy{DB: f.db.SQL}}
	mux := asynq.NewServeMux()
	RegisterJobs(mux, deps)
	payload, _ := json.Marshal(map[string]any{"to": "recipient@example.test", "subject": "Password reset", "text": "Use this one-time reset link"})
	raw, _ := json.Marshal(jobs.Envelope{EventID: uuid.New(), Payload: payload})
	if err := mux.ProcessTask(t.Context(), asynq.NewTask("email.send", raw)); err != nil {
		t.Fatal(err)
	}
	select {
	case message := <-messages:
		if !strings.Contains(message, "Subject: Password reset") || !strings.Contains(message, "Use this one-time reset link") {
			t.Fatal("authentication mail payload was changed")
		}
	default:
		t.Fatal("authentication mail did not reach SMTP")
	}
}
