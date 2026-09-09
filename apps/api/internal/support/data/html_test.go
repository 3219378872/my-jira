package data

import (
	"strings"
	"testing"
)

func TestEditorBlocksRemainSafe(t *testing.T) {
	policy := EditorHTMLPolicy()
	input := `<aside data-callout="warning" onclick="alert(1)"><p>Read this</p></aside><span data-mention-id="2f051d4b-72e5-4468-b807-71175bc46f47" data-mention-label="Sam">Sam</span><details data-disclosure-title="More"><summary>More</summary><div>Details</div></details><figure data-embed-src="https://www.youtube.com/embed/abcd"><iframe src="https://www.youtube.com/embed/abcd" onload="alert(1)"></iframe></figure><iframe src="https://www.youtube.com.evil.test/embed/abcd"></iframe><iframe src="javascript:alert(1)"></iframe><script>alert(1)</script>`
	output := policy.Sanitize(input)
	for _, wanted := range []string{`data-callout="warning"`, `data-mention-id="2f051d4b-72e5-4468-b807-71175bc46f47"`, `data-disclosure-title="More"`, `data-embed-src="https://www.youtube.com/embed/abcd"`, `src="https://www.youtube.com/embed/abcd"`} {
		if !strings.Contains(output, wanted) {
			t.Fatalf("editor block lost %s: %s", wanted, output)
		}
	}
	for _, unsafe := range []string{"onclick", "onload", "javascript:", "evil.test", "<script", "alert(1)"} {
		if strings.Contains(output, unsafe) {
			t.Fatalf("unsafe HTML survived: %s", output)
		}
	}
	for _, url := range []string{"https://player.vimeo.com/video/123", "https://www.figma.com/embed?embed_host=share&url=https%3A%2F%2Fwww.figma.com%2Ffile%2Fabc"} {
		if !strings.Contains(policy.Sanitize(`<iframe src="`+url+`"></iframe>`), "src=") {
			t.Fatalf("allowed embed removed: %s", url)
		}
	}
}

func TestWorkItemReferenceSanitization(t *testing.T) {
	policy := EditorHTMLPolicy()
	workspaceID := "095c024f-1f27-4e1a-a8ee-f3d69b121b2d"
	projectID := "73bc1b54-6482-4fef-ae20-f061c66cb40f"
	itemID := "eb96c4b2-48a8-4963-9428-684464f41b22"
	link := "/go/work-item/" + workspaceID + "/" + projectID + "/" + itemID
	input := `<div data-work-item-id="` + itemID + `" data-project-id="` + projectID + `" data-workspace-id="` + workspaceID + `" class="editor-work-item" onclick="alert(1)"><a href="` + link + `">Linked work item</a><script>alert(1)</script></div>`
	output := policy.Sanitize(input)
	for _, wanted := range []string{`data-work-item-id="` + itemID + `"`, `data-project-id="` + projectID + `"`, `data-workspace-id="` + workspaceID + `"`, `class="editor-work-item"`, `href="` + link + `"`, "Linked work item"} {
		if !strings.Contains(output, wanted) {
			t.Fatalf("work-item reference lost %s: %s", wanted, output)
		}
	}
	for _, unsafe := range []string{"onclick", "<script", "alert(1)"} {
		if strings.Contains(output, unsafe) {
			t.Fatalf("unsafe work-item HTML survived: %s", output)
		}
	}
	for _, attribute := range []string{"data-work-item-id", "data-project-id", "data-workspace-id"} {
		for _, invalid := range []string{"not-a-uuid", itemID + "/extra", "prefix" + itemID, `&quot; onmouseover=&quot;alert(1)`, `&lt;img src=x onerror=alert(1)&gt;`} {
			output := policy.Sanitize(`<div ` + attribute + `="` + invalid + `" class="editor-work-item other" style="color:red">Reference</div>`)
			if strings.Contains(output, attribute+"=") || strings.Contains(output, "class=") || strings.Contains(output, "style=") || strings.Contains(output, "alert(1)") {
				t.Fatalf("invalid reference attributes survived: %s", output)
			}
		}
		if output := policy.Sanitize(`<span ` + attribute + `="` + itemID + `">Reference</span>`); strings.Contains(output, attribute+"=") {
			t.Fatalf("reference attribute allowed on a different node: %s", output)
		}
	}
}
