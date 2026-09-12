package automation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"my-jira/apps/api/internal/integrations"
	"my-jira/apps/api/internal/platform"
	"my-jira/apps/api/internal/platform/apperror"
	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/identity"
	"my-jira/apps/api/internal/platform/jobs"
	"my-jira/apps/api/internal/platform/serviceconfig"
	"my-jira/apps/api/internal/requirements"
	"my-jira/apps/api/internal/resources"
	"my-jira/apps/api/internal/support/data"
	"my-jira/apps/api/internal/workitems"
)

func RegisterTasks(mux *asynq.ServeMux, d platform.Dependencies) {
	service := NewService(d)
	mux.HandleFunc(TaskType, func(ctx context.Context, task *asynq.Task) error {
		var envelope jobs.Envelope
		if e := json.Unmarshal(task.Payload(), &envelope); e != nil {
			return fmt.Errorf("invalid automation envelope: %w", asynq.SkipRetry)
		}
		var payload struct {
			RunID uuid.UUID `json:"run_id"`
		}
		if e := json.Unmarshal(envelope.Payload, &payload); e != nil || payload.RunID == uuid.Nil {
			return fmt.Errorf("invalid automation task: %w", asynq.SkipRetry)
		}
		return service.Run(ctx, payload.RunID)
	})
}

func (s *Service) checkInput(ctx context.Context, q database.DBTX, run Run, scope identity.Scope) error {
	requester, e := data.PageProjectScope(ctx, q, identity.Actor{UserID: run.RequestedBy}, run.WorkspaceID, run.ProjectID, identity.Member)
	if e != nil {
		return blocked("The requester's current project access was revoked")
	}
	if e := authorizeRunEvidence(ctx, q, requester, run); e != nil {
		return blocked("A captured work item or evidence source is no longer accessible in this project")
	}
	var revision int64
	if e := q.QueryRowContext(ctx, `SELECT revision FROM project_revisions WHERE project_id=$1 FOR UPDATE`, run.ProjectID).Scan(&revision); e != nil {
		return e
	}
	if revision != run.Input.ProjectRevision {
		return blocked("The input project revision changed; submit or automatically refresh a stable snapshot")
	}
	if run.Input.Source.PageID != nil {
		requester := scope
		requester.Actor = identity.Actor{UserID: run.RequestedBy}
		current, e := s.readSource(ctx, q, requester, run.Input.Source, true)
		if e != nil {
			return blocked("The source is no longer visible or its selected revision is unavailable")
		}
		if current.ObservedVersion != run.Input.Source.ObservedVersion || fingerprint(current) != fingerprint(run.Input.Source) {
			return blocked("The source revision or access scope changed")
		}
	}
	current, e := resources.NewService(s.d).Snapshot(ctx, q, scope)
	if e != nil {
		return e
	}
	if fingerprint(current) != fingerprint(json.RawMessage(run.Input.Resources)) {
		return blocked("Members, skills, calendars, capacity or task input changed")
	}
	return nil
}

// Run may be called by the durable Asynq consumer or an isolated integration
// test. Provider work occurs after the claim transaction has committed.
func (s *Service) Run(ctx context.Context, id uuid.UUID) error {
	initial, e := loadRun(ctx, s.d.DB.SQL, id, false)
	if e != nil {
		return e
	}
	var run Run
	var policy Policy
	claimed := false
	e = s.d.DB.WithinTx(ctx, func(q database.DBTX) error {
		if e := lockProject(ctx, q, initial.WorkspaceID, initial.ProjectID); e != nil {
			return e
		}
		var e error
		run, e = loadRun(ctx, q, id, true)
		if e != nil {
			return e
		}
		if run.Status != "queued" && run.Status != "running" && run.Status != "failed" {
			return nil
		}
		var liveLease bool
		if e = q.QueryRowContext(ctx, `SELECT COALESCE(lease_until>now(),false) FROM automation_runs WHERE id=$1`, id).Scan(&liveLease); e != nil {
			return e
		}
		if run.Status == "running" && liveLease {
			return errLeaseActive
		}
		if run.Attempts >= 10 {
			return blocked("The durable run exhausted ten attempts")
		}
		var scope identity.Scope
		policy, scope, e = s.currentPolicy(ctx, q, run)
		if e != nil {
			return e
		}
		if e = s.checkInput(ctx, q, run, scope); e != nil {
			return e
		}
		_, e = q.ExecContext(ctx, `UPDATE automation_runs SET status='running',attempts=attempts+1,lease_until=now()+interval '2 minutes',failure='',updated_at=now() WHERE id=$1`, id)
		claimed = e == nil
		return e
	})
	if errors.Is(e, errLeaseActive) {
		return e
	}
	if e != nil {
		return s.recordFailure(ctx, initial, e)
	}
	if !claimed {
		return nil
	}
	proposal, e := s.analyze(ctx, run, policy)
	if e != nil {
		return s.recordFailure(ctx, run, e)
	}
	// Validated output survives a stale-input or revoked-policy rejection at
	// commit, so retries and audit retain exactly what was proposed.
	if _, e = s.d.DB.SQL.ExecContext(ctx, `UPDATE automation_runs SET output=$2::jsonb,updated_at=now() WHERE id=$1 AND status='running'`, run.ID, string(raw(proposal))); e != nil {
		return e
	}
	e = s.d.DB.WithinTx(ctx, func(q database.DBTX) error {
		if e := lockProject(ctx, q, run.WorkspaceID, run.ProjectID); e != nil {
			return e
		}
		current, e := loadRun(ctx, q, id, true)
		if e != nil {
			return e
		}
		if current.Status != "running" {
			return nil
		}
		p, scope, e := s.currentPolicy(ctx, q, current)
		if e != nil {
			return e
		}
		if e = s.checkInput(ctx, q, current, scope); e != nil {
			return e
		}
		return s.commitProposal(ctx, q, current, p, scope, proposal)
	})
	if e != nil {
		return s.recordFailure(ctx, run, e)
	}
	return nil
}

var errLeaseActive = errors.New("automation run lease is active; retry after the worker lease expires")

func (s *Service) reserveModelCall(ctx context.Context, run Run) error {
	return s.d.DB.WithinTx(ctx, func(q database.DBTX) error {
		if e := lockProject(ctx, q, run.WorkspaceID, run.ProjectID); e != nil {
			return e
		}
		current, e := loadRun(ctx, q, run.ID, true)
		if e != nil {
			return e
		}
		if current.Status != "running" {
			return blocked("This run was cancelled before its provider call")
		}
		p, scope, e := s.currentPolicy(ctx, q, current)
		if e != nil {
			return e
		}
		if e = s.checkInput(ctx, q, current, scope); e != nil {
			return e
		}
		if p.CallsUsed >= p.BudgetCalls {
			return blocked("The monthly project model-call budget has been exhausted")
		}
		_, e = q.ExecContext(ctx, `UPDATE automation_policies SET calls_used=CASE WHEN budget_month=date_trunc('month',now())::date THEN calls_used+1 ELSE 1 END,budget_month=date_trunc('month',now())::date,updated_at=now() WHERE project_id=$1`, run.ProjectID)
		return e
	})
}
func (s *Service) analyze(ctx context.Context, run Run, p Policy) (Proposal, error) {
	switch run.Kind {
	case "decompose":
		if run.Input.Source.Private {
			return Proposal{Summary: "The selected source is private", Commands: []ProposalCommand{}, Findings: []string{"Work items are project-visible. A private PRD cannot create or update broader project work without an explicit source visibility change."}, Blocked: true}, nil
		}
		config, e := serviceconfig.Load(ctx, s.d.DB.SQL, "ai")
		if e != nil {
			return Proposal{}, e
		}
		if config.String("api_key") == "" || config.String("model") == "" {
			return Proposal{}, blocked("The instance administrator has not configured a model provider")
		}
		if e = s.reserveModelCall(ctx, run); e != nil {
			return Proposal{}, e
		}
		content := "PRD (one-based lines):\n" + run.Input.Source.Text + "\n\nExisting source mappings:\n" + string(raw(run.Input.Mappings)) + "\n\nExisting project requirements:\n" + string(raw(run.Input.Items))
		text, e := integrations.GenerateText(ctx, config, decompositionInstruction, content)
		if e != nil {
			return Proposal{}, e
		}
		parsed, e := ParseDecomposition(text, run.Input.Source.Text)
		if e != nil {
			return Proposal{}, e
		}
		proposal, e := DecompositionProposal(parsed, run.Input)
		if e != nil {
			return proposal, e
		}
		proposal.Results = map[string]any{"decomposition": parsed, "model": config.String("model"), "provider": config.String("provider"), "source_revision": run.Input.Source.Revision}
		return proposal, nil
	case "schedule":
		var snapshot resources.Snapshot
		if e := json.Unmarshal(run.Input.Resources, &snapshot); e != nil {
			return Proposal{}, e
		}
		request := scheduleRequest(run.Input, p)
		result, e := resources.Schedule(snapshot, request)
		if e != nil {
			return Proposal{}, e
		}
		proposal := Proposal{Summary: "Deterministic schedule respecting dependencies, resource calendars, skills and protected commitments", Commands: []ProposalCommand{}, Findings: []string{}, Results: result, Blocked: !result.Feasible}
		if !result.Feasible {
			proposal.Findings = append(proposal.Findings, "No complete feasible plan was found; none of the proposed assignments were applied")
			return proposal, nil
		}
		for _, a := range result.Assignments {
			proposal.Commands = append(proposal.Commands, ProposalCommand{Operation: "update", ID: a.ID, Version: a.Version, Fields: map[string]json.RawMessage{"start_date": raw(a.StartDate), "target_date": raw(a.TargetDate), "assignee_ids": raw(a.AssigneeIDs), "allocation_weights": raw(a.AllocationWeights)}})
		}
		return proposal, nil
	case "forecast":
		return Proposal{Summary: "Historical delivery simulation with cold-start limits and time-split evaluation", Commands: []ProposalCommand{}, Findings: []string{}, Results: ForecastDelivery(run.Input)}, nil
	case "risk":
		return AnalyzeRisks(run.Input, p)
	case "efficiency":
		proposal := AnalyzeEfficiency(run.Input, p)
		result := proposal.Results.(ImprovementResult)
		var active bool
		if e := s.d.DB.SQL.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM automation_improvements WHERE project_id=$1 AND key=$2 AND status IN ('proposed','observing'))`, run.ProjectID, result.Key).Scan(&active); e != nil {
			return Proposal{}, e
		}
		if active {
			proposal.Commands = []ProposalCommand{}
			proposal.Findings = append(proposal.Findings, "An existing action is still being observed; its baseline will be retained")
		}
		return proposal, nil
	}
	return Proposal{}, data.Invalid("Unknown automation algorithm")
}
func scheduleRequest(input Input, p Policy) resources.ScheduleRequest {
	start, end := input.Request.StartDate, input.Request.EndDate
	if start == "" {
		start = input.AsOf.Format(time.DateOnly)
	}
	if end == "" {
		end = input.AsOf.AddDate(0, 0, 180).Format(time.DateOnly)
	}
	ids := input.Request.TaskIDs
	if len(ids) == 0 && len(p.AllowedItemIDs) > 0 {
		ids = p.AllowedItemIDs
	}
	return resources.ScheduleRequest{StartDate: start, EndDate: end, TaskIDs: ids, MemberIDs: p.AllowedMemberIDs, HardDeadline: p.HardDeadline}
}

func (s *Service) recordFailure(ctx context.Context, run Run, reason error) error {
	status := "failed"
	var conflict *data.Error
	if errors.As(reason, &conflict) {
		status = "blocked"
	}
	var applicationError *apperror.Error
	if errors.As(reason, &applicationError) && applicationError.Status < 500 {
		status = "blocked"
	}
	// Error strings from this module/provider adapter contain neither source
	// text nor provider bodies. SQL/internal failures get a generic public label.
	message := reason.Error()
	if len(message) > 400 {
		message = "The automation operation failed; review its input and retry"
	}
	if strings.Contains(message, "SQLSTATE") || strings.Contains(message, "password") || strings.Contains(message, "Bearer") {
		message = "The automation transaction failed; no partial changes were committed"
	}
	_, e := s.d.DB.SQL.ExecContext(ctx, `UPDATE automation_runs SET status=$2,failure=$3,lease_until=NULL,updated_at=now() WHERE id=$1 AND status IN ('queued','running','failed')`, run.ID, status, message)
	if e != nil {
		return e
	}
	if status == "blocked" {
		if strings.Contains(message, "input project revision changed") || strings.Contains(message, "source revision or access scope changed") || strings.Contains(message, "Members, skills, calendars") {
			return s.refreshStale(ctx, run)
		}
		return nil
	}
	return errors.New("automation run failed; durable failure details were saved")
}

func (s *Service) refreshStale(ctx context.Context, old Run) error {
	return s.d.DB.WithinTx(ctx, func(q database.DBTX) error {
		if e := lockProject(ctx, q, old.WorkspaceID, old.ProjectID); e != nil {
			return e
		}
		p, e := policyAt(ctx, q, old.ProjectID, true)
		if e != nil {
			return e
		}
		if !p.Enabled || p.Version != old.PolicyVersion || old.Round+1 >= p.MaxRounds {
			return nil
		}
		scope, e := data.PageProjectScope(ctx, q, identity.Actor{UserID: old.RequestedBy}, old.WorkspaceID, old.ProjectID, identity.Member)
		if e != nil {
			return nil
		}
		request := old.Input.Request
		request.IdempotencyKey = "refresh:" + old.ID.String()
		request.Cause = "automation:" + old.ID.String()
		request.Round = old.Round + 1
		if request.Source.PageID != nil {
			if e = q.QueryRowContext(ctx, `SELECT version FROM pages p WHERE p.id=$1 AND p.workspace_id=$2 AND p.project_id=$3 AND p.deleted_at IS NULL AND NOT p.is_private`, *request.Source.PageID, old.WorkspaceID, old.ProjectID).Scan(&request.Source.Revision); e != nil {
				return nil
			}
		}
		_, e = s.insertRun(ctx, q, scope, request, true)
		return e
	})
}

type SavedItem struct {
	ID          uuid.UUID                  `json:"item_id"`
	Version     int64                      `json:"version"`
	Fingerprint string                     `json:"fingerprint"`
	Fields      map[string]json.RawMessage `json:"fields"`
	Created     bool                       `json:"created"`
}

func validateProposal(p Policy, input Input, proposal Proposal) error {
	if len(proposal.Commands) > p.MaxChanges {
		return blocked("The proposal exceeds the policy's maximum changes per batch")
	}
	items := map[uuid.UUID]Item{}
	for _, item := range input.Items {
		items[item.ID] = item
	}
	for _, command := range proposal.Commands {
		entity := "work_item"
		if existing, ok := items[command.ID]; ok && existing.RequirementType != "" {
			entity = existing.RequirementType
		}
		if command.Operation == "update" && len(p.AllowedEntities) > 0 && !contains(p.AllowedEntities, entity) {
			return blocked("The existing entity type %s is outside the automatic policy", entity)
		}
		if value, ok := command.Fields["requirement_type"]; ok {
			if string(value) == "null" {
				entity = "work_item"
			} else {
				_ = json.Unmarshal(value, &entity)
			}
		}
		if len(p.AllowedEntities) > 0 && !contains(p.AllowedEntities, entity) {
			return blocked("Entity type %s is outside the automatic policy", entity)
		}
		if !contains(p.AllowedOperations, command.Operation) {
			return blocked("Operation %s is outside the automatic policy", command.Operation)
		}
		if command.Operation == "create" && len(p.AllowedItemIDs) > 0 {
			return blocked("A policy restricted to existing work-item IDs cannot create new objects")
		}
		if command.Operation == "create" {
			var parent string
			if encoded, ok := command.Fields["parent_id"]; ok && json.Unmarshal(encoded, &parent) == nil && !strings.HasPrefix(parent, "$") {
				if id, e := uuid.Parse(parent); e == nil {
					if parentItem, exists := items[id]; !exists || parentItem.Protected() {
						return blocked("Automatic work cannot extend a protected or inaccessible parent")
					}
				}
			}
		}
		if command.Operation == "update" {
			item, ok := items[command.ID]
			if !ok || item.Protected() {
				return blocked("A proposal targets missing, started, completed, cancelled or locked work")
			}
			if len(p.AllowedItemIDs) > 0 && !contains(p.AllowedItemIDs, command.ID) {
				return blocked("The proposed work item is outside the policy scope")
			}
			if item.Version != command.Version {
				return blocked("The proposal does not match the captured item version")
			}
			if item.RequirementType == "story" {
				for _, key := range []string{"start_date", "target_date", "cycle_id"} {
					if _, changed := command.Fields[key]; changed {
						return blocked("Automatic actions cannot move a Story's delivery commitment")
					}
				}
			}
		}
		for field, value := range command.Fields {
			if !contains(p.AllowedFields, field) {
				return blocked("Field %s is outside the automatic policy", field)
			}
			if field == "target_date" && p.HardDeadline != "" {
				var date string
				if json.Unmarshal(value, &date) != nil || date > p.HardDeadline {
					return blocked("The proposal exceeds the hard project deadline")
				}
			}
			if field == "assignee_ids" && len(p.AllowedMemberIDs) > 0 {
				var ids []uuid.UUID
				if json.Unmarshal(value, &ids) != nil {
					return data.Invalid("Invalid generated assignees")
				}
				for _, id := range ids {
					if !contains(p.AllowedMemberIDs, id) {
						return blocked("A proposed assignee is outside the policy's allowed members")
					}
				}
			}
		}
	}
	return nil
}

func (s *Service) commitProposal(ctx context.Context, q database.DBTX, run Run, p Policy, scope identity.Scope, proposal Proposal) error {
	status := "completed"
	if proposal.Partial {
		status = "partial"
	}
	if proposal.Blocked {
		status = "blocked"
		proposal.Commands = []ProposalCommand{}
	}
	if e := validateProposal(p, run.Input, proposal); e != nil {
		return e
	}
	before := []SavedItem{}
	after := []SavedItem{}
	result := requirements.ChangeResult{CreatedIDs: map[string]uuid.UUID{}}
	commands := workitems.NewCommands(s.d)
	for _, cmd := range proposal.Commands {
		if cmd.Operation == "update" {
			item, e := commands.Load(ctx, q, scope, cmd.ID, true)
			if e != nil {
				return e
			}
			fields := map[string]json.RawMessage{}
			for key := range cmd.Fields {
				value, ok := item[key]
				if !ok {
					var old map[string]json.RawMessage
					for _, inputItem := range run.Input.Items {
						if inputItem.ID == cmd.ID {
							_ = json.Unmarshal(inputItem.Raw, &old)
							break
						}
					}
					fields[key] = old[key]
				} else {
					fields[key] = raw(value)
				}
			}
			for key, value := range fields {
				if len(value) == 0 {
					fields[key] = raw(nil)
				}
			}
			before = append(before, SavedItem{ID: cmd.ID, Version: cmd.Version, Fields: fields})
		}
	}
	if len(proposal.Commands) > 0 {
		batch := requirements.ChangeSet{IdempotencyKey: "automation:" + run.ID.String(), ExpectedRevision: &run.Input.ProjectRevision, Reason: "automation:" + run.ID.String() + ":" + run.Cause, Commands: []requirements.Command{}}
		for _, cmd := range proposal.Commands {
			batch.Commands = append(batch.Commands, requirements.Command{Operation: cmd.Operation, ID: cmd.ID, ClientID: cmd.ClientID, Version: cmd.Version, Fields: cmd.Fields})
		}
		var e error
		result, e = requirements.NewService(s.d).ApplyWithinTx(ctx, q, scope.Actor, scope.WorkspaceID, scope.ProjectID, batch)
		if e != nil {
			return e
		}
		if !proposal.Partial {
			status = "applied"
		}
		for _, cmd := range proposal.Commands {
			id := cmd.ID
			created := cmd.Operation == "create"
			if created {
				id = result.CreatedIDs[cmd.ClientID]
			}
			item, e := commands.Load(ctx, q, scope, id, false)
			if e != nil {
				return e
			}
			var parsed Item
			_ = json.Unmarshal(raw(item), &parsed)
			fp, e := workitems.MutationFingerprint(ctx, q, scope.WorkspaceID, scope.ProjectID, id)
			if e != nil {
				return e
			}
			fields := map[string]json.RawMessage{}
			for key := range cmd.Fields {
				fields[key] = raw(item[key])
			}
			after = append(after, SavedItem{ID: id, Version: parsed.Version, Fingerprint: fp, Fields: fields, Created: created})
		}
		// Fingerprints are taken after every dependency/parent write in the batch;
		// the last relation can legitimately change an earlier created item's fact.
		for i := range after {
			fp, e := workitems.MutationFingerprint(ctx, q, scope.WorkspaceID, scope.ProjectID, after[i].ID)
			if e != nil {
				return e
			}
			after[i].Fingerprint = fp
		}
	}
	if run.Kind == "schedule" && len(proposal.Commands) > 0 {
		snapshot, e := resources.NewService(s.d).Snapshot(ctx, q, scope)
		if e != nil {
			return e
		}
		request := scheduleRequest(run.Input, p)
		projection, e := resources.Project(snapshot, request.StartDate, request.EndDate)
		if e != nil {
			return e
		}
		for _, c := range projection.Conflicts {
			if contains([]string{"over_capacity", "dependency", "commitment", "skill_mismatch", "member_removed", "no_working_days", "date_order", "invalid_allocation"}, c.Type) {
				return blocked("The final schedule still violates %s; the complete batch was rolled back", c.Type)
			}
		}
	}
	if run.Kind == "decompose" && len(proposal.Commands) > 0 {
		if e := s.saveMappings(ctx, q, run, proposal, result, after); e != nil {
			return e
		}
	}
	if !proposal.Blocked && len(run.Input.External) == 0 {
		if e := s.persistAnalysis(ctx, q, run, scope, proposal, result); e != nil {
			return e
		}
	}
	_, e := q.ExecContext(ctx, `UPDATE automation_runs SET status=$2,output=$3::jsonb,batch_id=$4,before_state=$5::jsonb,after_state=$6::jsonb,failure=$7,lease_until=NULL,updated_at=now() WHERE id=$1`, run.ID, status, string(raw(proposal)), nullableID(result.BatchID), string(raw(before)), string(raw(after)), func() string {
		if proposal.Blocked {
			return strings.Join(proposal.Findings, "; ")
		}
		return ""
	}())
	if e != nil {
		return e
	}
	if len(proposal.Commands) > 0 {
		_, e = q.ExecContext(ctx, `UPDATE automation_forecasts SET stale=true WHERE project_id=$1`, run.ProjectID)
		if e != nil {
			return e
		}
	}
	// Automatic follow-up uses a fresh committed-in-this-transaction snapshot,
	// carries the origin chain, and is bounded by the same maximum rounds.
	next := ""
	if run.Kind == "decompose" && len(proposal.Commands) > 0 {
		next = "schedule"
	}
	if run.Kind == "schedule" {
		next = "risk"
	}
	if run.Kind == "quality" && len(proposal.Commands) > 0 {
		next = "risk"
	}
	if run.Kind == "risk" && !proposal.Blocked {
		next = "efficiency"
	}
	if next != "" && run.Round+1 < p.MaxRounds && contains(p.AllowedKinds, next) {
		b := Request{Kind: next, IdempotencyKey: "followup:" + run.ID.String() + ":" + next, Cause: "automation:" + run.ID.String(), Round: run.Round + 1}
		_, e = s.insertRun(ctx, q, scope, b, true)
		if e != nil {
			return e
		}
	}
	return nil
}
func nullableID(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}
func (s *Service) saveMappings(ctx context.Context, q database.DBTX, run Run, proposal Proposal, result requirements.ChangeResult, after []SavedItem) error {
	versions := map[uuid.UUID]int64{}
	for _, item := range after {
		versions[item.ID] = item.Version
	}
	for i, entity := range proposal.MappingEntities {
		if i >= len(proposal.Commands) {
			break
		}
		cmd := proposal.Commands[i]
		id := cmd.ID
		if cmd.Operation == "create" {
			id = result.CreatedIDs[cmd.ClientID]
		}
		_, e := q.ExecContext(ctx, `INSERT INTO automation_source_mappings(project_id,source_key,entity_key,work_item_id,run_id,source_revision,source_location,generated_fields,last_item_version) VALUES($1,$2,$3,$4,$5,$6,$7::jsonb,$8::jsonb,$9) ON CONFLICT(project_id,source_key,entity_key) DO UPDATE SET run_id=EXCLUDED.run_id,source_revision=EXCLUDED.source_revision,source_location=EXCLUDED.source_location,generated_fields=EXCLUDED.generated_fields,last_item_version=EXCLUDED.last_item_version,updated_at=now()`, run.ProjectID, run.Input.Source.Key, entity.Key, id, run.ID, fmt.Sprintf("%d:%s", run.Input.Source.Revision, fingerprint(run.Input.Source.Text)), string(raw(entity.Source)), string(raw(cmd.Fields)), versions[id])
		if e != nil {
			return e
		}
	}
	return nil
}
