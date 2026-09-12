# Resources and deterministic scheduling

These independently authored handlers use the existing project prefix:
`/api/v1/workspaces/:workspaceID/projects/:projectID/resources`.
Responses use the usual `{data: ...}` envelope. Resource configuration defaults
to unknown availability; it never invents an eight-hour working day.

After project authorization, all five HTTP endpoints return
`403 requirements_disabled` when `project.settings.requirements_enabled` is false.
Missing, null and true values keep the feature enabled. Resource and timezone
writes recheck the flag after waiting for their locks. Disabling the feature keeps
stored configuration intact. Internal authorized snapshots remain available to
the independently controlled automation policy.

| Method and suffix | Role | Behavior |
| --- | --- | --- |
| `GET /` | Guest with project access | Current active members, resource configuration and project timezone |
| `PUT /members/:memberID` | Admin | Replace configuration with optimistic `version`; zero creates the first configuration |
| `PATCH /` | Admin | Update an IANA `timezone` |
| `GET /load` | Guest with project access | Complete authorized project workload with daily and weekly totals |
| `POST /schedule` | Member | Return a deterministic proposal and input revision; no direct work-item mutations |

Member configuration requires `version`, `skills` (string array),
`weekday_minutes` (seven nullable integer values, Sunday through Saturday),
`project_minutes_per_day` (nullable integer), and `exceptions` (object mapping
`YYYY-MM-DD` to nullable integer minutes). Minute capacity values are in 0–1440.
Exceptions override weekdays. Effective capacity is the smaller of the calendar
and project quota; unknown stays unknown unless either constraint is explicitly
zero. Updates record current actor, before/after audit, project revision and outbox
event within the same transaction. Current Admin and target membership are checked
after obtaining the project graph lock. Concurrent writers must reload on 409.

Load queries require inclusive `start_date` and `end_date`, with at most 366 days.
Optional filters are `member_id`, `skill`, `state_id`, `epic_id`, `search`,
`work_item_ids` (at most 500 comma-separated UUIDs), `cycle_id` and
`commitment_cycle_id`. Both Cycle filters accept `backlog`. `cycle_id` means the
task's own execution Cycle; `commitment_cycle_id` means its nearest visible Story's
Cycle, falling back to the task's own Cycle for independent work.

Filters choose task bars and member rows. Daily/weekly `allocated_minutes` and
`over_capacity` retain every authorized recorded task, including other Sprint
work, so a view cannot hide overbooking. `selected_minutes` and `selected_unknown`
describe the filtered task subtotal. Each day also carries `task_minutes`,
`task_ids` and `unknown_task_ids` for attributable overload and unknown-work detail.
`capacity_minutes: null` and `unknown: true` must remain distinguishable from zero.

Workload uses remaining integer minutes exactly once per executable open leaf.
It is divided across assignees by explicit integer weights, or equal initial
weights. Largest remainders use UUID ordering for stable ties. Each member's share
is then distributed over the complete execution interval in proportion to known
working capacity, with stable date rounding. Viewport clipping happens afterward.
Unknown effort, missing dates, removed members, unavailable days and unknown
capacity remain explicit conflict/unassigned/unestimated/unscheduled sets.
Completed and cancelled items carry no future load; reopening exposes the stored,
editable remaining effort again. Story windows and explicit commitment Cycle
windows bound child execution without changing child Cycle assignments.

Scheduling input is `{start_date,end_date,task_ids?,member_ids?,hard_deadline?}`.
At most 200 unstarted, unlocked executable tasks are scheduled within a 366-day
horizon. The result includes `algorithm_version`, `input_revision`, `feasible`,
`assignments`, `unscheduled` and `existing_conflicts`. Each assignment includes ID,
work-item version, execution dates, assignee IDs, allocation weights and reason.
Protected work reserves existing capacity. Dependency order, priority, deadline,
skills, capacity, Story commitments and fixed successors constrain proposals.
Equal choices use stable date/member/task ordering. The algorithm is a bounded
greedy strategy; failure means this strategy did not find a valid plan, not that
no arrangement can exist. It does not infer execution Cycle membership from dates.

Automation calls `NewService(deps).Snapshot(ctx, tx, scope)`, then the pure
`Schedule(snapshot, request)` outside the commit transaction. It converts
assignments into shared requirements commands using `input_revision` and item
versions. A non-feasible result is not an applied schedule. Policy, authorization,
resource/input revisions and the resulting `Project` projection must be checked
inside the atomic application transaction. Resource code never writes work items.

Pure tests cover conservation, fractional ties, project timezone/DST, holidays,
unknown versus zero, complete/reopen behavior, deadlines, locks, dependencies,
skills, multi-assignee load and deterministic output. Isolated PostgreSQL tests
cover current Guest/tenant scope, removed members, commitment/execution Cycle
separation, configuration versions, waiting-lock authorization, competing writers
and rollback of configuration/audit/events after outbox failure. Feature-gate tests
cover every resource route and disabling the feature during both mutation lock
waits without writing configuration or events. These tests do not
constitute browser, live-provider or production capacity evidence.
