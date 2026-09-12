-- Independently authored automation state. Inputs and recovery records are not
-- subject to the twenty-version retention of interactive document history.
CREATE TABLE automation_policies (
 project_id uuid PRIMARY KEY REFERENCES projects(id), workspace_id uuid NOT NULL REFERENCES workspaces(id),
 version bigint NOT NULL DEFAULT 1, enabled boolean NOT NULL DEFAULT false,
 authorized_by uuid NOT NULL REFERENCES users(id), config jsonb NOT NULL,
 budget_month date NOT NULL DEFAULT date_trunc('month',now())::date,
 calls_used integer NOT NULL DEFAULT 0 CHECK(calls_used>=0),
 created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE automation_runs (
 id uuid PRIMARY KEY, workspace_id uuid NOT NULL REFERENCES workspaces(id), project_id uuid NOT NULL REFERENCES projects(id),
 requested_by uuid NOT NULL REFERENCES users(id), authorized_by uuid NOT NULL REFERENCES users(id),
 kind text NOT NULL CHECK(kind IN ('decompose','schedule','forecast','risk','efficiency','quality')),
 status text NOT NULL CHECK(status IN ('queued','running','applied','partial','completed','blocked','failed','cancelled','undone')),
 idempotency_key text NOT NULL, input_fingerprint text NOT NULL, policy_version bigint NOT NULL,
 input jsonb NOT NULL, output jsonb NOT NULL DEFAULT '{}', algorithm_version text NOT NULL,
 cause text NOT NULL DEFAULT 'manual', round integer NOT NULL DEFAULT 0 CHECK(round>=0),
 attempts integer NOT NULL DEFAULT 0, failure text NOT NULL DEFAULT '', batch_id uuid,
 before_state jsonb NOT NULL DEFAULT '[]', after_state jsonb NOT NULL DEFAULT '[]',
 undo_conflicts jsonb NOT NULL DEFAULT '[]', cancelled_at timestamptz,
 lease_until timestamptz, created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(project_id,idempotency_key), UNIQUE(project_id,kind,input_fingerprint,policy_version)
);
CREATE INDEX automation_runs_project_created ON automation_runs(project_id,created_at DESC);
CREATE TABLE automation_source_mappings (
 project_id uuid NOT NULL REFERENCES projects(id), source_key text NOT NULL, entity_key text NOT NULL,
 work_item_id uuid NOT NULL REFERENCES work_items(id), run_id uuid NOT NULL REFERENCES automation_runs(id),
 source_revision text NOT NULL, source_location jsonb NOT NULL, generated_fields jsonb NOT NULL,
 last_item_version bigint NOT NULL, created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(project_id,source_key,entity_key)
);
CREATE TABLE automation_forecasts (
 id uuid PRIMARY KEY, workspace_id uuid NOT NULL REFERENCES workspaces(id), project_id uuid NOT NULL REFERENCES projects(id),
 run_id uuid NOT NULL UNIQUE REFERENCES automation_runs(id), as_of timestamptz NOT NULL,
 status text NOT NULL CHECK(status IN ('ready','insufficient_data')), p50 date, p80 date,
 sample_count integer NOT NULL, coverage_start timestamptz, remaining integer NOT NULL,
 assumptions jsonb NOT NULL, backtest jsonb NOT NULL, algorithm_version text NOT NULL,
 stale boolean NOT NULL DEFAULT false, created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX automation_forecasts_project_time ON automation_forecasts(project_id,as_of DESC);
CREATE TABLE automation_risks (
 id uuid PRIMARY KEY, workspace_id uuid NOT NULL REFERENCES workspaces(id), project_id uuid NOT NULL REFERENCES projects(id),
 key text NOT NULL, type text NOT NULL, severity text NOT NULL CHECK(severity IN ('low','medium','high','critical')),
 status text NOT NULL CHECK(status IN ('open','acknowledged','resolved')), reason text NOT NULL,
 evidence jsonb NOT NULL, item_ids jsonb NOT NULL DEFAULT '[]', action_item_id uuid REFERENCES work_items(id),
 version bigint NOT NULL DEFAULT 1, first_seen timestamptz NOT NULL DEFAULT now(), last_seen timestamptz NOT NULL DEFAULT now(),
 UNIQUE(project_id,key)
);
CREATE TABLE automation_risk_events (
 id uuid PRIMARY KEY, risk_id uuid NOT NULL REFERENCES automation_risks(id), run_id uuid REFERENCES automation_runs(id),
 actor_id uuid REFERENCES users(id), status text NOT NULL, evidence jsonb NOT NULL, created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE automation_improvements (
 id uuid PRIMARY KEY, workspace_id uuid NOT NULL REFERENCES workspaces(id), project_id uuid NOT NULL REFERENCES projects(id),
 run_id uuid NOT NULL REFERENCES automation_runs(id), key text NOT NULL, reason text NOT NULL,
 target_metric text NOT NULL CHECK(target_metric IN ('blocked_ratio','wip','throughput','reopen_ratio')),
 status text NOT NULL CHECK(status IN ('proposed','observing','improved','no_improvement','insufficient_evidence')),
 baseline jsonb NOT NULL, observed jsonb NOT NULL DEFAULT '{}',
 window_start timestamptz NOT NULL, window_end timestamptz NOT NULL,
 action_item_id uuid REFERENCES work_items(id), owner_id uuid REFERENCES users(id), executed_at timestamptz,
 version bigint NOT NULL DEFAULT 1, created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX automation_improvements_active ON automation_improvements(project_id,key) WHERE status IN ('proposed','observing');
