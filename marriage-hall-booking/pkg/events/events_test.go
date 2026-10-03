package events

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// Events already in the log were written when Payload was a map. They must
// still decode after the switch to typed payloads.
func TestEnvelopeDecodesExistingMessages(t *testing.T) {
	old := `{"type":"payment.completed","occurredAt":"2026-09-01T10:00:00Z",
		"payload":{"bookingId":"b1","paymentId":"p1","amount":2500.5}}`
	var e Envelope
	if err := json.Unmarshal([]byte(old), &e); err != nil {
		t.Fatal(err)
	}
	var p Booking
	if err := e.Decode(&p); err != nil {
		t.Fatal(err)
	}
	if p.BookingID != "b1" || p.PaymentID != "p1" || p.Amount != 2500.5 {
		t.Errorf("decoded %+v", p)
	}
	if err := (Envelope{Payload: json.RawMessage(`"nope"`)}).Decode(&p); !errors.Is(err, ErrPermanent) {
		t.Errorf("undecodable payload: err = %v, want ErrPermanent", err)
	}
}

// With Kafka disabled the event is handled in-process, and Close waits for it -
// otherwise a shutdown drops the reactions to the last requests served.
func TestPublishWithoutKafkaHandlesLocally(t *testing.T) {
	p := NewPublisher(nil)
	var got Envelope
	p.Local = func(_ context.Context, e Envelope) error { got = e; return nil }
	p.Publish(context.Background(), TopicReviewCreated, "f1",
		ReviewCreated{ReviewID: "r1", FacilityID: "f1", Rating: 5})
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	var r ReviewCreated
	if err := got.Decode(&r); err != nil {
		t.Fatal(err)
	}
	if got.Type != TopicReviewCreated || r.ReviewID != "r1" || r.Rating != 5 {
		t.Errorf("local handler got %q %+v", got.Type, r)
	}
}
