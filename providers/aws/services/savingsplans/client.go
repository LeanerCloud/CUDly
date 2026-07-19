// Package savingsplans provides AWS Savings Plans purchase client
package savingsplans

import (
	"context"
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/savingsplans"
	"github.com/aws/aws-sdk-go-v2/service/savingsplans/types"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/providers/aws/internal/purchasecfg"
)

// SavingsPlansAPI defines the interface for Savings Plans operations (enables mocking)
type SavingsPlansAPI interface {
	CreateSavingsPlan(ctx context.Context, params *savingsplans.CreateSavingsPlanInput, optFns ...func(*savingsplans.Options)) (*savingsplans.CreateSavingsPlanOutput, error)
	DescribeSavingsPlans(ctx context.Context, params *savingsplans.DescribeSavingsPlansInput, optFns ...func(*savingsplans.Options)) (*savingsplans.DescribeSavingsPlansOutput, error)
	DescribeSavingsPlansOfferings(ctx context.Context, params *savingsplans.DescribeSavingsPlansOfferingsInput, optFns ...func(*savingsplans.Options)) (*savingsplans.DescribeSavingsPlansOfferingsOutput, error)
	DescribeSavingsPlansOfferingRates(ctx context.Context, params *savingsplans.DescribeSavingsPlansOfferingRatesInput, optFns ...func(*savingsplans.Options)) (*savingsplans.DescribeSavingsPlansOfferingRatesOutput, error)
}

// Client handles AWS Savings Plans, scoped to one plan type. Each plan type
// (Compute, EC2Instance, SageMaker, Database) has its own term/payment defaults
// in ServiceConfig, so the client is constructed once per plan type and tags
// the recommendations and existing commitments it returns with the matching
// per-plan-type ServiceType slug.
type Client struct {
	client   SavingsPlansAPI
	region   string
	planType types.SavingsPlanType
}

// NewClient creates a new Savings Plans client scoped to one plan type with
// purchase-path retry/timeout settings. The plan type determines which slug
// GetServiceType returns and which commitments GetExistingCommitments includes.
// See purchasecfg for retry rationale.
func NewClient(cfg aws.Config, planType types.SavingsPlanType) *Client {
	pcfg := purchasecfg.NewConfig(cfg)
	return &Client{
		client:   savingsplans.NewFromConfig(pcfg),
		region:   cfg.Region,
		planType: planType,
	}
}

// SetSavingsPlansAPI sets a custom Savings Plans API client (for testing)
func (c *Client) SetSavingsPlansAPI(api SavingsPlansAPI) {
	c.client = api
}

// GetServiceType returns the per-plan-type service slug (e.g.
// ServiceSavingsPlansCompute for a client constructed with
// SavingsPlanTypeCompute). Falls back to the legacy umbrella constant if the
// plan type is unrecognised — that branch should be unreachable in practice.
func (c *Client) GetServiceType() common.ServiceType {
	return ServiceTypeForPlanType(c.planType)
}

// ServiceTypeForPlanType maps an AWS Savings Plans API plan type to the
// matching common.ServiceType slug. Exported so the AWS provider's
// GetServiceClient dispatch can derive the slug for each registered service.
func ServiceTypeForPlanType(pt types.SavingsPlanType) common.ServiceType {
	switch pt {
	case types.SavingsPlanTypeCompute:
		return common.ServiceSavingsPlansCompute
	case types.SavingsPlanTypeEc2Instance:
		return common.ServiceSavingsPlansEC2Instance
	case types.SavingsPlanTypeSagemaker:
		return common.ServiceSavingsPlansSageMaker
	case types.SavingsPlanTypeDatabase:
		return common.ServiceSavingsPlansDatabase
	}
	return common.ServiceSavingsPlansAll
}

// PlanTypeForServiceType is the inverse mapping: a common.ServiceType slug to
// the AWS Savings Plans API plan type. Returns false for slugs that aren't
// per-plan-type SP services.
func PlanTypeForServiceType(s common.ServiceType) (types.SavingsPlanType, bool) {
	switch s {
	case common.ServiceSavingsPlansCompute:
		return types.SavingsPlanTypeCompute, true
	case common.ServiceSavingsPlansEC2Instance:
		return types.SavingsPlanTypeEc2Instance, true
	case common.ServiceSavingsPlansSageMaker:
		return types.SavingsPlanTypeSagemaker, true
	case common.ServiceSavingsPlansDatabase:
		return types.SavingsPlanTypeDatabase, true
	}
	return "", false
}

// GetRegion returns the region
func (c *Client) GetRegion() string {
	return c.region
}

// GetRecommendations returns empty as Savings Plans uses centralized Cost Explorer recommendations
func (c *Client) GetRecommendations(_ context.Context, _ *common.RecommendationParams) ([]common.Recommendation, error) {
	return []common.Recommendation{}, nil
}

// GetExistingCommitments retrieves existing Savings Plans across all pages.
// DescribeSavingsPlans is paginated (NextToken on both input and output); the
// original single-call implementation silently truncated to the first page,
// causing CUDly to under-report existing SPs and recommend redundant purchases
// (issue #1019). The loop mirrors the NextToken accumulator used in the
// MemoryDB and OpenSearch commitment fetches.
func (c *Client) GetExistingCommitments(ctx context.Context) ([]common.Commitment, error) {
	// Each client is scoped to one plan type, so partition the API result by
	// SavingsPlanType and only return commitments matching this client's type.
	// The provider registers four SP services and calls GetExistingCommitments
	// on each; without filtering, every SP commitment would surface four times.
	//
	// An empty planType signals umbrella sentinel mode (the
	// `case common.ServiceSavingsPlansAll` branch in provider.go's
	// GetServiceClient): in that mode, return every commitment unfiltered
	// to match pre-split behaviour. Per-plan-type clients still partition.
	commitments := make([]common.Commitment, 0)
	service := c.GetServiceType()
	page := 0
	var nextToken *string

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		page++
		if page > maxCommitmentPages {
			log.Printf("WARNING: savingsplans.GetExistingCommitments reached page cap (%d) — returning partial results (issue #1019)",
				maxCommitmentPages)
			break
		}

		input := &savingsplans.DescribeSavingsPlansInput{
			States: []types.SavingsPlanState{
				types.SavingsPlanStateActive,
				types.SavingsPlanStatePendingReturn,
				types.SavingsPlanStateQueued,
			},
			NextToken:  nextToken,
			MaxResults: aws.Int32(100),
		}

		result, err := c.client.DescribeSavingsPlans(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("failed to describe Savings Plans: %w", err)
		}

		for _, sp := range result.SavingsPlans {
			if commitment, ok := c.toCommitment(sp, service); ok {
				commitments = append(commitments, commitment)
			}
		}

		if result.NextToken == nil || aws.ToString(result.NextToken) == "" {
			break
		}
		nextToken = result.NextToken
	}

	return commitments, nil
}

// toCommitment converts a single DescribeSavingsPlans entry into a
// common.Commitment, returning ok=false for entries that should be skipped
// (missing ID, or a plan type that does not match this client's scope). It is
// split out of GetExistingCommitments to keep that function under the
// cyclomatic limit.
func (c *Client) toCommitment(sp types.SavingsPlan, service common.ServiceType) (common.Commitment, bool) {
	if sp.SavingsPlanId == nil {
		return common.Commitment{}, false
	}
	if c.planType != "" && sp.SavingsPlanType != c.planType {
		return common.Commitment{}, false
	}

	commitment := common.Commitment{
		Provider:       common.ProviderAWS,
		CommitmentID:   *sp.SavingsPlanId,
		CommitmentType: common.CommitmentSavingsPlan,
		Service:        service,
		Region:         aws.ToString(sp.Region),
		ResourceType:   string(sp.SavingsPlanType),
		Count:          1, // Savings Plans don't have a count
		State:          string(sp.State),
	}

	if sp.Start != nil {
		if startTime, err := time.Parse(time.RFC3339, *sp.Start); err == nil {
			commitment.StartDate = startTime
		}
	}
	if sp.End != nil {
		if endTime, err := time.Parse(time.RFC3339, *sp.End); err == nil {
			commitment.EndDate = endTime
		}
	}

	return commitment, true
}

// PurchaseCommitment purchases a Savings Plan
func (c *Client) PurchaseCommitment(ctx context.Context, rec common.Recommendation, opts common.PurchaseOptions) (common.PurchaseResult, error) {
	result := common.PurchaseResult{
		Recommendation: rec,
		DryRun:         false,
		Success:        false,
		Timestamp:      time.Now(),
	}

	spDetails, ok := rec.Details.(*common.SavingsPlanDetails)
	if !ok {
		result.Error = fmt.Errorf("invalid service details for Savings Plans")
		return result, result.Error
	}

	offeringID, err := c.findOfferingID(ctx, rec, opts.ExecutionID)
	if err != nil {
		result.Error = fmt.Errorf("failed to find Savings Plans offering: %w", err)
		return result, result.Error
	}

	input := &savingsplans.CreateSavingsPlanInput{
		SavingsPlanOfferingId: aws.String(offeringID),
		Commitment:            aws.String(fmt.Sprintf("%.2f", spDetails.HourlyCommitment)),
		UpfrontPaymentAmount:  nil, // AWS calculates this based on payment option
		PurchaseTime:          aws.Time(time.Now()),
		Tags:                  buildSavingsPlanTags(opts.Source),
	}

	// Native server-side idempotency (issue #636): a repeated CreateSavingsPlan
	// with the same ClientToken returns the original Savings Plan instead of
	// creating a second one, so re-driving a stranded execution can never
	// double-purchase. The token is deterministic across re-drives (derived from
	// execution_id + rec index). Left unset for the CLI path, which carries no
	// owning execution and keeps its prior non-idempotent behaviour.
	if opts.IdempotencyToken != "" {
		input.ClientToken = aws.String(opts.IdempotencyToken)
	}

	response, err := c.client.CreateSavingsPlan(ctx, input)
	if err != nil {
		result.Error = fmt.Errorf("failed to purchase Savings Plan: %w", err)
		return result, result.Error
	}

	if response.SavingsPlanId != nil {
		result.Success = true
		result.CommitmentID = *response.SavingsPlanId
	} else {
		result.Error = fmt.Errorf("purchase response was empty")
		return result, result.Error
	}

	return result, nil
}

// resolveSPPlanType resolves the effective plan type for an offering lookup.
// When the client is scoped to a specific plan type (post-split), it validates
// that the recommendation matches and returns c.planType. Umbrella/legacy
// clients (c.planType == "") fall back to spDetails.PlanType.
func (c *Client) resolveSPPlanType(spPlanType string) (types.SavingsPlanType, error) {
	planType, convErr := convertPlanType(spPlanType)
	if c.planType == "" {
		// Legacy umbrella client: accept any convertible plan type.
		return planType, convErr
	}
	// Scoped client: reject mismatches to prevent buying the wrong product.
	if convErr != nil {
		return "", convErr
	}
	if planType != c.planType {
		return "", fmt.Errorf(
			"recommendation plan type %q does not match client scope %q",
			spPlanType, c.planType,
		)
	}
	return c.planType, nil
}

// buildSPOfferingsInput constructs the DescribeSavingsPlansOfferings request.
// For EC2Instance plans it adds region and instanceFamily filters using the
// recommendation's own region/family (from CE SavingsPlansDetails). recRegion
// and instanceFamily are ignored for Compute, SageMaker, and Database plans
// which are global and carry no region or family property.
//
// Supported filter attributes for DescribeSavingsPlansOfferings:
//   - SavingsPlanOfferingFilterAttributeRegion       ("region")
//   - SavingsPlanOfferingFilterAttributeInstanceFamily ("instanceFamily")
func (c *Client) buildSPOfferingsInput(planType types.SavingsPlanType, termSeconds int64, paymentOption types.SavingsPlanPaymentOption, instanceFamily, recRegion, tag string) *savingsplans.DescribeSavingsPlansOfferingsInput {
	input := &savingsplans.DescribeSavingsPlansOfferingsInput{
		PlanTypes:      []types.SavingsPlanType{planType},
		Durations:      []int64{termSeconds},
		PaymentOptions: []types.SavingsPlanPaymentOption{paymentOption},
		// Pin to USD so non-USD currency offerings are excluded server-side.
		Currencies: []types.CurrencyCode{types.CurrencyCodeUsd},
	}
	// EC2Instance SPs are region-scoped and family-scoped. Apply both filters
	// using the recommendation's own values from CE (recRegion, instanceFamily)
	// rather than the client's configured region, which may differ from the
	// region CE recommended. Compute, SageMaker, and Database SPs are global
	// and do not carry region or family properties.
	if planType == types.SavingsPlanTypeEc2Instance {
		filterRegion := recRegion
		if filterRegion == "" {
			// Fall back to the client's region if CE did not supply one. Log so
			// operators can detect when the recommendation is missing the field.
			filterRegion = c.region
			if filterRegion != "" {
				log.Printf("purchase[%s]: SavingsPlans buildSPOfferingsInput: rec has no region; falling back to client region %s", tag, filterRegion)
			}
		}

		var filters []types.SavingsPlanOfferingFilterElement
		if filterRegion != "" {
			filters = append(filters, types.SavingsPlanOfferingFilterElement{
				Name:   types.SavingsPlanOfferingFilterAttributeRegion,
				Values: []string{filterRegion},
			})
		} else {
			log.Printf("purchase[%s]: SavingsPlans buildSPOfferingsInput: EC2Instance SP has no region in rec and client has no region; skipping region filter", tag)
		}
		if instanceFamily != "" {
			filters = append(filters, types.SavingsPlanOfferingFilterElement{
				Name:   types.SavingsPlanOfferingFilterAttributeInstanceFamily,
				Values: []string{instanceFamily},
			})
		}
		if len(filters) > 0 {
			input.Filters = filters
		}
	}
	return input
}

// spOffering holds the offering ID and the Properties-extracted instance family
// and region for a single DescribeSavingsPlansOfferings result. Used by the
// EC2Instance strict lookup to validate that the surviving offerings are
// unambiguous before committing to one.
type spOffering struct {
	id             string
	instanceFamily string
	region         string
}

// decodeOfferingProps converts a DescribeSavingsPlansOfferings result entry
// into an spOffering by extracting the OfferingId and reading the Properties
// slice for the instance-family and region values. Extracted to keep
// collectSPOfferings under the gocyclo-10 threshold.
func decodeOfferingProps(o types.SavingsPlanOffering) (spOffering, bool) {
	if o.OfferingId == nil {
		return spOffering{}, false
	}
	off := spOffering{id: *o.OfferingId}
	for _, p := range o.Properties {
		switch p.Name {
		case types.SavingsPlanOfferingPropertyKeyInstanceFamily:
			off.instanceFamily = aws.ToString(p.Value)
		case types.SavingsPlanOfferingPropertyKeyRegion:
			off.region = aws.ToString(p.Value)
		}
	}
	return off, true
}

// collectSPOfferings paginates DescribeSavingsPlansOfferings and returns every
// result as an spOffering (with Properties decoded). The caller validates the
// result set's consistency and picks the appropriate offering ID.
func (c *Client) collectSPOfferings(ctx context.Context, input *savingsplans.DescribeSavingsPlansOfferingsInput) ([]spOffering, error) {
	t0 := time.Now()
	page := 0
	var offerings []spOffering
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		page++
		if page > maxOfferingPages {
			return nil, fmt.Errorf("pagination cap reached after %d pages for Savings Plans offering lookup (issue #688)", maxOfferingPages)
		}
		result, err := c.client.DescribeSavingsPlansOfferings(ctx, input)
		if err != nil {
			log.Printf("purchase[SavingsPlans]: DescribeSavingsPlansOfferings page %d failed after %s: %v", page, time.Since(t0), err)
			return nil, fmt.Errorf("failed to describe Savings Plans offerings: %w", err)
		}
		log.Printf("purchase[SavingsPlans]: DescribeSavingsPlansOfferings page %d returned %d results in %s",
			page, len(result.SearchResults), time.Since(t0))
		for _, o := range result.SearchResults {
			if off, ok := decodeOfferingProps(o); ok {
				offerings = append(offerings, off)
			}
		}
		if result.NextToken == nil || aws.ToString(result.NextToken) == "" {
			break
		}
		input.NextToken = result.NextToken
	}
	return offerings, nil
}

// findOfferingID finds the appropriate Savings Plans offering ID.
// execID is the purchase execution UUID for log correlation; pass "" when
// calling outside of a purchase flow (ValidateOffering, GetOfferingDetails).
func (c *Client) findOfferingID(ctx context.Context, rec common.Recommendation, execID string) (string, error) {
	spDetails, ok := rec.Details.(*common.SavingsPlanDetails)
	if !ok {
		return "", fmt.Errorf("invalid service details for Savings Plans")
	}

	tag := execID
	if tag == "" {
		tag = "no-exec"
	}

	// If CE supplied an exact offering ID, use it directly — this is the
	// safest path because it bypasses DescribeSavingsPlansOfferings filtering
	// entirely and always resolves to exactly the workload CE recommended.
	if spDetails.OfferingID != "" {
		log.Printf("purchase[%s]: SavingsPlans findOfferingID: using CE-provided OfferingID %s (skipping DescribeSavingsPlansOfferings)", tag, spDetails.OfferingID)
		return spDetails.OfferingID, nil
	}

	planType, err := c.resolveSPPlanType(spDetails.PlanType)
	if err != nil {
		return "", err
	}
	termSeconds, err := convertTermToSeconds(rec.Term)
	if err != nil {
		return "", err
	}
	paymentOption, err := convertPaymentOption(rec.PaymentOption)
	if err != nil {
		return "", err
	}

	t0 := time.Now()
	log.Printf("purchase[%s]: SavingsPlans findOfferingID starting (planType=%s term=%s payment=%s)",
		tag, planType, rec.Term, rec.PaymentOption)

	input := c.buildSPOfferingsInput(planType, termSeconds, paymentOption, spDetails.InstanceFamily, spDetails.Region, tag)

	var offeringID string
	if planType == types.SavingsPlanTypeEc2Instance {
		offeringID, err = c.lookupEC2OfferingIDStrict(ctx, input, spDetails.InstanceFamily, spDetails.Region)
	} else {
		offeringID, err = c.lookupOfferingID(ctx, input)
	}
	if err != nil {
		log.Printf("purchase[%s]: SavingsPlans findOfferingID failed after %s: %v", tag, time.Since(t0), err)
	} else {
		log.Printf("purchase[%s]: SavingsPlans findOfferingID found offering in %s", tag, time.Since(t0))
	}
	return offeringID, err
}

// lookupEC2OfferingIDStrict resolves the offering ID for an EC2Instance Savings
// Plan and validates that the surviving offerings are unambiguous: all must
// belong to the same instance family and the same region. If the result set
// spans multiple families or multiple regions, or is empty, the function
// returns an error rather than silently committing to the wrong workload.
//
// expectedFamily and expectedRegion are the values from the CE recommendation;
// they are included in the error message when the result set is ambiguous so
// operators can diagnose mismatches quickly.
func (c *Client) lookupEC2OfferingIDStrict(ctx context.Context, input *savingsplans.DescribeSavingsPlansOfferingsInput, expectedFamily, expectedRegion string) (string, error) {
	offerings, err := c.collectSPOfferings(ctx, input)
	if err != nil {
		return "", err
	}
	if len(offerings) == 0 {
		return "", fmt.Errorf("no EC2Instance Savings Plans offerings found (expected family=%q region=%q) — cannot purchase", expectedFamily, expectedRegion)
	}

	// Validate that all surviving offerings share a single instance family and
	// a single region. Multiple distinct values mean the filters did not narrow
	// the result set enough to identify the correct workload unambiguously.
	families := make(map[string]struct{})
	regions := make(map[string]struct{})
	for _, o := range offerings {
		if o.instanceFamily != "" {
			families[o.instanceFamily] = struct{}{}
		}
		if o.region != "" {
			regions[o.region] = struct{}{}
		}
	}

	if len(families) > 1 {
		return "", fmt.Errorf(
			"EC2Instance SP offering lookup returned %d distinct instance families %v (expected %q region=%q): ambiguous — refusing to purchase to avoid committing to the wrong workload",
			len(families), mapKeys(families), expectedFamily, expectedRegion,
		)
	}
	if len(regions) > 1 {
		return "", fmt.Errorf(
			"EC2Instance SP offering lookup returned %d distinct regions %v (expected family=%q region=%q): ambiguous — refusing to purchase",
			len(regions), mapKeys(regions), expectedFamily, expectedRegion,
		)
	}

	// Sort IDs for a stable, deterministic result.
	ids := make([]string, 0, len(offerings))
	for _, o := range offerings {
		ids = append(ids, o.id)
	}
	sort.Strings(ids)
	return ids[0], nil
}

// convertPlanType converts a plan type string to AWS SDK type
func convertPlanType(planType string) (types.SavingsPlanType, error) {
	switch planType {
	case "Compute":
		return types.SavingsPlanTypeCompute, nil
	case "EC2Instance":
		return types.SavingsPlanTypeEc2Instance, nil
	case "SageMaker", "Sagemaker":
		return types.SavingsPlanTypeSagemaker, nil
	case "Database":
		return types.SavingsPlanTypeDatabase, nil
	default:
		return "", fmt.Errorf("unsupported Savings Plan type: %s", planType)
	}
}

// convertTermToSeconds converts a term string to seconds for the AWS Savings
// Plans API. Returns an error on any unrecognized or empty input so callers
// fail loud rather than silently buying the wrong commitment length.
func convertTermToSeconds(term string) (int64, error) {
	switch term {
	case "3yr", "3":
		return 94608000, nil // 3 years in seconds (365 * 3 * 86400)
	case "1yr", "1":
		return 31536000, nil // 1 year in seconds (365 * 86400)
	default:
		return 0, fmt.Errorf("unsupported Savings Plans term %q: must be one of 1yr, 1, 3yr, 3", term)
	}
}

// convertPaymentOption converts a payment option string to the AWS SDK type.
// Returns an error on any unrecognized or empty input so callers fail loud
// rather than silently buying the wrong (and potentially most expensive)
// payment option.
func convertPaymentOption(paymentOption string) (types.SavingsPlanPaymentOption, error) {
	switch paymentOption {
	case "All Upfront", "all-upfront":
		return types.SavingsPlanPaymentOptionAllUpfront, nil
	case "Partial Upfront", "partial-upfront":
		return types.SavingsPlanPaymentOptionPartialUpfront, nil
	case "No Upfront", "no-upfront":
		return types.SavingsPlanPaymentOptionNoUpfront, nil
	default:
		return "", fmt.Errorf("unsupported Savings Plans payment option %q: must be one of All Upfront, all-upfront, Partial Upfront, partial-upfront, No Upfront, no-upfront", paymentOption)
	}
}

// maxOfferingPages is the maximum number of DescribeSavingsPlansOfferings
// pages to walk before giving up. Exceeding the cap returns a diagnostic error
// instead of timing out the Lambda budget (issue #688).
const maxOfferingPages = 5

// maxCommitmentPages is the maximum number of DescribeSavingsPlans pages to
// walk when listing existing commitments. 100 pages * 100 items/page = 10,000
// SPs, which is a realistic upper bound for even large enterprise accounts.
// Exceeding the cap logs a warning but still returns the commitments collected
// so far, consistent with the #691 guidance (data loss is worse than a
// truncation warning for the commitment path).
const maxCommitmentPages = 100

// lookupOfferingID resolves the offering ID for non-EC2Instance Savings Plans
// (Compute, SageMaker, Database). These plan types are global and family-agnostic
// so no family/region ambiguity check is needed; a stable sort provides a
// deterministic tie-break when multiple offerings survive the server-side filters.
// EC2Instance SPs use lookupEC2OfferingIDStrict instead.
func (c *Client) lookupOfferingID(ctx context.Context, input *savingsplans.DescribeSavingsPlansOfferingsInput) (string, error) {
	offerings, err := c.collectSPOfferings(ctx, input)
	if err != nil {
		return "", err
	}
	if len(offerings) == 0 {
		return "", fmt.Errorf("no Savings Plans offerings found (issue #688)")
	}
	ids := make([]string, 0, len(offerings))
	for _, o := range offerings {
		ids = append(ids, o.id)
	}
	// Sort for a stable, deterministic tie-break when multiple offerings
	// survive the server-side filters. The first ID after sorting is returned.
	sort.Strings(ids)
	return ids[0], nil
}

// ValidateOffering checks if a Savings Plans offering exists
func (c *Client) ValidateOffering(ctx context.Context, rec common.Recommendation) error {
	_, err := c.findOfferingID(ctx, rec, "")
	return err
}

// GetOfferingDetails retrieves offering details
func (c *Client) GetOfferingDetails(ctx context.Context, rec common.Recommendation) (*common.OfferingDetails, error) {
	offeringID, err := c.findOfferingID(ctx, rec, "")
	if err != nil {
		return nil, err
	}

	spDetails, ok := rec.Details.(*common.SavingsPlanDetails)
	if !ok {
		return nil, fmt.Errorf("invalid service details for Savings Plans")
	}

	if err := c.validateOffering(ctx, offeringID); err != nil {
		return nil, err
	}

	hoursInTerm := calculateHoursInTerm(rec.Term)
	totalCost := spDetails.HourlyCommitment * hoursInTerm
	upfrontCost, recurringCost := calculatePaymentBreakdown(rec.PaymentOption, totalCost, hoursInTerm)

	return &common.OfferingDetails{
		OfferingID:          offeringID,
		ResourceType:        spDetails.PlanType,
		Term:                normalizeTermString(rec.Term),
		PaymentOption:       rec.PaymentOption,
		UpfrontCost:         upfrontCost,
		RecurringCost:       recurringCost,
		TotalCost:           totalCost,
		EffectiveHourlyRate: spDetails.HourlyCommitment,
		Currency:            "USD",
	}, nil
}

// validateOffering validates that the offering exists
func (c *Client) validateOffering(ctx context.Context, offeringID string) error {
	input := &savingsplans.DescribeSavingsPlansOfferingRatesInput{
		SavingsPlanOfferingIds: []string{offeringID},
	}

	_, err := c.client.DescribeSavingsPlansOfferingRates(ctx, input)
	if err != nil {
		return fmt.Errorf("failed to get offering rates: %w", err)
	}

	return nil
}

// calculateHoursInTerm calculates the number of hours in a commitment term.
// Uses 365 days/year to match AWS billing conventions for RIs and Savings Plans.
func calculateHoursInTerm(term string) float64 {
	if term == "3yr" || term == "3" {
		return 3 * 365 * 24 // 3 years (26280 hours)
	}
	return 365 * 24 // 1 year (8760 hours)
}

// calculatePaymentBreakdown calculates upfront and recurring costs based on payment option
func calculatePaymentBreakdown(paymentOption string, totalCost, hoursInTerm float64) (upfrontCost, recurringCost float64) {
	switch paymentOption {
	case "All Upfront", "all-upfront":
		return totalCost, 0
	case "Partial Upfront", "partial-upfront":
		return totalCost * 0.5, (totalCost * 0.5) / hoursInTerm
	case "No Upfront", "no-upfront":
		return 0, totalCost / hoursInTerm
	default:
		return totalCost, 0
	}
}

// normalizeTermString normalizes a term string to standard format
func normalizeTermString(term string) string {
	if term == "3yr" || term == "3" {
		return "3yr"
	}
	return "1yr"
}

// GetValidResourceTypes returns valid Savings Plan types
func (c *Client) GetValidResourceTypes(ctx context.Context) ([]string, error) {
	return []string{
		"Compute",
		"EC2Instance",
		"SageMaker",
		"Database",
	}, nil
}

// mapKeys extracts the keys from a string set (map[string]struct{}) as a
// sorted slice for deterministic error messages.
func mapKeys(m map[string]struct{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// buildSavingsPlanTags returns the tag map to stamp onto a newly-created
// Savings Plan. The Tags map on CreateSavingsPlanInput accepts tags at
// purchase time, so no follow-up call is needed. When source is empty the
// purchase-automation tag is skipped rather than writing an empty value.
func buildSavingsPlanTags(source string) map[string]string {
	tags := map[string]string{
		"Tool":         "CUDly",
		"PurchaseDate": time.Now().Format("2006-01-02"),
	}
	if source != "" {
		tags[common.PurchaseTagKey] = source
	}
	return tags
}
