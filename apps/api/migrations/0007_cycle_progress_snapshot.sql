ALTER TABLE cycles ADD COLUMN progress_snapshot jsonb;
ALTER TABLE cycles ADD CONSTRAINT cycles_progress_snapshot_object
  CHECK(progress_snapshot IS NULL OR jsonb_typeof(progress_snapshot)='object');
