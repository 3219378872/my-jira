// Independently authored live-provider acceptance. Generated evidence is redacted.
// Run only after the isolated API and durable worker have been rebuilt/restarted:
//   node scripts/verify-requirements-model.mjs --run
// Re-running resumes this evidence's private project and never resets its budget.
// After a reviewed runtime fix, --run --continue-incremental can spend the one
// remaining call after a rejected same-source run; that rejection remains visible.
import assert from "node:assert/strict";
import { createHash, randomUUID } from "node:crypto";
import { readFile, rename, writeFile } from "node:fs/promises";
import { setTimeout as delay } from "node:timers/promises";

const base = process.env.MYJIRA_API_URL ?? "http://127.0.0.1:18088";
const target = new URL(base);
assert(["127.0.0.1", "localhost"].includes(target.hostname) && target.port === "18088" && target.pathname === "/" && !target.username && !target.password && !target.search && !target.hash,
  "The real-model verifier requires the isolated test API on localhost:18088");
const origin = "http://127.0.0.1:14173";
const evidenceURL = new URL("../docs/REQUIREMENTS_MODEL_EVIDENCE.json", import.meta.url);
const fixtureURL = new URL("../tests/fixtures/requirements-prd.md", import.meta.url);
const sha256 = value => createHash("sha256").update(value).digest("hex");
const terminal = new Set(["applied", "partial", "completed", "blocked", "failed", "cancelled", "undone"]);
const success = new Set(["applied", "partial"]);
const continueIncremental = process.argv.includes("--continue-incremental");

// Bounded synthetic derivative of the repository's original Chinese fixture.
// English keeps nine complete structured entities within the configured 4096
// output tokens. This is a selected workflow, not full-PRD coverage evidence.
const initialSource = [
  "# Collaboration PRD: bounded backend acceptance v1",
  "Product goal: administrators, members and project managers organize requirements and track execution together.",
  "## R1 Member management and permissions",
  "Story U1: As an administrator I want to invite members with existing Admin, Member and Guest roles so that team access remains controlled.",
  "Acceptance: invitations can be accepted; Guests cannot change work items; removed members immediately lose read and write access.",
  "Task U1-API: implement invitation authorization for the existing roles; backend skill; estimate 240-480 minutes; assumption: no custom roles or new identity provider.",
  "## R2 Tasks and collaboration",
  "Story T1: As a team member I want to create Stories and assign owners so that responsibility is explicit.",
  "Acceptance: role, goal, benefit and criteria survive reload; concurrent stale commands are rejected atomically; a Task execution Cycle remains independent of its Story commitment Cycle.",
  "Task T1-API: implement versioned work-item commands; backend skill; estimate 360-600 minutes; assumption: existing project, role and state storage is available.",
  "## R3 Data and analysis",
  "Story D1: As a project manager I want to inspect member load and delivery risk so that decisions use recorded facts.",
  "Acceptance: only executable leaves contribute load; 960 minutes split 75/25 produces 720 and 240 minutes; zero capacity differs from unknown; insufficient history cannot imply reliable forecasts.",
  "Task D1-LOAD: implement daily load aggregation; backend skill; estimate 360-600 minutes; depends on T1-API; assumption: integer-minute work estimates and explicit allocation weights are available.",
  "Unresolved: real team capacity, concrete project start date and GitHub App credentials are absent.",
].join("\n");
const incrementalSource = initialSource.replace("acceptance v1", "acceptance v2").replace(
  "## R2 Tasks and collaboration",
  "Task U1-REVOKE: add membership-revocation regression tests; testing skill; estimate 120-240 minutes; depends on U1-API; assumption: the existing isolated test database and fixed roles are available.\n## R2 Tasks and collaboration",
);
const fixtureHash = sha256(await readFile(fixtureURL));
if (!process.argv.includes("--run")) {
  console.log(JSON.stringify({ status: "prepared", calls: 0, expected_initial_entities: 9, expected_incremental_entities: 10, initial_source_sha256: sha256(initialSource), fixture_sha256: fixtureHash,
    run: "node scripts/verify-requirements-model.mjs --run", limits: "Own private project; monthly budget 3; no explicit provider retries; disable owned policy on failure; preserved evidence project on resume" }, null, 2));
  process.exit(0);
}

class VerificationError extends Error {
  constructor(code) { super(code); this.code = code; }
}
function check(value, code) { if (!value) throw new VerificationError(code); }
function apiClient() {
  const cookies = new Map();
  let csrf = "";
  return async function request(path, method = "GET", body) {
    let res;
    try {
      res = await fetch(`${base}/api/v1${path}`, {
        method,
        headers: { "Content-Type": "application/json", Origin: origin, Cookie: [...cookies].map(([key, value]) => `${key}=${value}`).join("; "), "X-CSRF-Token": csrf },
        body: body === undefined ? undefined : JSON.stringify(body),
        signal: AbortSignal.timeout(15000),
      });
    } catch { throw new VerificationError("isolated_api_request_failed"); }
    for (const cookie of res.headers.getSetCookie()) {
      const pair = cookie.split(";")[0], index = pair.indexOf("=");
      cookies.set(pair.slice(0, index), pair.slice(index + 1));
    }
    let payload;
    try { payload = res.status === 204 ? {} : await res.json(); }
    catch { throw new VerificationError("isolated_api_invalid_json"); }
    if (!res.ok) {
      const apiCode = String(payload.error?.code ?? payload.code ?? "unknown");
      throw new VerificationError(`http_${res.status}_${/^[a-z_]{1,80}$/.test(apiCode) ? apiCode : "unknown"}`);
    }
    if (payload.data?.csrf_token) csrf = payload.data.csrf_token;
    return payload.data;
  };
}
const request = apiClient();
let evidence;
try { evidence = JSON.parse(await readFile(evidenceURL, "utf8")); }
catch (error) { if (error.code !== "ENOENT") throw new VerificationError("existing_evidence_unreadable"); }
evidence ??= {
  schema_version: 1, generated_by: "scripts/verify-requirements-model.mjs", verification_id: randomUUID(), created_at: new Date().toISOString(),
  environment: { api_origin: target.origin, app_origin: origin, required_instance_name: "My Jira Requirements Test", isolated_test_database: "myjira_requirements_test", private_project: true },
  scope: { fixture: "tests/fixtures/requirements-prd.md", fixture_sha256: fixtureHash, source: "bounded synthetic English derivative; 3 Epics, 3 Stories, 3 initial Tasks; one incremental Task", initial_source_sha256: sha256(initialSource), incremental_source_sha256: sha256(incrementalSource), max_provider_calls: 3 },
  steps: {}, status: "running",
  limits: ["Selected synthetic PRD workflow only; the complete repository PRD is not exercised.", "No real GitHub request or credential verification.", "This run does not establish model accuracy for other inputs or production readiness.", "No cookies, tokens, credentials, raw prompts, raw model text or user fields are retained in this artifact."],
};
check(evidence.generated_by === "scripts/verify-requirements-model.mjs" && evidence.schema_version === 1 && evidence.scope.initial_source_sha256 === sha256(initialSource) && evidence.scope.incremental_source_sha256 === sha256(incrementalSource), "existing_evidence_provenance_mismatch");
const checkpoint = async () => {
  evidence.updated_at = new Date().toISOString();
  const temporary = new URL(`${evidenceURL.href}.tmp`);
  await writeFile(temporary, `${JSON.stringify(evidence, null, 2)}\n`, { mode: 0o644 });
  await rename(temporary, evidenceURL);
};
let projectPath;
let terminalRecovery = false;
async function policy() {
  const value = await request(`${projectPath}/automation/policy`);
  check(value.calls_used <= 3 && value.budget_calls === 3 && (value.enabled === true || terminalRecovery) && value.allowed_kinds.length === 1 && value.allowed_kinds[0] === "decompose" && value.max_rounds === 1, "bounded_policy_changed");
  evidence.provider_calls_used = value.calls_used;
  evidence.last_observed_policy = { version: value.version, enabled: value.enabled, calls_used: value.calls_used, budget_calls: value.budget_calls, observed_at: new Date().toISOString() };
  return value;
}
function safeFailure(run) {
  const text = String(run.failure ?? "");
  const known = [
    [/provider did not finish/i, "provider_incomplete_response"], [/provider could not be reached|provider request failed/i, "provider_transport_failed"], [/provider returned no text/i, "provider_empty_response"], [/provider rejected|provider returned/i, "provider_rejected_request"],
    [/decomposition schema|exactly one JSON/i, "model_schema_invalid"], [/Choose one valid existing Epic or parent key/i, "model_epic_parent_choice_invalid"], [/Source excerpt/i, "model_source_location_invalid"], [/minute interval|estimation assumptions/i, "model_estimate_invalid"],
    [/policy|budget/i, "policy_or_budget_rejection"], [/changed|stale/i, "input_changed"], [/deadline|timed out|timeout/i, "request_timeout"],
  ];
  const status = text.match(/AI provider returned HTTP ([1-5][0-9]{2})/);
  return { category: known.find(([pattern]) => pattern.test(text))?.[1] ?? "run_did_not_apply", ...(status ? { provider_http_status: Number(status[1]) } : {}), message_sha256: sha256(text) };
}
async function awaitRun(id, label) {
  const started = Date.now();
  let lastStatus = "";
  for (;;) {
    const run = await request(`${projectPath}/automation/runs/${id}`);
    if (run.status !== lastStatus) {
      console.log(JSON.stringify({ step: label, run_id: id, status: run.status, elapsed_seconds: Math.round((Date.now() - started) / 1000) }));
      lastStatus = run.status;
    }
    if (terminal.has(run.status)) return run;
    check(Date.now() - started < 150000, "durable_worker_poll_timeout");
    await delay(1500);
  }
}
function summarizeRun(run) {
  return {
    run_id: run.id, status: run.status, policy_version: run.policy_version, input_fingerprint: run.input_fingerprint, attempts: run.attempts,
    created_at: run.created_at, updated_at: run.updated_at, algorithm_version: run.algorithm_version, batch_id: run.batch_id,
    command_count: run.output?.commands?.length ?? 0, created_count: (run.after_state ?? []).filter(item => item.created).length,
    source_mapping_input_count: run.input?.mappings?.length ?? 0, before_count: run.before_state?.length ?? 0, after_count: run.after_state?.length ?? 0,
    ...(success.has(run.status) ? {} : { failure: safeFailure(run) }),
  };
}
async function execute(label, source) {
  const old = evidence.steps[label];
  let run;
  if (old?.run_id) run = await awaitRun(old.run_id, label);
  else {
    // A response/checkpoint can be lost after the third call has been reserved.
    // Recover that owned durable run before deciding whether a new call fits.
    const existing = (await request(`${projectPath}/automation/runs`)).find(value => value.idempotency_key === `${evidence.verification_id}:${label}`);
    if (existing) run = existing;
    else {
      check(!terminalRecovery, "terminal_recovery_cannot_enqueue");
      const currentPolicy = await policy();
      check(currentPolicy.calls_used < 3, "model_call_budget_exhausted");
      run = await request(`${projectPath}/automation/runs`, "POST", { kind: "decompose", idempotency_key: `${evidence.verification_id}:${label}`, source: { key: evidence.source_key, text: source }, cause: "isolated-real-model-verification", round: 0 });
    }
    evidence.steps[label] = { run_id: run.id, status: run.status };
    await checkpoint();
    run = await awaitRun(run.id, label);
  }
  evidence.steps[label] = { ...old, ...summarizeRun(run) };
  await policy();
  await checkpoint();
  check(success.has(run.status), `${label}_${safeFailure(run).category}`);
  return run;
}
function validateApplied(run, snapshot, source, expectedTasks, protectedID) {
  const result = run.output?.results;
  check(result?.provider === "openai" && result?.model === "gpt-5.6-terra", "unexpected_applied_provider_or_model");
  const decomposition = result.decomposition;
  check(Array.isArray(decomposition?.entities), "decomposition_result_missing");
  const entities = decomposition.entities;
  const counts = { epic: 0, story: 0, task: 0 };
  for (const entity of entities) counts[entity.kind] = (counts[entity.kind] ?? 0) + 1;
  check(counts.epic === 3 && counts.story === 3 && counts.task === expectedTasks && entities.length === 6 + expectedTasks, "unexpected_model_entity_counts");
  const lines = source.split("\n"), seen = new Map(), itemByID = new Map(snapshot.items.map(item => [item.id, item])), idsByKey = new Map();
  const sourceTasks = new Map(), modeledTasks = new Map(), sectionKinds = new Map();
  const sections = lines.map((line, index) => ({ id: line.match(/^## (R[123]) /)?.[1], line: index + 1 })).filter(section => section.id);
  const sectionAt = line => sections.findLast(section => section.line <= line)?.id;
  for (const line of lines) {
    const task = line.match(/^Task ([A-Z0-9-]+):/), interval = line.match(/estimate ([0-9]+)-([0-9]+) minutes/);
    if (task && interval) sourceTasks.set(task[1], { min: Number(interval[1]), max: Number(interval[2]), dependency: line.match(/depends on ([A-Z0-9-]+);/)?.[1] });
  }
  for (const mapping of run.input.mappings ?? []) idsByKey.set(mapping.entity_key, mapping.work_item_id);
  for (let i = 0; i < run.output.commands.length; i++) {
    const command = run.output.commands[i];
    if (command.operation === "create") idsByKey.set(command.client_id, run.after_state[i]?.item_id);
  }
  let dependencyCount = 0;
  for (const entity of entities) {
    check(!seen.has(entity.key), "model_entity_key_duplicate");
    const loc = entity.source;
    check(Number.isInteger(loc.start_line) && Number.isInteger(loc.end_line) && loc.start_line >= 1 && loc.end_line >= loc.start_line && loc.end_line <= lines.length && loc.quote?.trim() && lines.slice(loc.start_line - 1, loc.end_line).join("\n").includes(loc.quote), "model_source_quote_mismatch");
    const section = sectionAt(loc.start_line);
    check(section && section === sectionAt(loc.end_line), "model_source_section_ambiguous");
    const sectionKind = `${section}:${entity.kind}`;
    sectionKinds.set(sectionKind, (sectionKinds.get(sectionKind) ?? 0) + 1);
    const item = itemByID.get(idsByKey.get(entity.key));
    check(item?.requirement_type === entity.kind, "generated_item_missing_or_wrong_type");
    if (entity.kind === "epic") check(!item.parent_id && !entity.parent_key, "epic_parent_invalid");
    else {
      check(seen.get(entity.parent_key)?.kind === (entity.kind === "story" ? "epic" : "story"), "model_hierarchy_invalid");
      check(seen.get(entity.parent_key)?.source_section === section, "model_parent_source_section_mismatch");
      check(item.parent_id === idsByKey.get(entity.parent_key), "persisted_hierarchy_invalid");
    }
    if (item.id !== protectedID) {
      check(item.name === entity.title, "model_title_not_applied");
      if (entity.kind === "story") check(entity.role?.trim() && entity.goal?.trim() && entity.benefit?.trim() && entity.acceptance_criteria?.length > 0 && item.story_role === entity.role && item.story_goal === entity.goal && item.story_benefit === entity.benefit && JSON.stringify(item.acceptance_criteria) === JSON.stringify(entity.acceptance_criteria), "story_contract_not_applied");
      if (entity.kind === "task") {
        check(Number.isInteger(entity.estimated_minutes_min) && Number.isInteger(entity.estimated_minutes_max) && entity.estimated_minutes_min > 0 && entity.estimated_minutes_max >= entity.estimated_minutes_min && entity.assumptions?.length > 0 && entity.skills?.length > 0, "task_estimate_contract_invalid");
        const taskLines = lines.slice(loc.start_line - 1, loc.end_line).filter(line => line.startsWith("Task "));
        check(taskLines.length === 1, "model_task_source_ambiguous");
        const taskKey = taskLines[0].match(/^Task ([A-Z0-9-]+):/)?.[1], expected = sourceTasks.get(taskKey);
        check(expected && !modeledTasks.has(taskKey) && entity.estimated_minutes_min === expected.min && entity.estimated_minutes_max === expected.max, "model_estimate_differs_from_prd_interval");
        modeledTasks.set(taskKey, entity);
        const estimate = Math.floor((entity.estimated_minutes_min + entity.estimated_minutes_max + 1) / 2);
        check(Number.isInteger(item.estimated_minutes) && item.estimated_minutes === estimate && item.remaining_minutes === estimate, "integer_minute_estimate_not_applied");
      }
    }
    for (const dependency of entity.depends_on ?? []) {
      check(snapshot.dependencies.some(edge => edge.source_id === idsByKey.get(dependency) && edge.target_id === item.id), "model_dependency_not_applied");
      dependencyCount++;
    }
    seen.set(entity.key, { ...entity, source_section: section });
  }
  for (const section of ["R1", "R2", "R3"]) check(sectionKinds.get(`${section}:epic`) === 1 && sectionKinds.get(`${section}:story`) === 1, "prd_section_coverage_missing");
  check(modeledTasks.size === sourceTasks.size, "prd_task_coverage_missing");
  for (const [key, expected] of sourceTasks) {
    const entity = modeledTasks.get(key), expectedKeys = expected.dependency ? [modeledTasks.get(expected.dependency)?.key] : [];
    check(entity && JSON.stringify(entity.depends_on ?? []) === JSON.stringify(expectedKeys), "model_dependency_differs_from_prd");
  }
  check(snapshot.items.length === entities.length, "duplicate_or_unexpected_work_items");
  check(dependencyCount === (expectedTasks === 3 ? 1 : 2) && snapshot.dependencies.length === dependencyCount, "expected_prd_dependency_missing");
  check(decomposition.complete === false && decomposition.unresolved.length > 0, "missing_prd_inputs_not_disclosed");
  check(run.batch_id && run.after_state.length > 0, "automatic_batch_application_missing");
  return { counts, item_count: snapshot.items.length, dependency_count: dependencyCount, unresolved_count: decomposition.unresolved.length, estimated_task_minutes: snapshot.items.filter(item => item.requirement_type === "task").reduce((total, item) => total + item.estimated_minutes, 0), source_locations_valid: true, all_three_prd_sections_covered: true, all_explicit_tasks_covered: true, source_estimate_intervals_preserved: true, source_dependency_directions_preserved: true, hierarchy_persisted: true, story_fields_and_criteria_persisted: true, integer_minute_estimates_persisted: true, task_dependencies_persisted: true, unresolved_inputs_disclosed: true, automatic_batch_applied: true, item_ids: [...itemByID.keys()].sort(), revision: snapshot.revision, provider: result.provider, model: result.model };
}

try {
  await request("/auth/csrf");
  const instance = await request("/instance");
  check(instance.name === "My Jira Requirements Test" && instance.is_setup_done, "isolated_instance_guard_failed");
  await request("/auth/login", "POST", { email: "demo@myjira.local", password: "MyJira-Local-2026!" });
  const admin = await request("/auth/me");
  const services = await request("/admin/services");
  check(services.ai?.configured && services.ai.api_key_configured && services.ai.provider === "openai" && services.ai.model === "gpt-5.6-terra", "configured_model_guard_failed");
  evidence.provider = { provider: services.ai.provider, model: services.ai.model, configured: true, transport: "Responses", configuration_changed: false };
  const workspace = (await request("/workspaces")).find(value => value.slug === "studio");
  check(workspace && (!evidence.workspace_id || evidence.workspace_id === workspace.id), "studio_workspace_not_found_or_changed");
  evidence.workspace_id = workspace.id;
  const workspacePath = `/workspaces/${workspace.id}`;
  await checkpoint();
  const projects = await request(`${workspacePath}/projects`);
  const ownershipMarker = `Synthetic model verifier ${evidence.verification_id}`;
  let project = evidence.project_id ? projects.find(value => value.id === evidence.project_id) : projects.find(value => value.description === ownershipMarker);
  check(!evidence.project_id || project, "evidence_private_project_missing");
  if (!project) project = await request(`${workspacePath}/projects`, "POST", { name: "Real-model requirements acceptance", identifier: `LM${evidence.verification_id.replaceAll("-", "").slice(0, 8)}`.toUpperCase(), network: "private", description: ownershipMarker });
  check(project.network === "private" && project.description === ownershipMarker, "private_project_ownership_guard_failed");
  evidence.project_id = project.id;
  evidence.source_key ??= `real-model:${evidence.verification_id}`;
  projectPath = `${workspacePath}/projects/${project.id}`;
  await checkpoint();
  const memberships = await request(`${projectPath}/members`);
  check(memberships.some(member => member.user_id === admin.id && member.role === 20), "project_admin_membership_missing");
  let currentPolicy = await request(`${projectPath}/automation/policy`);
  if (continueIncremental) {
    check(evidence.steps.initial?.semantic_checks && evidence.steps.exact_request_idempotency?.same_run && evidence.steps.same_source?.status === "blocked" && !evidence.steps.same_source.semantic_checks && currentPolicy.calls_used <= 3, "incremental_continuation_guard_failed");
    if (!currentPolicy.enabled) {
      if (currentPolicy.calls_used === 3) {
        // A final POST may have applied even if its response/checkpoint was lost.
        // Recover it without re-enabling the policy or granting another call.
        const previous = (await request(`${projectPath}/automation/runs`)).find(run => run.id === evidence.steps.incremental?.run_id || run.idempotency_key === `${evidence.verification_id}:incremental`);
        check(previous && evidence.human_edit, "terminal_incremental_run_not_found");
        evidence.steps.incremental = { ...evidence.steps.incremental, run_id: previous.id, status: previous.status };
        terminalRecovery = true;
      } else {
        check(currentPolicy.calls_used === 2 && !evidence.steps.incremental?.run_id && currentPolicy.budget_calls === 3 && currentPolicy.authorized_by === admin.id, "incremental_remaining_budget_guard_failed");
        currentPolicy = await request(`${projectPath}/automation/policy`, "PUT", { ...currentPolicy, enabled: true });
      }
    }
    evidence.continuation ??= { reason: "Use the single remaining call for incremental mapping and human-edit preservation; retain the rejected same-source result", policy_version: currentPolicy.version, started_at: new Date().toISOString(), prior_calls_used: currentPolicy.calls_used };
    await checkpoint();
  }
  if (!evidence.policy_version) {
    check(Array.isArray(currentPolicy.allowed_entities), "latest_automation_entity_policy_unavailable");
    check(currentPolicy.calls_used === 0 && (currentPolicy.version === 0 || (currentPolicy.version === 1 && currentPolicy.authorized_by === admin.id)), "new_project_automation_already_used");
    if (currentPolicy.version === 0) currentPolicy = await request(`${projectPath}/automation/policy`, "PUT", { ...currentPolicy, enabled: true, allowed_kinds: ["decompose"], allowed_entities: ["epic", "story", "task"], max_changes: 30, budget_calls: 3, min_interval_seconds: 0, max_rounds: 1 });
    else await policy(); // Recover our successful policy PUT after a lost checkpoint.
    evidence.policy_version = currentPolicy.version;
    await checkpoint();
  }
  await policy();
  const sourcePaths = ["apps/api/internal/automation/decompose.go", "apps/api/internal/automation/worker.go", "apps/api/internal/automation/service.go", "apps/api/internal/integrations/ai.go", "apps/api/internal/requirements/service.go", "scripts/verify-requirements-model.mjs"];
  const currentSourceHashes = Object.fromEntries(await Promise.all(sourcePaths.map(async path => [path, sha256(await readFile(new URL(`../${path}`, import.meta.url)))])));
  evidence.source_files_sha256 ??= currentSourceHashes;
  if (continueIncremental) evidence.continuation.source_files_sha256 ??= currentSourceHashes;
  const first = await execute("initial", initialSource);
  if (!evidence.steps.initial.semantic_checks) {
    const snapshot = await request(`${projectPath}/requirements/snapshot`);
    evidence.steps.initial.semantic_checks = validateApplied(first, snapshot, initialSource, 3);
    await checkpoint();
  }
  if (!evidence.steps.exact_request_idempotency) {
    const before = (await policy()).calls_used;
    const replay = await request(`${projectPath}/automation/runs`, "POST", { kind: "decompose", idempotency_key: `${evidence.verification_id}:initial`, source: { key: evidence.source_key, text: initialSource }, cause: "isolated-real-model-verification", round: 0 });
    const after = (await policy()).calls_used;
    check(replay.id === first.id && before === after, "exact_request_idempotency_failed");
    evidence.steps.exact_request_idempotency = { run_id: replay.id, same_run: true, additional_provider_calls: 0 };
    await checkpoint();
  }
  if (!continueIncremental) {
    const repeated = await execute("same_source", initialSource);
    if (!evidence.steps.same_source.semantic_checks) {
      const snapshot = await request(`${projectPath}/requirements/snapshot`);
      const checks = validateApplied(repeated, snapshot, initialSource, 3);
      check(JSON.stringify(checks.item_ids) === JSON.stringify(evidence.steps.initial.semantic_checks.item_ids) && repeated.after_state.every(item => !item.created), "same_source_created_duplicates");
      evidence.steps.same_source.semantic_checks = { ...checks, stable_work_item_ids: true, no_duplicate_creation: true };
      await checkpoint();
    }
  } else {
    // Record the retained rejection with current safe classification, without
    // resubmitting it or replacing it with an inferred success.
    const rejected = await request(`${projectPath}/automation/runs/${evidence.steps.same_source.run_id}`);
    const previous = evidence.steps.same_source;
    evidence.steps.same_source = { ...previous, ...summarizeRun(rejected) };
    if (!previous.rejected_without_item_creation_or_deletion) {
      const snapshot = await request(`${projectPath}/requirements/snapshot`);
      const ids = snapshot.items.map(item => item.id).sort();
      check(JSON.stringify(ids) === JSON.stringify(evidence.steps.initial.semantic_checks.item_ids), "rejected_same_source_changed_work_items");
    }
    evidence.steps.same_source.rejected_without_item_creation_or_deletion = true;
    await checkpoint();
  }
  if (!evidence.human_edit) {
    const snapshot = await request(`${projectPath}/requirements/snapshot`);
    const story = snapshot.items.find(item => item.requirement_type === "story");
    check(story, "human_edit_story_not_found");
    const edited = await request(`${projectPath}/issues/${story.id}`, "PATCH", { version: story.version, name: "Human-reviewed acceptance title: preserve this edit" });
    evidence.human_edit = { item_id: edited.id, version: edited.version, title_sha256: sha256(edited.name) };
    await checkpoint();
  }
  const incremental = await execute("incremental", incrementalSource);
  const finalSnapshot = await request(`${projectPath}/requirements/snapshot`);
  const finalChecks = validateApplied(incremental, finalSnapshot, incrementalSource, 4, evidence.human_edit.item_id);
  const preserved = finalSnapshot.items.find(item => item.id === evidence.human_edit.item_id);
  check(preserved && sha256(preserved.name) === evidence.human_edit.title_sha256 && preserved.version === evidence.human_edit.version, "human_edit_was_overwritten");
  check(incremental.output.findings.some(value => value.startsWith("Preserved changed or executed entity: ")), "human_edit_preservation_not_reported");
  const previousIDs = evidence.steps.same_source.semantic_checks?.item_ids ?? evidence.steps.initial.semantic_checks.item_ids;
  check(incremental.after_state.filter(item => item.created).length === 1 && previousIDs.every(id => finalChecks.item_ids.includes(id)), "incremental_identity_or_creation_failed");
  evidence.steps.incremental.semantic_checks = { ...finalChecks, human_edit_preserved: true, preservation_reported: true, existing_work_item_ids_preserved: true, new_task_count: 1 };
  let completedPolicy = await policy();
  if (completedPolicy.enabled) completedPolicy = await request(`${projectPath}/automation/policy`, "PUT", { ...completedPolicy, enabled: false });
  evidence.provider_calls_used = completedPolicy.calls_used;
  evidence.last_observed_policy = { version: completedPolicy.version, enabled: completedPolicy.enabled, calls_used: completedPolicy.calls_used, budget_calls: completedPolicy.budget_calls, observed_at: new Date().toISOString() };
  const observedHashes = evidence.continuation?.source_files_sha256 ?? evidence.source_files_sha256;
  const changedRuntimeSources = Object.keys(currentSourceHashes).filter(path => path.startsWith("apps/") && observedHashes[path] !== currentSourceHashes[path]);
  evidence.finalization = { policy_disabled: !completedPolicy.enabled, calls_used: completedPolicy.calls_used, budget_calls: completedPolicy.budget_calls, source_files_sha256: currentSourceHashes, runtime_sources_unchanged_since_last_call: changedRuntimeSources.length === 0, changed_runtime_source_paths: changedRuntimeSources, observed_at: new Date().toISOString() };
  evidence.status = continueIncremental ? "partial" : "passed";
  if (continueIncremental) {
    evidence.failure_code = `same_source_${evidence.steps.same_source.failure.category}`;
    process.exitCode = 2;
  }
  else delete evidence.failure_code;
} catch (error) {
  evidence.status = "failed";
  evidence.failure_code = error instanceof VerificationError ? error.code : "unexpected_verifier_failure";
  if (projectPath) {
    // A failed Asynq task can retry even after the run briefly says "failed".
    // Disable only this verifier's private-project policy under the API's
    // project lock, settling its counter and invalidating further claims.
    try {
      let current = await request(`${projectPath}/automation/policy`);
      if (current.enabled && current.budget_calls === 3) current = await request(`${projectPath}/automation/policy`, "PUT", { ...current, enabled: false });
      evidence.provider_calls_used = current.calls_used;
      evidence.last_observed_policy = { version: current.version, enabled: current.enabled, calls_used: current.calls_used, budget_calls: current.budget_calls, observed_at: new Date().toISOString() };
      evidence.failure_cleanup = { policy_disabled: !current.enabled, policy_version: current.version, calls_used: current.calls_used, observed_at: new Date().toISOString(), purpose: "Prevent background durable retries after this observed failure" };
    } catch { evidence.failure_cleanup = { status: "api_unreachable_or_policy_cleanup_failed", settled_budget_unverified: true }; }
  }
  process.exitCode = 1;
} finally {
  await checkpoint();
  console.log(JSON.stringify({ status: evidence.status, failure_code: evidence.failure_code, project_id: evidence.project_id, provider_calls_used: evidence.provider_calls_used, evidence: "docs/REQUIREMENTS_MODEL_EVIDENCE.json" }, null, 2));
}
