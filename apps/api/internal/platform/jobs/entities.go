package jobs

import (
	"context"
	"github.com/google/uuid"
	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/identity"
)

// PublishEntityChange stores a domain event with its mutation transaction.
// The data must be a public entity DTO, never a credential-bearing database row.
func PublishEntityChange(ctx context.Context, q database.DBTX, scope identity.Scope, event string, entityID uuid.UUID, action string, data any) error {
	id := uuid.New()
	return (Outbox{}).Publish(ctx, q, "entity.changed", map[string]any{"event_id": id, "workspace_id": scope.WorkspaceID, "project_id": scope.ProjectID, "entity_id": entityID, "event": event, "action": action, "data": data}, id.String())
}
