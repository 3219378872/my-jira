package scenarios

import (
	"fmt"
	"html"
	"sort"
	"strings"

	"github.com/google/uuid"
)

// SVG output contains only original geometric elements, fixed presentation
// attributes, escaped text and internal fragment links built from parsed UUIDs.
// There is no HTML, CSS input, script, image, external reference or foreignObject.
type svgWriter struct{ strings.Builder }

func (w *svgWriter) tag(format string, args ...any) { fmt.Fprintf(&w.Builder, format, args...) }
func escaped(value string) string                   { return html.EscapeString(value) }

func (w *svgWriter) start(width, height int, title string) {
	w.tag(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" role="img" aria-labelledby="diagram-title" font-family="system-ui, sans-serif" font-size="14" fill="#25334b">`, width, height, width, height)
	w.tag(`<title id="diagram-title">%s</title>`, escaped(title))
	w.WriteString(`<defs><marker id="arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse"><path d="M 0 0 L 10 5 L 0 10 z" fill="#526781"/></marker><marker id="open-arrow" viewBox="0 0 12 12" refX="11" refY="6" markerWidth="9" markerHeight="9" orient="auto-start-reverse"><path d="M 1 1 L 11 6 L 1 11" fill="none" stroke="#526781" stroke-width="1.5"/></marker><marker id="inheritance" viewBox="0 0 12 12" refX="11" refY="6" markerWidth="10" markerHeight="10" orient="auto-start-reverse"><path d="M 1 1 L 11 6 L 1 11 z" fill="white" stroke="#526781" stroke-width="1.2"/></marker></defs>`)
	w.tag(`<rect width="%d" height="%d" fill="#fff"/>`, width, height)
	text(w, 24, 32, title, 100, "start")
}

func text(w *svgWriter, x, y int, value string, width int, anchor string) {
	short := []rune(strings.Join(strings.Fields(value), " "))
	if len(short) > width {
		short = append(short[:max(0, width-1)], '…')
	}
	w.tag(`<text x="%d" y="%d" text-anchor="%s"><title>%s</title>%s</text>`, x, y, anchor, escaped(value), escaped(string(short)))
}

func scenarioLink(w *svgWriter, value Scenario) {
	w.tag(`<a href="#scenario-%s" data-source-kind="scenario" data-scenario-id="%s" data-story-id="%s"><title>Open scenario: %s</title>`, value.ID, value.ID, value.StoryID, escaped(value.Name))
}

func storyContext(w *svgWriter, value Scenario, x, y int) {
	w.tag(`<a href="#story-%s" data-source-kind="story" data-story-id="%s" data-scenario-id="%s" fill="#62728a">`, value.StoryID, value.StoryID, value.ID)
	label := value.Story.StateName
	if value.ReviewNeeded {
		label += " · Review needed"
	}
	text(w, x, y, label, 44, "middle")
	w.WriteString(`</a>`)
}

// SequenceSVG renders only the explicitly authored participant/message model.
// The stable source node identifiers are shared with the scenario editor.
func SequenceSVG(value Scenario) string {
	width := max(720, len(value.Participants)*210+100)
	height := max(300, 175+len(value.Steps)*62)
	w := &svgWriter{}
	w.start(width, height, value.label()+" — Sequence")
	w.tag(`<g id="scenario-%s" data-scenario-id="%s" data-story-id="%s">`, value.ID, value.ID, value.StoryID)
	storyContext(w, value, width/2, 58)
	positions := map[uuid.UUID]int{}
	for i, participant := range value.Participants {
		x := 105 + i*210
		positions[participant.ID] = x
		w.tag(`<g data-participant-id="%s"><line x1="%d" y1="116" x2="%d" y2="%d" stroke="#a6b1c1" stroke-dasharray="6 5"/>`, participant.ID, x, x, height-24)
		w.tag(`<rect x="%d" y="76" width="180" height="42" rx="7" fill="#edf3fb" stroke="#8296b2"/>`, x-90)
		text(w, x, 102, participant.Name, 22, "middle")
		w.WriteString(`</g>`)
	}
	for i, step := range value.Steps {
		if step.Kind != "alt" {
			continue
		}
		end := i
		for j := i + 1; j < len(value.Steps); j++ {
			if value.Steps[j].Kind == "end" {
				end = j
				break
			}
		}
		y := 137 + i*62
		w.tag(`<rect x="32" y="%d" width="%d" height="%d" fill="none" stroke="#8296b2" rx="3"/>`, y, width-64, max(62, (end-i)*62+28))
		w.tag(`<path d="M 32 %d h 42 v 20 l -9 9 h -33 z" fill="#edf3fb" stroke="#8296b2"/>`, y)
		text(w, 53, y+19, "alt", 3, "middle")
	}
	for i, step := range value.Steps {
		y := 154 + i*62
		w.tag(`<a id="step-%s" href="#step-%s" data-source-kind="step" data-step-id="%s" data-scenario-id="%s" data-story-id="%s"><title>%s</title>`, step.ID, step.ID, step.ID, value.ID, value.StoryID, escaped(step.Message))
		switch step.Kind {
		case "call", "return":
			from, to := positions[step.FromID], positions[step.ToID]
			marker, dash := "arrow", ""
			if step.Kind == "return" {
				marker, dash = "open-arrow", ` stroke-dasharray="7 4"`
			}
			if from == to {
				w.tag(`<path d="M %d %d h 64 v 20 h -64" fill="none" stroke="#526781" stroke-width="1.7" marker-end="url(#%s)"%s/>`, from, y, marker, dash)
				text(w, from+12, y-8, step.Message, 25, "start")
			} else {
				w.tag(`<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="#526781" stroke-width="1.7" marker-end="url(#%s)"%s/>`, from, y, to, y, marker, dash)
				text(w, (from+to)/2, y-9, fmt.Sprintf("%d. %s", i+1, step.Message), max(15, abs(to-from)/8), "middle")
			}
		case "alt":
			text(w, 88, y+3, "["+step.Message+"]", (width-130)/8, "start")
		case "else":
			w.tag(`<line x1="32" y1="%d" x2="%d" y2="%d" stroke="#8296b2" stroke-dasharray="6 4"/>`, y-19, width-32, y-19)
			text(w, 45, y+3, "else ["+step.Message+"]", (width-90)/8, "start")
		case "end":
			text(w, width-44, y, "end", 3, "end")
		}
		w.WriteString(`</a>`)
	}
	if len(value.Participants) == 0 {
		text(w, width/2, 160, "Add business participants and message steps to model this scenario.", 90, "middle")
	} else if len(value.Steps) == 0 {
		text(w, width/2, 165, "No interaction steps have been entered.", 70, "middle")
	}
	w.WriteString(`</g></svg>`)
	return w.String()
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

type point struct{ x, y int }

func useCaseRowHeight(value Scenario) int { return max(140, ((len(value.Participants)+2)/3)*85+35) }

func boundaryHeight(values []Scenario) int {
	height := 45
	for _, value := range values {
		height += useCaseRowHeight(value)
	}
	return height
}

// UseCaseSVG combines only the caller-authorized scenarios. Relationships are
// emitted only when both endpoints exist in that same authorized diagram.
func UseCaseSVG(values []Scenario) string {
	width := 1120
	groups := map[string][]Scenario{}
	boundaries := []string{}
	for _, value := range values {
		boundary := value.SystemBoundary
		if boundary == "" {
			boundary = "System"
		}
		if _, ok := groups[boundary]; !ok {
			boundaries = append(boundaries, boundary)
		}
		groups[boundary] = append(groups[boundary], value)
	}
	sort.Strings(boundaries)
	height := 95
	for _, boundary := range boundaries {
		height += 20 + boundaryHeight(groups[boundary])
	}
	height = max(280, height)
	w := &svgWriter{}
	w.start(width, height, "Business use cases")
	locations := map[uuid.UUID]point{}
	participantLocations := map[string]point{}
	top := 60
	for _, boundary := range boundaries {
		items := groups[boundary]
		w.tag(`<rect x="325" y="%d" width="745" height="%d" rx="8" fill="#f7f9fd" stroke="#8296b2"/>`, top, boundaryHeight(items))
		text(w, 343, top+27, boundary, 90, "start")
		rowTop := top + 45
		for _, value := range items {
			y := rowTop + useCaseRowHeight(value)/2 - 15
			locations[value.ID] = point{695, y}
			scenarioLink(w, value)
			w.tag(`<ellipse id="scenario-%s" cx="695" cy="%d" rx="180" ry="36" fill="#eaf1fc" stroke="#667f9e" stroke-width="1.5"/>`, value.ID, y)
			text(w, 695, y+5, value.label(), 43, "middle")
			w.WriteString(`</a>`)
			storyContext(w, value, 695, y+58)
			for i, participant := range value.Participants {
				// Participants are separate business objects within each scenario,
				// never platform users or authorization roles. UUIDs stay stable.
				px := 75 + (i%3)*90
				py := rowTop + 32 + (i/3)*85
				participantLocations[value.ID.String()+":"+participant.ID.String()] = point{px, py}
				w.tag(`<g data-participant-id="%s" data-scenario-id="%s"><title>%s (%s)</title>`, participant.ID, value.ID, escaped(participant.Name), escaped(participant.Kind))
				if participant.Kind == "actor" {
					w.tag(`<circle cx="%d" cy="%d" r="7" fill="white" stroke="#526781"/><path d="M %d %d v 20 m -13 -12 h 26 m -13 12 l -10 11 m 10 -11 l 10 11" fill="none" stroke="#526781"/>`, px, py-18, px, py-11)
				} else {
					w.tag(`<rect x="%d" y="%d" width="54" height="31" rx="3" fill="#edf3fb" stroke="#526781"/>`, px-27, py-23)
				}
				text(w, px, py+37, participant.Name, 12, "middle")
				w.WriteString(`</g>`)
			}
			rowTop += useCaseRowHeight(value)
		}
		top += 20 + boundaryHeight(items)
	}
	// Emit edges after all positions exist; no inferred actor or dependency edge
	// appears when the author has not explicitly entered that relationship.
	for _, value := range values {
		for _, relation := range value.Relationships {
			from, fromOK := locations[relation.FromID]
			to, toOK := locations[relation.ToID]
			if relation.Kind == "association" {
				from, fromOK = participantLocations[value.ID.String()+":"+relation.FromID.String()]
			}
			if !fromOK || !toOK {
				continue
			}
			w.tag(`<g data-relationship-id="%s" data-scenario-id="%s"><title>%s</title>`, relation.ID, value.ID, escaped(relation.Kind))
			if relation.Kind == "association" {
				w.tag(`<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="#526781"/>`, from.x+28, from.y, to.x-180, to.y)
			} else {
				dash, marker := ` stroke-dasharray="6 4"`, "open-arrow"
				if relation.Kind == "generalization" {
					dash, marker = "", "inheritance"
				}
				bend := 945
				w.tag(`<path d="M %d %d H %d V %d H %d" fill="none" stroke="#526781" marker-end="url(#%s)"%s/>`, from.x+180, from.y, bend, to.y, to.x+180, marker, dash)
				text(w, bend+8, (from.y+to.y)/2, "«"+relation.Kind+"»", 20, "start")
			}
			w.WriteString(`</g>`)
		}
	}
	if len(values) == 0 {
		text(w, width/2, 145, "No business scenarios are visible in this project.", 80, "middle")
	}
	w.WriteString(`</svg>`)
	return w.String()
}
