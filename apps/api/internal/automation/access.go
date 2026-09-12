package automation

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/identity"
	"my-jira/apps/api/internal/support/data"
)

func authorizeItems(ctx context.Context, q database.DBTX, scope identity.Scope, ids []uuid.UUID, allowDeleted bool) error {
	if len(ids) == 0 {
		return nil
	}
	seen := map[uuid.UUID]bool{}
	distinct := []uuid.UUID{}
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			distinct = append(distinct, id)
		}
	}
	var count int
	if e := q.QueryRowContext(ctx, `SELECT count(*) FROM work_items w WHERE w.workspace_id=$1 AND w.project_id=$2 AND (w.deleted_at IS NULL OR $4) AND w.id IN (SELECT value::uuid FROM jsonb_array_elements_text($3::jsonb))`, scope.WorkspaceID, scope.ProjectID, string(raw(distinct)), allowDeleted).Scan(&count); e != nil {
		return e
	}
	if count != len(distinct) {
		return data.Missing()
	}
	var hidden bool
	if e := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM automation_source_mappings m JOIN automation_runs origin ON origin.id=m.run_id WHERE m.project_id=$2 AND m.work_item_id IN (SELECT value::uuid FROM jsonb_array_elements_text($3::jsonb)) AND origin.input->'source'->>'page_id' IS NOT NULL AND NOT EXISTS(SELECT 1 FROM pages p WHERE p.id=(origin.input->'source'->>'page_id')::uuid AND p.project_id=$2 AND `+data.VisiblePage("p", "$1", "$4")+`))`, scope.WorkspaceID, scope.ProjectID, string(raw(distinct)), scope.Actor.UserID).Scan(&hidden); e != nil {
		return e
	}
	if hidden {
		return data.Missing()
	}
	return nil
}
func authorizeRunEvidence(ctx context.Context, q database.DBTX, scope identity.Scope, run Run) error {
	if e := authorizeInputEvidence(ctx, q, scope, run.Kind, run.Input); e != nil {
		return e
	}
	var afterRaw []byte
	if e := q.QueryRowContext(ctx, `SELECT after_state FROM automation_runs WHERE id=$1`, run.ID).Scan(&afterRaw); e != nil {
		return e
	}
	var after []SavedItem
	if e := json.Unmarshal(afterRaw, &after); e != nil {
		return e
	}
	ids := []uuid.UUID{}
	for _, item := range after {
		ids = append(ids, item.ID)
	}
	return authorizeItems(ctx, q, scope, ids, run.Status == "undone")
}
func authorizeInputEvidence(ctx context.Context, q database.DBTX, scope identity.Scope, kind string, input Input) error {
	ids := []uuid.UUID{}
	for _, item := range input.Items {
		ids = append(ids, item.ID)
	}
	for _, fact := range input.History {
		ids = append(ids, fact.ItemID)
	}
	if e := authorizeItems(ctx, q, scope, ids, false); e != nil {
		return e
	}
	for _, encoded := range input.Quality {
		var report struct {
			ID uuid.UUID `json:"id"`
		}
		if e := json.Unmarshal(encoded, &report); e != nil {
			return e
		}
		if e := authorizeQualityReport(ctx, q, scope, report.ID); e != nil {
			return e
		}
	}
	for _, risk := range input.Risks {
		if e := authorizeRiskEvidence(ctx, q, scope, risk); e != nil {
			return e
		}
	}
	if kind == "risk" && len(input.External) > 0 {
		var risk Risk
		if json.Unmarshal(input.External, &risk) == nil && risk.Type != "" {
			if e := authorizeRiskEvidence(ctx, q, scope, risk); e != nil {
				return e
			}
		}
	}
	if kind == "quality" && len(input.External) > 0 {
		var source struct {
			ReportID uuid.UUID `json:"report_id"`
		}
		if e := json.Unmarshal(input.External, &source); e != nil {
			return e
		}
		if source.ReportID != uuid.Nil {
			return authorizeQualityReport(ctx, q, scope, source.ReportID)
		}
	}
	return nil
}
