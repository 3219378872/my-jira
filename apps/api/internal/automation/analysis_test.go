package automation

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func historicalFixture() Input {
	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	input := Input{AsOf: start.AddDate(0, 0, 84), History: []Fact{}, Items: []Item{}}
	for i := 0; i < 70; i++ {
		id := uuid.New()
		created := start.AddDate(0, 0, i/2)
		before := raw(map[string]any{"id": id, "created_at": created, "state_group": "unstarted", "requirement_type": "task"})
		input.History = append(input.History, Fact{ItemID: id, At: created, Action: "insert", After: before})
		if i < 60 {
			finished := created.AddDate(0, 0, 7+i%5)
			input.History = append(input.History, Fact{ItemID: id, At: finished, Action: "update", Before: before, After: raw(map[string]any{"id": id, "created_at": created, "completed_at": finished, "state_group": "completed", "requirement_type": "task"})})
		}
	}
	return input
}
func TestForecastColdStartReproducibilityAndNoFutureLeakage(t *testing.T) {
	input := historicalFixture()
	f := ForecastDelivery(input)
	if f.Status != "ready" || f.SampleCount != 60 || f.Remaining != 10 || f.P50 == nil || f.P80 == nil || *f.P50 > *f.P80 {
		t.Fatalf("incorrect temporal forecast: %+v", f)
	}
	if fingerprint(f) != fingerprint(ForecastDelivery(input)) {
		t.Fatal("same immutable input produced a different simulation")
	}
	cutoff := input.AsOf
	futureID := uuid.New()
	input.History = append(input.History, Fact{ItemID: futureID, At: cutoff.AddDate(0, 0, 1), Action: "insert", After: raw(map[string]any{"id": futureID, "completed_at": cutoff.AddDate(0, 0, -1)})})
	future := ForecastDelivery(input)
	if *future.P50 != *f.P50 || *future.P80 != *f.P80 || future.SampleCount != f.SampleCount {
		t.Fatal("a fact recorded after cutoff leaked into the forecast")
	}
	input.AsOf = time.Date(2026, 1, 25, 12, 0, 0, 0, time.UTC)
	cold := ForecastDelivery(input)
	if cold.Status != "insufficient_data" || cold.P50 != nil {
		t.Fatalf("short coverage invented confidence: %+v", cold)
	}
	onlyBaseline := Input{AsOf: cutoff, History: []Fact{{ItemID: uuid.New(), At: cutoff.AddDate(0, 0, -90), Action: "coverage_started", After: raw(map[string]any{"completed_at": cutoff.AddDate(0, 0, -180)})}}}
	if got := ForecastDelivery(onlyBaseline); got.SampleCount != 0 || got.Status != "insufficient_data" {
		t.Fatalf("migration baseline invented completion history: %+v", got)
	}
}
func TestForecastBacktestUsesObservableScope(t *testing.T) {
	input := historicalFixture()
	input.History = input.History[:120]
	f := ForecastDelivery(input)
	if f.Backtest.Windows == 0 || f.Backtest.MeanAbsoluteErrorDays == nil || f.Backtest.P80Coverage == nil || f.Backtest.BaselineErrorDays == nil {
		t.Fatalf("expected scorable expanding time splits: %+v", f.Backtest)
	}
	if *f.Backtest.P80Coverage < 0 || *f.Backtest.P80Coverage > 1 {
		t.Fatal("invalid interval coverage")
	}
}
func validDecomposition() (Decomposition, string) {
	source := "安全登录\n成员使用账号登录\n实现身份校验\n验证拒绝非法身份"
	return Decomposition{Complete: true, Unresolved: []string{}, Entities: []DecomposedEntity{
		{Key: "epic", Kind: "epic", Title: "权限", Source: Location{1, 1, "安全登录"}},
		{Key: "story", Kind: "story", ParentKey: "epic", Title: "登录", Role: "成员", Goal: "安全登录", Benefit: "保护数据", AcceptanceCriteria: []string{"错误身份被拒绝"}, Source: Location{2, 2, "成员使用账号登录"}},
		{Key: "test", Kind: "task", ParentKey: "story", Title: "测试身份", EstimatedMinutesMin: 60, EstimatedMinutesMax: 120, Assumptions: []string{"采用现有测试框架"}, DependsOn: []string{"auth"}, Source: Location{4, 4, "验证拒绝非法身份"}},
		{Key: "auth", Kind: "task", ParentKey: "story", Title: "实现身份", EstimatedMinutesMin: 120, EstimatedMinutesMax: 240, Assumptions: []string{"已有账号基础"}, Source: Location{3, 3, "实现身份校验"}},
	}}, source
}
func TestStructuredDecompositionBoundsSourcesDependenciesAndManualConflicts(t *testing.T) {
	d, source := validDecomposition()
	parsed, e := ParseDecomposition(string(raw(d)), source)
	if e != nil {
		t.Fatal(e)
	}
	p, e := DecompositionProposal(parsed, Input{})
	if e != nil {
		t.Fatal(e)
	}
	if len(p.Commands) != 4 || p.Commands[2].ClientID != "auth" || p.Commands[3].ClientID != "test" {
		t.Fatalf("dependencies not topologically ordered: %+v", p.Commands)
	}
	var minutes int
	_ = json.Unmarshal(p.Commands[2].Fields["estimated_minutes"], &minutes)
	if minutes != 180 {
		t.Fatalf("hour/minute unit mismatch: %d", minutes)
	}
	bad := d
	bad.Entities = append([]DecomposedEntity{}, d.Entities...)
	bad.Entities[0].Source.Quote = "invented"
	if _, e = ParseDecomposition(string(raw(bad)), source); e == nil {
		t.Fatal("accepted an invented source excerpt")
	}
	bad = d
	bad.Entities = append([]DecomposedEntity{}, d.Entities...)
	bad.Entities[3].DependsOn = []string{"test"}
	if _, e = ParseDecomposition(string(raw(bad)), source); e == nil {
		t.Fatal("accepted cyclic generated dependencies")
	}
	if _, e = ParseDecomposition(string(raw(d))+" {}", source); e == nil {
		t.Fatal("accepted trailing output object")
	}
	id := uuid.New()
	input := Input{Items: []Item{{ID: id, Version: 4, StateGroup: "unstarted"}}, Mappings: []Mapping{{EntityKey: "story", ItemID: id, LastVersion: 3}, {EntityKey: "removed", ItemID: uuid.New(), LastVersion: 1}}}
	p, e = DecompositionProposal(d, input)
	if e != nil {
		t.Fatal(e)
	}
	if !p.Partial {
		t.Fatal("manual conflict and removed source were reported complete")
	}
	for _, cmd := range p.Commands {
		if cmd.ID == id {
			t.Fatal("manual edit was overwritten")
		}
	}
	if !strings.Contains(strings.Join(p.Findings, " "), "retained") {
		t.Fatal("source deletion did not produce a retained-work difference")
	}
}
func TestPolicyProtectsCommitmentsAndBoundedFields(t *testing.T) {
	p := DefaultPolicy()
	p.Enabled = true
	id := uuid.New()
	input := Input{Items: []Item{{ID: id, Version: 2, RequirementType: "story", StateGroup: "unstarted"}}}
	proposal := Proposal{Commands: []ProposalCommand{{Operation: "update", ID: id, Version: 2, Fields: map[string]json.RawMessage{"target_date": raw("2026-10-01")}}}}
	if e := validateProposal(p, input, proposal); e == nil {
		t.Fatal("automation moved a Story commitment")
	}
	proposal.Commands[0].Fields = map[string]json.RawMessage{"planning_locked": raw(false)}
	if e := validateProposal(p, input, proposal); e == nil {
		t.Fatal("automation unlocked human work")
	}
	p.AllowedEntities = []string{"story"}
	proposal.Commands[0].Fields = map[string]json.RawMessage{"requirement_type": raw(nil)}
	if e := validateProposal(p, input, proposal); e == nil {
		t.Fatal("null type escaped entity policy")
	}
	p.AllowedEntities = nil
	proposal.Commands[0].Fields = map[string]json.RawMessage{"name": raw("change")}
	input.Items[0].StateGroup = "started"
	if e := validateProposal(p, input, proposal); e == nil {
		t.Fatal("automation changed started work")
	}
	p.AllowedFields = append(p.AllowedFields, "planning_locked")
	if p.Validate() == nil {
		t.Fatal("policy allowed automatic unlocking")
	}
}
func TestEfficiencyDistinguishesObservationEvidenceAndScopeChange(t *testing.T) {
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	before := Metrics{CoverageDays: 14, Observations: 20, Scope: 20, WIP: 8, Throughput: 5}
	after := Metrics{CoverageDays: 14, Observations: 20, Scope: 20, WIP: 4, Throughput: 6}
	if status, _ := CompareImprovement(before, after, "wip", now, now.AddDate(0, 0, 1)); status != "observing" {
		t.Fatal(status)
	}
	if status, _ := CompareImprovement(before, after, "wip", now, now); status != "improved" {
		t.Fatal(status)
	}
	after.Scope = 30
	if status, _ := CompareImprovement(before, after, "wip", now, now); status != "insufficient_evidence" {
		t.Fatal("scope change was called a gain")
	}
	after.Scope = 20
	after.Throughput = 1
	if status, _ := CompareImprovement(before, after, "wip", now, now); status != "no_improvement" {
		t.Fatal("lower WIP with collapsed throughput was called a gain")
	}
}
func TestHistoricalEfficiencyDoesNotUsePresentWIPOrFutureRelation(t *testing.T) {
	input := historicalFixture()
	through := input.AsOf
	id := input.History[0].ItemID
	input.Items = []Item{{ID: id, StateGroup: "started", DependencyIDs: []uuid.UUID{uuid.New()}}}
	measured := Measure(input, through.AddDate(0, 0, -14), through)
	input.History = append(input.History, Fact{At: through.Add(time.Hour), Action: "work_item_relations.insert", After: raw(map[string]any{"id": uuid.New(), "source_id": id, "target_id": uuid.New(), "relation_type": "blocks"})})
	changed := Measure(input, through.AddDate(0, 0, -14), through)
	if measured.WIP != 0 || measured.BlockedRatio != changed.BlockedRatio {
		t.Fatal("current/future status contaminated a fixed historical window")
	}
}
