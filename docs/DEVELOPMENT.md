# Local development

The development stack owns only the `my-jira` Compose project and its named
volumes. PostgreSQL uses port 25432 and a dedicated `myjira_test` database for
integration tests. Existing services and databases in other projects are not used.

Start infrastructure with `make infra`. `make infra-stop` stops services without
deleting volumes. `.env.example` documents local connection settings. Application
processes use process environment variables; never point tests at `DATABASE_URL`.

| Service | Local address |
| --- | --- |
| Container application | http://127.0.0.1:4180 |
| Web | http://127.0.0.1:4173 |
| API | http://127.0.0.1:8088 |
| Collaboration | ws://127.0.0.1:3101 |
| S3 endpoint | http://127.0.0.1:29000 |
| S3 administration | http://127.0.0.1:29001 |
| Local email capture | http://127.0.0.1:28025 |

The delivered local instance runs through the container application address.
Development processes are started separately using the commands in README.md;
they were stopped after final acceptance. Validation results and their limits are
recorded in VALIDATION.md.

`make backup` creates a new private `.local/backups/snapshot.*` directory containing
a PostgreSQL dump, object files, a version 2 object manifest, and SHA256 files for
the dump and manifest. Object entries preserve size, SHA256, content type, content
encoding/disposition/language, cache-control and user metadata. The backup covers
current object versions. Quiesce application writers for a consistent database and
object snapshot; the two stores do not offer a shared atomic snapshot.

`make verify-backup SNAPSHOT=/absolute/snapshot/path` verifies the saved checksums,
restores the dump into a newly named `myjira_restore_test_*` database, explicitly
applies current versioned migrations to that isolated copy, and restores
objects into a new `myjira-restore-test-*` bucket. It refuses existing buckets and
uses conditional object creation. It verifies restored bytes and metadata, then
starts a temporary application HTTP server against the restored stores. Anonymous
attachment reads must fail, and currently authorized users download attachments
whose bytes and content type must match the snapshot. The command reports any
assets whose current permissions permit no active user; those are checked only at
the object layer. Only the exact database, bucket, objects and temporary sessions
created by this verification are cleaned up. The source snapshot stays available.

To retain restored objects, run `go -C apps/api run ./cmd/asset-backup -directory
/absolute/snapshot/path -restore -bucket NEW_BUCKET` with the configured storage
credentials. This creates a new bucket and never changes the application's active
storage configuration. Choose the target deployment's database and storage settings
separately when performing an actual recovery. Legacy manifests can still be
verified with `-verify`; full metadata restoration requires a new version 2 backup.

Keep `APP_ENCRYPTION_KEY` and any explicitly configured `LIVE_SERVICE_KEY` separately
in the deployment's secure backup process. The first decrypts persisted service
credentials; the second authenticates collaboration requests (and otherwise uses
the deployment-key fallback). Backup artifacts never include these key values.
