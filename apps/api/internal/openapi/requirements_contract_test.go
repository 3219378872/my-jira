package openapi_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"my-jira/apps/api/internal/resources"
)

func requirementsCompiler(t *testing.T) (map[string]any, *jsonschema.Compiler) {
	t.Helper()
	_, document, _ := contract(t)
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	if err := compiler.AddResource("https://requirements.example.test/openapi.json", document); err != nil {
		t.Fatal(err)
	}
	return document, compiler
}

func validateRequirementSchema(t *testing.T, compiler *jsonschema.Compiler, name string, value any, want bool) {
	t.Helper()
	schema, err := compiler.Compile("https://requirements.example.test/openapi.json#/components/schemas/" + name)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var decoded any
	if err = json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	err = schema.Validate(decoded)
	if (err == nil) != want {
		t.Fatalf("%s schema valid=%v, want %v for %s: %v", name, err == nil, want, raw, err)
	}
}

func TestRequirementsContractMatchesVersionAndCommandInputs(t *testing.T) {
	_, compiler := requirementsCompiler(t)
	for _, example := range []struct {
		name, input string
		valid       bool
	}{
		{"AllocationWeight", `{"member_id":"00000000-0000-0000-0000-000000000001","weight":0}`, true},
		{"AllocationWeight", `{"member_id":"00000000-0000-0000-0000-000000000001","weight":1000001}`, false},
		{"PlanningCommand", `{"operation":"create","client_id":"story","fields":{"name":"A story","parent_id":"$epic","dependency_ids":["$prerequisite"]}}`, true},
		{"PlanningCommand", `{"operation":"create","id":"00000000-0000-0000-0000-000000000001","fields":{"name":"Caller supplied ID"}}`, false},
		{"PlanningCommand", `{"operation":"update","id":"00000000-0000-0000-0000-000000000001","fields":{"name":"Missing version"}}`, false},
		{"PlanningCommand", `{"operation":"delete","id":"00000000-0000-0000-0000-000000000001","version":3}`, true},
		{"ScenarioCreate", `{"story_id":"00000000-0000-0000-0000-000000000001","name":"Empty structured scenario"}`, true},
		{"ScenarioCopy", `{"version":2}`, true},
		{"ScenarioCopy", `{"name":"Missing source version"}`, false},
		{"ScenarioStep", `{"id":"00000000-0000-0000-0000-000000000001","kind":"end"}`, true},
		{"ScenarioStep", `{"id":"00000000-0000-0000-0000-000000000001","kind":"return","message":"reply"}`, false},
		{"RiskPatch", `{"status":"acknowledged","version":2}`, true},
		{"RiskPatch", `{"status":"accepted","version":2}`, false},
		{"RiskPatch", `{"status":"open","note":"Unsupported note"}`, false},
		{"AutomationPolicyInput", `{"version":0,"enabled":true}`, false},
		{"AutomationPolicyInput", `{"version":0,"enabled":false,"max_changes":30,"max_rounds":3}`, true},
		{"AutomationPolicyInput", `{"version":0,"enabled":false,"max_changes":30,"max_rounds":3,"allowed_entities":["story","task"]}`, true},
		{"AutomationPolicyInput", `{"version":0,"enabled":false,"max_changes":30,"max_rounds":3,"allowed_entities":["page"]}`, false},
		{"AutomationRunCreate", `{"kind":"decompose","idempotency_key":"upload","source":{"text":"A sample requirement"}}`, true},
		{"AutomationRunCreate", `{"kind":"decompose","idempotency_key":"page","source":{"page_id":"00000000-0000-0000-0000-000000000001","revision":2}}`, true},
		{"AutomationRunCreate", `{"kind":"decompose","idempotency_key":"missing"}`, false},
		{"AutomationRunCreate", `{"kind":"decompose","idempotency_key":"ambiguous","source":{"page_id":"00000000-0000-0000-0000-000000000001","revision":2,"text":"Competing source"}}`, false},
		{"GitHubBindingInput", `{"installation_id":123,"repository":"owner/repository"}`, true},
		{"GitHubBindingInput", `{"installation_id":123,"repository_id":456,"repository":"owner/repository"}`, false},
		{"GitHubSync", `{"pull_request":17,"run_id":123,"run_attempt":2}`, true},
		{"GitHubSync", `{"pull_number":17}`, false},
		{"GitHubSync", `{"commit_sha":"` + strings.Repeat("a", 64) + `"}`, true},
		{"AutomationActionResult", `{"run_id":"00000000-0000-0000-0000-000000000001","item_ids":[],"status":"applied"}`, true},
		{"GitHubSyncQueued", `{"id":"00000000-0000-0000-0000-000000000001","status":"queued"}`, true},
	} {
		var value any
		if err := json.Unmarshal([]byte(example.input), &value); err != nil {
			t.Fatal(err)
		}
		validateRequirementSchema(t, compiler, example.name, value, example.valid)
	}
	commands := []any{}
	for i := 0; i < 200; i++ {
		commands = append(commands, map[string]any{"operation": "create", "fields": map[string]any{"name": "Batch item"}})
	}
	validateRequirementSchema(t, compiler, "PlanningChangeSet", map[string]any{"idempotency_key": "two-hundred", "commands": commands}, true)
	commands = append(commands, commands[0])
	validateRequirementSchema(t, compiler, "PlanningChangeSet", map[string]any{"idempotency_key": "too-many", "commands": commands}, false)
}

func TestResourceSchemasValidateActualProjectionAndSchedulerDTOs(t *testing.T) {
	_, compiler := requirementsCompiler(t)
	id := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	minutes, zero := 480, 0
	member := resources.Member{MemberID: id, DisplayName: "Resource member", Skills: []string{"go"}, WeekdayMinutes: []*int{&zero, &minutes, &minutes, &minutes, &minutes, &minutes, &zero}, ProjectMinutesPerDay: &minutes, Exceptions: map[string]*int{}, Version: 1}
	task := resources.Task{ID: uuid.MustParse("00000000-0000-0000-0000-000000000002"), Name: "Task", Version: 2, Executable: true, StateGroup: "unstarted", Priority: "medium", AssigneeIDs: []uuid.UUID{id}, AllocationWeights: []resources.AllocationWeight{}, RequiredSkills: []string{"go"}, Dependencies: []uuid.UUID{}, RemainingMinutes: &minutes, EstimatedMinutes: &minutes, StartDate: "2026-09-14", TargetDate: "2026-09-14"}
	snapshot := resources.Snapshot{Timezone: "Asia/Shanghai", Revision: 7, Members: []resources.Member{member}, Tasks: []resources.Task{task}}
	projection, err := resources.Project(snapshot, "2026-09-14", "2026-09-16")
	if err != nil {
		t.Fatal(err)
	}
	validateRequirementSchema(t, compiler, "ResourceLoad", projection, true)
	proposal, err := resources.Schedule(snapshot, resources.ScheduleRequest{StartDate: "2026-09-14", EndDate: "2026-09-16"})
	if err != nil {
		t.Fatal(err)
	}
	validateRequirementSchema(t, compiler, "ScheduleProposal", proposal, true)
	snapshot.Tasks[0].StartDate = ""
	snapshot.Tasks[0].TargetDate = ""
	snapshot.Tasks[0].RemainingMinutes = nil
	snapshot.Members[0].ProjectMinutesPerDay = nil
	unknown, err := resources.Project(snapshot, "2026-09-14", "2026-09-16")
	if err != nil {
		t.Fatal(err)
	}
	validateRequirementSchema(t, compiler, "ResourceLoad", unknown, true)
}

func TestRequirementsRouteParametersAccessAndResponseShapes(t *testing.T) {
	document, _ := requirementsCompiler(t)
	paths := document["paths"].(map[string]any)
	prefix := "/workspaces/{workspaceID}/projects/{projectID}"
	operation := func(path, method string) map[string]any { return paths[path].(map[string]any)[method].(map[string]any) }
	parameters := func(op map[string]any) map[string]map[string]any {
		result := map[string]map[string]any{}
		for _, raw := range op["parameters"].([]any) {
			p := raw.(map[string]any)
			result[p["in"].(string)+":"+p["name"].(string)] = p
		}
		return result
	}
	for _, path := range []string{prefix + "/requirements/activities/{activityID}", prefix + "/scenarios/{scenarioID}"} {
		op := operation(path, "delete")
		if op["requestBody"] != nil || parameters(op)["query:version"]["required"] != true {
			t.Fatalf("Deletion needs query version: %s", path)
		}
	}
	activityDelete := parameters(operation(prefix+"/requirements/activities/{activityID}", "delete"))
	if activityDelete["query:migrate_to"] == nil {
		t.Fatal("Activity deletion migration parameter missing")
	}
	events := operation(prefix+"/requirements/events", "get")
	eventParams := parameters(events)
	if eventParams["query:cursor"] == nil || eventParams["query:after"] != nil || eventParams["header:Last-Event-ID"] == nil {
		t.Fatal("SSE cursor contract drifted")
	}
	security, _ := json.Marshal(events["security"])
	if strings.Contains(string(security), "workspaceBearer") {
		t.Fatal("SSE advertises bearer-token access")
	}
	security, _ = json.Marshal(operation(prefix+"/scenarios", "get")["security"])
	if !strings.Contains(string(security), "workspaceBearer") {
		t.Fatal("Scenario contract omits supported scoped bearer access")
	}
	load := parameters(operation(prefix+"/resources/load", "get"))
	for _, name := range []string{"member_id", "skill", "state_id", "epic_id", "search", "work_item_ids", "cycle_id", "commitment_cycle_id"} {
		if load["query:"+name] == nil {
			t.Fatalf("Missing resource filter %s", name)
		}
	}
	useCases := parameters(operation(prefix+"/scenarios/use-case.svg", "get"))
	if useCases["query:version"] != nil || useCases["query:story_ids"] == nil || useCases["query:download"] == nil {
		t.Fatal("Aggregate UML filter contract drifted")
	}
	callback := operation("/github/callback", "get")["responses"].(map[string]any)
	callbackParams := parameters(operation("/github/callback", "get"))
	if callbackParams["query:state"]["required"] != true || callbackParams["query:code"]["required"] == true || callbackParams["query:installation_id"]["required"] == true {
		t.Fatal("GitHub setup and OAuth callbacks require different phase parameters")
	}
	if callback["303"] == nil || callback["302"] != nil {
		t.Fatal("GitHub callback status must be 303")
	}
	webhook := operation("/github/webhook", "post")["responses"].(map[string]any)
	if webhook["200"] == nil || webhook["202"] == nil {
		t.Fatal("Signed ping and event acknowledgements need separate statuses")
	}
}
