// Package cloudsql provides GCP Cloud SQL commitments client
package cloudsql

import (
	"context"
	"fmt"
	"strings"
	"time"

	"cloud.google.com/go/recommender/apiv1"
	"cloud.google.com/go/recommender/apiv1/recommenderpb"
	"google.golang.org/api/cloudbilling/v1"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	"google.golang.org/api/sqladmin/v1"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

// SQLAdminService interface for SQL admin operations (enables mocking)
type SQLAdminService interface {
	ListInstances(projectID string) (*sqladmin.InstancesListResponse, error)
	InsertInstance(projectID string, instance *sqladmin.DatabaseInstance) (*sqladmin.Operation, error)
	ListTiers(projectID string) (*sqladmin.TiersListResponse, error)
}

// BillingService interface for billing operations (enables mocking)
type BillingService interface {
	ListSKUs(serviceID string) (*cloudbilling.ListSkusResponse, error)
}

// RecommenderIterator interface for recommender iteration (enables mocking)
type RecommenderIterator interface {
	Next() (*recommenderpb.Recommendation, error)
}

// RecommenderClient interface for recommender operations (enables mocking)
type RecommenderClient interface {
	ListRecommendations(ctx context.Context, req *recommenderpb.ListRecommendationsRequest) RecommenderIterator
	Close() error
}

// CloudSQLClient handles GCP Cloud SQL commitments
type CloudSQLClient struct {
	ctx               context.Context
	projectID         string
	region            string
	clientOpts        []option.ClientOption
	sqlAdminService   SQLAdminService
	billingService    BillingService
	recommenderClient RecommenderClient
}

// NewClient creates a new GCP Cloud SQL client
func NewClient(ctx context.Context, projectID, region string, opts ...option.ClientOption) (*CloudSQLClient, error) {
	return &CloudSQLClient{
		ctx:        ctx,
		projectID:  projectID,
		region:     region,
		clientOpts: opts,
	}, nil
}

// SetSQLAdminService sets the SQL admin service (for testing)
func (c *CloudSQLClient) SetSQLAdminService(svc SQLAdminService) {
	c.sqlAdminService = svc
}

// SetBillingService sets the billing service (for testing)
func (c *CloudSQLClient) SetBillingService(svc BillingService) {
	c.billingService = svc
}

// SetRecommenderClient sets the recommender client (for testing)
func (c *CloudSQLClient) SetRecommenderClient(client RecommenderClient) {
	c.recommenderClient = client
}

// realSQLAdminService wraps the real sqladmin.Service
type realSQLAdminService struct {
	service *sqladmin.Service
}

func (r *realSQLAdminService) ListInstances(projectID string) (*sqladmin.InstancesListResponse, error) {
	return r.service.Instances.List(projectID).Do()
}

func (r *realSQLAdminService) InsertInstance(projectID string, instance *sqladmin.DatabaseInstance) (*sqladmin.Operation, error) {
	return r.service.Instances.Insert(projectID, instance).Do()
}

func (r *realSQLAdminService) ListTiers(projectID string) (*sqladmin.TiersListResponse, error) {
	return r.service.Tiers.List(projectID).Do()
}

// realBillingService wraps the real cloudbilling.APIService
type realBillingService struct {
	service *cloudbilling.APIService
}

func (r *realBillingService) ListSKUs(serviceID string) (*cloudbilling.ListSkusResponse, error) {
	return r.service.Services.Skus.List(serviceID).Do()
}

// realRecommenderIterator wraps the real recommender iterator
type realRecommenderIterator struct {
	it *recommender.RecommendationIterator
}

func (r *realRecommenderIterator) Next() (*recommenderpb.Recommendation, error) {
	return r.it.Next()
}

// realRecommenderClient wraps the real recommender client
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
func (c *CloudSQLClient) GetServiceType() common.ServiceType {
	return common.ServiceRelationalDB
}

// GetRegion returns the region
func (c *CloudSQLClient) GetRegion() string {
	return c.region
}

// GetRecommendations gets Cloud SQL recommendations from GCP Recommender API
func (c *CloudSQLClient) GetRecommendations(ctx context.Context, params common.RecommendationParams) ([]common.Recommendation, error) {
	recommendations := make([]common.Recommendation, 0)

	// Use injected client if available (for testing)
	var recClient RecommenderClient
	if c.recommenderClient != nil {
		recClient = c.recommenderClient
	} else {
		client, err := recommender.NewClient(ctx, c.clientOpts...)
		if err != nil {
			return nil, fmt.Errorf("failed to create recommender client: %w", err)
		}
		recClient = &realRecommenderClient{client: client}
	}
	defer recClient.Close()

	// Cloud SQL commitment recommender
	parent := fmt.Sprintf("projects/%s/locations/%s/recommenders/google.cloudsql.instance.PerformanceRecommender",
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

// GetExistingCommitments retrieves existing Cloud SQL commitments
func (c *CloudSQLClient) GetExistingCommitments(ctx context.Context) ([]common.Commitment, error) {
	commitments := make([]common.Commitment, 0)

	// Use injected service if available (for testing)
	var svc SQLAdminService
	if c.sqlAdminService != nil {
		svc = c.sqlAdminService
	} else {
		service, err := sqladmin.NewService(ctx, c.clientOpts...)
		if err != nil {
			return nil, fmt.Errorf("failed to create SQL admin service: %w", err)
		}
		svc = &realSQLAdminService{service: service}
	}

	// List all SQL instances in the project
	instances, err := svc.ListInstances(c.projectID)
	if err != nil {
		return nil, fmt.Errorf("failed to list SQL instances: %w", err)
	}

	if instances == nil {
		return commitments, nil
	}

	for _, instance := range instances.Items {
		// Check if instance has a commitment (long-term pricing plan)
		if instance.Settings != nil && instance.Settings.PricingPlan == "PACKAGE" {
			commitment := common.Commitment{
				Provider:       common.ProviderGCP,
				Account:        c.projectID,
				CommitmentType: common.CommitmentCUD,
				Service:        common.ServiceRelationalDB,
				Region:         instance.Region,
				CommitmentID:   instance.Name,
				State:          strings.ToLower(instance.State),
				ResourceType:   instance.DatabaseVersion,
			}

			// Extract tier (machine type)
			if instance.Settings.Tier != "" {
				commitment.ResourceType = instance.Settings.Tier
			}

			commitments = append(commitments, commitment)
		}
	}

	return commitments, nil
}

// PurchaseCommitment purchases a Cloud SQL commitment
func (c *CloudSQLClient) PurchaseCommitment(ctx context.Context, rec common.Recommendation) (common.PurchaseResult, error) {
	result := common.PurchaseResult{
		Recommendation: rec,
		DryRun:         false,
		Success:        false,
		Timestamp:      time.Now(),
	}

	// Use injected service if available (for testing)
	var svc SQLAdminService
	if c.sqlAdminService != nil {
		svc = c.sqlAdminService
	} else {
		service, err := sqladmin.NewService(ctx, c.clientOpts...)
		if err != nil {
			result.Error = fmt.Errorf("failed to create SQL admin service: %w", err)
			return result, result.Error
		}
		svc = &realSQLAdminService{service: service}
	}

	// Create a new Cloud SQL instance with commitment pricing
	instanceName := fmt.Sprintf("sql-committed-%d", time.Now().Unix())

	instance := &sqladmin.DatabaseInstance{
		Name:            instanceName,
		Region:          c.region,
		DatabaseVersion: "MYSQL_8_0", // Default to MySQL 8.0
		Settings: &sqladmin.Settings{
			Tier:        rec.ResourceType,
			PricingPlan: "PACKAGE", // This indicates a commitment
		},
	}

	op, err := svc.InsertInstance(c.projectID, instance)
	if err != nil {
		result.Error = fmt.Errorf("failed to create SQL instance with commitment: %w", err)
		return result, result.Error
	}

	// Wait for operation to complete (in production, you'd poll this)
	if op.Status != "DONE" {
		result.Error = fmt.Errorf("instance creation in progress: %s", op.Status)
		return result, result.Error
	}

	result.Success = true
	result.CommitmentID = instanceName
	result.Cost = rec.CommitmentCost

	return result, nil
}

// ValidateOffering validates that a Cloud SQL tier exists
func (c *CloudSQLClient) ValidateOffering(ctx context.Context, rec common.Recommendation) error {
	validTiers, err := c.GetValidResourceTypes(ctx)
	if err != nil {
		return fmt.Errorf("failed to get valid tiers: %w", err)
	}

	for _, tier := range validTiers {
		if tier == rec.ResourceType {
			return nil
		}
	}

	return fmt.Errorf("invalid Cloud SQL tier: %s", rec.ResourceType)
}

// GetOfferingDetails retrieves Cloud SQL offering details from GCP Billing API
func (c *CloudSQLClient) GetOfferingDetails(ctx context.Context, rec common.Recommendation) (*common.OfferingDetails, error) {
	termYears := 1
	if rec.Term == "3yr" || rec.Term == "3" {
		termYears = 3
	}

	pricing, err := c.getSQLPricing(ctx, rec.ResourceType, c.region, termYears)
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
		OfferingID:          fmt.Sprintf("gcp-cloudsql-%s-%s-%s", rec.ResourceType, c.region, rec.Term),
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

// GetValidResourceTypes returns valid Cloud SQL tiers
func (c *CloudSQLClient) GetValidResourceTypes(ctx context.Context) ([]string, error) {
	// Use injected service if available (for testing)
	var svc SQLAdminService
	if c.sqlAdminService != nil {
		svc = c.sqlAdminService
	} else {
		service, err := sqladmin.NewService(ctx, c.clientOpts...)
		if err != nil {
			return nil, fmt.Errorf("failed to create SQL admin service: %w", err)
		}
		svc = &realSQLAdminService{service: service}
	}

	// List available tiers for the region
	tiers, err := svc.ListTiers(c.projectID)
	if err != nil {
		return nil, fmt.Errorf("failed to list SQL tiers: %w", err)
	}

	validTiers := make([]string, 0)
	for _, tier := range tiers.Items {
		// Filter for tiers available in the region
		if len(tier.Region) == 0 || contains(tier.Region, c.region) {
			validTiers = append(validTiers, tier.Tier)
		}
	}

	if len(validTiers) == 0 {
		return nil, fmt.Errorf("no Cloud SQL tiers found for region %s", c.region)
	}

	return validTiers, nil
}

// SQLPricing contains pricing information for Cloud SQL
type SQLPricing struct {
	HourlyRate        float64
	CommitmentPrice   float64
	OnDemandPrice     float64
	Currency          string
	SavingsPercentage float64
}

// getSQLPricing gets pricing from GCP Cloud Billing Catalog API
func (c *CloudSQLClient) getSQLPricing(ctx context.Context, tier, region string, termYears int) (*SQLPricing, error) {
	svc, err := c.getOrCreateBillingService(ctx)
	if err != nil {
		return nil, err
	}

	skus, err := svc.ListSKUs("services/9662-B51E-5089")
	if err != nil {
		return nil, fmt.Errorf("failed to list SKUs: %w", err)
	}

	onDemandPrice, currency := extractSQLPricingFromSKUs(skus.Skus, tier, region)
	if onDemandPrice == 0 {
		return nil, fmt.Errorf("no pricing found for Cloud SQL tier %s", tier)
	}

	hoursInTerm := 8760.0 * float64(termYears)
	commitmentPrice := estimateSQLCommitmentPrice(onDemandPrice, hoursInTerm, termYears)
	savingsPercentage := calculateSQLSavingsPercentage(onDemandPrice, hoursInTerm, commitmentPrice)

	return &SQLPricing{
		HourlyRate:        commitmentPrice / hoursInTerm,
		CommitmentPrice:   commitmentPrice,
		OnDemandPrice:     onDemandPrice * hoursInTerm,
		Currency:          currency,
		SavingsPercentage: savingsPercentage,
	}, nil
}

// getOrCreateBillingService returns the billing service, creating it if needed
func (c *CloudSQLClient) getOrCreateBillingService(ctx context.Context) (BillingService, error) {
	if c.billingService != nil {
		return c.billingService, nil
	}

	service, err := cloudbilling.NewService(ctx, c.clientOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create billing service: %w", err)
	}

	return &realBillingService{service: service}, nil
}

// extractSQLPricingFromSKUs extracts on-demand pricing from SKU list
func extractSQLPricingFromSKUs(skus []*cloudbilling.Sku, tier, region string) (onDemand float64, currency string) {
	currency = "USD"

	for _, sku := range skus {
		if !skuMatchesTier(sku, tier, region) {
			continue
		}

		price, curr := extractSQLPriceFromSKU(sku)
		if price == 0 {
			continue
		}

		if curr != "" {
			currency = curr
		}

		// Cloud SQL doesn't have separate commitment pricing in the API
		onDemand = price
	}

	return onDemand, currency
}

// extractSQLPriceFromSKU extracts the unit price from a SKU
func extractSQLPriceFromSKU(sku *cloudbilling.Sku) (float64, string) {
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

// estimateSQLCommitmentPrice estimates commitment price based on GCP SQL package savings
func estimateSQLCommitmentPrice(onDemandPrice, hoursInTerm float64, termYears int) float64 {
	discount := 0.85 // 15% savings for 1 year
	if termYears == 3 {
		discount = 0.80 // 20% savings for 3 years
	}
	return onDemandPrice * hoursInTerm * discount
}

// calculateSQLSavingsPercentage calculates the savings percentage
func calculateSQLSavingsPercentage(onDemandPrice, hoursInTerm, commitmentPrice float64) float64 {
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
func (c *CloudSQLClient) convertGCPRecommendation(ctx context.Context, gcpRec *recommenderpb.Recommendation) *common.Recommendation {
	rec := &common.Recommendation{
		Provider:       common.ProviderGCP,
		Service:        common.ServiceRelationalDB,
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

// contains checks if a slice contains a string
func contains(slice []string, str string) bool {
	for _, s := range slice {
		if strings.EqualFold(s, str) {
			return true
		}
	}
	return false
}
