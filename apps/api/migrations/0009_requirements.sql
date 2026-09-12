-- Independently authored requirements model and transactional planning event log.
-- Existing work items retain NULL requirement_type; names never imply a type.
ALTER TABLE work_items
  ADD COLUMN requirement_type text CHECK(requirement_type IN ('epic','story','task')),
  ADD COLUMN story_role text NOT NULL DEFAULT '',
  ADD COLUMN story_goal text NOT NULL DEFAULT '',
  ADD COLUMN story_benefit text NOT NULL DEFAULT '',
  ADD COLUMN acceptance_criteria jsonb NOT NULL DEFAULT '[]' CHECK(jsonb_typeof(acceptance_criteria)='array'),
  ADD COLUMN activity_id uuid,
  ADD COLUMN map_position double precision NOT NULL DEFAULT 1024,
  ADD COLUMN estimated_minutes integer CHECK(estimated_minutes BETWEEN 0 AND 52560000),
  ADD COLUMN remaining_minutes integer CHECK(remaining_minutes BETWEEN 0 AND 52560000),
  ADD COLUMN required_skills jsonb NOT NULL DEFAULT '[]' CHECK(jsonb_typeof(required_skills)='array'),
  ADD COLUMN allocation_weights jsonb NOT NULL DEFAULT '[]' CHECK(jsonb_typeof(allocation_weights)='array'),
  ADD COLUMN planning_locked boolean NOT NULL DEFAULT false;

CREATE TABLE requirement_activities (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL, project_id uuid NOT NULL, epic_id uuid,
  name text NOT NULL CHECK(length(trim(name)) BETWEEN 1 AND 255),
  position double precision NOT NULL DEFAULT 1024,
  created_by uuid NOT NULL, updated_by uuid NOT NULL,
  version bigint NOT NULL DEFAULT 1 CHECK(version>0),
  created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
  archived_at timestamptz, deleted_at timestamptz,
  UNIQUE(id,project_id,workspace_id),
  FOREIGN KEY(project_id,workspace_id) REFERENCES projects(id,workspace_id) DEFERRABLE INITIALLY DEFERRED,
  FOREIGN KEY(epic_id,project_id,workspace_id) REFERENCES work_items(id,project_id,workspace_id) DEFERRABLE INITIALLY DEFERRED
);
ALTER TABLE work_items ADD CONSTRAINT work_items_activity_scope
  FOREIGN KEY(activity_id,project_id,workspace_id) REFERENCES requirement_activities(id,project_id,workspace_id) DEFERRABLE INITIALLY DEFERRED;
CREATE INDEX requirement_activities_order ON requirement_activities(project_id,epic_id,position,id) WHERE deleted_at IS NULL;
CREATE INDEX work_items_requirements ON work_items(project_id,requirement_type,activity_id,map_position,id) WHERE deleted_at IS NULL;

CREATE TABLE project_revisions (
  project_id uuid PRIMARY KEY REFERENCES projects(id), workspace_id uuid NOT NULL REFERENCES workspaces(id),
  revision bigint NOT NULL DEFAULT 0, updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE project_events (
  project_id uuid NOT NULL REFERENCES projects(id), workspace_id uuid NOT NULL,
  revision bigint NOT NULL, kind text NOT NULL, entity_id uuid,
  cause text NOT NULL DEFAULT 'human', batch_id uuid,
  created_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY(project_id,revision)
);
CREATE INDEX project_events_created ON project_events(created_at);
CREATE TABLE work_item_history (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), workspace_id uuid NOT NULL, project_id uuid NOT NULL,
  work_item_id uuid NOT NULL, revision bigint NOT NULL, actor_id uuid,
  action text NOT NULL, before_data jsonb, after_data jsonb,
  cause text NOT NULL DEFAULT 'human', batch_id uuid,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX work_item_history_time ON work_item_history(project_id,created_at,revision);
CREATE INDEX work_item_history_item ON work_item_history(work_item_id,revision);
CREATE TABLE planning_change_batches (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), workspace_id uuid NOT NULL, project_id uuid NOT NULL,
  actor_id uuid NOT NULL, idempotency_key text NOT NULL CHECK(length(idempotency_key) BETWEEN 1 AND 200),
  input_hash text NOT NULL, reason text NOT NULL DEFAULT 'human', result jsonb NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(), UNIQUE(project_id,actor_id,idempotency_key)
);

-- IDs and invalidation metadata only. Payloads are freshly authorized on fetch.
CREATE FUNCTION planning_emit_event(wid uuid,pid uuid,event_kind text,entity uuid)
RETURNS bigint LANGUAGE plpgsql AS $$
DECLARE next_revision bigint; origin text; batch uuid;
BEGIN
  IF pid IS NULL THEN RETURN 0; END IF;
  origin := COALESCE(NULLIF(current_setting('myjira.change_reason',true),''),'human');
  batch := NULLIF(current_setting('myjira.change_batch',true),'')::uuid;
  INSERT INTO project_revisions(project_id,workspace_id,revision) VALUES(pid,wid,1)
    ON CONFLICT(project_id) DO UPDATE SET revision=project_revisions.revision+1,updated_at=now()
    RETURNING revision INTO next_revision;
  INSERT INTO project_events(project_id,workspace_id,revision,kind,entity_id,cause,batch_id)
    VALUES(pid,wid,next_revision,event_kind,entity,origin,batch);
  RETURN next_revision;
END $$;

CREATE FUNCTION planning_work_item_event() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v bigint; parent_revision bigint; row_data jsonb; old_data jsonb; pid uuid; wid uuid; item uuid; actor uuid;
BEGIN
  IF TG_OP='DELETE' THEN row_data:=NULL; old_data:=(to_jsonb(OLD)-'description_binary')||jsonb_build_object('state_group',(SELECT group_name FROM states WHERE id=OLD.state_id));
    pid:=OLD.project_id; wid:=OLD.workspace_id; item:=OLD.id; actor:=OLD.updated_by;
  ELSE row_data:=(to_jsonb(NEW)-'description_binary')||jsonb_build_object('state_group',(SELECT group_name FROM states WHERE id=NEW.state_id)); pid:=NEW.project_id; wid:=NEW.workspace_id; item:=NEW.id; actor:=NEW.updated_by;
    IF TG_OP='UPDATE' THEN old_data:=(to_jsonb(OLD)-'description_binary')||jsonb_build_object('state_group',(SELECT group_name FROM states WHERE id=OLD.state_id)); END IF;
  END IF;
  IF TG_OP='UPDATE' AND OLD.project_id IS DISTINCT FROM NEW.project_id THEN
    PERFORM planning_emit_event(OLD.workspace_id,OLD.project_id,'work_item.removed',OLD.id);
  END IF;
  v:=planning_emit_event(wid,pid,'work_item.'||lower(TG_OP),item);
  INSERT INTO work_item_history(workspace_id,project_id,work_item_id,revision,actor_id,action,before_data,after_data,cause,batch_id)
    VALUES(wid,pid,item,v,actor,lower(TG_OP),old_data,row_data,
      COALESCE(NULLIF(current_setting('myjira.change_reason',true),''),'human'),NULLIF(current_setting('myjira.change_batch',true),'')::uuid);
  -- Parent undo must see later human child/linkage work, including an
  -- attach/detach sequence whose final child set equals its original set.
  IF TG_OP<>'INSERT' AND OLD.parent_id IS NOT NULL AND OLD.deleted_at IS NULL AND
    (TG_OP='DELETE' OR NEW.parent_id IS DISTINCT FROM OLD.parent_id OR NEW.project_id IS DISTINCT FROM OLD.project_id OR NEW.deleted_at IS NOT NULL) THEN
    parent_revision:=v;
    IF OLD.project_id IS DISTINCT FROM pid THEN parent_revision:=planning_emit_event(OLD.workspace_id,OLD.project_id,'work_item.hierarchy_changed',OLD.parent_id); END IF;
    INSERT INTO work_item_history(workspace_id,project_id,work_item_id,revision,actor_id,action,before_data,after_data,cause,batch_id)
      VALUES(OLD.workspace_id,OLD.project_id,OLD.parent_id,parent_revision,actor,'hierarchy.unlink',
        jsonb_build_object('child_id',OLD.id,'parent_id',OLD.parent_id),NULL,
        COALESCE(NULLIF(current_setting('myjira.change_reason',true),''),'human'),NULLIF(current_setting('myjira.change_batch',true),'')::uuid);
  END IF;
  IF TG_OP<>'DELETE' AND NEW.parent_id IS NOT NULL AND NEW.deleted_at IS NULL AND
    (TG_OP='INSERT' OR NEW.parent_id IS DISTINCT FROM OLD.parent_id OR NEW.project_id IS DISTINCT FROM OLD.project_id OR OLD.deleted_at IS NOT NULL) THEN
    INSERT INTO work_item_history(workspace_id,project_id,work_item_id,revision,actor_id,action,before_data,after_data,cause,batch_id)
      VALUES(NEW.workspace_id,NEW.project_id,NEW.parent_id,v,actor,'hierarchy.link',NULL,
        jsonb_build_object('child_id',NEW.id,'parent_id',NEW.parent_id),
        COALESCE(NULLIF(current_setting('myjira.change_reason',true),''),'human'),NULLIF(current_setting('myjira.change_batch',true),'')::uuid);
  END IF;
  RETURN COALESCE(NEW,OLD);
END $$;
CREATE TRIGGER planning_work_item_events AFTER INSERT OR UPDATE OR DELETE ON work_items FOR EACH ROW EXECUTE FUNCTION planning_work_item_event();

-- Related writes are durable facts too, including comments used for safe undo.
CREATE FUNCTION planning_related_event() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE row_data jsonb; previous jsonb; item uuid; v bigint; wid uuid; pid uuid;
BEGIN
  IF TG_OP='DELETE' THEN row_data:=to_jsonb(OLD); ELSE row_data:=to_jsonb(NEW); END IF;
  IF TG_OP<>'INSERT' THEN previous:=to_jsonb(OLD); END IF;
  wid:=(row_data->>'workspace_id')::uuid; pid:=(row_data->>'project_id')::uuid;
  IF wid IS NULL OR pid IS NULL THEN RETURN COALESCE(NEW,OLD); END IF;
  item:=COALESCE((row_data->>'work_item_id')::uuid,(row_data->>'source_id')::uuid);
  IF item IS NULL AND row_data->>'comment_id' IS NOT NULL THEN
    SELECT work_item_id INTO item FROM comments WHERE id=(row_data->>'comment_id')::uuid;
  END IF;
  IF TG_TABLE_NAME='file_assets' THEN
    row_data:=row_data-'object_key'-'metadata'; previous:=previous-'object_key'-'metadata';
  END IF;
  v:=planning_emit_event(wid,pid,TG_TABLE_NAME||'.'||lower(TG_OP),item);
  IF item IS NOT NULL THEN
    INSERT INTO work_item_history(workspace_id,project_id,work_item_id,revision,actor_id,action,before_data,after_data,cause,batch_id)
      VALUES(wid,pid,item,v,NULLIF(current_setting('myjira.change_actor',true),'')::uuid,TG_TABLE_NAME||'.'||lower(TG_OP),
        previous,CASE WHEN TG_OP='DELETE' THEN NULL ELSE row_data END,
        COALESCE(NULLIF(current_setting('myjira.change_reason',true),''),'human'),NULLIF(current_setting('myjira.change_batch',true),'')::uuid);
    IF row_data->>'target_id' IS NOT NULL THEN
      INSERT INTO work_item_history(workspace_id,project_id,work_item_id,revision,action,before_data,after_data,cause,batch_id)
        VALUES(wid,pid,(row_data->>'target_id')::uuid,v,TG_TABLE_NAME||'.'||lower(TG_OP),previous,
          CASE WHEN TG_OP='DELETE' THEN NULL ELSE row_data END,
          COALESCE(NULLIF(current_setting('myjira.change_reason',true),''),'human'),NULLIF(current_setting('myjira.change_batch',true),'')::uuid);
    END IF;
  END IF;
  RETURN COALESCE(NEW,OLD);
END $$;
DO $$ DECLARE table_name text; BEGIN
  FOREACH table_name IN ARRAY ARRAY['work_item_assignees','work_item_relations','work_item_labels','cycle_items','module_items','comments','work_item_links','work_item_subscribers','reactions','file_assets'] LOOP
    EXECUTE format('CREATE TRIGGER planning_related_events AFTER INSERT OR UPDATE OR DELETE ON %I FOR EACH ROW EXECUTE FUNCTION planning_related_event()',table_name);
  END LOOP;
END $$;

CREATE FUNCTION planning_entity_event() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE d jsonb;
BEGIN
  d:=CASE WHEN TG_OP='DELETE' THEN to_jsonb(OLD) ELSE to_jsonb(NEW) END;
  PERFORM planning_emit_event((d->>'workspace_id')::uuid,(d->>'project_id')::uuid,TG_TABLE_NAME||'.'||lower(TG_OP),(d->>'id')::uuid);
  RETURN COALESCE(NEW,OLD);
END $$;
DO $$ DECLARE table_name text; BEGIN
  FOREACH table_name IN ARRAY ARRAY['requirement_activities','cycles','states'] LOOP
    EXECUTE format('CREATE TRIGGER planning_entity_events AFTER INSERT OR UPDATE OR DELETE ON %I FOR EACH ROW EXECUTE FUNCTION planning_entity_event()',table_name);
  END LOOP;
END $$;

-- A workflow group edit changes execution meaning without updating state_id.
-- Record that fact at its observed time instead of joining today's group while
-- reconstructing an earlier forecast or bottleneck baseline.
CREATE FUNCTION planning_state_group_history() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE item record; v bigint; snapshot jsonb;
BEGIN
  IF NEW.group_name IS NOT DISTINCT FROM OLD.group_name THEN RETURN NEW; END IF;
  FOR item IN SELECT * FROM work_items WHERE state_id=NEW.id AND deleted_at IS NULL ORDER BY id LOOP
    snapshot:=to_jsonb(item)-'description_binary';
    v:=planning_emit_event(item.workspace_id,item.project_id,'work_item.workflow_changed',item.id);
    INSERT INTO work_item_history(workspace_id,project_id,work_item_id,revision,actor_id,action,before_data,after_data,cause,batch_id)
      VALUES(item.workspace_id,item.project_id,item.id,v,NULLIF(current_setting('myjira.change_actor',true),'')::uuid,'update',
        snapshot||jsonb_build_object('state_group',OLD.group_name),snapshot||jsonb_build_object('state_group',NEW.group_name),
        COALESCE(NULLIF(current_setting('myjira.change_reason',true),''),'human'),NULLIF(current_setting('myjira.change_batch',true),'')::uuid);
  END LOOP;
  RETURN NEW;
END $$;
CREATE TRIGGER planning_state_group_facts AFTER UPDATE OF group_name ON states FOR EACH ROW EXECUTE FUNCTION planning_state_group_history();

-- Current authorization is checked independently by SSE, even for idle projects.
CREATE FUNCTION planning_authorization_event() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE d jsonb; project record;
BEGIN
  d:=CASE WHEN TG_OP='DELETE' THEN to_jsonb(OLD) ELSE to_jsonb(NEW) END;
  FOR project IN SELECT id,workspace_id FROM projects WHERE workspace_id=(d->>'workspace_id')::uuid ORDER BY id LOOP
    PERFORM planning_emit_event(project.workspace_id,project.id,'authorization.changed',NULL);
  END LOOP;
  RETURN COALESCE(NEW,OLD);
END $$;
CREATE TRIGGER planning_workspace_membership_events AFTER INSERT OR UPDATE OR DELETE ON workspace_members FOR EACH ROW EXECUTE FUNCTION planning_authorization_event();
CREATE TRIGGER planning_project_membership_events AFTER INSERT OR UPDATE OR DELETE ON project_members FOR EACH ROW EXECUTE FUNCTION planning_authorization_event();
CREATE TRIGGER planning_project_visibility_events AFTER UPDATE OF network,guest_can_view_all,deleted_at,timezone ON projects FOR EACH ROW EXECUTE FUNCTION planning_authorization_event();

-- Historical baseline starts here; no invented pre-migration transitions.
INSERT INTO project_revisions(project_id,workspace_id,revision) SELECT id,workspace_id,0 FROM projects ON CONFLICT DO NOTHING;
INSERT INTO work_item_history(workspace_id,project_id,work_item_id,revision,actor_id,action,after_data,cause)
 SELECT workspace_id,project_id,id,0,updated_by,'coverage_started',(to_jsonb(w)-'description_binary')||jsonb_build_object('state_group',(SELECT group_name FROM states WHERE id=w.state_id)),'migration' FROM work_items w;
