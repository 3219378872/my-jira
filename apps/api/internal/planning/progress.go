package planning

import (
	"database/sql"
	"encoding/json"
	"math"
	"sort"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/httpapi"
	"my-jira/apps/api/internal/platform/identity"
	"my-jira/apps/api/internal/support/data"
)

type progressPerson struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	AvatarURL string    `json:"avatar_url,omitempty"`
	Color     string    `json:"color,omitempty"`
}
type progressItem struct {
	ID            uuid.UUID        `json:"id"`
	CreatedBy     uuid.UUID        `json:"created_by"`
	StateID       uuid.UUID        `json:"state_id"`
	StateName     string           `json:"state_name"`
	StateGroup    string           `json:"state_group"`
	StateColor    string           `json:"state_color"`
	StatePosition float64          `json:"state_position"`
	Estimate      float64          `json:"estimate"`
	CompletedAt   *time.Time       `json:"completed_at"`
	TargetDate    string           `json:"target_date"`
	Assignees     []progressPerson `json:"assignees"`
	Labels        []progressPerson `json:"labels"`
}
type progressSnapshot struct {
	CapturedAt time.Time      `json:"captured_at"`
	StartDate  string         `json:"start_date"`
	EndDate    string         `json:"end_date"`
	Items      []progressItem `json:"items"`
}

func activePlanningItems(alias string) string {
	return alias + ".deleted_at IS NULL AND " + alias + ".archived_at IS NULL AND NOT " + alias + ".is_draft AND NOT EXISTS(SELECT 1 FROM intake_items ip WHERE ip.work_item_id=" + alias + ".id AND ip.deleted_at IS NULL AND ip.status<>'accepted')"
}

func (s *service) collectProgress(c *gin.Context, q database.DBTX, spec resource, scope identity.Scope, id uuid.UUID, freeze bool) (progressSnapshot, bool, error) {
	var snapshot progressSnapshot
	var start, end sql.NullTime
	var raw []byte
	endColumn := "end_date"
	projection := "progress_snapshot"
	if spec.kind == "module" {
		endColumn = "target_date"
		projection = "NULL::jsonb"
	}
	err := q.QueryRowContext(c.Request.Context(), "SELECT start_date,"+endColumn+","+projection+" FROM "+spec.table+" WHERE id=$1 AND workspace_id=$2 AND project_id=$3 AND deleted_at IS NULL", id, scope.WorkspaceID, scope.ProjectID).Scan(&start, &end, &raw)
	if err != nil {
		return snapshot, false, err
	}
	if !freeze && c.Query("live") != "true" && len(raw) > 0 && string(raw) != "null" {
		if err = json.Unmarshal(raw, &snapshot); err != nil {
			return snapshot, false, err
		}
		return snapshot, true, nil
	}
	snapshot.CapturedAt = time.Now().UTC()
	if start.Valid {
		snapshot.StartDate = start.Time.Format("2006-01-02")
	}
	if end.Valid {
		snapshot.EndDate = end.Time.Format("2006-01-02")
	}
	query := `SELECT jsonb_build_object('id',w.id,'created_by',w.created_by,'state_id',st.id,'state_name',st.name,'state_group',st.group_name,'state_color',st.color,'state_position',st.position,'estimate',COALESCE(w.estimate,0),'completed_at',w.completed_at,'target_date',COALESCE(w.target_date::text,''),'assignees',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',u.id,'name',u.display_name,'avatar_url',u.avatar_url) ORDER BY u.id) FROM work_item_assignees wa JOIN users u ON u.id=wa.user_id AND u.is_active AND u.deleted_at IS NULL JOIN workspace_members wm ON wm.workspace_id=w.workspace_id AND wm.user_id=u.id AND wm.is_active AND wm.deleted_at IS NULL WHERE wa.work_item_id=w.id AND wa.deleted_at IS NULL),'[]'::jsonb),'labels',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',l.id,'name',l.name,'color',l.color) ORDER BY l.id) FROM work_item_labels wl JOIN labels l ON l.id=wl.label_id AND l.deleted_at IS NULL WHERE wl.work_item_id=w.id AND wl.deleted_at IS NULL),'[]'::jsonb)) FROM work_items w JOIN states st ON st.id=w.state_id JOIN ` + spec.kind + `_items pi ON pi.work_item_id=w.id AND pi.deleted_at IS NULL WHERE pi.` + spec.kind + `_id=$1 AND w.workspace_id=$2 AND w.project_id=$3 AND ` + activePlanningItems("w") + ` ORDER BY w.id`
	if freeze {
		query += " FOR SHARE OF w"
	}
	rows, err := data.Many(c, q, query, id, scope.WorkspaceID, scope.ProjectID)
	if err != nil {
		return snapshot, false, err
	}
	snapshot.Items = make([]progressItem, 0, len(rows))
	for _, raw := range rows {
		var item progressItem
		if err = json.Unmarshal(raw, &item); err != nil {
			return snapshot, false, err
		}
		snapshot.Items = append(snapshot.Items, item)
	}
	return snapshot, false, nil
}

type progressBucket struct {
	ID                *uuid.UUID `json:"id"`
	StateID           *uuid.UUID `json:"state_id,omitempty"`
	Name              string     `json:"name"`
	Group             string     `json:"group,omitempty"`
	Color             string     `json:"color,omitempty"`
	AvatarURL         string     `json:"avatar_url,omitempty"`
	Count             int        `json:"count"`
	Completed         int        `json:"completed"`
	Cancelled         int        `json:"cancelled"`
	Started           int        `json:"started"`
	Estimate          float64    `json:"estimate"`
	EstimateCompleted float64    `json:"estimate_completed"`
	position          float64
}

func addProgress(bucket *progressBucket, item progressItem) {
	bucket.Count++
	bucket.Estimate += item.Estimate
	switch item.StateGroup {
	case "completed":
		bucket.Completed++
		bucket.EstimateCompleted += item.Estimate
	case "cancelled":
		bucket.Cancelled++
	case "started":
		bucket.Started++
	}
}

func sortedProgress(buckets map[uuid.UUID]*progressBucket, byState bool) []*progressBucket {
	result := make([]*progressBucket, 0, len(buckets))
	for _, bucket := range buckets {
		result = append(result, bucket)
	}
	sort.Slice(result, func(i, j int) bool {
		if byState && result[i].position != result[j].position {
			return result[i].position < result[j].position
		}
		if result[i].Name != result[j].Name {
			return result[i].Name < result[j].Name
		}
		if result[i].ID == nil {
			return result[j].ID != nil
		}
		if result[j].ID == nil {
			return false
		}
		return result[i].ID.String() < result[j].ID.String()
	})
	return result
}

func progressData(snapshot progressSnapshot, scope identity.Scope, frozen bool) gin.H {
	items := make([]progressItem, 0, len(snapshot.Items))
	for _, item := range snapshot.Items {
		if scope.Role >= identity.Member || scope.GuestCanViewAll || item.CreatedBy == scope.Actor.UserID {
			items = append(items, item)
		}
	}
	states, assignees, labels := map[uuid.UUID]*progressBucket{}, map[uuid.UUID]*progressBucket{}, map[uuid.UUID]*progressBucket{}
	total := &progressBucket{}
	overdue, backlog, unstarted := 0, 0, 0
	today := time.Now().UTC().Format("2006-01-02")
	if frozen {
		today = snapshot.CapturedAt.Format("2006-01-02")
	}
	for _, item := range items {
		addProgress(total, item)
		if item.TargetDate != "" && item.TargetDate < today && item.StateGroup != "completed" && item.StateGroup != "cancelled" {
			overdue++
		}
		if item.StateGroup == "backlog" {
			backlog++
		}
		if item.StateGroup == "unstarted" {
			unstarted++
		}
		if states[item.StateID] == nil {
			id := item.StateID
			states[id] = &progressBucket{ID: &id, StateID: &id, Name: item.StateName, Group: item.StateGroup, Color: item.StateColor, position: item.StatePosition}
		}
		addProgress(states[item.StateID], item)
		for _, group := range []struct {
			people  []progressPerson
			buckets map[uuid.UUID]*progressBucket
			empty   string
		}{{item.Assignees, assignees, "Unassigned"}, {item.Labels, labels, "No label"}} {
			people := group.people
			if len(people) == 0 {
				people = []progressPerson{{Name: group.empty}}
			}
			for _, person := range people {
				if group.buckets[person.ID] == nil {
					bucket := &progressBucket{Name: person.Name, Color: person.Color, AvatarURL: person.AvatarURL}
					if person.ID != uuid.Nil {
						id := person.ID
						bucket.ID = &id
					}
					group.buckets[person.ID] = bucket
				}
				addProgress(group.buckets[person.ID], item)
			}
		}
	}
	percentage := 0.0
	if total.Count > 0 {
		percentage = 100 * float64(total.Completed) / float64(total.Count)
	}
	chart := []gin.H{}
	start, e1 := time.Parse("2006-01-02", snapshot.StartDate)
	end, e2 := time.Parse("2006-01-02", snapshot.EndDate)
	stride := 1
	if e1 == nil && e2 == nil && !end.Before(start) {
		duration := int((end.Unix() - start.Unix()) / 86400)
		stride = int(math.Max(1, math.Ceil(float64(duration+1)/3660)))
		for day := 0; day <= duration; day += stride {
			date := start.AddDate(0, 0, day)
			remaining, estimated := total.Count, total.Estimate
			for _, item := range items {
				if item.StateGroup == "completed" && item.CompletedAt != nil && item.CompletedAt.UTC().Format("2006-01-02") <= date.Format("2006-01-02") {
					remaining--
					estimated -= item.Estimate
				}
			}
			fraction := 0.0
			if duration > 0 {
				fraction = 1 - float64(day)/float64(duration)
			}
			row := gin.H{"date": date.Format("2006-01-02"), "ideal": float64(total.Count) * fraction, "estimate_ideal": total.Estimate * fraction, "remaining": nil, "estimate_remaining": nil}
			if row["date"].(string) <= today {
				row["remaining"] = remaining
				row["estimate_remaining"] = math.Max(0, estimated)
			}
			chart = append(chart, row)
			if day < duration && day+stride > duration {
				day = duration - stride
			}
		}
	}
	var captured any
	if frozen {
		captured = snapshot.CapturedAt
	}
	return gin.H{"total": total.Count, "completed": total.Completed, "cancelled": total.Cancelled, "started": total.Started, "backlog": backlog, "unstarted": unstarted, "overdue": overdue, "completion_percentage": percentage, "estimate_total": total.Estimate, "estimate_completed": total.EstimateCompleted, "states": sortedProgress(states, true), "assignees": sortedProgress(assignees, false), "labels": sortedProgress(labels, false), "burndown": chart, "burndown_step_days": stride, "start_date": snapshot.StartDate, "end_date": snapshot.EndDate, "is_snapshot": frozen, "snapshot_at": captured}
}

func (s *service) progress(spec resource) gin.HandlerFunc {
	return func(c *gin.Context) {
		scope, err := s.scope(c, identity.Guest)
		if err != nil {
			data.Fail(c, err)
			return
		}
		id, err := httpapi.UUIDParam(c, spec.param)
		if err != nil {
			data.Fail(c, err)
			return
		}
		snapshot, frozen, err := s.collectProgress(c, s.deps.DB.SQL, spec, scope, id, false)
		if err != nil {
			data.Fail(c, err)
			return
		}
		httpapi.JSON(c, 200, progressData(snapshot, scope, frozen))
	}
}
