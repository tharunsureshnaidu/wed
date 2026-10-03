// Package audit records who decided what, and why.
//
// Approvals and rejections were scattered: a facility's verdict lived only in
// facilities.status, a KYC reason in vendors.kyc_rejection_reason, a quote's in
// quotes.rejection_reason, and a booking's nowhere at all. Each table knew its
// own latest state and nothing knew the history - so "why was this rejected,
// and by whom" had no answer, and "how many were rejected last week" needed a
// different query per entity.
//
// One row per decision, in the audit_logs table that already existed for
// block/unblock.
package audit

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/logger"
)

// Entity names, stored in audit_logs.entity_name. Values, not an enum type,
// because the column is free text and other actions already write to it.
const (
	EntityFacility = "facilities"
	EntityVendor   = "vendors"
	EntityBooking  = "bookings"
	EntityQuote    = "quotes"
	EntityReview   = "reviews"
	EntityUser     = "users"
)

// Decision is one approve/reject, recorded as it happens.
type Decision struct {
	Actor    int64  // who decided; 0 for a system action
	Action   string // APPROVE_FACILITY, REJECT_QUOTE, ...
	Entity   string // one of the Entity constants
	EntityID string
	Status   string // the status written: APPROVED, REJECTED, CANCELLED, ...
	Reason   string // why, free text; empty when none was given
	IP       string

	// EventType is the occasion a booking was for (WEDDING, SANGEET, ...).
	// Only bookings have one; it rides along so the analytics can break
	// rejections down by occasion without joining back to bookings, which
	// would miss any booking later deleted.
	EventType string

	// Extra is merged into new_values for anything entity-specific.
	Extra map[string]any
}

// Execer is satisfied by *pgxpool.Pool and by pgx.Tx, so a decision can be
// recorded inside the very transaction that made it.
type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Record writes one decision.
//
// Best-effort by design: a failed audit write must not fail the decision the
// admin just made. Rolling back an approval because a log row would not insert
// turns an observability feature into an outage. Failures are logged loudly so
// they are never silent.
func Record(ctx context.Context, db Execer, d Decision) {
	values := map[string]any{"status": d.Status}
	if d.Reason != "" {
		values["reason"] = d.Reason
	}
	if d.EventType != "" {
		values["eventType"] = d.EventType
	}
	for k, v := range d.Extra {
		values[k] = v
	}
	payload, err := json.Marshal(values)
	if err != nil {
		logger.Error("audit: marshal", "action", d.Action, logger.Err(err))
		return
	}

	// A system action has no user; NULL is correct, 0 would be a user id that
	// does not exist and the FK would reject it.
	var actor any
	if d.Actor != 0 {
		actor = d.Actor
	}
	if _, err := db.Exec(ctx,
		`INSERT INTO audit_logs (user_id, action, entity_name, entity_id, new_values, ip_address)
		 VALUES ($1, $2, $3, $4, $5, NULLIF($6,''))`,
		actor, d.Action, d.Entity, d.EntityID, payload, d.IP); err != nil {
		logger.Error("audit: write", "action", d.Action,
			"entity", d.Entity, "entityId", d.EntityID, logger.Err(err))
	}
}

// ActorID renders an actor for an entity_id column, which is text.
func ActorID(id int64) string { return strconv.FormatInt(id, 10) }
