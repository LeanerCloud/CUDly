// Package azure provides service client factory functions
package azure

import (
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/LeanerCloud/CUDly/pkg/provider"
	"github.com/LeanerCloud/CUDly/providers/azure/services/cache"
	"github.com/LeanerCloud/CUDly/providers/azure/services/compute"
	"github.com/LeanerCloud/CUDly/providers/azure/services/cosmosdb"
	"github.com/LeanerCloud/CUDly/providers/azure/services/database"
)

// NewComputeClient creates a new Azure Compute (VM) client
func NewComputeClient(cred azcore.TokenCredential, subscriptionID, region string) provider.ServiceClient {
	return compute.NewClient(cred, subscriptionID, region)
}

// NewDatabaseClient creates a new Azure SQL Database client
func NewDatabaseClient(cred azcore.TokenCredential, subscriptionID, region string) provider.ServiceClient {
	return database.NewClient(cred, subscriptionID, region)
}

// NewCacheClient creates a new Azure Cache for Redis client
func NewCacheClient(cred azcore.TokenCredential, subscriptionID, region string) provider.ServiceClient {
	return cache.NewClient(cred, subscriptionID, region)
}

// NewCosmosDBClient creates a new Azure Cosmos DB client
func NewCosmosDBClient(cred azcore.TokenCredential, subscriptionID, region string) provider.ServiceClient {
	return cosmosdb.NewClient(cred, subscriptionID, region)
}

// NewRecommendationsClient creates a new Azure recommendations client
func NewRecommendationsClient(cred azcore.TokenCredential, subscriptionID string) provider.RecommendationsClient {
	return &RecommendationsClientAdapter{
		cred:           cred,
		subscriptionID: subscriptionID,
	}
}
