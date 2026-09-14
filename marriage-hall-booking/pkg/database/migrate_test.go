package database

import (
	"context"
	"os"
	"testing"
	"testing/fstest"
)

// Runs against the real local Postgres; skipped if TEST_DATABASE_URL is unset.
func TestMigrateAppliesOnceAndOrders(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run")
	}
	ctx := context.Background()
	pool, err := New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	pool.Exec(ctx, `DROP TABLE IF EXISTS mt_demo; DELETE FROM schema_migrations WHERE filename LIKE 'zz_%'`)
	defer pool.Exec(ctx, `DROP TABLE IF EXISTS mt_demo; DELETE FROM schema_migrations WHERE filename LIKE 'zz_%'`)

	// 002 depends on 001 having run first, so it only succeeds in filename order.
	fsys := fstest.MapFS{
		"zz_001.sql": {Data: []byte(`CREATE TABLE mt_demo (id INT)`)},
		"zz_002.sql": {Data: []byte(`ALTER TABLE mt_demo ADD COLUMN name TEXT`)},
	}

	if err := Migrate(ctx, pool, fsys, "."); err != nil {
		t.Fatalf("first run: %v", err)
	}
	// Second run must be a no-op; re-running zz_001 would error on duplicate table.
	if err := Migrate(ctx, pool, fsys, "."); err != nil {
		t.Fatalf("second run should be a no-op: %v", err)
	}

	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM schema_migrations WHERE filename LIKE 'zz_%'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("want 2 recorded migrations, got %d", n)
	}
}

// A failing migration must not be recorded, so it retries on the next boot.
func TestMigrateFailureIsNotRecorded(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run")
	}
	ctx := context.Background()
	pool, err := New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	defer pool.Exec(ctx, `DELETE FROM schema_migrations WHERE filename LIKE 'zz_%'`)

	fsys := fstest.MapFS{"zz_bad.sql": {Data: []byte(`THIS IS NOT SQL`)}}
	if err := Migrate(ctx, pool, fsys, "."); err == nil {
		t.Fatal("want error from invalid SQL, got nil")
	}

	var n int
	pool.QueryRow(ctx,
		`SELECT count(*) FROM schema_migrations WHERE filename = 'zz_bad.sql'`).Scan(&n)
	if n != 0 {
		t.Fatalf("failed migration must not be recorded, got %d rows", n)
	}
}
