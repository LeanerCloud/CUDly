package logging

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Note on parallelism (issue #58):
// The package exposes a mutable global `defaultLogger` plus `SetLevel` /
// `SetLevelValue` mutators. Tests that swap out or mutate the global
// (TestDefaultLogger, TestSetLevel, TestGetLevel, TestSetLevelValue,
// TestGetDefaultLogger) MUST stay serial — running them in parallel with
// each other or with anything that reads `defaultLogger` would race.
// The remaining tests construct their own local *Logger and are safe to
// parallelize.

func TestParseLevel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		expected Level
	}{
		{"debug", LevelDebug},
		{"DEBUG", LevelDebug},
		{"info", LevelInfo},
		{"INFO", LevelInfo},
		{"", LevelInfo},
		{"warn", LevelWarn},
		{"warning", LevelWarn},
		{"WARN", LevelWarn},
		{"error", LevelError},
		{"ERROR", LevelError},
		{"unknown", LevelInfo},
	}

	for _, tt := range tests {
		tt := tt // capture for parallel sub-test (defensive: pre-Go-1.22 toolchains)
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			result := ParseLevel(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestLogger_LevelFiltering(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := New(Config{
		Level:  "warn",
		Output: &buf,
	})

	logger.Debug("debug message")
	logger.Info("info message")
	logger.Warn("warn message")
	logger.Error("error message")

	output := buf.String()

	assert.NotContains(t, output, "debug message")
	assert.NotContains(t, output, "info message")
	assert.Contains(t, output, "warn message")
	assert.Contains(t, output, "error message")
}

func TestLogger_Formatting(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := New(Config{
		Level:  "debug",
		Output: &buf,
	})

	logger.Debugf("count: %d", 42)
	output := buf.String()

	assert.Contains(t, output, "[DEBUG]")
	assert.Contains(t, output, "count: 42")
}

func TestLogger_With(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := New(Config{
		Level:  "info",
		Output: &buf,
	})

	contextLogger := logger.With("request_id", "abc123").With("user", "john")
	contextLogger.Info("test message")

	output := buf.String()
	assert.Contains(t, output, "request_id=abc123")
	assert.Contains(t, output, "user=john")
}

func TestLogger_WithDoesNotModifyOriginal(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := New(Config{
		Level:  "info",
		Output: &buf,
	})

	_ = logger.With("key", "value")
	logger.Info("original logger")

	output := buf.String()
	assert.NotContains(t, output, "key=value")
}

// TestDefaultLogger mutates the package-level `defaultLogger` global; left
// SERIAL on purpose — see file-top note.
func TestDefaultLogger(t *testing.T) {
	// Test that default logger functions work
	oldLogger := defaultLogger
	defer func() { defaultLogger = oldLogger }()

	var buf bytes.Buffer
	defaultLogger = New(Config{
		Level:  "debug",
		Output: &buf,
	})

	Info("test info")
	Infof("test %s", "formatted")
	Debug("test debug")
	Debugf("test %s", "debug formatted")
	Warn("test warn")
	Warnf("test %s", "warn formatted")
	Error("test error")
	Errorf("test %s", "error formatted")

	output := buf.String()
	assert.Contains(t, output, "test info")
	assert.Contains(t, output, "test formatted")
	assert.Contains(t, output, "test debug")
	assert.Contains(t, output, "test warn")
	assert.Contains(t, output, "test error")
}

// TestSetLevel mutates the package-level `defaultLogger` global; left
// SERIAL on purpose — see file-top note.
func TestSetLevel(t *testing.T) {
	oldLogger := defaultLogger
	defer func() { defaultLogger = oldLogger }()

	var buf bytes.Buffer
	defaultLogger = New(Config{
		Level:  "debug",
		Output: &buf,
	})

	// Initially at debug level
	Debug("should appear")
	assert.Contains(t, buf.String(), "should appear")

	buf.Reset()
	SetLevel("error")

	Debug("should not appear")
	Info("should not appear")
	Warn("should not appear")

	output := buf.String()
	assert.NotContains(t, output, "should not appear")

	Error("should appear")
	assert.Contains(t, buf.String(), "should appear")
}

func TestLoggerLevels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		level    string
		testFunc func(*Logger)
		tag      string
	}{
		{"debug", func(l *Logger) { l.Debug("test") }, "[DEBUG]"},
		{"info", func(l *Logger) { l.Info("test") }, "[INFO]"},
		{"warn", func(l *Logger) { l.Warn("test") }, "[WARN]"},
		{"error", func(l *Logger) { l.Error("test") }, "[ERROR]"},
	}

	for _, tt := range tests {
		tt := tt // capture for parallel sub-test (defensive: pre-Go-1.22 toolchains)
		t.Run(tt.level, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			logger := New(Config{
				Level:  "debug",
				Output: &buf,
			})

			tt.testFunc(logger)
			output := buf.String()

			assert.Contains(t, output, tt.tag)
			assert.Contains(t, output, "test")
		})
	}
}

func TestLoggerOutput(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := New(Config{
		Level:  "info",
		Output: &buf,
	})

	logger.Infof("Processing %d items in %s", 5, "region-1")

	output := buf.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	assert.Len(t, lines, 1)
	assert.Contains(t, output, "Processing 5 items in region-1")
}

// TestGetLevel mutates the package-level `defaultLogger` global; left
// SERIAL on purpose — see file-top note.
func TestGetLevel(t *testing.T) {
	oldLogger := defaultLogger
	defer func() { defaultLogger = oldLogger }()

	defaultLogger = New(Config{
		Level: "warn",
	})

	assert.Equal(t, LevelWarn, GetLevel())

	SetLevel("debug")
	assert.Equal(t, LevelDebug, GetLevel())
}

// TestSetLevelValue mutates the package-level `defaultLogger` global; left
// SERIAL on purpose — see file-top note.
func TestSetLevelValue(t *testing.T) {
	oldLogger := defaultLogger
	defer func() { defaultLogger = oldLogger }()

	defaultLogger = New(Config{
		Level: "info",
	})

	SetLevelValue(LevelError)
	assert.Equal(t, LevelError, GetLevel())

	SetLevelValue(LevelDebug)
	assert.Equal(t, LevelDebug, GetLevel())
}

// TestGetDefaultLogger reads the package-level `defaultLogger` global; left
// SERIAL on purpose so it cannot race with the mutating tests above —
// see file-top note.
func TestGetDefaultLogger(t *testing.T) {
	logger := GetDefaultLogger()
	assert.NotNil(t, logger)
	assert.Equal(t, defaultLogger, logger)
}

func TestWith_ChainedCalls(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := New(Config{
		Level:  "info",
		Output: &buf,
	})

	logger.With("a", 1).With("b", 2).With("c", 3).Info("chained")

	output := buf.String()
	assert.Contains(t, output, "a=1")
	assert.Contains(t, output, "b=2")
	assert.Contains(t, output, "c=3")
}
