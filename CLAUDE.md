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

**Hooks only publish; the worker reacts.** Every reaction lives in
`internal/notification/consumer` (`Handle`). The worker runs it from Kafka; the
API runs the *same* `Handle` in-process via `Publisher.Local` when Kafka is
disabled or refuses the write, so a broker outage delays nothing and drops
nothing (verified: booking with Kafka stopped → 28ms response, 9 outbox rows).

- **At-least-once: `FetchMessage`, commit after success.** `ReadMessage` commits
  *before* returning, so a failed handler or a killed worker lost the event.
  Handlers must therefore be idempotent — the outbox unique key is what makes
  them so.
- **Retry ~1 min, then `<topic>.dlq`.** An error wrapping `events.ErrPermanent`
  (malformed, missing id) skips the retries. The loop does not move on until the
  DLQ write lands: kafka-go fetches forward regardless of commits.
- **A new topic goes in `events.Topics` *and* `consumer.Topics`**, or it is
  published and never handled. `TestEveryTopicHasAConsumer` enforces it.
- **`ClaimDue` skips rows of CANCELLED/EXPIRED bookings.** `booking.created` and
  `booking.cancelled` are separate topics with no ordering between them; the
  cancel was reproduced landing first, catching 1 of 9 rows. The send-time check
  holds for every ordering.
- **The worker validates `spoolPath` with `storage.InSpool`** before opening it.
  The broker has no auth, and a forged path would upload any readable file to
  public storage.

**`make test` runs the DB tests against the dev database**, and
`booking_test.go`'s `ExpireHolds` expires *every* lapsed hold there, not just its
fixtures. For a no-side-effect run: `env -u TEST_DATABASE_URL go test ./...`.

## Migrations

`internal/migrations/NNN_name.sql`, embedded, forward-only, applied on startup.
Next number: check `ls internal/migrations/ | tail -1` (currently at 050).

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
| `POST /auth/register`, `/register/vendor` | optional `address`: a string (→ `users.address`) **or** `{street, city, state, zipCode, country}`, each part optional (→ `addresses`, and its joined line → `users.address`); a vendor's also seeds `vendors.business_address` |
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

**The API speaks `HALL` only; the column still stores `MARRIAGE_HALL`.**
`GET /bookings` used to return `"targetType": "HALL"` beside a nested
`"type": "MARRIAGE_HALL"` for the same venue — two words for one thing in a
single response. `pkg/venuetype` translates at the edge:

- `venuetype.API(stored)` on the way **out** — call it at every scan that puts
  a facility type on the wire. `Facility.Type` is `json:"-"` and emitted by
  `MarshalJSON`, so list, detail and compare are covered in one place; the
  other seven scan sites translate individually.
- `venuetype.Stored(api)` on the way **in** — accepts `HALL` *and*
  `MARRIAGE_HALL`, so saved requests, `cmd/import` and deployed clients keep
  working. Anything else passes through, so adding a third facility type needs
  no change here.

Renaming the stored value was rejected: 153 rows, a CHECK constraint and 16
Go references, for a difference no user can see.

Two traps this hit, both silent:

- **`amenities.applicable_type` stores the DB word.** Comparing it against a
  request's `HALL` rejects every valid hall amenity with "does not apply to".
  `resolveAmenities` converts first.
- **A Postman capture compared `d.type === "MARRIAGE_HALL"`** to set `hallId`.
  Once responses said `HALL` it silently stopped capturing, and every later
  request in the chain would have addressed an empty id. It now accepts both.

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

## Venue search

`/search/venues`, its autocomplete/suggestions, and `/halls` / `/facilities`
`?search=` all match through **`pkg/venuesearch`**. Never write a fresh
`name ILIKE '%q%'` for venue text: that was the old match, and `palce` or
`grnd palace` returned 0 results.

- **Fuzzy on name + city, substring only on description.** `word_similarity`
  over `name||' '||city` at **0.4**, measured on live data: real typos score
  0.43-0.83, the closest unrelated venue 0.33. A trigram score over a paragraph
  matches nearly anything, so descriptions stay `ILIKE`.
- **Search text with no explicit `sort` ranks best match first**; an explicit
  sort still wins.
- **Postgres, not Elasticsearch, deliberately.** 52 live venues; ES would mean a
  JVM service, a sync pipeline for every facility write, and drift handling, for
  what pg_trgm does in one file. Revisit past ~50k venues: the per-row score has
  no index (see the `ponytail:` note in the package).
- Not handled: synonyms and renames (`banglore` -> Bengaluru scores 0.11).

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
- **An environment variable shadows a collection variable.** `postman_run.py`
  seeds `accessToken`/`refreshToken`/`userId` with `--env-var`, so a capture
  that only calls `pm.collectionVariables.set` is invisible to `{{...}}` — the
  request keeps sending the runner's original value. Any capture writing a
  seeded name must write **both** scopes.

  This shipped as a real failure: refresh tokens rotate, so `Refresh token
  (alias)` replayed the token the request before it had already burnt. Replay is
  the theft signal, so the API revoked every session for that user and 14
  assertions failed across Bookings, Quotes and Refunds. The API was correct
  throughout; the collection was replaying. The alias
  (`POST /auth/login/refresh`) is now in `SKIP_ROUTES`: the route is still
  served, but the collection refreshes once.

## My reviews and app feedback

Two different things on two tables, deliberately:

- `reviews` rates a **venue** and feeds that facility's `avg_rating`.
- `app_feedback` rates **our service** and must never touch a facility score.

Folding them together would need a nullable `facility_id` on `reviews` — a guard
every rating query would eventually forget — or app complaints dragging down a
hall's rating.

`GET /api/v1/reviews/my-reviews` returns the user's own reviews with venue name,
city and cover image, plus `totalReviews` and `avgRatingGiven` computed over the
**whole set, not the page** — "2 Total Reviews" must not become "20" on scroll.
`avgRatingGiven` is null with no reviews, never 0, which would draw as a
zero-star average.

`POST /api/v1/feedback` accepts **JSON or multipart** — the screenshot is
optional and a text-only report should not force a multipart body. The
attachment's type is sniffed with `http.DetectContentType`, never taken from the
filename: a shell script renamed `.png` is rejected (verified).

`GET/PUT /api/v1/admin/feedback` is the ops inbox. `admin_note` is internal and
is never returned by the reporter's own `my-feedback` read. `resolved_at` is
stamped on the way into RESOLVED/CLOSED and cleared on the way back out.

**`app_feedback.user_id` is `ON DELETE SET NULL`, not CASCADE.** Deleting a user
must not erase the bug report they filed.

### The unique index on reviews is partial

`uq_review_user_facility` ignored `is_deleted`, so a soft-deleted review kept
occupying the slot forever: a user who deleted their review was told *"You have
already reviewed this venue"* and could never write another. Harmless until the
My Reviews screen shipped a delete button. Migration 047 replaces it with a
partial unique index `WHERE is_deleted = FALSE`.

**A partial index needs the same predicate on every `ON CONFLICT`.**
`ON CONFLICT (user_id, facility_id)` alone matches no index and fails the whole
statement with a 500 — which is exactly what happened to `POST /admin/reviews`
and was caught by newman, not by the build. The admin upsert now reads
`ON CONFLICT (user_id, facility_id) WHERE is_deleted = FALSE`.

## Event types

What occasions a venue can host. Three endpoints:

```
GET /api/v1/events                     catalogue, public, ?category= filters
GET /api/v1/facilities/{id}/events     what one venue hosts, public
PUT /api/v1/facilities/{id}/events     owner sets the list
```

**The catalogue is hardcoded in `pkg/eventtypes`, not a table.** The list changes
with a release, not at runtime, and a seeded table nobody edits is a migration
pretending to be data. Its own package because two modules need it — `facility`
declares what a venue hosts, `booking` validates what a customer books — and
handler importing handler is the wrong direction.

**Codes are permanent.** They are stored in `facility_events` and in
`bookings.event_type`, so renaming one orphans every row that used it. A test
pins the five that already exist in bookings.

**There were two allowlists.** `internal/booking/handler` had its own 5-entry
map, so a venue could advertise Sangeet through the facility API and then have
the booking rejected. `eventtypes` is now the single source.

`PUT` **replaces, never merges** — the screen is a checkbox list, and a merge
would leave an unchecked box checked. Validation runs before the write, so a
payload with one bad code leaves the venue untouched.

### Silence means "not stated", never "no"

A venue with **no** declared events matches every `eventType` filter and accepts
every booking. 43 halls predate this feature; hiding them the moment a filter is
used would look like the search is broken, and refusing their bookings would be
a regression caused by a screen their owner has not seen.

Migration 048 backfills `WEDDING` for every marriage hall, plus any event type
a venue has already hosted according to `bookings` — 103 rows, recovering real
data rather than asking owners to re-enter it.

**Adding a search parameter shifts the paging placeholders.** `eventType` became
`$9`, which silently stole `LIMIT $9`, and *every* search returned 500 with
"argument of LIMIT must be type bigint". Caught only by running it. LIMIT/OFFSET
are now `$10`/`$11`.

## Venue FAQs

Per-venue questions and answers on the detail page: parking, outside catering,
decoration timings. Per-venue rather than a shared list, because "is outside
catering allowed" has a different answer at every hall.

```
GET    /api/v1/facilities/{id}/faqs             public
POST   /api/v1/facilities/{id}/faqs             owner
PUT    /api/v1/facilities/{id}/faqs/{childId}   owner, partial
DELETE /api/v1/facilities/{id}/faqs/{childId}   owner, soft
```

Also **embedded in the detail response** as `faqs`, so a client that already
fetched the venue needs no second call. Deliberately **not** in the list
response — the card shows no accordion, and loading them per row would be an
N+1 on the most-hit screen.

**The detail always sends `faqs` and `reviews`, as `[]` when there are none.**
One struct serialises both list and detail, so a plain `omitempty` did double
duty: it kept these keys off the card (wanted) *and* dropped them from the
detail for any venue with none yet (a client doing `reviews.length` breaks on
exactly those venues — and today that is most of them). Both are marshalled
through `detailOnly` — nil on the list, an empty slice on the detail. A test
pins both halves and fails if either regresses.

`rating` is separate and rides on **every** response, list included: it is the
summary (`value`, `reviewCount`) the card draws, computed from columns on the
facility row, so it never costs a per-row query. Do not confuse "the list has
no `reviews`" with "the list has no rating" — the card has always had the
stars.

`PUT` is partial: an omitted field is left alone, so fixing a typo in an answer
cannot blank the question. Writes are scoped to the facility *and* the id, so an
id from another venue matches nothing rather than editing someone else's FAQ.

### Events on the list response

`eventCodes` and the resolved `events` array now ride on **both** list and
detail. The list loads them with `EventsOfMany` in **one batched query for the
whole page** — measured at 3 queries for a 20-venue page (count, page, events),
not 22. Never call `EventsOf` per row here.

`events` is resolved through `eventtypes.Views` in `Facility.MarshalJSON`, so it
appears everywhere a facility is serialised without touching each endpoint.
`eventCodes` stays for clients already reading it.

## Comparing venues

`GET /api/v1/facilities/compare?ids=a,b,c` — public, 2 to 3 venues.

**Prices are never normalised.** A hotel quotes per night and a hall per event
day; these are different quantities, and three nights is not a wedding. Each
venue carries `price.unit` (`PER_NIGHT` / `PER_EVENT_DAY`) and the response
carries `comparablePrice: false` when they differ. Dividing one into the other
would make a ₹4,500 hotel look ten times cheaper than a ₹50,000 hall.

**A missing attribute is reported, never defaulted.** `missing: ["startingPrice",
"capacity"]` lets the client draw a dash. Half the hotels publish no price and 16
of 43 halls no capacity — this is the common case, not an edge. A missing price
is not a free venue, and a missing capacity is not zero guests.

`repository.ByIDs` loads all three venues, their amenities and their cover images
in **3 queries regardless of venue count** — measured with `log_statement=all`,
not assumed. Never call `Get` in a loop here: that is 9 queries for 3 venues.

Two smaller rules that each hid a bug:

- **`amenityKey` falls back to the name when `code` is NULL**, or every
  code-less amenity collapses into a single row keyed on `""`.
- **The matrix is sorted by display name.** Map iteration order would reshuffle
  the comparison table on every refresh.

`distanceKm` is nil for any pair where either venue lacks coordinates — 35 of 52
facilities still have none, and a guessed 0 reads as "same location".

### Distance from the caller

`?lat=&lng=` carries the app's live location. Accepted by `/facilities/compare`,
`/facilities`, `/halls` and the venue detail; each venue then carries its own
`distanceKm` from the caller, and compare adds `userLocation: true`.

**Optional everywhere, and malformed input degrades to "no location" rather than
400.** A denied GPS permission or a slow fix must not take the venue list down.
Half a location (`lat` with no `lng`), an unparseable value and an out-of-range
one are all treated as absent. So is `0,0` — Null Island is what an
uninitialised location object serialises to far more often than it is a real
position, and accepting it would put every Indian venue ~6000 km away.

**The venue-to-venue matrix under `distanceKm` on compare is unchanged.** The
caller's distance is a per-venue field; the matrix is a separate thing and
clients already read it.

One helper, `parseUserLocation` + `distanceFrom` in
`internal/facility/handler/userlocation.go`, so list, detail and compare cannot
drift. `haversineKm` moved there from `compare.go`. Straight-line, not driving
distance — there is no routing service here.

Duplicate ids are rejected rather than collapsed (comparing a venue with itself
is a client bug), and a well-formed id that does not exist gives 404 rather than
silently returning fewer columns than the client asked for.

## Production configuration

`cfg.Validate()` runs at boot in both binaries. In production (`APP_ENV=prod`
or `production`) any problem is **fatal**; in development they are warnings, so a
laptop still starts on defaults. Every check names the consequence, not the
setting — "acknowledge links sent by SMS would point at the server itself", not
"PUBLIC_BASE_URL looks wrong".

It catches what is silent at startup and only visible once a customer hits it: a
localhost `PUBLIC_BASE_URL` baked into SMS links and media URLs, an empty
`PAYMENT_WEBHOOK_SECRET` (anyone who finds the URL can mark a booking paid),
`sslmode=disable` to a remote host, `OTP_FIXED_CODE`, `LOG_OTP_CODES`, a `*` or
localhost CORS origin, and a short `JWT_SECRET`.

**`.env` overrides the real environment — except for the keys in `envWins`.**
That override exists for stale exported `DB_*` vars from the Java service, but
it meant a container could not set `APP_ENV=production`: the `.env` in the image
won, the service believed it was in development, and every check above was
skipped. `APP_ENV`, `PUBLIC_BASE_URL`, `TRUSTED_PROXIES`, `JWT_SECRET`,
`OTP_FIXED_CODE`, `LOG_OTP_CODES`, `CORS_ORIGINS` and `LOG_LEVEL` now come from
the deployment.

### X-Forwarded-For is only trusted from a configured proxy

`httpx.ClientIP` honours the header **only** when the direct peer matches
`TRUSTED_PROXIES` (CIDRs or bare IPs, comma-separated). Empty means trust
nothing — correct for a directly exposed service.

The previous code trusted the last XFF hop unconditionally, on the reasoning
that a proxy appends it. With no proxy in front the client supplies the whole
header, so one machine bypassed the 5-per-hour registration limit by varying a
string. **Verified before and after**: seven forged requests were all accepted
before, and now give 200 x5 then 429, 429.

This keys the rate limiter *and* the anonymous search-history bucket, so the
same forgery also let one visitor read another's searches.

### 201 vs 200 on a POST

`response.Created(w, message, location, data)` returns **201 with a Location
header**. The envelope is byte-identical to `OK`'s, so a client reading
`body.data` is unaffected — only the status line and one header change.

**Use it only for a true create.** An upsert returns 200, because a client that
branches on 201 would otherwise be told a resource was created when an existing
row was updated. These are upserts and deliberately stay 200:

- `PUT /vendors/me`, `/vendors/me/bank-account` — `ON CONFLICT`
- `POST /devices` — re-registering an install is an upsert by design
- `POST /admin/reviews` — admin correcting a rating
- advance rules, cancellation policies — `ON CONFLICT (facility_id)`

**`POST /bookings/halls` also stays 200**, and this one is subtle: an idempotent
retry returns the *original* booking, so 201 would claim a creation that did not
happen. Reporting it correctly needs the service to tell the handler which of
the two occurred, which is a larger change than the status code.

The Postman `COMMON_TEST` now asserts that any 201 carries a Location header —
the envelope check passes either way, which is exactly how every create sat at
200 unnoticed.

### List responses

Every paginated list returns `httpx.NewPaged`: `content`, `page`, `size`,
`totalElements`, `totalPages`. Three handlers had drifted to ad-hoc maps with
`items`/`venues` keys and no `totalPages`, which breaks a client's single
pagination helper. The notification feed keeps `content` but deliberately has no
page number — it is a keyset cursor, and a page number over a shifting feed is a
number no client can act on.

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

## Coupons

Three scopes, most specific first: one venue (`facility_id`), one vendor's
venues (`vendor_id`), every venue of a type (`facility_type`, admin only, via
`/api/v1/admin/coupons` — halls only today). A CHECK constraint forbids an
unscoped row.

**`pkg/coupon` is the only definition of "applies" and "what it takes off".**
`AppliesSQL` + `LiveSQL` drive checkout, `validate` and the public
`/coupons/available` list, so none can offer a code another refuses. Never
re-derive the scope in a handler — the old `validate` did, skipped it whenever
`facilityId` was omitted, and let one venue's code work at every venue.

**Checkout order: venue discount, then coupon.** `service.DayRate` rounds
exactly like the card's `discountedPrice`; before this the booking charged full
base price while the card advertised 15% off. Packages and add-ons are not
discounted by the venue offer; the coupon comes off the whole sum.

**Redeem and release live in the status transaction.** `coupon.Redeem` is a
conditional UPDATE inside `CreateHallBooking`'s tx (that is what enforces
`usage_limit` under concurrency); `coupon.Release` runs in `SetStatus` on
PENDING/CONFIRMED → CANCELLED/EXPIRED. By `bookings.coupon_id`, never by code —
a deleted coupon's code can be reused.

**The price preview does not match the booking**, and did not before coupons:
`POST /bookings/quote` adds a 2% fee and 18% GST the booking never charges, and
omits add-ons. The coupon is taken off before fee and tax in both. Settle which
side is right before touching either.

## Verification gates vendors, not customers

A customer logs in without verifying. A vendor must confirm their email or
phone before they can log in *or* list a venue.

Login used to refuse any `PENDING_VERIFICATION` account, so a customer who
signed up and had not yet opened the OTP mail was locked out of the account
they had just created - 19 customers were in that state. They are now let in
and the status is **left as PENDING_VERIFICATION** rather than flipped to
ACTIVE, so "never verified" stays visible to ops and can gate a future action.

**`Refresh` carries the same rule as `Login`.** Relaxing only login would let
an unverified customer in and then sign them out at the first token refresh -
the worst of both behaviours. Both check
`status == PENDING_VERIFICATION && HasRole(HALL_OWNER)`.

**`IsVerified()` is email OR phone, never both.** A vendor who registered by
phone has no email to confirm; demanding both would lock them out of their own
listing forever.

`POST /api/v1/facilities` checks verification before `HasVendor`, so an
unverified vendor is told to verify rather than sent to create a business they
already have. **Admin is exempt** - an admin listing a venue on a vendor's
behalf must not be blocked by that vendor's verification state. 69 of 73
vendors already pass; the 4 that do not are the same ones who could not log in
anyway.

Verification gates nothing else: an unverified customer browses, books and pays
like anyone else (verified live).

## A customer and a vendor are separate entities

`ROLE_CUSTOMER` and `ROLE_HALL_OWNER` are mutually exclusive, enforced by the
`trg_customer_vendor_exclusive` trigger on `user_roles` (migration 057).

In the database rather than only in Go, because roles are granted from three
places - registration, the admin create-user screen and `cmd/import` - and a
rule kept in one handler is one new code path away from being broken silently.
The admin create path translates the trigger's `23514` into a 409
`ROLE_CONFLICT`, so an admin reads the reason instead of a 500.

**`ROLE_ADMIN` + `ROLE_CUSTOMER` is still allowed and must stay allowed** - 107
accounts hold exactly that pair and admin tokens list both. The rule is narrow:
customer and vendor specifically, not "one role per user", which would break
every one of them. No row violated it when it was added.

One login API serves both: `POST /auth/login` already returns `user.roles`, and
the client picks the dashboard from that.

### Dashboards, one per entity

```
GET /api/v1/users/me/dashboard    customer
GET /api/v1/vendors/dashboard     vendor
GET /api/v1/admin/dashboard       admin
```

The customer one was missing, so the app assembled that screen from five calls.
It returns `stats`, `pendingActions`, `upcomingBookings`, `favourites`,
`recommended` and `coupons` in six queries - the counts are scalar aggregates
and the rest are capped lists, which one statement cannot express without
`array_agg` of whole rows.

- **A vendor gets 403 `WRONG_DASHBOARD` naming the vendor URL**, not an empty
  customer dashboard, which would read as lost data rather than a wrong screen.
- **`awaitingReview` mirrors what `POST /reviews` enforces** - a
  CONFIRMED/COMPLETED stay, unique per (user, facility) - or the screen prompts
  for a review whose submission 403s.
- **Recommendations degrade rather than empty.** Only 36 of 506 profiles have
  coordinates, so with no usable location it returns top-rated approved halls.
  `?lat=&lng=` overrides the saved profile location; bad input is treated as
  absent, never a 400.
- `pendingActions` are counts, not booleans: "2 unpaid" is actionable, "you
  have unpaid bookings" is not.

## Confirming a booking

```
GET  /api/v1/bookings/owner              the owner's inbox, ?status= filters
POST /api/v1/bookings/{id}/confirm       owner or admin
POST /api/v1/bookings/{id}/reject        owner or admin, body {"reason": "..."}
```

Before this an owner had **no booking routes at all**: a request arrived as a
notification and could then be found nowhere in the API, and the only way to
reach CONFIRMED was paying in full. Zero bookings had ever been CONFIRMED.

**CONFIRMED means "the venue accepted", not "the money arrived."** A hall
agrees an advance offline and the balance comes later, so the owner can confirm
an unpaid booking. Payment still confirms on its own, as before.

That has two consequences, both of which bit on the first run:

- **A CONFIRMED booking must stay payable.** `payments/create` only allowed
  PENDING, so confirming first made the balance impossible to settle - the
  booking owed money it could never pay. Caught by newman: the refund request
  collapsed to `/api/v1/refunds/` because no payment id was ever captured.
- **The payment history row must read the real previous status.** It hardcoded
  `PENDING -> CONFIRMED`, which became a false record once an owner could
  confirm first. It now captures the status before the update and writes
  `CONFIRMED -> CONFIRMED  Payment received`.

**REJECTED is its own status (migration 058), not CANCELLED.** A customer
cancelling is their own choice; a venue refusing is something the customer did
not ask for, carries a reason, and is the number an owner is judged on.
Folding them together makes "rejection rate" unanswerable.

**A rejected booking releases its inventory**, or the venue stays blocked by a
booking it just turned down - verified by rebooking the same date immediately
after. It releases its coupon too, via the same rule in `SetStatus`.

Only a PENDING booking can be decided: a second confirm is a 409 naming the
current status, never a silent no-op, and a cancelled booking can never be
revived. A customer cannot confirm their own booking - they have cancel.

## Decision audit

Every approve/reject writes one row to `audit_logs` through `pkg/audit`:
facility status, vendor KYC, quote settle, booking cancel. Before this, a
verdict lived only in the entity's own column - `facilities.status`,
`vendors.kyc_rejection_reason`, `quotes.rejection_reason`, and for a booking
nowhere at all - so "why was this rejected, and by whom" had no answer and
every entity needed its own query to count.

- **Best-effort, never fatal.** A failed audit write is logged loudly and the
  decision stands. Rolling back an approval because a log row would not insert
  turns observability into an outage.
- **A system actor is NULL, not 0.** `user_id` has an FK; 0 is not a real user
  and the insert would be rejected.
- **An absent reason omits the key** rather than storing `""`, because the
  analytics count rows "with a reason".
- **`eventType` rides on a booking's row.** The occasion is copied onto the
  audit entry rather than joined back from `bookings`, which would lose every
  booking later deleted.

`GET /api/v1/admin/analytics/decisions` reads those rows: counts per entity and
status, a rejection rate, the top rejection reasons, and a breakdown by event
type. `?entity=`, `?from=`, `?until=`.

- **It reads the audit, not current status.** A listing rejected then approved
  is indistinguishable from one approved first time if you only count statuses.
- **A bare `until=YYYY-MM-DD` means the end of that day**, or `?until=today`
  returns nothing that happened today.
- **`rejectionRate` is null, not 0, when nothing was decided** - 0% reads as
  "we rejected nobody", which is not "nothing happened".
- An unknown `?entity=` is a 400, not an empty result: a typo must not look
  like a quiet month.

### Coupons near the caller

`GET /api/v1/coupons/available?lat=&lng=&radiusKm=` — the signed-in offers
screen. A venue-scoped coupon is shown only when its venue is within the
radius (50 km by default).

**The live `lat`/`lng` beats the saved profile location.** It used to read the
profile only, and just 54 of 559 profiles have coordinates — so for ~90% of
users the radius filter did nothing. A phone knows where it is now; a profile
address set once does not.

**A coupon is hidden only when we can prove it is far away.** No coordinates on
either side means "not stated", never "no" — hiding every offer because a phone
refused GPS looks like a broken screen. Verified: from Bengaluru only the
Bengaluru venue's coupon shows, from Delhi only the Delhi one, with no location
both appear, and a point 20 km north reports `distanceKm: 20.04`.

**`radiusKm` is capped at 500 and an out-of-range value is a 400**, not a quiet
fallback to 50 km — a silent fallback answers a question the caller did not
ask, and that is exactly how a `radiusKm=3000` test looked like a broken filter
until the cap was made loud.

## Coupon scope: HALL, HOTEL or ALL

`POST /api/v1/admin/coupons` takes `appliesTo`. It was hardcoded to
`MARRIAGE_HALL`, so an admin coupon could never reach a hotel.

**`appliesTo` defaults to HALL when omitted.** Every admin coupon written
before this field existed was halls-only, and defaulting to ALL would silently
widen all of them.

`ALL` is a value in `facility_type`, not a NULL - `chk_coupons_scope` still
guarantees no coupon can mean "applies nowhere". `coupon.AppliesSQL` tests the
`ALL` branch **before** `c.facility_type = f.type`, or the equality would be
reached first and never match the literal. The no-venue branch of the public
offers list queries `IN (MARRIAGE_HALL, ALL)`, or an all-venue coupon would be
invisible on a screen that checkout nevertheless accepts it on.

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
