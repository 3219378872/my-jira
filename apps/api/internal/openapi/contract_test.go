package openapi_test

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"my-jira/apps/api/internal/application"
	"my-jira/apps/api/internal/foundation"
	"my-jira/apps/api/internal/openapi"
	"my-jira/apps/api/internal/platform"
)

func contract(t *testing.T) (*gin.Engine, map[string]any, []byte) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := application.Router(platform.Dependencies{}, foundation.Config{})
	document, err := openapi.Build(router.Routes())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err = json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	return router, decoded, append(raw, '\n')
}

func TestEveryRegisteredRouteHasTypedOpenAPIContract(t *testing.T) {
	router, document, _ := contract(t)
	paths := document["paths"].(map[string]any)
	count := 0
	operationIDs := map[string]bool{}
	for path, raw := range paths {
		for method, value := range raw.(map[string]any) {
			operation := value.(map[string]any)
			id := operation["operationId"].(string)
			if operationIDs[id] {
				t.Fatalf("duplicate operation id %s", id)
			}
			operationIDs[id] = true
			if operation["summary"] == "" || operation["security"] == nil {
				t.Fatalf("incomplete %s %s", method, path)
			}
			responses := operation["responses"].(map[string]any)
			if responses["409"] == nil || responses["404"] == nil || responses["403"] == nil {
				t.Fatalf("missing scope/conflict contract for %s %s", method, path)
			}
			parameters, _ := operation["parameters"].([]any)
			seen := map[string]bool{}
			for _, entry := range parameters {
				p := entry.(map[string]any)
				key := p["in"].(string) + ":" + p["name"].(string)
				if seen[key] {
					t.Fatalf("duplicate parameter %s on %s", key, path)
				}
				seen[key] = true
				if p["in"] == "path" && (p["required"] != true || !strings.Contains(path, "{"+p["name"].(string)+"}")) {
					t.Fatalf("invalid path parameter on %s", path)
				}
			}
			count++
		}
	}
	want := 0
	for _, route := range router.Routes() {
		if strings.HasPrefix(route.Path, "/api/v1/") {
			want++
		}
	}
	if count != want {
		t.Fatalf("documented %d operations but registered %d", count, want)
	}
	if _, err := openapi.Build(append(router.Routes(), gin.RouteInfo{Method: "GET", Path: "/api/v1/undocumented"})); err == nil {
		t.Fatal("a new undocumented route silently produced a complete contract")
	}
	schemas := document["components"].(map[string]any)["schemas"].(map[string]any)
	var checkReferences func(any)
	checkReferences = func(value any) {
		switch typed := value.(type) {
		case map[string]any:
			if reference, ok := typed["$ref"].(string); ok {
				name := strings.TrimPrefix(reference, "#/components/schemas/")
				if name == reference || schemas[name] == nil {
					t.Fatalf("unresolved schema reference %s", reference)
				}
			}
			for _, child := range typed {
				checkReferences(child)
			}
		case []any:
			for _, child := range typed {
				checkReferences(child)
			}
		}
	}
	checkReferences(document)
	for _, name := range []string{"WorkItemCreate", "WorkItemPatch", "PagePatch", "ProjectCreate", "AnalyticsQuery", "CommentInput", "AssetUpload", "ServicePatch"} {
		value := schemas[name].(map[string]any)
		if value["properties"] == nil && value["anyOf"] == nil {
			t.Fatalf("%s fell back to an untyped object", name)
		}
	}
}

func TestOpenAPISchemasValidateVersionedRequests(t *testing.T) {
	_, document, _ := contract(t)
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	const location = "https://contract.example.test/openapi.json"
	if err := compiler.AddResource(location, document); err != nil {
		t.Fatal(err)
	}
	schemas := document["components"].(map[string]any)["schemas"].(map[string]any)
	compiled := map[string]*jsonschema.Schema{}
	for name := range schemas {
		schema, err := compiler.Compile(location + "#/components/schemas/" + name)
		if err != nil {
			t.Fatalf("invalid %s schema: %v", name, err)
		}
		compiled[name] = schema
	}
	for _, example := range []struct {
		name, input string
		valid       bool
	}{
		{"WorkItemPatch", `{"name":"Updated","version":3,"assignee_ids":["f1d1a54f-3092-40a1-b45d-26ff2f922b25"]}`, true},
		{"WorkItemPatch", `{"name":"Missing version"}`, false},
		{"WorkItemPatch", `{"version":0,"name":"Invalid version"}`, false},
		{"WorkItemPatch", `{"version":1,"state_id":"not-a-uuid"}`, false},
		{"WorkItemPatch", `{"version":1,"target_date":"2026-13-01"}`, false},
		{"WorkItemPatch", `{"version":1,"password_hash":"unexpected"}`, false},
		{"PagePatch", `{"name":"New title"}`, false},
		{"PagePatch", `{"name":"New title","version":2}`, true},
		{"PagePatch", `{"is_locked":true}`, true},
		{"PageContentInput", `{"version":1,"content_json":{"type":"doc","content":[]}}`, true},
		{"PageContentInput", `{"content_html":"<p>missing version</p>"}`, false},
		{"BulkWorkItems", `{"ids":["f1d1a54f-3092-40a1-b45d-26ff2f922b25"],"changes":{"priority":"high"},"versions":{"f1d1a54f-3092-40a1-b45d-26ff2f922b25":2}}`, true},
		{"AnalyticsQuery", `{"x_axis":"state_group","project_id":"f1d1a54f-3092-40a1-b45d-26ff2f922b25","from":"2026-01-01"}`, true},
		{"AnalyticsQuery", `{"project_id":["f1d1a54f-3092-40a1-b45d-26ff2f922b25"]}`, false},
		{"ServicePatch", `{"host":"smtp.example.test","port":587,"password":null}`, true},
		{"ServicePatch", `{"port":587.5}`, false},
		{"StateCreate", `{"color":"#334455"}`, false},
		{"StateInput", `{"color":"#334455"}`, true},
		{"Comment", `{"id":"f1d1a54f-3092-40a1-b45d-26ff2f922b25","body_html":"<p>Public comment</p>","author":{"display_name":"Reader","avatar_url":""}}`, true},
		{"PreferenceResult", `{"scope":"home","value":{}}`, true},
		{"AssetBatchEntry", `{"entity_type":"work_item","entity_id":"f1d1a54f-3092-40a1-b45d-26ff2f922b25","assets":[]}`, true},
		{"AssetBatchEntry", `{"entity_type":"page","entity_id":"f1d1a54f-3092-40a1-b45d-26ff2f922b25","assets":null}`, false},
		{"AssetBatchEntry", `{"entity_type":"project","entity_id":"f1d1a54f-3092-40a1-b45d-26ff2f922b25","assets":[]}`, false},
	} {
		var input any
		if err := json.Unmarshal([]byte(example.input), &input); err != nil {
			t.Fatal(err)
		}
		err := compiled[example.name].Validate(input)
		if (err == nil) != example.valid {
			t.Fatalf("%s validation=%v, expected valid=%v for %s", example.name, err, example.valid, example.input)
		}
	}
}

func TestOpenAPIEndpointSecurityAndGeneratedArtifact(t *testing.T) {
	router, document, generated := contract(t)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest("GET", "/api/v1/openapi.json", nil))
	if response.Code != 200 {
		t.Fatalf("public contract endpoint: %d %s", response.Code, response.Body.String())
	}
	var served map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &served); err != nil {
		t.Fatal(err)
	}
	if served["openapi"] != "3.1.0" || served["data"] != nil {
		t.Fatal("the endpoint did not serve a raw OpenAPI document")
	}
	paths := document["paths"].(map[string]any)
	security := func(path, method string) string {
		raw, _ := json.Marshal(paths[path].(map[string]any)[method].(map[string]any)["security"])
		return string(raw)
	}
	if strings.Contains(security("/auth/me", "get"), "workspaceBearer") || strings.Contains(security("/workspaces/{workspaceID}/api-tokens", "post"), "workspaceBearer") {
		t.Fatal("account or credential creation incorrectly permits a workspace bearer")
	}
	if !strings.Contains(security("/workspaces/{workspaceID}/projects/{projectID}/issues/{issueID}", "patch"), "csrfHeader") || !strings.Contains(security("/workspaces/{workspaceID}/projects/{projectID}/issues/{issueID}", "patch"), "workspaceBearer") {
		t.Fatal("browser and workspace-token write contracts were not separated")
	}
	if security("/instance", "get") != "[]" {
		t.Fatal("public instance setup status was marked authenticated")
	}
	stored, err := os.ReadFile("../../../../docs/openapi.json")
	if err != nil {
		t.Fatal("Generate the artifact with go run ./cmd/openapi -output ../../docs/openapi.json: ", err)
	}
	if !bytes.Equal(stored, generated) {
		t.Fatal("docs/openapi.json is stale; regenerate it from the current router")
	}
}
