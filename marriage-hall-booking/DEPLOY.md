# Server setup (Amazon Linux 2023, EC2)

No Docker, no Kafka. PostgreSQL 16, Valkey, Go 1.26.

Amazon Linux uses `dnf`, and its service names differ from Ubuntu's —
`postgresql-server` must be initialised by hand, and the cache is Valkey
(service `valkey`, CLI `valkey-cli`), not `redis-server`.

---

## 1. Packages

```bash
sudo dnf update -y
sudo dnf install -y postgresql16-server postgresql16 valkey git make tar
```

Go is not packaged at 1.26 — install it from upstream:

```bash
curl -LO https://go.dev/dl/go1.26.0.linux-amd64.tar.gz
sudo rm -rf /usr/local/go && sudo tar -C /usr/local -xzf go1.26.0.linux-amd64.tar.gz
echo 'export PATH=$PATH:/usr/local/go/bin' | sudo tee /etc/profile.d/go.sh
source /etc/profile.d/go.sh
go version    # go1.26.0
```

On arm64 (t4g/Graviton) use `go1.26.0.linux-arm64.tar.gz` instead — check with
`uname -m` (`x86_64` vs `aarch64`).

No Java: that was only ever needed for Kafka.

---

## 2. PostgreSQL 16

Unlike Ubuntu, the cluster is not created for you:

```bash
sudo postgresql-setup --initdb
sudo systemctl enable --now postgresql
```

Create the database and set a password:

```bash
sudo -u postgres psql -c "CREATE DATABASE venue;"
sudo -u postgres psql -c "ALTER USER postgres WITH PASSWORD 'a-strong-password';"
```

Amazon Linux ships `pg_hba.conf` with `ident` for local TCP, which rejects
password auth. Switch the two `127.0.0.1`/`::1` lines to `scram-sha-256`:

```bash
sudo sed -i -E 's|^(host\s+all\s+all\s+(127\.0\.0\.1/32|::1/128)\s+)ident|\1scram-sha-256|' \
    /var/lib/pgsql/data/pg_hba.conf
sudo systemctl restart postgresql
```

Verify — this must succeed before the app will start:

```bash
PGPASSWORD='a-strong-password' psql -h localhost -U postgres -d venue -c '\conninfo'
```

The app creates its own tables; migrations run automatically on boot.

---

## 3. Valkey

Valkey is a drop-in fork of Redis and speaks the same protocol, so the app's
Redis client talks to it unchanged — `REDIS_ADDR` stays as it is.

```bash
sudo systemctl enable --now valkey
valkey-cli ping     # PONG
```

Used for rate limiting and access-token revocation. If it is down the app still
starts and logs `redis unavailable, rate limiting disabled` — degraded, not
broken.

`make status`, `make limits`, `make unlimit` and `make logs-valkey` detect
`valkey-cli` automatically, falling back to `redis-cli` on machines that still
have Redis.

---

## 4. No Kafka

Set `KAFKA_BROKERS=` (empty) in `.env`. Publishing then becomes a no-op: no
connection attempts, no retry warnings, no errors on the request path. Verified
with the full 134-request API suite — 0 failures, 0 Kafka lines in the log.

It must be set **in `.env`**, not exported in the shell: `.env` deliberately
overrides the process environment, so an exported value is ignored.

Do not run `cmd/worker` in this setup — it exists only to consume Kafka topics
and will otherwise sit logging `kafka unreachable, retrying`. The API is the
whole application without it.

If you moved the Kafka tarball to `/opt/kafka`, it is simply unused — nothing
in the repo references it.

---

## 5. Application

```bash
sudo useradd -r -m -d /opt/venue -s /sbin/nologin venue
sudo cp -r ~/wed/marriage-hall-booking /opt/venue/app
sudo chown -R venue:venue /opt/venue
cd /opt/venue/app
sudo -u venue /usr/local/go/bin/go build -o bin/api ./cmd/api
```

Only the API binary is needed — see §4.

### Configuration

```bash
sudo -u venue cp .env.example .env
sudo -u venue chmod 600 .env
sudo -u venue vi /opt/venue/app/.env
```

| Variable | Value |
|---|---|
| `DB_PASSWORD` | as set in §2 |
| `JWT_SECRET` | **start-up fails without it** — `openssl rand -base64 48` |
| `KAFKA_BROKERS` | **empty** — disables publishing (§4) |
| `PAYMENT_WEBHOOK_SECRET` | must match the gateway or webhooks are rejected |
| `AWS_S3_BUCKET` / `AWS_S3_REGION` | empty means media goes to `./uploads` and is lost on redeploy |
| `APP_ENV` | `production` |
| `CORS_ORIGINS` | your frontend origin, not `*` |
| `OTP_FIXED_CODE` | **delete the line** — it makes every OTP the same value |
| `LOG_OTP_CODES` | **delete the line** — it writes OTPs to the log |

On EC2, attach an IAM role with `s3:PutObject`/`s3:DeleteObject` on the bucket
and leave `AWS_ACCESS_KEY_ID`/`AWS_SECRET_ACCESS_KEY` unset. The SDK picks the
role up automatically and there is no key to leak or rotate.

---

## 6. systemd

```bash
sudo tee /etc/systemd/system/venue-api.service >/dev/null <<'EOF'
[Unit]
Description=Venue booking API
After=network.target postgresql.service valkey.service

[Service]
User=venue
WorkingDirectory=/opt/venue/app
EnvironmentFile=/opt/venue/app/.env
ExecStart=/opt/venue/app/bin/api
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable --now venue-api
```

`WorkingDirectory` matters: with local storage the app writes to `./uploads`
relative to it.

---

## 7. Verify

```bash
curl -s localhost:8080/health
# {"success":true,...,"data":{"database":"UP","status":"UP"}}

journalctl -u venue-api -n 40 --no-pager | grep -iE "storage|redis|kafka"
# backend=s3://<bucket>   <- S3, not local disk
```

`local:uploads` in production means `AWS_S3_BUCKET` is unset and uploads will be
lost on the next deploy. There should be no Kafka lines at all.

---

## 8. nginx and TLS

```bash
sudo dnf install -y nginx
sudo systemctl enable --now nginx
```

Proxy `:8080` behind nginx. Two settings the defaults get wrong here:

```nginx
client_max_body_size 200m;   # videos cap at 200MB; nginx defaults to 1MB
proxy_read_timeout   300s;   # uploads stream through the app
```

For TLS, certbot comes from EPEL on AL2023:

```bash
sudo dnf install -y python3-certbot-nginx
sudo certbot --nginx -d your-domain
```

In the EC2 security group open 80/443 only. Leave 8080 closed so the API is
reachable solely through nginx.

---

## Updating

```bash
cd /opt/venue/app
sudo -u venue git pull
sudo -u venue /usr/local/go/bin/go build -o bin/api ./cmd/api
sudo systemctl restart venue-api
```

Migrations run on start-up and are forward-only — there is no down step, so take
a database backup before deploying a release that adds one.
