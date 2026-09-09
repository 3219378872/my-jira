package foundation

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"my-jira/apps/api/internal/platform/serviceconfig"
)

func mockSMTP(t *testing.T) (string, string, chan string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	messages := make(chan string, 20)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(10 * time.Second))
				reader := bufio.NewReader(conn)
				fmt.Fprint(conn, "220 mock.local ESMTP\r\n")
				for {
					line, err := reader.ReadString('\n')
					if err != nil {
						return
					}
					command := strings.TrimSpace(line)
					switch {
					case strings.HasPrefix(command, "EHLO"), strings.HasPrefix(command, "HELO"):
						fmt.Fprint(conn, "250 mock.local\r\n")
					case strings.HasPrefix(command, "MAIL FROM"), strings.HasPrefix(command, "RCPT TO"):
						fmt.Fprint(conn, "250 accepted\r\n")
					case command == "DATA":
						fmt.Fprint(conn, "354 send message\r\n")
						var body strings.Builder
						for {
							line, err = reader.ReadString('\n')
							if err != nil {
								return
							}
							if strings.TrimSpace(line) == "." {
								break
							}
							body.WriteString(line)
						}
						messages <- body.String()
						fmt.Fprint(conn, "250 queued\r\n")
					case command == "QUIT":
						fmt.Fprint(conn, "221 bye\r\n")
						return
					default:
						fmt.Fprint(conn, "500 unsupported\r\n")
					}
				}
			}()
		}
	}()
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	return host, port, messages
}

func TestEncryptedServiceConfigurationAndActualSMTPTest(t *testing.T) {
	t.Setenv("APP_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	f := newFixture(t)
	admin := f.client()
	admin.account("admin@example.test", true)
	reader := f.client()
	reader.account("reader@example.test", false)
	reader.must("GET", "/admin/services", nil, 403)
	host, port, messages := mockSMTP(t)
	admin.must("PATCH", "/admin/services/email", map[string]any{"port": 2525.5}, 400)
	result := admin.must("PATCH", "/admin/services/email", map[string]any{"host": host, "port": port, "from": "my-jira <sender@example.test>", "password": "fixture-only-password", "user": "", "secure": false}, 200)
	if result["password"] != nil || result["password_configured"] != true || result["source"] != "instance" {
		t.Fatalf("configuration projection leaked or lost credential: %v", result)
	}
	var stored string
	if err := f.db.SQL.QueryRow(`SELECT settings::text FROM instances WHERE singleton`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, "fixture-only-password") || strings.Contains(stored, "sender@example.test") {
		t.Fatal("service config was persisted without encryption")
	}
	values, err := serviceconfig.Load(t.Context(), f.db.SQL, "email")
	if err != nil || values.String("password") != "fixture-only-password" {
		t.Fatalf("encrypted service load failed: %v", err)
	}
	admin.must("PATCH", "/admin/services/email", map[string]any{"from": "Changed Sender <sender@example.test>"}, 200)
	values, err = serviceconfig.Load(t.Context(), f.db.SQL, "email")
	if err != nil || values.String("password") != "fixture-only-password" {
		t.Fatal("omitted password was cleared")
	}
	result = admin.must("POST", "/admin/email/test", map[string]any{"email": "recipient@example.test"}, 200)
	if result["accepted"] != true {
		t.Fatal("SMTP test was not acknowledged")
	}
	select {
	case message := <-messages:
		if !strings.Contains(message, "To: recipient@example.test") || !strings.Contains(message, "Changed Sender") {
			t.Fatal("saved configuration was not used for the SMTP message")
		}
	case <-time.After(time.Second):
		t.Fatal("no SMTP message was actually sent")
	}
	admin.must("PATCH", "/admin/services/email", map[string]any{"password": nil}, 200)
	values, err = serviceconfig.Load(t.Context(), f.db.SQL, "email")
	if err != nil || values.String("password") != "" {
		t.Fatal("explicit password removal failed")
	}
	var wait sync.WaitGroup
	codes := make(chan int, 2)
	for _, input := range []map[string]any{{"from": "Concurrent Sender <sender@example.test>"}, {"password": "concurrent-fixture-password"}} {
		wait.Add(1)
		go func(input map[string]any) {
			defer wait.Done()
			code, _ := admin.request("PATCH", "/admin/services/email", input)
			codes <- code
		}(input)
	}
	wait.Wait()
	close(codes)
	for code := range codes {
		if code != 200 {
			t.Fatalf("concurrent service update status %d", code)
		}
	}
	values, err = serviceconfig.Load(t.Context(), f.db.SQL, "email")
	if err != nil || values.String("from") != "Concurrent Sender <sender@example.test>" || values.String("password") != "concurrent-fixture-password" {
		t.Fatal("concurrent service updates lost a saved field")
	}
}

func TestServicesDoNotSaveWithoutKey(t *testing.T) {
	t.Setenv("APP_ENCRYPTION_KEY", "")
	f := newFixture(t)
	admin := f.client()
	admin.account("admin@example.test", true)
	admin.must("PATCH", "/admin/services/ai", map[string]any{"api_key": "fixture-only-key"}, 503)
}
