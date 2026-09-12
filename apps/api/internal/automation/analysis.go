package automation

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"my-jira/apps/api/internal/resources"
)

type Risk struct {
	ID           uuid.UUID   `json:"id"`
	Key          string      `json:"key"`
	Type         string      `json:"type"`
	Severity     string      `json:"severity"`
	Status       string      `json:"status"`
	Reason       string      `json:"reason"`
	Evidence     any         `json:"evidence"`
	ItemIDs      []uuid.UUID `json:"item_ids"`
	ActionItemID *uuid.UUID  `json:"action_item_id"`
	Version      int64       `json:"version"`
}
type RiskResult struct {
	Risks      []Risk    `json:"risks"`
	Forecast   Forecast  `json:"forecast"`
	ObservedAt time.Time `json:"observed_at"`
	Unknown    []string  `json:"unknown"`
}

func AnalyzeRisks(input Input, p Policy) (Proposal, error) {
	out := RiskResult{Risks: []Risk{}, Forecast: ForecastDelivery(input), ObservedAt: input.AsOf, Unknown: []string{}}
	add := func(key, kind, severity, reason string, evidence any, ids ...uuid.UUID) {
		out.Risks = append(out.Risks, Risk{Key: key, Type: kind, Severity: severity, Status: "open", Reason: reason, Evidence: evidence, ItemIDs: ids})
	}
	if p.HardDeadline != "" && out.Forecast.P80 != nil && *out.Forecast.P80 > p.HardDeadline {
		add("project:forecast_deadline", "forecast_delay", "high", "P80 delivery exceeds the unchanged project hard deadline", map[string]any{"p80": out.Forecast.P80, "hard_deadline": p.HardDeadline, "as_of": out.Forecast.AsOf})
	}
	if out.Forecast.Status != "ready" {
		out.Unknown = append(out.Unknown, "Delivery history does not yet meet twenty completed items and four weeks of observation")
	}
	var resourceInput resources.Snapshot
	if len(input.Resources) > 0 && json.Unmarshal(input.Resources, &resourceInput) == nil {
		projection, e := resources.Project(resourceInput, input.AsOf.Format(time.DateOnly), input.AsOf.AddDate(0, 0, 90).Format(time.DateOnly))
		if e != nil {
			return Proposal{}, e
		}
		grouped := map[string]resources.Conflict{}
		for _, c := range projection.Conflicts {
			kind := c.Type
			switch kind {
			case "dependency", "over_capacity", "skill_mismatch", "member_removed", "commitment", "no_working_days", "unknown_capacity", "unknown_estimate", "date_order":
			default:
				continue
			}
			// Repeated daily excess is one active member/cause risk, with refreshed
			// evidence, rather than a new notification for every calendar cell.
			key := kind + ":" + c.TaskID.String() + ":" + c.MemberID.String()
			if _, ok := grouped[key]; !ok {
				grouped[key] = c
			}
		}
		keys := []string{}
		for key := range grouped {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			c := grouped[key]
			ids := []uuid.UUID{}
			if c.TaskID != uuid.Nil {
				ids = append(ids, c.TaskID)
			}
			if c.RelatedTaskID != uuid.Nil {
				ids = append(ids, c.RelatedTaskID)
			}
			add(key, c.Type, "high", c.Message, c, ids...)
		}
	}
	for _, item := range input.Items {
		if item.StateGroup == "completed" || item.StateGroup == "cancelled" {
			continue
		}
		if item.TargetDate != nil && cleanDate(item.TargetDate) < input.AsOf.Format(time.DateOnly) {
			add("overdue:"+item.ID.String(), "overdue", "high", "Execution target has elapsed while work remains open", map[string]any{"target_date": item.TargetDate, "state_group": item.StateGroup}, item.ID)
		}
		if item.StateGroup == "started" && input.AsOf.Sub(item.UpdatedAt) > 14*24*time.Hour {
			add("stagnation:"+item.ID.String(), "stagnation", "medium", "In-progress work has no recorded change for fourteen days", map[string]any{"last_recorded_change": item.UpdatedAt}, item.ID)
		}
	}
	created, reopened := 0, 0
	for _, f := range input.History {
		if f.At.Before(input.AsOf.AddDate(0, 0, -7)) || f.At.After(input.AsOf) {
			continue
		}
		if f.Action == "insert" {
			created++
		}
		if f.Action == "update" {
			var b, a historicalState
			_ = json.Unmarshal(f.Before, &b)
			_ = json.Unmarshal(f.After, &a)
			if b.CompletedAt != nil && a.CompletedAt == nil {
				reopened++
			}
		}
	}
	if created >= 5 && len(input.Items) > 0 && float64(created)/float64(len(input.Items)) >= .2 {
		add("project:scope_growth", "scope_growth", "medium", "At least twenty percent of current scope was added in the last week", map[string]any{"added": created, "current_scope": len(input.Items)})
	}
	if reopened >= 3 {
		add("project:rework", "rework", "medium", "Three or more completed work items reopened during the last week", map[string]int{"reopened": reopened})
	}
	for _, q := range input.Quality {
		var report struct {
			ID        uuid.UUID                                      `json:"id"`
			BindingID uuid.UUID                                      `json:"binding_id"`
			Commit    string                                         `json:"commit_sha"`
			Findings  []struct{ ID, Severity, Title, Status string } `json:"findings"`
		}
		if json.Unmarshal(q, &report) != nil {
			continue
		}
		for _, finding := range report.Findings {
			if finding.Status == "passed" || finding.Status == "resolved" || finding.Status == "missing" || finding.Status == "unknown" {
				continue
			}
			if finding.Severity != "high" && finding.Severity != "critical" && finding.Severity != "error" {
				continue
			}
			add("quality:"+report.BindingID.String()+":"+finding.ID, "quality", "high", finding.Title, map[string]any{"report_id": report.ID, "commit_sha": report.Commit, "finding_id": finding.ID})
		}
	}
	proposal := Proposal{Summary: "Risk causes evaluated without changing delivery commitments", Results: out, Commands: []ProposalCommand{}, Findings: out.Unknown}
	existing := map[string]Risk{}
	for _, r := range input.Risks {
		existing[r.Key] = r
	}
	if contains(p.AllowedOperations, "create") && contains(p.AllowedFields, "name") && contains(p.AllowedFields, "requirement_type") && contains(p.AllowedFields, "description_html") && len(p.AllowedItemIDs) == 0 {
		for _, r := range out.Risks {
			if known, ok := existing[r.Key]; ok && known.ActionItemID != nil {
				continue
			}
			if len(proposal.Commands) >= p.MaxChanges {
				proposal.Partial = true
				proposal.Findings = append(proposal.Findings, "Some risk actions exceed the policy batch limit")
				break
			}
			proposal.Commands = append(proposal.Commands, actionCommand("risk:"+fingerprint(r.Key)[:20], "Risk response: "+r.Reason, "Investigate the recorded cause, confirm prerequisites, and resolve the constraint. Reassess this risk after execution. Evidence: "+string(raw(r.Evidence))))
		}
	}
	return proposal, nil
}
func actionCommand(key, title, description string) ProposalCommand {
	titleRunes := []rune(title)
	if len(titleRunes) > 255 {
		title = string(titleRunes[:255])
	}
	// The business command sanitizes this plain, escaped paragraph again.
	escaped := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;").Replace(description)
	return ProposalCommand{Operation: "create", ClientID: key, Fields: map[string]json.RawMessage{"name": raw(title), "description_html": raw("<p>" + escaped + "</p>"), "requirement_type": raw(nil)}}
}

type Metrics struct {
	From         time.Time `json:"from"`
	Through      time.Time `json:"through"`
	Throughput   int       `json:"throughput"`
	WIP          int       `json:"wip"`
	Scope        int       `json:"scope"`
	BlockedRatio float64   `json:"blocked_ratio"`
	ReopenRatio  float64   `json:"reopen_ratio"`
	Added        int       `json:"added"`
	CoverageDays float64   `json:"coverage_days"`
	Observations int       `json:"observations"`
}
type ImprovementResult struct {
	Key           string    `json:"key"`
	Reason        string    `json:"reason"`
	TargetMetric  string    `json:"target_metric"`
	Baseline      Metrics   `json:"baseline"`
	WindowStart   time.Time `json:"window_start"`
	WindowEnd     time.Time `json:"window_end"`
	Preconditions []string  `json:"preconditions"`
	Action        string    `json:"action"`
}

func Measure(input Input, from, through time.Time) Metrics {
	completed, states, start := observedHistory(input.History, through)
	m := Metrics{From: from, Through: through, Scope: len(states)}
	if start != nil {
		begin := from
		if start.After(begin) {
			begin = *start
		}
		m.CoverageDays = through.Sub(begin).Hours() / 24
		if m.CoverageDays < 0 {
			m.CoverageDays = 0
		}
	}
	for _, c := range completed {
		if !c.at.Before(from) {
			m.Throughput++
		}
	}
	reopens := 0
	for _, f := range input.History {
		if f.At.Before(from) || f.At.After(through) {
			continue
		}
		m.Observations++
		if f.Action == "insert" {
			m.Added++
		}
		if f.Action == "update" {
			var b, a historicalState
			_ = json.Unmarshal(f.Before, &b)
			_ = json.Unmarshal(f.After, &a)
			if b.CompletedAt != nil && a.CompletedAt == nil {
				reopens++
			}
		}
	}
	if m.Throughput+reopens > 0 {
		m.ReopenRatio = float64(reopens) / float64(m.Throughput+reopens)
	}
	// Current WIP and dependency status may be used only for a current snapshot.
	// Historical observation states carry state_group from the recorded event.
	for _, state := range states {
		if state.StateGroup == "started" && state.DeletedAt == nil && state.ArchivedAt == nil {
			m.WIP++
		}
	}
	blocked, active := 0, 0
	relations := map[string]struct{ Source, Target string }{}
	ordered := append([]Fact{}, input.History...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].At.Before(ordered[j].At) })
	for _, f := range ordered {
		if f.At.After(through) || !strings.HasPrefix(f.Action, "work_item_relations.") {
			continue
		}
		var relation struct {
			ID, SourceID, TargetID, RelationType string
			DeletedAt                            *time.Time
		}
		// Related history uses SQL snake_case names.
		var value struct {
			ID        string     `json:"id"`
			SourceID  string     `json:"source_id"`
			TargetID  string     `json:"target_id"`
			Type      string     `json:"relation_type"`
			DeletedAt *time.Time `json:"deleted_at"`
		}
		payload := f.After
		if len(payload) == 0 || string(payload) == "null" {
			payload = f.Before
		}
		if json.Unmarshal(payload, &value) != nil {
			continue
		}
		relation.ID = value.ID
		if f.Action == "work_item_relations.delete" || value.DeletedAt != nil || value.Type != "blocks" {
			delete(relations, relation.ID)
		} else {
			relations[relation.ID] = struct{ Source, Target string }{value.SourceID, value.TargetID}
		}
	}
	blockedIDs := map[string]bool{}
	for _, relation := range relations {
		before, ok := states[relation.Source]
		if ok && before.CompletedAt == nil && before.StateGroup != "cancelled" && before.DeletedAt == nil {
			blockedIDs[relation.Target] = true
		}
	}
	for id, state := range states {
		if state.CompletedAt != nil || state.StateGroup == "cancelled" || state.DeletedAt != nil || state.ArchivedAt != nil {
			continue
		}
		active++
		if blockedIDs[id] {
			blocked++
		}
	}
	if active > 0 {
		m.BlockedRatio = float64(blocked) / float64(active)
	}
	if input.HistoryRestricted {
		m.CoverageDays = 0
	}
	return m
}
func AnalyzeEfficiency(input Input, p Policy) Proposal {
	baseline := Measure(input, input.AsOf.AddDate(0, 0, -14), input.AsOf)
	result := ImprovementResult{Key: "reduce-wip", Reason: "Limit simultaneous work and finish the oldest started items before opening new work", TargetMetric: "wip", Baseline: baseline, WindowStart: input.AsOf, WindowEnd: input.AsOf.AddDate(0, 0, 14), Preconditions: []string{"A project member owns the action", "No hard deadlines or locked execution facts are changed", "Observe fourteen days after the action is assigned"}, Action: "Agree and apply a team work-in-progress limit; review the oldest started items daily"}
	if baseline.BlockedRatio >= .25 {
		result.Key = "reduce-blocking"
		result.TargetMetric = "blocked_ratio"
		result.Reason = "At least one quarter of open scope waits on unfinished prerequisites"
		result.Action = "Assign an owner to resolve the longest dependency chain and review blocked work daily"
	} else if baseline.ReopenRatio >= .2 {
		result.Key = "reduce-rework"
		result.TargetMetric = "reopen_ratio"
		result.Reason = "At least twenty percent of completion/reopen events represent rework"
		result.Action = "Review acceptance criteria before completion and investigate the top recurring reopen cause"
	} else if baseline.WIP <= 3 {
		result.Key = "improve-throughput"
		result.TargetMetric = "throughput"
		result.Reason = "Establish a measured review of work intake and delivery; no strong bottleneck is currently proven"
		result.Action = "Review intake, record missing estimates and blockers, then compare team throughput after fourteen days"
	}
	proposal := Proposal{Summary: "Team process improvement with a fixed baseline and observation window", Commands: []ProposalCommand{}, Findings: []string{}, Results: result}
	if baseline.CoverageDays < 14 || baseline.Observations < 5 {
		proposal.Findings = append(proposal.Findings, "Baseline evidence is insufficient; this action is exploratory and does not establish an efficiency gain")
	}
	if contains(p.AllowedOperations, "create") && len(p.AllowedItemIDs) == 0 {
		proposal.Commands = append(proposal.Commands, actionCommand("improvement", "Process improvement: "+result.Key, result.Action+". Preconditions: "+strings.Join(result.Preconditions, "; ")))
	}
	return proposal
}
func CompareImprovement(before, after Metrics, metric string, now, windowEnd time.Time) (string, []string) {
	notes := []string{}
	if now.Before(windowEnd) {
		return "observing", []string{"The fixed observation window is still open"}
	}
	if before.CoverageDays < 14 || after.CoverageDays < 14 || before.Observations < 5 || after.Observations < 5 {
		return "insufficient_evidence", []string{"Both windows require fourteen observed days and at least five recorded events"}
	}
	if before.Scope > 0 && float64(abs(after.Scope-before.Scope))/float64(before.Scope) > .25 {
		return "insufficient_evidence", []string{"Scope changed by more than twenty-five percent; causal improvement cannot be inferred"}
	}
	improved := false
	switch metric {
	case "blocked_ratio":
		improved = after.BlockedRatio < before.BlockedRatio
	case "wip":
		improved = after.WIP < before.WIP && after.Throughput >= before.Throughput
	case "throughput":
		improved = after.Throughput > before.Throughput
	case "reopen_ratio":
		improved = after.ReopenRatio < before.ReopenRatio
	default:
		return "insufficient_evidence", []string{"Unknown target metric"}
	}
	notes = append(notes, fmt.Sprintf("Compared fixed observation windows for %s; association does not establish causation", metric))
	if improved {
		return "improved", notes
	}
	return "no_improvement", notes
}
func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
