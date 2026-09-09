package data

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"my-jira/apps/api/internal/platform"
	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/identity"
)

// RecordIssue stores the audit row, immutable version, and outbox event in the
// same transaction as the domain change. Callers have already authorized it.
func RecordIssue(ctx context.Context, deps platform.Dependencies, q database.DBTX, scope identity.Scope, id uuid.UUID, action string, before, after any, snapshot bool) error {
	oldJSON, err := json.Marshal(before)
	if err != nil {
		return err
	}
	newJSON, err := json.Marshal(after)
	if err != nil {
		return err
	}
	eventID := uuid.New()
	_, err = q.ExecContext(ctx, `INSERT INTO activities(id,workspace_id,project_id,work_item_id,actor_id,action,field_name,old_value,new_value,metadata) VALUES($1,$2,$3,$4,$5,$6,'',$7::jsonb,$8::jsonb,'{}')`, eventID, scope.WorkspaceID, scope.ProjectID, id, scope.Actor.UserID, action, string(oldJSON), string(newJSON))
	if err != nil {
		return err
	}
	if snapshot {
		_, err = q.ExecContext(ctx, `INSERT INTO work_item_versions(id,workspace_id,project_id,work_item_id,saved_by,version,snapshot) SELECT $1,workspace_id,project_id,id,$2,version,to_jsonb(work_items)-'description_binary' FROM work_items WHERE id=$3`, uuid.New(), scope.Actor.UserID, id)
		if err != nil {
			return err
		}
	}
	if deps.Jobs != nil {
		return deps.Jobs.Publish(ctx, q, "work_item.changed", map[string]any{"event_id": eventID, "workspace_id": scope.WorkspaceID, "project_id": scope.ProjectID, "work_item_id": id, "actor_id": scope.Actor.UserID, "action": action}, eventID.String())
	}
	return nil
}

func LockEstimates(ctx context.Context, q database.DBTX, projectID uuid.UUID) error {
	_, err := q.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", "estimates:"+projectID.String())
	return err
}
