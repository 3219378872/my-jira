package resources

import (
	"reflect"
	"testing"

	"github.com/google/uuid"
)

var alice = uuid.MustParse("00000000-0000-0000-0000-000000000001")
var bob = uuid.MustParse("00000000-0000-0000-0000-000000000002")

func member(id uuid.UUID, minutes int) Member {
	return Member{MemberID: id, Skills: []string{"go"}, WeekdayMinutes: []*int{number(0), number(minutes), number(minutes), number(minutes), number(minutes), number(minutes), number(0)}, ProjectMinutesPerDay: number(minutes), Exceptions: map[string]*int{}}
}
func task(id string, minutes int) Task {
	return Task{ID: uuid.MustParse(id), Version: 3, Name: "Executable task", RequirementType: "task", Executable: true, StateGroup: "unstarted", RemainingMinutes: number(minutes), EstimatedMinutes: number(minutes), RequiredSkills: []string{"go"}, AssigneeIDs: []uuid.UUID{alice}, AllocationWeights: []AllocationWeight{}, Dependencies: []uuid.UUID{}}
}

func TestApportionConservesMinutesAndStableRemainders(t *testing.T) {
	got, err := Apportion(960, []uuid.UUID{bob, alice}, []AllocationWeight{{bob, 25}, {alice, 75}})
	if err != nil || got[alice] != 720 || got[bob] != 240 {
		t.Fatalf("16 hours 75/25: %v %v", got, err)
	}
	for _, ids := range [][]uuid.UUID{{alice, bob}, {bob, alice}} {
		got, err = Apportion(3, ids, nil)
		if err != nil || got[alice] != 2 || got[bob] != 1 {
			t.Fatalf("Stable remainder: %v %v", got, err)
		}
	}
	if _, err = Apportion(3, []uuid.UUID{alice, bob}, []AllocationWeight{{alice, 1}}); err == nil {
		t.Fatal("Partial explicit allocation accepted")
	}
	if _, err = Apportion(3, []uuid.UUID{alice, alice}, nil); err == nil {
		t.Fatal("Duplicate assignee accepted")
	}
	if _, err = Apportion(3, []uuid.UUID{alice, bob}, []AllocationWeight{{alice, 0}, {bob, 0}}); err == nil {
		t.Fatal("Zero denominator accepted")
	}
}

func TestProjectionUsesWholeIntervalAndOnlyExecutableOpenLeaves(t *testing.T) {
	work := task("10000000-0000-0000-0000-000000000001", 960)
	work.StartDate = "2026-09-14"
	work.TargetDate = "2026-09-15"
	work.AssigneeIDs = []uuid.UUID{alice, bob}
	work.AllocationWeights = []AllocationWeight{{alice, 75}, {bob, 25}}
	parent := work
	parent.ID = uuid.New()
	parent.Executable = false
	parent.RequirementType = "story"
	closed := work
	closed.ID = uuid.New()
	closed.StateGroup = "completed"
	cancelled := work
	cancelled.ID = uuid.New()
	cancelled.StateGroup = "cancelled"
	snapshot := Snapshot{Timezone: "Asia/Shanghai", Members: []Member{member(alice, 480), member(bob, 480)}, Tasks: []Task{work, parent, closed, cancelled}}
	full, err := Project(snapshot, "2026-09-14", "2026-09-15")
	if err != nil {
		t.Fatal(err)
	}
	if full.Members[0].Days[0].AllocatedMinutes != 360 || full.Members[0].Days[1].AllocatedMinutes != 360 || full.Members[1].Days[0].AllocatedMinutes != 120 {
		t.Fatalf("Conserved shares: %+v", full.Members)
	}
	view, err := Project(snapshot, "2026-09-15", "2026-09-15")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(view.Members[0].Days[0], full.Members[0].Days[1]) {
		t.Fatal("Viewport changed daily allocation")
	}
	closed.StateGroup = "unstarted"
	snapshot.Tasks = []Task{closed}
	reopened, _ := Project(snapshot, "2026-09-14", "2026-09-15")
	if reopened.Members[0].Days[0].AllocatedMinutes != 360 {
		t.Fatal("Reopening did not restore remaining load")
	}
}

func TestProjectionDistinguishesUnknownZeroAndCalendarException(t *testing.T) {
	work := task("10000000-0000-0000-0000-000000000001", 600)
	work.StartDate = "2026-09-14"
	work.TargetDate = "2026-09-15"
	m := member(alice, 480)
	m.Exceptions["2026-09-15"] = number(0)
	load, err := Project(Snapshot{Timezone: "UTC", Members: []Member{m}, Tasks: []Task{work}}, work.StartDate, work.TargetDate)
	if err != nil {
		t.Fatal(err)
	}
	if load.Members[0].Days[0].AllocatedMinutes != 600 || !load.Members[0].Days[0].OverCapacity || load.Members[0].Days[1].AllocatedMinutes != 0 {
		t.Fatalf("Holiday/overload: %+v", load.Members[0])
	}
	m.ProjectMinutesPerDay = nil
	load, _ = Project(Snapshot{Timezone: "UTC", Members: []Member{m}, Tasks: []Task{work}}, work.StartDate, work.TargetDate)
	if load.Members[0].Days[0].CapacityMinutes != nil || !load.Members[0].Days[0].Unknown || *load.Members[0].Days[1].CapacityMinutes != 0 {
		t.Fatalf("Unknown and zero conflated: %+v", load.Members[0])
	}
	m.ProjectMinutesPerDay = number(0)
	load, _ = Project(Snapshot{Timezone: "UTC", Members: []Member{m}, Tasks: []Task{work}}, work.StartDate, work.TargetDate)
	if !load.Members[0].Days[0].Unknown || !containsConflict(load.Conflicts, "no_working_days") {
		t.Fatal("Known zero capacity was treated as a schedulable zero load")
	}
	work.RemainingMinutes = nil
	load, _ = Project(Snapshot{Timezone: "UTC", Members: []Member{m}, Tasks: []Task{work}}, work.StartDate, work.TargetDate)
	if len(load.Unestimated) != 1 || !load.Members[0].Days[0].Unknown {
		t.Fatal("Unknown effort was treated as zero")
	}
}

func TestProjectDatesUseCalendarArithmeticAcrossDST(t *testing.T) {
	days, err := dates("2026-03-06", "2026-03-09", "America/New_York", 10)
	if err != nil || len(days) != 4 || days[3].Format(DateFormat) != "2026-03-09" {
		t.Fatalf("DST date range: %v %v", days, err)
	}
	m := member(alice, 480)
	m.ProjectMinutesPerDay = number(240)
	values, problem := distribute(m, 481, "2026-03-06", "2026-03-09", "America/New_York")
	if problem != "" || values["2026-03-06"] != 241 || values["2026-03-09"] != 240 {
		t.Fatalf("Workdays across DST: %v %s", values, problem)
	}
}

func containsConflict(conflicts []Conflict, kind string) bool {
	for _, c := range conflicts {
		if c.Type == kind {
			return true
		}
	}
	return false
}
