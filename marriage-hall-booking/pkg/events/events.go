// Package events publishes domain events to Kafka.
package events

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/logger"
	"time"

	"github.com/segmentio/kafka-go"
)

const (
	TopicUserRegistered   = "user.registered"
	TopicBookingCreated   = "booking.created"
	TopicBookingCancelled = "booking.cancelled"
	TopicPaymentCompleted = "payment.completed"
	TopicPaymentFailed    = "payment.failed"

	// TopicMediaUploadRequested carries a reference to a file already written
	// to the spool directory - never the bytes. Kafka's default max message is
	// 1MB and a compressed venue photo is often larger, so a payload carrying
	// the image would simply be rejected; brokers are also a poor place to
	// store blobs that a filesystem holds for free.
	TopicMediaUploadRequested = "media.upload.requested"
)

// topics is every topic above; ensureTopics creates them at start-up.
var topics = []string{TopicUserRegistered, TopicBookingCreated, TopicBookingCancelled,
	TopicPaymentCompleted, TopicPaymentFailed, TopicMediaUploadRequested}

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
	Type       string         `json:"type"`
	OccurredAt time.Time      `json:"occurredAt"`
	Payload    map[string]any `json:"payload"`
}

type Publisher struct {
	writer *kafka.Writer
	// Enabled is false when no brokers are configured; publishing then becomes a
	// no-op so the API runs without Kafka.
	Enabled bool
}

func NewPublisher(brokers []string) *Publisher {
	if len(brokers) == 0 || brokers[0] == "" {
		return &Publisher{Enabled: false}
	}
	ensureTopics(brokers)
	return &Publisher{
		Enabled: true,
		writer: &kafka.Writer{
			Addr:     kafka.TCP(brokers...),
			Balancer: &kafka.LeastBytes{},
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
	cfgs := make([]kafka.TopicConfig, len(topics))
	for i, t := range topics {
		cfgs[i] = kafka.TopicConfig{Topic: t, NumPartitions: -1, ReplicationFactor: -1}
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

// Publish never blocks the caller's request and never returns an error: a
// booking must not fail because the event bus is down. The write happens on its
// own goroutine with its own timeout, detached from the request context - using
// the request's context would cancel the publish the moment the HTTP response
// is written, which is usually before the broker has acknowledged anything.
// PublishSync publishes and reports whether the broker accepted the message.
//
// Publish is fire-and-forget, which is right for a notification - a dropped
// welcome email is a nuisance. It is wrong when the event is the only record
// that work still needs doing: a dropped media event leaves an uploaded file
// spooled forever and its row PENDING with nobody to finish it. Callers in that
// position use this and fall back to doing the work themselves.
func (p *Publisher) PublishSync(ctx context.Context, topic, key string, payload map[string]any) error {
	if !p.Enabled {
		return errors.New("publisher disabled")
	}
	body, err := json.Marshal(Envelope{
		Type: topic, OccurredAt: time.Now(), Payload: payload,
	})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return p.writer.WriteMessages(ctx, kafka.Message{
		Topic: topic, Key: []byte(key), Value: body,
	})
}

func (p *Publisher) Publish(_ context.Context, topic, key string, payload map[string]any) {
	if !p.Enabled {
		return
	}
	body, err := json.Marshal(Envelope{
		Type: topic, OccurredAt: time.Now(), Payload: payload,
	})
	if err != nil {
		logger.Error("marshal event", "topic", topic, logger.Err(err))
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := p.writer.WriteMessages(ctx, kafka.Message{
			Topic: topic, Key: []byte(key), Value: body,
		}); err != nil {
			logger.Warn("kafka publish failed (event dropped)", "topic", topic, logger.Err(err))
		}
	}()
}

func (p *Publisher) Close() error {
	if p.writer == nil {
		return nil
	}
	return p.writer.Close()
}
