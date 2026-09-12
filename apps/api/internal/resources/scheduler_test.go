package resources

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestSchedulerRespectsPriorityDependenciesCapacityAndCommitment(t *testing.T) {
	first := task("10000000-0000-0000-0000-000000000001", 480)
	first.Priority = "urgent"
	second := task("10000000-0000-0000-0000-000000000002", 480)
	second.Priority = "high"
	second.Dependencies = []uuid.UUID{first.ID}
	second.CommitmentTargetDate = "2026-09-16"
	fixed := task("10000000-0000-0000-0000-000000000003", 240)
	fixed.PlanningLocked = true
	fixed.StartDate = "2026-09-14"
	fixed.TargetDate = "2026-09-14"
	story := task("10000000-0000-0000-0000-000000000004", 10000)
	story.RequirementType = "story"
	story.Executable = false
	story.StartDate = "2026-09-14"
	story.TargetDate = "2026-09-16"
	request := ScheduleRequest{StartDate: "2026-09-14", EndDate: "2026-09-18"}
	snapshot := Snapshot{Timezone: "UTC", Members: []Member{member(alice, 480)}, Tasks: []Task{second, fixed, story, first}}
	result, err := Schedule(snapshot, request)
	if err != nil || !result.Feasible || len(result.Assignments) != 2 {
		t.Fatalf("Schedule: %+v %v", result, err)
	}
	if result.Assignments[0].ID != first.ID || result.Assignments[0].TargetDate != "2026-09-15" || result.Assignments[1].StartDate != "2026-09-16" || result.Assignments[1].TargetDate != "2026-09-16" {
		t.Fatalf("Priority/dependencies: %+v", result.Assignments)
	}
	for _, assignment := range result.Assignments {
		if assignment.ID == fixed.ID || assignment.ID == story.ID {
			t.Fatal("Protected work or Story commitment changed")
		}
	}
	for i := range snapshot.Tasks {
		for _, assignment := range result.Assignments {
			if snapshot.Tasks[i].ID == assignment.ID {
				snapshot.Tasks[i].StartDate = assignment.StartDate
				snapshot.Tasks[i].TargetDate = assignment.TargetDate
				snapshot.Tasks[i].AssigneeIDs = assignment.AssigneeIDs
				snapshot.Tasks[i].AllocationWeights = assignment.AllocationWeights
			}
		}
	}
	projection, err := Project(snapshot, request.StartDate, request.EndDate)
	if err != nil || len(projection.Conflicts) != 0 {
		t.Fatalf("Emitted plan has a hard conflict: %+v %v", projection.Conflicts, err)
	}
}

func TestSchedulerMultipleAssigneesSkillsHolidayAndStableOutput(t *testing.T) {
	work := task("10000000-0000-0000-0000-000000000001", 960)
	work.AssigneeIDs = []uuid.UUID{bob, alice}
	work.AllocationWeights = []AllocationWeight{{alice, 75}, {bob, 25}}
	a, b := member(alice, 480), member(bob, 480)
	a.Exceptions["2026-09-14"] = number(0)
	request := ScheduleRequest{StartDate: "2026-09-14", EndDate: "2026-09-18"}
	snapshot := Snapshot{Timezone: "Asia/Shanghai", Members: []Member{b, a}, Tasks: []Task{work}}
	result, err := Schedule(snapshot, request)
	if err != nil || !result.Feasible {
		t.Fatalf("Multi-assignee schedule: %+v %v", result, err)
	}
	if result.Assignments[0].TargetDate != "2026-09-15" {
		t.Fatalf("Expected earliest feasible finish: %+v", result.Assignments)
	}
	// Candidate selection may choose a different allowed allocation, but every
	// emitted assignment must conserve all 960 minutes and meet both calendars.
	assignment := result.Assignments[0]
	work.StartDate = assignment.StartDate
	work.TargetDate = assignment.TargetDate
	work.AssigneeIDs = assignment.AssigneeIDs
	work.AllocationWeights = assignment.AllocationWeights
	projection, _ := Project(Snapshot{Timezone: snapshot.Timezone, Members: snapshot.Members, Tasks: []Task{work}}, request.StartDate, request.EndDate)
	total := 0
	for _, m := range projection.Members {
		for _, day := range m.Days {
			total += day.AllocatedMinutes
			if day.OverCapacity {
				t.Fatal("Scheduler overbooked a day")
			}
		}
	}
	if total != 960 {
		t.Fatalf("Minutes lost: %d", total)
	}
	snapshot.Members = []Member{a, b}
	again, _ := Schedule(snapshot, request)
	left, _ := json.Marshal(result)
	right, _ := json.Marshal(again)
	if string(left) != string(right) {
		t.Fatalf("Input ordering changed schedule: %s != %s", left, right)
	}
	snapshot.Tasks[0].RequiredSkills = []string{"rust"}
	gap, _ := Schedule(snapshot, request)
	if gap.Feasible || !containsConflict(gap.Unscheduled, "skill_mismatch") {
		t.Fatal("Missing skills were ignored")
	}
}

func TestSchedulerReportsUnknownDeadlineCyclesAndRemovedMembers(t *testing.T) {
	work := task("10000000-0000-0000-0000-000000000001", 960)
	request := ScheduleRequest{StartDate: "2026-09-14", EndDate: "2026-09-14"}
	snapshot := Snapshot{Timezone: "UTC", Members: []Member{member(alice, 480)}, Tasks: []Task{work}}
	result, err := Schedule(snapshot, request)
	if err != nil || result.Feasible || len(result.Assignments) != 0 || !containsConflict(result.Unscheduled, "capacity") {
		t.Fatalf("Infeasible deadline not reported: %+v %v", result, err)
	}
	snapshot.Members[0].ProjectMinutesPerDay = nil
	result, _ = Schedule(snapshot, request)
	if result.Feasible || !containsConflict(result.Unscheduled, "unknown_capacity") {
		t.Fatal("Unknown capacity became zero/free capacity")
	}
	snapshot.Members[0] = member(bob, 480)
	snapshot.Tasks[0].RemainingMinutes = number(120)
	result, _ = Schedule(snapshot, request)
	if !result.Feasible || result.Assignments[0].AssigneeIDs[0] != bob {
		t.Fatalf("Removed member used: %+v", result)
	}
	request.MemberIDs = []uuid.UUID{alice}
	if _, err = Schedule(snapshot, request); err == nil {
		t.Fatal("Removed allowed member accepted")
	}
	request.MemberIDs = nil
	snapshot.Tasks[0].Dependencies = []uuid.UUID{snapshot.Tasks[0].ID}
	result, _ = Schedule(snapshot, request)
	if result.Feasible || !containsConflict(result.Unscheduled, "dependency_cycle") {
		t.Fatal("Dependency cycle accepted")
	}
}

func TestSchedulerProtectsStartedWorkAndUnknownExistingLoad(t *testing.T) {
	work := task("10000000-0000-0000-0000-000000000001", 60)
	started := task("10000000-0000-0000-0000-000000000002", 60)
	started.StateGroup = "started"
	started.RemainingMinutes = nil
	snapshot := Snapshot{Timezone: "UTC", Members: []Member{member(alice, 480)}, Tasks: []Task{work, started}}
	request := ScheduleRequest{StartDate: "2026-09-14", EndDate: "2026-09-14"}
	result, err := Schedule(snapshot, request)
	if err != nil || result.Feasible || !containsConflict(result.Unscheduled, "unknown_capacity") {
		t.Fatalf("Undated/unknown started workload ignored: %+v %v", result, err)
	}
	request.TaskIDs = []uuid.UUID{started.ID}
	if _, err = Schedule(snapshot, request); err == nil {
		t.Fatal("Started task was editable")
	}
}

func TestSchedulerHonorsFixedSuccessorAndUnavailablePrerequisite(t *testing.T) {
	work := task("10000000-0000-0000-0000-000000000001", 480)
	fixed := task("10000000-0000-0000-0000-000000000002", 480)
	fixed.PlanningLocked = true
	fixed.StartDate = "2026-09-15"
	fixed.TargetDate = "2026-09-15"
	fixed.Dependencies = []uuid.UUID{work.ID}
	snapshot := Snapshot{Timezone: "UTC", Members: []Member{member(alice, 480)}, Tasks: []Task{work, fixed}}
	request := ScheduleRequest{StartDate: "2026-09-14", EndDate: "2026-09-18"}
	result, err := Schedule(snapshot, request)
	if err != nil || !result.Feasible || result.Assignments[0].TargetDate != "2026-09-14" {
		t.Fatalf("Fixed successor deadline: %+v %v", result, err)
	}
	snapshot.Tasks[0].RemainingMinutes = number(960)
	result, _ = Schedule(snapshot, request)
	if result.Feasible {
		t.Fatal("Moved prerequisite past fixed successor")
	}
	snapshot.Tasks[0].Dependencies = []uuid.UUID{uuid.New()}
	result, _ = Schedule(snapshot, request)
	if result.Feasible || !containsConflict(result.Unscheduled, "dependency") {
		t.Fatal("Unavailable prerequisite was ignored")
	}
}
