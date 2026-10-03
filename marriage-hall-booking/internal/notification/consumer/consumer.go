// Package consumer is every notification reaction to a domain event.
//
// The worker calls Handle for each message it reads from Kafka; the API calls
// the same Handle in-process when Kafka is disabled or the broker refused a
// write. One implementation, so the two paths cannot drift.
package consumer

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/notification/repository"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/notification/service"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/events"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/logger"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/notify"
)

// Topics is what Handle understands; the worker subscribes to exactly these.
// Media uploads are not here - they need a storage backend and are the
// worker's own consumer.
var Topics = []string{
	events.TopicUserRegistered, events.TopicBookingCreated, events.TopicBookingCancelled,
	events.TopicBookingExpired, events.TopicPaymentCompleted, events.TopicPaymentFailed,
	events.TopicAdminStatusChanged, events.TopicCouponCreated, events.TopicReviewCreated,
	events.TopicFeedbackSubmitted, events.TopicFacilityCreated, events.TopicAmenitiesAdded,
}

type Consumer struct {
	n  *service.Service
	db *pgxpool.Pool
}

func New(n *service.Service, db *pgxpool.Pool) *Consumer {
	return &Consumer{n: n, db: db}
}

// Handle returns nil once the event needs nothing further, a retryable error
// when it should be tried again (the database is down), and an error wrapping
// events.ErrPermanent when no retry can help.
//
// Redelivery is safe: every write lands on the outbox unique key, so a second
// delivery of the same event enqueues nothing.
func (c *Consumer) Handle(ctx context.Context, e events.Envelope) error {
	switch e.Type {
	case events.TopicUserRegistered:
		var p events.UserRegistered
		if err := e.Decode(&p); err != nil {
			return err
		}
		logger.Info("notify: welcome email", "userId", p.UserID)
		return nil

	case events.TopicBookingCreated:
		p, err := bookingOf(e)
		if err != nil {
			return err
		}
		// A booking that is not against a facility (or was already deleted)
		// has no owner to chase. Nothing to retry: it is not coming back.
		if err := c.n.EnqueueBookingCreated(ctx, p.BookingID); errors.Is(err, repository.ErrNotFound) {
			logger.Warn("notify: booking not found, skipping", "bookingId", p.BookingID)
			return nil
		} else if err != nil {
			return fmt.Errorf("enqueue booking %s: %w", p.BookingID, err)
		}
		return nil

	case events.TopicBookingCancelled, events.TopicBookingExpired:
		// Either way nobody needs to answer for this booking any more.
		p, err := bookingOf(e)
		if err != nil {
			return err
		}
		logger.Info("notify: stop chasing", "event", e.Type, "bookingId", p.BookingID)
		return c.n.Cancel(ctx, p.BookingID)

	case events.TopicPaymentCompleted:
		p, err := bookingOf(e)
		if err != nil {
			return err
		}
		logger.Info("notify: payment receipt", "bookingId", p.BookingID, "paymentId", p.PaymentID)
		if err := c.n.NotifyPaymentReceived(ctx, p.BookingID, p.PaymentID, p.Amount); errors.Is(err, repository.ErrNotFound) {
			logger.Warn("notify: payment for unknown booking, skipping", "bookingId", p.BookingID)
			return nil
		} else if err != nil {
			return err
		}
		return nil

	case events.TopicPaymentFailed:
		p, err := bookingOf(e)
		if err != nil {
			return err
		}
		logger.Warn("notify: payment failed", "bookingId", p.BookingID, "reason", p.Reason)
		return nil

	case events.TopicAdminStatusChanged:
		var p events.AdminStatusChanged
		if err := e.Decode(&p); err != nil {
			return err
		}
		return c.statusChanged(ctx, p)

	case events.TopicCouponCreated:
		var p events.CouponCreated
		if err := e.Decode(&p); err != nil {
			return err
		}
		if p.FacilityID != "" {
			c.n.AnnounceFacilityNearby(ctx, p.FacilityID, "has a new offer: "+p.Code, "coupon:"+p.CouponID)
			return nil
		}
		// No venue to measure a radius from: an all-halls coupon is for every
		// customer. Push only, the same rule as the geo announcements.
		_, err := c.n.AnnounceToCustomers(ctx, "coupon.all_halls", p.CouponID,
			"New offer: "+p.Code,
			fmt.Sprintf("Get %s on any marriage hall. Use code %s at checkout.", offerText(p), p.Code))
		return err

	case events.TopicAmenitiesAdded:
		var p events.AmenitiesAdded
		if err := e.Decode(&p); err != nil {
			return err
		}
		if len(p.Names) == 0 {
			return nil
		}
		// Told to nearby customers, not to admins - an amenity needs no approval.
		c.n.AnnounceFacilityNearby(ctx, p.FacilityID,
			"now offers "+strings.Join(p.Names, ", "),
			"amenities:"+strings.Join(p.Names, ","))
		return nil

	case events.TopicReviewCreated:
		var p events.ReviewCreated
		if err := e.Decode(&p); err != nil {
			return err
		}
		return c.reviewCreated(ctx, p)

	case events.TopicFeedbackSubmitted:
		var p events.FeedbackSubmitted
		if err := e.Decode(&p); err != nil {
			return err
		}
		subject := "[ops] New app feedback"
		if p.Rating != nil {
			subject = fmt.Sprintf("[ops] New app feedback (%d/5)", *p.Rating)
		}
		return c.n.NotifyAdmins(ctx, service.Event{
			Type: "feedback.submitted", SubjectID: p.FeedbackID,
			Subject: subject, Body: p.Message,
		})

	case events.TopicFacilityCreated:
		var p events.FacilityCreated
		if err := e.Decode(&p); err != nil {
			return err
		}
		return c.n.NotifyAdmins(ctx, service.Event{
			Type:      "facility.created",
			SubjectID: p.FacilityID,
			Subject:   "[ops] New listing awaiting approval: " + p.Name,
			Body: fmt.Sprintf("A new facility has been submitted and is waiting for approval.\n\n%s\nFacility ref: %s\n\nIt stays invisible to customers until approved.",
				p.Name, p.FacilityID),
		})
	}
	return fmt.Errorf("%w: unhandled event type %q", events.ErrPermanent, e.Type)
}

// offerText is "20% off (up to ₹5,000)" or "₹2,000 off".
func offerText(p events.CouponCreated) string {
	if p.DiscountType == "PERCENT" {
		s := strconv.FormatFloat(p.DiscountValue, 'f', -1, 64) + "% off"
		if p.MaxDiscount != nil {
			s += " (up to ₹" + rupees(*p.MaxDiscount) + ")"
		}
		return s
	}
	return "₹" + rupees(p.DiscountValue) + " off"
}

// rupees groups whole rupees the Indian way: 1,50,000.
func rupees(v float64) string {
	s := strconv.FormatInt(int64(math.Round(v)), 10)
	if len(s) <= 3 {
		return s
	}
	head, tail := s[:len(s)-3], s[len(s)-3:]
	var parts []string
	for len(head) > 2 {
		parts = append([]string{head[len(head)-2:]}, parts...)
		head = head[:len(head)-2]
	}
	parts = append([]string{head}, parts...)
	return strings.Join(parts, ",") + "," + tail
}

// bookingOf decodes a booking/payment payload. One without a booking id can
// never be acted on, however often it is retried.
func bookingOf(e events.Envelope) (events.Booking, error) {
	var p events.Booking
	if err := e.Decode(&p); err != nil {
		return p, err
	}
	if p.BookingID == "" {
		return p, fmt.Errorf("%w: %s without bookingId", events.ErrPermanent, e.Type)
	}
	return p, nil
}

// statusChanged turns one admin decision into a notification the affected user
// can act on. Wording matters more than usual here: these are the messages
// that tell someone their livelihood listing was rejected.
func (c *Consumer) statusChanged(ctx context.Context, ev events.AdminStatusChanged) error {
	var subject, body string
	// A blocked user's sessions are revoked as part of the block, so push and
	// anything in-app is unreadable by the time it arrives. Email and SMS are
	// the only channels that still reach them.
	var channels []notify.Channel

	switch ev.Entity {
	case "vendor.kyc":
		if ev.Status == "APPROVED" {
			subject = "Your KYC has been approved"
			body = "Good news - your KYC verification for " + ev.Name + " has been approved.\n\nYou can now publish listings and accept bookings."
		} else {
			subject = "Your KYC needs attention"
			body = "Your KYC verification for " + ev.Name + " was not approved."
			if ev.Reason != "" {
				body += "\n\nReason: " + ev.Reason
			}
			body += "\n\nYou can correct the details and submit again."
		}
	case "facility":
		switch ev.Status {
		case "APPROVED":
			subject = "Your listing is live: " + ev.Name
			body = ev.Name + " has been approved and is now visible to customers."
			// Announced on approval rather than on creation: a PENDING venue is
			// invisible to customers, so telling them about it sends them to a
			// listing they cannot open.
			c.n.AnnounceFacilityNearby(ctx, ev.EntityID, "is a new venue near you", "approved")
		case "REJECTED":
			subject = "Your listing was not approved: " + ev.Name
			body = ev.Name + " was not approved."
			if ev.Reason != "" {
				body += "\n\nReason: " + ev.Reason
			}
		case "BLOCKED":
			subject = "Your listing has been suspended: " + ev.Name
			body = ev.Name + " has been suspended and is no longer visible to customers."
		default:
			// PENDING and anything added later: no message worth sending.
			return nil
		}
	case "user":
		if ev.Status == "SUSPENDED" {
			subject = "Your account has been suspended"
			body = "Your account has been suspended and you have been signed out."
			if ev.Reason != "" {
				body += "\n\nReason: " + ev.Reason
			}
			body += "\n\nContact support if you believe this is a mistake."
			channels = []notify.Channel{notify.Email, notify.SMS}
		} else {
			subject = "Your account has been reactivated"
			body = "Your account is active again. You can sign in as usual."
		}
	default:
		return nil
	}

	return c.n.NotifyUser(ctx, service.Event{
		Type:      ev.Entity + "." + strings.ToLower(ev.Status),
		SubjectID: ev.EntityID,
		UserID:    ev.UserID,
		Subject:   subject,
		Body:      body,
		Channels:  channels,
	})
}

// reviewCreated tells a venue owner they have a new review. The owner is looked
// up here rather than carried in the event: the review handler has no reason to
// know who owns the facility.
func (c *Consumer) reviewCreated(ctx context.Context, p events.ReviewCreated) error {
	var ownerID int64
	var name string
	if err := c.db.QueryRow(ctx,
		`SELECT owner_id, name FROM facilities WHERE id = $1 AND is_deleted = FALSE`,
		p.FacilityID).Scan(&ownerID, &name); errors.Is(err, pgx.ErrNoRows) {
		logger.Warn("notify: review for a deleted facility, skipping", "facilityId", p.FacilityID)
		return nil
	} else if err != nil {
		return fmt.Errorf("review owner lookup %s: %w", p.FacilityID, err)
	}
	// Keyed on the review. The old key, facility+rating, made a venue's second
	// 5-star review collide with its first on the outbox unique index, so the
	// owner was never told about it.
	return c.n.NotifyUser(ctx, service.Event{
		Type:      "review.created",
		SubjectID: p.ReviewID,
		UserID:    ownerID,
		Subject:   fmt.Sprintf("New %d-star review for %s", p.Rating, name),
		Body: fmt.Sprintf("%s received a new %d-star review.\n\nOpen the app to read it and reply.",
			name, p.Rating),
	})
}
