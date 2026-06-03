# =============================================================================
# Multi-stage Docker build for the idempotency plugin example application.
#
# Build:
#   docker build -t idempotency-example:latest .
#
# Run (memory storage, no Redis needed):
#   docker run -p 8080:8080 idempotency-example:latest
#
# Run (with Redis, see docker-compose.yml):
#   docker compose up
# =============================================================================

# ---- Stage 1: Build ----
FROM golang:1.25-alpine AS builder

# Install git for private module resolution (if needed)
RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /src

# Copy the root module files for dependency caching
COPY go.mod go.sum ./
RUN go mod download

# Copy the entire project source
COPY . .

# Capture build metadata
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_TIME=unknown

# Build the example binary with stripped debug info and version injection
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath \
    -ldflags="-s -w -X main.Version=${VERSION} -X main.Commit=${COMMIT} -X main.BuildTime=${BUILD_TIME}" \
    -o /bin/idempotency-example \
    ./examples/gin/

# ---- Stage 2: Runtime ----
FROM scratch

# Copy CA certificates for HTTPS and timezone data
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

# Copy the built binary
COPY --from=builder /bin/idempotency-example /bin/idempotency-example

# Copy static assets for the test UI
COPY --from=builder /src/examples/gin/static /static

# Expose the default port
EXPOSE 8080

# Run as non-root (scratch has no users; use a numeric UID)
USER 65534:65534

ENTRYPOINT ["/bin/idempotency-example"]
