// Package testutil provides common utilities for testing
package testutil

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestContext creates a context with a reasonable timeout for tests
func TestContext(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// SetEnv sets an environment variable for the duration of the test
func SetEnv(t *testing.T, key, value string) {
	old := os.Getenv(key)
	os.Setenv(key, value)
	t.Cleanup(func() {
		if old == "" {
			os.Unsetenv(key)
		} else {
			os.Setenv(key, old)
		}
	})
}

// RequireEnv skips the test if the environment variable is not set
func RequireEnv(t *testing.T, key string) string {
	value := os.Getenv(key)
	if value == "" {
		t.Skipf("Environment variable %s not set", key)
	}
	return value
}

// SkipIfShort skips the test if running in short mode
func SkipIfShort(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping test in short mode")
	}
}

// SkipCI skips the test if running in CI environment
func SkipCI(t *testing.T) {
	if os.Getenv("CI") == "true" {
		t.Skip("Skipping test in CI environment")
	}
}

// AssertNoError fails the test if err is not nil
func AssertNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
}

// AssertError fails the test if err is nil
func AssertError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("Expected an error, got nil")
	}
}

// AssertEqual fails the test if expected != actual
func AssertEqual(t *testing.T, expected, actual any) {
	t.Helper()
	if expected != actual {
		t.Fatalf("Expected %v, got %v", expected, actual)
	}
}

// AssertNotEqual fails the test if expected == actual
func AssertNotEqual(t *testing.T, expected, actual any) {
	t.Helper()
	if expected == actual {
		t.Fatalf("Expected values to be different, but both were %v", expected)
	}
}

// AssertTrue fails the test if condition is false
func AssertTrue(t *testing.T, condition bool, message string) {
	t.Helper()
	if !condition {
		t.Fatalf("Expected true: %s", message)
	}
}

// AssertFalse fails the test if condition is true
func AssertFalse(t *testing.T, condition bool, message string) {
	t.Helper()
	if condition {
		t.Fatalf("Expected false: %s", message)
	}
}

// AssertContains fails the test if substr is not in str
func AssertContains(t *testing.T, str, substr string) {
	t.Helper()
	if !contains(str, substr) {
		t.Fatalf("Expected string to contain %q, got %q", substr, str)
	}
}

// AssertNotContains fails the test if substr is in str
func AssertNotContains(t *testing.T, str, substr string) {
	t.Helper()
	if contains(str, substr) {
		t.Fatalf("Expected string not to contain %q, got %q", substr, str)
	}
}

func contains(str, substr string) bool {
	return len(str) >= len(substr) && (str == substr || len(substr) == 0 || indexSubstring(str, substr) >= 0)
}

func indexSubstring(str, substr string) int {
	for i := 0; i <= len(str)-len(substr); i++ {
		if str[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// WaitFor waits for a condition to be true, checking every interval
func WaitFor(t *testing.T, condition func() bool, timeout time.Duration, message string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	interval := 100 * time.Millisecond

	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(interval)
	}

	t.Fatalf("Timeout waiting for condition: %s", message)
}
