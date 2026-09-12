package openapi

func addRequirementsRoutes(b catalogBuilder, p string) {
	r := p + "/requirements"
	b.add("GET", r+"/snapshot", "Requirements", "Read a complete authorized planning snapshot and matching revision", nil, ref("RequirementSnapshot"), 200)
	b.describe("GET", r+"/snapshot", "One repeatable-read boundary includes all visible items, activities, cycles, states and canonical blocking relations. Old items retain a null requirement type. The revision is the resume cursor for SSE; content is always fetched under current authorization.")
	b.add("GET", r+"/events", "Requirements", "Subscribe to durable project invalidations and independent authorization changes", nil, stringSchema(), 200)
	b.edit("GET", r+"/events", func(o *operation) { o.Raw, o.ResponseMedia, o.Access = true, "text/event-stream", "session" })
	b.query("GET", r+"/events", query("cursor", S{"type": "integer", "minimum": 0, "default": 0}, "Last applied revision."), S{"name": "Last-Event-ID", "in": "header", "required": false, "schema": stringSchema(), "description": "Nonnegative revision; takes precedence over cursor."})
	b.describe("GET", r+"/events", "Browser session required; workspace bearer tokens are rejected. SSE change events carry revision, kind and an optional currently authorized entity_id. cursor_expired carries revision and resnapshot=true; authorization_changed carries clear=true and may carry revision/reconnect. Both require clearing the affected caches and fetching a current snapshot. retry requests reconnection. Comment heartbeats carry no entity content; replayed revisions may repeat.")
	b.add("GET", r+"/activities", "Requirements", "List visible activity columns, including empty columns", nil, array(ref("RequirementActivity")), 200)
	b.add("POST", r+"/activities", "Requirements", "Create an explicitly scoped user activity", ref("RequirementActivityCreate"), ref("RequirementActivity"), 201)
	b.add("PATCH", r+"/activities/:activityID", "Requirements", "Version, rename, reorder or archive an activity", ref("RequirementActivityPatch"), ref("RequirementActivity"), 200)
	b.add("DELETE", r+"/activities/:activityID", "Requirements", "Migrate stories before removing an activity atomically", nil, nil, 204)
	b.query("DELETE", r+"/activities/:activityID", requiredQuery("version", version(), "Current activity version."), query("migrate_to", S{"anyOf": []any{uuidSchema(), enum("null")}}, "Explicitly move stories to an active activity in the same Epic, or use literal null to unassign them. Required when the activity contains Stories."))
	b.add("POST", p+"/planning/changesets", "Requirements", "Atomically apply versioned and idempotent planning commands", ref("PlanningChangeSet"), ref("PlanningChangeResult"), 200)
	b.describe("POST", p+"/planning/changesets", "Member permission required. Commands use the same domain implementation as work-item HTTP writes and automation. Current permissions are rechecked after graph locks. Every command must succeed; work items, history, revisions, audit and outbox roll back together. A replay with the same actor/project/key and input retains its original batch ID and reloads current authorized entities and revision; changed input conflicts. parent_id, activity_id and dependency_ids may reference earlier created command identities as $client_id. Story Cycle changes preserve task dates/Cycles unless explicit child commands are supplied.")
	b.add("GET", r+"/history", "Requirements", "Read authorized persistent facts and their coverage boundary", nil, array(ref("RequirementFact")), 200)
	b.query("GET", r+"/history", query("limit", S{"type": "integer", "minimum": 1, "maximum": 1000, "default": 200}, "Latest current-authorized facts, ordered by time and revision descending; Member permission required."))

	b.add("GET", p+"/resources", "Resources", "Read project timezone, current members, skills and capacity", nil, ref("ResourceSnapshot"), 200)
	b.add("PATCH", p+"/resources", "Resources", "Set the IANA project timezone as an administrator", object(S{"timezone": stringSchema()}, "timezone"), object(S{"timezone": stringSchema()}), 200)
	b.add("PUT", p+"/resources/members/:memberID", "Resources", "Version a current member's project calendar and skills", ref("ResourceMemberInput"), ref("ResourceMember"), 200)
	b.add("GET", p+"/resources/load", "Resources", "Aggregate conserved leaf-task minutes across the complete authorized set", nil, ref("ResourceLoad"), 200)
	b.query("GET", p+"/resources/load", requiredQuery("start_date", dateSchema(), "First displayed project-local date."), requiredQuery("end_date", dateSchema(), "Last displayed date; at most 366 inclusive calendar days."), query("member_id", uuidSchema(), "Select a member row and assigned task bars."), query("skill", stringSchema(), "Select members with this skill."), query("state_id", uuidSchema(), "Filter task bars by state."), query("epic_id", uuidSchema(), "Filter visible descendants of this Epic."), query("search", stringSchema(), "Case-insensitive substring of the task name."), query("work_item_ids", stringSchema(), "Intersect task bars with at most 500 comma-separated UUIDs."), query("cycle_id", ref("ResourceCycleFilter"), "Filter each task's execution Cycle; backlog selects no execution Cycle."), query("commitment_cycle_id", ref("ResourceCycleFilter"), "Filter the nearest visible Story's commitment Cycle, falling back to the own Cycle for independent tasks; backlog selects no commitment Cycle."))
	b.describe("GET", p+"/resources/load", "Integer remaining minutes are apportioned once among assignees, with stable remainder order. Parent rollups do not consume capacity; completed/cancelled tasks contribute no future load. Null means unknown, explicit zero means no capacity. Daily/weekly allocated_minutes and over_capacity retain the complete authorized project workload; filters select task bars and the selected_minutes/selected_unknown subtotal. task_minutes and unknown_task_ids provide server-calculated daily contributors. This measures recorded project allocation, not global utilization across unrecorded projects.")
	b.add("POST", p+"/resources/schedule", "Resources", "Propose deterministic assignments within skills, calendars and deadlines", ref("ScheduleRequest"), ref("ScheduleProposal"), 200)
	b.describe("POST", p+"/resources/schedule", "Member permission required. Read-only proposal for at most 200 unstarted, unlocked executable tasks over at most 366 inclusive days. Locked, started, completed work and Story commitments remain protected. A bounded deterministic greedy strategy reports unscheduled reasons; it does not prove mathematical infeasibility. feasible is false when any selected task is unscheduled or a protected hard conflict remains. Automation applies assignments through the shared atomic command path using input_revision and item versions. Execution Cycle membership is never inferred from dates.")
	b.describe("PUT", p+"/resources/members/:memberID", "Admin permission required. Supply a full configuration and its current version, or version=0 for an unconfigured member. Null capacity is unknown; zero is explicitly unavailable. Weekday order is Sunday through Saturday. Calendar exceptions override weekdays; capacity is constrained by both the calendar and project quota.")

	sp := p + "/scenarios"
	b.add("GET", sp, "Scenarios", "List current-authorized structured scenarios", nil, array(ref("Scenario")), 200)
	b.query("GET", sp, query("story_id", uuidSchema(), "Optional primary Story."))
	b.add("POST", sp, "Scenarios", "Create a versioned business scenario", ref("ScenarioCreate"), ref("Scenario"), 201)
	b.add("GET", sp+"/:scenarioID", "Scenarios", "Read a scenario with current Story labels and review state", nil, ref("Scenario"), 200)
	b.query("GET", sp+"/:scenarioID", query("version", version(), "Optional historical scenario revision, subject to current source authorization."))
	b.add("PATCH", sp+"/:scenarioID", "Scenarios", "Version scenario content or explicitly acknowledge source review", ref("ScenarioPatch"), ref("Scenario"), 200)
	b.add("DELETE", sp+"/:scenarioID", "Scenarios", "Delete a scenario using its current version", nil, nil, 204)
	b.query("DELETE", sp+"/:scenarioID", requiredQuery("version", version(), "Current scenario version."))
	b.add("POST", sp+"/:scenarioID/copy", "Scenarios", "Copy a scenario while retaining private-source provenance", ref("ScenarioCopy"), ref("Scenario"), 201)
	b.add("GET", sp+"/:scenarioID/versions", "Scenarios", "List immutable scenario revisions under current source access", nil, array(ref("ScenarioVersion")), 200)
	b.add("GET", sp+"/:scenarioID/versions/:versionID", "Scenarios", "Read a numbered historical scenario revision", nil, ref("Scenario"), 200)
	for _, path := range []string{sp + "/use-case.svg", sp + "/:scenarioID/use-case.svg", sp + "/:scenarioID/sequence.svg"} {
		b.add("GET", path, "Scenarios", "Render an independently authored and authorized UML SVG", nil, stringSchema(), 200)
		b.edit("GET", path, func(o *operation) { o.Raw, o.ResponseMedia = true, "image/svg+xml" })
		if path != sp+"/use-case.svg" {
			b.query("GET", path, query("version", version(), "Optional fixed scenario revision."))
		} else {
			b.query("GET", path, query("story_ids", stringSchema(), "Optional comma-separated primary Story UUIDs. Explicit empty string selects no Stories; omission selects all current-authorized scenarios."))
		}
		b.query("GET", path, query("download", enum("1"), "Use 1 to request an attachment response."))
		b.describe("GET", path, "Current primary Story and all retained explicit source ACLs are intersected, including historical exports. Text is XML escaped; diagrams contain no scripts or external resources. Dependencies and dates do not invent business interactions. Responses are private no-store.")
	}

	a := p + "/automation"
	b.add("GET", a+"/capabilities", "Automation", "Read enabled automation kinds without administrator policy details", nil, object(S{"enabled": boolSchema(), "allowed_kinds": array(stringSchema())}), 200)
	b.add("GET", a+"/policy", "Automation", "Read the versioned project automation policy and usage as an administrator", nil, ref("AutomationPolicy"), 200)
	b.add("PUT", a+"/policy", "Automation", "Enable, constrain or disable automation as a project administrator", ref("AutomationPolicyInput"), ref("AutomationPolicy"), 200)
	b.add("GET", a+"/runs", "Automation", "List persistent authorized automation run summaries", nil, array(ref("AutomationRunSummary")), 200)
	b.describe("GET", a+"/runs", "Member permission required. Returns the latest 100 currently source-authorized runs. Summaries omit input, output, before_state and after_state; fetch a run detail for these fields.")
	b.add("POST", a+"/runs", "Automation", "Queue a source-pinned and idempotent analysis run", ref("AutomationRunCreate"), ref("AutomationRun"), 202)
	b.add("GET", a+"/runs/:runID", "Automation", "Inspect input fingerprint, result, failure and protected change diff", nil, ref("AutomationRun"), 200)
	for _, action := range []string{"cancel", "retry", "undo"} {
		b.add("POST", a+"/runs/:runID/"+action, "Automation", "Request "+action+" as an administrator with protected history checks", nil, ref("AutomationRun"), 200)
	}
	b.describe("POST", a+"/runs/:runID/undo", "Undo is atomic and checks all post-application history, including comments and associations. Conflicting human edits are preserved and produce 409; no partial rollback is presented as success.")
	b.add("GET", a+"/forecasts", "Automation", "Read immutable delivery distributions and cold-start evidence", nil, array(ref("Forecast")), 200)
	b.add("GET", a+"/risks", "Automation", "Read deduplicated risks and evidence-linked lifecycle history", nil, array(ref("Risk")), 200)
	b.add("PATCH", a+"/risks/:riskID", "Automation", "Version a risk disposition without changing delivery commitments", ref("RiskPatch"), ref("Risk"), 200)
	b.add("POST", a+"/risks/:riskID/action", "Automation", "Apply an allowed and traceable risk response as an administrator", nil, ref("AutomationActionResult"), 200)
	b.add("GET", a+"/improvements", "Automation", "Read bottleneck actions, fixed baselines and observations", nil, array(ref("Improvement")), 200)
	b.add("POST", a+"/improvements/:improvementID/observe", "Automation", "Compute the next observation against the saved baseline", nil, ref("Improvement"), 200)
	b.add("POST", a+"/improvements/:improvementID/action", "Automation", "Create a policy-authorized improvement action as an administrator", nil, ref("AutomationActionResult"), 200)
	for key, op := range b.entries {
		if op.Tag == "Automation" {
			op.Access = "session"
			op.Description += " Browser session with current explicit project membership required; workspace bearer tokens are rejected. Policy reads/writes, cancellation, retry, undo and response-action application require Admin. Other reads, analysis requests, risk disposition and improvement observations require Member."
			b.entries[key] = op
		}
	}

	b.add("GET", p+"/github", "Quality", "Read independent GitHub App configuration, grants and repository bindings", nil, ref("GitHubConnection"), 200)
	b.add("POST", p+"/github/connect", "Quality", "Start a one-time administrator-bound App installation authorization", nil, object(S{"install_url": stringSchema(), "expires_at": timestamp()}), 201)
	b.add("POST", p+"/github/bindings", "Quality", "Bind an installation-authorized repository to this project", ref("GitHubBindingInput"), ref("GitHubBinding"), 201)
	b.add("DELETE", p+"/github/bindings/:bindingID", "Quality", "Revoke a project repository binding and automatic reads", nil, nil, 204)
	b.add("POST", p+"/github/bindings/:bindingID/sync", "Quality", "Queue a pinned repository or Actions evidence synchronization", ref("GitHubSync"), ref("GitHubSyncQueued"), 202)
	b.add("GET", p+"/quality/reports", "Quality", "Read source-authorized versioned quality report summaries", nil, array(ref("QualityReportSummary")), 200)
	b.describe("GET", p+"/quality/reports", "Returns up to 100 current-authorized reports. Summary rows omit evidence and findings; fetch report detail for exact evidence, findings and automatic repair actions.")
	b.add("GET", p+"/quality/reports/:reportID", "Quality", "Read exact SHA, run/attempt, artifact and document evidence", nil, ref("QualityReport"), 200)
	b.add("POST", "/github/webhook", "Quality", "Accept signed and replay-safe independent GitHub App events", freeObject(), object(S{"accepted": boolSchema(), "duplicate": boolSchema()}, "accepted", "duplicate"), 202)
	b.access("POST", "/github/webhook", "github")
	b.query("POST", "/github/webhook", S{"name": "X-GitHub-Delivery", "in": "header", "required": true, "schema": stringSchema()}, S{"name": "X-GitHub-Event", "in": "header", "required": true, "schema": stringSchema()})
	b.describe("POST", "/github/webhook", "The exact raw body is authorized by X-Hub-Signature-256. A signed ping returns 200 with accepted=true. Other valid deliveries return 202 with accepted and duplicate. Reusing a delivery ID with different content returns 409.")
	b.add("GET", "/github/callback", "Quality", "Redeem one-time installation state and verify repository admin rights", nil, nil, 303)
	b.access("GET", "/github/callback", "anonymous")
	b.query("GET", "/github/callback", requiredQuery("state", stringSchema(), "One-time state bound to the initiating administrator session."), query("code", stringSchema(), "Required for the OAuth phase; omitted during installation setup."), query("installation_id", integer(), "Required during installation setup; the OAuth phase recovers it from its bound state."))
	b.describe("GET", "/github/callback", "Both GitHub App Setup URL and OAuth Callback URL use this route. Setup receives state and installation_id, then redirects with 303 to explicit OAuth authorization using PKCE. OAuth receives code and the installation-bound state, consumes the state once, verifies repository administrator rights and redirects with 303 to the project. A fresh initiating session and current project administrator permission are required in both phases; ambient browser cookies are not the authorization source.")
	for key, op := range b.entries {
		if op.Tag == "Quality" && op.Access == "workspace" {
			op.Access = "session"
			op.Description += " Browser session and explicit project membership required. Connection/report reads require Member; connect, bind, unbind and synchronization require Admin. Workspace bearer tokens are rejected."
			b.entries[key] = op
		}
	}
}
