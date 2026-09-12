package quality

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"my-jira/apps/api/internal/automation"
	"my-jira/apps/api/internal/platform"
	"my-jira/apps/api/internal/platform/apperror"
	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/identity"
	"my-jira/apps/api/internal/platform/jobs"
	"my-jira/apps/api/internal/support/data"
)

func RegisterTasks(mux *asynq.ServeMux, d platform.Dependencies) {
	RegisterTasksWithConfig(mux, d, EnvironmentConfig())
}
func RegisterTasksWithConfig(mux *asynq.ServeMux, d platform.Dependencies, c Config) {
	h := newHandler(d, c)
	mux.HandleFunc("quality.sync", h.syncTask)
	mux.HandleFunc("quality.repair", h.repairTask)
	mux.HandleFunc("quality.reconcile", h.reconcileTask)
}

func decodeTask(t *asynq.Task, target any) error {
	var env jobs.Envelope
	if json.Unmarshal(t.Payload(), &env) != nil || json.Unmarshal(env.Payload, target) != nil {
		return fmt.Errorf("invalid quality task: %w", asynq.SkipRetry)
	}
	return nil
}

func (h *handler) syncTask(ctx context.Context, t *asynq.Task) error {
	var input struct {
		RequestID uuid.UUID `json:"request_id"`
	}
	if e := decodeTask(t, &input); e != nil {
		return e
	}
	if input.RequestID == uuid.Nil {
		return asynq.SkipRetry
	}
	return h.processSync(ctx, input.RequestID)
}

func (h *handler) processSync(ctx context.Context, requestID uuid.UUID) error {
	var b binding
	var sourceRaw []byte
	var status string
	var retryAt sql.NullTime
	e := h.d.DB.SQL.QueryRowContext(ctx, `SELECT b.id,b.workspace_id,b.project_id,b.created_by,b.installation_id,b.repository_id,b.repository,r.source,r.status,b.next_retry_at FROM github_sync_requests r JOIN github_bindings b ON b.id=r.binding_id WHERE r.id=$1`, requestID).Scan(&b.ID, &b.WorkspaceID, &b.ProjectID, &b.CreatedBy, &b.InstallationID, &b.RepositoryID, &b.Repository, &sourceRaw, &status, &retryAt)
	if errors.Is(e, sql.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	if status == "completed" || status == "revoked" {
		return nil
	}
	if retryAt.Valid && retryAt.Time.After(time.Now()) {
		return &rateLimitError{retryAt.Time}
	}
	var source Source
	if json.Unmarshal(sourceRaw, &source) != nil {
		return asynq.SkipRetry
	}
	s := identity.Scope{WorkspaceID: b.WorkspaceID, ProjectID: b.ProjectID, Actor: identity.Actor{UserID: b.CreatedBy}}
	authorize := func() error {
		if _, e := data.PageProjectScope(ctx, h.d.DB.SQL, s.Actor, s.WorkspaceID, s.ProjectID, identity.Admin); e != nil {
			return errRevoked
		}
		return authorizeBinding(ctx, h.d.DB.SQL, b.ID, b.WorkspaceID, b.ProjectID, false)
	}
	e = h.d.DB.WithinTx(ctx, func(q database.DBTX) error {
		if e := h.current(ctx, q, s, identity.Admin); e != nil {
			return errRevoked
		}
		if e := authorizeBinding(ctx, q, b.ID, b.WorkspaceID, b.ProjectID, true); e != nil {
			return e
		}
		if _, e := q.ExecContext(ctx, `UPDATE github_sync_requests SET status='running',last_error='',updated_at=now() WHERE id=$1`, requestID); e != nil {
			return e
		}
		_, e := q.ExecContext(ctx, `UPDATE github_bindings SET sync_status='syncing',last_error='',updated_at=now() WHERE id=$1`, b.ID)
		return e
	})
	if e != nil {
		return h.syncFailure(ctx, requestID, b, e)
	}
	evidence, e := h.p.readEvidence(ctx, b.InstallationID, b.RepositoryID, b.Repository, source, authorize)
	if e != nil {
		return h.syncFailure(ctx, requestID, b, e)
	}
	if e = authorize(); e != nil {
		return h.syncFailure(ctx, requestID, b, e)
	}
	var reportID uuid.UUID
	e = h.d.DB.WithinTx(ctx, func(q database.DBTX) error {
		if e := h.current(ctx, q, s, identity.Admin); e != nil {
			return errRevoked
		}
		if e := authorizeBinding(ctx, q, b.ID, b.WorkspaceID, b.ProjectID, true); e != nil {
			return e
		}
		docs, e := loadDocuments(ctx, q, b, evidence.Message)
		if e != nil {
			return e
		}
		dimensions, findings := analyze(evidence, docs)
		revision := sourceRevision(evidence, docs)
		dimensionRaw, _ := json.Marshal(dimensions)
		findingsRaw, _ := json.Marshal(findings)
		allEvidence, _ := json.Marshal(map[string]any{"repository": evidence, "documents": docs, "algorithm": "quality-v1", "execution": "existing_artifacts_only", "external_provider_verified": false})
		reportID = uuid.New()
		e = q.QueryRowContext(ctx, `INSERT INTO quality_reports(id,workspace_id,project_id,binding_id,repository,commit_sha,pull_request,run_id,run_attempt,source_revision,source_at,dimensions,findings,evidence) VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,0),NULLIF($8,0),NULLIF($9,0),$10,$11,$12::jsonb,$13::jsonb,$14::jsonb) ON CONFLICT(binding_id,source_revision) DO UPDATE SET source_revision=EXCLUDED.source_revision RETURNING id`, reportID, b.WorkspaceID, b.ProjectID, b.ID, b.Repository, evidence.CommitSHA, evidence.PullRequest, evidence.RunID, evidence.RunAttempt, revision, evidence.SourceAt, string(dimensionRaw), string(findingsRaw), string(allEvidence)).Scan(&reportID)
		if e != nil {
			return e
		}
		if _, e = q.ExecContext(ctx, `UPDATE github_sync_requests SET status='completed',report_id=$2,last_error='',updated_at=now() WHERE id=$1`, requestID, reportID); e != nil {
			return e
		}
		// Historical events create their own immutable report but cannot replace a
		// newer source as the current binding report.
		if _, e = q.ExecContext(ctx, `UPDATE github_bindings SET sync_status='synced',last_synced_at=now(),last_error='',next_retry_at=NULL,latest_source_at=CASE WHEN latest_source_at IS NULL OR latest_source_at<=$2 THEN $2 ELSE latest_source_at END,latest_report_id=CASE WHEN latest_source_at IS NULL OR latest_source_at<=$2 THEN $3 ELSE latest_report_id END,updated_at=now() WHERE id=$1`, b.ID, evidence.SourceAt, reportID); e != nil {
			return e
		}
		return h.d.Jobs.Publish(ctx, q, "quality.repair", map[string]any{"report_id": reportID}, "quality.repair:"+reportID.String())
	})
	if e != nil {
		return h.syncFailure(ctx, requestID, b, e)
	}
	return nil
}

func (h *handler) syncFailure(ctx context.Context, requestID uuid.UUID, b binding, cause error) error {
	status, requestStatus, message := "failed", "failed", "GitHub evidence synchronization failed"
	var retry any
	var limit *rateLimitError
	if errors.As(cause, &limit) {
		status = "rate_limited"
		requestStatus = "queued"
		retry = limit.RetryAt
		message = "GitHub rate limit reached; waiting before retry"
	}
	if errors.Is(cause, errRevoked) {
		status = "revoked"
		requestStatus = "revoked"
		message = "Repository or project authorization was revoked"
	}
	if errors.Is(cause, errSource) {
		message = "The requested source revision is missing or does not match GitHub evidence"
	}
	e := h.d.DB.WithinTx(ctx, func(q database.DBTX) error {
		if e := lockProject(ctx, q, b.ProjectID); e != nil {
			return e
		}
		if _, e := q.ExecContext(ctx, `UPDATE github_sync_requests SET status=$2,last_error=$3,updated_at=now() WHERE id=$1 AND status<>'completed'`, requestID, requestStatus, message); e != nil {
			return e
		}
		_, e := q.ExecContext(ctx, `UPDATE github_bindings SET sync_status=$2,last_error=$3,next_retry_at=$4,active=CASE WHEN $2='revoked' THEN false ELSE active END,updated_at=now() WHERE id=$1 AND active`, b.ID, status, message, retry)
		return e
	})
	if e != nil {
		return e
	}
	if errors.Is(cause, errRevoked) || errors.Is(cause, errSource) {
		return nil
	}
	return cause
}

// Only project-visible sources enter shared reports. Private PRDs and generated
// stories derived from them cannot leak through repository quality evidence.
func loadDocuments(ctx context.Context, q database.DBTX, b binding, message string) ([]documentSource, error) {
	rows, e := q.QueryContext(ctx, `SELECT w.id,w.version,w.name,p.identifier||'-'||w.sequence_id,COALESCE(to_jsonb(w)->>'story_role',''),COALESCE(to_jsonb(w)->>'story_goal',''),COALESCE(to_jsonb(w)->>'story_benefit',''),COALESCE(to_jsonb(w)->'acceptance_criteria','[]'::jsonb) FROM work_items w JOIN projects p ON p.id=w.project_id WHERE w.workspace_id=$1 AND w.project_id=$2 AND w.deleted_at IS NULL AND w.archived_at IS NULL AND NOT w.is_draft AND to_jsonb(w)->>'requirement_type'='story' AND NOT EXISTS(SELECT 1 FROM automation_source_mappings m JOIN automation_runs r ON r.id=m.run_id LEFT JOIN pages sp ON sp.id::text=r.input->'source'->>'page_id' WHERE m.work_item_id=w.id AND (r.input->'source'->>'private'='true' OR (r.input->'source'->>'page_id' IS NOT NULL AND (sp.id IS NULL OR sp.deleted_at IS NOT NULL OR sp.is_private OR sp.workspace_id<>$1 OR sp.project_id IS DISTINCT FROM $2)))) ORDER BY w.id LIMIT 500`, b.WorkspaceID, b.ProjectID)
	if e != nil {
		return nil, e
	}
	docs := []documentSource{}
	for rows.Next() {
		var d documentSource
		var criteria []byte
		d.Kind = "story"
		if e = rows.Scan(&d.ID, &d.Version, &d.Name, &d.Identifier, &d.Role, &d.Goal, &d.Benefit, &criteria); e != nil {
			rows.Close()
			return nil, e
		}
		if e = json.Unmarshal(criteria, &d.Criteria); e != nil {
			rows.Close()
			return nil, e
		}
		d.Linked = strings.Contains(message, d.ID) || containsIdentifier(message, d.Identifier)
		raw, _ := json.Marshal(d)
		sum := sha256.Sum256(raw)
		d.ContentHash = hex.EncodeToString(sum[:])
		docs = append(docs, d)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	rows, e = q.QueryContext(ctx, `SELECT id,version,name,content_html FROM pages WHERE workspace_id=$1 AND project_id=$2 AND deleted_at IS NULL AND archived_at IS NULL AND NOT is_private ORDER BY id LIMIT 100`, b.WorkspaceID, b.ProjectID)
	if e != nil {
		return nil, e
	}
	for rows.Next() {
		var d documentSource
		d.Kind = "page"
		if e = rows.Scan(&d.ID, &d.Version, &d.Name, &d.Content); e != nil {
			rows.Close()
			return nil, e
		}
		sum := sha256.Sum256([]byte(d.Content))
		d.ContentHash = hex.EncodeToString(sum[:])
		d.Linked = strings.Contains(message, d.ID)
		if len(d.Content) > 50000 {
			d.Content = d.Content[:50000]
		}
		docs = append(docs, d)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	// Scenarios with retained private, removed, or moved source pages are excluded.
	rows, e = q.QueryContext(ctx, `SELECT sc.id,sc.version,COALESCE(sc.body->>'name',sc.body->>'title','Scenario'),sc.body::text FROM business_scenarios sc JOIN work_items w ON w.id=sc.story_id AND w.workspace_id=sc.workspace_id AND w.project_id=sc.project_id WHERE sc.workspace_id=$1 AND sc.project_id=$2 AND sc.deleted_at IS NULL AND w.deleted_at IS NULL AND w.archived_at IS NULL AND NOT w.is_draft AND to_jsonb(w)->>'requirement_type'='story' AND NOT EXISTS(SELECT 1 FROM business_scenario_sources ss LEFT JOIN pages p ON p.id=ss.page_id WHERE ss.scenario_id=sc.id AND (p.id IS NULL OR p.workspace_id<>$1 OR p.deleted_at IS NOT NULL OR p.archived_at IS NOT NULL OR p.is_private OR p.project_id IS DISTINCT FROM $2)) ORDER BY sc.id LIMIT 100`, b.WorkspaceID, b.ProjectID)
	if e != nil {
		return nil, e
	}
	for rows.Next() {
		var d documentSource
		d.Kind = "scenario"
		if e = rows.Scan(&d.ID, &d.Version, &d.Name, &d.Content); e != nil {
			rows.Close()
			return nil, e
		}
		sum := sha256.Sum256([]byte(d.Content))
		d.ContentHash = hex.EncodeToString(sum[:])
		d.Linked = strings.Contains(message, d.ID)
		docs = append(docs, d)
	}
	e = rows.Err()
	rows.Close()
	return docs, e
}

func (h *handler) repairTask(ctx context.Context, t *asynq.Task) error {
	var input struct {
		ReportID uuid.UUID `json:"report_id"`
	}
	if e := decodeTask(t, &input); e != nil {
		return e
	}
	if input.ReportID == uuid.Nil {
		return asynq.SkipRetry
	}
	return h.applyRepairs(ctx, input.ReportID)
}

func (h *handler) applyRepairs(ctx context.Context, reportID uuid.UUID) error {
	var b binding
	var findingsRaw, evidenceRaw []byte
	var revision string
	e := h.d.DB.SQL.QueryRowContext(ctx, `SELECT b.id,b.workspace_id,b.project_id,b.created_by,b.installation_id,b.repository_id,b.repository,r.findings,r.evidence,r.source_revision FROM quality_reports r JOIN github_bindings b ON b.id=r.binding_id WHERE r.id=$1`, reportID).Scan(&b.ID, &b.WorkspaceID, &b.ProjectID, &b.CreatedBy, &b.InstallationID, &b.RepositoryID, &b.Repository, &findingsRaw, &evidenceRaw, &revision)
	if errors.Is(e, sql.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	var findings []Finding
	if e = json.Unmarshal(findingsRaw, &findings); e != nil {
		return e
	}
	s := identity.Scope{WorkspaceID: b.WorkspaceID, ProjectID: b.ProjectID, Actor: identity.Actor{UserID: b.CreatedBy}}
	for _, f := range findings {
		if !f.Actionable {
			continue
		}
		reused, e := h.reuseRepair(ctx, s, b, reportID, f.ID)
		if e != nil {
			return e
		}
		if reused {
			continue
		}
		evidence := map[string]any{"report_id": reportID, "source_revision": revision, "finding": f, "repository": b.Repository}
		key := "quality:" + reportID.String() + ":" + f.ID
		description := "<p>" + html.EscapeString(f.Description) + "</p><p>Quality report " + reportID.String() + "; immutable source " + revision + ".</p>"
		if f.Path != "" {
			description += "<p>" + html.EscapeString(f.Path) + fmt.Sprintf(":%d</p>", f.Line)
		}
		title := []rune(f.Title)
		if len(title) > 255 {
			title = title[:255]
		}
		result, applyErr := automation.ApplyExternal(ctx, h.d, s, automation.ExternalInput{IdempotencyKey: key, Kind: "quality", Evidence: evidence, Title: string(title), Description: description, OnApplied: func(ctx context.Context, q database.DBTX, items []uuid.UUID) error {
			encoded, _ := json.Marshal(items)
			_, e := q.ExecContext(ctx, `INSERT INTO quality_repair_actions(report_id,finding_id,run_id,item_ids,status) SELECT $1,$2,id,$3::jsonb,status FROM automation_runs WHERE project_id=$4 AND idempotency_key=$5 ON CONFLICT(report_id,finding_id) DO UPDATE SET run_id=EXCLUDED.run_id,item_ids=EXCLUDED.item_ids,status=EXCLUDED.status,updated_at=now()`, reportID, f.ID, string(encoded), b.ProjectID, key)
			return e
		}}, func(ctx context.Context, q database.DBTX) error {
			if e := authorizeBinding(ctx, q, b.ID, b.WorkspaceID, b.ProjectID, true); e != nil {
				return e
			}
			var current bool
			if e := q.QueryRowContext(ctx, `SELECT COALESCE(latest_report_id=$2,false) FROM github_bindings WHERE id=$1`, b.ID, reportID).Scan(&current); e != nil {
				return e
			}
			if !current {
				return apperror.Conflict("A newer repository report superseded this evidence; historical findings cannot create automatic repair tasks")
			}
			return validateReportSources(ctx, q, reportID, b.WorkspaceID, b.ProjectID, true)
		})
		status := result.Status
		if status == "" {
			status = "blocked"
		}
		if applyErr != nil {
			var api *apperror.Error
			if errors.Is(applyErr, errRevoked) {
				status = "revoked"
			} else if errors.As(applyErr, &api) && api.Code == "automation_frequency" {
				return errors.New("Quality repair actions are waiting for the project automation frequency window")
			} else if !errors.As(applyErr, &api) {
				return applyErr
			}
		}
		items, _ := json.Marshal(result.ItemIDs)
		if result.ItemIDs == nil {
			items = []byte("[]")
		}
		var runID any
		if result.RunID != uuid.Nil {
			runID = result.RunID
		}
		if _, e = h.d.DB.SQL.ExecContext(ctx, `INSERT INTO quality_repair_actions(report_id,finding_id,run_id,item_ids,status) VALUES($1,$2,$3,$4::jsonb,$5) ON CONFLICT(report_id,finding_id) DO UPDATE SET run_id=COALESCE(EXCLUDED.run_id,quality_repair_actions.run_id),item_ids=CASE WHEN EXCLUDED.run_id IS NOT NULL THEN EXCLUDED.item_ids ELSE quality_repair_actions.item_ids END,status=CASE WHEN quality_repair_actions.status='applied' THEN 'applied' ELSE EXCLUDED.status END,updated_at=now()`, reportID, f.ID, runID, string(items), status); e != nil {
			return e
		}
		if status == "revoked" {
			return nil
		}
	}
	return nil
}

// An unchanged finding at the same immutable commit may reappear when a PRD
// revision produces a new report. Reuse its existing live action rather than
// creating another work item just because unrelated document evidence changed.
func (h *handler) reuseRepair(ctx context.Context, s identity.Scope, b binding, reportID uuid.UUID, findingID string) (bool, error) {
	var runID uuid.UUID
	var items []byte
	e := h.d.DB.SQL.QueryRowContext(ctx, `SELECT a.run_id,a.item_ids FROM quality_repair_actions a JOIN quality_reports old ON old.id=a.report_id JOIN quality_reports current ON current.id=$1 AND current.binding_id=old.binding_id AND current.commit_sha=old.commit_sha JOIN automation_runs ar ON ar.id=a.run_id AND ar.status IN ('applied','partial') WHERE old.binding_id=$2 AND a.finding_id=$3 AND a.status='applied' AND a.report_id<>$1 ORDER BY old.created_at DESC LIMIT 1`, reportID, b.ID, findingID).Scan(&runID, &items)
	if errors.Is(e, sql.ErrNoRows) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	reused := false
	e = h.d.DB.WithinTx(ctx, func(q database.DBTX) error {
		if e := h.current(ctx, q, s, identity.Admin); e != nil {
			return nil
		}
		if e := authorizeBinding(ctx, q, b.ID, b.WorkspaceID, b.ProjectID, true); e != nil {
			return nil
		}
		var current bool
		if e := q.QueryRowContext(ctx, `SELECT COALESCE(latest_report_id=$2,false) FROM github_bindings WHERE id=$1`, b.ID, reportID).Scan(&current); e != nil {
			return e
		}
		if !current {
			return nil
		}
		if e := validateReportSources(ctx, q, reportID, b.WorkspaceID, b.ProjectID, true); e != nil {
			return nil
		}
		var active bool
		if e := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM automation_runs WHERE id=$1 AND status IN ('applied','partial'))`, runID).Scan(&active); e != nil {
			return e
		}
		if !active {
			return nil
		}
		_, e := q.ExecContext(ctx, `INSERT INTO quality_repair_actions(report_id,finding_id,run_id,item_ids,status) VALUES($1,$2,$3,$4::jsonb,'applied') ON CONFLICT(report_id,finding_id) DO NOTHING`, reportID, findingID, runID, string(items))
		reused = e == nil
		return e
	})
	return reused, e
}

func validateReportSources(ctx context.Context, q database.DBTX, reportID, wid, pid uuid.UUID, versions bool) error {
	var raw []byte
	if e := q.QueryRowContext(ctx, `SELECT r.evidence->'documents' FROM quality_reports r JOIN github_bindings b ON b.id=r.binding_id JOIN github_installations i USING(installation_id) WHERE r.id=$1 AND r.workspace_id=$2 AND r.project_id=$3 AND b.active AND i.revoked_at IS NULL`, reportID, wid, pid).Scan(&raw); e != nil {
		if errors.Is(e, sql.ErrNoRows) {
			return apperror.NotFound()
		}
		return e
	}
	var docs []documentSource
	if e := json.Unmarshal(raw, &docs); e != nil {
		return e
	}
	for _, d := range docs {
		var version int64
		var e error
		switch d.Kind {
		case "page":
			e = q.QueryRowContext(ctx, `SELECT version FROM pages WHERE id=$1 AND workspace_id=$2 AND project_id=$3 AND deleted_at IS NULL AND archived_at IS NULL AND NOT is_private FOR SHARE`, d.ID, wid, pid).Scan(&version)
		case "story":
			e = q.QueryRowContext(ctx, `SELECT w.version FROM work_items w WHERE w.id=$1 AND w.workspace_id=$2 AND w.project_id=$3 AND w.deleted_at IS NULL AND w.archived_at IS NULL AND NOT w.is_draft AND w.requirement_type='story' AND NOT EXISTS(SELECT 1 FROM automation_source_mappings m JOIN automation_runs r ON r.id=m.run_id LEFT JOIN pages p ON p.id::text=r.input->'source'->>'page_id' WHERE m.work_item_id=w.id AND (r.input->'source'->>'private'='true' OR (r.input->'source'->>'page_id' IS NOT NULL AND (p.id IS NULL OR p.deleted_at IS NOT NULL OR p.archived_at IS NOT NULL OR p.is_private OR p.workspace_id<>$2 OR p.project_id IS DISTINCT FROM $3)))) FOR SHARE OF w`, d.ID, wid, pid).Scan(&version)
		case "scenario":
			version, e = lockScenarioSource(ctx, q, d.ID, wid, pid)
		default:
			return apperror.NotFound()
		}
		if e != nil {
			return apperror.NotFound()
		}
		if versions && version != d.Version {
			return apperror.Conflict("Quality source documents changed after the report; synchronize before applying repairs")
		}
	}
	return nil
}

// Match scenario command ordering: Story, retained source Pages, then Scenario.
// A page/Story update also updates scenario review state, so reversing those
// locks would deadlock a report reader with a normal source edit.
func lockScenarioSource(ctx context.Context, q database.DBTX, id string, wid, pid uuid.UUID) (int64, error) {
	var storyID uuid.UUID
	if e := q.QueryRowContext(ctx, `SELECT story_id FROM business_scenarios WHERE id=$1 AND workspace_id=$2 AND project_id=$3 AND deleted_at IS NULL`, id, wid, pid).Scan(&storyID); e != nil {
		return 0, e
	}
	var lockedStory uuid.UUID
	if e := q.QueryRowContext(ctx, `SELECT id FROM work_items WHERE id=$1 AND workspace_id=$2 AND project_id=$3 AND requirement_type='story' AND deleted_at IS NULL AND archived_at IS NULL AND NOT is_draft FOR SHARE`, storyID, wid, pid).Scan(&lockedStory); e != nil {
		return 0, e
	}
	rows, e := q.QueryContext(ctx, `SELECT page_id::text FROM business_scenario_sources WHERE scenario_id=$1 ORDER BY page_id`, id)
	if e != nil {
		return 0, e
	}
	pages := []string{}
	for rows.Next() {
		var pageID string
		if e = rows.Scan(&pageID); e != nil {
			rows.Close()
			return 0, e
		}
		pages = append(pages, pageID)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return 0, e
	}
	for _, pageID := range pages {
		var page uuid.UUID
		if e = q.QueryRowContext(ctx, `SELECT id FROM pages WHERE id=$1 AND workspace_id=$2 AND project_id=$3 AND deleted_at IS NULL AND archived_at IS NULL AND NOT is_private FOR SHARE`, pageID, wid, pid).Scan(&page); e != nil {
			return 0, e
		}
	}
	pageJSON, _ := json.Marshal(pages)
	var version int64
	e = q.QueryRowContext(ctx, `SELECT version FROM business_scenarios WHERE id=$1 AND workspace_id=$2 AND project_id=$3 AND story_id=$4 AND deleted_at IS NULL AND NOT EXISTS(SELECT 1 FROM business_scenario_sources ss WHERE ss.scenario_id=$1 AND ss.page_id::text NOT IN (SELECT jsonb_array_elements_text($5::jsonb))) FOR SHARE`, id, wid, pid, storyID, string(pageJSON)).Scan(&version)
	return version, e
}

// Reconcile is also exported for operational one-shot compensation. The worker
// registers quality.reconcile for a scheduler; its payload needs no browser auth.
func Reconcile(ctx context.Context, d platform.Dependencies) error {
	return newHandler(d, EnvironmentConfig()).reconcileTask(ctx, nil)
}
func (h *handler) reconcileTask(ctx context.Context, _ *asynq.Task) error {
	if !h.p.config.configured() {
		return nil
	}
	rows, e := h.d.DB.SQL.QueryContext(ctx, `SELECT b.id,b.workspace_id,b.project_id,b.created_by,b.installation_id,b.repository_id,b.repository,b.last_synced_at FROM github_bindings b JOIN github_installations i USING(installation_id) WHERE b.active AND i.revoked_at IS NULL AND (b.next_retry_at IS NULL OR b.next_retry_at<=now()) ORDER BY b.id LIMIT 1000`)
	if e != nil {
		return e
	}
	type entry struct {
		b     binding
		since sql.NullTime
	}
	entries := []entry{}
	for rows.Next() {
		var x entry
		if e = rows.Scan(&x.b.ID, &x.b.WorkspaceID, &x.b.ProjectID, &x.b.CreatedBy, &x.b.InstallationID, &x.b.RepositoryID, &x.b.Repository, &x.since); e != nil {
			rows.Close()
			return e
		}
		entries = append(entries, x)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, entry := range entries {
		b := entry.b
		s := identity.Scope{WorkspaceID: b.WorkspaceID, ProjectID: b.ProjectID, Actor: identity.Actor{UserID: b.CreatedBy}}
		if _, e = data.PageProjectScope(ctx, h.d.DB.SQL, s.Actor, s.WorkspaceID, s.ProjectID, identity.Admin); e != nil {
			continue
		}
		if e = authorizeBinding(ctx, h.d.DB.SQL, b.ID, b.WorkspaceID, b.ProjectID, false); e != nil {
			continue
		}
		token, e := h.p.installationToken(ctx, b.InstallationID, b.RepositoryID)
		if e != nil {
			_ = h.syncFailure(ctx, uuid.Nil, b, e)
			continue
		}
		sources := []Source{}
		since := time.Now().Add(-30 * 24 * time.Hour)
		if entry.since.Valid {
			since = entry.since.Time.Add(-24 * time.Hour)
		}
		for page := 1; page <= 10; page++ {
			if e = authorizeBinding(ctx, h.d.DB.SQL, b.ID, b.WorkspaceID, b.ProjectID, false); e != nil {
				break
			}
			var result struct {
				WorkflowRuns []workflowRun `json:"workflow_runs"`
			}
			headers, err := h.p.api(ctx, "GET", fmt.Sprintf("/repos/%s/actions/runs?status=completed&per_page=100&page=%d", b.Repository, page), token, nil, &result)
			if err != nil {
				e = err
				break
			}
			for _, run := range result.WorkflowRuns {
				if run.UpdatedAt.Before(since) {
					continue
				}
				if shaPattern.MatchString(run.HeadSHA) {
					sources = append(sources, Source{CommitSHA: run.HeadSHA, RunID: run.ID, RunAttempt: run.RunAttempt, EventTime: run.UpdatedAt})
				}
			}
			if !hasNext(headers) {
				break
			}
		}
		if e == nil {
			more, err := h.reconcileChanges(ctx, b, token, since)
			if err != nil {
				e = err
			} else {
				sources = append(sources, more...)
			}
		}
		if e != nil {
			_ = h.syncFailure(ctx, uuid.Nil, b, e)
			continue
		}
		if len(sources) == 0 {
			sources = append(sources, Source{})
		}
		var projectRevision int64
		if e = h.d.DB.SQL.QueryRowContext(ctx, `SELECT COALESCE((SELECT revision FROM project_revisions WHERE project_id=$1),0)`, b.ProjectID).Scan(&projectRevision); e != nil {
			return e
		}
		e = h.d.DB.WithinTx(ctx, func(q database.DBTX) error {
			if e := h.current(ctx, q, s, identity.Admin); e != nil {
				return nil
			}
			if e := authorizeBinding(ctx, q, b.ID, b.WorkspaceID, b.ProjectID, true); e != nil {
				return nil
			}
			for _, source := range sources {
				// A document/Story revision needs fresh analysis even when repository
				// evidence is unchanged; the immutable report key still deduplicates
				// unrelated changes and its repair actions remain idempotent.
				key := fmt.Sprintf("quality:reconcile:%s:%s:%d:%d:%d:%s:%d", b.ID, source.CommitSHA, source.PullRequest, source.RunID, source.RunAttempt, source.EventTime.Format(time.RFC3339Nano), projectRevision)
				if source.RunID == 0 && source.CommitSHA == "" && source.PullRequest == 0 {
					key += time.Now().UTC().Format("2006-01-02-15")
				}
				id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(key))
				if e := h.enqueue(ctx, q, id, b.WorkspaceID, b.ProjectID, b.ID, source); e != nil {
					return e
				}
			}
			return nil
		})
		if e != nil {
			return e
		}
	}
	return nil
}

func (h *handler) reconcileChanges(ctx context.Context, b binding, token string, since time.Time) ([]Source, error) {
	sources := []Source{}
	for page := 1; page <= 10; page++ {
		if e := authorizeBinding(ctx, h.d.DB.SQL, b.ID, b.WorkspaceID, b.ProjectID, false); e != nil {
			return nil, e
		}
		var pulls []struct {
			Number    int64     `json:"number"`
			UpdatedAt time.Time `json:"updated_at"`
			Head      struct {
				SHA string `json:"sha"`
			} `json:"head"`
		}
		headers, e := h.p.api(ctx, "GET", fmt.Sprintf("/repos/%s/pulls?state=all&sort=updated&direction=desc&per_page=100&page=%d", b.Repository, page), token, nil, &pulls)
		if e != nil {
			return nil, e
		}
		past := false
		for _, pull := range pulls {
			if pull.UpdatedAt.Before(since) {
				past = true
				continue
			}
			if shaPattern.MatchString(pull.Head.SHA) {
				sources = append(sources, Source{PullRequest: pull.Number, CommitSHA: pull.Head.SHA, EventTime: pull.UpdatedAt})
			}
		}
		if past || !hasNext(headers) {
			break
		}
	}
	for page := 1; page <= 10; page++ {
		if e := authorizeBinding(ctx, h.d.DB.SQL, b.ID, b.WorkspaceID, b.ProjectID, false); e != nil {
			return nil, e
		}
		var commits []struct {
			SHA    string `json:"sha"`
			Commit struct {
				Committer struct {
					Date time.Time `json:"date"`
				} `json:"committer"`
			} `json:"commit"`
		}
		headers, e := h.p.api(ctx, "GET", fmt.Sprintf("/repos/%s/commits?since=%s&per_page=100&page=%d", b.Repository, url.QueryEscape(since.UTC().Format(time.RFC3339)), page), token, nil, &commits)
		if e != nil {
			return nil, e
		}
		for _, commit := range commits {
			if shaPattern.MatchString(commit.SHA) {
				sources = append(sources, Source{CommitSHA: commit.SHA, EventTime: commit.Commit.Committer.Date})
			}
		}
		if !hasNext(headers) {
			break
		}
	}
	return sources, nil
}
