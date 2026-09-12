-- Independently authored structured business scenarios and immutable revisions.
-- Source provenance is retained when a reference is removed from the editor, so
-- an earlier private source cannot be laundered into a wider readership.
CREATE TABLE business_scenarios (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL,
  project_id uuid NOT NULL,
  story_id uuid NOT NULL,
  body jsonb NOT NULL,
  version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
  review_needed boolean NOT NULL DEFAULT false,
  created_by uuid NOT NULL REFERENCES users(id),
  updated_by uuid NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  deleted_at timestamptz,
  FOREIGN KEY (project_id,workspace_id) REFERENCES projects(id,workspace_id),
  -- A later work-item move invalidates visibility in the old project. Keeping
  -- this foreign key on the stable identity lets that move commit atomically.
  FOREIGN KEY (story_id) REFERENCES work_items(id),
  CHECK (jsonb_typeof(body)='object')
);
CREATE INDEX business_scenarios_project ON business_scenarios(project_id,story_id,id) WHERE deleted_at IS NULL;
CREATE TABLE business_scenario_sources (
  scenario_id uuid NOT NULL REFERENCES business_scenarios(id),
  workspace_id uuid NOT NULL,
  page_id uuid NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (scenario_id,page_id),
  FOREIGN KEY (page_id,workspace_id) REFERENCES pages(id,workspace_id)
);
CREATE INDEX business_scenario_sources_page ON business_scenario_sources(page_id,scenario_id);
CREATE TABLE business_scenario_versions (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  scenario_id uuid NOT NULL REFERENCES business_scenarios(id),
  version bigint NOT NULL,
  snapshot jsonb NOT NULL,
  saved_by uuid NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(scenario_id,version)
);

CREATE FUNCTION scenarios_record_change() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE revision bigint; previous jsonb; action_name text;
BEGIN
  INSERT INTO business_scenario_versions(scenario_id,version,snapshot,saved_by)
    VALUES(NEW.id,NEW.version,to_jsonb(NEW),NEW.updated_by);
  revision := planning_emit_event(NEW.workspace_id,NEW.project_id,'scenario.changed',NEW.id);
  action_name := 'scenario.created';
  IF TG_OP='UPDATE' THEN
    previous := jsonb_build_object('scenario_id',OLD.id,'version',OLD.version,'deleted',OLD.deleted_at IS NOT NULL);
    action_name := CASE WHEN NEW.deleted_at IS NOT NULL AND OLD.deleted_at IS NULL THEN 'scenario.deleted' ELSE 'scenario.changed' END;
  END IF;
  -- A user's later scenario work is a durable association with its Story.
  -- Protected undo must detect it even if that scenario is subsequently deleted.
  -- Record only stable identifiers/revisions, never private scenario/source text.
  INSERT INTO work_item_history(workspace_id,project_id,work_item_id,revision,actor_id,action,before_data,after_data,cause,batch_id)
    VALUES(NEW.workspace_id,NEW.project_id,NEW.story_id,revision,NEW.updated_by,action_name,previous,
      jsonb_build_object('scenario_id',NEW.id,'version',NEW.version,'deleted',NEW.deleted_at IS NOT NULL),
      COALESCE(NULLIF(current_setting('myjira.change_reason',true),''),'human'),NULLIF(current_setting('myjira.change_batch',true),'')::uuid);
  RETURN NEW;
END $$;
CREATE TRIGGER business_scenarios_record_change AFTER INSERT OR UPDATE ON business_scenarios
  FOR EACH ROW EXECUTE FUNCTION scenarios_record_change();

CREATE FUNCTION scenarios_story_review() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE changed boolean; prior_project record;
BEGIN
  changed := (NEW.description_html,NEW.description_json,NEW.story_role,NEW.story_goal,NEW.story_benefit,NEW.acceptance_criteria)
    IS DISTINCT FROM
    (OLD.description_html,OLD.description_json,OLD.story_role,OLD.story_goal,OLD.story_benefit,OLD.acceptance_criteria);
  IF (NEW.project_id,NEW.workspace_id) IS DISTINCT FROM (OLD.project_id,OLD.workspace_id) THEN
    FOR prior_project IN SELECT DISTINCT workspace_id,project_id FROM business_scenarios WHERE story_id=NEW.id AND deleted_at IS NULL LOOP
      PERFORM planning_emit_event(prior_project.workspace_id,prior_project.project_id,'scenario.removed',NULL);
    END LOOP;
    UPDATE business_scenarios SET workspace_id=NEW.workspace_id,project_id=NEW.project_id,
      review_needed=review_needed OR changed,version=version+1,updated_at=now(),updated_by=NEW.updated_by
      WHERE story_id=NEW.id AND deleted_at IS NULL;
  ELSIF changed THEN
    UPDATE business_scenarios SET review_needed=true,version=version+1,updated_at=now(),updated_by=NEW.updated_by
      WHERE story_id=NEW.id AND deleted_at IS NULL;
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER business_scenario_story_review AFTER UPDATE ON work_items
  FOR EACH ROW EXECUTE FUNCTION scenarios_story_review();

CREATE FUNCTION scenarios_page_review() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE linked record;
BEGIN
  IF (NEW.content_html,NEW.content_json) IS DISTINCT FROM (OLD.content_html,OLD.content_json) THEN
    UPDATE business_scenarios s SET review_needed=true,version=version+1,updated_at=now()
      WHERE s.deleted_at IS NULL AND EXISTS(SELECT 1 FROM business_scenario_sources src WHERE src.scenario_id=s.id AND src.page_id=NEW.id);
  END IF;
  IF (NEW.is_private,NEW.owner_id,NEW.project_id,NEW.archived_at,NEW.deleted_at)
     IS DISTINCT FROM (OLD.is_private,OLD.owner_id,OLD.project_id,OLD.archived_at,OLD.deleted_at) THEN
    FOR linked IN SELECT DISTINCT s.workspace_id,s.project_id FROM business_scenarios s
      JOIN business_scenario_sources src ON src.scenario_id=s.id WHERE src.page_id=NEW.id AND s.deleted_at IS NULL
    LOOP
      -- Identifiers only. SSE consumers discard/refetch cached scenarios using
      -- their current source permissions, independently of content revisions.
      PERFORM planning_emit_event(linked.workspace_id,linked.project_id,'scenario.visibility_changed',NULL);
    END LOOP;
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER business_scenario_page_review AFTER UPDATE ON pages
  FOR EACH ROW EXECUTE FUNCTION scenarios_page_review();
