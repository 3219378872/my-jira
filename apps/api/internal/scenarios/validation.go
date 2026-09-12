package scenarios

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"my-jira/apps/api/internal/support/data"
)

var contentFields = []string{"story_id", "name", "goal", "trigger", "preconditions", "outcome", "system_boundary", "bind_story_name", "source_page_ids", "participants", "steps", "relationships", "version", "review_needed"}

func decodeContent(input data.Object, previous *Content) (Content, error) {
	value := Content{BindStoryName: true, SourcePageIDs: []uuid.UUID{}, Participants: []Participant{}, Steps: []Step{}, Relationships: []Relationship{}}
	if previous != nil {
		value = *previous
	}
	copy := data.Object{}
	for field, raw := range input {
		if field == "version" || field == "review_needed" {
			continue
		}
		if string(raw) == "null" {
			return value, data.Invalid(field + " cannot be null")
		}
		copy[field] = raw
	}
	raw, err := json.Marshal(copy)
	if err != nil {
		return value, err
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, data.Invalid("Invalid scenario content: " + err.Error())
	}
	return value, nil
}

func checkText(field, value string, maximum int, required bool) error {
	if required && strings.TrimSpace(value) == "" {
		return data.Invalid(field + " is required")
	}
	if len([]rune(value)) > maximum {
		return data.Invalid(fmt.Sprintf("%s supports at most %d characters", field, maximum))
	}
	for _, char := range value {
		if (unicode.IsControl(char) && char != '\n' && char != '\t' && char != '\r') || char == '\ufffe' || char == '\uffff' {
			return data.Invalid(field + " contains an unsupported control character")
		}
	}
	return nil
}

func validateContent(value Content, scenarioID uuid.UUID) error {
	if value.StoryID == uuid.Nil {
		return data.Invalid("story_id must identify a Story")
	}
	for _, field := range []struct {
		name, value string
		maximum     int
		required    bool
	}{{"name", value.Name, 240, true}, {"goal", value.Goal, 4000, false}, {"trigger", value.Trigger, 4000, false}, {"preconditions", value.Preconditions, 8000, false}, {"outcome", value.Outcome, 8000, false}, {"system_boundary", value.SystemBoundary, 240, false}} {
		if err := checkText(field.name, field.value, field.maximum, field.required); err != nil {
			return err
		}
	}
	if len(value.Participants) > 24 || len(value.Steps) > 300 || len(value.Relationships) > 200 || len(value.SourcePageIDs) > 50 {
		return data.Invalid("A scenario supports at most 24 participants, 300 steps, 200 relationships and 50 source pages")
	}
	ids := map[uuid.UUID]bool{scenarioID: true}
	participants := map[uuid.UUID]string{}
	for _, participant := range value.Participants {
		if participant.ID == uuid.Nil || ids[participant.ID] {
			return data.Invalid("Participant IDs must be unique nonzero UUIDs")
		}
		if participant.Kind != "actor" && participant.Kind != "system" {
			return data.Invalid("Participant kind must be actor or system")
		}
		if err := checkText("participant name", participant.Name, 160, true); err != nil {
			return err
		}
		ids[participant.ID], participants[participant.ID] = true, participant.Kind
	}
	sources := map[uuid.UUID]bool{}
	for _, id := range value.SourcePageIDs {
		if id == uuid.Nil || sources[id] {
			return data.Invalid("Source page IDs must be unique nonzero UUIDs")
		}
		sources[id] = true
	}
	type call struct {
		step   Step
		branch string
	}
	calls := map[uuid.UUID]call{}
	returns := map[string]bool{}
	branch, alt := "", uuid.Nil
	hasElse, branchMessages := false, 0
	for _, step := range value.Steps {
		if step.ID == uuid.Nil || ids[step.ID] {
			return data.Invalid("Step IDs must be unique nonzero UUIDs")
		}
		ids[step.ID] = true
		if err := checkText("step message", step.Message, 1000, step.Kind != "end"); err != nil {
			return err
		}
		switch step.Kind {
		case "call", "return":
			if participants[step.FromID] == "" || participants[step.ToID] == "" {
				return data.Invalid("Every message must refer to existing participants")
			}
			branchMessages++
			if step.Kind == "call" {
				if step.ReturnOf != uuid.Nil {
					return data.Invalid("Only return steps may have return_of")
				}
				calls[step.ID] = call{step, branch}
			} else {
				original, ok := calls[step.ReturnOf]
				if !ok || original.step.FromID != step.ToID || original.step.ToID != step.FromID || (original.branch != "" && original.branch != branch) {
					return data.Invalid("A return must reverse an earlier call on the same possible execution branch")
				}
				key := step.ReturnOf.String() + ":" + branch
				if returns[key] || (branch != "" && returns[step.ReturnOf.String()+":"]) {
					return data.Invalid("A call can return only once on an execution branch")
				}
				returns[key] = true
			}
		case "alt", "else", "end":
			if step.FromID != uuid.Nil || step.ToID != uuid.Nil || step.ReturnOf != uuid.Nil {
				return data.Invalid("Branch markers cannot specify participants or return_of")
			}
			switch step.Kind {
			case "alt":
				if alt != uuid.Nil {
					return data.Invalid("Nested alternatives are not supported")
				}
				alt, hasElse, branchMessages = step.ID, false, 0
				branch = alt.String() + ":if"
			case "else":
				if alt == uuid.Nil || hasElse || branchMessages == 0 {
					return data.Invalid("else requires one nonempty alt branch")
				}
				hasElse, branchMessages = true, 0
				branch = alt.String() + ":else"
			case "end":
				if alt == uuid.Nil || !hasElse || branchMessages == 0 {
					return data.Invalid("end requires a complete nonempty alt/else block")
				}
				// A call returned on either alternative has already returned on at
				// least one possible path. Returning it again after the branch (or
				// in a later alternative) would duplicate a response on that path.
				for callID, original := range calls {
					if original.branch == "" && (returns[callID.String()+":"+alt.String()+":if"] || returns[callID.String()+":"+alt.String()+":else"]) {
						returns[callID.String()+":"] = true
					}
				}
				alt, branch = uuid.Nil, ""
			}
		default:
			return data.Invalid("Step kind must be call, return, alt, else or end")
		}
	}
	if alt != uuid.Nil {
		return data.Invalid("Every alt block must end with else and end")
	}
	for _, rel := range value.Relationships {
		if rel.ID == uuid.Nil || ids[rel.ID] || rel.FromID == uuid.Nil || rel.ToID == uuid.Nil || rel.FromID == rel.ToID {
			return data.Invalid("Relationship IDs and distinct endpoints must be valid UUIDs")
		}
		ids[rel.ID] = true
		switch rel.Kind {
		case "association":
			if participants[rel.FromID] == "" || participants[rel.ToID] != "" {
				return data.Invalid("An association connects a participant to a scenario")
			}
		case "include", "extend", "generalization":
			if participants[rel.FromID] != "" || participants[rel.ToID] != "" || (rel.FromID != scenarioID && rel.ToID != scenarioID) {
				return data.Invalid("Use-case relationships must connect this scenario with another scenario")
			}
		default:
			return data.Invalid("Unknown use-case relationship kind")
		}
	}
	return nil
}
