// Package liveauth authenticates CRDT persistence from the collaboration service.
// Browser/API users continue to authenticate normally and supply HTML plus JSON.
package liveauth

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"my-jira/apps/api/internal/platform/httpapi"
)

func VerifyBinary(c *gin.Context) error {
	raw, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 2<<20))
	if err != nil {
		return httpapi.NewError(400, "invalid_request", "Document input exceeds 2 MiB")
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(raw))
	var input map[string]json.RawMessage
	if json.Unmarshal(raw, &input) != nil {
		return nil
	} // The ordinary binder reports malformed JSON.
	binary, present := input["content_binary"]
	if !present || string(binary) == "null" || string(binary) == `""` {
		return nil
	}
	secret := os.Getenv("LIVE_SERVICE_KEY")
	if secret == "" {
		secret = os.Getenv("APP_ENCRYPTION_KEY")
	}
	denied := func() error {
		return httpapi.NewError(403, "live_service_required", "Binary document state can only be stored by the collaboration service; supply HTML and JSON for a document replacement")
	}
	if len(secret) < 32 {
		return denied()
	}
	timestamp := c.GetHeader("X-MyJira-Live-Time")
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || seconds < time.Now().Unix()-60 || seconds > time.Now().Unix()+60 {
		return denied()
	}
	supplied, err := hex.DecodeString(c.GetHeader("X-MyJira-Live-Signature"))
	if err != nil {
		return denied()
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte("my-jira:live:v1\n" + timestamp + "\n" + c.Request.Method + "\n" + c.Request.URL.EscapedPath() + "\n"))
	_, _ = mac.Write(raw)
	if !hmac.Equal(supplied, mac.Sum(nil)) {
		return denied()
	}
	return nil
}
