// Command worker consumes domain events and runs periodic maintenance.
//
// It is a separate process from the API so that slow work (sending mail,
// sweeping expired holds) can never add latency to a user's request.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/segmentio/kafka-go"

	bookingrepo "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/booking/repository"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/notification/consumer"
	notifyrepo "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/notification/repository"
	notifysvc "github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/notification/service"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/config"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/database"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/events"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/logger"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/notify"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/storage"
)

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
	notifier := notifysvc.New(notifyrepo.New(db), notify.FromEnv(nil))
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

	// Kafka's own publisher: the sweeper below announces expiries, and the
	// consumers park poison events on their dead-letter topic. Local is the
	// same handler the consumers run, so with Kafka off (or down) an expiry is
	// still handled, in-process.
	reactions := consumer.New(notifier, db)
	pub := events.NewPublisher(cfg.KafkaBrokers)
	pub.Local = reactions.Handle
	defer pub.Close()

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
				ids, err := bookings.ExpireHolds(ctx)
				for _, id := range ids {
					pub.Publish(ctx, events.TopicBookingExpired, id, events.Booking{BookingID: id})
				}
				if err != nil {
					logger.Error("expire holds", logger.Err(err))
					continue
				}
				if len(ids) > 0 {
					logger.Info("released unpaid bookings", "count", len(ids))
				}
			}
		}
	}()

	// Media uploads need a storage backend; a slow S3 PUT runs on its own
	// consumer, so it never delays a notification event.
	media, err := storage.New(ctx, "uploads", os.Getenv("PUBLIC_BASE_URL"))
	if err != nil {
		logger.Fatal("media storage unavailable", logger.Err(err))
	}

	var wg sync.WaitGroup
	if pub.Enabled {
		for _, topic := range consumer.Topics {
			wg.Add(1)
			go func() {
				defer wg.Done()
				consume(ctx, cfg.KafkaBrokers, topic, "booking-worker-"+topic, pub, reactions.Handle)
			}()
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			consume(ctx, cfg.KafkaBrokers, events.TopicMediaUploadRequested, "media-upload",
				pub, mediaUploader(db, media))
		}()
	} else {
		// No brokers: the API handles its events in-process and uploads media
		// inline, so there is nothing to consume.
		logger.Warn("kafka disabled, no consumers started")
	}

	logger.Info("worker started", "kafka", cfg.KafkaBrokers)
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	logger.Info("worker stopping")
	// Wait for the consumers to leave their groups. Returning straight away
	// skipped every reader's Close, so the broker held the dead members until
	// their 30s session timeout and the next worker consumed nothing meanwhile.
	cancel()
	wg.Wait()
}

// retryDelay is the first wait between attempts; it doubles each time. A var
// so the test does not sleep for a minute.
var retryDelay = 2 * time.Second

// maxAttempts x doubling from 2s is ~1 minute of retries - long enough to ride
// out a database restart, short enough that one poison event does not hold up
// its topic for long.
const maxAttempts = 6

// consume reads one topic at-least-once: the offset is committed only after
// the handler succeeded or the message was dead-lettered.
//
// ReadMessage used to be called here, and it commits BEFORE returning the
// message - so a handler that failed (database blip) or a worker killed
// mid-handle lost the event for good.
//
// One consumer group per topic. A single group spanning several single-partition
// topics leaves the group permanently rebalancing as its members contend for
// assignments, and nothing is ever delivered (observed live: the worker sat in
// "rebalancing" and consumed zero messages).
func consume(ctx context.Context, brokers []string, topic, group string, pub *events.Publisher, handle events.Handler) {
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers: brokers,
		Topic:   topic,
		GroupID: group,
		// Start from the beginning so events published while the worker was
		// down are still handled.
		StartOffset: kafka.FirstOffset,
		// Every message is one unit of work; nothing to gain from waiting to
		// batch reads.
		MaxWait: time.Second,
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
		m, err := r.FetchMessage(ctx)
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

		err = handleWithRetry(ctx, topic, m.Value, handle)
		if ctx.Err() != nil {
			return // shutting down: left uncommitted, redelivered on the next start
		}
		if err != nil {
			logger.Error("event failed, dead-lettering", "topic", topic,
				"offset", m.Offset, "key", string(m.Key), logger.Err(err))
			// Must land before the commit, or the event is gone. kafka-go keeps
			// fetching forward regardless of commits, so the only way to not
			// lose it is to not move on.
			for pub.DeadLetter(ctx, m, err) != nil {
				if !sleep(ctx, 5*time.Second) {
					return
				}
			}
		}
		if err := r.CommitMessages(ctx, m); err != nil && ctx.Err() == nil {
			// Harmless: the next commit covers this offset, and at worst the
			// event is redelivered and absorbed by the outbox unique key.
			logger.Warn("kafka commit failed", "topic", topic, logger.Err(err))
		}
	}
}

// handleWithRetry runs the handler until it succeeds, fails permanently, or
// runs out of attempts. The returned error, if any, is why it gave up.
func handleWithRetry(ctx context.Context, topic string, value []byte, handle events.Handler) error {
	var e events.Envelope
	if err := json.Unmarshal(value, &e); err != nil {
		return fmt.Errorf("%w: malformed event: %w", events.ErrPermanent, err)
	}
	delay := retryDelay
	for attempt := 1; ; attempt++ {
		err := handle(ctx, e)
		if err == nil || errors.Is(err, events.ErrPermanent) || attempt == maxAttempts {
			return err
		}
		logger.Warn("event handler failed, retrying", "topic", topic,
			"attempt", attempt, "in", delay, logger.Err(err))
		if !sleep(ctx, delay) {
			return ctx.Err()
		}
		delay *= 2
	}
}

// sleep waits d, or reports false if ctx ended first.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
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
