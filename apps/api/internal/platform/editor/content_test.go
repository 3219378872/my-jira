package editor

import (
	"encoding/json"
	"testing"
)

func TestDocumentGrammarRejectsStatesThatCannotRender(t *testing.T) {
	for _, bad := range []string{
		`{"type":"doc","content":[{"type":"unknown"}]}`,
		`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"table"}]}]}`,
		`{"type":"doc","content":[{"type":"callout","attrs":{"tone":{}},"content":[{"type":"paragraph"}]}]}`,
		`{"type":"doc","content":[{"type":"heading","attrs":{"level":50}}]}`,
		`{"type":"doc","content":[{"type":"table","content":[]}]}`,
		`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"bad","marks":[{"type":"script"}]}]}]}`,
	} {
		if err := Validate(json.RawMessage(bad)); err == nil {
			t.Fatalf("accepted invalid schema: %s", bad)
		}
	}
	valid := `{"type":"doc","content":[{"type":"callout","attrs":{"tone":"info"},"content":[{"type":"paragraph","content":[{"type":"mention","attrs":{"id":"member","label":"Team"}},{"type":"text","text":" Useful original content.","marks":[{"type":"bold"}]}]}]},{"type":"taskList","content":[{"type":"taskItem","attrs":{"checked":true},"content":[{"type":"paragraph"}]}]}]}`
	if err := Validate(json.RawMessage(valid)); err != nil {
		t.Fatal(err)
	}
}
