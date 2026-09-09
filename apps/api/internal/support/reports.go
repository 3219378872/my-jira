package support

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/minio/minio-go/v7"
	"my-jira/apps/api/internal/platform"
	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/httpapi"
	"my-jira/apps/api/internal/platform/identity"
	"my-jira/apps/api/internal/platform/jobs"
	"my-jira/apps/api/internal/platform/objectstore"
	"my-jira/apps/api/internal/support/data"
)

func RegisterJobs(mux *asynq.ServeMux, deps platform.Dependencies) {
	s := &service{deps: deps}
	mux.HandleFunc("report.generate", s.reportJob)
}

func (s *service) exportAnalysis(c *gin.Context) {
	scope, err := s.scope(c)
	if err != nil {
		data.Fail(c, err)
		return
	}
	if scope.Role < identity.Member {
		data.Fail(c, data.Forbidden())
		return
	}
	input, err := data.Bind(c, "query")
	if err != nil {
		data.Fail(c, err)
		return
	}
	raw, ok := input["query"]
	if !ok {
		raw = json.RawMessage("{}")
	}
	query, err := analysisQuery(raw)
	if err != nil {
		data.Fail(c, err)
		return
	}
	if err = s.validateAnalysis(c, scope, query); err != nil {
		data.Fail(c, err)
		return
	}
	filters, err := json.Marshal(map[string]any{"report_type": "analytics", "query": query})
	if err != nil {
		data.Fail(c, err)
		return
	}
	id := uuid.New()
	err = s.deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if _, err := q.ExecContext(c.Request.Context(), "INSERT INTO exports(id,workspace_id,requested_by,format,filters,status) VALUES($1,$2,$3,'csv',$4::jsonb,'queued')", id, scope.WorkspaceID, scope.Actor.UserID, string(filters)); err != nil {
			return err
		}
		return s.deps.Jobs.Publish(c.Request.Context(), q, "report.generate", map[string]any{"export_id": id}, "report:"+id.String())
	})
	if err != nil {
		data.Fail(c, err)
		return
	}
	httpapi.JSON(c, http.StatusAccepted, gin.H{"id": id, "format": "csv", "report_type": "analytics", "status": "queued", "download_url": fmt.Sprintf("/api/v1/workspaces/%s/exports/%s/download", scope.WorkspaceID, id)})
}

func analysisCSV(result gin.H) ([]byte, error) {
	var output bytes.Buffer
	writer := csv.NewWriter(&output)
	if err := writer.Write([]string{"Dimension", "Segment", "Work items", "Estimate", "Value"}); err != nil {
		return nil, err
	}
	for _, raw := range result["distribution"].([]json.RawMessage) {
		var row struct {
			Label    string  `json:"label"`
			Segment  string  `json:"segment_label"`
			Count    int     `json:"count"`
			Estimate float64 `json:"estimate"`
			Value    float64 `json:"value"`
		}
		if err := json.Unmarshal(raw, &row); err != nil {
			return nil, err
		}
		if err := writer.Write([]string{safeCSV(row.Label), safeCSV(row.Segment), strconv.Itoa(row.Count), strconv.FormatFloat(row.Estimate, 'f', -1, 64), strconv.FormatFloat(row.Value, 'f', -1, 64)}); err != nil {
			return nil, err
		}
	}
	writer.Flush()
	return output.Bytes(), writer.Error()
}

func (s *service) reportJob(ctx context.Context, task *asynq.Task) (resultErr error) {
	var envelope jobs.Envelope
	var body struct {
		ExportID uuid.UUID `json:"export_id"`
	}
	if json.Unmarshal(task.Payload(), &envelope) != nil || json.Unmarshal(envelope.Payload, &body) != nil || body.ExportID == uuid.Nil {
		return fmt.Errorf("invalid report task: %w", asynq.SkipRetry)
	}
	// Queue deliveries can overlap after a worker loses its lease. Serialize the
	// complete generation, including object replacement, for this export ID.
	guard, err := s.deps.DB.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer guard.Rollback()
	if _, err = guard.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", "report:"+body.ExportID.String()); err != nil {
		return err
	}
	var workspaceID, userID uuid.UUID
	var format, status string
	var raw []byte
	err = s.deps.DB.SQL.QueryRowContext(ctx, "SELECT workspace_id,requested_by,format,status,filters FROM exports WHERE id=$1 AND deleted_at IS NULL", body.ExportID).Scan(&workspaceID, &userID, &format, &status, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if status == "completed" {
		return nil
	}
	var filters struct {
		ReportType string            `json:"report_type"`
		Query      map[string]string `json:"query"`
	}
	if json.Unmarshal(raw, &filters) != nil || filters.ReportType != "analytics" || format != "csv" {
		return fmt.Errorf("invalid report specification: %w", asynq.SkipRetry)
	}
	defer func() {
		if resultErr != nil {
			next := "queued"
			retried, _ := asynq.GetRetryCount(ctx)
			maximum, _ := asynq.GetMaxRetry(ctx)
			if errors.Is(resultErr, asynq.SkipRetry) || retried >= maximum {
				next = "failed"
			}
			_, _ = s.deps.DB.SQL.ExecContext(context.Background(), "UPDATE exports SET status=$2,error_message='Report generation failed; retry after checking access and service configuration',updated_at=now() WHERE id=$1 AND status<>'completed'", body.ExportID, next)
		}
	}()
	actor := identity.Actor{UserID: userID}
	scope, err := s.deps.Policy.Workspace(ctx, actor, workspaceID, identity.Member)
	if err != nil {
		return fmt.Errorf("report access was revoked: %w", asynq.SkipRetry)
	}
	if _, err = s.deps.DB.SQL.ExecContext(ctx, "UPDATE exports SET status='processing',error_message='',updated_at=now() WHERE id=$1 AND status<>'completed'", body.ExportID); err != nil {
		return err
	}
	// The actor is selected from the persisted export request. No HTTP header can
	// provide this internal worker identity.
	request := &http.Request{Method: http.MethodGet, URL: &url.URL{Path: "/analytics"}}
	c := &gin.Context{Request: request.WithContext(ctx)}
	c.Set(httpapi.ActorKey, actor)
	result, projects, err := s.analyze(queryContext(c, filters.Query), scope)
	if err != nil {
		return err
	}
	for _, projectID := range projects {
		if _, err = s.deps.Policy.Project(ctx, actor, workspaceID, projectID, identity.Member); err != nil {
			return fmt.Errorf("report project access was revoked: %w", asynq.SkipRetry)
		}
	}
	content, err := analysisCSV(result)
	if err != nil {
		return err
	}
	store, bucket, err := objectstore.Load(ctx, s.deps.DB.SQL)
	if err != nil {
		return err
	}
	if err = objectstore.EnsureBucket(ctx, store, bucket); err != nil {
		return err
	}
	key := "exports/" + workspaceID.String() + "/" + body.ExportID.String() + ".csv"
	if _, err = store.PutObject(ctx, bucket, key, bytes.NewReader(content), int64(len(content)), minio.PutObjectOptions{ContentType: "text/csv; charset=utf-8"}); err != nil {
		return err
	}
	scopes, err := json.Marshal(projects)
	if err != nil {
		return err
	}
	return s.deps.DB.WithinTx(ctx, func(q database.DBTX) error {
		var existing string
		if err := q.QueryRowContext(ctx, "SELECT status FROM exports WHERE id=$1 AND deleted_at IS NULL FOR UPDATE", body.ExportID).Scan(&existing); err != nil {
			return err
		}
		if existing == "completed" {
			return nil
		}
		if _, err := q.ExecContext(ctx, "UPDATE exports SET status='completed',object_key=$2,filters=jsonb_set(filters,'{_scope_project_ids}',$3::jsonb),completed_at=now(),updated_at=now() WHERE id=$1", body.ExportID, key, string(scopes)); err != nil {
			return err
		}
		return s.deps.Jobs.Publish(ctx, q, "email.report-ready", map[string]any{"export_id": body.ExportID}, "report-email:"+body.ExportID.String())
	})
}
