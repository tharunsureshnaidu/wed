# Payment advance, balance, and cancellation policy

This document describes the payment policy layer added to the existing venue booking marketplace backend.

## What has been implemented

The project now includes the following payment-policy work in the existing booking/payment domain:

- a server-side Cashfree integration with provider wiring and env-based sandbox/production configuration
- a reusable payment policy model for advance, balance, and cancellation behavior
- DB-backed marketplace and venue policy storage via `payment_policies`
- facility-level override precedence over the marketplace default
- admin and facility-owner API endpoints for reading and updating payment policies
- backend-side calculation of due amounts and cancellation refunds so the frontend never determines accepted payment or refund values
- targeted unit tests covering precedence, validation, and policy calculations

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

## Precedence and validation behavior

The effective policy follows this precedence:

1. if a facility-level override exists, that policy is used for that venue
2. otherwise the marketplace default is used
3. if neither exists, the default built-in policy is returned

The policy normalization layer clamps invalid values so they remain safe and deterministic:

- negative percentages are clamped to 0
- percentages above 100 are capped at 100
- negative timing windows are clamped to 0

This is implemented centrally in the policy helpers and is covered in unit tests.

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

## API surface

The policy endpoints are mounted in the existing API server and are available for admin and facility-owner management:

- `GET /api/v1/payment-policies` for marketplace policy lookup
- `PUT /api/v1/payment-policies` for marketplace policy updates
- `GET /api/v1/facilities/{facilityId}/payment-policy` for facility policy lookup
- `PUT /api/v1/facilities/{facilityId}/payment-policy` for facility policy updates

The facility route enforces ownership checks for hall owners while allowing super admins to manage any facility.

## Safety

- payment amounts are calculated by the backend from persisted booking totals
- the frontend only receives the resulting `checkoutUrl` / `paymentSessionId` or payment status
- cancellation and refund logic is based on the booking event time, not a browser callback
- the logic is deterministic and easy to audit from a single policy object

## Files added or updated

- `internal/payment/service/policy.go`
- `internal/payment/service/policy_test.go`
- `internal/payment/service/service.go`
- `internal/payment/handler/policy.go`
- `internal/migrations/068_payment_policies.sql`
- `docs/payment-advance-and-cancellation-policy.md`

## Validation

The policy logic is covered by targeted unit tests for:

- facility override precedence
- marketplace fallback when no facility override exists
- invalid-value normalization
- advance and refund calculations

## Next possible extension

The next production step would be to expand the same policy layer into a richer booking lifecycle and settlement workflow, including:

- admin-side policy versions and audit history
- per-venue settlement and commission rules
- cancellation workflows tied to confirmed bookings and refunds
- broader API integration with facility dashboard screens and owner reporting
