package foundation

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
)

func TestEmailConfirmationLinksMatchClientRoutes(t *testing.T) {
	t.Setenv("SMTP_HOST", "127.0.0.1")
	f := newFixture(t)
	user := f.client()
	user.account("owner@example.test", true)
	readLink := func() *url.URL {
		t.Helper()
		var text string
		if err := f.db.SQL.QueryRow(`SELECT payload->>'text' FROM outbox_events WHERE deduplication_key LIKE 'auth-email:%' ORDER BY created_at DESC LIMIT 1`).Scan(&text); err != nil {
			t.Fatal(err)
		}
		parts := strings.Fields(text)
		link, err := url.Parse(parts[len(parts)-1])
		if err != nil || link.Path != "/verify-email" || link.Query().Get("token") == "" {
			t.Fatalf("confirmation link does not match the browser route: %s", text)
		}
		return link
	}
	user.must("POST", "/auth/email-verification/request", nil, 200)
	link := readLink()
	if link.Query().Get("change") != "" {
		t.Fatal("verification incorrectly requests an email change")
	}
	user.must("POST", "/auth/email-verification/confirm", map[string]any{"token": link.Query().Get("token")}, 204)
	user.must("POST", "/auth/email-change/request", map[string]any{"email": "changed@example.test"}, 200)
	link = readLink()
	if link.Query().Get("change") != "1" {
		t.Fatal("email-change link did not select the client confirmation action")
	}
	user.must("POST", "/auth/email-change/confirm", map[string]any{"token": link.Query().Get("token")}, 204)
	if user.must("GET", "/auth/me", nil, 200)["email"] != "changed@example.test" {
		t.Fatal("confirmed email change was not persisted")
	}
}

func TestPasswordResetMagicAndAdministration(t *testing.T) {
	t.Setenv("SMTP_HOST", "127.0.0.1")
	f := newFixture(t)
	admin := f.client()
	admin.account("admin@example.test", true)
	user := f.client()
	u := user.account("reader@example.test", false)
	admin.must("GET", "/admin/stats", nil, 200)
	user.must("GET", "/admin/stats", nil, 403)
	user.must("POST", "/auth/forgot-password", map[string]any{"email": "reader@example.test"}, 200)
	var text string
	if err := f.db.SQL.QueryRow(`SELECT payload->>'text' FROM outbox_events WHERE deduplication_key LIKE 'auth-email:%' ORDER BY created_at DESC LIMIT 1`).Scan(&text); err != nil {
		t.Fatal(err)
	}
	link, err := url.Parse(strings.Fields(text)[len(strings.Fields(text))-1])
	if err != nil || link.Path != "/reset-password" {
		t.Fatalf("reset link does not match the browser route: %s", text)
	}
	token := link.Query().Get("token")
	user.must("POST", "/auth/reset-password", map[string]any{"token": token, "new_password": "new-test-password-456!"}, 204)
	user.must("GET", "/auth/me", nil, 401)
	user.must("POST", "/auth/reset-password", map[string]any{"token": token, "new_password": "new-test-password-456!"}, 400)
	user.must("POST", "/auth/login", map[string]any{"email": "reader@example.test", "password": "test-password-123!"}, 401)
	user.must("POST", "/auth/login", map[string]any{"email": "reader@example.test", "password": "new-test-password-456!"}, 200)
	magic := f.client()
	challenge := magic.must("POST", "/auth/magic/request", map[string]any{"email": "magic@example.test"}, 200)
	if err := f.db.SQL.QueryRow(`SELECT payload->>'text' FROM outbox_events WHERE deduplication_key LIKE 'auth-magic:%' ORDER BY created_at DESC LIMIT 1`).Scan(&text); err != nil {
		t.Fatal(err)
	}
	code := regexp.MustCompile(`\b\d{6}\b`).FindString(text)
	if code == "" {
		t.Fatal("no emailed code")
	}
	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}
	magic.must("POST", "/auth/magic/verify", map[string]any{"challenge_id": challenge["challenge_id"], "code": wrong}, 400)
	var attempts int
	if err := f.db.SQL.QueryRow(`SELECT attempts FROM auth_challenges WHERE id=$1`, challenge["challenge_id"]).Scan(&attempts); err != nil || attempts != 1 {
		t.Fatalf("incorrect code attempts=%d err=%v", attempts, err)
	}
	result := magic.must("POST", "/auth/magic/verify", map[string]any{"challenge_id": challenge["challenge_id"], "code": code, "display_name": "Magic"}, 200)
	if result["user"].(map[string]any)["password_set"] != false {
		t.Fatal("magic account must not claim an assigned password")
	}
	magic.must("POST", "/auth/magic/verify", map[string]any{"challenge_id": challenge["challenge_id"], "code": code}, 400)
	magic.must("POST", "/auth/password", map[string]any{"new_password": "first-magic-password-789!"}, 204)
	admin.must("PATCH", "/admin/users/"+u["id"].(string), map[string]any{"is_active": false}, 200)
	user.must("GET", "/auth/me", nil, 401)
	adminMe := admin.must("GET", "/auth/me", nil, 200)
	admin.must("PATCH", "/admin/users/"+adminMe["id"].(string), map[string]any{"is_instance_admin": false}, 409)
	admin.must("PATCH", "/admin/configuration", map[string]any{"registration_enabled": false, "settings": map[string]any{"allow_workspace_creation": false, "magic_login_enabled": false}}, 200)
	magic.must("POST", "/workspaces", map[string]any{"name": "Blocked", "slug": "blocked"}, 403)
}

func TestOAuthCodeFlowStatePKCEAndVerifiedIdentity(t *testing.T) {
	f := newFixture(t)
	admin := f.client()
	admin.account("admin@example.test", true)
	var currentChallenge atomic.Value
	currentChallenge.Store("")
	var verified atomic.Bool
	verified.Store(true)
	var tokenCalls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/token":
			r.ParseForm()
			verifier := r.Form.Get("code_verifier")
			digest := sha256.Sum256([]byte(verifier))
			if verifier == "" || base64.RawURLEncoding.EncodeToString(digest[:]) != currentChallenge.Load().(string) || r.Form.Get("client_secret") != "test-client-secret" {
				t.Error("OAuth PKCE/client binding was not preserved")
				w.WriteHeader(400)
				return
			}
			tokenCalls.Add(1)
			json.NewEncoder(w).Encode(map[string]any{"access_token": "test-access-token"})
		case "/userinfo":
			if r.Header.Get("Authorization") != "Bearer test-access-token" {
				t.Error("provider token not supplied")
				w.WriteHeader(401)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"sub": "provider-user-42", "email": "provider@example.test", "email_verified": verified.Load(), "name": "Provider User"})
		default:
			w.WriteHeader(404)
		}
	}))
	defer provider.Close()
	t.Setenv("OAUTH_GOOGLE_CLIENT_ID", "test-client")
	t.Setenv("OAUTH_GOOGLE_CLIENT_SECRET", "test-client-secret")
	t.Setenv("OAUTH_GOOGLE_AUTHORIZE_URL", provider.URL+"/authorize")
	t.Setenv("OAUTH_GOOGLE_TOKEN_URL", provider.URL+"/token")
	t.Setenv("OAUTH_GOOGLE_USERINFO_URL", provider.URL+"/userinfo")
	t.Setenv("API_PUBLIC_URL", f.server.URL)
	client := f.client()
	client.http.CheckRedirect = func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }
	start, err := client.http.Get(f.server.URL + "/api/v1/auth/oauth/google")
	if err != nil {
		t.Fatal(err)
	}
	start.Body.Close()
	if start.StatusCode != 302 {
		t.Fatalf("OAuth start status %d", start.StatusCode)
	}
	location, _ := url.Parse(start.Header.Get("Location"))
	state := location.Query().Get("state")
	currentChallenge.Store(location.Query().Get("code_challenge"))
	callback := fmt.Sprintf("%s/api/v1/auth/oauth/google/callback?code=test-code&state=%s", f.server.URL, url.QueryEscape(state))
	resp, err := client.http.Get(callback)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 302 || resp.Header.Get("Location") != "http://app.test/" {
		t.Fatalf("OAuth callback: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	me := client.must("GET", "/auth/me", nil, 200)
	if me["email"] != "provider@example.test" || me["email_verified"] != true {
		t.Fatalf("wrong OAuth user: %v", me)
	}
	replay, err := client.http.Get(callback)
	if err != nil {
		t.Fatal(err)
	}
	replay.Body.Close()
	if !strings.Contains(replay.Header.Get("Location"), "oauth_state_invalid") || tokenCalls.Load() != 1 {
		t.Fatalf("OAuth state replay was accepted: %s", replay.Header.Get("Location"))
	}
	var count int
	if err = f.db.SQL.QueryRow(`SELECT count(*) FROM oauth_accounts WHERE provider='google'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("provider binding count=%d err=%v", count, err)
	}
	verified.Store(false)
	unverified := f.client()
	unverified.http.CheckRedirect = func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }
	start, err = unverified.http.Get(f.server.URL + "/api/v1/auth/oauth/google")
	if err != nil {
		t.Fatal(err)
	}
	start.Body.Close()
	location, _ = url.Parse(start.Header.Get("Location"))
	currentChallenge.Store(location.Query().Get("code_challenge"))
	resp, err = unverified.http.Get(f.server.URL + "/api/v1/auth/oauth/google/callback?code=unverified-code&state=" + location.Query().Get("state"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if !strings.Contains(resp.Header.Get("Location"), "oauth_identity_unverified") {
		t.Fatalf("unverified identity accepted: %s", resp.Header.Get("Location"))
	}
	unverified.must("GET", "/auth/me", nil, 401)
}

func TestOAuthProviderIdentityAdapters(t *testing.T) {
	for _, name := range []string{"google", "github", "gitlab", "gitea"} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/token":
					json.NewEncoder(w).Encode(map[string]any{"access_token": "adapter-token"})
				case "/userinfo":
					if r.Header.Get("Authorization") != "Bearer adapter-token" {
						t.Error("missing provider authorization")
					}
					if name == "github" {
						json.NewEncoder(w).Encode(map[string]any{"id": 9123456789012345, "login": "git-person", "email": "unverified@example.test", "avatar_url": "https://example.test/avatar.png"})
					} else {
						json.NewEncoder(w).Encode(map[string]any{"sub": "subject-1", "email": "verified@example.test", "email_verified": true, "name": "Person"})
					}
				case "/emails":
					json.NewEncoder(w).Encode([]map[string]any{{"email": "unverified@example.test", "verified": false, "primary": true}, {"email": "verified@example.test", "verified": true, "primary": false}})
				default:
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			p := oauthProvider{Name: name, ClientID: "id", ClientSecret: "secret", TokenURL: server.URL + "/token", UserInfoURL: server.URL + "/userinfo", EmailsURL: server.URL + "/emails"}
			user, err := exchangeIdentity(t.Context(), p, "code", "verifier", "http://app.test/callback")
			if err != nil || !user.Verified || user.Email != "verified@example.test" || user.Subject == "" {
				t.Fatalf("identity=%+v err=%v", user, err)
			}
		})
	}
}

func TestInvitationAndBearerWorkspaceBoundary(t *testing.T) {
	t.Setenv("SMTP_HOST", "127.0.0.1")
	f := newFixture(t)
	owner := f.client()
	ownerUser := owner.account("owner@example.test", true)
	recipient := f.client()
	recipient.account("recipient@example.test", false)
	stranger := f.client()
	stranger.account("stranger@example.test", false)
	w := owner.must("POST", "/workspaces", map[string]any{"name": "Invitations", "slug": "invitations"}, 201)
	wid := w["id"].(string)
	invitation := owner.must("POST", "/workspaces/"+wid+"/invitations", map[string]any{"email": "recipient@example.test", "role": 15}, 201)
	if invitation["token"] != nil || invitation["token_hash"] != nil {
		t.Fatal("invitation secret leaked")
	}
	var text string
	if err := f.db.SQL.QueryRow(`SELECT payload->>'text' FROM outbox_events WHERE deduplication_key=$1`, "invitation:"+invitation["id"].(string)).Scan(&text); err != nil {
		t.Fatal(err)
	}
	link, err := url.Parse(strings.Fields(text)[len(strings.Fields(text))-1])
	if err != nil || !strings.HasPrefix(link.Path, "/invitations/") {
		t.Fatalf("invitation link does not match the browser route: %s", text)
	}
	token := strings.TrimPrefix(link.Path, "/invitations/")
	stranger.must("POST", "/invitations/accept", map[string]any{"token": token}, 403)
	recipient.must("POST", "/invitations/accept", map[string]any{"token": token}, 200)
	recipient.must("POST", "/invitations/accept", map[string]any{"token": token}, 404)
	other := owner.must("POST", "/workspaces", map[string]any{"name": "Other", "slug": "other"}, 201)
	apiToken := "mj_test-token-only-for-isolated-fixture"
	if _, err := f.db.SQL.Exec(`INSERT INTO api_tokens(user_id,workspace_id,name,token_hash,prefix) VALUES($1,$2,'Test',$3,'mj_test')`, ownerUser["id"], wid, hashToken(apiToken)); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]int{"/workspaces/" + wid: 200, "/workspaces/" + other["id"].(string): 404, "/auth/me": 403} {
		req, _ := http.NewRequest("GET", f.server.URL+"/api/v1"+path, nil)
		req.Header.Set("Authorization", "Bearer "+apiToken)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("bearer %s got %d want %d", path, resp.StatusCode, want)
		}
	}
}
