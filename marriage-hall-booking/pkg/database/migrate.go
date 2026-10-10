package database

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Migrate applies every .sql file in dir, in filename order, exactly once.
// Applied filenames are recorded in schema_migrations.
//
// ponytail: replaces Flyway. Forward-only, no checksums, no down migrations -
// reach for golang-migrate if rollback or drift detection is ever needed.
func Migrate(ctx context.Context, db *pgxpool.Pool, fsys fs.FS, dir string) error {
	// One connection for the whole run, holding an advisory lock: replicas
	// booting together otherwise race on the same DDL, and the loser exits
	// fatally into a restart loop. The pool's statement/lock timeouts are
	// lifted on this connection - a migration may take longer than a request,
	// and waiting out another replica's run is the point of the lock.
	pool, err := db.Acquire(ctx)
	if err != nil {
		return err
	}
	defer pool.Release()
	if _, err := pool.Exec(ctx, `SET statement_timeout = 0; SET lock_timeout = 0`); err != nil {
		return err
	}
	// RESET ALL restores the pool's connection defaults before it is reused.
	defer pool.Exec(context.Background(), `SELECT pg_advisory_unlock_all(); RESET ALL`)
	if _, err := pool.Exec(ctx, `SELECT pg_advisory_lock(7262001)`); err != nil {
		return fmt.Errorf("migration lock: %w", err)
	}

	_, err = pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			filename    VARCHAR(255) PRIMARY KEY,
			applied_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`)
	if err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	rows, err := pool.Query(ctx, `SELECT filename FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("read schema_migrations: %w", err)
	}
	applied := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		applied[name] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	entries, err := fs.Glob(fsys, dir+"/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(entries)

	for _, path := range entries {
		name := filepath.Base(path)
		if applied[name] {
			continue
		}
		body, err := fs.ReadFile(fsys, path)
		if err != nil {
			return err
		}

		// Each migration runs in its own transaction, so a failure half-way
		// leaves no partially-applied file behind.
		tx, err := pool.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(body)); err != nil {
			tx.Rollback(ctx)
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO schema_migrations (filename) VALUES ($1)`, name); err != nil {
			tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		fmt.Printf("migrated: %s\n", name)
	}
	return nil
}
