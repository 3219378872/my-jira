-- A move may update the owning item and its children within one transaction.
-- Every referenced scope must be consistent when that transaction commits.
DO $$
DECLARE fk record;
BEGIN
  FOR fk IN SELECT c.conname,t.relname FROM pg_constraint c JOIN pg_class t ON t.oid=c.conrelid
    JOIN pg_namespace n ON n.oid=t.relnamespace WHERE c.contype='f' AND n.nspname=current_schema()
  LOOP
    EXECUTE format('ALTER TABLE %I ALTER CONSTRAINT %I DEFERRABLE INITIALLY DEFERRED',fk.relname,fk.conname);
  END LOOP;
END $$;
