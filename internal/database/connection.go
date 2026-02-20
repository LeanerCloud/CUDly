package database

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/LeanerCloud/CUDly/pkg/logging"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/tracelog"
)

// Connection wraps a PostgreSQL connection pool
type Connection struct {
	pool   *pgxpool.Pool
	config *Config
}

// SecretResolver interface for retrieving secrets from cloud providers
type SecretResolver interface {
	GetSecret(ctx context.Context, secretID string) (string, error)
	Close() error
}

// NewConnection creates a new database connection pool
// If secretResolver is provided and config.PasswordSecret is set, password will be retrieved from secret manager
func NewConnection(ctx context.Context, config *Config, secretResolver SecretResolver) (*Connection, error) {
	// Resolve password from secret manager if needed
	password := config.Password
	if config.PasswordSecret != "" && secretResolver != nil {
		secret, err := secretResolver.GetSecret(ctx, config.PasswordSecret)
		if err != nil {
			return nil, fmt.Errorf("failed to retrieve database password from secret manager: %w", err)
		}

		// Try to parse as JSON (for RDS Proxy format: {"username": "...", "password": "..."})
		// If it's not JSON, use the raw string as the password
		var secretData map[string]interface{}
		if err := json.Unmarshal([]byte(secret), &secretData); err == nil {
			// Successfully parsed as JSON, extract password field
			if pwd, ok := secretData["password"].(string); ok {
				password = pwd
			} else {
				return nil, fmt.Errorf("secret is JSON but missing 'password' field")
			}
		} else {
			// Not JSON, use the raw secret as password (backward compatibility)
			password = secret
		}
	}

	// If no password was resolved, fail
	if password == "" {
		return nil, fmt.Errorf("database password not configured (set DB_PASSWORD or DB_PASSWORD_SECRET)")
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

// createConnectionPoolWithRetry creates a connection pool with exponential backoff retry
// This is necessary for Lambda functions in VPCs where the ENI may not be fully attached during init
func createConnectionPoolWithRetry(ctx context.Context, poolConfig *pgxpool.Config, config *Config) (*pgxpool.Pool, error) {
	maxRetries := 5
	baseDelay := 2 * time.Second
	maxDelay := 30 * time.Second

	var pool *pgxpool.Pool
	var lastErr error

	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			// Calculate exponential backoff delay: 2s, 4s, 8s, 16s, 30s (capped)
			delay := time.Duration(1<<uint(attempt)) * baseDelay
			if delay > maxDelay {
				delay = maxDelay
			}

			logging.Warnf("Connection attempt %d failed, retrying in %v...", attempt, delay)

			// Check if context is cancelled
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("connection cancelled: %w", ctx.Err())
			case <-time.After(delay):
				// Continue to retry
			}
		}

		// Attempt to create connection pool
		var err error
		pool, err = pgxpool.NewWithConfig(ctx, poolConfig)
		if err != nil {
			lastErr = fmt.Errorf("failed to create connection pool (attempt %d/%d): %w", attempt+1, maxRetries, err)
			continue
		}

		// Test connection with ping
		if err := pool.Ping(ctx); err != nil {
			lastErr = fmt.Errorf("failed to ping database (attempt %d/%d): %w", attempt+1, maxRetries, err)
			pool.Close()
			continue
		}

		// Success!
		if attempt > 0 {
			logging.Infof("Successfully connected to database after %d attempts", attempt+1)
		}
		return pool, nil
	}

	return nil, fmt.Errorf("failed to connect to database after %d attempts: %w", maxRetries, lastErr)
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

	// Check pool statistics
	stats := c.pool.Stat()
	if stats.AcquireCount() > 0 && stats.AcquiredConns() == 0 && stats.IdleConns() == 0 {
		return fmt.Errorf("connection pool has no available connections")
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
func (c *Connection) Query(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error) {
	return c.pool.Query(ctx, sql, args...)
}

// QueryRow executes a query that returns at most one row
func (c *Connection) QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row {
	return c.pool.QueryRow(ctx, sql, args...)
}

// Exec executes a command
func (c *Connection) Exec(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error) {
	return c.pool.Exec(ctx, sql, args...)
}

// Ping checks the database connection
func (c *Connection) Ping(ctx context.Context) error {
	return c.pool.Ping(ctx)
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

func (l *stdLogger) Log(ctx context.Context, level tracelog.LogLevel, msg string, data map[string]interface{}) {
	// Filter out sensitive data from logs
	safeData := make(map[string]interface{})
	for k, v := range data {
		// Skip potentially sensitive fields
		if k == "password" || k == "secret" || k == "token" || k == "sql" {
			continue
		}
		safeData[k] = v
	}

	switch level {
	case tracelog.LogLevelDebug:
		logging.Debugf("%s %v", msg, safeData)
	case tracelog.LogLevelInfo:
		logging.Infof("%s", msg)
	case tracelog.LogLevelWarn:
		logging.Warnf("%s %v", msg, safeData)
	case tracelog.LogLevelError:
		logging.Errorf("%s %v", msg, safeData)
	}
}
