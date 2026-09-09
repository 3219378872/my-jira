package foundation

import (
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"math/big"
	"net/url"
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

func (s *Server) rateLimit(c *gin.Context, limit int) error {
	key := hashToken(c.ClientIP() + ":" + c.FullPath())
	var count int
	err := s.Deps.DB.SQL.QueryRowContext(c.Request.Context(), `INSERT INTO auth_rate_limits(key,attempts) VALUES($1,1) ON CONFLICT(key) DO UPDATE SET attempts=CASE WHEN auth_rate_limits.window_started<now()-interval '1 minute' THEN 1 ELSE auth_rate_limits.attempts+1 END,window_started=CASE WHEN auth_rate_limits.window_started<now()-interval '1 minute' THEN now() ELSE auth_rate_limits.window_started END,updated_at=now() RETURNING attempts`, key).Scan(&count)
	if err != nil {
		return err
	}
	if count > limit {
		c.Header("Retry-After", "60")
		return apperror.New(429, "rate_limited", "Too many attempts; please wait a minute")
	}
	return nil
}
func (s *Server) emailConfigured() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	values, err := serviceconfig.Load(ctx, s.Deps.DB.SQL, "email")
	return err == nil && values.String("host") != ""
}
func (s *Server) emailReady(c *gin.Context) bool {
	if !s.emailConfigured() {
		httpapi.Fail(c, apperror.New(503, "email_unavailable", "Email delivery has not been configured"))
		return false
	}
	return true
}
func (s *Server) emailNotice(c *gin.Context) {
	httpapi.JSON(c, 200, gin.H{"message": "If this request is eligible, an email will arrive shortly"})
}
func (s *Server) appURL(path string) string {
	return strings.TrimRight(strings.Split(s.Config.AppOrigin, ",")[0], "/") + path
}

func (s *Server) forgotPassword(c *gin.Context) {
	if !s.emailReady(c) {
		return
	}
	input, err := httpapi.Bind[struct {
		Email string `json:"email"`
	}](c)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	email := strings.ToLower(strings.TrimSpace(input.Email))
	if !validEmail(email) {
		httpapi.Fail(c, apperror.Invalid("A valid email is required"))
		return
	}
	var userID uuid.UUID
	err = s.Deps.DB.SQL.QueryRowContext(c.Request.Context(), `SELECT id FROM users WHERE email=$1 AND is_active AND deleted_at IS NULL`, email).Scan(&userID)
	if err == sql.ErrNoRows {
		s.emailNotice(c)
		return
	}
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	err = s.queueToken(c, userID, email, "reset", "Reset your password", "/reset-password")
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	s.emailNotice(c)
}

func (s *Server) queueToken(c *gin.Context, userID uuid.UUID, email, purpose, subject, path string) error {
	token, err := randomToken()
	if err != nil {
		return err
	}
	id := uuid.New()
	link, err := url.Parse(s.appURL(path))
	if err != nil {
		return err
	}
	query := link.Query()
	query.Set("token", token)
	link.RawQuery = query.Encode()
	return s.Deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if _, err := q.ExecContext(c.Request.Context(), `UPDATE auth_challenges SET consumed_at=now(),updated_at=now() WHERE user_id=$1 AND purpose=$2 AND consumed_at IS NULL`, userID, purpose); err != nil {
			return err
		}
		if _, err := q.ExecContext(c.Request.Context(), `INSERT INTO auth_challenges(id,user_id,email,purpose,token_hash,expires_at) VALUES($1,$2,$3,$4,$5,$6)`, id, userID, email, purpose, hashToken(token), time.Now().Add(30*time.Minute)); err != nil {
			return err
		}
		return s.Deps.Jobs.Publish(c.Request.Context(), q, "email.send", map[string]any{"to": email, "subject": subject, "text": subject + ": " + link.String()}, "auth-email:"+id.String())
	})
}
func (s *Server) resetPassword(c *gin.Context) {
	input, err := httpapi.Bind[struct {
		Token       string `json:"token"`
		NewPassword string `json:"new_password"`
	}](c)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	if len(input.Token) != 43 || !validPassword(input.NewPassword) {
		httpapi.Fail(c, apperror.Invalid("A valid token and password of 10 to 72 bytes are required"))
		return
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(input.NewPassword), 12)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	err = s.Deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		var id, userID uuid.UUID
		err := q.QueryRowContext(c.Request.Context(), `SELECT a.id,a.user_id FROM auth_challenges a JOIN users u ON u.id=a.user_id WHERE a.token_hash=$1 AND a.purpose='reset' AND a.consumed_at IS NULL AND a.expires_at>now() AND a.deleted_at IS NULL AND u.is_active AND u.deleted_at IS NULL FOR UPDATE OF a`, hashToken(input.Token)).Scan(&id, &userID)
		if err == sql.ErrNoRows {
			return apperror.Invalid("The reset link is invalid or expired")
		}
		if err != nil {
			return err
		}
		if _, err = q.ExecContext(c.Request.Context(), `UPDATE users SET password_hash=$2,password_set=true,email_verified=true,updated_at=now() WHERE id=$1`, userID, string(passwordHash)); err != nil {
			return err
		}
		if _, err = q.ExecContext(c.Request.Context(), `UPDATE sessions SET revoked_at=now(),updated_at=now() WHERE user_id=$1 AND revoked_at IS NULL`, userID); err != nil {
			return err
		}
		_, err = q.ExecContext(c.Request.Context(), `UPDATE auth_challenges SET consumed_at=now(),updated_at=now() WHERE user_id=$1 AND purpose='reset' AND consumed_at IS NULL`, userID)
		return err
	})
	noContent(c, err)
}

func (s *Server) magicRequest(c *gin.Context) {
	if !s.emailReady(c) {
		return
	}
	input, err := httpapi.Bind[struct {
		Email string `json:"email"`
	}](c)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	email := strings.ToLower(strings.TrimSpace(input.Email))
	if !validEmail(email) {
		httpapi.Fail(c, apperror.Invalid("A valid email is required"))
		return
	}
	var enabled, signup bool
	err = s.Deps.DB.SQL.QueryRowContext(c.Request.Context(), `SELECT COALESCE((settings->>'magic_login_enabled')::bool,true),registration_enabled FROM instances WHERE singleton AND deleted_at IS NULL`).Scan(&enabled, &signup)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	if !enabled {
		httpapi.Fail(c, apperror.Forbidden())
		return
	}
	var userID *uuid.UUID
	err = s.Deps.DB.SQL.QueryRowContext(c.Request.Context(), `SELECT id FROM users WHERE email=$1 AND is_active AND deleted_at IS NULL`, email).Scan(&userID)
	if err != nil && err != sql.ErrNoRows {
		httpapi.Fail(c, err)
		return
	}
	id := uuid.New()
	number, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	code := fmt.Sprintf("%06d", number.Int64())
	err = s.Deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if _, err := q.ExecContext(c.Request.Context(), `UPDATE auth_challenges SET consumed_at=now(),updated_at=now() WHERE email=$1 AND purpose='magic' AND consumed_at IS NULL`, email); err != nil {
			return err
		}
		if _, err := q.ExecContext(c.Request.Context(), `INSERT INTO auth_challenges(id,user_id,email,purpose,token_hash,expires_at) VALUES($1,$2,$3,'magic',$4,$5)`, id, userID, email, hashToken(id.String()+":"+code), time.Now().Add(10*time.Minute)); err != nil {
			return err
		}
		if userID == nil && !signup {
			return nil
		}
		return s.Deps.Jobs.Publish(c.Request.Context(), q, "email.send", map[string]any{"to": email, "subject": "Your my-jira sign-in code", "text": "Your sign-in code is " + code + ". It expires in 10 minutes."}, "auth-magic:"+id.String())
	})
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.JSON(c, 200, gin.H{"challenge_id": id})
}
func (s *Server) magicVerify(c *gin.Context) {
	input, err := httpapi.Bind[struct {
		ChallengeID uuid.UUID `json:"challenge_id"`
		Code        string    `json:"code"`
		DisplayName string    `json:"display_name"`
	}](c)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	if input.ChallengeID == uuid.Nil || len(input.Code) != 6 {
		httpapi.Fail(c, apperror.Invalid("A valid challenge and six-digit code are required"))
		return
	}
	var result authResult
	var validationErr error
	err = s.Deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		var tokenHash, email string
		var userID *uuid.UUID
		var attempts int
		err := q.QueryRowContext(c.Request.Context(), `SELECT user_id,email,token_hash,attempts FROM auth_challenges WHERE id=$1 AND purpose='magic' AND consumed_at IS NULL AND expires_at>now() AND deleted_at IS NULL FOR UPDATE`, input.ChallengeID).Scan(&userID, &email, &tokenHash, &attempts)
		if err == sql.ErrNoRows {
			return apperror.Invalid("The sign-in code is invalid or expired")
		}
		if err != nil {
			return err
		}
		var methodEnabled bool
		if err = q.QueryRowContext(c.Request.Context(), `SELECT COALESCE((settings->>'magic_login_enabled')::bool,true) FROM instances WHERE singleton`).Scan(&methodEnabled); err != nil {
			return err
		}
		if !methodEnabled {
			return apperror.Forbidden()
		}
		if attempts >= 5 {
			return apperror.New(429, "code_locked", "Request a new sign-in code")
		}
		if _, err = q.ExecContext(c.Request.Context(), `UPDATE auth_challenges SET attempts=attempts+1,updated_at=now() WHERE id=$1`, input.ChallengeID); err != nil {
			return err
		}
		if !equalToken(tokenHash, hashToken(input.ChallengeID.String()+":"+input.Code)) {
			validationErr = apperror.Invalid("The sign-in code is incorrect")
			return nil
		}
		if userID == nil {
			var signup bool
			if err = q.QueryRowContext(c.Request.Context(), `SELECT registration_enabled FROM instances WHERE singleton`).Scan(&signup); err != nil {
				return err
			}
			if !signup {
				return apperror.Forbidden()
			}
			name := strings.TrimSpace(input.DisplayName)
			if name == "" {
				name = strings.Split(email, "@")[0]
			}
			if !validName(name) {
				return apperror.Invalid("Invalid display name")
			}
			password, err := randomToken()
			if err != nil {
				return err
			}
			hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
			if err != nil {
				return err
			}
			id := uuid.New()
			if _, err = q.ExecContext(c.Request.Context(), `INSERT INTO users(id,email,password_hash,password_set,display_name,email_verified) VALUES($1,$2,$3,false,$4,true)`, id, email, string(hash), name); err != nil {
				return err
			}
			userID = &id
		} else {
			var active bool
			if err = q.QueryRowContext(c.Request.Context(), `SELECT is_active AND deleted_at IS NULL FROM users WHERE id=$1`, userID).Scan(&active); err != nil {
				return err
			}
			if !active {
				return apperror.Forbidden()
			}
			if _, err = q.ExecContext(c.Request.Context(), `UPDATE users SET email_verified=true,updated_at=now() WHERE id=$1`, userID); err != nil {
				return err
			}
		}
		if _, err = q.ExecContext(c.Request.Context(), `UPDATE auth_challenges SET consumed_at=now(),updated_at=now() WHERE id=$1`, input.ChallengeID); err != nil {
			return err
		}
		result, err = s.newSession(c.Request.Context(), q, *userID, c.Request.UserAgent(), c.ClientIP())
		return err
	})
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	if validationErr != nil {
		httpapi.Fail(c, validationErr)
		return
	}
	s.finishAuth(c, 200, result)
}
func (s *Server) requestEmailVerification(c *gin.Context) {
	if !s.emailReady(c) {
		return
	}
	a, _ := httpapi.Actor(c)
	var email string
	if err := s.Deps.DB.SQL.QueryRowContext(c.Request.Context(), `SELECT email FROM users WHERE id=$1`, a.UserID).Scan(&email); err != nil {
		httpapi.Fail(c, err)
		return
	}
	if err := s.queueToken(c, a.UserID, email, "verify_email", "Verify your email address", "/verify-email"); err != nil {
		httpapi.Fail(c, err)
		return
	}
	s.emailNotice(c)
}
func (s *Server) requestEmailChange(c *gin.Context) {
	if !s.emailReady(c) {
		return
	}
	a, _ := httpapi.Actor(c)
	input, err := httpapi.Bind[struct {
		Email string `json:"email"`
	}](c)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	email := strings.ToLower(strings.TrimSpace(input.Email))
	if !validEmail(email) {
		httpapi.Fail(c, apperror.Invalid("A valid email is required"))
		return
	}
	var exists bool
	if err = s.Deps.DB.SQL.QueryRowContext(c.Request.Context(), `SELECT EXISTS(SELECT 1 FROM users WHERE email=$1 AND deleted_at IS NULL)`, email).Scan(&exists); err != nil {
		httpapi.Fail(c, err)
		return
	}
	if exists {
		httpapi.Fail(c, apperror.Conflict("This email address is already in use"))
		return
	}
	if err = s.queueToken(c, a.UserID, email, "change_email", "Confirm your new email address", "/verify-email?change=1"); err != nil {
		httpapi.Fail(c, err)
		return
	}
	s.emailNotice(c)
}
func (s *Server) confirmEmailVerification(c *gin.Context) { s.confirmEmail(c, "verify_email") }
func (s *Server) confirmEmailChange(c *gin.Context)       { s.confirmEmail(c, "change_email") }
func (s *Server) confirmEmail(c *gin.Context, purpose string) {
	a, _ := httpapi.Actor(c)
	input, err := httpapi.Bind[struct {
		Token string `json:"token"`
	}](c)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	if len(input.Token) != 43 {
		httpapi.Fail(c, apperror.Invalid("Invalid verification token"))
		return
	}
	err = s.Deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		var id uuid.UUID
		var email string
		err := q.QueryRowContext(c.Request.Context(), `SELECT id,email FROM auth_challenges WHERE user_id=$1 AND token_hash=$2 AND purpose=$3 AND expires_at>now() AND consumed_at IS NULL AND deleted_at IS NULL FOR UPDATE`, a.UserID, hashToken(input.Token), purpose).Scan(&id, &email)
		if err == sql.ErrNoRows {
			return apperror.Invalid("The verification link is invalid or expired")
		}
		if err != nil {
			return err
		}
		if purpose == "change_email" {
			if _, err = q.ExecContext(c.Request.Context(), `UPDATE users SET email=$2,email_verified=true,updated_at=now() WHERE id=$1`, a.UserID, email); err != nil {
				return err
			}
			if _, err = q.ExecContext(c.Request.Context(), `UPDATE sessions SET revoked_at=now(),updated_at=now() WHERE user_id=$1 AND id<>$2 AND revoked_at IS NULL`, a.UserID, a.SessionID); err != nil {
				return err
			}
		} else {
			if err = affected(q.ExecContext(c.Request.Context(), `UPDATE users SET email_verified=true,updated_at=now() WHERE id=$1 AND email=$2`, a.UserID, email)); err != nil {
				return err
			}
		}
		_, err = q.ExecContext(c.Request.Context(), `UPDATE auth_challenges SET consumed_at=now(),updated_at=now() WHERE id=$1`, id)
		return err
	})
	noContent(c, err)
}

func (s *Server) accounts(c *gin.Context) {
	a, _ := httpapi.Actor(c)
	value, err := queryList(c.Request.Context(), s.Deps.DB.SQL, `SELECT jsonb_build_object('id',id,'provider',provider,'email',email,'created_at',created_at) FROM oauth_accounts WHERE user_id=$1 AND deleted_at IS NULL ORDER BY created_at`, a.UserID)
	reply(c, 200, value, err)
}
func (s *Server) unlinkAccount(c *gin.Context) {
	a, _ := httpapi.Actor(c)
	id, err := httpapi.UUIDParam(c, "accountID")
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	err = s.Deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		var passwordSet bool
		if err := q.QueryRowContext(c.Request.Context(), `SELECT password_set FROM users WHERE id=$1 FOR UPDATE`, a.UserID).Scan(&passwordSet); err != nil {
			return err
		}
		var count int
		if err := q.QueryRowContext(c.Request.Context(), `SELECT count(*) FROM oauth_accounts WHERE user_id=$1 AND deleted_at IS NULL`, a.UserID).Scan(&count); err != nil {
			return err
		}
		if !passwordSet && count <= 1 {
			return apperror.Conflict("Set a password before unlinking your last provider")
		}
		return affected(q.ExecContext(c.Request.Context(), `UPDATE oauth_accounts SET deleted_at=now(),updated_at=now() WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL`, id, a.UserID))
	})
	noContent(c, err)
}
