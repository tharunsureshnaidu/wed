# wed — marriage hall & hotel booking (Go)

Conventions and gotchas for `marriage-hall-booking/`. Everything here was
learned by getting it wrong first; each entry exists to save one wasted round
trip.

## Working rules

**Verify against the running API, not just `go build`.** A feature is not done
until a real request has produced the expected row. Every bug in this repo that
reached "done" did so because something compiled and was assumed to work.

**Check the schema before writing SQL.** Column names here are not guessable —
`facility_images.url` (not `image_url`), `.sort_order` (not `display_order`),
`facility_pricing_rules.valid_from` (not `start_date`), and it has no
`is_deleted`. Run `\d <table>` first.

**Check the request struct before writing a payload.** Field names differ from
the obvious guess; see the table below.

**Clean up test data.** Delete test users, bookings, reviews and any row you
mutated in a shared table. Restore any facility you edited.

## Layout

Three shapes coexist, deliberately. Match the one already in the package:

| Shape | Packages |
|---|---|
| handler → service → repository | `auth`, `booking`, `payment`, `notification`, `user` |
| handler → repository | `facility` |
| handler → raw pgxpool | `admin`, `review`, `quote`, `vendors`, `search` |

Don't add a service layer to a package that has none. A flat table with
validation belongs in the handler.

## Events

Cross-module reactions use **callback hooks**, not imports. A handler or
service exposes `OnSomething func(...)`, and `cmd/api/main.go` wires it. That
keeps `admin` from importing `notification`.

Hooks fire **after commit** — never tell a user about a change that rolled back.
A hook failure must not fail the request that triggered it.

## Migrations

`internal/migrations/NNN_name.sql`, embedded, forward-only, applied on startup.
Next number: check `ls internal/migrations/ | tail -1` (currently at 043).

House style: `IF NOT EXISTS`, `UUID PRIMARY KEY DEFAULT gen_random_uuid()`,
`DECIMAL(10,2)` for money, `TIMESTAMPTZ`, `VARCHAR` + inline `CHECK` for enums,
`is_deleted BOOLEAN` soft delete.

**Always set `ON DELETE` on a FK to `users(id)`.** Omitting it silently makes
the referenced account undeletable — this happened with `notifications` and only
surfaced when a cleanup failed.

## API payloads that bite

| Endpoint | Correct fields |
|---|---|
| `POST /auth/register` | `fullName`, `email`, `phoneNumber`, `password` — **not** `name` |
| `POST /auth/register/verify-email` | `target`, `otpCode` — **not** `identifier`/`otp` |
| `POST /auth/login` | `identifier`, `password` |
| `POST /bookings/halls` | `hallId`, `startDate`, `endDate`, `startTime`, `endTime`, `guestCount`, `roomCount` (optional), `eventType`, `idempotentKey` |
| `POST /bookings/hotels` | `hotelId`, `checkIn`, `checkOut`, `rooms: [{roomTypeId, quantity}]` — `rooms` is an **array**, not a count |

**Halls and hotels count rooms differently, deliberately.** A hall booking takes
`roomCount`, a plain optional integer that does not affect the total — the owner
arranges the rooms offline. A hotel booking takes `rooms`, an array priced
against `room_types` into `booking_rooms`. Do not unify them: halls have no room
types defined.

`roomCount` has three distinct states and they must stay distinct — absent
(*not stated*), `0` (*explicitly none*) and a positive count. The owner's
notification omits the Rooms line entirely when it is absent.

There is no `/auth/verify-otp` route. Email and phone verification are separate
paths: `/auth/register/verify-email` and `/auth/register/verify-phone`.

## Roles

```
ROLE_CUSTOMER  ROLE_HALL_OWNER  ROLE_ADMIN  ROLE_STAFF
```

**There is no `ROLE_VENDOR`** — a vendor is `ROLE_HALL_OWNER`.

**Use `middleware.HasRole`, never `middleware.Role`.** `Role` returns only the
primary role, so an admin whose token lists `ROLE_ADMIN` second fails the check.
This was a live 403 bug in `requireOwner`.

## Facility types vs booking target types

Two different vocabularies on two tables — a frequent source of empty queries:

- `facilities.type` — `MARRIAGE_HALL` | `HOTEL`
- `bookings.target_type` — `HALL` | `HOTEL`

So a marriage hall is `MARRIAGE_HALL` in `facilities` and `HALL` in `bookings`.

## Running and testing

```bash
make start      # kafka + api + worker      make status   # what is up
make stop       # all of it, kafka included make restart  # api+worker only, kafka stays up
make logs       # both processes, one stream
make unlimit    # clear rate-limit counters
make test       # full suite     make check  # vet + test
```

Kafka is systemd-managed, not started at boot: it comes up with `make start`
and down with `make stop`. `make restart` deliberately leaves it running.

**A 429 mid-test means the rate limiter, not a bug** — registration is 5/hour
per IP. Run `make unlimit`.

**Restart after code changes.** `make start` on an already-running API silently
keeps the old binary; use `make restart`.

### Test recipe

```bash
make unlimit
ts=$(date +%s); EMAIL="t+$ts@example.com"
curl -sS -X POST localhost:8080/api/v1/auth/register -H 'Content-Type: application/json' \
  -d "{\"fullName\":\"T\",\"email\":\"$EMAIL\",\"phoneNumber\":\"9$ts\",\"password\":\"Passw0rd!23\"}"
curl -sS -X POST localhost:8080/api/v1/auth/register/verify-email -H 'Content-Type: application/json' \
  -d "{\"target\":\"$EMAIL\",\"otpCode\":\"000000\"}"      # OTP_FIXED_CODE in .env
TOK=$(curl -sS -X POST localhost:8080/api/v1/auth/login -H 'Content-Type: application/json' \
  -d "{\"identifier\":\"$EMAIL\",\"password\":\"Passw0rd!23\"}" \
  | python3 -c "import sys,json;print(json.load(sys.stdin)['data']['accessToken'])")
```

The fixed OTP comes from `OTP_FIXED_CODE=000000` in `.env` — dev only, and the
code rejects it outside development.

**To act as an admin** without knowing a password, mint a token with the app's
own signer (`pkg/jwt`, `JWT_SECRET` from `.env`). Note `Generate(subject,
userID, roles...)` takes the **primary role first**.

### Gotchas that cost time

- `pgrep -f 'bin/api'` matches your own command line. Match the executable, as
  the Makefile does.
- A review needs a `CONFIRMED`/`COMPLETED` booking at that facility, or 403.
- Foreground `sleep` is blocked; poll with `until <check>; do sleep 2; done`.
- `UID` is readonly in bash — name the variable something else.
- Consumer-group offsets survive a worker restart, so a "missing" log line may
  just be late. Check lag with `kafka-consumer-groups.sh --describe`, don't
  conclude from a short `tail`.

## Notifications

One outbox table, four channels (`EMAIL`, `SMS`, `WHATSAPP`, `PUSH`), retried
until acknowledged for `OWNER`/`ADMIN`, terminal on first send for
`CUSTOMER`/`USER`.

Every channel **degrades to logging when its credentials are absent** — the
whole pipeline is exercisable with no vendor account. `notify: would send` in
the log means missing config, not a failure.

Adding a channel to an existing event means checking the **fixed channel list**
in `cmd/worker/main.go`'s startup report too — it will silently omit a new one.

Two rules with real consequences:

- **A blocked user cannot read a push or in-app message** — blocking revokes
  their sessions. Those notifications must go by email/SMS.
- **`rating_is_manual` pins an imported rating** so `recalcRating` skips it.
  Without it the first real review replaces "4.3 from 218" with "1.0 from 1".

## Facility responses

`GET /halls`, `/halls/{id}`, `/facilities` all go through one `facilityCols` +
`scanFacility` pair — add a field there and it appears in list *and* detail.

Discount fields (`discountPercent`, `discountLabel`, `discountedPrice`,
`hasDiscount`) ride alongside `startingPrice`. Two rules:

- **`discountedPrice` is computed server-side.** Four clients rounding a
  percentage four ways is four different prices for the same venue.
- **An expired discount is filtered out in SQL**, so it can never reach a
  client. The row stays in the database; it is simply not served.

Set it with `PATCH /api/v1/admin/facilities/{id}`; `clearDiscount: true`
removes the percent, label and expiry together.

## My bookings

`GET /bookings` and `/bookings/{id}` return each booking with a `facility`
summary (name, city, cover image, rating) and review state — `canReview`,
`hasReviewed`, `myRating`, `myReviewId`. One call renders the screen; without
the facility block a client holds a bare `targetId`.

`Enrich` resolves the whole page in **one** query, deduplicated by facility.
Never make it per-booking — the bookings list is the most-hit screen.

**`canReview` must mirror what `POST /reviews` enforces**, or the app shows a
Rate button that 403s: a `CONFIRMED`/`COMPLETED` stay, and reviews are unique
per **(user, facility)** — not per booking, so a second booking at the same
venue is not a second chance to review it.

## Search history

`GET /api/v1/search/recent` (and `/recently-viewed`) were already built and
already recording — `recordSearch` writes to `search_events` and pushes onto a
Redis list capped at 10, deduplicated, 30-day TTL. `DELETE /api/v1/search/recent`
clears the caller's own list.

**`viewer()` keys these lists, and every unauthenticated caller used to share
the literal `"anonymous"` bucket** — so one logged-out visitor's history was
served to the next person who opened the app. Confirmed live: a search for
"divorce party venue" came back in a different caller's history. Anonymous
callers are now keyed by a hash of IP + user agent.

That key is a best-effort device fingerprint, not an identity: visitors behind
one NAT on the same browser still share a bucket. Fine for a convenience list,
never to be used for anything else.

## Notification feed

`GET /api/v1/notifications` is the in-app list; `/unread-count` is the badge,
`PUT /{id}/read` and `PUT /read-all` are the read state. All four are scoped to
the caller — an id from someone else's feed matches nothing and returns
`updated: 0`, the same shape as an already-read call, so it reveals nothing.

**The feed collapses the channel fan-out.** One event to one person is up to
four outbox rows (EMAIL/SMS/WHATSAPP/PUSH) with identical text; the feed returns
one item via `DISTINCT ON (event_type, COALESCE(subject_id, id::text))`. Verified:
4 rows in, 1 item out. The `COALESCE` is load-bearing — NULLs are distinct from
each other in `DISTINCT ON`, so without it every pre-migration row survives.

**`read_at` is not `status`.** Status is delivery ("did the SMS leave"), `read_at`
is attention ("did the person look"). A push sits SENT for days while unread, and
marking it read must never make the retry machinery think it was acknowledged.

Marking one item read updates **every channel row behind it** (`updated: 4`), or
the badge would still count the SMS copy of a push the user just opened.

**Pagination is a keyset cursor (`before`), not an offset** — notifications
arrive while the user scrolls and an OFFSET page repeats rows. `nextBefore` is
returned only on a full page. The handler repairs a cursor whose `+05:30` arrived
as ` 05:30`, since `+` decodes to space in a query string and the cursor is our
own value handed back.

### Date-driven notifications

`SweepReminders` (hourly, `REMINDER_TICK`) enqueues what no event can trigger:
`booking.upcoming` at T-3 and T-1 days, and `review.request` after checkout.

- **Idempotency is the outbox unique index, not a "reminded" column.** Six sweep
  runs produced 4 rows, not 24.
- **`subject_id` for a reminder is `{bookingId}:{days}`.** Keyed on the booking
  alone, the 1-day reminder collides with the 3-day one and never sends — the
  same collision the geo announcements hit.
- **The review request excludes anyone who already reviewed that facility**,
  mirroring the `(user, facility)` uniqueness `POST /reviews` enforces. Without
  it the app nags for a review whose link would 403.
- `payment.received` keys on `paymentId`, so a redelivered webhook collapses but
  a genuine second payment (advance, then balance) shows as its own item.

`make_interval(days => $1)` — not `($1 || ' days')::interval`, which makes pgx
infer text and fails with "cannot find encode plan" at runtime, not at build.

### Postman generator

A new route appears in the collection automatically, but three things do not,
and each fails *silently* — the run stays green while testing nothing:

- **`path_var()` must map `{id}`** or it falls through to `facilityId`, and the
  request addresses a facility UUID as a notification id. The handler returns
  `200 updated: 0` by design, so no assertion catches it.
- **`REQUEST_ORDER` must list the route** or it sorts alphabetically after every
  curated one. `read-all` sorted before `{id}/read`, so everything was already
  read by the time the single-item request ran.
- **A captured variable needs a fallback.** An unset one collapses the URL to
  `/notifications//read`, which 307s to a route that does not exist. The feed
  capture seeds the nil UUID when the list is empty.

## Geo-targeted announcements

Facility approved, amenities added and coupon created push to customers within
`GEO_RADIUS_KM` (50) of the venue, via `earthdistance`/`cube` with a GiST index
— not PostGIS, which is a large dependency for one distance predicate.

- **PUSH only.** This is marketing: SMS and WhatsApp bill per message, so a
  coupon to 2000 nearby users would be a real invoice.
- **A facility with no coordinates is skipped and logged**, never guessed at.
  35 of 52 facilities still have no lat/lng.
- **Each announcement needs its own dedupe key.** A coupon and an amenity at the
  same venue are both `facility.nearby` for the same facility, so without one
  the second silently collides on the outbox unique key and never sends. This
  bug shipped and was caught in testing.
- **Only genuinely new amenities announce.** `ON CONFLICT DO NOTHING` hides
  whether anything changed; re-adding an existing amenity must notify nobody.

## App settings (support helpline)

`app_settings` is a key/value table for admin-editable values; `support.*` holds
the helpline. `GET /api/v1/support` is **public and unauthenticated** — a
customer who cannot sign in is the one who needs it. `PUT /api/v1/admin/support`
is admin-only.

`is_public` gates what the public read returns, and defaults to false, so a key
added later cannot leak to every app user by accident.

**Do not validate a display phone with `validate.Phone`.** That enforces E.164
for a user's login number and rejects `"+91 98765 43210"`. The support handler
counts digits instead and keeps the admin's formatting.

## Docs in this repo

- `ADMIN_API.md` — admin endpoints for editing a venue
- `RUN_WITH_DOCKER.md`, `RUN_WITH_MAKE.md` — new-developer setup, two ways
- `RUNNING.md`, `DEPLOY.md` — local and server operation
- `POSTMAN.md` + `postman_collection.json` — regenerate with `make postman`
