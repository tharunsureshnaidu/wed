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
	"strings"
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
	provider PaymentProvider
	policy   Policy
	OnPaid   func(ctx context.Context, bookingID, paymentID string, amount float64)
	OnFailed func(ctx context.Context, bookingID, paymentID string, reason string)
}

func New(db *pgxpool.Pool, books *bookingrepo.Repo, webhookSecret string) *Service {
	return &Service{db: db, books: books, secret: []byte(webhookSecret), policy: DefaultPolicy()}
}

func (s *Service) SetCashfreeClient(provider PaymentProvider) {
	s.provider = provider
}

func (s *Service) SetPolicy(policy Policy) {
	s.policy = policy.normalized()
}

func (s *Service) Policy() Policy {
	if s == nil {
		return DefaultPolicy()
	}
	if s.policy.AdvancePercent == 0 && s.policy.CancellationPenaltyPercent == 0 && s.policy.BalanceDueHoursBeforeEvent == 0 && s.policy.CancellationWindowHours == 0 && s.policy.MinAdvanceAmount == 0 && s.policy.MaxAdvanceAmount == 0 {
		return DefaultPolicy()
	}
	return s.policy.normalized()
}

func (s *Service) ComputeDueAmount(totalAmount, paidAmount float64) float64 {
	if s == nil {
		return 0
	}
	return s.Policy().DueForBooking(totalAmount, paidAmount)
}

func (s *Service) LoadPolicyForFacility(ctx context.Context, facilityID string) (Policy, error) {
	if s == nil {
		return DefaultPolicy(), nil
	}
	if strings.TrimSpace(facilityID) == "" {
		return s.Policy(), nil
	}

	var (
		advancePercent             float64
		minAdvanceAmount           float64
		maxAdvanceAmount           float64
		balanceDueHoursBeforeEvent int
		cancellationWindowHours    int
		cancellationPenaltyPercent float64
	)
	query := `
		SELECT advance_percent, min_advance_amount, max_advance_amount,
		       balance_due_hours_before_event, cancellation_window_hours,
		       cancellation_penalty_percent
		  FROM payment_policies
		 WHERE facility_id = $1 AND scope = 'VENUE'
		 ORDER BY updated_at DESC, id DESC
		 LIMIT 1`
	if err := s.db.QueryRow(ctx, query, facilityID).Scan(
		&advancePercent,
		&minAdvanceAmount,
		&maxAdvanceAmount,
		&balanceDueHoursBeforeEvent,
		&cancellationWindowHours,
		&cancellationPenaltyPercent,
	); err == nil {
		return Policy{
			AdvancePercent:             advancePercent,
			MinAdvanceAmount:           minAdvanceAmount,
			MaxAdvanceAmount:           maxAdvanceAmount,
			BalanceDueHoursBeforeEvent: balanceDueHoursBeforeEvent,
			CancellationWindowHours:    cancellationWindowHours,
			CancellationPenaltyPercent: cancellationPenaltyPercent,
		}.normalized(), nil
	}

	if err := s.db.QueryRow(ctx, `
		SELECT advance_percent, min_advance_amount, max_advance_amount,
		       balance_due_hours_before_event, cancellation_window_hours,
		       cancellation_penalty_percent
		  FROM payment_policies
		 WHERE scope = 'MARKETPLACE' AND facility_id IS NULL
		 ORDER BY updated_at DESC, id DESC
		 LIMIT 1`,
	).Scan(&advancePercent,
		&minAdvanceAmount,
		&maxAdvanceAmount,
		&balanceDueHoursBeforeEvent,
		&cancellationWindowHours,
		&cancellationPenaltyPercent,
	); err == nil {
		return Policy{
			AdvancePercent:             advancePercent,
			MinAdvanceAmount:           minAdvanceAmount,
			MaxAdvanceAmount:           maxAdvanceAmount,
			BalanceDueHoursBeforeEvent: balanceDueHoursBeforeEvent,
			CancellationWindowHours:    cancellationWindowHours,
			CancellationPenaltyPercent: cancellationPenaltyPercent,
		}.normalized(), nil
	}

	return s.Policy(), nil
}

func (s *Service) LoadPolicyForBooking(ctx context.Context, bookingID string) (Policy, error) {
	if s == nil {
		return DefaultPolicy(), nil
	}
	if strings.TrimSpace(bookingID) == "" {
		return s.Policy(), nil
	}
	var facilityID string
	if err := s.db.QueryRow(ctx, `SELECT target_id FROM bookings WHERE id = $1 AND is_deleted = FALSE`, bookingID).Scan(&facilityID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return s.Policy(), nil
		}
		return DefaultPolicy(), err
	}
	return s.LoadPolicyForFacility(ctx, facilityID)
}

type Payment struct {
	ID               string    `json:"id"`
	BookingID        string    `json:"bookingId"`
	UserID           int64     `json:"userId"`
	Amount           float64   `json:"amount"`
	Currency         string    `json:"currency"`
	Status           string    `json:"status"`
	Gateway          string    `json:"gateway"`
	GatewayOrderID   *string   `json:"gatewayOrderId"`
	GatewayPaymentID *string   `json:"gatewayPaymentId,omitempty"`
	PaymentType      string    `json:"paymentType"`
	CheckoutURL      string    `json:"checkoutUrl,omitempty"`
	PaymentSessionID string    `json:"paymentSessionId,omitempty"`
	CreatedAt        time.Time `json:"createdAt"`
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
	if err := tx.QueryRow(ctx,
		`SELECT user_id, total_amount, COALESCE(paid_amount,0), status
		 FROM bookings WHERE id = $1 AND is_deleted = FALSE FOR UPDATE`, bookingID,
	).Scan(&ownerID, &total, &paid, &status); errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.BadRequest("BOOKING_NOT_FOUND", "Booking not found")
	} else if err != nil {
		return nil, err
	}
	if ownerID != userID {
		return nil, apperr.Forbidden("NOT_BOOKING_OWNER", "You cannot pay for this booking")
	}
	if status != "PENDING" && status != "CONFIRMED" {
		return nil, apperr.Conflict("INVALID_STATE", "Booking is not awaiting payment")
	}
	due := total - paid
	if due <= 0 {
		return nil, apperr.Conflict("ALREADY_PAID", "Booking is already paid")
	}

	policy, err := s.LoadPolicyForBooking(ctx, bookingID)
	if err != nil {
		logger.Warn("payment: policy lookup failed, using default policy", "bookingId", bookingID, "err", err)
		policy = DefaultPolicy()
	}
	if paid <= 0 {
		due = policy.DueForBooking(total, 0)
	} else {
		due = policy.RemainingBalance(total, paid)
	}
	if due <= 0 {
		return nil, apperr.Conflict("ALREADY_PAID", "Booking is already paid")
	}

	payType := "FULL"
	if paid > 0 {
		payType = "BALANCE"
	} else if policy.AdvancePercent > 0 && policy.AdvancePercent < 100 {
		payType = "ADVANCE"
	}

	const ret = ` id, booking_id, user_id, amount, currency, status, gateway,
		           gateway_order_id, gateway_payment_id, payment_type, checkout_url, payment_session_id, created_at`
	var p Payment
	dest := []any{&p.ID, &p.BookingID, &p.UserID, &p.Amount, &p.Currency, &p.Status,
		&p.Gateway, &p.GatewayOrderID, &p.GatewayPaymentID, &p.PaymentType, &p.CheckoutURL,
		&p.PaymentSessionID, &p.CreatedAt}

	if err := tx.QueryRow(ctx,
		`SELECT `+ret+` FROM payments WHERE booking_id = $1 AND status = 'PENDING' ORDER BY created_at DESC LIMIT 1`, bookingID,
	).Scan(dest...); err == nil {
		return &p, tx.Commit(ctx)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}

	gateway := "MOCK"
	gatewayOrderID := "order_" + randHex(12)
	checkoutURL := ""
	paymentSessionID := ""
	if s.provider != nil {
		providerOrder, err := s.provider.CreateOrder(ctx, bookingID, userID, due, "INR", "", "")
		if err != nil {
			return nil, err
		}
		gateway = "CASHFREE"
		gatewayOrderID = providerOrder.OrderID
		checkoutURL = providerOrder.CheckoutURL
		paymentSessionID = providerOrder.SessionID
	}

	if err := tx.QueryRow(ctx,
		`INSERT INTO payments (booking_id, user_id, amount, currency, gateway, gateway_order_id, payment_type, checkout_url, payment_session_id)
		 VALUES ($1, $2, $3, 'INR', $4, $5, $6, $7, $8)
		 RETURNING `+ret,
		bookingID, userID, due, gateway, gatewayOrderID, payType, checkoutURL, paymentSessionID,
	).Scan(dest...); err != nil {
		return nil, err
	}

	p.Gateway = gateway
	p.GatewayOrderID = &gatewayOrderID
	p.PaymentType = payType
	p.Currency = "INR"
	p.Amount = due
	p.CheckoutURL = checkoutURL
	p.PaymentSessionID = paymentSessionID
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &p, nil
}

type WebhookPayload struct {
	EventID   string  `json:"eventId"`
	OrderID   string  `json:"orderId"`
	PaymentID string  `json:"paymentId"`
	Status    string  `json:"status"`
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
	_, _ = m.Write(body)
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
		return nil
	}

	var paymentID, bookingID, status string
	var amount float64
	if err := tx.QueryRow(ctx,
		`SELECT id, booking_id, status, amount FROM payments
		 WHERE gateway_order_id = $1 FOR UPDATE`, p.OrderID,
	).Scan(&paymentID, &bookingID, &status, &amount); errors.Is(err, pgx.ErrNoRows) {
		return apperr.BadRequest("UNKNOWN_ORDER", "Unknown order")
	} else if err != nil {
		return err
	}
	if status != "PENDING" {
		return nil
	}

	switch p.Status {
	case "SUCCESS":
		if _, err := tx.Exec(ctx,
			`UPDATE payments SET status = 'SUCCESS', gateway_payment_id = $2,
			    updated_at = CURRENT_TIMESTAMP WHERE id = $1`, paymentID, p.PaymentID); err != nil {
			return err
		}
		var wasStatus string
		var total, paid float64
		if err := tx.QueryRow(ctx,
			`SELECT status, total_amount, COALESCE(paid_amount,0) FROM bookings WHERE id = $1 FOR UPDATE`,
			bookingID,
		).Scan(&wasStatus, &total, &paid); err != nil {
			return err
		}
		if paid+amount > total {
			logger.Warn("payment: overpayment", "bookingId", bookingID, "paymentId", paymentID,
				"total", total, "paidBefore", paid, "amount", amount)
		}
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
	if amount <= 0 || math.IsNaN(amount) || math.IsInf(amount, 0) {
		return "", apperr.BadRequest("INVALID_AMOUNT", "Refund amount must be positive")
	}
	if _, err := s.db.Exec(ctx, "SELECT 1"); err != nil {
		return "", err
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	var paid float64
	var status, bookingID string
	var venueOwner int64
	if err := tx.QueryRow(ctx,
		`SELECT p.amount, p.status, p.booking_id, f.owner_id
		   FROM payments p
		   JOIN bookings b ON b.id = p.booking_id
		   JOIN facilities f ON f.id = b.target_id
		  WHERE p.id = $1 FOR UPDATE OF p`, paymentID,
	).Scan(&paid, &status, &bookingID, &venueOwner); errors.Is(err, pgx.ErrNoRows) {
		return "", apperr.BadRequest("PAYMENT_NOT_FOUND", "Payment not found")
	} else if err != nil {
		return "", err
	}
	if !isAdmin && venueOwner != callerID {
		return "", apperr.BadRequest("PAYMENT_NOT_FOUND", "Payment not found")
	}
	if status != "SUCCESS" && status != "PARTIALLY_REFUNDED" {
		return "", apperr.Conflict("INVALID_STATE", "Only a successful payment can be refunded")
	}

	var alreadyRefunded float64
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(sum(amount),0) FROM refunds WHERE payment_id = $1 AND status <> 'FAILED'`, paymentID,
	).Scan(&alreadyRefunded); err != nil {
		return "", err
	}
	if alreadyRefunded+amount > paid {
		return "", apperr.BadRequest("REFUND_EXCEEDS_PAYMENT", "Refund would exceed the amount paid")
	}

	var refundID string
	if err := tx.QueryRow(ctx,
		`INSERT INTO refunds (payment_id, amount, reason, status, gateway_refund_id)
		 VALUES ($1, $2, $3, 'SUCCESS', $4) RETURNING id`,
		paymentID, amount, reason, "rfnd_"+randHex(10),
	).Scan(&refundID); err != nil {
		return "", err
	}

	newStatus := "PARTIALLY_REFUNDED"
	if alreadyRefunded+amount >= paid {
		newStatus = "REFUNDED"
	}
	if _, err := tx.Exec(ctx,
		`UPDATE payments SET status = $2, updated_at = CURRENT_TIMESTAMP WHERE id = $1`, paymentID, newStatus); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE bookings SET paid_amount = GREATEST(paid_amount - $2, 0), updated_at = CURRENT_TIMESTAMP WHERE id = $1`, bookingID, amount); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return refundID, nil
}

func (s *Service) UpsertPolicy(ctx context.Context, scope, facilityID string, policy Policy) (Policy, error) {
	if s == nil {
		return DefaultPolicy(), nil
	}
	p := policy.normalized()
	if strings.TrimSpace(scope) == "" {
		scope = "MARKETPLACE"
	}

	var rowID string
	if scope == "VENUE" {
		if strings.TrimSpace(facilityID) == "" {
			return DefaultPolicy(), apperr.BadRequest("INVALID_POLICY", "facilityId is required for venue policy")
		}
		if err := s.db.QueryRow(ctx, `
			INSERT INTO payment_policies (scope, facility_id, advance_percent, min_advance_amount, max_advance_amount,
				balance_due_hours_before_event, cancellation_window_hours, cancellation_penalty_percent, is_active, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, TRUE, CURRENT_TIMESTAMP)
			ON CONFLICT (scope, facility_id) DO UPDATE SET
				advance_percent = EXCLUDED.advance_percent,
				min_advance_amount = EXCLUDED.min_advance_amount,
				max_advance_amount = EXCLUDED.max_advance_amount,
				balance_due_hours_before_event = EXCLUDED.balance_due_hours_before_event,
				cancellation_window_hours = EXCLUDED.cancellation_window_hours,
				cancellation_penalty_percent = EXCLUDED.cancellation_penalty_percent,
				updated_at = CURRENT_TIMESTAMP,
				is_active = TRUE
			RETURNING id::text`,
			scope, facilityID, p.AdvancePercent, p.MinAdvanceAmount, p.MaxAdvanceAmount,
			p.BalanceDueHoursBeforeEvent, p.CancellationWindowHours, p.CancellationPenaltyPercent,
		).Scan(&rowID); err != nil {
			return DefaultPolicy(), err
		}
	} else {
		if err := s.db.QueryRow(ctx, `
			INSERT INTO payment_policies (scope, facility_id, advance_percent, min_advance_amount, max_advance_amount,
				balance_due_hours_before_event, cancellation_window_hours, cancellation_penalty_percent, is_active, updated_at)
			VALUES ($1, NULL, $2, $3, $4, $5, $6, $7, TRUE, CURRENT_TIMESTAMP)
			ON CONFLICT (scope, facility_id) DO UPDATE SET
				advance_percent = EXCLUDED.advance_percent,
				min_advance_amount = EXCLUDED.min_advance_amount,
				max_advance_amount = EXCLUDED.max_advance_amount,
				balance_due_hours_before_event = EXCLUDED.balance_due_hours_before_event,
				cancellation_window_hours = EXCLUDED.cancellation_window_hours,
				cancellation_penalty_percent = EXCLUDED.cancellation_penalty_percent,
				updated_at = CURRENT_TIMESTAMP,
				is_active = TRUE
			RETURNING id::text`,
			scope, p.AdvancePercent, p.MinAdvanceAmount, p.MaxAdvanceAmount,
			p.BalanceDueHoursBeforeEvent, p.CancellationWindowHours, p.CancellationPenaltyPercent,
		).Scan(&rowID); err != nil {
			return DefaultPolicy(), err
		}
	}
	_ = rowID
	return p, nil
}

func (s *Service) ResolvePolicy(ctx context.Context, facilityID string) (Policy, error) {
	if s == nil {
		return DefaultPolicy(), nil
	}
	if strings.TrimSpace(facilityID) == "" {
		return s.Policy(), nil
	}
	return s.LoadPolicyForFacility(ctx, facilityID)
}

func (s *Service) Pool() *pgxpool.Pool {
	if s == nil {
		return nil
	}
	return s.db
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return hex.EncodeToString([]byte(time.Now().String()))
	}
	return hex.EncodeToString(b)
}
