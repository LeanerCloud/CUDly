// Package aws provides service client implementations
package aws

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"

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

// NewSavingsPlansClient creates a new Savings Plans service client
func NewSavingsPlansClient(cfg aws.Config) provider.ServiceClient {
	return savingsplans.NewClient(cfg)
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

	// Apply filters
	if len(params.AccountFilter) > 0 {
		filtered := make([]common.Recommendation, 0)
		accountMap := make(map[string]bool)
		for _, acc := range params.AccountFilter {
			accountMap[acc] = true
		}
		for _, rec := range recs {
			if accountMap[rec.Account] {
				filtered = append(filtered, rec)
			}
		}
		recs = filtered
	}

	if len(params.IncludeRegions) > 0 {
		filtered := make([]common.Recommendation, 0)
		regionMap := make(map[string]bool)
		for _, region := range params.IncludeRegions {
			regionMap[region] = true
		}
		for _, rec := range recs {
			if regionMap[rec.Region] {
				filtered = append(filtered, rec)
			}
		}
		recs = filtered
	}

	if len(params.ExcludeRegions) > 0 {
		regionMap := make(map[string]bool)
		for _, region := range params.ExcludeRegions {
			regionMap[region] = true
		}
		filtered := make([]common.Recommendation, 0)
		for _, rec := range recs {
			if !regionMap[rec.Region] {
				filtered = append(filtered, rec)
			}
		}
		recs = filtered
	}

	return recs, nil
}

// GetRecommendationsForService gets recommendations for a specific service
func (r *RecommendationsClientAdapter) GetRecommendationsForService(ctx context.Context, service common.ServiceType) ([]common.Recommendation, error) {
	return r.client.GetRecommendationsForService(ctx, service)
}

// GetAllRecommendations gets recommendations for all supported services
func (r *RecommendationsClientAdapter) GetAllRecommendations(ctx context.Context) ([]common.Recommendation, error) {
	return r.client.GetAllRecommendations(ctx)
}
