// Package repository stores the notification outbox: one row per
// (booking, recipient, channel), retried until the owner acknowledges.
package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("not found")

type Repo struct{ db *pgxpool.Pool }

func New(db *pgxpool.Pool) *Repo { return &Repo{db: db} }

// BookingContext is everything the templates need, resolved in one query so a
// notification never issues a fan-out of lookups per channel.
type BookingContext struct {
	BookingID   string
	FacilityID  string
	Facility    string
	City        *string
	StartDate   time.Time
	EndDate     time.Time
	StartTime   *string
	EndTime     *string
	GuestCount  *int
	RoomCount   *int
	EventType   *string
	TotalAmount float64

	OwnerUserID int64
	OwnerName   string
	OwnerEmail  *string
	OwnerPhone  *string

	CustomerUserID int64
	CustomerName   string
	// Guest* come off the booking itself and win over the account's contacts:
	// a booking made on someone else's behalf must reach the person attending.
	CustomerEmail *string
	CustomerPhone *string
}

// LoadBookingContext joins booking -> facility -> owner, and the booking's
// guest contacts falling back to the customer's account.
//
// Only facility bookings resolve: target_type HALL/HOTEL both point at
// facilities.
// Anything else returns ErrNotFound and the caller skips it rather than
// guessing at an owner.
func (r *Repo) LoadBookingContext(ctx context.Context, bookingID string) (*BookingContext, error) {
	var c BookingContext
	err := r.db.QueryRow(ctx, `
		SELECT b.id, f.id, f.name, f.city,
		       b.check_in, b.check_out, b.start_time, b.end_time,
		       b.guest_count, b.room_count, b.event_type, b.total_amount,
		       o.id, o.full_name, o.email, o.phone_number,
		       u.id, u.full_name,
		       COALESCE(b.guest_email, u.email), COALESCE(b.guest_phone, u.phone_number)
		  FROM bookings b
		  JOIN facilities f ON f.id = b.target_id
		  JOIN users o ON o.id = f.owner_id
		  JOIN users u ON u.id = b.user_id
		 WHERE b.id = $1`, bookingID).Scan(
		&c.BookingID, &c.FacilityID, &c.Facility, &c.City,
		&c.StartDate, &c.EndDate, &c.StartTime, &c.EndTime,
		&c.GuestCount, &c.RoomCount, &c.EventType, &c.TotalAmount,
		&c.OwnerUserID, &c.OwnerName, &c.OwnerEmail, &c.OwnerPhone,
		&c.CustomerUserID, &c.CustomerName,
		&c.CustomerEmail, &c.CustomerPhone)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

type Notification struct {
	ID            string
	BookingID     string
	RecipientRole string
	RecipientID   *int64
	Channel       string
	Destination   string
	Subject       *string
	Body          string
	Attempts      int

	// AckToken is set only for chased roles; AckTokenTTL is how long the link
	// in the message stays usable.
	AckToken    string
	AckTokenTTL time.Duration

	// EventType and SubjectID identify what happened and to what. Together
	// with role and channel they are the idempotency key, so a redelivered
	// event updates nothing instead of sending twice. SubjectID is the
	// booking, facility or user id depending on the event.
	EventType string
	SubjectID string
}

// Enqueue inserts one outbox row. A repeated Kafka delivery collides with
// uq_notification_target and is silently absorbed, which is what keeps the
// consumer at-least-once without sending twice.
func (r *Repo) Enqueue(ctx context.Context, n Notification) error {
	var token *string
	var expires *time.Time
	if n.AckToken != "" {
		token = &n.AckToken
		t := time.Now().Add(n.AckTokenTTL)
		expires = &t
	}
	// A non-booking event (facility approved, user blocked) has no booking to
	// reference, and the FK rejects an empty string.
	var bookingID *string
	if n.BookingID != "" {
		bookingID = &n.BookingID
	}
	subjectID := n.SubjectID
	if subjectID == "" {
		subjectID = n.BookingID
	}
	eventType := n.EventType
	if eventType == "" {
		eventType = "booking.created"
	}
	_, err := r.db.Exec(ctx, `
		INSERT INTO notifications
		    (booking_id, recipient_role, recipient_user_id, channel, destination,
		     subject, body, ack_token, ack_token_expires_at, event_type, subject_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (event_type, subject_id, recipient_role, channel) DO NOTHING`,
		bookingID, n.RecipientRole, n.RecipientID, n.Channel,
		n.Destination, n.Subject, n.Body, token, expires, eventType, subjectID)
	return err
}

// AckByToken redeems a one-tap link. It clears every chased row for that
// booking and role - the owner taps the link in whichever message reached them
// first, and the reminders on their other channels stop too, which is the
// whole point of acknowledging.
//
// An expired or unknown token matches nothing and is reported as not found:
// the endpoint is unauthenticated, so it must not distinguish "wrong token"
// from "already used" to anyone probing it.
func (r *Repo) AckByToken(ctx context.Context, token string) (bookingID, role string, err error) {
	err = r.db.QueryRow(ctx, `
		SELECT booking_id, recipient_role FROM notifications
		 WHERE ack_token = $1
		   AND (ack_token_expires_at IS NULL OR ack_token_expires_at > CURRENT_TIMESTAMP)`,
		token).Scan(&bookingID, &role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrNotFound
	}
	if err != nil {
		return "", "", err
	}
	tag, err := r.db.Exec(ctx, `
		UPDATE notifications
		   SET status = 'ACKED', acked_at = CURRENT_TIMESTAMP,
		       next_attempt_at = NULL, updated_at = CURRENT_TIMESTAMP
		 WHERE booking_id = $1 AND recipient_role = $2
		   AND status IN ('PENDING', 'SENT')`, bookingID, role)
	if err != nil {
		return "", "", err
	}
	if tag.RowsAffected() == 0 {
		// Valid token, but nothing left to stop - already acknowledged.
		return bookingID, role, ErrNotFound
	}
	return bookingID, role, nil
}

// ClaimDue locks and returns up to limit rows that are due to send.
//
// FOR UPDATE SKIP LOCKED so a second worker takes different rows instead of
// blocking - the retry loop stays correct if this is ever run more than once.
// The claim bumps attempts and pushes next_attempt_at forward inside the same
// transaction, so a crash mid-send costs one retry interval, never a duplicate
// storm.
func (r *Repo) ClaimDue(ctx context.Context, limit int, backoff time.Duration) ([]Notification, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `
		SELECT id, COALESCE(booking_id::text, ''), recipient_role, recipient_user_id, channel,
		       destination, subject, body, attempts, COALESCE(event_type, ''), COALESCE(subject_id, '')
		  FROM notifications
		 WHERE status = 'PENDING'
		   AND next_attempt_at IS NOT NULL
		   AND next_attempt_at <= CURRENT_TIMESTAMP
		 ORDER BY next_attempt_at
		 LIMIT $1
		   FOR UPDATE SKIP LOCKED`, limit)
	if err != nil {
		return nil, err
	}
	var out []Notification
	var ids []string
	for rows.Next() {
		var n Notification
		if err := rows.Scan(&n.ID, &n.BookingID, &n.RecipientRole, &n.RecipientID,
			&n.Channel, &n.Destination, &n.Subject, &n.Body, &n.Attempts,
			&n.EventType, &n.SubjectID); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, n)
		ids = append(ids, n.ID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) > 0 {
		if _, err := tx.Exec(ctx, `
			UPDATE notifications
			   SET attempts = attempts + 1,
			       next_attempt_at = CURRENT_TIMESTAMP + $2::interval,
			       updated_at = CURRENT_TIMESTAMP
			 WHERE id = ANY($1)`, ids, backoff.String()); err != nil {
			return nil, err
		}
	}
	return out, tx.Commit(ctx)
}

// MarkSent records a successful delivery. The row stays PENDING for an OWNER
// notification - delivery is not acknowledgement, and the whole point is to
// keep chasing until a human confirms. A CUSTOMER row is informational and
// terminal on first success.
func (r *Repo) MarkSent(ctx context.Context, id string, done bool) error {
	if done {
		_, err := r.db.Exec(ctx, `
			UPDATE notifications
			   SET status = 'SENT', sent_at = CURRENT_TIMESTAMP,
			       next_attempt_at = NULL, last_error = NULL,
			       updated_at = CURRENT_TIMESTAMP
			 WHERE id = $1`, id)
		return err
	}
	_, err := r.db.Exec(ctx, `
		UPDATE notifications
		   SET sent_at = COALESCE(sent_at, CURRENT_TIMESTAMP),
		       last_error = NULL, updated_at = CURRENT_TIMESTAMP
		 WHERE id = $1`, id)
	return err
}

// MarkFailed records the error. After maxAttempts the row goes terminal rather
// than retrying forever: a permanently bad number cannot be fixed by resending,
// and an unbounded loop would hammer the vendor and the log for that booking's
// whole lifetime.
func (r *Repo) MarkFailed(ctx context.Context, id string, attempts, maxAttempts int, cause string) error {
	status := "PENDING"
	if attempts >= maxAttempts {
		status = "FAILED"
	}
	_, err := r.db.Exec(ctx, `
		UPDATE notifications
		   SET status = $2,
		       last_error = $3,
		       next_attempt_at = CASE WHEN $2 = 'FAILED' THEN NULL ELSE next_attempt_at END,
		       updated_at = CURRENT_TIMESTAMP
		 WHERE id = $1`, id, status, cause)
	return err
}

// AckAdmin stops the ADMIN retries for a booking. Authorisation is the
// caller's ROLE_ADMIN, checked in the handler: these rows have no
// recipient_user_id to match on, because the destination is an ops address
// rather than a person.
func (r *Repo) AckAdmin(ctx context.Context, bookingID string) (int64, error) {
	tag, err := r.db.Exec(ctx, `
		UPDATE notifications
		   SET status = 'ACKED', acked_at = CURRENT_TIMESTAMP,
		       next_attempt_at = NULL, updated_at = CURRENT_TIMESTAMP
		 WHERE booking_id = $1
		   AND recipient_role = 'ADMIN'
		   AND status IN ('PENDING', 'SENT')`, bookingID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// Ack stops the OWNER retries for a booking. The ownership test lives in the
// UPDATE's join, so a non-owner silently matches nothing; the returned count is
// what the handler reports back so a second ack is visibly a no-op.
func (r *Repo) Ack(ctx context.Context, bookingID string, ownerID int64) (int64, error) {
	tag, err := r.db.Exec(ctx, `
		UPDATE notifications n
		   SET status = 'ACKED', acked_at = CURRENT_TIMESTAMP,
		       next_attempt_at = NULL, updated_at = CURRENT_TIMESTAMP
		  FROM bookings b, facilities f
		 WHERE n.booking_id = $1
		   AND b.id = n.booking_id
		   AND f.id = b.target_id
		   AND f.owner_id = $2
		   AND n.recipient_role = 'OWNER'
		   AND n.status IN ('PENDING', 'SENT')`, bookingID, ownerID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// CancelForBooking stops chasing a booking that no longer needs an answer -
// cancelled by the customer, or expired by the sweeper. Without this the owner
// keeps being paged about a dead booking.
func (r *Repo) CancelForBooking(ctx context.Context, bookingID string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE notifications
		   SET status = 'CANCELLED', next_attempt_at = NULL,
		       updated_at = CURRENT_TIMESTAMP
		 WHERE booking_id = $1 AND status = 'PENDING'`, bookingID)
	return err
}

// --- device tokens ---

type Device struct {
	Token    string
	Platform string
}

// SaveDevice registers or refreshes a device token.
//
// Upsert on the token, not on (user, token): the same install can change hands
// - a shared tablet, a reassigned work phone - and the row must follow the
// current user rather than leave the previous one still being pushed to.
// Re-registering also revives a token previously retired as dead, which is
// what happens when the app is reinstalled.
func (r *Repo) SaveDevice(ctx context.Context, userID int64, d Device) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO device_tokens (user_id, token, platform)
		VALUES ($1, $2, $3)
		ON CONFLICT (token) DO UPDATE
		   SET user_id = EXCLUDED.user_id,
		       platform = EXCLUDED.platform,
		       is_active = TRUE,
		       last_seen_at = CURRENT_TIMESTAMP`,
		userID, d.Token, d.Platform)
	return err
}

// DeleteDevice deregisters one token, scoped to its owner so a caller cannot
// unregister someone else's device by guessing a token.
func (r *Repo) DeleteDevice(ctx context.Context, userID int64, token string) (int64, error) {
	tag, err := r.db.Exec(ctx,
		`DELETE FROM device_tokens WHERE user_id = $1 AND token = $2`, userID, token)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ActiveTokens returns the live device tokens for a user. A PUSH outbox row
// addresses a person; delivery fans out to whatever devices they currently
// have, which is why the row's destination is the user id rather than a token.
func (r *Repo) ActiveTokens(ctx context.Context, userID int64) ([]string, error) {
	rows, err := r.db.Query(ctx,
		`SELECT token FROM device_tokens WHERE user_id = $1 AND is_active`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// DeactivateToken retires a token FCM has rejected as dead. Not deleted: the
// row is evidence of an install that existed, and a reinstall revives it
// through SaveDevice's upsert.
func (r *Repo) DeactivateToken(ctx context.Context, token string) error {
	_, err := r.db.Exec(ctx,
		`UPDATE device_tokens SET is_active = FALSE WHERE token = $1`, token)
	return err
}

// UserContact is the minimum needed to reach one user.
type UserContact struct {
	Name  string
	Email *string
	Phone *string
}

// LoadUserContact resolves a user's contact details. Deleted users resolve to
// ErrNotFound so an event about an account that no longer exists is skipped
// rather than queued to a dead address.
func (r *Repo) LoadUserContact(ctx context.Context, userID int64) (*UserContact, error) {
	var c UserContact
	err := r.db.QueryRow(ctx,
		`SELECT full_name, email, phone_number FROM users
		  WHERE id = $1 AND is_deleted = FALSE`, userID).
		Scan(&c.Name, &c.Email, &c.Phone)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// DeclineByToken is the owner refusing a booking from the link in their
// message. Distinct from Ack so ops can tell "said no" from "never answered":
// the rows go DECLINED rather than ACKED, and the caller tells the admins.
func (r *Repo) DeclineByToken(ctx context.Context, token string) (bookingID, role string, err error) {
	err = r.db.QueryRow(ctx, `
		SELECT COALESCE(booking_id::text, ''), recipient_role FROM notifications
		 WHERE ack_token = $1
		   AND (ack_token_expires_at IS NULL OR ack_token_expires_at > CURRENT_TIMESTAMP)`,
		token).Scan(&bookingID, &role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrNotFound
	}
	if err != nil {
		return "", "", err
	}
	tag, err := r.db.Exec(ctx, `
		UPDATE notifications
		   SET status = 'DECLINED', acked_at = CURRENT_TIMESTAMP,
		       next_attempt_at = NULL, updated_at = CURRENT_TIMESTAMP
		 WHERE booking_id = $1 AND recipient_role = $2
		   AND status IN ('PENDING', 'SENT')`, bookingID, role)
	if err != nil {
		return "", "", err
	}
	if tag.RowsAffected() == 0 {
		return bookingID, role, ErrNotFound
	}
	return bookingID, role, nil
}

// --- geo targeting ---

// GeoTarget is one user to notify about something near them.
type GeoTarget struct {
	UserID   int64
	Name     string
	Email    *string
	Phone    *string
	Distance float64 // metres from the subject, for the message
}

// UsersNear returns customers within radiusM metres of a point.
//
// Two predicates, deliberately: earth_box is a bounding cube the GiST index can
// answer, earth_distance is the exact great-circle check. The box alone would
// include corners up to ~41% beyond the radius; the distance alone cannot use
// an index and scans every row.
//
// Excludes: users who opted out, deleted or suspended accounts, and the actor
// who caused the event - nobody needs telling about their own listing.
func (r *Repo) UsersNear(ctx context.Context, lat, lng, radiusM float64, excludeUserID int64, limit int) ([]GeoTarget, error) {
	rows, err := r.db.Query(ctx, `
		SELECT u.id, u.full_name, u.email, u.phone_number,
		       earth_distance(ll_to_earth(p.lat, p.lng), ll_to_earth($1, $2)) AS dist
		  FROM user_profiles p
		  JOIN users u ON u.id = p.id
		  JOIN user_roles ur ON ur.user_id = u.id
		  JOIN roles ro ON ro.id = ur.role_id
		 WHERE p.lat IS NOT NULL AND p.lng IS NOT NULL
		   AND p.geo_notifications_enabled
		   AND p.is_deleted = FALSE
		   AND u.is_deleted = FALSE
		   AND u.status = 'ACTIVE'
		   AND ro.role_name = 'ROLE_CUSTOMER'
		   AND u.id <> $5
		   AND ll_to_earth(p.lat, p.lng) <@ earth_box(ll_to_earth($1, $2), $3)
		   AND earth_distance(ll_to_earth(p.lat, p.lng), ll_to_earth($1, $2)) <= $3
		 ORDER BY dist
		 LIMIT $4`, lat, lng, radiusM, limit, excludeUserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []GeoTarget
	for rows.Next() {
		var t GeoTarget
		if err := rows.Scan(&t.UserID, &t.Name, &t.Email, &t.Phone, &t.Distance); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// FacilityPoint is a facility's coordinates, or ok=false when it has none.
func (r *Repo) FacilityPoint(ctx context.Context, facilityID string) (lat, lng float64, name string, ownerID int64, ok bool, err error) {
	var la, ln *float64
	err = r.db.QueryRow(ctx,
		`SELECT lat::double precision, lng::double precision, name, owner_id
		   FROM facilities WHERE id = $1 AND is_deleted = FALSE`,
		facilityID).Scan(&la, &ln, &name, &ownerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, 0, "", 0, false, ErrNotFound
	}
	if err != nil {
		return 0, 0, "", 0, false, err
	}
	if la == nil || ln == nil {
		return 0, 0, name, ownerID, false, nil
	}
	return *la, *ln, name, ownerID, true, nil
}

// SaveUserLocation stores a user's coordinates.
//
// A weaker source never overwrites a stronger one: DEVICE is a GPS fix the user
// consented to, and an inference from their bookings must not replace it.
func (r *Repo) SaveUserLocation(ctx context.Context, userID int64, lat, lng float64, source string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE user_profiles
		   SET lat = $2, lng = $3, location_source = $4,
		       location_updated_at = CURRENT_TIMESTAMP,
		       updated_at = CURRENT_TIMESTAMP
		 WHERE id = $1
		   AND ($4 = 'DEVICE'
		        OR location_source IS NULL
		        OR location_source = 'BOOKING'
		        OR ($4 = 'CITY' AND location_source <> 'DEVICE'))`,
		userID, lat, lng, source)
	return err
}

// SetGeoNotifications is the per-user opt-out.
func (r *Repo) SetGeoNotifications(ctx context.Context, userID int64, enabled bool) error {
	_, err := r.db.Exec(ctx,
		`UPDATE user_profiles SET geo_notifications_enabled = $2,
		        updated_at = CURRENT_TIMESTAMP WHERE id = $1`, userID, enabled)
	return err
}

// BackfillLocationsFromBookings gives a location to users who never set one,
// averaging the coordinates of venues they have booked.
//
// Weakest source, so SaveUserLocation's precedence rules let a real fix replace
// it later. Without this every existing user is invisible to geo targeting
// until the app ships a location update.
func (r *Repo) BackfillLocationsFromBookings(ctx context.Context) (int64, error) {
	tag, err := r.db.Exec(ctx, `
		UPDATE user_profiles p
		   SET lat = s.lat, lng = s.lng, location_source = 'BOOKING',
		       location_updated_at = CURRENT_TIMESTAMP,
		       updated_at = CURRENT_TIMESTAMP
		  FROM (SELECT b.user_id,
		               avg(f.lat::double precision) AS lat,
		               avg(f.lng::double precision) AS lng
		          FROM bookings b
		          JOIN facilities f ON f.id = b.target_id
		         WHERE f.lat IS NOT NULL AND f.lng IS NOT NULL
		           AND b.is_deleted = FALSE
		         GROUP BY b.user_id) s
		 WHERE p.id = s.user_id
		   AND p.lat IS NULL`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// FeedItem is one row of the in-app notification list. It is a collapsed view
// of the outbox: the same event delivered by email, SMS, WhatsApp and push is
// four rows here but one line in the app.
type FeedItem struct {
	ID        string     `json:"id"`
	EventType string     `json:"eventType"`
	SubjectID string     `json:"subjectId,omitempty"`
	BookingID *string    `json:"bookingId,omitempty"`
	Title     string     `json:"title"`
	Body      string     `json:"body"`
	IsRead    bool       `json:"isRead"`
	ReadAt    *time.Time `json:"readAt,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
}

// feedCols is shared by Feed and the collapse subquery so the two cannot drift.
//
// DISTINCT ON collapses the channel fan-out: one event to one person is up to
// four outbox rows with identical text, and a feed that showed all four would
// repeat "Booking Confirmed!" four times. The row kept is the earliest, which
// is when the user was first told.
//
// COALESCE on the grouping key matters: subject_id is NULL on older rows, and
// NULLs are distinct from each other in DISTINCT ON, so without it every
// pre-migration row would survive the collapse.
const feedCols = `
	SELECT DISTINCT ON (n.event_type, COALESCE(n.subject_id, n.id::text))
	       n.id, COALESCE(n.event_type, 'unknown'), COALESCE(n.subject_id, ''),
	       n.booking_id, COALESCE(n.subject, ''), n.body,
	       n.read_at, n.created_at
	  FROM notifications n
	 WHERE n.recipient_user_id = $1`

// Feed returns one page of a user's notifications, newest first.
//
// unreadOnly is a filter rather than a separate method because the app uses the
// same list with a toggle. before is a keyset cursor: OFFSET would drift as new
// notifications arrive while the user is scrolling.
func (r *Repo) Feed(ctx context.Context, userID int64, unreadOnly bool, before *time.Time, limit int) ([]FeedItem, error) {
	// The collapse has to happen before the ordering: DISTINCT ON forces its
	// own ORDER BY, so the newest-first sort is applied to the collapsed set in
	// an outer query.
	q := feedCols
	if unreadOnly {
		q += ` AND n.read_at IS NULL`
	}
	if before != nil {
		q += ` AND n.created_at < $3`
	}
	q += ` ORDER BY n.event_type, COALESCE(n.subject_id, n.id::text), n.created_at`
	q = `SELECT * FROM (` + q + `) f ORDER BY f.created_at DESC LIMIT $2`

	args := []any{userID, limit}
	if before != nil {
		args = append(args, *before)
	}
	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []FeedItem{}
	for rows.Next() {
		var it FeedItem
		var bookingID *string
		if err := rows.Scan(&it.ID, &it.EventType, &it.SubjectID, &bookingID,
			&it.Title, &it.Body, &it.ReadAt, &it.CreatedAt); err != nil {
			return nil, err
		}
		it.BookingID = bookingID
		it.IsRead = it.ReadAt != nil
		out = append(out, it)
	}
	return out, rows.Err()
}

// UnreadCount is the badge. Counts collapsed events, not outbox rows, or one
// booking would show as 4 unread.
func (r *Repo) UnreadCount(ctx context.Context, userID int64) (int, error) {
	var n int
	err := r.db.QueryRow(ctx, `
		SELECT COUNT(DISTINCT (event_type, COALESCE(subject_id, id::text)))
		  FROM notifications
		 WHERE recipient_user_id = $1 AND read_at IS NULL`, userID).Scan(&n)
	return n, err
}

// MarkRead marks every outbox row behind one feed item as read. The app sends
// the feed item's id; the other channels of the same event must go read with
// it, or the badge would still count the SMS copy of a push the user just
// opened.
//
// Scoped to the caller's own rows: an id from another user's feed matches
// nothing rather than revealing that it exists.
func (r *Repo) MarkRead(ctx context.Context, userID int64, id string) (int64, error) {
	tag, err := r.db.Exec(ctx, `
		UPDATE notifications SET read_at = CURRENT_TIMESTAMP
		 WHERE recipient_user_id = $1 AND read_at IS NULL
		   AND (event_type, COALESCE(subject_id, id::text)) IN (
		       SELECT event_type, COALESCE(subject_id, id::text)
		         FROM notifications
		        WHERE id = $2 AND recipient_user_id = $1)`, userID, id)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// MarkAllRead is the "Read All" button.
func (r *Repo) MarkAllRead(ctx context.Context, userID int64) (int64, error) {
	tag, err := r.db.Exec(ctx, `
		UPDATE notifications SET read_at = CURRENT_TIMESTAMP
		 WHERE recipient_user_id = $1 AND read_at IS NULL`, userID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// BookingsStartingIn returns confirmed bookings whose stay or event begins
// exactly daysOut days from today.
//
// Exact day, not a range: the sweep runs hourly and a range would match the
// same booking on every run. The outbox unique index would absorb the repeats,
// but matching one day keeps the query small as the table grows.
func (r *Repo) BookingsStartingIn(ctx context.Context, daysOut int) ([]string, error) {
	rows, err := r.db.Query(ctx, `
		SELECT b.id::text
		  FROM bookings b
		 WHERE b.status = 'CONFIRMED'
		   AND b.is_deleted = false
		   AND b.check_in = CURRENT_DATE + make_interval(days => $1)`, daysOut)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanIDs(rows)
}

// BookingsAwaitingReview returns bookings whose stay has ended and whose
// customer has not reviewed that venue.
//
// The NOT EXISTS mirrors what POST /reviews enforces - reviews are unique per
// (user, facility), not per booking. Without it a repeat customer would be
// asked to review a venue they already rated and the link would 403.
//
// The 30-day floor stops the sweep asking about bookings from last year the
// first time it runs.
func (r *Repo) BookingsAwaitingReview(ctx context.Context) ([]string, error) {
	rows, err := r.db.Query(ctx, `
		SELECT b.id::text
		  FROM bookings b
		 WHERE b.status IN ('CONFIRMED', 'COMPLETED')
		   AND b.is_deleted = false
		   AND b.check_out < CURRENT_DATE
		   AND b.check_out >= CURRENT_DATE - INTERVAL '30 days'
		   AND NOT EXISTS (
		       SELECT 1 FROM reviews rv
		        WHERE rv.user_id = b.user_id
		          AND rv.facility_id = b.target_id
		          AND rv.is_deleted = false)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanIDs(rows)
}

func scanIDs(rows pgx.Rows) ([]string, error) {
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
