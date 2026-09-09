# Planning API

All routes are relative to `/api/v1`; successful results are wrapped in `data`.
The project prefix is `/workspaces/:workspaceID/projects/:projectID`.

| Resource | Collection | Fields |
| --- | --- | --- |
| Cycle | `/cycles` | `name,description,start_date,end_date,owner_id,position,settings,archived` |
| Module | `/modules` | `name,description,start_date,target_date,lead_id,status,member_ids,position,settings,archived` |
| Saved view | `/views` | `name,description,layout,filters,display,is_private,position` |

Collections support GET/POST. Resources support GET/PATCH/DELETE at `/:cycleID`,
`/:moduleID`, `/:viewID`. Workspace views use `/workspaces/:workspaceID/views`.
Names are required on creation. `archived` is a write-only boolean; responses use
`archived_at`. Optional dates and lead IDs accept explicit null. Lists accept
`search` and `archived=true`. Module status includes backlog, planned, in-progress,
paused, completed, cancelled. Layout is list, kanban, calendar, spreadsheet or gantt.

GET `/:resource/:id/items` lists its active work items. POST with `work_item_ids`
adds them; adding to a cycle moves the work items from their previous cycle.
DELETE `/:resource/:id/items/:itemID` removes an association.
POST `/cycles/:cycleID/transfer` accepts `target_cycle_id,work_item_ids`; an omitted
or empty list transfers active incomplete source items. Explicit IDs must also be
incomplete. Completed/cancelled work remains in the source, and a destination
whose end date has passed is rejected. This operation is transactional.

GET `/:resource/:id/progress` returns total, completed, cancelled, started, overdue,
completion_percentage, estimate_total, estimate_completed and state distributions.
It also returns `backlog,unstarted,assignees,labels,burndown,start_date,end_date,
burndown_step_days,is_snapshot,snapshot_at`. Assignee/label rows include
`id,name,color?,avatar_url?,count,completed,cancelled,started,estimate,
estimate_completed`; missing assignments use null IDs. A work item contributes
once per assigned member/label, so row totals can exceed the distinct total.

Burndown rows are `{date,remaining,estimate_remaining,ideal,estimate_ideal}`.
Future actual values are null; the ideal line spans the configured period. Dates
are daily for ordinary ranges and use a stated day stride for very long ranges.
Unscheduled resources return an empty chart. A cycle transfer atomically freezes
the first pre-transfer progress and distributions; retries retain that snapshot.
Default progress then reads the frozen statistics, with `?live=true` available
for current remaining work. Guest aggregates include only the guest's own created
items unless the project's current `guest_can_view_all` setting is enabled.
The current setting also applies to historical snapshots, resource counts and
associated item lists, and never permits guest mutations. Raw snapshots never appear in resource DTOs
or bookmarks. Failed transfers leave associations, snapshots and outbox unchanged.

Association changes increment affected work-item versions and produce work-item
activity/outbox records. Cycle/module CRUD and links publish `.changed` events;
membership changes and both sides of transfer publish `.items_changed` through
the transaction outbox. Views remain private to their normal visibility policy
and do not produce project Webhook events.

Reads require workspace/project access. Mutations require a member role. Deleting
cycles/modules requires an admin. Views are controlled by their owner/admin;
private views are only visible to the owner. Related users and work items must
belong to the same active workspace/project. One active cycle per work item is
enforced transactionally and by a database index.

GET/POST `/modules/:moduleID/links` and PATCH/DELETE
`/modules/:moduleID/links/:linkID` manage external links with `{title,url}`.
URLs must use HTTP or HTTPS without embedded credentials. Empty titles use the
URL hostname. Reads allow guests; mutations require active project membership
and an unarchived module. Link IDs are scoped to their module and project.

# Estimate schemes and cycle dates

The following additional paths are relative to
`/api/v1/workspaces/:workspaceID/projects/:projectID`. Successful requests use the
normal `{data: ...}` wrapper. Reads permit Guest; estimate mutations require Admin.

| Method/path | Input and result |
| --- | --- |
| GET `/estimate-templates` | Fibonacci, linear numeric, and size-category templates; each has `id,name,kind,points` |
| GET `/estimates` | All project schemes, including their ordered live `points` |
| POST `/estimates` | `{name,description?,kind:'points'|'categories',points:[{label,numeric_value?,position?}]}`; 1–100 initial points |
| GET/PATCH `/estimates/:estimateID` | Read scheme / update `{name?,description?}`; kind is immutable |
| DELETE `/estimates/:estimateID` | Removes scheme, clears active project pointer and affected work-item estimates |
| POST `/estimates/:estimateID/points` | `{label,numeric_value?,position?}` |
| PATCH `/estimates/:estimateID/points/:pointID` | Any of `{label,numeric_value,position}`; updates linked numeric projections and versions |
| DELETE `/estimates/:estimateID/points/:pointID` | Optional `?replacement_id=UUID`; replacement must be live in the same scheme, otherwise affected values clear |
| GET/PATCH `/estimate-settings` | Read `{estimate_id,estimate}` / write `{estimate_id:UUID|null}` to switch or disable |
| POST `/cycles/check-dates` | Member/Admin; `{start_date,end_date,exclude_cycle_id?}` → `{available,conflicts:[{id,name,start_date,end_date,archived_at}]}` |

Point labels contain 1–20 characters and are unique within a scheme. Numeric
schemes require a finite nonnegative numeric value; category points require null.
Switching/disabling the active scheme preserves existing work-item assignments.
Point deletion/replacement and scheme deletion update affected assignments,
increment their versions and record events. Deletion and assignment are
serialized per project so concurrent writes cannot attach a stale scheme point.
All referenced schemes and points are validated against workspace and project.

Cycle date ranges are inclusive. The check includes archived, nondeleted cycles
and rejects an exclusion ID outside the project. This is an advisory check;
overlapping cycles can still be explicitly saved, matching the reference API's
separate check workflow. Creating a scheduled cycle requires both dates; a draft
can have neither. Partial updates retain the existing boundary when validating
date order.
