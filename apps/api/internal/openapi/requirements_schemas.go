package openapi

func requirementItemFields() S {
	return S{"requirement_type": nullable(enum("epic", "story", "task")), "story_role": stringSchema(), "story_goal": stringSchema(), "story_benefit": stringSchema(), "acceptance_criteria": array(stringSchema()), "activity_id": nullable(uuidSchema()), "map_position": numberSchema(), "estimated_minutes": nullable(S{"type": "integer", "minimum": 0, "maximum": 52560000}), "remaining_minutes": nullable(S{"type": "integer", "minimum": 0, "maximum": 52560000}), "required_skills": array(stringSchema()), "allocation_weights": array(ref("AllocationWeight")), "planning_locked": boolSchema(), "dependency_ids": array(uuidSchema())}
}

func dateOrEmpty() S           { return S{"anyOf": []any{dateSchema(), enum("")}} }
func boundedIDs(maximum int) S { return S{"type": "array", "items": uuidSchema(), "maxItems": maximum} }

func addRequirementsSchemas(s S) {
	s["AllocationWeight"] = object(S{"member_id": uuidSchema(), "weight": S{"type": "integer", "minimum": 0, "maximum": 1000000}}, "member_id", "weight")
	s["AllocationWeight"].(S)["description"] = "Integer weight for one assignee. Explicit allocations cover exactly the current assignee set and must have a positive total; zero-share members are allowed."
	a := S{"epic_id": nullable(uuidSchema()), "name": S{"type": "string", "minLength": 1, "maxLength": 255}, "position": numberSchema(), "archived_at": nullable(timestamp())}
	s["RequirementActivityCreate"] = object(a, "name")
	ap := properties("RequirementActivityCreate", s)
	ap["version"] = version()
	s["RequirementActivityPatch"], s["RequirementActivity"] = object(ap, "version"), scoped(ap)
	s["RequirementSnapshot"] = object(S{"revision": integer(), "items": array(ref("WorkItem")), "activities": array(ref("RequirementActivity")), "cycles": array(ref("Cycle")), "states": array(ref("State")), "dependencies": array(ref("Relation")), "permissions": object(S{"can_edit": boolSchema(), "can_admin": boolSchema()}, "can_edit", "can_admin")}, "revision", "items", "activities", "cycles", "states", "dependencies", "permissions")
	clientReference := S{"anyOf": []any{uuidSchema(), S{"type": "string", "pattern": "^\\$.+", "maxLength": 101}}}
	fields := properties("WorkItemChanges", s)
	fields["parent_id"], fields["activity_id"], fields["dependency_ids"] = nullable(clientReference), nullable(clientReference), array(clientReference)
	s["PlanningCommandFields"] = object(fields)
	s["PlanningCommand"] = object(S{"operation": enum("create", "update", "delete"), "id": uuidSchema(), "client_id": S{"type": "string", "maxLength": 100}, "version": version(), "fields": ref("PlanningCommandFields")}, "operation")
	s["PlanningCommand"].(S)["allOf"] = []any{
		S{"if": object(S{"operation": enum("update", "delete")}, "operation"), "then": S{"required": []string{"id", "version"}}},
		S{"if": object(S{"operation": enum("create")}, "operation"), "then": S{"required": []string{"fields"}, "properties": S{"id": enum("00000000-0000-0000-0000-000000000000"), "fields": S{"required": []string{"name"}}}}},
	}
	// Conditions inspect only operation; other command fields must not make the
	// conditional fail because object() normally rejects additional properties.
	for _, condition := range s["PlanningCommand"].(S)["allOf"].([]any) {
		condition.(S)["if"].(S)["additionalProperties"] = true
	}
	s["PlanningChangeSet"] = object(S{"idempotency_key": S{"type": "string", "minLength": 1, "maxLength": 200}, "expected_revision": nullable(integer()), "reason": S{"type": "string", "maxLength": 1000}, "commands": S{"type": "array", "items": ref("PlanningCommand"), "minItems": 1, "maxItems": 200}}, "idempotency_key", "commands")
	s["PlanningChangeResult"] = object(S{"batch_id": uuidSchema(), "revision": integer(), "items": array(ref("WorkItem")), "created_ids": S{"type": "object", "additionalProperties": uuidSchema()}, "deleted_ids": array(uuidSchema())}, "batch_id", "revision", "items", "created_ids", "deleted_ids")
	s["RequirementFact"] = scoped(S{"work_item_id": uuidSchema(), "revision": integer(), "actor_id": nullable(uuidSchema()), "action": stringSchema(), "before_data": nullable(freeObject()), "after_data": nullable(freeObject()), "cause": stringSchema(), "batch_id": nullable(uuidSchema())})
	addResourceSchemas(s)
	addScenarioSchemas(s)
	addAutomationSchemas(s)
	addQualitySchemas(s)
}

func addResourceSchemas(s S) {
	minutes := nullable(S{"type": "integer", "minimum": 0, "maximum": 1440})
	rm := S{"skills": S{"type": "array", "items": S{"type": "string", "minLength": 1, "maxLength": 80}, "maxItems": 100}, "weekday_minutes": S{"type": "array", "items": minutes, "minItems": 7, "maxItems": 7}, "project_minutes_per_day": minutes, "exceptions": S{"type": "object", "additionalProperties": minutes, "propertyNames": dateSchema(), "maxProperties": 1000}, "version": S{"type": "integer", "minimum": 0}}
	s["ResourceMemberInput"] = object(rm, "version", "skills", "weekday_minutes", "project_minutes_per_day", "exceptions")
	rmc := properties("ResourceMemberInput", s)
	rmc["member_id"], rmc["display_name"] = uuidSchema(), stringSchema()
	s["ResourceMember"] = object(rmc, "member_id", "display_name", "version", "skills", "weekday_minutes", "project_minutes_per_day", "exceptions")
	s["ResourceSnapshot"] = object(S{"timezone": stringSchema(), "members": array(ref("ResourceMember"))}, "timezone", "members")
	s["ResourceCycleFilter"] = S{"anyOf": []any{uuidSchema(), enum("backlog")}}
	s["ResourceConflict"] = object(S{"type": stringSchema(), "task_id": uuidSchema(), "member_id": uuidSchema(), "related_task_id": uuidSchema(), "date": dateSchema(), "message": stringSchema(), "severity": enum("warning", "error", "info")}, "type", "message", "severity")
	s["ResourceDay"] = object(S{"date": dateSchema(), "capacity_minutes": nullable(integer()), "allocated_minutes": integer(), "selected_minutes": integer(), "unknown": boolSchema(), "selected_unknown": boolSchema(), "over_capacity": boolSchema(), "task_ids": array(uuidSchema()), "task_minutes": S{"type": "object", "propertyNames": uuidSchema(), "additionalProperties": integer()}, "unknown_task_ids": array(uuidSchema())}, "date", "capacity_minutes", "allocated_minutes", "selected_minutes", "unknown", "selected_unknown", "over_capacity", "task_ids", "task_minutes", "unknown_task_ids")
	s["ResourceWeek"] = object(S{"start_date": dateSchema(), "capacity_minutes": nullable(integer()), "allocated_minutes": integer(), "selected_minutes": integer(), "unknown": boolSchema(), "selected_unknown": boolSchema(), "over_capacity": boolSchema()}, "start_date", "capacity_minutes", "allocated_minutes", "selected_minutes", "unknown", "selected_unknown", "over_capacity")
	memberLoad := properties("ResourceMember", s)
	memberLoad["days"], memberLoad["weeks"] = array(ref("ResourceDay")), array(ref("ResourceWeek"))
	s["ResourceMemberLoad"] = object(memberLoad, "member_id", "display_name", "version", "skills", "weekday_minutes", "project_minutes_per_day", "exceptions", "days", "weeks")
	task := S{"id": uuidSchema(), "name": stringSchema(), "version": version(), "parent_id": nullable(uuidSchema()), "requirement_type": enum("", "epic", "story", "task"), "state_id": uuidSchema(), "state_group": stringSchema(), "priority": stringSchema(), "start_date": dateOrEmpty(), "target_date": dateOrEmpty(), "estimated_minutes": nullable(integer()), "remaining_minutes": nullable(integer()), "assignee_ids": array(uuidSchema()), "allocation_weights": array(ref("AllocationWeight")), "required_skills": array(stringSchema()), "planning_locked": boolSchema(), "cycle_id": nullable(uuidSchema()), "commitment_cycle_id": nullable(uuidSchema()), "executable": boolSchema(), "commitment_start_date": dateOrEmpty(), "commitment_target_date": dateOrEmpty(), "dependencies": array(uuidSchema())}
	s["ResourceTask"] = object(task, "id", "name", "version", "parent_id", "requirement_type", "state_id", "state_group", "priority", "start_date", "target_date", "estimated_minutes", "remaining_minutes", "assignee_ids", "allocation_weights", "required_skills", "planning_locked", "cycle_id", "commitment_cycle_id", "executable", "commitment_start_date", "commitment_target_date", "dependencies")
	s["ResourceLoad"] = object(S{"timezone": stringSchema(), "start_date": dateSchema(), "end_date": dateSchema(), "members": array(ref("ResourceMemberLoad")), "tasks": array(ref("ResourceTask")), "conflicts": array(ref("ResourceConflict")), "unassigned": array(uuidSchema()), "unestimated": array(uuidSchema()), "unscheduled": array(uuidSchema()), "scope": stringSchema()}, "timezone", "start_date", "end_date", "members", "tasks", "conflicts", "unassigned", "unestimated", "unscheduled", "scope")
	s["ScheduleRequest"] = object(S{"start_date": dateSchema(), "end_date": dateSchema(), "task_ids": boundedIDs(200), "member_ids": array(uuidSchema()), "hard_deadline": dateOrEmpty()}, "start_date", "end_date")
	s["ScheduleAssignment"] = object(S{"id": uuidSchema(), "version": version(), "start_date": dateSchema(), "target_date": dateSchema(), "assignee_ids": array(uuidSchema()), "allocation_weights": array(ref("AllocationWeight")), "reason": stringSchema()}, "id", "version", "start_date", "target_date", "assignee_ids", "allocation_weights", "reason")
	s["ScheduleProposal"] = object(S{"algorithm_version": stringSchema(), "input_revision": integer(), "feasible": boolSchema(), "assignments": array(ref("ScheduleAssignment")), "unscheduled": array(ref("ResourceConflict")), "existing_conflicts": array(ref("ResourceConflict"))}, "algorithm_version", "input_revision", "feasible", "assignments", "unscheduled", "existing_conflicts")
}

func addScenarioSchemas(s S) {
	s["ScenarioParticipant"] = object(S{"id": uuidSchema(), "name": S{"type": "string", "minLength": 1, "maxLength": 160}, "kind": enum("actor", "system")}, "id", "name", "kind")
	s["ScenarioStep"] = object(S{"id": uuidSchema(), "kind": enum("call", "return", "alt", "else", "end"), "from_id": uuidSchema(), "to_id": uuidSchema(), "message": S{"type": "string", "maxLength": 1000}, "return_of": uuidSchema()}, "id", "kind")
	s["ScenarioStep"].(S)["allOf"] = []any{
		S{"if": S{"properties": S{"kind": enum("call", "return", "alt", "else")}, "required": []string{"kind"}}, "then": S{"required": []string{"message"}, "properties": S{"message": S{"minLength": 1}}}},
		S{"if": S{"properties": S{"kind": enum("call", "return")}, "required": []string{"kind"}}, "then": S{"required": []string{"from_id", "to_id"}}},
		S{"if": S{"properties": S{"kind": enum("return")}, "required": []string{"kind"}}, "then": S{"required": []string{"return_of"}}},
	}
	s["ScenarioRelationship"] = object(S{"id": uuidSchema(), "kind": enum("association", "include", "extend", "generalization"), "from_id": uuidSchema(), "to_id": uuidSchema()}, "id", "kind", "from_id", "to_id")
	sc := S{"story_id": uuidSchema(), "name": S{"type": "string", "minLength": 1, "maxLength": 240}, "goal": S{"type": "string", "maxLength": 4000}, "trigger": S{"type": "string", "maxLength": 4000}, "preconditions": S{"type": "string", "maxLength": 8000}, "outcome": S{"type": "string", "maxLength": 8000}, "system_boundary": S{"type": "string", "maxLength": 240}, "bind_story_name": boolSchema(), "source_page_ids": boundedIDs(50), "participants": S{"type": "array", "items": ref("ScenarioParticipant"), "maxItems": 24}, "steps": S{"type": "array", "items": ref("ScenarioStep"), "maxItems": 300}, "relationships": S{"type": "array", "items": ref("ScenarioRelationship"), "maxItems": 200}}
	s["ScenarioCreate"] = object(sc, "story_id", "name")
	scp := properties("ScenarioCreate", s)
	scp["version"], scp["review_needed"] = version(), boolSchema()
	s["ScenarioPatch"] = object(scp, "version")
	s["ScenarioPatch"].(S)["description"] = "The primary story_id cannot change. review_needed=false explicitly acknowledges the current authorized source revisions."
	s["ScenarioCopy"] = object(S{"version": version(), "name": S{"type": "string", "minLength": 1, "maxLength": 240}}, "version")
	s["ScenarioVersion"] = object(S{"id": uuidSchema(), "version": version(), "created_at": timestamp()}, "id", "version", "created_at")
	scr := properties("ScenarioPatch", s)
	scr["source_story_version"], scr["source_page_versions"], scr["provenance_source_page_ids"] = version(), S{"type": "object", "additionalProperties": version()}, array(uuidSchema())
	scr["story"] = object(S{"id": uuidSchema(), "name": stringSchema(), "state_id": uuidSchema(), "state_name": stringSchema(), "state_group": stringSchema(), "version": version()})
	s["Scenario"] = scoped(scr)
}

func addAutomationSchemas(s S) {
	kinds := array(enum("decompose", "schedule", "forecast", "risk", "efficiency", "quality"))
	policy := S{"version": S{"type": "integer", "minimum": 0}, "enabled": boolSchema(), "allowed_kinds": kinds, "allowed_fields": array(stringSchema()), "allowed_operations": array(enum("create", "update")), "allowed_item_ids": boundedIDs(500), "allowed_member_ids": boundedIDs(500), "allowed_page_ids": boundedIDs(500), "hard_deadline": dateOrEmpty(), "max_changes": S{"type": "integer", "minimum": 1, "maximum": 100}, "budget_calls": S{"type": "integer", "minimum": 0, "maximum": 10000}, "min_interval_seconds": S{"type": "integer", "minimum": 0, "maximum": 86400}, "max_rounds": S{"type": "integer", "minimum": 1, "maximum": 10}}
	policy["allowed_entities"] = array(enum("epic", "story", "task", "work_item"))
	s["AutomationPolicyInput"] = object(policy, "version", "enabled", "max_changes", "max_rounds")
	s["AutomationPolicyInput"].(S)["description"] = "Full replacement, not a defaults merge. Admin required. Array scopes and limits should be carried from the current policy; omitted budget/frequency values become zero."
	pr := properties("AutomationPolicyInput", s)
	pr["authorized_by"], pr["calls_used"] = uuidSchema(), integer()
	s["AutomationPolicy"] = object(pr)
	s["AutomationSource"] = object(S{"page_id": nullable(uuidSchema()), "revision": S{"type": "integer", "minimum": 0}, "observed_version": S{"type": "integer", "minimum": 0, "readOnly": true}, "text": S{"type": "string", "maxLength": 50000, "description": "At most 50,000 UTF-8 bytes."}, "key": S{"type": "string", "maxLength": 160}, "private": S{"type": "boolean", "readOnly": true}, "owner_id": S{"anyOf": []any{uuidSchema(), S{"type": "null"}}, "readOnly": true}})
	s["AutomationSource"].(S)["allOf"] = []any{S{"if": S{"properties": S{"page_id": uuidSchema()}, "required": []string{"page_id"}}, "then": S{"required": []string{"revision"}, "properties": S{"revision": version()}}}}
	s["AutomationRunCreate"] = object(S{"kind": enum("decompose", "schedule", "forecast", "risk", "efficiency"), "idempotency_key": S{"type": "string", "minLength": 1, "maxLength": 160}, "source": ref("AutomationSource"), "cause": S{"type": "string", "maxLength": 160, "description": "Manual causes cannot start with automation:."}, "round": S{"type": "integer", "const": 0}, "start_date": dateOrEmpty(), "end_date": dateOrEmpty(), "task_ids": boundedIDs(500)}, "kind", "idempotency_key")
	s["AutomationRunCreate"].(S)["description"] = "Decomposition requires exactly one selected page with a positive revision or nonempty uploaded text. Manual runs begin at round zero; quality runs originate from authorized integration evidence."
	s["AutomationRunCreate"].(S)["allOf"] = []any{S{"if": S{"properties": S{"kind": enum("decompose")}, "required": []string{"kind"}}, "then": S{"required": []string{"source"}, "properties": S{"source": S{"oneOf": []any{
		S{"required": []string{"page_id", "revision"}, "properties": S{"page_id": uuidSchema(), "revision": version(), "text": enum("")}},
		S{"required": []string{"text"}, "properties": S{"page_id": S{"type": "null"}, "text": S{"type": "string", "minLength": 1}}},
	}}}}}}
	s["AutomationSavedItem"] = object(S{"item_id": uuidSchema(), "version": version(), "fingerprint": stringSchema(), "fields": ref("WorkItemChanges"), "created": boolSchema()}, "item_id", "version", "fingerprint", "fields", "created")
	s["AutomationUndoConflict"] = object(S{"item_id": uuidSchema(), "reason": stringSchema()}, "item_id", "reason")
	run := S{"requested_by": uuidSchema(), "authorized_by": uuidSchema(), "kind": enum("decompose", "schedule", "forecast", "risk", "efficiency", "quality"), "status": enum("queued", "running", "applied", "partial", "completed", "blocked", "failed", "cancelled", "undone"), "idempotency_key": stringSchema(), "policy_version": version(), "input_fingerprint": stringSchema(), "cause": stringSchema(), "round": integer(), "attempts": integer(), "failure": stringSchema(), "algorithm_version": stringSchema(), "batch_id": nullable(uuidSchema()), "undo_conflicts": array(ref("AutomationUndoConflict")), "cancelled_at": nullable(timestamp()), "lease_until": nullable(timestamp())}
	s["AutomationRunSummary"] = scoped(run)
	detail := properties("AutomationRunSummary", s)
	detail["input"], detail["output"], detail["before_state"], detail["after_state"] = freeObject(), freeObject(), array(ref("AutomationSavedItem")), array(ref("AutomationSavedItem"))
	s["AutomationRun"] = object(detail, "id")
	s["AutomationRun"].(S)["additionalProperties"] = true
	s["AutomationActionResult"] = object(S{"run_id": uuidSchema(), "item_ids": array(uuidSchema()), "status": stringSchema()}, "run_id", "item_ids", "status")
	s["ForecastBacktest"] = object(S{"windows": integer(), "mean_absolute_error_days": nullable(numberSchema()), "p80_coverage": nullable(numberSchema()), "baseline_error_days": nullable(numberSchema()), "method": stringSchema()}, "windows", "mean_absolute_error_days", "p80_coverage", "baseline_error_days", "method")
	s["Forecast"] = scoped(S{"run_id": uuidSchema(), "as_of": timestamp(), "status": enum("ready", "insufficient_data"), "p50": nullable(dateSchema()), "p80": nullable(dateSchema()), "sample_count": integer(), "coverage_start": nullable(timestamp()), "remaining": integer(), "assumptions": array(stringSchema()), "backtest": ref("ForecastBacktest"), "stale": boolSchema(), "algorithm_version": stringSchema()})
	s["RiskPatch"] = object(S{"status": enum("open", "acknowledged", "resolved"), "version": version()}, "status", "version")
	s["RiskEvent"] = object(S{"id": uuidSchema(), "risk_id": uuidSchema(), "run_id": nullable(uuidSchema()), "actor_id": nullable(uuidSchema()), "status": enum("open", "acknowledged", "resolved"), "evidence": freeObject(), "created_at": timestamp()})
	s["Risk"] = scoped(S{"key": stringSchema(), "type": stringSchema(), "status": enum("open", "acknowledged", "resolved"), "severity": enum("low", "medium", "high", "critical"), "reason": stringSchema(), "evidence": S{}, "item_ids": array(uuidSchema()), "action_item_id": nullable(uuidSchema()), "version": version(), "first_seen": timestamp(), "last_seen": timestamp(), "history": array(ref("RiskEvent"))})
	s["ImprovementMetrics"] = object(S{"from": timestamp(), "through": timestamp(), "throughput": integer(), "wip": integer(), "scope": integer(), "blocked_ratio": numberSchema(), "reopen_ratio": numberSchema(), "added": integer(), "coverage_days": numberSchema(), "observations": integer()})
	s["Improvement"] = scoped(S{"run_id": uuidSchema(), "key": stringSchema(), "reason": stringSchema(), "target_metric": enum("blocked_ratio", "wip", "throughput", "reopen_ratio"), "status": enum("proposed", "observing", "improved", "no_improvement", "insufficient_evidence"), "baseline": ref("ImprovementMetrics"), "observed": object(S{"metrics": ref("ImprovementMetrics"), "notes": array(stringSchema()), "observed_at": timestamp()}), "action_item_id": nullable(uuidSchema()), "owner_id": nullable(uuidSchema()), "executed_at": nullable(timestamp()), "version": version(), "window_start": timestamp(), "window_end": timestamp()})
}

func addQualitySchemas(s S) {
	s["GitHubBindingInput"] = object(S{"installation_id": S{"type": "integer", "minimum": 1}, "repository": S{"type": "string", "pattern": "^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$", "description": "Full owner/repository name selected from the verified installation grant."}}, "installation_id", "repository")
	s["GitHubBinding"] = scoped(S{"installation_id": integer(), "repository_id": integer(), "repository": stringSchema(), "active": boolSchema(), "created_by": uuidSchema(), "sync_status": enum("idle", "queued", "syncing", "synced", "failed", "rate_limited", "revoked", "disabled"), "last_synced_at": nullable(timestamp()), "last_error": stringSchema(), "next_retry_at": nullable(timestamp()), "latest_source_at": nullable(timestamp()), "latest_report_id": nullable(uuidSchema())})
	s["GitHubRepository"] = object(S{"id": integer(), "full_name": stringSchema(), "default_branch": stringSchema(), "permissions": object(S{"admin": boolSchema()})})
	s["GitHubConnection"] = object(S{"configured": boolSchema(), "install_url": stringSchema(), "bindings": array(ref("GitHubBinding")), "grants": array(object(S{"installation_id": integer(), "repositories": array(ref("GitHubRepository")), "expires_at": timestamp()})), "required_permissions": object(S{"contents": stringSchema(), "pull_requests": stringSchema(), "actions": stringSchema(), "checks": stringSchema()})}, "configured", "install_url", "bindings", "grants", "required_permissions")
	s["GitHubSync"] = object(S{"commit_sha": S{"anyOf": []any{enum(""), S{"type": "string", "pattern": "^[a-fA-F0-9]{40}([a-fA-F0-9]{24})?$"}}}, "pull_request": S{"type": "integer", "minimum": 0}, "run_id": S{"type": "integer", "minimum": 0}, "run_attempt": S{"type": "integer", "minimum": 0}})
	s["GitHubSync"].(S)["description"] = "Optional exact SHA, PR number, or Actions run/attempt. A positive run_attempt requires a positive run_id. event_time is integration-owned and cannot be supplied manually."
	s["GitHubSyncQueued"] = object(S{"id": uuidSchema(), "status": enum("queued")}, "id", "status")
	s["QualityFinding"] = object(S{"id": stringSchema(), "dimension": enum("code", "documentation", "tests"), "rule": stringSchema(), "severity": stringSchema(), "title": stringSchema(), "description": stringSchema(), "path": stringSchema(), "line": integer(), "status": stringSchema(), "actionable": boolSchema(), "evidence": freeObject()})
	s["QualityDimension"] = object(S{"status": enum("passed", "failed", "skipped", "missing", "unknown"), "findings": integer(), "summary": freeObject()})
	report := S{"binding_id": uuidSchema(), "repository": stringSchema(), "commit_sha": stringSchema(), "pull_request": nullable(integer()), "run_id": nullable(integer()), "run_attempt": nullable(integer()), "source_revision": stringSchema(), "source_at": timestamp(), "dimensions": object(S{"code": ref("QualityDimension"), "documentation": ref("QualityDimension"), "tests": ref("QualityDimension")})}
	s["QualityReportSummary"] = scoped(report)
	detail := properties("QualityReportSummary", s)
	detail["findings"], detail["evidence"] = array(ref("QualityFinding")), freeObject()
	detail["actions"] = array(object(S{"report_id": uuidSchema(), "finding_id": stringSchema(), "run_id": nullable(uuidSchema()), "item_ids": array(uuidSchema()), "status": stringSchema(), "updated_at": timestamp()}))
	s["QualityReport"] = object(detail, "id")
	s["QualityReport"].(S)["additionalProperties"] = true
}
