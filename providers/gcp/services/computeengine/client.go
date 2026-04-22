// Package computeengine provides GCP Compute Engine Committed Use Discounts client
package computeengine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"cloud.google.com/go/recommender/apiv1"
	"cloud.google.com/go/recommender/apiv1/recommenderpb"
	gax "github.com/googleapis/gax-go/v2"
	"google.golang.org/api/cloudbilling/v1"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/retry"
)

// CommitmentsService interface for commitments operations (enables mocking)
type CommitmentsService interface {
	List(ctx context.Context, req *computepb.ListRegionCommitmentsRequest) CommitmentsIterator
	Insert(ctx context.Context, req *computepb.InsertRegionCommitmentRequest) (CommitmentsOperation, error)
	Close() error
}

// CommitmentsIterator interface for commitments iteration (enables mocking)
type CommitmentsIterator interface {
	Next() (*computepb.Commitment, error)
}

// CommitmentsOperation interface for commitment operations (enables mocking)
type CommitmentsOperation interface {
	Wait(ctx context.Context, opts ...gax.CallOption) error
}

// MachineTypesService interface for machine types operations (enables mocking)
type MachineTypesService interface {
	List(ctx context.Context, req *computepb.ListMachineTypesRequest) MachineTypesIterator
	Close() error
}

// MachineTypesIterator interface for machine types iteration (enables mocking)
type MachineTypesIterator interface {
	Next() (*computepb.MachineType, error)
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

// ComputeEngineClient handles GCP Compute Engine Committed Use Discounts
type ComputeEngineClient struct {
	ctx                 context.Context
	projectID           string
	region              string
	clientOpts          []option.ClientOption
	commitmentsService  CommitmentsService
	machineTypesService MachineTypesService
	billingService      BillingService
	recommenderClient   RecommenderClient
}

// NewClient creates a new GCP Compute Engine client
func NewClient(ctx context.Context, projectID, region string, opts ...option.ClientOption) (*ComputeEngineClient, error) {
	return &ComputeEngineClient{
		ctx:        ctx,
		projectID:  projectID,
		region:     region,
		clientOpts: opts,
	}, nil
}

// SetCommitmentsService sets the commitments service (for testing)
func (c *ComputeEngineClient) SetCommitmentsService(svc CommitmentsService) {
	c.commitmentsService = svc
}

// SetMachineTypesService sets the machine types service (for testing)
func (c *ComputeEngineClient) SetMachineTypesService(svc MachineTypesService) {
	c.machineTypesService = svc
}

// SetBillingService sets the billing service (for testing)
func (c *ComputeEngineClient) SetBillingService(svc BillingService) {
	c.billingService = svc
}

// SetRecommenderClient sets the recommender client (for testing)
func (c *ComputeEngineClient) SetRecommenderClient(client RecommenderClient) {
	c.recommenderClient = client
}

// realCommitmentsService wraps the real compute.RegionCommitmentsClient
type realCommitmentsService struct {
	client *compute.RegionCommitmentsClient
}

func (r *realCommitmentsService) List(ctx context.Context, req *computepb.ListRegionCommitmentsRequest) CommitmentsIterator {
	return r.client.List(ctx, req)
}

func (r *realCommitmentsService) Insert(ctx context.Context, req *computepb.InsertRegionCommitmentRequest) (CommitmentsOperation, error) {
	return r.client.Insert(ctx, req)
}

func (r *realCommitmentsService) Close() error {
	return r.client.Close()
}

// realMachineTypesService wraps the real compute.MachineTypesClient
type realMachineTypesService struct {
	client *compute.MachineTypesClient
}

func (r *realMachineTypesService) List(ctx context.Context, req *computepb.ListMachineTypesRequest) MachineTypesIterator {
	return r.client.List(ctx, req)
}

func (r *realMachineTypesService) Close() error {
	return r.client.Close()
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
func (c *ComputeEngineClient) GetServiceType() common.ServiceType {
	return common.ServiceCompute
}

// GetRegion returns the region
func (c *ComputeEngineClient) GetRegion() string {
	return c.region
}

// GetRecommendations gets CUD recommendations from GCP Recommender API
func (c *ComputeEngineClient) GetRecommendations(ctx context.Context, params common.RecommendationParams) ([]common.Recommendation, error) {
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

	// Recommender ID for GCP CUD recommendations
	parent := fmt.Sprintf("projects/%s/locations/%s/recommenders/google.billing.CostInsight.commitmentRecommender",
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
			// Iterator errors (quota, auth, transient 5xx) must propagate so
			// callers don't silently act on a partial recommendation list —
			// a missed recommendation can lead to under-committing or
			// double-purchasing. Callers should retry.
			return nil, fmt.Errorf("computeengine: iterate recommendations: %w", err)
		}

		converted := c.convertGCPRecommendation(ctx, rec)
		if converted != nil {
			recommendations = append(recommendations, *converted)
		}
	}

	return recommendations, nil
}

// GetExistingCommitments retrieves existing Compute Engine CUDs
func (c *ComputeEngineClient) GetExistingCommitments(ctx context.Context) ([]common.Commitment, error) {
	svc, err := c.createCommitmentsService(ctx)
	if err != nil {
		return nil, err
	}
	defer svc.Close()

	req := &computepb.ListRegionCommitmentsRequest{
		Project: c.projectID,
		Region:  c.region,
	}

	return c.collectCommitments(ctx, svc, req)
}

// createCommitmentsService creates a commitments service client
func (c *ComputeEngineClient) createCommitmentsService(ctx context.Context) (CommitmentsService, error) {
	// Use injected service if available (for testing)
	if c.commitmentsService != nil {
		return c.commitmentsService, nil
	}

	client, err := compute.NewRegionCommitmentsRESTClient(ctx, c.clientOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create commitments client: %w", err)
	}

	return &realCommitmentsService{client: client}, nil
}

// collectCommitments iterates through commitments and converts them to common format
func (c *ComputeEngineClient) collectCommitments(ctx context.Context, svc CommitmentsService, req *computepb.ListRegionCommitmentsRequest) ([]common.Commitment, error) {
	commitments := make([]common.Commitment, 0)

	it := svc.List(ctx, req)
	for {
		commitment, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to list commitments: %w", err)
		}

		if commitment.Name == nil {
			continue
		}

		com := c.convertGCPCommitmentToCommon(commitment)
		commitments = append(commitments, com)
	}

	return commitments, nil
}

// convertGCPCommitmentToCommon converts a GCP commitment to common format
func (c *ComputeEngineClient) convertGCPCommitmentToCommon(commitment *computepb.Commitment) common.Commitment {
	status := "unknown"
	if commitment.Status != nil {
		status = strings.ToLower(*commitment.Status)
	}

	commitmentType := common.CommitmentCUD
	if commitment.Type != nil && *commitment.Type == "GENERAL_PURPOSE" {
		commitmentType = common.CommitmentCUD
	}

	com := common.Commitment{
		Provider:       common.ProviderGCP,
		Account:        c.projectID,
		CommitmentType: commitmentType,
		Service:        common.ServiceCompute,
		Region:         c.region,
		CommitmentID:   *commitment.Name,
		State:          status,
	}

	// Extract resource type from commitment resources
	if len(commitment.Resources) > 0 {
		resource := commitment.Resources[0]
		if resource.Type != nil {
			com.ResourceType = *resource.Type
		}
	}

	return com
}

// ResourceCommitment represents a single resource within a GCP commitment.
type ResourceCommitment struct {
	Amount int64  // number of vCPUs or memory in MB
	Type   string // "VCPU" or "MEMORY_MB"
}

// CommitmentRequest represents a single GCP commitment to create.
type CommitmentRequest struct {
	Name      string // unique per region+project
	Plan      string // "TWELVE_MONTH" or "THIRTY_SIX_MONTH"
	Region    string
	Resources []ResourceCommitment
}

// GroupCommitments groups recommendations by project+region+term into CommitmentRequests.
// GCP requires both VCPU and MEMORY_MB in a single commitments.insert call.
// Each recommendation's Count is treated as vCPU count; memory is estimated at 4 GB per vCPU.
func GroupCommitments(recs []common.Recommendation) []CommitmentRequest {
	type key struct{ account, region, term string }
	type agg struct {
		vcpus int64
		plan  string
	}
	groups := make(map[key]*agg)

	for _, rec := range recs {
		if rec.Service != common.ServiceCompute || rec.Provider != common.ProviderGCP {
			continue
		}
		k := key{account: rec.Account, region: rec.Region, term: rec.Term}
		if _, ok := groups[k]; !ok {
			plan := "TWELVE_MONTH"
			if rec.Term == "3yr" || rec.Term == "3" {
				plan = "THIRTY_SIX_MONTH"
			}
			groups[k] = &agg{plan: plan}
		}
		groups[k].vcpus += int64(rec.Count)
	}

	result := make([]CommitmentRequest, 0, len(groups))
	ts := time.Now().UnixNano()
	counter := 0
	for k, a := range groups {
		result = append(result, CommitmentRequest{
			Name:   fmt.Sprintf("cud-%s-%d-%d", k.region, ts, counter),
			Plan:   a.plan,
			Region: k.region,
			Resources: []ResourceCommitment{
				{Type: "VCPU", Amount: a.vcpus},
				{Type: "MEMORY_MB", Amount: a.vcpus * 4096},
			},
		})
		counter++
	}
	return result
}

// isResourceExhausted reports whether the error represents a RESOURCE_EXHAUSTED (429) response.
func isResourceExhausted(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "ResourceExhausted") || strings.Contains(s, "RESOURCE_EXHAUSTED") || strings.Contains(s, "429")
}

// stripPermanentPrefix removes the `retry: permanent error, do not retry: `
// text added when a non-retryable SDK error is wrapped via
// fmt.Errorf("%w: <message>: %w", retry.ErrPermanent, sdkErr). The original
// SDK error remains in the chain (errors.Is/As still work) but the
// user-facing message no longer leaks the retry sentinel. Errors that don't
// carry the sentinel are returned unchanged.
func stripPermanentPrefix(err error) error {
	if err == nil {
		return nil
	}
	if !errors.Is(err, retry.ErrPermanent) {
		return err
	}
	prefix := retry.ErrPermanent.Error() + ": "
	if msg := err.Error(); strings.HasPrefix(msg, prefix) {
		// Build a new error with the trimmed message but preserve unwrap
		// chain access to the underlying SDK error.
		return errors.Join(errors.New(msg[len(prefix):]), unwrapNonSentinel(err))
	}
	return err
}

// unwrapNonSentinel returns the first non-ErrPermanent error in a multi-%w
// chain. Used to keep errors.Is/As access to the SDK error after we strip
// the user-facing sentinel prefix.
func unwrapNonSentinel(err error) error {
	if mw, ok := err.(interface{ Unwrap() []error }); ok {
		for _, inner := range mw.Unwrap() {
			if !errors.Is(inner, retry.ErrPermanent) {
				return inner
			}
		}
	}
	return nil
}

// PurchaseCommitment purchases a Compute Engine CUD
func (c *ComputeEngineClient) PurchaseCommitment(ctx context.Context, rec common.Recommendation, opts common.PurchaseOptions) (common.PurchaseResult, error) {
	result := common.PurchaseResult{
		Recommendation: rec,
		DryRun:         false,
		Success:        false,
		Timestamp:      time.Now(),
	}

	// Use injected service if available (for testing)
	var svc CommitmentsService
	if c.commitmentsService != nil {
		svc = c.commitmentsService
	} else {
		client, err := compute.NewRegionCommitmentsRESTClient(ctx, c.clientOpts...)
		if err != nil {
			result.Error = fmt.Errorf("failed to create commitments client: %w", err)
			return result, result.Error
		}
		svc = &realCommitmentsService{client: client}
	}
	defer svc.Close()

	// Determine plan based on term
	plan := "TWELVE_MONTH"
	if rec.Term == "3yr" || rec.Term == "3" {
		plan = "THIRTY_SIX_MONTH"
	}

	// GCP requires both VCPU and MEMORY_MB in a single commitment insert.
	commitment := &computepb.Commitment{
		Name:        stringPtr(fmt.Sprintf("cud-%d", time.Now().Unix())),
		Plan:        stringPtr(plan),
		Type:        stringPtr("GENERAL_PURPOSE"),
		Description: stringPtr(fmt.Sprintf("CUD for %s", rec.ResourceType)),
		Resources: []*computepb.ResourceCommitment{
			{
				Type:   stringPtr("VCPU"),
				Amount: int64Ptr(int64(rec.Count)),
			},
			{
				Type:   stringPtr("MEMORY_MB"),
				Amount: int64Ptr(int64(rec.Count) * 4096),
			},
		},
	}

	insertReq := &computepb.InsertRegionCommitmentRequest{
		Project:            c.projectID,
		Region:             c.region,
		CommitmentResource: commitment,
	}

	// Exponential backoff on RESOURCE_EXHAUSTED: BaseDelay 1s with 2× growth
	// capped at MaxDelay 4s gives the same 1s/2s/4s sequence the open-coded
	// loop produced (max 4 attempts = original + 3 retries). Non-retryable
	// SDK errors are wrapped with retry.ErrPermanent so the helper short-
	// circuits without consuming the retry budget.
	cfg := retry.Config{
		MaxAttempts: 4,
		BaseDelay:   time.Second,
		MaxDelay:    4 * time.Second,
	}
	doErr := retry.Do(ctx, cfg, func(perAttemptCtx context.Context, _ int) error {
		op, err := svc.Insert(perAttemptCtx, insertReq)
		if err != nil {
			if isResourceExhausted(err) {
				return fmt.Errorf("failed to create commitment: %w", err) // retryable
			}
			return fmt.Errorf("%w: failed to create commitment: %w", retry.ErrPermanent, err)
		}
		if err := op.Wait(perAttemptCtx); err != nil {
			// Wait failures aren't quota-related — don't retry.
			return fmt.Errorf("%w: commitment creation failed: %w", retry.ErrPermanent, err)
		}
		return nil
	})
	if doErr != nil {
		// Strip the `retry: permanent error, do not retry: ` prefix so the
		// user-facing message matches the pre-refactor shape, while keeping
		// the original SDK error reachable via errors.Is/As.
		result.Error = stripPermanentPrefix(doErr)
		return result, result.Error
	}

	result.Success = true
	result.CommitmentID = *commitment.Name
	result.Cost = rec.CommitmentCost

	return result, nil
}

// ValidateOffering validates that a machine type exists
func (c *ComputeEngineClient) ValidateOffering(ctx context.Context, rec common.Recommendation) error {
	validTypes, err := c.GetValidResourceTypes(ctx)
	if err != nil {
		return fmt.Errorf("failed to get valid machine types: %w", err)
	}

	for _, machineType := range validTypes {
		if machineType == rec.ResourceType {
			return nil
		}
	}

	return fmt.Errorf("invalid GCP machine type: %s", rec.ResourceType)
}

// GetOfferingDetails retrieves CUD offering details from GCP Billing API
func (c *ComputeEngineClient) GetOfferingDetails(ctx context.Context, rec common.Recommendation) (*common.OfferingDetails, error) {
	termYears := 1
	if rec.Term == "3yr" || rec.Term == "3" {
		termYears = 3
	}

	pricing, err := c.getComputePricing(ctx, rec.ResourceType, c.region, termYears)
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
		OfferingID:          fmt.Sprintf("gcp-compute-%s-%s-%s", rec.ResourceType, c.region, rec.Term),
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

// GetValidResourceTypes returns valid machine types from GCP Compute API
func (c *ComputeEngineClient) GetValidResourceTypes(ctx context.Context) ([]string, error) {
	// Use injected service if available (for testing)
	var svc MachineTypesService
	if c.machineTypesService != nil {
		svc = c.machineTypesService
	} else {
		client, err := compute.NewMachineTypesRESTClient(ctx, c.clientOpts...)
		if err != nil {
			return nil, fmt.Errorf("failed to create machine types client: %w", err)
		}
		svc = &realMachineTypesService{client: client}
	}
	defer svc.Close()

	req := &computepb.ListMachineTypesRequest{
		Project: c.projectID,
		Zone:    c.region + "-a", // Use zone a for the region
	}

	machineTypes := make([]string, 0)
	it := svc.List(ctx, req)

	for {
		machineType, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to list machine types: %w", err)
		}

		if machineType.Name != nil {
			machineTypes = append(machineTypes, *machineType.Name)
		}
	}

	if len(machineTypes) == 0 {
		return nil, fmt.Errorf("no machine types found for region %s", c.region)
	}

	return machineTypes, nil
}

// ComputePricing contains pricing information for Compute Engine
type ComputePricing struct {
	HourlyRate        float64
	CommitmentPrice   float64
	OnDemandPrice     float64
	Currency          string
	SavingsPercentage float64
}

// getComputePricing gets pricing from GCP Cloud Billing Catalog API
func (c *ComputeEngineClient) getComputePricing(ctx context.Context, machineType, region string, termYears int) (*ComputePricing, error) {
	svc, err := c.getOrCreateBillingService(ctx)
	if err != nil {
		return nil, err
	}

	skus, err := svc.ListSKUs("services/6F81-5844-456A")
	if err != nil {
		return nil, fmt.Errorf("failed to list SKUs: %w", err)
	}

	onDemandPrice, commitmentPrice, currency := extractComputePricingFromSKUs(skus.Skus, machineType, region)
	if onDemandPrice == 0 {
		return nil, fmt.Errorf("no on-demand pricing found for machine type %s", machineType)
	}

	hoursInTerm := 8760.0 * float64(termYears)
	if commitmentPrice == 0 {
		commitmentPrice = estimateComputeCommitmentPrice(onDemandPrice, hoursInTerm, termYears)
	}

	savingsPercentage := calculateComputeSavingsPercentage(onDemandPrice, hoursInTerm, commitmentPrice)

	return &ComputePricing{
		HourlyRate:        commitmentPrice / hoursInTerm,
		CommitmentPrice:   commitmentPrice,
		OnDemandPrice:     onDemandPrice * hoursInTerm,
		Currency:          currency,
		SavingsPercentage: savingsPercentage,
	}, nil
}

// getOrCreateBillingService returns the billing service, creating it if needed
func (c *ComputeEngineClient) getOrCreateBillingService(ctx context.Context) (BillingService, error) {
	if c.billingService != nil {
		return c.billingService, nil
	}

	service, err := cloudbilling.NewService(ctx, c.clientOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create billing service: %w", err)
	}

	return &realBillingService{service: service}, nil
}

// extractComputePricingFromSKUs extracts on-demand and commitment pricing from SKU list
func extractComputePricingFromSKUs(skus []*cloudbilling.Sku, machineType, region string) (onDemand, commitment float64, currency string) {
	currency = "USD"

	for _, sku := range skus {
		if !skuMatchesMachineType(sku, machineType, region) {
			continue
		}

		price, curr := extractComputePriceFromSKU(sku)
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

// extractComputePriceFromSKU extracts the unit price from a SKU
func extractComputePriceFromSKU(sku *cloudbilling.Sku) (float64, string) {
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

// estimateComputeCommitmentPrice estimates commitment price based on GCP CUD discounts
func estimateComputeCommitmentPrice(onDemandPrice, hoursInTerm float64, termYears int) float64 {
	discount := 0.63 // 37% savings for 1 year
	if termYears == 3 {
		discount = 0.45 // 55% savings for 3 years
	}
	return onDemandPrice * hoursInTerm * discount
}

// calculateComputeSavingsPercentage calculates the savings percentage
func calculateComputeSavingsPercentage(onDemandPrice, hoursInTerm, commitmentPrice float64) float64 {
	onDemandTotal := onDemandPrice * hoursInTerm
	return ((onDemandTotal - commitmentPrice) / onDemandTotal) * 100
}

// skuMatchesMachineType checks if a SKU matches the machine type and region
func skuMatchesMachineType(sku *cloudbilling.Sku, machineType, region string) bool {
	// Check if the SKU description contains the machine type
	if !strings.Contains(strings.ToLower(sku.Description), strings.ToLower(machineType)) {
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
func (c *ComputeEngineClient) convertGCPRecommendation(ctx context.Context, gcpRec *recommenderpb.Recommendation) *common.Recommendation {
	rec := &common.Recommendation{
		Provider:       common.ProviderGCP,
		Service:        common.ServiceCompute,
		Account:        c.projectID,
		Region:         c.region,
		CommitmentType: common.CommitmentCUD,
		Timestamp:      time.Now(),
		Term:           "1yr",
		PaymentOption:  "upfront",
	}

	extractResourceTypeFromRecommendation(gcpRec, rec)
	extractCostImpactFromRecommendation(gcpRec, rec)

	return rec
}

// extractResourceTypeFromRecommendation extracts the resource type from a GCP recommendation
func extractResourceTypeFromRecommendation(gcpRec *recommenderpb.Recommendation, rec *common.Recommendation) {
	if gcpRec.Content == nil || gcpRec.Content.OperationGroups == nil {
		return
	}

	for _, opGroup := range gcpRec.Content.OperationGroups {
		if resourceType := extractResourceTypeFromOperations(opGroup.Operations); resourceType != "" {
			rec.ResourceType = resourceType
			return
		}
	}
}

// extractResourceTypeFromOperations extracts resource type from operation list
func extractResourceTypeFromOperations(operations []*recommenderpb.Operation) string {
	for _, op := range operations {
		if op.Resource != "" {
			// Extract machine type from resource path
			parts := strings.Split(op.Resource, "/")
			if len(parts) > 0 {
				return parts[len(parts)-1]
			}
		}
	}
	return ""
}

// extractCostImpactFromRecommendation extracts the cost impact from a GCP recommendation
func extractCostImpactFromRecommendation(gcpRec *recommenderpb.Recommendation, rec *common.Recommendation) {
	if gcpRec.PrimaryImpact == nil {
		return
	}

	costProj := gcpRec.PrimaryImpact.GetCostProjection()
	if costProj == nil || costProj.Cost == nil {
		return
	}

	cost := costProj.Cost
	if cost.Units != 0 || cost.Nanos != 0 {
		// Cost savings is negative of cost projection
		savings := -(float64(cost.Units) + float64(cost.Nanos)/1e9)
		rec.EstimatedSavings = savings
	}
}

// Helper functions
func stringPtr(s string) *string {
	return &s
}

func int64Ptr(i int64) *int64 {
	return &i
}
