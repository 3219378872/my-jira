// Package testutil creates isolated schemas only in an explicitly named test DB.
package testutil

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"my-jira/apps/api/internal/platform"
	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/httpapi"
	"my-jira/apps/api/internal/platform/identity"
	"my-jira/apps/api/internal/platform/jobs"
	"my-jira/apps/api/migrations"
)

type Fixture struct {
	DB                                                                                                    *database.Database
	Deps                                                                                                  platform.Dependencies
	WorkspaceID, OwnerID, MemberID, GuestID, OutsiderID, ProjectID, StateID, OtherProjectID, OtherStateID uuid.UUID
}

func New(t *testing.T) *Fixture {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL must explicitly name an isolated test database")
	}
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	name := strings.TrimPrefix(parsed.Path, "/")
	if !strings.Contains(strings.ToLower(name), "test") {
		t.Fatal("Refusing integration setup: database name must contain test")
	}
	ctx := context.Background()
	base, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := "myjira_features_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := base.SQL.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		base.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := base.SQL.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("remove owned test schema: %v", err)
		}
		base.Close()
	})
	params := parsed.Query()
	params.Set("search_path", schema)
	parsed.RawQuery = params.Encode()
	db, err := database.Open(ctx, parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := migrations.Apply(ctx, db.SQL); err != nil {
		t.Fatal(err)
	}
	f := &Fixture{DB: db}
	f.Deps = platform.Dependencies{DB: db, Policy: &identity.SQLPolicy{DB: db.SQL}, Jobs: jobs.Outbox{}}
	f.OwnerID = f.user(t, "owner")
	f.MemberID = f.user(t, "member")
	f.GuestID = f.user(t, "guest")
	f.OutsiderID = f.user(t, "outsider")
	f.WorkspaceID = uuid.New()
	f.Exec(t, "INSERT INTO workspaces(id,name,slug,owner_id) VALUES($1,'Test workspace',$2,$3)", f.WorkspaceID, "test-"+uuid.NewString(), f.OwnerID)
	for _, member := range []struct {
		id   uuid.UUID
		role int
	}{{f.OwnerID, 20}, {f.MemberID, 15}, {f.GuestID, 5}} {
		f.Exec(t, "INSERT INTO workspace_members(id,workspace_id,user_id,role) VALUES($1,$2,$3,$4)", uuid.New(), f.WorkspaceID, member.id, member.role)
	}
	f.ProjectID, f.StateID = f.Project(t, "MAIN")
	f.OtherProjectID, f.OtherStateID = f.Project(t, "HIDDEN")
	for _, member := range []struct {
		id   uuid.UUID
		role int
	}{{f.OwnerID, 20}, {f.MemberID, 15}, {f.GuestID, 5}} {
		f.Exec(t, "INSERT INTO project_members(id,workspace_id,project_id,user_id,role) VALUES($1,$2,$3,$4,$5)", uuid.New(), f.WorkspaceID, f.ProjectID, member.id, member.role)
	}
	f.Exec(t, "INSERT INTO project_members(id,workspace_id,project_id,user_id,role) VALUES($1,$2,$3,$4,20)", uuid.New(), f.WorkspaceID, f.OtherProjectID, f.OwnerID)
	return f
}

func (f *Fixture) user(t *testing.T, name string) uuid.UUID {
	id := uuid.New()
	f.Exec(t, "INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'not-a-real-login',$3)", id, name+uuid.NewString()+"@example.test", name)
	return id
}
func (f *Fixture) Exec(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, err := f.DB.SQL.ExecContext(context.Background(), query, args...); err != nil {
		t.Fatalf("fixture SQL: %v", err)
	}
}
func (f *Fixture) Project(t *testing.T, identifier string) (uuid.UUID, uuid.UUID) {
	pid, sid := uuid.New(), uuid.New()
	f.Exec(t, "INSERT INTO projects(id,workspace_id,name,identifier,network) VALUES($1,$2,$3,$3,'private')", pid, f.WorkspaceID, identifier)
	f.Exec(t, "INSERT INTO states(id,workspace_id,project_id,name,group_name,is_default) VALUES($1,$2,$3,'Todo','unstarted',true)", sid, f.WorkspaceID, pid)
	return pid, sid
}
func (f *Fixture) Issue(t *testing.T, projectID, stateID, creator uuid.UUID, name string, sequence int) uuid.UUID {
	id := uuid.New()
	f.Exec(t, "INSERT INTO work_items(id,workspace_id,project_id,state_id,created_by,updated_by,name,sequence_id) VALUES($1,$2,$3,$4,$5,$5,$6,$7)", id, f.WorkspaceID, projectID, stateID, creator, name, sequence)
	f.Exec(t, "UPDATE projects SET next_sequence=GREATEST(next_sequence,$2) WHERE id=$1", projectID, sequence+1)
	return id
}
func (f *Fixture) Prefix() string {
	return fmt.Sprintf("/api/v1/workspaces/%s/projects/%s", f.WorkspaceID, f.ProjectID)
}
func (f *Fixture) WorkspacePrefix() string { return "/api/v1/workspaces/" + f.WorkspaceID.String() }
func (f *Fixture) Router(registers ...func(*gin.RouterGroup, platform.Dependencies)) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		id, err := uuid.Parse(c.GetHeader("X-Test-Actor"))
		if err != nil {
			c.AbortWithStatus(401)
			return
		}
		c.Set(httpapi.ActorKey, identity.Actor{UserID: id})
		c.Next()
	})
	group := r.Group("/api/v1")
	for _, register := range registers {
		register(group, f.Deps)
	}
	return r
}
func Request(t *testing.T, r *gin.Engine, actor uuid.UUID, method, path string, input any, want int) map[string]any {
	t.Helper()
	var body []byte
	if input != nil {
		var err error
		body, err = json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-Actor", actor.String())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != want {
		t.Fatalf("%s %s: status %d, want %d; %s", method, path, w.Code, want, w.Body.String())
	}
	if w.Body.Len() == 0 {
		return nil
	}
	var result map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}
func Object(t *testing.T, envelope map[string]any) map[string]any {
	t.Helper()
	result, ok := envelope["data"].(map[string]any)
	if !ok {
		t.Fatalf("expected object data: %#v", envelope)
	}
	return result
}
func Array(t *testing.T, envelope map[string]any) []any {
	t.Helper()
	result, ok := envelope["data"].([]any)
	if !ok {
		t.Fatalf("expected array data: %#v", envelope)
	}
	return result
}
