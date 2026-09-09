package foundation

import (
	"math"
	"net/mail"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	miniocredentials "github.com/minio/minio-go/v7/pkg/credentials"
	"my-jira/apps/api/internal/platform/apperror"
	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/httpapi"
	"my-jira/apps/api/internal/platform/serviceconfig"
)

func secretField(key string) bool {
	return key == "password" || key == "client_secret" || key == "secret_key" || key == "access_key" || key == "api_key"
}
func publicService(service string, values serviceconfig.Values) map[string]any {
	result := map[string]any{}
	for key, value := range values {
		if secretField(key) {
			result[key+"_configured"] = values.String(key) != ""
		} else {
			result[key] = value
		}
	}
	switch service {
	case "email":
		result["configured"] = values.String("host") != ""
	case "storage":
		result["configured"] = values.String("endpoint") != "" && values.String("bucket") != "" && values.String("access_key") != "" && values.String("secret_key") != ""
	case "ai":
		result["configured"] = values.String("api_key") != "" && values.String("base_url") != ""
	case "unsplash":
		result["configured"] = values.String("access_key") != ""
	default:
		_, err := providerFromValues(service, values)
		result["configured"] = err == nil
	}
	return result
}
func (s *Server) adminServices(c *gin.Context) {
	if !s.admin(c) {
		return
	}
	result := map[string]any{"secret_storage_enabled": serviceconfig.SecretStorageEnabled()}
	oauth := map[string]any{}
	for _, service := range []string{"email", "storage", "google", "github", "gitlab", "gitea", "ai", "unsplash"} {
		values, err := serviceconfig.Load(c.Request.Context(), s.Deps.DB.SQL, service)
		var info map[string]any
		if err != nil {
			info = map[string]any{"configured": false, "error": "Saved configuration cannot be read; check the instance encryption key"}
		} else {
			info = publicService(service, values)
		}
		switch service {
		case "google", "github", "gitlab", "gitea":
			oauth[service] = info
		default:
			result[service] = info
		}
	}
	result["oauth"] = oauth
	httpapi.JSON(c, 200, result)
}
func (s *Server) saveService(c *gin.Context) {
	if !s.admin(c) {
		return
	}
	service := c.Param("service")
	if !serviceconfig.Known(service) {
		httpapi.Fail(c, apperror.NotFound())
		return
	}
	if !serviceconfig.SecretStorageEnabled() {
		httpapi.Fail(c, apperror.New(503, "configuration_storage_unavailable", "Configure the instance encryption key before saving service credentials"))
		return
	}
	input, err := httpapi.Bind[map[string]any](c)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	if len(input) == 0 {
		httpapi.Fail(c, apperror.Invalid("No changes were provided"))
		return
	}
	changes := serviceconfig.Values{}
	defaults := serviceconfig.Defaults(service)
	for key, value := range input {
		if _, known := defaults[key]; !known {
			httpapi.Fail(c, apperror.Invalid("Unsupported service field: "+key))
			return
		}
		if value == nil {
			if !secretField(key) {
				httpapi.Fail(c, apperror.Invalid("Only a credential may be explicitly cleared"))
				return
			}
			changes[key] = nil
			continue
		}
		if key == "secure" {
			if _, ok := value.(bool); !ok {
				httpapi.Fail(c, apperror.Invalid("secure must be true or false"))
				return
			}
			changes[key] = value
			continue
		}
		if key == "port" {
			if number, ok := value.(float64); ok {
				if math.Trunc(number) != number || number < 1 || number > 65535 {
					httpapi.Fail(c, apperror.Invalid("Port must be a whole number between 1 and 65535"))
					return
				}
				value = strconv.Itoa(int(number))
			}
		}
		text, ok := value.(string)
		if !ok || len(text) > 16384 || strings.ContainsAny(text, "\r\n") {
			httpapi.Fail(c, apperror.Invalid("Invalid "+key))
			return
		}
		if !secretField(key) {
			text = strings.TrimSpace(text)
		}
		if key == "port" {
			port, e := strconv.Atoi(text)
			if e != nil || port < 1 || port > 65535 {
				httpapi.Fail(c, apperror.Invalid("Port must be between 1 and 65535"))
				return
			}
		}
		if strings.HasSuffix(key, "_url") && text != "" {
			endpoint, e := url.Parse(text)
			if e != nil || endpoint.Host == "" || (endpoint.Scheme != "https" && endpoint.Scheme != "http") || endpoint.User != nil {
				httpapi.Fail(c, apperror.Invalid("Provide an HTTP or HTTPS URL without embedded credentials"))
				return
			}
		}
		if key == "from" {
			if _, e := mail.ParseAddress(text); e != nil {
				httpapi.Fail(c, apperror.Invalid("Invalid sender address"))
				return
			}
		}
		if key == "endpoint" && (strings.Contains(text, "/") || strings.TrimSpace(text) == "") {
			httpapi.Fail(c, apperror.Invalid("Storage endpoint must be a hostname with an optional port"))
			return
		}
		if service == "ai" && ((key == "model" && text != "gpt-5.6-terra") || (key == "wire_api" && text != "responses")) {
			httpapi.Fail(c, apperror.Invalid("This instance uses gpt-5.6-terra with the Responses API"))
			return
		}
		changes[key] = text
	}
	var values serviceconfig.Values
	err = s.Deps.DB.WithinTx(c.Request.Context(), func(q database.DBTX) error {
		if _, err := q.ExecContext(c.Request.Context(), `SELECT id FROM instances WHERE singleton FOR UPDATE`); err != nil {
			return err
		}
		actor, _ := httpapi.Actor(c)
		var stillAdmin bool
		if err := q.QueryRowContext(c.Request.Context(), `SELECT is_instance_admin AND is_active AND deleted_at IS NULL FROM users WHERE id=$1`, actor.UserID).Scan(&stillAdmin); err != nil {
			return err
		}
		if !stillAdmin {
			return apperror.Forbidden()
		}
		var err error
		values, err = serviceconfig.Load(c.Request.Context(), q, service)
		if err != nil {
			return apperror.New(503, "configuration_storage_unavailable", "The saved configuration could not be read")
		}
		for key, value := range changes {
			values[key] = value
		}
		return serviceconfig.Save(c.Request.Context(), q, service, values)
	})
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	values["source"] = "instance"
	httpapi.JSON(c, 200, publicService(service, values))
}
func (s *Server) testEmail(c *gin.Context) {
	if !s.admin(c) {
		return
	}
	if err := s.rateLimit(c, 5); err != nil {
		httpapi.Fail(c, err)
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
		httpapi.Fail(c, apperror.Invalid("A valid recipient email is required"))
		return
	}
	values, err := serviceconfig.Load(c.Request.Context(), s.Deps.DB.SQL, "email")
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	if err = sendEmailWithConfig(c.Request.Context(), values, email, "my-jira email configuration test", "Your my-jira instance successfully connected to this SMTP server.", uuid.NewString()); err != nil {
		httpapi.Fail(c, apperror.New(502, "email_test_failed", "The SMTP server did not accept the test message; check the email configuration"))
		return
	}
	httpapi.JSON(c, 200, gin.H{"accepted": true, "message": "The SMTP server accepted the test message"})
}
func (s *Server) testStorage(c *gin.Context) {
	if !s.admin(c) {
		return
	}
	values, err := serviceconfig.Load(c.Request.Context(), s.Deps.DB.SQL, "storage")
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	client, err := minio.New(values.String("endpoint"), &minio.Options{Creds: miniocredentials.NewStaticV4(values.String("access_key"), values.String("secret_key"), ""), Secure: values.Bool("secure"), Region: values.String("region")})
	if err != nil {
		httpapi.Fail(c, apperror.Invalid("Storage endpoint is invalid"))
		return
	}
	exists, err := client.BucketExists(c.Request.Context(), values.String("bucket"))
	if err != nil {
		httpapi.Fail(c, apperror.New(502, "storage_test_failed", "The storage connection check failed"))
		return
	}
	httpapi.JSON(c, 200, gin.H{"connected": true, "bucket_exists": exists})
}
