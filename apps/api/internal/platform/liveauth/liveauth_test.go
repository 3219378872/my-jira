package liveauth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestBinaryRequiresFreshSignatureBoundToContentAndRoute(t *testing.T) {
	const secret = "test-service-key-isolated-from-real-deployment"
	t.Setenv("LIVE_SERVICE_KEY", secret)
	const path = "/api/v1/workspaces/example/pages/document/content"
	const body = `{"content_binary":"AAA=","content_json":{"type":"doc","content":[]},"content_html":"<p></p>","version":1}`
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte("my-jira:live:v1\n" + timestamp + "\nPUT\n" + path + "\n" + body))
	signature := hex.EncodeToString(mac.Sum(nil))
	for _, tc := range []struct {
		name, path, body, timestamp, signature string
		ok                                     bool
	}{
		{"signed", path, body, timestamp, signature, true},
		{"unsigned", path, body, timestamp, "", false},
		{"changed document", path + "other", body, timestamp, signature, false},
		{"changed version", path, strings.Replace(body, `"version":1`, `"version":2`, 1), timestamp, signature, false},
		{"expired", path, body, "1", signature, false},
		{"ordinary JSON replacement", path, `{"content_json":{"type":"doc","content":[]},"content_html":"<p></p>"}`, "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("PUT", tc.path, strings.NewReader(tc.body))
			c.Request.Header.Set("X-MyJira-Live-Time", tc.timestamp)
			c.Request.Header.Set("X-MyJira-Live-Signature", tc.signature)
			if err := VerifyBinary(c); (err == nil) != tc.ok {
				t.Fatalf("signature result: %v", err)
			}
			raw, _ := io.ReadAll(c.Request.Body)
			if string(raw) != tc.body {
				t.Fatal("ordinary JSON binder lost the request body")
			}
		})
	}
}
