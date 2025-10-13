package common

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLoggerSetEnabled(t *testing.T) {
	// Save original state
	originalEnabled := AppLogger.enabled

	// Test enabling
	AppLogger.SetEnabled(true)
	assert.True(t, AppLogger.enabled)

	// Test disabling
	AppLogger.SetEnabled(false)
	assert.False(t, AppLogger.enabled)

	// Restore original state
	AppLogger.SetEnabled(originalEnabled)
}

func TestLoggerPrintf(t *testing.T) {
	var buf bytes.Buffer
	originalEnabled := AppLogger.enabled

	// Create a test logger with buffer
	testLogger := NewLogger(&buf, &buf, true)

	// Test Printf when enabled
	testLogger.Printf("Test message: %s", "hello")
	assert.Contains(t, buf.String(), "Test message: hello")

	// Test Printf when disabled
	buf.Reset()
	testLogger.SetEnabled(false)
	testLogger.Printf("Should not appear: %s", "world")
	assert.Empty(t, buf.String())

	// Restore original state
	AppLogger.SetEnabled(originalEnabled)
}

func TestLoggerPrintln(t *testing.T) {
	var buf bytes.Buffer
	originalEnabled := AppLogger.enabled

	// Create a test logger with buffer
	testLogger := NewLogger(&buf, &buf, true)

	// Test Println when enabled
	testLogger.Println("Test message")
	assert.Contains(t, buf.String(), "Test message")

	// Test Println when disabled
	buf.Reset()
	testLogger.SetEnabled(false)
	testLogger.Println("Should not appear")
	assert.Empty(t, buf.String())

	// Restore original state
	AppLogger.SetEnabled(originalEnabled)
}
