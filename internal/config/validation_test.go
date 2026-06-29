package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGlobalConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  GlobalConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid empty config",
			config: GlobalConfig{
				DefaultTerm: 3,
			},
			wantErr: false,
		},
		{
			name: "valid config with all fields",
			config: GlobalConfig{
				EnabledProviders:  []string{"aws", "azure", "gcp"},
				NotificationEmail: stringPtr("test@example.com"),
				DefaultTerm:       3,
				DefaultPayment:    "all-upfront",
				DefaultCoverage:   80,
			},
			wantErr: false,
		},
		{
			name: "invalid provider",
			config: GlobalConfig{
				EnabledProviders: []string{"aws", "invalid"},
			},
			wantErr: true,
			errMsg:  "invalid provider: invalid",
		},
		{
			name: "invalid email format",
			config: GlobalConfig{
				NotificationEmail: stringPtr("not-an-email"),
			},
			wantErr: true,
			errMsg:  "invalid notification email format",
		},
		{
			name: "invalid term",
			config: GlobalConfig{
				DefaultTerm: 5,
			},
			wantErr: true,
			errMsg:  "default term must be 1 or 3 years",
		},
		{
			name: "valid term 1 year",
			config: GlobalConfig{
				DefaultTerm: 1,
			},
			wantErr: false,
		},
		{
			name: "invalid payment option",
			config: GlobalConfig{
				DefaultTerm:    3,
				DefaultPayment: "invalid-payment",
			},
			wantErr: true,
			errMsg:  "invalid payment option",
		},
		// Issue #698: GlobalConfig accepts union of all provider payment tokens
		{
			name: "global config accepts azure/gcp upfront token",
			config: GlobalConfig{
				DefaultTerm:    3,
				DefaultPayment: "upfront",
			},
			wantErr: false,
		},
		{
			name: "global config accepts azure/gcp monthly token",
			config: GlobalConfig{
				DefaultTerm:    3,
				DefaultPayment: "monthly",
			},
			wantErr: false,
		},
		{
			name: "coverage too low",
			config: GlobalConfig{
				DefaultTerm:     3,
				DefaultCoverage: -1,
			},
			wantErr: true,
			errMsg:  "default coverage must be between 0 and 100",
		},
		{
			name: "coverage too high",
			config: GlobalConfig{
				DefaultTerm:     3,
				DefaultCoverage: 101,
			},
			wantErr: true,
			errMsg:  "default coverage must be between 0 and 100",
		},
		{
			name: "valid no-upfront payment",
			config: GlobalConfig{
				DefaultTerm:    3,
				DefaultPayment: "no-upfront",
			},
			wantErr: false,
		},
		{
			name: "valid partial-upfront payment",
			config: GlobalConfig{
				DefaultTerm:    3,
				DefaultPayment: "partial-upfront",
			},
			wantErr: false,
		},
		{
			name: "invalid collection schedule",
			config: GlobalConfig{
				DefaultTerm:        3,
				CollectionSchedule: "minutely",
			},
			wantErr: true,
			errMsg:  "invalid collection_schedule",
		},
		{
			name: "valid empty collection schedule",
			config: GlobalConfig{
				DefaultTerm:        3,
				CollectionSchedule: "",
			},
			wantErr: false,
		},
		{
			name: "valid hourly collection schedule",
			config: GlobalConfig{
				DefaultTerm:        3,
				CollectionSchedule: "hourly",
			},
			wantErr: false,
		},
		{
			name: "valid daily collection schedule",
			config: GlobalConfig{
				DefaultTerm:        3,
				CollectionSchedule: "daily",
			},
			wantErr: false,
		},
		{
			name: "notification days before negative",
			config: GlobalConfig{
				DefaultTerm:            3,
				NotificationDaysBefore: -1,
			},
			wantErr: true,
			errMsg:  "notification_days_before must be between 0",
		},
		{
			name: "notification days before exceeds max",
			config: GlobalConfig{
				DefaultTerm:            3,
				NotificationDaysBefore: MaxNotificationDaysBefore + 1,
			},
			wantErr: true,
			errMsg:  "notification_days_before must be between 0",
		},
		{
			name: "notification days before at max is valid",
			config: GlobalConfig{
				DefaultTerm:            3,
				NotificationDaysBefore: MaxNotificationDaysBefore,
			},
			wantErr: false,
		},
		// Issue #301: RecommendationsCacheStaleHours validation
		{
			name: "stale hours zero is valid (disable auto-refresh)",
			config: GlobalConfig{
				DefaultTerm:                    3,
				RecommendationsCacheStaleHours: 0,
				RecommendationsLookbackDays:    7,
			},
			wantErr: false,
		},
		{
			name: "stale hours at max (8760) is valid",
			config: GlobalConfig{
				DefaultTerm:                    3,
				RecommendationsCacheStaleHours: MaxRecommendationsCacheStaleHours,
				RecommendationsLookbackDays:    7,
			},
			wantErr: false,
		},
		{
			name: "stale hours negative is invalid",
			config: GlobalConfig{
				DefaultTerm:                    3,
				RecommendationsCacheStaleHours: -1,
				RecommendationsLookbackDays:    7,
			},
			wantErr: true,
			errMsg:  "recommendations_cache_stale_hours must be between 0",
		},
		{
			name: "stale hours above max is invalid",
			config: GlobalConfig{
				DefaultTerm:                    3,
				RecommendationsCacheStaleHours: MaxRecommendationsCacheStaleHours + 1,
				RecommendationsLookbackDays:    7,
			},
			wantErr: true,
			errMsg:  "recommendations_cache_stale_hours must be between 0",
		},
		// Issue #301: RecommendationsLookbackDays validation (AWS enum {7,30,60})
		{
			name: "lookback 7 is valid",
			config: GlobalConfig{
				DefaultTerm:                 3,
				RecommendationsLookbackDays: 7,
			},
			wantErr: false,
		},
		{
			name: "lookback 30 is valid",
			config: GlobalConfig{
				DefaultTerm:                 3,
				RecommendationsLookbackDays: 30,
			},
			wantErr: false,
		},
		{
			name: "lookback 60 is valid",
			config: GlobalConfig{
				DefaultTerm:                 3,
				RecommendationsLookbackDays: 60,
			},
			wantErr: false,
		},
		{
			name: "lookback 14 is invalid (not in AWS enum)",
			config: GlobalConfig{
				DefaultTerm:                 3,
				RecommendationsLookbackDays: 14,
			},
			wantErr: true,
			errMsg:  "recommendations_lookback_days must be one of",
		},
		{
			name: "lookback 0 is valid (unset, uses backend default)",
			config: GlobalConfig{
				DefaultTerm:                 3,
				RecommendationsLookbackDays: 0,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestServiceConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  ServiceConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid config",
			config: ServiceConfig{
				Provider: "aws",
				Service:  "rds",
				Enabled:  true,
				Term:     3,
				Coverage: 80,
				Payment:  "all-upfront",
			},
			wantErr: false,
		},
		{
			name: "missing provider",
			config: ServiceConfig{
				Service: "rds",
			},
			wantErr: true,
			errMsg:  "provider is required",
		},
		{
			name: "invalid provider",
			config: ServiceConfig{
				Provider: "invalid",
				Service:  "rds",
			},
			wantErr: true,
			errMsg:  "invalid provider",
		},
		{
			name: "missing service",
			config: ServiceConfig{
				Provider: "aws",
			},
			wantErr: true,
			errMsg:  "service is required",
		},
		{
			name: "invalid term",
			config: ServiceConfig{
				Provider: "aws",
				Service:  "rds",
				Term:     5,
			},
			wantErr: true,
			errMsg:  "term must be 1 or 3 years",
		},
		{
			name: "valid term 1 year",
			config: ServiceConfig{
				Provider: "azure",
				Service:  "vm",
				Term:     1,
			},
			wantErr: false,
		},
		{
			name: "invalid payment option",
			config: ServiceConfig{
				Provider: "gcp",
				Service:  "compute",
				Payment:  "invalid-payment",
			},
			wantErr: true,
			errMsg:  "invalid payment option",
		},
		// Issue #698: provider-canonical payment validation
		{
			name: "aws all-upfront is valid",
			config: ServiceConfig{
				Provider: "aws",
				Service:  "ec2",
				Payment:  "all-upfront",
			},
			wantErr: false,
		},
		{
			name: "aws monthly is rejected",
			config: ServiceConfig{
				Provider: "aws",
				Service:  "ec2",
				Payment:  "monthly",
			},
			wantErr: true,
			errMsg:  "invalid payment option",
		},
		{
			name: "azure upfront is valid",
			config: ServiceConfig{
				Provider: "azure",
				Service:  "vm",
				Payment:  "upfront",
			},
			wantErr: false,
		},
		{
			name: "azure monthly is valid",
			config: ServiceConfig{
				Provider: "azure",
				Service:  "vm",
				Payment:  "monthly",
			},
			wantErr: false,
		},
		{
			name: "azure all-upfront is rejected (aws-only token)",
			config: ServiceConfig{
				Provider: "azure",
				Service:  "vm",
				Payment:  "all-upfront",
			},
			wantErr: true,
			errMsg:  "invalid payment option",
		},
		{
			name: "azure no-upfront is rejected (aws-only token)",
			config: ServiceConfig{
				Provider: "azure",
				Service:  "vm",
				Payment:  "no-upfront",
			},
			wantErr: true,
			errMsg:  "invalid payment option",
		},
		{
			name: "azure partial-upfront is rejected (aws-only token)",
			config: ServiceConfig{
				Provider: "azure",
				Service:  "vm",
				Payment:  "partial-upfront",
			},
			wantErr: true,
			errMsg:  "invalid payment option",
		},
		{
			name: "azure error message includes canonical set",
			config: ServiceConfig{
				Provider: "azure",
				Service:  "vm",
				Payment:  "all-upfront",
			},
			wantErr: true,
			errMsg:  "valid for azure: upfront, monthly",
		},
		{
			name: "gcp monthly is valid",
			config: ServiceConfig{
				Provider: "gcp",
				Service:  "computeengine",
				Payment:  "monthly",
			},
			wantErr: false,
		},
		{
			name: "gcp upfront is rejected (gcp is monthly-only)",
			config: ServiceConfig{
				Provider: "gcp",
				Service:  "computeengine",
				Payment:  "upfront",
			},
			wantErr: true,
			errMsg:  "invalid payment option",
		},
		{
			name: "gcp partial-upfront is rejected (aws-only token)",
			config: ServiceConfig{
				Provider: "gcp",
				Service:  "computeengine",
				Payment:  "partial-upfront",
			},
			wantErr: true,
			errMsg:  "invalid payment option",
		},
		{
			name: "gcp all-upfront is rejected (aws-only token)",
			config: ServiceConfig{
				Provider: "gcp",
				Service:  "computeengine",
				Payment:  "all-upfront",
			},
			wantErr: true,
			errMsg:  "invalid payment option",
		},
		{
			name: "gcp no-upfront is rejected (aws-only token)",
			config: ServiceConfig{
				Provider: "gcp",
				Service:  "computeengine",
				Payment:  "no-upfront",
			},
			wantErr: true,
			errMsg:  "invalid payment option",
		},
		{
			name: "gcp error message includes canonical set",
			config: ServiceConfig{
				Provider: "gcp",
				Service:  "computeengine",
				Payment:  "no-upfront",
			},
			wantErr: true,
			errMsg:  "valid for gcp: monthly",
		},
		{
			name: "coverage too low",
			config: ServiceConfig{
				Provider: "aws",
				Service:  "ec2",
				Coverage: -10,
			},
			wantErr: true,
			errMsg:  "coverage must be between 0 and 100",
		},
		{
			name: "coverage too high",
			config: ServiceConfig{
				Provider: "aws",
				Service:  "ec2",
				Coverage: 150,
			},
			wantErr: true,
			errMsg:  "coverage must be between 0 and 100",
		},
		{
			name: "valid min_count",
			config: ServiceConfig{
				Provider: "aws",
				Service:  "ec2",
				Term:     3,
				Coverage: 80,
				Payment:  "all-upfront",
				MinCount: 5,
			},
			wantErr: false,
		},
		{
			name: "negative min_count",
			config: ServiceConfig{
				Provider: "aws",
				Service:  "ec2",
				Coverage: 80,
				MinCount: -1,
			},
			wantErr: true,
			errMsg:  "min_count must be 0 (no filter) or a positive number",
		},
		{
			name: "min_count too high",
			config: ServiceConfig{
				Provider: "aws",
				Service:  "ec2",
				Coverage: 80,
				MinCount: MaxServiceMinCount + 1,
			},
			wantErr: true,
			errMsg:  "exceeds reasonable limit",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestRampSchedule_Validate(t *testing.T) {
	tests := []struct {
		name    string
		sched   RampSchedule
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid empty schedule",
			sched:   RampSchedule{},
			wantErr: false,
		},
		{
			name: "valid immediate schedule",
			sched: RampSchedule{
				Type:             "immediate",
				PercentPerStep:   100,
				StepIntervalDays: 0,
				TotalSteps:       1,
			},
			wantErr: false,
		},
		{
			name: "valid weekly schedule",
			sched: RampSchedule{
				Type:             "weekly",
				PercentPerStep:   25,
				StepIntervalDays: 7,
				TotalSteps:       4,
			},
			wantErr: false,
		},
		{
			name: "valid monthly schedule",
			sched: RampSchedule{
				Type:             "monthly",
				PercentPerStep:   10,
				StepIntervalDays: 30,
				TotalSteps:       10,
			},
			wantErr: false,
		},
		{
			name: "valid custom schedule",
			sched: RampSchedule{
				Type:             "custom",
				PercentPerStep:   50,
				StepIntervalDays: 14,
				TotalSteps:       2,
			},
			wantErr: false,
		},
		{
			name: "invalid schedule type",
			sched: RampSchedule{
				Type: "invalid",
			},
			wantErr: true,
			errMsg:  "invalid ramp schedule type",
		},
		{
			name: "percent per step too low",
			sched: RampSchedule{
				PercentPerStep: -10,
			},
			wantErr: true,
			errMsg:  "percent per step must be between 0 and 100",
		},
		{
			name: "percent per step too high",
			sched: RampSchedule{
				PercentPerStep: 150,
			},
			wantErr: true,
			errMsg:  "percent per step must be between 0 and 100",
		},
		{
			name: "step interval too low",
			sched: RampSchedule{
				StepIntervalDays: -1,
			},
			wantErr: true,
			errMsg:  "step interval must be between 0 and 365 days",
		},
		{
			name: "step interval too high",
			sched: RampSchedule{
				StepIntervalDays: 400,
			},
			wantErr: true,
			errMsg:  "step interval must be between 0 and 365 days",
		},
		{
			name: "current step negative",
			sched: RampSchedule{
				CurrentStep: -1,
			},
			wantErr: true,
			errMsg:  "current step cannot be negative",
		},
		{
			name: "total steps too low",
			sched: RampSchedule{
				TotalSteps: -1,
			},
			wantErr: true,
			errMsg:  "total steps must be between 0 and 100",
		},
		{
			name: "total steps too high",
			sched: RampSchedule{
				TotalSteps: 150,
			},
			wantErr: true,
			errMsg:  "total steps must be between 0 and 100",
		},
		{
			name: "weekly schedule with zero percent per step",
			sched: RampSchedule{
				Type:             "weekly",
				PercentPerStep:   0,
				StepIntervalDays: 7,
				TotalSteps:       4,
			},
			wantErr: true,
			errMsg:  "percent per step must be between",
		},
		{
			name: "monthly schedule with zero percent per step",
			sched: RampSchedule{
				Type:             "monthly",
				PercentPerStep:   0,
				StepIntervalDays: 30,
				TotalSteps:       10,
			},
			wantErr: true,
			errMsg:  "percent per step must be between",
		},
		{
			name: "custom schedule with negative percent per step",
			sched: RampSchedule{
				Type:             "custom",
				PercentPerStep:   -5,
				StepIntervalDays: 14,
				TotalSteps:       5,
			},
			wantErr: true,
			errMsg:  "percent per step must be between",
		},
		{
			name: "custom schedule with percent over 100",
			sched: RampSchedule{
				Type:             "custom",
				PercentPerStep:   150,
				StepIntervalDays: 14,
				TotalSteps:       5,
			},
			wantErr: true,
			errMsg:  "percent per step must be between",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.sched.Validate()
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestPurchasePlan_Validate(t *testing.T) {
	tests := []struct {
		name    string
		plan    PurchasePlan
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid plan",
			plan: PurchasePlan{
				Name:    "Test Plan",
				Enabled: true,
				RampSchedule: RampSchedule{
					Type: "immediate",
				},
				NotificationDaysBefore: 7,
				Services: map[string]ServiceConfig{
					"aws/rds": {Provider: "aws", Service: "rds"},
				},
			},
			wantErr: false,
		},
		{
			name:    "missing name",
			plan:    PurchasePlan{},
			wantErr: true,
			errMsg:  "plan name is required",
		},
		{
			name: "name too long",
			plan: PurchasePlan{
				Name: "This is a very long plan name that exceeds the maximum allowed length of one hundred characters and should fail validation",
			},
			wantErr: true,
			errMsg:  "plan name is too long",
		},
		{
			name: "notification days too low",
			plan: PurchasePlan{
				Name:                   "Test Plan",
				NotificationDaysBefore: -1,
			},
			wantErr: true,
			errMsg:  "notification days must be between 0 and 30",
		},
		{
			name: "notification days too high",
			plan: PurchasePlan{
				Name:                   "Test Plan",
				NotificationDaysBefore: 60,
			},
			wantErr: true,
			errMsg:  "notification days must be between 0 and 30",
		},
		{
			name: "invalid ramp schedule",
			plan: PurchasePlan{
				Name: "Test Plan",
				RampSchedule: RampSchedule{
					Type: "invalid",
				},
			},
			wantErr: true,
			errMsg:  "invalid ramp schedule",
		},
		{
			name: "invalid service config",
			plan: PurchasePlan{
				Name: "Test Plan",
				Services: map[string]ServiceConfig{
					"invalid": {
						Provider: "invalid",
						Service:  "test",
					},
				},
			},
			wantErr: true,
			errMsg:  "invalid service config",
		},
		{
			name: "valid plan with services",
			plan: PurchasePlan{
				Name: "Full Plan",
				Services: map[string]ServiceConfig{
					"aws/rds": {
						Provider: "aws",
						Service:  "rds",
						Enabled:  true,
						Term:     3,
						Coverage: 80,
					},
					"azure/vm": {
						Provider: "azure",
						Service:  "vm",
						Enabled:  true,
						Term:     1,
						Coverage: 50,
					},
				},
			},
			wantErr: false,
		},
		{
			name: "enabled plan with no services",
			plan: PurchasePlan{
				Name:     "Enabled Empty Plan",
				Enabled:  true,
				Services: map[string]ServiceConfig{},
			},
			wantErr: true,
			errMsg:  "plan must have at least one service",
		},
		{
			name: "disabled plan with no services is valid",
			plan: PurchasePlan{
				Name:     "Draft Plan",
				Enabled:  false,
				Services: map[string]ServiceConfig{},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.plan.Validate()
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestIsValidProvider(t *testing.T) {
	assert.True(t, isValidProvider("aws"))
	assert.True(t, isValidProvider("azure"))
	assert.True(t, isValidProvider("gcp"))
	assert.False(t, isValidProvider("invalid"))
	assert.False(t, isValidProvider(""))
}

func TestIsValidPaymentOption(t *testing.T) {
	// AWS tokens
	assert.True(t, isValidPaymentOption("no-upfront"))
	assert.True(t, isValidPaymentOption("partial-upfront"))
	assert.True(t, isValidPaymentOption("all-upfront"))
	// Azure/GCP tokens (union set)
	assert.True(t, isValidPaymentOption("upfront"))
	assert.True(t, isValidPaymentOption("monthly"))
	// Unknown tokens rejected
	assert.False(t, isValidPaymentOption("invalid"))
	assert.False(t, isValidPaymentOption(""))
}

func TestValidPaymentOptionsUnionDeterministic(t *testing.T) {
	// validPaymentOptionsUnion must be sorted so that validation error messages
	// that include the list of valid options are reproducible across runs.
	// Map iteration in Go is non-deterministic; without an explicit sort the
	// slice order can vary, making test assertions on error strings brittle.
	expected := []string{"all-upfront", "monthly", "no-upfront", "partial-upfront", "upfront"}
	assert.Equal(t, expected, validPaymentOptionsUnion,
		"validPaymentOptionsUnion must be in sorted order for deterministic error messages")

	// Double-check: building the union a second time (simulating another init
	// call) must produce the same sorted result. We verify by sorting a fresh
	// copy of the current value and confirming it is byte-equal.
	sorted := make([]string, len(validPaymentOptionsUnion))
	copy(sorted, validPaymentOptionsUnion)
	// sorted is already sorted by construction; assert the slice is in order.
	for i := 1; i < len(sorted); i++ {
		assert.LessOrEqual(t, sorted[i-1], sorted[i],
			"validPaymentOptionsUnion[%d] %q must be <= validPaymentOptionsUnion[%d] %q",
			i-1, sorted[i-1], i, sorted[i])
	}
}

func TestNormalizePaymentOption(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		raw      string
		want     string
		ok       bool
	}{
		// AWS: passthrough for every canonical token.
		{"aws no-upfront passthrough", "aws", "no-upfront", "no-upfront", true},
		{"aws partial-upfront passthrough", "aws", "partial-upfront", "partial-upfront", true},
		{"aws all-upfront passthrough", "aws", "all-upfront", "all-upfront", true},
		// AWS: Azure/GCP-style tokens are left as-is and flagged for the
		// next validator boundary to surface.
		{"aws upfront left as-is", "aws", "upfront", "upfront", false},
		{"aws monthly left as-is", "aws", "monthly", "monthly", false},

		// Azure: canonical passthrough.
		{"azure upfront passthrough", "azure", "upfront", "upfront", true},
		{"azure monthly passthrough", "azure", "monthly", "monthly", true},
		// Azure: AWS-style aliases coerced to canonical.
		{"azure all-upfront → upfront", "azure", "all-upfront", "upfront", true},
		{"azure no-upfront → monthly", "azure", "no-upfront", "monthly", true},
		{"azure partial-upfront → upfront (nearest)", "azure", "partial-upfront", "upfront", true},

		// GCP: canonical passthrough (monthly-only — every non-monthly token
		// collapses to monthly because GCP CUDs only model one billing plan).
		{"gcp monthly passthrough", "gcp", "monthly", "monthly", true},
		{"gcp upfront → monthly (gcp is monthly-only)", "gcp", "upfront", "monthly", true},
		// GCP: AWS-style aliases coerced to the one canonical token.
		{"gcp all-upfront → monthly", "gcp", "all-upfront", "monthly", true},
		{"gcp no-upfront → monthly", "gcp", "no-upfront", "monthly", true},
		{"gcp partial-upfront → monthly", "gcp", "partial-upfront", "monthly", true},

		// Empty raw: passthrough on any known provider.
		{"empty raw on aws", "aws", "", "", true},
		{"empty raw on azure", "azure", "", "", true},
		{"empty raw on gcp", "gcp", "", "", true},

		// Unknown provider: ok=false, no canonicalization.
		{"unknown provider", "ibm", "all-upfront", "", false},
		{"empty provider", "", "monthly", "", false},

		// Garbage tokens on known providers: left as-is, ok=false.
		{"azure garbage", "azure", "ohai", "ohai", false},
		{"gcp garbage", "gcp", "ohai", "ohai", false},
		{"aws garbage", "aws", "ohai", "ohai", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := NormalizePaymentOption(tt.provider, tt.raw)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.ok, ok)
		})
	}
}

func TestIsValidRampScheduleType(t *testing.T) {
	assert.True(t, isValidRampScheduleType("immediate"))
	assert.True(t, isValidRampScheduleType("weekly"))
	assert.True(t, isValidRampScheduleType("monthly"))
	assert.True(t, isValidRampScheduleType("custom"))
	assert.False(t, isValidRampScheduleType("invalid"))
	assert.False(t, isValidRampScheduleType(""))
}

// Helper function for creating string pointers in tests.
func stringPtr(s string) *string {
	return &s
}
