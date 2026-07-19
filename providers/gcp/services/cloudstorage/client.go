// Package cloudstorage provides GCP Cloud Storage commitments client
package cloudstorage

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"cloud.google.com/go/recommender/apiv1"
	"cloud.google.com/go/recommender/apiv1/recommenderpb"
	"cloud.google.com/go/storage"
	"google.golang.org/api/cloudbilling/v1"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

// maxRecsPages caps GCP Recommender API iteration to avoid looping forever on a
// stalled or unexpectedly large result set.
const maxRecsPages = 20

// StorageService interface for storage operations (enables mocking)
type StorageService interface {
	Buckets(ctx context.Context, projectID string) BucketIterator
	Bucket(name string) BucketHandle
	Close() error
}

// BucketIterator interface for bucket iteration (enables mocking)
type BucketIterator interface {
	Next() (*storage.BucketAttrs, error)
}

// BucketHandle interface for bucket operations (enables mocking)
type BucketHandle interface {
	Create(ctx context.Context, projectID string, attrs *storage.BucketAttrs) error
}

// RecommenderClient interface for recommender operations (enables mocking)
type RecommenderClient interface {
	ListRecommendations(ctx context.Context, req *recommenderpb.ListRecommendationsRequest) RecommenderIterator
	Close() error
}

// RecommenderIterator interface for recommender iteration (enables mocking)
type RecommenderIterator interface {
	Next() (*recommenderpb.Recommendation, error)
}

// BillingService interface for billing operations (enables mocking)
type BillingService interface {
	ListSKUs(serviceID string) (*cloudbilling.ListSkusResponse, error)
}

// CloudStorageClient handles GCP Cloud Storage commitments
type CloudStorageClient struct {
	ctx               context.Context
	projectID         string
	region            string
	clientOpts        []option.ClientOption
	storageService    StorageService
	recommenderClient RecommenderClient
	billingService    BillingService
}

// NewClient creates a new GCP Cloud Storage client
func NewClient(ctx context.Context, projectID, region string, opts ...option.ClientOption) (*CloudStorageClient, error) {
	return &CloudStorageClient{
		ctx:        ctx,
		projectID:  projectID,
		region:     region,
		clientOpts: opts,
	}, nil
}

// SetStorageService sets the storage service (for testing)
func (c *CloudStorageClient) SetStorageService(svc StorageService) {
	c.storageService = svc
}

// SetRecommenderClient sets the recommender client (for testing)
func (c *CloudStorageClient) SetRecommenderClient(client RecommenderClient) {
	c.recommenderClient = client
}

// SetBillingService sets the billing service (for testing)
func (c *CloudStorageClient) SetBillingService(svc BillingService) {
	c.billingService = svc
}

// realStorageService wraps the real storage.Client
type realStorageService struct {
	client *storage.Client
}

func (r *realStorageService) Buckets(ctx context.Context, projectID string) BucketIterator {
	return r.client.Buckets(ctx, projectID)
}

func (r *realStorageService) Bucket(name string) BucketHandle {
	return &realBucketHandle{bucket: r.client.Bucket(name)}
}

func (r *realStorageService) Close() error {
	return r.client.Close()
}

// realBucketHandle wraps the real storage.BucketHandle
type realBucketHandle struct {
	bucket *storage.BucketHandle
}

func (r *realBucketHandle) Create(ctx context.Context, projectID string, attrs *storage.BucketAttrs) error {
	return r.bucket.Create(ctx, projectID, attrs)
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

// realBillingService wraps the real cloudbilling.APIService
type realBillingService struct {
	service *cloudbilling.APIService
}

func (r *realBillingService) ListSKUs(serviceID string) (*cloudbilling.ListSkusResponse, error) {
	return r.service.Services.Skus.List(serviceID).Do()
}

// GetServiceType returns the service type
func (c *CloudStorageClient) GetServiceType() common.ServiceType {
	return common.ServiceStorage
}

// GetRegion returns the region
func (c *CloudStorageClient) GetRegion() string {
	return c.region
}

// GetRecommendations gets Cloud Storage recommendations from GCP Recommender API
func (c *CloudStorageClient) GetRecommendations(ctx context.Context, params common.RecommendationParams) ([]common.Recommendation, error) {
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

	// Cloud Storage commitment recommender
	parent := fmt.Sprintf("projects/%s/locations/%s/recommenders/google.storage.bucket.CostRecommender",
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
			return nil, fmt.Errorf("cloudstorage: GetRecommendations iteration cap (%d items) reached", maxRecsPages)
		}
		rec, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			// Iterator errors must propagate so callers don't silently act on a
			// partial recommendation list -- see the computeengine client for the
			// full rationale (issue #1022 H2 fix).
			return nil, fmt.Errorf("cloudstorage: iterate recommendations: %w", err)
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

// GetExistingCommitments returns an empty slice for Cloud Storage. GCP Cloud
// Storage has no commitment API: there is no committed-use discount purchase
// for GCS, and enumerating regional buckets does not represent a commitment --
// it caused every bucket in a region to appear as a "commitment" in the UI
// (10-L2). Return empty until a proper commitment-detection path is available.
func (c *CloudStorageClient) GetExistingCommitments(_ context.Context) ([]common.Commitment, error) {
	return nil, nil
}

// PurchaseCommitment is intentionally a no-op for Cloud Storage: GCP has no CUD
// or commitment purchase API for GCS at all. The previous implementation created
// a brand-new empty bucket, which is not a commitment and incurs ongoing cost, so
// a "purchase" silently provisioned billable infrastructure. Cloud Storage
// recommendations are therefore advisory only; this returns a clear not-supported
// error and never calls any resource-creation API (issue #640).
func (c *CloudStorageClient) PurchaseCommitment(ctx context.Context, rec common.Recommendation, opts common.PurchaseOptions) (common.PurchaseResult, error) {
	return common.PurchaseResult{
		Recommendation: rec,
		DryRun:         false,
		Success:        false,
		Timestamp:      time.Now(),
		Error: fmt.Errorf("%w: GCP Cloud Storage offers no committed-use discount or "+
			"commitment purchase API; this recommendation is advisory only",
			common.ErrCommitmentPurchaseNotSupported),
	}, fmt.Errorf("%w: Cloud Storage", common.ErrCommitmentPurchaseNotSupported)
}

// ValidateOffering validates that a storage class exists
func (c *CloudStorageClient) ValidateOffering(ctx context.Context, rec common.Recommendation) error {
	validClasses, err := c.GetValidResourceTypes(ctx)
	if err != nil {
		return fmt.Errorf("failed to get valid storage classes: %w", err)
	}

	for _, class := range validClasses {
		if class == rec.ResourceType {
			return nil
		}
	}

	return fmt.Errorf("invalid Cloud Storage class: %s", rec.ResourceType)
}

// GetOfferingDetails retrieves Cloud Storage offering details from GCP Billing API
func (c *CloudStorageClient) GetOfferingDetails(ctx context.Context, rec common.Recommendation) (*common.OfferingDetails, error) {
	termYears := 1
	if rec.Term == "3yr" || rec.Term == "3" {
		termYears = 3
	}

	pricing, err := c.getStoragePricing(ctx, rec.ResourceType, c.region, termYears)
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
		OfferingID:          fmt.Sprintf("gcp-storage-%s-%s-%s", rec.ResourceType, c.region, rec.Term),
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

// GetValidResourceTypes returns valid Cloud Storage classes
func (c *CloudStorageClient) GetValidResourceTypes(ctx context.Context) ([]string, error) {
	// Cloud Storage has predefined storage classes
	validClasses := []string{
		"STANDARD",
		"NEARLINE",
		"COLDLINE",
		"ARCHIVE",
	}

	return validClasses, nil
}

// StoragePricing contains pricing information for Cloud Storage
type StoragePricing struct {
	HourlyRate        float64
	CommitmentPrice   float64
	OnDemandPrice     float64
	Currency          string
	SavingsPercentage float64
}

// getStoragePricing gets pricing from GCP Cloud Billing Catalog API.
// It returns an error when commitment pricing is absent from the catalog rather
// than fabricating a price from a hardcoded discount factor (issue #1020).
func (c *CloudStorageClient) getStoragePricing(ctx context.Context, storageClass, region string, termYears int) (*StoragePricing, error) {
	svc, err := c.getOrCreateBillingService(ctx)
	if err != nil {
		return nil, err
	}

	skus, err := svc.ListSKUs("services/95FF-2EF5-5EA1")
	if err != nil {
		return nil, fmt.Errorf("failed to list SKUs: %w", err)
	}

	onDemandPrice, commitmentPrice, currency := extractStoragePricingFromSKUs(skus.Skus, storageClass, region)
	if onDemandPrice == 0 {
		return nil, fmt.Errorf("no pricing found for Cloud Storage class %s", storageClass)
	}
	if commitmentPrice == 0 {
		return nil, fmt.Errorf("no commitment pricing found for Cloud Storage class %s in region %s: catalog has no CUD SKU; cannot compute savings percentage", storageClass, region)
	}

	hoursInTerm := 8760.0 * float64(termYears)
	// Scale the per-unit commitment price to a term total so it is on the
	// same basis as onDemandPrice * hoursInTerm. Without this, the savings
	// percentage would be nearly 100% (per-unit price vs term total).
	commitmentPriceTerm := commitmentPrice * hoursInTerm
	savingsPercentage := calculateStorageSavingsPercentage(onDemandPrice, hoursInTerm, commitmentPriceTerm)

	return &StoragePricing{
		HourlyRate:        commitmentPrice,
		CommitmentPrice:   commitmentPriceTerm,
		OnDemandPrice:     onDemandPrice * hoursInTerm,
		Currency:          currency,
		SavingsPercentage: savingsPercentage,
	}, nil
}

// getOrCreateBillingService returns the billing service, creating it if needed
func (c *CloudStorageClient) getOrCreateBillingService(ctx context.Context) (BillingService, error) {
	if c.billingService != nil {
		return c.billingService, nil
	}

	service, err := cloudbilling.NewService(ctx, c.clientOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create billing service: %w", err)
	}

	return &realBillingService{service: service}, nil
}

// extractStoragePricingFromSKUs extracts on-demand and commitment pricing from SKU list
func extractStoragePricingFromSKUs(skus []*cloudbilling.Sku, storageClass, region string) (onDemand, commitment float64, currency string) {
	currency = "USD"

	for _, sku := range skus {
		if !skuMatchesStorageClass(sku, storageClass, region) {
			continue
		}

		price, curr := extractStoragePriceFromSKU(sku)
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

// extractStoragePriceFromSKU extracts the unit price from a SKU
func extractStoragePriceFromSKU(sku *cloudbilling.Sku) (float64, string) {
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

// calculateStorageSavingsPercentage calculates the savings percentage
func calculateStorageSavingsPercentage(onDemandPrice, hoursInTerm, commitmentPrice float64) float64 {
	onDemandTotal := onDemandPrice * hoursInTerm
	return ((onDemandTotal - commitmentPrice) / onDemandTotal) * 100
}

// skuMatchesStorageClass checks if a SKU matches the storage class and region
func skuMatchesStorageClass(sku *cloudbilling.Sku, storageClass, region string) bool {
	// Check if the SKU description contains the storage class
	if !strings.Contains(strings.ToLower(sku.Description), strings.ToLower(storageClass)) {
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

// extractGCPResourceType returns the last path segment of the first non-empty
// resource field found across all operation groups, or "" if none is present.
func extractGCPResourceType(rec *recommenderpb.Recommendation) string {
	if rec.Content == nil || rec.Content.OperationGroups == nil {
		return ""
	}
	for _, opGroup := range rec.Content.OperationGroups {
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

// extractGCPSavings returns the estimated monthly savings (positive value)
// from the primary cost impact of a GCP recommendation, or 0 if absent.
func extractGCPSavings(rec *recommenderpb.Recommendation) float64 {
	if rec.PrimaryImpact == nil {
		return 0
	}
	costProj := rec.PrimaryImpact.GetCostProjection()
	if costProj == nil || costProj.Cost == nil {
		return 0
	}
	cost := costProj.Cost
	return -(float64(cost.Units) + float64(cost.Nanos)/1e9)
}

// fillStoragePricing calls getStoragePricing and, on success, writes CommitmentCost,
// OnDemandCost, SavingsPercentage, and BreakEvenMonths into rec. Pricing
// failures are logged and do not discard the recommendation.
func (c *CloudStorageClient) fillStoragePricing(ctx context.Context, rec *common.Recommendation, termYears int) {
	pricing, err := c.getStoragePricing(ctx, rec.ResourceType, c.region, termYears)
	if err != nil {
		log.Printf("cloudstorage: pricing unavailable for %s in %s (issue #1020): %v", rec.ResourceType, c.region, err)
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

// termYearsFromLabel converts a term string such as "1yr" or "3yr" to an
// integer number of years (defaults to 1 for any unrecognized value).
func termYearsFromLabel(term string) int {
	if term == "3yr" || term == "3" {
		return 3
	}
	return 1
}

// convertGCPRecommendation converts a GCP Recommender recommendation to common format.
// It also calls getStoragePricing to fill CommitmentCost/OnDemandCost/SavingsPercentage/
// BreakEvenMonths so the scorer can filter and rank GCP recommendations correctly
// (issue #1022 C2). Pricing failures are logged but do not discard the recommendation.
func (c *CloudStorageClient) convertGCPRecommendation(ctx context.Context, gcpRec *recommenderpb.Recommendation, params common.RecommendationParams) *common.Recommendation {
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
		Service:        common.ServiceStorage,
		Account:        c.projectID,
		Region:         c.region,
		CommitmentType: common.CommitmentReservedCapacity,
		Timestamp:      time.Now(),
		Term:           term,
		PaymentOption:  paymentOption,
	}

	rec.ResourceType = extractGCPResourceType(gcpRec)
	rec.EstimatedSavings = extractGCPSavings(gcpRec)

	// Thread pricing into the converter so the scorer can rank/filter GCP recs
	// correctly (issue #1022 C2). fillStoragePricing performs the single billing
	// lookup and populates CommitmentCost; we reuse that value below to derive
	// RecurringMonthlyCost rather than issuing a second SKU call.
	if rec.ResourceType != "" {
		termYears := termYearsFromLabel(rec.Term)
		c.fillStoragePricing(ctx, rec, termYears)

		// Cloud Storage committed-use discounts are monthly-payment commitments,
		// so the per-month charge is CommitmentCost / termMonths. When the
		// billing lookup failed, CommitmentCost stays 0 and RecurringMonthlyCost
		// remains nil so the frontend renders "—" rather than a stale value.
		if rec.CommitmentCost > 0 {
			monthly := rec.CommitmentCost / float64(termYears*12)
			rec.RecurringMonthlyCost = &monthly
		}
	}

	return rec
}
