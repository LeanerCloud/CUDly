# CUDly Development Guide

## Prerequisites

- Docker and Docker Compose
- Go 1.25+
- Node.js and npm (for frontend development)
- Make (optional, for convenience commands)

## Quick Start

### 1. Start Local Environment

```bash
# Start PostgreSQL and CUDly application
docker-compose up -d

# View logs
docker-compose logs -f app

# Stop environment
docker-compose down
```

### 2. Access Services

- **CUDly API**: <http://localhost:8080>
- **PostgreSQL**: localhost:5432
  - Database: `cudly`
  - User: `cudly`
  - Password: `cudly_local_dev`
- **pgAdmin** (optional): <http://localhost:5050>
  - Email: `admin@cudly.local`
  - Password: `admin`
  - Start with: `docker-compose --profile tools up`

### 3. Database Access

```bash
# Connect to PostgreSQL using psql
docker-compose exec postgres psql -U cudly -d cudly

# Run migrations manually
docker-compose exec app migrate -path /app/internal/database/postgres/migrations \
    -database "postgresql://cudly:cudly_local_dev@postgres:5432/cudly?sslmode=disable" up

# Check migration status
docker-compose exec app migrate -path /app/internal/database/postgres/migrations \
    -database "postgresql://cudly:cudly_local_dev@postgres:5432/cudly?sslmode=disable" version
```

## Development Workflow

### Hot Reload

The development environment uses **Air** for hot reload. Any changes to `.go` files automatically trigger a rebuild and restart.

```bash
# Air automatically detects changes and reloads
docker-compose logs -f app
```

### Database Migrations

```bash
# Create a new migration
migrate create -ext sql -dir internal/database/postgres/migrations -seq add_new_feature

# Run migrations
docker-compose exec app migrate -path /app/internal/database/postgres/migrations \
    -database "postgresql://cudly:cudly_local_dev@postgres:5432/cudly?sslmode=disable" up

# Rollback last migration
docker-compose exec app migrate -path /app/internal/database/postgres/migrations \
    -database "postgresql://cudly:cudly_local_dev@postgres:5432/cudly?sslmode=disable" down 1
```

## Environment Variables

Configured in docker-compose.yml:

### Database

- `DB_HOST`: PostgreSQL hostname (docker-compose: `postgres`)
- `DB_PORT`: PostgreSQL port (default: `5432`)
- `DB_NAME`: Database name (default: `cudly`)
- `DB_USER`: Database user (default: `cudly`)
- `DB_PASSWORD`: Database password (docker-compose: `cudly_local_dev`)
- `DB_SSL_MODE`: SSL mode (app default: `require`; docker-compose overrides to `disable`)
- `DB_AUTO_MIGRATE`: Auto-run migrations on startup (default: `true`)

### Secrets

- `SECRET_PROVIDER`: Secret manager provider (default: `env`)
  - `env`: Use environment variables (suitable for local dev)
  - `aws`: AWS Secrets Manager
  - `gcp`: GCP Secret Manager
  - `azure`: Azure Key Vault

### Multi-Account Credential Encryption

CUDly encrypts stored cloud account credentials with AES-256-GCM. The encryption key must be provided via one of:

- **Local dev** — set `CREDENTIAL_ENCRYPTION_KEY` to a 64-character hex string (32 bytes):

  ```bash
  export CREDENTIAL_ENCRYPTION_KEY=$(openssl rand -hex 32)
  ```

  Add this to your `docker-compose.yml` or `.env` file for local use.

- **Production (AWS Lambda)** — set `CREDENTIAL_ENCRYPTION_KEY_SECRET_ARN` to the ARN of an AWS Secrets Manager secret whose value is the 64-char hex key. Terraform creates this secret automatically when `create_credential_encryption_key = true` is set in `secrets.tf`. See [Deployment Guide](DEPLOYMENT.md#multi-account-credential-encryption) and `specs/multi-account-execution/iac.md` for details.

If neither variable is set, the application falls back to an insecure dev key and logs a warning. **Never use the dev key in production.**

### Application

- `ENVIRONMENT`: Environment name (default: `development`)
- `LOG_LEVEL`: Logging level (default: `debug`)

---

## Testing

### Testing Levels

**Unit tests** verify individual functions in isolation. Located in `*_test.go` files, run by default. Coverage target: >85%.

```bash
make test-unit
# or
go test -v -race -short ./...
```

**Integration tests** verify component interactions with real dependencies (databases via testcontainers). Located in `*_test.go` files with `//go:build integration` tag. Coverage target: >80%.

```bash
make test-integration
# or
go test -v -race -tags=integration ./...
```

**E2E tests** verify the complete application flow using docker-compose.

```bash
make docker-compose-test
# or
docker-compose -f docker-compose.test.yml up --abort-on-container-exit --exit-code-from test-runner
```

### Running Tests

```bash
# Quick test (unit only)
make test

# Full test suite (unit + integration + coverage)
make full-test

# Coverage report
make test-coverage
# Generates coverage.out and coverage.html
open coverage.html

# Specific package
go test -v ./internal/server/...

# Specific function
go test -v -run TestHandleScheduledTask ./internal/server/

# Integration tests (requires PostgreSQL)
docker-compose up -d postgres
DB_HOST=localhost DB_PASSWORD=cudly_local_dev go test -tags=integration ./internal/database/...
```

### Writing Tests

Use the AAA (Arrange-Act-Assert) pattern:

```go
func TestMyFunction(t *testing.T) {
    // Arrange
    ctx := testutil.TestContext(t)
    input := "test data"

    // Act
    result, err := MyFunction(ctx, input)

    // Assert
    testutil.AssertNoError(t, err)
    testutil.AssertEqual(t, "expected", result)
}
```

Table-driven tests for multiple scenarios:

```go
func TestMultipleScenarios(t *testing.T) {
    tests := []struct {
        name        string
        input       string
        expected    string
        expectError bool
    }{
        {name: "valid input", input: "test", expected: "TEST"},
        {name: "invalid input", input: "", expectError: true},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            result, err := Transform(tt.input)
            if tt.expectError {
                testutil.AssertError(t, err)
            } else {
                testutil.AssertNoError(t, err)
                testutil.AssertEqual(t, tt.expected, result)
            }
        })
    }
}
```

### Test Helpers (`internal/testutil`)

```go
// Context with timeout
ctx := testutil.TestContext(t)

// Environment variables
testutil.SetEnv(t, "DB_HOST", "localhost")

// Skip conditions
testutil.SkipIfShort(t)
testutil.SkipCI(t)

// Assertions
testutil.AssertNoError(t, err)
testutil.AssertEqual(t, expected, actual)
testutil.AssertTrue(t, condition, "message")
testutil.AssertContains(t, haystack, needle)

// Wait for condition
testutil.WaitFor(t, func() bool {
    return server.IsReady()
}, 5*time.Second, "server to be ready")
```

### Mocking Dependencies

```go
mockScheduler := &testutil.MockScheduler{
    CollectRecommendationsFunc: func(ctx context.Context) (*scheduler.CollectResult, error) {
        return &scheduler.CollectResult{Count: 10}, nil
    },
}

app := &Application{Scheduler: mockScheduler}
```

### Integration Test Setup

```go
//go:build integration

func TestWithPostgres(t *testing.T) {
    if testing.Short() {
        t.Skip("Skipping integration test")
    }

    ctx := testutil.TestContext(t)
    pg, err := testutil.SetupPostgresContainer(ctx, t)
    testutil.AssertNoError(t, err)

    for k, v := range pg.Config() {
        testutil.SetEnv(t, k, v)
    }

    // Test with real database...
}
```

### Coverage

**Targets:** Unit >85%, Integration >80%, Critical paths 100%

```bash
make test-coverage
go tool cover -func=coverage.out           # by package
go tool cover -html=coverage.out           # in browser
```

**Exceptions**: Generated code, trivial getters/setters, unreachable panic handlers.

### CI/CD

Tests run on PRs, pushes to main, and release tags via `.github/workflows/ci.yml`.

```bash
# Run full CI pipeline locally
make ci    # formatting, vet, complexity check, unit tests, security scanning, terraform validation

# Pre-commit hook
make pre-commit    # formatting, vet, complexity check, unit tests
```

### Security Testing

```bash
# All security scans
make security-scan

# Individual scans
make security-scan-go          # gosec
make security-scan-docker      # trivy (container + filesystem)
make security-scan-terraform   # tfsec
```

### Terraform & Docker Testing

```bash
make terraform-validate
make terraform-fmt-check
make terraform-fmt
make cost-estimate             # requires infracost

make docker-build              # build Docker image
make docker-test               # build and test image
make docker-compose-test       # E2E tests with docker-compose
```

---

## Frontend Development

The frontend is a TypeScript application in `frontend/src/`, built with webpack.

```bash
cd frontend

# Install dependencies
npm install

# Development build (with watch)
npm run dev

# Production build
npm run build

# Run tests
npx jest

# Run tests with coverage
npx jest --coverage
```

The frontend builds to `frontend/dist/` and is deployed to CDN (CloudFront/Azure CDN/Cloud CDN) as static files.

---

## Troubleshooting

### Database Connection Issues

```bash
docker-compose ps postgres
docker-compose logs postgres
docker-compose exec app pg_isready -h postgres -U cudly
```

### Migration Issues

```bash
# Check current migration version
docker-compose exec postgres psql -U cudly -d cudly -c "SELECT * FROM schema_migrations;"

# Force migration version (use with caution)
migrate -path internal/database/postgres/migrations \
    -database "postgresql://cudly:cudly_local_dev@localhost:5432/cudly?sslmode=disable" \
    force <version>
```

### Clean Restart

```bash
docker-compose down -v
docker-compose up --build -d
```

### Test Issues

- **"context deadline exceeded"**: Increase timeout in `testutil.TestContext()` or use a longer context
- **Docker not available**: Integration tests require Docker: <https://docs.docker.com/get-docker/>
- **testcontainers fails**: Ensure Docker daemon is running (`docker ps`)
- **Race condition detected**: Run with `go test -race ./...`
- **Coverage too low**: Find uncovered code with `go tool cover -func=coverage.out | grep -v "100.0%"`

---

## Testing with Real AWS Credentials

```bash
# Option 1: Mount AWS credentials
# In docker-compose.yml:
#   app:
#     environment:
#       SECRET_PROVIDER: aws
#     volumes:
#       - ~/.aws:/root/.aws:ro

# Option 2: Environment variables
export AWS_ACCESS_KEY_ID=xxx
export AWS_SECRET_ACCESS_KEY=xxx
export AWS_REGION=us-east-1

docker-compose restart app
```

## Production-Like Testing

```bash
# Build production image
docker build -t cudly:latest .

# Run with PostgreSQL
docker run --rm \
  --network cudly_cudly-network \
  -e DB_HOST=postgres \
  -e DB_PASSWORD=cudly_local_dev \
  -e RUNTIME_MODE=http \
  -p 8080:8080 \
  cudly:latest
```
