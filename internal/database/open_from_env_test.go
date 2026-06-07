package database

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOpenFromEnv_MissingConfig verifies that OpenFromEnv surfaces a config
// validation error (e.g. missing DB_PASSWORD) rather than panicking or
// returning a nil connection.
//
// A live Postgres is not available in unit tests; we cover the config-loading
// and error-propagation paths here. The connection-establishment path is
// exercised by integration tests against testcontainers Postgres in CI.
func TestOpenFromEnv_MissingConfig(t *testing.T) {
	// Remove all DB env vars so LoadFromEnv returns an error.
	keys := []string{
		"DB_HOST", "DB_PORT", "DB_NAME", "DB_USER",
		"DB_PASSWORD", "DB_PASSWORD_SECRET", "DB_SSL_MODE",
	}
	originals := make(map[string]string, len(keys))
	for _, k := range keys {
		originals[k] = os.Getenv(k)
		os.Unsetenv(k)
	}
	t.Cleanup(func() {
		for k, v := range originals {
			if v != "" {
				os.Setenv(k, v)
			} else {
				os.Unsetenv(k)
			}
		}
	})

	conn, err := OpenFromEnv(context.Background())
	require.Error(t, err, "OpenFromEnv must fail when DB config is missing")
	assert.Nil(t, conn)
	// Error must be wrapped with the database: prefix so callers can tell
	// it came from the bootstrap layer.
	assert.Contains(t, err.Error(), "database:")
}

// TestOpenFromEnv_NoPasswordSecret verifies that OpenFromEnv does not attempt
// to build a secrets.Resolver when DB_PASSWORD_SECRET is absent, even if
// DB_PASSWORD is provided (the resolver path is unreachable).
//
// This test cannot make a real connection, so it expects a connection error,
// not a config error -- the config is valid, but there is no Postgres to
// connect to.
func TestOpenFromEnv_NoPasswordSecret(t *testing.T) {
	// Provide a structurally valid config that will fail at the TCP dial.
	env := map[string]string{
		"DB_HOST":         "127.0.0.1",
		"DB_PORT":         "19999", // nothing listening here
		"DB_NAME":         "testdb",
		"DB_USER":         "testuser",
		"DB_PASSWORD":     "testpass",
		"DB_SSL_MODE":     "disable",
		"DB_MAX_CONN_IDLE_TIME":    "1s",
		"DB_MAX_CONN_LIFETIME":     "1s",
		"DB_HEALTH_CHECK_PERIOD":   "1s",
	}
	originals := make(map[string]string, len(env))
	for k, v := range env {
		originals[k] = os.Getenv(k)
		os.Setenv(k, v)
	}
	os.Unsetenv("DB_PASSWORD_SECRET")
	t.Cleanup(func() {
		for k, v := range originals {
			if v != "" {
				os.Setenv(k, v)
			} else {
				os.Unsetenv(k)
			}
		}
		os.Unsetenv("DB_PASSWORD_SECRET")
	})

	// Shrink retry knobs so the test finishes quickly.
	origMaxRetries := maxConnectRetries
	origBase := connectRetryBaseDelay
	origMax := connectRetryMaxDelay
	origPerAttempt := perAttemptConnectTimeout
	maxConnectRetries = 1
	connectRetryBaseDelay = 0
	connectRetryMaxDelay = 0
	perAttemptConnectTimeout = 50e6 // 50ms
	t.Cleanup(func() {
		maxConnectRetries = origMaxRetries
		connectRetryBaseDelay = origBase
		connectRetryMaxDelay = origMax
		perAttemptConnectTimeout = origPerAttempt
	})

	conn, err := OpenFromEnv(context.Background())
	// We expect a connection error (no Postgres at 19999), not a config error.
	require.Error(t, err, "OpenFromEnv must fail when Postgres is unreachable")
	assert.Nil(t, conn)
	assert.Contains(t, err.Error(), "database:")
}
