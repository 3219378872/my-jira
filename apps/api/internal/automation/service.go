package automation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"my-jira/apps/api/internal/platform"
	"my-jira/apps/api/internal/platform/apperror"
	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/httpapi"
	"my-jira/apps/api/internal/platform/identity"
	"my-jira/apps/api/internal/platform/jobs"
	"my-jira/apps/api/internal/requirements"
	"my-jira/apps/api/internal/resources"
	"my-jira/apps/api/internal/support/data"
	"my-jira/apps/api/internal/workitems"
)

type Service struct{ d platform.Dependencies }

func NewService(d platform.Dependencies) *Service { return &Service{d} }
func Register(r *gin.RouterGroup, d platform.Dependencies) {
	s := NewService(d)
	p := r.Group("/workspaces/:workspaceID/projects/:projectID/automation")
	p.GET("/policy", s.getPolicy)
	p.PUT("/policy", s.putPolicy)
	p.GET("/capabilities", s.capabilities)
	p.GET("/runs", s.listRuns)
	p.POST("/runs", s.createRun)
	p.GET("/runs/:runID", s.runDetail)
	p.POST("/runs/:runID/cancel", s.cancelRun)
	p.POST("/runs/:runID/retry", s.retryRun)
	p.POST("/runs/:runID/undo", s.undoRun)
	p.GET("/forecasts", s.forecasts)
	p.GET("/risks", s.risks)
	p.PATCH("/risks/:riskID", s.updateRisk)
	p.POST("/risks/:riskID/action", s.riskAction)
	p.GET("/improvements", s.improvements)
	p.POST("/improvements/:improvementID/observe", s.observeImprovement)
	p.POST("/improvements/:improvementID/action", s.improvementAction)
}
func (s *Service) scope(c *gin.Context, minimum identity.Role) (identity.Scope, error) {
	actor, e := httpapi.Actor(c)
	if e != nil {
		return identity.Scope{}, e
	}
	if actor.TokenWorkspaceID != uuid.Nil {
		return identity.Scope{}, data.Forbidden()
	}
	wid, e := httpapi.UUIDParam(c, "workspaceID")
	if e != nil {
		return identity.Scope{}, e
	}
	pid, e := httpapi.UUIDParam(c, "projectID")
	if e != nil {
		return identity.Scope{}, e
	}
	return s.d.Policy.Project(c.Request.Context(), actor, wid, pid, minimum)
}
func lockProject(ctx context.Context, q database.DBTX, wid, pid uuid.UUID) error {
	if e := workitems.LockProject(ctx, q, pid); e != nil {
		return e
	}
	var id uuid.UUID
	return q.QueryRowContext(ctx, `SELECT id FROM projects WHERE id=$1 AND workspace_id=$2 AND deleted_at IS NULL FOR UPDATE`, pid, wid).Scan(&id)
}
func currentActorScope(ctx context.Context, q database.DBTX, actor identity.Actor, wid, pid uuid.UUID, minimum identity.Role) (identity.Scope, error) {
	if _, e := workitems.CurrentScope(ctx, q, actor, wid, pid, minimum); e != nil {
		return identity.Scope{}, e
	}
	return data.PageProjectScope(ctx, q, actor, wid, pid, minimum)
}

// unavailableAuthority applies only to explicit authorization-query errors.
// SQL, context and validation errors must retain their original causes.
func unavailableAuthority(err error) bool {
	var problem *apperror.Error
	if !errors.As(err, &problem) {
		return false
	}
	return problem.Status == 401 && problem.Code == "unauthenticated" ||
		problem.Status == 403 && problem.Code == "forbidden" ||
		problem.Status == 404 && problem.Code == "not_found"
}
func policyAt(ctx context.Context, q database.DBTX, pid uuid.UUID, lock bool) (Policy, error) {
	p := DefaultPolicy()
	var config []byte
	query := `SELECT config,version,enabled,authorized_by,CASE WHEN budget_month=date_trunc('month',now())::date THEN calls_used ELSE 0 END FROM automation_policies WHERE project_id=$1`
	if lock {
		query += " FOR UPDATE"
	}
	var version int64
	var enabled bool
	var authorized uuid.UUID
	var used int
	e := q.QueryRowContext(ctx, query, pid).Scan(&config, &version, &enabled, &authorized, &used)
	if e == sql.ErrNoRows {
		return p, nil
	}
	if e != nil {
		return p, e
	}
	if e = json.Unmarshal(config, &p); e != nil {
		return p, e
	}
	p.Version = version
	p.Enabled = enabled
	p.AuthorizedBy = authorized
	p.CallsUsed = used
	return p, nil
}
func (s *Service) getPolicy(c *gin.Context) {
	scope, e := s.scope(c, identity.Admin)
	if e != nil {
		data.Fail(c, e)
		return
	}
	p, e := policyAt(c.Request.Context(), s.d.DB.SQL, scope.ProjectID, false)
	data.Send(c, p, e)
}
func (s *Service) capabilities(c *gin.Context) {
	scope, e := s.scope(c, identity.Member)
	if e != nil {
		data.Fail(c, e)
		return
	}
	p, e := policyAt(c.Request.Context(), s.d.DB.SQL, scope.ProjectID, false)
	data.Send(c, gin.H{"enabled": p.Enabled, "allowed_kinds": p.AllowedKinds}, e)
}
func (s *Service) putPolicy(c *gin.Context) {
	scope, e := s.scope(c, identity.Admin)
	if e != nil {
		data.Fail(c, e)
		return
	}
	input, e := httpapi.Bind[Policy](c)
	if e != nil {
		data.Fail(c, e)
		return
	}
	if e = input.Validate(); e != nil {
		data.Fail(c, e)
		return
	}
	ctx := c.Request.Context()
	e = s.d.DB.WithinTx(ctx, func(q database.DBTX) error {
		if e := lockProject(ctx, q, scope.WorkspaceID, scope.ProjectID); e != nil {
			return e
		}
		current, e := currentActorScope(ctx, q, scope.Actor, scope.WorkspaceID, scope.ProjectID, identity.Admin)
		if e != nil {
			return e
		}
		old, e := policyAt(ctx, q, scope.ProjectID, true)
		if e != nil {
			return e
		}
		if old.Version != input.Version {
			return blocked("The policy was changed by another administrator")
		}
		if e = validatePolicyScopes(ctx, q, current, input); e != nil {
			return e
		}
		input.Version++
		input.AuthorizedBy = scope.Actor.UserID
		input.CallsUsed = old.CallsUsed
		_, e = q.ExecContext(ctx, `INSERT INTO automation_policies(project_id,workspace_id,version,enabled,authorized_by,config) VALUES($1,$2,$3,$4,$5,$6::jsonb) ON CONFLICT(project_id) DO UPDATE SET version=EXCLUDED.version,enabled=EXCLUDED.enabled,authorized_by=EXCLUDED.authorized_by,config=EXCLUDED.config,updated_at=now()`, scope.ProjectID, scope.WorkspaceID, input.Version, input.Enabled, input.AuthorizedBy, string(raw(input)))
		if e != nil {
			return e
		}
		_, e = q.ExecContext(ctx, `UPDATE automation_runs SET status='cancelled',cancelled_at=now(),failure='Policy changed or automation disabled',updated_at=now() WHERE project_id=$1 AND status IN ('queued','running')`, scope.ProjectID)
		if e != nil {
			return e
		}
		_, e = requirements.RecordEvent(ctx, q, current, "automation.policy", scope.ProjectID)
		return e
	})
	data.Send(c, input, e)
}
func validatePolicyScopes(ctx context.Context, q database.DBTX, s identity.Scope, p Policy) error {
	for _, id := range p.AllowedItemIDs {
		var found bool
		if e := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM work_items WHERE id=$1 AND workspace_id=$2 AND project_id=$3 AND deleted_at IS NULL)`, id, s.WorkspaceID, s.ProjectID).Scan(&found); e != nil {
			return e
		}
		if !found {
			return data.Invalid("A policy work item is outside this project")
		}
	}
	for _, id := range p.AllowedMemberIDs {
		if _, e := data.PageProjectScope(ctx, q, identity.Actor{UserID: id}, s.WorkspaceID, s.ProjectID, identity.Member); e != nil {
			return data.Invalid("Assignable members need current project membership")
		}
	}
	for _, id := range p.AllowedPageIDs {
		var found bool
		if e := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pages p WHERE p.id=$1 AND p.project_id=$3 AND `+data.VisiblePage("p", "$2", "$4")+`)`, id, s.WorkspaceID, s.ProjectID, s.Actor.UserID).Scan(&found); e != nil {
			return e
		}
		if !found {
			return data.Invalid("A policy page is outside the administrator's current source access")
		}
	}
	return nil
}
func (s *Service) readSource(ctx context.Context, q database.DBTX, scope identity.Scope, source Source, lock bool) (Source, error) {
	if source.PageID == nil {
		source.Private = false
		source.OwnerID = nil
		if strings.HasPrefix(source.Key, "page:") {
			return source, data.Invalid("Uploaded PRDs cannot use the reserved page source namespace")
		}
		if source.Key == "" {
			source.Key = "upload:" + fingerprint(source.Text)
		} else if !strings.HasPrefix(source.Key, "upload:") {
			source.Key = "upload:" + source.Key
		}
		return source, nil
	}
	query := `SELECT p.version,p.is_private,p.owner_id FROM pages p WHERE p.id=$1 AND p.project_id=$3 AND p.archived_at IS NULL AND ` + data.VisiblePage("p", "$2", "$4")
	if lock {
		query += " FOR SHARE OF p"
	}
	var version int64
	var private bool
	var owner uuid.UUID
	if e := q.QueryRowContext(ctx, query, *source.PageID, scope.WorkspaceID, scope.ProjectID, scope.Actor.UserID).Scan(&version, &private, &owner); e != nil {
		return source, e
	}
	if source.Revision > version {
		return source, data.Invalid("The selected page revision does not exist")
	}
	if source.Revision == version {
		if e := q.QueryRowContext(ctx, `SELECT content_html FROM pages WHERE id=$1`, *source.PageID).Scan(&source.Text); e != nil {
			return source, e
		}
	} else {
		if e := q.QueryRowContext(ctx, `SELECT content_html FROM page_versions WHERE page_id=$1 AND workspace_id=$2 AND version=$3 AND deleted_at IS NULL`, *source.PageID, scope.WorkspaceID, source.Revision).Scan(&source.Text); e != nil {
			return source, e
		}
	}
	source.Private = private
	source.OwnerID = &owner
	source.ObservedVersion = version
	source.Key = "page:" + source.PageID.String()
	if len(source.Text) > 50000 {
		return source, data.Invalid("The selected PRD exceeds 50,000 bytes")
	}
	return source, nil
}
func (s *Service) snapshot(ctx context.Context, q database.DBTX, scope identity.Scope, b Request) (Input, error) {
	input := Input{AsOf: time.Now().UTC(), Request: b, Items: []Item{}, Mappings: []Mapping{}, History: []Fact{}, Quality: []json.RawMessage{}, Risks: []Risk{}}
	if _, e := q.ExecContext(ctx, `INSERT INTO project_revisions(project_id,workspace_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, scope.ProjectID, scope.WorkspaceID); e != nil {
		return input, e
	}
	if e := q.QueryRowContext(ctx, `SELECT revision FROM project_revisions WHERE project_id=$1 FOR UPDATE`, scope.ProjectID).Scan(&input.ProjectRevision); e != nil {
		return input, e
	}
	var e error
	input.Source, e = s.readSource(ctx, q, scope, b.Source, true)
	if e != nil {
		return input, e
	}
	if b.Kind != "decompose" {
		input.Source = Source{}
	}
	rows, e := q.QueryContext(ctx, `SELECT (to_jsonb(w)-'description_binary') || jsonb_build_object('state_group',st.group_name,'assignee_ids',COALESCE((SELECT jsonb_agg(a.user_id ORDER BY a.user_id) FROM work_item_assignees a WHERE a.work_item_id=w.id AND a.deleted_at IS NULL),'[]'::jsonb),'dependency_ids',COALESCE((SELECT jsonb_agg(r.source_id ORDER BY r.source_id) FROM work_item_relations r WHERE r.target_id=w.id AND r.relation_type='blocks' AND r.deleted_at IS NULL),'[]'::jsonb)) FROM work_items w JOIN states st ON st.id=w.state_id WHERE w.workspace_id=$1 AND w.project_id=$2 AND w.deleted_at IS NULL AND w.archived_at IS NULL AND NOT w.is_draft ORDER BY w.id`, scope.WorkspaceID, scope.ProjectID)
	if e != nil {
		return input, e
	}
	for rows.Next() {
		var encoded []byte
		if e = rows.Scan(&encoded); e != nil {
			break
		}
		var item Item
		if e = json.Unmarshal(encoded, &item); e != nil {
			break
		}
		item.Raw = encoded
		input.Items = append(input.Items, item)
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		return input, e
	}
	resourcesSnapshot, e := resources.NewService(s.d).Snapshot(ctx, q, scope)
	if e != nil {
		return input, e
	}
	input.Resources = raw(resourcesSnapshot)
	if b.Kind == "decompose" {
		rows, e = q.QueryContext(ctx, `SELECT source_key,entity_key,work_item_id,last_item_version,generated_fields,source_location,run_id,source_revision FROM automation_source_mappings WHERE project_id=$1 AND source_key=$2 ORDER BY entity_key`, scope.ProjectID, input.Source.Key)
		if e != nil {
			return input, e
		}
		for rows.Next() {
			var m Mapping
			var fields, location []byte
			if e = rows.Scan(&m.SourceKey, &m.EntityKey, &m.ItemID, &m.LastVersion, &fields, &location, &m.RunID, &m.SourceRevision); e != nil {
				break
			}
			if e = json.Unmarshal(fields, &m.Fields); e != nil {
				break
			}
			if e = json.Unmarshal(location, &m.Location); e != nil {
				break
			}
			input.Mappings = append(input.Mappings, m)
		}
		if e == nil {
			e = rows.Err()
		}
		rows.Close()
		if e != nil {
			return input, e
		}
	}
	if b.Kind == "forecast" || b.Kind == "risk" || b.Kind == "efficiency" {
		if e = q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM work_item_history h WHERE h.workspace_id=$1 AND h.project_id=$2 AND NOT EXISTS(SELECT 1 FROM work_items w WHERE w.id=h.work_item_id AND w.workspace_id=h.workspace_id AND w.project_id=h.project_id AND w.deleted_at IS NULL))`, scope.WorkspaceID, scope.ProjectID).Scan(&input.HistoryRestricted); e != nil {
			return input, e
		}
		rows, e = q.QueryContext(ctx, `SELECT h.work_item_id,h.created_at,h.action,COALESCE(h.before_data,'null'::jsonb),COALESCE(h.after_data,'null'::jsonb) FROM work_item_history h JOIN work_items w ON w.id=h.work_item_id AND w.workspace_id=h.workspace_id AND w.project_id=h.project_id AND w.deleted_at IS NULL WHERE h.workspace_id=$1 AND h.project_id=$2 AND h.created_at<=$3 ORDER BY h.created_at,h.revision LIMIT 100001`, scope.WorkspaceID, scope.ProjectID, input.AsOf)
		if e != nil {
			return input, e
		}
		for rows.Next() {
			var f Fact
			if e = rows.Scan(&f.ItemID, &f.At, &f.Action, &f.Before, &f.After); e != nil {
				break
			}
			input.History = append(input.History, f)
		}
		if e == nil {
			e = rows.Err()
		}
		rows.Close()
		if e != nil {
			return input, e
		}
		if len(input.History) > 100000 {
			return input, data.Invalid("This project requires a larger historical projection before automation can analyze it")
		}
	}
	if b.Kind == "risk" {
		rows, e = q.QueryContext(ctx, `SELECT to_jsonb(r) FROM automation_risks r WHERE project_id=$1 ORDER BY key`, scope.ProjectID)
		if e != nil {
			return input, e
		}
		for rows.Next() {
			var encoded []byte
			var risk Risk
			if e = rows.Scan(&encoded); e != nil {
				break
			}
			if e = json.Unmarshal(encoded, &risk); e != nil {
				break
			}
			input.Risks = append(input.Risks, risk)
		}
		if e == nil {
			e = rows.Err()
		}
		rows.Close()
		if e != nil {
			return input, e
		}
		visibleRisks := []Risk{}
		for _, risk := range input.Risks {
			if authorizeRiskEvidence(ctx, q, scope, risk) == nil {
				visibleRisks = append(visibleRisks, risk)
			}
		}
		input.Risks = visibleRisks
		// Quality is an optional separately authorized integration. Only active
		// installation evidence may enter a fresh risk snapshot.
		var exists bool
		if e = q.QueryRowContext(ctx, `SELECT to_regclass('quality_reports') IS NOT NULL`).Scan(&exists); e != nil {
			return input, e
		}
		if exists {
			rows, e = q.QueryContext(ctx, `SELECT to_jsonb(r)-'evidence' FROM quality_reports r JOIN github_bindings gb ON gb.latest_report_id=r.id AND gb.id=r.binding_id JOIN github_installations gi ON gi.installation_id=gb.installation_id WHERE r.workspace_id=$1 AND r.project_id=$2 AND gb.active AND gi.revoked_at IS NULL ORDER BY gb.id`, scope.WorkspaceID, scope.ProjectID)
			if e != nil {
				return input, e
			}
			for rows.Next() {
				var encoded []byte
				if e = rows.Scan(&encoded); e != nil {
					break
				}
				input.Quality = append(input.Quality, encoded)
			}
			if e == nil {
				e = rows.Err()
			}
			rows.Close()
			if e != nil {
				return input, e
			}
			visible := []json.RawMessage{}
			for _, encoded := range input.Quality {
				var report struct {
					ID uuid.UUID `json:"id"`
				}
				if json.Unmarshal(encoded, &report) == nil && authorizeQualityReport(ctx, q, scope, report.ID) == nil {
					visible = append(visible, encoded)
				}
			}
			input.Quality = visible
		}
	}
	return input, nil
}
func (s *Service) currentPolicy(ctx context.Context, q database.DBTX, run Run) (Policy, identity.Scope, error) {
	p, e := policyAt(ctx, q, run.ProjectID, true)
	if e != nil {
		return p, identity.Scope{}, e
	}
	if !p.Enabled || p.Version != run.PolicyVersion || !contains(p.AllowedKinds, run.Kind) || run.Round >= p.MaxRounds {
		return p, identity.Scope{}, blocked("Automation is disabled, changed, or outside its allowed cause-chain rounds")
	}
	scope, e := data.PageProjectScope(ctx, q, identity.Actor{UserID: p.AuthorizedBy}, run.WorkspaceID, run.ProjectID, identity.Admin)
	if e != nil {
		return p, scope, blocked("The administrator who authorized this policy no longer has current project authority")
	}
	if _, e = data.PageProjectScope(ctx, q, identity.Actor{UserID: run.RequestedBy}, run.WorkspaceID, run.ProjectID, identity.Member); e != nil {
		return p, scope, blocked("The member who requested this run no longer has current project authority")
	}
	return p, scope, nil
}
func (s *Service) insertRun(ctx context.Context, q database.DBTX, requester identity.Scope, b Request, internal bool) (uuid.UUID, error) {
	p, e := policyAt(ctx, q, requester.ProjectID, true)
	if e != nil {
		return uuid.Nil, e
	}
	if !p.Enabled || !contains(p.AllowedKinds, b.Kind) || b.Round >= p.MaxRounds {
		return uuid.Nil, blocked("Automation kind is disabled or exceeds allowed adjustment rounds")
	}
	authorized, e := data.PageProjectScope(ctx, q, identity.Actor{UserID: p.AuthorizedBy}, requester.WorkspaceID, requester.ProjectID, identity.Admin)
	if e != nil {
		if unavailableAuthority(e) {
			return uuid.Nil, blocked("The policy's authorizing administrator no longer has current access")
		}
		return uuid.Nil, e
	}
	var existing uuid.UUID
	var oldInput []byte
	e = q.QueryRowContext(ctx, `SELECT id,input->'request' FROM automation_runs WHERE project_id=$1 AND idempotency_key=$2`, requester.ProjectID, b.IdempotencyKey).Scan(&existing, &oldInput)
	if e == nil {
		var previous Request
		if json.Unmarshal(oldInput, &previous) != nil || fingerprint(previous) != fingerprint(b) {
			return uuid.Nil, blocked("The idempotency key was already used for another request")
		}
		return existing, nil
	}
	if e != sql.ErrNoRows {
		return uuid.Nil, e
	}
	if !internal && p.MinIntervalSeconds > 0 {
		var recent bool
		if e = q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM automation_runs WHERE project_id=$1 AND created_at>now()-make_interval(secs=>$2))`, requester.ProjectID, p.MinIntervalSeconds).Scan(&recent); e != nil {
			return uuid.Nil, e
		}
		if recent {
			return uuid.Nil, blocked("The project automation frequency limit has not elapsed")
		}
	}
	if b.Kind == "decompose" && b.Source.PageID != nil && len(p.AllowedPageIDs) > 0 && !contains(p.AllowedPageIDs, *b.Source.PageID) {
		return uuid.Nil, blocked("The selected page is outside the automation source scope")
	}
	if b.Kind == "decompose" && b.Source.PageID == nil && len(p.AllowedPageIDs) > 0 {
		return uuid.Nil, blocked("This policy permits selected pages only; uploaded PRDs are outside its source scope")
	}
	input, e := s.snapshot(ctx, q, requester, b)
	if e != nil {
		return uuid.Nil, e
	}
	if e = authorizeInputEvidence(ctx, q, requester, b.Kind, input); e != nil {
		return uuid.Nil, e
	}
	// Volatile time and request idempotency keys are not source identity.
	hash := fingerprint(map[string]any{"kind": b.Kind, "revision": input.ProjectRevision, "source": input.Source, "resources": input.Resources, "task_ids": sortedIDs(b.TaskIDs), "start_date": b.StartDate, "end_date": b.EndDate, "history": input.History, "quality": input.Quality})
	e = q.QueryRowContext(ctx, `SELECT id FROM automation_runs WHERE project_id=$1 AND kind=$2 AND input_fingerprint=$3 AND policy_version=$4`, requester.ProjectID, b.Kind, hash, p.Version).Scan(&existing)
	if e == nil {
		return existing, nil
	}
	if e != sql.ErrNoRows {
		return uuid.Nil, e
	}
	id := uuid.New()
	cause := b.Cause
	if cause == "" {
		cause = "manual"
	}
	_, e = q.ExecContext(ctx, `INSERT INTO automation_runs(id,workspace_id,project_id,requested_by,authorized_by,kind,status,idempotency_key,input_fingerprint,policy_version,input,algorithm_version,cause,round) VALUES($1,$2,$3,$4,$5,$6,'queued',$7,$8,$9,$10::jsonb,$11,$12,$13)`, id, requester.WorkspaceID, requester.ProjectID, requester.Actor.UserID, authorized.Actor.UserID, b.Kind, b.IdempotencyKey, hash, p.Version, string(raw(input)), AlgorithmVersion, cause, b.Round)
	if e != nil {
		return uuid.Nil, e
	}
	publisher := s.d.Jobs
	if publisher == nil {
		publisher = jobs.Outbox{}
	}
	e = publisher.Publish(ctx, q, TaskType, map[string]any{"run_id": id}, "automation:"+id.String()+":0")
	return id, e
}
func (s *Service) createRun(c *gin.Context) {
	scope, e := s.scope(c, identity.Member)
	if e != nil {
		data.Fail(c, e)
		return
	}
	b, e := httpapi.Bind[Request](c)
	if e != nil {
		data.Fail(c, e)
		return
	}
	if e = validateRequest(b); e != nil {
		data.Fail(c, e)
		return
	}
	if b.Round != 0 || strings.HasPrefix(b.Cause, "automation:") {
		data.Fail(c, data.Invalid("Manual runs start at round zero"))
		return
	}
	var id uuid.UUID
	e = s.d.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if e := lockProject(c.Request.Context(), q, scope.WorkspaceID, scope.ProjectID); e != nil {
			return e
		}
		current, e := currentActorScope(c.Request.Context(), q, scope.Actor, scope.WorkspaceID, scope.ProjectID, identity.Member)
		if e != nil {
			return e
		}
		id, e = s.insertRun(c.Request.Context(), q, current, b, false)
		return e
	})
	if e != nil {
		data.Fail(c, e)
		return
	}
	s.sendRun(c, scope, id, 202)
}
func visibleRunSQL() string {
	return `(r.input->'source'->>'page_id' IS NULL OR EXISTS(SELECT 1 FROM pages p WHERE p.id=(r.input->'source'->>'page_id')::uuid AND ` + data.VisiblePage("p", "$1", "$3") + `))`
}
func (s *Service) listRuns(c *gin.Context) {
	scope, e := s.scope(c, identity.Member)
	if e != nil {
		data.Fail(c, e)
		return
	}
	result, e := data.Many(c, s.d.DB.SQL, `SELECT to_jsonb(r) FROM automation_runs r WHERE r.workspace_id=$1 AND r.project_id=$2 AND `+visibleRunSQL()+` ORDER BY created_at DESC LIMIT 100`, scope.WorkspaceID, scope.ProjectID, scope.Actor.UserID)
	if e != nil {
		data.Fail(c, e)
		return
	}
	visible := []json.RawMessage{}
	for _, encoded := range result {
		var run Run
		if json.Unmarshal(encoded, &run) != nil {
			continue
		}
		if authorizeRunEvidence(c.Request.Context(), s.d.DB.SQL, scope, run) != nil {
			continue
		}
		var object map[string]json.RawMessage
		_ = json.Unmarshal(encoded, &object)
		for _, key := range []string{"input", "output", "before_state", "after_state"} {
			delete(object, key)
		}
		visible = append(visible, raw(object))
	}
	data.Send(c, visible, nil)
}
func (s *Service) sendRun(c *gin.Context, scope identity.Scope, id uuid.UUID, status int) {
	encoded, e := data.One(c, s.d.DB.SQL, `SELECT to_jsonb(r) FROM automation_runs r WHERE r.workspace_id=$1 AND r.project_id=$2 AND r.id=$4 AND `+visibleRunSQL(), scope.WorkspaceID, scope.ProjectID, scope.Actor.UserID, id)
	if e != nil {
		data.Fail(c, e)
		return
	}
	var run Run
	if e = json.Unmarshal(encoded, &run); e != nil {
		data.Fail(c, e)
		return
	}
	if e = authorizeRunEvidence(c.Request.Context(), s.d.DB.SQL, scope, run); e != nil {
		data.Fail(c, e)
		return
	}
	httpapi.JSON(c, status, encoded)
}
func (s *Service) runDetail(c *gin.Context) {
	scope, e := s.scope(c, identity.Member)
	if e != nil {
		data.Fail(c, e)
		return
	}
	id, e := httpapi.UUIDParam(c, "runID")
	if e != nil {
		data.Fail(c, e)
		return
	}
	s.sendRun(c, scope, id, 200)
}
func loadRun(ctx context.Context, q database.DBTX, id uuid.UUID, lock bool) (Run, error) {
	var r Run
	query := `SELECT to_jsonb(r) FROM automation_runs r WHERE id=$1`
	if lock {
		query += " FOR UPDATE"
	}
	var encoded []byte
	e := q.QueryRowContext(ctx, query, id).Scan(&encoded)
	if e == nil {
		e = json.Unmarshal(encoded, &r)
	}
	return r, e
}
func (s *Service) runMutation(c *gin.Context, operation string) {
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
	e = s.d.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		ctx := c.Request.Context()
		if e := lockProject(ctx, q, scope.WorkspaceID, scope.ProjectID); e != nil {
			return e
		}
		if _, e := currentActorScope(ctx, q, scope.Actor, scope.WorkspaceID, scope.ProjectID, identity.Admin); e != nil {
			return e
		}
		run, e := loadRun(ctx, q, id, true)
		if e != nil {
			return e
		}
		if run.ProjectID != scope.ProjectID || run.WorkspaceID != scope.WorkspaceID {
			return data.Missing()
		}
		if run.Input.Source.PageID != nil {
			if _, e := s.readSource(ctx, q, scope, run.Input.Source, true); e != nil {
				return e
			}
		}
		switch operation {
		case "cancel":
			if run.Status != "queued" && run.Status != "running" {
				return blocked("Only queued or running work can be cancelled")
			}
			_, e = q.ExecContext(ctx, `UPDATE automation_runs SET status='cancelled',cancelled_at=now(),failure='Cancelled by a project administrator',updated_at=now() WHERE id=$1`, id)
		case "retry":
			if run.Status != "failed" && run.Status != "blocked" && run.Status != "cancelled" {
				return blocked("Only failed, blocked or cancelled runs can be retried")
			}
			if _, _, e = s.currentPolicy(ctx, q, run); e != nil {
				return e
			}
			if e = s.checkInput(ctx, q, run, scope); e != nil {
				return e
			}
			if run.Attempts >= 10 {
				return blocked("This run exhausted its retry limit; submit a fresh current input")
			}
			_, e = q.ExecContext(ctx, `UPDATE automation_runs SET status='queued',failure='',cancelled_at=NULL,lease_until=NULL,updated_at=now() WHERE id=$1`, id)
			if e == nil {
				publisher := s.d.Jobs
				if publisher == nil {
					publisher = jobs.Outbox{}
				}
				e = publisher.Publish(ctx, q, TaskType, map[string]any{"run_id": id}, fmt.Sprintf("automation:%s:retry:%d", id, run.Attempts))
			}
		}
		return e
	})
	if e != nil {
		data.Fail(c, e)
		return
	}
	s.sendRun(c, scope, id, 200)
}
func (s *Service) cancelRun(c *gin.Context) { s.runMutation(c, "cancel") }
func (s *Service) retryRun(c *gin.Context)  { s.runMutation(c, "retry") }
