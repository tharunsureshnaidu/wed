package health

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/response"
)

type Handler struct {
	db       *pgxpool.Pool
	rdb      *redis.Client
	draining atomic.Bool
}

func NewHandler(db *pgxpool.Pool, rdb *redis.Client) *Handler { return &Handler{db: db, rdb: rdb} }

// Register serves three probes. /health is the original and is kept for
// whatever already polls it. /livez answers "is the process alive" with no
// dependencies, so a database outage does not get every replica restarted.
// /readyz answers "should traffic come here": the database is required, Redis
// is reported but only degrades (the rate limiter fails open without it), and
// it goes 503 once shutdown has begun so a load balancer stops sending.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /health", h.health)
	mux.HandleFunc("GET /livez", h.livez)
	mux.HandleFunc("GET /readyz", h.readyz)
}

// Drain marks the process as shutting down; /readyz reports 503 from then on.
func (h *Handler) Drain() { h.draining.Store(true) }

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

func (h *Handler) livez(w http.ResponseWriter, _ *http.Request) {
	response.OK(w, "Alive", map[string]string{"status": "UP"})
}

func (h *Handler) readyz(w http.ResponseWriter, r *http.Request) {
	if h.draining.Load() {
		response.Error(w, http.StatusServiceUnavailable, "Shutting down", "DRAINING")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := h.db.Ping(ctx); err != nil {
		response.Error(w, http.StatusServiceUnavailable, "Database unreachable", "DB_DOWN")
		return
	}
	redisStatus := "UP"
	if h.rdb == nil || h.rdb.Ping(ctx).Err() != nil {
		redisStatus = "DEGRADED"
	}
	response.OK(w, "Ready", map[string]string{
		"status":   "UP",
		"database": "UP",
		"redis":    redisStatus,
	})
}
