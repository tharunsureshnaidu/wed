package service

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	bookingrepo "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/booking/repository"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/apperr"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/logger"
)

type Service struct {
	db       *pgxpool.Pool
	books    *bookingrepo.Repo
	secret   []byte
	OnPaid   func(ctx context.Context, bookingID, paymentID string, amount float64)
	OnFailed func(ctx context.Context, bookingID, paymentID string, reason string)
}

func New(db *pgxpool.Pool, books *bookingrepo.Repo, webhookSecret string) *Service {
	return &Service{db: db, books: books, secret: []byte(webhookSecret)}
}

type Payment struct {
	ID             string    `json:"id"`
	BookingID      string    `json:"bookingId"`
	UserID         int64     `json:"userId"`
	Amount         float64   `json:"amount"`
	Currency       string    `json:"currency"`
	Status         string    `json:"status"`
	Gateway        string    `json:"gateway"`
	GatewayOrderID *string   `json:"gatewayOrderId"`
	PaymentType    string    `json:"paymentType"`
	CreatedAt      time.Time `json:"createdAt"`
}

// Create opens a payment against a booking. The amount is the booking's own
// outstanding balance - never a number supplied by the caller.
//
// One open order per booking: a double-tap or a second device gets the same
// PENDING order back. Two orders each for the full balance could both be paid,
// charging the customer twice. The booking row lock serialises the check.
func (s *Service) Create(ctx context.Context, userID int64, bookingID string) (*Payment, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var (
		ownerID     int64
		total, paid float64
		status      string
	)
	err = tx.QueryRow(ctx,
		`SELECT user_id, total_amount, COALESCE(paid_amount,0), status
		 FROM bookings WHERE id = $1 AND is_deleted = FALSE FOR UPDATE`, bookingID).
		Scan(&ownerID, &total, &paid, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.BadRequest("BOOKING_NOT_FOUND", "Booking not found")
	}
	if err != nil {
		return nil, err
	}
	if ownerID != userID {
		return nil, apperr.Forbidden("NOT_BOOKING_OWNER", "You cannot pay for this booking")
	}
	// CONFIRMED is payable as well as PENDING. A venue owner can accept a
	// booking before the money arrives - that is what confirmation means here,
	// and the balance is usually paid afterwards. Refusing payment on a
	// confirmed booking would leave it owing money it could never settle.
	if status != "PENDING" && status != "CONFIRMED" {
		return nil, apperr.Conflict("INVALID_STATE", "Booking is not awaiting payment")
	}
	due := total - paid
	if due <= 0 {
		return nil, apperr.Conflict("ALREADY_PAID", "Booking is already paid")
	}

	// Derived, never taken from the caller: the charge is always the outstanding
	// balance, so anything already paid makes this the closing BALANCE payment.
	payType := "FULL"
	if paid > 0 {
		payType = "BALANCE"
	}

	const ret = ` id, booking_id, user_id, amount, currency, status, gateway,
		           gateway_order_id, payment_type, created_at`
	var p Payment
	dest := []any{&p.ID, &p.BookingID, &p.UserID, &p.Amount, &p.Currency, &p.Status,
		&p.Gateway, &p.GatewayOrderID, &p.PaymentType, &p.CreatedAt}

	// While one order is open nothing else can be paid, so the balance cannot
	// shrink under it: the open order is still the right amount to pay.
	err = tx.QueryRow(ctx,
		`SELECT`+ret+` FROM payments WHERE booking_id = $1 AND status = 'PENDING'
		 ORDER BY created_at DESC LIMIT 1`, bookingID).Scan(dest...)
	if err == nil {
		return &p, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}

	orderID := "order_" + randHex(12)
	err = tx.QueryRow(ctx,
		`INSERT INTO payments (booking_id, user_id, amount, gateway, gateway_order_id, payment_type)
		 VALUES ($1, $2, $3, 'MOCK', $4, $5)
		 RETURNING`+ret, bookingID, userID, due, orderID, payType).Scan(dest...)
	if err != nil {
		return nil, err
	}
	return &p, tx.Commit(ctx)
}

type WebhookPayload struct {
	EventID   string  `json:"eventId"`
	OrderID   string  `json:"orderId"`
	PaymentID string  `json:"paymentId"`
	Status    string  `json:"status"` // SUCCESS or FAILED
	Amount    float64 `json:"amount"`
	Reason    string  `json:"reason"`
}

// VerifySignature checks the HMAC the gateway sends. Without this anyone who
// can reach the webhook URL could mark any booking paid.
func (s *Service) VerifySignature(body []byte, signature string) bool {
	if len(s.secret) == 0 {
		return false
	}
	m := hmac.New(sha256.New, s.secret)
	m.Write(body)
	want := hex.EncodeToString(m.Sum(nil))
	return hmac.Equal([]byte(want), []byte(signature))
}

// HandleWebhook applies a gateway callback exactly once.
//
// The UNIQUE event_id insert is the idempotency guard: a gateway that retries a
// delivery (they all do) hits a duplicate key and the event is ignored rather
// than crediting the booking twice.
//
// The insert is inside the transaction that applies the payment. Committed on
// its own first, a transient failure later on rolled the work back but kept the
// event row, so the gateway's retry was told "already processed" - the money
// taken, the booking never credited.
func (s *Service) HandleWebhook(ctx context.Context, raw []byte, p WebhookPayload) error {
	if p.EventID == "" || p.OrderID == "" {
		return apperr.BadRequest("INVALID_WEBHOOK", "eventId and orderId are required")
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	payload, _ := json.Marshal(p)
	tag, err := tx.Exec(ctx,
		`INSERT INTO payment_webhook_events (event_id, payload) VALUES ($1, $2)
		 ON CONFLICT (event_id) DO NOTHING`, p.EventID, payload)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return nil // already processed; replay is a no-op
	}

	// FOR UPDATE so two concurrent deliveries for the same order serialise.
	var paymentID, bookingID, status string
	var amount float64
	err = tx.QueryRow(ctx,
		`SELECT id, booking_id, status, amount FROM payments
		 WHERE gateway_order_id = $1 FOR UPDATE`, p.OrderID).
		Scan(&paymentID, &bookingID, &status, &amount)
	if errors.Is(err, pgx.ErrNoRows) {
		return apperr.BadRequest("UNKNOWN_ORDER", "Unknown order")
	}
	if err != nil {
		return err
	}
	if status != "PENDING" {
		return nil // terminal already
	}

	switch p.Status {
	case "SUCCESS":
		if _, err := tx.Exec(ctx,
			`UPDATE payments SET status = 'SUCCESS', gateway_payment_id = $2,
			    updated_at = CURRENT_TIMESTAMP WHERE id = $1`, paymentID, p.PaymentID); err != nil {
			return err
		}
		// Captured before the update: once the row is written the previous
		// status is gone, and the history row would have to guess at it.
		// FOR UPDATE so the sweeper or a cancel cannot end the booking between
		// this read and the update below.
		var wasStatus string
		var total, paid float64
		if err := tx.QueryRow(ctx,
			`SELECT status, total_amount, COALESCE(paid_amount,0) FROM bookings WHERE id = $1 FOR UPDATE`,
			bookingID).Scan(&wasStatus, &total, &paid); err != nil {
			return err
		}
		if paid+amount > total {
			logger.Warn("payment: overpayment", "bookingId", bookingID, "paymentId", paymentID,
				"total", total, "paidBefore", paid, "amount", amount)
		}
		// A booking that already ended (expired hold, cancelled, rejected) has
		// released its dates, possibly to someone else. Confirming it would be
		// a double booking, so the money is recorded but the status is left
		// alone and the payment flagged for a refund.
		live := wasStatus == "PENDING" || wasStatus == "CONFIRMED"
		if !live {
			logger.Error("payment: received for a booking that has ended - refund due",
				"bookingId", bookingID, "paymentId", paymentID, "status", wasStatus, "amount", amount)
		}
		if _, err := tx.Exec(ctx,
			`UPDATE bookings SET paid_amount = paid_amount + $2,
			    status = CASE WHEN $3 AND paid_amount + $2 >= total_amount THEN 'CONFIRMED' ELSE status END,
			    expires_at = CASE WHEN $3 AND paid_amount + $2 >= total_amount THEN NULL ELSE expires_at END,
			    updated_at = CURRENT_TIMESTAMP
			 WHERE id = $1`, bookingID, amount, live); err != nil {
			return err
		}
		// The real previous status, not a hardcoded PENDING: an owner may have
		// confirmed the booking before the money arrived, and a history row
		// claiming PENDING -> CONFIRMED would be a false record.
		if _, err := tx.Exec(ctx,
			`INSERT INTO booking_status_history (booking_id, from_status, to_status, reason)
			 SELECT $1, $2, status, 'Payment received' FROM bookings WHERE id = $1`,
			bookingID, wasStatus); err != nil {
			return err
		}
	case "FAILED":
		if _, err := tx.Exec(ctx,
			`UPDATE payments SET status = 'FAILED', failure_reason = $2,
			    updated_at = CURRENT_TIMESTAMP WHERE id = $1`, paymentID, p.Reason); err != nil {
			return err
		}
	default:
		return apperr.BadRequest("INVALID_STATUS", "status must be SUCCESS or FAILED")
	}

	if _, err := tx.Exec(ctx,
		`UPDATE payment_webhook_events SET processed = TRUE WHERE event_id = $1`, p.EventID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}

	if p.Status == "SUCCESS" && s.OnPaid != nil {
		s.OnPaid(ctx, bookingID, paymentID, amount)
	}
	if p.Status == "FAILED" && s.OnFailed != nil {
		s.OnFailed(ctx, bookingID, paymentID, p.Reason)
	}
	return nil
}

// Refund issues a refund against a successful payment. The total refunded can
// never exceed what was paid.
//
// Only the owner of the venue the payment was for, or an admin, may refund it.
// The route admits any ROLE_HALL_OWNER, and without this check one vendor
// could refund every other vendor's customers.
func (s *Service) Refund(ctx context.Context, paymentID string, amount float64, reason string, callerID int64, isAdmin bool) (string, error) {
	// NaN slips past "<= 0" and every later comparison, then poisons the
	// refunded sum so no limit ever holds again.
	if amount <= 0 || math.IsNaN(amount) || math.IsInf(amount, 0) {
		return "", apperr.BadRequest("INVALID_AMOUNT", "Refund amount must be positive")
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	var paid float64
	var status, bookingID string
	var venueOwner int64
	err = tx.QueryRow(ctx,
		`SELECT p.amount, p.status, p.booking_id, f.owner_id
		   FROM payments p
		   JOIN bookings b ON b.id = p.booking_id
		   JOIN facilities f ON f.id = b.target_id
		  WHERE p.id = $1 FOR UPDATE OF p`,
		paymentID).Scan(&paid, &status, &bookingID, &venueOwner)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", apperr.BadRequest("PAYMENT_NOT_FOUND", "Payment not found")
	}
	if err != nil {
		return "", err
	}
	if !isAdmin && venueOwner != callerID {
		// Same answer as a missing payment: an id from another venue reveals nothing.
		return "", apperr.BadRequest("PAYMENT_NOT_FOUND", "Payment not found")
	}
	if status != "SUCCESS" && status != "PARTIALLY_REFUNDED" {
		return "", apperr.Conflict("INVALID_STATE", "Only a successful payment can be refunded")
	}

	var alreadyRefunded float64
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(sum(amount),0) FROM refunds
		 WHERE payment_id = $1 AND status <> 'FAILED'`, paymentID).Scan(&alreadyRefunded); err != nil {
		return "", err
	}
	if alreadyRefunded+amount > paid {
		return "", apperr.BadRequest("REFUND_EXCEEDS_PAYMENT",
			"Refund would exceed the amount paid")
	}

	var refundID string
	if err := tx.QueryRow(ctx,
		`INSERT INTO refunds (payment_id, amount, reason, status, gateway_refund_id)
		 VALUES ($1, $2, $3, 'SUCCESS', $4) RETURNING id`,
		paymentID, amount, reason, "rfnd_"+randHex(10)).Scan(&refundID); err != nil {
		return "", err
	}

	newStatus := "PARTIALLY_REFUNDED"
	if alreadyRefunded+amount >= paid {
		newStatus = "REFUNDED"
	}
	if _, err := tx.Exec(ctx,
		`UPDATE payments SET status = $2, updated_at = CURRENT_TIMESTAMP WHERE id = $1`,
		paymentID, newStatus); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE bookings SET paid_amount = GREATEST(paid_amount - $2, 0),
		    updated_at = CURRENT_TIMESTAMP WHERE id = $1`, bookingID, amount); err != nil {
		return "", err
	}

	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return refundID, nil
}

func randHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}
