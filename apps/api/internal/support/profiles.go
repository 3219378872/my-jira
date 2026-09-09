package support

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"my-jira/apps/api/internal/platform/httpapi"
	"my-jira/apps/api/internal/platform/identity"
	"my-jira/apps/api/internal/support/data"
	"my-jira/apps/api/internal/workitems"
)

func (s *service) profileScope(c *gin.Context) (identity.Scope, uuid.UUID, error) {
	scope, err := s.scope(c)
	if err != nil {
		return scope, uuid.Nil, err
	}
	id := scope.Actor.UserID
	if c.Param("userID") != "me" {
		id, err = httpapi.UUIDParam(c, "userID")
		if err != nil {
			return scope, id, err
		}
	}
	var active bool
	err = s.deps.DB.SQL.QueryRowContext(c.Request.Context(), "SELECT EXISTS(SELECT 1 FROM workspace_members wm JOIN users u ON u.id=wm.user_id AND u.is_active AND u.deleted_at IS NULL WHERE wm.workspace_id=$1 AND wm.user_id=$2 AND wm.is_active AND wm.deleted_at IS NULL)", scope.WorkspaceID, id).Scan(&active)
	if err == nil && !active {
		err = data.Missing()
	}
	return scope, id, err
}

func (s *service) profileWhere(c *gin.Context, scope identity.Scope) (string, []any, error) {
	return workitems.CompileQuery(c, "w.workspace_id=$1 AND w.deleted_at IS NULL AND "+visibleIssue("w"), []any{scope.WorkspaceID, scope.Actor.UserID})
}

func (s *service) profile(c *gin.Context) {
	scope, id, err := s.profileScope(c)
	if err != nil {
		data.Fail(c, err)
		return
	}
	user, err := data.One(c, s.deps.DB.SQL, "SELECT jsonb_build_object('id',u.id,'display_name',u.display_name,'first_name',u.first_name,'last_name',u.last_name,'avatar_url',u.avatar_url,'timezone',u.timezone,'joined_at',wm.created_at) FROM users u JOIN workspace_members wm ON wm.user_id=u.id AND wm.workspace_id=$2 AND wm.is_active AND wm.deleted_at IS NULL WHERE u.id=$1 AND u.is_active AND u.deleted_at IS NULL", id, scope.WorkspaceID)
	if err != nil {
		data.Fail(c, err)
		return
	}
	stats, err := s.profileStatistics(c, scope, id)
	if err != nil {
		data.Fail(c, err)
		return
	}
	httpapi.JSON(c, 200, gin.H{"user": user, "stats": stats})
}

func (s *service) profileStats(c *gin.Context) {
	scope, id, err := s.profileScope(c)
	if err != nil {
		data.Fail(c, err)
		return
	}
	result, err := s.profileStatistics(c, scope, id)
	data.Send(c, result, err)
}

func (s *service) profileStatistics(c *gin.Context, scope identity.Scope, id uuid.UUID) (gin.H, error) {
	where, args, err := s.profileWhere(c, scope)
	if err != nil {
		return nil, err
	}
	args = append(args, id)
	user := fmt.Sprintf("$%d", len(args))
	assigned := "EXISTS(SELECT 1 FROM work_item_assignees r WHERE r.work_item_id=w.id AND r.user_id=" + user + " AND r.deleted_at IS NULL)"
	subscribed := "EXISTS(SELECT 1 FROM work_item_subscribers r WHERE r.work_item_id=w.id AND r.user_id=" + user + " AND r.deleted_at IS NULL)"
	var created, assignedCount, pending, completed, subscribedCount int
	err = s.deps.DB.SQL.QueryRowContext(c.Request.Context(), "SELECT count(*) FILTER(WHERE w.created_by="+user+"),count(*) FILTER(WHERE "+assigned+"),count(*) FILTER(WHERE "+assigned+" AND st.group_name NOT IN('completed','cancelled')),count(*) FILTER(WHERE "+assigned+" AND st.group_name='completed'),count(*) FILTER(WHERE "+subscribed+") FROM work_items w JOIN states st ON st.id=w.state_id WHERE "+where, args...).Scan(&created, &assignedCount, &pending, &completed, &subscribedCount)
	if err != nil {
		return nil, err
	}
	result := gin.H{"created": created, "assigned": assignedCount, "pending": pending, "completed": completed, "subscribed": subscribedCount}
	queries := map[string]string{
		"by_state":    "SELECT jsonb_build_object('state_id',st.id,'name',st.name,'color',st.color,'group',st.group_name,'count',count(*)) FROM work_items w JOIN states st ON st.id=w.state_id WHERE " + where + " AND " + assigned + " GROUP BY st.id ORDER BY st.position,st.id",
		"by_priority": "SELECT jsonb_build_object('priority',w.priority,'count',count(*)) FROM work_items w WHERE " + where + " AND " + assigned + " GROUP BY w.priority ORDER BY w.priority",
		"by_project":  "SELECT jsonb_build_object('project_id',p.id,'name',p.name,'identifier',p.identifier,'created',count(*) FILTER(WHERE w.created_by=" + user + "),'assigned',count(*) FILTER(WHERE " + assigned + "),'pending',count(*) FILTER(WHERE " + assigned + " AND st.group_name NOT IN('completed','cancelled')),'completed',count(*) FILTER(WHERE " + assigned + " AND st.group_name='completed')) FROM work_items w JOIN projects p ON p.id=w.project_id JOIN states st ON st.id=w.state_id WHERE " + where + " AND (w.created_by=" + user + " OR " + assigned + ") GROUP BY p.id ORDER BY p.name,p.id",
		"cycles":      "SELECT jsonb_build_object('id',cy.id,'project_id',cy.project_id,'name',cy.name,'start_date',cy.start_date,'end_date',cy.end_date,'status',CASE WHEN cy.start_date>CURRENT_DATE THEN 'upcoming' ELSE 'current' END,'total',count(DISTINCT w.id),'completed',count(DISTINCT w.id) FILTER(WHERE st.group_name='completed')) FROM work_items w JOIN states st ON st.id=w.state_id JOIN cycle_items ci ON ci.work_item_id=w.id AND ci.deleted_at IS NULL JOIN cycles cy ON cy.id=ci.cycle_id AND cy.deleted_at IS NULL AND cy.archived_at IS NULL WHERE " + where + " AND " + assigned + " AND cy.end_date>=CURRENT_DATE GROUP BY cy.id ORDER BY cy.start_date,cy.id",
	}
	for key, query := range queries {
		result[key], err = data.Many(c, s.deps.DB.SQL, query, args...)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (s *service) profileActivityQuery(c *gin.Context, scope identity.Scope, id uuid.UUID, exporting bool) (string, []any, error) {
	where, args, err := s.profileWhere(c, scope)
	if err != nil {
		return "", nil, err
	}
	args = append(args, id)
	where += fmt.Sprintf(" AND a.actor_id=$%d AND a.workspace_id=w.workspace_id AND a.project_id=w.project_id AND a.deleted_at IS NULL AND a.action NOT LIKE 'comment%%' AND a.action NOT LIKE 'reaction%%' AND a.action NOT LIKE 'vote%%' AND a.action NOT LIKE 'draft%%'", len(args))
	from, to := c.Query("from"), c.Query("to")
	if exporting && (from == "" || to == "") {
		return "", nil, data.Invalid("Activity export requires from and to dates")
	}
	for _, item := range []struct{ key, value, op string }{{"from", from, ">="}, {"to", to, "<="}} {
		if item.value == "" {
			continue
		}
		if _, err = time.Parse("2006-01-02", item.value); err != nil {
			return "", nil, data.Invalid(item.key + " must use YYYY-MM-DD")
		}
		args = append(args, item.value)
		where += fmt.Sprintf(" AND a.created_at::date%s$%d::date", item.op, len(args))
	}
	if from != "" && to != "" && from > to {
		return "", nil, data.Invalid("from cannot be after to")
	}
	return where, args, nil
}

func (s *service) profileActivities(c *gin.Context) {
	scope, id, err := s.profileScope(c)
	if err != nil {
		data.Fail(c, err)
		return
	}
	where, args, err := s.profileActivityQuery(c, scope, id, false)
	if err != nil {
		data.Fail(c, err)
		return
	}
	limit, offset := 50, 0
	for key, target := range map[string]*int{"limit": &limit, "offset": &offset} {
		if text := c.Query(key); text != "" {
			value, e := strconv.Atoi(text)
			if e != nil || value < 0 || value > 1000000 || (key == "limit" && (value < 1 || value > 200)) {
				data.Fail(c, data.Invalid("Invalid activity pagination"))
				return
			}
			*target = value
		}
	}
	var total int
	err = s.deps.DB.SQL.QueryRowContext(c.Request.Context(), "SELECT count(*) FROM activities a JOIN work_items w ON w.id=a.work_item_id WHERE "+where, args...).Scan(&total)
	if err != nil {
		data.Fail(c, err)
		return
	}
	rows, err := data.Many(c, s.deps.DB.SQL, "SELECT jsonb_build_object('id',a.id,'created_at',a.created_at,'actor_id',a.actor_id,'project_id',w.project_id,'work_item_id',w.id,'work_item_name',w.name,'sequence_id',w.sequence_id,'identifier',p.identifier,'action',a.action,'field_name',a.field_name,'old_value',a.old_value,'new_value',a.new_value) FROM activities a JOIN work_items w ON w.id=a.work_item_id JOIN projects p ON p.id=w.project_id WHERE "+where+fmt.Sprintf(" ORDER BY a.created_at DESC,a.id DESC LIMIT %d OFFSET %d", limit, offset), args...)
	if err != nil {
		data.Fail(c, err)
		return
	}
	httpapi.JSON(c, 200, gin.H{"items": rows, "total": total, "limit": limit, "offset": offset, "has_more": offset+len(rows) < total})
}

func safeCSV(value string) string {
	trimmed := strings.TrimLeft(value, " \t\r\n")
	if trimmed != "" && strings.ContainsRune("=+-@", rune(trimmed[0])) {
		return "'" + value
	}
	if strings.HasPrefix(value, "\t") || strings.HasPrefix(value, "\r") || strings.HasPrefix(value, "\n") {
		return "'" + value
	}
	return value
}

func (s *service) exportProfileActivities(c *gin.Context) {
	scope, id, err := s.profileScope(c)
	if err != nil {
		data.Fail(c, err)
		return
	}
	where, args, err := s.profileActivityQuery(c, scope, id, true)
	if err != nil {
		data.Fail(c, err)
		return
	}
	rows, err := s.deps.DB.SQL.QueryContext(c.Request.Context(), "SELECT a.created_at,p.identifier,w.sequence_id,w.name,a.action,a.field_name,a.old_value,a.new_value FROM activities a JOIN work_items w ON w.id=a.work_item_id JOIN projects p ON p.id=w.project_id WHERE "+where+" ORDER BY a.created_at DESC,a.id DESC LIMIT 10001", args...)
	if err != nil {
		data.Fail(c, err)
		return
	}
	defer rows.Close()
	var output bytes.Buffer
	w := csv.NewWriter(&output)
	_ = w.Write([]string{"Date", "Work item", "Title", "Action", "Field", "Before", "After"})
	count := 0
	for rows.Next() {
		count++
		if count > 10000 {
			data.Fail(c, data.Invalid("Activity export exceeds 10000 entries; narrow the dates or project filter"))
			return
		}
		var when time.Time
		var identifier, title, action, field string
		var sequence int
		var before, after json.RawMessage
		if err = rows.Scan(&when, &identifier, &sequence, &title, &action, &field, &before, &after); err != nil {
			data.Fail(c, err)
			return
		}
		record := []string{when.UTC().Format(time.RFC3339), fmt.Sprintf("%s-%d", identifier, sequence), title, action, field, string(before), string(after)}
		for i := range record {
			record[i] = safeCSV(record[i])
		}
		if err = w.Write(record); err != nil {
			data.Fail(c, err)
			return
		}
	}
	if err = rows.Err(); err != nil {
		data.Fail(c, err)
		return
	}
	w.Flush()
	if err = w.Error(); err != nil {
		data.Fail(c, err)
		return
	}
	c.Header("Content-Disposition", `attachment; filename="member-activities.csv"`)
	c.Header("Cache-Control", "private, no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(http.StatusOK, "text/csv; charset=utf-8", output.Bytes())
}
