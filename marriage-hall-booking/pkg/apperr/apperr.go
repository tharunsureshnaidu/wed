// Package apperr is the Go equivalent of the Java BaseAppException: an error
// that carries the HTTP status and machine-readable errorCode the handler
// layer turns into an ApiResponse.
package apperr

import "net/http"

type Error struct {
	Message string
	Code    string
	Status  int
}

func (e *Error) Error() string { return e.Message }

func New(status int, code, message string) *Error {
	return &Error{Message: message, Code: code, Status: status}
}

func BadRequest(code, msg string) *Error   { return New(http.StatusBadRequest, code, msg) }
func Unauthorized(code, msg string) *Error { return New(http.StatusUnauthorized, code, msg) }
func Forbidden(code, msg string) *Error    { return New(http.StatusForbidden, code, msg) }
func Conflict(code, msg string) *Error     { return New(http.StatusConflict, code, msg) }
func TooMany(code, msg string) *Error      { return New(http.StatusTooManyRequests, code, msg) }
func Internal(code, msg string) *Error     { return New(http.StatusInternalServerError, code, msg) }
