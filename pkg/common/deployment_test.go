package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeDeploymentName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"Cost Explorer spelling", "Multi-AZ", "multiaz"},
		{"parser spelling", "multi-az", "multiaz"},
		{"single-az variants agree", "Single-AZ", "singleaz"},
		{"underscores", "multi_az", "multiaz"},
		{"spaces", "Multi AZ", "multiaz"},
		{"parentheses", "Multi-AZ (readable standby)", "multiazreadablestandby"},
		// Empty stays empty rather than becoming a synthetic bucket, so a
		// non-RDS commitment and a non-RDS recommendation land on one key.
		{"empty stays empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, NormalizeDeploymentName(tt.input))
		})
	}
}

// The two spellings the RDS path actually produces must collapse together;
// that agreement is what the duplicate-identity key depends on.
func TestNormalizeDeploymentName_SpellingsCollapse(t *testing.T) {
	t.Parallel()
	assert.Equal(t, NormalizeDeploymentName("Multi-AZ"), NormalizeDeploymentName("multi-az"))
	assert.NotEqual(t, NormalizeDeploymentName("Multi-AZ"), NormalizeDeploymentName("Single-AZ"))
}

func TestDeploymentFromDetails(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "multi-az", DeploymentFromDetails(&DatabaseDetails{AZConfig: "multi-az"}))
	assert.Empty(t, DeploymentFromDetails(nil), "untyped nil details")
	assert.Empty(t, DeploymentFromDetails(&CacheDetails{Engine: "redis"}), "non-database details")
	assert.Empty(t, DeploymentFromDetails(&DatabaseDetails{}), "database details with no AZConfig")
}

// A typed nil stored in the interface satisfies the type assertion, so
// without the type-specific nil guard this panics rather than returning "".
func TestDeploymentFromDetails_TypedNilDoesNotPanic(t *testing.T) {
	t.Parallel()
	var typedNil *DatabaseDetails
	assert.NotPanics(t, func() {
		assert.Empty(t, DeploymentFromDetails(typedNil))
	})
}
