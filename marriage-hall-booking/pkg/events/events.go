// Package events publishes domain events to Kafka.
package events

import (
	"context"
	"encoding/json"
	"github.com/tripfcatory/marriage-hall-booking/pkg/logger"
	"time"

	"github.com/segmentio/kafka-go"
)

const (
	TopicUserRegistered   = "user.registered"
	TopicBookingCreated   = "booking.created"
	TopicBookingCancelled = "booking.cancelled"
	TopicPaymentCompleted = "payment.completed"
	TopicPaymentFailed    = "payment.failed"
)

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

// Publish never blocks the caller's request and never returns an error: a
// booking must not fail because the event bus is down. The write happens on its
// own goroutine with its own timeout, detached from the request context - using
// the request's context would cancel the publish the moment the HTTP response
// is written, which is usually before the broker has acknowledged anything.
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
