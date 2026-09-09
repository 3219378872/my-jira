// Package serviceconfig loads instance-level service configuration without
// exposing credentials through the public API. Persisted values use AES-GCM.
package serviceconfig

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"my-jira/apps/api/internal/platform/database"
)

type Values map[string]any

func (v Values) String(key string) string { value, _ := v[key].(string); return value }
func (v Values) Bool(key string) bool     { value, _ := v[key].(bool); return value }
func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
func Known(service string) bool {
	switch service {
	case "email", "storage", "google", "github", "gitlab", "gitea", "ai", "unsplash":
		return true
	}
	return false
}
func Defaults(service string) Values {
	switch service {
	case "email":
		return Values{"host": os.Getenv("SMTP_HOST"), "port": env("SMTP_PORT", "587"), "from": env("SMTP_FROM", "my-jira <noreply@myjira.local>"), "user": os.Getenv("SMTP_USER"), "password": os.Getenv("SMTP_PASSWORD"), "secure": os.Getenv("SMTP_SECURE") == "true"}
	case "storage":
		return Values{"endpoint": os.Getenv("S3_ENDPOINT"), "public_endpoint": os.Getenv("S3_PUBLIC_ENDPOINT"), "bucket": os.Getenv("S3_BUCKET"), "access_key": os.Getenv("S3_ACCESS_KEY"), "secret_key": os.Getenv("S3_SECRET_KEY"), "secure": os.Getenv("S3_USE_SSL") == "true", "region": os.Getenv("S3_REGION")}
	case "ai":
		return Values{"provider": "openai", "base_url": env("AI_BASE_URL", "https://api.weblearning.fun/v1"), "wire_api": "responses", "model": "gpt-5.6-terra", "api_key": env("MINE_API_KEY", os.Getenv("AI_API_KEY"))}
	case "unsplash":
		return Values{"access_key": os.Getenv("UNSPLASH_ACCESS_KEY")}
	default:
		prefix := "OAUTH_" + strings.ToUpper(service) + "_"
		return Values{"client_id": os.Getenv(prefix + "CLIENT_ID"), "client_secret": os.Getenv(prefix + "CLIENT_SECRET"), "base_url": os.Getenv(prefix + "BASE_URL"), "authorize_url": os.Getenv(prefix + "AUTHORIZE_URL"), "token_url": os.Getenv(prefix + "TOKEN_URL"), "userinfo_url": os.Getenv(prefix + "USERINFO_URL"), "emails_url": os.Getenv(prefix + "EMAILS_URL")}
	}
}
func cipherForSettings() (cipher.AEAD, error) {
	key, err := base64.StdEncoding.DecodeString(os.Getenv("APP_ENCRYPTION_KEY"))
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("APP_ENCRYPTION_KEY must contain a base64-encoded 32-byte key")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
func SecretStorageEnabled() bool { _, err := cipherForSettings(); return err == nil }

func Load(ctx context.Context, q database.DBTX, service string) (Values, error) {
	if !Known(service) {
		return nil, fmt.Errorf("unknown service configuration")
	}
	values := Defaults(service)
	values["source"] = "environment"
	var encoded sql.NullString
	err := q.QueryRowContext(ctx, `SELECT settings->'_services'->>$1 FROM instances WHERE singleton AND deleted_at IS NULL`, service).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return values, nil
	}
	if err != nil {
		return nil, err
	}
	if !encoded.Valid || encoded.String == "" {
		return values, nil
	}
	aead, err := cipherForSettings()
	if err != nil {
		return nil, err
	}
	raw, err := base64.RawStdEncoding.DecodeString(encoded.String)
	if err != nil || len(raw) < aead.NonceSize() {
		return nil, fmt.Errorf("stored service configuration is invalid")
	}
	plaintext, err := aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], []byte(service))
	if err != nil {
		return nil, fmt.Errorf("stored service configuration could not be decrypted")
	}
	var stored Values
	if err = json.Unmarshal(plaintext, &stored); err != nil {
		return nil, fmt.Errorf("stored service configuration is invalid")
	}
	for key, value := range stored {
		values[key] = value
	}
	values["source"] = "instance"
	return values, nil
}

// Save receives a complete validated map. Callers preserve omitted fields by
// first loading the existing map. A null value explicitly clears an override.
func Save(ctx context.Context, q database.DBTX, service string, values Values) error {
	if !Known(service) {
		return fmt.Errorf("unknown service configuration")
	}
	aead, err := cipherForSettings()
	if err != nil {
		return err
	}
	copy := Values{}
	for key, value := range values {
		if key != "source" {
			copy[key] = value
		}
	}
	plaintext, err := json.Marshal(copy)
	if err != nil {
		return err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return err
	}
	sealed := aead.Seal(nonce, nonce, plaintext, []byte(service))
	encoded := base64.RawStdEncoding.EncodeToString(sealed)
	result, err := q.ExecContext(ctx, `UPDATE instances SET settings=jsonb_set(settings,'{_services}',COALESCE(settings->'_services','{}'::jsonb)||jsonb_build_object($1::text,$2::text),true),updated_at=now() WHERE singleton AND deleted_at IS NULL`, service, encoded)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("instance is not configured")
	}
	return nil
}
