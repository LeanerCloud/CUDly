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
# Image pinned to a SHA256 digest for reproducible builds — a registry
# tag mutation (Docker Hub allows re-tagging) cannot poison this build.
# To refresh: `docker buildx imagetools inspect golang:1.26.6-alpine3.24`
# (or use the Docker Hub API tags endpoint) and update the digest below.
# A Renovate / Dependabot config can automate this if desired.
FROM --platform=$BUILDPLATFORM golang:1.26.6-alpine3.24@sha256:3889b425f035be855a72fb4755265311293b6d414521f0a519d819df32222d83 AS builder

# Re-declare args for use in this stage
ARG TARGETARCH
ARG TARGETOS

# Build metadata stamped into the binary via ldflags and surfaced by the
# public GET /version endpoint. GIT_COMMIT and BUILD_DATE are supplied by the
# terraform build module (modules/build); they default to "unknown" so a bare
# `docker build .` still succeeds without git context.
ARG VERSION=dev
ARG GIT_COMMIT=unknown
ARG BUILD_DATE

# Install build dependencies
RUN apk add --no-cache \
    git \
    ca-certificates \
    postgresql-client

# Set shell with pipefail for safer pipe operations
SHELL ["/bin/ash", "-eo", "pipefail", "-c"]

# Build golang-migrate from source on this stage's pinned Go toolchain, the same
# way `make install-tools` does. Upstream's prebuilt release tarballs carry
# whatever toolchain upstream built them with (v4.19.1 ships go1.25.4), which is
# how issue #1833's stdlib CVEs reached the runtime image, where entrypoint.sh
# runs `migrate up` against the database on every container start.
# `go install` refuses GOBIN when cross-compiling and writes to
# bin/${GOOS}_${GOARCH}/ instead, so resolve both layouts; the final `mv` fails
# the build if neither produced a binary.
# Keep this version in step with MIGRATE_VERSION in the Makefile.
# -tags=pgx5, not postgres: the postgres tag links the lib/pq driver, which
# carries three unfixable advisories (GO-2026-6170/6171/6172, no fixed version
# in any release) reached through Driver.Open and conn.Exec on every container
# start. The pgx5 tag builds the same driver on jackc/pgx v5, which the
# application already uses. It registers the "pgx5" URL scheme only, so
# scripts/entrypoint.sh and .github/workflows/database-migration.yml must build
# pgx5:// URLs - a postgres:// URL against this binary fails at runtime with
# "unknown driver". See issue #1849.
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
      go install -tags=pgx5 -ldflags="-s -w -X main.Version=v4.19.1" \
      github.com/golang-migrate/migrate/v4/cmd/migrate@v4.19.1 && \
    GOPATH_BIN="$(go env GOPATH)/bin" && \
    MIGRATE_BIN="${GOPATH_BIN}/${TARGETOS}_${TARGETARCH}/migrate" && \
    { [ -x "${MIGRATE_BIN}" ] || MIGRATE_BIN="${GOPATH_BIN}/migrate"; } && \
    mv "${MIGRATE_BIN}" /usr/local/bin/migrate

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
    BUILD_TIME="${BUILD_DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}" && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
    -p 6 \
    -ldflags="-s -w -X main.Version=${VERSION:-dev} -X main.BuildTime=${BUILD_TIME} -X main.GitSHA=${GIT_COMMIT:-unknown}" \
    -o /app/cudly \
    ./cmd/server

# Binary built successfully

# ==============================================
# Frontend build stage
# ==============================================
# Image pinned to a SHA256 digest for reproducible builds. Refresh via
# the Docker Hub API tags endpoint and update the digest below when the
# `node:24-alpine` tag is bumped.
FROM --platform=$BUILDPLATFORM node:24-alpine@sha256:d1b3b4da11eefd5941e7f0b9cf17783fc99d9c6fc34884a665f40a06dbdfc94f AS frontend-builder

WORKDIR /frontend
COPY frontend/package*.json ./
# `--no-progress`: disables the npm progress reporter, whose worker has a
#   long-standing race condition in npm 10/11 that surfaces as
#   `npm error Exit handler never called!` on memory-constrained hosts
#   (Linux VMs, low-RAM CI runners). See npm/cli issues for the bug; the
#   fix is "don't run that worker".
# `--maxsockets 1`: serialise registry fetches so peak memory during the
#   install stays low. With 810 lockfile entries, parallel fetch + the
#   gzip-decode workers race the OOM killer on hosts with <2 GB free.
# `--no-audit --no-fund`: skip post-install network calls that aren't
#   relevant to a build context.
# `test -x`: existing guard against silent zero-exit npm failures
#   (kept from #044dc583c — addresses a different failure mode where
#   npm exits 0 but leaves node_modules empty).
RUN npm ci --no-progress --maxsockets 1 --no-audit --no-fund && \
    test -x node_modules/.bin/webpack
COPY frontend/ ./
RUN npm run build

# ==============================================
# Runtime stage - multi-arch base image
# ==============================================
# Image pinned to a SHA256 digest for reproducible builds.
# To refresh: `docker buildx imagetools inspect alpine:3.24.1` and update the
# digest below. This is the multi-arch index digest, not a per-platform one, so
# it stays correct for every TARGETARCH this image is built for.
FROM alpine:3.24.1@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b

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

# Switch to non-root user. Numeric form so the identity is resolvable without
# the image's /etc/passwd: Kubernetes `runAsNonRoot` and similar admission
# checks cannot verify a name-form USER and will refuse to start the pod.
# 1000:1000 is exactly the uid:gid created above, so this is a rename only.
USER 1000:1000

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
#
# JSON (exec) form, but invoking /bin/sh explicitly: the `||` is a shell
# operator, so a bare exec-form list would hand `||` and `exit` to curl as
# literal arguments and the healthcheck would never report unhealthy correctly.
# This is the same process tree the shell form produced, written so the
# dependency on a shell is declared rather than implied.
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
    CMD ["/bin/sh", "-c", "curl -f http://localhost:8080/health || exit 1"]

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
