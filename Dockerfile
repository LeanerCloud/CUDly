# ==============================================
# Multi-stage build for cloud-agnostic deployment
# Works on: AWS Lambda, AWS Fargate, GCP Cloud Run, Azure Container Apps
# Supports: ARM64 (default) and AMD64 architectures
# ==============================================

# Build arguments for multi-architecture support
# TARGETARCH and TARGETOS are set automatically by docker buildx
ARG TARGETARCH
ARG TARGETOS=linux

# Build stage
# TODO: Pin to SHA256 digest for reproducible builds:
#   docker buildx imagetools inspect golang:1.25.4-alpine3.21
FROM golang:1.25.4-alpine3.21 AS builder

# Re-declare args for use in this stage
ARG TARGETARCH
ARG TARGETOS

# Install build dependencies
RUN apk add --no-cache \
    git \
    ca-certificates \
    postgresql-client \
    curl

# Set shell with pipefail for safer pipe operations
SHELL ["/bin/ash", "-eo", "pipefail", "-c"]

# Install golang-migrate for database migrations (architecture-aware, checksum-verified)
RUN MIGRATE_ARCH=$([ "$TARGETARCH" = "arm64" ] && echo "arm64" || echo "amd64") && \
    if [ "$MIGRATE_ARCH" = "arm64" ]; then \
      MIGRATE_SHA256="9c95441cc430ffdac89276d14de5e2f18bfafca00796c2895490d62e3776d104"; \
    else \
      MIGRATE_SHA256="26c53c9162c9c4aaa84c47cd12455d4a9ac725befbe82850a5937b5ec1e7b8e6"; \
    fi && \
    curl -Lo migrate.tar.gz "https://github.com/golang-migrate/migrate/releases/download/v4.17.0/migrate.linux-${MIGRATE_ARCH}.tar.gz" && \
    echo "${MIGRATE_SHA256}  migrate.tar.gz" | sha256sum -c - && \
    tar xzf migrate.tar.gz && \
    mv migrate /usr/local/bin/migrate && \
    chmod +x /usr/local/bin/migrate && \
    rm migrate.tar.gz

WORKDIR /app

# Copy go module files
COPY go.mod go.sum ./

# Copy provider modules (multi-module setup)
COPY pkg/go.mod pkg/go.sum ./pkg/
COPY providers/aws/go.mod providers/aws/go.sum providers/aws/
COPY providers/azure/go.mod providers/azure/go.sum providers/azure/
COPY providers/gcp/go.mod providers/gcp/go.sum providers/gcp/

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build unified server binary (cloud-agnostic)
# Supports both ARM64 and AMD64 via build args
# Default: ARM64 for cost optimization (20% savings on AWS Fargate)
RUN echo "Building for ${TARGETOS}/${TARGETARCH}" && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
    -p 6 \
    -ldflags="-s -w -X main.Version=${VERSION:-dev} -X main.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    -o /app/cudly \
    ./cmd/server

# Binary built successfully

# ==============================================
# Frontend build stage
# ==============================================
# TODO: Pin to SHA256 digest for reproducible builds:
#   docker buildx imagetools inspect node:20.19-alpine3.21
FROM --platform=$BUILDPLATFORM node:24-alpine AS frontend-builder

WORKDIR /frontend
COPY frontend/package*.json ./
RUN npm ci && test -d node_modules/.bin/webpack
COPY frontend/ ./
RUN npm run build

# ==============================================
# Runtime stage - multi-arch base image
# ==============================================
# TODO: Pin to SHA256 digest for reproducible builds:
#   docker buildx imagetools inspect alpine:3.21.3
FROM alpine:3.21.3

# Re-declare args for use in this stage
ARG TARGETARCH
ARG TARGETOS

# Install runtime dependencies
RUN apk add --no-cache \
    ca-certificates \
    postgresql-client \
    curl \
    tzdata

# Create non-root user for security
RUN addgroup -g 1000 cudly && \
    adduser -D -u 1000 -G cudly cudly

# Create app directory
WORKDIR /app

# Copy binary, migrations, and frontend from build stages
COPY --from=builder --chown=cudly:cudly /app/cudly /app/cudly
COPY --from=builder --chown=cudly:cudly /usr/local/bin/migrate /usr/local/bin/migrate
COPY --chown=cudly:cudly internal/database/postgres/migrations /app/migrations
COPY --from=frontend-builder --chown=cudly:cudly /frontend/dist /app/static

# Copy unified entrypoint script and set permissions
COPY --chown=cudly:cudly scripts/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

# Switch to non-root user
USER cudly

# Environment defaults
ENV DB_MIGRATIONS_PATH=/app/migrations \
    DB_AUTO_MIGRATE=true \
    RUNTIME_MODE=auto \
    PORT=8080 \
    STATIC_DIR=/app/static \
    GOARCH=${TARGETARCH} \
    GOOS=${TARGETOS}

# Expose HTTP port (used by Fargate, Cloud Run, Container Apps)
# Lambda ignores this
EXPOSE 8080

# Health check (works for HTTP mode, ignored in Lambda mode)
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
    CMD curl -f http://localhost:8080/health || exit 1

# Unified entrypoint handles both Lambda and HTTP modes
ENTRYPOINT ["/entrypoint.sh"]
CMD ["/app/cudly"]

# ==============================================
# Build Instructions:
# ==============================================
#
# Build for ARM64 (AWS Lambda/Fargate with Graviton):
#   docker buildx build --platform linux/arm64 -t cudly:arm64 .
#
# Build for AMD64 (GCP Cloud Run, Azure Container Apps):
#   docker buildx build --platform linux/amd64 -t cudly:amd64 .
#
# CI/CD builds (GitHub Actions):
#   AWS Lambda/Fargate: --platform linux/arm64 (Graviton2, 20% cost savings)
#   GCP Cloud Run:      --platform linux/amd64 (ARM64 not supported)
#   Azure Container Apps: --platform linux/amd64 (ARM64 not supported)
#
# Build and load for local testing:
#   docker buildx build --platform linux/arm64 -t cudly:arm64 --load .
# ==============================================
