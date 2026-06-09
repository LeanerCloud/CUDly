package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDerivePlanProviders(t *testing.T) {
	plan := &PurchasePlan{
		Services: map[string]ServiceConfig{
			"aws/ec2":       {},
			"aws/rds":       {},
			"azure/compute": {},
			"gcp:compute":   {},
			"malformed":     {},
		},
	}

	assert.Equal(t, []string{"aws", "azure"}, DerivePlanProviders(plan))
	assert.Nil(t, DerivePlanProviders(nil))
}
