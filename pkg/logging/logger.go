// Package logging provides a standardized logging interface for CUDly.
// This package wraps the standard log package and provides structured logging
// capabilities with log levels.
package logging

import (
	"fmt"
	"io"
	"log"
	"os"
	"sort"
	"strings"
	"sync/atomic"
)

// Level represents a logging level
type Level int

const (
	// LevelDebug is for detailed debugging information
	LevelDebug Level = iota
	// LevelInfo is for general informational messages
	LevelInfo
	// LevelWarn is for warning messages
	LevelWarn
	// LevelError is for error messages
	LevelError
)

// Logger provides structured logging capabilities.
//
// level is stored as an atomic.Int32 so that SetLevel/SetLevelValue (called at
// runtime, sometimes after worker goroutines have launched) and the level reads
// performed by every Debug/Info/Warn/Error call are race-free under the
// concurrent fan-out, which logs from many goroutines.
type Logger struct {
	level    atomic.Int32
	logger   *log.Logger
	output   io.Writer
	prefix   string
	metadata map[string]interface{}
}

// getLevel returns the logger's current level, read atomically.
func (l *Logger) getLevel() Level {
	return Level(l.level.Load())
}

// setLevel stores the logger's level atomically.
func (l *Logger) setLevel(level Level) {
	l.level.Store(int32(level))
}

// Config holds logger configuration
type Config struct {
	Level      string
	Output     io.Writer
	Prefix     string
	TimeFormat string
}

var defaultLogger *Logger

func init() {
	defaultLogger = New(Config{
		Level:  os.Getenv("LOG_LEVEL"),
		Output: os.Stderr,
	})
}

// ParseLevel parses a string log level
func ParseLevel(s string) Level {
	switch strings.ToLower(s) {
	case "debug":
		return LevelDebug
	case "info", "":
		return LevelInfo
	case "warn", "warning":
		return LevelWarn
	case "error":
		return LevelError
	default:
		return LevelInfo
	}
}

// New creates a new logger with the given configuration
func New(cfg Config) *Logger {
	output := cfg.Output
	if output == nil {
		output = os.Stderr
	}

	flags := log.LstdFlags
	if cfg.TimeFormat != "" {
		flags = 0 // Use custom time format
	}

	l := &Logger{
		logger:   log.New(output, cfg.Prefix, flags),
		output:   output,
		prefix:   cfg.Prefix,
		metadata: make(map[string]interface{}),
	}
	l.setLevel(ParseLevel(cfg.Level))
	return l
}

// SetLevel sets the logging level
func SetLevel(level string) {
	defaultLogger.setLevel(ParseLevel(level))
}

// SetLevelValue sets the logging level using a Level value
func SetLevelValue(level Level) {
	defaultLogger.setLevel(level)
}

// GetLevel returns the current log level
func GetLevel() Level {
	return defaultLogger.getLevel()
}

// With creates a new logger with additional metadata
func (l *Logger) With(key string, value interface{}) *Logger {
	newLogger := &Logger{
		logger:   l.logger,
		output:   l.output,
		prefix:   l.prefix,
		metadata: make(map[string]interface{}),
	}
	newLogger.setLevel(l.getLevel())
	for k, v := range l.metadata {
		newLogger.metadata[k] = v
	}
	newLogger.metadata[key] = value
	return newLogger
}

// formatMessage formats a log message with metadata
func (l *Logger) formatMessage(msg string) string {
	if len(l.metadata) == 0 {
		return msg
	}

	keys := make([]string, 0, len(l.metadata))
	for k := range l.metadata {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var pairs []string
	for _, k := range keys {
		pairs = append(pairs, fmt.Sprintf("%s=%v", k, l.metadata[k]))
	}
	return fmt.Sprintf("%s [%s]", msg, strings.Join(pairs, " "))
}

// Debug logs a debug message
func (l *Logger) Debug(msg string) {
	if l.getLevel() <= LevelDebug {
		l.logger.Printf("[DEBUG] %s", l.formatMessage(msg))
	}
}

// Debugf logs a formatted debug message
func (l *Logger) Debugf(format string, args ...interface{}) {
	if l.getLevel() <= LevelDebug {
		l.logger.Printf("[DEBUG] %s", l.formatMessage(fmt.Sprintf(format, args...)))
	}
}

// Info logs an info message
func (l *Logger) Info(msg string) {
	if l.getLevel() <= LevelInfo {
		l.logger.Printf("[INFO] %s", l.formatMessage(msg))
	}
}

// Infof logs a formatted info message
func (l *Logger) Infof(format string, args ...interface{}) {
	if l.getLevel() <= LevelInfo {
		l.logger.Printf("[INFO] %s", l.formatMessage(fmt.Sprintf(format, args...)))
	}
}

// Warn logs a warning message
func (l *Logger) Warn(msg string) {
	if l.getLevel() <= LevelWarn {
		l.logger.Printf("[WARN] %s", l.formatMessage(msg))
	}
}

// Warnf logs a formatted warning message
func (l *Logger) Warnf(format string, args ...interface{}) {
	if l.getLevel() <= LevelWarn {
		l.logger.Printf("[WARN] %s", l.formatMessage(fmt.Sprintf(format, args...)))
	}
}

// Error logs an error message
func (l *Logger) Error(msg string) {
	if l.getLevel() <= LevelError {
		l.logger.Printf("[ERROR] %s", l.formatMessage(msg))
	}
}

// Errorf logs a formatted error message
func (l *Logger) Errorf(format string, args ...interface{}) {
	if l.getLevel() <= LevelError {
		l.logger.Printf("[ERROR] %s", l.formatMessage(fmt.Sprintf(format, args...)))
	}
}

// Package-level functions for default logger

// Debug logs a debug message using the default logger
func Debug(msg string) {
	defaultLogger.Debug(msg)
}

// Debugf logs a formatted debug message using the default logger
func Debugf(format string, args ...interface{}) {
	defaultLogger.Debugf(format, args...)
}

// Info logs an info message using the default logger
func Info(msg string) {
	defaultLogger.Info(msg)
}

// Infof logs a formatted info message using the default logger
func Infof(format string, args ...interface{}) {
	defaultLogger.Infof(format, args...)
}

// Warn logs a warning message using the default logger
func Warn(msg string) {
	defaultLogger.Warn(msg)
}

// Warnf logs a formatted warning message using the default logger
func Warnf(format string, args ...interface{}) {
	defaultLogger.Warnf(format, args...)
}

// Error logs an error message using the default logger
func Error(msg string) {
	defaultLogger.Error(msg)
}

// Errorf logs a formatted error message using the default logger
func Errorf(format string, args ...interface{}) {
	defaultLogger.Errorf(format, args...)
}

// With creates a new logger with additional metadata from the default logger
func With(key string, value interface{}) *Logger {
	return defaultLogger.With(key, value)
}

// GetDefaultLogger returns the default logger instance
func GetDefaultLogger() *Logger {
	return defaultLogger
}

// SetOutput redirects the default logger's output to w and returns the
// previous io.Writer so callers can restore it (e.g. in a test t.Cleanup).
// The default logger binds os.Stderr at init() time, so reassigning the
// os.Stderr variable does not redirect it; this is the supported seam for
// capturing default-logger output in tests.
func SetOutput(w io.Writer) io.Writer {
	prev := defaultLogger.output
	defaultLogger.output = w
	defaultLogger.logger.SetOutput(w)
	return prev
}
