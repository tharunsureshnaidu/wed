# Running with the Makefile

Runs the API and worker as plain processes on your machine, against PostgreSQL,
Valkey (or Redis) and Kafka installed as system services. This is how the
production server runs, and the fastest loop once it is set up.

Setting this up takes a while. If you only want the app running, use
[RUN_WITH_DOCKER.md](RUN_WITH_DOCKER.md).

The commands below are for Ubuntu/Debian. Other systems need the same pieces.

## 1. Prerequisites

| Tool | Why | Check |
|------|-----|-------|
| Go 1.26+ | builds the API and worker | `go version` |
| PostgreSQL 16 (14+ works) | database `venue`, with the `cube`, `earthdistance`, `pg_trgm` extensions | `pg_isready -h 127.0.0.1` |
| Valkey or Redis | rate limiting, recent searches | `valkey-cli ping` or `redis-cli ping` |
| Kafka 4.x at `/opt/kafka` | domain events, run as a systemd unit named `kafka` | `ss -ltn \| grep 9092` |
| `make`, `curl`, `ss` | used by the Makefile | |

### PostgreSQL

```bash
sudo apt install postgresql postgresql-contrib   # contrib holds cube/earthdistance/pg_trgm
sudo -u postgres psql -c "ALTER USER postgres PASSWORD 'postgres';"
sudo -u postgres createdb venue
```

The migrations create the extensions themselves. The database only has to
exist.

### Valkey / Redis

```bash
sudo apt install valkey        # or: sudo apt install redis-server
```

The Makefile detects whichever CLI is installed.

### Kafka

Kafka is the one service the Makefile starts and stops (it holds ~512MB of heap,
so it runs only while you work). It expects a systemd unit called `kafka`:

```bash
# Download Kafka 4.x from https://kafka.apache.org/downloads, then:
sudo tar -xzf kafka_2.13-4.*.tgz -C /opt && sudo mv /opt/kafka_2.13-4.* /opt/kafka
sudo chown -R $USER: /opt/kafka

# One-time KRaft storage format (single node)
cd /opt/kafka
bin/kafka-storage.sh format --standalone -t "$(bin/kafka-storage.sh random-uuid)" -c config/server.properties
```

`/etc/systemd/system/kafka.service` (replace `YOURUSER`):

```ini
[Unit]
Description=Kafka
After=network.target

[Service]
Type=simple
User=YOURUSER
Group=YOURUSER
Environment=KAFKA_HEAP_OPTS=-Xmx512M -Xms256M
ExecStart=/opt/kafka/bin/kafka-server-start.sh /opt/kafka/config/server.properties
ExecStop=/opt/kafka/bin/kafka-server-stop.sh
SuccessExitStatus=143
Restart=on-failure
RestartSec=10

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload     # do NOT enable it - make start/stop manage it
```

`make start` runs `sudo -n systemctl start kafka`, so it needs passwordless
sudo for that command. Without it, start Kafka yourself
(`sudo systemctl start kafka`) and run with `KAFKA_MANAGED=0`. The same flag
applies if Kafka is shared or lives elsewhere. Kafka not at `/opt/kafka`? Pass
`KAFKA_HOME=...`.

**Shortcut:** instead of installing these three services, start them in Docker
with `docker compose up -d postgres valkey kafka` and use `KAFKA_MANAGED=0` below.
See [RUN_WITH_DOCKER.md](RUN_WITH_DOCKER.md#hybrid-infrastructure-in-docker-go-on-the-host).

## 2. Configure

From `marriage-hall-booking/`:

```bash
cp .env.example .env
```

Edit `.env`:

```bash
JWT_SECRET=<output of: openssl rand -base64 48>   # required
DB_PASSWORD=postgres                              # whatever you set above

# Optional, development only: every OTP becomes 000000.
OTP_FIXED_CODE=000000
APP_ENV=dev
```

`.env` **overrides** variables already exported in your shell. That is
deliberate: stale `DB_*` variables in a shell profile have pointed the app at
the wrong database before. Always run from `marriage-hall-booking/`, because
`.env` is read from the current directory.

## 3. Start

```bash
make start       # builds bin/api + bin/worker, starts kafka, both processes, checks /health
make status      # api, worker, postgres, valkey, kafka - up or down
```

Migrations run on API start-up. The seeded admin is **`admin@example.com` /
`Admin@123`**.

```bash
curl localhost:8080/health
```

## Commands

### Running

| Command | What it does |
|---------|--------------|
| `make start` | build, start Kafka, start API + worker in the background, check health |
| `make stop` | stop API + worker, **and Kafka** |
| `make restart` | rebuild and bounce API + worker. Kafka stays up. Use after every code change |
| `make status` | what is running |
| `make run` | API in the foreground (`go run`), for watching output directly |
| `make worker` | worker in the foreground |
| `make build` | just build `bin/api` and `bin/worker` |

**After changing code, use `make restart`.** `make start` on an API that is
already running skips it, and the old binary keeps serving.

### Logs

API and worker both write to `logs/app.log`, one stream in real order.

| Command | Shows |
|---------|-------|
| `make logs` | live, INFO and above. The one you usually want |
| `make logs-api` / `make logs-worker` | one process |
| `make logs-http` | HTTP requests only |
| `make logs-warn` / `make logs-error` | by level |
| `make logs-all` | including DEBUG (needs `LOG_LEVEL=debug` in `.env` + restart) |
| `make errors` | past warnings/errors, not live |
| `make otp` | OTP codes and password-reset links (they are logged, not sent) |
| `make logs-clear` | truncate the log |
| `make logs-db` / `logs-valkey` / `logs-kafka` | infrastructure logs |

Log format and levels: [RUNNING.md](RUNNING.md).

### Rate limits

Registration is 5/hour per IP and login 10/15min. A `429` during testing is
the limiter, not a bug.

| Command | |
|---------|--|
| `make unlimit` | clear all rate-limit counters |
| `make limits` | show current counters and their expiry |

### Quality

| Command | |
|---------|--|
| `make test` | full suite. **Needs Postgres and a running API** (the security tests hit it) |
| `make race` | same, under the race detector (a few minutes) |
| `make vet` / `make fmt` / `make tidy` | the usual |
| `make check` | vet + test |

### Postman

| Command | |
|---------|--|
| `make postman` | regenerate `postman_collection.json` from the routes in the code |
| `make postman-test` | run the whole collection with newman (`npm install -g newman`) |

## Sample venue data (optional)

```bash
gunzip -c dumps/venue_halls_dataonly.sql.gz | PGPASSWORD=postgres psql -h 127.0.0.1 -U postgres -d venue
```

Start the API once first so migrations have created the tables. Use the
data-only dump on a database the API has already migrated. Details are in
`dumps/README.md`.

## Troubleshooting

**`health FAILED`** after `make start`: `tail -50 logs/app.log`. Usually a
missing `JWT_SECRET` or a database that is not reachable.

**`kafka start FAILED (sudo?)`**: no passwordless sudo. Start Kafka yourself
and use `make start KAFKA_MANAGED=0`.

**`api: running (2 copies)`** in `make status`: run `make restart`.

**`database: failed to connect ... 3306`**: you are not in
`marriage-hall-booking/`, so `.env` was not found and stale shell variables won.

**`redis unavailable ... rate limiting disabled`**: a warning. The API still
serves.

**`kafka publish failed`**: the API keeps working. Events are best-effort, and
a booking never fails because Kafka is down.

**`notify: would send`**: not an error. A channel with no credentials in
`.env` logs instead of sending.
