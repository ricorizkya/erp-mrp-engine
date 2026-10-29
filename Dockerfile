# ─── Build Stage ────────────────────────────────────────
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

# Build statically linked binary
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o mrp-engine ./cmd/worker/main.go

# ─── Runtime Stage ──────────────────────────────────────
FROM alpine:3.21

RUN apk --no-cache add ca-certificates tzdata
RUN addgroup -S -g 10001 mrp && adduser -S -D -H -u 10001 -G mrp mrp
WORKDIR /app

COPY --from=builder /app/mrp-engine .

USER 10001:10001

CMD ["./mrp-engine"]
