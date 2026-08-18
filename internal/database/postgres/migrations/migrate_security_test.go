package migrations

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"testing"

	"github.com/golang-migrate/migrate/v4/database"
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
	result := buildMigrateDSN(poolCfg)

	// The sentinel must appear in the returned DSN (proves the function embeds it).
	assert.Contains(t, result, sentinelPassword,
		"buildMigrateDSN return value must contain the password")

	// The sentinel must NOT have leaked into the log buffer.
	assert.NotContains(t, logBuf.String(), sentinelPassword,
		"buildMigrateDSN must not emit the database password to the log output")
}

// TestBuildMigrateDSN_PreservesSSLMode guards against silently downgrading the
// migration DSN's sslmode: a strict mode (verify-ca / verify-full) must survive
// the DSN -> pgx *tls.Config -> migration DSN round-trip rather than collapsing
// to require. This also pins the pgx-version-specific TLSConfig mapping in
// sslModeFromTLSConfig.
func TestBuildMigrateDSN_PreservesSSLMode(t *testing.T) {
	for _, mode := range []string{"disable", "require", "verify-ca", "verify-full"} {
		t.Run(mode, func(t *testing.T) {
			rawDSN := fmt.Sprintf("postgres://user:pass@localhost:5432/db?sslmode=%s", mode)
			poolCfg, err := pgxpool.ParseConfig(rawDSN)
			require.NoError(t, err, "pgxpool.ParseConfig must accept sslmode=%s", mode)

			result := buildMigrateDSN(poolCfg)

			assert.Contains(t, result, "sslmode="+mode,
				"buildMigrateDSN must preserve the configured sslmode, not downgrade it")
		})
	}
}

// TestBuildMigrateDSN_SchemeMatchesRegisteredDriver pins the migration DSN's
// URL scheme to a driver this package actually imports. golang-migrate resolves
// the driver by scheme at Open() time, so a scheme naming no registered driver
// fails at migration time -- on every container start, since entrypoint.sh runs
// migrations with DB_AUTO_MIGRATE=true -- and not at compile time. Swapping the
// driver import without swapping the scheme (or the reverse) is exactly the
// mistake this guards: the pgx/v5 driver registers "pgx5" only, where the
// lib/pq-backed driver registered "postgres"/"postgresql".
func TestBuildMigrateDSN_SchemeMatchesRegisteredDriver(t *testing.T) {
	poolCfg, err := pgxpool.ParseConfig("postgres://user:pass@localhost:5432/db?sslmode=require")
	require.NoError(t, err, "pgxpool.ParseConfig must accept the fixture DSN")

	parsed, err := url.Parse(buildMigrateDSN(poolCfg))
	require.NoError(t, err, "buildMigrateDSN must return a parseable URL")

	assert.Equal(t, migrateURLScheme, parsed.Scheme,
		"buildMigrateDSN must emit the scheme named by migrateURLScheme")
	assert.Contains(t, database.List(), migrateURLScheme,
		"migrateURLScheme must name a golang-migrate driver this package imports; "+
			"registered drivers: %v", database.List())
}

// TestLibPqDriverNotRegistered asserts the lib/pq-backed golang-migrate driver
// is not linked into this package. lib/pq carries three advisories with no
// fixed version in any release (GO-2026-6170/6171/6172, CVE-2026-56871/2/3),
// reached through Driver.Open and conn.Exec, so importing it fails the
// repo-wide govulncheck gate with no bump available to clear it (issue #1849).
// Re-adding the import is a one-line change that would otherwise only surface
// as a red CI run on an unrelated pull request.
func TestLibPqDriverNotRegistered(t *testing.T) {
	for _, scheme := range []string{"postgres", "postgresql"} {
		assert.NotContains(t, database.List(), scheme,
			"golang-migrate driver %q is registered, which means the lib/pq-backed "+
				"database/postgres driver was imported somewhere in this package", scheme)
	}
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
