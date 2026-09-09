package integrations

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"my-jira/apps/api/internal/platform/serviceconfig"
	"my-jira/apps/api/internal/support/testutil"
)

func TestAIUsesChosenResponsesModelAndProtectsCredentials(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" || r.Header.Get("Authorization") != "Bearer fixture-secret" || r.Header.Get("User-Agent") != "my-jira/0.1" {
			t.Errorf("wrong provider protocol or headers")
		}
		var input map[string]any
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		if input["model"] != "gpt-5.6-terra" || input["store"] != false {
			t.Errorf("wrong model or store flag")
		}
		fmt.Fprint(w, `{"status":"completed","output":[{"type":"reasoning","summary":[]},{"type":"message","content":[{"type":"output_text","text":"Rewritten text"}]}]}`)
	}))
	defer provider.Close()
	t.Setenv("AI_BASE_URL", provider.URL+"/v1")
	t.Setenv("MINE_API_KEY", "fixture-secret")
	f := testutil.New(t)
	router := f.Router(Register)
	body := map[string]any{"instruction": "Rewrite clearly", "content": "fixture"}
	result := testutil.Object(t, testutil.Request(t, router, f.MemberID, "POST", f.WorkspacePrefix()+"/ai/text", body, 200))
	if result["text"] != "Rewritten text" || result["model"] != "gpt-5.6-terra" {
		t.Fatalf("unexpected response: %#v", result)
	}
	testutil.Request(t, router, f.GuestID, "POST", f.WorkspacePrefix()+"/ai/text", body, 403)
	testutil.Request(t, router, f.OutsiderID, "POST", f.WorkspacePrefix()+"/ai/text", body, 404)
	provider.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		fmt.Fprint(w, `{"error":"fixture-secret"}`)
	})
	result = testutil.Request(t, router, f.MemberID, "POST", f.WorkspacePrefix()+"/ai/text", body, 502)
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), "fixture-secret") {
		t.Fatal("upstream credential was exposed")
	}
}

func TestAIResponseContentSelection(t *testing.T) {
	for _, tc := range []struct{ provider, raw, want string }{
		{"openai", `{"status":"completed","output":[{"type":"reasoning","content":[{"type":"output_text","text":"hidden"}]},{"type":"message","content":[{"type":"refusal","text":"hidden"},{"type":"output_text","text":"visible"}]}]}`, "visible"},
		{"anthropic", `{"content":[{"type":"thinking","text":"hidden"},{"type":"text","text":"visible"}]}`, "visible"},
		{"gemini", `{"candidates":[{"content":{"parts":[{"thought":true,"text":"hidden"},{"text":"visible"}]}},{"content":{"parts":[{"text":"alternative"}]}}]}`, "visible"},
	} {
		text, err := responseText(tc.provider, []byte(tc.raw))
		if err != nil || text != tc.want {
			t.Errorf("%s: %q %v", tc.provider, text, err)
		}
	}
	if _, err := responseText("openai", []byte(`{"status":"incomplete","output":[]}`)); err == nil {
		t.Fatal("incomplete response treated as success")
	}
	_, err := generateText(context.Background(), serviceconfig.Values{"provider": "unknown"}, "x", "")
	if err == nil {
		t.Fatal("unsupported provider accepted")
	}
}
