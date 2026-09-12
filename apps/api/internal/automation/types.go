// Package automation executes versioned project policies through the same
// transactional commands as interactive planning. Model output is untrusted.
package automation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"my-jira/apps/api/internal/support/data"
)

const TaskType = "automation.run.v1"
const AlgorithmVersion = "myjira-automation-1"

var AllowedFields = []string{"name", "description_html", "requirement_type", "story_role", "story_goal", "story_benefit", "acceptance_criteria", "parent_id", "estimated_minutes", "remaining_minutes", "required_skills", "start_date", "target_date", "assignee_ids", "allocation_weights", "dependency_ids", "cycle_id", "activity_id", "map_position", "priority", "state_id"}
var Kinds = []string{"decompose", "schedule", "forecast", "risk", "efficiency", "quality"}

type Policy struct {
	Version            int64       `json:"version"`
	Enabled            bool        `json:"enabled"`
	AllowedKinds       []string    `json:"allowed_kinds"`
	AllowedEntities    []string    `json:"allowed_entities"`
	AllowedFields      []string    `json:"allowed_fields"`
	AllowedOperations  []string    `json:"allowed_operations"`
	AllowedItemIDs     []uuid.UUID `json:"allowed_item_ids"`
	AllowedMemberIDs   []uuid.UUID `json:"allowed_member_ids"`
	AllowedPageIDs     []uuid.UUID `json:"allowed_page_ids"`
	HardDeadline       string      `json:"hard_deadline"`
	MaxChanges         int         `json:"max_changes"`
	BudgetCalls        int         `json:"budget_calls"`
	MinIntervalSeconds int         `json:"min_interval_seconds"`
	MaxRounds          int         `json:"max_rounds"`
	AuthorizedBy       uuid.UUID   `json:"authorized_by"`
	CallsUsed          int         `json:"calls_used"`
}

func DefaultPolicy() Policy {
	return Policy{Version: 0, AllowedKinds: append([]string{}, Kinds...), AllowedEntities: []string{"epic", "story", "task", "work_item"}, AllowedFields: append([]string{}, AllowedFields...), AllowedOperations: []string{"create", "update"}, AllowedItemIDs: []uuid.UUID{}, AllowedMemberIDs: []uuid.UUID{}, AllowedPageIDs: []uuid.UUID{}, MaxChanges: 30, BudgetCalls: 100, MinIntervalSeconds: 60, MaxRounds: 3}
}
func contains[T comparable](values []T, value T) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
func (p Policy) Validate() error {
	if p.Version < 0 || p.MaxChanges < 1 || p.MaxChanges > 100 || p.BudgetCalls < 0 || p.BudgetCalls > 10000 || p.MinIntervalSeconds < 0 || p.MinIntervalSeconds > 86400 || p.MaxRounds < 1 || p.MaxRounds > 10 {
		return data.Invalid("Policy limits are outside their supported range")
	}
	for _, v := range p.AllowedKinds {
		if !contains(Kinds, v) {
			return data.Invalid("Unknown automation kind: " + v)
		}
	}
	for _, v := range p.AllowedEntities {
		if !contains([]string{"epic", "story", "task", "work_item"}, v) {
			return data.Invalid("Unknown automatic entity type: " + v)
		}
	}
	for _, v := range p.AllowedFields {
		if !contains(AllowedFields, v) {
			return data.Invalid("Automation cannot modify field: " + v)
		}
	}
	for _, v := range p.AllowedOperations {
		if v != "create" && v != "update" {
			return data.Invalid("Automatic operations must be create or update")
		}
	}
	for _, values := range [][]uuid.UUID{p.AllowedItemIDs, p.AllowedMemberIDs, p.AllowedPageIDs} {
		if len(values) > 500 {
			return data.Invalid("Policy scope exceeds 500 objects")
		}
		for _, v := range values {
			if v == uuid.Nil {
				return data.Invalid("Policy contains an invalid object ID")
			}
		}
	}
	if p.HardDeadline != "" {
		if _, e := time.Parse(time.DateOnly, p.HardDeadline); e != nil {
			return data.Invalid("Hard deadline must use YYYY-MM-DD")
		}
	}
	return nil
}

type Source struct {
	PageID          *uuid.UUID `json:"page_id,omitempty"`
	Revision        int64      `json:"revision,omitempty"`
	ObservedVersion int64      `json:"observed_version,omitempty"`
	Text            string     `json:"text,omitempty"`
	Key             string     `json:"key,omitempty"`
	Private         bool       `json:"private,omitempty"`
	OwnerID         *uuid.UUID `json:"owner_id,omitempty"`
}
type Request struct {
	Kind           string      `json:"kind"`
	IdempotencyKey string      `json:"idempotency_key"`
	Source         Source      `json:"source"`
	Cause          string      `json:"cause"`
	Round          int         `json:"round"`
	StartDate      string      `json:"start_date"`
	EndDate        string      `json:"end_date"`
	TaskIDs        []uuid.UUID `json:"task_ids"`
}
type Item struct {
	ID               uuid.UUID       `json:"id"`
	Version          int64           `json:"version"`
	Name             string          `json:"name"`
	RequirementType  string          `json:"requirement_type"`
	StateGroup       string          `json:"state_group"`
	PlanningLocked   bool            `json:"planning_locked"`
	ParentID         *uuid.UUID      `json:"parent_id"`
	StartDate        *string         `json:"start_date"`
	TargetDate       *string         `json:"target_date"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
	CompletedAt      *time.Time      `json:"completed_at"`
	RemainingMinutes *int            `json:"remaining_minutes"`
	EstimatedMinutes *int            `json:"estimated_minutes"`
	AssigneeIDs      []uuid.UUID     `json:"assignee_ids"`
	DependencyIDs    []uuid.UUID     `json:"dependency_ids"`
	RequiredSkills   []string        `json:"required_skills"`
	Raw              json.RawMessage `json:"fields"`
}

func (i Item) Protected() bool {
	return i.PlanningLocked || i.StateGroup == "started" || i.StateGroup == "completed" || i.StateGroup == "cancelled"
}

type Mapping struct {
	RunID          uuid.UUID                  `json:"run_id"`
	SourceRevision string                     `json:"source_revision"`
	SourceKey      string                     `json:"source_key"`
	EntityKey      string                     `json:"entity_key"`
	ItemID         uuid.UUID                  `json:"work_item_id"`
	LastVersion    int64                      `json:"last_item_version"`
	Fields         map[string]json.RawMessage `json:"generated_fields"`
	Location       Location                   `json:"source_location"`
}
type Input struct {
	AsOf              time.Time         `json:"as_of"`
	ProjectRevision   int64             `json:"project_revision"`
	Source            Source            `json:"source"`
	Items             []Item            `json:"items"`
	Mappings          []Mapping         `json:"mappings"`
	Resources         json.RawMessage   `json:"resources"`
	Request           Request           `json:"request"`
	History           []Fact            `json:"history"`
	HistoryRestricted bool              `json:"history_restricted"`
	Quality           []json.RawMessage `json:"quality"`
	Risks             []Risk            `json:"risks"`
	External          json.RawMessage   `json:"external,omitempty"`
}
type Run struct {
	ID               uuid.UUID       `json:"id"`
	WorkspaceID      uuid.UUID       `json:"workspace_id"`
	ProjectID        uuid.UUID       `json:"project_id"`
	RequestedBy      uuid.UUID       `json:"requested_by"`
	AuthorizedBy     uuid.UUID       `json:"authorized_by"`
	Kind             string          `json:"kind"`
	Status           string          `json:"status"`
	PolicyVersion    int64           `json:"policy_version"`
	InputFingerprint string          `json:"input_fingerprint"`
	Input            Input           `json:"input"`
	Output           json.RawMessage `json:"output"`
	Cause            string          `json:"cause"`
	Round            int             `json:"round"`
	Attempts         int             `json:"attempts"`
	Failure          string          `json:"failure"`
}
type Fact struct {
	ItemID uuid.UUID       `json:"work_item_id"`
	At     time.Time       `json:"at"`
	Action string          `json:"action"`
	Before json.RawMessage `json:"before"`
	After  json.RawMessage `json:"after"`
}

func fingerprint(value any) string {
	encoded, _ := json.Marshal(value)
	var canonical any
	if json.Unmarshal(encoded, &canonical) == nil {
		encoded, _ = json.Marshal(canonical)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}
func raw(value any) json.RawMessage { v, _ := json.Marshal(value); return v }
func cleanDate(v *string) string {
	if v == nil {
		return ""
	}
	if len(*v) >= 10 {
		return (*v)[:10]
	}
	return *v
}
func sortedIDs(ids []uuid.UUID) []uuid.UUID {
	result := append([]uuid.UUID{}, ids...)
	sort.Slice(result, func(i, j int) bool { return result[i].String() < result[j].String() })
	return result
}
func validateRequest(b Request) error {
	if !contains(Kinds, b.Kind) || b.Kind == "quality" {
		return data.Invalid("Choose decompose, schedule, forecast, risk or efficiency")
	}
	if len(strings.TrimSpace(b.IdempotencyKey)) < 1 || len(b.IdempotencyKey) > 160 || len(b.Cause) > 160 || b.Round < 0 {
		return data.Invalid("Supply an idempotency key and a bounded cause chain")
	}
	if len(b.Source.Text) > 50000 || len(b.Source.Key) > 160 || b.Source.Revision < 0 {
		return data.Invalid("PRD input exceeds its bounds")
	}
	if b.Kind == "decompose" && ((b.Source.PageID == nil) == (strings.TrimSpace(b.Source.Text) == "")) {
		return data.Invalid("Choose one page revision or upload a text PRD")
	}
	if b.Source.PageID != nil && (*b.Source.PageID == uuid.Nil || b.Source.Revision < 1) {
		return data.Invalid("Page input requires an exact positive revision")
	}
	for _, d := range []string{b.StartDate, b.EndDate} {
		if d != "" {
			if _, e := time.Parse(time.DateOnly, d); e != nil {
				return data.Invalid("Schedule dates must use YYYY-MM-DD")
			}
		}
	}
	if len(b.TaskIDs) > 500 {
		return data.Invalid("At most 500 selected tasks are supported")
	}
	return nil
}
func blocked(format string, args ...any) error { return data.Conflict(fmt.Sprintf(format, args...)) }
