// Package aws provides service client implementations
package aws

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	sptypes "github.com/aws/aws-sdk-go-v2/service/savingsplans/types"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/provider"

	"github.com/LeanerCloud/CUDly/providers/aws/recommendations"
	"github.com/LeanerCloud/CUDly/providers/aws/services/ec2"
	"github.com/LeanerCloud/CUDly/providers/aws/services/elasticache"
	"github.com/LeanerCloud/CUDly/providers/aws/services/memorydb"
	"github.com/LeanerCloud/CUDly/providers/aws/services/opensearch"
	"github.com/LeanerCloud/CUDly/providers/aws/services/rds"
	"github.com/LeanerCloud/CUDly/providers/aws/services/redshift"
	"github.com/LeanerCloud/CUDly/providers/aws/services/savingsplans"
)

// NewEC2Client creates a new EC2 service client
func NewEC2Client(cfg aws.Config) provider.ServiceClient {
	return ec2.NewClient(cfg)
}

// NewRDSClient creates a new RDS service client
func NewRDSClient(cfg aws.Config) provider.ServiceClient {
	return rds.NewClient(cfg)
}

// NewElastiCacheClient creates a new ElastiCache service client
func NewElastiCacheClient(cfg aws.Config) provider.ServiceClient {
	return elasticache.NewClient(cfg)
}

// NewOpenSearchClient creates a new OpenSearch service client
func NewOpenSearchClient(cfg aws.Config) provider.ServiceClient {
	return opensearch.NewClient(cfg)
}

// NewRedshiftClient creates a new Redshift service client
func NewRedshiftClient(cfg aws.Config) provider.ServiceClient {
	return redshift.NewClient(cfg)
}

// NewMemoryDBClient creates a new MemoryDB service client
func NewMemoryDBClient(cfg aws.Config) provider.ServiceClient {
	return memorydb.NewClient(cfg)
}

// NewSavingsPlansClient creates a Savings Plans service client scoped to one
// AWS plan type. The four per-plan-type slugs (Compute, EC2Instance,
// SageMaker, Database) each get their own client instance via the AWS
// provider's GetServiceClient dispatch — see provider.go.
func NewSavingsPlansClient(cfg aws.Config, planType sptypes.SavingsPlanType) provider.ServiceClient {
	return savingsplans.NewClient(cfg, planType)
}

// RecommendationsClientAdapter adapts the recommendations client to the provider interface
type RecommendationsClientAdapter struct {
	client *recommendations.Client
}

// NewRecommendationsClient creates a new recommendations client
func NewRecommendationsClient(cfg aws.Config) provider.RecommendationsClient {
	return &RecommendationsClientAdapter{
		client: recommendations.NewClient(cfg),
	}
}

// GetRecommendations gets recommendations with filtering
func (r *RecommendationsClientAdapter) GetRecommendations(ctx context.Context, params common.RecommendationParams) ([]common.Recommendation, error) {
	recs, err := r.client.GetRecommendations(ctx, params)
	if err != nil {
		return nil, err
	}

	recs = applyRecommendationFilters(recs, params)
	return recs, nil
}

// applyRecommendationFilters applies account and region filters to recommendations
func applyRecommendationFilters(recs []common.Recommendation, params common.RecommendationParams) []common.Recommendation {
	if len(params.AccountFilter) > 0 {
		recs = filterByAccounts(recs, params.AccountFilter)
	}

	if len(params.IncludeRegions) > 0 {
		recs = filterByIncludedRegions(recs, params.IncludeRegions)
	}

	if len(params.ExcludeRegions) > 0 {
		recs = filterByExcludedRegions(recs, params.ExcludeRegions)
	}

	return recs
}

// filterByAccounts filters recommendations by account IDs
func filterByAccounts(recs []common.Recommendation, accounts []string) []common.Recommendation {
	accountMap := make(map[string]bool)
	for _, acc := range accounts {
		accountMap[acc] = true
	}

	filtered := make([]common.Recommendation, 0, len(recs))
	for _, rec := range recs {
		if accountMap[rec.Account] {
			filtered = append(filtered, rec)
		}
	}

	return filtered
}

// filterByIncludedRegions filters recommendations to only included regions
func filterByIncludedRegions(recs []common.Recommendation, regions []string) []common.Recommendation {
	regionMap := make(map[string]bool)
	for _, region := range regions {
		regionMap[region] = true
	}

	filtered := make([]common.Recommendation, 0, len(recs))
	for _, rec := range recs {
		if regionMap[rec.Region] {
			filtered = append(filtered, rec)
		}
	}

	return filtered
}

// filterByExcludedRegions filters out recommendations from excluded regions
func filterByExcludedRegions(recs []common.Recommendation, regions []string) []common.Recommendation {
	regionMap := make(map[string]bool)
	for _, region := range regions {
		regionMap[region] = true
	}

	filtered := make([]common.Recommendation, 0, len(recs))
	for _, rec := range recs {
		if !regionMap[rec.Region] {
			filtered = append(filtered, rec)
		}
	}

	return filtered
}

// GetRecommendationsForService gets recommendations for a specific service
func (r *RecommendationsClientAdapter) GetRecommendationsForService(ctx context.Context, service common.ServiceType) ([]common.Recommendation, error) {
	return r.client.GetRecommendationsForService(ctx, service)
}

// GetAllRecommendations gets recommendations for all supported services
func (r *RecommendationsClientAdapter) GetAllRecommendations(ctx context.Context) ([]common.Recommendation, error) {
	return r.client.GetAllRecommendations(ctx)
}

// GetRIUtilization gets per-RI utilization from Cost Explorer.
func (r *RecommendationsClientAdapter) GetRIUtilization(ctx context.Context, lookbackDays int) ([]recommendations.RIUtilization, error) {
	return r.client.GetRIUtilization(ctx, lookbackDays)
}

// NewRecommendationsClientDirect creates a new recommendations client returning the concrete type
// (needed for GetRIUtilization which is not part of the generic provider interface).
func NewRecommendationsClientDirect(cfg aws.Config) *RecommendationsClientAdapter {
	return &RecommendationsClientAdapter{
		client: recommendations.NewClient(cfg),
	}
}

// NewEC2ClientDirect creates a new EC2 client returning the concrete type
// (needed for ListConvertibleReservedInstances which is not part of the generic provider interface).
func NewEC2ClientDirect(cfg aws.Config) *ec2.Client {
	return ec2.NewClient(cfg)
}
