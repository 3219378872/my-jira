package scenarios

import (
	"encoding/xml"
	"io"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func exampleContent() (Content, uuid.UUID) {
	id, story := uuid.New(), uuid.New()
	a, b, callID := uuid.New(), uuid.New(), uuid.New()
	return Content{StoryID: story, Name: "Confirm a booking", Goal: "A customer receives a confirmed booking", SystemBoundary: "Booking service", BindStoryName: true,
		SourcePageIDs: []uuid.UUID{},
		Participants:  []Participant{{a, "Customer", "actor"}, {b, "Booking service", "system"}},
		Steps:         []Step{{ID: callID, Kind: "call", FromID: a, ToID: b, Message: "Request a booking"}, {ID: uuid.New(), Kind: "return", FromID: b, ToID: a, Message: "Booking confirmed", ReturnOf: callID}},
		Relationships: []Relationship{{ID: uuid.New(), Kind: "association", FromID: a, ToID: id}}}, id
}

func TestSequenceValidatesReturnDirectionOrderAndParticipantIdentity(t *testing.T) {
	good, id := exampleContent()
	if err := validateContent(good, id); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Content){
		func(c *Content) { c.Steps[1].ReturnOf = uuid.New() },
		func(c *Content) { c.Steps[1].ToID = c.Steps[1].FromID },
		func(c *Content) { c.Steps[0], c.Steps[1] = c.Steps[1], c.Steps[0] },
		func(c *Content) { c.Participants = c.Participants[:1] },
		func(c *Content) { c.Participants[1].ID = c.Participants[0].ID },
		func(c *Content) { c.Steps[1].ID = c.Steps[0].ID },
		func(c *Content) { c.Steps[0].ReturnOf = c.Steps[1].ID },
	} {
		bad, sid := exampleContent()
		mutate(&bad)
		if err := validateContent(bad, sid); err == nil {
			t.Fatal("Invalid return, order or participant deletion was accepted")
		}
	}
	// A replacement changes the business participant while preserving stable IDs
	// and all message references; it never edits a platform user or role.
	good.Participants[1].Name = "Replacement booking system"
	if err := validateContent(good, id); err != nil {
		t.Fatal(err)
	}
}

func TestOneLevelAlternativesAndBranchLocalReturns(t *testing.T) {
	value, id := exampleContent()
	call := value.Steps[0]
	returnStep := value.Steps[1]
	value.Steps = []Step{{ID: uuid.New(), Kind: "alt", Message: "room available"}, call, returnStep,
		{ID: uuid.New(), Kind: "else", Message: "fully booked"},
		{ID: uuid.New(), Kind: "call", FromID: call.FromID, ToID: call.ToID, Message: "Join waitlist"},
		{ID: uuid.New(), Kind: "end"}}
	if err := validateContent(value, id); err != nil {
		t.Fatal(err)
	}
	bad := value
	bad.Steps = append([]Step{}, value.Steps...)
	bad.Steps[4] = returnStep
	bad.Steps[4].ID = uuid.New()
	if err := validateContent(bad, id); err == nil {
		t.Fatal("else branch returned a call that occurs only in the alt branch")
	}
	bad.Steps[4] = Step{ID: uuid.New(), Kind: "alt", Message: "nested"}
	if err := validateContent(bad, id); err == nil {
		t.Fatal("Nested alternative was accepted")
	}
	bad.Steps = value.Steps[:len(value.Steps)-1]
	if err := validateContent(bad, id); err == nil {
		t.Fatal("Unclosed alternative was accepted")
	}
	// A main-flow call can return differently in the two alternatives, but it
	// cannot return a third time after those branches merge.
	value.Steps = []Step{call, {ID: uuid.New(), Kind: "alt", Message: "room available"}, returnStep,
		{ID: uuid.New(), Kind: "else", Message: "fully booked"},
		{ID: uuid.New(), Kind: "return", FromID: returnStep.FromID, ToID: returnStep.ToID, Message: "Unavailable", ReturnOf: call.ID},
		{ID: uuid.New(), Kind: "end"}}
	if err := validateContent(value, id); err != nil {
		t.Fatal(err)
	}
	value.Steps = append(value.Steps, Step{ID: uuid.New(), Kind: "return", FromID: returnStep.FromID, ToID: returnStep.ToID, Message: "Duplicate after branch", ReturnOf: call.ID})
	if err := validateContent(value, id); err == nil {
		t.Fatal("A call returned again after an alternative already returned it")
	}
}

func TestCopyRemapsInternalReferencesWithoutMutatingOriginal(t *testing.T) {
	content, id := exampleContent()
	source := uuid.New()
	original := Scenario{Content: content, ID: id, ProvenanceSourcePageIDs: []uuid.UUID{source}}
	copyID := uuid.New()
	clone := cloneContent(original, copyID, "Alternative booking")
	if err := validateContent(clone, copyID); err != nil {
		t.Fatal(err)
	}
	if clone.Steps[0].ID == original.Steps[0].ID || clone.Participants[0].ID == original.Participants[0].ID || clone.Steps[1].ReturnOf != clone.Steps[0].ID || clone.Relationships[0].ToID != copyID {
		t.Fatal("Copied diagram identity or message references are invalid")
	}
	if original.Steps[1].ReturnOf != original.Steps[0].ID || original.Relationships[0].ToID != id {
		t.Fatal("Copy mutated original scenario")
	}
	if len(clone.SourcePageIDs) != 1 || clone.SourcePageIDs[0] != source {
		t.Fatal("Copy dropped retained private source provenance")
	}
}

func assertSafeSVG(t *testing.T, source string) {
	t.Helper()
	allowed := map[string]bool{"svg": true, "title": true, "defs": true, "marker": true, "path": true, "rect": true, "text": true, "line": true, "circle": true, "ellipse": true, "g": true, "a": true}
	decoder := xml.NewDecoder(strings.NewReader(source))
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Invalid standalone SVG XML: %v", err)
		}
		if element, ok := token.(xml.StartElement); ok {
			if !allowed[element.Name.Local] {
				t.Fatalf("Unsafe SVG element %q", element.Name.Local)
			}
			for _, attr := range element.Attr {
				if strings.HasPrefix(strings.ToLower(attr.Name.Local), "on") || attr.Name.Local == "style" || (attr.Name.Local == "href" && !strings.HasPrefix(attr.Value, "#")) {
					t.Fatalf("Unsafe SVG attribute %#v", attr)
				}
			}
		}
	}
}

func TestOriginalSVGIsEscapedAndContainsStableSourceLinks(t *testing.T) {
	content, id := exampleContent()
	injection := `</text><script>alert("x")</script><image href="https://attacker.test/x" onload="alert(1)"/> & "`
	content.Name, content.SystemBoundary, content.Participants[0].Name, content.Steps[0].Message = injection, injection, injection, injection
	value := Scenario{ID: id, Content: content, Version: 1, Story: StoryContext{ID: content.StoryID, Name: injection, StateName: "In progress"}}
	for _, svg := range []string{SequenceSVG(value), UseCaseSVG([]Scenario{value})} {
		assertSafeSVG(t, svg)
		if strings.Contains(svg, `<script>`) || !strings.Contains(svg, `&lt;script&gt;`) {
			t.Fatal("User supplied markup was not represented as escaped text")
		}
		if !strings.Contains(svg, `data-story-id="`+content.StoryID.String()+`"`) || !strings.Contains(svg, `data-scenario-id="`+id.String()+`"`) {
			t.Fatal("SVG lacks source navigation")
		}
	}
	if svg := SequenceSVG(value); !strings.Contains(svg, `data-step-id="`+content.Steps[0].ID.String()+`"`) {
		t.Fatal("Sequence diagram lost stable step navigation")
	}
}

func TestUseCasesOnlyRenderExplicitVisibleRelationships(t *testing.T) {
	content, id := exampleContent()
	value := Scenario{Content: content, ID: id, Story: StoryContext{Name: "Book room"}}
	value.Relationships = nil
	if strings.Contains(UseCaseSVG([]Scenario{value}), "data-relationship-id") {
		t.Fatal("A UML relationship was inferred without explicit authoring")
	}
	relationID := uuid.New()
	value.Relationships = []Relationship{{ID: relationID, Kind: "include", FromID: id, ToID: uuid.New()}}
	if strings.Contains(UseCaseSVG([]Scenario{value}), relationID.String()) {
		t.Fatal("A relationship to a hidden scenario was rendered")
	}
}
