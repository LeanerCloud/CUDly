//go:build integration
// +build integration

package migrations_test

import (
	"path/filepath"
	"runtime"
)

// getMigrationsPath resolves the migrations directory relative to this test
// file so that migration test files can locate the SQL files regardless of
// the working directory when tests are invoked.
func getMigrationsPath() string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Dir(filename)
}
