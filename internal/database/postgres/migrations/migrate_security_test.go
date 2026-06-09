package migrations

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newUnreachablePool returns a *pgxpool.Pool pointed at a port that will
// immediately refuse connections. pgxpool v5 is lazy -- it does not dial
// until the first Acquire/Exec call -- so construction succeeds and the
// pool can be passed to functions whose logging fires before any DB access.
func newUnreachablePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig("postgres://user:pass@127.0.0.1:1/db?sslmode=disable")
	require.NoError(t, err, "pgxpool.ParseConfig must succeed for unreachable DSN")
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	require.NoError(t, err, "pgxpool.NewWithConfig must succeed (lazy -- no dial yet)")
	t.Cleanup(pool.Close)
	return pool
}

// captureLogOutput redirects the standard logger output to a buffer for the
// duration of the test, restoring the original flags and writer on cleanup.
func captureLogOutput(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	origFlags := log.Flags()
	origOutput := log.Writer()
	log.SetFlags(0)
	log.SetOutput(&buf)
	t.Cleanup(func() {
		log.SetFlags(origFlags)
		log.SetOutput(origOutput)
	})
	return &buf
}

// captureStdout redirects os.Stdout to a pipe and returns a function that
// closes the pipe, restores stdout, and returns everything that was written.
func captureStdout(t *testing.T) func() string {
	t.Helper()
	origStdout := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err, "os.Pipe must succeed")
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = origStdout })
	return func() string {
		w.Close()
		os.Stdout = origStdout
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(r)
		r.Close()
		return buf.String()
	}
}

// TestEnsureAdminUserWithPassword_LogsToStderr_NotStdout is a regression test
// for issue #440: admin password activity must not be echoed to stdout.
// Previously the function used fmt.Printf which writes to stdout; it now uses
// log.Printf which writes to stderr.
func TestEnsureAdminUserWithPassword_LogsToStderr_NotStdout(t *testing.T) {
	readStdout := captureStdout(t)
	logBuf := captureLogOutput(t)

	// Call the real function. It logs before touching the pool so the log
	// assertions below are valid regardless of the subsequent Exec error.
	pool := newUnreachablePool(t)
	// The function returns an error (connection refused) but that is expected.
	_ = ensureAdminUserWithPassword(context.Background(), pool, "admin@example.com", "supersecretpassword")

	stdoutContent := readStdout()

	// Assertion: nothing about admin password activity on stdout.
	assert.Empty(t, stdoutContent,
		"log.Printf must not write to stdout; found on stdout: %q", stdoutContent)

	// Assertion: the log messages landed in the stderr-bound log buffer.
	logContent := logBuf.String()
	assert.Contains(t, logContent, "admin@example.com",
		"admin email must appear in log output (stderr path)")
	// The message describes the operation but must NOT include the actual password.
	assert.NotContains(t, logContent, "supersecretpassword",
		"log messages must never include the actual password value")
}

// TestEnsureAdminUser_NoPasswordVariant_LogsToStderr is a companion regression
// test for the no-password variant of ensureAdminUser (issue #440).
func TestEnsureAdminUser_NoPasswordVariant_LogsToStderr(t *testing.T) {
	readStdout := captureStdout(t)
	logBuf := captureLogOutput(t)

	// Call the real function with empty password (no-password path).
	pool := newUnreachablePool(t)
	_ = ensureAdminUser(context.Background(), pool, "admin@example.com", "")

	stdoutContent := readStdout()

	assert.Empty(t, stdoutContent,
		"log.Printf must not write to stdout")
	assert.Contains(t, logBuf.String(), "admin@example.com")
}

// TestBuildMigrateDSN_PasswordNotInLogs verifies that buildMigrateDSN embeds
// the password only in the returned string and not in any log call, serving as
// a structural guard against accidental log emission of the DSN.
func TestBuildMigrateDSN_PasswordNotInLogs(t *testing.T) {
	const sentinelPassword = "SUPER_SECRET_SENTINEL_XYZ"

	logBuf := captureLogOutput(t)

	// Build a real *pgxpool.Config whose ConnConfig.Password holds the sentinel.
	rawDSN := fmt.Sprintf("postgres://user:%s@localhost:5432/db?sslmode=disable", sentinelPassword)
	poolCfg, err := pgxpool.ParseConfig(rawDSN)
	require.NoError(t, err, "pgxpool.ParseConfig must accept the sentinel DSN")

	// Call the function under test.
	result := buildMigrateDSN(poolCfg, "")

	// The sentinel must appear in the returned DSN (proves the function embeds it).
	assert.Contains(t, result, sentinelPassword,
		"buildMigrateDSN return value must contain the password")

	// The sentinel must NOT have leaked into the log buffer.
	assert.NotContains(t, logBuf.String(), sentinelPassword,
		"buildMigrateDSN must not emit the database password to the log output")
}

// TestMaybeForceVersion_NonNumericError ensures a non-numeric
// CUDLY_FORCE_MIGRATION_VERSION produces an error without logging the
// bad value to stdout.
func TestMaybeForceVersion_NonNumericError(t *testing.T) {
	readStdout := captureStdout(t)

	t.Setenv("CUDLY_FORCE_MIGRATION_VERSION", "not-a-number")

	// maybeForceMigrationVersion returns an error from strconv.Atoi before it
	// ever calls m.Force(), so nil is safe to pass for the non-numeric path.
	err := maybeForceMigrationVersion(nil)

	stdoutContent := readStdout()

	require.Error(t, err,
		"non-numeric CUDLY_FORCE_MIGRATION_VERSION must return an error")
	assert.Contains(t, err.Error(), "not-a-number",
		"error message must echo back the bad value for operator clarity")
	assert.Empty(t, stdoutContent,
		"error handling must not write to stdout")
}
