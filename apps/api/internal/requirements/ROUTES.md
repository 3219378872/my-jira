# Requirements and planning commands

All routes use `/api/v1/workspaces/:workspaceID/projects/:projectID`. They use the
existing browser/API identity and project policy; Guest work-item visibility is
preserved. The SSE subscription requires a browser session and independently
rechecks its current session, membership and project visibility every second.

| Method | Path | Behavior |
| --- | --- | --- |
| GET | `/requirements/snapshot` | Complete authorized items, activities, cycles, normalized blocking dependencies and states, with one consistent `revision` |
| GET | `/requirements/activities` | Ordered authorized activities, including empty activities available to the reader |
| POST | `/requirements/activities` | Create `name`, optional `epic_id`, `position` and `archived_at` |
| PATCH | `/requirements/activities/:activityID` | Versioned activity update; moving a populated activity to another Epic requires moving its Stories first |
| DELETE | `/requirements/activities/:activityID?version=N&migrate_to=UUID` | Atomically migrate Stories to an active activity in the same Epic and soft-delete the activity; explicit `migrate_to=null` preserves each Story/Epic while clearing its activity |
| POST | `/planning/changesets` | Versioned, idempotent atomic work-item create/update/delete commands |
| GET | `/requirements/events?cursor=N` | Durable SSE replay; `Last-Event-ID` takes precedence; expired cursors require a new snapshot |
| GET | `/requirements/history?limit=200` | Long-term authorized work-item facts, including related mutations; Member access required |

Existing `/issues` create/patch/detail/list routes expose the same extensions:
`requirement_type` (`null`, `epic`, `story`, `task`), `story_role`, `story_goal`,
`story_benefit`, `acceptance_criteria` (string array), `activity_id`, `map_position`,
`estimated_minutes`, `remaining_minutes`, `required_skills`, `allocation_weights`
(`[{member_id,weight}]`), `planning_locked` and `dependency_ids` (predecessors).
Projected reads explicitly allow these fields. Optional types preserve legacy
work-item semantics; names do not classify an existing item.

```json
{
  "idempotency_key": "client-generated-stable-key",
  "expected_revision": 120,
  "commands": [
    {
      "operation": "update",
      "id": "work-item-uuid",
      "version": 3,
      "fields": {"cycle_id": "cycle-uuid", "map_position": 2048}
    }
  ]
}
```

`expected_revision` is optional for manual commands, while each update/delete
requires its work-item `version`. Creation supports `client_id`; later commands
may refer to that ID using `$client_id` in `parent_id` or `dependency_ids`.
Parents and dependencies must precede their references. Every command is
validated in one transaction, which also stores the batch, immutable history
facts and revision events. A duplicate key with different content returns 409.
An identical replay is reauthorized and returns current visible entities rather
than disclosing a saved body after a work item has moved out of the project.

The response is `{data:{batch_id,revision,items,created_ids,deleted_ids}}`.
`created_ids` maps client IDs to allocated UUIDs. `items` reflects final batch
state. Moving a Story to another Cycle changes its commitment only. A newly
created Task inherits its parent Story's Cycle if omitted; existing Task Cycles
and dates require explicitly selected, versioned commands.

Snapshots also return current `permissions.can_edit` and `permissions.can_admin`.
Archived projects remain readable and report both permissions as false; planning
commands require restoring the project first. Project archive/restore changes
publish durable authorization invalidations in migration `0014`.

The Admin-controlled project setting `settings.requirements_enabled` gates the
requirements, scenarios and resource HTTP capabilities. An omitted or JSON `null`
value enables them; explicit `false` returns 403 `requirements_disabled` after
authorization. The same check covers typed extension writes through `/issues`
and atomic planning commands, including idempotent replay. Ordinary work-item
reads and base-field writes remain available. Mutation checks repeat after the
project locks are held, so disabling the setting also blocks queued typed writes.
Automation policy settings remain independent; automatic planning application
still uses the gated command service.

Migration `0015_requirements_feature_gate.sql` validates the setting and emits a
durable authorization invalidation when its effective value changes. The
metadata-only SSE route remains available while disabled. Clients clear cached
requirements entities and editors, retain only the last event cursor, and can
observe re-enablement without polling content endpoints.

SSE `change` events contain `revision`, `kind` and an optional currently visible
work-item `entity_id`. Other domain changes are generic invalidations, with no
stored content in the event payload. `authorization_changed` requires clearing
cached entities immediately; when `reconnect:true`, fetch a new authorized
snapshot before subscribing again. Membership and source-visibility changes are
durable invalidations, including changes between snapshot and subscription.
`cursor_expired` requires a new snapshot. Consumers deduplicate by revision and
must discard responses from an earlier authorization or navigation generation.

The `workitems.Commands` API is shared with HTTP and background callers.
`requirements.Service.ApplyWithinTx` allows a worker to validate its policy,
source revisions and constraints inside the exact transaction that applies the
batch. The worker must roll back if any command or subsequent constraint check
fails. It must not substitute an unrelated business `DATABASE_URL` for the
explicitly named isolated integration test database.

Migration `0009_requirements.sql` defines the durable SQL event/history models.
The work-item Ent fields are generated from independently authored
`ent/schema/models.go` with `go generate ./ent`; no startup migration is added.
History coverage begins at the migration and does not invent earlier execution
transitions. Immutable facts include the workflow group at observation time.
