package documents

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/gin-gonic/gin"
	"golang.org/x/net/html"
	"my-jira/apps/api/internal/platform/httpapi"
	"my-jira/apps/api/internal/platform/identity"
	"my-jira/apps/api/internal/support/data"
)

func (s *service) summary(c *gin.Context) {
	scope, err := s.scope(c, identity.Guest)
	if err != nil {
		data.Fail(c, err)
		return
	}
	result, err := data.One(c, s.deps.DB.SQL, `SELECT jsonb_build_object('public_pages',count(*) FILTER(WHERE NOT is_private AND archived_at IS NULL),'private_pages',count(*) FILTER(WHERE is_private AND archived_at IS NULL),'archived_pages',count(*) FILTER(WHERE archived_at IS NOT NULL),'total',count(*)) FROM pages p WHERE project_id IS NOT DISTINCT FROM $2::uuid AND parent_id IS NULL AND `+data.VisiblePage("p", "$1", "$3"), scope.WorkspaceID, project(scope), scope.Actor.UserID)
	data.Send(c, result, err)
}

type pageResource struct {
	URL  string `json:"url"`
	Text string `json:"text"`
	Kind string `json:"kind"`
}
type pageHeading struct {
	Level int    `json:"level"`
	Text  string `json:"text"`
	ID    string `json:"id"`
}

func extractResources(content string) ([]pageResource, []pageHeading, error) {
	links := []pageResource{}
	headings := []pageHeading{}
	seen := map[string]bool{}
	tokenizer := html.NewTokenizer(strings.NewReader(content))
	var activeLink *pageResource
	var activeHeading *pageHeading
	flushLink := func() {
		if activeLink != nil {
			activeLink.Text = strings.TrimSpace(activeLink.Text)
			key := activeLink.Kind + ":" + activeLink.URL
			if !seen[key] {
				links = append(links, *activeLink)
				seen[key] = true
			}
			activeLink = nil
		}
	}
	for {
		kind := tokenizer.Next()
		switch kind {
		case html.ErrorToken:
			if tokenizer.Err() != io.EOF {
				return nil, nil, tokenizer.Err()
			}
			flushLink()
			return links, headings, nil
		case html.StartTagToken, html.SelfClosingTagToken:
			token := tokenizer.Token()
			attrs := map[string]string{}
			for _, attr := range token.Attr {
				attrs[attr.Key] = attr.Val
			}
			if token.Data == "a" && attrs["href"] != "" {
				flushLink()
				activeLink = &pageResource{URL: attrs["href"], Kind: "link"}
			}
			if token.Data == "img" && attrs["src"] != "" {
				key := "image:" + attrs["src"]
				if !seen[key] {
					links = append(links, pageResource{URL: attrs["src"], Text: attrs["alt"], Kind: "image"})
					seen[key] = true
				}
			}
			if token.Data == "figure" && attrs["data-embed-src"] != "" {
				key := "embed:" + attrs["data-embed-src"]
				if !seen[key] {
					links = append(links, pageResource{URL: attrs["data-embed-src"], Text: attrs["title"], Kind: "embed"})
					seen[key] = true
				}
			}
			if len(token.Data) == 2 && token.Data[0] == 'h' && token.Data[1] >= '1' && token.Data[1] <= '6' {
				activeHeading = &pageHeading{Level: int(token.Data[1] - '0'), ID: attrs["id"]}
			}
		case html.TextToken:
			text := string(tokenizer.Text())
			if activeLink != nil {
				activeLink.Text += text
			}
			if activeHeading != nil {
				activeHeading.Text += text
			}
		case html.EndTagToken:
			token := tokenizer.Token()
			if token.Data == "a" {
				flushLink()
			}
			if activeHeading != nil && token.Data == fmt.Sprintf("h%d", activeHeading.Level) {
				activeHeading.Text = strings.TrimSpace(activeHeading.Text)
				if activeHeading.ID == "" {
					activeHeading.ID = fmt.Sprintf("heading-%d", len(headings)+1)
				}
				headings = append(headings, *activeHeading)
				activeHeading = nil
			}
		}
	}
}

func (s *service) resources(c *gin.Context) {
	scope, id, err := s.pageScope(c, identity.Guest)
	if err != nil {
		data.Fail(c, err)
		return
	}
	page, err := s.load(c, s.deps.DB.SQL, scope, id, false)
	if err != nil {
		data.Fail(c, err)
		return
	}
	var body struct {
		HTML string `json:"content_html"`
	}
	if err = json.Unmarshal(page.Raw, &body); err != nil {
		data.Fail(c, err)
		return
	}
	links, headings, err := extractResources(body.HTML)
	if err != nil {
		data.Fail(c, err)
		return
	}
	prefix := "/api/v1/workspaces/" + scope.WorkspaceID.String()
	if project(scope) != nil {
		prefix += "/projects/" + scope.ProjectID.String()
	}
	assets, err := data.Many(c, s.deps.DB.SQL, `SELECT jsonb_build_object('id',id,'filename',filename,'content_type',content_type,'size_bytes',size_bytes,'created_at',created_at,'uploaded_by',uploaded_by,'download_url',$4||'/assets/'||id::text||'/download') FROM file_assets fa WHERE page_id=$1 AND workspace_id=$2 AND project_id IS NOT DISTINCT FROM $3::uuid AND deleted_at IS NULL AND upload_status='completed' AND EXISTS(SELECT 1 FROM pages p WHERE p.id=fa.page_id AND `+data.VisiblePage("p", "$2", "$5")+`) ORDER BY created_at,id`, id, scope.WorkspaceID, project(scope), prefix, scope.Actor.UserID)
	if err != nil {
		data.Fail(c, err)
		return
	}
	httpapi.JSON(c, 200, gin.H{"page_id": id, "version": page.Version, "links": links, "headings": headings, "assets": assets})
}
