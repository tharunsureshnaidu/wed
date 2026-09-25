package middleware

import (
	"net/http"
	"time"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/logger"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/response"
)

// Recover keeps one panicking request from taking down the whole process.
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				logger.Error("panic recovered", "method", r.Method, "path", r.URL.Path, "panic", v)
				response.Error(w, http.StatusInternalServerError, "Something went wrong", "INTERNAL_ERROR")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func RequestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		// One line per request at INFO. This is the log people actually want to
		// see; 4xx/5xx are promoted so a failure still stands out by level.
		took := time.Since(start)
		log := logger.Info
		msg := "request"
		switch {
		case sw.status >= 500:
			log, msg = logger.Error, "request failed"
		case sw.status >= 400:
			log, msg = logger.Warn, "request rejected"
		}
		log(msg, "id", RequestIDOf(r.Context()), "method", r.Method,
			"path", r.URL.Path, "status", sw.status, logger.Dur(took))
	})
}
