# Work-item query and automation contract

All paths below are relative to `/api/v1`. Browser requests use the ordinary
session and CSRF middleware. Workspace/project visibility is enforced before
filtering; a workspace Guest stays a Guest even when a project role is higher.
The project's `guest_can_view_all` flag defaults to false. Enabling it lets guests
read other project work items and use their comment/reaction/self-subscription
interactions; it does not allow them to edit someone else's work-item fields.

## Lists and advanced filtering

`GET /workspaces/:workspaceID/projects/:projectID/issues` and
`GET /workspaces/:workspaceID/issues` share the same query contract. A normal
response is `{data: Issue[], pagination: {total, has_more, next_cursor}}`.

- `limit`: 1–500, default 100; `cursor`: opaque returned continuation.
- `order_by`: up to four comma-separated fields, with `-` for descending:
  `position, created_at, updated_at, name, sequence_id, start_date, target_date,
  completed_at, estimate, state, priority`. ID breaks ties.
- Comma-separated inclusion filters: `id, project_id, state_id, state_group,
  parent_id, created_by, priority, estimate, estimate_point_id, sequence_id,
  assignee_id, label_id, cycle_id, module_id, subscriber_id, mention_id`.
  User UUID fields accept `me`. Nullable fields/associations accept `null` or
  `none`; priority `none` means the no-priority value.
- Date filters: `start_date, target_date, created_at, updated_at, completed_at`
  accept a date or comma-separated dates. Add `_before` / `_after` for inclusive
  upper/lower bounds. Dates use `YYYY-MM-DD` and timestamps are filtered by UTC date.
- `search` matches title and project identifier plus sequence number.
- `archived`, `draft`, `deleted`: `true`, `false`, or `all`; default `false`.
  Deleted collections require Member. Workspace lists exclude archived projects
  by default; `project_archived=true|all` selects/includes them.
- `include_subitems=false` selects roots; default includes sub-items.
  `scheduled=true` requires both start and target dates; false selects incomplete dates.
- `intake_status=pending,accepted,rejected,duplicate,snoozed` explicitly selects
  intake records. Without this parameter, non-accepted intake entries are excluded.
- `mention_id` matches a Tiptap `{type:'mention',attrs:{id:userUUID}}` node in the
  description JSON or a live comment's JSON. It does not search arbitrary text.

`filter` is a URL-encoded JSON expression. A node is exactly one of
`{and:[...]}`, `{or:[...]}`, `{not:{...}}`, or `{field,op,value}`. Operators are
`eq` (default), `ne`, `in`, `not_in`, `all`, `is_empty`, `not_empty`, `contains`,
`starts_with`, `gt`, `gte`, `lt`, `lte`. Text matching applies to `name`;
comparisons apply to dates and numeric values; `all` requires all selected
association values. `in/not_in/all` take arrays, while other value operators take
a scalar. A date value can also be `{relative_days:-7}`. Bounds: 32 KiB JSON,
80 total nodes, depth six, 200 values in one inclusion condition. Unsupported
fields/operators and malformed values return 400.

```json
{"and":[{"field":"state_group","op":"in","value":["started","unstarted"]},{"or":[{"field":"assignee_id","value":"me"},{"field":"assignee_id","op":"is_empty"}]},{"field":"target_date","op":"lte","value":{"relative_days":7}}]}
```

## Grouped pagination

Supply `group_by`, optionally `sub_group_by`, choosing from `state_id,
state_group, priority, project_id, assignee_id, label_id, cycle_id, module_id,
created_by, estimate_point_id`. The two fields must differ. These requests return:

```json
{
  "data": [{"key":"high","label":"high","total":12,"items":[],"pagination":{"total":12,"has_more":true,"next_cursor":"..."}}],
  "group_by":"priority",
  "sub_group_by":"",
  "total_items":12,
  "pagination":{"total":1,"has_more":false,"next_cursor":null}
}
```

With subgrouping, parent groups have `groups` containing the same leaf shape.
An issue with multiple assignees/labels appears in each corresponding leaf;
parent `total` and `total_items` still count that issue once. Leaf `limit` and
`cursor` apply to each requested leaf. Continue one leaf by also sending its
`group_key` and, if applicable, `sub_group_key`; keep all filters/order unchanged.
`group_limit` (1–200, default100) and `group_cursor` paginate the parent groups.

`show_empty=true` includes standard priority/state-group buckets and all live
states in a project, plus the empty association bucket for association groupings.
`none` is the null bucket key except for priority, where it is the priority value.
Pagination uses stable ordering over current data; it is not a historical snapshot
across concurrent edits.

## Identifier lookup and subscriber management

`GET /workspaces/:workspaceID/issues/lookup/:identifier` resolves a project
identifier and positive sequence number, such as `APP-123`, case-insensitively.
It returns the same full work-item DTO as an individual issue request. Malformed
identifiers return 400; hidden or absent work items return 404. The normal list
filters and current caller permissions apply, including guest ownership rules.
Drafts, archived items, non-accepted intake entries and archived projects are
excluded by default. Use the corresponding lifecycle query parameters to opt in;
deleted items always remain excluded from this lookup, even with `deleted=true`.

`GET .../issues/:issueID/subscribers` lists currently active eligible project
subscribers. `POST` to that collection accepts optional `{user_id: UUID}`;
omitting the body subscribes the current user. The response is
`{data:{subscribed:true,user_id}}`. `DELETE` on the collection unsubscribes the
current user, while `DELETE .../subscribers/:userID` removes a specific user.
Managing another user's subscription requires Member or Admin. New subscribers
must be active members of both the workspace and project; a guest can subscribe
only to a work item they can currently read. Add/remove are idempotent and do not modify the
work-item version. All operations recheck current membership inside the mutation
transaction, and stale subscriber records do not appear in the list.

## Estimates on work items

Create/PATCH accept `estimate_point_id: UUID|null`. It must be in a live scheme
belonging to the project. Responses include `estimate_point_id`,
`estimate_point_detail` and the numeric `estimate` projection (`null` for category
points). Free numeric `estimate` input remains available when no scheme is active;
with an active scheme choose a point. Do not send both fields in one request.
PATCH still requires current `version`; point changes also increment it.
Switching/disabling the active scheme preserves existing assignments. Explicit
scheme/point deletion clears or replaces them and increments affected versions.
Moving to another project clears the old estimate association.

## Project automation

`GET|PATCH /workspaces/:workspaceID/projects/:projectID/automation`:

```json
{"archive_after_months":0,"close_after_months":0,"close_state_id":null}
```

PATCH requires Admin. Each month count is 0–12; zero disables that operation.
The close target must be a completed/cancelled state in this project. With a null
target and closing enabled, the project's first cancelled state is used. One
month is 30 days, measured from the item's last update. Ordinary project settings
writes cannot bypass this validated endpoint.

`POST .../automation/run` requires Admin and applies the current configuration,
returning `{data:{archived,closed}}`. The worker also runs it hourly. Only live,
non-draft, unarchived work items in active projects qualify. Archiving applies to
completed/cancelled items, closing to other workflow groups. A cycle/module with
no ending date or an ending date today/in the future blocks the action, as do
pending/snoozed intake records. Every actual change increments the version and
writes an activity, version snapshot and outbox event atomically. Repeating the
same maintenance pass does not repeat its effects.

The maintenance worker separately keeps the newest 20 page/work-item versions,
and removes webhook delivery logs after 14 days only when delivered or after 11
attempts (initial attempt plus ten retries). It does not delete projects or work
items. Exported worker integration is `RegisterJobs(mux,deps)` plus
`RunScheduler(ctx,deps)`.

Existing comments support `body_html`, `body_json`, optional `parent_id`; comment
edit/delete authorize author/admin as appropriate. There is no bulk-comment
endpoint. Batch work-item edits use `PATCH .../issues/bulk` with
`{ids,changes,versions:{issueUUID:version}}`; any failed member rolls back all.
