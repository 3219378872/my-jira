package automation

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"math"
	"math/rand"
	"sort"
	"time"
)

const ForecastVersion = "weekly-throughput-bootstrap-1"

type Forecast struct {
	AsOf             time.Time  `json:"as_of"`
	Status           string     `json:"status"`
	P50              *string    `json:"p50"`
	P80              *string    `json:"p80"`
	SampleCount      int        `json:"sample_count"`
	CoverageStart    *time.Time `json:"coverage_start"`
	Remaining        int        `json:"remaining"`
	Assumptions      []string   `json:"assumptions"`
	Backtest         Backtest   `json:"backtest"`
	AlgorithmVersion string     `json:"algorithm_version"`
	Stale            bool       `json:"stale"`
}
type Backtest struct {
	Windows               int      `json:"windows"`
	MeanAbsoluteErrorDays *float64 `json:"mean_absolute_error_days"`
	P80Coverage           *float64 `json:"p80_coverage"`
	BaselineErrorDays     *float64 `json:"baseline_error_days"`
	Method                string   `json:"method"`
}
type historicalState struct {
	ID              uuidID     `json:"id"`
	CreatedAt       time.Time  `json:"created_at"`
	CompletedAt     *time.Time `json:"completed_at"`
	DeletedAt       *time.Time `json:"deleted_at"`
	ArchivedAt      *time.Time `json:"archived_at"`
	ParentID        *string    `json:"parent_id"`
	RequirementType string     `json:"requirement_type"`
	StateGroup      string     `json:"state_group"`
}

// A string avoids conflating missing IDs with a synthetically generated UUID.
type uuidID string
type completion struct {
	id string
	at time.Time
}

// observedHistory reconstructs only facts recorded by cutoff. It never reads
// today's work-item status to fill gaps in a historical backtest.
func observedHistory(facts []Fact, cutoff time.Time) ([]completion, map[string]historicalState, *time.Time) {
	ordered := append([]Fact{}, facts...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].At.Before(ordered[j].At) })
	states := map[string]historicalState{}
	completions := []completion{}
	seen := map[string]bool{}
	var start *time.Time
	for _, f := range ordered {
		if f.At.After(cutoff) {
			continue
		}
		if f.Action != "insert" && f.Action != "update" && f.Action != "delete" && f.Action != "coverage_started" {
			continue
		}
		if start == nil {
			t := f.At
			start = &t
		}
		var before, after historicalState
		_ = json.Unmarshal(f.Before, &before)
		_ = json.Unmarshal(f.After, &after)
		id := f.ItemID.String()
		if string(f.After) == "null" || len(f.After) == 0 {
			delete(states, id)
			continue
		}
		// The coverage snapshot describes state at migration time, but does not
		// prove the time or sequence of earlier completion events.
		if f.Action != "coverage_started" && after.CompletedAt != nil && !after.CompletedAt.After(cutoff) && (before.CompletedAt == nil || !before.CompletedAt.Equal(*after.CompletedAt)) {
			key := id + after.CompletedAt.UTC().Format(time.RFC3339Nano)
			if !seen[key] {
				completions = append(completions, completion{id, *after.CompletedAt})
				seen[key] = true
			}
		}
		states[id] = after
	}
	// Parent containers do not count twice in throughput or remaining scope.
	parents := map[string]bool{}
	for _, s := range states {
		if s.ParentID != nil {
			parents[*s.ParentID] = true
		}
	}
	filtered := completions[:0]
	for _, c := range completions {
		if !parents[c.id] {
			filtered = append(filtered, c)
		}
	}
	return filtered, states, start
}
func remainingScope(states map[string]historicalState) []string {
	parents := map[string]bool{}
	for _, s := range states {
		if s.ParentID != nil {
			parents[*s.ParentID] = true
		}
	}
	result := []string{}
	for id, s := range states {
		if !parents[id] && s.CompletedAt == nil && s.StateGroup != "cancelled" && s.DeletedAt == nil && s.ArchivedAt == nil && s.RequirementType != "epic" {
			result = append(result, id)
		}
	}
	sort.Strings(result)
	return result
}
func ForecastDelivery(input Input) Forecast {
	f := forecastAt(input.History, input.AsOf, 0)
	if input.HistoryRestricted {
		f.Status = "insufficient_data"
		f.P50 = nil
		f.P80 = nil
		f.Assumptions = append(f.Assumptions, "Some historical work is no longer in the authorized project scope. Forecast and backtest confidence are withheld to avoid survivor bias.")
		return f
	}
	f.Backtest = backtest(input.History, input.AsOf)
	return f
}
func forecastAt(facts []Fact, asOf time.Time, remainingOverride int) Forecast {
	completions, states, start := observedHistory(facts, asOf)
	remaining := len(remainingScope(states))
	if remainingOverride > 0 {
		remaining = remainingOverride
	}
	f := Forecast{AsOf: asOf, Status: "insufficient_data", Remaining: remaining, CoverageStart: start, AlgorithmVersion: ForecastVersion, Assumptions: []string{"Fixed remaining scope; future scope changes require a new forecast", "Weekly historical throughput is resampled with a deterministic seed; work-item containers are excluded", "Only facts recorded by the forecast cutoff are used; pre-migration completion dates are not reconstructed", "This is a team delivery distribution, not measured personal effort"}, Backtest: Backtest{Method: "Expanding weekly time splits; later facts are used only to score outcomes"}}
	unique := map[string]bool{}
	for _, c := range completions {
		unique[c.id] = true
	}
	f.SampleCount = len(unique)
	if start == nil || f.SampleCount < 20 || asOf.Sub(*start) < 28*24*time.Hour {
		return f
	}
	if remaining == 0 {
		d := asOf.Format(time.DateOnly)
		f.Status = "ready"
		f.P50 = &d
		f.P80 = &d
		return f
	}
	origin := start.UTC().Truncate(24 * time.Hour)
	days := int(asOf.UTC().Truncate(24*time.Hour).Sub(origin) / (24 * time.Hour))
	weeks := days / 7
	if weeks < 4 {
		return f
	}
	blocks := make([][7]int, weeks)
	for _, c := range completions {
		index := int(c.at.UTC().Truncate(24*time.Hour).Sub(origin) / (24 * time.Hour))
		if index >= 0 && index < weeks*7 {
			blocks[index/7][index%7]++
		}
	}
	sum := 0
	for _, b := range blocks {
		for _, n := range b {
			sum += n
		}
	}
	if sum == 0 {
		return f
	}
	seed := sha256.Sum256([]byte(asOf.UTC().Format(time.RFC3339Nano) + "|" + fingerprint(completionsForSeed(completions))))
	rng := rand.New(rand.NewSource(int64(binary.LittleEndian.Uint64(seed[:8]))))
	results := make([]int, 2000)
	for iteration := range results {
		done, elapsed := 0, 0
		for done < remaining && elapsed < 3650 {
			b := blocks[rng.Intn(len(blocks))]
			for _, n := range b {
				elapsed++
				done += n
				if done >= remaining || elapsed >= 3650 {
					break
				}
			}
		}
		results[iteration] = elapsed
	}
	sort.Ints(results)
	p50 := asOf.AddDate(0, 0, results[len(results)/2]).Format(time.DateOnly)
	p80 := asOf.AddDate(0, 0, results[int(float64(len(results)-1)*.8)]).Format(time.DateOnly)
	f.Status = "ready"
	f.P50 = &p50
	f.P80 = &p80
	if results[len(results)-1] >= 3650 {
		f.Assumptions = append(f.Assumptions, "Some simulations exceed the ten-year horizon; their dates are censored")
	}
	return f
}
func completionsForSeed(c []completion) []string {
	r := make([]string, len(c))
	for i, v := range c {
		r[i] = v.id + v.at.UTC().Format(time.RFC3339Nano)
	}
	return r
}
func backtest(facts []Fact, asOf time.Time) Backtest {
	b := Backtest{Method: "Expanding weekly time splits; later facts are used only to score outcomes"}
	_, _, start := observedHistory(facts, asOf)
	if start == nil {
		return b
	}
	errors, baselineErrors, covered := 0.0, 0.0, 0.0
	for cutoff := start.Add(28 * 24 * time.Hour); cutoff.Before(asOf); cutoff = cutoff.Add(7 * 24 * time.Hour) {
		predicted := forecastAt(facts, cutoff, 0)
		if predicted.Status != "ready" || predicted.Remaining == 0 {
			continue
		}
		observed, states, _ := observedHistory(facts, cutoff)
		scope := remainingScope(states)
		need := map[string]bool{}
		for _, id := range scope {
			need[id] = true
		}
		actual := cutoff
		future, _, _ := observedHistory(facts, asOf)
		sort.Slice(future, func(i, j int) bool { return future[i].at.Before(future[j].at) })
		for _, c := range future {
			if c.at.After(cutoff) && need[c.id] {
				delete(need, c.id)
				if c.at.After(actual) {
					actual = c.at
				}
			}
		}
		if len(need) > 0 {
			continue
		}
		median, _ := time.Parse(time.DateOnly, *predicted.P50)
		upper, _ := time.Parse(time.DateOnly, *predicted.P80)
		errors += math.Abs(actual.Sub(median).Hours() / 24)
		if !actual.UTC().Truncate(24 * time.Hour).After(upper) {
			covered++
		}
		rate := float64(len(observed)) / math.Max(1, cutoff.Sub(*start).Hours()/24)
		baseline := cutoff.Add(time.Duration(float64(len(scope)) / rate * float64(24*time.Hour)))
		baselineErrors += math.Abs(actual.Sub(baseline).Hours() / 24)
		b.Windows++
	}
	if b.Windows > 0 {
		n := float64(b.Windows)
		err := errors / n
		base := baselineErrors / n
		coverage := covered / n
		b.MeanAbsoluteErrorDays = &err
		b.BaselineErrorDays = &base
		b.P80Coverage = &coverage
	}
	return b
}
