# Running it

## Prerequisites

Already installed and running as system services on this machine:

```bash
pg_isready -h 127.0.0.1 -p 5432    # PostgreSQL, database "venue"
redis-cli ping                      # Redis
ss -ltn | grep 9092                 # Kafka
```

If any is down:

```bash
sudo systemctl start postgresql
sudo systemctl start redis-server
# Kafka lives at ~/mine/projects/wed/kafka_2.13-4.3.1
```

Credentials live in `.env` (already filled in for this machine). It deliberately
overrides exported shell variables — this machine exports stale MySQL-era
`DB_*` vars that would otherwise point the app at a dead MySQL on 3306.

## Start

Two processes. The API serves requests; the worker handles background work.

```bash
cd ~/mine/projects/wed/marriage-hall-booking

make start      # builds both, starts them in the background, checks /health
make status     # what is up: api, worker, postgres, redis, kafka
make stop
make restart
```

Or in two terminals, to watch the output directly instead of through log files:

```bash
make run        # terminal 1 — API on :8080
make worker     # terminal 2 — event consumer + expired-hold sweeper
```

Migrations run automatically at API start-up, so there is no separate step. They
are embedded in the binary and applied once each.

## Check it is up

```bash
curl localhost:8080/health
# {"success":true,"message":"Service is healthy","data":{"database":"UP","status":"UP"}}
```

Both processes shut down gracefully on SIGTERM: the API finishes in-flight
requests first.

# Logs

API and worker both append to one file, `logs/app.log`, so `make logs` is a
single stream in real order. Each line names its own component, which is what
makes one file readable - `tail -f` over two files printed `==>` banners and
jumped back and forth instead.

```bash
make logs           # THE ONE YOU WANT - both processes, live, INFO and above
make logs-api       # just the API
make logs-worker    # just the worker
make logs-http      # just HTTP requests
make errors         # past warnings and errors, not a live tail
make otp            # OTP codes and password-reset links
make logs-clear     # start a fresh log
```

`make logs` shows every HTTP request plus everything the worker does, and hides
only DEBUG.

## Levels

| Level | What goes here |
|-------|----------------|
| DEBUG | Internal detail. Off by default. |
| INFO  | Every HTTP request, start-up, shutdown, background work done (`released unpaid bookings`), notifications sent. |
| WARN  | Rejected requests (4xx), OTPs logged instead of sent, Kafka publish failures, Redis unavailable. |
| ERROR | 5xx responses, panics, unhandled errors, anything that needs a person. |

```bash
make logs-error     # only ERROR
make logs-warn      # WARN and ERROR
make logs-debug     # only DEBUG
make logs-http      # only HTTP requests
make logs-all       # no filtering at all
```

To see DEBUG the process must have been started with it enabled - the level is
a threshold at write time, not a filter at read time:

```bash
LOG_LEVEL=debug ./bin/api >> logs/app.log 2>&1 &
# or set LOG_LEVEL=debug in .env and `make restart`
```

## Reading a line

```
21:57:54 WARN  api  request rejected  id=2ddd7583  method=GET  path=/api/v1/auth/me  status=401  took=0s
└ time   └ level └ component          └ fields
```

`id` is also returned to the caller as the `X-Request-Id` header, so a response
someone saw can be traced back:

```bash
grep 2ddd7583 logs/app.log
```

`component` is `api` or `worker`. Both processes append to the same file;
each log line is written in a single append, so lines never interleave.

## JSON

For a log shipper, set `LOG_FORMAT=json` in `.env`:

```json
{"time":"2026-09-12T21:58:35+05:30","level":"WARN","msg":"request rejected",
 "component":"api","id":"b141654f","method":"GET","path":"/api/v1/auth/me","status":401}
```

## Infrastructure logs

Deliberately NOT part of `make logs` - Kafka's broker chatter and PostgreSQL's
internals drown the application's own output, and none of it is actionable from
here. Kafka's client libraries are also silenced at the source (`pkg/events`,
`cmd/worker`), so a publish or read failure is reported once, by this
application, at WARN.

```bash
make logs-kafka     # the Kafka broker
make logs-db        # PostgreSQL
make logs-redis     # Redis
```

## OTPs and reset links

There is no mail service, so these are logged at WARN rather than sent - the
level is a reminder that it is not production behaviour:

```bash
make otp
# 21:58:28 WARN api  OTP logged instead of sent (LOG_OTP_CODES=true)  target=lvl@ex.com  code=065047
```

Turn it off with `LOG_OTP_CODES=false` in `.env`, which you must do before this
faces real users.

## Live data

Sometimes the database is the better log:

```bash
PGPASSWORD=postgres psql -h 127.0.0.1 -U postgres -d venue

SELECT identifier, success, failure_reason, attempted_at
  FROM login_attempts ORDER BY attempted_at DESC LIMIT 20;

SELECT id, status, total_amount, paid_amount, expires_at
  FROM bookings ORDER BY created_at DESC LIMIT 20;

SELECT action, entity_name, entity_id, created_at FROM audit_logs;

SELECT query_text, city, result_count, created_at
  FROM search_events ORDER BY created_at DESC LIMIT 20;
```

Redis holds the rate-limit counters and recent-search lists:

```bash
redis-cli --scan --pattern 'rate_limit:*'
redis-cli --scan --pattern 'search:*'
```

## Try it

No mail service is wired up, so OTPs are written to the API log
(`LOG_OTP_CODES=true`). Register, read the code out of the log, verify:

```bash
curl -X POST localhost:8080/api/v1/auth/register \
  -H 'Content-Type: application/json' \
  -d '{"fullName":"Test User","email":"you@example.com","password":"Passw0rd!!"}'

grep 'EMAIL_VERIFICATION for you@example.com' logs/app.log | tail -1

curl -X POST localhost:8080/api/v1/auth/register/verify-email \
  -H 'Content-Type: application/json' \
  -d '{"target":"you@example.com","otpCode":"<the code>"}'
```

That returns an `accessToken` — send it as `Authorization: Bearer <token>` on
anything that needs a login. Access tokens last 1 hour; use
`POST /api/v1/auth/refresh` with the refresh token after that.

Use `/api/v1/auth/register/vendor` instead of `/register` to create a venue
owner, which is what you need to create halls and hotels.

## Postman

Import `postman_collection.json` from the Java project and set:

- `baseUrl` = `http://localhost:8080`

Every endpoint in that collection is implemented here.

## Tests

```bash
make test     # unit + integration + security (needs the API running for the security suite)
make race     # the same under the race detector — takes a couple of minutes
make vet
```

## Ports

| Service    | Port | |
|------------|------|--|
| PostgreSQL | 5432 | database `venue` |
| Redis      | 6379 | rate limiting, recent searches |
| Kafka      | 9092 | domain events |
| API        | 8080 | |
| Worker     | —    | no port; consumes Kafka, sweeps expired holds |

## Troubleshooting

**`bind: address already in use`** — something is already on 8080:
`pkill -f bin/api`, or run with `SERVER_PORT=8081 ./bin/api`.

**`database: failed to connect ... 3306`** — the `.env` file was not found.
Run from the project directory; the API reads `./.env` relative to the working
directory.

**`redis unavailable ... rate limiting disabled`** — a warning, not fatal. The
API still serves; rate limiting fails open until Redis returns.

**`kafka publish ...`** in the log — the API keeps working. Events are
best-effort by design: a booking must never fail because the event bus is down.
