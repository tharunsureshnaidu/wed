# Running with Docker

The quickest way to get the whole stack up. You need only **Docker** with the
Compose plugin (`docker compose version` should work) — no Go, PostgreSQL or
Kafka on your machine.

What you get:

| Service    | Container | Host port | |
|------------|-----------|-----------|--|
| PostgreSQL 16 | `postgres` | 5432 | database `venue`, user/password `postgres` |
| Valkey 8   | `valkey`   | 6379 | rate limiting, recent searches (Redis-compatible) |
| Kafka 4.3  | `kafka`    | 9092 | domain events (single node, KRaft) |
| API        | `api`      | 8080 | HTTP server |
| Worker     | `worker`   | —    | Kafka consumers, notifications, expired-hold sweeper |

## 1. Configure

All commands run from `marriage-hall-booking/`.

```bash
cp .env.example .env
```

Then edit `.env`:

```bash
# Required - the API refuses to start without it
JWT_SECRET=<paste the output of: openssl rand -base64 48>

# Optional, development only: every OTP becomes 000000, so you never have to
# read codes out of the log. The API refuses to start with this outside dev.
OTP_FIXED_CODE=000000
APP_ENV=dev
```

Leave `DB_HOST`, `REDIS_ADDR` and `KAFKA_BROKERS` as they are. Compose
overrides them inside the containers (`postgres`, `valkey:6379`,
`kafka:29092`). The `localhost` values in `.env` are for running Go on the host
(see [Hybrid](#hybrid-infrastructure-in-docker-go-on-the-host)).

Leave `AWS_S3_BUCKET` empty unless you have credentials. Empty means uploads
are stored in a Docker volume and served by the API.

## 2. Start

```bash
docker compose up -d --build
```

The first build takes a few minutes. Containers start in dependency order:
postgres, valkey and kafka must be healthy before the API starts, and the API
must be healthy (migrations applied) before the worker starts.

```bash
docker compose ps                  # every service should be "healthy" / "running"
curl localhost:8080/health
# {"success":true,"message":"Service is healthy","data":{"database":"UP","status":"UP"}}
```

## 3. Log in

Migrations seed a development admin: **`admin@example.com` / `Admin@123`**.

```bash
curl -sS -X POST localhost:8080/api/v1/auth/login -H 'Content-Type: application/json' \
  -d '{"identifier":"admin@example.com","password":"Admin@123"}'
```

Register a normal user (the field is `fullName`, not `name`):

```bash
ts=$(date +%s); EMAIL="t+$ts@example.com"
curl -sS -X POST localhost:8080/api/v1/auth/register -H 'Content-Type: application/json' \
  -d "{\"fullName\":\"T\",\"email\":\"$EMAIL\",\"phoneNumber\":\"9$ts\",\"password\":\"Passw0rd!23\"}"
curl -sS -X POST localhost:8080/api/v1/auth/register/verify-email -H 'Content-Type: application/json' \
  -d "{\"target\":\"$EMAIL\",\"otpCode\":\"000000\"}"
```

Without `OTP_FIXED_CODE`, read the code from the log:
`docker compose logs api | grep 'OTP logged'`.

## Everyday commands

```bash
docker compose logs -f api worker      # follow the app (both processes)
docker compose logs -f kafka           # infrastructure, when you need it
docker compose up -d --build api worker  # after changing Go code: rebuild + restart
docker compose restart api             # after changing .env only
docker compose stop                    # stop, keep data
docker compose down                    # remove containers, keep data
docker compose down -v                 # remove containers AND wipe database, Kafka, uploads
```

**Code changes need `--build`.** A plain `restart` reruns the old image.

### Database shell

```bash
docker compose exec postgres psql -U postgres -d venue
```

### Rate limits

Registration is limited to 5/hour per IP, so manual testing runs out quickly. A
`429` is the limiter, not a bug. Clear the counters with:

```bash
docker compose exec valkey sh -c "valkey-cli --scan --pattern 'rate_limit:*' | xargs -r valkey-cli DEL"
```

### Sample venue data (optional)

The database starts with only the admin account. To load 1,134 scraped marriage
halls and their reviews (see `dumps/README.md`), with the stack running:

```bash
gunzip -c dumps/venue_halls_dataonly.sql.gz | docker compose exec -T postgres psql -U postgres -d venue
```

Use the **data-only** dump, not `venue_halls_full`. The API has already created
the schema, and the full dump would conflict with it. The load is safe to run
twice.

## Hybrid: infrastructure in Docker, Go on the host

Best for a fast edit-run loop. You need Go 1.26 installed.

```bash
docker compose up -d postgres valkey kafka
```

Set `DB_PASSWORD=postgres` in `.env`; the other defaults in `.env.example`
already point at `localhost`. Then run the API and worker directly:

```bash
make run        # terminal 1 - API on :8080
make worker     # terminal 2
```

`make start` also works, but pass `KAFKA_MANAGED=0` so it does not try to start
a systemd Kafka service: `make start KAFKA_MANAGED=0`. Kafka advertises
`localhost:9092` to host processes and `kafka:29092` to containers, so both
modes reach the same broker.

## Port already in use

If you already run PostgreSQL, Redis or Kafka locally, move the container's
host port:

```bash
PG_PORT=5433 VALKEY_PORT=6380 KAFKA_PORT=9093 API_PORT=8081 docker compose up -d
```

Available: `PG_PORT`, `VALKEY_PORT`, `KAFKA_PORT`, `API_PORT`. This only
changes the port on your machine; containers still talk to each other on the
defaults. In hybrid mode, match `.env` (for example `DB_PORT=5433`).

## Troubleshooting

**`env file .env not found`**: run `cp .env.example .env` first.

**`api` keeps restarting with `JWT_SECRET is required`**: set it in `.env`,
then `docker compose up -d`.

**`api` points at `localhost` inside the container**: `.env` got baked into
an image. It is excluded by `.dockerignore`; do not remove that line. The app
lets `.env` override the container environment.

**`notify: would send` in the worker log**: not an error. Email, SMS, WhatsApp
and push fall back to logging when their credentials in `.env` are empty.

**`media is being written to local disk`**: expected without `AWS_S3_BUCKET`.
Uploads live in the `uploads` volume, shared by the API and worker, and
`down -v` deletes them.

For the log format, levels and what each line means, see [RUNNING.md](RUNNING.md).
