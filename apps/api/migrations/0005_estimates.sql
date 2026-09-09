CREATE TABLE estimates (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),deleted_at timestamptz,
  workspace_id uuid NOT NULL,project_id uuid NOT NULL,name text NOT NULL,
  description text NOT NULL DEFAULT '',kind text NOT NULL DEFAULT 'points' CHECK(kind IN('points','categories')),
  UNIQUE(id,project_id,workspace_id),
  FOREIGN KEY(project_id,workspace_id) REFERENCES projects(id,workspace_id) DEFERRABLE INITIALLY DEFERRED
);
CREATE UNIQUE INDEX estimates_name_active ON estimates(project_id,name) WHERE deleted_at IS NULL;
CREATE TABLE estimate_points (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),deleted_at timestamptz,
  workspace_id uuid NOT NULL,project_id uuid NOT NULL,estimate_id uuid NOT NULL,
  position double precision NOT NULL DEFAULT 1024,label varchar(20) NOT NULL,
  numeric_value double precision CHECK(numeric_value IS NULL OR numeric_value>=0),
  UNIQUE(id,project_id,workspace_id),
  FOREIGN KEY(estimate_id,project_id,workspace_id) REFERENCES estimates(id,project_id,workspace_id) DEFERRABLE INITIALLY DEFERRED
);
CREATE UNIQUE INDEX estimate_points_label_active ON estimate_points(estimate_id,label) WHERE deleted_at IS NULL;
ALTER TABLE projects ADD COLUMN estimate_id uuid REFERENCES estimates(id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE work_items ADD COLUMN estimate_point_id uuid;
ALTER TABLE work_items ADD FOREIGN KEY(estimate_point_id,project_id,workspace_id) REFERENCES estimate_points(id,project_id,workspace_id) DEFERRABLE INITIALLY DEFERRED;
