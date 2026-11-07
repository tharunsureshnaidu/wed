package main

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/events"
)

// The retry decision is what makes the consumer at-least-once without letting
// one poison event block its topic.
func TestHandleWithRetry(t *testing.T) {
	defer func(d time.Duration) { retryDelay = d }(retryDelay)
	retryDelay = time.Millisecond
	msg := []byte(`{"type":"booking.created","payload":{"bookingId":"b1"}}`)
	transient := errors.New("db down")

	cases := []struct {
		name      string
		failFirst int   // attempts that fail before success
		fail      error // what they fail with
		wantCalls int
		wantErr   bool
	}{
		{"succeeds first time", 0, nil, 1, false},
		{"transient then success", 2, transient, 3, false},
		{"transient forever gives up", 99, transient, maxAttempts, true},
		{"permanent is not retried", 99, fmt.Errorf("%w: bad", events.ErrPermanent), 1, true},
	}
	for _, c := range cases {
		calls := 0
		err := handleWithRetry(context.Background(), "t", msg, func(_ context.Context, e events.Envelope) error {
			calls++
			if e.Type != "booking.created" {
				t.Fatalf("%s: envelope type %q", c.name, e.Type)
			}
			if calls <= c.failFirst {
				return c.fail
			}
			return nil
		})
		if calls != c.wantCalls || (err != nil) != c.wantErr {
			t.Errorf("%s: calls=%d err=%v, want calls=%d err=%v", c.name, calls, err, c.wantCalls, c.wantErr)
		}
	}

	calls := 0
	err := handleWithRetry(context.Background(), "t", []byte("not json"), func(context.Context, events.Envelope) error {
		calls++
		return nil
	})
	if calls != 0 || !errors.Is(err, events.ErrPermanent) {
		t.Errorf("malformed message: calls=%d err=%v, want 0 calls and a permanent error", calls, err)
	}

	// A panicking handler must not crash the worker: one attempt, then the DLQ.
	calls = 0
	err = handleWithRetry(context.Background(), "t", msg, func(context.Context, events.Envelope) error {
		calls++
		panic("boom")
	})
	if calls != 1 || !errors.Is(err, events.ErrPermanent) {
		t.Errorf("panic: calls=%d err=%v, want 1 call and a permanent error", calls, err)
	}
}

// Shutdown mid-retry must return promptly, not sit out the backoff - the
// message is then left uncommitted and redelivered on the next start.
func TestHandleWithRetryStopsOnShutdown(t *testing.T) {
	defer func(d time.Duration) { retryDelay = d }(retryDelay)
	retryDelay = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() {
		done <- handleWithRetry(ctx, "t", []byte(`{"type":"x","payload":{}}`),
			func(context.Context, events.Envelope) error { return errors.New("db down") })
	}()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("still waiting out the backoff after shutdown")
	}
}
