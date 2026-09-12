-- Independently authored project resource calendars; minutes are integer facts.
-- NULL means unknown. Zero is a known unavailable day or project allocation.
CREATE TABLE project_resources (
    workspace_id uuid NOT NULL,
    project_id uuid NOT NULL,
    member_id uuid NOT NULL REFERENCES users(id),
    skills text[] NOT NULL DEFAULT '{}',
    weekday_minutes jsonb NOT NULL DEFAULT '[null,null,null,null,null,null,null]'::jsonb,
    project_minutes_per_day integer,
    exceptions jsonb NOT NULL DEFAULT '{}'::jsonb,
    version bigint NOT NULL DEFAULT 1,
    updated_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, member_id),
    FOREIGN KEY (project_id, workspace_id) REFERENCES projects(id,workspace_id),
    CHECK (project_minutes_per_day IS NULL OR project_minutes_per_day BETWEEN 0 AND 1440),
    CHECK (jsonb_typeof(weekday_minutes) = 'array' AND jsonb_array_length(weekday_minutes) = 7),
    CHECK (jsonb_typeof(exceptions) = 'object'),
    CHECK (version > 0)
);
CREATE INDEX project_resources_workspace ON project_resources(workspace_id,project_id);

CREATE TABLE project_resource_history (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL,
    project_id uuid NOT NULL,
    member_id uuid REFERENCES users(id),
    actor_id uuid NOT NULL REFERENCES users(id),
    before_data jsonb,
    after_data jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (project_id, workspace_id) REFERENCES projects(id,workspace_id)
);
CREATE INDEX project_resource_history_project ON project_resource_history(project_id,created_at);
