package service

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	bookingrepo "github.com/tripfcatory/marriage-hall-booking/internal/booking/repository"
	"github.com/tripfcatory/marriage-hall-booking/pkg/apperr"
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
func (s *Service) Create(ctx context.Context, userID int64, bookingID string) (*Payment, error) {
	var (
		ownerID     int64
		total, paid float64
		status      string
	)
	err := s.db.QueryRow(ctx,
		`SELECT user_id, total_amount, COALESCE(paid_amount,0), status
		 FROM bookings WHERE id = $1 AND is_deleted = FALSE`, bookingID).
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
	if status != "PENDING" {
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

	orderID := "order_" + randHex(12)
	var p Payment
	err = s.db.QueryRow(ctx,
		`INSERT INTO payments (booking_id, user_id, amount, gateway, gateway_order_id, payment_type)
		 VALUES ($1, $2, $3, 'MOCK', $4, $5)
		 RETURNING id, booking_id, user_id, amount, currency, status, gateway,
		           gateway_order_id, payment_type, created_at`,
		bookingID, userID, due, orderID, payType).
		Scan(&p.ID, &p.BookingID, &p.UserID, &p.Amount, &p.Currency, &p.Status,
			&p.Gateway, &p.GatewayOrderID, &p.PaymentType, &p.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
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
func (s *Service) HandleWebhook(ctx context.Context, raw []byte, p WebhookPayload) error {
	if p.EventID == "" || p.OrderID == "" {
		return apperr.BadRequest("INVALID_WEBHOOK", "eventId and orderId are required")
	}

	payload, _ := json.Marshal(p)
	_, err := s.db.Exec(ctx,
		`INSERT INTO payment_webhook_events (event_id, payload) VALUES ($1, $2)`,
		p.EventID, payload)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil // already processed; replay is a no-op
		}
		return err
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

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
		if _, err := tx.Exec(ctx,
			`UPDATE bookings SET paid_amount = paid_amount + $2,
			    status = CASE WHEN paid_amount + $2 >= total_amount THEN 'CONFIRMED' ELSE status END,
			    expires_at = CASE WHEN paid_amount + $2 >= total_amount THEN NULL ELSE expires_at END,
			    updated_at = CURRENT_TIMESTAMP
			 WHERE id = $1`, bookingID, amount); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO booking_status_history (booking_id, from_status, to_status, reason)
			 VALUES ($1, 'PENDING', 'CONFIRMED', 'Payment received')`, bookingID); err != nil {
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
func (s *Service) Refund(ctx context.Context, paymentID string, amount float64, reason string) (string, error) {
	if amount <= 0 {
		return "", apperr.BadRequest("INVALID_AMOUNT", "Refund amount must be positive")
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	var paid float64
	var status, bookingID string
	err = tx.QueryRow(ctx,
		`SELECT amount, status, booking_id FROM payments WHERE id = $1 FOR UPDATE`,
		paymentID).Scan(&paid, &status, &bookingID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", apperr.BadRequest("PAYMENT_NOT_FOUND", "Payment not found")
	}
	if err != nil {
		return "", err
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
