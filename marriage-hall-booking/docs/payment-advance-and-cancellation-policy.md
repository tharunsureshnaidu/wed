# Payment advance, balance, and cancellation policy

This document describes the payment policy layer added to the existing venue booking marketplace backend.

## Goals

- keep the policy inside the current booking/payment domain
- make the logic configurable without introducing a second booking system
- ensure the backend calculates the payment due and refund logic, never trusting the frontend
- preserve safe webhook and idempotency guarantees already present in the project

## Policy model

The policy is defined in `internal/payment/service/policy.go`.

Key fields:

- `AdvancePercent`: percentage of the booking total that must be collected at booking time
- `MinAdvanceAmount`: minimum amount floor for the initial payment
- `MaxAdvanceAmount`: optional cap for the initial payment
- `BalanceDueHoursBeforeEvent`: when the remaining balance becomes due before the event start
- `CancellationWindowHours`: full-refund window before the event start
- `CancellationPenaltyPercent`: refund penalty applied after the full-refund window starts

The default values are:

- 50% advance
- balance due 72 hours before the event
- full refund if cancelled before 168 hours (7 days) before the event
- 25% cancellation penalty after that cutoff

## Booking flow

The payment service now exposes the policy logic in a reusable form:

- `AdvanceAmount(total)` computes the required initial payment.
- `RemainingBalance(total, paid)` computes the not-yet-paid amount.
- `DueForBooking(total, paid)` returns the amount currently due.
- `RefundForCancellation(totalAmount, paidAmount, cancelledAt, eventStart)` calculates the refundable amount and policy outcome.

This is used by the existing booking/payment domain and remains compatible with the repo’s current PostgreSQL-based booking records.

## Why this matches the existing project

The repo already has these primitives:

- booking `total_amount`
- booking `paid_amount`
- payment rows with status and type
- refund rows and webhook idempotency
- payment status transition logic

The new policy logic is implemented as a server-side calculation layer on top of that system, instead of creating a new separate booking flow.

## Safety

- payment amounts are calculated by the backend from persisted booking totals
- the frontend only receives the resulting `checkoutUrl` / `paymentSessionId` or payment status
- cancellation and refund logic is based on the booking event time, not a browser callback
- the logic is deterministic and easy to audit from a single policy object

## Files added

- `internal/payment/service/policy.go`
- `docs/payment-advance-and-cancellation-policy.md`

## Next possible extension

The next production step would be to persist policy values in a configuration table (for each venue or marketplace), rather than keeping a single default policy in code. That would allow:

- venue-specific policy overrides
- policy versioning
- audit trails for accepted refund amounts
- settlement and commission calculation by payment stage
