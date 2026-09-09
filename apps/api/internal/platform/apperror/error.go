package apperror

import "fmt"

type Error struct {
	Status  int               `json:"-"`
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }
func New(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}
func Invalid(message string) *Error { return New(400, "validation_failed", message) }
func Unauthorized() *Error          { return New(401, "unauthenticated", "Sign in to continue") }
func Forbidden() *Error {
	return New(403, "forbidden", "You do not have permission to perform this action")
}
func NotFound() *Error               { return New(404, "not_found", "The requested resource was not found") }
func Conflict(message string) *Error { return New(409, "conflict", message) }
