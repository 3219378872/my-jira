-- Independently authored GitHub App integration. Access tokens are short lived
-- in memory only; browser installation proofs contain repository IDs, not secrets.
CREATE TABLE github_installations (
    installation_id bigint PRIMARY KEY CHECK (installation_id > 0),
    account_login text NOT NULL DEFAULT '',
    revoked_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE github_connect_states (
    state_hash text PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces(id),
    project_id uuid NOT NULL REFERENCES projects(id),
    actor_id uuid NOT NULL REFERENCES users(id),
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE github_installation_grants (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces(id),
    project_id uuid NOT NULL REFERENCES projects(id),
    actor_id uuid NOT NULL REFERENCES users(id),
    installation_id bigint NOT NULL REFERENCES github_installations(installation_id),
    repositories jsonb NOT NULL DEFAULT '[]',
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE github_bindings (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces(id),
    project_id uuid NOT NULL REFERENCES projects(id),
    installation_id bigint NOT NULL REFERENCES github_installations(installation_id),
    repository_id bigint NOT NULL CHECK (repository_id > 0),
    repository text NOT NULL,
    active boolean NOT NULL DEFAULT true,
    created_by uuid NOT NULL REFERENCES users(id),
    sync_status text NOT NULL DEFAULT 'idle' CHECK (sync_status IN ('idle','queued','syncing','synced','failed','rate_limited','revoked','disabled')),
    last_synced_at timestamptz,
    last_error text NOT NULL DEFAULT '',
    next_retry_at timestamptz,
    latest_source_at timestamptz,
    latest_report_id uuid,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(project_id, repository_id)
);
CREATE INDEX github_bindings_installation ON github_bindings(installation_id) WHERE active;
CREATE TABLE github_deliveries (
    delivery_id text PRIMARY KEY,
    installation_id bigint NOT NULL,
    event_type text NOT NULL,
    payload_hash text NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX github_deliveries_replay ON github_deliveries(installation_id,event_type,payload_hash);
CREATE TABLE github_sync_requests (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces(id),
    project_id uuid NOT NULL REFERENCES projects(id),
    binding_id uuid NOT NULL REFERENCES github_bindings(id),
    source jsonb NOT NULL DEFAULT '{}',
    status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','running','completed','failed','revoked')),
    report_id uuid,
    last_error text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE quality_reports (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces(id),
    project_id uuid NOT NULL REFERENCES projects(id),
    binding_id uuid NOT NULL REFERENCES github_bindings(id),
    repository text NOT NULL,
    commit_sha text NOT NULL,
    pull_request bigint,
    run_id bigint,
    run_attempt integer,
    source_revision text NOT NULL,
    source_at timestamptz NOT NULL,
    dimensions jsonb NOT NULL,
    findings jsonb NOT NULL DEFAULT '[]',
    evidence jsonb NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(binding_id, source_revision)
);
CREATE INDEX quality_reports_project ON quality_reports(project_id,created_at DESC,id);
CREATE TABLE quality_repair_actions (
    report_id uuid NOT NULL REFERENCES quality_reports(id),
    finding_id text NOT NULL,
    run_id uuid,
    item_ids jsonb NOT NULL DEFAULT '[]',
    status text NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(report_id,finding_id)
);
