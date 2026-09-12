package automation

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/httpapi"
	"my-jira/apps/api/internal/platform/identity"
	"my-jira/apps/api/internal/requirements"
	"my-jira/apps/api/internal/support/data"
	"my-jira/apps/api/internal/workitems"
)

type UndoConflict struct {
	ItemID uuid.UUID `json:"item_id"`
	Reason string    `json:"reason"`
}

func (s *Service) undoRun(c *gin.Context) {
	scope, e := s.scope(c, identity.Admin)
	if e != nil {
		data.Fail(c, e)
		return
	}
	id, e := httpapi.UUIDParam(c, "runID")
	if e != nil {
		data.Fail(c, e)
		return
	}
	conflicts, e := s.Undo(c.Request.Context(), scope, id)
	if e != nil {
		data.Fail(c, e)
		return
	}
	if len(conflicts) > 0 {
		c.JSON(409, gin.H{"error": gin.H{"code": "undo_conflict", "message": "Later edits, comments or related changes prevent atomic undo; all work was retained", "conflicts": conflicts}})
		return
	}
	s.sendRun(c, scope, id, 200)
}
func (s *Service) Undo(ctx context.Context, scope identity.Scope, id uuid.UUID) ([]UndoConflict, error) {
	conflicts := []UndoConflict{}
	e := s.d.DB.WithinTx(ctx, func(q database.DBTX) error {
		if e := lockProject(ctx, q, scope.WorkspaceID, scope.ProjectID); e != nil {
			return e
		}
		current, e := currentActorScope(ctx, q, scope.Actor, scope.WorkspaceID, scope.ProjectID, identity.Admin)
		if e != nil {
			return e
		}
		run, e := loadRun(ctx, q, id, true)
		if e != nil {
			return e
		}
		if run.WorkspaceID != scope.WorkspaceID || run.ProjectID != scope.ProjectID {
			return data.Missing()
		}
		if run.Status == "undone" {
			return nil
		}
		if run.Status != "applied" && run.Status != "partial" {
			return blocked("Only an applied change batch can be undone")
		}
		if run.Input.Source.PageID != nil {
			if _, e := s.readSource(ctx, q, current, run.Input.Source, true); e != nil {
				return e
			}
		}
		var beforeRaw, afterRaw []byte
		if e = q.QueryRowContext(ctx, `SELECT before_state,after_state FROM automation_runs WHERE id=$1`, id).Scan(&beforeRaw, &afterRaw); e != nil {
			return e
		}
		var before, after []SavedItem
		if e = json.Unmarshal(beforeRaw, &before); e != nil {
			return e
		}
		if e = json.Unmarshal(afterRaw, &after); e != nil {
			return e
		}
		if len(after) == 0 {
			return blocked("This run did not create an undoable business batch")
		}
		ids := []uuid.UUID{}
		for _, item := range after {
			ids = append(ids, item.ID)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
		for _, itemID := range ids {
			var locked uuid.UUID
			if e = q.QueryRowContext(ctx, `SELECT id FROM work_items WHERE id=$1 AND workspace_id=$2 AND project_id=$3 FOR UPDATE`, itemID, scope.WorkspaceID, scope.ProjectID).Scan(&locked); e != nil {
				return e
			}
		}
		// Recheck authorization after item waits, then hold the same revision row
		// used by comment/relation triggers. A concurrent edit cannot commit between
		// fingerprint inspection and inverse command application.
		current, e = currentActorScope(ctx, q, scope.Actor, scope.WorkspaceID, scope.ProjectID, identity.Admin)
		if e != nil {
			return e
		}
		var revision int64
		if e = q.QueryRowContext(ctx, `SELECT revision FROM project_revisions WHERE project_id=$1 FOR UPDATE`, scope.ProjectID).Scan(&revision); e != nil {
			return e
		}
		for _, item := range after {
			fp, e := workitems.MutationFingerprint(ctx, q, scope.WorkspaceID, scope.ProjectID, item.ID)
			if e != nil {
				return e
			}
			if fp != item.Fingerprint {
				conflicts = append(conflicts, UndoConflict{ItemID: item.ID, Reason: "A durable item, comment or relation fact changed after the automatic batch"})
			}
		}
		if len(conflicts) > 0 {
			_, e = q.ExecContext(ctx, `UPDATE automation_runs SET undo_conflicts=$2::jsonb,updated_at=now() WHERE id=$1`, id, string(raw(conflicts)))
			return e
		}
		versions := map[uuid.UUID]int64{}
		for _, item := range after {
			versions[item.ID] = item.Version
		}
		batch := requirements.ChangeSet{IdempotencyKey: "automation-undo:" + id.String(), ExpectedRevision: &revision, Reason: "human:undo:" + id.String(), Commands: []requirements.Command{}}
		// Restore updates first, then delete created objects in reverse creation
		// order so child/dependency rows never cascade away unrelated work.
		for _, item := range before {
			batch.Commands = append(batch.Commands, requirements.Command{Operation: "update", ID: item.ID, Version: versions[item.ID], Fields: item.Fields})
		}
		for i := len(after) - 1; i >= 0; i-- {
			if after[i].Created {
				batch.Commands = append(batch.Commands, requirements.Command{Operation: "delete", ID: after[i].ID, Version: after[i].Version})
			}
		}
		if _, e = requirements.NewService(s.d).ApplyWithinTx(ctx, q, current.Actor, current.WorkspaceID, current.ProjectID, batch); e != nil {
			return e
		}
		if _, e = q.ExecContext(ctx, `DELETE FROM automation_source_mappings WHERE run_id=$1`, id); e != nil {
			return e
		}
		restored := map[uuid.UUID]bool{}
		for _, item := range before {
			restored[item.ID] = true
		}
		for _, mapping := range run.Input.Mappings {
			if !restored[mapping.ItemID] {
				continue
			}
			var version int64
			if e = q.QueryRowContext(ctx, `SELECT version FROM work_items WHERE id=$1`, mapping.ItemID).Scan(&version); e != nil {
				return e
			}
			if _, e = q.ExecContext(ctx, `INSERT INTO automation_source_mappings(project_id,source_key,entity_key,work_item_id,run_id,source_revision,source_location,generated_fields,last_item_version) VALUES($1,$2,$3,$4,$5,$6,$7::jsonb,$8::jsonb,$9) ON CONFLICT(project_id,source_key,entity_key) DO UPDATE SET run_id=EXCLUDED.run_id,source_revision=EXCLUDED.source_revision,source_location=EXCLUDED.source_location,generated_fields=EXCLUDED.generated_fields,last_item_version=EXCLUDED.last_item_version,updated_at=now()`, scope.ProjectID, mapping.SourceKey, mapping.EntityKey, mapping.ItemID, mapping.RunID, mapping.SourceRevision, string(raw(mapping.Location)), string(raw(mapping.Fields)), version); e != nil {
				return e
			}
		}
		if _, e = q.ExecContext(ctx, `UPDATE automation_risks SET action_item_id=NULL,version=version+1 WHERE action_item_id IN (SELECT (x->>'item_id')::uuid FROM jsonb_array_elements($1::jsonb) x WHERE x->>'created'='true')`, string(afterRaw)); e != nil {
			return e
		}
		if _, e = q.ExecContext(ctx, `UPDATE automation_improvements SET action_item_id=NULL,status='insufficient_evidence',observed=jsonb_build_object('reason','The action was undone; improvement is not established'),version=version+1,updated_at=now() WHERE action_item_id IN (SELECT (x->>'item_id')::uuid FROM jsonb_array_elements($1::jsonb) x WHERE x->>'created'='true')`, string(afterRaw)); e != nil {
			return e
		}
		_, e = q.ExecContext(ctx, `UPDATE automation_runs SET status='undone',undo_conflicts='[]'::jsonb,updated_at=now() WHERE id=$1`, id)
		return e
	})
	return conflicts, e
}
