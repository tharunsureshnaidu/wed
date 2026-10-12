package database

import (
	"context"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func New(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	// 8 matches the Java service's Hikari maximum-pool-size; DB_MAX_CONNS
	// raises it for a production host without a rebuild.
	cfg.MaxConns = 8
	if n, err := strconv.Atoi(os.Getenv("DB_MAX_CONNS")); err == nil && n > 0 {
		cfg.MaxConns = int32(n)
	}
	cfg.MaxConnIdleTime = 5 * time.Minute
	// Server-side limits, because WriteTimeout does not cancel a handler's
	// query: without them one stuck lock wait or slow search holds a
	// connection forever, and eight of those stall the whole API.
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "10s"
	cfg.ConnConfig.RuntimeParams["lock_timeout"] = "5s"
	cfg.ConnConfig.RuntimeParams["idle_in_transaction_session_timeout"] = "30s"

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}
