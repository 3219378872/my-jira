# Workspace support API

Prefix `/api/v1/workspaces/:workspaceID`; successful JSON responses use `data`.

| Path | Method and contract |
| --- | --- |
| `/search?q=text` | GET `{projects:[],issues:[],cycles:[],modules:[],pages:[]}`; 25 matches per category |
| `/analytics` | GET overview, distributions, date trends; detailed query contract below |
| `/analytics/export` | POST `{query:{...string parameters}}` queues an analytics CSV report; returns 202 `{id,status,format,report_type,download_url}` |
| `/analyses` | GET saved analysis array; POST admin `{name,description,query:{...string parameters}}` |
| `/analyses/:analysisID` | GET; admin PATCH/DELETE |
| `/analyses/:analysisID/run` | GET evaluates the stored query under the current caller's access |
| `/profiles/:userID` | GET `{user,stats}`; `me` selects current user; target must be an active workspace member |
| `/profiles/:userID/stats` | GET `{created,assigned,pending,completed,subscribed,by_state,by_priority,by_project,cycles}` |
| `/profiles/:userID/activities` | GET `{items,total,limit,offset,has_more}`; limit 1–200, optional date and work-item filters |
| `/profiles/:userID/activities/export` | GET CSV with required inclusive `from,to` dates; at most 10000 rows, spreadsheet formula protection |
| `/notifications` | GET array plus pagination; filters `unread,read,archived,snoozed,reason,project_id`; details below |
| `/notifications/unread-count` | GET `{count}` |
| `/notifications/mark-all-read` | POST `{updated}`; same query filters as the list |
| `/notifications/:notificationID` | PATCH `{read,archived,snoozed_until}` (RFC3339 or null); DELETE |
| `/favorites` | GET array with resolved `entity`; POST `{entity_type,entity_id,position}` |
| `/favorites/:favoriteID` | PATCH `{position}`; DELETE |
| `/recent-visits` | GET array with resolved `entity`; POST `{entity_type,entity_id}` |
| `/preferences` | GET own workspace preference records |
| `/preferences/:key` | GET one (default empty value); PATCH `{value:{...}}` shallow-merges; PUT replaces |
| `/stickies` | GET array (`archived=true` optional); POST creates |
| `/stickies/:stickyID` | GET/PATCH/DELETE own sticky |

Entity types: `project,issue,cycle,module,view,page`. Favorites and recent visits
recheck current membership and private-page/view access every time they are read.
Notifications and analytics filter inaccessible projects. Search and statistics
respect guest work-item visibility and exclude drafts/archived work items.
The project's current `guest_can_view_all` setting expands guest reading to other
work items and public pages. Disabling it immediately restricts subsequent search,
statistics, profile activity, notification, favorite and recent-visit reads to
currently accessible entities; private pages remain owner-only in both modes.

Sticky fields: `title,content_html,content_json,color,position,is_archived`.
HTML is sanitized. Preference/sticky/bookmark operations are limited to the current
user; adding another user's ID to a request never selects their records.

Notification `reason` is a comma-separated selection of
`mentions,assigned,created,subscribed`, matching the recorded delivery reasons.
`read=true` selects read entries and `read=false` or `unread=true` selects unread
entries; contradictory combinations fail. Archives and active snoozes are omitted
unless selected. `limit` is 1–500 (default 100), `offset` is a nonnegative integer.
The response keeps the ordinary `data` array and adds top-level
`pagination:{total,limit,offset,next_offset,has_more}`. Bulk mark-read applies the
same filters and access predicates. Project pages in search, bookmarks and
notifications require explicit current project membership even for public projects.

## Analysis queries

GET `/analytics` preserves `total,completed,started,overdue,by_state,by_priority,
by_project,trend` and adds `estimate,distribution,x_axis,segment,metric,interval,
date_field,from,to`. Each distribution row is `{key,label,segment_key,segment_label,
count,estimate,value}`. `value` follows the selected metric. Work items with no
dimension value use `key="none"`; an omitted segment uses an empty key/label.
Multiple assignees or labels place an item once in each matching bucket; summing
all buckets can therefore exceed the distinct overview total.

The main dimension `x_axis` defaults to `state_group`. It and optional `segment`
accept `state,state_group,priority,project,label,assignee,estimate,cycle,module,
start_date,target_date,created_at,updated_at,completed_at`; they must differ.
`metric` is `count` (default) or `estimate`. `interval` is `day` (default), `week`,
or `month` and controls date dimensions and trend buckets. `estimate` groups by
estimate point; categorical points have no numeric value in estimate totals.

`from,to` are inclusive YYYY-MM-DD dates on `date_field` (default `created_at`,
also `updated_at,completed_at,start_date,target_date`). Without dates, totals
cover all currently eligible work items and trend covers the last 30 days.
Explicit trend ranges span at most ten years. Analyses support the complete
work-item query grammar documented in `../workitems/ROUTES.md`, including multi
value filters and recursive `filter` JSON. Drafts, archived and unaccepted Intake
items are excluded by default. Deleted work items remain excluded.

Saved queries and exports encode these URL parameters as JSON string values,
for example `{"x_axis":"assignee","metric":"estimate","priority":"high,urgent",
"from":"2026-09-01","to":"2026-09-30"}`. Saved analyses are shared workspace
definitions administered by workspace admins. Running a saved definition never
uses its owner's visibility. Export requires membership; the worker rechecks
membership and project roles, generates CSV through `report.generate`, records
the included project scopes, and emits `email.report-ready`. Export history and
download use the existing `/exports` API and recheck access to every included
project. Completion and notification publication are idempotent.

Profiles return safe display details, with no account credentials or private
preferences. Their state/priority/cycle breakdowns describe assigned work items.
Project summaries include created and assigned items visible to the caller.
Activity lists exclude comments, reactions, votes and draft events, apply current
work-item visibility, and accept inclusive activity dates via `from,to`.
