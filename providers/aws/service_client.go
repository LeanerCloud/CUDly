// Package aws provides service client implementations
package aws

import (
	"context"
	"fmt"

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
		client: recommendations.NewClient(&cfg),
	}
}

// GetRecommendations gets recommendations with filtering
func (r *RecommendationsClientAdapter) GetRecommendations(ctx context.Context, params *common.RecommendationParams) ([]common.Recommendation, error) {
	if params == nil {
		return nil, fmt.Errorf("params cannot be nil")
	}
	recs, err := r.client.GetRecommendations(ctx, params)
	if err != nil {
		return nil, err
	}

	recs = applyRecommendationFilters(recs, *params)
	return recs, nil
}

// applyRecommendationFilters applies account and region filters to recommendations.
//
// GetReservationPurchaseRecommendation and GetSavingsPlansPurchaseRecommendation
// are both account-level Cost Explorer APIs: neither request carries a region
// parameter, so AWS returns recommendations across every region the account
// has usage in regardless of what params.Region asks for (issue #1506) --
// this is the only region enforcement a single-region search gets. Region is
// folded into the include-region set here (rather than requiring the caller
// to pass include_regions) so cudly_search_recommendations' region="us-east-1"
// argument -- documented as filtering the search -- actually does.
func applyRecommendationFilters(recs []common.Recommendation, params common.RecommendationParams) []common.Recommendation {
	if len(params.AccountFilter) > 0 {
		recs = filterByAccounts(recs, params.AccountFilter)
	}

	includeRegions := params.IncludeRegions
	if params.Region != "" {
		includeRegions = append(append([]string{}, includeRegions...), params.Region)
	}
	if len(includeRegions) > 0 {
		recs = filterByIncludedRegions(recs, includeRegions)
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

// EffectiveRegion returns the region to use for region-filter matching.
// Savings Plans recommendations never populate the top-level rec.Region
// (GetSavingsPlansPurchaseRecommendation is account-level and carries no
// region parameter -- see applyRecommendationFilters); EC2Instance Savings
// Plans instead carry their recommended region in Details.Region
// (SavingsPlanDetails, populated by parser_sp.go's extractEC2SPFields).
// Falling back to it here means a region/include_regions filter matches
// EC2Instance SP recs on their real region instead of dropping them via an
// always-empty top-level Region. Compute/SageMaker/Database SP recs are
// genuinely region-agnostic (Details.Region stays "" for them), so this
// still returns "" for those, and IsRegionAgnostic is what decides that such
// a rec is exempt from region filtering (see #1495).
func EffectiveRegion(rec common.Recommendation) string {
	if rec.Region != "" {
		return rec.Region
	}
	if sp, ok := rec.Details.(*common.SavingsPlanDetails); ok && sp != nil {
		return sp.Region
	}
	return ""
}

// IsRegionAgnostic reports whether rec legitimately belongs to no single
// region and must therefore be exempt from both region filters: an
// account-level Savings Plan (Compute/SageMaker/Database), which
// GetSavingsPlansPurchaseRecommendation returns without any region because
// the plan applies account-wide.
//
// The CommitmentSavingsPlan check is load-bearing, not decorative. An empty
// effective region is NOT by itself proof that a rec is region-agnostic:
// every reservation parser in parser_services.go writes rec.Region only
// under `if <svc>Details.Region != nil`, so an EC2/RDS/ElastiCache/
// OpenSearch/Redshift/MemoryDB rec whose Cost Explorer payload omitted the
// region field lands here with Region == "" while still being a
// single-region purchase. Treating those as region-agnostic would let a rec
// of unknown region survive an explicit "us-east-1 only" filter and be
// bought in whatever region the service client happens to resolve. A
// reservation rec with no region is dropped by an include filter (its region
// cannot be shown to match) and kept by an exclude filter (it cannot be
// shown to be excluded) -- the same conservative direction each filter had
// before Savings Plans support was added.
//
// CommitmentSavingsPlan alone is likewise not enough. Only the account-level
// plan types (Compute, SageMaker, Database) belong to no region; an
// EC2Instance Savings Plan is region-SCOPED, and extractEC2SPFields
// (recommendations/parser_sp.go) yields Region == "" whenever Cost Explorer
// omitted SavingsPlansDetails or its Region field, because aws.ToString maps
// a nil pointer to "". Exempting those would reopen exactly the hole the
// paragraph above closes for reservations, just for EC2Instance SPs. So the
// exemption requires POSITIVE evidence that the plan is account-level:
// isAccountLevelSPPlanType must recognize the plan type, and anything
// unknown (nil Details, a non-SavingsPlanDetails payload, a plan type this
// build has never heard of) stays region-scoped and is filtered
// conservatively rather than exempted.
func IsRegionAgnostic(rec common.Recommendation) bool {
	if rec.CommitmentType != common.CommitmentSavingsPlan || EffectiveRegion(rec) != "" {
		return false
	}
	sp, ok := rec.Details.(*common.SavingsPlanDetails)
	return ok && sp != nil && isAccountLevelSPPlanType(sp.PlanType)
}

// isAccountLevelSPPlanType reports whether planType names a Savings Plans
// product that applies account-wide rather than to one region. Compared
// against the SDK's own sptypes.SavingsPlanType members rather than bare
// string literals (feedback_sdk_enum_string_literals); those members are
// exactly the display strings recommendations/parser_sp.go's
// spPlanTypeDisplayString writes into SavingsPlanDetails.PlanType, so the two
// vocabularies cannot drift silently.
//
// EC2Instance is the one plan type deliberately absent: it is region-scoped,
// which is the whole point of this function.
//
// Unknown values deliberately return false: spPlanTypeDisplayString passes
// unrecognised SDK plan types through verbatim for forward compatibility, and
// a plan type this build does not know about must not be granted a
// region-filter exemption on the strength of a name nobody has checked.
func isAccountLevelSPPlanType(planType string) bool {
	switch sptypes.SavingsPlanType(planType) {
	case sptypes.SavingsPlanTypeCompute, sptypes.SavingsPlanTypeSagemaker, sptypes.SavingsPlanTypeDatabase:
		return true
	default:
		return false
	}
}

// regionSet builds the lookup set for a region filter, skipping blank
// entries. A caller-supplied "" (or a whitespace-only value trimmed to "")
// must never become a matching key: it matches no real region code, and
// without this it would make the exclude filter drop every region-agnostic
// rec via a key that was never a region in the first place.
func regionSet(regions []string) map[string]bool {
	set := make(map[string]bool, len(regions))
	for _, region := range regions {
		if region == "" {
			continue
		}
		set[region] = true
	}
	return set
}

// filterByIncludedRegions filters recommendations to only included regions.
// Region-agnostic recommendations (account-level Savings Plans -- see
// IsRegionAgnostic) are always kept: an include filter narrows region-scoped
// recs, it must not silently drop recs that belong to no region at all.
func filterByIncludedRegions(recs []common.Recommendation, regions []string) []common.Recommendation {
	regionMap := regionSet(regions)

	filtered := make([]common.Recommendation, 0, len(recs))
	for _, rec := range recs {
		if IsRegionAgnostic(rec) || regionMap[EffectiveRegion(rec)] {
			filtered = append(filtered, rec)
		}
	}

	return filtered
}

// filterByExcludedRegions filters out recommendations from excluded regions.
// Region-agnostic recommendations (account-level Savings Plans -- see
// IsRegionAgnostic) are never excluded: they do not belong to any of the
// excluded regions.
func filterByExcludedRegions(recs []common.Recommendation, regions []string) []common.Recommendation {
	regionMap := regionSet(regions)

	filtered := make([]common.Recommendation, 0, len(recs))
	for _, rec := range recs {
		if IsRegionAgnostic(rec) || !regionMap[EffectiveRegion(rec)] {
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

// GetRIUtilization gets per-RI utilization from Cost Explorer, scoped to
// EC2 RIs in region.
func (r *RecommendationsClientAdapter) GetRIUtilization(ctx context.Context, lookbackDays int, region string) ([]recommendations.RIUtilization, error) {
	return r.client.GetRIUtilization(ctx, lookbackDays, region)
}

// GetRICoverageMap returns the per-pool RI coverage % over the last
// lookbackDays days, keyed by "region:instance_type:account" (or
// "region:instance_type:engine:account" for RDS) so the apply helper
// can look up per-linked-account coverage. Caller passes the regions
// to scan; CE returns coverage filtered to that region and grouped by
// LINKED_ACCOUNT + INSTANCE_TYPE.
func (r *RecommendationsClientAdapter) GetRICoverageMap(ctx context.Context, lookbackDays int, regions []string) (recommendations.PoolCoverageMap, error) {
	return r.client.GetRICoverageMap(ctx, lookbackDays, regions)
}

// SetRecLookbackPeriod configures the LookbackPeriodInDays forwarded to
// GetReservationPurchaseRecommendation. Valid values: "7d", "30d", "60d".
func (r *RecommendationsClientAdapter) SetRecLookbackPeriod(period string) {
	r.client.SetRecLookbackPeriod(period)
}

// NewRecommendationsClientDirect creates a new recommendations client returning the concrete type
// (needed for GetRIUtilization which is not part of the generic provider interface).
func NewRecommendationsClientDirect(cfg aws.Config) *RecommendationsClientAdapter {
	return &RecommendationsClientAdapter{
		client: recommendations.NewClient(&cfg),
	}
}

// NewEC2ClientDirect creates a new EC2 client returning the concrete type
// (needed for ListConvertibleReservedInstances which is not part of the generic provider interface).
func NewEC2ClientDirect(cfg aws.Config) *ec2.Client {
	return ec2.NewClient(cfg)
}
