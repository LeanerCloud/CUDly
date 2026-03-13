package main

import (
	"os"
	"testing"
	"time"
)

func TestGetTaskTimeout(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		expected time.Duration
	}{
		{
			name:     "default when env not set",
			envValue: "",
			expected: 15 * time.Minute,
		},
		{
			name:     "valid env value",
			envValue: "60",
			expected: 60 * time.Second,
		},
		{
			name:     "invalid env value falls back to default",
			envValue: "not-a-number",
			expected: 15 * time.Minute,
		},
		{
			name:     "zero falls back to default",
			envValue: "0",
			expected: 15 * time.Minute,
		},
		{
			name:     "negative falls back to default",
			envValue: "-10",
			expected: 15 * time.Minute,
		},
		{
			name:     "large value",
			envValue: "3600",
			expected: 3600 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			old := os.Getenv("TASK_TIMEOUT")
			defer func() {
				if old == "" {
					os.Unsetenv("TASK_TIMEOUT")
				} else {
					os.Setenv("TASK_TIMEOUT", old)
				}
			}()

			if tt.envValue != "" {
				os.Setenv("TASK_TIMEOUT", tt.envValue)
			} else {
				os.Unsetenv("TASK_TIMEOUT")
			}

			result := getTaskTimeout()
			if result != tt.expected {
				t.Errorf("getTaskTimeout() = %v, want %v", result, tt.expected)
			}
		})
	}
}
