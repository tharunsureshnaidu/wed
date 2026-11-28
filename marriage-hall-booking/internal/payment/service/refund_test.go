package service

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/apperr"
)

// NaN passes "<= 0" and every later limit, and a NaN in the refunds table makes
// the refunded sum NaN so no limit holds again. Refused before any DB work.
func TestRefundRefusesNonFiniteAmounts(t *testing.T) {
	s := &Service{}
	for _, amt := range []float64{math.NaN(), math.Inf(1), 0, -5} {
		_, err := s.Refund(context.Background(), "p", amt, "", 1, true)
		var ae *apperr.Error
		if !errors.As(err, &ae) || ae.Code != "INVALID_AMOUNT" {
			t.Fatalf("amount %v: want INVALID_AMOUNT, got %v", amt, err)
		}
	}
}
