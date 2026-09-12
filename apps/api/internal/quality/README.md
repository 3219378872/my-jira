# GitHub quality integration

This module uses an independently authorized GitHub App. It does not reuse the
platform's GitHub login OAuth credentials and never executes repository code.

Register the App with these URLs, replacing `APP_URL` with the public application
origin:

- Setup URL: `APP_URL/api/v1/github/callback`.
- User authorization callback URL: `APP_URL/api/v1/github/callback`.
- Webhook URL: `APP_URL/api/v1/github/webhook`.

Leave **Request user authorization (OAuth) during installation** disabled. The
setup handler explicitly starts App user authorization, retaining the selected
installation and original browser session in its one-use state and using PKCE.
The OAuth callback verifies accessible repositories and repository administrator
rights before issuing a fifteen-minute project-specific selection grant. No user
or installation access token is persisted. **Redirect on update** may be enabled
when a changed installation returns through a connection started from the
platform. Setup and OAuth callbacks require that connection's unexpired
ten-minute state; an installation update opened directly from GitHub must restart
the connection in the platform. Code callbacks require the extended setup state
and PKCE; the original installation state cannot exchange a code. An exchange
failure consumes the state, so start a new platform connection before retrying.

The App needs read permissions for Contents, Pull requests, Actions, and Checks
(Metadata read is implicit). Subscribe to Push, Pull request, and Workflow run;
installation lifecycle events remove access immediately. Configure
`GITHUB_APP_ID`, `GITHUB_APP_SLUG`, `GITHUB_APP_PRIVATE_KEY`,
`GITHUB_APP_CLIENT_ID`, `GITHUB_APP_CLIENT_SECRET`, and
`GITHUB_WEBHOOK_SECRET`. The RSA key accepts PEM newlines or literal `\n` escapes.
These values are server-only and are never returned by the connection API.

`Register`, `RegisterPublic`, and `RegisterTasks` install the browser routes,
signed webhook and state-validated setup/OAuth routes, and durable Asynq consumers.
Schedule `quality.reconcile` periodically; the application worker uses fifteen minutes.
Reconciliation pages through completed Actions runs, PR updates, and commits,
with a thirty-day initial window and a twenty-four-hour overlap after a successful
sync. Each list is bounded to ten pages of one hundred results. Local project
revisions also request fresh document analysis when repository evidence has not
changed. Explicit commit/PR/run inputs support targeted compensation outside the
periodic window. A revoked/suspended installation remains a tombstone; reconnect
using a newly authorized installation instead of replaying a creation event.

Reports pin repository, commit SHA, PR base SHA when available, Actions run and
attempt, artifact IDs/digests, and document versions. Repeated deliveries are
deduplicated by both delivery ID and signed payload hash. An older report cannot
replace a newer source or create automatic repair tasks. A rerun cannot supply
artifacts for an older attempt. ZIP paths, symlinks, expansion, entry count,
network downloads, report input, and source excerpts have explicit bounds.

Code findings come from existing SARIF output and carry file/line and artifact
evidence plus bounded source excerpts. Document analysis checks missing story
narratives/acceptance criteria and explicit contradictory requirements within or
between PRD and story versions. General implementation conformance remains
unknown. Test analysis consumes JUnit, Cobertura, LCOV, and coverage-summary JSON.
Passed, failed, skipped, missing, and unknown remain distinct; a workflow failure
without a failing test case does not prove that tests failed. Private or archived
sources, removed/reclassified/moved Stories, and revoked bindings invalidate
historical report access as well as automatic actions.

Actionable findings use `automation.ApplyExternal`, its current project policy,
the shared work-item command path, and an atomic action-link callback. Replays
and unchanged findings at the same commit reuse existing actions. Fresh policy,
session, membership, installation, current-report, and source checks happen after
lock waits. Sources lock in Story, Page, Scenario order to match normal editing.

The provider/analysis tests use bounded mocks. Integration tests require
`TEST_DATABASE_URL` naming an isolated database containing `test` in its name and
create a separate schema per case. Real GitHub authorization, Actions artifacts,
model-provider interpretation, and production capacity require separate evidence.

Protocol references used when implementing the setup/OAuth separation:

- <https://docs.github.com/en/apps/creating-github-apps/registering-a-github-app/about-the-setup-url>
- <https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-a-user-access-token-for-a-github-app>
