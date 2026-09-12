// Package scenarios implements independently authored, structured business UML.
// No diagram is inferred from work-item dependencies or scheduling dates.
package scenarios

import (
	"time"

	"github.com/google/uuid"
)

type Participant struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
	Kind string    `json:"kind"`
}

type Step struct {
	ID       uuid.UUID `json:"id"`
	Kind     string    `json:"kind"`
	FromID   uuid.UUID `json:"from_id,omitempty"`
	ToID     uuid.UUID `json:"to_id,omitempty"`
	Message  string    `json:"message"`
	ReturnOf uuid.UUID `json:"return_of,omitempty"`
}

type Relationship struct {
	ID     uuid.UUID `json:"id"`
	Kind   string    `json:"kind"`
	FromID uuid.UUID `json:"from_id"`
	ToID   uuid.UUID `json:"to_id"`
}

// Content is versioned. Array order is the only source of sequence step order.
// Participant and step UUIDs remain stable through reordering and editing.
type Content struct {
	StoryID        uuid.UUID      `json:"story_id"`
	Name           string         `json:"name"`
	Goal           string         `json:"goal"`
	Trigger        string         `json:"trigger"`
	Preconditions  string         `json:"preconditions"`
	Outcome        string         `json:"outcome"`
	SystemBoundary string         `json:"system_boundary"`
	BindStoryName  bool           `json:"bind_story_name"`
	SourcePageIDs  []uuid.UUID    `json:"source_page_ids"`
	Participants   []Participant  `json:"participants"`
	Steps          []Step         `json:"steps"`
	Relationships  []Relationship `json:"relationships"`
	// Captured by the server when created or explicitly reviewed. Historical
	// renders retain these source revisions while rechecking current access.
	SourceStoryVersion int64            `json:"source_story_version"`
	SourcePageVersions map[string]int64 `json:"source_page_versions"`
}

type StoryContext struct {
	ID         uuid.UUID `json:"id"`
	Name       string    `json:"name"`
	StateID    uuid.UUID `json:"state_id"`
	StateName  string    `json:"state_name"`
	StateGroup string    `json:"state_group"`
	Version    int64     `json:"version"`
}

type Scenario struct {
	Content
	ID                      uuid.UUID    `json:"id"`
	WorkspaceID             uuid.UUID    `json:"workspace_id"`
	ProjectID               uuid.UUID    `json:"project_id"`
	Version                 int64        `json:"version"`
	ReviewNeeded            bool         `json:"review_needed"`
	Story                   StoryContext `json:"story"`
	ProvenanceSourcePageIDs []uuid.UUID  `json:"provenance_source_page_ids"`
	CreatedAt               time.Time    `json:"created_at"`
	UpdatedAt               time.Time    `json:"updated_at"`
}

type Version struct {
	ID        uuid.UUID `json:"id"`
	Version   int64     `json:"version"`
	CreatedAt time.Time `json:"created_at"`
}

func (s Scenario) label() string {
	if s.BindStoryName {
		return s.Story.Name
	}
	return s.Name
}
