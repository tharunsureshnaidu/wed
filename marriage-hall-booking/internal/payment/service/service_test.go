package service

import (
	"context"
	"testing"
)

func TestService_ComputeDueAmount(t *testing.T) {
	svc := New(nil, nil, "wh-secret")
	svc.SetPolicy(Policy{
		AdvancePercent:   40,
		MinAdvanceAmount: 1000,
		MaxAdvanceAmount: 10000,
	})

	// Unpaid booking of 10,000 -> 40% = 4,000
	due := svc.ComputeDueAmount(10000, 0)
	if due != 4000 {
		t.Fatalf("expected due 4000, got %.2f", due)
	}

	// Unpaid booking of 1,000 -> 40% is 400, but min advance is 1000
	dueMin := svc.ComputeDueAmount(1000, 0)
	if dueMin != 1000 {
		t.Fatalf("expected due 1000 (min advance), got %.2f", dueMin)
	}

	// Partially paid booking (paid 4,000 of 10,000) -> remaining balance 6,000
	dueBalance := svc.ComputeDueAmount(10000, 4000)
	if dueBalance != 6000 {
		t.Fatalf("expected remaining balance 6000, got %.2f", dueBalance)
	}

	// Fully paid booking -> 0 due
	duePaid := svc.ComputeDueAmount(10000, 10000)
	if duePaid != 0 {
		t.Fatalf("expected due 0, got %.2f", duePaid)
	}
}

func TestService_ProviderWiring(t *testing.T) {
	svc := New(nil, nil, "wh-secret")
	if svc.provider == nil {
		t.Fatal("expected default mock provider to be configured")
	}

	customMock := NewMockPaymentProvider()
	customMock.MockStatus = "ACTIVE"
	svc.SetCashfreeClient(customMock)

	if svc.provider != customMock {
		t.Fatal("SetCashfreeClient did not set provider")
	}

	// Verify signature pass-through to provider
	if !svc.VerifySignature([]byte("{}"), "mock-valid-signature") {
		t.Fatal("VerifySignature should delegate to provider")
	}
}

func TestService_DefaultPolicyFallback(t *testing.T) {
	var nilSvc *Service
	if nilSvc.ComputeDueAmount(1000, 0) != 0 {
		t.Fatal("nil service should safely return 0")
	}

	svc := New(nil, nil, "")
	p := svc.Policy()
	if p.AdvancePercent != 50 {
		t.Fatalf("expected default advance percent 50, got %.2f", p.AdvancePercent)
	}

	resolved, err := svc.ResolvePolicy(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.AdvancePercent != 50 {
		t.Fatalf("expected resolved advance percent 50, got %.2f", resolved.AdvancePercent)
	}
}
