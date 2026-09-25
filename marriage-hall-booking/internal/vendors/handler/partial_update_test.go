package handler

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/database"
)

var pool *pgxpool.Pool

func TestMain(m *testing.M) {
	if url := os.Getenv("TEST_DATABASE_URL"); url != "" {
		if p, err := database.New(context.Background(), url); err == nil {
			pool = p
			defer p.Close()
		}
	}
	os.Exit(m.Run())
}

// A vendor editing one field must not lose the others. The upsert previously
// assigned excluded.* to all 13 columns, so saving just the business name set
// address, phone and website to NULL - silent data loss on an ordinary edit.
//
// This drives the same SQL the handler runs rather than going through HTTP, so
// it fails on the query itself rather than on routing or auth.
func TestPartialUpdateKeepsUnsentFields(t *testing.T) {
	if pool == nil {
		t.Skip("set TEST_DATABASE_URL to run")
	}
	ctx := context.Background()

	var userID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (full_name, email, password_hash, status)
		 VALUES ('Partial Test', $1, 'x', 'ACTIVE') RETURNING id`,
		"partial-"+t.Name()+"@example.com").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM vendors WHERE user_id = $1`, userID)
		pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
	})

	save := func(name, addr, phone *string) {
		t.Helper()
		if _, err := pool.Exec(ctx,
			`INSERT INTO vendors (user_id, business_name, business_address, business_phone)
			 VALUES ($1,$2,$3,$4)
			 ON CONFLICT (user_id) DO UPDATE SET
			    business_name = COALESCE(excluded.business_name, vendors.business_name),
			    business_address = COALESCE(excluded.business_address, vendors.business_address),
			    business_phone = COALESCE(excluded.business_phone, vendors.business_phone)`,
			userID, name, addr, phone); err != nil {
			t.Fatal(err)
		}
	}
	str := func(s string) *string { return &s }

	save(str("Kumar Weddings"), str("42 MG Road"), str("+919876543210"))
	// The edit that used to wipe the rest: name only, everything else absent.
	save(str("Kumar Weddings & Events"), nil, nil)

	var name string
	var addr, phone *string
	if err := pool.QueryRow(ctx,
		`SELECT business_name, business_address, business_phone FROM vendors WHERE user_id = $1`,
		userID).Scan(&name, &addr, &phone); err != nil {
		t.Fatal(err)
	}
	if name != "Kumar Weddings & Events" {
		t.Errorf("business_name = %q, want the updated value", name)
	}
	if addr == nil || *addr != "42 MG Road" {
		t.Errorf("business_address = %v, want it preserved", addr)
	}
	if phone == nil || *phone != "+919876543210" {
		t.Errorf("business_phone = %v, want it preserved", phone)
	}
}
