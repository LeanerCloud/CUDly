// Package memorystore provides GCP Memorystore (Redis) commitments client
package memorystore

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"cloud.google.com/go/recommender/apiv1"
	"cloud.google.com/go/recommender/apiv1/recommenderpb"
	"cloud.google.com/go/redis/apiv1"
	"cloud.google.com/go/redis/apiv1/redispb"
	gax "github.com/googleapis/gax-go/v2"
	"google.golang.org/api/cloudbilling/v1"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

// maxRecsPages caps GCP Recommender API iteration.
const maxRecsPages = 20

// RedisService interface for Redis operations
type RedisService interface {
	ListInstances(ctx context.Context, req *redispb.ListInstancesRequest) RedisIterator
	CreateInstance(ctx context.Context, req *redispb.CreateInstanceRequest) (CreateInstanceOperation, error)
	Close() error
}

// RedisIterator interface for iterating Redis instances
type RedisIterator interface {
	Next() (*redispb.Instance, error)
}

// CreateInstanceOperation interface for create instance operation
type CreateInstanceOperation interface {
	Wait(ctx context.Context, opts ...gax.CallOption) (*redispb.Instance, error)
}

// BillingService interface for Cloud Billing operations
type BillingService interface {
	ListSKUs(serviceID string) (*cloudbilling.ListSkusResponse, error)
}

// RecommenderIterator interface for iterating recommendations
type RecommenderIterator interface {
	Next() (*recommenderpb.Recommendation, error)
}

// RecommenderClient interface for recommender operations
type RecommenderClient interface {
	ListRecommendations(ctx context.Context, req *recommenderpb.ListRecommendationsRequest) RecommenderIterator
	Close() error
}

// MemorystoreClient handles GCP Memorystore (Redis) commitments
type MemorystoreClient struct {
	ctx               context.Context
	projectID         string
	region            string
	clientOpts        []option.ClientOption
	redisService      RedisService
	billingService    BillingService
	recommenderClient RecommenderClient
}

// NewClient creates a new GCP Memorystore client
func NewClient(ctx context.Context, projectID, region string, opts ...option.ClientOption) (*MemorystoreClient, error) {
	return &MemorystoreClient{
		ctx:        ctx,
		projectID:  projectID,
		region:     region,
		clientOpts: opts,
	}, nil
}

// SetRedisService sets the Redis service (for testing)
func (c *MemorystoreClient) SetRedisService(svc RedisService) {
	c.redisService = svc
}

// SetBillingService sets the billing service (for testing)
func (c *MemorystoreClient) SetBillingService(svc BillingService) {
	c.billingService = svc
}

// SetRecommenderClient sets the recommender client (for testing)
func (c *MemorystoreClient) SetRecommenderClient(client RecommenderClient) {
	c.recommenderClient = client
}

// realRedisService wraps the actual Redis client
type realRedisService struct {
	client *redis.CloudRedisClient
}

func (r *realRedisService) ListInstances(ctx context.Context, req *redispb.ListInstancesRequest) RedisIterator {
	return r.client.ListInstances(ctx, req)
}

func (r *realRedisService) CreateInstance(ctx context.Context, req *redispb.CreateInstanceRequest) (CreateInstanceOperation, error) {
	return r.client.CreateInstance(ctx, req)
}

func (r *realRedisService) Close() error {
	return r.client.Close()
}

// realBillingService wraps the actual Cloud Billing service
type realBillingService struct {
	service *cloudbilling.APIService
}

func (r *realBillingService) ListSKUs(serviceID string) (*cloudbilling.ListSkusResponse, error) {
	return r.service.Services.Skus.List(serviceID).Do()
}

// realRecommenderIterator wraps the real recommender iterator (10-L4: makes
// the memorystore client diffable against the other three service clients).
type realRecommenderIterator struct {
	it *recommender.RecommendationIterator
}

func (r *realRecommenderIterator) Next() (*recommenderpb.Recommendation, error) {
	return r.it.Next()
}

// realRecommenderClient wraps the actual recommender client
type realRecommenderClient struct {
	client *recommender.Client
}

func (r *realRecommenderClient) ListRecommendations(ctx context.Context, req *recommenderpb.ListRecommendationsRequest) RecommenderIterator {
	return &realRecommenderIterator{it: r.client.ListRecommendations(ctx, req)}
}

func (r *realRecommenderClient) Close() error {
	return r.client.Close()
}

// GetServiceType returns the service type
func (c *MemorystoreClient) GetServiceType() common.ServiceType {
	return common.ServiceCache
}

// GetRegion returns the region
func (c *MemorystoreClient) GetRegion() string {
	return c.region
}

// resolveRecommenderClient returns the injected client (for testing) or creates
// a new one from the stored options.
func (c *MemorystoreClient) resolveRecommenderClient(ctx context.Context) (RecommenderClient, error) {
	if c.recommenderClient != nil {
		return c.recommenderClient, nil
	}
	client, err := recommender.NewClient(ctx, c.clientOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create recommender client: %w", err)
	}
	return &realRecommenderClient{client: client}, nil
}

// GetRecommendations gets Memorystore Redis recommendations from GCP Recommender API
func (c *MemorystoreClient) GetRecommendations(ctx context.Context, p *common.RecommendationParams) ([]common.Recommendation, error) {
	if p == nil {
		return nil, fmt.Errorf("params cannot be nil")
	}
	params := *p
	recClient, err := c.resolveRecommenderClient(ctx)
	if err != nil {
		return nil, err
	}
	defer recClient.Close()

	recommendations := make([]common.Recommendation, 0)

	// Memorystore Redis recommender (if available)
	parent := fmt.Sprintf("projects/%s/locations/%s/recommenders/google.memorystore.redis.PerformanceRecommender",
		c.projectID, c.region)

	req := &recommenderpb.ListRecommendationsRequest{
		Parent: parent,
	}

	it := recClient.ListRecommendations(ctx, req)
	for pageIdx := 0; ; pageIdx++ {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("context cancelled during pagination: %w", err)
		}
		if pageIdx >= maxRecsPages {
			return nil, fmt.Errorf("memorystore: GetRecommendations iteration cap (%d items) reached", maxRecsPages)
		}
		rec, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			// Iterator errors must propagate so callers don't silently act
			// on a partial recommendation list -- see the computeengine
			// client for the full rationale.
			return nil, fmt.Errorf("memorystore: iterate recommendations: %w", err)
		}

		// Skip non-ACTIVE recommendations (CLAIMED/SUCCEEDED/FAILED/DISMISSED).
		// See computeengine.GetRecommendations for the full rationale.
		if rec.GetStateInfo().GetState() != recommenderpb.RecommendationStateInfo_ACTIVE {
			continue
		}

		converted := c.convertGCPRecommendation(ctx, rec, params)
		if converted != nil {
			recommendations = append(recommendations, *converted)
		}
	}

	return recommendations, nil
}

// GetExistingCommitments retrieves existing Memorystore Redis commitments
func (c *MemorystoreClient) GetExistingCommitments(ctx context.Context) ([]common.Commitment, error) {
	// GCP Memorystore Redis does not expose commitment status via the Redis API.
	// ReservedIpRange (previously used here) is the VPC peering CIDR, not a
	// commitment indicator. Return empty until a proper detection method is available.
	return nil, nil
}

// PurchaseCommitment is intentionally a no-op for Memorystore: GCP exposes no
// standalone committed-use discount purchase API for Memorystore. Memorystore is
// covered by spend-based CUDs bought via the Cloud Billing console / Cloud
// Commerce Consumer Procurement API, not by creating an instance. The previous
// implementation created a brand-new billable Redis instance (a reserved IP range
// is networking config, not a commitment), so a "purchase" silently spun up a new
// Redis instance that kept billing. Memorystore recommendations are therefore
// advisory only; this returns a clear not-supported error and never calls any
// resource-creation API (issue #640).
func (c *MemorystoreClient) PurchaseCommitment(ctx context.Context, rec common.Recommendation, opts common.PurchaseOptions) (common.PurchaseResult, error) {
	return common.PurchaseResult{
		Recommendation: rec,
		DryRun:         false,
		Success:        false,
		Timestamp:      time.Now(),
		Error: fmt.Errorf("%w: GCP Memorystore has no standalone committed-use discount "+
			"purchase API (spend-based CUDs are bought via the Cloud Billing console or "+
			"Cloud Commerce Consumer Procurement API); this recommendation is advisory only",
			common.ErrCommitmentPurchaseNotSupported),
	}, fmt.Errorf("%w: Memorystore", common.ErrCommitmentPurchaseNotSupported)
}

// ValidateOffering validates that a Redis tier exists
func (c *MemorystoreClient) ValidateOffering(ctx context.Context, rec common.Recommendation) error {
	validTiers, err := c.GetValidResourceTypes(ctx)
	if err != nil {
		return fmt.Errorf("failed to get valid tiers: %w", err)
	}

	for _, tier := range validTiers {
		if tier == rec.ResourceType {
			return nil
		}
	}

	return fmt.Errorf("invalid Memorystore tier: %s", rec.ResourceType)
}

// GetOfferingDetails retrieves Memorystore offering details from GCP Billing API
func (c *MemorystoreClient) GetOfferingDetails(ctx context.Context, rec common.Recommendation) (*common.OfferingDetails, error) {
	termYears := 1
	if rec.Term == "3yr" || rec.Term == "3" {
		termYears = 3
	}

	pricing, err := c.getRedisPricing(ctx, rec.ResourceType, c.region, termYears)
	if err != nil {
		return nil, fmt.Errorf("failed to get pricing: %w", err)
	}

	var upfrontCost, recurringCost float64
	totalCost := pricing.CommitmentPrice

	switch rec.PaymentOption {
	case "all-upfront", "upfront":
		upfrontCost = totalCost
		recurringCost = 0
	case "monthly", "no-upfront":
		upfrontCost = 0
		recurringCost = totalCost / (float64(termYears) * 12)
	default:
		upfrontCost = totalCost
	}

	return &common.OfferingDetails{
		OfferingID:          fmt.Sprintf("gcp-memorystore-%s-%s-%s", rec.ResourceType, c.region, rec.Term),
		ResourceType:        rec.ResourceType,
		Term:                rec.Term,
		PaymentOption:       rec.PaymentOption,
		UpfrontCost:         upfrontCost,
		RecurringCost:       recurringCost,
		TotalCost:           totalCost,
		EffectiveHourlyRate: pricing.HourlyRate,
		Currency:            pricing.Currency,
	}, nil
}

// GetValidResourceTypes returns valid Memorystore tiers
func (c *MemorystoreClient) GetValidResourceTypes(ctx context.Context) ([]string, error) {
	// Memorystore Redis has predefined tiers
	validTiers := []string{
		"BASIC",
		"STANDARD_HA",
	}

	return validTiers, nil
}

// RedisPricing contains pricing information for Memorystore Redis
type RedisPricing struct {
	HourlyRate        float64
	CommitmentPrice   float64
	OnDemandPrice     float64
	Currency          string
	SavingsPercentage float64
}

// getRedisPricing gets pricing from GCP Cloud Billing Catalog API.
// It returns an error when commitment pricing is absent from the catalog rather
// than fabricating a price from a hardcoded discount factor (issue #1020).
func (c *MemorystoreClient) getRedisPricing(ctx context.Context, tier, region string, termYears int) (*RedisPricing, error) {
	billingSvc, err := c.getOrCreateBillingService(ctx)
	if err != nil {
		return nil, err
	}

	skus, err := billingSvc.ListSKUs("services/D559-82DA-3A56")
	if err != nil {
		return nil, fmt.Errorf("failed to list SKUs: %w", err)
	}

	onDemandPrice, commitmentPrice, currency := extractPricingFromSKUs(skus.Skus, tier, region)
	if onDemandPrice == 0 {
		return nil, fmt.Errorf("no pricing found for Memorystore tier %s", tier)
	}
	if commitmentPrice == 0 {
		return nil, fmt.Errorf("no commitment pricing found for Memorystore tier %s in region %s: catalog has no CUD SKU; cannot compute savings percentage", tier, region)
	}

	hoursInTerm := 8760.0 * float64(termYears)
	// Scale the per-hour commitment price to a term total so it is on the
	// same basis as onDemandPrice * hoursInTerm. Without this, the savings
	// percentage would be nearly 100% (per-hour price vs term total).
	commitmentPriceTerm := commitmentPrice * hoursInTerm
	savingsPercentage := calculateSavingsPercentage(onDemandPrice, hoursInTerm, commitmentPriceTerm)

	return &RedisPricing{
		HourlyRate:        commitmentPrice,
		CommitmentPrice:   commitmentPriceTerm,
		OnDemandPrice:     onDemandPrice * hoursInTerm,
		Currency:          currency,
		SavingsPercentage: savingsPercentage,
	}, nil
}

// getOrCreateBillingService returns the billing service, creating it if needed
func (c *MemorystoreClient) getOrCreateBillingService(ctx context.Context) (BillingService, error) {
	if c.billingService != nil {
		return c.billingService, nil
	}

	service, err := cloudbilling.NewService(ctx, c.clientOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create billing service: %w", err)
	}

	return &realBillingService{service: service}, nil
}

// extractPricingFromSKUs extracts on-demand and commitment pricing from SKU list
func extractPricingFromSKUs(skus []*cloudbilling.Sku, tier, region string) (onDemand, commitment float64, currency string) {
	currency = "USD"

	for _, sku := range skus {
		if !skuMatchesTier(sku, tier, region) {
			continue
		}

		price, curr := extractPriceFromSKU(sku)
		if price == 0 {
			continue
		}

		if curr != "" {
			currency = curr
		}

		if strings.Contains(strings.ToLower(sku.Description), "commitment") {
			commitment = price
		} else {
			onDemand = price
		}
	}

	return onDemand, commitment, currency
}

// extractPriceFromSKU extracts the unit price from a SKU
func extractPriceFromSKU(sku *cloudbilling.Sku) (float64, string) {
	if len(sku.PricingInfo) == 0 {
		return 0, ""
	}

	pricingInfo := sku.PricingInfo[0]
	if pricingInfo.PricingExpression == nil || len(pricingInfo.PricingExpression.TieredRates) == 0 {
		return 0, ""
	}

	rate := pricingInfo.PricingExpression.TieredRates[0]
	if rate.UnitPrice == nil {
		return 0, ""
	}

	price := float64(rate.UnitPrice.Units) + float64(rate.UnitPrice.Nanos)/1e9
	return price, rate.UnitPrice.CurrencyCode
}

// calculateSavingsPercentage calculates the savings percentage
func calculateSavingsPercentage(onDemandPrice, hoursInTerm, commitmentPrice float64) float64 {
	onDemandTotal := onDemandPrice * hoursInTerm
	return ((onDemandTotal - commitmentPrice) / onDemandTotal) * 100
}

// skuMatchesTier checks if a SKU matches the tier and region
func skuMatchesTier(sku *cloudbilling.Sku, tier, region string) bool {
	// Check if the SKU description contains the tier
	if !strings.Contains(strings.ToLower(sku.Description), strings.ToLower(tier)) {
		return false
	}

	// Check if the SKU is available in the region
	if sku.ServiceRegions != nil {
		for _, serviceRegion := range sku.ServiceRegions {
			if strings.EqualFold(serviceRegion, region) {
				return true
			}
		}
		return false
	}

	return true
}

// extractResourceTypeFromContent extracts the last path segment of the first
// non-empty Operation.Resource across all operation groups. Used by all four
// GCP service converters to set rec.ResourceType from Recommender payloads.
func extractResourceTypeFromContent(content *recommenderpb.RecommendationContent) string {
	if content == nil || content.OperationGroups == nil {
		return ""
	}
	for _, opGroup := range content.OperationGroups {
		for _, op := range opGroup.Operations {
			if op.Resource == "" {
				continue
			}
			parts := strings.Split(op.Resource, "/")
			if len(parts) > 0 {
				return parts[len(parts)-1]
			}
		}
	}
	return ""
}

// extractEstimatedSavings returns the negative of the PrimaryImpact cost
// projection (GCP encodes savings as a negative cost delta).
func extractEstimatedSavings(gcpRec *recommenderpb.Recommendation) float64 {
	if gcpRec.PrimaryImpact == nil {
		return 0
	}
	costProj := gcpRec.PrimaryImpact.GetCostProjection()
	if costProj == nil || costProj.Cost == nil {
		return 0
	}
	cost := costProj.Cost
	return -(float64(cost.Units) + float64(cost.Nanos)/1e9)
}

// fillRedisPricing calls getRedisPricing and, on success, writes CommitmentCost,
// OnDemandCost, SavingsPercentage, and BreakEvenMonths into rec. Pricing
// failures are logged and do not discard the recommendation.
func (c *MemorystoreClient) fillRedisPricing(ctx context.Context, rec *common.Recommendation, termYears int) {
	pricing, err := c.getRedisPricing(ctx, rec.ResourceType, c.region, termYears)
	if err != nil {
		log.Printf("memorystore: pricing unavailable for %s in %s (issue #1020): %v", rec.ResourceType, c.region, err)
		return
	}
	rec.CommitmentCost = pricing.CommitmentPrice
	rec.OnDemandCost = pricing.OnDemandPrice
	rec.SavingsPercentage = pricing.SavingsPercentage
	if pricing.OnDemandPrice > 0 && pricing.SavingsPercentage > 0 {
		monthlySavings := pricing.OnDemandPrice * pricing.SavingsPercentage / 100.0 / float64(termYears*12)
		if monthlySavings > 0 {
			rec.BreakEvenMonths = pricing.CommitmentPrice / monthlySavings
		}
	}
}

// convertGCPRecommendation converts a GCP Recommender recommendation to common format.
// It also calls getRedisPricing to fill CommitmentCost/OnDemandCost/SavingsPercentage/
// BreakEvenMonths so the scorer can filter and rank GCP recommendations correctly
// (issue #1022 C2). Pricing failures are logged but do not discard the recommendation.
func (c *MemorystoreClient) convertGCPRecommendation(ctx context.Context, gcpRec *recommenderpb.Recommendation, params common.RecommendationParams) *common.Recommendation {
	paymentOption := params.PaymentOption
	if paymentOption == "" {
		paymentOption = "monthly"
	}

	term := params.Term
	if term == "" {
		term = "1yr"
	}

	rec := &common.Recommendation{
		Provider:       common.ProviderGCP,
		Service:        common.ServiceCache,
		Account:        c.projectID,
		Region:         c.region,
		CommitmentType: common.CommitmentCUD,
		Timestamp:      time.Now(),
		Term:           term,
		PaymentOption:  paymentOption,
	}

	rec.ResourceType = extractResourceTypeFromContent(gcpRec.Content)
	rec.EstimatedSavings = extractEstimatedSavings(gcpRec)

	// Thread pricing into the converter so the scorer can rank/filter GCP recs
	// correctly (issue #1022 C2).
	if rec.ResourceType != "" {
		termYears := 1
		if rec.Term == "3yr" || rec.Term == "3" {
			termYears = 3
		}
		c.fillRedisPricing(ctx, rec, termYears)
	}

	return rec
}
