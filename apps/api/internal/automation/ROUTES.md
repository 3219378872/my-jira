# Project automation

All routes use `/api/v1/workspaces/:workspaceID/projects/:projectID/automation`.
Browser automation grants are distinct from external API tokens. Project Member
is the minimum read/run role; policy, cancellation, retry, action creation and
undo require a current explicit project Admin. Current session/token validity,
membership and project/source scope are rechecked after transaction lock waits.

| Method and suffix | Behavior |
| --- | --- |
| `GET /capabilities` | Member-safe enabled flag and allowed algorithm names. |
| `GET /policy`, `PUT /policy` | Full Admin policy; PUT requires its current `version`. |
| `GET /runs`, `POST /runs` | Last 100 currently accessible runs; create a durable, fixed input. |
| `GET /runs/:runID` | Independent input, structured output, before/after fields, failure and undo conflicts. |
| `POST /runs/:runID/cancel` | Cancel queued/in-flight work before any final application. |
| `POST /runs/:runID/retry` | Retry the same still-current input, up to ten attempts. |
| `POST /runs/:runID/undo` | Restore an entire unchanged batch; return 409 conflicts and preserve everything after later item/comment/relation changes. |
| `GET /forecasts` | P50/P80, historical coverage, assumptions, expanding time-split scores and staleness. |
| `GET /risks` | Current causes, source evidence, linked actions and lifecycle history. |
| `PATCH /risks/:riskID` | `{version,status}`; `open`, `acknowledged`, or `resolved`. A persistent cause reopens on reevaluation. |
| `POST /risks/:riskID/action` | Policy-authorized response task and atomic risk link. |
| `GET /improvements` | Fixed metric baseline, target metric, action, owner and observations. |
| `POST /improvements/:improvementID/action` | Policy-authorized process task and atomic observation link. |
| `POST /improvements/:improvementID/observe` | Record metrics after actual action execution; distinguish observing, improved, no improvement and insufficient evidence. |

Policy controls `allowed_kinds`, `allowed_entities` (epic/story/task/work_item),
`allowed_operations` (create/update), `allowed_fields`, item/member/page ID
scopes, hard deadline, maximum changes, monthly provider-call budget, minimum
frequency and maximum causal rounds. Empty item/member/page lists use the
current authorized project scope. A policy confined to existing item IDs cannot
create new objects. Empty entity types retain the default set for compatibility.
`calls_used` counts real reserved provider attempts in the current UTC month;
changing policy configuration does not reset usage.
For an immutable external report, `max_changes` caps the total applied actions
across its distinct findings. A rejected finding commits no task, run, history,
event or action link; replaying an already applied finding remains idempotent
after the report reaches its cap.

Run creation accepts `{kind,idempotency_key,source?,start_date?,end_date?,task_ids?}`.
Kinds are `decompose`, `schedule`, `forecast`, `risk`, and `efficiency`; the GitHub
worker uses the internal `ApplyExternal` boundary for `quality`. A decomposition
source is one exact `{page_id,revision}` or `{text,key?}` upload. Keep the upload
key stable across revisions. Upload keys have their own namespace and cannot
impersonate Page sources. Source text is capped at 50,000 bytes. Structured
entities require exact source lines/quotes, valid hierarchy, acyclic dependency
references, testable Story acceptance conditions and positive integer-minute
Task estimates with assumptions. Model output is never executable code.

Run details expose `before_state` and `after_state` arrays containing
`{item_id,version,fields,created,fingerprint}` and a `batch_id`. Validated proposed
output survives a stale-input commit rejection. Policy changes cancel queued
and active runs. Historical source access is checked for the actual requesting
member before enqueue, provider use, commit and later reads; the policy Admin
does not expand the requester's source access. Composite records whose source
items moved outside the project are unavailable. Private Page sources cannot
generate broader project-visible work.

The worker registers `automation.run.v1` with the existing Asynq outbox.
`RunScheduler(ctx,deps)` reconciles once per minute. Configured/previously
processed Pages are watched for stable revisions; ordinary project documents
are not automatically classified as PRDs. Input fingerprints deduplicate work,
expired worker leases recover, and stale inputs can refresh within the same
bounded reason chain. Applied decomposition queues schedule, schedule queues
risk (including infeasible plans), and risk can queue efficiency. Exact rounds
depend on policy. AI-origin events cannot restart the chain at round zero.
Policies whose project was deleted or whose authorizing Admin lost access are
skipped. Other project failures retain their original error causes and are
reported after the remaining projects have been processed, so one invalid
project configuration cannot prevent another project from being queued.

The deterministic scheduler is shared with the resources API; unknown effort or
capacity and protected work produce explicit conflicts. No assignment is applied
when the complete proposed plan is infeasible. Parent Story commitments remain
independent from execution dates. The historical forecast resamples observed
weekly team throughput with a deterministic seed. It requires at least twenty
distinct completions and four observed weeks, excludes future-recorded facts,
and scores only historical scopes with a subsequently observable outcome.
Unobservable moved/deleted history suppresses confidence rather than introducing
survivor bias. Forecasts describe a fixed remaining scope under historical
throughput assumptions; current resource constraints are recorded and evaluated
by the scheduler/risk projection separately.

Quality risks use the binding's current report pointer rather than arrival
time, so a late old report cannot become newest evidence. Current installation,
Page, Story-provenance and scenario-source access remain required. Persistent
risk causes update one record without repeating notifications. Improvement
outcomes require actual linked action execution, complete observation windows
and comparable scope; creating a suggestion or task never proves an efficiency
gain.

Validation lives in `analysis_test.go`, `integration_test.go` and
`scheduler_test.go`. The PostgreSQL suite uses `TEST_DATABASE_URL` with an
explicitly named test database and one
isolated schema per fixture. It covers a controlled Responses-shaped provider,
first/incremental decomposition, manual conflicts, budgets, model-time source
changes, source ACLs and reserved-key attacks, scheduler application/undo, risk
deduplication, a bounded four-stage chain, concurrent idempotence, revoked
membership during lock waits, atomic callback rollback, durable comment
undo conflicts, reconciliation after project deletion or Admin removal/demotion,
preservation of configuration and SQL errors while processing other projects,
and atomic rejection of additional report findings at the policy action cap.
These fixtures do not establish real provider availability,
production worker restart behavior, real prediction accuracy or team gains.
