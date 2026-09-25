# One image, two binaries: compose runs /app/api and /app/worker from it.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/api ./cmd/api && CGO_ENABLED=0 go build -o /out/worker ./cmd/worker

FROM alpine:3.22
# tzdata: time zones are resolved at runtime; wget (busybox) serves the healthcheck.
RUN apk add --no-cache tzdata && adduser -D -u 10001 app
WORKDIR /app
COPY --from=build /out/api /out/worker ./
# Created here so the named volume inherits app's ownership on first mount.
RUN mkdir -p uploads/spool && chown -R app:app /app
USER app
EXPOSE 8080
CMD ["/app/api"]
