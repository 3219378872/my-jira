package migrations_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"

	"my-jira/apps/api/internal/workitems"
	"my-jira/apps/api/migrations"
)

// This is an upgrade test: fixtures are inserted into the actual 0008 schema,
// before any requirement fields, histories or event triggers exist.
func TestRequirementsUpgradeFrom0008PreservesLegacyData(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	db := requirementsUpgradeDatabase(t, ctx)
	files, err := fs.Glob(migrations.Files, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	upgradeExec(t, ctx, db, `CREATE TABLE schema_migrations(version text PRIMARY KEY,checksum text NOT NULL,applied_at timestamptz NOT NULL DEFAULT now())`)
	baselineCount := 0
	for _, name := range files {
		if name > "0008_project_guest_visibility.sql" {
			break
		}
		content, err := migrations.Files.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.ExecContext(ctx, string(content)); err != nil {
			tx.Rollback()
			t.Fatalf("apply legacy %s: %v", name, err)
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO schema_migrations(version,checksum) VALUES($1,$2)`, name, fmt.Sprintf("%x", sha256.Sum256(content))); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
		baselineCount++
	}
	if baselineCount != 8 {
		t.Fatalf("legacy baseline has %d migrations, want 8", baselineCount)
	}
	var requirementColumns int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='work_items' AND column_name='requirement_type'`).Scan(&requirementColumns); err != nil || requirementColumns != 0 {
		t.Fatalf("fixture is not the legacy work-item schema: %d, %v", requirementColumns, err)
	}

	owner, workspace, otherWorkspace := uuid.New(), uuid.New(), uuid.New()
	project, sibling, unrelated, state := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	parent, child, deleted, cycle := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	upgradeExec(t, ctx, db, `INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test-only','Legacy owner')`, owner, "upgrade-"+uuid.NewString()+"@example.test")
	for _, wid := range []uuid.UUID{workspace, otherWorkspace} {
		upgradeExec(t, ctx, db, `INSERT INTO workspaces(id,name,slug,owner_id) VALUES($1,'Legacy workspace',$2,$3)`, wid, "upgrade-"+wid.String(), owner)
	}
	for i, scope := range []struct{ project, workspace uuid.UUID }{{project, workspace}, {sibling, workspace}, {unrelated, otherWorkspace}} {
		upgradeExec(t, ctx, db, `INSERT INTO projects(id,workspace_id,name,identifier,settings,guest_can_view_all) VALUES($1,$2,$3,$3,'{"legacy_preference":"retained"}',true)`, scope.project, scope.workspace, fmt.Sprintf("OLD%d", i))
	}
	upgradeExec(t, ctx, db, `INSERT INTO states(id,workspace_id,project_id,name,group_name,is_default) VALUES($1,$2,$3,'In progress','started',true)`, state, workspace, project)
	upgradeExec(t, ctx, db, `INSERT INTO work_items(id,workspace_id,project_id,state_id,created_by,updated_by,name,description_html,description_json,description_binary,sequence_id,priority,position,start_date,target_date,estimate,type_name,version) VALUES($1,$2,$3,$4,$5,$5,'Epic release plan','<p>Legacy description</p>','{"type":"doc","content":[]}',decode('010203','hex'),1,'high',987,'2026-08-01','2026-08-31',3.5,'Epic',7)`, parent, workspace, project, state, owner)
	upgradeExec(t, ctx, db, `INSERT INTO work_items(id,workspace_id,project_id,state_id,created_by,updated_by,name,sequence_id,parent_id,type_name) VALUES($1,$2,$3,$4,$5,$5,'Task execution',2,$6,'Task')`, child, workspace, project, state, owner, parent)
	upgradeExec(t, ctx, db, `INSERT INTO work_items(id,workspace_id,project_id,state_id,created_by,updated_by,name,sequence_id,archived_at,deleted_at) VALUES($1,$2,$3,$4,$5,$5,'User story retained tombstone',3,'2026-08-15T12:00:00Z','2026-08-20T12:00:00Z')`, deleted, workspace, project, state, owner)
	upgradeExec(t, ctx, db, `INSERT INTO work_item_assignees(workspace_id,project_id,work_item_id,user_id) VALUES($1,$2,$3,$4)`, workspace, project, child, owner)
	upgradeExec(t, ctx, db, `INSERT INTO work_item_relations(workspace_id,project_id,source_id,target_id,relation_type) VALUES($1,$2,$3,$4,'blocks')`, workspace, project, parent, child)
	upgradeExec(t, ctx, db, `INSERT INTO cycles(id,workspace_id,project_id,name,owner_id,start_date,end_date) VALUES($1,$2,$3,'Legacy commitment',$4,'2026-08-01','2026-08-31')`, cycle, workspace, project, owner)
	upgradeExec(t, ctx, db, `INSERT INTO cycle_items(workspace_id,project_id,cycle_id,work_item_id) VALUES($1,$2,$3,$4)`, workspace, project, cycle, child)
	legacyItems := upgradeJSON(t, ctx, db, `SELECT jsonb_agg(to_jsonb(w) ORDER BY id) FROM work_items w`)
	legacyProjects := upgradeJSON(t, ctx, db, `SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM projects p`)
	legacyLinks := upgradeJSON(t, ctx, db, `SELECT jsonb_build_object('assignees',(SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM work_item_assignees a),'dependencies',(SELECT jsonb_agg(to_jsonb(r) ORDER BY id) FROM work_item_relations r),'cycles',(SELECT jsonb_agg(to_jsonb(c) ORDER BY id) FROM cycle_items c))`)
	legacyLedger := upgradeJSON(t, ctx, db, `SELECT jsonb_agg(to_jsonb(m) ORDER BY version) FROM schema_migrations m`)
	if err = migrations.Apply(ctx, db); err != nil {
		t.Fatalf("upgrade real 0008 fixture: %v", err)
	}

	t.Run("legacy_rows_and_defaults", func(t *testing.T) {
		upgraded := upgradeJSON(t, ctx, db, `SELECT jsonb_agg(to_jsonb(w)-ARRAY['requirement_type','story_role','story_goal','story_benefit','acceptance_criteria','activity_id','map_position','estimated_minutes','remaining_minutes','required_skills','allocation_weights','planning_locked'] ORDER BY id) FROM work_items w`)
		if !reflect.DeepEqual(legacyItems, upgraded) {
			t.Fatal("upgrade changed legacy work-item fields, hierarchy, dates, content or tombstones")
		}
		if !reflect.DeepEqual(legacyProjects, upgradeJSON(t, ctx, db, `SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM projects p`)) {
			t.Fatal("upgrade changed existing projects or settings")
		}
		if !reflect.DeepEqual(legacyLinks, upgradeJSON(t, ctx, db, `SELECT jsonb_build_object('assignees',(SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM work_item_assignees a),'dependencies',(SELECT jsonb_agg(to_jsonb(r) ORDER BY id) FROM work_item_relations r),'cycles',(SELECT jsonb_agg(to_jsonb(c) ORDER BY id) FROM cycle_items c))`)) {
			t.Fatal("upgrade changed old assignees, dependencies or Cycle membership")
		}
		var defaults, coverage, emptyEvents, initialRevisions bool
		if err := db.QueryRowContext(ctx, `SELECT bool_and(requirement_type IS NULL AND activity_id IS NULL AND estimated_minutes IS NULL AND remaining_minutes IS NULL AND story_role='' AND story_goal='' AND story_benefit='' AND acceptance_criteria='[]' AND required_skills='[]' AND allocation_weights='[]' AND map_position=1024 AND NOT planning_locked) FROM work_items`).Scan(&defaults); err != nil || !defaults {
			t.Fatalf("legacy requirement defaults: %t, %v", defaults, err)
		}
		if err := db.QueryRowContext(ctx, `SELECT count(*)=3 AND count(DISTINCT work_item_id)=3 AND bool_and(revision=0 AND action='coverage_started' AND cause='migration' AND before_data IS NULL AND after_data->>'state_group'='started' AND NOT after_data ? 'description_binary' AND after_data->'requirement_type'='null'::jsonb) FROM work_item_history`).Scan(&coverage); err != nil || !coverage {
			t.Fatalf("migration invented history or lost coverage/defaults: %t, %v", coverage, err)
		}
		if err := db.QueryRowContext(ctx, `SELECT count(*)=0 FROM project_events`).Scan(&emptyEvents); err != nil || !emptyEvents {
			t.Fatalf("upgrade fabricated pre-migration events: %t, %v", emptyEvents, err)
		}
		if err := db.QueryRowContext(ctx, `SELECT count(*)=3 AND bool_and(revision=0) FROM project_revisions`).Scan(&initialRevisions); err != nil || !initialRevisions {
			t.Fatalf("initial project revisions: %t, %v", initialRevisions, err)
		}
		if err := workitems.RequireRequirements(ctx, db, workspace, project); err != nil {
			t.Fatalf("legacy missing rollout flag disabled requirements: %v", err)
		}
		newID := uuid.New()
		upgradeExec(t, ctx, db, `INSERT INTO work_items(id,workspace_id,project_id,state_id,created_by,updated_by,name,sequence_id) VALUES($1,$2,$3,$4,$5,$5,'New basic work after upgrade',4)`, newID, workspace, project, state, owner)
		if err := db.QueryRowContext(ctx, `SELECT requirement_type IS NULL AND estimated_minutes IS NULL AND remaining_minutes IS NULL AND map_position=1024 AND acceptance_criteria='[]' AND required_skills='[]' AND allocation_weights='[]' AND NOT planning_locked FROM work_items WHERE id=$1`, newID).Scan(&defaults); err != nil || !defaults {
			t.Fatalf("new basic work defaults after upgrade: %t, %v", defaults, err)
		}
	})

	t.Run("archive_and_rollout_events", func(t *testing.T) {
		assertEvents := func(name, query string, wantProject, wantSibling int64, args ...any) {
			t.Helper()
			before := upgradeEventCounts(t, ctx, db, project, sibling, unrelated)
			upgradeExec(t, ctx, db, query, args...)
			after := upgradeEventCounts(t, ctx, db, project, sibling, unrelated)
			want := [3]int64{before[0] + wantProject, before[1] + wantSibling, before[2]}
			if after != want {
				t.Fatalf("%s event counts = %v, want %v", name, after, want)
			}
		}
		assertEvents("archive", `UPDATE projects SET archived_at='2026-09-12T12:00:00Z' WHERE id=$1`, 1, 1, project)
		assertEvents("unchanged archive", `UPDATE projects SET archived_at=archived_at WHERE id=$1`, 0, 0, project)
		assertEvents("restore", `UPDATE projects SET archived_at=NULL WHERE id=$1`, 1, 1, project)
		assertEvents("missing to false", `UPDATE projects SET settings=settings||'{"requirements_enabled":false}' WHERE id=$1`, 1, 0, project)
		if err := workitems.RequireRequirements(ctx, db, workspace, project); err == nil {
			t.Fatal("false rollout flag allowed requirements")
		}
		assertEvents("false to false", `UPDATE projects SET settings=settings||'{"requirements_enabled":false}' WHERE id=$1`, 0, 0, project)
		assertEvents("false to null", `UPDATE projects SET settings=settings||'{"requirements_enabled":null}' WHERE id=$1`, 1, 0, project)
		if err := workitems.RequireRequirements(ctx, db, workspace, project); err != nil {
			t.Fatalf("null flag did not restore requirements: %v", err)
		}
		assertEvents("null to true", `UPDATE projects SET settings=settings||'{"requirements_enabled":true}' WHERE id=$1`, 0, 0, project)
		assertEvents("true to missing", `UPDATE projects SET settings=settings-'requirements_enabled' WHERE id=$1`, 0, 0, project)
		assertEvents("unrelated settings", `UPDATE projects SET settings=settings||'{"other_preference":123}' WHERE id=$1`, 0, 0, project)
		before := upgradeEventCounts(t, ctx, db, project, sibling, unrelated)
		for _, invalid := range []string{`"false"`, `0`, `[]`, `{}`} {
			if _, err := db.ExecContext(ctx, `UPDATE projects SET settings=jsonb_set(settings,'{requirements_enabled}',$2::jsonb) WHERE id=$1`, project, invalid); err == nil {
				t.Fatalf("migration CHECK accepted non-boolean rollout value %s", invalid)
			}
		}
		if after := upgradeEventCounts(t, ctx, db, project, sibling, unrelated); after != before {
			t.Fatalf("rejected flag values published events: %v -> %v", before, after)
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE projects SET settings=settings||'{"requirements_enabled":false}',archived_at=now() WHERE id=$1`, project); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		if after := upgradeEventCounts(t, ctx, db, project, sibling, unrelated); after != before {
			t.Fatalf("rolled-back archive/flag write leaked events: %v -> %v", before, after)
		}
		var privatePayload, contiguous bool
		if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM project_events WHERE kind='authorization.changed' AND (entity_id IS NOT NULL OR workspace_id<>$1 OR cause<>'human' OR batch_id IS NOT NULL))`, workspace).Scan(&privatePayload); err != nil || privatePayload {
			t.Fatalf("authorization invalidation contains unexpected payload/provenance: %t, %v", privatePayload, err)
		}
		if err := db.QueryRowContext(ctx, `SELECT bool_and(p.revision=(SELECT count(*) FROM project_events e WHERE e.project_id=p.project_id) AND p.revision=COALESCE((SELECT max(revision) FROM project_events e WHERE e.project_id=p.project_id),0)) FROM project_revisions p`).Scan(&contiguous); err != nil || !contiguous {
			t.Fatalf("rollback left project revision gaps: %t, %v", contiguous, err)
		}
	})

	t.Run("idempotency_and_checksum_validation", func(t *testing.T) {
		if !reflect.DeepEqual(legacyLedger, upgradeJSON(t, ctx, db, `SELECT jsonb_agg(to_jsonb(m) ORDER BY version) FROM schema_migrations m WHERE version<='0008_project_guest_visibility.sql'`)) {
			t.Fatal("upgrade rewrote old migration checksums or application timestamps")
		}
		var count int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&count); err != nil || count != len(files) {
			t.Fatalf("migration ledger has %d entries, want %d: %v", count, len(files), err)
		}
		ledger := upgradeJSON(t, ctx, db, `SELECT jsonb_agg(to_jsonb(m) ORDER BY version) FROM schema_migrations m`)
		facts := upgradeJSON(t, ctx, db, `SELECT jsonb_build_object('history',(SELECT jsonb_agg(to_jsonb(h) ORDER BY id) FROM work_item_history h),'events',(SELECT jsonb_agg(to_jsonb(e) ORDER BY project_id,revision) FROM project_events e),'revisions',(SELECT jsonb_agg(to_jsonb(r) ORDER BY project_id) FROM project_revisions r))`)
		if err := migrations.Apply(ctx, db); err != nil {
			t.Fatalf("idempotent reapplication: %v", err)
		}
		if !reflect.DeepEqual(ledger, upgradeJSON(t, ctx, db, `SELECT jsonb_agg(to_jsonb(m) ORDER BY version) FROM schema_migrations m`)) || !reflect.DeepEqual(facts, upgradeJSON(t, ctx, db, `SELECT jsonb_build_object('history',(SELECT jsonb_agg(to_jsonb(h) ORDER BY id) FROM work_item_history h),'events',(SELECT jsonb_agg(to_jsonb(e) ORDER BY project_id,revision) FROM project_events e),'revisions',(SELECT jsonb_agg(to_jsonb(r) ORDER BY project_id) FROM project_revisions r))`)) {
			t.Fatal("reapplication changed migration ledger, history or events")
		}
		// Change only the owned test ledger to simulate an applied-file mismatch.
		// The embedded SQL and all frozen migration files remain untouched.
		upgradeExec(t, ctx, db, `UPDATE schema_migrations SET checksum='intentional-test-mismatch' WHERE version='0015_requirements_feature_gate.sql'`)
		if err := migrations.Apply(ctx, db); err == nil || !strings.Contains(err.Error(), "migration 0015_requirements_feature_gate.sql changed after it was applied") {
			t.Fatalf("checksum mismatch was not rejected: %v", err)
		}
		content, err := migrations.Files.ReadFile("0015_requirements_feature_gate.sql")
		if err != nil {
			t.Fatal(err)
		}
		upgradeExec(t, ctx, db, `UPDATE schema_migrations SET checksum=$1 WHERE version='0015_requirements_feature_gate.sql'`, fmt.Sprintf("%x", sha256.Sum256(content)))
		if err := migrations.Apply(ctx, db); err != nil {
			t.Fatalf("restored test ledger did not recover: %v", err)
		}
		if !reflect.DeepEqual(ledger, upgradeJSON(t, ctx, db, `SELECT jsonb_agg(to_jsonb(m) ORDER BY version) FROM schema_migrations m`)) || !reflect.DeepEqual(facts, upgradeJSON(t, ctx, db, `SELECT jsonb_build_object('history',(SELECT jsonb_agg(to_jsonb(h) ORDER BY id) FROM work_item_history h),'events',(SELECT jsonb_agg(to_jsonb(e) ORDER BY project_id,revision) FROM project_events e),'revisions',(SELECT jsonb_agg(to_jsonb(r) ORDER BY project_id) FROM project_revisions r))`)) {
			t.Fatal("checksum rejection/recovery changed the ledger or domain facts")
		}
	})
	t.Logf("upgraded actual 0008 fixture through %s (%d checksummed migrations)", files[len(files)-1], len(files))
}

func requirementsUpgradeDatabase(t *testing.T, ctx context.Context) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL must explicitly name an isolated test database")
	}
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal("invalid isolated test database URL")
	}
	if !strings.Contains(strings.ToLower(strings.TrimPrefix(parsed.Path, "/")), "test") {
		t.Fatal("refusing migration test: database name must contain test")
	}
	base, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { base.Close() })
	var databaseName string
	if err := base.QueryRowContext(ctx, `SELECT current_database()`).Scan(&databaseName); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(databaseName), "test") {
		t.Fatal("refusing migration test: connected database name must contain test")
	}
	schema := "myjira_upgrade_0008_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	upgradeExec(t, ctx, base, `CREATE SCHEMA `+schema)
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := base.ExecContext(cleanupCtx, `DROP SCHEMA `+schema+` CASCADE`); err != nil {
			t.Errorf("remove owned upgrade test schema: %v", err)
		}
	})
	params := parsed.Query()
	params.Set("search_path", schema)
	parsed.RawQuery = params.Encode()
	db, err := sql.Open("pgx", parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	t.Logf("isolated migration fixture: database=%s schema=%s", databaseName, schema)
	return db
}

func upgradeExec(t *testing.T, ctx context.Context, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(ctx, query, args...); err != nil {
		t.Fatal(err)
	}
}

func upgradeJSON(t *testing.T, ctx context.Context, db *sql.DB, query string) any {
	t.Helper()
	var raw []byte
	if err := db.QueryRowContext(ctx, query).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var result any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func upgradeEventCounts(t *testing.T, ctx context.Context, db *sql.DB, project, sibling, unrelated uuid.UUID) [3]int64 {
	t.Helper()
	var result [3]int64
	if err := db.QueryRowContext(ctx, `SELECT count(*) FILTER(WHERE project_id=$1),count(*) FILTER(WHERE project_id=$2),count(*) FILTER(WHERE project_id=$3) FROM project_events WHERE kind='authorization.changed'`, project, sibling, unrelated).Scan(&result[0], &result[1], &result[2]); err != nil {
		t.Fatal(err)
	}
	return result
}
