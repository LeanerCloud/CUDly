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
			name:    "valid empty config",
			config:  GlobalConfig{},
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
				DefaultPayment: "invalid-payment",
			},
			wantErr: true,
			errMsg:  "invalid payment option",
		},
		{
			name: "coverage too low",
			config: GlobalConfig{
				DefaultCoverage: -1,
			},
			wantErr: true,
			errMsg:  "default coverage must be between 0 and 100",
		},
		{
			name: "coverage too high",
			config: GlobalConfig{
				DefaultCoverage: 101,
			},
			wantErr: true,
			errMsg:  "default coverage must be between 0 and 100",
		},
		{
			name: "valid no-upfront payment",
			config: GlobalConfig{
				DefaultPayment: "no-upfront",
			},
			wantErr: false,
		},
		{
			name: "valid partial-upfront payment",
			config: GlobalConfig{
				DefaultPayment: "partial-upfront",
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
	assert.True(t, isValidPaymentOption("no-upfront"))
	assert.True(t, isValidPaymentOption("partial-upfront"))
	assert.True(t, isValidPaymentOption("all-upfront"))
	assert.False(t, isValidPaymentOption("invalid"))
	assert.False(t, isValidPaymentOption(""))
}

func TestIsValidRampScheduleType(t *testing.T) {
	assert.True(t, isValidRampScheduleType("immediate"))
	assert.True(t, isValidRampScheduleType("weekly"))
	assert.True(t, isValidRampScheduleType("monthly"))
	assert.True(t, isValidRampScheduleType("custom"))
	assert.False(t, isValidRampScheduleType("invalid"))
	assert.False(t, isValidRampScheduleType(""))
}

// Helper function for creating string pointers in tests
func stringPtr(s string) *string {
	return &s
}
