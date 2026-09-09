package planning

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"my-jira/apps/api/internal/platform"
	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/httpapi"
	"my-jira/apps/api/internal/platform/identity"
	"my-jira/apps/api/internal/platform/jobs"
	"my-jira/apps/api/internal/support/data"
)

type service struct{ deps platform.Dependencies }
type resource struct {
	table, path, param, kind string
	fields                   map[string]string
}

var cycles = resource{"cycles", "cycles", "cycleID", "cycle", map[string]string{"name": "name", "description": "text", "start_date": "date", "end_date": "date", "owner_id": "member", "position": "number", "settings": "json", "archived": "archive"}}
var modules = resource{"modules", "modules", "moduleID", "module", map[string]string{"name": "name", "description": "text", "start_date": "date", "target_date": "date", "lead_id": "nullable_member", "status": "status", "position": "number", "settings": "json", "archived": "archive", "member_ids": "members"}}
var views = resource{"saved_views", "views", "viewID", "view", map[string]string{"name": "name", "description": "text", "layout": "layout", "filters": "json", "display": "json", "is_private": "bool", "position": "number"}}

func Register(r *gin.RouterGroup, deps platform.Dependencies) {
	s := &service{deps: deps}
	p := r.Group("/workspaces/:workspaceID/projects/:projectID")
	s.registerEstimates(p)
	for _, spec := range []resource{cycles, modules, views} {
		s.register(p, spec)
	}
	s.register(r.Group("/workspaces/:workspaceID"), views)
	p.GET("/cycles/:cycleID/progress", s.progress(cycles))
	p.GET("/modules/:moduleID/progress", s.progress(modules))
	for _, spec := range []resource{cycles, modules} {
		p.GET("/"+spec.path+"/:"+spec.param+"/items", s.items(spec))
		p.POST("/"+spec.path+"/:"+spec.param+"/items", s.changeItems(spec, false))
		p.DELETE("/"+spec.path+"/:"+spec.param+"/items/:itemID", s.changeItems(spec, true))
	}
	p.POST("/cycles/:cycleID/transfer", s.transfer)
	p.POST("/cycles/check-dates", s.checkCycleDates)
	p.GET("/modules/:moduleID/links", s.moduleLinks)
	p.POST("/modules/:moduleID/links", s.mutateModuleLink)
	p.PATCH("/modules/:moduleID/links/:linkID", s.mutateModuleLink)
	p.DELETE("/modules/:moduleID/links/:linkID", s.mutateModuleLink)
}

func (s *service) register(r *gin.RouterGroup, spec resource) {
	r.GET("/"+spec.path, s.list(spec))
	r.POST("/"+spec.path, s.create(spec))
	r.GET("/"+spec.path+"/:"+spec.param, s.get(spec))
	r.PATCH("/"+spec.path+"/:"+spec.param, s.update(spec, false))
	r.DELETE("/"+spec.path+"/:"+spec.param, s.update(spec, true))
}

func (s *service) scope(c *gin.Context, min identity.Role) (identity.Scope, error) {
	actor, err := httpapi.Actor(c)
	if err != nil {
		return identity.Scope{}, err
	}
	wid, err := httpapi.UUIDParam(c, "workspaceID")
	if err != nil {
		return identity.Scope{}, err
	}
	if c.Param("projectID") == "" {
		return s.deps.Policy.Workspace(c.Request.Context(), actor, wid, min)
	}
	pid, err := httpapi.UUIDParam(c, "projectID")
	if err != nil {
		return identity.Scope{}, err
	}
	return s.deps.Policy.Project(c.Request.Context(), actor, wid, pid, min)
}

func project(scope identity.Scope) any {
	if scope.ProjectID == uuid.Nil {
		return nil
	}
	return scope.ProjectID
}
func allowed(spec resource) []string {
	keys := make([]string, 0, len(spec.fields))
	for k := range spec.fields {
		keys = append(keys, k)
	}
	return keys
}

func projection(spec resource, guestFilter string) string {
	base := "to_jsonb(t)-'deleted_at'-'progress_snapshot'"
	active := " AND " + activePlanningItems("wi") + guestFilter
	if spec.kind == "module" {
		return base + " || jsonb_build_object('member_ids',COALESCE((SELECT jsonb_agg(mm.user_id) FROM module_members mm WHERE mm.module_id=t.id AND mm.deleted_at IS NULL),'[]'::jsonb),'work_item_count',(SELECT count(*) FROM module_items mi JOIN work_items wi ON wi.id=mi.work_item_id AND wi.deleted_at IS NULL WHERE mi.module_id=t.id AND mi.deleted_at IS NULL" + active + "))"
	}
	if spec.kind == "cycle" {
		return base + " || jsonb_build_object('work_item_count',(SELECT count(*) FROM cycle_items ci JOIN work_items wi ON wi.id=ci.work_item_id AND wi.deleted_at IS NULL WHERE ci.cycle_id=t.id AND ci.deleted_at IS NULL" + active + "))"
	}
	return base
}

func (s *service) read(c *gin.Context, q database.DBTX, spec resource, scope identity.Scope, id uuid.UUID) (json.RawMessage, error) {
	filter := ""
	if spec.kind == "view" {
		filter = " AND (NOT t.is_private OR t.owner_id=$4)"
	}
	args := []any{scope.WorkspaceID, project(scope), id}
	if spec.kind == "view" {
		args = append(args, scope.Actor.UserID)
	}
	guestFilter := ""
	if spec.kind != "view" && scope.Role < 15 && !scope.GuestCanViewAll {
		args = append(args, scope.Actor.UserID)
		guestFilter = " AND wi.created_by=$4"
	}
	return data.One(c, q, "SELECT "+projection(spec, guestFilter)+" FROM "+spec.table+" t WHERE t.workspace_id=$1 AND t.project_id IS NOT DISTINCT FROM $2::uuid AND t.id=$3 AND t.deleted_at IS NULL"+filter, args...)
}

func (s *service) list(spec resource) gin.HandlerFunc {
	return func(c *gin.Context) {
		scope, err := s.scope(c, 5)
		if err != nil {
			data.Fail(c, err)
			return
		}
		args := []any{scope.WorkspaceID, project(scope)}
		where := "t.workspace_id=$1 AND t.project_id IS NOT DISTINCT FROM $2::uuid AND t.deleted_at IS NULL"
		if spec.kind == "view" {
			args = append(args, scope.Actor.UserID)
			where += " AND (NOT t.is_private OR t.owner_id=$3)"
		} else if c.Query("archived") == "true" {
			where += " AND t.archived_at IS NOT NULL"
		} else {
			where += " AND t.archived_at IS NULL"
		}
		guestFilter := ""
		if spec.kind != "view" && scope.Role < 15 && !scope.GuestCanViewAll {
			args = append(args, scope.Actor.UserID)
			guestFilter = " AND wi.created_by=$3"
		}
		if text := strings.TrimSpace(c.Query("search")); text != "" {
			args = append(args, "%"+text+"%")
			where += fmt.Sprintf(" AND t.name ILIKE $%d", len(args))
		}
		result, err := data.Many(c, s.deps.DB.SQL, "SELECT "+projection(spec, guestFilter)+" FROM "+spec.table+" t WHERE "+where+" ORDER BY t.position,t.created_at,t.id", args...)
		data.Send(c, result, err)
	}
}

func (s *service) get(spec resource) gin.HandlerFunc {
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
		value, err := s.read(c, s.deps.DB.SQL, spec, scope, id)
		data.Send(c, value, err)
	}
}

func parse(spec resource, input data.Object, creating bool) (map[string]any, error) {
	values := map[string]any{}
	if creating {
		values["name"] = ""
		values["description"] = ""
		values["position"] = 0.0
		if spec.kind == "view" {
			values["layout"] = "list"
			values["filters"] = "{}"
			values["display"] = "{}"
			values["is_private"] = false
		} else {
			values["settings"] = "{}"
		}
		if spec.kind == "module" {
			values["status"] = "planned"
		}
	}
	if creating {
		if _, ok := input["name"]; !ok {
			return nil, data.Invalid("name is required")
		}
	}
	for key := range input {
		var value any
		var err error
		switch spec.fields[key] {
		case "name":
			value, err = input.String(key, true, 255)
		case "text":
			value, err = input.String(key, false, 10000)
		case "date":
			value, err = input.Date(key)
		case "member":
			value, err = input.UUID(key, false)
		case "nullable_member":
			value, err = input.UUID(key, true)
		case "members":
			continue
		case "number":
			value, err = input.Number(key)
		case "json":
			value, err = input.JSON(key)
		case "bool":
			value, err = input.Bool(key)
		case "archive":
			var archived bool
			archived, err = input.Bool(key)
			key = "archived_at"
			if archived {
				value = time.Now().UTC()
			}
		case "status":
			var text string
			text, err = input.String(key, true, 30)
			if err == nil && !contains([]string{"backlog", "planned", "in-progress", "paused", "completed", "cancelled"}, text) {
				err = data.Invalid("Unknown module status")
			}
			value = text
		case "layout":
			var text string
			text, err = input.String(key, true, 30)
			if err == nil && !contains([]string{"list", "kanban", "calendar", "spreadsheet", "gantt"}, text) {
				err = data.Invalid("Unknown view layout")
			}
			value = text
		default:
			err = data.Invalid("Unknown field: " + key)
		}
		if err != nil {
			return nil, err
		}
		values[key] = value
	}
	return values, nil
}

func contains(values []string, target string) bool {
	for _, v := range values {
		if v == target {
			return true
		}
	}
	return false
}

func validateDates(spec resource, values map[string]any, existing data.Object) error {
	if spec.kind == "view" {
		return nil
	}
	endKey := "end_date"
	if spec.kind == "module" {
		endKey = "target_date"
	}
	get := func(key string) (any, error) {
		if v, ok := values[key]; ok {
			return v, nil
		}
		return existing.Date(key)
	}
	start, err := get("start_date")
	if err != nil {
		return err
	}
	end, err := get(endKey)
	if err != nil {
		return err
	}
	if start != nil && end != nil && start.(string) > end.(string) {
		return data.Invalid("The end date cannot precede the start date")
	}
	return nil
}

func (s *service) validateMembers(c *gin.Context, q database.DBTX, scope identity.Scope, values map[string]any) error {
	for _, key := range []string{"owner_id", "lead_id"} {
		value, ok := values[key]
		if !ok || value == nil {
			continue
		}
		var exists bool
		query := "SELECT EXISTS(SELECT 1 FROM workspace_members WHERE workspace_id=$1 AND user_id=$2 AND is_active AND deleted_at IS NULL)"
		args := []any{scope.WorkspaceID, value}
		if scope.ProjectID != uuid.Nil {
			query = "SELECT EXISTS(SELECT 1 FROM project_members pm JOIN workspace_members wm ON wm.workspace_id=pm.workspace_id AND wm.user_id=pm.user_id AND wm.is_active AND wm.deleted_at IS NULL WHERE pm.workspace_id=$1 AND pm.user_id=$2 AND pm.project_id=$3 AND pm.is_active AND pm.deleted_at IS NULL)"
			args = append(args, scope.ProjectID)
		}
		if err := q.QueryRowContext(c.Request.Context(), query, args...).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return data.Invalid(key + " must reference an active member")
		}
	}
	return nil
}

func (s *service) writeModuleMembers(c *gin.Context, q database.DBTX, scope identity.Scope, id uuid.UUID, input data.Object) error {
	if _, ok := input["member_ids"]; !ok {
		return nil
	}
	ids, err := input.UUIDs("member_ids")
	if err != nil {
		return err
	}
	for _, userID := range ids {
		if err := s.validateMembers(c, q, scope, map[string]any{"lead_id": userID}); err != nil {
			return err
		}
	}
	if _, err := q.ExecContext(c.Request.Context(), "UPDATE module_members SET deleted_at=now(),updated_at=now() WHERE module_id=$1 AND workspace_id=$2 AND deleted_at IS NULL", id, scope.WorkspaceID); err != nil {
		return err
	}
	for _, userID := range ids {
		if _, err := q.ExecContext(c.Request.Context(), "INSERT INTO module_members(id,workspace_id,project_id,module_id,user_id) VALUES($1,$2,$3,$4,$5)", uuid.New(), scope.WorkspaceID, scope.ProjectID, id, userID); err != nil {
			return err
		}
	}
	return nil
}

func record(c *gin.Context, q database.DBTX, scope identity.Scope, spec resource, id uuid.UUID, action string) error {
	metadata, _ := json.Marshal(map[string]any{"entity_type": spec.kind, "entity_id": id})
	_, err := q.ExecContext(c.Request.Context(), "INSERT INTO activities(id,workspace_id,project_id,actor_id,action,field_name,metadata) VALUES($1,$2,$3,$4,$5,'',$6::jsonb)", uuid.New(), scope.WorkspaceID, project(scope), scope.Actor.UserID, spec.kind+"."+action, string(metadata))
	if err != nil || spec.kind == "view" {
		return err
	}
	value, err := data.One(c, q, "SELECT to_jsonb(t)-'deleted_at'-'progress_snapshot' FROM "+spec.table+" t WHERE id=$1 AND workspace_id=$2 AND project_id=$3", id, scope.WorkspaceID, scope.ProjectID)
	if err != nil {
		return err
	}
	event := spec.kind + ".changed"
	if strings.HasPrefix(action, "items_") {
		event = spec.kind + ".items_changed"
		var payload map[string]any
		if err = json.Unmarshal(value, &payload); err != nil {
			return err
		}
		var itemIDs []byte
		if err = q.QueryRowContext(c.Request.Context(), "SELECT COALESCE(jsonb_agg(work_item_id ORDER BY work_item_id),'[]'::jsonb) FROM "+spec.kind+"_items WHERE "+spec.kind+"_id=$1 AND deleted_at IS NULL", id).Scan(&itemIDs); err != nil {
			return err
		}
		payload["work_item_ids"] = json.RawMessage(itemIDs)
		value, err = json.Marshal(payload)
		if err != nil {
			return err
		}
	}
	return jobs.PublishEntityChange(c.Request.Context(), q, scope, event, id, action, value)
}

func (s *service) create(spec resource) gin.HandlerFunc {
	return func(c *gin.Context) {
		scope, err := s.scope(c, 15)
		if err != nil {
			data.Fail(c, err)
			return
		}
		input, err := data.Bind(c, allowed(spec)...)
		if err != nil {
			data.Fail(c, err)
			return
		}
		values, err := parse(spec, input, true)
		if err != nil {
			data.Fail(c, err)
			return
		}
		if spec.kind == "cycle" || spec.kind == "view" {
			if _, ok := values["owner_id"]; !ok {
				values["owner_id"] = scope.Actor.UserID
			}
		}
		if err := validateDates(spec, values, nil); err != nil {
			data.Fail(c, err)
			return
		}
		if spec.kind == "cycle" && (values["start_date"] == nil) != (values["end_date"] == nil) {
			data.Fail(c, data.Invalid("A scheduled cycle needs both start_date and end_date"))
			return
		}
		id := uuid.New()
		var result json.RawMessage
		err = s.deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
			if err := s.validateMembers(c, q, scope, values); err != nil {
				return err
			}
			keys := make([]string, 0, len(values))
			for key := range values {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			columns := []string{"id", "workspace_id", "project_id"}
			args := []any{id, scope.WorkspaceID, project(scope)}
			for _, key := range keys {
				columns = append(columns, key)
				args = append(args, values[key])
			}
			if _, err := q.ExecContext(c.Request.Context(), "INSERT INTO "+spec.table+"("+strings.Join(columns, ",")+") VALUES("+data.Markers(1, len(args))+")", args...); err != nil {
				return err
			}
			if spec.kind == "module" {
				if err := s.writeModuleMembers(c, q, scope, id, input); err != nil {
					return err
				}
			}
			if err := record(c, q, scope, spec, id, "created"); err != nil {
				return err
			}
			var err error
			result, err = s.read(c, q, spec, scope, id)
			return err
		})
		if err != nil {
			data.Fail(c, err)
			return
		}
		httpapi.JSON(c, http.StatusCreated, result)
	}
}

func (s *service) update(spec resource, remove bool) gin.HandlerFunc {
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
		input := data.Object{}
		if !remove {
			input, err = data.Bind(c, allowed(spec)...)
			if err != nil {
				data.Fail(c, err)
				return
			}
		}
		values, err := parse(spec, input, false)
		if err != nil {
			data.Fail(c, err)
			return
		}
		var result json.RawMessage
		err = s.deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
			var raw []byte
			if err := q.QueryRowContext(c.Request.Context(), "SELECT to_jsonb(t) FROM "+spec.table+" t WHERE id=$1 AND workspace_id=$2 AND project_id IS NOT DISTINCT FROM $3::uuid AND deleted_at IS NULL FOR UPDATE", id, scope.WorkspaceID, project(scope)).Scan(&raw); err != nil {
				return err
			}
			var existing data.Object
			if err := json.Unmarshal(raw, &existing); err != nil {
				return err
			}
			if spec.kind == "view" {
				owner, err := existing.UUID("owner_id", false)
				if err != nil {
					return err
				}
				private, _ := existing.Bool("is_private")
				if private && owner != scope.Actor.UserID {
					return data.Missing()
				}
				if owner != scope.Actor.UserID && scope.Role < 20 {
					return data.Forbidden()
				}
			} else if remove && scope.Role < 20 {
				return data.Forbidden()
			}
			if err := validateDates(spec, values, existing); err != nil {
				return err
			}
			if err := s.validateMembers(c, q, scope, values); err != nil {
				return err
			}
			if remove {
				values = map[string]any{"deleted_at": time.Now().UTC()}
			}
			keys := make([]string, 0, len(values))
			for key := range values {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			args := []any{id, scope.WorkspaceID}
			sets := []string{"updated_at=now()"}
			for _, key := range keys {
				args = append(args, values[key])
				sets = append(sets, fmt.Sprintf("%s=$%d", key, len(args)))
			}
			if _, err := q.ExecContext(c.Request.Context(), "UPDATE "+spec.table+" SET "+strings.Join(sets, ",")+" WHERE id=$1 AND workspace_id=$2", args...); err != nil {
				return err
			}
			if spec.kind == "module" && !remove {
				if err := s.writeModuleMembers(c, q, scope, id, input); err != nil {
					return err
				}
			}
			if remove && (spec.kind == "cycle" || spec.kind == "module") {
				if _, err := q.ExecContext(c.Request.Context(), "UPDATE "+spec.kind+"_items SET deleted_at=now(),updated_at=now() WHERE "+spec.kind+"_id=$1 AND deleted_at IS NULL", id); err != nil {
					return err
				}
			}
			action := "updated"
			if remove {
				action = "deleted"
			}
			if err := record(c, q, scope, spec, id, action); err != nil {
				return err
			}
			if remove {
				return nil
			}
			result, err = s.read(c, q, spec, scope, id)
			return err
		})
		if err != nil {
			data.Fail(c, err)
			return
		}
		if remove {
			c.Status(204)
			return
		}
		httpapi.JSON(c, 200, result)
	}
}
