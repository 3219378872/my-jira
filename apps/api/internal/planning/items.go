package planning

import (
	"encoding/json"
	"sort"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/httpapi"
	"my-jira/apps/api/internal/platform/identity"
	"my-jira/apps/api/internal/support/data"
)

func (s *service) items(spec resource) gin.HandlerFunc {
	return func(c *gin.Context) {
		scope, err := s.scope(c, 5)
		if err != nil {
			data.Fail(c, err)
			return
		}
		id, err := httpapi.UUIDParam(c, spec.param)
		if err != nil {
			data.Fail(c, err)
			return
		}
		if _, err := s.read(c, s.deps.DB.SQL, spec, scope, id); err != nil {
			data.Fail(c, err)
			return
		}
		args := []any{id, scope.WorkspaceID, scope.ProjectID}
		guestFilter := ""
		if scope.Role < 15 && !scope.GuestCanViewAll {
			args = append(args, scope.Actor.UserID)
			guestFilter = " AND w.created_by=$4"
		}
		result, err := data.Many(c, s.deps.DB.SQL, "SELECT to_jsonb(w)-'deleted_at'-'description_binary' FROM work_items w JOIN "+spec.kind+"_items a ON a.work_item_id=w.id AND a.deleted_at IS NULL WHERE a."+spec.kind+"_id=$1 AND w.workspace_id=$2 AND w.project_id=$3 AND "+activePlanningItems("w")+guestFilter+" ORDER BY w.position,w.id", args...)
		data.Send(c, result, err)
	}
}

func (s *service) lockResource(c *gin.Context, q database.DBTX, spec resource, scope identity.Scope, id uuid.UUID) error {
	var found uuid.UUID
	return q.QueryRowContext(c.Request.Context(), "SELECT id FROM "+spec.table+" WHERE id=$1 AND workspace_id=$2 AND project_id=$3 AND deleted_at IS NULL AND archived_at IS NULL FOR UPDATE", id, scope.WorkspaceID, scope.ProjectID).Scan(&found)
}

func lockItems(c *gin.Context, q database.DBTX, scope identity.Scope, ids []uuid.UUID) error {
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
	for _, id := range ids {
		var found uuid.UUID
		if err := q.QueryRowContext(c.Request.Context(), "SELECT w.id FROM work_items w WHERE w.id=$1 AND w.workspace_id=$2 AND w.project_id=$3 AND "+activePlanningItems("w")+" FOR UPDATE", id, scope.WorkspaceID, scope.ProjectID).Scan(&found); err != nil {
			return err
		}
	}
	return nil
}

func (s *service) changeItems(spec resource, remove bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		scope, err := s.scope(c, 15)
		if err != nil {
			data.Fail(c, err)
			return
		}
		id, err := httpapi.UUIDParam(c, spec.param)
		if err != nil {
			data.Fail(c, err)
			return
		}
		var ids []uuid.UUID
		if remove {
			itemID, err := httpapi.UUIDParam(c, "itemID")
			if err != nil {
				data.Fail(c, err)
				return
			}
			ids = []uuid.UUID{itemID}
		} else {
			input, err := data.Bind(c, "work_item_ids")
			if err != nil {
				data.Fail(c, err)
				return
			}
			ids, err = input.UUIDs("work_item_ids")
			if err != nil {
				data.Fail(c, err)
				return
			}
			if len(ids) == 0 {
				data.Fail(c, data.Invalid("At least one work item is required"))
				return
			}
		}
		err = s.deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
			if err := s.lockResource(c, q, spec, scope, id); err != nil {
				return err
			}
			if err := lockItems(c, q, scope, ids); err != nil {
				return err
			}
			previousCycles := map[uuid.UUID]bool{}
			for _, itemID := range ids {
				var previous []byte
				if err := q.QueryRowContext(c.Request.Context(), "SELECT COALESCE(jsonb_agg("+spec.kind+"_id),'[]'::jsonb) FROM "+spec.kind+"_items WHERE work_item_id=$1 AND deleted_at IS NULL", itemID).Scan(&previous); err != nil {
					return err
				}
				if spec.kind == "cycle" && !remove {
					var previousIDs []uuid.UUID
					if err := json.Unmarshal(previous, &previousIDs); err != nil {
						return err
					}
					for _, previousID := range previousIDs {
						if previousID != id {
							previousCycles[previousID] = true
						}
					}
				}
				if remove {
					changed, err := q.ExecContext(c.Request.Context(), "UPDATE "+spec.kind+"_items SET deleted_at=now(),updated_at=now() WHERE "+spec.kind+"_id=$1 AND work_item_id=$2 AND workspace_id=$3 AND deleted_at IS NULL", id, itemID, scope.WorkspaceID)
					if err != nil {
						return err
					}
					if count, _ := changed.RowsAffected(); count > 0 {
						if err = s.recordPlanningItem(c, q, scope, spec, itemID, previous); err != nil {
							return err
						}
					}
					continue
				}
				if spec.kind == "cycle" {
					if _, err := q.ExecContext(c.Request.Context(), "UPDATE cycle_items SET deleted_at=now(),updated_at=now() WHERE work_item_id=$1 AND workspace_id=$2 AND deleted_at IS NULL", itemID, scope.WorkspaceID); err != nil {
						return err
					}
				}
				changed, err := q.ExecContext(c.Request.Context(), "INSERT INTO "+spec.kind+"_items(id,workspace_id,project_id,"+spec.kind+"_id,work_item_id) SELECT $1,$2,$3,$4,$5 WHERE NOT EXISTS(SELECT 1 FROM "+spec.kind+"_items WHERE "+spec.kind+"_id=$4 AND work_item_id=$5 AND deleted_at IS NULL)", uuid.New(), scope.WorkspaceID, scope.ProjectID, id, itemID)
				if err != nil {
					return err
				}
				if count, _ := changed.RowsAffected(); count > 0 {
					if err = s.recordPlanningItem(c, q, scope, spec, itemID, previous); err != nil {
						return err
					}
				}
			}
			for previousID := range previousCycles {
				if err := record(c, q, scope, cycles, previousID, "items_changed"); err != nil {
					return err
				}
			}
			return record(c, q, scope, spec, id, "items_changed")
		})
		if err != nil {
			data.Fail(c, err)
			return
		}
		if remove {
			c.Status(204)
			return
		}
		httpapi.JSON(c, 200, gin.H{"work_item_ids": ids})
	}
}

func (s *service) transfer(c *gin.Context) {
	scope, err := s.scope(c, 15)
	if err != nil {
		data.Fail(c, err)
		return
	}
	source, err := httpapi.UUIDParam(c, "cycleID")
	if err != nil {
		data.Fail(c, err)
		return
	}
	input, err := data.Bind(c, "target_cycle_id", "work_item_ids")
	if err != nil {
		data.Fail(c, err)
		return
	}
	value, err := input.UUID("target_cycle_id", false)
	if err != nil {
		data.Fail(c, err)
		return
	}
	target := value.(uuid.UUID)
	if target == source {
		data.Fail(c, data.Invalid("Choose a different destination cycle"))
		return
	}
	ids, err := input.UUIDs("work_item_ids")
	if err != nil {
		data.Fail(c, err)
		return
	}
	err = s.deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		order := []uuid.UUID{source, target}
		sort.Slice(order, func(i, j int) bool { return order[i].String() < order[j].String() })
		for _, id := range order {
			if err := s.lockResource(c, q, cycles, scope, id); err != nil {
				return err
			}
		}
		var ended bool
		if err := q.QueryRowContext(c.Request.Context(), "SELECT COALESCE(end_date<CURRENT_DATE,false) FROM cycles WHERE id=$1", target).Scan(&ended); err != nil {
			return err
		}
		if ended {
			return data.Invalid("The destination cycle has already ended")
		}
		if len(ids) == 0 {
			rows, err := q.QueryContext(c.Request.Context(), "SELECT ci.work_item_id FROM cycle_items ci JOIN work_items wi ON wi.id=ci.work_item_id JOIN states st ON st.id=wi.state_id WHERE ci.cycle_id=$1 AND ci.deleted_at IS NULL AND "+activePlanningItems("wi")+" AND st.group_name IN('backlog','unstarted','started')", source)
			if err != nil {
				return err
			}
			for rows.Next() {
				var id uuid.UUID
				if err := rows.Scan(&id); err != nil {
					rows.Close()
					return err
				}
				ids = append(ids, id)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
		}
		if err := lockItems(c, q, scope, ids); err != nil {
			return err
		}
		for _, itemID := range ids {
			var eligible bool
			if err := q.QueryRowContext(c.Request.Context(), "SELECT EXISTS(SELECT 1 FROM cycle_items ci JOIN work_items w ON w.id=ci.work_item_id JOIN states st ON st.id=w.state_id WHERE ci.cycle_id=$1 AND ci.work_item_id=$2 AND ci.deleted_at IS NULL AND st.group_name IN('backlog','unstarted','started'))", source, itemID).Scan(&eligible); err != nil {
				return err
			}
			if !eligible {
				return data.Invalid("Every transferred work item must be incomplete and belong to the source cycle")
			}
		}
		snapshot, _, err := s.collectProgress(c, q, cycles, scope, source, true)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(snapshot)
		if err != nil {
			return err
		}
		if _, err = q.ExecContext(c.Request.Context(), "UPDATE cycles SET progress_snapshot=COALESCE(progress_snapshot,$2::jsonb),updated_at=now() WHERE id=$1", source, string(raw)); err != nil {
			return err
		}
		for _, itemID := range ids {
			result, err := q.ExecContext(c.Request.Context(), "UPDATE cycle_items SET cycle_id=$1,updated_at=now() WHERE cycle_id=$2 AND work_item_id=$3 AND workspace_id=$4 AND deleted_at IS NULL", target, source, itemID, scope.WorkspaceID)
			if err != nil {
				return err
			}
			count, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if count != 1 {
				return data.Invalid("Every transferred work item must belong to the source cycle")
			}
			previous, _ := json.Marshal([]uuid.UUID{source})
			if err = s.recordPlanningItem(c, q, scope, cycles, itemID, previous); err != nil {
				return err
			}
		}
		if err := record(c, q, scope, cycles, source, "items_transferred"); err != nil {
			return err
		}
		return record(c, q, scope, cycles, target, "items_received")
	})
	if err != nil {
		data.Fail(c, err)
		return
	}
	httpapi.JSON(c, 200, gin.H{"source_cycle_id": source, "target_cycle_id": target, "work_item_ids": ids})
}

func (s *service) recordPlanningItem(c *gin.Context, q database.DBTX, scope identity.Scope, spec resource, id uuid.UUID, before json.RawMessage) error {
	var after []byte
	if err := q.QueryRowContext(c.Request.Context(), "SELECT COALESCE(jsonb_agg("+spec.kind+"_id),'[]'::jsonb) FROM "+spec.kind+"_items WHERE work_item_id=$1 AND deleted_at IS NULL", id).Scan(&after); err != nil {
		return err
	}
	if _, err := q.ExecContext(c.Request.Context(), "UPDATE work_items SET version=version+1,updated_at=now(),updated_by=$2 WHERE id=$1", id, scope.Actor.UserID); err != nil {
		return err
	}
	return data.RecordIssue(c.Request.Context(), s.deps, q, scope, id, spec.kind+"_changed", map[string]any{spec.kind + "_ids": before}, map[string]any{spec.kind + "_ids": json.RawMessage(after)}, true)
}
