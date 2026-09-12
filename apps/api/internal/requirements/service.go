// Package requirements implements versioned planning commands and the shared
// snapshot/event boundary used by the four requirements views.
package requirements

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"

	"github.com/google/uuid"
	"my-jira/apps/api/internal/platform"
	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/httpapi"
	"my-jira/apps/api/internal/platform/identity"
	"my-jira/apps/api/internal/workitems"
)

type Command struct {
	Operation string                     `json:"operation"`
	ID        uuid.UUID                  `json:"id,omitempty"`
	ClientID  string                     `json:"client_id,omitempty"`
	Version   int64                      `json:"version,omitempty"`
	Fields    map[string]json.RawMessage `json:"fields,omitempty"`
}

type ChangeSet struct {
	IdempotencyKey   string    `json:"idempotency_key"`
	ExpectedRevision *int64    `json:"expected_revision,omitempty"`
	Reason           string    `json:"reason,omitempty"`
	Commands         []Command `json:"commands"`
}

type ChangeResult struct {
	BatchID    uuid.UUID            `json:"batch_id"`
	Revision   int64                `json:"revision"`
	Items      []map[string]any     `json:"items"`
	CreatedIDs map[string]uuid.UUID `json:"created_ids"`
	DeletedIDs []uuid.UUID          `json:"deleted_ids"`
}

type Service struct {
	deps     platform.Dependencies
	commands *workitems.Commands
}

func NewService(deps platform.Dependencies) *Service {
	return &Service{deps: deps, commands: workitems.NewCommands(deps)}
}

func (s *Service) Apply(ctx context.Context, actor identity.Actor, wid, pid uuid.UUID, changes ChangeSet) (ChangeResult, error) {
	var result ChangeResult
	err := s.deps.DB.WithinTx(ctx, func(q database.DBTX) error {
		var err error
		result, err = s.ApplyWithinTx(ctx, q, actor, wid, pid, changes)
		return err
	})
	return result, err
}

// ApplyWithinTx lets an automation worker check its current policy, budget,
// source revision and output constraints in the exact transaction that applies
// these commands. Returning any error must roll back the caller's transaction.
func (s *Service) ApplyWithinTx(ctx context.Context, q database.DBTX, actor identity.Actor, wid, pid uuid.UUID, changes ChangeSet) (ChangeResult, error) {
	result := ChangeResult{Items: []map[string]any{}, CreatedIDs: map[string]uuid.UUID{}, DeletedIDs: []uuid.UUID{}}
	if strings.TrimSpace(changes.IdempotencyKey) == "" || len(changes.IdempotencyKey) > 200 || len(changes.Commands) < 1 || len(changes.Commands) > 200 || len(changes.Reason) > 1000 {
		return result, httpapi.NewError(400, "validation_failed", "A changeset needs an idempotency key and between 1 and 200 commands")
	}
	if changes.Reason == "" {
		changes.Reason = "human"
	}
	encoded, err := json.Marshal(changes)
	if err != nil {
		return result, err
	}
	sum := sha256.Sum256(encoded)
	digest := hex.EncodeToString(sum[:])
	if err = workitems.LockProject(ctx, q, pid); err != nil {
		return result, err
	}
	var storedHash string
	var previous []byte
	err = q.QueryRowContext(ctx, `SELECT input_hash,result FROM planning_change_batches WHERE project_id=$1 AND actor_id=$2 AND idempotency_key=$3`, pid, actor.UserID, changes.IdempotencyKey).Scan(&storedHash, &previous)
	if err == nil {
		scope, accessErr := workitems.CurrentScope(ctx, q, actor, wid, pid, identity.Member)
		if accessErr != nil {
			return result, accessErr
		}
		if err := workitems.RequireRequirements(ctx, q, wid, pid); err != nil {
			return result, err
		}
		if storedHash != digest {
			return result, httpapi.NewError(409, "idempotency_conflict", "The idempotency key was already used for different commands")
		}
		if err = json.Unmarshal(previous, &result); err != nil {
			return result, err
		}
		// Replays must not reveal the saved body of a subsequently moved item.
		visible := []map[string]any{}
		for _, item := range result.Items {
			id, parseErr := uuid.Parse(item["id"].(string))
			if parseErr != nil {
				return result, parseErr
			}
			current, loadErr := s.commands.Load(ctx, q, scope, id, false)
			if loadErr == sql.ErrNoRows {
				continue
			}
			if loadErr != nil {
				return result, loadErr
			}
			visible = append(visible, current)
		}
		result.Items = visible
		if err = q.QueryRowContext(ctx, `SELECT revision FROM project_revisions WHERE project_id=$1`, pid).Scan(&result.Revision); err != nil {
			return result, err
		}
		return result, nil
	}
	if err != sql.ErrNoRows {
		return result, err
	}
	// Item locks precede membership rechecks, so revocation during a lock wait
	// is observed before a single write or idempotent replay can be returned.
	ids := []uuid.UUID{}
	seen := map[uuid.UUID]bool{}
	clientIDs := map[string]bool{}
	for _, command := range changes.Commands {
		if command.Operation != "create" && command.Operation != "update" && command.Operation != "delete" {
			return result, httpapi.NewError(400, "validation_failed", "Unsupported command operation")
		}
		if command.Operation == "create" {
			if command.ID != uuid.Nil {
				return result, httpapi.NewError(400, "validation_failed", "Create IDs are assigned by the service")
			}
			if command.ClientID != "" {
				if clientIDs[command.ClientID] || len(command.ClientID) > 100 {
					return result, httpapi.NewError(400, "validation_failed", "Client IDs must be unique and at most 100 characters")
				}
				clientIDs[command.ClientID] = true
			}
			continue
		}
		if command.ID == uuid.Nil || command.Version < 1 || seen[command.ID] {
			return result, httpapi.NewError(400, "validation_failed", "Changed work items need distinct IDs and positive versions")
		}
		seen[command.ID] = true
		ids = append(ids, command.ID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
	for _, id := range ids {
		var exists uuid.UUID
		if err = q.QueryRowContext(ctx, `SELECT id FROM work_items WHERE id=$1 AND workspace_id=$2 AND project_id=$3 AND deleted_at IS NULL FOR UPDATE`, id, wid, pid).Scan(&exists); err != nil {
			return result, err
		}
	}
	scope, err := workitems.CurrentScope(ctx, q, actor, wid, pid, identity.Member)
	if err != nil {
		return result, err
	}
	if err = workitems.RequireRequirements(ctx, q, wid, pid); err != nil {
		return result, err
	}
	var revision int64
	if _, err = q.ExecContext(ctx, `INSERT INTO project_revisions(project_id,workspace_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, pid, wid); err != nil {
		return result, err
	}
	if err = q.QueryRowContext(ctx, `SELECT revision FROM project_revisions WHERE project_id=$1 FOR UPDATE`, pid).Scan(&revision); err != nil {
		return result, err
	}
	if changes.ExpectedRevision != nil && *changes.ExpectedRevision != revision {
		return result, httpapi.NewError(409, "stale_revision", "The planning input changed; refresh before applying this batch")
	}
	result.BatchID = uuid.New()
	if _, err = q.ExecContext(ctx, `SELECT set_config('myjira.change_reason',$1,true),set_config('myjira.change_batch',$2,true)`, changes.Reason, result.BatchID.String()); err != nil {
		return result, err
	}
	for _, command := range changes.Commands {
		fields := map[string]json.RawMessage{}
		for key, value := range command.Fields {
			var reference string
			if (key == "parent_id" || key == "activity_id") && json.Unmarshal(value, &reference) == nil && strings.HasPrefix(reference, "$") {
				id, ok := result.CreatedIDs[strings.TrimPrefix(reference, "$")]
				if !ok {
					return result, httpapi.NewError(400, "validation_failed", "A command references an unknown or later client ID")
				}
				value, _ = json.Marshal(id)
			}
			if key == "dependency_ids" {
				var refs []string
				if json.Unmarshal(value, &refs) == nil {
					for i, ref := range refs {
						if strings.HasPrefix(ref, "$") {
							id, ok := result.CreatedIDs[strings.TrimPrefix(ref, "$")]
							if !ok {
								return result, httpapi.NewError(400, "validation_failed", "Unknown dependency client ID")
							}
							refs[i] = id.String()
						}
					}
					value, _ = json.Marshal(refs)
				}
			}
			fields[key] = value
		}
		switch command.Operation {
		case "create":
			item, err := s.commands.Create(ctx, q, scope, fields)
			if err != nil {
				return result, err
			}
			result.Items = append(result.Items, item)
			if command.ClientID != "" {
				result.CreatedIDs[command.ClientID] = uuid.MustParse(item["id"].(string))
			}
		case "update":
			fields["version"], _ = json.Marshal(command.Version)
			item, err := s.commands.Update(ctx, q, scope, command.ID, fields, true)
			if err != nil {
				return result, err
			}
			result.Items = append(result.Items, item)
		case "delete":
			item, err := s.commands.Load(ctx, q, scope, command.ID, true)
			if err != nil {
				return result, err
			}
			var version int64
			switch n := item["version"].(type) {
			case json.Number:
				version, _ = n.Int64()
			case float64:
				version = int64(n)
			}
			if version != command.Version {
				return result, httpapi.NewError(409, "conflict", "A deleted work item changed before the batch could apply")
			}
			if err = s.commands.Delete(ctx, q, scope, command.ID); err != nil {
				return result, err
			}
			result.DeletedIDs = append(result.DeletedIDs, command.ID)
		}
	}
	for index, item := range result.Items {
		id, parseErr := uuid.Parse(item["id"].(string))
		if parseErr != nil {
			return result, parseErr
		}
		current, loadErr := s.commands.Load(ctx, q, scope, id, false)
		if loadErr != nil {
			return result, loadErr
		}
		result.Items[index] = current
	}
	if err = q.QueryRowContext(ctx, `SELECT revision FROM project_revisions WHERE project_id=$1`, pid).Scan(&result.Revision); err != nil {
		return result, err
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return result, err
	}
	_, err = q.ExecContext(ctx, `INSERT INTO planning_change_batches(id,workspace_id,project_id,actor_id,idempotency_key,input_hash,reason,result) VALUES($1,$2,$3,$4,$5,$6,$7,$8::jsonb)`, result.BatchID, wid, pid, actor.UserID, changes.IdempotencyKey, digest, changes.Reason, string(payload))
	return result, err
}

func RecordEvent(ctx context.Context, q database.DBTX, scope identity.Scope, kind string, id uuid.UUID) (int64, error) {
	var revision int64
	err := q.QueryRowContext(ctx, `SELECT planning_emit_event($1,$2,$3,$4)`, scope.WorkspaceID, scope.ProjectID, kind, id).Scan(&revision)
	return revision, err
}
