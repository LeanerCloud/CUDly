package database

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/LeanerCloud/CUDly/pkg/logging"
	"github.com/LeanerCloud/CUDly/pkg/retry"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/tracelog"
)

// Connection wraps a PostgreSQL connection pool
type Connection struct {
	pool   *pgxpool.Pool
	config *Config
	// lockedConns stores pinned pool connections for session-level advisory locks.
	// Session-level advisory locks are tied to the PostgreSQL session (backend),
	// not the transaction, so lock and unlock must use the same underlying connection.
	lockedConns sync.Map // map[int64]*pgxpool.Conn
}

// SecretResolver interface for retrieving secrets from cloud providers
type SecretResolver interface {
	GetSecret(ctx context.Context, secretID string) (string, error)
	Close() error
}

// NewConnection creates a new database connection pool
// If secretResolver is provided and config.PasswordSecret is set, password will be retrieved from secret manager
func NewConnection(ctx context.Context, config *Config, secretResolver SecretResolver) (*Connection, error) {
	// Check if secret resolver is needed but not provided
	if config.PasswordSecret != "" && secretResolver == nil {
		return nil, fmt.Errorf("DB_PASSWORD_SECRET is set but no secret resolver was provided")
	}

	password, err := resolvePassword(ctx, config, secretResolver)
	if err != nil {
		return nil, err
	}

	// Build connection pool configuration
	poolConfig, err := buildPoolConfig(config, password)
	if err != nil {
		return nil, fmt.Errorf("failed to build connection pool config: %w", err)
	}

	// Create connection pool with retry logic (for Lambda ENI attachment)
	pool, err := createConnectionPoolWithRetry(ctx, poolConfig, config)
	if err != nil {
		return nil, err
	}

	return &Connection{
		pool:   pool,
		config: config,
	}, nil
}

// resolvePassword returns the database password, fetching from secret manager if configured.
// Falls back to the static password in config if no secret is set.
func resolvePassword(ctx context.Context, config *Config, secretResolver SecretResolver) (string, error) {
	if config.PasswordSecret == "" || secretResolver == nil {
		if config.Password == "" {
			return "", fmt.Errorf("database password not configured (set DB_PASSWORD or DB_PASSWORD_SECRET)")
		}
		return config.Password, nil
	}

	secret, err := secretResolver.GetSecret(ctx, config.PasswordSecret)
	if err != nil {
		return "", fmt.Errorf("failed to retrieve database password from secret manager: %w", err)
	}

	return extractPasswordFromSecret(secret)
}

// extractPasswordFromSecret parses a secret value, which may be a raw password string
// or a JSON object in RDS Proxy format: {"username": "...", "password": "..."}.
func extractPasswordFromSecret(secret string) (string, error) {
	var secretData map[string]any
	if err := json.Unmarshal([]byte(secret), &secretData); err != nil {
		// Not JSON — use the raw string as the password (backward compatibility)
		return secret, nil
	}
	// Successfully parsed as JSON, extract password field
	if pwd, ok := secretData["password"].(string); ok {
		return pwd, nil
	}
	return "", fmt.Errorf("secret is JSON but missing 'password' field")
}

// Retry / per-attempt-deadline knobs for createConnectionPoolWithRetry.
// Declared as `var` (not `const`) so tests can shrink them via t.Cleanup
// to keep the test suite fast.
//
// perAttemptConnectTimeout bounds each individual pool-creation + Ping
// attempt. Without it, on Lambda cold-start the kernel TCP SYN
// retransmit hangs a single attempt for ~30-45s before pgx surfaces
// "failed to write startup message: timeout: context deadline
// exceeded"; multiplied by maxConnectRetries that produced the
// observed 3m55s total wait in production logs (2026-04-21T17:59:01Z
// and 18:02:04Z, cudly-dev-426fc8af-api). With the 15s per-attempt
// cap, worst-case retry budget is bounded to ~135s and the common
// cold-start case resolves on the 2nd or 3rd attempt within a minute
// once the ENI attaches.
var (
	maxConnectRetries        = 5
	connectRetryBaseDelay    = 2 * time.Second
	connectRetryMaxDelay     = 30 * time.Second
	perAttemptConnectTimeout = 15 * time.Second
)

// attemptOpenPool runs ONE pool-creation + Ping attempt under a bounded
// per-attempt context. The bounded context is independent of the outer
// ctx (cancellation still propagates because context.WithTimeout takes
// the earlier of the two deadlines), so a hung attempt fails after
// perAttemptConnectTimeout and the retry loop can move forward.
//
// `defer cancel()` is correct here because it sits inside a function
// body — putting the same defer inside the retry loop would leak a
// context per iteration. Same pattern as
// providers/azure/services/compute/client.go::fetchOnePricingPage
// (committed in f416b614c for identical defer-in-loop avoidance).
//
// Both NewWithConfig and Ping receive perAttemptCtx. pgxpool defers
// the actual TCP dial until the first acquire/Ping, so wrapping only
// NewWithConfig wouldn't bound the cold-start hang.
func attemptOpenPool(ctx context.Context, poolConfig *pgxpool.Config, perAttemptTimeout time.Duration) (*pgxpool.Pool, error) {
	perAttemptCtx, cancel := context.WithTimeout(ctx, perAttemptTimeout)
	defer cancel()

	pool, err := pgxpool.NewWithConfig(perAttemptCtx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection pool: %w", err)
	}

	if err := pool.Ping(perAttemptCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return pool, nil
}

// createConnectionPoolWithRetry creates a connection pool with exponential
// backoff retry. This is necessary for Lambda functions in VPCs where the
// ENI may not be fully attached during init — a single attempt can hang
// for ~30-45s on a TCP SYN retransmit, so each attempt is wrapped in a
// PerAttemptTimeout via the shared retry helper.
func createConnectionPoolWithRetry(ctx context.Context, poolConfig *pgxpool.Config, _ *Config) (*pgxpool.Pool, error) {
	var pool *pgxpool.Pool

	cfg := retry.Config{
		MaxAttempts:       maxConnectRetries,
		BaseDelay:         connectRetryBaseDelay,
		MaxDelay:          connectRetryMaxDelay,
		PerAttemptTimeout: perAttemptConnectTimeout,
		OnAttempt: func(attempt int, prevErr error) {
			if prevErr != nil {
				logging.Warnf("Connection attempt %d failed (%v), retrying...", attempt-1, prevErr)
			}
		},
	}

	err := retry.Do(ctx, cfg, func(perAttemptCtx context.Context, attempt int) error {
		// attemptOpenPool already wraps its work in its own per-attempt
		// context for the nested NewWithConfig + Ping pair. Pass
		// perAttemptCtx through so the outer retry budget cooperates.
		p, err := attemptOpenPool(perAttemptCtx, poolConfig, perAttemptConnectTimeout)
		if err != nil {
			return fmt.Errorf("attempt %d/%d (timeout %s): %w", attempt, maxConnectRetries, perAttemptConnectTimeout, err)
		}
		pool = p
		if attempt > 1 {
			logging.Infof("Successfully connected to database after %d attempts", attempt)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}
	return pool, nil
}

// buildPoolConfig creates a pgxpool.Config from our Config
func buildPoolConfig(config *Config, password string) (*pgxpool.Config, error) {
	// Build connection string
	dsn := config.DSN(password)

	// Parse DSN into pgxpool config
	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to parse DSN: %w", err)
	}

	// Set pool configuration
	if config.MaxConnections > math.MaxInt32 {
		return nil, fmt.Errorf("MaxConnections value %d exceeds int32 max", config.MaxConnections)
	}
	if config.MinConnections > math.MaxInt32 {
		return nil, fmt.Errorf("MinConnections value %d exceeds int32 max", config.MinConnections)
	}
	poolConfig.MaxConns = int32(config.MaxConnections)
	poolConfig.MinConns = int32(config.MinConnections)
	poolConfig.MaxConnLifetime = config.MaxConnLifetime
	poolConfig.MaxConnIdleTime = config.MaxConnIdleTime
	poolConfig.HealthCheckPeriod = config.HealthCheckPeriod

	// Configure logging
	logLevel := parseLogLevel(config.LogLevel)
	poolConfig.ConnConfig.Tracer = &tracelog.TraceLog{
		Logger:   &stdLogger{},
		LogLevel: logLevel,
	}

	// NOTE: statement_timeout is NOT supported by RDS Proxy
	// Application-level timeouts should be used instead via context.Context
	// poolConfig.ConnConfig.RuntimeParams["statement_timeout"] = "30000" // 30 seconds

	// Set timezone to UTC
	poolConfig.ConnConfig.RuntimeParams["timezone"] = "UTC"

	return poolConfig, nil
}

// Pool returns the underlying connection pool
func (c *Connection) Pool() *pgxpool.Pool {
	return c.pool
}

// Close closes the connection pool
func (c *Connection) Close() {
	c.pool.Close()
}

// HealthCheck verifies the database connection is healthy
func (c *Connection) HealthCheck(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Ping the database
	if err := c.pool.Ping(ctx); err != nil {
		return fmt.Errorf("database ping failed: %w", err)
	}

	// Check pool has any connections available
	stats := c.pool.Stat()
	if stats.TotalConns() == 0 {
		return fmt.Errorf("connection pool has no connections")
	}

	return nil
}

// Stats returns connection pool statistics
func (c *Connection) Stats() *pgxpool.Stat {
	return c.pool.Stat()
}

// Acquire gets a connection from the pool
func (c *Connection) Acquire(ctx context.Context) (*pgxpool.Conn, error) {
	return c.pool.Acquire(ctx)
}

// Begin starts a new transaction
func (c *Connection) Begin(ctx context.Context) (pgx.Tx, error) {
	return c.pool.Begin(ctx)
}

// BeginTx starts a new transaction with options
func (c *Connection) BeginTx(ctx context.Context, txOptions pgx.TxOptions) (pgx.Tx, error) {
	return c.pool.BeginTx(ctx, txOptions)
}

// Query executes a query
func (c *Connection) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return c.pool.Query(ctx, sql, args...)
}

// QueryRow executes a query that returns at most one row
func (c *Connection) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return c.pool.QueryRow(ctx, sql, args...)
}

// Exec executes a command
func (c *Connection) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return c.pool.Exec(ctx, sql, args...)
}

// Ping checks the database connection
func (c *Connection) Ping(ctx context.Context) error {
	return c.pool.Ping(ctx)
}

// TryAdvisoryLock attempts to acquire a PostgreSQL session-level advisory lock.
// Returns true if the lock was acquired, false if another session holds it.
//
// Session-level advisory locks in PostgreSQL are tied to the backend session, not
// the transaction. With a connection pool, lock and unlock must use the same underlying
// connection. This method pins a connection for the duration of the lock and stores it
// internally; callers MUST call ReleaseAdvisoryLock to release both the lock and the
// pinned connection.
func (c *Connection) TryAdvisoryLock(ctx context.Context, lockID int64) (bool, error) {
	conn, err := c.pool.Acquire(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to acquire pinned connection for advisory lock: %w", err)
	}

	var acquired bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", lockID).Scan(&acquired); err != nil {
		conn.Release()
		return false, err
	}

	if !acquired {
		conn.Release()
		return false, nil
	}

	// Store the pinned connection so ReleaseAdvisoryLock uses the same session.
	c.lockedConns.Store(lockID, conn)
	return true, nil
}

// ReleaseAdvisoryLock releases a previously acquired advisory lock.
// It reuses the same pinned connection used by TryAdvisoryLock to ensure
// the unlock targets the correct PostgreSQL session.
func (c *Connection) ReleaseAdvisoryLock(ctx context.Context, lockID int64) {
	val, ok := c.lockedConns.LoadAndDelete(lockID)
	if !ok {
		logging.Warnf("ReleaseAdvisoryLock called for lock %d but no pinned connection found", lockID)
		return
	}
	conn := val.(*pgxpool.Conn)
	defer conn.Release()

	var released bool
	if err := conn.QueryRow(ctx, "SELECT pg_advisory_unlock($1)", lockID).Scan(&released); err != nil {
		logging.Warnf("Failed to release advisory lock %d: %v", lockID, err)
	} else if !released {
		logging.Warnf("Advisory lock %d was not held by this session", lockID)
	}
}

// parseLogLevel converts string log level to pgx tracelog level
func parseLogLevel(level string) tracelog.LogLevel {
	switch level {
	case "debug":
		return tracelog.LogLevelDebug
	case "info":
		return tracelog.LogLevelInfo
	case "warn":
		return tracelog.LogLevelWarn
	case "error":
		return tracelog.LogLevelError
	default:
		return tracelog.LogLevelInfo
	}
}

// stdLogger implements pgx tracelog.Logger using the logging package
type stdLogger struct{}

func (l *stdLogger) Log(ctx context.Context, level tracelog.LogLevel, msg string, data map[string]any) {
	// Filter out sensitive data from logs
	safeData := make(map[string]any)
	for k, v := range data {
		// Skip potentially sensitive fields
		if k == "password" || k == "secret" || k == "token" {
			continue
		}
		safeData[k] = v
	}

	switch level {
	case tracelog.LogLevelDebug:
		logging.Debugf("%s %v", msg, safeData)
	case tracelog.LogLevelInfo:
		// Note: Structured data is intentionally omitted at info level to reduce log verbosity.
		// Only message is logged; use warn/error levels for structured data.
		logging.Infof("%s", msg)
	case tracelog.LogLevelWarn:
		logging.Warnf("%s %v", msg, safeData)
	case tracelog.LogLevelError:
		logging.Errorf("%s %v", msg, safeData)
	}
}
