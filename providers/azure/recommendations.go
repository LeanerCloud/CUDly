// Package azure provides Azure recommendations client
package azure

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/advisor/armadvisor"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/logging"
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
	if err != nil {
		logging.Errorf("Failed to get Azure Advisor recommendations: %v", err)
	} else {
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
			logging.Warnf("Azure Advisor pagination error (partial results may be returned): %v", err)
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

	service := extractServiceType(advisorRec)
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

	if advisorRec.ID != nil {
		if region := extractRegionFromResourceID(*advisorRec.ID); region != "" {
			rec.Region = region
		}
	}

	populateFromExtendedProperties(rec, advisorRec.Properties.ExtendedProperties)
	return rec
}

// populateFromExtendedProperties fills savings, SKU, term, and count from
// the Advisor recommendation's ExtendedProperties map.
func populateFromExtendedProperties(rec *common.Recommendation, ext map[string]*string) {
	if ext == nil {
		return
	}
	rec.EstimatedSavings = extFloat(ext, "annualSavingsAmount") / 12
	rec.ResourceType = extString(ext, "sku", rec.ResourceType)
	rec.Term = extString(ext, "term", rec.Term)
	rec.Count = extInt(ext, "qty", rec.Count)
}

func extString(m map[string]*string, key, fallback string) string {
	if v, ok := m[key]; ok && v != nil && *v != "" {
		return *v
	}
	return fallback
}

func extFloat(m map[string]*string, key string) float64 {
	if v, ok := m[key]; ok && v != nil {
		if f, err := strconv.ParseFloat(*v, 64); err == nil {
			return f
		}
	}
	return 0
}

func extInt(m map[string]*string, key string, fallback int) int {
	if v, ok := m[key]; ok && v != nil {
		if n, err := strconv.Atoi(*v); err == nil {
			return n
		}
	}
	return fallback
}

// extractServiceType determines the service type from an Advisor recommendation.
// First checks ImpactedField (resource-scoped), then falls back to
// ExtendedProperties (subscription-scoped reservations/savings plans).
func extractServiceType(rec *armadvisor.ResourceRecommendationBase) string {
	if rec.Properties == nil {
		return ""
	}
	if svc := serviceFromImpactedField(rec.Properties.ImpactedField); svc != "" {
		return svc
	}
	return serviceFromExtendedProperties(rec.Properties.ExtendedProperties)
}

// serviceFromImpactedField maps Azure resource namespace to service type.
func serviceFromImpactedField(field *string) string {
	if field == nil {
		return ""
	}
	f := *field
	switch {
	case contains(f, "Microsoft.Compute"):
		return string(common.ServiceCompute)
	case contains(f, "Microsoft.Sql"):
		return string(common.ServiceRelationalDB)
	case contains(f, "Microsoft.Cache"):
		return string(common.ServiceCache)
	case contains(f, "Microsoft.DBforMySQL"), contains(f, "Microsoft.DBforPostgreSQL"):
		return string(common.ServiceRelationalDB)
	}
	return ""
}

// serviceFromExtendedProperties resolves service type for subscription-scoped
// recommendations where ImpactedField is "Microsoft.Subscriptions/subscriptions".
func serviceFromExtendedProperties(ext map[string]*string) string {
	if ext == nil {
		return ""
	}
	if rrt, ok := ext["reservedResourceType"]; ok && rrt != nil {
		switch strings.ToLower(*rrt) {
		case "virtualmachines":
			return string(common.ServiceCompute)
		case "sqldatabases":
			return string(common.ServiceRelationalDB)
		case "rediscache":
			return string(common.ServiceCache)
		}
	}
	if subcat, ok := ext["recommendationSubCategory"]; ok && subcat != nil {
		if strings.EqualFold(*subcat, "SavingsPlan") {
			return string(common.ServiceCompute)
		}
	}
	return ""
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
