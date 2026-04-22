// Package cloudstorage provides GCP Cloud Storage commitments client
package cloudstorage

import (
	"context"
	"fmt"
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

// GetExistingCommitments retrieves existing Cloud Storage commitments
func (c *CloudStorageClient) GetExistingCommitments(ctx context.Context) ([]common.Commitment, error) {
	commitments := make([]common.Commitment, 0)

	// Use injected service if available (for testing)
	var svc StorageService
	if c.storageService != nil {
		svc = c.storageService
	} else {
		client, err := storage.NewClient(ctx, c.clientOpts...)
		if err != nil {
			return nil, fmt.Errorf("failed to create storage client: %w", err)
		}
		svc = &realStorageService{client: client}
	}
	defer svc.Close()

	// List all buckets in the project
	it := svc.Buckets(ctx, c.projectID)
	for {
		bucket, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to list buckets: %w", err)
		}

		// Check if bucket has committed storage
		if bucket.Location == c.region {
			commitment := common.Commitment{
				Provider:       common.ProviderGCP,
				Account:        c.projectID,
				CommitmentType: common.CommitmentReservedCapacity,
				Service:        common.ServiceStorage,
				Region:         c.region,
				CommitmentID:   bucket.Name,
				State:          "active",
				ResourceType:   bucket.StorageClass,
			}

			commitments = append(commitments, commitment)
		}
	}

	return commitments, nil
}

// PurchaseCommitment purchases a Cloud Storage commitment
func (c *CloudStorageClient) PurchaseCommitment(ctx context.Context, rec common.Recommendation, opts common.PurchaseOptions) (common.PurchaseResult, error) {
	result := common.PurchaseResult{
		Recommendation: rec,
		DryRun:         false,
		Success:        false,
		Timestamp:      time.Now(),
	}

	// Use injected service if available (for testing)
	var svc StorageService
	if c.storageService != nil {
		svc = c.storageService
	} else {
		client, err := storage.NewClient(ctx, c.clientOpts...)
		if err != nil {
			result.Error = fmt.Errorf("failed to create storage client: %w", err)
			return result, result.Error
		}
		svc = &realStorageService{client: client}
	}
	defer svc.Close()

	// Create a new Cloud Storage bucket with committed storage class
	bucketName := fmt.Sprintf("storage-committed-%d", time.Now().Unix())

	bucket := svc.Bucket(bucketName)
	attrs := &storage.BucketAttrs{
		Location:     c.region,
		StorageClass: rec.ResourceType,
	}

	err := bucket.Create(ctx, c.projectID, attrs)
	if err != nil {
		result.Error = fmt.Errorf("failed to create storage bucket with commitment: %w", err)
		return result, result.Error
	}

	result.Success = true
	result.CommitmentID = bucketName
	result.Cost = rec.CommitmentCost

	return result, nil
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

// getStoragePricing gets pricing from GCP Cloud Billing Catalog API
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

	hoursInTerm := 8760.0 * float64(termYears)
	if commitmentPrice == 0 {
		commitmentPrice = estimateStorageCommitmentPrice(onDemandPrice, hoursInTerm, termYears)
	}

	savingsPercentage := calculateStorageSavingsPercentage(onDemandPrice, hoursInTerm, commitmentPrice)

	return &StoragePricing{
		HourlyRate:        commitmentPrice / hoursInTerm,
		CommitmentPrice:   commitmentPrice,
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

// estimateStorageCommitmentPrice estimates commitment price based on GCP storage savings
func estimateStorageCommitmentPrice(onDemandPrice, hoursInTerm float64, termYears int) float64 {
	discount := 0.75 // 25% savings for 1 year
	if termYears == 3 {
		discount = 0.70 // 30% savings for 3 years
	}
	return onDemandPrice * hoursInTerm * discount
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

// convertGCPRecommendation converts a GCP Recommender recommendation to common format
func (c *CloudStorageClient) convertGCPRecommendation(ctx context.Context, gcpRec *recommenderpb.Recommendation) *common.Recommendation {
	rec := &common.Recommendation{
		Provider:       common.ProviderGCP,
		Service:        common.ServiceStorage,
		Account:        c.projectID,
		Region:         c.region,
		CommitmentType: common.CommitmentReservedCapacity,
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
