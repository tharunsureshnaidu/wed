package response

import (
	"encoding/json"
	"net/http"
	"time"
)

// Envelope mirrors the Java service's ApiResponse<T> field-for-field, so existing
// API clients and the Postman collection keep working against the Go service.
type Envelope struct {
	Success   bool   `json:"success"`
	Message   string `json:"message"`
	Data      any    `json:"data"`
	ErrorCode string `json:"errorCode,omitempty"`
	Timestamp string `json:"timestamp"`
}

func OK(w http.ResponseWriter, message string, data any) {
	write(w, http.StatusOK, Envelope{
		Success: true, Message: message, Data: data, Timestamp: now(),
	})
}

func Error(w http.ResponseWriter, status int, message, errorCode string) {
	write(w, status, Envelope{
		Success: false, Message: message, ErrorCode: errorCode, Timestamp: now(),
	})
}

func write(w http.ResponseWriter, status int, body Envelope) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

// Java serialises LocalDateTime without a zone offset; match that format.
func now() string { return time.Now().Format("2006-01-02T15:04:05.000000000") }
