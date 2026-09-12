package automation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
	"my-jira/apps/api/internal/platform"
	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/httpapi"
	"my-jira/apps/api/internal/platform/identity"
	"my-jira/apps/api/internal/support/data"
)

type ExternalInput struct {
	IdempotencyKey string `json:"idempotency_key"`
	Kind           string `json:"kind"`
	Evidence       any    `json:"evidence"`
	Title          string `json:"title"`
	Description    string `json:"description"`
	// OnApplied persists an owning module's action link in the same transaction.
	OnApplied func(context.Context, database.DBTX, []uuid.UUID) error `json:"-"`
}
type ExternalResult struct {
	RunID   uuid.UUID   `json:"run_id"`
	ItemIDs []uuid.UUID `json:"item_ids"`
	Status  string      `json:"status"`
}

// ApplyExternal is the boundary for immutable external evidence already analyzed
// by a durable integration worker. The validator holds the integration's current
// installation/source authorization locks through the shared atomic batch.
func ApplyExternal(ctx context.Context, d platform.Dependencies, scope identity.Scope, in ExternalInput, validate func(context.Context, database.DBTX) error) (ExternalResult, error) {
	result := ExternalResult{ItemIDs: []uuid.UUID{}}
	if !contains([]string{"quality", "risk", "efficiency"}, in.Kind) || strings.TrimSpace(in.IdempotencyKey) == "" || len(in.IdempotencyKey) > 160 || strings.TrimSpace(in.Title) == "" || len([]rune(in.Title)) > 255 || len(in.Description) > 50000 || len(raw(in.Evidence)) > 2<<20 {
		return result, httpapi.NewError(400, "validation_failed", "External actions need bounded immutable evidence, an idempotency key and a title")
	}
	service := NewService(d)
	err := d.DB.WithinTx(ctx, func(q database.DBTX) error {
		if e := lockProject(ctx, q, scope.WorkspaceID, scope.ProjectID); e != nil {
			return e
		}
		current, e := currentActorScope(ctx, q, scope.Actor, scope.WorkspaceID, scope.ProjectID, identity.Admin)
		if e != nil {
			return e
		}
		if validate == nil {
			return data.Invalid("External actions require a current source authorization validator")
		}
		if e = validate(ctx, q); e != nil {
			return e
		}
		hash := fingerprint(in)
		var existingID uuid.UUID
		var existingHash, status string
		var saved []byte
		e = q.QueryRowContext(ctx, `SELECT id,input_fingerprint,status,after_state FROM automation_runs WHERE project_id=$1 AND idempotency_key=$2`, scope.ProjectID, in.IdempotencyKey).Scan(&existingID, &existingHash, &status, &saved)
		if e == nil {
			if hash != existingHash {
				return blocked("The external action idempotency key already identifies different evidence")
			}
			result.RunID = existingID
			result.Status = status
			var after []SavedItem
			if e = json.Unmarshal(saved, &after); e != nil {
				return e
			}
			for _, item := range after {
				if item.Created {
					result.ItemIDs = append(result.ItemIDs, item.ID)
				}
			}
			return nil
		}
		if e != sql.ErrNoRows {
			return e
		}
		p, e := policyAt(ctx, q, scope.ProjectID, true)
		if e != nil {
			return e
		}
		if !p.Enabled || !contains(p.AllowedKinds, in.Kind) {
			return blocked("Project automation does not authorize this external action")
		}
		var group struct {
			ReportID string `json:"report_id"`
		}
		_ = json.Unmarshal(raw(in.Evidence), &group)
		if p.MinIntervalSeconds > 0 {
			// All findings from one immutable report share one frequency window and
			// maximum batch budget; new reports cannot bypass the interval.
			var recent int
			e = q.QueryRowContext(ctx, `SELECT count(*) FROM automation_runs WHERE project_id=$1 AND created_at>now()-make_interval(secs=>$2) AND (COALESCE(input->'external'->>'report_id','')<>$3 OR $3='')`, scope.ProjectID, p.MinIntervalSeconds, group.ReportID).Scan(&recent)
			if e != nil {
				return e
			}
			if recent > 0 {
				return httpapi.NewError(409, "automation_frequency", "The project automation frequency window has not elapsed")
			}
		}
		if group.ReportID != "" {
			var count int
			if e = q.QueryRowContext(ctx, `SELECT count(*) FROM automation_runs WHERE project_id=$1 AND input->'external'->>'report_id'=$2 AND status IN ('applied','partial')`, scope.ProjectID, group.ReportID).Scan(&count); e != nil {
				return e
			}
			if count >= p.MaxChanges {
				return blocked("This report reached the policy's maximum changes per batch")
			}
		}
		input, e := service.snapshot(ctx, q, current, Request{Kind: "quality", IdempotencyKey: in.IdempotencyKey, Cause: "external"})
		if e != nil {
			return e
		}
		input.External = raw(in.Evidence)
		input.Request.Kind = in.Kind
		run := Run{ID: uuid.New(), WorkspaceID: scope.WorkspaceID, ProjectID: scope.ProjectID, RequestedBy: scope.Actor.UserID, AuthorizedBy: p.AuthorizedBy, Kind: in.Kind, Status: "running", PolicyVersion: p.Version, InputFingerprint: hash, Input: input, Cause: "external", Attempts: 1}
		_, e = q.ExecContext(ctx, `INSERT INTO automation_runs(id,workspace_id,project_id,requested_by,authorized_by,kind,status,idempotency_key,input_fingerprint,policy_version,input,algorithm_version,cause,attempts) VALUES($1,$2,$3,$4,$5,$6,'running',$7,$8,$9,$10::jsonb,$11,'external',1)`, run.ID, run.WorkspaceID, run.ProjectID, run.RequestedBy, run.AuthorizedBy, run.Kind, in.IdempotencyKey, hash, p.Version, string(raw(input)), AlgorithmVersion)
		if e != nil {
			return e
		}
		p, authorized, e := service.currentPolicy(ctx, q, run)
		if e != nil {
			return e
		}
		if e = service.checkInput(ctx, q, run, authorized); e != nil {
			return e
		}
		proposal := Proposal{Summary: "Policy-authorized action for immutable external evidence", Commands: []ProposalCommand{{Operation: "create", ClientID: "action", Fields: map[string]json.RawMessage{"name": raw(in.Title), "description_html": raw(in.Description), "requirement_type": raw(nil)}}}, Findings: []string{}, Results: in.Evidence}
		if e = service.commitProposal(ctx, q, run, p, authorized, proposal); e != nil {
			return e
		}
		if e = q.QueryRowContext(ctx, `SELECT status,after_state FROM automation_runs WHERE id=$1`, run.ID).Scan(&result.Status, &saved); e != nil {
			return e
		}
		var after []SavedItem
		if e = json.Unmarshal(saved, &after); e != nil {
			return e
		}
		result.RunID = run.ID
		for _, item := range after {
			if item.Created {
				result.ItemIDs = append(result.ItemIDs, item.ID)
			}
		}
		if in.OnApplied != nil {
			return in.OnApplied(ctx, q, result.ItemIDs)
		}
		return nil
	})
	var problem *data.Error
	if errors.As(err, &problem) {
		err = httpapi.NewError(problem.Status, problem.Code, problem.Message)
	}
	return result, err
}
