package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func boolPtr(b bool) *bool          { return &b }
func intPtr(i int) *int             { return &i }
func strPtr(s string) *string       { return &s }
func float64Ptr(f float64) *float64 { return &f }

func TestResolveServiceConfig_NilOverride(t *testing.T) {
	global := &ServiceConfig{
		Provider: "aws", Service: "ec2",
		Enabled: true, Term: 3, Payment: "all_upfront", Coverage: 80.0,
	}
	result := ResolveServiceConfig("aws", "ec2", global, nil)
	assert.Same(t, global, result, "nil override should return the global pointer unchanged")
}

func TestResolveServiceConfig_AllFieldsOverridden(t *testing.T) {
	global := &ServiceConfig{
		Provider: "aws", Service: "ec2",
		Enabled: true, Term: 3, Payment: "all_upfront", Coverage: 80.0,
		RampSchedule:   "gradual",
		IncludeEngines: []string{"m5"},
		ExcludeEngines: []string{"t3"},
		IncludeRegions: []string{"us-east-1"},
		ExcludeRegions: []string{"ap-south-1"},
		IncludeTypes:   []string{"m5.large"},
		ExcludeTypes:   []string{"t3.micro"},
	}
	override := &AccountServiceOverride{
		Enabled:        boolPtr(false),
		Term:           intPtr(1),
		Payment:        strPtr("no_upfront"),
		Coverage:       float64Ptr(50.0),
		RampSchedule:   strPtr("immediate"),
		IncludeEngines: []string{"c5"},
		ExcludeEngines: []string{"r5"},
		IncludeRegions: []string{"eu-west-1"},
		ExcludeRegions: []string{"sa-east-1"},
		IncludeTypes:   []string{"c5.xlarge"},
		ExcludeTypes:   []string{"r5.large"},
	}

	result := ResolveServiceConfig("aws", "ec2", global, override)

	assert.False(t, result.Enabled)
	assert.Equal(t, 1, result.Term)
	assert.Equal(t, "no_upfront", result.Payment)
	assert.Equal(t, 50.0, result.Coverage)
	assert.Equal(t, "immediate", result.RampSchedule)
	assert.Equal(t, []string{"c5"}, result.IncludeEngines)
	assert.Equal(t, []string{"r5"}, result.ExcludeEngines)
	assert.Equal(t, []string{"eu-west-1"}, result.IncludeRegions)
	assert.Equal(t, []string{"sa-east-1"}, result.ExcludeRegions)
	assert.Equal(t, []string{"c5.xlarge"}, result.IncludeTypes)
	assert.Equal(t, []string{"r5.large"}, result.ExcludeTypes)

	// provider/service preserved from global
	assert.Equal(t, "aws", result.Provider)
	assert.Equal(t, "ec2", result.Service)
}

func TestResolveServiceConfig_SparseOverride(t *testing.T) {
	global := &ServiceConfig{
		Enabled: true, Term: 3, Payment: "all_upfront", Coverage: 80.0,
		IncludeRegions: []string{"us-east-1"},
	}
	override := &AccountServiceOverride{
		Term: intPtr(1), // only override Term
	}

	result := ResolveServiceConfig("aws", "rds", global, override)

	assert.Equal(t, 1, result.Term)
	assert.True(t, result.Enabled)                                // inherited
	assert.Equal(t, "all_upfront", result.Payment)                // inherited
	assert.Equal(t, 80.0, result.Coverage)                        // inherited
	assert.Equal(t, []string{"us-east-1"}, result.IncludeRegions) // inherited
}

func TestResolveServiceConfig_EmptySliceDoesNotOverride(t *testing.T) {
	global := &ServiceConfig{IncludeRegions: []string{"us-east-1"}}
	override := &AccountServiceOverride{
		IncludeRegions: []string{}, // empty — should NOT override
	}

	result := ResolveServiceConfig("aws", "rds", global, override)
	assert.Equal(t, []string{"us-east-1"}, result.IncludeRegions)
}

func TestResolveServiceConfig_DoesNotMutateGlobal(t *testing.T) {
	global := &ServiceConfig{Term: 3}
	override := &AccountServiceOverride{Term: intPtr(1)}

	result := ResolveServiceConfig("aws", "ec2", global, override)
	assert.Equal(t, 1, result.Term)
	assert.Equal(t, 3, global.Term, "global must not be mutated")
}

// TestResolveServiceConfig_NilGlobalWithOverride verifies that a per-account override
// takes effect even when no global ServiceConfig row exists.
func TestResolveServiceConfig_NilGlobalWithOverride(t *testing.T) {
	override := &AccountServiceOverride{
		Enabled:  boolPtr(false),
		Coverage: float64Ptr(60.0),
	}

	result := ResolveServiceConfig("aws", "rds", nil, override)

	assert.NotNil(t, result)
	assert.Equal(t, "aws", result.Provider, "Provider taken from parameters")
	assert.Equal(t, "rds", result.Service, "Service taken from parameters")
	assert.False(t, result.Enabled, "override Enabled=false applied against synthesised baseline")
	assert.Equal(t, 60.0, result.Coverage, "override Coverage applied")
}

// TestResolveServiceConfig_BothNil verifies that nil is returned when both
// global and override are nil.
func TestResolveServiceConfig_BothNil(t *testing.T) {
	result := ResolveServiceConfig("aws", "rds", nil, nil)
	assert.Nil(t, result)
}
