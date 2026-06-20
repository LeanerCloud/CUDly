// Package cloudsql provides GCP Cloud SQL commitments client
package cloudsql

import (
	"context"
	"fmt"
	"log"
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

// maxRecsPages caps GCP Recommender API iteration.
const maxRecsPages = 20

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
func (c *CloudSQLClient) GetRecommendations(ctx context.Context, params *common.RecommendationParams) ([]common.Recommendation, error) {
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
	for pageIdx := 0; ; pageIdx++ {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("context cancelled during pagination: %w", err)
		}
		if pageIdx >= maxRecsPages {
			return nil, fmt.Errorf("cloudsql: GetRecommendations iteration cap (%d items) reached", maxRecsPages)
		}
		rec, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			// Iterator errors must propagate so callers don't silently act
			// on a partial recommendation list -- see the computeengine
			// client for the full rationale.
			return nil, fmt.Errorf("cloudsql: iterate recommendations: %w", err)
		}

		// Skip non-ACTIVE recommendations (CLAIMED/SUCCEEDED/FAILED/DISMISSED).
		// See computeengine.GetRecommendations for the full rationale.
		if rec.GetStateInfo().GetState() != recommenderpb.RecommendationStateInfo_ACTIVE {
			continue
		}

		converted := c.convertGCPRecommendation(ctx, rec, *params)
		if converted != nil {
			recommendations = append(recommendations, *converted)
		}
	}

	return recommendations, nil
}

// GetExistingCommitments returns an empty slice for Cloud SQL. Cloud SQL
// spend-based CUDs are purchased via the Cloud Billing console / Cloud Commerce
// Consumer Procurement API, not exposed through the sqladmin API. The legacy
// PricingPlan "PACKAGE" is a per-instance billing mode (non-commitment,
// deprecated) -- treating it as a commitment caused double-counting against
// real spend-based CUDs (10-L3). Return empty until a proper commitment-
// detection path is available.
func (c *CloudSQLClient) GetExistingCommitments(_ context.Context) ([]common.Commitment, error) {
	return nil, nil
}

// PurchaseCommitment is intentionally a no-op for Cloud SQL: GCP exposes no
// programmatic API to buy a Cloud SQL committed-use discount. CUD purchases are
// spend-based and bought via the Cloud Billing console / Cloud Commerce
// Consumer Procurement API, not via sqladmin.Instances.Insert. The previous
// implementation created a brand-new billable SQL instance (the legacy
// PricingPlan "PACKAGE" is a per-instance billing mode, not a commitment), so a
// "purchase" silently spun up a new database that kept billing. Cloud SQL
// recommendations are therefore advisory only; this returns a clear
// not-supported error and never calls any resource-creation API (issue #640).
func (c *CloudSQLClient) PurchaseCommitment(ctx context.Context, rec *common.Recommendation, opts common.PurchaseOptions) (common.PurchaseResult, error) {
	return common.PurchaseResult{
		Recommendation: *rec,
		DryRun:         false,
		Success:        false,
		Timestamp:      time.Now(),
		Error: fmt.Errorf("%w: GCP Cloud SQL committed-use discounts are spend-based and "+
			"must be purchased via the Cloud Billing console or Cloud Commerce Consumer "+
			"Procurement API; this recommendation is advisory only", common.ErrCommitmentPurchaseNotSupported),
	}, fmt.Errorf("%w: Cloud SQL", common.ErrCommitmentPurchaseNotSupported)
}

// ValidateOffering validates that a Cloud SQL tier exists
func (c *CloudSQLClient) ValidateOffering(ctx context.Context, rec *common.Recommendation) error {
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
func (c *CloudSQLClient) GetOfferingDetails(ctx context.Context, rec *common.Recommendation) (*common.OfferingDetails, error) {
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

// getSQLPricing gets pricing from GCP Cloud Billing Catalog API.
// It returns an error when commitment pricing is absent from the catalog rather
// than fabricating a price from a hardcoded discount factor (issue #1020).
func (c *CloudSQLClient) getSQLPricing(ctx context.Context, tier, region string, termYears int) (*SQLPricing, error) {
	svc, err := c.getOrCreateBillingService(ctx)
	if err != nil {
		return nil, err
	}

	skus, err := svc.ListSKUs("services/9662-B51E-5089")
	if err != nil {
		return nil, fmt.Errorf("failed to list SKUs: %w", err)
	}

	onDemandPrice, commitmentPrice, currency := extractSQLPricingFromSKUs(skus.Skus, tier, region)
	if onDemandPrice == 0 {
		return nil, fmt.Errorf("no pricing found for Cloud SQL tier %s", tier)
	}
	if commitmentPrice == 0 {
		return nil, fmt.Errorf("no commitment pricing found for Cloud SQL tier %s in region %s: catalog has no CUD SKU; cannot compute savings percentage", tier, region)
	}

	hoursInTerm := 8760.0 * float64(termYears)
	// Scale the per-hour commitment price to a term total so it is on the
	// same basis as onDemandPrice * hoursInTerm. Without this, the savings
	// percentage would be nearly 100% (per-hour price vs term total).
	commitmentPriceTerm := commitmentPrice * hoursInTerm
	savingsPercentage := calculateSQLSavingsPercentage(onDemandPrice, hoursInTerm, commitmentPriceTerm)

	return &SQLPricing{
		HourlyRate:        commitmentPrice,
		CommitmentPrice:   commitmentPriceTerm,
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

// extractSQLPricingFromSKUs extracts on-demand and commitment pricing from the SKU list.
// Cloud SQL committed-use discounts are surfaced as "commitment" SKUs in the billing catalog.
func extractSQLPricingFromSKUs(skus []*cloudbilling.Sku, tier, region string) (onDemand, commitment float64, currency string) {
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

		if strings.Contains(strings.ToLower(sku.Description), "commitment") {
			commitment = price
		} else {
			onDemand = price
		}
	}

	return onDemand, commitment, currency
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

// fillSQLPricing calls getSQLPricing and, on success, writes CommitmentCost,
// OnDemandCost, SavingsPercentage, and BreakEvenMonths into rec. Pricing
// failures are logged and do not discard the recommendation.
func (c *CloudSQLClient) fillSQLPricing(ctx context.Context, rec *common.Recommendation, termYears int) {
	pricing, err := c.getSQLPricing(ctx, rec.ResourceType, c.region, termYears)
	if err != nil {
		log.Printf("cloudsql: pricing unavailable for %s in %s (issue #1020): %v", rec.ResourceType, c.region, err)
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
// It also calls getSQLPricing to fill CommitmentCost/OnDemandCost/SavingsPercentage/
// BreakEvenMonths so the scorer can filter and rank GCP recommendations correctly
// (issue #1022 C2). Pricing failures are logged but do not discard the recommendation.
func (c *CloudSQLClient) convertGCPRecommendation(ctx context.Context, gcpRec *recommenderpb.Recommendation, params common.RecommendationParams) *common.Recommendation {
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
		Service:        common.ServiceRelationalDB,
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
		c.fillSQLPricing(ctx, rec, termYears)
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
