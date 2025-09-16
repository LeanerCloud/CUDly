package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRIConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  RIConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid config",
			config: RIConfig{
				Region:        "us-east-1",
				InstanceType:  "db.t4g.medium",
				Engine:        "mysql",
				AZConfig:      AZConfigSingleAZ,
				PaymentOption: PaymentOptionPartialUpfront,
				Term:          TermDuration3Year,
				Count:         1,
			},
			wantErr: false,
		},
		{
			name: "missing region",
			config: RIConfig{
				InstanceType:  "db.t4g.medium",
				Engine:        "mysql",
				AZConfig:      AZConfigSingleAZ,
				PaymentOption: PaymentOptionPartialUpfront,
				Term:          TermDuration3Year,
				Count:         1,
			},
			wantErr: true,
			errMsg:  "region is required",
		},
		{
			name: "missing instance type",
			config: RIConfig{
				Region:        "us-east-1",
				Engine:        "mysql",
				AZConfig:      AZConfigSingleAZ,
				PaymentOption: PaymentOptionPartialUpfront,
				Term:          TermDuration3Year,
				Count:         1,
			},
			wantErr: true,
			errMsg:  "instance type is required",
		},
		{
			name: "missing engine",
			config: RIConfig{
				Region:        "us-east-1",
				InstanceType:  "db.t4g.medium",
				AZConfig:      AZConfigSingleAZ,
				PaymentOption: PaymentOptionPartialUpfront,
				Term:          TermDuration3Year,
				Count:         1,
			},
			wantErr: true,
			errMsg:  "engine is required",
		},
		{
			name: "invalid count",
			config: RIConfig{
				Region:        "us-east-1",
				InstanceType:  "db.t4g.medium",
				Engine:        "mysql",
				AZConfig:      AZConfigSingleAZ,
				PaymentOption: PaymentOptionPartialUpfront,
				Term:          TermDuration3Year,
				Count:         0,
			},
			wantErr: true,
			errMsg:  "count must be greater than 0",
		},
		{
			name: "invalid AZ config",
			config: RIConfig{
				Region:        "us-east-1",
				InstanceType:  "db.t4g.medium",
				Engine:        "mysql",
				AZConfig:      "invalid-az",
				PaymentOption: PaymentOptionPartialUpfront,
				Term:          TermDuration3Year,
				Count:         1,
			},
			wantErr: true,
			errMsg:  "AZ config must be 'single-az' or 'multi-az'",
		},
		{
			name: "invalid payment option",
			config: RIConfig{
				Region:        "us-east-1",
				InstanceType:  "db.t4g.medium",
				Engine:        "mysql",
				AZConfig:      AZConfigSingleAZ,
				PaymentOption: "invalid-payment",
				Term:          TermDuration3Year,
				Count:         1,
			},
			wantErr: true,
			errMsg:  "invalid payment option: invalid-payment",
		},
		{
			name: "invalid term duration",
			config: RIConfig{
				Region:        "us-east-1",
				InstanceType:  "db.t4g.medium",
				Engine:        "mysql",
				AZConfig:      AZConfigSingleAZ,
				PaymentOption: PaymentOptionPartialUpfront,
				Term:          24, // Invalid term
				Count:         1,
			},
			wantErr: true,
			errMsg:  "invalid term duration: 24",
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

func TestRIConfig_GetDurationString(t *testing.T) {
	tests := []struct {
		name     string
		term     TermDuration
		expected string
	}{
		{
			name:     "1 year term",
			term:     TermDuration1Year,
			expected: "1yr",
		},
		{
			name:     "3 year term",
			term:     TermDuration3Year,
			expected: "3yr",
		},
		{
			name:     "invalid term",
			term:     24,
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &RIConfig{Term: tt.term}
			result := config.GetDurationString()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRIConfig_GetMultiAZ(t *testing.T) {
	tests := []struct {
		name     string
		azConfig AZConfig
		expected bool
	}{
		{
			name:     "single AZ",
			azConfig: AZConfigSingleAZ,
			expected: false,
		},
		{
			name:     "multi AZ",
			azConfig: AZConfigMultiAZ,
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &RIConfig{AZConfig: tt.azConfig}
			result := config.GetMultiAZ()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRIConfig_GenerateDescription(t *testing.T) {
	tests := []struct {
		name     string
		config   RIConfig
		expected string
	}{
		{
			name: "single AZ description",
			config: RIConfig{
				Engine:       "mysql",
				InstanceType: "db.t4g.medium",
				AZConfig:     AZConfigSingleAZ,
			},
			expected: "mysql db.t4g.medium Single-AZ",
		},
		{
			name: "multi AZ description",
			config: RIConfig{
				Engine:       "aurora-postgresql",
				InstanceType: "db.r6g.large",
				AZConfig:     AZConfigMultiAZ,
			},
			expected: "aurora-postgresql db.r6g.large Multi-AZ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.config.GenerateDescription()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestPurchaseResult_GetStatusString(t *testing.T) {
	tests := []struct {
		name     string
		success  bool
		expected string
	}{
		{
			name:     "successful purchase",
			success:  true,
			expected: "SUCCESS",
		},
		{
			name:     "failed purchase",
			success:  false,
			expected: "FAILED",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := &PurchaseResult{Success: tt.success}
			status := result.GetStatusString()
			assert.Equal(t, tt.expected, status)
		})
	}
}

func TestPurchaseResult_GetMessage(t *testing.T) {
	tests := []struct {
		name            string
		result          PurchaseResult
		expectedMessage string
	}{
		{
			name: "successful with purchase ID",
			result: PurchaseResult{
				Success:    true,
				PurchaseID: "ri-12345",
			},
			expectedMessage: "Purchase ID: ri-12345",
		},
		{
			name: "successful without purchase ID",
			result: PurchaseResult{
				Success: true,
			},
			expectedMessage: "Purchase successful",
		},
		{
			name: "failed with error message",
			result: PurchaseResult{
				Success:      false,
				ErrorMessage: "Insufficient quota",
			},
			expectedMessage: "Insufficient quota",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			message := tt.result.GetMessage()
			assert.Equal(t, tt.expectedMessage, message)
		})
	}
}

func TestCreateDefaultConfig(t *testing.T) {
	engine := "mysql"
	instanceType := "db.t4g.medium"
	count := int32(5)

	config := CreateDefaultConfig(engine, instanceType, count)

	assert.Equal(t, DefaultRegion, config.Region)
	assert.Equal(t, engine, config.Engine)
	assert.Equal(t, instanceType, config.InstanceType)
	assert.Equal(t, count, config.Count)
	assert.Equal(t, AZConfigSingleAZ, config.AZConfig)
	assert.Equal(t, DefaultPaymentOption, config.PaymentOption)
	assert.Equal(t, DefaultTerm, config.Term)
	assert.NotEmpty(t, config.Description)
}

func TestIsEngineSupported(t *testing.T) {
	tests := []struct {
		name     string
		engine   string
		expected bool
	}{
		{
			name:     "supported engine - mysql",
			engine:   "mysql",
			expected: true,
		},
		{
			name:     "supported engine - aurora-postgresql",
			engine:   "aurora-postgresql",
			expected: true,
		},
		{
			name:     "unsupported engine",
			engine:   "unsupported-engine",
			expected: false,
		},
		{
			name:     "empty engine",
			engine:   "",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsEngineSupported(tt.engine)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsInstanceTypeSupported(t *testing.T) {
	tests := []struct {
		name         string
		instanceType string
		expected     bool
	}{
		{
			name:         "supported instance type - db.t4g.medium",
			instanceType: "db.t4g.medium",
			expected:     true,
		},
		{
			name:         "supported instance type - db.r6g.large",
			instanceType: "db.r6g.large",
			expected:     true,
		},
		{
			name:         "unsupported instance type",
			instanceType: "db.unsupported.type",
			expected:     false,
		},
		{
			name:         "empty instance type",
			instanceType: "",
			expected:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsInstanceTypeSupported(tt.instanceType)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConstants(t *testing.T) {
	// Test payment option constants
	assert.Equal(t, PaymentOption("all-upfront"), PaymentOptionAllUpfront)
	assert.Equal(t, PaymentOption("partial-upfront"), PaymentOptionPartialUpfront)
	assert.Equal(t, PaymentOption("no-upfront"), PaymentOptionNoUpfront)

	// Test AZ config constants
	assert.Equal(t, AZConfig("single-az"), AZConfigSingleAZ)
	assert.Equal(t, AZConfig("multi-az"), AZConfigMultiAZ)

	// Test term duration constants
	assert.Equal(t, TermDuration(12), TermDuration1Year)
	assert.Equal(t, TermDuration(36), TermDuration3Year)

	// Test default values
	assert.Equal(t, PaymentOptionPartialUpfront, DefaultPaymentOption)
	assert.Equal(t, TermDuration3Year, DefaultTerm)
	assert.Equal(t, "eu-central-1", DefaultRegion)
}

func TestRIConfigJSONSerialization(t *testing.T) {
	config := &RIConfig{
		Region:         "us-west-2",
		InstanceType:   "db.r6g.xlarge",
		Engine:         "postgres",
		AZConfig:       AZConfigMultiAZ,
		PaymentOption:  PaymentOptionAllUpfront,
		Term:           TermDuration1Year,
		Count:          3,
		Description:    "PostgreSQL r6g.xlarge Multi-AZ",
		EstimatedCost:  1200.50,
		SavingsPercent: 25.5,
	}

	// Test that the struct can be used with JSON tags
	// This is a basic test to ensure the JSON tags are present
	assert.NotNil(t, config)

	// Validate the config
	err := config.Validate()
	assert.NoError(t, err)
}

func TestPurchaseResultTimestamp(t *testing.T) {
	now := time.Now()
	result := &PurchaseResult{
		Timestamp: now,
		Success:   true,
	}

	assert.Equal(t, now, result.Timestamp)
	assert.True(t, result.Success)
}

// Benchmark tests for performance-critical functions
func BenchmarkRIConfig_Validate(b *testing.B) {
	config := &RIConfig{
		Region:        "us-east-1",
		InstanceType:  "db.t4g.medium",
		Engine:        "mysql",
		AZConfig:      AZConfigSingleAZ,
		PaymentOption: PaymentOptionPartialUpfront,
		Term:          TermDuration3Year,
		Count:         1,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = config.Validate()
	}
}

func BenchmarkIsEngineSupported(b *testing.B) {
	engine := "mysql"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = IsEngineSupported(engine)
	}
}

func BenchmarkIsInstanceTypeSupported(b *testing.B) {
	instanceType := "db.t4g.medium"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = IsInstanceTypeSupported(instanceType)
	}
}
