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
func Migrate(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS, dir string) error {
	_, err := pool.Exec(ctx, `
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
