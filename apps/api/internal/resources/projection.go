package resources

import (
	"sort"
	"time"

	"github.com/google/uuid"
)

// distribute spreads one member's share across the complete task interval in
// proportion to known available minutes. The viewport never changes allocation.
func distribute(member Member, minutes int, start, end, timezone string) (map[string]int, string) {
	calendar, err := dates(start, end, timezone, 3660)
	if err != nil {
		return nil, "date_order"
	}
	result := map[string]int{}
	if minutes == 0 {
		return result, ""
	}
	var capacity int64
	for _, date := range calendar {
		available := member.Capacity(date)
		if available == nil {
			return nil, "unknown_capacity"
		}
		capacity += int64(*available)
	}
	if capacity == 0 {
		return nil, "no_working_days"
	}
	type remainder struct {
		date     string
		fraction int64
	}
	fractions := []remainder{}
	allocated := 0
	for _, date := range calendar {
		available := *member.Capacity(date)
		if available == 0 {
			continue
		}
		product := int64(minutes) * int64(available)
		value := int(product / capacity)
		result[date.Format(DateFormat)] = value
		allocated += value
		fractions = append(fractions, remainder{date.Format(DateFormat), product % capacity})
	}
	sort.SliceStable(fractions, func(i, j int) bool { return fractions[i].fraction > fractions[j].fraction })
	for i := 0; i < minutes-allocated; i++ {
		result[fractions[i].date]++
	}
	return result, ""
}

// Project aggregates the full authorized snapshot, independent of client paging.
// Unknown effort/capacity is carried separately from the known allocated subtotal.
func Project(snapshot Snapshot, start, end string) (Projection, error) {
	days, err := dates(start, end, snapshot.Timezone, 366)
	if err != nil {
		return Projection{}, err
	}
	out := Projection{Timezone: snapshot.Timezone, StartDate: start, EndDate: end, Members: []MemberLoad{}, Tasks: append([]Task{}, snapshot.Tasks...), Conflicts: []Conflict{}, Unassigned: []uuid.UUID{}, Unestimated: []uuid.UUID{}, Unscheduled: []uuid.UUID{}, Scope: "Current project and recorded assignments visible to the current actor; daily totals include every authorized task, independent of task filters and pagination."}
	members := append([]Member(nil), snapshot.Members...)
	sort.Slice(members, func(i, j int) bool { return members[i].MemberID.String() < members[j].MemberID.String() })
	memberIndex := map[uuid.UUID]int{}
	dayIndex := map[string]int{}
	for i, date := range days {
		dayIndex[date.Format(DateFormat)] = i
	}
	for _, member := range members {
		row := MemberLoad{Member: member, Days: []DayLoad{}, Weeks: []WeekLoad{}}
		for _, date := range days {
			row.Days = append(row.Days, DayLoad{Date: date.Format(DateFormat), CapacityMinutes: member.Capacity(date), TaskIDs: []uuid.UUID{}, TaskMinutes: map[uuid.UUID]int{}, UnknownTaskIDs: []uuid.UUID{}})
		}
		memberIndex[member.MemberID] = len(out.Members)
		out.Members = append(out.Members, row)
	}
	taskMap := map[uuid.UUID]Task{}
	for _, task := range snapshot.Tasks {
		taskMap[task.ID] = task
	}
	addConflict := func(kind string, task Task, member uuid.UUID, message string) {
		out.Conflicts = append(out.Conflicts, Conflict{Type: kind, TaskID: task.ID, MemberID: member, Message: message, Severity: "warning"})
	}
	unknown := func(task Task, memberID uuid.UUID) {
		i, ok := memberIndex[memberID]
		if !ok {
			return
		}
		for j := range out.Members[i].Days {
			day := &out.Members[i].Days[j]
			if task.StartDate != "" && task.TargetDate != "" && day.Date >= task.StartDate && day.Date <= task.TargetDate {
				day.Unknown = true
				day.TaskIDs = appendUnique(day.TaskIDs, task.ID)
				day.UnknownTaskIDs = appendUnique(day.UnknownTaskIDs, task.ID)
			}
		}
	}
	for _, task := range snapshot.Tasks {
		if task.Finished() {
			continue
		}
		if task.StartDate != "" && task.TargetDate != "" && task.StartDate > task.TargetDate {
			addConflict("date_order", task, uuid.Nil, "Execution start is after its target date")
		}
		if task.CommitmentStartDate != "" && task.StartDate != "" && task.StartDate < task.CommitmentStartDate || task.CommitmentTargetDate != "" && task.TargetDate != "" && task.TargetDate > task.CommitmentTargetDate {
			addConflict("commitment", task, uuid.Nil, "Execution dates fall outside the Story commitment window")
		}
		for _, dependency := range task.Dependencies {
			before, visible := taskMap[dependency]
			if !visible {
				out.Conflicts = append(out.Conflicts, Conflict{Type: "dependency", TaskID: task.ID, RelatedTaskID: dependency, Message: "The prerequisite has no active execution plan", Severity: "warning"})
				continue
			}
			if before.Finished() {
				continue
			}
			if task.StartDate != "" && (before.TargetDate == "" || before.TargetDate >= task.StartDate) {
				out.Conflicts = append(out.Conflicts, Conflict{Type: "dependency", TaskID: task.ID, RelatedTaskID: dependency, Date: task.StartDate, Message: "The prerequisite must finish before this task starts", Severity: "warning"})
			}
		}
		if !task.Executable {
			continue
		}
		if len(task.AssigneeIDs) == 0 {
			out.Unassigned = append(out.Unassigned, task.ID)
			addConflict("unassigned", task, uuid.Nil, "No responsible member is assigned")
		}
		if task.RemainingMinutes == nil {
			out.Unestimated = append(out.Unestimated, task.ID)
			addConflict("unknown_estimate", task, uuid.Nil, "Remaining effort is unknown")
		}
		if task.StartDate == "" || task.TargetDate == "" {
			out.Unscheduled = append(out.Unscheduled, task.ID)
			addConflict("unscheduled", task, uuid.Nil, "The task needs both execution dates")
		}
		if len(task.AssigneeIDs) == 0 {
			continue
		}
		shares := map[uuid.UUID]int{}
		if task.RemainingMinutes != nil {
			shares, err = Apportion(*task.RemainingMinutes, task.AssigneeIDs, task.AllocationWeights)
			if err != nil {
				addConflict("invalid_allocation", task, uuid.Nil, err.Error())
				for _, id := range task.AssigneeIDs {
					unknown(task, id)
				}
				continue
			}
		}
		for _, memberID := range task.AssigneeIDs {
			idx, present := memberIndex[memberID]
			if !present {
				addConflict("member_removed", task, memberID, "An assignee is no longer an active project member")
				continue
			}
			member := out.Members[idx].Member
			if (task.RemainingMinutes == nil || shares[memberID] > 0) && !hasSkills(member, task.RequiredSkills) {
				addConflict("skill_mismatch", task, memberID, "The member does not have every required skill")
			}
			if task.RemainingMinutes == nil {
				unknown(task, memberID)
				continue
			}
			if task.StartDate == "" || task.TargetDate == "" {
				continue
			}
			values, problem := distribute(member, shares[memberID], task.StartDate, task.TargetDate, snapshot.Timezone)
			if problem != "" {
				message := "No available working day exists in this task interval"
				if problem == "unknown_capacity" {
					message = "Calendar or project capacity is unknown in this task interval"
				}
				if problem == "date_order" {
					message = "Task dates are invalid or span more than ten years"
				}
				addConflict(problem, task, memberID, message)
				unknown(task, memberID)
				continue
			}
			for date, value := range values {
				if j, ok := dayIndex[date]; ok && value > 0 {
					day := &out.Members[idx].Days[j]
					day.AllocatedMinutes += value
					day.TaskMinutes[task.ID] += value
					day.TaskIDs = appendUnique(day.TaskIDs, task.ID)
				}
			}
		}
	}
	for i := range out.Members {
		member := &out.Members[i]
		for j := range member.Days {
			day := &member.Days[j]
			sort.Slice(day.TaskIDs, func(i, j int) bool { return day.TaskIDs[i].String() < day.TaskIDs[j].String() })
			day.OverCapacity = day.CapacityMinutes != nil && day.AllocatedMinutes > *day.CapacityMinutes
			day.SelectedMinutes = day.AllocatedMinutes
			day.SelectedUnknown = day.Unknown
			if day.OverCapacity {
				out.Conflicts = append(out.Conflicts, Conflict{Type: "over_capacity", MemberID: member.MemberID, Date: day.Date, Message: "Recorded work exceeds this member's project capacity", Severity: "warning"})
			}
		}
		member.Weeks = weekly(member.Days, days)
	}
	return out, nil
}

func appendUnique(ids []uuid.UUID, id uuid.UUID) []uuid.UUID {
	for _, current := range ids {
		if current == id {
			return ids
		}
	}
	return append(ids, id)
}

func weekly(loads []DayLoad, days []time.Time) []WeekLoad {
	result := []WeekLoad{}
	for i, day := range loads {
		offset := (int(days[i].Weekday()) + 6) % 7
		monday := days[i].AddDate(0, 0, -offset).Format(DateFormat)
		if len(result) == 0 || result[len(result)-1].StartDate != monday {
			result = append(result, WeekLoad{StartDate: monday, CapacityMinutes: number(0)})
		}
		week := &result[len(result)-1]
		if day.CapacityMinutes == nil {
			week.CapacityMinutes = nil
		} else if week.CapacityMinutes != nil {
			*week.CapacityMinutes += *day.CapacityMinutes
		}
		week.AllocatedMinutes += day.AllocatedMinutes
		week.SelectedMinutes += day.SelectedMinutes
		week.Unknown = week.Unknown || day.Unknown
		week.SelectedUnknown = week.SelectedUnknown || day.SelectedUnknown
		// Any daily violation remains visible even when spare capacity later in the
		// week would hide it in a weekly subtotal.
		week.OverCapacity = week.OverCapacity || day.OverCapacity
	}
	return result
}
