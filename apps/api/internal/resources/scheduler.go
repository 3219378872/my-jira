package resources

import (
	"sort"
	"strings"

	"github.com/google/uuid"
	"my-jira/apps/api/internal/support/data"
)

type ScheduleRequest struct {
	StartDate    string      `json:"start_date"`
	EndDate      string      `json:"end_date"`
	TaskIDs      []uuid.UUID `json:"task_ids,omitempty"`
	MemberIDs    []uuid.UUID `json:"member_ids,omitempty"`
	HardDeadline string      `json:"hard_deadline,omitempty"`
}

type Assignment struct {
	ID                uuid.UUID          `json:"id"`
	Version           int64              `json:"version"`
	StartDate         string             `json:"start_date"`
	TargetDate        string             `json:"target_date"`
	AssigneeIDs       []uuid.UUID        `json:"assignee_ids"`
	AllocationWeights []AllocationWeight `json:"allocation_weights"`
	Reason            string             `json:"reason"`
}

type ScheduleResult struct {
	Assignments       []Assignment `json:"assignments"`
	Unscheduled       []Conflict   `json:"unscheduled"`
	ExistingConflicts []Conflict   `json:"existing_conflicts"`
	AlgorithmVersion  string       `json:"algorithm_version"`
	InputRevision     int64        `json:"input_revision"`
	Feasible          bool         `json:"feasible"`
}

// Schedule is a deterministic, bounded greedy planner. It guarantees constraints
// for emitted assignments; an unscheduled result reports that this strategy did
// not find a plan, not a mathematical proof that no possible plan exists.
// Story/Epic windows and locked, started and completed work are never changed.
func Schedule(snapshot Snapshot, request ScheduleRequest) (ScheduleResult, error) {
	calendar, err := dates(request.StartDate, request.EndDate, snapshot.Timezone, 366)
	if err != nil {
		return ScheduleResult{}, err
	}
	if request.HardDeadline != "" {
		if _, err = dates(request.StartDate, request.HardDeadline, snapshot.Timezone, 3660); err != nil {
			return ScheduleResult{}, data.Invalid("hard_deadline must be a valid date on or after start_date")
		}
	}
	out := ScheduleResult{Assignments: []Assignment{}, Unscheduled: []Conflict{}, ExistingConflicts: []Conflict{}, AlgorithmVersion: AlgorithmVersion, InputRevision: snapshot.Revision}
	tasks := map[uuid.UUID]Task{}
	for _, task := range snapshot.Tasks {
		tasks[task.ID] = task
	}
	selected := map[uuid.UUID]bool{}
	if len(request.TaskIDs) == 0 {
		for _, task := range snapshot.Tasks {
			if !task.Protected() {
				selected[task.ID] = true
			}
		}
	} else {
		for _, id := range request.TaskIDs {
			task, ok := tasks[id]
			if !ok {
				return out, data.Invalid("A selected task is outside the authorized snapshot")
			}
			if task.Protected() {
				return out, data.Invalid("Only unstarted, unlocked executable tasks can be scheduled")
			}
			selected[id] = true
		}
	}
	if len(selected) > 200 {
		return out, data.Invalid("A scheduling proposal supports at most 200 tasks")
	}
	allowed := map[uuid.UUID]bool{}
	for _, id := range request.MemberIDs {
		allowed[id] = true
	}
	members := map[uuid.UUID]Member{}
	for _, member := range snapshot.Members {
		members[member.MemberID] = member
	}
	for id := range allowed {
		if _, ok := members[id]; !ok {
			return out, data.Invalid("An allowed member is no longer an active project member")
		}
	}
	// Validate the complete authorized dependency graph before planning.
	colors := map[uuid.UUID]int{}
	var visit func(uuid.UUID) bool
	visit = func(id uuid.UUID) bool {
		if colors[id] == 1 {
			return false
		}
		if colors[id] == 2 {
			return true
		}
		colors[id] = 1
		for _, dep := range tasks[id].Dependencies {
			if _, ok := tasks[dep]; ok && !visit(dep) {
				return false
			}
		}
		colors[id] = 2
		return true
	}
	for id := range tasks {
		if !visit(id) {
			out.Unscheduled = append(out.Unscheduled, Conflict{Type: "dependency_cycle", Message: "Dependency graph contains a cycle", Severity: "error"})
			return out, nil
		}
	}
	baseline := snapshot
	baseline.Tasks = []Task{}
	for _, task := range snapshot.Tasks {
		if !selected[task.ID] {
			baseline.Tasks = append(baseline.Tasks, task)
		}
	}
	projection, err := Project(baseline, request.StartDate, request.EndDate)
	if err != nil {
		return out, err
	}
	loads := map[uuid.UUID]map[string]DayLoad{}
	for _, member := range projection.Members {
		loads[member.MemberID] = map[string]DayLoad{}
		for _, day := range member.Days {
			loads[member.MemberID][day.Date] = day
		}
	}
	// An undated fixed workload cannot be silently treated as free capacity.
	for _, task := range baseline.Tasks {
		if !task.Executable || task.Finished() || task.StartDate != "" && task.TargetDate != "" {
			continue
		}
		for _, memberID := range task.AssigneeIDs {
			for date, day := range loads[memberID] {
				day.Unknown = true
				loads[memberID][date] = day
			}
		}
	}
	for _, conflict := range projection.Conflicts {
		if selected[conflict.TaskID] || selected[conflict.RelatedTaskID] {
			continue
		}
		if conflict.Type == "over_capacity" || conflict.Type == "dependency" || conflict.Type == "commitment" || conflict.Type == "date_order" || conflict.Type == "skill_mismatch" || conflict.Type == "member_removed" {
			out.ExistingConflicts = append(out.ExistingConflicts, conflict)
		}
	}
	pending := map[uuid.UUID]bool{}
	for id := range selected {
		pending[id] = true
	}
	failed := map[uuid.UUID]bool{}
	markFailure := func(task Task, kind, message string) {
		failed[task.ID] = true
		delete(pending, task.ID)
		out.Unscheduled = append(out.Unscheduled, Conflict{Type: kind, TaskID: task.ID, Message: message, Severity: "error"})
	}
	for len(pending) > 0 {
		ready := []Task{}
		for id := range pending {
			isReady := true
			for _, dep := range tasks[id].Dependencies {
				if pending[dep] {
					isReady = false
					break
				}
			}
			if isReady {
				ready = append(ready, tasks[id])
			}
		}
		if len(ready) == 0 {
			return out, data.Invalid("Dependency graph cannot make progress")
		}
		sort.Slice(ready, func(i, j int) bool {
			pi, pj := priority(ready[i].Priority), priority(ready[j].Priority)
			if pi != pj {
				return pi < pj
			}
			di, dj := deadline(ready[i], request), deadline(ready[j], request)
			if di != dj {
				return di < dj
			}
			return ready[i].ID.String() < ready[j].ID.String()
		})
		task := ready[0]
		if task.RemainingMinutes == nil {
			markFailure(task, "unknown_estimate", "Remaining effort is required before scheduling")
			continue
		}
		if *task.RemainingMinutes < 0 {
			markFailure(task, "unknown_estimate", "Remaining effort cannot be negative")
			continue
		}
		earliest := request.StartDate
		if task.CommitmentStartDate > earliest {
			earliest = task.CommitmentStartDate
		}
		blocked := false
		for _, dep := range task.Dependencies {
			before, ok := tasks[dep]
			if !ok || failed[dep] {
				blocked = true
				break
			}
			if before.Finished() {
				continue
			}
			if before.TargetDate == "" {
				blocked = true
				break
			}
			date, parseErr := dates(before.TargetDate, before.TargetDate, snapshot.Timezone, 1)
			if parseErr != nil {
				blocked = true
				break
			}
			next := date[0].AddDate(0, 0, 1).Format(DateFormat)
			if next > earliest {
				earliest = next
			}
		}
		if blocked {
			markFailure(task, "dependency", "A prerequisite has no feasible completion date")
			continue
		}
		latest := deadline(task, request)
		// A fixed successor is an additional hard bound, even when its deadline
		// differs from the Story commitment or request deadline.
		for _, successor := range snapshot.Tasks {
			if selected[successor.ID] || successor.Finished() || successor.StartDate == "" {
				continue
			}
			for _, dep := range successor.Dependencies {
				if dep == task.ID {
					date, parseErr := dates(successor.StartDate, successor.StartDate, snapshot.Timezone, 1)
					if parseErr == nil {
						bound := date[0].AddDate(0, 0, -1).Format(DateFormat)
						if bound < latest {
							latest = bound
						}
					}
				}
			}
		}
		if earliest > latest {
			markFailure(task, "deadline", "Dependencies and protected commitment dates leave no execution window")
			continue
		}
		compatible := []uuid.UUID{}
		for _, member := range snapshot.Members {
			if (len(allowed) == 0 || allowed[member.MemberID]) && hasSkills(member, task.RequiredSkills) {
				compatible = append(compatible, member.MemberID)
			}
		}
		sort.Slice(compatible, func(i, j int) bool { return compatible[i].String() < compatible[j].String() })
		if len(compatible) == 0 {
			markFailure(task, "skill_mismatch", "No allowed active member has every required skill")
			continue
		}
		type group struct {
			ids     []uuid.UUID
			weights []AllocationWeight
		}
		groups := []group{}
		existing := len(task.AssigneeIDs) > 0
		for _, id := range task.AssigneeIDs {
			member, ok := members[id]
			if !ok || len(allowed) > 0 && !allowed[id] || !hasSkills(member, task.RequiredSkills) {
				existing = false
			}
		}
		if existing {
			groups = append(groups, group{append([]uuid.UUID(nil), task.AssigneeIDs...), task.AllocationWeights})
		}
		for _, id := range compatible {
			groups = append(groups, group{[]uuid.UUID{id}, nil})
		}
		if len(compatible) > 1 {
			groups = append(groups, group{compatible, nil})
		}
		var best *Assignment
		bestLoads := map[uuid.UUID]map[string]int{}
		sawUnknown := false
		for _, group := range groups {
			shares, shareErr := Apportion(*task.RemainingMinutes, group.ids, group.weights)
			if shareErr != nil {
				continue
			}
			for first, start := range calendar {
				s := start.Format(DateFormat)
				if s < earliest || s > latest || best != nil && s > best.TargetDate {
					continue
				}
				// Start on an actual workday for every contributing member.
				canStart := true
				for _, id := range group.ids {
					if shares[id] > 0 {
						day := loads[id][s]
						if day.CapacityMinutes == nil || day.Unknown {
							sawUnknown = true
							canStart = false
						}
						if day.CapacityMinutes != nil && *day.CapacityMinutes <= day.AllocatedMinutes {
							canStart = false
						}
					}
				}
				if !canStart {
					continue
				}
				for _, finish := range calendar[first:] {
					e := finish.Format(DateFormat)
					if e > latest || best != nil && e > best.TargetDate {
						break
					}
					candidateLoads := map[uuid.UUID]map[string]int{}
					fits := true
					for _, id := range group.ids {
						values, problem := distribute(members[id], shares[id], s, e, snapshot.Timezone)
						if problem != "" {
							if problem == "unknown_capacity" {
								sawUnknown = true
							}
							fits = false
							break
						}
						for date, value := range values {
							day := loads[id][date]
							if day.CapacityMinutes == nil || day.Unknown {
								fits = false
								sawUnknown = true
								break
							}
							if day.AllocatedMinutes+value > *day.CapacityMinutes {
								fits = false
								break
							}
						}
						if !fits {
							break
						}
						candidateLoads[id] = values
					}
					if !fits {
						continue
					}
					ids := append([]uuid.UUID(nil), group.ids...)
					sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
					weights := append([]AllocationWeight{}, group.weights...)
					if len(weights) == 0 {
						for _, id := range ids {
							weights = append(weights, AllocationWeight{id, 1})
						}
					}
					sort.Slice(weights, func(i, j int) bool { return weights[i].MemberID.String() < weights[j].MemberID.String() })
					candidate := Assignment{ID: task.ID, Version: task.Version, StartDate: s, TargetDate: e, AssigneeIDs: ids, AllocationWeights: weights, Reason: "Earliest feasible finish after prerequisites, within skills, known capacity and protected commitments"}
					if best == nil || candidate.TargetDate < best.TargetDate || candidate.TargetDate == best.TargetDate && (candidate.StartDate < best.StartDate || candidate.StartDate == best.StartDate && memberKey(ids) < memberKey(best.AssigneeIDs)) {
						best = &candidate
						bestLoads = candidateLoads
					}
					break
				}
			}
		}
		if best == nil {
			kind, message := "capacity", "No capacity-feasible interval found before the hard deadline by the deterministic planner"
			if sawUnknown {
				kind, message = "unknown_capacity", "Unknown capacity or existing workload prevents a verified schedule"
			}
			markFailure(task, kind, message)
			continue
		}
		for id, values := range bestLoads {
			for date, value := range values {
				day := loads[id][date]
				day.AllocatedMinutes += value
				loads[id][date] = day
			}
		}
		task.StartDate = best.StartDate
		task.TargetDate = best.TargetDate
		task.AssigneeIDs = best.AssigneeIDs
		task.AllocationWeights = best.AllocationWeights
		tasks[task.ID] = task
		out.Assignments = append(out.Assignments, *best)
		delete(pending, task.ID)
	}
	out.Feasible = len(out.Unscheduled) == 0 && len(out.ExistingConflicts) == 0
	return out, nil
}

func deadline(task Task, request ScheduleRequest) string {
	value := request.EndDate
	for _, bound := range []string{task.CommitmentTargetDate, request.HardDeadline} {
		if bound != "" && bound < value {
			value = bound
		}
	}
	return value
}
func priority(value string) int {
	switch value {
	case "urgent":
		return 0
	case "high":
		return 1
	case "medium":
		return 2
	case "low":
		return 3
	default:
		return 4
	}
}
func memberKey(ids []uuid.UUID) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = id.String()
	}
	return strings.Join(parts, ",")
}
