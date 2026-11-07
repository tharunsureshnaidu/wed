package service

import (
	"testing"
	"time"
)

func TestResolvePolicyPrecedencePrefersFacilityOverride(t *testing.T) {
	marketplace := Policy{
		AdvancePercent:             25,
		MinAdvanceAmount:           1000,
		MaxAdvanceAmount:           5000,
		BalanceDueHoursBeforeEvent: 48,
		CancellationWindowHours:    120,
		CancellationPenaltyPercent: 10,
	}
	facility := Policy{
		AdvancePercent:             60,
		MinAdvanceAmount:           2000,
		MaxAdvanceAmount:           12000,
		BalanceDueHoursBeforeEvent: 72,
		CancellationWindowHours:    168,
		CancellationPenaltyPercent: 25,
	}

	got := ResolvePolicyPrecedence(facility, marketplace)
	if got.AdvancePercent != 60 {
		t.Fatalf("facility advance percent should win; got %.2f want 60", got.AdvancePercent)
	}
	if got.BalanceDueHoursBeforeEvent != 72 {
		t.Fatalf("facility balance due window should win; got %d want 72", got.BalanceDueHoursBeforeEvent)
	}
	if got.CancellationPenaltyPercent != 25 {
		t.Fatalf("facility penalty should win; got %.2f want 25", got.CancellationPenaltyPercent)
	}
}

func TestResolvePolicyPrecedenceFallsBackToMarketplace(t *testing.T) {
	marketplace := Policy{
		AdvancePercent:             30,
		MinAdvanceAmount:           500,
		MaxAdvanceAmount:           8000,
		BalanceDueHoursBeforeEvent: 48,
		CancellationWindowHours:    120,
		CancellationPenaltyPercent: 15,
	}

	got := ResolvePolicyPrecedence(Policy{}, marketplace)
	if got.AdvancePercent != 30 {
		t.Fatalf("marketplace policy should be used when facility policy is empty; got %.2f want 30", got.AdvancePercent)
	}
	if got.CancellationWindowHours != 120 {
		t.Fatalf("marketplace cancellation window should be used; got %d want 120", got.CancellationWindowHours)
	}
}

func TestPolicyNormalizationClampsInvalidValues(t *testing.T) {
	p := Policy{
		AdvancePercent:             -10,
		MinAdvanceAmount:           100,
		MaxAdvanceAmount:           10000,
		BalanceDueHoursBeforeEvent: -15,
		CancellationWindowHours:    -30,
		CancellationPenaltyPercent: 120,
	}

	got := p.Normalize()
	if got.AdvancePercent != 0 {
		t.Fatalf("advance percent should be clamped to 0; got %.2f", got.AdvancePercent)
	}
	if got.CancellationPenaltyPercent != 100 {
		t.Fatalf("penalty percent should be clamped to 100; got %.2f", got.CancellationPenaltyPercent)
	}
	if got.BalanceDueHoursBeforeEvent != 0 {
		t.Fatalf("balance due hours should clamp to 0; got %d", got.BalanceDueHoursBeforeEvent)
	}
	if got.CancellationWindowHours != 0 {
		t.Fatalf("cancellation window should clamp to 0; got %d", got.CancellationWindowHours)
	}
}

func TestPolicyAdvanceAndRefundCalculations(t *testing.T) {
	p := Policy{
		AdvancePercent:             50,
		MinAdvanceAmount:           1000,
		MaxAdvanceAmount:           20000,
		BalanceDueHoursBeforeEvent: 72,
		CancellationWindowHours:    168,
		CancellationPenaltyPercent: 25,
	}

	if got := p.AdvanceAmount(20000); got != 10000 {
		t.Fatalf("advance amount mismatch: got %.2f want 10000", got)
	}

	if got := p.DueForBooking(20000, 5000); got != 15000 {
		t.Fatalf("due amount mismatch: got %.2f want 15000", got)
	}

	now := time.Now().UTC()
	eventStart := now.Add(240 * time.Hour)
	fullRefundAt := now.Add(50 * time.Hour)
	refund, status := p.RefundForCancellation(20000, 20000, fullRefundAt, eventStart)
	if refund != 20000 {
		t.Fatalf("full refund mismatch: got %.2f want 20000", refund)
	}
	if status != "FULL_REFUND" {
		t.Fatalf("status mismatch: got %s want FULL_REFUND", status)
	}

	penaltyAt := now.Add(150 * time.Hour)
	refund, status = p.RefundForCancellation(20000, 20000, penaltyAt, eventStart)
	if refund != 15000 {
		t.Fatalf("penalty refund mismatch: got %.2f want 15000", refund)
	}
	if status != "PENALTY_APPLIED" {
		t.Fatalf("status mismatch: got %s want PENALTY_APPLIED", status)
	}
}
