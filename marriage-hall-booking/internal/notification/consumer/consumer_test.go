package consumer

import (
	"context"
	"errors"
	"testing"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/events"
)

// A topic in events.Topics that nothing subscribes to is published, created,
// and silently never handled. Media is the worker's own consumer.
func TestEveryTopicHasAConsumer(t *testing.T) {
	handled := map[string]bool{events.TopicMediaUploadRequested: true}
	for _, topic := range Topics {
		handled[topic] = true
	}
	for _, topic := range events.Topics {
		if !handled[topic] {
			t.Errorf("topic %q is published but no consumer subscribes to it", topic)
		}
	}
}

// Payloads that can never succeed go straight to the dead-letter topic rather
// than being retried for a minute. None of these reach the database.
func TestPermanentFailures(t *testing.T) {
	c := New(nil, nil)
	for _, e := range []events.Envelope{
		{Type: "no.such.topic", Payload: []byte(`{}`)},
		{Type: events.TopicBookingCreated, Payload: []byte(`{}`)},
		{Type: events.TopicBookingCancelled, Payload: []byte(`"not an object"`)},
	} {
		if err := c.Handle(context.Background(), e); !errors.Is(err, events.ErrPermanent) {
			t.Errorf("%s %s: err = %v, want ErrPermanent", e.Type, e.Payload, err)
		}
	}
}

func TestOfferText(t *testing.T) {
	max := 150000.0
	for _, c := range []struct {
		p    events.CouponCreated
		want string
	}{
		{events.CouponCreated{DiscountType: "PERCENT", DiscountValue: 20}, "20% off"},
		{events.CouponCreated{DiscountType: "PERCENT", DiscountValue: 12.5, MaxDiscount: &max}, "12.5% off (up to ₹1,50,000)"},
		{events.CouponCreated{DiscountType: "FLAT", DiscountValue: 2000}, "₹2,000 off"},
		{events.CouponCreated{DiscountType: "FLAT", DiscountValue: 500}, "₹500 off"},
		{events.CouponCreated{DiscountType: "FLAT", DiscountValue: 12345678}, "₹1,23,45,678 off"},
	} {
		if got := offerText(c.p); got != c.want {
			t.Errorf("offerText(%+v) = %q, want %q", c.p, got, c.want)
		}
	}
}
