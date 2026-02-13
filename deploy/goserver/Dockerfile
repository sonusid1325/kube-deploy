# ============================================================================
# goserver — Test application for kube-deploy
# Multi-stage build for minimal production image
# ============================================================================

# Stage 1: Build
FROM golang:1.23-alpine AS builder

ARG VERSION=v1
ARG FAIL_AFTER=""

RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /build

# Copy only the Go source (goserver is a standalone single-file app)
COPY main.go .

# Initialize a standalone module for goserver
RUN go mod init goserver && \
    go mod tidy

# Build a statically-linked binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-s -w -extldflags '-static'" \
    -trimpath \
    -o /goserver \
    main.go

# Stage 2: Runtime — distroless for minimal attack surface
FROM gcr.io/distroless/static-debian12:nonroot

ARG VERSION=v1
ARG FAIL_AFTER=""

# Labels for container metadata
LABEL maintainer="sonu"
LABEL app="goserver"
LABEL version="${VERSION}"
LABEL description="Test HTTP server for kube-deploy zero-downtime deployment pipeline"

# Copy timezone data and CA certificates from builder
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Copy the compiled binary
COPY --from=builder /goserver /goserver

# Set environment variables
ENV VERSION=${VERSION}
ENV FAIL_AFTER=${FAIL_AFTER}
ENV PORT=8080
ENV FAIL_MODE=unhealthy

# Expose the application port
EXPOSE 8080

# Health check — Docker-level health check (Kubernetes uses its own probes)
# This is useful when running outside of Kubernetes (e.g., docker-compose)
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
    CMD ["/goserver", "-h"] || exit 1

# Run as non-root user (65532 is the nonroot user in distroless)
USER 65532:65532

# Start the server
ENTRYPOINT ["/goserver"]
