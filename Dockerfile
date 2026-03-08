# ============================================================================
# kube-deploy-server — Dockerfile
# Multi-stage build for the kube-deploy gRPC server binary.
#
# Usage:
#   docker build -t kube-deploy-server:latest .
#   docker build -t kube-deploy-server:v1.0.0 --build-arg VERSION=v1.0.0 .
#
# Run:
#   docker run -p 9090:9090 -v ~/.kube/config:/home/nonroot/.kube/config:ro \
#     kube-deploy-server:latest
# ============================================================================

# ── Stage 1: Build ──────────────────────────────────────────────────────────
FROM golang:1.23-alpine AS builder

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

RUN apk add --no-cache git ca-certificates tzdata make

WORKDIR /src

# Copy dependency manifests first for layer caching.
COPY go.mod go.sum ./
RUN go mod download && go mod verify

# Copy the entire source tree.
COPY . .

# Build the server binary — static, stripped, trimmed.
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-s -w -extldflags '-static' \
      -X main.version=${VERSION} \
      -X main.commit=${COMMIT} \
      -X main.buildDate=${BUILD_DATE}" \
    -trimpath \
    -o /bin/kube-deploy-server \
    ./cmd/kube-deploy-server

# Build the CLI binary as well (useful for debugging inside the cluster).
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-s -w -extldflags '-static' \
      -X main.version=${VERSION} \
      -X main.commit=${COMMIT} \
      -X main.buildDate=${BUILD_DATE}" \
    -trimpath \
    -o /bin/kdctl \
    ./cmd/kdctl

# ── Stage 2: Runtime ───────────────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12:nonroot

ARG VERSION=dev

LABEL maintainer="sonu"
LABEL app="kube-deploy-server"
LABEL version="${VERSION}"
LABEL description="Zero-downtime Kubernetes deployment pipeline — gRPC control plane server"
LABEL org.opencontainers.image.source="https://github.com/sonu/kube-deploy"
LABEL org.opencontainers.image.title="kube-deploy-server"
LABEL org.opencontainers.image.description="gRPC server for zero-downtime Kubernetes deployments with automated rollback"

# Copy timezone data and CA certificates from the builder.
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Copy the compiled binaries.
COPY --from=builder /bin/kube-deploy-server /kube-deploy-server
COPY --from=builder /bin/kdctl /kdctl

# Expose the gRPC server port.
EXPOSE 9090

# Run as non-root user (65532 is the nonroot user in distroless).
USER 65532:65532

ENTRYPOINT ["/kube-deploy-server"]
CMD ["--port=9090", "--log-format=json", "--log-level=info"]
