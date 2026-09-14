package health

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tripfcatory/marriage-hall-booking/pkg/response"
)

type Handler struct{ db *pgxpool.Pool }

func NewHandler(db *pgxpool.Pool) *Handler { return &Handler{db: db} }

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /health", h.health)
}

func (h *Handler) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	if err := h.db.Ping(ctx); err != nil {
		response.Error(w, http.StatusServiceUnavailable, "Database unreachable", "DB_DOWN")
		return
	}
	response.OK(w, "Service is healthy", map[string]string{
		"status":   "UP",
		"database": "UP",
	})
}
