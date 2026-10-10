// Package events publishes domain events to Kafka.
//
// The API only publishes; the worker reacts. Every reaction to a domain event -
// the owner's booking notification, the nearby-customer announcement, the ops
// email about new feedback - runs in the worker's consumer, never on the
// request that caused it.
package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"
	"time"

	"github.com/segmentio/kafka-go"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/logger"
)

const (
	TopicUserRegistered   = "user.registered"
	TopicBookingCreated   = "booking.created"
	TopicBookingCancelled = "booking.cancelled"
	// TopicBookingExpired is published by the worker's hold sweeper. Without it
	// an unpaid, expired booking kept chasing its owner for an answer.
	TopicBookingExpired   = "booking.expired"
	TopicPaymentCompleted = "payment.completed"
	TopicPaymentFailed    = "payment.failed"

	TopicAdminStatusChanged = "admin.status.changed"
	TopicCouponCreated      = "coupon.created"
	TopicReviewCreated      = "review.created"
	TopicFeedbackSubmitted  = "feedback.submitted"
	TopicFacilityCreated    = "facility.created"
	TopicAmenitiesAdded     = "facility.amenities.added"

	// TopicMediaUploadRequested carries a reference to a file already written
	// to the spool directory - never the bytes. Kafka's default max message is
	// 1MB and a compressed venue photo is often larger, so a payload carrying
	// the image would simply be rejected; brokers are also a poor place to
	// store blobs that a filesystem holds for free.
	TopicMediaUploadRequested = "media.upload.requested"
)

// DLQSuffix names the dead-letter topic of every topic: an event whose handler
// still fails after its retries is parked on "<topic>.dlq" with the error in a
// header, so one poison event cannot block its topic and is never lost.
const DLQSuffix = ".dlq"

// Topics is every topic above; ensureTopics creates them, and their dead-letter
// topics, at start-up.
var Topics = []string{TopicUserRegistered, TopicBookingCreated, TopicBookingCancelled,
	TopicBookingExpired, TopicPaymentCompleted, TopicPaymentFailed,
	TopicAdminStatusChanged, TopicCouponCreated, TopicReviewCreated,
	TopicFeedbackSubmitted, TopicFacilityCreated, TopicAmenitiesAdded,
	TopicMediaUploadRequested}

// ErrPermanent marks a failure that no retry can fix - a malformed message, a
// payload missing its id. The consumer sends it straight to the dead-letter
// topic instead of retrying.
var ErrPermanent = errors.New("permanent")

// Booking is the payload of every booking.* and payment.* event. Not every
// field is set on every topic; the json names match what has always been on
// the wire, so events already in the log still decode.
type Booking struct {
	BookingID   string  `json:"bookingId"`
	UserID      int64   `json:"userId,omitempty"`
	TargetID    string  `json:"targetId,omitempty"`
	TotalAmount float64 `json:"totalAmount,omitempty"`
	PaymentID   string  `json:"paymentId,omitempty"`
	Amount      float64 `json:"amount,omitempty"`
	Reason      string  `json:"reason,omitempty"`
}

type UserRegistered struct {
	UserID    int64  `json:"userId"`
	FirstName string `json:"firstName"`
}

// AdminStatusChanged is one admin decision about a vendor's KYC, a facility or
// a user account.
type AdminStatusChanged struct {
	Entity   string `json:"entity"` // "vendor.kyc", "facility", "user"
	EntityID string `json:"entityId"`
	UserID   int64  `json:"userId"`
	Status   string `json:"status"`
	Reason   string `json:"reason,omitempty"`
	Name     string `json:"name,omitempty"`
}

// CouponCreated announces a coupon. An empty FacilityID is an all-halls
// coupon, told to every customer; otherwise customers near that venue.
type CouponCreated struct {
	CouponID      string   `json:"couponId"`
	Code          string   `json:"code"`
	FacilityID    string   `json:"facilityId,omitempty"`
	DiscountType  string   `json:"discountType,omitempty"`
	DiscountValue float64  `json:"discountValue,omitempty"`
	MaxDiscount   *float64 `json:"maxDiscount,omitempty"`
	CreatedBy     int64    `json:"createdBy"`
}

type ReviewCreated struct {
	ReviewID   string `json:"reviewId"`
	FacilityID string `json:"facilityId"`
	Rating     int    `json:"rating"`
}

type FeedbackSubmitted struct {
	FeedbackID string `json:"feedbackId"`
	Message    string `json:"message"`
	Rating     *int   `json:"rating,omitempty"`
}

type FacilityCreated struct {
	FacilityID string `json:"facilityId"`
	Name       string `json:"name"`
	OwnerID    int64  `json:"ownerId"`
}

type AmenitiesAdded struct {
	FacilityID string   `json:"facilityId"`
	Names      []string `json:"names"`
}

// MediaUpload is the payload of TopicMediaUploadRequested. Every field is
// small: the worker reads SpoolPath off disk and uploads it under Key.
type MediaUpload struct {
	MediaID     string `json:"mediaId"`
	Table       string `json:"table"` // facility_images or facility_videos
	FacilityID  string `json:"facilityId"`
	VendorID    string `json:"vendorId"`
	SpoolPath   string `json:"spoolPath"`
	ContentType string `json:"contentType"`
	Ext         string `json:"ext"`
	Size        int64  `json:"size"`
	Kind        int    `json:"kind"` // storage.Kind
}

type Envelope struct {
	Type       string          `json:"type"`
	OccurredAt time.Time       `json:"occurredAt"`
	Payload    json.RawMessage `json:"payload"`
}

// Decode unmarshals the payload. A payload that does not decode never will, so
// the error is permanent.
func (e Envelope) Decode(v any) error {
	if err := json.Unmarshal(e.Payload, v); err != nil {
		return fmt.Errorf("%w: payload: %w", ErrPermanent, err)
	}
	return nil
}

// Handler reacts to one event. A nil return means done; an error wrapping
// ErrPermanent is dead-lettered at once, any other error is retried.
type Handler func(ctx context.Context, e Envelope) error

type Publisher struct {
	writer *kafka.Writer
	// Enabled is false when no brokers are configured.
	Enabled bool

	// Local handles an event in this process when it cannot go through Kafka:
	// Kafka disabled, or the broker refused the write. The reactions are the
	// same code either way, so a laptop without Kafka still gets its
	// notifications, and a broker outage delays nothing. Handlers are
	// idempotent (the outbox unique key), so a write that timed out but did
	// land, and is then also handled here, costs nothing.
	Local Handler

	// inflight lets Close wait for publishes still on their goroutine, so a
	// shutdown does not drop the events of the last requests it served.
	inflight sync.WaitGroup
}

func NewPublisher(brokers []string) *Publisher {
	if len(brokers) == 0 || brokers[0] == "" {
		return &Publisher{Enabled: false}
	}
	ensureTopics(brokers)
	return &Publisher{
		Enabled: true,
		writer: &kafka.Writer{
			Addr: kafka.TCP(brokers...),
			// Hash, not LeastBytes: every event is keyed by the thing it is
			// about, and only a key-hashing balancer keeps one booking's events
			// on one partition, in order, once a topic has more than one.
			Balancer: &kafka.Hash{},
			// Deliberately no Logger: kafka-go's own broker chatter (metadata
			// refreshes, connection churn) drowns the application's log and
			// says nothing actionable. Publish failures are reported by this
			// package instead, at WARN.
			// Topics are created on demand rather than by a separate admin step.
			AllowAutoTopicCreation: true,
			// Synchronous, with retries. Auto-creation makes the FIRST write to a
			// new topic fail with UNKNOWN_TOPIC_OR_PARTITION - the topic is being
			// created as that very request is rejected. In async mode that write
			// is simply lost (found live: every booking.created event was dropped
			// while the topic itself existed afterwards). Sync + retries lets the
			// write land on the second attempt, once the metadata has propagated.
			Async:       false,
			MaxAttempts: 5,
			// A sync write of one message otherwise waits out kafka-go's default
			// 1s batch window for 99 messages that never come - a full second
			// added to every media upload request.
			BatchTimeout: 10 * time.Millisecond,
			// Bounded so a broker outage cannot stall a request indefinitely; the
			// caller publishes from a goroutine anyway.
			WriteTimeout: 5 * time.Second,
			RequiredAcks: kafka.RequireOne,
		},
	}
}

// ensureTopics creates the topics up front. Relying on auto-creation alone
// drops the first event of every type on a fresh broker: the sync retries below
// lose the race against metadata propagation (observed on a clean docker
// compose up - user.registered was dropped with UNKNOWN_TOPIC_OR_PARTITION). It
// also lets a worker that starts later join a group that has partitions.
//
// Best-effort: a broker that is down now is not fatal, and auto-creation stays
// on as the fallback. -1 takes the broker's own partition/replication defaults,
// exactly what auto-creation would have used.
func ensureTopics(brokers []string) {
	var cfgs []kafka.TopicConfig
	for _, t := range Topics {
		for _, name := range []string{t, t + DLQSuffix} {
			cfgs = append(cfgs, kafka.TopicConfig{Topic: name, NumPartitions: -1, ReplicationFactor: -1})
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := &kafka.Client{Addr: kafka.TCP(brokers...)}
	res, err := c.CreateTopics(ctx, &kafka.CreateTopicsRequest{Topics: cfgs})
	if err != nil {
		logger.Warn("kafka topics not created, relying on auto-creation", logger.Err(err))
		return
	}
	for t, e := range res.Errors {
		if e != nil && !errors.Is(e, kafka.TopicAlreadyExists) {
			logger.Warn("kafka topic not created, relying on auto-creation", "topic", t, logger.Err(e))
		}
	}
}

func envelope(topic string, payload any) ([]byte, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return json.Marshal(Envelope{Type: topic, OccurredAt: time.Now(), Payload: raw})
}

// PublishSync publishes and reports whether the broker accepted the message.
//
// Publish is fire-and-forget, which is right for a notification. It is wrong
// when the event is the only record that work still needs doing: a dropped
// media event leaves an uploaded file spooled forever and its row PENDING with
// nobody to finish it. Callers in that position use this and fall back to doing
// the work themselves - so Local is not used here.
func (p *Publisher) PublishSync(ctx context.Context, topic, key string, payload any) error {
	if !p.Enabled {
		return errors.New("publisher disabled")
	}
	body, err := envelope(topic, payload)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return p.writer.WriteMessages(ctx, kafka.Message{
		Topic: topic, Key: []byte(key), Value: body,
	})
}

// Publish never blocks the caller's request and never returns an error: a
// booking must not fail because the event bus is down. The write happens on its
// own goroutine with its own timeout, detached from the request context - using
// the request's context would cancel the publish the moment the HTTP response
// is written, which is usually before the broker has acknowledged anything.
func (p *Publisher) Publish(_ context.Context, topic, key string, payload any) {
	body, err := envelope(topic, payload)
	if err != nil {
		logger.Error("marshal event", "topic", topic, logger.Err(err))
		return
	}

	p.inflight.Add(1)
	go func() {
		defer p.inflight.Done()
		if p.Enabled {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			err := p.writer.WriteMessages(ctx, kafka.Message{
				Topic: topic, Key: []byte(key), Value: body,
			})
			cancel()
			if err == nil {
				return
			}
			logger.Warn("kafka publish failed, handling in-process", "topic", topic, logger.Err(err))
		}
		p.handleLocal(topic, body)
	}()
}

// handleLocal runs the event through Local, decoding the same bytes the
// consumer would have read, so both paths exercise one serialisation.
func (p *Publisher) handleLocal(topic string, body []byte) {
	if p.Local == nil {
		logger.Warn("event dropped: no broker and no local handler", "topic", topic)
		return
	}
	var e Envelope
	if err := json.Unmarshal(body, &e); err != nil {
		logger.Error("local event", "topic", topic, logger.Err(err))
		return
	}
	// Longer than a publish: a nearby announcement fans out to every customer
	// within the radius.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// This runs on a bare goroutine, outside the HTTP Recover middleware: a
	// panic in any reaction would otherwise take the whole API down.
	defer func() {
		if v := recover(); v != nil {
			logger.Error("local event handler panicked (event dropped)", "topic", topic,
				"panic", v, "stack", string(debug.Stack()))
		}
	}()
	if err := p.Local(ctx, e); err != nil {
		logger.Error("local event handler failed (event dropped)", "topic", topic, logger.Err(err))
	}
}

// DeadLetter parks a message that could not be handled on "<topic>.dlq", with
// the reason and its origin in headers so it can be inspected and replayed.
func (p *Publisher) DeadLetter(ctx context.Context, m kafka.Message, cause error) error {
	if !p.Enabled {
		return errors.New("publisher disabled")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return p.writer.WriteMessages(ctx, kafka.Message{
		Topic: m.Topic + DLQSuffix, Key: m.Key, Value: m.Value,
		Headers: append(m.Headers,
			kafka.Header{Key: "dlq-error", Value: []byte(cause.Error())},
			kafka.Header{Key: "dlq-origin", Value: []byte(m.Topic)},
			kafka.Header{Key: "dlq-at", Value: []byte(time.Now().UTC().Format(time.RFC3339))}),
	})
}

// Close waits for publishes still in flight - each is bounded by its own
// timeout - then closes the writer.
func (p *Publisher) Close() error {
	p.inflight.Wait()
	if p.writer == nil {
		return nil
	}
	return p.writer.Close()
}
