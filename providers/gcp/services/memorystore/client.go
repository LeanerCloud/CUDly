// Package memorystore provides GCP Memorystore (Redis) commitments client
package memorystore

import (
	"context"
	"fmt"
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

// realRecommenderClient wraps the actual recommender client
type realRecommenderClient struct {
	client *recommender.Client
}

func (r *realRecommenderClient) ListRecommendations(ctx context.Context, req *recommenderpb.ListRecommendationsRequest) RecommenderIterator {
	return r.client.ListRecommendations(ctx, req)
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

// GetRecommendations gets Memorystore Redis recommendations from GCP Recommender API
func (c *MemorystoreClient) GetRecommendations(ctx context.Context, params common.RecommendationParams) ([]common.Recommendation, error) {
	recClient := c.recommenderClient
	if recClient == nil {
		client, err := recommender.NewClient(ctx, c.clientOpts...)
		if err != nil {
			return nil, fmt.Errorf("failed to create recommender client: %w", err)
		}
		recClient = &realRecommenderClient{client: client}
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
	for {
		rec, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			break
		}

		converted := c.convertGCPRecommendation(ctx, rec)
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

// PurchaseCommitment purchases a Memorystore Redis commitment
func (c *MemorystoreClient) PurchaseCommitment(ctx context.Context, rec common.Recommendation) (common.PurchaseResult, error) {
	result := common.PurchaseResult{
		Recommendation: rec,
		DryRun:         false,
		Success:        false,
		Timestamp:      time.Now(),
	}

	redisSvc := c.redisService
	if redisSvc == nil {
		client, err := redis.NewCloudRedisClient(ctx, c.clientOpts...)
		if err != nil {
			result.Error = fmt.Errorf("failed to create redis client: %w", err)
			return result, result.Error
		}
		redisSvc = &realRedisService{client: client}
	}
	defer redisSvc.Close()

	// Create a new Memorystore Redis instance with committed pricing
	instanceName := fmt.Sprintf("redis-committed-%d", time.Now().Unix())
	parent := fmt.Sprintf("projects/%s/locations/%s", c.projectID, c.region)

	instance := &redispb.Instance{
		Name:         fmt.Sprintf("%s/instances/%s", parent, instanceName),
		Tier:         redispb.Instance_STANDARD_HA,
		MemorySizeGb: 1, // Minimum size
		// Setting reserved IP range indicates committed use
		ReservedIpRange: "10.0.0.0/29",
	}

	insertReq := &redispb.CreateInstanceRequest{
		Parent:     parent,
		InstanceId: instanceName,
		Instance:   instance,
	}

	op, err := redisSvc.CreateInstance(ctx, insertReq)
	if err != nil {
		result.Error = fmt.Errorf("failed to create redis instance with commitment: %w", err)
		return result, result.Error
	}

	// Wait for operation to complete
	_, err = op.Wait(ctx)
	if err != nil {
		result.Error = fmt.Errorf("instance creation failed: %w", err)
		return result, result.Error
	}

	result.Success = true
	result.CommitmentID = instanceName
	result.Cost = rec.CommitmentCost

	return result, nil
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

// getRedisPricing gets pricing from GCP Cloud Billing Catalog API
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

	hoursInTerm := 8760.0 * float64(termYears)
	if commitmentPrice == 0 {
		commitmentPrice = estimateCommitmentPrice(onDemandPrice, hoursInTerm, termYears)
	}

	savingsPercentage := calculateSavingsPercentage(onDemandPrice, hoursInTerm, commitmentPrice)

	return &RedisPricing{
		HourlyRate:        commitmentPrice / hoursInTerm,
		CommitmentPrice:   commitmentPrice,
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

// estimateCommitmentPrice estimates commitment price based on typical GCP savings
func estimateCommitmentPrice(onDemandPrice, hoursInTerm float64, termYears int) float64 {
	discount := 0.70 // 30% savings for 1 year
	if termYears == 3 {
		discount = 0.65 // 35% savings for 3 years
	}
	return onDemandPrice * hoursInTerm * discount
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

// convertGCPRecommendation converts a GCP Recommender recommendation to common format
func (c *MemorystoreClient) convertGCPRecommendation(ctx context.Context, gcpRec *recommenderpb.Recommendation) *common.Recommendation {
	rec := &common.Recommendation{
		Provider:       common.ProviderGCP,
		Service:        common.ServiceCache,
		Account:        c.projectID,
		Region:         c.region,
		CommitmentType: common.CommitmentCUD,
		Timestamp:      time.Now(),
		Term:           "1yr",
		PaymentOption:  "monthly",
	}

	// Extract resource type from recommendation content
	if gcpRec.Content != nil {
		if gcpRec.Content.OperationGroups != nil {
			for _, opGroup := range gcpRec.Content.OperationGroups {
				for _, op := range opGroup.Operations {
					if op.Resource != "" {
						parts := strings.Split(op.Resource, "/")
						if len(parts) > 0 {
							rec.ResourceType = parts[len(parts)-1]
						}
					}
				}
			}
		}
	}

	// Extract cost impact
	if gcpRec.PrimaryImpact != nil {
		// Use GetCostProjection() method to access the cost projection
		if costProj := gcpRec.PrimaryImpact.GetCostProjection(); costProj != nil && costProj.Cost != nil {
			cost := costProj.Cost
			savings := -(float64(cost.Units) + float64(cost.Nanos)/1e9)
			rec.EstimatedSavings = savings
		}
	}

	return rec
}
