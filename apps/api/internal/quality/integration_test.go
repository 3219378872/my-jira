package quality

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"my-jira/apps/api/internal/automation"
	"my-jira/apps/api/internal/platform"
	"my-jira/apps/api/internal/platform/httpapi"
	"my-jira/apps/api/internal/platform/identity"
	"my-jira/apps/api/internal/support/testutil"
	"my-jira/apps/api/internal/workitems"
)

func insertBinding(t *testing.T, f *testutil.Fixture) uuid.UUID {
	t.Helper()
	id := uuid.New()
	f.Exec(t, `INSERT INTO github_installations(installation_id,account_login) VALUES(5,'org')`)
	f.Exec(t, `INSERT INTO github_bindings(id,workspace_id,project_id,installation_id,repository_id,repository,created_by) VALUES($1,$2,$3,5,99,'org/repo',$4)`, id, f.WorkspaceID, f.ProjectID, f.OwnerID)
	return id
}

func webhookRequest(t *testing.T, r *gin.Engine, secret, event, delivery string, payload any, want int) map[string]any {
	t.Helper()
	raw, e := json.Marshal(payload)
	if e != nil {
		t.Fatal(e)
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(raw)
	req := httptest.NewRequest("POST", "/api/v1/github/webhook", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", event)
	req.Header.Set("X-GitHub-Delivery", delivery)
	req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != want {
		t.Fatalf("webhook status %d want %d: %s", w.Code, want, w.Body.String())
	}
	var result map[string]any
	if json.Unmarshal(w.Body.Bytes(), &result) != nil {
		t.Fatal("non-JSON webhook response")
	}
	return result
}

func TestSignedWebhookReplayOutOfOrderAndRevocation(t *testing.T) {
	f := testutil.New(t)
	bindingID := insertBinding(t, f)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterPublicWithConfig(r.Group("/api/v1"), f.Deps, Config{WebhookSecret: "signed-secret"})
	push := map[string]any{"installation": map[string]int{"id": 5}, "repository": map[string]int{"id": 99}, "after": strings.Repeat("a", 40), "head_commit": map[string]string{"timestamp": "2026-09-11T12:00:00Z"}}
	delivery := uuid.NewString()
	webhookRequest(t, r, "wrong", "push", delivery, push, 401)
	webhookRequest(t, r, "signed-secret", "push", delivery, push, 202)
	duplicate := testutil.Object(t, webhookRequest(t, r, "signed-secret", "push", delivery, push, 202))
	if duplicate["duplicate"] != true {
		t.Fatal("webhook replay not recognized")
	}
	newDeliveryReplay := testutil.Object(t, webhookRequest(t, r, "signed-secret", "push", uuid.NewString(), push, 202))
	if newDeliveryReplay["duplicate"] != true {
		t.Fatal("signed payload replay with a changed delivery header was not deduplicated")
	}
	push["after"] = strings.Repeat("b", 40)
	webhookRequest(t, r, "signed-secret", "push", delivery, push, 409)
	var queued int
	if e := f.DB.SQL.QueryRow(`SELECT count(*) FROM github_sync_requests WHERE binding_id=$1`, bindingID).Scan(&queued); e != nil || queued != 1 {
		t.Fatalf("replay enqueued duplicate work: %d %v", queued, e)
	}
	revoked := map[string]any{"installation": map[string]int{"id": 5}, "action": "deleted"}
	webhookRequest(t, r, "signed-secret", "installation", uuid.NewString(), revoked, 202)
	revoked["action"] = "created"
	webhookRequest(t, r, "signed-secret", "installation", uuid.NewString(), revoked, 202)
	webhookRequest(t, r, "signed-secret", "push", uuid.NewString(), push, 202)
	var active bool
	var state string
	if e := f.DB.SQL.QueryRow(`SELECT active,sync_status FROM github_bindings WHERE id=$1`, bindingID).Scan(&active, &state); e != nil || active || state != "revoked" {
		t.Fatalf("out-of-order delivery reactivated revoked installation: %v %s %v", active, state, e)
	}
	if e := f.DB.SQL.QueryRow(`SELECT count(*) FROM github_sync_requests WHERE binding_id=$1`, bindingID).Scan(&queued); e != nil || queued != 1 {
		t.Fatalf("revoked installation still queued reads: %d %v", queued, e)
	}
}

func TestQualityScopeGrantsAndHistoricalPrivacy(t *testing.T) {
	f := testutil.New(t)
	bid := insertBinding(t, f)
	r := f.Router(Register)
	base := f.Prefix()
	testutil.Request(t, r, f.MemberID, "POST", base+"/github/bindings", map[string]any{"installation_id": 5, "repository": "org/repo"}, 403)
	testutil.Request(t, r, f.OwnerID, "POST", base+"/github/bindings", map[string]any{"installation_id": 5, "repository": "org/repo"}, 403)
	testutil.Request(t, r, f.GuestID, "GET", base+"/quality/reports", nil, 403)
	testutil.Request(t, r, f.OutsiderID, "GET", base+"/quality/reports", nil, 404)
	pageID, reportID := uuid.New(), uuid.New()
	f.Exec(t, `INSERT INTO pages(id,workspace_id,project_id,owner_id,name) VALUES($1,$2,$3,$4,'PRD')`, pageID, f.WorkspaceID, f.ProjectID, f.OwnerID)
	evidence, _ := json.Marshal(map[string]any{"documents": []documentSource{{ID: pageID.String(), Kind: "page", Version: 1, Name: "PRD"}}})
	f.Exec(t, `INSERT INTO quality_reports(id,workspace_id,project_id,binding_id,repository,commit_sha,source_revision,source_at,dimensions,evidence) VALUES($1,$2,$3,$4,'org/repo',$5,'version-one',now(),'{}',$6::jsonb)`, reportID, f.WorkspaceID, f.ProjectID, bid, strings.Repeat("a", 40), string(evidence))
	testutil.Request(t, r, f.MemberID, "GET", base+"/quality/reports/"+reportID.String(), nil, 200)
	f.Exec(t, `UPDATE pages SET is_private=true WHERE id=$1`, pageID)
	testutil.Request(t, r, f.MemberID, "GET", base+"/quality/reports/"+reportID.String(), nil, 404)
	if got := testutil.Array(t, testutil.Request(t, r, f.MemberID, "GET", base+"/quality/reports", nil, 200)); len(got) != 0 {
		t.Fatal("historical report leaked newly private source")
	}
	testutil.Request(t, r, f.MemberID, "DELETE", base+"/github/bindings/"+bid.String(), nil, 403)
	testutil.Request(t, r, f.OwnerID, "DELETE", base+"/github/bindings/"+bid.String(), nil, 204)
	testutil.Request(t, r, f.OwnerID, "POST", base+"/github/bindings/"+bid.String()+"/sync", map[string]any{}, 403)
}

func TestDurableSyncImmutableEvidenceAndLatestOrdering(t *testing.T) {
	f := testutil.New(t)
	bid := insertBinding(t, f)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		sha := strings.Repeat("a", 40)
		switch {
		case r.URL.Path == "/app/installations/5":
			fmt.Fprint(w, `{"id":5}`)
		case r.URL.Path == "/app/installations/5/access_tokens":
			fmt.Fprint(w, `{"token":"test-installation-token"}`)
		case r.URL.Path == "/repos/org/repo":
			fmt.Fprint(w, `{"id":99,"full_name":"org/repo","default_branch":"main"}`)
		case strings.HasPrefix(r.URL.Path, "/repos/org/repo/actions/runs/") && strings.HasSuffix(r.URL.Path, "/artifacts"):
			fmt.Fprint(w, `{"artifacts":[]}`)
		case strings.HasPrefix(r.URL.Path, "/repos/org/repo/actions/runs/"):
			id, day := int64(42), 12
			if strings.Contains(r.URL.Path, "/41") {
				id = 41
				day = 11
				sha = strings.Repeat("b", 40)
			}
			fmt.Fprintf(w, `{"id":%d,"head_sha":%q,"run_attempt":1,"status":"completed","conclusion":"success","updated_at":"2026-09-%dT12:00:00Z"}`, id, sha, day)
		case strings.HasPrefix(r.URL.Path, "/repos/org/repo/commits/"):
			if strings.Contains(r.URL.Path, strings.Repeat("b", 40)) {
				sha = strings.Repeat("b", 40)
			}
			fmt.Fprintf(w, `{"sha":%q,"commit":{"message":"CI evidence","committer":{"date":"2026-09-10T12:00:00Z"}},"files":[]}`, sha)
		default:
			t.Errorf("unexpected source request: %s", r.URL.String())
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	h := newHandler(f.Deps, providerConfig(t, server.URL))
	request := func(run int64) uuid.UUID {
		id := uuid.New()
		source, _ := json.Marshal(Source{RunID: run})
		f.Exec(t, `INSERT INTO github_sync_requests(id,workspace_id,project_id,binding_id,source) VALUES($1,$2,$3,$4,$5::jsonb)`, id, f.WorkspaceID, f.ProjectID, bid, string(source))
		return id
	}
	newRequest, oldRequest := request(42), request(41)
	if e := h.processSync(context.Background(), newRequest); e != nil {
		t.Fatal(e)
	}
	if e := h.processSync(context.Background(), oldRequest); e != nil {
		t.Fatal(e)
	}
	var latest int64
	var count int
	var testsStatus, coverage string
	if e := f.DB.SQL.QueryRow(`SELECT r.run_id,r.dimensions->'tests'->>'status',r.dimensions->'tests'->'summary'->>'coverage_status' FROM github_bindings b JOIN quality_reports r ON r.id=b.latest_report_id WHERE b.id=$1`, bid).Scan(&latest, &testsStatus, &coverage); e != nil || latest != 42 || testsStatus != "missing" || coverage != "unknown" {
		t.Fatalf("ordering or missing evidence semantics failed: %d %s %s %v", latest, testsStatus, coverage, e)
	}
	before := calls.Load()
	if e := h.processSync(context.Background(), newRequest); e != nil {
		t.Fatal(e)
	}
	if calls.Load() != before {
		t.Fatal("completed durable request re-read provider")
	}
	if e := f.DB.SQL.QueryRow(`SELECT count(*) FROM quality_reports WHERE binding_id=$1`, bid).Scan(&count); e != nil || count != 2 {
		t.Fatalf("versioned reports not retained: %d %v", count, e)
	}
	f.Exec(t, `UPDATE github_installations SET revoked_at=now() WHERE installation_id=5`)
	revokedRequest := request(42)
	before = calls.Load()
	if e := h.processSync(context.Background(), revokedRequest); e != nil {
		t.Fatal(e)
	}
	if calls.Load() != before {
		t.Fatal("revoked installation still read provider")
	}
}

func TestWaitingSyncRechecksRemovedMembership(t *testing.T) {
	f := testutil.New(t)
	bid := insertBinding(t, f)
	r := f.Router(Register)
	tx, e := f.DB.SQL.BeginTx(context.Background(), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	if _, e = tx.Exec(`SELECT id FROM projects WHERE id=$1 FOR UPDATE`, f.ProjectID); e != nil {
		t.Fatal(e)
	}
	done := make(chan int, 1)
	go func() {
		req := httptest.NewRequest("POST", f.Prefix()+"/github/bindings/"+bid.String()+"/sync", strings.NewReader(`{}`))
		req.Header.Set("X-Test-Actor", f.OwnerID.String())
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		done <- w.Code
	}()
	select {
	case code := <-done:
		t.Fatalf("scope operation did not wait for project lock: %d", code)
	case <-time.After(50 * time.Millisecond):
	}
	if _, e = tx.Exec(`UPDATE project_members SET is_active=false WHERE project_id=$1 AND user_id=$2`, f.ProjectID, f.OwnerID); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	select {
	case code := <-done:
		if code != 404 {
			t.Fatalf("removed admin queued request after lock wait: %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("sync did not finish after lock release")
	}
	var count int
	if e = f.DB.SQL.QueryRow(`SELECT count(*) FROM github_sync_requests`).Scan(&count); e != nil || count != 0 {
		t.Fatal("unauthorized queued job was committed")
	}
}

func TestQualityRepairsUsePolicyCommandsAndIdempotentAtomicLinks(t *testing.T) {
	f := testutil.New(t)
	bid := insertBinding(t, f)
	h := newHandler(f.Deps, Config{})
	p := automation.DefaultPolicy()
	p.Enabled = true
	p.MinIntervalSeconds = 0
	p.AuthorizedBy = f.OwnerID
	config, _ := json.Marshal(p)
	f.Exec(t, `INSERT INTO automation_policies(project_id,workspace_id,authorized_by,enabled,config) VALUES($1,$2,$3,true,$4::jsonb)`, f.ProjectID, f.WorkspaceID, f.OwnerID, string(config))
	createReport := func(rule string) uuid.UUID {
		id := uuid.New()
		finding := Finding{ID: rule, Dimension: "code", Rule: rule, Severity: "high", Title: "Repair unsafe query", Description: "Known defect from the pinned SARIF artifact", Path: "query.go", Line: 12, Status: "failed", Actionable: true, Evidence: map[string]any{"artifact_id": 91, "artifact_sha256": "immutable-digest"}}
		findings, _ := json.Marshal([]Finding{finding})
		f.Exec(t, `INSERT INTO quality_reports(id,workspace_id,project_id,binding_id,repository,commit_sha,source_revision,source_at,dimensions,findings,evidence) VALUES($1,$2,$3,$4,'org/repo',$5,$6,now(),'{}',$7::jsonb,'{"documents":[]}')`, id, f.WorkspaceID, f.ProjectID, bid, strings.Repeat("a", 40), rule, string(findings))
		f.Exec(t, `UPDATE github_bindings SET latest_report_id=$2,latest_source_at=now() WHERE id=$1`, bid, id)
		return id
	}
	reportID := createReport("unsafe-query")
	if e := h.applyRepairs(context.Background(), reportID); e != nil {
		t.Fatal(e)
	}
	var itemID, runID uuid.UUID
	var state string
	if e := f.DB.SQL.QueryRow(`SELECT (a.item_ids->>0)::uuid,a.run_id,a.status FROM quality_repair_actions a WHERE report_id=$1`, reportID).Scan(&itemID, &runID, &state); e != nil || itemID == uuid.Nil || runID == uuid.Nil || state != "applied" {
		t.Fatalf("policy command did not create and link task: %s %s %s %v", itemID, runID, state, e)
	}
	var events int
	if e := f.DB.SQL.QueryRow(`SELECT count(*) FROM work_item_history WHERE work_item_id=$1`, itemID).Scan(&events); e != nil || events < 1 {
		t.Fatalf("repair bypassed command history/outbox: %d %v", events, e)
	}
	if e := h.applyRepairs(context.Background(), reportID); e != nil {
		t.Fatal(e)
	}
	var count int
	if e := f.DB.SQL.QueryRow(`SELECT count(*) FROM work_items WHERE project_id=$1`, f.ProjectID).Scan(&count); e != nil || count != 1 {
		t.Fatalf("repair replay created duplicate task: %d %v", count, e)
	}
	newRevision := uuid.New()
	f.Exec(t, `INSERT INTO quality_reports(id,workspace_id,project_id,binding_id,repository,commit_sha,source_revision,source_at,dimensions,findings,evidence) SELECT $1,workspace_id,project_id,binding_id,repository,commit_sha,'updated-document-evidence',now(),dimensions,findings,evidence FROM quality_reports WHERE id=$2`, newRevision, reportID)
	f.Exec(t, `UPDATE github_bindings SET latest_report_id=$2 WHERE id=$1`, bid, newRevision)
	if e := h.applyRepairs(context.Background(), newRevision); e != nil {
		t.Fatal(e)
	}
	if e := f.DB.SQL.QueryRow(`SELECT count(*) FROM work_items WHERE project_id=$1`, f.ProjectID).Scan(&count); e != nil || count != 1 {
		t.Fatalf("unchanged immutable finding duplicated task after document refresh: %d %v", count, e)
	}
	f.Exec(t, `UPDATE github_installations SET revoked_at=now() WHERE installation_id=5`)
	revokedReport := createReport("second-defect")
	if e := h.applyRepairs(context.Background(), revokedReport); e != nil {
		t.Fatal(e)
	}
	if e := f.DB.SQL.QueryRow(`SELECT count(*) FROM work_items WHERE project_id=$1`, f.ProjectID).Scan(&count); e != nil || count != 1 {
		t.Fatalf("revoked installation produced automatic actions: %d %v", count, e)
	}
	if e := f.DB.SQL.QueryRow(`SELECT status FROM quality_repair_actions WHERE report_id=$1`, revokedReport).Scan(&state); e != nil || state != "revoked" {
		t.Fatalf("revoked finding status was not preserved: %s %v", state, e)
	}
}

func TestInstallationCallbackStateCannotReplayOrBindUnverifiedRepositories(t *testing.T) {
	f := testutil.New(t)
	var exchanges atomic.Int32
	var providerCalls atomic.Int32
	var expectedChallenge string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/login/oauth/access_token":
			exchanges.Add(1)
			var request map[string]string
			if e := json.NewDecoder(r.Body).Decode(&request); e != nil {
				t.Error(e)
			}
			challenge := sha256.Sum256([]byte(request["code_verifier"]))
			if len(request["code_verifier"]) != 43 || base64.RawURLEncoding.EncodeToString(challenge[:]) != expectedChallenge {
				t.Error("OAuth code exchange did not prove the initiating PKCE challenge")
			}
			fmt.Fprint(w, `{"access_token":"independent-user-proof"}`)
		case "/user/installations/5/repositories":
			fmt.Fprint(w, `{"repositories":[{"id":99,"full_name":"org/repo","permissions":{"admin":true}}]}`)
		case "/app/installations/5":
			fmt.Fprint(w, `{"id":5,"account":{"login":"org"}}`)
		case "/app/installations/5/access_tokens":
			fmt.Fprint(w, `{"token":"scoped-installation-token"}`)
		case "/repos/org/repo":
			fmt.Fprint(w, `{"id":99,"full_name":"org/repo"}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	config := providerConfig(t, server.URL)
	private := f.Router(func(r *gin.RouterGroup, d platform.Dependencies) { RegisterWithConfig(r, d, config) })
	connected := testutil.Object(t, testutil.Request(t, private, f.OwnerID, "POST", f.Prefix()+"/github/connect", map[string]any{}, 201))
	installURL, e := url.Parse(connected["install_url"].(string))
	if e != nil {
		t.Fatal(e)
	}
	state := installURL.Query().Get("state")
	public := gin.New()
	RegisterPublicWithConfig(public.Group("/api/v1"), f.Deps, config)
	setup := httptest.NewRecorder()
	public.ServeHTTP(setup, httptest.NewRequest("GET", "/api/v1/github/callback?installation_id=5&setup_action=install&state="+url.QueryEscape(state), nil))
	if setup.Code != 303 || exchanges.Load() != 0 {
		t.Fatalf("setup URL did not start a distinct OAuth authorization: %d", setup.Code)
	}
	authorizeURL, e := url.Parse(setup.Header().Get("Location"))
	if e != nil {
		t.Fatal(e)
	}
	expectedChallenge = authorizeURL.Query().Get("code_challenge")
	if authorizeURL.Path != "/login/oauth/authorize" || authorizeURL.Query().Get("code_challenge_method") != "S256" || len(expectedChallenge) != 43 {
		t.Fatalf("setup omitted the bounded OAuth/PKCE state: %s", authorizeURL.Path)
	}
	t.Run("legacy_code_callback_rejected_without_consuming_state", func(t *testing.T) {
		legacy := httptest.NewRecorder()
		public.ServeHTTP(legacy, httptest.NewRequest("GET", "/api/v1/github/callback?installation_id=5&code=test-code&state="+url.QueryEscape(state), nil))
		if legacy.Code != 400 || providerCalls.Load() != 0 {
			t.Fatalf("Legacy callback reached GitHub without the setup PKCE state: status=%d provider_calls=%d body=%s", legacy.Code, providerCalls.Load(), legacy.Body.String())
		}
		hash := sha256.Sum256([]byte(state))
		var available bool
		if e := f.DB.SQL.QueryRow(`SELECT consumed_at IS NULL AND expires_at>now() FROM github_connect_states WHERE state_hash=$1`, hex.EncodeToString(hash[:])).Scan(&available); e != nil || !available {
			t.Fatalf("Rejected legacy callback consumed the valid connection state: available=%v error=%v", available, e)
		}
		var grants int
		if e := f.DB.SQL.QueryRow(`SELECT count(*) FROM github_installation_grants WHERE project_id=$1`, f.ProjectID).Scan(&grants); e != nil || grants != 0 {
			t.Fatalf("Rejected legacy callback persisted an installation grant: count=%d error=%v", grants, e)
		}
	})
	// The documented OAuth callback contains code/state, not installation_id.
	callback := "/api/v1/github/callback?code=test-code&state=" + url.QueryEscape(authorizeURL.Query().Get("state"))
	first := httptest.NewRecorder()
	public.ServeHTTP(first, httptest.NewRequest("GET", callback, nil))
	if first.Code != 303 || !strings.Contains(first.Header().Get("Location"), "/automation?tab=quality&github=connected") {
		t.Fatalf("installation callback failed: %d %s", first.Code, first.Body.String())
	}
	testutil.Request(t, private, f.OwnerID, "POST", f.Prefix()+"/github/bindings", map[string]any{"installation_id": 5, "repository": "org/not-granted"}, 403)
	bound := testutil.Object(t, testutil.Request(t, private, f.OwnerID, "POST", f.Prefix()+"/github/bindings", map[string]any{"installation_id": 5, "repository": "org/repo"}, 201))
	if bound["repository_id"] != float64(99) {
		t.Fatalf("wrong repository grant: %#v", bound)
	}
	second := httptest.NewRecorder()
	public.ServeHTTP(second, httptest.NewRequest("GET", callback, nil))
	if second.Code != 400 || exchanges.Load() != 1 {
		t.Fatalf("callback state replayed OAuth: %d %d", second.Code, exchanges.Load())
	}
	info := testutil.Object(t, testutil.Request(t, private, f.OwnerID, "GET", f.Prefix()+"/github", nil, 200))
	raw, _ := json.Marshal(info)
	if strings.Contains(string(raw), "independent-user-proof") || strings.Contains(string(raw), "scoped-installation-token") {
		t.Fatal("credentials leaked through installation response")
	}
}

func TestReconciliationBackfillsPaginatedRunsPullRequestsAndCommits(t *testing.T) {
	f := testutil.New(t)
	bid := insertBinding(t, f)
	sha := strings.Repeat("a", 40)
	at := time.Now().UTC().Format(time.RFC3339)
	var runPages atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/app/installations/5":
			fmt.Fprint(w, `{"id":5}`)
		case "/app/installations/5/access_tokens":
			fmt.Fprint(w, `{"token":"reconciliation-installation-token"}`)
		case "/repos/org/repo/actions/runs":
			runPages.Add(1)
			id := 41
			if r.URL.Query().Get("page") == "1" {
				w.Header().Set("Link", `<https://api.github.com/ignored>; rel="next"`)
			} else {
				id = 42
			}
			fmt.Fprintf(w, `{"workflow_runs":[{"id":%d,"head_sha":%q,"run_attempt":2,"status":"completed","conclusion":"success","updated_at":%q}]}`, id, sha, at)
		case "/repos/org/repo/pulls":
			fmt.Fprintf(w, `[{"number":7,"head":{"sha":%q},"updated_at":%q}]`, sha, at)
		case "/repos/org/repo/commits":
			fmt.Fprintf(w, `[{"sha":%q,"commit":{"committer":{"date":%q}}}]`, sha, at)
		default:
			t.Errorf("unexpected reconciliation request: %s", r.URL.String())
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	h := newHandler(f.Deps, providerConfig(t, server.URL))
	if e := h.reconcileTask(context.Background(), nil); e != nil {
		t.Fatal(e)
	}
	if e := h.reconcileTask(context.Background(), nil); e != nil {
		t.Fatal(e)
	}
	var count int
	if e := f.DB.SQL.QueryRow(`SELECT count(*) FROM github_sync_requests WHERE binding_id=$1`, bid).Scan(&count); e != nil || count != 4 || runPages.Load() != 4 {
		t.Fatalf("compensation did not page/deduplicate all evidence: %d %d %v", count, runPages.Load(), e)
	}
	if e := f.DB.SQL.QueryRow(`SELECT count(*) FROM outbox_events WHERE topic='quality.sync'`).Scan(&count); e != nil || count != 4 {
		t.Fatalf("reconciliation did not durably publish exactly one task per source: %d %v", count, e)
	}
	f.Exec(t, `INSERT INTO project_revisions(project_id,workspace_id,revision) VALUES($1,$2,1) ON CONFLICT(project_id) DO UPDATE SET revision=project_revisions.revision+1`, f.ProjectID, f.WorkspaceID)
	if e := h.reconcileTask(context.Background(), nil); e != nil {
		t.Fatal(e)
	}
	if e := f.DB.SQL.QueryRow(`SELECT count(*) FROM github_sync_requests WHERE binding_id=$1`, bid).Scan(&count); e != nil || count != 8 {
		t.Fatalf("new local document revision did not refresh unchanged repository evidence: %d %v", count, e)
	}
}

func scenarioQualityFixture(t *testing.T, f *testutil.Fixture) (uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	bid := insertBinding(t, f)
	story := f.Issue(t, f.ProjectID, f.StateID, f.OwnerID, "Source Story", 1)
	f.Exec(t, `UPDATE work_items SET requirement_type='story' WHERE id=$1`, story)
	page, scenario, report := uuid.New(), uuid.New(), uuid.New()
	f.Exec(t, `INSERT INTO pages(id,workspace_id,project_id,owner_id,name) VALUES($1,$2,$3,$4,'Source PRD')`, page, f.WorkspaceID, f.ProjectID, f.OwnerID)
	f.Exec(t, `INSERT INTO business_scenarios(id,workspace_id,project_id,story_id,body,created_by,updated_by) VALUES($1,$2,$3,$4,'{"name":"Source scenario"}',$5,$5)`, scenario, f.WorkspaceID, f.ProjectID, story, f.OwnerID)
	f.Exec(t, `INSERT INTO business_scenario_sources(scenario_id,workspace_id,page_id) VALUES($1,$2,$3)`, scenario, f.WorkspaceID, page)
	ev, _ := json.Marshal(map[string]any{"documents": []documentSource{{ID: scenario.String(), Kind: "scenario", Version: 1, Name: "Scenario-derived evidence"}}})
	f.Exec(t, `INSERT INTO quality_reports(id,workspace_id,project_id,binding_id,repository,commit_sha,source_revision,source_at,dimensions,evidence) VALUES($1,$2,$3,$4,'org/repo',$5,'scenario-source',now(),'{}',$6::jsonb)`, report, f.WorkspaceID, f.ProjectID, bid, strings.Repeat("a", 40), string(ev))
	return story, page, report
}

func TestQualityScenarioArchiveAndReclassification(t *testing.T) {
	f := testutil.New(t)
	story, page, report := scenarioQualityFixture(t, f)
	r := f.Router(Register)
	endpoint := f.Prefix() + "/quality/reports/" + report.String()
	testutil.Request(t, r, f.MemberID, "GET", endpoint, nil, 200)
	f.Exec(t, `UPDATE pages SET archived_at=now() WHERE id=$1`, page)
	testutil.Request(t, r, f.MemberID, "GET", endpoint, nil, 404)
	f.Exec(t, `UPDATE pages SET archived_at=NULL WHERE id=$1`, page)
	f.Exec(t, `UPDATE work_items SET requirement_type=NULL WHERE id=$1`, story)
	testutil.Request(t, r, f.MemberID, "GET", endpoint, nil, 404)
}

func TestQualitySourceReadLockOrder(t *testing.T) {
	for _, kind := range []string{"story", "page"} {
		t.Run(kind, func(t *testing.T) {
			f := testutil.New(t)
			story, page, report := scenarioQualityFixture(t, f)
			r := f.Router(Register)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			tx, e := f.DB.SQL.BeginTx(ctx, nil)
			if e != nil {
				t.Fatal(e)
			}
			defer tx.Rollback()
			var locker int
			if e = tx.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&locker); e != nil {
				t.Fatal(e)
			}
			if kind == "story" {
				if e = workitems.LockProject(ctx, tx, f.ProjectID); e != nil {
					t.Fatal(e)
				}
				_, e = tx.ExecContext(ctx, `SELECT id FROM work_items WHERE id=$1 FOR UPDATE`, story)
			} else {
				_, e = tx.ExecContext(ctx, `SELECT id FROM pages WHERE id=$1 FOR UPDATE`, page)
			}
			if e != nil {
				t.Fatal(e)
			}
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				req := httptest.NewRequest("GET", f.Prefix()+"/quality/reports/"+report.String(), nil)
				req.Header.Set("X-Test-Actor", f.MemberID.String())
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				done <- w
			}()
			blocked := false
			until := time.Now().Add(3 * time.Second)
			for time.Now().Before(until) {
				if e = f.DB.SQL.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, locker).Scan(&blocked); e != nil {
					t.Fatal(e)
				}
				if blocked {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			if !blocked {
				t.Fatalf("reader did not wait for %s source", kind)
			}
			if kind == "story" {
				_, e = tx.ExecContext(ctx, `UPDATE work_items SET description_html='<p>Changed while report waited</p>',version=version+1 WHERE id=$1`, story)
			} else {
				_, e = tx.ExecContext(ctx, `UPDATE pages SET content_html='<p>Changed while report waited</p>',version=version+1 WHERE id=$1`, page)
			}
			if e != nil {
				t.Fatalf("source edit hit reversed-lock deadlock: %v", e)
			}
			if e = tx.Commit(); e != nil {
				t.Fatal(e)
			}
			select {
			case response := <-done:
				if response.Code != 200 {
					t.Fatalf("reader did not resume after ordered source edit: %d %s", response.Code, response.Body.String())
				}
			case <-ctx.Done():
				t.Fatal("reader blocked after source commit")
			}
		})
	}
}

func TestGitHubCallbackAndSyncRejectRevokedInitiatingSession(t *testing.T) {
	f := testutil.New(t)
	bid := insertBinding(t, f)
	sessionID := uuid.New()
	f.Exec(t, `INSERT INTO sessions(id,user_id,token_hash,csrf_hash,expires_at) VALUES($1,$2,$3,'test-csrf',now()+interval '1 hour')`, sessionID, f.OwnerID, uuid.NewString())
	var providerCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { providerCalls.Add(1); w.WriteHeader(500) }))
	defer server.Close()
	config := providerConfig(t, server.URL)
	private := gin.New()
	private.Use(func(c *gin.Context) {
		c.Set(httpapi.ActorKey, identity.Actor{UserID: f.OwnerID, SessionID: sessionID})
		c.Next()
	})
	RegisterWithConfig(private.Group("/api/v1"), f.Deps, config)
	connected := testutil.Object(t, testutil.Request(t, private, f.OwnerID, "POST", f.Prefix()+"/github/connect", map[string]any{}, 201))
	installURL, e := url.Parse(connected["install_url"].(string))
	if e != nil {
		t.Fatal(e)
	}
	f.Exec(t, `UPDATE sessions SET revoked_at=now() WHERE id=$1`, sessionID)
	public := gin.New()
	RegisterPublicWithConfig(public.Group("/api/v1"), f.Deps, config)
	callback := httptest.NewRecorder()
	public.ServeHTTP(callback, httptest.NewRequest("GET", "/api/v1/github/callback?code=unused&state="+url.QueryEscape(installURL.Query().Get("state")+".5"), nil))
	if callback.Code != 401 || providerCalls.Load() != 0 {
		t.Fatalf("logged-out installation callback reached provider: status=%d calls=%d body=%s", callback.Code, providerCalls.Load(), callback.Body.String())
	}
	testutil.Request(t, private, f.OwnerID, "POST", f.Prefix()+"/github/bindings/"+bid.String()+"/sync", map[string]any{}, 401)
	var queued int
	if e = f.DB.SQL.QueryRow(`SELECT count(*) FROM github_sync_requests`).Scan(&queued); e != nil || queued != 0 {
		t.Fatalf("revoked session queued integration work: %d %v", queued, e)
	}
}
