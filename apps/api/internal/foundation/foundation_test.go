package foundation

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"my-jira/apps/api/internal/platform"
	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/identity"
	"my-jira/apps/api/internal/platform/jobs"
	"my-jira/apps/api/migrations"
)

type fixture struct {
	db     *database.Database
	server *httptest.Server
	t      *testing.T
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL is required for isolated PostgreSQL integration tests")
	}
	u, err := url.Parse(raw)
	if err != nil || !strings.HasSuffix(strings.Trim(u.Path, "/"), "_test") {
		t.Fatal("TEST_DATABASE_URL must identify a database ending in _test")
	}
	bootstrap, err := sql.Open("pgx", raw)
	if err != nil {
		t.Fatal(err)
	}
	schema := "foundation_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = bootstrap.ExecContext(context.Background(), `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		bootstrap.ExecContext(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
		bootstrap.Close()
	})
	query := u.Query()
	query.Set("search_path", schema)
	u.RawQuery = query.Encode()
	db, err := database.Open(context.Background(), u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err = migrations.Apply(context.Background(), db.SQL); err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	deps := platform.Dependencies{DB: db, Policy: &identity.SQLPolicy{DB: db.SQL}, Jobs: jobs.Outbox{}}
	service := New(deps, Config{AppOrigin: "http://app.test"})
	router.Use(service.Middleware())
	service.RegisterPublic(router.Group("/api/v1"))
	service.Register(router.Group("/api/v1", service.RequireAuth()))
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	return &fixture{db, server, t}
}

type testClient struct {
	f    *fixture
	http *http.Client
	csrf string
}

func (f *fixture) client() *testClient {
	jar, _ := cookiejar.New(nil)
	client := &testClient{f: f, http: &http.Client{Jar: jar}}
	status, body := client.request("GET", "/auth/csrf", nil)
	if status != 200 {
		f.t.Fatalf("csrf %d: %v", status, body)
	}
	client.csrf = body["data"].(map[string]any)["csrf_token"].(string)
	return client
}
func (c *testClient) request(method, path string, input any) (int, map[string]any) {
	c.f.t.Helper()
	raw, _ := json.Marshal(input)
	req, _ := http.NewRequest(method, c.f.server.URL+"/api/v1"+path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://app.test")
	if method != "GET" {
		req.Header.Set("X-CSRF-Token", c.csrf)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.f.t.Fatal(err)
	}
	defer resp.Body.Close()
	body := map[string]any{}
	if resp.StatusCode != 204 {
		if err = json.NewDecoder(resp.Body).Decode(&body); err != nil {
			c.f.t.Fatal(err)
		}
	}
	if data, ok := body["data"].(map[string]any); ok {
		if token, ok := data["csrf_token"].(string); ok {
			c.csrf = token
		}
	}
	return resp.StatusCode, body
}
func (c *testClient) must(method, path string, input any, status int) map[string]any {
	c.f.t.Helper()
	got, body := c.request(method, path, input)
	if got != status {
		c.f.t.Fatalf("%s %s: got %d want %d: %v", method, path, got, status, body)
	}
	value, _ := body["data"].(map[string]any)
	return value
}
func (c *testClient) account(email string, setup bool) map[string]any {
	path := "/auth/register"
	if setup {
		path = "/instance/setup"
	}
	data := c.must("POST", path, map[string]any{"email": email, "password": "test-password-123!", "display_name": email, "instance_name": "Test"}, 201)
	return data["user"].(map[string]any)
}

func TestFoundationAuthenticationTenantAndDefaults(t *testing.T) {
	f := newFixture(t)
	owner := f.client()
	ownerUser := owner.account("owner@example.test", true)
	stranger := f.client()
	stranger.account("stranger@example.test", false)
	w := owner.must("POST", "/workspaces", map[string]any{"name": "Engineering", "slug": "engineering"}, 201)
	wid := w["id"].(string)
	p := owner.must("POST", "/workspaces/"+wid+"/projects", map[string]any{"name": "Secret", "identifier": "SEC", "network": "private"}, 201)
	pid := p["id"].(string)
	status, body := owner.request("GET", "/workspaces/"+wid+"/projects/"+pid+"/states", nil)
	if status != 200 || len(body["data"].([]any)) != 5 {
		t.Fatalf("default states: %d %v", status, body)
	}
	stranger.must("GET", "/workspaces/"+wid, nil, 404)
	stranger.must("GET", "/workspaces/"+wid+"/projects/"+pid, nil, 404)
	owner.csrf = "invalid"
	owner.must("PATCH", "/auth/me", map[string]any{"display_name": "unsafe"}, 403)
	status, body = owner.request("GET", "/auth/csrf", nil)
	if status != 200 {
		t.Fatal(body)
	}
	me := owner.must("GET", "/auth/me", nil, 200)
	if me["id"] != ownerUser["id"] || me["password_hash"] != nil {
		t.Fatalf("user serialization: %v", me)
	}
	owner.must("POST", "/auth/logout", nil, 204)
	owner.must("GET", "/auth/me", nil, 401)
}

func TestLastAdminConcurrentChangesAndMembershipRevocation(t *testing.T) {
	f := newFixture(t)
	first := f.client()
	first.account("first@example.test", true)
	second := f.client()
	second.account("second@example.test", false)
	w := first.must("POST", "/workspaces", map[string]any{"name": "Team", "slug": "team"}, 201)
	wid := w["id"].(string)
	secondMember := first.must("POST", "/workspaces/"+wid+"/members", map[string]any{"email": "second@example.test", "role": 20}, 201)
	status, list := first.request("GET", "/workspaces/"+wid+"/members", nil)
	if status != 200 {
		t.Fatal(list)
	}
	var firstMember string
	for _, raw := range list["data"].([]any) {
		m := raw.(map[string]any)
		if m["email"] == "first@example.test" {
			firstMember = m["id"].(string)
		}
	}
	var wait sync.WaitGroup
	codes := make(chan int, 2)
	for index, client := range []*testClient{first, second} {
		member := firstMember
		if index == 1 {
			member = secondMember["id"].(string)
		}
		wait.Add(1)
		go func(c *testClient, id string) {
			defer wait.Done()
			status, _ := c.request("PATCH", "/workspaces/"+wid+"/members/"+id, map[string]any{"role": 15})
			codes <- status
		}(client, member)
	}
	wait.Wait()
	close(codes)
	counts := map[int]int{}
	for code := range codes {
		counts[code]++
	}
	if counts[200] != 1 || counts[409] != 1 {
		t.Fatalf("concurrent admin results: %v", counts)
	}
	var count int
	if err := f.db.SQL.QueryRow(`SELECT count(*) FROM workspace_members WHERE workspace_id=$1 AND role=20 AND deleted_at IS NULL`, wid).Scan(&count); err != nil || count != 1 {
		t.Fatalf("admin invariant count=%d err=%v", count, err)
	}
	if _, err := f.db.SQL.Exec(`UPDATE workspace_members SET is_active=false WHERE workspace_id=$1 AND user_id=(SELECT id FROM users WHERE email='second@example.test')`, wid); err != nil {
		t.Fatal(err)
	}
	second.must("GET", "/workspaces/"+wid, nil, 404)
}

func TestOutboxRollbackAndDeduplication(t *testing.T) {
	f := newFixture(t)
	publisher := jobs.Outbox{}
	ctx := context.Background()
	err := f.db.WithinTx(ctx, func(q database.DBTX) error {
		if err := publisher.Publish(ctx, q, "email.send", map[string]string{"to": "test@example.test"}, "rollback"); err != nil {
			return err
		}
		return fmt.Errorf("force rollback")
	})
	if err == nil {
		t.Fatal("expected rollback")
	}
	for i := 0; i < 2; i++ {
		if err := publisher.Publish(ctx, f.db.SQL, "email.send", map[string]string{"to": "test@example.test"}, "once"); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := f.db.SQL.QueryRow(`SELECT count(*) FROM outbox_events`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("outbox count=%d err=%v", count, err)
	}
}
