// Command worker consumes domain events and runs periodic maintenance.
//
// It is a separate process from the API so that slow work (sending mail,
// sweeping expired holds) can never add latency to a user's request.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/segmentio/kafka-go"

	bookingrepo "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/booking/repository"
	notifyrepo "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/notification/repository"
	notifysvc "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/notification/service"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/config"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/database"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/events"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/logger"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/notify"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/storage"
)

// notifier is package-level because handle() is called from every consumer
// goroutine and threading it through each one buys nothing - there is exactly
// one, set once before the consumers start.
var notifier *notifysvc.Service

func main() {
	logger.Init("worker")
	cfg := config.Load()

	// Refuse to serve real users on a development configuration. Each of these
	// is silent at startup and only visible once a customer hits it - a dead
	// acknowledge link, an unauthenticated payment webhook, an OTP that is
	// always 000000. In development they are warnings so a laptop still starts.
	if problems := cfg.Validate(); len(problems) > 0 {
		if config.IsProduction() {
			for _, p := range problems {
				logger.Error("config", "problem", p)
			}
			logger.Fatal("refusing to start in production with an unsafe configuration",
				"problems", len(problems))
		}
		for _, p := range problems {
			logger.Warn("config (dev)", "problem", p)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	db, err := database.New(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Fatal("database", logger.Err(err))
	}
	defer db.Close()

	bookings := bookingrepo.New(db)

	// Notifications: the consumer below only writes outbox rows; this ticker
	// does the vendor calls and the retry-until-acknowledged chasing.
	notifier = notifysvc.New(notifyrepo.New(db), notify.FromEnv(nil))
	if d := envDuration("NOTIFY_RETRY_EVERY", 0); d > 0 {
		notifier.RetryEvery = d
	}
	// Tick faster than the retry interval: the tick only decides how promptly a
	// due row is noticed, RetryEvery decides how often one becomes due.
	go notifier.Run(ctx, envDuration("NOTIFY_TICK", 30*time.Second))

	// Users who never sent a location get one inferred from the venues they
	// have booked, so geo targeting is not empty on day one. Weakest source,
	// so a real fix from the app replaces it.
	if n, err := notifier.BackfillLocations(ctx); err != nil {
		logger.Error("geo: backfill locations", logger.Err(err))
	} else if n > 0 {
		logger.Info("geo: inferred locations from bookings", "users", n)
	}
	logChannels(notify.FromEnv(nil), notifier.RetryEvery)

	// Reminder sweep: the date-driven notifications nothing else can trigger.
	// Hourly rather than per-minute - these are day-granularity reminders, and
	// the outbox unique index absorbs the repeated runs within a day.
	go func() {
		ticker := time.NewTicker(envDuration("REMINDER_TICK", time.Hour))
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				n, err := notifier.SweepReminders(ctx)
				if err != nil {
					logger.Error("notify: reminder sweep", logger.Err(err))
					continue
				}
				if n > 0 {
					logger.Info("notify: reminders queued", "count", n)
				}
			}
		}
	}()

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

	// Media uploads get their own consumer: it needs the database and a storage
	// backend, and a slow S3 PUT must not delay notification events.
	media, err := storage.New(ctx, "uploads", os.Getenv("PUBLIC_BASE_URL"))
	if err != nil {
		logger.Fatal("media storage unavailable", logger.Err(err))
	}
	go consumeMediaUploads(ctx, cfg.KafkaBrokers, db, media)

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
		// On a fresh broker the topic does not exist until the API's first
		// publish auto-creates it. A group that joined before then is assigned
		// zero partitions and, without this, never rebalances - the worker runs
		// and consumes nothing (observed on a clean docker compose up).
		WatchPartitionChanges: true,
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
		handle(ctx, e)
	}
}

// handle is where notifications would be dispatched. There is no mail or SMS
// provider configured, so for now each event is logged - the consumer, topics
// and offsets are real, only the delivery side is a stub.
func handle(ctx context.Context, e events.Envelope) {
	switch e.Type {
	case events.TopicUserRegistered:
		logger.Info("notify: welcome email", "userId", e.Payload["userId"])
	case events.TopicBookingCreated:
		id, _ := e.Payload["bookingId"].(string)
		if id == "" || notifier == nil {
			return
		}
		// Enqueue only. A booking that is not against a facility (or was
		// already deleted) has no owner to chase, and is skipped rather than
		// retried - the event is not coming back.
		if err := notifier.EnqueueBookingCreated(ctx, id); err != nil {
			if errors.Is(err, notifyrepo.ErrNotFound) {
				logger.Warn("notify: booking not found, skipping", "bookingId", id)
				return
			}
			logger.Error("notify: enqueue", "bookingId", id, logger.Err(err))
		}
	case events.TopicBookingCancelled:
		id, _ := e.Payload["bookingId"].(string)
		logger.Info("notify: cancellation", "bookingId", id)
		if id != "" && notifier != nil {
			if err := notifier.Cancel(ctx, id); err != nil {
				logger.Error("notify: cancel", "bookingId", id, logger.Err(err))
			}
		}
	case events.TopicPaymentCompleted:
		bookingID, _ := e.Payload["bookingId"].(string)
		paymentID, _ := e.Payload["paymentId"].(string)
		amount, _ := e.Payload["amount"].(float64)
		logger.Info("notify: payment receipt", "bookingId", bookingID, "paymentId", paymentID)
		if bookingID != "" && notifier != nil {
			if err := notifier.NotifyPaymentReceived(ctx, bookingID, paymentID, amount); err != nil {
				logger.Error("notify: payment receipt", "bookingId", bookingID, logger.Err(err))
			}
		}
	case events.TopicPaymentFailed:
		logger.Warn("notify: payment failed",
			"bookingId", e.Payload["bookingId"], "reason", e.Payload["reason"])
	default:
		logger.Warn("unhandled event type", "type", e.Type)
	}
}

// envDuration reads a Go duration string ("45s", "30m"). An unparseable value
// falls back rather than refusing to start: a typo in an interval must not take
// the worker down.
func envDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		logger.Warn("bad duration, using default", "key", key, "value", v, "default", fallback)
		return fallback
	}
	return d
}

// logChannels says once, at startup, which channels will really send. Without
// it a log full of "would send" looks like a bug rather than missing config.
func logChannels(s notify.Senders, retry time.Duration) {
	var live, stub []string
	for _, ch := range []notify.Channel{notify.Email, notify.SMS, notify.WhatsApp, notify.Push} {
		if s[ch].Live() {
			live = append(live, string(ch))
		} else {
			stub = append(stub, string(ch))
		}
	}
	logger.Info("notify: channels ready", "live", live, "log-only", stub, "retryEvery", retry)
}
