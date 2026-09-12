package resources

import (
	"context"
	"database/sql"
	"encoding/json"
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
	"my-jira/apps/api/internal/workitems"
)

type Service struct{ deps platform.Dependencies }

func NewService(deps platform.Dependencies) *Service { return &Service{deps: deps} }

func Register(r *gin.RouterGroup, deps platform.Dependencies) {
	s := NewService(deps)
	p := r.Group("/workspaces/:workspaceID/projects/:projectID/resources")
	p.GET("", s.list)
	p.PATCH("", s.timezone)
	p.PUT("/members/:memberID", s.putMember)
	p.GET("/load", s.load)
	p.POST("/schedule", s.schedule)
}

func (s *Service) scope(c *gin.Context, minimum identity.Role) (identity.Scope, error) {
	actor, err := httpapi.Actor(c)
	if err != nil {
		return identity.Scope{}, err
	}
	wid, err := httpapi.UUIDParam(c, "workspaceID")
	if err != nil {
		return identity.Scope{}, err
	}
	pid, err := httpapi.UUIDParam(c, "projectID")
	if err != nil {
		return identity.Scope{}, err
	}
	scope, err := s.deps.Policy.Project(c.Request.Context(), actor, wid, pid, minimum)
	if err == nil {
		err = workitems.RequireRequirements(c.Request.Context(), s.deps.DB.SQL, wid, pid)
	}
	return scope, err
}

// Snapshot reads all current authorized project work, not a web list page. Callers
// that need an input revision boundary pass the same transaction as their revision
// read; automation rechecks this snapshot after applying its command batch.
func (s *Service) Snapshot(ctx context.Context, q database.DBTX, scope identity.Scope) (Snapshot, error) {
	scope, err := (&identity.SQLPolicy{DB: q}).Project(ctx, scope.Actor, scope.WorkspaceID, scope.ProjectID, identity.Guest)
	if err != nil {
		return Snapshot{}, err
	}
	out := Snapshot{Members: []Member{}, Tasks: []Task{}}
	if err = q.QueryRowContext(ctx, `SELECT timezone FROM projects WHERE id=$1 AND workspace_id=$2 AND deleted_at IS NULL`, scope.ProjectID, scope.WorkspaceID).Scan(&out.Timezone); err != nil {
		return out, err
	}
	if _, err = time.LoadLocation(out.Timezone); err != nil {
		return out, data.Invalid("Project timezone must be a valid IANA timezone")
	}
	if err = q.QueryRowContext(ctx, `SELECT COALESCE((SELECT revision FROM project_revisions WHERE workspace_id=$1 AND project_id=$2),0)`, scope.WorkspaceID, scope.ProjectID).Scan(&out.Revision); err != nil {
		return out, err
	}
	rows, err := q.QueryContext(ctx, `SELECT jsonb_build_object('member_id',u.id,'display_name',u.display_name,
 'skills',COALESCE(to_jsonb(r.skills),'[]'::jsonb),'weekday_minutes',COALESCE(r.weekday_minutes,'[null,null,null,null,null,null,null]'::jsonb),
 'project_minutes_per_day',r.project_minutes_per_day,'exceptions',COALESCE(r.exceptions,'{}'::jsonb),'version',COALESCE(r.version,0))
 FROM project_members pm JOIN projects p ON p.id=pm.project_id AND p.workspace_id=pm.workspace_id
 JOIN workspace_members wm ON wm.workspace_id=pm.workspace_id AND wm.user_id=pm.user_id AND wm.is_active AND wm.deleted_at IS NULL
 JOIN users u ON u.id=pm.user_id AND u.is_active AND u.deleted_at IS NULL
 LEFT JOIN project_resources r ON r.project_id=pm.project_id AND r.workspace_id=pm.workspace_id AND r.member_id=pm.user_id
 WHERE pm.workspace_id=$1 AND pm.project_id=$2 AND pm.is_active AND pm.deleted_at IS NULL AND p.deleted_at IS NULL ORDER BY u.id`, scope.WorkspaceID, scope.ProjectID)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			break
		}
		var member Member
		if err = json.Unmarshal(raw, &member); err != nil {
			break
		}
		out.Members = append(out.Members, member)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return out, err
	}
	guest := scope.Role < identity.Member && !scope.GuestCanViewAll
	rows, err = q.QueryContext(ctx, `SELECT jsonb_build_object(
 'id',w.id,'name',w.name,'version',w.version,'parent_id',w.parent_id,'requirement_type',COALESCE(w.requirement_type,''),
 'state_id',w.state_id,'state_group',st.group_name,'priority',w.priority,'start_date',COALESCE(to_char(w.start_date,'YYYY-MM-DD'),''),
 'target_date',COALESCE(to_char(w.target_date,'YYYY-MM-DD'),''),'estimated_minutes',w.estimated_minutes,'remaining_minutes',w.remaining_minutes,
 'assignee_ids',COALESCE((SELECT jsonb_agg(a.user_id ORDER BY a.user_id) FROM work_item_assignees a WHERE a.work_item_id=w.id AND a.workspace_id=w.workspace_id AND a.project_id=w.project_id AND a.deleted_at IS NULL),'[]'::jsonb),
 'allocation_weights',w.allocation_weights,'required_skills',w.required_skills,'planning_locked',w.planning_locked,
 'cycle_id',(SELECT ci.cycle_id FROM cycle_items ci JOIN cycles cy ON cy.id=ci.cycle_id AND cy.deleted_at IS NULL WHERE ci.work_item_id=w.id AND ci.deleted_at IS NULL LIMIT 1),
 'executable',COALESCE(w.requirement_type NOT IN ('epic','story'),true) AND NOT EXISTS(SELECT 1 FROM work_items ch WHERE ch.parent_id=w.id AND ch.workspace_id=w.workspace_id AND ch.project_id=w.project_id AND ch.deleted_at IS NULL AND ch.archived_at IS NULL AND NOT ch.is_draft),
 'dependencies',COALESCE((SELECT jsonb_agg(r.source_id ORDER BY r.source_id) FROM work_item_relations r
 JOIN work_items source ON source.id=r.source_id AND source.workspace_id=w.workspace_id AND source.project_id=w.project_id AND source.deleted_at IS NULL
 JOIN states source_state ON source_state.id=source.state_id AND source_state.project_id=w.project_id
 WHERE r.target_id=w.id AND r.workspace_id=w.workspace_id AND r.project_id=w.project_id AND r.deleted_at IS NULL AND r.relation_type='blocks' AND (source.archived_at IS NULL AND NOT source.is_draft OR source_state.group_name NOT IN ('completed','cancelled')) AND (NOT $4::boolean OR source.created_by=$3)),'[]'::jsonb))
 FROM work_items w JOIN states st ON st.id=w.state_id AND st.project_id=w.project_id AND st.workspace_id=w.workspace_id
 WHERE w.workspace_id=$1 AND w.project_id=$2 AND w.deleted_at IS NULL AND w.archived_at IS NULL AND NOT w.is_draft AND (NOT $4::boolean OR w.created_by=$3)
 ORDER BY w.id`, scope.WorkspaceID, scope.ProjectID, scope.Actor.UserID, guest)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			break
		}
		var task Task
		if err = json.Unmarshal(raw, &task); err != nil {
			break
		}
		out.Tasks = append(out.Tasks, task)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return out, err
	}
	// Only visible ancestors contribute metadata, preventing a hidden Story from
	// leaking through a child's resource projection.
	byID := map[uuid.UUID]Task{}
	for _, task := range out.Tasks {
		byID[task.ID] = task
	}
	type cycleWindow struct{ start, end string }
	cycles := map[uuid.UUID]cycleWindow{}
	rows, err = q.QueryContext(ctx, `SELECT id,COALESCE(to_char(start_date,'YYYY-MM-DD'),''),COALESCE(to_char(end_date,'YYYY-MM-DD'),'') FROM cycles WHERE workspace_id=$1 AND project_id=$2 AND deleted_at IS NULL`, scope.WorkspaceID, scope.ProjectID)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var id uuid.UUID
		var window cycleWindow
		if err = rows.Scan(&id, &window.start, &window.end); err != nil {
			break
		}
		cycles[id] = window
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return out, err
	}
	for i, task := range out.Tasks {
		out.Tasks[i].CommitmentCycleID = task.CycleID
		seen := map[uuid.UUID]bool{}
		for task.ParentID != nil && !seen[*task.ParentID] {
			seen[*task.ParentID] = true
			parent, ok := byID[*task.ParentID]
			if !ok {
				break
			}
			if parent.RequirementType == "story" {
				out.Tasks[i].CommitmentCycleID = parent.CycleID
				out.Tasks[i].CommitmentStartDate = parent.StartDate
				out.Tasks[i].CommitmentTargetDate = parent.TargetDate
				if parent.CycleID != nil {
					window := cycles[*parent.CycleID]
					if window.start > out.Tasks[i].CommitmentStartDate {
						out.Tasks[i].CommitmentStartDate = window.start
					}
					if window.end != "" && (out.Tasks[i].CommitmentTargetDate == "" || window.end < out.Tasks[i].CommitmentTargetDate) {
						out.Tasks[i].CommitmentTargetDate = window.end
					}
				}
				break
			}
			task = parent
		}
	}
	return out, nil
}

// readSnapshot fixes the data boundary with PostgreSQL repeatable read, then
// checks current membership again outside that snapshot before returning it.
func (s *Service) readSnapshot(ctx context.Context, scope identity.Scope) (Snapshot, error) {
	tx, err := s.deps.DB.SQL.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return Snapshot{}, err
	}
	defer tx.Rollback()
	inputScope, err := (&identity.SQLPolicy{DB: tx}).Project(ctx, scope.Actor, scope.WorkspaceID, scope.ProjectID, identity.Guest)
	if err != nil {
		return Snapshot{}, err
	}
	snapshot, err := s.Snapshot(ctx, tx, scope)
	if err != nil {
		return snapshot, err
	}
	if err = tx.Commit(); err != nil {
		return snapshot, err
	}
	current, err := s.deps.Policy.Project(ctx, scope.Actor, scope.WorkspaceID, scope.ProjectID, identity.Guest)
	if err == nil && current.Role < identity.Member && ((inputScope.Role >= identity.Member) || inputScope.GuestCanViewAll && !current.GuestCanViewAll) {
		err = data.Forbidden()
	}
	return snapshot, err
}

func (s *Service) list(c *gin.Context) {
	scope, err := s.scope(c, identity.Guest)
	if err != nil {
		data.Fail(c, err)
		return
	}
	snapshot, err := s.readSnapshot(c.Request.Context(), scope)
	if err != nil {
		data.Fail(c, err)
		return
	}
	httpapi.JSON(c, 200, gin.H{"timezone": snapshot.Timezone, "members": snapshot.Members})
}

func (s *Service) load(c *gin.Context) {
	scope, err := s.scope(c, identity.Guest)
	if err != nil {
		data.Fail(c, err)
		return
	}
	snapshot, err := s.readSnapshot(c.Request.Context(), scope)
	if err != nil {
		data.Fail(c, err)
		return
	}
	projection, err := Project(snapshot, c.Query("start_date"), c.Query("end_date"))
	if err != nil {
		data.Fail(c, err)
		return
	}
	if err = filterProjection(&projection, c); err != nil {
		data.Fail(c, err)
		return
	}
	httpapi.JSON(c, 200, projection)
}

func (s *Service) schedule(c *gin.Context) {
	scope, err := s.scope(c, identity.Member)
	if err != nil {
		data.Fail(c, err)
		return
	}
	request, err := httpapi.Bind[ScheduleRequest](c)
	if err != nil {
		data.Fail(c, err)
		return
	}
	snapshot, err := s.readSnapshot(c.Request.Context(), scope)
	if err != nil {
		data.Fail(c, err)
		return
	}
	result, err := Schedule(snapshot, request)
	if err != nil {
		data.Fail(c, err)
		return
	}
	httpapi.JSON(c, 200, result)
}

// lockCurrentScope follows the shared graph -> resource row -> authorization
// lock order. Authorization is read after waiting, never from the HTTP precheck.
func lockCurrentScope(ctx context.Context, q database.DBTX, scope identity.Scope, memberID uuid.UUID) (identity.Scope, error) {
	if _, err := q.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, scope.ProjectID.String()); err != nil {
		return scope, err
	}
	for _, query := range []string{
		`SELECT id FROM users WHERE id IN ($1,$4) ORDER BY id FOR SHARE`,
		`SELECT id FROM workspace_members WHERE workspace_id=$2 AND user_id IN ($1,$4) ORDER BY id FOR SHARE`,
		`SELECT id FROM project_members WHERE workspace_id=$2 AND project_id=$3 AND user_id IN ($1,$4) ORDER BY id FOR SHARE`,
		`SELECT id FROM projects WHERE workspace_id=$2 AND id=$3 FOR SHARE`,
	} {
		// Each query names all parameters through a typed, harmless predicate so
		// PostgreSQL can infer unused bind parameter types consistently.
		query = "SELECT id FROM (" + query + ") locked WHERE $1::uuid IS NOT NULL AND $2::uuid IS NOT NULL AND $3::uuid IS NOT NULL AND $4::uuid IS NOT NULL"
		rows, err := q.QueryContext(ctx, query, scope.Actor.UserID, scope.WorkspaceID, scope.ProjectID, memberID)
		if err != nil {
			return scope, err
		}
		for rows.Next() {
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return scope, err
		}
	}
	current, err := workitems.CurrentScope(ctx, q, scope.Actor, scope.WorkspaceID, scope.ProjectID, identity.Admin)
	if err != nil {
		return current, err
	}
	return current, workitems.RequireRequirements(ctx, q, current.WorkspaceID, current.ProjectID)
}

func (s *Service) putMember(c *gin.Context) {
	scope, err := s.scope(c, identity.Admin)
	if err != nil {
		data.Fail(c, err)
		return
	}
	memberID, err := httpapi.UUIDParam(c, "memberID")
	if err != nil {
		data.Fail(c, err)
		return
	}
	input, err := data.Bind(c, "version", "skills", "weekday_minutes", "project_minutes_per_day", "exceptions")
	if err != nil {
		data.Fail(c, err)
		return
	}
	for _, key := range []string{"version", "skills", "weekday_minutes", "project_minutes_per_day", "exceptions"} {
		if _, ok := input[key]; !ok {
			data.Fail(c, data.Invalid(key+" is required"))
			return
		}
	}
	var member Member
	raw, err := json.Marshal(input)
	if err == nil {
		err = json.Unmarshal(raw, &member)
	}
	if err != nil || member.Version < 0 || string(input["version"]) == "null" || string(input["skills"]) == "null" || string(input["exceptions"]) == "null" {
		data.Fail(c, data.Invalid("Resource configuration must contain valid skills, calendar, exceptions and version"))
		return
	}
	member.MemberID = memberID
	if err = validateMember(&member); err != nil {
		data.Fail(c, err)
		return
	}
	err = s.deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		current, e := lockCurrentScope(c.Request.Context(), q, scope, memberID)
		if e != nil {
			return e
		}
		scope = current
		if e = q.QueryRowContext(c.Request.Context(), `SELECT u.display_name FROM project_members pm JOIN workspace_members wm ON wm.workspace_id=pm.workspace_id AND wm.user_id=pm.user_id AND wm.is_active AND wm.deleted_at IS NULL JOIN users u ON u.id=pm.user_id AND u.is_active AND u.deleted_at IS NULL WHERE pm.workspace_id=$1 AND pm.project_id=$2 AND pm.user_id=$3 AND pm.is_active AND pm.deleted_at IS NULL`, scope.WorkspaceID, scope.ProjectID, memberID).Scan(&member.DisplayName); e != nil {
			return e
		}
		var previous []byte
		var version int64
		e = q.QueryRowContext(c.Request.Context(), `SELECT to_jsonb(r),version FROM project_resources r WHERE workspace_id=$1 AND project_id=$2 AND member_id=$3 FOR UPDATE`, scope.WorkspaceID, scope.ProjectID, memberID).Scan(&previous, &version)
		if e != nil && e != sql.ErrNoRows {
			return e
		}
		if version != member.Version {
			return data.Conflict("Resource configuration changed; reload before saving")
		}
		weekdays, _ := json.Marshal(member.WeekdayMinutes)
		exceptions, _ := json.Marshal(member.Exceptions)
		if _, e = q.ExecContext(c.Request.Context(), `INSERT INTO project_resources(workspace_id,project_id,member_id,skills,weekday_minutes,project_minutes_per_day,exceptions,updated_by) VALUES($1,$2,$3,$4,$5::jsonb,$6,$7::jsonb,$8) ON CONFLICT(project_id,member_id) DO UPDATE SET skills=EXCLUDED.skills,weekday_minutes=EXCLUDED.weekday_minutes,project_minutes_per_day=EXCLUDED.project_minutes_per_day,exceptions=EXCLUDED.exceptions,updated_by=EXCLUDED.updated_by,version=project_resources.version+1,updated_at=now()`, scope.WorkspaceID, scope.ProjectID, memberID, member.Skills, string(weekdays), member.ProjectMinutesPerDay, string(exceptions), scope.Actor.UserID); e != nil {
			return e
		}
		member.Version = version + 1
		return recordResourceChange(c.Request.Context(), q, scope, memberID, previous, member)
	})
	if err != nil {
		data.Fail(c, err)
		return
	}
	httpapi.JSON(c, 200, member)
}

func (s *Service) timezone(c *gin.Context) {
	scope, err := s.scope(c, identity.Admin)
	if err != nil {
		data.Fail(c, err)
		return
	}
	input, err := data.Bind(c, "timezone")
	if err != nil {
		data.Fail(c, err)
		return
	}
	zone, err := input.String("timezone", true, 100)
	if err != nil {
		data.Fail(c, err)
		return
	}
	if _, err = time.LoadLocation(zone); err != nil {
		data.Fail(c, data.Invalid("timezone must be an IANA timezone"))
		return
	}
	err = s.deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		current, e := lockCurrentScope(c.Request.Context(), q, scope, scope.Actor.UserID)
		if e != nil {
			return e
		}
		scope = current
		var previous string
		if e = q.QueryRowContext(c.Request.Context(), `SELECT timezone FROM projects WHERE id=$1 AND workspace_id=$2 FOR UPDATE`, scope.ProjectID, scope.WorkspaceID).Scan(&previous); e != nil {
			return e
		}
		if _, e = q.ExecContext(c.Request.Context(), `UPDATE projects SET timezone=$3,updated_at=now() WHERE id=$1 AND workspace_id=$2`, scope.ProjectID, scope.WorkspaceID, zone); e != nil {
			return e
		}
		before, _ := json.Marshal(gin.H{"timezone": previous})
		return recordResourceChange(c.Request.Context(), q, scope, uuid.Nil, before, gin.H{"timezone": zone})
	})
	if err != nil {
		data.Fail(c, err)
		return
	}
	httpapi.JSON(c, 200, gin.H{"timezone": zone})
}

func recordResourceChange(ctx context.Context, q database.DBTX, scope identity.Scope, memberID uuid.UUID, before []byte, after any) error {
	var member any
	if memberID != uuid.Nil {
		member = memberID
	}
	var old any
	if len(before) > 0 {
		old = string(before)
	}
	value, err := json.Marshal(after)
	if err != nil {
		return err
	}
	if _, err = q.ExecContext(ctx, `INSERT INTO project_resource_history(id,workspace_id,project_id,member_id,actor_id,before_data,after_data) VALUES($1,$2,$3,$4,$5,$6::jsonb,$7::jsonb)`, uuid.New(), scope.WorkspaceID, scope.ProjectID, member, scope.Actor.UserID, old, string(value)); err != nil {
		return err
	}
	if _, err = q.ExecContext(ctx, `SELECT planning_emit_event($1,$2,'resources.updated',$3)`, scope.WorkspaceID, scope.ProjectID, member); err != nil {
		return err
	}
	return jobs.PublishEntityChange(ctx, q, scope, "resources.changed", scope.ProjectID, "updated", map[string]any{"member_id": member})
}

// Filters select task bars and member rows after full aggregation. Other tasks
// remain in daily load/contributors so a Sprint filter cannot hide overbooking.
func filterProjection(projection *Projection, c *gin.Context) error {
	filters := map[string]uuid.UUID{}
	backlog := c.Query("cycle_id") == "backlog"
	commitmentBacklog := c.Query("commitment_cycle_id") == "backlog"
	for _, key := range []string{"member_id", "cycle_id", "state_id", "epic_id", "commitment_cycle_id"} {
		if value := c.Query(key); value != "" {
			if key == "cycle_id" && backlog {
				continue
			}
			if key == "commitment_cycle_id" && commitmentBacklog {
				continue
			}
			id, err := uuid.Parse(value)
			if err != nil || id == uuid.Nil {
				return data.Invalid(key + " must be a UUID")
			}
			filters[key] = id
		}
	}
	itemIDs := map[uuid.UUID]bool{}
	if raw := c.Query("work_item_ids"); raw != "" {
		values := strings.Split(raw, ",")
		if len(values) > 500 {
			return data.Invalid("work_item_ids supports at most 500 IDs")
		}
		for _, value := range values {
			id, err := uuid.Parse(value)
			if err != nil || id == uuid.Nil {
				return data.Invalid("work_item_ids must be comma-separated UUIDs")
			}
			itemIDs[id] = true
		}
	}
	byID := map[uuid.UUID]Task{}
	for _, task := range projection.Tasks {
		if commitmentBacklog && task.CommitmentCycleID != nil {
			continue
		}
		if id, ok := filters["commitment_cycle_id"]; ok && (task.CommitmentCycleID == nil || *task.CommitmentCycleID != id) {
			continue
		}
		byID[task.ID] = task
	}
	search := strings.ToLower(strings.TrimSpace(c.Query("search")))
	skill := c.Query("skill")
	selectedMembers := map[uuid.UUID]bool{}
	members := []MemberLoad{}
	for _, member := range projection.Members {
		if id, ok := filters["member_id"]; ok && member.MemberID != id {
			continue
		}
		if skill != "" && !hasSkills(member.Member, []string{skill}) {
			continue
		}
		members = append(members, member)
		selectedMembers[member.MemberID] = true
	}
	projection.Members = members
	tasks := []Task{}
	for _, task := range projection.Tasks {
		if backlog && task.CycleID != nil {
			continue
		}
		if len(itemIDs) > 0 && !itemIDs[task.ID] {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(task.Name), search) {
			continue
		}
		if epic, ok := filters["epic_id"]; ok {
			ancestor := task
			seen := map[uuid.UUID]bool{}
			matched := ancestor.ID == epic
			for !matched && ancestor.ParentID != nil && !seen[*ancestor.ParentID] {
				seen[*ancestor.ParentID] = true
				parent, present := byID[*ancestor.ParentID]
				if !present {
					break
				}
				ancestor = parent
				matched = ancestor.ID == epic
			}
			if !matched {
				continue
			}
		}
		if id, ok := filters["cycle_id"]; ok && (task.CycleID == nil || *task.CycleID != id) {
			continue
		}
		if id, ok := filters["state_id"]; ok && task.StateID != id {
			continue
		}
		if _, ok := filters["member_id"]; ok || skill != "" {
			matched := false
			for _, id := range task.AssigneeIDs {
				if selectedMembers[id] {
					matched = true
				}
			}
			if !matched {
				continue
			}
		}
		tasks = append(tasks, task)
	}
	projection.Tasks = tasks
	selected := map[uuid.UUID]bool{}
	for _, task := range tasks {
		selected[task.ID] = true
	}
	calendar, err := dates(projection.StartDate, projection.EndDate, projection.Timezone, 366)
	if err != nil {
		return err
	}
	for i := range projection.Members {
		member := &projection.Members[i]
		for j := range member.Days {
			day := &member.Days[j]
			day.SelectedMinutes = 0
			day.SelectedUnknown = false
			for id, minutes := range day.TaskMinutes {
				if selected[id] {
					day.SelectedMinutes += minutes
				}
			}
			for _, id := range day.UnknownTaskIDs {
				if selected[id] {
					day.SelectedUnknown = true
				}
			}
		}
		member.Weeks = weekly(member.Days, calendar)
	}
	return nil
}
