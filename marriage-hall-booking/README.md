# Marriage Hall & Hotel Booking — Go

Go rewrite of the Java/Spring monolith. No Docker: PostgreSQL, Redis and Kafka
run as ordinary local services.

## Running

```bash
make build     # api + worker
make run       # API on :8080
make worker    # event consumer + expired-hold sweeper
make test      # unit + integration + security
make race      # same, under the race detector
```

Infrastructure (already running on this machine):

| Service    | Address          | Notes                    |
|------------|------------------|--------------------------|
| PostgreSQL | `localhost:5432` | database `venue`         |
| Redis      | `localhost:6379` | rate limiting            |
| Kafka      | `localhost:9092` | domain events            |
| API        | `localhost:8080` | |
| Worker     | —                | background process       |

Migrations run automatically at API start-up; they are embedded in the binary
and applied once each, in filename order.

## Layout

```
cmd/api          HTTP server
cmd/worker       Kafka consumers + expired-hold sweeper
internal/auth    registration, login, OTP, JWT, password reset
internal/user    profiles, favourites
internal/facility  halls & hotels, amenities
internal/booking   booking engine (the concurrency-critical part)
internal/payment   payments, webhooks, refunds
internal/vendors   vendor business profile, KYC, payouts
internal/admin     dashboard, moderation, fraud reports
internal/migrations  embedded SQL
pkg/…            jwt, middleware, config, response, validation, events
```

## Notes on the port

These are deliberate differences from the Java implementation, not omissions.

**OTP generation.** `OtpService.generateOtp` in the Java service returned a
hardcoded `"000000"` for every OTP. That is a backdoor into any account if it
ever reaches an environment with real users. The Go version always generates a
`crypto/rand` code; `LOG_OTP_CODES=true` prints it in development, standing in
for the notification service that does not exist yet.

**OTP and reset-token hashing.** SHA-256 with a constant-time comparison rather
than BCrypt. These are already high-entropy random values, so there is no
dictionary for BCrypt's work factor to slow down. Passwords are still BCrypt
cost 12.

**Refresh-token rotation.** A conditional `UPDATE … WHERE revoked = FALSE`, so
two concurrent refreshes have exactly one winner. The Java read-then-write could
let both mint a session.

**Booking concurrency.** The slot claim is a single conditional `INSERT …
ON CONFLICT … WHERE status = 'AVAILABLE'`. The Java version read the row,
checked its status in application code, then wrote — a race that only held
because a unique index happened to backstop it, and that surfaced lost races as
500s instead of a clean 409.

**Bookings are not partitioned.** Partitioning by `created_at` forced a
composite primary key, which forced every child table to carry a redundant
`booking_created_at` column purely to satisfy the foreign key. That complexity
is permanent; the table can be partitioned later if volume ever justifies it.

**Multi-role tokens.** The Java token carried a single `role` claim, so a user
who was both a hall owner and an admin was denied admin routes. Tokens now carry
every role; the single `role` claim is kept for gateway compatibility.

**Skipped Java migrations.** `V9__seed_admin_user` seeds an admin with a
hardcoded password hash — a known-credential account in every fresh database.
`V10__add_unique_constraints_email_phone` exists only so a Spring exception
handler can string-match a constraint name, and targets an `app_auth` schema
that does not exist here.

## API coverage

Every endpoint in `postman_collection.json` (107) and every public Java
controller route (110) is implemented. Verified by diffing the route tables, not
by inspection.

**Search** runs on PostgreSQL with trigram indexes rather than Elasticsearch,
which is not available here; recent searches, recently-viewed and trending use
Redis and `search_events` exactly as the Java service did. Every documented
parameter — `q`, `city`, `venueType`, capacity and budget ranges, `amenities`
(AND semantics, by name or code) and all five `sort` modes — is honoured.

**Quotes** carry line items with server-computed totals, and each reply/counter
is an immutable version, so the negotiation history is preserved. An accepted
quote converts into a booking through the same booking service as a direct
booking, so slot claiming and idempotency behave identically; the agreed total
overrides the hall's list price.

**Reviews** require an eligible booking (`CONFIRMED` or `COMPLETED`) at that
venue, one per user per facility, and keep `facilities.avg_rating` /
`review_count` in the same transaction as the review.

## Stubbed integrations

These need credentials or services this machine does not have. The endpoints,
storage and rules are real; only the outbound integration is missing:

- **Media** — image/video endpoints record URLs of already-hosted files
  (ordering, cover-image and ownership rules all work). There is no S3 uploader,
  so the caller supplies the URL. Same for quote attachments.
- **Email/SMS delivery** — the worker consumes the events and logs what it
  would send. Topics, offsets and consumers are real; only delivery is a stub.
- **Payment gateway** — a `MOCK` gateway issues order ids and accepts
  HMAC-signed webhooks in the real shape. Swapping in a provider is one adapter.
