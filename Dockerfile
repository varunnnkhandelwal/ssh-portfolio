# ── Stage 1: Build ────────────────────────────────────────────────────────────
FROM golang:1.22-alpine AS builder

WORKDIR /app

# Copy dependency files first for layer caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build a fully static binary
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o portfolio .

# ── Stage 2: Runtime ──────────────────────────────────────────────────────────
FROM alpine:latest

# ca-certificates for any outbound TLS (e.g. openURL calls)
RUN apk add --no-cache ca-certificates

WORKDIR /app

COPY --from=builder /app/portfolio .

# /data is the Fly.io persistent volume mount point.
# The SSH host key is stored there so it survives deploys.
VOLUME ["/data"]

EXPOSE 2323

CMD ["/app/portfolio"]
