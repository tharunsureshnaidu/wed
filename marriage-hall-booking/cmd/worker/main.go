// Command worker consumes domain events and runs periodic maintenance.
//
// It is a separate process from the API so that slow work (sending mail,
// sweeping expired holds) can never add latency to a user's request.
package main

import (
	"context"
	"encoding/json"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/segmentio/kafka-go"

	bookingrepo "github.com/tripfcatory/marriage-hall-booking/internal/booking/repository"
	"github.com/tripfcatory/marriage-hall-booking/pkg/config"
	"github.com/tripfcatory/marriage-hall-booking/pkg/database"
	"github.com/tripfcatory/marriage-hall-booking/pkg/events"
	"github.com/tripfcatory/marriage-hall-booking/pkg/logger"
)

func main() {
	logger.Init("worker")
	cfg := config.Load()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	db, err := database.New(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Fatal("database", logger.Err(err))
	}
	defer db.Close()

	bookings := bookingrepo.New(db)

	// Sweeper: release slots held by bookings that were never paid for. This is
	// what stops an abandoned checkout from blocking a date forever.
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				n, err := bookings.ExpireHolds(ctx)
				if err != nil {
					logger.Error("expire holds", logger.Err(err))
					continue
				}
				if n > 0 {
					logger.Info("released unpaid bookings", "count", n)
				}
			}
		}
	}()

	topics := []string{
		events.TopicUserRegistered,
		events.TopicBookingCreated,
		events.TopicBookingCancelled,
		events.TopicPaymentCompleted,
		events.TopicPaymentFailed,
	}
	for _, topic := range topics {
		go consume(ctx, cfg.KafkaBrokers, topic)
	}

	logger.Info("worker started", "kafka", cfg.KafkaBrokers)
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	logger.Info("worker stopping")
}

func consume(ctx context.Context, brokers []string, topic string) {
	// One consumer group per topic. A single group spanning several single-partition
	// topics leaves the group permanently rebalancing as its members contend for
	// assignments, and nothing is ever delivered (observed live: the worker sat in
	// "rebalancing" and consumed zero messages).
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers: brokers,
		Topic:   topic,
		GroupID: "booking-worker-" + topic,
		// Start from the beginning so events published while the worker was
		// down are still handled.
		StartOffset: kafka.FirstOffset,
		// No Logger/ErrorLogger: kafka-go would otherwise print a running
		// commentary of group coordination and offset commits. Read errors are
		// logged by this loop, at WARN.
	})
	defer r.Close()

	// ponytail: one line per outage, not one per retry. With the broker down
	// every topic goroutine re-reads every 2s, which used to bury the real log
	// under identical "connection refused" lines.
	failing := false
	for {
		m, err := r.ReadMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if !failing {
				failing = true
				logger.Warn("kafka unreachable, retrying", "topic", topic, logger.Err(err))
			}
			time.Sleep(2 * time.Second)
			continue
		}
		if failing {
			failing = false
			logger.Info("kafka reconnected", "topic", topic)
		}
		var e events.Envelope
		if err := json.Unmarshal(m.Value, &e); err != nil {
			logger.Error("malformed event", "topic", topic, logger.Err(err))
			continue
		}
		handle(e)
	}
}

// handle is where notifications would be dispatched. There is no mail or SMS
// provider configured, so for now each event is logged - the consumer, topics
// and offsets are real, only the delivery side is a stub.
func handle(e events.Envelope) {
	switch e.Type {
	case events.TopicUserRegistered:
		logger.Info("notify: welcome email", "userId", e.Payload["userId"])
	case events.TopicBookingCreated:
		logger.Info("notify: booking confirmation",
			"bookingId", e.Payload["bookingId"], "amount", e.Payload["totalAmount"])
	case events.TopicBookingCancelled:
		logger.Info("notify: cancellation", "bookingId", e.Payload["bookingId"])
	case events.TopicPaymentCompleted:
		logger.Info("notify: payment receipt",
			"bookingId", e.Payload["bookingId"], "paymentId", e.Payload["paymentId"])
	case events.TopicPaymentFailed:
		logger.Warn("notify: payment failed",
			"bookingId", e.Payload["bookingId"], "reason", e.Payload["reason"])
	default:
		logger.Warn("unhandled event type", "type", e.Type)
	}
}
