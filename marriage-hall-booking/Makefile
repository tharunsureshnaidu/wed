# Same path on the dev box and the server, so nothing has to be overridden per
# machine. `make KAFKA_HOME=... logs-kafka` if yours lives elsewhere.
KAFKA_HOME ?= /opt/kafka

# Valkey is a drop-in fork of Redis, so the CLI is wire-compatible and only the
# binary and unit names differ. Detected rather than hardcoded: the server runs
# valkey while some dev machines still have redis, and the same Makefile has to
# work on both. Override with `make VALKEY=... status` if yours is elsewhere.
VALKEY ?= $(shell command -v valkey-cli 2>/dev/null || command -v redis-cli 2>/dev/null || echo valkey-cli)
VALKEY_UNIT ?= $(shell systemctl list-unit-files 2>/dev/null | grep -qE '^valkey\.service' && echo valkey || echo redis-server)
DB  ?= postgres://postgres:postgres@localhost:5432/venue?sslmode=disable
API ?= http://localhost:8080

.PHONY: run worker build start stop restart status logs logs-all logs-api logs-worker logs-error logs-warn logs-debug logs-http logs-clear unlimit limits logs-db logs-valkey logs-kafka errors otp postman postman-test test race vet fmt tidy check

## --- running ---
run:    ; go run ./cmd/api
worker: ; go run ./cmd/worker
build:  ; go build -o bin/api ./cmd/api && go build -o bin/worker ./cmd/worker

# Start both in the background. Both append to the SAME logs/app.log: each
# line already carries its component (api/worker), so one file is one
# chronological stream - which `tail -f a.log b.log` never was, it printed
# "==>" banners and jumped between files.
#
# Each is skipped if already running. The API would refuse a second copy anyway
# (the port is taken), but the worker binds no port, so without this check a
# second `make start` silently leaves two of them running.
start: build
	@mkdir -p logs
	@[ -n "$(PIDS_API)" ] \
		&& echo "api    already running" \
		|| { $(PWD)/bin/api >> logs/app.log 2>&1 & echo "api    started"; }
	@[ -n "$(PIDS_WORKER)" ] \
		&& echo "worker already running" \
		|| { $(PWD)/bin/worker >> logs/app.log 2>&1 & echo "worker started"; }
	@sleep 2 && curl -fsS $(API)/health >/dev/null && echo "health OK" || echo "health FAILED - check logs/app.log"
	@echo "logs:  make logs"

# pkill -f would also match make's own command line (it contains "bin/api") and
# kill the make process mid-target. -x matches the executable path exactly.
# Matches both spellings: a process started as ./bin/api and one started with
# the absolute path. Matching only "$(PWD)/bin/api" missed the relative form,
# and a stale copy kept running - and kept writing to the log - through every
# stop/start cycle. pgrep -x on the basename is confined to this project by the
# cwd check, so it cannot hit another Go binary of the same name.
PIDS_API    = $$(pgrep -f '(^|/)bin/api$$'    2>/dev/null | while read p; do [ "$$(readlink -f /proc/$$p/cwd 2>/dev/null)" = "$(PWD)" ] && echo $$p; done)
PIDS_WORKER = $$(pgrep -f '(^|/)bin/worker$$' 2>/dev/null | while read p; do [ "$$(readlink -f /proc/$$p/cwd 2>/dev/null)" = "$(PWD)" ] && echo $$p; done)

stop:
	@p="$(PIDS_API)";    [ -n "$$p" ] && { kill $$p 2>/dev/null; echo "api stopped";    } || echo "api not running"
	@p="$(PIDS_WORKER)"; [ -n "$$p" ] && { kill $$p 2>/dev/null; echo "worker stopped"; } || echo "worker not running"
	@sleep 1
	@sleep 1

restart:
	@$(MAKE) stop
	@$(MAKE) start

status:
	@n=$$(echo "$(PIDS_API)" | wc -w); \
		[ "$$n" = 1 ] && echo "api:    running" || { [ "$$n" = 0 ] && echo "api:    stopped" || echo "api:    running ($$n copies - run 'make restart')"; }
	@n=$$(echo "$(PIDS_WORKER)" | wc -w); \
		[ "$$n" = 1 ] && echo "worker: running" || { [ "$$n" = 0 ] && echo "worker: stopped" || echo "worker: running ($$n copies - run 'make restart')"; }
	@pg_isready -h 127.0.0.1 -p 5432 >/dev/null 2>&1 && echo "postgres: up" || echo "postgres: DOWN"
	@$(VALKEY) ping >/dev/null 2>&1 && echo "valkey:   up" || echo "valkey:   DOWN"
	@(ss -ltn 2>/dev/null | grep -q 9092) && echo "kafka:    up" || echo "kafka:    DOWN"

## --- logs ---
# `make logs` is the one you want: everything both processes do, live, in
# order, INFO and above - every API request included. Kafka's own broker
# chatter is off at the source (see pkg/events and cmd/worker).
LOG = logs/app.log

logs:       ; @tail -f -n 60 $(LOG) | grep --line-buffered -vE ' DEBUG '

# Everything, DEBUG included. Needs LOG_LEVEL=debug set before `make start`.
logs-all:   ; @tail -f -n 60 $(LOG)

# One process only.
logs-api:    ; @tail -f -n 60 $(LOG) | grep --line-buffered ' api '
logs-worker: ; @tail -f -n 60 $(LOG) | grep --line-buffered ' worker '

# By level. WARN shows warnings and errors; ERROR shows only errors.
logs-error: ; @tail -f -n 200 $(LOG) | grep --line-buffered -E ' ERROR '
logs-warn:  ; @tail -f -n 200 $(LOG) | grep --line-buffered -E ' (WARN|ERROR) '
logs-debug: ; @tail -f -n 200 $(LOG) | grep --line-buffered -E ' DEBUG '

# Every HTTP request and nothing else.
logs-http:  ; @tail -f -n 200 $(LOG) | grep --line-buffered -E ' request'

# Past errors, not a live tail.
errors:
	@grep -E ' (WARN|ERROR) ' $(LOG) | tail -40

# OTPs and reset links, which are logged instead of being sent.
otp:
	@grep 'OTP logged\|reset link logged' $(LOG) | tail -20

# Start a fresh log.
logs-clear: ; @: > $(LOG) && echo "cleared $(LOG)"

## --- rate limit ---
# Registration is 5/hour per IP, login 10/15min - realistic in production,
# but exhausted in a minute of manual Postman testing. This clears the
# counters; it does not disable the limiter.
unlimit:
	@n=$$($(VALKEY) --scan --pattern 'rate_limit:*' | wc -l); \
		$(VALKEY) --scan --pattern 'rate_limit:*' | xargs -r $(VALKEY) DEL >/dev/null; \
		echo "cleared $$n rate-limit counter(s)"

# What is currently counted against you, and for how long.
limits:
	@$(VALKEY) --scan --pattern 'rate_limit:*' | sort | while read k; do \
		printf '%-40s %4s  expires in %ss\n' "$$k" "$$($(VALKEY) GET $$k)" "$$($(VALKEY) TTL $$k)"; \
	done; echo "(limits: register 5/hr, login 10/15min, otp-resend 3/15min, global 100/min)"

# Infrastructure, kept separate on purpose - none of it belongs in `make logs`.
logs-db:
	@sudo tail -f /var/log/postgresql/postgresql-*-main.log
logs-valkey:
	@sudo journalctl -u $(VALKEY_UNIT) -f
logs-kafka:
	@tail -f $(KAFKA_HOME)/logs/server.log

## --- postman ---
# Regenerate the collection from the routes registered in the Go code.
postman:
	@python3 scripts/gen_postman.py > postman_collection.json

# Run the whole collection with newman. A full run is 125 requests against a
# 100/min global limit, so the limiter is switched off for this run only.
postman-test: build postman
	@command -v newman >/dev/null || { echo "npm install -g newman"; exit 1; }
	@$(MAKE) stop >/dev/null 2>&1 || true
	@sleep 1
	@RATE_LIMIT_DISABLED=true $(PWD)/bin/api > logs/app.log 2>&1 & sleep 3
	@python3 scripts/postman_run.py || true
	@$(MAKE) stop >/dev/null 2>&1 || true

## --- quality ---
test:  ; TEST_DATABASE_URL="$(DB)" API_URL="$(API)" go test ./...
race:  ; TEST_DATABASE_URL="$(DB)" API_URL="$(API)" go test -race ./...
vet:   ; go vet ./...
fmt:   ; gofmt -w .
tidy:  ; go mod tidy
check: vet test
