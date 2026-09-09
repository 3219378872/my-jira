package foundation

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"my-jira/apps/api/internal/platform/apperror"
	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/httpapi"
	"my-jira/apps/api/internal/platform/serviceconfig"
)

type oauthProvider struct{ Name, ClientID, ClientSecret, AuthorizeURL, TokenURL, UserInfoURL, EmailsURL, Scope string }

func providerConfig(name string) (oauthProvider, error) {
	return providerFromValues(name, serviceconfig.Defaults(name))
}
func (s *Server) resolveProvider(ctx context.Context, name string) (oauthProvider, error) {
	if name != "google" && name != "github" && name != "gitlab" && name != "gitea" {
		return oauthProvider{}, apperror.NotFound()
	}
	values, err := serviceconfig.Load(ctx, s.Deps.DB.SQL, name)
	if err != nil {
		return oauthProvider{}, err
	}
	return providerFromValues(name, values)
}
func providerFromValues(name string, values serviceconfig.Values) (oauthProvider, error) {
	p := oauthProvider{Name: name}
	p.ClientID = values.String("client_id")
	p.ClientSecret = values.String("client_secret")
	switch name {
	case "google":
		p.AuthorizeURL = "https://accounts.google.com/o/oauth2/v2/auth"
		p.TokenURL = "https://oauth2.googleapis.com/token"
		p.UserInfoURL = "https://openidconnect.googleapis.com/v1/userinfo"
		p.Scope = "openid email profile"
	case "github":
		p.AuthorizeURL = "https://github.com/login/oauth/authorize"
		p.TokenURL = "https://github.com/login/oauth/access_token"
		p.UserInfoURL = "https://api.github.com/user"
		p.EmailsURL = "https://api.github.com/user/emails"
		p.Scope = "read:user user:email"
	case "gitlab":
		base := strings.TrimRight(values.String("base_url"), "/")
		if base == "" {
			base = "https://gitlab.com"
		}
		p.AuthorizeURL = base + "/oauth/authorize"
		p.TokenURL = base + "/oauth/token"
		p.UserInfoURL = base + "/oauth/userinfo"
		p.Scope = "openid email profile"
	case "gitea":
		base := strings.TrimRight(values.String("base_url"), "/")
		if base == "" {
			return p, apperror.New(503, "provider_unavailable", "Gitea base URL is not configured")
		}
		p.AuthorizeURL = base + "/login/oauth/authorize"
		p.TokenURL = base + "/login/oauth/access_token"
		p.UserInfoURL = base + "/login/oauth/userinfo"
		p.Scope = "openid email profile"
	default:
		return p, apperror.NotFound()
	}
	if v := values.String("authorize_url"); v != "" {
		p.AuthorizeURL = v
	}
	if v := values.String("token_url"); v != "" {
		p.TokenURL = v
	}
	if v := values.String("userinfo_url"); v != "" {
		p.UserInfoURL = v
	}
	if v := values.String("emails_url"); v != "" {
		p.EmailsURL = v
	}
	if p.ClientID == "" || p.ClientSecret == "" {
		return p, apperror.New(503, "provider_unavailable", "This sign-in provider is not configured")
	}
	for _, endpoint := range []string{p.AuthorizeURL, p.TokenURL, p.UserInfoURL} {
		u, err := url.Parse(endpoint)
		if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
			return p, apperror.New(503, "provider_unavailable", "The provider endpoint configuration is invalid")
		}
	}
	return p, nil
}
func (s *Server) authMethods() []string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	methods := []string{"password"}
	if s.emailConfigured() {
		methods = append(methods, "magic")
	}
	for _, name := range []string{"google", "github", "gitlab", "gitea"} {
		if _, err := s.resolveProvider(ctx, name); err == nil {
			methods = append(methods, name)
		}
	}
	return methods
}
func (s *Server) callbackURL(c *gin.Context, provider string) (string, error) {
	base := strings.TrimRight(os.Getenv("API_PUBLIC_URL"), "/")
	if base == "" {
		host := c.Request.Host
		hostname := host
		if value, _, err := net.SplitHostPort(host); err == nil {
			hostname = value
		}
		if hostname != "localhost" {
			ip := net.ParseIP(hostname)
			if ip == nil || !ip.IsLoopback() {
				return "", apperror.New(503, "provider_unavailable", "API_PUBLIC_URL must be configured for OAuth")
			}
		}
		scheme := "http"
		if c.Request.TLS != nil {
			scheme = "https"
		}
		base = scheme + "://" + host
	}
	return base + "/api/v1/auth/oauth/" + provider + "/callback", nil
}
func (s *Server) oauthStart(c *gin.Context) {
	if err := s.rateLimit(c, 30); err != nil {
		httpapi.Fail(c, err)
		return
	}
	provider, err := s.resolveProvider(c.Request.Context(), c.Param("provider"))
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	callback, err := s.callbackURL(c, provider.Name)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	var setup bool
	if err = s.Deps.DB.SQL.QueryRowContext(c.Request.Context(), `SELECT EXISTS(SELECT 1 FROM instances WHERE singleton AND deleted_at IS NULL)`).Scan(&setup); err != nil {
		httpapi.Fail(c, err)
		return
	}
	if !setup {
		httpapi.Fail(c, apperror.Conflict("Set up the instance before signing in"))
		return
	}
	state, err := randomToken()
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	verifier, err := randomToken()
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	returnTo := c.Query("return_to")
	if returnTo == "" {
		returnTo = "/"
	}
	if !strings.HasPrefix(returnTo, "/") || strings.HasPrefix(returnTo, "//") || strings.ContainsAny(returnTo, "\\\r\n") {
		httpapi.Fail(c, apperror.Invalid("Invalid return path"))
		return
	}
	metadata, _ := json.Marshal(map[string]string{"verifier": verifier, "return_to": returnTo, "redirect_uri": callback})
	if _, err = s.Deps.DB.SQL.ExecContext(c.Request.Context(), `INSERT INTO auth_challenges(email,purpose,token_hash,expires_at,metadata) VALUES('',$1,$2,$3,$4::jsonb)`, "oauth:"+provider.Name, hashToken(state), time.Now().Add(10*time.Minute), string(metadata)); err != nil {
		httpapi.Fail(c, err)
		return
	}
	s.setCookie(c, "mj_oauth_"+provider.Name, state, 600, true)
	authorization, _ := url.Parse(provider.AuthorizeURL)
	query := authorization.Query()
	query.Set("client_id", provider.ClientID)
	query.Set("redirect_uri", callback)
	query.Set("response_type", "code")
	query.Set("scope", provider.Scope)
	query.Set("state", state)
	digest := sha256.Sum256([]byte(verifier))
	query.Set("code_challenge", base64.RawURLEncoding.EncodeToString(digest[:]))
	query.Set("code_challenge_method", "S256")
	authorization.RawQuery = query.Encode()
	c.Redirect(302, authorization.String())
}

type providerUser struct {
	Subject, Email, Name, AvatarURL string
	Verified                        bool
}

func (s *Server) oauthFailure(c *gin.Context, code string) {
	c.Redirect(302, s.appURL("/login?auth_error="+url.QueryEscape(code)))
}
func (s *Server) oauthCallback(c *gin.Context) {
	provider, err := s.resolveProvider(c.Request.Context(), c.Param("provider"))
	if err != nil {
		s.oauthFailure(c, "provider_unavailable")
		return
	}
	state := c.Query("state")
	cookie, _ := c.Cookie("mj_oauth_" + provider.Name)
	s.setCookie(c, "mj_oauth_"+provider.Name, "", -1, true)
	if len(state) != 43 || !equalToken(cookie, state) || c.Query("code") == "" || c.Query("error") != "" {
		s.oauthFailure(c, "oauth_state_invalid")
		return
	}
	var metadata struct {
		Verifier    string `json:"verifier"`
		ReturnTo    string `json:"return_to"`
		RedirectURI string `json:"redirect_uri"`
	}
	err = s.Deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		var id uuid.UUID
		var raw []byte
		if err := q.QueryRowContext(c.Request.Context(), `SELECT id,metadata FROM auth_challenges WHERE purpose=$1 AND token_hash=$2 AND expires_at>now() AND consumed_at IS NULL AND deleted_at IS NULL FOR UPDATE`, "oauth:"+provider.Name, hashToken(state)).Scan(&id, &raw); err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &metadata); err != nil {
			return err
		}
		_, err := q.ExecContext(c.Request.Context(), `UPDATE auth_challenges SET consumed_at=now(),updated_at=now() WHERE id=$1`, id)
		return err
	})
	if err != nil {
		s.oauthFailure(c, "oauth_state_invalid")
		return
	}
	user, err := exchangeIdentity(c.Request.Context(), provider, c.Query("code"), metadata.Verifier, metadata.RedirectURI)
	if err != nil || !user.Verified || !validEmail(user.Email) || user.Subject == "" {
		s.oauthFailure(c, "oauth_identity_unverified")
		return
	}
	var result authResult
	err = s.Deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if _, err := q.ExecContext(c.Request.Context(), `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, provider.Name+":"+user.Subject); err != nil {
			return err
		}
		var id uuid.UUID
		err := q.QueryRowContext(c.Request.Context(), `SELECT u.id FROM oauth_accounts a JOIN users u ON u.id=a.user_id WHERE a.provider=$1 AND a.subject=$2 AND a.deleted_at IS NULL AND u.is_active AND u.deleted_at IS NULL`, provider.Name, user.Subject).Scan(&id)
		if err == sql.ErrNoRows {
			err = q.QueryRowContext(c.Request.Context(), `SELECT id FROM users WHERE email=$1 AND is_active AND deleted_at IS NULL`, user.Email).Scan(&id)
			if err == sql.ErrNoRows {
				var signup bool
				if err = q.QueryRowContext(c.Request.Context(), `SELECT registration_enabled FROM instances WHERE singleton`).Scan(&signup); err != nil {
					return err
				}
				if !signup {
					return apperror.Forbidden()
				}
				password, err := randomToken()
				if err != nil {
					return err
				}
				hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
				if err != nil {
					return err
				}
				id = uuid.New()
				if !validName(user.Name) {
					user.Name = strings.Split(user.Email, "@")[0]
				}
				if _, err = q.ExecContext(c.Request.Context(), `INSERT INTO users(id,email,password_hash,password_set,display_name,avatar_url,email_verified) VALUES($1,$2,$3,false,$4,$5,true)`, id, user.Email, string(hash), user.Name, user.AvatarURL); err != nil {
					return err
				}
			} else if err != nil {
				return err
			}
			if _, err = q.ExecContext(c.Request.Context(), `INSERT INTO oauth_accounts(user_id,provider,subject,email) VALUES($1,$2,$3,$4)`, id, provider.Name, user.Subject, user.Email); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		if _, err = q.ExecContext(c.Request.Context(), `UPDATE users SET email_verified=true,updated_at=now() WHERE id=$1`, id); err != nil {
			return err
		}
		result, err = s.newSession(c.Request.Context(), q, id, c.Request.UserAgent(), c.ClientIP())
		return err
	})
	if err != nil {
		s.oauthFailure(c, "oauth_sign_in_failed")
		return
	}
	s.setCookie(c, "mj_session", result.sessionToken, int(s.Config.SessionDuration.Seconds()), true)
	s.setCookie(c, "mj_csrf", result.csrfToken, int(s.Config.SessionDuration.Seconds()), false)
	c.Redirect(302, s.appURL(metadata.ReturnTo))
}
func exchangeIdentity(ctx context.Context, p oauthProvider, code, verifier, callback string) (providerUser, error) {
	var identity providerUser
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {p.ClientID}, "client_secret": {p.ClientSecret}, "redirect_uri": {callback}, "code_verifier": {verifier}}
	req, err := http.NewRequestWithContext(ctx, "POST", p.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return identity, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	var token struct {
		AccessToken string `json:"access_token"`
	}
	if err = providerJSON(req, &token); err != nil || token.AccessToken == "" {
		return identity, fmt.Errorf("provider token exchange failed")
	}
	req, err = http.NewRequestWithContext(ctx, "GET", p.UserInfoURL, nil)
	if err != nil {
		return identity, err
	}
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	req.Header.Set("Accept", "application/json")
	var info map[string]any
	if err = providerJSON(req, &info); err != nil {
		return identity, err
	}
	identity.Subject = stringValue(info["sub"])
	identity.Email = strings.ToLower(strings.TrimSpace(stringValue(info["email"])))
	identity.Name = stringValue(info["name"])
	identity.AvatarURL = stringValue(info["picture"])
	identity.Verified, _ = info["email_verified"].(bool)
	if p.Name == "github" {
		identity.Subject = stringValue(info["id"])
		identity.AvatarURL = stringValue(info["avatar_url"])
		if identity.Name == "" {
			identity.Name = stringValue(info["login"])
		}
		emailReq, err := http.NewRequestWithContext(ctx, "GET", p.EmailsURL, nil)
		if err != nil {
			return identity, err
		}
		emailReq.Header.Set("Authorization", "Bearer "+token.AccessToken)
		emailReq.Header.Set("Accept", "application/json")
		var emails []struct {
			Email    string `json:"email"`
			Verified bool   `json:"verified"`
			Primary  bool   `json:"primary"`
		}
		if err = providerJSON(emailReq, &emails); err != nil {
			return identity, err
		}
		identity.Verified = false
		for _, email := range emails {
			if email.Verified && (email.Primary || !identity.Verified) {
				identity.Email = strings.ToLower(email.Email)
				identity.Verified = true
				if email.Primary {
					break
				}
			}
		}
	}
	return identity, nil
}
func providerJSON(req *http.Request, target any) error {
	client := http.Client{Timeout: 20 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("provider returned HTTP %d", resp.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 1<<20))
	decoder.UseNumber()
	return decoder.Decode(target)
}
func stringValue(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	default:
		return ""
	}
}
