package database

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests for RedactedDSN
func TestRedactedDSN(t *testing.T) {
	cfg := &Config{
		Host:           "db.example.com",
		Port:           5432,
		User:           "admin",
		Password:       "supersecret",
		Database:       "cudly",
		SSLMode:        "require",
		ConnectTimeout: 10 * time.Second,
	}

	dsn := cfg.RedactedDSN()
	assert.Contains(t, dsn, "db.example.com")
	assert.Contains(t, dsn, "admin")
	assert.Contains(t, dsn, "*****")
	assert.Contains(t, dsn, "cudly")
	assert.Contains(t, dsn, "require")
	assert.NotContains(t, dsn, "supersecret")
}

// Tests for extractPasswordFromSecret
func TestExtractPasswordFromSecret_JSONWithPassword(t *testing.T) {
	secret := `{"username":"admin","password":"db-pass-123","host":"db.example.com"}`
	pwd, err := extractPasswordFromSecret(secret)
	require.NoError(t, err)
	assert.Equal(t, "db-pass-123", pwd)
}

func TestExtractPasswordFromSecret_PlainString(t *testing.T) {
	secret := "plain-text-password"
	pwd, err := extractPasswordFromSecret(secret)
	require.NoError(t, err)
	assert.Equal(t, "plain-text-password", pwd)
}

func TestExtractPasswordFromSecret_JSONMissingPassword(t *testing.T) {
	secret := `{"username":"admin","host":"db.example.com"}`
	_, err := extractPasswordFromSecret(secret)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing 'password' field")
}

func TestExtractPasswordFromSecret_EmptyString(t *testing.T) {
	// Empty string is not valid JSON → treated as plain password
	pwd, err := extractPasswordFromSecret("")
	require.NoError(t, err)
	assert.Equal(t, "", pwd)
}

func TestExtractPasswordFromSecret_JSONNull(t *testing.T) {
	// JSON where password field is null (not a string)
	secret := `{"password": null}`
	_, err := extractPasswordFromSecret(secret)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing 'password' field")
}

// Tests for buildPoolConfig overflow protection
func TestBuildPoolConfig_MaxConnectionsOverflow(t *testing.T) {
	cfg := &Config{
		Host:           "localhost",
		Port:           5432,
		User:           "user",
		Password:       "pass",
		Database:       "db",
		SSLMode:        "disable",
		MaxConnections: 1<<32 + 1, // exceeds int32 max
		MinConnections: 1,
		ConnectTimeout: 5 * time.Second,
		LogLevel:       "info",
	}

	_, err := buildPoolConfig(cfg, "pass")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "MaxConnections")
}

func TestBuildPoolConfig_MinConnectionsOverflow(t *testing.T) {
	cfg := &Config{
		Host:           "localhost",
		Port:           5432,
		User:           "user",
		Password:       "pass",
		Database:       "db",
		SSLMode:        "disable",
		MaxConnections: 10,
		MinConnections: 1<<32 + 1, // exceeds int32 max
		ConnectTimeout: 5 * time.Second,
		LogLevel:       "info",
	}

	_, err := buildPoolConfig(cfg, "pass")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "MinConnections")
}

// Tests for resolvePassword branches
func TestResolvePassword_NeitherPasswordNorSecret(t *testing.T) {
	cfg := &Config{
		Password:       "",
		PasswordSecret: "",
	}
	_, err := resolvePassword(context.Background(), cfg, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "database password not configured")
}

func TestResolvePassword_DirectPassword(t *testing.T) {
	cfg := &Config{
		Password:       "direct-pass",
		PasswordSecret: "",
	}
	pwd, err := resolvePassword(context.Background(), cfg, nil)
	require.NoError(t, err)
	assert.Equal(t, "direct-pass", pwd)
}

func TestResolvePassword_NilResolverWithSecret(t *testing.T) {
	// PasswordSecret set but secretResolver is nil → falls back to direct password
	cfg := &Config{
		Password:       "fallback-pass",
		PasswordSecret: "some-secret",
	}
	pwd, err := resolvePassword(context.Background(), cfg, nil)
	require.NoError(t, err)
	assert.Equal(t, "fallback-pass", pwd)
}

func TestResolvePassword_SecretResolverSucceeds(t *testing.T) {
	cfg := &Config{
		Password:       "",
		PasswordSecret: "my-secret",
	}
	resolver := &MockSecretResolver{SecretValue: "resolved-password"}
	pwd, err := resolvePassword(context.Background(), cfg, resolver)
	require.NoError(t, err)
	assert.Equal(t, "resolved-password", pwd)
}

func TestResolvePassword_SecretResolverFails(t *testing.T) {
	cfg := &Config{
		Password:       "",
		PasswordSecret: "bad-secret",
	}
	resolver := &MockSecretResolver{SecretError: assert.AnError}
	_, err := resolvePassword(context.Background(), cfg, resolver)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to retrieve database password from secret manager")
}

// Tests for NewConnection when PasswordSecret set without resolver
func TestNewConnection_SecretRequiredButNoResolver(t *testing.T) {
	cfg := &Config{
		Host:           "localhost",
		Port:           5432,
		User:           "user",
		PasswordSecret: "my-secret",
		Database:       "db",
		SSLMode:        "disable",
		MaxConnections: 5,
		MinConnections: 1,
		ConnectTimeout: time.Second,
		LogLevel:       "info",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := NewConnection(ctx, cfg, nil)
	// PasswordSecret set without resolver → "DB_PASSWORD_SECRET is set but no secret resolver was provided"
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DB_PASSWORD_SECRET is set but no secret resolver was provided")
}

// Tests for Pool() — returns the internal pool (nil when not connected)
func TestConnection_Pool_ReturnsPool(t *testing.T) {
	conn := &Connection{
		pool:   nil,
		config: &Config{},
	}
	assert.Nil(t, conn.Pool())
}

// Tests for DSN()
func TestConfig_DSN(t *testing.T) {
	cfg := &Config{
		Host:           "pg.example.com",
		Port:           5432,
		User:           "myuser",
		Database:       "mydb",
		SSLMode:        "disable",
		ConnectTimeout: 30 * time.Second,
	}

	dsn := cfg.DSN("mypassword")
	assert.Contains(t, dsn, "pg.example.com")
	assert.Contains(t, dsn, "5432")
	assert.Contains(t, dsn, "myuser")
	assert.Contains(t, dsn, "mypassword")
	assert.Contains(t, dsn, "mydb")
	assert.Contains(t, dsn, "disable")
}

// newLazyPool creates a pgxpool.Pool pointing at a non-existent host with
// MinConns=0 so no eager TCP connections are made. Returns nil if pool creation
// fails for any reason (caller should skip).
func newLazyPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	cfg := &Config{
		Host:              "127.0.0.1",
		Port:              9, // nothing listening — connections refused immediately
		User:              "testuser",
		Password:          "testpass",
		Database:          "testdb",
		SSLMode:           "disable",
		MaxConnections:    2,
		MinConnections:    0, // no eager connects
		MaxConnLifetime:   time.Hour,
		MaxConnIdleTime:   30 * time.Minute,
		HealthCheckPeriod: time.Minute, // must be > 0 to avoid panic
		ConnectTimeout:    time.Second,
		LogLevel:          "error",
	}
	poolConfig, err := buildPoolConfig(cfg, "testpass")
	if err != nil {
		t.Skipf("cannot build pool config: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), poolConfig)
	if err != nil {
		t.Skipf("cannot create lazy pool: %v", err)
	}
	return pool
}

// TestConnectionMethodsWithLazyPool exercises Connection wrapper methods using
// a lazily-created pool that never establishes a real TCP connection.
func TestConnectionMethodsWithLazyPool(t *testing.T) {
	pool := newLazyPool(t)
	cfg := &Config{}
	conn := &Connection{pool: pool, config: cfg}

	// Test Pool() returns the pool
	assert.Equal(t, pool, conn.Pool())

	// Test Stats() returns a non-nil stat
	stats := conn.Stats()
	assert.NotNil(t, stats)
	assert.Equal(t, int32(0), stats.TotalConns()) // No connections yet

	// Test Close() does not panic
	assert.NotPanics(t, func() {
		conn.Close()
	})
}

// TestConnectionPing_Fails exercises the Ping() wrapper on a pool with no reachable DB.
func TestConnectionPing_Fails(t *testing.T) {
	pool := newLazyPool(t)
	conn := &Connection{pool: pool, config: &Config{}}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Ping will fail (no real DB at port 9) — but the wrapper should call pool.Ping
	err := conn.Ping(ctx)
	assert.Error(t, err)
}

// TestConnectionHealthCheck_Fails exercises HealthCheck on a pool with no reachable DB.
func TestConnectionHealthCheck_Fails(t *testing.T) {
	pool := newLazyPool(t)
	conn := &Connection{pool: pool, config: &Config{}}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := conn.HealthCheck(ctx)
	assert.Error(t, err)
}

// TestConnectionAcquire_Fails exercises Acquire on a pool with no reachable DB.
func TestConnectionAcquire_Fails(t *testing.T) {
	pool := newLazyPool(t)
	conn := &Connection{pool: pool, config: &Config{}}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := conn.Acquire(ctx)
	assert.Error(t, err)
}

// TestConnectionBegin_Fails exercises Begin on a pool with no reachable DB.
func TestConnectionBegin_Fails(t *testing.T) {
	pool := newLazyPool(t)
	conn := &Connection{pool: pool, config: &Config{}}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := conn.Begin(ctx)
	assert.Error(t, err)
}

// TestConnectionQuery_Fails exercises Query on a pool with no reachable DB.
func TestConnectionQuery_Fails(t *testing.T) {
	pool := newLazyPool(t)
	conn := &Connection{pool: pool, config: &Config{}}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := conn.Query(ctx, "SELECT 1")
	assert.Error(t, err)
}

// TestConnectionQueryRow exercises QueryRow (returns pgx.Row, no error return).
func TestConnectionQueryRow_DoesNotPanic(t *testing.T) {
	pool := newLazyPool(t)
	conn := &Connection{pool: pool, config: &Config{}}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// QueryRow returns a pgx.Row — scan will fail but QueryRow itself should not panic
	assert.NotPanics(t, func() {
		row := conn.QueryRow(ctx, "SELECT 1")
		var v int
		_ = row.Scan(&v) // error expected, but no panic
	})
}

// TestConnectionExec_Fails exercises Exec on a pool with no reachable DB.
func TestConnectionExec_Fails(t *testing.T) {
	pool := newLazyPool(t)
	conn := &Connection{pool: pool, config: &Config{}}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := conn.Exec(ctx, "SELECT 1")
	assert.Error(t, err)
}

// TestConnectionBeginTx_Fails exercises BeginTx on a pool with no reachable DB.
func TestConnectionBeginTx_Fails(t *testing.T) {
	pool := newLazyPool(t)
	conn := &Connection{pool: pool, config: &Config{}}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := conn.BeginTx(ctx, pgx.TxOptions{})
	assert.Error(t, err)
}

// TestReleaseAdvisoryLock_NoEntry exercises ReleaseAdvisoryLock when no lock was acquired.
// This tests the "no pinned connection" warning branch.
func TestReleaseAdvisoryLock_NoEntry(t *testing.T) {
	pool := newLazyPool(t)
	conn := &Connection{pool: pool, config: &Config{}}
	defer conn.Close()

	ctx := context.Background()
	// Calling Release on a lock that was never acquired should log a warning and return
	assert.NotPanics(t, func() {
		conn.ReleaseAdvisoryLock(ctx, 999999)
	})
}
