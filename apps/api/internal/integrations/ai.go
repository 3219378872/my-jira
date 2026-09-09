package integrations

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"my-jira/apps/api/internal/platform/httpapi"
	"my-jira/apps/api/internal/platform/identity"
	"my-jira/apps/api/internal/platform/serviceconfig"
)

// The user-selected default is gpt-5.6-terra over the configured Responses API.
// Text extraction follows https://developers.openai.com/api/docs/guides/text .
func (h *handler) aiText(c *gin.Context) {
	if _, e := h.scope(c, identity.Member); e != nil {
		httpapi.Fail(c, e)
		return
	}
	var body struct {
		Instruction string `json:"instruction"`
		Content     string `json:"content"`
	}
	if e := c.ShouldBindJSON(&body); e != nil || strings.TrimSpace(body.Instruction) == "" || len(body.Instruction) > 4000 || len(body.Content) > 50000 {
		fail(c, 400, "Supply an instruction and at most 50,000 bytes of content")
		return
	}
	config, e := serviceconfig.Load(c.Request.Context(), h.d.DB.SQL, "ai")
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	if config.String("api_key") == "" || config.String("model") == "" {
		fail(c, 503, "The instance administrator has not configured AI assistance")
		return
	}
	text, e := generateText(c.Request.Context(), config, body.Instruction, body.Content)
	if e != nil {
		httpapi.Fail(c, httpapi.NewError(502, "provider_error", e.Error()))
		return
	}
	httpapi.JSON(c, 200, gin.H{"text": text, "model": config.String("model")})
}

func generateText(ctx context.Context, config serviceconfig.Values, instruction, content string) (string, error) {
	provider := config.String("provider")
	if provider == "" {
		provider = "openai"
	}
	base := strings.TrimRight(config.String("base_url"), "/")
	model := config.String("model")
	key := config.String("api_key")
	var endpoint string
	var payload map[string]any
	text := instruction
	if content != "" {
		text += "\n\nContent to work with:\n" + content
	}
	headers := map[string]string{"Content-Type": "application/json", "User-Agent": "my-jira/0.1"}
	switch provider {
	case "openai":
		if base == "" {
			base = "https://api.openai.com/v1"
		}
		endpoint = base + "/responses"
		headers["Authorization"] = "Bearer " + key
		payload = map[string]any{"model": model, "input": []any{map[string]any{"role": "user", "content": []any{map[string]string{"type": "input_text", "text": text}}}}, "max_output_tokens": 4096, "store": false}
		if model == "gpt-5.6-terra" {
			payload["reasoning"] = map[string]string{"effort": "low"}
		}
	case "anthropic":
		if base == "" {
			base = "https://api.anthropic.com/v1"
		}
		endpoint = base + "/messages"
		headers["x-api-key"] = key
		headers["anthropic-version"] = "2023-06-01"
		payload = map[string]any{"model": model, "max_tokens": 4096, "messages": []any{map[string]string{"role": "user", "content": text}}}
	case "gemini":
		if base == "" {
			base = "https://generativelanguage.googleapis.com/v1beta"
		}
		endpoint = base + "/models/" + url.PathEscape(model) + ":generateContent"
		headers["x-goog-api-key"] = key
		payload = map[string]any{"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]string{"text": text}}}}, "generationConfig": map[string]any{"maxOutputTokens": 4096}}
	default:
		return "", fmt.Errorf("The configured AI provider is unsupported")
	}
	u, e := url.Parse(endpoint)
	if e != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil {
		return "", fmt.Errorf("The AI endpoint configuration is invalid")
	}
	raw, e := json.Marshal(payload)
	if e != nil {
		return "", e
	}
	request, e := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if e != nil {
		return "", e
	}
	for k, v := range headers {
		request.Header.Set(k, v)
	}
	client := &http.Client{Timeout: 50 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, e := client.Do(request)
	if e != nil {
		return "", fmt.Errorf("The AI provider could not be reached")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("The AI provider returned HTTP %d", response.StatusCode)
	}
	data, e := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if e != nil {
		return "", fmt.Errorf("The AI response could not be read")
	}
	result, e := responseText(provider, data)
	if e != nil {
		return "", e
	}
	if strings.TrimSpace(result) == "" {
		return "", fmt.Errorf("The AI provider returned no text; retry the request")
	}
	return result, nil
}

func responseText(provider string, raw []byte) (string, error) {
	var result struct {
		Status string `json:"status"`
		Output []struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text    string `json:"text"`
					Thought bool   `json:"thought"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if e := json.Unmarshal(raw, &result); e != nil {
		return "", fmt.Errorf("The AI provider returned an invalid response")
	}
	var text strings.Builder
	switch provider {
	case "openai":
		if result.Status == "incomplete" || result.Status == "failed" {
			return "", fmt.Errorf("The AI provider did not finish this response")
		}
		for _, item := range result.Output {
			if item.Type != "message" {
				continue
			}
			for _, part := range item.Content {
				if part.Type == "output_text" {
					text.WriteString(part.Text)
				}
			}
		}
	case "anthropic":
		for _, part := range result.Content {
			if part.Type == "text" {
				text.WriteString(part.Text)
			}
		}
	case "gemini":
		if len(result.Candidates) > 0 {
			for _, part := range result.Candidates[0].Content.Parts {
				if !part.Thought {
					text.WriteString(part.Text)
				}
			}
		}
	}
	return text.String(), nil
}

func (h *handler) images(c *gin.Context) {
	if _, e := h.scope(c, identity.Member); e != nil {
		httpapi.Fail(c, e)
		return
	}
	query := strings.TrimSpace(c.Query("q"))
	if query == "" || len(query) > 200 {
		fail(c, 400, "Supply an image search query")
		return
	}
	config, e := serviceconfig.Load(c.Request.Context(), h.d.DB.SQL, "unsplash")
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	key := config.String("access_key")
	if key == "" {
		fail(c, 503, "Image search has not been configured")
		return
	}
	params := url.Values{"query": {query}, "per_page": {"20"}, "content_filter": {"high"}}
	request, e := http.NewRequestWithContext(c.Request.Context(), "GET", "https://api.unsplash.com/search/photos?"+params.Encode(), nil)
	if e != nil {
		httpapi.Fail(c, e)
		return
	}
	request.Header.Set("Authorization", "Client-ID "+key)
	request.Header.Set("User-Agent", "my-jira/0.1")
	response, e := (&http.Client{Timeout: 15 * time.Second}).Do(request)
	if e != nil {
		fail(c, 502, "Image provider could not be reached")
		return
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		fail(c, 502, "Image provider rejected the request")
		return
	}
	var body struct {
		Results []struct {
			ID   string                          `json:"id"`
			Alt  string                          `json:"alt_description"`
			URLs struct{ Small, Regular string } `json:"urls"`
			User struct {
				Name  string
				Links struct {
					HTML string `json:"html"`
				} `json:"links"`
			} `json:"user"`
			Links struct {
				HTML string `json:"html"`
			} `json:"links"`
		} `json:"results"`
	}
	if e = json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&body); e != nil {
		fail(c, 502, "Image provider returned an invalid response")
		return
	}
	data := []gin.H{}
	for _, image := range body.Results {
		data = append(data, gin.H{"id": image.ID, "alt": image.Alt, "preview_url": image.URLs.Small, "url": image.URLs.Regular, "author": image.User.Name, "author_url": image.User.Links.HTML, "source_url": image.Links.HTML})
	}
	httpapi.JSON(c, 200, data)
}
