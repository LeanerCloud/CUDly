//go:build integration
// +build integration

package migrations_test

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"runtime"
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

// captureStdout redirects os.Stdout to a pipe and returns a function that
// closes the pipe, restores stdout, and returns everything written to it.
//
// Mirrors the helper of the same name in migrate_security_test.go; the
// duplication is forced by the package boundary (that file lives in
// `package migrations`, while integration tests live in `package
// migrations_test`). Centralising this copy here keeps every integration
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
