# Structured business scenarios

All routes require the current actor's active workspace and explicit project
membership. Read access also intersects the primary Story's current visibility
with every page ever used as an explicit source. Private pages remain owner-only,
including for administrators. Removed source links retain provenance; copies
retain it too. Deleted/archived/draft/untyped primary Stories and unavailable source
pages cannot be used to read current content, historical content or SVG exports.

Every HTTP route also requires the project's requirements bundle to be enabled.
Only `project.settings.requirements_enabled=false` disables it; a missing key or
JSON null preserves access. Authorized requests then return
`403 requirements_disabled`, including historical reads and SVG downloads. Outsiders
still receive 404. The flag is checked after membership authorization and again
after mutation lock waits, with the project row locked through commit; disabling
the bundle preserves existing scenario content and versions for re-enablement.

Base: `/api/v1/workspaces/:workspaceID/projects/:projectID/scenarios`

| Method | Path | Behavior |
| --- | --- | --- |
| GET | base | Complete authorized collection; optional `story_id` filter |
| POST | base | Create from a typed Story; member or administrator |
| GET | `/:scenarioID` | Current scenario and current bound Story label/progress |
| PATCH | `/:scenarioID` | Partial content update; required positive integer `version` |
| DELETE | `/:scenarioID?version=N` | Version-checked soft deletion, 204 |
| POST | `/:scenarioID/copy` | `{version,name?}`; fresh scenario/participant/step/relationship IDs |
| GET | `/:scenarioID/versions` | Immutable `{id,version,created_at}` revisions |
| GET | `/:scenarioID/versions/:versionID` | Positive integer version; current access required |
| GET | `/use-case.svg` | All currently visible project scenarios and explicit relationships |
| GET | `/:scenarioID/use-case.svg` | Scenario plus currently visible explicit relationship endpoints |
| GET | `/:scenarioID/sequence.svg` | Authored participant lifelines and message steps |

The project use-case SVG accepts `story_ids=UUID,UUID` to intersect the authorized
collection with common four-view Story filters. An explicitly empty `story_ids=`
renders an empty diagram; omitting it selects all authorized Stories. Invalid or
zero UUIDs are rejected. Relationship endpoints must both remain in that filtered
diagram. This filter does not alter per-scenario diagrams.

The two per-scenario SVG endpoints accept `version=N` to render a historical
revision. All SVG endpoints accept `download=1`; downloads stay authenticated,
are generated synchronously and use `private, no-store`. There is no durable
public export URL. UUID path identifiers and integer versions are strictly parsed.
JSON responses use the existing `{data: ...}` envelope; SVG endpoints return
`image/svg+xml` with script, external resource and embedding restrictions.

Editable content:

```ts
type Participant = { id: string; name: string; kind: "actor" | "system" };
type Step = {
  id: string; kind: "call" | "return" | "alt" | "else" | "end";
  from_id?: string; to_id?: string; message: string; return_of?: string;
};
type Relationship = {
  id: string; kind: "association" | "include" | "extend" | "generalization";
  from_id: string; to_id: string;
};
type ScenarioContent = {
  story_id: string; name: string; goal: string; trigger: string;
  preconditions: string; outcome: string; system_boundary: string;
  bind_story_name: boolean; source_page_ids: string[];
  participants: Participant[]; steps: Step[]; relationships: Relationship[];
};
```

Only `story_id` and `name` are required at creation. Empty participant/step/edge
arrays are represented honestly by an empty diagram message, without fabricated
messages or relationships. Scenario creation assigns an ID; add relationships
that reference that new scenario in a following version-checked PATCH.

Participant UUIDs model business actors/systems independently of platform users
and Admin/Member/Guest. Steps are ordered by array position; identities survive
reordering. Calls and returns refer to existing participants, and a return must
reverse an earlier call on the same possible execution path. Each alternative is
one `alt`, a nonempty branch, one `else`, a nonempty branch, and `end`; nesting is
rejected. A call cannot return twice along any modeled path. Deleting/replacing
participants or changing step order validates the entire resulting model before
anything is committed.

Associations connect a local participant to a scenario. Other relationships
connect the current scenario with another currently visible scenario in the same
project. No work-item dependency becomes a UML edge. Later loss of target
visibility hides that relationship in both current/historical JSON and SVG.

The output adds `id,workspace_id,project_id,version,review_needed,created_at,
updated_at,story,provenance_source_page_ids,source_story_version,
source_page_versions`. `story` contains `id,name,state_id,state_name,state_group,
version` from the current Story. Source version fields are server-owned and are
captured at creation/review; they cannot be supplied in a write. Story prose,
story narrative, acceptance and source page content changes create a new
`review_needed` revision without rewriting steps. PATCH `review_needed:false`
explicitly acknowledges review and captures current source versions. A Story
project move transfers its scenarios, preserves source provenance and history,
and invalidates both projects. Source visibility still uses current membership
in each source page's project.

All content changes, immutable snapshots and `scenario.changed` project events
commit together. Rejected/stale writes do not create history or events. Source
scenario creation, edits and removal also record identifier-only durable facts on
the primary Story, so an automatic Story undo preserves later human scenario work
even if that scenario was subsequently removed. Source
page privacy, owner, project, archive and deletion changes emit
`scenario.visibility_changed` without requiring a content edit. Generic SSE
payloads contain no scenario prose or private source content. Membership and page
locks serialize authorization changes with reads, mutations and SVG generation.

SVG is independently authored geometry. Text is XML escaped; element names,
presentation attributes and internal marker references are source-controlled.
No HTML, scripts, CSS input, image, external URL or `foreignObject` is accepted.
`data-story-id`, `data-scenario-id`, `data-participant-id`, `data-step-id` and
`data-relationship-id` retain source navigation. Internal fragment links contain
only parsed UUIDs. `data-source-kind` distinguishes `story`, `scenario` and `step`
links when several IDs provide shared context. Client zoom/pan/fit controls transform the SVG viewport; clients
must obtain exports through authenticated requests and clear cached content on
authorization invalidation.

Verification: `TEST_DATABASE_URL` must explicitly name an isolated test database.
The module tests cover semantic call/return/alternative validation, escaped XML,
private-source intersection across current/history/copy/removal/export, cross
project access, guest visibility, Story moves, stable identities, source revision
review, transactional event rollback, optimistic concurrency, membership
demotion, credential revocation and project-disable races. Disabled-bundle checks
cover every HTTP route, history and SVG exports. These are automated
API/database/rendering checks, separate from
browser interaction or external-provider verification.
