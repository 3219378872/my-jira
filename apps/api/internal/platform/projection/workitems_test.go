package projection_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"my-jira/apps/api/internal/application"
	"my-jira/apps/api/internal/foundation"
	"my-jira/apps/api/internal/platform/projection"
	"my-jira/apps/api/internal/support/testutil"
)

func request(t *testing.T, router *gin.Engine, path, cookie, bearer string, status int) map[string]any {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	if cookie != "" {
		req.Header.Set("Cookie", "mj_session="+cookie)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	if response.Code != status {
		t.Fatalf("GET %s: %d, want %d: %s", path, response.Code, status, response.Body.String())
	}
	var result map[string]any
	decoder := json.NewDecoder(response.Body)
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil {
		t.Fatal(err)
	}
	return result
}

func hash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func TestProjectionUsesCurrentBrowserAndTokenScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	f := testutil.New(t)
	router := application.Router(f.Deps, foundation.Config{})
	issue := f.Issue(t, f.ProjectID, f.StateID, f.MemberID, "Member work", 1)
	guestIssue := f.Issue(t, f.ProjectID, f.StateID, f.GuestID, "Guest work", 2)
	hidden := f.Issue(t, f.OtherProjectID, f.OtherStateID, f.OwnerID, "Private work", 1)
	f.Exec(t, "UPDATE work_items SET sequence_id=9007199254740993,description_html='<p>Full content</p>' WHERE id=$1", issue)
	f.Exec(t, "INSERT INTO work_item_assignees(id,workspace_id,project_id,work_item_id,user_id) VALUES($1,$2,$3,$4,$5)", uuid.New(), f.WorkspaceID, f.ProjectID, issue, f.MemberID)
	cookie, bearer, guestCookie := uuid.NewString(), "mjt_"+uuid.NewString(), uuid.NewString()
	for user, value := range map[uuid.UUID]string{f.MemberID: cookie, f.GuestID: guestCookie} {
		f.Exec(t, "INSERT INTO sessions(id,user_id,token_hash,csrf_hash,expires_at) VALUES($1,$2,$3,$4,now()+interval '1 hour')", uuid.New(), user, hash(value), hash("unused-read-token"))
	}
	tokenID := uuid.New()
	f.Exec(t, "INSERT INTO api_tokens(id,user_id,workspace_id,name,token_hash,prefix) VALUES($1,$2,$3,'Projection test',$4,'mjt_test')", tokenID, f.MemberID, f.WorkspaceID, hash(bearer))
	detail := f.Prefix() + "/issues/" + issue.String()
	plain := testutil.Object(t, request(t, router, detail, cookie, "", 200))
	if plain["description_html"] == nil || plain["state_detail"] == nil {
		t.Fatal("ordinary browser representation changed")
	}
	selected := testutil.Object(t, request(t, router, detail+"?fields=name,sequence_id&expand=state,assignees,estimate_point", cookie, "", 200))
	if len(selected) != 6 || selected["id"] != issue.String() || selected["sequence_id"] != json.Number("9007199254740993") || selected["estimate_point_detail"] != nil {
		t.Fatalf("incorrect field selection or integer precision: %#v", selected)
	}
	if selected["state_detail"].(map[string]any)["group"] != "unstarted" || len(selected["assignee_details"].([]any)) != 1 {
		t.Fatal("selected associations were lost")
	}
	compact := testutil.Object(t, request(t, router, detail+"?expand=none", "", bearer, 200))
	if compact["name"] != "Member work" || compact["state_detail"] != nil || compact["description_binary"] != nil {
		t.Fatal("compact token representation was not safely projected")
	}
	for _, query := range []string{"fields=password_hash", "fields=description_binary", "fields=object_key", "fields=state_detail", "fields=", "fields=id&fields=name", "expand=project.settings", "expand=none,state", "expand=*"} {
		request(t, router, detail+"?"+query, cookie, "", 400)
	}
	list := request(t, router, f.WorkspacePrefix()+"/issues?fields=name&expand=project&limit=1", "", bearer, 200)
	if list["pagination"].(map[string]any)["has_more"] != true || len(testutil.Array(t, list)) != 1 {
		t.Fatal("pagination metadata did not survive field selection")
	}
	grouped := request(t, router, f.Prefix()+"/issues?fields=name&expand=state&group_by=priority&sub_group_by=created_by", cookie, "", 200)
	if grouped["total_items"] != json.Number("2") || grouped["pagination"] == nil {
		t.Fatal("grouped envelope lost counts or pagination")
	}
	groups := testutil.Array(t, grouped)
	for _, raw := range groups {
		for _, child := range raw.(map[string]any)["groups"].([]any) {
			for _, item := range child.(map[string]any)["items"].([]any) {
				if len(item.(map[string]any)) != 3 {
					t.Fatal("nested group item escaped projection")
				}
			}
		}
	}
	// Invalid requested fields cannot replace the domain's access-denial response.
	request(t, router, detail+"?fields=password_hash", "", "", 401)
	request(t, router, detail+"?fields=password_hash", guestCookie, "", 404)
	request(t, router, f.Prefix()+"/issues/"+guestIssue.String()+"?fields=name&expand=project", guestCookie, "", 200)
	hiddenPath := f.WorkspacePrefix() + "/projects/" + f.OtherProjectID.String() + "/issues/" + hidden.String()
	request(t, router, hiddenPath+"?fields=password_hash&expand=project", "", bearer, 404)
	request(t, router, strings.Replace(detail, f.WorkspaceID.String(), uuid.NewString(), 1)+"?fields=name", "", bearer, 404)
	f.Exec(t, "UPDATE project_members SET is_active=false WHERE project_id=$1 AND user_id=$2", f.ProjectID, f.MemberID)
	request(t, router, detail+"?fields=name&expand=project", "", bearer, 404)
	f.Exec(t, "UPDATE api_tokens SET revoked_at=now() WHERE id=$1", tokenID)
	request(t, router, f.WorkspacePrefix()+"/issues?fields=name", "", bearer, 401)
}

func TestProjectionWhitelistsNestedDataAndBoundsBufferedResponses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const path = "/api/v1/workspaces/:workspaceID/projects/:projectID/issues/:issueID"
	router := gin.New()
	router.Use(gin.RecoveryWithWriter(io.Discard), projection.WorkItems())
	router.GET(path, func(c *gin.Context) {
		switch c.Query("scenario") {
		case "panic":
			panic("synthetic projection panic")
		case "large":
			c.JSON(200, gin.H{"data": gin.H{"id": "safe", "name": strings.Repeat("x", 17<<20)}})
		default:
			c.Header("Content-Length", "999999")
			c.JSON(200, gin.H{"data": gin.H{"id": "safe", "name": "Name", "sequence_id": json.Number("9007199254740993"), "description_binary": "internal", "project_detail": gin.H{"id": "project", "name": "Project", "settings": gin.H{"secret": "internal"}}, "assignee_details": []any{gin.H{"id": "user", "display_name": "Reader", "password_hash": "internal", "email": "private@example.test"}}}})
		}
	})
	url := "/api/v1/workspaces/w/projects/p/issues/i"
	result := testutil.Object(t, request(t, router, url+"?fields=name,sequence_id&expand=project,assignees", "", "", 200))
	if len(result) != 5 || result["sequence_id"] != json.Number("9007199254740993") || len(result["project_detail"].(map[string]any)) != 2 || len(result["assignee_details"].([]any)[0].(map[string]any)) != 2 {
		t.Fatalf("nested secret fields escaped the whitelist: %#v", result)
	}
	request(t, router, url+"?fields=id&scenario=large", "", "", 413)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest("GET", url+"?fields=id&scenario=panic", nil))
	if response.Code != 500 {
		t.Fatal("buffering swallowed panic recovery")
	}
}
