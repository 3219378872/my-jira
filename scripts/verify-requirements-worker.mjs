// Original acceptance probe for a deliberately stopped/restarted dedicated worker.
// Stop only the worker launched by requirements-test-runtime.sh, run --prepare,
// restart that worker, then run --verify. This script never controls services.
import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { readFile, rename, writeFile } from "node:fs/promises";
import { setTimeout as delay } from "node:timers/promises";

const base = "http://127.0.0.1:18088";
const origin = "http://127.0.0.1:14173";
const artifact = new URL(
  "../docs/REQUIREMENTS_WORKER_EVIDENCE.json",
  import.meta.url,
);
const mode = process.argv[2];
assert(
  ["--prepare", "--verify"].includes(mode),
  "Supply --prepare or --verify",
);
const cookies = new Map();
let csrf = "";
async function request(path, method = "GET", body) {
  const response = await fetch(`${base}/api/v1${path}`, {
    method,
    headers: {
      "Content-Type": "application/json",
      Origin: origin,
      Cookie: [...cookies].map(([key, value]) => `${key}=${value}`).join("; "),
      "X-CSRF-Token": csrf,
    },
    body: body === undefined ? undefined : JSON.stringify(body),
    signal: AbortSignal.timeout(15000),
  });
  for (const cookie of response.headers.getSetCookie()) {
    const pair = cookie.split(";")[0],
      index = pair.indexOf("=");
    cookies.set(pair.slice(0, index), pair.slice(index + 1));
  }
  const payload = response.status === 204 ? {} : await response.json();
  assert(response.ok, `Isolated acceptance request failed: ${response.status}`);
  if (payload.data?.csrf_token) csrf = payload.data.csrf_token;
  return payload.data;
}
await request("/auth/csrf");
const instance = await request("/instance");
assert(
  instance.is_setup_done && instance.name === "My Jira Requirements Test",
  "Dedicated instance guard failed",
);
await request("/auth/login", "POST", {
  email: "demo@myjira.local",
  password: "MyJira-Local-2026!",
});
let evidence;
try {
  evidence = JSON.parse(await readFile(artifact, "utf8"));
} catch (error) {
  if (error.code !== "ENOENT") throw error;
}
if (evidence)
  assert.equal(evidence.generated_by, "scripts/verify-requirements-worker.mjs");
else {
  assert.equal(mode, "--prepare", "Prepare the owned run first");
  const workspace = (await request("/workspaces")).find(
    (value) => value.slug === "studio",
  );
  assert(workspace, "Seed the requirements fixture first");
  evidence = {
    generated_by: "scripts/verify-requirements-worker.mjs",
    schema_version: 1,
    verification_id: randomUUID(),
    workspace_id: workspace.id,
    status: "preparing",
    api_origin: base,
    database: "myjira_requirements_test",
    created_at: new Date().toISOString(),
    limits: [
      "Graceful worker stop and restart with a PostgreSQL outbox entry; no Redis restart or in-flight process-kill claim.",
      "Forecast only, zero model budget; no provider request or predictive-accuracy claim.",
    ],
  };
}
async function checkpoint() {
  const temporary = new URL(`${artifact.href}.tmp`);
  await writeFile(temporary, `${JSON.stringify(evidence, null, 2)}\n`);
  await rename(temporary, artifact);
}
const workspacePath = `/workspaces/${evidence.workspace_id}`;
if (!evidence.project_id) {
  const identifier = `R${evidence.verification_id.replaceAll("-", "").slice(0, 10)}`;
  const existing = (await request(`${workspacePath}/projects`)).find(
    (value) => value.identifier === identifier,
  );
  const project =
    existing ??
    (await request(`${workspacePath}/projects`, "POST", {
      name: `Worker recovery acceptance ${identifier}`,
      identifier,
      network: "private",
    }));
  evidence.project_id = project.id;
  await checkpoint();
}
const path = `${workspacePath}/projects/${evidence.project_id}`;
const input = {
  kind: "forecast",
  idempotency_key: evidence.request_key ?? evidence.verification_id,
};
if (mode === "--prepare") {
  assert.notEqual(
    evidence.status,
    "passed",
    "Completed evidence is preserved; use --verify to inspect it",
  );
  if (evidence.run_id) {
    const previous = await request(
      `${path}/automation/runs/${evidence.run_id}`,
    );
    if (previous.status === "blocked") {
      assert.equal(
        previous.failure,
        "The input project revision changed; submit or automatically refresh a stable snapshot",
        "Inspect other failures before preparing a new run",
      );
      evidence.rejected_inputs ??= [];
      evidence.rejected_inputs.push({
        run_id: previous.id,
        status: previous.status,
        attempts: previous.attempts,
        reason: "input_revision_changed",
        before: evidence.before,
        observed_at: new Date().toISOString(),
      });
      input.idempotency_key = `${evidence.verification_id}:refresh:${evidence.rejected_inputs.length}`;
      evidence.request_key = input.idempotency_key;
      evidence.run_id = null;
      await checkpoint();
    }
  }
  let policy = await request(`${path}/automation/policy`);
  if (policy.version === 0 || policy.min_interval_seconds > 60)
    policy = await request(`${path}/automation/policy`, "PUT", {
      ...policy,
      enabled: true,
      allowed_kinds: ["forecast"],
      max_changes: 1,
      budget_calls: 0,
      max_rounds: 1,
      min_interval_seconds: 60,
    });
  assert(
    policy.enabled &&
      policy.budget_calls === 0 &&
      policy.allowed_kinds.join() === "forecast",
  );
  const run = await request(`${path}/automation/runs`, "POST", input);
  evidence.run_id = run.id;
  evidence.input_fingerprint = run.input_fingerprint;
  evidence.before = {
    status: run.status,
    attempts: run.attempts,
    policy_version: run.policy_version,
    observed_at: new Date().toISOString(),
  };
  await checkpoint();
  assert.equal(
    run.status,
    "queued",
    "Stop the dedicated worker before preparing",
  );
  assert.equal(run.attempts, 0);
  evidence.status = "queued_while_worker_stopped";
  await checkpoint();
  console.log(JSON.stringify({ status: evidence.status, run_id: run.id }));
} else {
  assert(evidence.run_id, "Prepared run is required");
  let run;
  for (let attempt = 0; attempt < 45; attempt++) {
    run = await request(`${path}/automation/runs/${evidence.run_id}`);
    if (!["queued", "running"].includes(run.status)) break;
    await delay(1000);
  }
  assert.equal(run.status, "completed");
  assert.equal(run.input_fingerprint, evidence.input_fingerprint);
  assert.equal(run.attempts, 1);
  const forecasts = (await request(`${path}/automation/forecasts`)).filter(
    (value) => value.run_id === run.id,
  );
  assert.equal(forecasts.length, 1);
  assert.equal(forecasts[0].status, "insufficient_data");
  let policy = await request(`${path}/automation/policy`);
  const replay = policy.enabled
    ? await request(`${path}/automation/runs`, "POST", input)
    : { id: evidence.after?.replay_run_id, attempts: evidence.after?.attempts };
  assert.equal(replay.id, run.id);
  assert.equal(replay.attempts, 1);
  assert.equal(policy.calls_used, 0);
  if (policy.enabled)
    policy = await request(`${path}/automation/policy`, "PUT", {
      ...policy,
      enabled: false,
    });
  evidence.after = {
    status: run.status,
    attempts: run.attempts,
    forecast_count: forecasts.length,
    forecast_status: forecasts[0].status,
    replay_run_id: replay.id,
    calls_used: policy.calls_used,
    policy_enabled: policy.enabled,
    observed_at: new Date().toISOString(),
  };
  evidence.status = "passed";
  await checkpoint();
  console.log(
    JSON.stringify({
      status: evidence.status,
      run_id: run.id,
      attempts: run.attempts,
      forecasts: forecasts.length,
      calls_used: policy.calls_used,
    }),
  );
}
