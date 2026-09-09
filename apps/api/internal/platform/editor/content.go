// Package editor validates the independently configured Tiptap document grammar.
package editor

import (
	"encoding/json"
	"fmt"
)

type node struct {
	Type    string                     `json:"type"`
	Text    string                     `json:"text"`
	Attrs   map[string]json.RawMessage `json:"attrs"`
	Content []node                     `json:"content"`
	Marks   []struct {
		Type  string                     `json:"type"`
		Attrs map[string]json.RawMessage `json:"attrs"`
	} `json:"marks"`
}

var inline = map[string]bool{"text": true, "hardBreak": true, "mention": true}
var blocks = map[string]bool{"paragraph": true, "heading": true, "blockquote": true, "bulletList": true, "orderedList": true, "codeBlock": true, "horizontalRule": true, "taskList": true, "table": true, "image": true, "callout": true, "embed": true, "disclosure": true, "workItem": true}

func attributes(attrs map[string]json.RawMessage) error {
	for key, raw := range attrs {
		if string(raw) == "null" {
			continue
		}
		var value any
		if json.Unmarshal(raw, &value) != nil {
			return fmt.Errorf("invalid editor attribute")
		}
		switch value.(type) {
		case string, bool, float64:
		case []any:
			if key != "colwidth" {
				return fmt.Errorf("invalid editor attribute array")
			}
			for _, width := range value.([]any) {
				if number, ok := width.(float64); !ok || number < 0 || number > 10000 {
					return fmt.Errorf("invalid table column width")
				}
			}
		default:
			return fmt.Errorf("editor attributes cannot contain objects")
		}
		switch key {
		case "src", "alt", "title", "tone", "id", "label", "workspaceId", "projectId", "issueId", "identifier", "language", "href", "target", "rel", "class":
			if _, ok := value.(string); !ok {
				return fmt.Errorf("%s must be text", key)
			}
		case "level":
			if level, ok := value.(float64); !ok || level < 1 || level > 6 || level != float64(int(level)) {
				return fmt.Errorf("heading level must be 1 through 6")
			}
		case "colspan", "rowspan":
			if span, ok := value.(float64); !ok || span < 1 || span > 1000 || span != float64(int(span)) {
				return fmt.Errorf("invalid table span")
			}
		case "checked":
			if _, ok := value.(bool); !ok {
				return fmt.Errorf("task checked must be boolean")
			}
		}
	}
	return nil
}

func Validate(raw json.RawMessage) error {
	var root node
	if len(raw) > 2<<20 || json.Unmarshal(raw, &root) != nil || root.Type != "doc" || root.Content == nil {
		return fmt.Errorf("content_json must be an editor document with a content array")
	}
	count := 0
	var visit func(node, int) error
	visit = func(n node, depth int) error {
		count++
		if depth > 48 || count > 25000 {
			return fmt.Errorf("document content is too complex")
		}
		if err := attributes(n.Attrs); err != nil {
			return err
		}
		for _, mark := range n.Marks {
			switch mark.Type {
			case "bold", "italic", "strike", "code", "underline", "link":
			default:
				return fmt.Errorf("unknown editor mark: %s", mark.Type)
			}
			if err := attributes(mark.Attrs); err != nil {
				return err
			}
		}
		leaf := false
		childOK := func(string) bool { return false }
		switch n.Type {
		case "doc", "blockquote", "callout", "disclosure":
			childOK = func(kind string) bool { return blocks[kind] }
		case "paragraph", "heading":
			childOK = func(kind string) bool { return inline[kind] }
		case "codeBlock":
			childOK = func(kind string) bool { return kind == "text" }
		case "bulletList", "orderedList":
			childOK = func(kind string) bool { return kind == "listItem" }
		case "taskList":
			childOK = func(kind string) bool { return kind == "taskItem" }
		case "listItem", "taskItem":
			childOK = func(kind string) bool { return blocks[kind] }
			if len(n.Content) == 0 || n.Content[0].Type != "paragraph" {
				return fmt.Errorf("list items must start with a paragraph")
			}
		case "table":
			childOK = func(kind string) bool { return kind == "tableRow" }
		case "tableRow":
			childOK = func(kind string) bool { return kind == "tableCell" || kind == "tableHeader" }
		case "tableCell", "tableHeader":
			childOK = func(kind string) bool { return blocks[kind] }
		case "text":
			leaf = true
			if n.Text == "" {
				return fmt.Errorf("text nodes must contain text")
			}
		case "hardBreak", "horizontalRule", "image", "mention", "embed", "workItem":
			leaf = true
		default:
			return fmt.Errorf("unknown editor node: %s", n.Type)
		}
		if leaf && len(n.Content) > 0 {
			return fmt.Errorf("leaf editor nodes cannot have children")
		}
		if !leaf && n.Type != "doc" && n.Type != "paragraph" && n.Type != "heading" && n.Type != "codeBlock" && len(n.Content) == 0 {
			return fmt.Errorf("%s must contain document content", n.Type)
		}
		if n.Type != "text" && n.Text != "" {
			return fmt.Errorf("text is only valid on text nodes")
		}
		for _, child := range n.Content {
			if !childOK(child.Type) {
				return fmt.Errorf("invalid %s child in %s", child.Type, n.Type)
			}
			if err := visit(child, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	return visit(root, 0)
}
