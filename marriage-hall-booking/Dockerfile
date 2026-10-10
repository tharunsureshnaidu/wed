# One image, two binaries: compose runs /app/api and /app/worker from it.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# -trimpath keeps build-machine paths out of the binary and its panics; -s -w
# drops the symbol table and DWARF, which only a debugger needs.
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/api ./cmd/api && \
    CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/worker ./cmd/worker

FROM alpine:3.22
# tzdata: time zones are resolved at runtime; wget (busybox) serves the healthcheck.
RUN apk add --no-cache tzdata && adduser -D -u 10001 app
WORKDIR /app
COPY --from=build /out/api /out/worker ./
# Created here so the named volume inherits app's ownership on first mount.
RUN mkdir -p uploads/spool && chown -R app:app /app
USER app
EXPOSE 8080
# /livez has no dependencies, so a database outage does not get the container
# restarted. The worker runs from this same image and serves no HTTP, so its
# compose service disables this check.
HEALTHCHECK --interval=10s --timeout=3s --retries=3 CMD wget -qO- http://127.0.0.1:8080/livez >/dev/null || exit 1
CMD ["/app/api"]
