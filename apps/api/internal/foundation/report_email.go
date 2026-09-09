package foundation

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"my-jira/apps/api/internal/platform"
	"my-jira/apps/api/internal/platform/identity"
	"my-jira/apps/api/internal/platform/jobs"
	"my-jira/apps/api/internal/platform/serviceconfig"
)

func sendReportReady(ctx context.Context, deps platform.Dependencies, task *asynq.Task) error {
	var envelope jobs.Envelope
	if err := json.Unmarshal(task.Payload(), &envelope); err != nil {
		return fmt.Errorf("invalid report email envelope: %w", asynq.SkipRetry)
	}
	var message struct {
		ExportID    uuid.UUID `json:"export_id"`
		WorkspaceID uuid.UUID `json:"workspace_id"`
		UserID      uuid.UUID `json:"user_id"`
	}
	if err := json.Unmarshal(envelope.Payload, &message); err != nil || message.ExportID == uuid.Nil {
		return fmt.Errorf("invalid report email task: %w", asynq.SkipRetry)
	}
	var email string
	var workspaceID, userID uuid.UUID
	var projectID uuid.NullUUID
	var raw []byte
	err := deps.DB.SQL.QueryRowContext(ctx, `SELECT u.email,e.project_id,COALESCE(e.filters->'_scope_project_ids','[]'::jsonb),e.workspace_id,e.requested_by FROM exports e JOIN users u ON u.id=e.requested_by WHERE e.id=$1 AND e.status='completed' AND e.deleted_at IS NULL AND u.is_active AND u.deleted_at IS NULL`, message.ExportID).Scan(&email, &projectID, &raw, &workspaceID, &userID)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if (message.WorkspaceID != uuid.Nil && message.WorkspaceID != workspaceID) || (message.UserID != uuid.Nil && message.UserID != userID) {
		return nil
	}
	message.WorkspaceID, message.UserID = workspaceID, userID
	actor := identity.Actor{UserID: message.UserID}
	if _, err = deps.Policy.Workspace(ctx, actor, message.WorkspaceID, identity.Member); err != nil {
		if unavailableLocation(err) {
			return nil
		}
		return err
	}
	var scopeProjects []uuid.UUID
	if err = json.Unmarshal(raw, &scopeProjects); err != nil {
		return err
	}
	if projectID.Valid {
		scopeProjects = append(scopeProjects, projectID.UUID)
	}
	for _, project := range scopeProjects {
		if _, err = deps.Policy.Project(ctx, actor, message.WorkspaceID, project, identity.Member); err != nil {
			if unavailableLocation(err) {
				return nil
			}
			return err
		}
	}
	origin := strings.TrimRight(strings.TrimSpace(strings.Split(os.Getenv("APP_ORIGIN"), ",")[0]), "/")
	if origin == "" {
		return fmt.Errorf("APP_ORIGIN is required for report download links")
	}
	link := fmt.Sprintf("%s/api/v1/workspaces/%s/exports/%s/download", origin, message.WorkspaceID, message.ExportID)
	values, err := serviceconfig.Load(ctx, deps.DB.SQL, "email")
	if err != nil {
		return err
	}
	return sendEmailWithConfig(ctx, values, email, "Your my-jira report is ready", "Your requested report is ready. Sign in to the same account, then download it here:\n\n"+link+"\n\nYour access is checked again when you download the file.", envelope.EventID.String())
}
