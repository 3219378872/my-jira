CREATE TABLE module_links (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),deleted_at timestamptz,
  workspace_id uuid NOT NULL,project_id uuid NOT NULL,module_id uuid NOT NULL,
  created_by uuid NOT NULL,title varchar(255) NOT NULL DEFAULT '',url varchar(4096) NOT NULL,
  FOREIGN KEY(module_id,project_id,workspace_id) REFERENCES modules(id,project_id,workspace_id) DEFERRABLE INITIALLY DEFERRED,
  FOREIGN KEY(created_by) REFERENCES users(id) DEFERRABLE INITIALLY DEFERRED
);
CREATE INDEX module_links_module_active ON module_links(module_id,created_at) WHERE deleted_at IS NULL;

CREATE TABLE saved_analyses (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),deleted_at timestamptz,
  workspace_id uuid NOT NULL,name varchar(255) NOT NULL,description text NOT NULL DEFAULT '',
  owner_id uuid NOT NULL,query jsonb NOT NULL DEFAULT '{}'::jsonb CHECK(jsonb_typeof(query)='object'),
  FOREIGN KEY(workspace_id) REFERENCES workspaces(id) DEFERRABLE INITIALLY DEFERRED,
  FOREIGN KEY(owner_id) REFERENCES users(id) DEFERRABLE INITIALLY DEFERRED
);
CREATE UNIQUE INDEX saved_analyses_name_active ON saved_analyses(workspace_id,name) WHERE deleted_at IS NULL;
