// Package resources provides project-scoped resource calendars, conserved minute
// projections and a deterministic constraint scheduler. It never edits work items
// directly; proposal assignments are applied through requirements business commands.
package resources

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"my-jira/apps/api/internal/support/data"
)

const DateFormat = "2006-01-02"
const AlgorithmVersion = "capacity-topological-v1"

type AllocationWeight struct {
	MemberID uuid.UUID `json:"member_id"`
	Weight   int       `json:"weight"`
}

type Member struct {
	MemberID             uuid.UUID       `json:"member_id"`
	DisplayName          string          `json:"display_name"`
	Skills               []string        `json:"skills"`
	WeekdayMinutes       []*int          `json:"weekday_minutes"`
	ProjectMinutesPerDay *int            `json:"project_minutes_per_day"`
	Exceptions           map[string]*int `json:"exceptions"`
	Version              int64           `json:"version"`
}

type Task struct {
	ID                   uuid.UUID          `json:"id"`
	Name                 string             `json:"name"`
	Version              int64              `json:"version"`
	ParentID             *uuid.UUID         `json:"parent_id"`
	RequirementType      string             `json:"requirement_type"`
	StateID              uuid.UUID          `json:"state_id"`
	StateGroup           string             `json:"state_group"`
	Priority             string             `json:"priority"`
	StartDate            string             `json:"start_date"`
	TargetDate           string             `json:"target_date"`
	EstimatedMinutes     *int               `json:"estimated_minutes"`
	RemainingMinutes     *int               `json:"remaining_minutes"`
	AssigneeIDs          []uuid.UUID        `json:"assignee_ids"`
	AllocationWeights    []AllocationWeight `json:"allocation_weights"`
	RequiredSkills       []string           `json:"required_skills"`
	PlanningLocked       bool               `json:"planning_locked"`
	CycleID              *uuid.UUID         `json:"cycle_id"`
	CommitmentCycleID    *uuid.UUID         `json:"commitment_cycle_id"`
	Executable           bool               `json:"executable"`
	CommitmentStartDate  string             `json:"commitment_start_date"`
	CommitmentTargetDate string             `json:"commitment_target_date"`
	Dependencies         []uuid.UUID        `json:"dependencies"`
}

func (t Task) Finished() bool { return t.StateGroup == "completed" || t.StateGroup == "cancelled" }
func (t Task) Protected() bool {
	return t.PlanningLocked || t.Finished() || t.StateGroup == "started" || !t.Executable
}

type Snapshot struct {
	Timezone string   `json:"timezone"`
	Revision int64    `json:"revision"`
	Members  []Member `json:"members"`
	Tasks    []Task   `json:"tasks"`
}

type Conflict struct {
	Type          string    `json:"type"`
	TaskID        uuid.UUID `json:"task_id,omitempty"`
	MemberID      uuid.UUID `json:"member_id,omitempty"`
	RelatedTaskID uuid.UUID `json:"related_task_id,omitempty"`
	Date          string    `json:"date,omitempty"`
	Message       string    `json:"message"`
	Severity      string    `json:"severity"`
}

// UUID is an array, so encoding/json's omitempty does not omit uuid.Nil itself.
// Unscoped conflicts must not expose a fake zero-ID task/member link.
func (c Conflict) MarshalJSON() ([]byte, error) {
	out := map[string]any{"type": c.Type, "message": c.Message, "severity": c.Severity}
	if c.TaskID != uuid.Nil {
		out["task_id"] = c.TaskID
	}
	if c.MemberID != uuid.Nil {
		out["member_id"] = c.MemberID
	}
	if c.RelatedTaskID != uuid.Nil {
		out["related_task_id"] = c.RelatedTaskID
	}
	if c.Date != "" {
		out["date"] = c.Date
	}
	return json.Marshal(out)
}

type DayLoad struct {
	Date             string            `json:"date"`
	CapacityMinutes  *int              `json:"capacity_minutes"`
	AllocatedMinutes int               `json:"allocated_minutes"`
	SelectedMinutes  int               `json:"selected_minutes"`
	Unknown          bool              `json:"unknown"`
	SelectedUnknown  bool              `json:"selected_unknown"`
	OverCapacity     bool              `json:"over_capacity"`
	TaskIDs          []uuid.UUID       `json:"task_ids"`
	TaskMinutes      map[uuid.UUID]int `json:"task_minutes"`
	UnknownTaskIDs   []uuid.UUID       `json:"unknown_task_ids"`
}

type WeekLoad struct {
	StartDate        string `json:"start_date"`
	CapacityMinutes  *int   `json:"capacity_minutes"`
	AllocatedMinutes int    `json:"allocated_minutes"`
	SelectedMinutes  int    `json:"selected_minutes"`
	Unknown          bool   `json:"unknown"`
	SelectedUnknown  bool   `json:"selected_unknown"`
	OverCapacity     bool   `json:"over_capacity"`
}

type MemberLoad struct {
	Member
	Days  []DayLoad  `json:"days"`
	Weeks []WeekLoad `json:"weeks"`
}

type Projection struct {
	Timezone    string       `json:"timezone"`
	StartDate   string       `json:"start_date"`
	EndDate     string       `json:"end_date"`
	Members     []MemberLoad `json:"members"`
	Tasks       []Task       `json:"tasks"`
	Conflicts   []Conflict   `json:"conflicts"`
	Unassigned  []uuid.UUID  `json:"unassigned"`
	Unestimated []uuid.UUID  `json:"unestimated"`
	Unscheduled []uuid.UUID  `json:"unscheduled"`
	Scope       string       `json:"scope"`
}

// Capacity takes the smaller of calendar availability and the project's quota.
// An explicit zero dominates an unknown limit; all other unknowns stay unknown.
func (m Member) Capacity(date time.Time) *int {
	var calendar *int
	if v, ok := m.Exceptions[date.Format(DateFormat)]; ok {
		calendar = v
	} else if len(m.WeekdayMinutes) == 7 {
		calendar = m.WeekdayMinutes[int(date.Weekday())]
	}
	quota := m.ProjectMinutesPerDay
	if calendar != nil && *calendar == 0 || quota != nil && *quota == 0 {
		return number(0)
	}
	if calendar == nil || quota == nil {
		return nil
	}
	if *calendar < *quota {
		return number(*calendar)
	}
	return number(*quota)
}

func number(v int) *int { return &v }

func dates(start, end, timezone string, maximum int) ([]time.Time, error) {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, data.Invalid("Project timezone is not a valid IANA timezone")
	}
	s, err := time.ParseInLocation(DateFormat, start, loc)
	if err != nil {
		return nil, data.Invalid("start_date must use YYYY-MM-DD")
	}
	e, err := time.ParseInLocation(DateFormat, end, loc)
	if err != nil {
		return nil, data.Invalid("end_date must use YYYY-MM-DD")
	}
	if e.Before(s) {
		return nil, data.Invalid("end_date cannot precede start_date")
	}
	result := []time.Time{}
	for current := s; !current.After(e); current = current.AddDate(0, 0, 1) {
		if len(result) == maximum {
			return nil, data.Invalid(fmt.Sprintf("Date range supports at most %d days", maximum))
		}
		result = append(result, current)
	}
	return result, nil
}

func validateMember(m *Member) error {
	if len(m.WeekdayMinutes) != 7 {
		return data.Invalid("weekday_minutes must contain Sunday through Saturday (7 nullable minute values)")
	}
	validMinutes := func(v *int) bool { return v == nil || *v >= 0 && *v <= 1440 }
	if !validMinutes(m.ProjectMinutesPerDay) {
		return data.Invalid("project_minutes_per_day must be null or between 0 and 1440")
	}
	for _, v := range m.WeekdayMinutes {
		if !validMinutes(v) {
			return data.Invalid("weekday_minutes values must be null or between 0 and 1440")
		}
	}
	if len(m.Exceptions) > 1000 {
		return data.Invalid("At most 1000 calendar exceptions are supported")
	}
	for date, v := range m.Exceptions {
		if _, err := time.Parse(DateFormat, date); err != nil || !validMinutes(v) {
			return data.Invalid("exceptions must map YYYY-MM-DD dates to null or minutes between 0 and 1440")
		}
	}
	if len(m.Skills) > 100 {
		return data.Invalid("At most 100 skills are supported")
	}
	seen := map[string]bool{}
	clean := []string{}
	for _, skill := range m.Skills {
		skill = strings.ToLower(strings.TrimSpace(skill))
		if skill == "" || len([]rune(skill)) > 80 {
			return data.Invalid("Skills must contain between 1 and 80 characters")
		}
		if !seen[skill] {
			clean = append(clean, skill)
			seen[skill] = true
		}
	}
	sort.Strings(clean)
	m.Skills = clean
	if m.Exceptions == nil {
		m.Exceptions = map[string]*int{}
	}
	return nil
}

func hasSkills(m Member, required []string) bool {
	known := map[string]bool{}
	for _, skill := range m.Skills {
		known[strings.ToLower(strings.TrimSpace(skill))] = true
	}
	for _, skill := range required {
		if !known[strings.ToLower(strings.TrimSpace(skill))] {
			return false
		}
	}
	return true
}

// Apportion uses largest integer remainders with UUID ordering as the stable tie
// breaker. It conserves minutes exactly and defaults an initial assignment to equal
// shares. Explicit weights must describe precisely the current assignee set.
func Apportion(total int, members []uuid.UUID, weights []AllocationWeight) (map[uuid.UUID]int, error) {
	if total < 0 {
		return nil, data.Invalid("Remaining minutes cannot be negative")
	}
	ids := append([]uuid.UUID(nil), members...)
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
	w := map[uuid.UUID]int{}
	for i, id := range ids {
		if id == uuid.Nil || i > 0 && ids[i-1] == id {
			return nil, data.Invalid("Assignees must be distinct valid members")
		}
		w[id] = 1
	}
	if len(ids) == 0 {
		return nil, data.Invalid("A workload requires at least one assignee")
	}
	if len(weights) > 0 {
		if len(weights) != len(ids) {
			return nil, data.Invalid("Allocation weights must cover every assignee exactly once")
		}
		seen := map[uuid.UUID]bool{}
		for _, weight := range weights {
			if _, ok := w[weight.MemberID]; !ok || seen[weight.MemberID] || weight.Weight < 0 || weight.Weight > 1000000 {
				return nil, data.Invalid("Invalid allocation weight or assignee")
			}
			seen[weight.MemberID] = true
			w[weight.MemberID] = weight.Weight
		}
	}
	var sum int64
	for _, v := range w {
		sum += int64(v)
	}
	if sum == 0 {
		return nil, data.Invalid("At least one allocation weight must be positive")
	}
	type fraction struct {
		id        uuid.UUID
		remainder int64
	}
	fractions := []fraction{}
	result := map[uuid.UUID]int{}
	allocated := 0
	for _, id := range ids {
		product := int64(total) * int64(w[id])
		result[id] = int(product / sum)
		allocated += result[id]
		fractions = append(fractions, fraction{id, product % sum})
	}
	sort.SliceStable(fractions, func(i, j int) bool { return fractions[i].remainder > fractions[j].remainder })
	for i := 0; i < total-allocated; i++ {
		result[fractions[i].id]++
	}
	return result, nil
}
