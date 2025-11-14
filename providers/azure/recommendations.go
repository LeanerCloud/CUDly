// Package azure provides Azure recommendations client
package azure

import (
	"context"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/advisor/armadvisor"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/providers/azure/services/cache"
	"github.com/LeanerCloud/CUDly/providers/azure/services/compute"
	"github.com/LeanerCloud/CUDly/providers/azure/services/database"
)

// RecommendationsClientAdapter aggregates Azure reservation recommendations across all services
type RecommendationsClientAdapter struct {
	cred           azcore.TokenCredential
	subscriptionID string
}

// GetRecommendations retrieves all Azure reservation recommendations across all services and regions
func (r *RecommendationsClientAdapter) GetRecommendations(ctx context.Context, params common.RecommendationParams) ([]common.Recommendation, error) {
	allRecommendations := make([]common.Recommendation, 0)

	// Get list of regions to check
	regions, err := r.getRegions(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get regions: %w", err)
	}

	// Collect recommendations from each service type across all regions
	for _, region := range regions {
		// Compute (VM) recommendations
		if shouldIncludeService(params, common.ServiceCompute) {
			computeClient := compute.NewClient(r.cred, r.subscriptionID, region)
			computeRecs, err := computeClient.GetRecommendations(ctx, params)
			if err == nil {
				allRecommendations = append(allRecommendations, computeRecs...)
			}
		}

		// Database (SQL) recommendations
		if shouldIncludeService(params, common.ServiceRelationalDB) {
			dbClient := database.NewClient(r.cred, r.subscriptionID, region)
			dbRecs, err := dbClient.GetRecommendations(ctx, params)
			if err == nil {
				allRecommendations = append(allRecommendations, dbRecs...)
			}
		}

		// Cache (Redis) recommendations
		if shouldIncludeService(params, common.ServiceCache) {
			cacheClient := cache.NewClient(r.cred, r.subscriptionID, region)
			cacheRecs, err := cacheClient.GetRecommendations(ctx, params)
			if err == nil {
				allRecommendations = append(allRecommendations, cacheRecs...)
			}
		}
	}

	// Get additional recommendations from Azure Advisor
	advisorRecs, err := r.getAdvisorRecommendations(ctx, params)
	if err == nil {
		allRecommendations = append(allRecommendations, advisorRecs...)
	}

	return allRecommendations, nil
}

// GetRecommendationsForService retrieves Azure reservation recommendations for a specific service
func (r *RecommendationsClientAdapter) GetRecommendationsForService(ctx context.Context, service common.ServiceType) ([]common.Recommendation, error) {
	params := common.RecommendationParams{
		Service: service,
	}
	return r.GetRecommendations(ctx, params)
}

// GetAllRecommendations retrieves all Azure reservation recommendations across all services
func (r *RecommendationsClientAdapter) GetAllRecommendations(ctx context.Context) ([]common.Recommendation, error) {
	params := common.RecommendationParams{}
	return r.GetRecommendations(ctx, params)
}

// getAdvisorRecommendations retrieves cost optimization recommendations from Azure Advisor
func (r *RecommendationsClientAdapter) getAdvisorRecommendations(ctx context.Context, params common.RecommendationParams) ([]common.Recommendation, error) {
	client, err := armadvisor.NewRecommendationsClient(r.subscriptionID, r.cred, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create advisor client: %w", err)
	}

	recommendations := make([]common.Recommendation, 0)

	// Filter for cost recommendations
	filter := "Category eq 'Cost'"
	pager := client.NewListPager(&armadvisor.RecommendationsClientListOptions{
		Filter: &filter,
	})

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			break
		}

		for _, advisorRec := range page.Value {
			if advisorRec.Properties == nil {
				continue
			}

			// Convert Azure Advisor recommendation to our common format
			rec := r.convertAdvisorRecommendation(advisorRec)
			if rec != nil && shouldIncludeService(params, rec.Service) {
				recommendations = append(recommendations, *rec)
			}
		}
	}

	return recommendations, nil
}

// convertAdvisorRecommendation converts an Azure Advisor recommendation to common format
func (r *RecommendationsClientAdapter) convertAdvisorRecommendation(advisorRec *armadvisor.ResourceRecommendationBase) *common.Recommendation {
	if advisorRec.Properties == nil {
		return nil
	}

	props := advisorRec.Properties

	// Extract service type from the resource ID or recommendation metadata
	service := r.extractServiceType(advisorRec)
	if service == "" {
		return nil
	}

	rec := &common.Recommendation{
		Provider:       common.ProviderAzure,
		Service:        common.ServiceType(service),
		Account:        r.subscriptionID,
		CommitmentType: common.CommitmentReservedInstance,
		Term:           "1yr",
		PaymentOption:  "upfront",
	}

	// Extract region from resource ID if available
	if advisorRec.ID != nil {
		region := extractRegionFromResourceID(*advisorRec.ID)
		if region != "" {
			rec.Region = region
		}
	}

	// Extract cost savings if available
	if props.ExtendedProperties != nil {
		// ExtendedProperties is map[string]*string in the Azure SDK
		if annualSavingsStr, ok := props.ExtendedProperties["annualSavingsAmount"]; ok && annualSavingsStr != nil {
			// Would need to parse the string to float64
			// For now, just note that savings info is available
		}
		if savingsCurrency, ok := props.ExtendedProperties["savingsCurrency"]; ok && savingsCurrency != nil {
			// Store currency information if needed
		}
	}

	return rec
}

// extractServiceType determines the service type from an Advisor recommendation
func (r *RecommendationsClientAdapter) extractServiceType(rec *armadvisor.ResourceRecommendationBase) string {
	if rec.Properties == nil || rec.Properties.ImpactedField == nil {
		return ""
	}

	impactedField := *rec.Properties.ImpactedField

	// Map Azure resource types to our service types
	switch {
	case contains(impactedField, "Microsoft.Compute"):
		return string(common.ServiceCompute)
	case contains(impactedField, "Microsoft.Sql"):
		return string(common.ServiceRelationalDB)
	case contains(impactedField, "Microsoft.Cache"):
		return string(common.ServiceCache)
	case contains(impactedField, "Microsoft.DBforMySQL"), contains(impactedField, "Microsoft.DBforPostgreSQL"):
		return string(common.ServiceRelationalDB)
	default:
		return ""
	}
}

// extractRegionFromResourceID extracts the region from an Azure resource ID
func extractRegionFromResourceID(resourceID string) string {
	// Azure resource IDs don't always contain region information
	// This would need to query the resource or use resource metadata
	// For now, return empty string as region will be set by service clients
	return ""
}

// getRegions retrieves available Azure regions for the subscription
func (r *RecommendationsClientAdapter) getRegions(ctx context.Context) ([]string, error) {
	// Create a temporary provider to get regions
	provider := &AzureProvider{
		cred: r.cred,
	}

	regions, err := provider.GetRegions(ctx)
	if err != nil {
		return nil, err
	}

	regionNames := make([]string, 0, len(regions))
	for _, region := range regions {
		regionNames = append(regionNames, region.ID)
	}

	return regionNames, nil
}

// shouldIncludeService checks if a service should be included based on params
func shouldIncludeService(params common.RecommendationParams, service common.ServiceType) bool {
	// If no service specified in params, include all
	if params.Service == "" {
		return true
	}

	// Check if this is the requested service
	return params.Service == service
}

// contains checks if a string contains a substring
func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}
