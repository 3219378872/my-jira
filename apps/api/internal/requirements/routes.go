package requirements

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"my-jira/apps/api/internal/platform"
	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/httpapi"
	"my-jira/apps/api/internal/platform/identity"
	"my-jira/apps/api/internal/support/data"
	"my-jira/apps/api/internal/workitems"
)

type handler struct {
	deps    platform.Dependencies
	service *Service
}

func Register(r *gin.RouterGroup, deps platform.Dependencies) {
	h := &handler{deps: deps, service: NewService(deps)}
	p := r.Group("/workspaces/:workspaceID/projects/:projectID")
	p.GET("/requirements/snapshot", h.snapshot)
	p.GET("/requirements/events", h.events)
	p.GET("/requirements/activities", h.activities)
	p.POST("/requirements/activities", h.createActivity)
	p.PATCH("/requirements/activities/:activityID", h.updateActivity)
	p.DELETE("/requirements/activities/:activityID", h.deleteActivity)
	p.POST("/planning/changesets", h.changeset)
	p.GET("/requirements/history", h.history)
}

func (h *handler) scope(c *gin.Context, role identity.Role) (identity.Scope, error) {
	return h.projectScope(c, role, true)
}

func (h *handler) projectScope(c *gin.Context, role identity.Role, requireFeature bool) (identity.Scope, error) {
	a, err := httpapi.Actor(c)
	if err != nil {
		return identity.Scope{}, err
	}
	w, err := httpapi.UUIDParam(c, "workspaceID")
	if err != nil {
		return identity.Scope{}, err
	}
	p, err := httpapi.UUIDParam(c, "projectID")
	if err != nil {
		return identity.Scope{}, err
	}
	s, err := h.deps.Policy.Project(c.Request.Context(), a, w, p, role)
	if err == nil && requireFeature {
		err = workitems.RequireRequirements(c.Request.Context(), h.deps.DB.SQL, w, p)
	}
	return s, err
}

func (h *handler) changeset(c *gin.Context) {
	s, err := h.scope(c, identity.Member)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	input, err := httpapi.Bind[ChangeSet](c)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	result, err := h.service.Apply(c.Request.Context(), s.Actor, s.WorkspaceID, s.ProjectID, input)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.JSON(c, 200, result)
}

type Snapshot struct {
	Revision     int64             `json:"revision"`
	Items        []json.RawMessage `json:"items"`
	Activities   []json.RawMessage `json:"activities"`
	Cycles       []json.RawMessage `json:"cycles"`
	Dependencies []json.RawMessage `json:"dependencies"`
	States       []json.RawMessage `json:"states"`
	Permissions  struct {
		CanEdit  bool `json:"can_edit"`
		CanAdmin bool `json:"can_admin"`
	} `json:"permissions"`
}

func queryMany(ctx context.Context, q database.DBTX, query string, args ...any) ([]json.RawMessage, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []json.RawMessage{}
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		out = append(out, json.RawMessage(raw))
	}
	return out, rows.Err()
}

func activityQuery() string {
	return `SELECT to_jsonb(a)-'deleted_at' FROM requirement_activities a WHERE a.workspace_id=$1 AND a.project_id=$2 AND a.deleted_at IS NULL AND ($4 OR a.created_by=$3 OR EXISTS(SELECT 1 FROM work_items w WHERE w.activity_id=a.id AND w.deleted_at IS NULL AND w.created_by=$3)) ORDER BY a.position,a.id`
}

// Snapshot returns the complete authorized collection and its cursor from one
// repeatable-read snapshot. No page-sized subset is used for view summaries.
func (s *Service) Snapshot(ctx context.Context, actor identity.Actor, wid, pid uuid.UUID) (Snapshot, error) {
	result := Snapshot{}
	tx, err := s.deps.DB.SQL.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	scope, err := workitems.CurrentScope(ctx, tx, actor, wid, pid, identity.Guest)
	if err != nil {
		return result, err
	}
	if err = workitems.RequireRequirements(ctx, tx, wid, pid); err != nil {
		return result, err
	}
	all := scope.Role >= identity.Member || scope.GuestCanViewAll
	var writable bool
	if err = tx.QueryRowContext(ctx, `SELECT archived_at IS NULL FROM projects WHERE id=$1 AND workspace_id=$2`, pid, wid).Scan(&writable); err != nil {
		return result, err
	}
	result.Permissions.CanEdit = writable && scope.Role >= identity.Member
	result.Permissions.CanAdmin = writable && scope.Role >= identity.Admin
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT revision FROM project_revisions WHERE project_id=$1),0)`, pid).Scan(&result.Revision); err != nil {
		return result, err
	}
	result.Items, err = queryMany(ctx, tx, workitems.SelectSQL("$3")+`WHERE w.workspace_id=$1 AND w.project_id=$2 AND w.deleted_at IS NULL AND ($4 OR w.created_by=$3) ORDER BY w.map_position,w.id`, wid, pid, actor.UserID, all)
	if err != nil {
		return result, err
	}
	result.Activities, err = queryMany(ctx, tx, activityQuery(), wid, pid, actor.UserID, all)
	if err != nil {
		return result, err
	}
	result.Cycles, err = queryMany(ctx, tx, `SELECT to_jsonb(c)-'deleted_at' FROM cycles c WHERE c.workspace_id=$1 AND c.project_id=$2 AND c.deleted_at IS NULL ORDER BY c.start_date NULLS LAST,c.created_at,c.id`, wid, pid)
	if err != nil {
		return result, err
	}
	result.States, err = queryMany(ctx, tx, `SELECT (to_jsonb(st)-'deleted_at')||jsonb_build_object('group',st.group_name) FROM states st WHERE st.workspace_id=$1 AND st.project_id=$2 AND st.deleted_at IS NULL ORDER BY st.position,st.id`, wid, pid)
	if err != nil {
		return result, err
	}
	result.Dependencies, err = queryMany(ctx, tx, `SELECT to_jsonb(r)-'deleted_at' FROM work_item_relations r JOIN work_items a ON a.id=r.source_id JOIN work_items b ON b.id=r.target_id WHERE r.workspace_id=$1 AND r.project_id=$2 AND r.relation_type='blocks' AND r.deleted_at IS NULL AND a.deleted_at IS NULL AND b.deleted_at IS NULL AND a.workspace_id=$1 AND a.project_id=$2 AND b.workspace_id=$1 AND b.project_id=$2 AND ($4 OR (a.created_by=$3 AND b.created_by=$3)) ORDER BY r.source_id,r.target_id`, wid, pid, actor.UserID, all)
	if err != nil {
		return result, err
	}
	return result, tx.Commit()
}

func (h *handler) snapshot(c *gin.Context) {
	s, err := h.scope(c, identity.Guest)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	result, err := h.service.Snapshot(c.Request.Context(), s.Actor, s.WorkspaceID, s.ProjectID)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.JSON(c, 200, result)
}
func (h *handler) activities(c *gin.Context) {
	s, err := h.scope(c, identity.Guest)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	result, err := queryMany(c.Request.Context(), h.deps.DB.SQL, activityQuery(), s.WorkspaceID, s.ProjectID, s.Actor.UserID, s.Role >= identity.Member || s.GuestCanViewAll)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.JSON(c, 200, result)
}

func activityFields(input data.Object, creating bool) (map[string]any, error) {
	values := map[string]any{}
	if _, ok := input["name"]; ok || creating {
		v, err := input.String("name", true, 255)
		if err != nil {
			return nil, err
		}
		values["name"] = v
	}
	if _, ok := input["epic_id"]; ok {
		v, err := input.UUID("epic_id", true)
		if err != nil {
			return nil, err
		}
		values["epic_id"] = v
	}
	if _, ok := input["position"]; ok {
		v, err := input.Number("position")
		if err != nil || math.Abs(v) > 1e15 {
			return nil, data.Invalid("Invalid activity position")
		}
		values["position"] = v
	}
	if raw, ok := input["archived_at"]; ok {
		if string(raw) == "null" {
			values["archived_at"] = nil
		} else {
			var text string
			if json.Unmarshal(raw, &text) != nil {
				return nil, data.Invalid("Invalid archive timestamp")
			}
			v, err := time.Parse(time.RFC3339, text)
			if err != nil {
				return nil, data.Invalid("Invalid archive timestamp")
			}
			values["archived_at"] = v
		}
	}
	return values, nil
}

func validateEpic(ctx context.Context, q database.DBTX, s identity.Scope, value any) error {
	if value == nil {
		return nil
	}
	var ok bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM work_items WHERE id=$1 AND workspace_id=$2 AND project_id=$3 AND requirement_type='epic' AND deleted_at IS NULL AND archived_at IS NULL)`, value, s.WorkspaceID, s.ProjectID).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return data.Invalid("Activity Epic must be an active Epic in this project")
	}
	return nil
}

func (h *handler) createActivity(c *gin.Context) {
	s, err := h.scope(c, identity.Member)
	if err != nil {
		data.Fail(c, err)
		return
	}
	input, err := data.Bind(c, "name", "epic_id", "position", "archived_at")
	if err != nil {
		data.Fail(c, err)
		return
	}
	values, err := activityFields(input, true)
	if err != nil {
		data.Fail(c, err)
		return
	}
	var result []byte
	ctx := c.Request.Context()
	err = h.deps.DB.WithinTx(ctx, func(q database.DBTX) error {
		if err := workitems.LockProject(ctx, q, s.ProjectID); err != nil {
			return err
		}
		var err error
		s, err = workitems.CurrentScope(ctx, q, s.Actor, s.WorkspaceID, s.ProjectID, identity.Member)
		if err != nil {
			return err
		}
		if err = workitems.RequireRequirements(ctx, q, s.WorkspaceID, s.ProjectID); err != nil {
			return err
		}
		if err = validateEpic(ctx, q, s, values["epic_id"]); err != nil {
			return err
		}
		position := any(1024)
		if v, ok := values["position"]; ok {
			position = v
		}
		return q.QueryRowContext(ctx, `INSERT INTO requirement_activities(workspace_id,project_id,epic_id,name,position,archived_at,created_by,updated_by) VALUES($1,$2,$3,$4,$5,$6,$7,$7) RETURNING to_jsonb(requirement_activities)-'deleted_at'`, s.WorkspaceID, s.ProjectID, values["epic_id"], values["name"], position, values["archived_at"], s.Actor.UserID).Scan(&result)
	})
	if err != nil {
		data.Fail(c, err)
		return
	}
	httpapi.JSON(c, 201, json.RawMessage(result))
}

func (h *handler) updateActivity(c *gin.Context) {
	s, err := h.scope(c, identity.Member)
	if err != nil {
		data.Fail(c, err)
		return
	}
	id, err := httpapi.UUIDParam(c, "activityID")
	if err != nil {
		data.Fail(c, err)
		return
	}
	input, err := data.Bind(c, "name", "epic_id", "position", "archived_at", "version")
	if err != nil {
		data.Fail(c, err)
		return
	}
	var version int64
	if json.Unmarshal(input["version"], &version) != nil || version < 1 {
		data.Fail(c, data.Invalid("A positive version is required"))
		return
	}
	values, err := activityFields(input, false)
	if err != nil {
		data.Fail(c, err)
		return
	}
	ctx := c.Request.Context()
	var result []byte
	err = h.deps.DB.WithinTx(ctx, func(q database.DBTX) error {
		if err := workitems.LockProject(ctx, q, s.ProjectID); err != nil {
			return err
		}
		var current int64
		var epic *uuid.UUID
		if err := q.QueryRowContext(ctx, `SELECT version,epic_id FROM requirement_activities WHERE id=$1 AND workspace_id=$2 AND project_id=$3 AND deleted_at IS NULL FOR UPDATE`, id, s.WorkspaceID, s.ProjectID).Scan(&current, &epic); err != nil {
			return err
		}
		var err error
		s, err = workitems.CurrentScope(ctx, q, s.Actor, s.WorkspaceID, s.ProjectID, identity.Member)
		if err != nil {
			return err
		}
		if err = workitems.RequireRequirements(ctx, q, s.WorkspaceID, s.ProjectID); err != nil {
			return err
		}
		if version != current {
			return data.Conflict("The activity changed before saving")
		}
		if value, ok := values["epic_id"]; ok {
			if err = validateEpic(ctx, q, s, value); err != nil {
				return err
			}
			old := ""
			if epic != nil {
				old = epic.String()
			}
			next := ""
			if value != nil {
				next = value.(uuid.UUID).String()
			}
			if old != next {
				var populated bool
				if err = q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM work_items WHERE activity_id=$1 AND deleted_at IS NULL)`, id).Scan(&populated); err != nil {
					return err
				}
				if populated {
					return data.Conflict("Move the activity's Stories before changing its Epic")
				}
			}
		}
		args := []any{id, s.Actor.UserID}
		sets := []string{"updated_at=now()", "updated_by=$2", "version=version+1"}
		for _, key := range []string{"name", "epic_id", "position", "archived_at"} {
			if value, ok := values[key]; ok {
				args = append(args, value)
				sets = append(sets, key+"=$"+strconv.Itoa(len(args)))
			}
		}
		return q.QueryRowContext(ctx, `UPDATE requirement_activities SET `+strings.Join(sets, ",")+` WHERE id=$1 RETURNING to_jsonb(requirement_activities)-'deleted_at'`, args...).Scan(&result)
	})
	if err != nil {
		data.Fail(c, err)
		return
	}
	httpapi.JSON(c, 200, json.RawMessage(result))
}

func (h *handler) deleteActivity(c *gin.Context) {
	s, err := h.scope(c, identity.Member)
	if err != nil {
		data.Fail(c, err)
		return
	}
	id, err := httpapi.UUIDParam(c, "activityID")
	if err != nil {
		data.Fail(c, err)
		return
	}
	version, err := strconv.ParseInt(c.Query("version"), 10, 64)
	if err != nil || version < 1 {
		data.Fail(c, data.Invalid("A positive version query parameter is required"))
		return
	}
	destination, explicit := c.GetQuery("migrate_to")
	var to any
	if explicit && destination != "null" {
		to, err = uuid.Parse(destination)
		if err != nil || to == id {
			data.Fail(c, data.Invalid("Choose a different migration activity or null"))
			return
		}
	}
	ctx := c.Request.Context()
	err = h.deps.DB.WithinTx(ctx, func(q database.DBTX) error {
		if err := workitems.LockProject(ctx, q, s.ProjectID); err != nil {
			return err
		}
		var current int64
		var epic *uuid.UUID
		if err := q.QueryRowContext(ctx, `SELECT version,epic_id FROM requirement_activities WHERE id=$1 AND workspace_id=$2 AND project_id=$3 AND deleted_at IS NULL FOR UPDATE`, id, s.WorkspaceID, s.ProjectID).Scan(&current, &epic); err != nil {
			return err
		}
		var err error
		s, err = workitems.CurrentScope(ctx, q, s.Actor, s.WorkspaceID, s.ProjectID, identity.Member)
		if err != nil {
			return err
		}
		if err = workitems.RequireRequirements(ctx, q, s.WorkspaceID, s.ProjectID); err != nil {
			return err
		}
		if current != version {
			return data.Conflict("The activity changed before deletion")
		}
		var count int
		if err = q.QueryRowContext(ctx, `SELECT count(*) FROM work_items WHERE activity_id=$1 AND deleted_at IS NULL`, id).Scan(&count); err != nil {
			return err
		}
		if count > 0 && !explicit {
			return data.Conflict("Migrate the activity's Stories before deleting it")
		}
		if to != nil {
			var valid bool
			if err = q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM requirement_activities WHERE id=$1 AND workspace_id=$2 AND project_id=$3 AND epic_id IS NOT DISTINCT FROM $4::uuid AND deleted_at IS NULL AND archived_at IS NULL)`, to, s.WorkspaceID, s.ProjectID, epic).Scan(&valid); err != nil {
				return err
			}
			if !valid {
				return data.Invalid("Migration activity must be active in the same Epic")
			}
		}
		rows, err := q.QueryContext(ctx, `SELECT id,version FROM work_items WHERE activity_id=$1 AND deleted_at IS NULL ORDER BY id`, id)
		if err != nil {
			return err
		}
		type migrating struct {
			id      uuid.UUID
			version int64
		}
		items := []migrating{}
		for rows.Next() {
			var item migrating
			if err = rows.Scan(&item.id, &item.version); err != nil {
				rows.Close()
				return err
			}
			items = append(items, item)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		destinationJSON, _ := json.Marshal(to)
		for _, item := range items {
			versionJSON, _ := json.Marshal(item.version)
			if _, err = h.service.commands.Update(ctx, q, s, item.id, map[string]json.RawMessage{"activity_id": destinationJSON, "version": versionJSON}, true); err != nil {
				return err
			}
		}
		_, err = q.ExecContext(ctx, `UPDATE requirement_activities SET deleted_at=now(),updated_at=now(),updated_by=$2,version=version+1 WHERE id=$1`, id, s.Actor.UserID)
		return err
	})
	if err != nil {
		data.Fail(c, err)
		return
	}
	c.Status(204)
}

func (h *handler) history(c *gin.Context) {
	s, err := h.scope(c, identity.Member)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	limit := 200
	if text := c.Query("limit"); text != "" {
		limit, err = strconv.Atoi(text)
		if err != nil || limit < 1 || limit > 1000 {
			httpapi.Fail(c, httpapi.NewError(400, "validation_failed", "History limit must be between 1 and 1000"))
			return
		}
	}
	// History cannot re-expose a moved/deleted object outside current scope.
	result, err := queryMany(c.Request.Context(), h.deps.DB.SQL, `SELECT to_jsonb(h) FROM work_item_history h JOIN work_items w ON w.id=h.work_item_id AND w.workspace_id=h.workspace_id AND w.project_id=h.project_id AND w.deleted_at IS NULL WHERE h.workspace_id=$1 AND h.project_id=$2 ORDER BY h.created_at DESC,h.revision DESC LIMIT $3`, s.WorkspaceID, s.ProjectID, limit)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.JSON(c, 200, result)
}
