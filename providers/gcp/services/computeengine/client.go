// Package computeengine provides GCP Compute Engine Committed Use Discounts client
package computeengine

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"cloud.google.com/go/recommender/apiv1"
	"cloud.google.com/go/recommender/apiv1/recommenderpb"
	gax "github.com/googleapis/gax-go/v2"
	"google.golang.org/api/cloudbilling/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/retry"
)

// maxRecsPages caps GCP Recommender API iteration to avoid burning a Lambda
// deadline on a stalled or unexpectedly large result set.
const maxRecsPages = 20

// maxCommitmentsPages caps GCP committed-use discount iteration.
const maxCommitmentsPages = 50

// maxMachineTypeItems caps GCP machine types iteration (one item per Next() call).
const maxMachineTypeItems = 20


// termPlan converts a commitment term string to the canonical GCP Compute API
// commitment plan value derived from the SDK enum constants.
//
// Accepted forms:
//   - 1-year: "1yr", "1", "12mo"
//   - 3-year: "3yr", "3", "36mo"
//
// An empty or unrecognised term returns an error rather than silently defaulting
// to 12 months; a silent mis-default can purchase the wrong term and waste money.
func termPlan(term string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(term)) {
	case "3yr", "3", "36mo":
		return computepb.Commitment_THIRTY_SIX_MONTH.String(), nil
	case "1yr", "1", "12mo":
		return computepb.Commitment_TWELVE_MONTH.String(), nil
	default:
		return "", fmt.Errorf("termPlan: unrecognised commitment term %q (accepted: 1yr/1/12mo or 3yr/3/36mo)", term)
	}
}

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
	for pageIdx := 0; ; pageIdx++ {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("context cancelled during pagination: %w", err)
		}
		if pageIdx >= maxRecsPages {
			return nil, fmt.Errorf("computeengine: GetRecommendations iteration cap (%d items) reached", maxRecsPages)
		}
		rec, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			// Iterator errors (quota, auth, transient 5xx) must propagate so
			// callers don't silently act on a partial recommendation list --
			// a missed recommendation can lead to under-committing or
			// double-purchasing. Callers should retry.
			return nil, fmt.Errorf("computeengine: iterate recommendations: %w", err)
		}

		// Skip non-ACTIVE recommendations (CLAIMED/SUCCEEDED/FAILED/DISMISSED).
		// The GCP Recommender returns all states unless filtered at the API layer;
		// acting on an already-CLAIMED or SUCCEEDED recommendation is a
		// cross-run double-purchase vector and inflates actionable rec counts.
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
	for pageIdx := 0; ; pageIdx++ {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("context cancelled during pagination: %w", err)
		}
		if pageIdx >= maxCommitmentsPages {
			return nil, fmt.Errorf("computeengine: GetExistingCommitments iteration cap (%d items) reached", maxCommitmentsPages)
		}
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

	// All commitment types (GENERAL_PURPOSE, ACCELERATOR) map to CommitmentCUD
	// for the purposes of the common layer. The if-branch checking for
	// "GENERAL_PURPOSE" was a no-op (both arms assigned CommitmentCUD) and
	// is removed (10-L1).
	commitmentType := common.CommitmentCUD

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
	Type   string // GCP ResourceCommitment.Type enum: "VCPU" or "MEMORY"
}

// CommitmentRequest represents a single GCP commitment to create.
type CommitmentRequest struct {
	Name      string // unique per region+project
	Plan      string // "TWELVE_MONTH" or "THIRTY_SIX_MONTH"
	Region    string
	Resources []ResourceCommitment
}

// GroupCommitments groups recommendations by project+region+term into CommitmentRequests.
// GCP requires both a VCPU and a MEMORY resource (memory Amount in MB) in a
// single commitments.insert call.
// Each recommendation's Count is treated as vCPU count; the memory Amount is
// read from ComputeDetails.MemoryGB (populated by extractMemoryMBFromRecommendation).
// Recommendations with an unrecognised term or missing memory are skipped with a
// log warning so a bad rec never contaminates an otherwise valid group.
func GroupCommitments(recs []common.Recommendation) []CommitmentRequest {
	type key struct{ account, region, term string }
	type agg struct {
		vcpus    int64
		memoryMB int64
		plan     string
	}
	groups := make(map[key]*agg)

	for _, rec := range recs {
		if rec.Service != common.ServiceCompute || rec.Provider != common.ProviderGCP {
			continue
		}
		plan, err := termPlan(rec.Term)
		if err != nil {
			log.Printf("GroupCommitments: skipping recommendation with unrecognised term %q: %v", rec.Term, err)
			continue
		}
		recMemMB, err := memoryMBFromDetails(rec)
		if err != nil {
			log.Printf("GroupCommitments: skipping recommendation missing memory amount: %v", err)
			continue
		}
		k := key{account: rec.Account, region: rec.Region, term: rec.Term}
		if _, ok := groups[k]; !ok {
			groups[k] = &agg{plan: plan}
		}
		groups[k].vcpus += int64(rec.Count)
		groups[k].memoryMB += recMemMB
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
				// "MEMORY" is the GCP ResourceCommitment.Type enum member; the
				// Amount is in MB (see buildInsertRequest, issue #1022).
				{Type: "MEMORY", Amount: a.memoryMB},
			},
		})
		counter++
	}
	return result
}

// isResourceExhausted reports whether the error represents a RESOURCE_EXHAUSTED
// (quota / 429) response. Uses typed checks first: errors.As for REST API errors
// (*googleapi.Error with Code 429) and status.FromError for gRPC errors
// (codes.ResourceExhausted). Falls back to string matching only for error types
// that neither unwraps as a *googleapi.Error nor as a gRPC status (10-M3).
func isResourceExhausted(err error) bool {
	if err == nil {
		return false
	}
	// REST path: *googleapi.Error wraps HTTP 429 Too Many Requests.
	var gapiErr *googleapi.Error
	if errors.As(err, &gapiErr) {
		return gapiErr.Code == 429
	}
	// gRPC path: RESOURCE_EXHAUSTED maps to quota-exceeded responses from
	// Google Cloud APIs served over gRPC (e.g. quota for Recommender).
	if st, ok := status.FromError(err); ok {
		return st.Code() == codes.ResourceExhausted
	}
	// Fallback for wrapped/non-standard errors that carry the signal as text.
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

	insertReq, commitmentName, buildErr := c.buildInsertRequest(rec, opts)
	if buildErr != nil {
		result.Error = buildErr
		return result, buildErr
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
	result.CommitmentID = commitmentName
	result.Cost = rec.CommitmentCost

	return result, nil
}

// buildInsertRequest assembles the RegionCommitments.Insert request for a
// purchase, threading opts.IdempotencyToken into both GCP idempotency levers so
// a re-drive of the same execution cannot create a second CUD (issue #654, the
// financial double-buy). It returns the request, the commitment name (used as
// the resulting CommitmentID), and an error when rec.Count <= 0 (which would
// produce a zero-vCPU / zero-MB commitment that GCP rejects or silently wastes).
//
//   - RequestId is GCP's native server-side idempotency key on Insert, which the
//     API documents as preventing clients from accidentally creating duplicate
//     commitments. It MUST be a valid non-zero UUID, so we format the token (a
//     SHA-256 hex digest) into a deterministic canonical UUID via
//     common.IdempotencyGUID — the same mechanism PR #653 used for the Azure
//     reservationOrderID. The same token always yields the same RequestId, so a
//     second Insert is a server-side no-op rather than a new purchase.
//   - Name is also derived from the token as defense in depth: commitment names
//     are unique per project+region, so a re-drive that somehow reached Insert
//     (e.g. RequestId expired) collides on the name and GCP rejects it with
//     ALREADY_EXISTS instead of creating a duplicate.
//
// An empty token preserves the prior non-idempotent timestamp-based name (the
// CLI path, which has no owning execution). The token is masked in logs via
// common.MaskToken and never logged verbatim.
func (c *ComputeEngineClient) buildInsertRequest(rec common.Recommendation, opts common.PurchaseOptions) (*computepb.InsertRegionCommitmentRequest, string, error) {
	if rec.Count <= 0 {
		return nil, "", fmt.Errorf("buildInsertRequest: rec.Count must be > 0 (got %d); a zero-vCPU commitment is invalid (issue #1022)", rec.Count)
	}

	plan, err := termPlan(rec.Term)
	if err != nil {
		return nil, "", fmt.Errorf("buildInsertRequest: %w", err)
	}

	// GCP's computepb.Commitment has no Labels field, and the RegionCommitments
	// client exposes no SetLabels call -- CUDs cannot be tagged or labeled via
	// the API. Encode the source into Description so customers can still filter
	// with `gcloud compute commitments list --filter="description:..."`.
	description := fmt.Sprintf("CUD for %s", rec.ResourceType)
	if opts.Source != "" {
		description = fmt.Sprintf("%s [%s=%s]", description, common.PurchaseTagKey, opts.Source)
	}

	commitmentName := idempotentCommitmentName(opts.IdempotencyToken)

	// Derive the memory Amount from the Recommender payload (stored in
	// ComputeDetails.MemoryGB by extractMemoryMBFromRecommendation). Using the
	// real payload value is required for non-standard families (e.g. high-memory
	// N2 uses 6 GB/vCPU rather than the GENERAL_PURPOSE 4 GB). We fail loud when
	// the payload omits memory rather than silently falling back to a ratio that
	// would produce an incorrect commitment for any non-standard family.
	memMB, err := memoryMBFromDetails(rec)
	if err != nil {
		return nil, "", err
	}

	// GCP requires both a VCPU and a MEMORY resource (memory Amount in MB) in a
	// single commitment insert.
	commitment := &computepb.Commitment{
		Name:        stringPtr(commitmentName),
		Plan:        stringPtr(plan),
		Type:        stringPtr("GENERAL_PURPOSE"),
		Description: stringPtr(description),
		Resources: []*computepb.ResourceCommitment{
			{
				Type:   stringPtr("VCPU"),
				Amount: int64Ptr(int64(rec.Count)),
			},
			{
				// GCP's ResourceCommitment.Type enum is VCPU/MEMORY/LOCAL_SSD/
				// ACCELERATOR; the memory member is "MEMORY" (the Amount is still in
				// MB). Sending "MEMORY_MB" is an invalid enum value and GCP rejects
				// the commitments.insert, failing the purchase (issue #1022).
				Type:   stringPtr("MEMORY"),
				Amount: int64Ptr(memMB),
			},
		},
	}

	insertReq := &computepb.InsertRegionCommitmentRequest{
		Project:            c.projectID,
		Region:             c.region,
		CommitmentResource: commitment,
	}
	if requestID := common.IdempotencyGUID(opts.IdempotencyToken); requestID != "" {
		insertReq.RequestId = stringPtr(requestID)
		log.Printf("GCP CUD purchase using idempotent request ID for token %s (commitment %s); a re-drive will not double-purchase (issue #654)",
			common.MaskToken(opts.IdempotencyToken), commitmentName)
	}

	return insertReq, commitmentName, nil
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

	for itemIdx := 0; ; itemIdx++ {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("context cancelled during pagination: %w", err)
		}
		if itemIdx >= maxMachineTypeItems {
			return nil, fmt.Errorf("computeengine: GetValidResourceTypes iteration cap (%d items) reached", maxMachineTypeItems)
		}
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

// getComputePricing gets pricing from GCP Cloud Billing Catalog API.
// It returns an error when commitment pricing is absent from the catalog rather
// than fabricating a price from a hardcoded discount factor (issue #1020).
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
	if commitmentPrice == 0 {
		return nil, fmt.Errorf("no commitment pricing found for machine type %s in region %s: catalog has no CUD SKU; cannot compute savings percentage", machineType, region)
	}

	hoursInTerm := 8760.0 * float64(termYears)
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

// convertGCPRecommendation converts a GCP Recommender recommendation to common format.
// It also calls getComputePricing to fill CommitmentCost/OnDemandCost/SavingsPercentage/
// BreakEvenMonths so the scorer can filter and rank GCP recommendations correctly
// (issue #1022 C2). Pricing failures are logged but do not discard the recommendation:
// EstimatedSavings from the Recommender payload is the authoritative savings signal.
// Returns nil when the params.Term is unrecognised so the caller skips an
// unroutable recommendation rather than queuing a purchase with an invalid plan.
func (c *ComputeEngineClient) convertGCPRecommendation(ctx context.Context, gcpRec *recommenderpb.Recommendation, params common.RecommendationParams) *common.Recommendation {
	// GCP CUDs are billed monthly with no upfront option; force "monthly"
	// unconditionally and log any non-monthly input so scheduler
	// misconfiguration is visible. Supersedes the monthly stamp introduced
	// in PR #829 (which stamped "monthly" in the purchase body but not in
	// the converter default -- see PR #1047 for the supersession note).
	if params.PaymentOption != "" && !strings.EqualFold(params.PaymentOption, "monthly") {
		log.Printf("computeengine: unsupported GCP payment option %q; forcing monthly", params.PaymentOption)
	}
	paymentOption := "monthly"

	// H-3: propagate params.Term (default "1yr") and validate it so an
	// unrecognised term fails loud here rather than reaching buildInsertRequest.
	term := params.Term
	if term == "" {
		term = "1yr"
	}
	if _, err := termPlan(term); err != nil {
		log.Printf("computeengine: skipping recommendation with unrecognised term %q: %v", term, err)
		return nil
	}

	rec := &common.Recommendation{
		Provider:       common.ProviderGCP,
		Service:        common.ServiceCompute,
		Account:        c.projectID,
		Region:         c.region,
		CommitmentType: common.CommitmentCUD,
		Timestamp:      time.Now(),
		Term:           term,
		PaymentOption:  paymentOption,
	}

	extractResourceTypeFromRecommendation(gcpRec, rec)
	extractCostImpactFromRecommendation(gcpRec, rec)
	extractVCPUCountFromRecommendation(gcpRec, rec)
	extractMemoryMBFromRecommendation(gcpRec, rec)

	// Thread pricing into the converter so the scorer can rank/filter GCP recs
	// correctly (issue #1022 C2). If the billing catalog lacks a commitment SKU
	// we propagate the error as a log line rather than dropping the rec entirely
	// -- the Recommender-derived EstimatedSavings is still valid.
	if rec.ResourceType != "" {
		termYears := 1
		if rec.Term == "3yr" || rec.Term == "3" {
			termYears = 3
		}
		if pricing, err := c.getComputePricing(ctx, rec.ResourceType, c.region, termYears); err != nil {
			log.Printf("computeengine: pricing unavailable for %s in %s (issue #1020): %v", rec.ResourceType, c.region, err)
		} else {
			rec.CommitmentCost = pricing.CommitmentPrice
			rec.OnDemandCost = pricing.OnDemandPrice
			rec.SavingsPercentage = pricing.SavingsPercentage
			// BreakEvenMonths: months of accrued savings required to cover the
			// commitment cost (conservative: treats the whole period cost as sunk,
			// which is accurate for all-upfront and overestimates for monthly CUDs).
			// monthlySavings = monthly spend difference between on-demand and CUD.
			if pricing.OnDemandPrice > 0 && pricing.SavingsPercentage > 0 {
				monthlySavings := pricing.OnDemandPrice * pricing.SavingsPercentage / 100.0 / float64(termYears*12)
				if monthlySavings > 0 {
					rec.BreakEvenMonths = pricing.CommitmentPrice / monthlySavings
				}
			}
		}
	}

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
		// Cost savings is the negative of the cost projection impact.
		// GCP Recommender sizes CUD recommendations for 100% coverage of
		// the project's historical on-demand usage; this is the 100%-coverage
		// baseline the dashboard scaler in summarizeRecommendationsWithCoverage
		// depends on (issue #215 audit).
		savings := -(float64(cost.Units) + float64(cost.Nanos)/1e9)
		rec.EstimatedSavings = savings
	}
}

// isMemoryAmountOp returns true when op's path_filters indicate a memory
// resource type. Used by extractVCPUCountFromRecommendation to skip the memory
// sibling of the VCPU operation in a GCP commitment resource operation group.
// Matches both "MEMORY" (the canonical ResourceCommitment.Type enum member) and
// the legacy "MEMORY_MB" spelling, since the Recommender's path_filter encoding
// is not contractually documented (issue #1022).
func isMemoryAmountOp(op *recommenderpb.Operation) bool {
	for filterKey, filterVal := range op.GetPathFilters() {
		if !strings.Contains(strings.ToLower(filterKey), "type") {
			continue
		}
		if sv, ok := filterVal.GetKind().(*structpb.Value_StringValue); ok {
			if strings.EqualFold(sv.StringValue, "MEMORY") || strings.EqualFold(sv.StringValue, "MEMORY_MB") {
				return true
			}
		}
	}
	return false
}

// vcpuCountFromOperationGroups walks the commitment operation groups and
// returns the VCPU count encoded in the operation's numeric value, or 0 if
// none is found. Extracted from extractVCPUCountFromRecommendation to keep
// cyclomatic complexity in check.
func vcpuCountFromOperationGroups(content *recommenderpb.RecommendationContent) int {
	for _, opGroup := range content.GetOperationGroups() {
		for _, op := range opGroup.GetOperations() {
			if !strings.Contains(strings.ToLower(op.GetResourceType()), "commitment") {
				continue
			}
			if !strings.Contains(strings.ToLower(op.GetPath()), "amount") {
				continue
			}
			if isMemoryAmountOp(op) {
				continue
			}
			if v := op.GetValue(); v != nil {
				if nv, ok := v.GetKind().(*structpb.Value_NumberValue); ok && nv.NumberValue > 0 {
					return int(nv.NumberValue)
				}
			}
		}
	}
	return 0
}

// extractVCPUCountFromRecommendation extracts the recommended vCPU count from a
// GCP Commitment Recommender response (issue #1022 C1).
//
// The Recommender encodes the commitment resource amounts in two places:
//   - Operation.Value: a structpb.Value whose numeric value is the amount, with
//     Operation.Path indicating the resource type (e.g. "/resources/0/amount").
//     Operations with ResourceType "compute.googleapis.com/Commitment" and a path
//     containing "amount" carry the VCPU count; the sibling MEMORY amount is
//     extracted by extractMemoryMBFromRecommendation.
//   - RecommendationContent.Overview: a JSON struct with a "numericValue" field.
//
// We prefer the operation-value path because it is structured and unambiguous.
// If no VCPU operation is found we fall back to the overview's numericValue.
func extractVCPUCountFromRecommendation(gcpRec *recommenderpb.Recommendation, rec *common.Recommendation) {
	if gcpRec.Content == nil {
		return
	}

	if count := vcpuCountFromOperationGroups(gcpRec.Content); count > 0 {
		rec.Count = count
		return
	}

	// Fallback: overview numericValue (used by older recommender versions).
	if gcpRec.Content.GetOverview() != nil {
		if nv := gcpRec.Content.GetOverview().GetFields()["numericValue"]; nv != nil {
			if count := nv.GetNumberValue(); count > 0 {
				rec.Count = int(count)
			}
		}
	}
}

// memoryMBFromOperationGroups walks the commitment operation groups and returns
// the MEMORY resource amount (in MB) encoded in the operation's numeric value,
// or 0 if none is found. It is the sibling of vcpuCountFromOperationGroups:
// both look at "compute.googleapis.com/Commitment" operations whose Path
// contains "amount", but this function selects only those where isMemoryAmountOp
// returns true (i.e. the path_filter type is MEMORY or MEMORY_MB).
func memoryMBFromOperationGroups(content *recommenderpb.RecommendationContent) int64 {
	for _, opGroup := range content.GetOperationGroups() {
		for _, op := range opGroup.GetOperations() {
			if !strings.Contains(strings.ToLower(op.GetResourceType()), "commitment") {
				continue
			}
			if !strings.Contains(strings.ToLower(op.GetPath()), "amount") {
				continue
			}
			if !isMemoryAmountOp(op) {
				continue
			}
			if v := op.GetValue(); v != nil {
				if nv, ok := v.GetKind().(*structpb.Value_NumberValue); ok && nv.NumberValue > 0 {
					return int64(nv.NumberValue)
				}
			}
		}
	}
	return 0
}

// extractMemoryMBFromRecommendation extracts the MEMORY resource amount (in MB)
// from the Recommender payload and stores it in rec.Details as ComputeDetails.MemoryGB.
// This allows buildInsertRequest to use the payload-sourced amount rather than the
// general-purpose ratio approximation. No-op when the payload carries no MEMORY op.
func extractMemoryMBFromRecommendation(gcpRec *recommenderpb.Recommendation, rec *common.Recommendation) {
	if gcpRec.Content == nil {
		return
	}
	memMB := memoryMBFromOperationGroups(gcpRec.Content)
	if memMB <= 0 {
		return
	}
	// Store as ComputeDetails.MemoryGB (MB -> GB conversion). GCP memory amounts
	// are always whole multiples of at least 256 MB, so the float64 roundtrip is
	// exact: memMB / 1024 * 1024 == memMB for any value divisible by 1.
	rec.Details = common.ComputeDetails{MemoryGB: float64(memMB) / 1024.0}
}

// memoryMBFromDetails reads the MEMORY amount (in MB) from rec.Details when it
// was populated by extractMemoryMBFromRecommendation. Returns an error when the
// field is absent or zero -- callers must not silently fall back to a ratio.
func memoryMBFromDetails(rec common.Recommendation) (int64, error) {
	if cd, ok := rec.Details.(common.ComputeDetails); ok && cd.MemoryGB > 0 {
		return int64(cd.MemoryGB * 1024), nil
	}
	return 0, fmt.Errorf("memoryMBFromDetails: MEMORY resource amount absent from recommendation Details (no MEMORY op in Recommender payload); cannot build CUD insert without explicit memory")
}

// idempotentNameTokenLen is how many leading hex characters of the idempotency
// token are folded into the derived commitment name. A GCP commitment name must
// match RFC1035 (1-63 chars, lowercase [a-z]([-a-z0-9]*[a-z0-9])?); the "cud-"
// prefix is 4 chars, so 32 hex chars (128 bits, collision-free at any realistic
// volume) keeps the result at 36 chars, well under the 63-char limit.
const idempotentNameTokenLen = 32

// idempotentCommitmentName derives a deterministic, RFC1035-valid GCP commitment
// name from an idempotency token (issue #654). The same token always yields the
// same name, so a re-drive collides on the unique-per-project+region name and
// GCP rejects the duplicate with ALREADY_EXISTS instead of creating a second
// CUD. An empty token preserves the prior non-idempotent timestamp-based name
// (the CLI path, which has no owning execution).
//
// The token is a lowercase SHA-256 hex digest, so the leading chars are already
// valid RFC1035 name characters and need no further sanitisation.
func idempotentCommitmentName(token string) string {
	if token == "" {
		return fmt.Sprintf("cud-%d", time.Now().Unix())
	}
	t := strings.ToLower(token)
	if len(t) > idempotentNameTokenLen {
		t = t[:idempotentNameTokenLen]
	}
	return "cud-" + t
}

// Helper functions
func stringPtr(s string) *string {
	return &s
}

func int64Ptr(i int64) *int64 {
	return &i
}
