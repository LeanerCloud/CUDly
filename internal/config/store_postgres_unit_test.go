package config

import (
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestTimeFromTTL tests the timeFromTTL helper function
func TestTimeFromTTL(t *testing.T) {
	tests := []struct {
		name     string
		ttl      int64
		expected interface{}
	}{
		{
			name:     "zero TTL returns nil",
			ttl:      0,
			expected: nil,
		},
		{
			name: "positive TTL returns time pointer",
			ttl:  1704067200, // 2024-01-01 00:00:00 UTC
		},
		{
			name: "negative TTL returns time pointer",
			ttl:  -86400, // negative timestamp (before epoch)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := timeFromTTL(tt.ttl)
			if tt.ttl == 0 {
				assert.Nil(t, result)
			} else {
				assert.NotNil(t, result)
				timePtr, ok := result.(*time.Time)
				assert.True(t, ok, "result should be *time.Time")
				assert.Equal(t, tt.ttl, timePtr.Unix())
			}
		})
	}
}

// TestTtlFromTime tests the ttlFromTime helper function
func TestTtlFromTime(t *testing.T) {
	tests := []struct {
		name     string
		time     time.Time
		expected int64
	}{
		{
			name:     "specific time returns unix timestamp",
			time:     time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			expected: 1704067200,
		},
		{
			name:     "zero time returns negative",
			time:     time.Time{},
			expected: -62135596800, // Unix timestamp of Go zero time
		},
		{
			name:     "current time",
			time:     time.Now(),
			expected: time.Now().Unix(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ttlFromTime(tt.time)
			if tt.name == "current time" {
				// Allow small delta for current time test
				assert.InDelta(t, tt.expected, result, 1)
			} else {
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

// TestNullStringFromString tests the nullStringFromString helper function
func TestNullStringFromString(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected sql.NullString
	}{
		{
			name:     "empty string returns invalid NullString",
			input:    "",
			expected: sql.NullString{String: "", Valid: false},
		},
		{
			name:     "non-empty string returns valid NullString",
			input:    "test",
			expected: sql.NullString{String: "test", Valid: true},
		},
		{
			name:     "string with spaces",
			input:    "hello world",
			expected: sql.NullString{String: "hello world", Valid: true},
		},
		{
			name:     "string with special characters",
			input:    "test@example.com",
			expected: sql.NullString{String: "test@example.com", Valid: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := nullStringFromString(tt.input)
			assert.Equal(t, tt.expected.String, result.String)
			assert.Equal(t, tt.expected.Valid, result.Valid)
		})
	}
}

// TestNewPostgresStore tests creating a new PostgresStore
func TestNewPostgresStore(t *testing.T) {
	// Test that NewPostgresStore returns a non-nil store even with nil db
	store := NewPostgresStore(nil)
	assert.NotNil(t, store)
}

// TestTimeFromTTLRoundTrip tests that timeFromTTL and ttlFromTime are consistent
func TestTimeFromTTLRoundTrip(t *testing.T) {
	// Test round trip conversion
	originalTime := time.Date(2024, 6, 15, 12, 30, 0, 0, time.UTC)
	ttl := ttlFromTime(originalTime)

	result := timeFromTTL(ttl)
	assert.NotNil(t, result)

	timePtr, ok := result.(*time.Time)
	assert.True(t, ok)
	// Note: seconds precision only
	assert.Equal(t, originalTime.Unix(), timePtr.Unix())
}

// TestValidProvidersConstant tests that ValidProviders is properly defined
func TestValidProvidersConstant(t *testing.T) {
	assert.Contains(t, ValidProviders, "aws")
	assert.Contains(t, ValidProviders, "azure")
	assert.Contains(t, ValidProviders, "gcp")
	assert.Len(t, ValidProviders, 3)
}

// TestValidPaymentOptionsConstant tests that ValidPaymentOptions is properly defined
func TestValidPaymentOptionsConstant(t *testing.T) {
	assert.Contains(t, ValidPaymentOptions, "no-upfront")
	assert.Contains(t, ValidPaymentOptions, "partial-upfront")
	assert.Contains(t, ValidPaymentOptions, "all-upfront")
	assert.Len(t, ValidPaymentOptions, 3)
}

// TestValidRampScheduleTypesConstant tests that ValidRampScheduleTypes is properly defined
func TestValidRampScheduleTypesConstant(t *testing.T) {
	assert.Contains(t, ValidRampScheduleTypes, "immediate")
	assert.Contains(t, ValidRampScheduleTypes, "weekly")
	assert.Contains(t, ValidRampScheduleTypes, "monthly")
	assert.Contains(t, ValidRampScheduleTypes, "custom")
	assert.Len(t, ValidRampScheduleTypes, 4)
}
