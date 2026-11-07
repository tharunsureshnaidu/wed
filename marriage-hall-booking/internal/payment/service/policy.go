package service

import (
	"math"
	"time"
)

// Policy defines the business rules used by the booking payment flow.
//
// The values are intentionally conservative defaults for an Indian venue-booking
// marketplace: the first payment is an advance, the remainder is collected
// before the event date, and cancellation refunds are determined by how much time
// remains before the event.
type Policy struct {
	AdvancePercent             float64 `json:"advancePercent"`
	MinAdvanceAmount           float64 `json:"minAdvanceAmount"`
	MaxAdvanceAmount           float64 `json:"maxAdvanceAmount"`
	BalanceDueHoursBeforeEvent int     `json:"balanceDueHoursBeforeEvent"`
	CancellationWindowHours    int     `json:"cancellationWindowHours"`
	CancellationPenaltyPercent float64 `json:"cancellationPenaltyPercent"`
}

func DefaultPolicy() Policy {
	return Policy{
		AdvancePercent:             50,
		MinAdvanceAmount:           0,
		MaxAdvanceAmount:           0,
		BalanceDueHoursBeforeEvent: 72,
		CancellationWindowHours:    168,
		CancellationPenaltyPercent: 25,
	}
}

func (p Policy) normalized() Policy {
	out := p
	if out.AdvancePercent < 0 {
		out.AdvancePercent = 0
	}
	if out.AdvancePercent > 100 {
		out.AdvancePercent = 100
	}
	if out.CancellationPenaltyPercent < 0 {
		out.CancellationPenaltyPercent = 0
	}
	if out.CancellationPenaltyPercent > 100 {
		out.CancellationPenaltyPercent = 100
	}
	if out.BalanceDueHoursBeforeEvent < 0 {
		out.BalanceDueHoursBeforeEvent = 0
	}
	if out.CancellationWindowHours < 0 {
		out.CancellationWindowHours = 0
	}
	return out
}

func (p Policy) Normalize() Policy {
	return p.normalized()
}

func roundCurrency(v float64) float64 {
	return math.Round(v*100) / 100
}

func (p Policy) AdvanceAmount(total float64) float64 {
	if total <= 0 {
		return 0
	}
	out := p.normalized()
	amount := total * out.AdvancePercent / 100
	if out.MinAdvanceAmount > 0 && amount < out.MinAdvanceAmount {
		amount = out.MinAdvanceAmount
	}
	if out.MaxAdvanceAmount > 0 && amount > out.MaxAdvanceAmount {
		amount = out.MaxAdvanceAmount
	}
	if amount > total {
		amount = total
	}
	return roundCurrency(amount)
}

func (p Policy) RemainingBalance(total, paid float64) float64 {
	if total <= 0 {
		return 0
	}
	remaining := total - paid
	if remaining < 0 {
		return 0
	}
	return roundCurrency(remaining)
}

func (p Policy) RefundForCancellation(totalAmount, paidAmount float64, cancelledAt, eventStart time.Time) (float64, string) {
	paidAmount = roundCurrency(paidAmount)
	if paidAmount <= 0 {
		return 0, "NO_REFUND"
	}
	if totalAmount <= 0 {
		return 0, "NO_REFUND"
	}
	policy := p.normalized()
	if eventStart.IsZero() || cancelledAt.IsZero() {
		return paidAmount, "FULL_REFUND"
	}
	cutoff := eventStart.Add(-time.Duration(policy.CancellationWindowHours) * time.Hour)
	if cancelledAt.Before(cutoff) || cancelledAt.Equal(cutoff) {
		return paidAmount, "FULL_REFUND"
	}
	penalty := (paidAmount * policy.CancellationPenaltyPercent) / 100
	refundable := paidAmount - penalty
	if refundable < 0 {
		refundable = 0
	}
	return roundCurrency(refundable), "PENALTY_APPLIED"
}

func (p Policy) DueForBooking(total, paid float64) float64 {
	if total <= 0 {
		return 0
	}
	remaining := p.RemainingBalance(total, paid)
	if paid <= 0 {
		advance := p.AdvanceAmount(total)
		if remaining <= 0 {
			return 0
		}
		if advance <= 0 {
			return remaining
		}
		if advance < remaining {
			return advance
		}
		return remaining
	}
	return remaining
}
