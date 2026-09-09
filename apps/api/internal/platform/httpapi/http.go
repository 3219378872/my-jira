package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"my-jira/apps/api/internal/platform/apperror"
	"my-jira/apps/api/internal/platform/identity"
)

type Error = apperror.Error

func NewError(status int, code, message string) *Error { return apperror.New(status, code, message) }

const ActorKey = "my-jira.actor"

func Actor(c *gin.Context) (identity.Actor, error) {
	value, ok := c.Get(ActorKey)
	if !ok {
		return identity.Actor{}, apperror.Unauthorized()
	}
	actor, ok := value.(identity.Actor)
	if !ok || actor.UserID == uuid.Nil {
		return actor, apperror.Unauthorized()
	}
	return actor, nil
}
func UUIDParam(c *gin.Context, name string) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Param(name))
	if err != nil || id == uuid.Nil {
		return uuid.Nil, apperror.Invalid("Invalid " + name)
	}
	return id, nil
}
func JSON(c *gin.Context, status int, value any) { c.JSON(status, gin.H{"data": value}) }
func Fail(c *gin.Context, err error) {
	var apiErr *apperror.Error
	if !errors.As(err, &apiErr) {
		if errors.Is(err, sql.ErrNoRows) {
			apiErr = apperror.NotFound()
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch pgErr.Code {
			case "23505":
				apiErr = apperror.Conflict("A resource with these values already exists")
			case "23503":
				apiErr = apperror.Invalid("A related resource does not exist or is still in use")
			case "23514", "22P02", "22001", "22003":
				apiErr = apperror.Invalid("The supplied values are invalid")
			case "40001", "40P01":
				apiErr = apperror.Conflict("A concurrent change occurred; please retry")
			}
		}
		if apiErr == nil {
			slog.Error("request failed", "method", c.Request.Method, "path", c.FullPath(), "error", err)
			apiErr = apperror.New(500, "internal_error", "The request could not be completed")
		}
	}
	c.AbortWithStatusJSON(apiErr.Status, gin.H{"error": apiErr})
}
func Bind[T any](c *gin.Context) (T, error) {
	var result T
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2<<20)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return result, apperror.Invalid("The request must contain a valid JSON object")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return result, apperror.Invalid("Only one JSON object is allowed")
	}
	return result, nil
}

type Pagination struct {
	NextCursor *string `json:"next_cursor"`
	HasMore    bool    `json:"has_more"`
	Total      int64   `json:"total"`
}
type Page[T any] struct {
	Data       []T        `json:"data"`
	Pagination Pagination `json:"pagination"`
}
