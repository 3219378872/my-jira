# Original API contract

The requirements iteration adds project-scoped snapshots/SSE, versioned atomic
planning commands, resources, structured scenarios/SVG, automation and quality.
See [requirements operations](REQUIREMENTS_OPERATIONS.md) and the generated
[OpenAPI contract](openapi.json) for exact inputs, statuses and authorization.
GitHub HMAC ingress, browser sessions and workspace tokens remain distinct.

This API is independently designed for my-jira. It does not provide Plane API
compatibility. All browser JSON routes use `/api/v1`; UUIDs identify resources.

## Shared protocol

- Objects and unpaginated arrays: `{"data": ...}`. Created objects return 201.
- Paginated arrays: `{"data":[],"pagination":{"next_cursor":null,"has_more":false,"total":0}}`.
- Errors: `{"error":{"code":"validation_failed","message":"...","fields":{}}}`.
  Validation is 400, missing authentication 401, insufficient role 403, absent or
  cross-workspace objects 404, conflicting writes 409. Deletes return 204.
- Snake-case fields, string UUIDs, RFC3339 timestamps, `YYYY-MM-DD` business dates.
- `mj_session` is an opaque HttpOnly cookie. Its hash is persisted. `mj_csrf` is
  readable by the browser; unsafe requests send it as `X-CSRF-Token`.
- Obtain an initial CSRF token with `GET /auth/csrf`; registration, login and setup
  also require it. Successful authentication returns a fresh `csrf_token`.
- Credentialed cross-origin requests are accepted only from `APP_ORIGIN`.
- JSON input is limited to 2 MiB by default. Passwords are never serialized.

The complete machine-readable contract is served as a raw OpenAPI 3.1 document at
`GET /api/v1/openapi.json` and checked into `docs/openapi.json`. From `apps/api`,
run `go run ./cmd/openapi -output ../../docs/openapi.json` after changing routes or
schemas. Generation uses the same application router as the API and fails if a
registered operation has no reviewed contract. Contract tests validate route
coverage, schema references, versioned requests, security alternatives and the
generated artifact. This is contract evidence; it does not replace domain or
provider integration tests.

Work-item detail and workspace/project work-item lists accept optional `fields`
and `expand` parameters. `fields=name,priority,version` selects approved scalar,
ID or count fields and always retains `id`. `expand=state,project,assignees,labels,
estimate_point` adds the corresponding existing `*_detail` / `*_details` objects;
`expand=none` omits these associations. When only `expand` is supplied, approved
scalar fields remain. Without either parameter the ordinary representation is
unchanged. OpenAPI lists the complete field and association allowlists.

The domain handler authorizes and filters first; projection cannot retrieve more
rows or expose binary content, object keys, private account fields or arbitrary
project settings. Pagination and group metadata remain intact, including nested
group leaves. Invalid, repeated, dotted or wildcard names return 400 only after a
successful authorized read; existing 401/403/404 errors remain unchanged. Each
parameter is limited to 2,048 bytes and 64 names. The authorized JSON representation
before projection is bounded to 16 MiB; a 413 instructs callers to reduce the list
limit. Projection affects response representation, not database query cost.

`POST .../projects/:projectID/issues/:issueID/move` requires current Member access
to both projects and the source `version`. The destination `project_id` is required;
an optional `state_id` selects a destination state, otherwise its default is used.
Optional `changes` accepts `position`, `priority`, `assignee_ids`, `label_ids`,
`cycle_id`, `module_ids` and `estimate_point_id`. These fields use the same formats
and destination-scope validation as a normal update. The move clears previous
project associations, applies the selected destination groups, allocates a new
number and records one version/activity atomically. Invalid associations and stale
versions roll back the whole move, including the destination's number allocation.
An item with children must detach or move those children first.

## Foundation routes

All paths below are relative to `/api/v1`.

| Method | Path | Data |
| --- | --- | --- |
| GET | `/instance` | Public setup status, instance name and signup availability |
| POST | `/instance/setup` | `instance_name,email,password,display_name`; creates first admin and session atomically |
| PATCH | `/instance` | Instance admin updates `name,registration_enabled` |
| GET | `/auth/csrf` | `{csrf_token}` and CSRF cookie |
| POST | `/auth/register` | `email,password,display_name`; returns `{user,csrf_token}` |
| POST | `/auth/login` | `email,password`; returns `{user,csrf_token}` |
| POST | `/auth/logout` | Revokes current session, 204 |
| GET/PATCH | `/auth/me` | Current user; editable `display_name,first_name,last_name,avatar_url,timezone,preferences` |
| GET/PATCH | `/auth/last-visited` | `{workspace_id,project_id}`; either may be null, project requires its workspace; inaccessible saved locations return null on read |
| POST | `/auth/password` | `current_password,new_password`; revokes other sessions |
| GET | `/auth/sessions` | Current user's sessions, without token hashes |
| DELETE | `/auth/sessions/:sessionID` | Revoke own session |
| GET/POST | `/workspaces` | List memberships; create with `name,slug,timezone` |
| GET | `/workspaces/availability?slug=...` | Normalized `{slug,available}`; authenticated, validated syntax |
| GET/PATCH/DELETE | `/workspaces/:workspaceID` | Workspace detail/settings/deletion |
| POST | `/workspaces/:workspaceID/leave` | Remove own workspace and project memberships, preserving admins; 204 |
| POST | `/workspaces/:workspaceID/join` | `{token}`; recipient-bound invitation must belong to the requested workspace |
| GET/POST | `/workspaces/:workspaceID/members` | List/add existing user with `email,role` |
| PATCH/DELETE | `/workspaces/:workspaceID/members/:memberID` | Change role/remove; preserve final admin |
| GET/POST | `/workspaces/:workspaceID/invitations` | List/create invitation with `email,role` |
| DELETE | `/workspaces/:workspaceID/invitations/:invitationID` | Revoke invitation |
| POST | `/invitations/accept` | Authenticated intended recipient submits `token` |
| GET/POST | `/workspaces/:workspaceID/projects` | List accessible projects; create `name,identifier,description,network` |
| GET | `/workspaces/:workspaceID/projects/availability?identifier=...` | Normalized `{identifier,available}`; requires workspace member role |
| GET/PATCH/DELETE | `/workspaces/:workspaceID/projects/:projectID` | Project detail/settings/deletion |
| POST | `.../projects/:projectID/join` | Join a public project; idempotent 200 membership; guests keep role 5 |
| POST | `.../projects/:projectID/leave` | Remove own explicit project membership, preserving admins; 204 |
| GET/POST | `.../projects/:projectID/members` | List/add workspace member using `user_id,role` |
| PATCH/DELETE | `.../projects/:projectID/members/:memberID` | Role/remove, preserving final project admin |
| GET/POST | `.../projects/:projectID/states` | `name,color,group,position,is_default` |
| PATCH/DELETE | `.../projects/:projectID/states/:stateID` | Update; cannot delete a state in use |
| GET/POST | `.../projects/:projectID/labels` | `name,color,description,parent_id` |
| PATCH/DELETE | `.../projects/:projectID/labels/:labelID` | Update/remove |
| GET/POST | `/workspaces/:workspaceID/labels` | GET global labels plus accessible active project labels; POST creates global `project_id:null` labels |
| PATCH/DELETE | `/workspaces/:workspaceID/labels/:labelID` | Update/remove a workspace label; member role required |
| POST | `/workspaces/:workspaceID/labels/bulk` | `{labels:[...]}`; atomically creates 1–100 global labels |
| POST | `.../projects/:projectID/labels/bulk` | `{labels:[...]}`; atomically creates 1–100 project labels |

Roles are `5` guest, `15` member and `20` admin. Project network is `private` or
`public`; public means visible to active workspace members, not anonymous users.
Workspaces and projects return the current actor's effective `role`. An instance
admin does not automatically gain private workspace/project access.

Projects expose `guest_can_view_all` (default false). Project creation accepts it;
changing an existing project's value requires Admin through project PATCH. The
flag expands Guest read visibility inside an accessible project while preserving
role 5, project privacy and all independent resource ownership restrictions. It
never permits Member-only mutations or changes workspace/project memberships.
`identity.Scope.GuestCanViewAll` carries the current project setting separately
from the effective role.

Projects include `is_member`, `features` and `cover_image_url`. Features are
`cycles,modules,pages,views,intake` booleans, enabled by default. Create and patch
accept a partial `features` object and HTTP(S) `cover_image_url`; null or an empty
string removes the cover. Patching project `settings` merges nested fields so a
display or feature change preserves independent configuration. Automation and
estimate settings use their dedicated endpoints and cannot be changed through
generic project `settings`.

Leaving a public project removes explicit membership; visibility still follows
workspace membership. Private projects require an administrator to add members.
Workspace removals and guest demotions also preserve active project admins.
Workspace labels are included in project label lists, but mutation uses the label's
own workspace or project route. Parents must share that scope. Hierarchy mutations
serialize validation and writes to prevent concurrent parent cycles.

Workspace label lists aggregate global labels and labels from currently accessible,
non-archived projects. Private projects require active explicit membership. Bulk
creation uses the route's own scope; parent IDs must already exist in that same
hierarchy. A duplicate name, invalid parent or failed row rolls back the batch.

Changing a workflow state's group propagates to all its live work items in the
same transaction: completion timestamps change, versions increment, `updated_by`
records the actor, and activity/version/outbox entries are persisted. Entering
completed sets the timestamp; leaving completed clears it. Re-saving the same
group does not increment item versions. Failed history or outbox writes roll back
the state and all affected items together.

User preference patches merge fields and preserve the reserved `_last_visited`
location. The dedicated location endpoint validates current access on write and
read. When access to a private project is lost, its saved `project_id` returns null;
when workspace access is lost, both IDs return null.

## Module registration and Go shared types

Go module: `my-jira/apps/api`. Other modules export
`func Register(r *gin.RouterGroup, deps platform.Dependencies)` and receive the
already authenticated `/api/v1` router group. Foundation registers public auth
routes separately. Do not install an independent session or CSRF middleware.

`platform.Dependencies` contains `DB *database.Database`, `Policy identity.Policy`,
and `Jobs jobs.Publisher`. `Database` exposes `SQL *sql.DB`, `Ent *ent.Client` and
`WithinTx(context.Context,func(database.DBTX) error) error`. `DBTX` supports the
standard `ExecContext`, `QueryContext`, `QueryRowContext` methods. All database
effects within a transaction must use the supplied DBTX.

`identity.Actor` has `UserID,SessionID uuid.UUID` and `IsAdmin bool`.
`identity.Scope` has `Actor,WorkspaceID,ProjectID,Role`.
`Policy.Workspace(ctx,actor,workspaceID,minimumRole)` and
`Policy.Project(ctx,actor,workspaceID,projectID,minimumRole)` return a validated
scope or an error. Membership is queried on every call.

`httpapi.Actor(c) (identity.Actor,error)`, `UUIDParam(c,name) (uuid.UUID,error)`,
`JSON(c,status,value)`, `Fail(c,error)` and `Bind[T](c) (T,error)` are shared helpers.
`httpapi.Page[T]` and `Pagination` implement the pagination envelope above.

`jobs.Publisher.Publish(ctx,q,topic,payload,deduplicationKey)` inserts a durable
outbox event using the caller's DBTX. Dispatch to Asynq happens after commit.
Consumers must be idempotent because queue delivery is at least once.

Further domain routes are documented here by their implementing module owner.

## Email, OAuth and instance administration

| Method | Path under `/api/v1` | Data |
| --- | --- | --- |
| POST | `/auth/forgot-password` | `{email}`; always returns `{message}` without disclosing account existence |
| POST | `/auth/reset-password` | `{token,new_password}`; one-use email token; 204 and revokes all sessions |
| POST | `/auth/magic/request` | `{email}` → `{challenge_id}`; sends six-digit email code |
| POST | `/auth/magic/verify` | `{challenge_id,code,display_name?}` → `{user,csrf_token}` |
| GET | `/auth/oauth/:provider` | Browser navigation; redirects to configured Google/GitHub/GitLab/Gitea provider |
| GET | `/auth/oauth/:provider/callback` | OAuth code callback; state + PKCE checked, session created, redirects to app |
| GET | `/auth/accounts` | Current user's connected providers; no credentials |
| DELETE | `/auth/accounts/:accountID` | Unlink provider while preserving an authentication method |
| POST | `/auth/email-verification/request` | Sends current user verification link; `{message}` |
| POST | `/auth/email-verification/confirm` | `{token}`; 204 |
| POST | `/auth/email-change/request` | `{email}`; sends confirmation link to new address; `{message}` |
| POST | `/auth/email-change/confirm` | `{token}`; current user confirms change; 204 |
| GET | `/admin/stats` | Instance admin aggregate user/workspace/project/work-item/storage counts |
| GET | `/admin/users` | `?search=` optional; instance users with workspace count |
| PATCH | `/admin/users/:userID` | `{is_active?,is_instance_admin?}`; preserves final active admin; suspension revokes sessions |
| GET | `/admin/workspaces` | Instance workspace list with project/member counts |
| GET/PATCH | `/admin/configuration` | `name,registration_enabled,settings` where settings supports `allow_workspace_creation,magic_login_enabled` |

`auth/me` includes `password_set`. OAuth/magic-created users may set a first
password at `/auth/password` by submitting `new_password`; users with an existing
password must also submit `current_password`. Passwords are 10–72 bytes.

`instance.auth_methods` lists configured methods, e.g. `password,magic,google`.
OAuth credentials are instance environment settings (`OAUTH_GOOGLE_CLIENT_ID`,
`OAUTH_GOOGLE_CLIENT_SECRET`, analogous GITHUB/GITLAB/GITEA variables). The callback
base is `API_PUBLIC_URL`, while `APP_ORIGIN`/`ALLOWED_ORIGINS` identifies the app.
Self-hosted GitLab/Gitea can set `OAUTH_<PROVIDER>_BASE_URL`. SMTP uses `SMTP_HOST`,
`SMTP_PORT`, `SMTP_FROM`, optional `SMTP_USER`, `SMTP_PASSWORD`, `SMTP_SECURE`.
Missing provider/SMTP configuration is reported explicitly, never as success.

Instance admins can `GET /admin/services` to inspect email, storage, AI, Unsplash
and OAuth configuration. Credential values are omitted; booleans such as
`password_configured`, `secret_key_configured` and `api_key_configured` are returned.
OAuth entries are nested under `oauth` by provider name. The response also includes
`secret_storage_enabled`, indicating a valid deployment `APP_ENCRYPTION_KEY`.

`PATCH /admin/services/:service` persists configuration for `email`, `storage`,
`google`, `github`, `gitlab`, `gitea`, `ai` or `unsplash`. Omitted credentials are
preserved; explicit null clears a credential. Values are encrypted with AES-GCM
using the deployment key and immediately override environment defaults. Losing
the deployment key makes those saved values unreadable. No key is stored in Git.

| Service | Fields |
| --- | --- |
| email | host, port (string or number), from, user, password, secure |
| storage | endpoint (host:port), public_endpoint, bucket, access_key, secret_key, secure, region |
| OAuth provider | client_id, client_secret, base_url, optional authorize_url/token_url/userinfo_url/emails_url |
| ai | provider, base_url, model (`gpt-5.6-terra`), wire_api (`responses`), api_key |
| unsplash | access_key |

`POST /admin/email/test` with `{email}` returns `{accepted:true,message}` only
after an SMTP server accepts the test message. This does not claim delivery to the
recipient's inbox. `POST /admin/storage/test` returns `{connected,bucket_exists}`
from an actual bucket existence request and never creates or deletes a bucket.

`email.send` supports ordinary authentication mail `{to,subject,text}` and
notification mail `{notification_id}`. Notification jobs load the current recipient,
workspace/project access, entity privacy and title at execution time. Workspace
notification preferences use `scope=notifications` with `email,mentions,assigned,
subscribed,created` booleans, each defaulting to true. Email is sent only when email
is enabled and at least one notification `data.reasons` entry is enabled. Guest
recipients can receive work-item details only for their own items unless the
project's current `guest_can_view_all` flag permits the read. Project page emails
require explicit project membership and current owner-or-flag Guest visibility;
private pages
and views require ownership. Revoked access, deleted entities and disabled
preferences discard a queued notification mail without an SMTP request. A
`data.silent` notification can still produce email when `in_app` is disabled.
SMTP transport is at least once; an SMTP acceptance is not inbox-delivery proof.

Report generation queues `email.report-ready` with an `export_id`. The mail worker
loads the current owner and completed export, rechecks active Member access to its
workspace and every captured project, and sends an authenticated download link.
Revoked scope or ownership prevents delivery. Object keys, public object URLs and
report content never appear in the email.

Authentication mail links use the browser routes `/invitations/:token`,
`/verify-email?token=...`, `/verify-email?change=1&token=...` and
`/reset-password?token=...`. Tokens are one-use values; invitation acceptance and
email changes additionally validate the authenticated recipient.

Project create, update and delete publish `project.changed` with a fresh domain
event ID and action `created`, `updated` or `deleted` in the mutation's transaction.
The event payload includes only the approved public project fields; arbitrary
settings and the requesting actor's role or membership flags are excluded. Failure
to insert the outbox record rolls back the project mutation.

## Batch entity assets

`GET /workspaces/:workspaceID/assets/batch` and
`GET /workspaces/:workspaceID/projects/:projectID/assets/batch` accept
`work_item_ids` and `page_ids` as comma-separated nonzero UUIDs, repeated query
parameters, or both. Supply at least one identifier and at most 100 in total
across both parameters, counting duplicates. Duplicate entities appear once;
work items precede pages, preserving the first requested order within each type.

The response is `{data:[{entity_type:"work_item"|"page",entity_id,assets:[...]}]}`.
Each asset has the ordinary asset representation and a `download_url` using its
parent's actual project. Authorized parents without files return `assets:[]`.
Workspace batches can span accessible projects and workspace pages; a project
batch only accepts parents from that exact project.

Every parent is checked independently using current workspace/project access,
Guest visibility and page privacy. Project pages require active explicit project
membership and private pages require ownership, including for administrators.
One unknown, deleted or inaccessible parent makes the entire batch return the
same 404 without partial results, even when that parent has no files. Storage
object keys are never returned.

Only completed uploads are listed. By default the batch returns live files;
`deleted=true` selects only deleted files under the existing recovery-list rules.
It does not bypass parent access or make deleted files downloadable. `deleted`
must be a single `true` or `false` value when supplied. Personal assets without
entity parents continue to use the personal asset list.
