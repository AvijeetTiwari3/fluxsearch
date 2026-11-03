# ────────────────────────────────────────────────────────────────
# Stage 1: Build — Go 1.22 Alpine
# ────────────────────────────────────────────────────────────────
FROM golang:1.22-alpine AS builder

RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /app

COPY . .
RUN go mod tidy
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-w -s" \
    -o /fluxsearch ./cmd/server/main.go

# ────────────────────────────────────────────────────────────────
# Stage 2: Runtime — scratch (zero attack surface)
# ────────────────────────────────────────────────────────────────
FROM scratch

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /fluxsearch /fluxsearch

EXPOSE 8080 10000

ENTRYPOINT ["/fluxsearch"]
