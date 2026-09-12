-- The independently enabled requirements bundle is separate from AI policy.
-- Missing/null keeps compatibility for projects created before rollout controls.
ALTER TABLE projects ADD CONSTRAINT projects_requirements_flag_boolean CHECK (
  NOT (settings ? 'requirements_enabled')
  OR settings->'requirements_enabled'='null'::jsonb
  OR jsonb_typeof(settings->'requirements_enabled')='boolean'
);

CREATE FUNCTION planning_requirements_flag_event() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  PERFORM planning_emit_event(NEW.workspace_id,NEW.id,'authorization.changed',NULL);
  RETURN NEW;
END $$;
CREATE TRIGGER planning_requirements_flag_events
  AFTER UPDATE OF settings ON projects
  FOR EACH ROW WHEN (
    COALESCE(OLD.settings->'requirements_enabled'='false'::jsonb,false)
    IS DISTINCT FROM COALESCE(NEW.settings->'requirements_enabled'='false'::jsonb,false)
  ) EXECUTE FUNCTION planning_requirements_flag_event();
