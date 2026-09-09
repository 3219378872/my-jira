package foundation

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"my-jira/apps/api/internal/platform/apperror"
	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/httpapi"
	"my-jira/apps/api/internal/platform/identity"
)

const userJSON = `jsonb_build_object('id',u.id,'email',u.email,'display_name',u.display_name,'first_name',u.first_name,'last_name',u.last_name,'avatar_url',u.avatar_url,'timezone',u.timezone,'is_instance_admin',u.is_instance_admin,'email_verified',u.email_verified,'password_set',u.password_set,'preferences',u.preferences,'created_at',u.created_at,'updated_at',u.updated_at)`

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
func equalToken(a, b string) bool {
	return len(a) > 0 && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
func validEmail(email string) bool {
	address, err := mail.ParseAddress(email)
	return err == nil && address.Address == email && len(email) <= 254
}
func validPassword(password string) bool { return len(password) >= 10 && len(password) <= 72 }
func (s *Server) setCookie(c *gin.Context, name, value string, maxAge int, httpOnly bool) {
	http.SetCookie(c.Writer, &http.Cookie{Name: name, Value: value, Path: "/", MaxAge: maxAge, HttpOnly: httpOnly, Secure: s.Config.CookieSecure, SameSite: http.SameSiteLaxMode})
}

// Middleware establishes an optional session and validates every unsafe browser
// request before any handler. API token/public surfaces mount their own policy.
func (s *Server) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		allowedOrigin := origin == ""
		for _, allowed := range strings.Split(s.Config.AppOrigin, ",") {
			if origin != "" && origin == strings.TrimSpace(allowed) {
				allowedOrigin = true
			}
		}
		if !allowedOrigin {
			httpapi.Fail(c, apperror.New(403, "origin_rejected", "This request origin is not allowed"))
			return
		}
		if origin != "" {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Vary", "Origin")
			c.Header("Access-Control-Allow-Credentials", "true")
		}
		if c.Request.Method == http.MethodOptions {
			c.Header("Access-Control-Allow-Methods", "GET,POST,PATCH,PUT,DELETE,OPTIONS")
			c.Header("Access-Control-Allow-Headers", "Content-Type,X-CSRF-Token")
			c.Status(204)
			c.Abort()
			return
		}
		if authorization := c.GetHeader("Authorization"); authorization != "" {
			if !strings.HasPrefix(authorization, "Bearer ") {
				httpapi.Fail(c, apperror.Unauthorized())
				return
			}
			var actor identity.Actor
			var tokenID uuid.UUID
			err := s.Deps.DB.SQL.QueryRowContext(c.Request.Context(), `SELECT t.id,u.id,t.workspace_id FROM api_tokens t JOIN users u ON u.id=t.user_id WHERE t.token_hash=$1 AND t.revoked_at IS NULL AND (t.expires_at IS NULL OR t.expires_at>now()) AND t.deleted_at IS NULL AND u.is_active AND u.deleted_at IS NULL`, hashToken(strings.TrimSpace(strings.TrimPrefix(authorization, "Bearer ")))).Scan(&tokenID, &actor.UserID, &actor.TokenWorkspaceID)
			if err == sql.ErrNoRows {
				httpapi.Fail(c, apperror.Unauthorized())
				return
			}
			if err != nil {
				httpapi.Fail(c, err)
				return
			}
			if !strings.HasPrefix(c.Request.URL.Path, "/api/v1/workspaces/") && !(c.Request.URL.Path == "/api/v1/workspaces" && c.Request.Method == "GET") {
				httpapi.Fail(c, apperror.Forbidden())
				return
			}
			c.Set(httpapi.ActorKey, actor)
			if _, err = s.Deps.DB.SQL.ExecContext(c.Request.Context(), `UPDATE api_tokens SET last_used_at=now(),updated_at=now() WHERE id=$1`, tokenID); err != nil {
				httpapi.Fail(c, err)
				return
			}
		} else if token, err := c.Cookie("mj_session"); err == nil && token != "" {
			var actor identity.Actor
			var csrfHash string
			err = s.Deps.DB.SQL.QueryRowContext(c.Request.Context(), `SELECT s.id,u.id,u.is_instance_admin,s.csrf_hash FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=$1 AND s.expires_at>now() AND s.revoked_at IS NULL AND s.deleted_at IS NULL AND u.is_active AND u.deleted_at IS NULL`, hashToken(token)).Scan(&actor.SessionID, &actor.UserID, &actor.IsAdmin, &csrfHash)
			if err == nil {
				c.Set(httpapi.ActorKey, actor)
				c.Set("my-jira.csrf_hash", csrfHash)
			} else if err != sql.ErrNoRows {
				httpapi.Fail(c, err)
				return
			}
		}
		if c.Request.Method != "GET" && c.Request.Method != "HEAD" {
			if actor, err := httpapi.Actor(c); err == nil && actor.TokenWorkspaceID != uuid.Nil {
				c.Next()
				return
			}
			cookie, _ := c.Cookie("mj_csrf")
			header := c.GetHeader("X-CSRF-Token")
			if len(cookie) != 43 || !equalToken(cookie, header) || c.GetHeader("Sec-Fetch-Site") == "cross-site" {
				httpapi.Fail(c, apperror.New(403, "csrf_failed", "Refresh the security token and retry"))
				return
			}
			if expected, ok := c.Get("my-jira.csrf_hash"); ok && !equalToken(expected.(string), hashToken(header)) {
				httpapi.Fail(c, apperror.New(403, "csrf_failed", "Refresh the security token and retry"))
				return
			}
			if c.Request.Method == "POST" && (strings.HasPrefix(c.Request.URL.Path, "/api/v1/auth/") || c.Request.URL.Path == "/api/v1/instance/setup") {
				if err := s.rateLimit(c, 20); err != nil {
					httpapi.Fail(c, err)
					return
				}
			}
		}
		c.Next()
	}
}

func (s *Server) csrf(c *gin.Context) {
	token, _ := c.Cookie("mj_csrf")
	if len(token) == 43 {
		if expected, ok := c.Get("my-jira.csrf_hash"); !ok || equalToken(expected.(string), hashToken(token)) {
			httpapi.JSON(c, 200, gin.H{"csrf_token": token})
			return
		}
	}
	token, err := randomToken()
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	if actor, err := httpapi.Actor(c); err == nil {
		if _, err = s.Deps.DB.SQL.ExecContext(c.Request.Context(), `UPDATE sessions SET csrf_hash=$2,updated_at=now() WHERE id=$1`, actor.SessionID, hashToken(token)); err != nil {
			httpapi.Fail(c, err)
			return
		}
	}
	s.setCookie(c, "mj_csrf", token, int(s.Config.SessionDuration.Seconds()), false)
	httpapi.JSON(c, 200, gin.H{"csrf_token": token})
}

type credentials struct {
	Email        string `json:"email"`
	Password     string `json:"password"`
	DisplayName  string `json:"display_name"`
	InstanceName string `json:"instance_name"`
}
type authResult struct {
	user                    map[string]any
	sessionToken, csrfToken string
}

func (s *Server) newSession(ctx context.Context, q database.DBTX, userID uuid.UUID, userAgent, ip string) (authResult, error) {
	var result authResult
	var err error
	result.sessionToken, err = randomToken()
	if err != nil {
		return result, err
	}
	result.csrfToken, err = randomToken()
	if err != nil {
		return result, err
	}
	_, err = q.ExecContext(ctx, `INSERT INTO sessions(user_id,token_hash,csrf_hash,expires_at,user_agent,ip_address) VALUES($1,$2,$3,$4,$5,$6)`, userID, hashToken(result.sessionToken), hashToken(result.csrfToken), time.Now().Add(s.Config.SessionDuration), userAgent, ip)
	if err != nil {
		return result, err
	}
	result.user, err = queryObject(ctx, q, `SELECT `+userJSON+` FROM users u WHERE id=$1 AND deleted_at IS NULL`, userID)
	return result, err
}
func (s *Server) finishAuth(c *gin.Context, status int, result authResult) {
	s.setCookie(c, "mj_session", result.sessionToken, int(s.Config.SessionDuration.Seconds()), true)
	s.setCookie(c, "mj_csrf", result.csrfToken, int(s.Config.SessionDuration.Seconds()), false)
	httpapi.JSON(c, status, gin.H{"user": result.user, "csrf_token": result.csrfToken})
}

func (s *Server) instance(c *gin.Context) {
	value, err := queryObject(c.Request.Context(), s.Deps.DB.SQL, `SELECT jsonb_build_object('is_setup_done',true,'name',name,'registration_enabled',registration_enabled,'magic_login_enabled',COALESCE((settings->>'magic_login_enabled')::bool,true),'allow_workspace_creation',COALESCE((settings->>'allow_workspace_creation')::bool,true)) FROM instances WHERE singleton AND deleted_at IS NULL`)
	if e, ok := err.(*apperror.Error); ok && e.Status == 404 {
		httpapi.JSON(c, 200, gin.H{"is_setup_done": false, "name": "my-jira", "registration_enabled": false, "auth_methods": []string{"password"}})
		return
	}
	if err == nil {
		methods := s.authMethods()
		if value["magic_login_enabled"] == false {
			filtered := []string{}
			for _, method := range methods {
				if method != "magic" {
					filtered = append(filtered, method)
				}
			}
			methods = filtered
		}
		value["auth_methods"] = methods
	}
	reply(c, 200, value, err)
}
func (s *Server) setup(c *gin.Context)    { s.createAccount(c, true) }
func (s *Server) register(c *gin.Context) { s.createAccount(c, false) }
func (s *Server) createAccount(c *gin.Context, setup bool) {
	input, err := httpapi.Bind[credentials](c)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	if !validEmail(input.Email) || !validName(input.DisplayName) || !validPassword(input.Password) {
		httpapi.Fail(c, apperror.Invalid("Provide a valid email, display name, and password of 10 to 72 bytes"))
		return
	}
	if setup && !validName(input.InstanceName) {
		httpapi.Fail(c, apperror.Invalid("An instance name is required"))
		return
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(input.Password), 12)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	var result authResult
	err = s.Deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if _, err := q.ExecContext(c.Request.Context(), `SELECT pg_advisory_xact_lock(928430117)`); err != nil {
			return err
		}
		var enabled bool
		err := q.QueryRowContext(c.Request.Context(), `SELECT registration_enabled FROM instances WHERE singleton AND deleted_at IS NULL`).Scan(&enabled)
		if setup {
			if err == nil {
				return apperror.Conflict("This instance is already configured")
			}
			if err != sql.ErrNoRows {
				return err
			}
		} else {
			if err == sql.ErrNoRows {
				return apperror.Conflict("Set up the instance before registering")
			}
			if err != nil {
				return err
			}
			if !enabled {
				return apperror.Forbidden()
			}
		}
		id := uuid.New()
		_, err = q.ExecContext(c.Request.Context(), `INSERT INTO users(id,email,password_hash,display_name,is_instance_admin) VALUES($1,$2,$3,$4,$5)`, id, input.Email, string(passwordHash), input.DisplayName, setup)
		if err != nil {
			return err
		}
		if setup {
			_, err = q.ExecContext(c.Request.Context(), `INSERT INTO instances(name,setup_by) VALUES($1,$2)`, strings.TrimSpace(input.InstanceName), id)
			if err != nil {
				return err
			}
		}
		result, err = s.newSession(c.Request.Context(), q, id, c.Request.UserAgent(), c.ClientIP())
		return err
	})
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	s.finishAuth(c, 201, result)
}
func (s *Server) login(c *gin.Context) {
	input, err := httpapi.Bind[credentials](c)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	var id uuid.UUID
	var passwordHash string
	err = s.Deps.DB.SQL.QueryRowContext(c.Request.Context(), `SELECT id,password_hash FROM users WHERE email=$1 AND is_active AND deleted_at IS NULL`, input.Email).Scan(&id, &passwordHash)
	if err != nil && err != sql.ErrNoRows {
		httpapi.Fail(c, err)
		return
	}
	if err == sql.ErrNoRows || bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(input.Password)) != nil {
		httpapi.Fail(c, apperror.New(401, "invalid_credentials", "The email or password is incorrect"))
		return
	}
	var result authResult
	err = s.Deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		var err error
		result, err = s.newSession(c.Request.Context(), q, id, c.Request.UserAgent(), c.ClientIP())
		return err
	})
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	s.finishAuth(c, 200, result)
}
func (s *Server) logout(c *gin.Context) {
	actor, _ := httpapi.Actor(c)
	_, err := s.Deps.DB.SQL.ExecContext(c.Request.Context(), `UPDATE sessions SET revoked_at=now(),updated_at=now() WHERE id=$1`, actor.SessionID)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	s.setCookie(c, "mj_session", "", -1, true)
	s.setCookie(c, "mj_csrf", "", -1, false)
	c.Status(204)
}
func (s *Server) me(c *gin.Context) {
	a, _ := httpapi.Actor(c)
	// Ent is also the typed data access layer for ordinary entity retrieval.
	u, err := s.Deps.DB.Ent.User.Get(c.Request.Context(), a.UserID)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.JSON(c, 200, gin.H{"id": u.ID, "email": u.Email, "display_name": u.DisplayName, "first_name": u.FirstName, "last_name": u.LastName, "avatar_url": u.AvatarURL, "timezone": u.Timezone, "is_instance_admin": u.IsInstanceAdmin, "email_verified": u.EmailVerified, "password_set": u.PasswordSet, "preferences": u.Preferences, "created_at": u.CreatedAt, "updated_at": u.UpdatedAt})
}
func (s *Server) updateMe(c *gin.Context) {
	a, _ := httpapi.Actor(c)
	input, err := httpapi.Bind[map[string]any](c)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	values := map[string]any{}
	var preferenceChanges map[string]any
	for key, value := range input {
		switch key {
		case "display_name", "first_name", "last_name", "avatar_url", "timezone":
			v, ok := value.(string)
			if !ok || len(v) > 2048 || (key == "display_name" && !validName(v)) || (key == "timezone" && !validTimezone(v)) {
				httpapi.Fail(c, apperror.Invalid("Invalid "+key))
				return
			}
			values[key] = strings.TrimSpace(v)
		case "preferences":
			preferences, ok := value.(map[string]any)
			if !ok {
				httpapi.Fail(c, apperror.Invalid("Preferences must be an object"))
				return
			}
			// Navigation is owned by its scoped endpoint. Preference forms may
			// send the user's current object, but cannot inject a location.
			delete(preferences, "_last_visited")
			preferenceChanges = preferences
		default:
			httpapi.Fail(c, apperror.Invalid("Unsupported user field: "+key))
			return
		}
	}
	err = s.Deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if preferenceChanges != nil {
			var raw []byte
			if err := q.QueryRowContext(c.Request.Context(), `SELECT preferences FROM users WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, a.UserID).Scan(&raw); err != nil {
				return err
			}
			preferences := map[string]any{}
			if err := json.Unmarshal(raw, &preferences); err != nil {
				return err
			}
			merged, err := json.Marshal(mergeJSONObjects(preferences, preferenceChanges))
			if err != nil {
				return err
			}
			values["preferences"] = string(merged)
		}
		return patch(c.Request.Context(), q, "users", a.UserID, "", nil, values)
	})
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	s.me(c)
}
func (s *Server) updateInstance(c *gin.Context) {
	a, _ := httpapi.Actor(c)
	if !a.IsAdmin {
		httpapi.Fail(c, apperror.Forbidden())
		return
	}
	input, err := httpapi.Bind[struct {
		Name                *string `json:"name"`
		RegistrationEnabled *bool   `json:"registration_enabled"`
	}](c)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	if input.Name != nil && !validName(*input.Name) {
		httpapi.Fail(c, apperror.Invalid("An instance name is required"))
		return
	}
	_, err = s.Deps.DB.SQL.ExecContext(c.Request.Context(), `UPDATE instances SET name=COALESCE($1,name),registration_enabled=COALESCE($2,registration_enabled),updated_at=now() WHERE singleton`, input.Name, input.RegistrationEnabled)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	s.instance(c)
}
func (s *Server) changePassword(c *gin.Context) {
	a, _ := httpapi.Actor(c)
	input, err := httpapi.Bind[struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}](c)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	if !validPassword(input.NewPassword) {
		httpapi.Fail(c, apperror.Invalid("Password must contain 10 to 72 bytes"))
		return
	}
	var oldHash string
	var passwordSet bool
	if err = s.Deps.DB.SQL.QueryRowContext(c.Request.Context(), `SELECT password_hash,password_set FROM users WHERE id=$1`, a.UserID).Scan(&oldHash, &passwordSet); err != nil {
		httpapi.Fail(c, err)
		return
	}
	if passwordSet && bcrypt.CompareHashAndPassword([]byte(oldHash), []byte(input.CurrentPassword)) != nil {
		httpapi.Fail(c, apperror.New(400, "invalid_password", "The current password is incorrect"))
		return
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte(input.NewPassword), 12)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	err = s.Deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		var currentHash string
		var currentPasswordSet bool
		if err := q.QueryRowContext(c.Request.Context(), `SELECT password_hash,password_set FROM users WHERE id=$1 AND is_active AND deleted_at IS NULL FOR UPDATE`, a.UserID).Scan(&currentHash, &currentPasswordSet); err != nil {
			return err
		}
		if currentHash != oldHash || currentPasswordSet != passwordSet {
			return apperror.Conflict("The password changed during this request; sign in again before retrying")
		}
		if _, err := q.ExecContext(c.Request.Context(), `UPDATE users SET password_hash=$2,password_set=true,updated_at=now() WHERE id=$1`, a.UserID, string(newHash)); err != nil {
			return err
		}
		_, err := q.ExecContext(c.Request.Context(), `UPDATE sessions SET revoked_at=now(),updated_at=now() WHERE user_id=$1 AND id<>$2 AND revoked_at IS NULL`, a.UserID, a.SessionID)
		return err
	})
	noContent(c, err)
}
func (s *Server) sessions(c *gin.Context) {
	a, _ := httpapi.Actor(c)
	value, err := queryList(c.Request.Context(), s.Deps.DB.SQL, `SELECT jsonb_build_object('id',id,'created_at',created_at,'expires_at',expires_at,'user_agent',user_agent,'ip_address',ip_address,'is_current',id=$2) FROM sessions WHERE user_id=$1 AND expires_at>now() AND revoked_at IS NULL AND deleted_at IS NULL ORDER BY created_at DESC`, a.UserID, a.SessionID)
	reply(c, 200, value, err)
}
func (s *Server) revokeSession(c *gin.Context) {
	a, _ := httpapi.Actor(c)
	id, err := httpapi.UUIDParam(c, "sessionID")
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	err = affected(s.Deps.DB.SQL.ExecContext(c.Request.Context(), `UPDATE sessions SET revoked_at=now(),updated_at=now() WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL`, id, a.UserID))
	noContent(c, err)
}
