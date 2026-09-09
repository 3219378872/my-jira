package data

import (
	"regexp"

	"github.com/microcosm-cc/bluemonday"
)

// EditorHTMLPolicy preserves the project's authored document blocks while
// keeping event handlers, scripts, styles, and arbitrary frames disallowed.
func EditorHTMLPolicy() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	p.AllowElements("aside", "figure", "figcaption", "details", "summary", "iframe")
	p.AllowAttrs("data-callout").Matching(regexp.MustCompile(`^(info|warning|success)$`)).OnElements("aside")
	uuid := regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	p.AllowAttrs("data-mention-id").Matching(uuid).OnElements("span")
	p.AllowAttrs("data-work-item-id", "data-project-id", "data-workspace-id").Matching(uuid).OnElements("div")
	p.AllowAttrs("class").Matching(regexp.MustCompile(`^editor-work-item$`)).OnElements("div")
	p.AllowAttrs("data-mention-label").Matching(regexp.MustCompile(`^[^\x00-\x1f]{1,255}$`)).OnElements("span")
	p.AllowAttrs("data-disclosure-title").Matching(regexp.MustCompile(`^[^\x00-\x1f]{0,255}$`)).OnElements("details")
	p.AllowAttrs("open").OnElements("details")
	embed := regexp.MustCompile(`^https://((www\.)?youtube(-nocookie)?\.com/embed/[A-Za-z0-9_-]+|player\.vimeo\.com/video/[0-9]+|(www\.)?figma\.com/embed)(\?[^\s<>"']*)?$`)
	p.AllowAttrs("src").Matching(embed).OnElements("iframe")
	p.AllowAttrs("data-embed-src").Matching(embed).OnElements("figure")
	p.AllowAttrs("title").OnElements("iframe")
	p.AllowAttrs("width", "height").Matching(regexp.MustCompile(`^[0-9]{1,4}%?$`)).OnElements("iframe")
	p.AllowAttrs("allowfullscreen").OnElements("iframe")
	p.AllowAttrs("loading").Matching(regexp.MustCompile(`^(lazy|eager)$`)).OnElements("iframe")
	p.AllowAttrs("referrerpolicy").Matching(regexp.MustCompile(`^(no-referrer|strict-origin-when-cross-origin)$`)).OnElements("iframe")
	p.AllowAttrs("data-type").Matching(regexp.MustCompile(`^(taskList|taskItem)$`)).OnElements("ul", "li")
	p.AllowAttrs("data-checked").Matching(regexp.MustCompile(`^(true|false)$`)).OnElements("li")
	return p
}
