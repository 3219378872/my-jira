package support

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/httpapi"
	"my-jira/apps/api/internal/platform/identity"
	"my-jira/apps/api/internal/support/data"
	"my-jira/apps/api/internal/workitems"
)

type analysisOptions struct {
	Axis, Segment, Metric, Interval, DateField string
	From, To, TrendFrom, TrendTo               string
}

func analysisDimension(axis, interval string) (string, error) {
	switch axis {
	case "":
		return "SELECT ''::text AS key,''::text AS label", nil
	case "priority":
		return "SELECT w.priority::text AS key,w.priority::text AS label", nil
	case "state":
		return "SELECT id::text AS key,name AS label FROM states WHERE id=w.state_id", nil
	case "state_group":
		return "SELECT group_name AS key,group_name AS label FROM states WHERE id=w.state_id", nil
	case "project":
		return "SELECT id::text AS key,name AS label FROM projects WHERE id=w.project_id", nil
	case "estimate":
		return "SELECT id::text AS key,label FROM estimate_points WHERE id=w.estimate_point_id AND deleted_at IS NULL", nil
	case "label":
		return "SELECT DISTINCT l.id::text AS key,l.name AS label FROM work_item_labels r JOIN labels l ON l.id=r.label_id AND l.deleted_at IS NULL WHERE r.work_item_id=w.id AND r.deleted_at IS NULL", nil
	case "assignee":
		return "SELECT DISTINCT u.id::text AS key,u.display_name AS label FROM work_item_assignees r JOIN users u ON u.id=r.user_id AND u.deleted_at IS NULL JOIN workspace_members wm ON wm.user_id=u.id AND wm.workspace_id=w.workspace_id AND wm.is_active AND wm.deleted_at IS NULL WHERE r.work_item_id=w.id AND r.deleted_at IS NULL", nil
	case "cycle":
		return "SELECT DISTINCT p.id::text AS key,p.name AS label FROM cycle_items r JOIN cycles p ON p.id=r.cycle_id AND p.deleted_at IS NULL WHERE r.work_item_id=w.id AND r.deleted_at IS NULL", nil
	case "module":
		return "SELECT DISTINCT p.id::text AS key,p.name AS label FROM module_items r JOIN modules p ON p.id=r.module_id AND p.deleted_at IS NULL WHERE r.work_item_id=w.id AND r.deleted_at IS NULL", nil
	case "start_date", "target_date", "created_at", "completed_at", "updated_at":
		value := "to_char(date_trunc('" + interval + "',w." + axis + "),'YYYY-MM-DD')"
		return "SELECT " + value + " AS key," + value + " AS label", nil
	default:
		return "", data.Invalid("Unsupported analysis dimension: " + axis)
	}
}

func parseAnalysis(c *gin.Context) (analysisOptions, error) {
	o := analysisOptions{Axis: c.DefaultQuery("x_axis", "state_group"), Segment: c.Query("segment"), Metric: c.DefaultQuery("metric", "count"), Interval: c.DefaultQuery("interval", "day"), DateField: c.DefaultQuery("date_field", "created_at"), From: c.Query("from"), To: c.Query("to")}
	if o.Interval != "day" && o.Interval != "week" && o.Interval != "month" {
		return o, data.Invalid("interval must be day, week, or month")
	}
	if o.Metric != "count" && o.Metric != "estimate" {
		return o, data.Invalid("metric must be count or estimate")
	}
	if !strings.Contains("|created_at|updated_at|completed_at|start_date|target_date|", "|"+o.DateField+"|") {
		return o, data.Invalid("Invalid analysis date_field")
	}
	if _, err := analysisDimension(o.Axis, o.Interval); err != nil {
		return o, err
	}
	if _, err := analysisDimension(o.Segment, o.Interval); err != nil {
		return o, err
	}
	if o.Axis == o.Segment && o.Axis != "" {
		return o, data.Invalid("The segment must differ from the main dimension")
	}
	end := time.Now().UTC().Truncate(24 * time.Hour)
	var start time.Time
	for key, value := range map[string]string{"from": o.From, "to": o.To} {
		if value == "" {
			continue
		}
		d, err := time.Parse("2006-01-02", value)
		if err != nil {
			return o, data.Invalid(key + " must use YYYY-MM-DD")
		}
		if key == "from" {
			start = d
		} else {
			end = d
		}
	}
	if start.IsZero() {
		start = end.AddDate(0, 0, -29)
	}
	if start.After(end) || end.Sub(start) > 3660*24*time.Hour {
		return o, data.Invalid("Analysis dates must be ordered and span at most ten years")
	}
	o.TrendFrom, o.TrendTo = start.Format("2006-01-02"), end.Format("2006-01-02")
	return o, nil
}

func (s *service) analysisWhere(c *gin.Context, scope identity.Scope, o analysisOptions) (string, []any, error) {
	where := "w.workspace_id=$1 AND w.deleted_at IS NULL AND " + visibleIssue("w")
	args := []any{scope.WorkspaceID, scope.Actor.UserID}
	for _, item := range []struct{ value, op string }{{o.From, ">="}, {o.To, "<="}} {
		if item.value != "" {
			args = append(args, item.value)
			where += fmt.Sprintf(" AND w.%s::date%s$%d::date", o.DateField, item.op, len(args))
		}
	}
	return workitems.CompileQuery(c, where, args)
}

func (s *service) analyze(c *gin.Context, scope identity.Scope) (gin.H, []uuid.UUID, error) {
	o, err := parseAnalysis(c)
	if err != nil {
		return nil, nil, err
	}
	where, args, err := s.analysisWhere(c, scope, o)
	if err != nil {
		return nil, nil, err
	}
	var total, completed, started, overdue int
	var estimated float64
	err = s.deps.DB.SQL.QueryRowContext(c.Request.Context(), "SELECT count(*),count(*) FILTER(WHERE st.group_name='completed'),count(*) FILTER(WHERE st.group_name='started'),count(*) FILTER(WHERE w.target_date<CURRENT_DATE AND st.group_name NOT IN('completed','cancelled')),COALESCE(sum(w.estimate),0) FROM work_items w JOIN states st ON st.id=w.state_id WHERE "+where, args...).Scan(&total, &completed, &started, &overdue, &estimated)
	if err != nil {
		return nil, nil, err
	}
	result := gin.H{"total": total, "completed": completed, "started": started, "overdue": overdue, "estimate": estimated, "x_axis": o.Axis, "segment": o.Segment, "metric": o.Metric, "interval": o.Interval, "from": o.From, "to": o.To, "date_field": o.DateField}
	x, _ := analysisDimension(o.Axis, o.Interval)
	y, _ := analysisDimension(o.Segment, o.Interval)
	metric := "count(DISTINCT w.id)::double precision"
	if o.Metric == "estimate" {
		metric = "COALESCE(sum(w.estimate),0)"
	}
	queries := map[string]string{
		"by_state":     "SELECT jsonb_build_object('state_id',st.id,'name',st.name,'color',st.color,'group',st.group_name,'count',count(*)) FROM work_items w JOIN states st ON st.id=w.state_id WHERE " + where + " GROUP BY st.id ORDER BY st.position,st.id",
		"by_priority":  "SELECT jsonb_build_object('priority',w.priority,'count',count(*)) FROM work_items w WHERE " + where + " GROUP BY w.priority ORDER BY w.priority",
		"by_project":   "SELECT jsonb_build_object('project_id',p.id,'name',p.name,'identifier',p.identifier,'total',count(*),'completed',count(*) FILTER(WHERE st.group_name='completed')) FROM work_items w JOIN projects p ON p.id=w.project_id JOIN states st ON st.id=w.state_id WHERE " + where + " GROUP BY p.id ORDER BY p.name,p.id",
		"distribution": "SELECT jsonb_build_object('key',COALESCE(x.key,'none'),'label',COALESCE(x.label,'None'),'segment_key',COALESCE(y.key,'none'),'segment_label',COALESCE(y.label,'None'),'count',count(DISTINCT w.id),'estimate',COALESCE(sum(w.estimate),0),'value'," + metric + ") FROM work_items w LEFT JOIN LATERAL (" + x + ") x ON true LEFT JOIN LATERAL (" + y + ") y ON true WHERE " + where + " GROUP BY x.key,x.label,y.key,y.label ORDER BY x.label,x.key,y.label,y.key LIMIT 10001",
	}
	for key, query := range queries {
		values, err := data.Many(c, s.deps.DB.SQL, query, args...)
		if err != nil {
			return nil, nil, err
		}
		if key == "distribution" && len(values) > 10000 {
			return nil, nil, data.Invalid("This analysis exceeds 10000 groups; narrow the date range or filters")
		}
		result[key] = values
	}
	trendArgs := append(append([]any{}, args...), o.TrendFrom, o.TrendTo)
	trendQuery := fmt.Sprintf("WITH allowed AS(SELECT w.created_at,w.completed_at FROM work_items w WHERE %s),days AS(SELECT generate_series(date_trunc('%s',$%d::date),date_trunc('%s',$%d::date),'1 %s'::interval) AS day) SELECT jsonb_build_object('date',to_char(day,'YYYY-MM-DD'),'created',(SELECT count(*) FROM allowed a WHERE a.created_at>=day AND a.created_at<day+'1 %s'::interval),'completed',(SELECT count(*) FROM allowed a WHERE a.completed_at>=day AND a.completed_at<day+'1 %s'::interval)) FROM days ORDER BY day", where, o.Interval, len(args)+1, o.Interval, len(args)+2, o.Interval, o.Interval, o.Interval)
	result["trend"], err = data.Many(c, s.deps.DB.SQL, trendQuery, trendArgs...)
	if err != nil {
		return nil, nil, err
	}
	projectRows := result["by_project"].([]json.RawMessage)
	projects := make([]uuid.UUID, 0, len(projectRows))
	for _, raw := range projectRows {
		var project struct {
			ID uuid.UUID `json:"project_id"`
		}
		if err = json.Unmarshal(raw, &project); err != nil {
			return nil, nil, err
		}
		projects = append(projects, project.ID)
	}
	return result, projects, nil
}

func (s *service) analytics(c *gin.Context) {
	scope, err := s.scope(c)
	if err != nil {
		data.Fail(c, err)
		return
	}
	result, _, err := s.analyze(c, scope)
	data.Send(c, result, err)
}

// Stored analyses use the same bounded query-string grammar as GET /analytics.
func analysisQuery(raw json.RawMessage) (map[string]string, error) {
	var query map[string]string
	if json.Unmarshal(raw, &query) != nil || query == nil || len(query) > 60 || len(raw) > 40000 {
		return nil, data.Invalid("query must be an object of analysis parameter strings")
	}
	allowed := "|x_axis|segment|metric|interval|date_field|from|to|id|project_id|state_id|state_group|parent_id|created_by|priority|sequence_id|estimate|estimate_point_id|start_date|target_date|created_at|updated_at|completed_at|assignee_id|label_id|cycle_id|module_id|subscriber_id|mention_id|search|archived|draft|deleted|include_subitems|scheduled|filter|intake_status|"
	for key := range query {
		base := strings.TrimSuffix(strings.TrimSuffix(key, "_before"), "_after")
		if !strings.Contains(allowed, "|"+base+"|") {
			return nil, data.Invalid("Unknown analysis query field: " + key)
		}
	}
	return query, nil
}

func queryContext(c *gin.Context, query map[string]string) *gin.Context {
	child := &gin.Context{Request: c.Request.Clone(c.Request.Context())}
	child.Request.URL = &url.URL{Path: c.Request.URL.Path}
	values := url.Values{}
	for key, value := range query {
		values.Set(key, value)
	}
	child.Request.URL.RawQuery = values.Encode()
	actor, _ := httpapi.Actor(c)
	child.Set(httpapi.ActorKey, actor)
	return child
}

func (s *service) validateAnalysis(c *gin.Context, scope identity.Scope, query map[string]string) error {
	child := queryContext(c, query)
	o, err := parseAnalysis(child)
	if err == nil {
		_, _, err = s.analysisWhere(child, scope, o)
	}
	return err
}

func (s *service) savedAnalyses(c *gin.Context) {
	scope, err := s.scope(c)
	if err != nil {
		data.Fail(c, err)
		return
	}
	result, err := data.Many(c, s.deps.DB.SQL, "SELECT to_jsonb(a)-'deleted_at' FROM saved_analyses a WHERE workspace_id=$1 AND deleted_at IS NULL ORDER BY name,id", scope.WorkspaceID)
	data.Send(c, result, err)
}

func (s *service) savedAnalysis(c *gin.Context) {
	scope, err := s.scope(c)
	if err != nil {
		data.Fail(c, err)
		return
	}
	id, err := httpapi.UUIDParam(c, "analysisID")
	if err != nil {
		data.Fail(c, err)
		return
	}
	var raw []byte
	err = s.deps.DB.SQL.QueryRowContext(c.Request.Context(), "SELECT to_jsonb(a)-'deleted_at' FROM saved_analyses a WHERE id=$1 AND workspace_id=$2 AND deleted_at IS NULL", id, scope.WorkspaceID).Scan(&raw)
	if err != nil || !strings.HasSuffix(c.Request.URL.Path, "/run") {
		data.Send(c, json.RawMessage(raw), err)
		return
	}
	var saved struct {
		Query map[string]string `json:"query"`
	}
	if err = json.Unmarshal(raw, &saved); err != nil {
		data.Fail(c, err)
		return
	}
	result, _, err := s.analyze(queryContext(c, saved.Query), scope)
	data.Send(c, result, err)
}

func (s *service) mutateAnalysis(c *gin.Context) {
	scope, err := s.scope(c)
	if err != nil {
		data.Fail(c, err)
		return
	}
	if scope.Role < identity.Admin {
		data.Fail(c, data.Forbidden())
		return
	}
	id := uuid.New()
	if c.Request.Method != http.MethodPost {
		id, err = httpapi.UUIDParam(c, "analysisID")
		if err != nil {
			data.Fail(c, err)
			return
		}
	}
	input := data.Object{}
	if c.Request.Method != http.MethodDelete {
		input, err = data.Bind(c, "name", "description", "query")
		if err != nil {
			data.Fail(c, err)
			return
		}
	}
	var result json.RawMessage
	err = s.deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		name, description, queryRaw := "", "", []byte("{}")
		if c.Request.Method != http.MethodPost {
			if err := q.QueryRowContext(c.Request.Context(), "SELECT name,description,query FROM saved_analyses WHERE id=$1 AND workspace_id=$2 AND deleted_at IS NULL FOR UPDATE", id, scope.WorkspaceID).Scan(&name, &description, &queryRaw); err != nil {
				return err
			}
		}
		if c.Request.Method == http.MethodDelete {
			_, err := q.ExecContext(c.Request.Context(), "UPDATE saved_analyses SET deleted_at=now(),updated_at=now() WHERE id=$1", id)
			return err
		}
		if _, ok := input["name"]; ok || c.Request.Method == http.MethodPost {
			name, err = input.String("name", true, 255)
			if err != nil {
				return err
			}
		}
		if _, ok := input["description"]; ok {
			description, err = input.String("description", false, 10000)
			if err != nil {
				return err
			}
		}
		if raw, ok := input["query"]; ok {
			queryRaw = raw
		}
		query, err := analysisQuery(queryRaw)
		if err != nil {
			return err
		}
		if err = s.validateAnalysis(c, scope, query); err != nil {
			return err
		}
		if c.Request.Method == http.MethodPost {
			result, err = data.One(c, q, "INSERT INTO saved_analyses(id,workspace_id,owner_id,name,description,query) VALUES($1,$2,$3,$4,$5,$6::jsonb) RETURNING to_jsonb(saved_analyses)-'deleted_at'", id, scope.WorkspaceID, scope.Actor.UserID, name, description, string(queryRaw))
		} else {
			result, err = data.One(c, q, "UPDATE saved_analyses SET name=$2,description=$3,query=$4::jsonb,updated_at=now() WHERE id=$1 RETURNING to_jsonb(saved_analyses)-'deleted_at'", id, name, description, string(queryRaw))
		}
		return err
	})
	if err != nil {
		data.Fail(c, err)
		return
	}
	status := http.StatusOK
	if c.Request.Method == http.MethodPost {
		status = http.StatusCreated
	}
	if c.Request.Method == http.MethodDelete {
		c.Status(http.StatusNoContent)
		return
	}
	httpapi.JSON(c, status, result)
}
