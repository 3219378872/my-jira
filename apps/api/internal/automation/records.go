package automation

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/httpapi"
	"my-jira/apps/api/internal/platform/identity"
	"my-jira/apps/api/internal/requirements"
	"my-jira/apps/api/internal/support/data"
)

func (s *Service) persistAnalysis(ctx context.Context, q database.DBTX, run Run, scope identity.Scope, proposal Proposal, result requirements.ChangeResult) error {
	switch run.Kind {
	case "forecast":
		return saveForecast(ctx, q, run, proposal.Results.(Forecast))
	case "risk":
		risks := proposal.Results.(RiskResult)
		if e := saveForecast(ctx, q, run, risks.Forecast); e != nil {
			return e
		}
		seen := map[string]bool{}
		for _, risk := range risks.Risks {
			seen[risk.Key] = true
			var existing Risk
			var encoded []byte
			e := q.QueryRowContext(ctx, `SELECT to_jsonb(r) FROM automation_risks r WHERE project_id=$1 AND key=$2 FOR UPDATE`, run.ProjectID, risk.Key).Scan(&encoded)
			fresh := e == sql.ErrNoRows
			if e != nil && !fresh {
				return e
			}
			if !fresh {
				if e = json.Unmarshal(encoded, &existing); e != nil {
					return e
				}
				risk.ID = existing.ID
			} else {
				risk.ID = uuid.New()
			}
			changed := fresh || existing.Status == "resolved" || existing.Severity != risk.Severity || existing.Reason != risk.Reason
			status := "open"
			if !fresh && existing.Status == "acknowledged" {
				status = "acknowledged"
			}
			action := existing.ActionItemID
			if id, ok := result.CreatedIDs["risk:"+fingerprint(risk.Key)[:20]]; ok {
				action = &id
			}
			_, e = q.ExecContext(ctx, `INSERT INTO automation_risks(id,workspace_id,project_id,key,type,severity,status,reason,evidence,item_ids,action_item_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9::jsonb,$10::jsonb,$11) ON CONFLICT(project_id,key) DO UPDATE SET type=EXCLUDED.type,severity=EXCLUDED.severity,status=EXCLUDED.status,reason=EXCLUDED.reason,evidence=EXCLUDED.evidence,item_ids=EXCLUDED.item_ids,action_item_id=COALESCE(EXCLUDED.action_item_id,automation_risks.action_item_id),version=automation_risks.version+1,last_seen=now()`, risk.ID, run.WorkspaceID, run.ProjectID, risk.Key, risk.Type, risk.Severity, status, risk.Reason, string(raw(risk.Evidence)), string(raw(risk.ItemIDs)), action)
			if e != nil {
				return e
			}
			if changed {
				if _, e = q.ExecContext(ctx, `INSERT INTO automation_risk_events(id,risk_id,run_id,status,evidence) VALUES($1,$2,$3,$4,$5::jsonb)`, uuid.New(), risk.ID, run.ID, status, string(raw(risk.Evidence))); e != nil {
					return e
				}
				if e = notifyRisk(ctx, q, scope, risk); e != nil {
					return e
				}
			}
		}
		for _, old := range run.Input.Risks {
			if seen[old.Key] || old.Status == "resolved" {
				continue
			}
			if old.Type == "quality" && len(run.Input.Quality) == 0 {
				continue
			}
			if _, e := q.ExecContext(ctx, `UPDATE automation_risks SET status='resolved',version=version+1,last_seen=now() WHERE id=$1`, old.ID); e != nil {
				return e
			}
			if _, e := q.ExecContext(ctx, `INSERT INTO automation_risk_events(id,risk_id,run_id,status,evidence) VALUES($1,$2,$3,'resolved',$4::jsonb)`, uuid.New(), old.ID, run.ID, string(raw(map[string]any{"reason": "The original cause is absent from the current complete authorized snapshot", "as_of": run.Input.AsOf}))); e != nil {
				return e
			}
		}
	case "efficiency":
		improvement := proposal.Results.(ImprovementResult)
		var active bool
		if e := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM automation_improvements WHERE project_id=$1 AND key=$2 AND status IN ('proposed','observing'))`, run.ProjectID, improvement.Key).Scan(&active); e != nil {
			return e
		}
		if active {
			if len(result.CreatedIDs) > 0 {
				return blocked("An existing improvement action is still observing; no duplicate action was committed")
			}
			return nil
		}
		id := uuid.New()
		action := result.CreatedIDs["improvement"]
		status := "proposed"
		if action != uuid.Nil {
			status = "observing"
		}
		_, e := q.ExecContext(ctx, `INSERT INTO automation_improvements(id,workspace_id,project_id,run_id,key,reason,target_metric,status,baseline,window_start,window_end,action_item_id,owner_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9::jsonb,$10,$11,$12,$13)`, id, run.WorkspaceID, run.ProjectID, run.ID, improvement.Key, improvement.Reason, improvement.TargetMetric, status, string(raw(improvement.Baseline)), improvement.WindowStart, improvement.WindowEnd, nullableID(action), scope.Actor.UserID)
		return e
	}
	return nil
}
func saveForecast(ctx context.Context, q database.DBTX, run Run, f Forecast) error {
	_, e := q.ExecContext(ctx, `INSERT INTO automation_forecasts(id,workspace_id,project_id,run_id,as_of,status,p50,p80,sample_count,coverage_start,remaining,assumptions,backtest,algorithm_version) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12::jsonb,$13::jsonb,$14) ON CONFLICT(run_id) DO NOTHING`, uuid.New(), run.WorkspaceID, run.ProjectID, run.ID, f.AsOf, f.Status, f.P50, f.P80, f.SampleCount, f.CoverageStart, f.Remaining, string(raw(f.Assumptions)), string(raw(f.Backtest)), f.AlgorithmVersion)
	return e
}
func notifyRisk(ctx context.Context, q database.DBTX, scope identity.Scope, r Risk) error {
	// Notifications carry a project link and a generic title. Reading the risk
	// still checks current source authorization; private report details do not
	// become permanent copies in every member's notification inbox.
	_, e := q.ExecContext(ctx, `INSERT INTO notifications(id,workspace_id,project_id,user_id,actor_id,entity_type,entity_id,title,body,data) SELECT gen_random_uuid(),$1,$2,pm.user_id,$3,'automation_risk',$4,'Project risk requires attention','Open the current authorized risk report for evidence',$5::jsonb FROM project_members pm JOIN workspace_members wm ON wm.workspace_id=pm.workspace_id AND wm.user_id=pm.user_id WHERE pm.project_id=$2 AND pm.workspace_id=$1 AND pm.is_active AND pm.deleted_at IS NULL AND pm.role>=15 AND wm.is_active AND wm.deleted_at IS NULL AND wm.role>=15`, scope.WorkspaceID, scope.ProjectID, scope.Actor.UserID, r.ID, string(raw(map[string]any{"project_id": scope.ProjectID, "risk_id": r.ID})))
	return e
}
func (s *Service) forecasts(c *gin.Context) {
	scope, e := s.scope(c, identity.Member)
	if e != nil {
		data.Fail(c, e)
		return
	}
	result, e := data.Many(c, s.d.DB.SQL, `SELECT to_jsonb(f)||jsonb_build_object('stale',f.stale OR COALESCE((SELECT revision FROM project_revisions WHERE project_id=f.project_id),0)<>COALESCE((SELECT (input->>'project_revision')::bigint FROM automation_runs WHERE id=f.run_id),0)) FROM automation_forecasts f WHERE workspace_id=$1 AND project_id=$2 ORDER BY as_of DESC LIMIT 100`, scope.WorkspaceID, scope.ProjectID)
	data.Send(c, result, e)
}
func (s *Service) risks(c *gin.Context) {
	scope, e := s.scope(c, identity.Member)
	if e != nil {
		data.Fail(c, e)
		return
	}
	results, e := data.Many(c, s.d.DB.SQL, `SELECT to_jsonb(r)||jsonb_build_object('history',COALESCE((SELECT jsonb_agg(to_jsonb(e) ORDER BY e.created_at) FROM automation_risk_events e WHERE e.risk_id=r.id),'[]'::jsonb)) FROM automation_risks r WHERE workspace_id=$1 AND project_id=$2 ORDER BY last_seen DESC LIMIT 200`, scope.WorkspaceID, scope.ProjectID)
	if e != nil {
		data.Fail(c, e)
		return
	}
	filtered := []json.RawMessage{}
	for _, encoded := range results {
		var risk Risk
		if e = json.Unmarshal(encoded, &risk); e != nil {
			data.Fail(c, e)
			return
		}
		if e = authorizeRiskEvidence(c.Request.Context(), s.d.DB.SQL, scope, risk); e == nil {
			filtered = append(filtered, encoded)
		}
	}
	httpapi.JSON(c, 200, filtered)
}
func (s *Service) improvements(c *gin.Context) {
	scope, e := s.scope(c, identity.Member)
	if e != nil {
		data.Fail(c, e)
		return
	}
	result, e := data.Many(c, s.d.DB.SQL, `SELECT to_jsonb(i) FROM automation_improvements i WHERE workspace_id=$1 AND project_id=$2 ORDER BY created_at DESC LIMIT 100`, scope.WorkspaceID, scope.ProjectID)
	data.Send(c, result, e)
}
func (s *Service) updateRisk(c *gin.Context) {
	scope, e := s.scope(c, identity.Member)
	if e != nil {
		data.Fail(c, e)
		return
	}
	id, e := httpapi.UUIDParam(c, "riskID")
	if e != nil {
		data.Fail(c, e)
		return
	}
	body, e := httpapi.Bind[struct {
		Status  string `json:"status"`
		Version int64  `json:"version"`
	}](c)
	if e != nil {
		data.Fail(c, e)
		return
	}
	if !contains([]string{"open", "acknowledged", "resolved"}, body.Status) || body.Version < 1 {
		data.Fail(c, data.Invalid("A risk update needs its current version and a lifecycle status"))
		return
	}
	var encoded []byte
	e = s.d.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		ctx := c.Request.Context()
		if e := lockProject(ctx, q, scope.WorkspaceID, scope.ProjectID); e != nil {
			return e
		}
		if _, e := currentActorScope(ctx, q, scope.Actor, scope.WorkspaceID, scope.ProjectID, identity.Member); e != nil {
			return e
		}
		var risk Risk
		if e := q.QueryRowContext(ctx, `SELECT to_jsonb(r) FROM automation_risks r WHERE id=$1 AND workspace_id=$2 AND project_id=$3 FOR UPDATE`, id, scope.WorkspaceID, scope.ProjectID).Scan(&encoded); e != nil {
			return e
		}
		if e := json.Unmarshal(encoded, &risk); e != nil {
			return e
		}
		if e := authorizeRiskEvidence(ctx, q, scope, risk); e != nil {
			return e
		}
		if risk.Version != body.Version {
			return blocked("The risk changed; refresh its latest evidence")
		}
		if _, e := q.ExecContext(ctx, `INSERT INTO automation_risk_events(id,risk_id,actor_id,status,evidence) VALUES($1,$2,$3,$4,$5::jsonb)`, uuid.New(), id, scope.Actor.UserID, body.Status, string(raw(map[string]any{"reason": "Explicit member lifecycle change; automated reevaluation reopens unresolved causes"}))); e != nil {
			return e
		}
		return q.QueryRowContext(ctx, `UPDATE automation_risks SET status=$2,version=version+1,last_seen=now() WHERE id=$1 RETURNING to_jsonb(automation_risks)`, id, body.Status).Scan(&encoded)
	})
	data.Send(c, json.RawMessage(encoded), e)
}
func (s *Service) riskAction(c *gin.Context) {
	scope, e := s.scope(c, identity.Admin)
	if e != nil {
		data.Fail(c, e)
		return
	}
	id, e := httpapi.UUIDParam(c, "riskID")
	if e != nil {
		data.Fail(c, e)
		return
	}
	var risk Risk
	var encoded []byte
	e = s.d.DB.SQL.QueryRowContext(c.Request.Context(), `SELECT to_jsonb(r) FROM automation_risks r WHERE id=$1 AND workspace_id=$2 AND project_id=$3`, id, scope.WorkspaceID, scope.ProjectID).Scan(&encoded)
	if e != nil {
		data.Fail(c, e)
		return
	}
	if e = json.Unmarshal(encoded, &risk); e != nil {
		data.Fail(c, e)
		return
	}
	result, e := ApplyExternal(c.Request.Context(), s.d, scope, ExternalInput{IdempotencyKey: "risk-action:" + id.String(), Kind: "risk", Evidence: risk, Title: "Risk response: " + risk.Reason, Description: "<p>Investigate the recorded risk cause, resolve its prerequisites, and reevaluate the current evidence before closing this action.</p>", OnApplied: func(ctx context.Context, q database.DBTX, ids []uuid.UUID) error {
		if len(ids) == 0 {
			return nil
		}
		_, e := q.ExecContext(ctx, `UPDATE automation_risks SET action_item_id=$2,version=version+1 WHERE id=$1 AND action_item_id IS NULL`, id, ids[0])
		return e
	}}, func(ctx context.Context, q database.DBTX) error {
		var current []byte
		if e := q.QueryRowContext(ctx, `SELECT to_jsonb(r) FROM automation_risks r WHERE id=$1 FOR UPDATE`, id).Scan(&current); e != nil {
			return e
		}
		var value Risk
		if e := json.Unmarshal(current, &value); e != nil {
			return e
		}
		if e := authorizeRiskEvidence(ctx, q, scope, value); e != nil {
			return e
		}
		if value.Status == "resolved" {
			return blocked("The risk is already resolved")
		}
		if value.ActionItemID != nil {
			return blocked("The risk already has a linked response action")
		}
		return nil
	})
	if e != nil {
		data.Fail(c, e)
		return
	}
	data.Send(c, result, e)
}
func (s *Service) improvementAction(c *gin.Context) {
	scope, e := s.scope(c, identity.Admin)
	if e != nil {
		data.Fail(c, e)
		return
	}
	id, e := httpapi.UUIDParam(c, "improvementID")
	if e != nil {
		data.Fail(c, e)
		return
	}
	var reason string
	if e = s.d.DB.SQL.QueryRowContext(c.Request.Context(), `SELECT reason FROM automation_improvements WHERE id=$1 AND workspace_id=$2 AND project_id=$3`, id, scope.WorkspaceID, scope.ProjectID).Scan(&reason); e != nil {
		data.Fail(c, e)
		return
	}
	result, e := ApplyExternal(c.Request.Context(), s.d, scope, ExternalInput{IdempotencyKey: "improvement-action:" + id.String(), Kind: "efficiency", Evidence: map[string]any{"improvement_id": id}, Title: "Process improvement action", Description: "<p>" + reason + ". Assign an owner, execute the process change and observe its fixed baseline.</p>", OnApplied: func(ctx context.Context, q database.DBTX, ids []uuid.UUID) error {
		if len(ids) == 0 {
			return nil
		}
		_, e := q.ExecContext(ctx, `UPDATE automation_improvements SET action_item_id=$2,owner_id=$3,status='observing',window_start=now(),window_end=now()+interval '14 days',version=version+1,updated_at=now() WHERE id=$1 AND action_item_id IS NULL`, id, ids[0], scope.Actor.UserID)
		return e
	}}, func(ctx context.Context, q database.DBTX) error {
		var action *uuid.UUID
		if e := q.QueryRowContext(ctx, `SELECT action_item_id FROM automation_improvements WHERE id=$1 FOR UPDATE`, id).Scan(&action); e != nil {
			return e
		}
		if action != nil {
			return blocked("The improvement already has an action")
		}
		return nil
	})
	if e != nil {
		data.Fail(c, e)
		return
	}
	data.Send(c, result, e)
}
func (s *Service) observeImprovement(c *gin.Context) {
	scope, e := s.scope(c, identity.Member)
	if e != nil {
		data.Fail(c, e)
		return
	}
	id, e := httpapi.UUIDParam(c, "improvementID")
	if e != nil {
		data.Fail(c, e)
		return
	}
	var encoded []byte
	e = s.d.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		ctx := c.Request.Context()
		if e := lockProject(ctx, q, scope.WorkspaceID, scope.ProjectID); e != nil {
			return e
		}
		current, e := currentActorScope(ctx, q, scope.Actor, scope.WorkspaceID, scope.ProjectID, identity.Member)
		if e != nil {
			return e
		}
		var baselineRaw []byte
		var from, end time.Time
		var metric string
		var action *uuid.UUID
		var executed *time.Time
		if e = q.QueryRowContext(ctx, `SELECT baseline,window_start,window_end,target_metric,action_item_id,executed_at FROM automation_improvements WHERE id=$1 AND workspace_id=$2 AND project_id=$3 FOR UPDATE`, id, scope.WorkspaceID, scope.ProjectID).Scan(&baselineRaw, &from, &end, &metric, &action, &executed); e != nil {
			return e
		}
		var baseline Metrics
		if e = json.Unmarshal(baselineRaw, &baseline); e != nil {
			return e
		}
		input, e := s.snapshot(ctx, q, current, Request{Kind: "efficiency"})
		if e != nil {
			return e
		}
		if action != nil && executed == nil {
			if e = q.QueryRowContext(ctx, `SELECT min(created_at) FROM work_item_history WHERE project_id=$1 AND work_item_id=$2 AND created_at>=$3 AND after_data->>'state_group' IN ('started','completed')`, scope.ProjectID, *action, from).Scan(&executed); e != nil {
				return e
			}
			if executed != nil {
				from = *executed
				end = from.AddDate(0, 0, 14)
			}
		}
		through := input.AsOf
		if through.After(end) {
			through = end
		}
		measured := Measure(input, from, through)
		status, notes := CompareImprovement(baseline, measured, metric, input.AsOf, end)
		if action == nil || executed == nil {
			status = "insufficient_evidence"
			notes = append(notes, "No linked action was executed; an unexecuted suggestion is not evidence of improvement")
		}
		observed := map[string]any{"metrics": measured, "notes": notes, "observed_at": input.AsOf}
		return q.QueryRowContext(ctx, `UPDATE automation_improvements SET status=$2,observed=$3::jsonb,executed_at=$4,window_start=$5,window_end=$6,version=version+1,updated_at=now() WHERE id=$1 RETURNING to_jsonb(automation_improvements)`, id, status, string(raw(observed)), executed, from, end).Scan(&encoded)
	})
	data.Send(c, json.RawMessage(encoded), e)
}

// Report-derived records obey current source ACLs on every read and action.
func authorizeRiskEvidence(ctx context.Context, q database.DBTX, scope identity.Scope, r Risk) error {
	if r.Type != "quality" {
		return nil
	}
	var evidence struct {
		ReportID uuid.UUID `json:"report_id"`
	}
	if json.Unmarshal(raw(r.Evidence), &evidence) != nil || evidence.ReportID == uuid.Nil {
		return data.Missing()
	}
	return authorizeQualityReport(ctx, q, scope, evidence.ReportID)
}
func authorizeQualityReport(ctx context.Context, q database.DBTX, scope identity.Scope, id uuid.UUID) error {
	var evidence []byte
	e := q.QueryRowContext(ctx, `SELECT r.evidence FROM quality_reports r JOIN github_bindings b ON b.id=r.binding_id JOIN github_installations i ON i.installation_id=b.installation_id WHERE r.id=$1 AND r.workspace_id=$2 AND r.project_id=$3 AND b.active AND i.revoked_at IS NULL FOR SHARE OF r,b,i`, id, scope.WorkspaceID, scope.ProjectID).Scan(&evidence)
	if e != nil {
		return e
	}
	var sources struct {
		Documents []struct {
			ID   uuid.UUID `json:"id"`
			Kind string    `json:"kind"`
		} `json:"documents"`
	}
	if e = json.Unmarshal(evidence, &sources); e != nil {
		return e
	}
	for _, source := range sources.Documents {
		var visible bool
		var locked uuid.UUID
		if source.Kind == "page" {
			if e = q.QueryRowContext(ctx, `SELECT id FROM pages WHERE id=$1 AND workspace_id=$2 AND project_id=$3 FOR SHARE`, source.ID, scope.WorkspaceID, scope.ProjectID).Scan(&locked); e != nil {
				return e
			}
		}
		if source.Kind == "story" {
			if e = q.QueryRowContext(ctx, `SELECT id FROM work_items WHERE id=$1 AND workspace_id=$2 AND project_id=$3 FOR SHARE`, source.ID, scope.WorkspaceID, scope.ProjectID).Scan(&locked); e != nil {
				return e
			}
		}
		if source.Kind == "scenario" {
			if e = q.QueryRowContext(ctx, `SELECT id FROM business_scenarios WHERE id=$1 AND workspace_id=$2 AND project_id=$3 FOR SHARE`, source.ID, scope.WorkspaceID, scope.ProjectID).Scan(&locked); e != nil {
				return e
			}
		}
		switch source.Kind {
		case "page":
			e = q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pages p WHERE p.id=$1 AND p.project_id=$3 AND NOT p.is_private AND p.archived_at IS NULL AND `+data.VisiblePage("p", "$2", "$4")+`)`, source.ID, scope.WorkspaceID, scope.ProjectID, scope.Actor.UserID).Scan(&visible)
		case "story":
			e = q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM work_items w WHERE w.id=$1 AND w.workspace_id=$2 AND w.project_id=$3 AND w.deleted_at IS NULL AND w.archived_at IS NULL AND NOT w.is_draft AND NOT EXISTS(SELECT 1 FROM automation_source_mappings m JOIN automation_runs origin ON origin.id=m.run_id LEFT JOIN pages p ON p.id::text=origin.input->'source'->>'page_id' WHERE m.work_item_id=w.id AND origin.input->'source'->>'page_id' IS NOT NULL AND (p.id IS NULL OR p.is_private OR p.deleted_at IS NOT NULL OR p.archived_at IS NOT NULL OR p.workspace_id<>$2 OR p.project_id IS DISTINCT FROM $3)))`, source.ID, scope.WorkspaceID, scope.ProjectID).Scan(&visible)
		case "scenario":
			e = q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM business_scenarios sc JOIN work_items w ON w.id=sc.story_id WHERE sc.id=$1 AND sc.workspace_id=$2 AND sc.project_id=$3 AND sc.deleted_at IS NULL AND w.deleted_at IS NULL AND NOT EXISTS(SELECT 1 FROM business_scenario_sources ss WHERE ss.scenario_id=sc.id AND NOT EXISTS(SELECT 1 FROM pages p WHERE p.id=ss.page_id AND NOT p.is_private AND p.archived_at IS NULL AND `+data.VisiblePage("p", "$2", "$4")+`)))`, source.ID, scope.WorkspaceID, scope.ProjectID, scope.Actor.UserID).Scan(&visible)
		default:
			return data.Missing()
		}
		if e != nil {
			return e
		}
		if !visible {
			return data.Missing()
		}
	}
	return nil
}
