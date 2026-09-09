// Package data contains bounded SQL and JSON helpers shared by feature modules.
package data

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"my-jira/apps/api/internal/platform/database"
	"my-jira/apps/api/internal/platform/httpapi"
)

type Error struct {
	Status        int
	Code, Message string
}

func (e *Error) Error() string     { return e.Message }
func Invalid(message string) error { return &Error{400, "validation_failed", message} }
func Forbidden() error {
	return &Error{403, "forbidden", "You do not have permission to perform this action"}
}
func Conflict(message string) error { return &Error{409, "conflict", message} }
func Missing() error                { return &Error{404, "not_found", "The requested resource was not found"} }

func Fail(c *gin.Context, err error) {
	var problem *Error
	if errors.As(err, &problem) {
		c.AbortWithStatusJSON(problem.Status, gin.H{"error": gin.H{"code": problem.Code, "message": problem.Message}})
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		Fail(c, Missing())
		return
	}
	httpapi.Fail(c, err)
}

type Object map[string]json.RawMessage

func Bind(c *gin.Context, allowed ...string) (Object, error) {
	var input Object
	input, err := httpapi.Bind[Object](c)
	if err != nil || input == nil {
		return nil, Invalid("A JSON object is required")
	}
	set := make(map[string]bool, len(allowed))
	for _, key := range allowed {
		set[key] = true
	}
	for key := range input {
		if !set[key] {
			return nil, Invalid("Unknown field: " + key)
		}
	}
	return input, nil
}

func (o Object) String(key string, required bool, maximum int) (string, error) {
	var value string
	raw, ok := o[key]
	if !ok {
		if required {
			return "", Invalid(key + " is required")
		}
		return "", nil
	}
	if string(raw) == "null" || json.Unmarshal(raw, &value) != nil {
		return "", Invalid(key + " must be a string")
	}
	value = strings.TrimSpace(value)
	if required && value == "" {
		return "", Invalid(key + " cannot be empty")
	}
	if maximum > 0 && len([]rune(value)) > maximum {
		return "", Invalid(fmt.Sprintf("%s must have at most %d characters", key, maximum))
	}
	return value, nil
}

func (o Object) Bool(key string) (bool, error) {
	var value bool
	raw, ok := o[key]
	if !ok {
		return false, nil
	}
	if string(raw) == "null" || json.Unmarshal(raw, &value) != nil {
		return false, Invalid(key + " must be a boolean")
	}
	return value, nil
}

func (o Object) Number(key string) (float64, error) {
	var value float64
	raw, ok := o[key]
	if !ok {
		return 0, nil
	}
	if string(raw) == "null" || json.Unmarshal(raw, &value) != nil || math.IsInf(value, 0) || math.IsNaN(value) {
		return 0, Invalid(key + " must be a finite number")
	}
	return value, nil
}

func (o Object) UUID(key string, nullable bool) (any, error) {
	raw, ok := o[key]
	if !ok || string(raw) == "null" {
		if nullable {
			return nil, nil
		}
		return nil, Invalid(key + " is required")
	}
	value, err := o.String(key, true, 100)
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(value)
	if err != nil || id == uuid.Nil {
		return nil, Invalid(key + " must be a UUID")
	}
	return id, nil
}

func (o Object) UUIDs(key string) ([]uuid.UUID, error) {
	raw, ok := o[key]
	if !ok {
		return nil, nil
	}
	var values []string
	if string(raw) == "null" || json.Unmarshal(raw, &values) != nil {
		return nil, Invalid(key + " must be a UUID array")
	}
	if len(values) > 500 {
		return nil, Invalid(key + " supports at most 500 entries")
	}
	result := make([]uuid.UUID, 0, len(values))
	seen := map[uuid.UUID]bool{}
	for _, value := range values {
		id, err := uuid.Parse(value)
		if err != nil || id == uuid.Nil {
			return nil, Invalid(key + " contains an invalid UUID")
		}
		if !seen[id] {
			seen[id] = true
			result = append(result, id)
		}
	}
	return result, nil
}

func (o Object) Date(key string) (any, error) {
	raw, ok := o[key]
	if !ok || string(raw) == "null" {
		return nil, nil
	}
	value, err := o.String(key, false, 10)
	if err != nil {
		return nil, err
	}
	if value == "" {
		return nil, nil
	}
	date, err := time.Parse("2006-01-02", value)
	if err != nil {
		return nil, Invalid(key + " must use YYYY-MM-DD")
	}
	return date.Format("2006-01-02"), nil
}

func (o Object) JSON(key string) (any, error) {
	raw, ok := o[key]
	if !ok {
		return "{}", nil
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return nil, Invalid(key + " must contain JSON")
	}
	if _, ok := value.(map[string]any); !ok {
		return nil, Invalid(key + " must be a JSON object")
	}
	return string(raw), nil
}

func One(c *gin.Context, q database.DBTX, query string, args ...any) (json.RawMessage, error) {
	var raw []byte
	err := q.QueryRowContext(c.Request.Context(), query, args...).Scan(&raw)
	return json.RawMessage(raw), err
}

func Many(c *gin.Context, q database.DBTX, query string, args ...any) ([]json.RawMessage, error) {
	rows, err := q.QueryContext(c.Request.Context(), query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]json.RawMessage, 0)
	for rows.Next() {
		var value []byte
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		result = append(result, json.RawMessage(value))
	}
	return result, rows.Err()
}

func Send(c *gin.Context, value any, err error) {
	if err != nil {
		Fail(c, err)
		return
	}
	httpapi.JSON(c, http.StatusOK, value)
}

func Markers(start, count int) string {
	values := make([]string, count)
	for i := range values {
		values[i] = fmt.Sprintf("$%d", start+i)
	}
	return strings.Join(values, ",")
}
