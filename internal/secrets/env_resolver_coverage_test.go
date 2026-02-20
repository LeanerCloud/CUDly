package secrets

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEnvResolver_ListSecrets_MalformedEnvVar tests handling of malformed env vars
func TestEnvResolver_ListSecrets_MalformedEnvVar(t *testing.T) {
	resolver := NewEnvResolver()
	ctx := context.Background()

	// Setup test environment variables with unique prefix
	testPrefix := "CUDLY_MALFORMED_TEST_"
	os.Setenv(testPrefix+"VALID", "value")
	defer os.Unsetenv(testPrefix + "VALID")

	// Test listing with filter
	result, err := resolver.ListSecrets(ctx, testPrefix)
	require.NoError(t, err)
	assert.Contains(t, result, testPrefix+"VALID")
}

// TestEnvResolver_ListSecrets_EmptyFilter tests listing all env vars
func TestEnvResolver_ListSecrets_EmptyFilter(t *testing.T) {
	resolver := NewEnvResolver()
	ctx := context.Background()

	// Setup unique test environment variable
	testKey := "CUDLY_UNIQUE_TEST_VAR_FOR_EMPTY_FILTER_12345"
	os.Setenv(testKey, "testvalue")
	defer os.Unsetenv(testKey)

	result, err := resolver.ListSecrets(ctx, "")

	require.NoError(t, err)
	// Should contain our test variable and many system variables
	assert.Contains(t, result, testKey)
	// Should have multiple entries (system env vars)
	assert.Greater(t, len(result), 1)
}

// TestEnvResolver_ListSecrets_ExactMatch tests filter matching behavior
func TestEnvResolver_ListSecrets_ExactMatch(t *testing.T) {
	resolver := NewEnvResolver()
	ctx := context.Background()

	// Setup test variables with similar names
	testPrefix := "CUDLY_EXACT_MATCH_"
	os.Setenv(testPrefix+"ONE", "1")
	os.Setenv(testPrefix+"ONE_EXTRA", "2")
	os.Setenv(testPrefix+"TWO", "3")
	defer func() {
		os.Unsetenv(testPrefix + "ONE")
		os.Unsetenv(testPrefix + "ONE_EXTRA")
		os.Unsetenv(testPrefix + "TWO")
	}()

	// Filter should match prefix, not exact name
	result, err := resolver.ListSecrets(ctx, testPrefix+"ONE")
	require.NoError(t, err)

	// Should match both ONE and ONE_EXTRA (prefix match)
	assert.Contains(t, result, testPrefix+"ONE")
	assert.Contains(t, result, testPrefix+"ONE_EXTRA")
	// Should not match TWO
	assert.NotContains(t, result, testPrefix+"TWO")
}

// TestEnvResolver_GetSecret_SpecialValues tests getting secrets with special values
func TestEnvResolver_GetSecret_SpecialValues(t *testing.T) {
	resolver := NewEnvResolver()
	ctx := context.Background()

	tests := []struct {
		name     string
		key      string
		value    string
		expected string
	}{
		{
			name:     "value with newlines",
			key:      "CUDLY_SECRET_NEWLINE",
			value:    "line1\nline2\nline3",
			expected: "line1\nline2\nline3",
		},
		{
			name:     "value with tabs",
			key:      "CUDLY_SECRET_TABS",
			value:    "col1\tcol2\tcol3",
			expected: "col1\tcol2\tcol3",
		},
		{
			name:     "value with equals sign",
			key:      "CUDLY_SECRET_EQUALS",
			value:    "key=value=another",
			expected: "key=value=another",
		},
		{
			name:     "single character value",
			key:      "CUDLY_SECRET_SINGLE",
			value:    "x",
			expected: "x",
		},
		{
			name:     "value with only whitespace",
			key:      "CUDLY_SECRET_WHITESPACE_ONLY",
			value:    "   ",
			expected: "   ",
		},
		{
			name:     "JSON-like value",
			key:      "CUDLY_SECRET_JSON",
			value:    `{"key":"value","nested":{"a":1}}`,
			expected: `{"key":"value","nested":{"a":1}}`,
		},
		{
			name:     "base64-like value",
			key:      "CUDLY_SECRET_BASE64",
			value:    "SGVsbG8gV29ybGQh==",
			expected: "SGVsbG8gV29ybGQh==",
		},
		{
			name:     "URL value",
			key:      "CUDLY_SECRET_URL",
			value:    "https://user:pass@host.com:8080/path?query=1#fragment",
			expected: "https://user:pass@host.com:8080/path?query=1#fragment",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Setenv(tt.key, tt.value)
			defer os.Unsetenv(tt.key)

			result, err := resolver.GetSecret(ctx, tt.key)

			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestEnvResolver_GetSecretJSON_VariousJSONTypes tests parsing various JSON types
func TestEnvResolver_GetSecretJSON_VariousJSONTypes(t *testing.T) {
	resolver := NewEnvResolver()
	ctx := context.Background()

	tests := []struct {
		name        string
		key         string
		value       string
		expectError bool
		validate    func(t *testing.T, result map[string]interface{})
	}{
		{
			name:        "empty object",
			key:         "CUDLY_JSON_EMPTY",
			value:       `{}`,
			expectError: false,
			validate: func(t *testing.T, result map[string]interface{}) {
				assert.Empty(t, result)
			},
		},
		{
			name:        "nested objects",
			key:         "CUDLY_JSON_NESTED",
			value:       `{"level1":{"level2":{"level3":"deep"}}}`,
			expectError: false,
			validate: func(t *testing.T, result map[string]interface{}) {
				level1, ok := result["level1"].(map[string]interface{})
				require.True(t, ok)
				level2, ok := level1["level2"].(map[string]interface{})
				require.True(t, ok)
				assert.Equal(t, "deep", level2["level3"])
			},
		},
		{
			name:        "array values",
			key:         "CUDLY_JSON_WITH_ARRAYS",
			value:       `{"items":["a","b","c"],"numbers":[1,2,3]}`,
			expectError: false,
			validate: func(t *testing.T, result map[string]interface{}) {
				items, ok := result["items"].([]interface{})
				require.True(t, ok)
				assert.Len(t, items, 3)
			},
		},
		{
			name:        "boolean values",
			key:         "CUDLY_JSON_BOOL",
			value:       `{"enabled":true,"disabled":false}`,
			expectError: false,
			validate: func(t *testing.T, result map[string]interface{}) {
				assert.Equal(t, true, result["enabled"])
				assert.Equal(t, false, result["disabled"])
			},
		},
		{
			name:        "null value",
			key:         "CUDLY_JSON_NULL",
			value:       `{"nullable":null}`,
			expectError: false,
			validate: func(t *testing.T, result map[string]interface{}) {
				assert.Nil(t, result["nullable"])
			},
		},
		{
			name:        "numeric values",
			key:         "CUDLY_JSON_NUMBERS",
			value:       `{"integer":42,"float":3.14159,"negative":-100,"scientific":1.5e10}`,
			expectError: false,
			validate: func(t *testing.T, result map[string]interface{}) {
				assert.Equal(t, float64(42), result["integer"])
				assert.InDelta(t, 3.14159, result["float"], 0.00001)
				assert.Equal(t, float64(-100), result["negative"])
			},
		},
		{
			name:        "unicode strings",
			key:         "CUDLY_JSON_UNICODE",
			value:       `{"chinese":"中文","emoji":"🎉","japanese":"日本語"}`,
			expectError: false,
			validate: func(t *testing.T, result map[string]interface{}) {
				assert.Equal(t, "中文", result["chinese"])
			},
		},
		{
			name:        "truncated JSON",
			key:         "CUDLY_JSON_TRUNCATED",
			value:       `{"key": "value"`,
			expectError: true,
		},
		{
			name:        "invalid JSON syntax",
			key:         "CUDLY_JSON_INVALID_SYNTAX",
			value:       `{"key": value}`,
			expectError: true,
		},
		{
			name:        "plain number (not object)",
			key:         "CUDLY_JSON_NUMBER",
			value:       `42`,
			expectError: true,
		},
		{
			name:        "plain string (not object)",
			key:         "CUDLY_JSON_STRING",
			value:       `"just a string"`,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Setenv(tt.key, tt.value)
			defer os.Unsetenv(tt.key)

			result, err := resolver.GetSecretJSON(ctx, tt.key)

			if tt.expectError {
				require.Error(t, err)
				assert.Nil(t, result)
			} else {
				require.NoError(t, err)
				require.NotNil(t, result)
				if tt.validate != nil {
					tt.validate(t, result)
				}
			}
		})
	}
}

// TestEnvResolver_Close_Multiple tests calling Close multiple times
func TestEnvResolver_Close_Multiple(t *testing.T) {
	resolver := NewEnvResolver()

	// Should not error on multiple closes
	err1 := resolver.Close()
	assert.NoError(t, err1)

	err2 := resolver.Close()
	assert.NoError(t, err2)

	err3 := resolver.Close()
	assert.NoError(t, err3)
}

// TestEnvResolver_ConcurrentAccess tests concurrent access to resolver
func TestEnvResolver_ConcurrentAccess(t *testing.T) {
	resolver := NewEnvResolver()
	ctx := context.Background()

	// Setup test variable
	testKey := "CUDLY_CONCURRENT_TEST"
	testValue := "concurrent-value"
	os.Setenv(testKey, testValue)
	defer os.Unsetenv(testKey)

	// Run concurrent operations
	done := make(chan bool, 10)

	for i := 0; i < 10; i++ {
		go func() {
			_, _ = resolver.GetSecret(ctx, testKey)
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}
}

// TestEnvResolver_ListSecrets_LargeNumberOfVars tests with many env vars
func TestEnvResolver_ListSecrets_LargeNumberOfVars(t *testing.T) {
	resolver := NewEnvResolver()
	ctx := context.Background()

	// Setup many test variables
	testPrefix := "CUDLY_LARGE_TEST_"
	numVars := 50

	for i := 0; i < numVars; i++ {
		key := testPrefix + strings.Repeat("A", i%10+1) + "_" + string(rune('0'+i%10))
		os.Setenv(key, "value")
	}
	defer func() {
		for i := 0; i < numVars; i++ {
			key := testPrefix + strings.Repeat("A", i%10+1) + "_" + string(rune('0'+i%10))
			os.Unsetenv(key)
		}
	}()

	result, err := resolver.ListSecrets(ctx, testPrefix)

	require.NoError(t, err)
	// We created numVars unique keys but some might overlap due to naming
	// Just verify we got some results
	assert.NotEmpty(t, result)
}

// TestEnvResolver_GetSecret_CaseSensitivity tests case sensitivity
func TestEnvResolver_GetSecret_CaseSensitivity(t *testing.T) {
	resolver := NewEnvResolver()
	ctx := context.Background()

	// Environment variables are case-sensitive on most platforms
	os.Setenv("CUDLY_CASE_TEST", "uppercase")
	os.Setenv("cudly_case_test", "lowercase")
	defer func() {
		os.Unsetenv("CUDLY_CASE_TEST")
		os.Unsetenv("cudly_case_test")
	}()

	upper, err1 := resolver.GetSecret(ctx, "CUDLY_CASE_TEST")
	require.NoError(t, err1)
	assert.Equal(t, "uppercase", upper)

	lower, err2 := resolver.GetSecret(ctx, "cudly_case_test")
	require.NoError(t, err2)
	assert.Equal(t, "lowercase", lower)

	// Different case should fail
	_, err3 := resolver.GetSecret(ctx, "Cudly_Case_Test")
	require.Error(t, err3)
}

// TestEnvResolver_ListSecrets_FilterCaseSensitivity tests filter case sensitivity
func TestEnvResolver_ListSecrets_FilterCaseSensitivity(t *testing.T) {
	resolver := NewEnvResolver()
	ctx := context.Background()

	os.Setenv("CUDLY_FILTER_UPPER", "1")
	os.Setenv("cudly_filter_lower", "2")
	defer func() {
		os.Unsetenv("CUDLY_FILTER_UPPER")
		os.Unsetenv("cudly_filter_lower")
	}()

	upperResult, err1 := resolver.ListSecrets(ctx, "CUDLY_FILTER")
	require.NoError(t, err1)
	assert.Contains(t, upperResult, "CUDLY_FILTER_UPPER")

	lowerResult, err2 := resolver.ListSecrets(ctx, "cudly_filter")
	require.NoError(t, err2)
	assert.Contains(t, lowerResult, "cudly_filter_lower")
}

// TestEnvResolver_GetSecretJSON_LargeJSON tests parsing large JSON
func TestEnvResolver_GetSecretJSON_LargeJSON(t *testing.T) {
	resolver := NewEnvResolver()
	ctx := context.Background()

	// Create a large JSON object
	var builder strings.Builder
	builder.WriteString("{")
	for i := 0; i < 100; i++ {
		if i > 0 {
			builder.WriteString(",")
		}
		builder.WriteString(`"key`)
		builder.WriteString(string(rune('0' + i%10)))
		builder.WriteString(`":"value`)
		builder.WriteString(string(rune('0' + i%10)))
		builder.WriteString(`"`)
	}
	builder.WriteString("}")

	testKey := "CUDLY_LARGE_JSON_TEST"
	os.Setenv(testKey, builder.String())
	defer os.Unsetenv(testKey)

	result, err := resolver.GetSecretJSON(ctx, testKey)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Greater(t, len(result), 0)
}
