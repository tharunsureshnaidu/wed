package response

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/logger"
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

// Created is 201 for a request that brought a new resource into existence.
//
// The envelope is identical to OK's, so a client reading body.data is
// unaffected; only the status line changes. location, when non-empty, is sent
// as the Location header - the canonical way to tell a client where the new
// resource lives without it having to construct the URL.
//
// Use OK, not this, for an upsert: PUT /vendors/me and device registration may
// update an existing row, and a client that branches on 201 would be told a
// resource was created when nothing was.
func Created(w http.ResponseWriter, message, location string, data any) {
	if location != "" {
		w.Header().Set("Location", location)
	}
	write(w, http.StatusCreated, Envelope{
		Success: true, Message: message, Data: data, Timestamp: now(),
	})
}

func Error(w http.ResponseWriter, status int, message, errorCode string) {
	write(w, status, Envelope{
		Success: false, Message: message, ErrorCode: errorCode, Timestamp: now(),
	})
}

// write encodes before sending the status line. Encoding straight into w sent
// 200 first, so a value JSON cannot hold (a NaN distance) produced a 200 with
// an empty body and the error went nowhere.
func write(w http.ResponseWriter, status int, body Envelope) {
	buf, err := json.Marshal(body)
	if err != nil {
		logger.Error("response: encode", "status", status, logger.Err(err))
		status = http.StatusInternalServerError
		buf, _ = json.Marshal(Envelope{
			Success: false, Message: "Something went wrong",
			ErrorCode: "INTERNAL_ERROR", Timestamp: now(),
		})
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(append(buf, '\n'))
}

// Java serialises LocalDateTime without a zone offset; match that format.
func now() string { return time.Now().Format("2006-01-02T15:04:05.000000000") }
