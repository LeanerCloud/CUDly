//go:build integration
// +build integration

package migrations_test

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// getMigrationsPath resolves the migrations directory relative to this test
// file so that migration test files can locate the SQL files regardless of
// the working directory when tests are invoked.
func getMigrationsPath() string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Dir(filename)
}

// migrationVersionsDesc returns every migration version present on disk, newest
// first, read from the `.up.sql` filenames.
func migrationVersionsDesc(t *testing.T) []uint {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(getMigrationsPath(), "*.up.sql"))
	require.NoError(t, err, "globbing migration files must succeed")
	require.NotEmpty(t, paths, "no migration files found")

	versions := make([]uint, 0, len(paths))
	for _, p := range paths {
		base := filepath.Base(p)
		idx := strings.IndexByte(base, '_')
		require.Positive(t, idx, "migration filename %q must be <version>_<name>.up.sql", base)
		n, err := strconv.ParseUint(base[:idx], 10, 64)
		require.NoError(t, err, "migration filename %q must start with a numeric version", base)
		versions = append(versions, uint(n))
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i] > versions[j] })
	return versions
}

// previousMigrationVersion returns the highest migration version strictly below
// version.
//
// Migration numbers in this repository are NOT contiguous -- renumbering to
// dodge collisions with in-flight PRs leaves gaps (60->63, 67->70, 81->83,
// 83->86, 93->95). So `version - 1` is not necessarily a real migration, and a
// test that rolls back one step lands on the next version that actually EXISTS.
// Deriving it from the files on disk keeps such tests correct no matter where
// the gaps fall -- including the case that motivated this helper, a gap
// immediately below head.
func previousMigrationVersion(t *testing.T, version uint) uint {
	t.Helper()
	for _, v := range migrationVersionsDesc(t) {
		if v < version {
			return v
		}
	}
	t.Fatalf("no migration version below %d", version)
	return 0
}

// captureStdout redirects os.Stdout to a pipe and returns a function that
// closes the pipe, restores stdout, and returns everything written to it.
//
// Mirrors the helper of the same name in migrate_security_test.go; the
// duplication is forced by the package boundary (that file lives in
// `package migrations`, while integration tests live in `package
// migrations_test`). Centralizing this copy here keeps every integration
// test that needs the helper pointed at one definition.
func captureStdout(t *testing.T) func() string {
	t.Helper()
	origStdout := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err, "os.Pipe must succeed")
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = origStdout })
	return func() string {
		_ = w.Close()
		os.Stdout = origStdout
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(r)
		_ = r.Close()
		return buf.String()
	}
}

// captureLogOutput redirects the standard logger to a buffer for the duration
// of the test, restoring the original flags and writer on cleanup.
//
// Mirrors the helper of the same name in migrate_security_test.go for the
// same package-boundary reason described on captureStdout.
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
