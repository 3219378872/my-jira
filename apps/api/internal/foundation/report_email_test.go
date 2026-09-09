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

func TestReportReadyMailUsesAuthenticatedDownloadAndCurrentScope(t *testing.T) {
	f := newFixture(t)
	owner := f.client()
	owner.account("owner@example.test", true)
	member := f.client()
	memberUser := member.account("member@example.test", false)
	w := owner.must("POST", "/workspaces", map[string]any{"name": "Team", "slug": "team"}, 201)
	base := "/workspaces/" + w["id"].(string)
	owner.must("POST", base+"/members", map[string]any{"email": "member@example.test", "role": 15}, 201)
	p := owner.must("POST", base+"/projects", map[string]any{"name": "Private", "identifier": "PRI", "network": "private"}, 201)
	owner.must("POST", base+"/projects/"+p["id"].(string)+"/members", map[string]any{"user_id": memberUser["id"], "role": 15}, 201)
	exportID := uuid.New()
	filters, _ := json.Marshal(map[string]any{"_scope_project_ids": []any{p["id"]}})
	if _, err := f.db.SQL.Exec(`INSERT INTO exports(id,workspace_id,requested_by,format,filters,status,object_key) VALUES($1,$2,$3,'csv',$4::jsonb,'completed','private/never-expose-this-object-key')`, exportID, w["id"], memberUser["id"], string(filters)); err != nil {
		t.Fatal(err)
	}
	host, port, messages := mockSMTP(t)
	t.Setenv("SMTP_HOST", host)
	t.Setenv("SMTP_PORT", port)
	t.Setenv("SMTP_FROM", "sender@example.test")
	t.Setenv("SMTP_USER", "")
	t.Setenv("SMTP_PASSWORD", "")
	t.Setenv("SMTP_SECURE", "false")
	t.Setenv("APP_ORIGIN", "https://app.example.test")
	deps := platform.Dependencies{DB: f.db, Policy: &identity.SQLPolicy{DB: f.db.SQL}}
	mux := asynq.NewServeMux()
	RegisterJobs(mux, deps)
	run := func(user any, wantSend bool) string {
		t.Helper()
		input := map[string]any{"export_id": exportID}
		if user != nil {
			input["workspace_id"], input["user_id"] = w["id"], user
		}
		payload, _ := json.Marshal(input)
		raw, _ := json.Marshal(jobs.Envelope{EventID: uuid.New(), Payload: payload})
		if err := mux.ProcessTask(t.Context(), asynq.NewTask("email.report-ready", raw)); err != nil {
			t.Fatal(err)
		}
		select {
		case message := <-messages:
			if !wantSend {
				t.Fatal("report-ready message sent to an unauthorized user")
			}
			return message
		default:
			if wantSend {
				t.Fatal("report-ready message did not reach SMTP")
			}
			return ""
		}
	}
	message := run(memberUser["id"], true)
	if !strings.Contains(message, "https://app.example.test/api/v1/workspaces/"+w["id"].(string)+"/exports/"+exportID.String()+"/download") || strings.Contains(message, "never-expose-this-object-key") {
		t.Fatal("report email did not use an authenticated download link")
	}
	run(nil, true)
	run(uuid.New(), false)
	if _, err := f.db.SQL.Exec(`UPDATE project_members SET is_active=false WHERE project_id=$1 AND user_id=$2`, p["id"], memberUser["id"]); err != nil {
		t.Fatal(err)
	}
	run(memberUser["id"], false)
	if _, err := f.db.SQL.Exec(`UPDATE project_members SET is_active=true WHERE project_id=$1 AND user_id=$2`, p["id"], memberUser["id"]); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.SQL.Exec(`UPDATE workspace_members SET role=5 WHERE workspace_id=$1 AND user_id=$2`, w["id"], memberUser["id"]); err != nil {
		t.Fatal(err)
	}
	run(memberUser["id"], false)
}
