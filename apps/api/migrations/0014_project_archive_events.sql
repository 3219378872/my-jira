-- Archiving is a planning permission change even while project reads remain
-- authorized. Publish it durably so a snapshot/subscription gap cannot retain
-- editing controls or source-backed content from the earlier scope.
CREATE TRIGGER planning_project_archive_events
  AFTER UPDATE OF archived_at ON projects
  FOR EACH ROW WHEN (OLD.archived_at IS DISTINCT FROM NEW.archived_at)
  EXECUTE FUNCTION planning_authorization_event();
