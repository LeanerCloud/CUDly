package migrations

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// libPqModulePath is the module whose absence the guards below assert. lib/pq
// carries five advisories with no fixed version in any release (GO-2026-6166,
// 6168, 6170, 6171 and 6172; issue #1849 was filed when only the last three
// existed), reached through Driver.Open and conn.Exec, so linking it fails the
// repo-wide govulncheck gate with no bump available to clear it.
const libPqModulePath = "github.com/lib/pq"

// migrateCLIPackage and migrateCLIBuildTag mirror how the Dockerfile, the
// Makefile and the ci / database-migration workflows build the migrate binary
// that runs `migrate up` against the database on every container start.
const (
	migrateCLIPackage  = "github.com/golang-migrate/migrate/v4/cmd/migrate"
	migrateCLIBuildTag = "pgx5"
)

// TestLibPqDriverNotRegistered asserts lib/pq is not reachable from anything
// this repo builds, along two independent axes.
//
// Registration is the weaker axis on its own: a change could link lib/pq
// without registering a golang-migrate driver, and the registration assertions
// would stay green. So the build closure is asserted directly, which is the
// same property `go list -deps` measures and the one govulncheck's source mode
// analyses. Both targets are covered, because the module and the CLI are
// separate builds and covering one leaves the other unguarded.
//
// Known limit, stated so nobody reads more into it: the CLI subtest pins the
// build as configured, with migrateCLIBuildTag. It asserts that build stays
// lib/pq-free; it cannot notice someone changing the tag back to `postgres` in
// the Dockerfile, which is a build-configuration change no Go test observes.
func TestLibPqDriverNotRegistered(t *testing.T) {
	t.Run("driver not registered", func(t *testing.T) {
		for _, scheme := range []string{"postgres", "postgresql"} {
			assert.NotContains(t, database.List(), scheme,
				"golang-migrate driver %q is registered, which means the lib/pq-backed "+
					"database/postgres driver was imported somewhere in this package", scheme)
		}
	})

	t.Run("absent from the root module build closure", func(t *testing.T) {
		deps := goListDeps(t, "./...")
		assert.Empty(t, packagesFromModule(deps, libPqModulePath),
			"%s packages are reachable from `go list -deps ./...`; the module is linked "+
				"into this repo's build even if no golang-migrate driver registered it",
			libPqModulePath)
	})

	t.Run("absent from the migrate CLI build closure", func(t *testing.T) {
		deps := goListDeps(t, "-tags="+migrateCLIBuildTag, migrateCLIPackage)
		assert.Empty(t, packagesFromModule(deps, libPqModulePath),
			"%s packages are reachable from the migrate CLI built with -tags=%s; that is "+
				"the binary scripts/entrypoint.sh runs against the database on every "+
				"container start", libPqModulePath, migrateCLIBuildTag)
	})
}

// goListDeps returns the build closure `go list -deps <args...>` reports,
// resolved from the main module's root so `./...` means the whole module rather
// than whichever package directory the test happens to run in.
//
// A failed command and an empty result are both hard failures rather than an
// empty closure: "the package list does not contain lib/pq" is trivially true
// of a list that is empty because the toolchain was missing, the module root
// could not be resolved, or `go list` errored. That would turn this guard into
// one that passes for the wrong reason, which is worse than not having it.
func goListDeps(t *testing.T, args ...string) []string {
	t.Helper()

	root := mainModuleRoot(t)

	cmd := exec.CommandContext(t.Context(), "go", append([]string{"list", "-deps"}, args...)...)
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	require.NoErrorf(t, err, "go list -deps %v in %s failed: %s", args, root, stderr.String())

	pkgs := strings.Fields(string(out))
	require.NotEmptyf(t, pkgs, "go list -deps %v in %s returned no packages", args, root)

	return pkgs
}

// mainModuleRoot resolves the directory holding the main module's go.mod.
// `go env GOMOD` reports the module the test's own directory belongs to, which
// is the root module even under the go.work workspace.
func mainModuleRoot(t *testing.T) string {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "go", "env", "GOMOD")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	require.NoErrorf(t, err, "go env GOMOD failed: %s", stderr.String())

	// `go env GOMOD` reports os.DevNull, not an empty string, when the toolchain
	// is in GOPATH mode. Both mean there is no module root to resolve, and
	// neither may be allowed to reach filepath.Dir and produce a plausible-looking
	// directory that `go list` would then run in.
	goMod := strings.TrimSpace(string(out))
	require.NotEmpty(t, goMod, "go env GOMOD returned no path; the test is not running inside a module")
	require.NotEqual(t, os.DevNull, goMod, "go env GOMOD reported %s; the toolchain is not in module mode", os.DevNull)

	return filepath.Dir(goMod)
}

// packagesFromModule returns the entries of pkgs that belong to module, matching
// the module path itself and any package beneath it (lib/pq's vulnerable symbols
// span both `github.com/lib/pq` and `github.com/lib/pq/scram`). Prefix matching
// is anchored on a trailing slash so a same-prefixed but unrelated module such
// as `github.com/lib/pqfoo` is not counted.
func packagesFromModule(pkgs []string, module string) []string {
	var found []string
	for _, pkg := range pkgs {
		if pkg == module || strings.HasPrefix(pkg, module+"/") {
			found = append(found, pkg)
		}
	}
	return found
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
