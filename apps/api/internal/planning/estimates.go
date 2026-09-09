package planning

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/httpapi"
	"my-jira/apps/api/internal/platform/identity"
	"my-jira/apps/api/internal/support/data"
)

type estimatePointInput struct {
	Label        string   `json:"label"`
	NumericValue *float64 `json:"numeric_value"`
	Position     *float64 `json:"position"`
}

func (s *service) registerEstimates(p *gin.RouterGroup) {
	p.GET("/estimate-templates", s.estimateTemplates)
	p.GET("/estimate-settings", s.estimateSettings)
	p.PATCH("/estimate-settings", s.configureEstimate)
	p.GET("/estimates", s.listEstimates)
	p.POST("/estimates", s.createEstimate)
	p.GET("/estimates/:estimateID", s.getEstimate)
	p.PATCH("/estimates/:estimateID", s.updateEstimate)
	p.DELETE("/estimates/:estimateID", s.deleteEstimate)
	p.POST("/estimates/:estimateID/points", s.createEstimatePoint)
	p.PATCH("/estimates/:estimateID/points/:pointID", s.updateEstimatePoint)
	p.DELETE("/estimates/:estimateID/points/:pointID", s.deleteEstimatePoint)
}

const estimateProjection = `(to_jsonb(e)-'deleted_at')||jsonb_build_object('points',COALESCE((SELECT jsonb_agg(to_jsonb(ep)-'deleted_at' ORDER BY ep.position,ep.id) FROM estimate_points ep WHERE ep.estimate_id=e.id AND ep.deleted_at IS NULL),'[]'::jsonb))`

func (s *service) readEstimate(c *gin.Context, q database.DBTX, scope identity.Scope, id uuid.UUID) (json.RawMessage, error) {
	return data.One(c, q, "SELECT "+estimateProjection+" FROM estimates e WHERE e.id=$1 AND e.workspace_id=$2 AND e.project_id=$3 AND e.deleted_at IS NULL", id, scope.WorkspaceID, scope.ProjectID)
}

func (s *service) estimateTemplates(c *gin.Context) {
	if _, err := s.scope(c, identity.Guest); err != nil {
		data.Fail(c, err)
		return
	}
	templates := []map[string]any{}
	for _, entry := range []struct {
		id, name, kind string
		labels         []string
	}{{"fibonacci", "Fibonacci", "points", []string{"0", "1", "2", "3", "5", "8", "13", "21"}}, {"linear", "Linear", "points", []string{"0", "1", "2", "3", "4", "5"}}, {"sizes", "Sizes", "categories", []string{"XS", "S", "M", "L", "XL"}}} {
		points := []map[string]any{}
		for i, label := range entry.labels {
			var numeric any
			if entry.kind == "points" {
				var n float64
				fmt.Sscan(label, &n)
				numeric = n
			}
			points = append(points, map[string]any{"label": label, "numeric_value": numeric, "position": (i + 1) * 1024})
		}
		templates = append(templates, map[string]any{"id": entry.id, "name": entry.name, "kind": entry.kind, "points": points})
	}
	httpapi.JSON(c, 200, templates)
}

func (s *service) listEstimates(c *gin.Context) {
	scope, err := s.scope(c, identity.Guest)
	if err != nil {
		data.Fail(c, err)
		return
	}
	result, err := data.Many(c, s.deps.DB.SQL, "SELECT "+estimateProjection+" FROM estimates e WHERE e.workspace_id=$1 AND e.project_id=$2 AND e.deleted_at IS NULL ORDER BY e.name,e.id", scope.WorkspaceID, scope.ProjectID)
	data.Send(c, result, err)
}

func (s *service) getEstimate(c *gin.Context) {
	scope, err := s.scope(c, identity.Guest)
	if err != nil {
		data.Fail(c, err)
		return
	}
	id, err := httpapi.UUIDParam(c, "estimateID")
	if err != nil {
		data.Fail(c, err)
		return
	}
	result, err := s.readEstimate(c, s.deps.DB.SQL, scope, id)
	data.Send(c, result, err)
}

func validateEstimatePoint(point estimatePointInput, kind string) error {
	if strings.TrimSpace(point.Label) == "" || len([]rune(point.Label)) > 20 {
		return data.Invalid("Estimate labels need 1 to 20 characters")
	}
	if point.Position != nil && (math.IsNaN(*point.Position) || math.IsInf(*point.Position, 0) || math.Abs(*point.Position) > 1e15) {
		return data.Invalid("Invalid point position")
	}
	if kind == "points" {
		if point.NumericValue == nil || math.IsNaN(*point.NumericValue) || math.IsInf(*point.NumericValue, 0) || *point.NumericValue < 0 || *point.NumericValue > 1e12 {
			return data.Invalid("Numeric estimates need a nonnegative numeric_value")
		}
	} else if point.NumericValue != nil {
		return data.Invalid("Category estimates do not have numeric values")
	}
	return nil
}

func (s *service) createEstimate(c *gin.Context) {
	scope, err := s.scope(c, identity.Admin)
	if err != nil {
		data.Fail(c, err)
		return
	}
	var body struct {
		Name        string               `json:"name"`
		Description string               `json:"description"`
		Kind        string               `json:"kind"`
		Points      []estimatePointInput `json:"points"`
	}
	body, err = httpapi.Bind[struct {
		Name        string               `json:"name"`
		Description string               `json:"description"`
		Kind        string               `json:"kind"`
		Points      []estimatePointInput `json:"points"`
	}](c)
	if body.Kind == "" {
		body.Kind = "points"
	}
	if err != nil || strings.TrimSpace(body.Name) == "" || len([]rune(body.Name)) > 255 || len(body.Description) > 10000 || (body.Kind != "points" && body.Kind != "categories") || len(body.Points) == 0 || len(body.Points) > 100 {
		data.Fail(c, data.Invalid("Provide a name, kind, and 1 to 100 estimate points"))
		return
	}
	seen := map[string]bool{}
	for _, point := range body.Points {
		if err := validateEstimatePoint(point, body.Kind); err != nil {
			data.Fail(c, err)
			return
		}
		label := strings.TrimSpace(point.Label)
		if seen[label] {
			data.Fail(c, data.Invalid("Point labels must be unique"))
			return
		}
		seen[label] = true
	}
	id := uuid.New()
	var result json.RawMessage
	err = s.deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if err := data.LockEstimates(c.Request.Context(), q, scope.ProjectID); err != nil {
			return err
		}
		if _, err := q.ExecContext(c.Request.Context(), "INSERT INTO estimates(id,workspace_id,project_id,name,description,kind) VALUES($1,$2,$3,$4,$5,$6)", id, scope.WorkspaceID, scope.ProjectID, strings.TrimSpace(body.Name), body.Description, body.Kind); err != nil {
			return err
		}
		for i, point := range body.Points {
			position := float64((i + 1) * 1024)
			if point.Position != nil {
				position = *point.Position
			}
			if _, err := q.ExecContext(c.Request.Context(), "INSERT INTO estimate_points(id,workspace_id,project_id,estimate_id,label,numeric_value,position) VALUES($1,$2,$3,$4,$5,$6,$7)", uuid.New(), scope.WorkspaceID, scope.ProjectID, id, strings.TrimSpace(point.Label), point.NumericValue, position); err != nil {
				return err
			}
		}
		var err error
		result, err = s.readEstimate(c, q, scope, id)
		return err
	})
	if err != nil {
		data.Fail(c, err)
		return
	}
	httpapi.JSON(c, 201, result)
}

func (s *service) updateEstimate(c *gin.Context) {
	scope, err := s.scope(c, identity.Admin)
	if err != nil {
		data.Fail(c, err)
		return
	}
	id, err := httpapi.UUIDParam(c, "estimateID")
	if err != nil {
		data.Fail(c, err)
		return
	}
	input, err := data.Bind(c, "name", "description")
	if err != nil {
		data.Fail(c, err)
		return
	}
	sets := []string{"updated_at=now()"}
	args := []any{id, scope.WorkspaceID, scope.ProjectID}
	for _, field := range []string{"name", "description"} {
		if _, ok := input[field]; !ok {
			continue
		}
		maximum := 10000
		if field == "name" {
			maximum = 255
		}
		value, err := input.String(field, field == "name", maximum)
		if err != nil {
			data.Fail(c, err)
			return
		}
		args = append(args, value)
		sets = append(sets, fmt.Sprintf("%s=$%d", field, len(args)))
	}
	var result json.RawMessage
	err = s.deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if err := data.LockEstimates(c.Request.Context(), q, scope.ProjectID); err != nil {
			return err
		}
		row, err := q.ExecContext(c.Request.Context(), "UPDATE estimates SET "+strings.Join(sets, ",")+" WHERE id=$1 AND workspace_id=$2 AND project_id=$3 AND deleted_at IS NULL", args...)
		if err != nil {
			return err
		}
		count, _ := row.RowsAffected()
		if count == 0 {
			return data.Missing()
		}
		result, err = s.readEstimate(c, q, scope, id)
		return err
	})
	data.Send(c, result, err)
}

func (s *service) estimateSettings(c *gin.Context) {
	scope, err := s.scope(c, identity.Guest)
	if err != nil {
		data.Fail(c, err)
		return
	}
	result, err := s.readEstimateSettings(c, s.deps.DB.SQL, scope)
	data.Send(c, result, err)
}
func (s *service) readEstimateSettings(c *gin.Context, q database.DBTX, scope identity.Scope) (json.RawMessage, error) {
	return data.One(c, q, "SELECT jsonb_build_object('estimate_id',p.estimate_id,'estimate',(SELECT "+estimateProjection+" FROM estimates e WHERE e.id=p.estimate_id AND e.deleted_at IS NULL)) FROM projects p WHERE p.id=$1 AND p.workspace_id=$2 AND p.deleted_at IS NULL", scope.ProjectID, scope.WorkspaceID)
}

func (s *service) configureEstimate(c *gin.Context) {
	scope, err := s.scope(c, identity.Admin)
	if err != nil {
		data.Fail(c, err)
		return
	}
	input, err := data.Bind(c, "estimate_id")
	if err != nil {
		data.Fail(c, err)
		return
	}
	if _, ok := input["estimate_id"]; !ok {
		data.Fail(c, data.Invalid("estimate_id is required; null disables estimates"))
		return
	}
	id, err := input.UUID("estimate_id", true)
	if err != nil {
		data.Fail(c, err)
		return
	}
	var result json.RawMessage
	err = s.deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if err := data.LockEstimates(c.Request.Context(), q, scope.ProjectID); err != nil {
			return err
		}
		if id != nil {
			if _, err := s.readEstimate(c, q, scope, id.(uuid.UUID)); err != nil {
				return err
			}
		}
		var old uuid.NullUUID
		if err := q.QueryRowContext(c.Request.Context(), "SELECT estimate_id FROM projects WHERE id=$1 AND workspace_id=$2 AND deleted_at IS NULL FOR UPDATE", scope.ProjectID, scope.WorkspaceID).Scan(&old); err != nil {
			return err
		}
		if (id == nil && !old.Valid) || (old.Valid && id == old.UUID) {
			var err error
			result, err = s.readEstimateSettings(c, q, scope)
			return err
		}
		if _, err := q.ExecContext(c.Request.Context(), "UPDATE projects SET estimate_id=$2,updated_at=now() WHERE id=$1", scope.ProjectID, id); err != nil {
			return err
		}
		// The active scheme chooses options for new estimates. Existing issue
		// assignments survive switching/disabling until explicitly reassigned.
		var err error
		result, err = s.readEstimateSettings(c, q, scope)
		return err
	})
	data.Send(c, result, err)
}

func (s *service) reassignEstimate(c *gin.Context, q database.DBTX, scope identity.Scope, condition string, conditionArgs []any, newPoint any) error {
	args := []any{scope.WorkspaceID, scope.ProjectID}
	args = append(args, conditionArgs...)
	rows, err := q.QueryContext(c.Request.Context(), "SELECT id,to_jsonb(w)-'description_binary' FROM work_items w WHERE workspace_id=$1 AND project_id=$2 AND (estimate_point_id IS NOT NULL OR estimate IS NOT NULL) AND "+condition+" ORDER BY id FOR UPDATE", args...)
	if err != nil {
		return err
	}
	type change struct {
		id  uuid.UUID
		old json.RawMessage
	}
	changes := []change{}
	for rows.Next() {
		var item change
		if err = rows.Scan(&item.id, &item.old); err != nil {
			rows.Close()
			return err
		}
		changes = append(changes, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, item := range changes {
		var after []byte
		if err := q.QueryRowContext(c.Request.Context(), "UPDATE work_items SET estimate_point_id=$2,estimate=(SELECT numeric_value FROM estimate_points WHERE id=$2),updated_at=now(),updated_by=$3,version=version+1 WHERE id=$1 RETURNING to_jsonb(work_items)-'description_binary'", item.id, newPoint, scope.Actor.UserID).Scan(&after); err != nil {
			return err
		}
		if err := data.RecordIssue(c.Request.Context(), s.deps, q, scope, item.id, "estimate.changed", item.old, json.RawMessage(after), true); err != nil {
			return err
		}
	}
	return nil
}

func (s *service) deleteEstimate(c *gin.Context) {
	scope, err := s.scope(c, identity.Admin)
	if err != nil {
		data.Fail(c, err)
		return
	}
	id, err := httpapi.UUIDParam(c, "estimateID")
	if err != nil {
		data.Fail(c, err)
		return
	}
	err = s.deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if err := data.LockEstimates(c.Request.Context(), q, scope.ProjectID); err != nil {
			return err
		}
		if _, err := s.readEstimate(c, q, scope, id); err != nil {
			return err
		}
		if err := s.reassignEstimate(c, q, scope, "estimate_point_id IN(SELECT id FROM estimate_points WHERE estimate_id=$3)", []any{id}, nil); err != nil {
			return err
		}
		for _, statement := range []string{"UPDATE projects SET estimate_id=NULL,updated_at=now() WHERE id=$1 AND estimate_id=$2", "UPDATE estimate_points SET deleted_at=now(),updated_at=now() WHERE project_id=$1 AND estimate_id=$2 AND deleted_at IS NULL", "UPDATE estimates SET deleted_at=now(),updated_at=now() WHERE project_id=$1 AND id=$2"} {
			if _, err := q.ExecContext(c.Request.Context(), statement, scope.ProjectID, id); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		data.Fail(c, err)
		return
	}
	c.Status(204)
}

func (s *service) estimatePointScope(c *gin.Context) (identity.Scope, uuid.UUID, error) {
	scope, err := s.scope(c, identity.Admin)
	if err != nil {
		return scope, uuid.Nil, err
	}
	id, err := httpapi.UUIDParam(c, "estimateID")
	return scope, id, err
}
func (s *service) estimateKind(c *gin.Context, q database.DBTX, scope identity.Scope, id uuid.UUID) (string, error) {
	var kind string
	err := q.QueryRowContext(c.Request.Context(), "SELECT kind FROM estimates WHERE id=$1 AND workspace_id=$2 AND project_id=$3 AND deleted_at IS NULL", id, scope.WorkspaceID, scope.ProjectID).Scan(&kind)
	return kind, err
}

func (s *service) createEstimatePoint(c *gin.Context) {
	scope, id, err := s.estimatePointScope(c)
	if err != nil {
		data.Fail(c, err)
		return
	}
	point, err := httpapi.Bind[estimatePointInput](c)
	if err != nil {
		data.Fail(c, err)
		return
	}
	var result json.RawMessage
	err = s.deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if err := data.LockEstimates(c.Request.Context(), q, scope.ProjectID); err != nil {
			return err
		}
		kind, err := s.estimateKind(c, q, scope, id)
		if err != nil {
			return err
		}
		if err = validateEstimatePoint(point, kind); err != nil {
			return err
		}
		position := point.Position
		if position == nil {
			var next float64
			if err = q.QueryRowContext(c.Request.Context(), "SELECT COALESCE(max(position),0)+1024 FROM estimate_points WHERE estimate_id=$1 AND deleted_at IS NULL", id).Scan(&next); err != nil {
				return err
			}
			position = &next
		}
		result, err = data.One(c, q, "INSERT INTO estimate_points(id,workspace_id,project_id,estimate_id,label,numeric_value,position) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING to_jsonb(estimate_points)-'deleted_at'", uuid.New(), scope.WorkspaceID, scope.ProjectID, id, strings.TrimSpace(point.Label), point.NumericValue, position)
		return err
	})
	if err != nil {
		data.Fail(c, err)
		return
	}
	httpapi.JSON(c, 201, result)
}

func (s *service) updateEstimatePoint(c *gin.Context) {
	scope, id, err := s.estimatePointScope(c)
	if err != nil {
		data.Fail(c, err)
		return
	}
	pointID, err := httpapi.UUIDParam(c, "pointID")
	if err != nil {
		data.Fail(c, err)
		return
	}
	input, err := data.Bind(c, "label", "numeric_value", "position")
	if err != nil {
		data.Fail(c, err)
		return
	}
	var result json.RawMessage
	err = s.deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if err := data.LockEstimates(c.Request.Context(), q, scope.ProjectID); err != nil {
			return err
		}
		kind, err := s.estimateKind(c, q, scope, id)
		if err != nil {
			return err
		}
		var point estimatePointInput
		var raw []byte
		if err = q.QueryRowContext(c.Request.Context(), "SELECT jsonb_build_object('label',label,'numeric_value',numeric_value,'position',position) FROM estimate_points WHERE id=$1 AND estimate_id=$2 AND deleted_at IS NULL", pointID, id).Scan(&raw); err != nil {
			return err
		}
		if err = json.Unmarshal(raw, &point); err != nil {
			return err
		}
		if value, ok := input["label"]; ok {
			if json.Unmarshal(value, &point.Label) != nil || string(value) == "null" {
				return data.Invalid("Invalid label")
			}
		}
		if value, ok := input["numeric_value"]; ok {
			if json.Unmarshal(value, &point.NumericValue) != nil {
				return data.Invalid("Invalid numeric_value")
			}
		}
		if value, ok := input["position"]; ok {
			if json.Unmarshal(value, &point.Position) != nil || string(value) == "null" {
				return data.Invalid("Invalid position")
			}
		}
		if err := validateEstimatePoint(point, kind); err != nil {
			return err
		}
		result, err = data.One(c, q, "UPDATE estimate_points SET label=$2,numeric_value=$3,position=$4,updated_at=now() WHERE id=$1 RETURNING to_jsonb(estimate_points)-'deleted_at'", pointID, strings.TrimSpace(point.Label), point.NumericValue, point.Position)
		if err != nil {
			return err
		}
		return s.reassignEstimate(c, q, scope, "estimate_point_id=$3", []any{pointID}, pointID)
	})
	data.Send(c, result, err)
}

func (s *service) deleteEstimatePoint(c *gin.Context) {
	scope, id, err := s.estimatePointScope(c)
	if err != nil {
		data.Fail(c, err)
		return
	}
	pointID, err := httpapi.UUIDParam(c, "pointID")
	if err != nil {
		data.Fail(c, err)
		return
	}
	var replacement any
	if text := c.Query("replacement_id"); text != "" {
		value, e := uuid.Parse(text)
		if e != nil || value == uuid.Nil || value == pointID {
			data.Fail(c, data.Invalid("Choose another estimate point"))
			return
		}
		replacement = value
	}
	err = s.deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if err := data.LockEstimates(c.Request.Context(), q, scope.ProjectID); err != nil {
			return err
		}
		if _, err := s.estimateKind(c, q, scope, id); err != nil {
			return err
		}
		var current bool
		if err := q.QueryRowContext(c.Request.Context(), "SELECT EXISTS(SELECT 1 FROM estimate_points WHERE id=$1 AND estimate_id=$2 AND deleted_at IS NULL)", pointID, id).Scan(&current); err != nil {
			return err
		}
		if !current {
			return data.Missing()
		}
		if replacement != nil {
			var valid bool
			if err := q.QueryRowContext(c.Request.Context(), "SELECT EXISTS(SELECT 1 FROM estimate_points WHERE id=$1 AND estimate_id=$2 AND deleted_at IS NULL)", replacement, id).Scan(&valid); err != nil {
				return err
			}
			if !valid {
				return data.Invalid("Replacement must belong to the same estimate scheme")
			}
		}
		if err := s.reassignEstimate(c, q, scope, "estimate_point_id=$3", []any{pointID}, replacement); err != nil {
			return err
		}
		_, err := q.ExecContext(c.Request.Context(), "UPDATE estimate_points SET deleted_at=now(),updated_at=now() WHERE id=$1", pointID)
		return err
	})
	if err != nil {
		data.Fail(c, err)
		return
	}
	c.Status(204)
}
