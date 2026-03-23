# --- Build stage ---
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder

WORKDIR /src

# Cache dependency downloads. Go modules are fetched based on go.mod/go.sum,
# so copying these first means Docker can cache this layer and skip the
# download on subsequent builds if dependencies haven't changed.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO_ENABLED=0 produces a static binary — no libc dependency at runtime.
# This is what lets us use 'scratch' (empty) as the final base image.
ARG TARGETOS TARGETARCH
# Build both binaries in a single layer to share compilation cache.
# Go's build cache means shared packages (config, fpl, store, poller)
# are compiled once and reused across both targets.
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -ldflags="-s -w" -o /bot ./cmd/bot && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -ldflags="-s -w" -o /backfill ./cmd/backfill

# --- Runtime stage ---
FROM scratch

# TLS root certificates so the binary can call HTTPS endpoints (FPL API).
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Run as non-root (Principle of Least Privilege).
USER 65534:65534

COPY --from=builder /bot /bot
COPY --from=builder /backfill /backfill

ENTRYPOINT ["/bot"]
