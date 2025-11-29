package azure

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

func TestNewComputeClient(t *testing.T) {
	client := NewComputeClient(nil, "test-subscription", "eastus")

	require.NotNil(t, client)
	assert.Equal(t, common.ServiceCompute, client.GetServiceType())
	assert.Equal(t, "eastus", client.GetRegion())
}

func TestNewDatabaseClient(t *testing.T) {
	client := NewDatabaseClient(nil, "test-subscription", "westeurope")

	require.NotNil(t, client)
	assert.Equal(t, common.ServiceRelationalDB, client.GetServiceType())
	assert.Equal(t, "westeurope", client.GetRegion())
}

func TestNewCacheClient(t *testing.T) {
	client := NewCacheClient(nil, "test-subscription", "westus2")

	require.NotNil(t, client)
	assert.Equal(t, common.ServiceCache, client.GetServiceType())
	assert.Equal(t, "westus2", client.GetRegion())
}

func TestNewRecommendationsClient(t *testing.T) {
	client := NewRecommendationsClient(nil, "test-subscription")

	require.NotNil(t, client)
	adapter, ok := client.(*RecommendationsClientAdapter)
	require.True(t, ok)
	assert.Equal(t, "test-subscription", adapter.subscriptionID)
}
