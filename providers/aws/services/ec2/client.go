// Package ec2 provides AWS EC2 Reserved Instances client
package ec2

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/exchange"
	"github.com/LeanerCloud/CUDly/pkg/retry"
)

// EC2API defines the interface for EC2 operations (enables mocking)
type EC2API interface {
	PurchaseReservedInstancesOffering(ctx context.Context, params *ec2.PurchaseReservedInstancesOfferingInput, optFns ...func(*ec2.Options)) (*ec2.PurchaseReservedInstancesOfferingOutput, error)
	DescribeReservedInstancesOfferings(ctx context.Context, params *ec2.DescribeReservedInstancesOfferingsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeReservedInstancesOfferingsOutput, error)
	DescribeReservedInstances(ctx context.Context, params *ec2.DescribeReservedInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeReservedInstancesOutput, error)
	DescribeInstanceTypeOfferings(ctx context.Context, params *ec2.DescribeInstanceTypeOfferingsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstanceTypeOfferingsOutput, error)
	GetReservedInstancesExchangeQuote(ctx context.Context, params *ec2.GetReservedInstancesExchangeQuoteInput, optFns ...func(*ec2.Options)) (*ec2.GetReservedInstancesExchangeQuoteOutput, error)
	AcceptReservedInstancesExchangeQuote(ctx context.Context, params *ec2.AcceptReservedInstancesExchangeQuoteInput, optFns ...func(*ec2.Options)) (*ec2.AcceptReservedInstancesExchangeQuoteOutput, error)
	CreateTags(ctx context.Context, params *ec2.CreateTagsInput, optFns ...func(*ec2.Options)) (*ec2.CreateTagsOutput, error)
}

// Client handles AWS EC2 Reserved Instances
type Client struct {
	client EC2API
	region string
}

// NewClient creates a new EC2 client
func NewClient(cfg aws.Config) *Client {
	return &Client{
		client: ec2.NewFromConfig(cfg),
		region: cfg.Region,
	}
}

// SetEC2API sets a custom EC2 API client (for testing)
func (c *Client) SetEC2API(api EC2API) {
	c.client = api
}

// GetServiceType returns the service type
func (c *Client) GetServiceType() common.ServiceType {
	return common.ServiceCompute
}

// GetRegion returns the region
func (c *Client) GetRegion() string {
	return c.region
}

// GetRecommendations returns empty as EC2 uses centralized Cost Explorer recommendations
func (c *Client) GetRecommendations(ctx context.Context, params common.RecommendationParams) ([]common.Recommendation, error) {
	// EC2 recommendations come from Cost Explorer API via RecommendationsClient
	return []common.Recommendation{}, nil
}

// GetExistingCommitments retrieves existing EC2 Reserved Instances
func (c *Client) GetExistingCommitments(ctx context.Context) ([]common.Commitment, error) {
	commitments := make([]common.Commitment, 0)

	input := &ec2.DescribeReservedInstancesInput{
		Filters: []types.Filter{
			{
				Name:   aws.String("state"),
				Values: []string{"active", "payment-pending"},
			},
		},
	}

	response, err := c.client.DescribeReservedInstances(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to describe reserved instances: %w", err)
	}

	for _, ri := range response.ReservedInstances {

		commitment := common.Commitment{
			Provider:       common.ProviderAWS,
			CommitmentID:   aws.ToString(ri.ReservedInstancesId),
			CommitmentType: common.CommitmentReservedInstance,
			Service:        common.ServiceEC2,
			Region:         c.region,
			ResourceType:   string(ri.InstanceType),
			Count:          int(aws.ToInt32(ri.InstanceCount)),
			State:          string(ri.State),
			StartDate:      aws.ToTime(ri.Start),
			EndDate:        aws.ToTime(ri.End),
		}

		commitments = append(commitments, commitment)
	}

	return commitments, nil
}

// PurchaseCommitment purchases an EC2 Reserved Instance
func (c *Client) PurchaseCommitment(ctx context.Context, rec common.Recommendation, opts common.PurchaseOptions) (common.PurchaseResult, error) {
	result := common.PurchaseResult{
		Recommendation: rec,
		DryRun:         false,
		Success:        false,
		Timestamp:      time.Now(),
	}

	// Idempotency dedupe guard (issue #636). EC2's
	// PurchaseReservedInstancesOfferingInput has no ClientToken, so the API
	// cannot dedupe a repeated purchase server-side. Instead, when an
	// idempotency token is supplied, look for an RI already tagged with it
	// before buying: if one exists, this is a re-drive of a purchase that
	// already succeeded, so short-circuit and return the existing RI rather
	// than buying a second one.
	if opts.IdempotencyToken != "" {
		existingID, found, lookupErr := c.findRIByIdempotencyToken(ctx, opts.IdempotencyToken)
		if lookupErr != nil {
			// A failed lookup must NOT fall through to a purchase: doing so
			// would defeat the guard and risk a double-buy on a re-drive. Fail
			// loudly so the recovery sweep treats it as not-yet-purchased and
			// retries the whole guarded path, rather than silently buying.
			result.Error = fmt.Errorf("idempotency lookup failed before EC2 RI purchase (refusing to purchase to avoid a possible double-buy): %w", lookupErr)
			return result, result.Error
		}
		if found {
			log.Printf("EC2 RI for idempotency token %s already exists (%s); skipping purchase (issue #636 re-drive)", opts.IdempotencyToken, existingID)
			result.Success = true
			result.CommitmentID = existingID
			return result, nil
		}
	}

	// Find the offering ID
	offeringID, err := c.findOfferingID(ctx, rec)
	if err != nil {
		result.Error = fmt.Errorf("failed to find offering: %w", err)
		return result, result.Error
	}

	// Create the purchase request
	input := &ec2.PurchaseReservedInstancesOfferingInput{
		ReservedInstancesOfferingId: aws.String(offeringID),
		InstanceCount:               aws.Int32(int32(rec.Count)),
	}

	// Execute the purchase
	response, err := c.client.PurchaseReservedInstancesOffering(ctx, input)
	if err != nil {
		result.Error = fmt.Errorf("failed to purchase EC2 RI: %w", err)
		return result, result.Error
	}

	// Extract purchase information
	if response.ReservedInstancesId != nil {
		result.Success = true
		result.CommitmentID = aws.ToString(response.ReservedInstancesId)
	} else {
		result.Error = fmt.Errorf("purchase response was empty")
		return result, result.Error
	}

	// PurchaseReservedInstancesOfferingInput has no TagSpecifications — tag the
	// commitment post-purchase. Failure is logged but does NOT fail the
	// purchase: the RI is already bought, and failing here would leave the
	// customer with a paid-for-but-untagged commitment and no way to retry
	// without double-purchasing.
	//
	// The idempotency-token tag (issue #636) rides this same CreateTags call.
	// It is load-bearing for the dedupe guard above: if this write fails the
	// guard degrades for that one RI (a re-drive would not find the tag and
	// could double-buy). That residual window is backstopped by the recovery
	// sweep's safe-fail + operator-confirm Retry (PR #635), since EC2 offers no
	// atomic alternative. The cosmetic tags and the idempotency tag share one
	// call so they cannot drift apart.
	if err := c.tagReservedInstance(ctx, result.CommitmentID, rec, opts.Source, opts.IdempotencyToken); err != nil {
		log.Printf("WARNING: failed to tag EC2 RI %s after purchase (commitment is bought; tag missing): %v", result.CommitmentID, err)
	}

	return result, nil
}

// findRIByIdempotencyToken looks for an active or payment-pending Reserved
// Instance tagged with the given idempotency token (issue #636). It returns the
// RI ID and true when exactly such an RI exists, so a re-driven purchase can
// short-circuit instead of buying a second commitment. Retired/cancelled RIs are
// excluded (they carry the same state filter as GetExistingCommitments) so a
// returned or expired commitment does not suppress a legitimate fresh purchase.
func (c *Client) findRIByIdempotencyToken(ctx context.Context, token string) (string, bool, error) {
	input := &ec2.DescribeReservedInstancesInput{
		Filters: []types.Filter{
			{
				Name:   aws.String("tag:" + common.IdempotencyTagKey),
				Values: []string{token},
			},
			{
				Name:   aws.String("state"),
				Values: []string{"active", "payment-pending"},
			},
		},
	}

	response, err := c.client.DescribeReservedInstances(ctx, input)
	if err != nil {
		return "", false, fmt.Errorf("failed to describe reserved instances for idempotency check: %w", err)
	}
	for _, ri := range response.ReservedInstances {
		if ri.ReservedInstancesId != nil {
			return aws.ToString(ri.ReservedInstancesId), true, nil
		}
	}
	return "", false, nil
}

// tagReservedInstance applies the standard CUDly tag set (including the
// purchase-automation attribution tag when source is non-empty) to an RI
// that was just created by PurchaseReservedInstancesOffering.
//
// Retries up to 4 attempts (1s/2s/4s backoff) on InvalidReservationID.NotFound,
// since AWS sometimes needs a couple of seconds before the RI ID is visible
// to CreateTags. Non-NotFound errors short-circuit immediately.
func (c *Client) tagReservedInstance(ctx context.Context, riID string, rec common.Recommendation, source, idempotencyToken string) error {
	tags := []types.Tag{
		{Key: aws.String("Purpose"), Value: aws.String("Reserved Instance Purchase")},
		{Key: aws.String("ResourceType"), Value: aws.String(rec.ResourceType)},
		{Key: aws.String("Region"), Value: aws.String(rec.Region)},
		{Key: aws.String("PurchaseDate"), Value: aws.String(time.Now().Format("2006-01-02"))},
		{Key: aws.String("Tool"), Value: aws.String("CUDly")},
	}
	if source != "" {
		tags = append(tags, types.Tag{
			Key:   aws.String(common.PurchaseTagKey),
			Value: aws.String(source),
		})
	}
	// The idempotency tag is what findRIByIdempotencyToken matches on for the
	// dedupe guard (issue #636); it must be written for a re-drive to recognise
	// this RI as already-purchased.
	if idempotencyToken != "" {
		tags = append(tags, types.Tag{
			Key:   aws.String(common.IdempotencyTagKey),
			Value: aws.String(idempotencyToken),
		})
	}

	cfg := retry.Config{
		MaxAttempts: 4,
		BaseDelay:   time.Second,
		MaxDelay:    4 * time.Second,
	}
	return retry.Do(ctx, cfg, func(perAttemptCtx context.Context, _ int) error {
		_, err := c.client.CreateTags(perAttemptCtx, &ec2.CreateTagsInput{
			Resources: []string{riID},
			Tags:      tags,
		})
		if err == nil {
			return nil
		}
		// Only retry if the RI isn't yet visible. Anything else is a
		// permanent failure (permissions, bad tag shape, etc.).
		if strings.Contains(err.Error(), "InvalidReservationID.NotFound") {
			return err // retryable
		}
		return fmt.Errorf("%w: %w", retry.ErrPermanent, err)
	})
}

// canonicalizeEC2Tenancy maps legacy lowercase/hyphenated tenancy values that
// were written by parser versions before fix #598 to the canonical EC2 API enum
// values. New parser output already carries the correct casing, so this is a
// defensive shim for pre-fix-collected recommendations persisted in the DB.
//
// Canonical mappings (per types.Tenancy in the AWS SDK):
//
//	"shared"    -> "default"  (CE-to-EC2 mismatch that the old parser passed through)
//	"default"   -> "default"  (already canonical; no-op)
//	"dedicated" -> "dedicated" (already canonical; no-op)
func canonicalizeEC2Tenancy(t string) string {
	switch strings.ToLower(t) {
	case "shared", "default":
		return string(types.TenancyDefault)
	case "dedicated":
		return string(types.TenancyDedicated)
	default:
		return t
	}
}

// canonicalizeEC2Scope maps legacy lowercase/hyphenated scope values that were
// written by parser versions before fix #598 to the canonical EC2 API enum
// values. New parser output already carries the correct casing.
//
// Canonical mappings (per types.Scope in the AWS SDK):
//
//	"region"            -> "Region"           (lowercase, old parser)
//	"availability-zone" -> "Availability Zone" (hyphenated, old parser)
//	"Region"            -> "Region"            (no-op)
//	"Availability Zone" -> "Availability Zone" (no-op)
func canonicalizeEC2Scope(s string) string {
	switch strings.ToLower(s) {
	case "region":
		return string(types.ScopeRegional)
	case "availability-zone", "availability zone":
		return string(types.ScopeAvailabilityZone)
	default:
		return s
	}
}

// maxOfferingPages is the maximum number of DescribeReservedInstancesOfferings
// pages to walk before giving up. At MaxResults=100 per page this caps the
// search at 500 offerings. Exceeding the cap returns a diagnostic error instead
// of timing out the Lambda budget (issue #688).
const maxOfferingPages = 5

// convertEC2PaymentOption maps a rec payment-option slug to the AWS
// DescribeReservedInstancesOfferings OfferingType enum value.
func convertEC2PaymentOption(option string) (types.OfferingTypeValues, error) {
	switch option {
	case "all-upfront":
		return types.OfferingTypeValuesAllUpfront, nil
	case "partial-upfront":
		return types.OfferingTypeValuesPartialUpfront, nil
	case "no-upfront":
		return types.OfferingTypeValuesNoUpfront, nil
	default:
		return "", fmt.Errorf("unsupported EC2 payment option: %s", option)
	}
}

// ec2OfferingQuery holds the typed lookup parameters for an EC2 RI offering.
type ec2OfferingQuery struct {
	instanceType     types.InstanceType
	productDesc      types.RIProductDescription
	tenancy          types.Tenancy
	scope            string
	duration         int64
	wantOfferingType types.OfferingTypeValues
}

// buildEC2OfferingQuery resolves the typed lookup parameters from a rec,
// canonicalising legacy tenancy/scope values and applying API defaults.
func buildEC2OfferingQuery(rec common.Recommendation, details *common.ComputeDetails, duration int64) ec2OfferingQuery {
	platform := details.Platform
	if platform == "" {
		platform = "Linux/UNIX"
	}
	tenancy := canonicalizeEC2Tenancy(details.Tenancy)
	if tenancy == "" {
		tenancy = string(types.TenancyDefault)
	}
	scope := canonicalizeEC2Scope(details.Scope)
	if scope == "" {
		scope = string(types.ScopeRegional)
	}
	return ec2OfferingQuery{
		instanceType: types.InstanceType(rec.ResourceType),
		productDesc:  types.RIProductDescription(platform),
		tenancy:      types.Tenancy(tenancy),
		scope:        scope,
		duration:     duration,
	}
}

// describeInputFromQuery builds the SDK request struct for one page of the
// typed lookup. Typed fields land on AWS's primary indices; only scope has no
// typed equivalent and stays in Filters[].
func describeInputFromQuery(q ec2OfferingQuery, nextToken *string) *ec2.DescribeReservedInstancesOfferingsInput {
	return &ec2.DescribeReservedInstancesOfferingsInput{
		InstanceType:       q.instanceType,
		ProductDescription: q.productDesc,
		InstanceTenancy:    q.tenancy,
		MinDuration:        aws.Int64(q.duration),
		MaxDuration:        aws.Int64(q.duration),
		OfferingClass:      types.OfferingClassTypeConvertible,
		OfferingType:       q.wantOfferingType,
		IncludeMarketplace: aws.Bool(false),
		MaxResults:         aws.Int32(100),
		NextToken:          nextToken,
		Filters: []types.Filter{
			{Name: aws.String("scope"), Values: []string{q.scope}},
		},
	}
}

// findOfferingID finds the appropriate EC2 Reserved Instance offering ID.
//
// The input is built from typed first-class fields on
// DescribeReservedInstancesOfferingsInput (InstanceType, ProductDescription,
// InstanceTenancy, MinDuration/MaxDuration, OfferingClass, OfferingType)
// rather than packing everything into Filters[]. The typed shape was verified
// against live AWS to return the exact matching offering immediately; the
// Filter[]-heavy shape caused AWS to return empty pages with NextToken on
// sparse offering sets, walking until the Lambda budget expired (issue #688).
// Only scope has no typed equivalent on the input struct, so it stays in Filters[].
func (c *Client) findOfferingID(ctx context.Context, rec common.Recommendation) (string, error) {
	details, ok := rec.Details.(*common.ComputeDetails)
	if !ok || details == nil {
		return "", fmt.Errorf("invalid service details for EC2")
	}
	wantOfferingType, err := convertEC2PaymentOption(rec.PaymentOption)
	if err != nil {
		return "", err
	}
	q := buildEC2OfferingQuery(rec, details, c.getDurationValue(rec.Term))
	q.wantOfferingType = wantOfferingType

	var nextToken *string
	page := 0
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		page++
		if page > maxOfferingPages {
			return "", fmt.Errorf("pagination cap reached after %d pages for EC2 %s %s %s (issue #688)",
				maxOfferingPages, rec.ResourceType, details.Platform, rec.PaymentOption)
		}
		pageStart := time.Now()
		result, err := c.client.DescribeReservedInstancesOfferings(ctx, describeInputFromQuery(q, nextToken))
		if err != nil {
			return "", fmt.Errorf("failed to describe offerings: %w", err)
		}
		log.Printf("EC2 findOfferingID page %d: %d offerings in %s",
			page, len(result.ReservedInstancesOfferings), time.Since(pageStart))
		if id := scanEC2OfferingPage(result.ReservedInstancesOfferings, wantOfferingType); id != "" {
			return id, nil
		}
		if result.NextToken == nil {
			break
		}
		nextToken = result.NextToken
	}
	return "", fmt.Errorf("no offerings found for EC2 %s %s %s after %d page(s) (issue #688)",
		rec.ResourceType, details.Platform, rec.PaymentOption, page)
}

// scanEC2OfferingPage returns the first offering whose OfferingType matches
// wantType. With the typed OfferingType field set on the request this should
// always be the first offering, but the check is kept as defense in depth.
// Mismatched offerings are skipped (logged), not treated as errors -- a
// mismatch indicates an API-side anomaly worth observing, not a reason to fail
// the rec while a valid offering may still be on a later page.
func scanEC2OfferingPage(offerings []types.ReservedInstancesOffering, wantType types.OfferingTypeValues) string {
	for _, o := range offerings {
		if o.OfferingType != wantType {
			log.Printf("EC2 findOfferingID skipping mismatched variant %s (got %q want %q)",
				aws.ToString(o.ReservedInstancesOfferingId), o.OfferingType, wantType)
			continue
		}
		return aws.ToString(o.ReservedInstancesOfferingId)
	}
	return ""
}

// ValidateOffering checks if an offering exists without purchasing
func (c *Client) ValidateOffering(ctx context.Context, rec common.Recommendation) error {
	_, err := c.findOfferingID(ctx, rec)
	return err
}

// GetOfferingDetails retrieves offering details
func (c *Client) GetOfferingDetails(ctx context.Context, rec common.Recommendation) (*common.OfferingDetails, error) {
	offeringID, err := c.findOfferingID(ctx, rec)
	if err != nil {
		return nil, err
	}

	input := &ec2.DescribeReservedInstancesOfferingsInput{
		ReservedInstancesOfferingIds: []string{offeringID},
	}

	result, err := c.client.DescribeReservedInstancesOfferings(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to get offering details: %w", err)
	}

	if len(result.ReservedInstancesOfferings) == 0 {
		return nil, fmt.Errorf("offering not found: %s", offeringID)
	}

	offering := result.ReservedInstancesOfferings[0]

	// Extract fixed price from pricing details
	var fixedPrice float64
	for _, pricing := range offering.PricingDetails {
		if pricing.Price != nil {
			fixedPrice = *pricing.Price
			break
		}
	}

	details := &common.OfferingDetails{
		OfferingID:    aws.ToString(offering.ReservedInstancesOfferingId),
		ResourceType:  string(offering.InstanceType),
		Term:          rec.Term,
		PaymentOption: string(offering.OfferingType),
		UpfrontCost:   fixedPrice,
		RecurringCost: float64(aws.ToFloat32(offering.UsagePrice)),
		Currency:      string(offering.CurrencyCode),
	}

	return details, nil
}

// GetValidResourceTypes returns valid EC2 instance types
func (c *Client) GetValidResourceTypes(ctx context.Context) ([]string, error) {
	instanceTypesMap := make(map[string]bool)
	var nextToken *string

	for {
		input := &ec2.DescribeInstanceTypeOfferingsInput{
			LocationType: types.LocationTypeRegion,
			NextToken:    nextToken,
			MaxResults:   aws.Int32(1000),
		}

		result, err := c.client.DescribeInstanceTypeOfferings(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("failed to describe EC2 instance type offerings: %w", err)
		}

		for _, offering := range result.InstanceTypeOfferings {
			instanceTypesMap[string(offering.InstanceType)] = true
		}

		if result.NextToken == nil || aws.ToString(result.NextToken) == "" {
			break
		}
		nextToken = result.NextToken
	}

	instanceTypes := make([]string, 0, len(instanceTypesMap))
	for instanceType := range instanceTypesMap {
		instanceTypes = append(instanceTypes, instanceType)
	}

	sort.Strings(instanceTypes)
	return instanceTypes, nil
}

// Duration constants for RI term calculations
const (
	OneYearSeconds   = 31536000 // 365 days in seconds
	ThreeYearSeconds = 94608000 // 3 * 365 days in seconds
)

// getDurationValue converts term string to seconds for EC2 API
func (c *Client) getDurationValue(term string) int64 {
	if term == "3yr" || term == "3" {
		return ThreeYearSeconds
	}
	return OneYearSeconds
}

// ConvertibleRI represents an active convertible Reserved Instance.
type ConvertibleRI struct {
	ReservedInstanceID  string    `json:"reserved_instance_id"`
	InstanceType        string    `json:"instance_type"`
	AvailabilityZone    string    `json:"availability_zone"`
	InstanceCount       int32     `json:"instance_count"`
	Start               time.Time `json:"start"`
	End                 time.Time `json:"end"`
	OfferingType        string    `json:"offering_type"`
	FixedPrice          float64   `json:"fixed_price"`
	UsagePrice          float64   `json:"usage_price"`
	State               string    `json:"state"`
	NormalizationFactor float64   `json:"normalization_factor"`
	ProductDescription  string    `json:"product_description"`
	InstanceTenancy     string    `json:"instance_tenancy"`
	Scope               string    `json:"scope"`
	Duration            int64     `json:"duration"`
	// CurrencyCode is the ISO-4217 currency that FixedPrice / UsagePrice
	// are denominated in (typically "USD"). Plumbed through to
	// exchange.RIInfo so the dollar-units pre-filter on cross-family
	// alternatives can refuse comparisons across currencies.
	CurrencyCode string `json:"currency_code,omitempty"`
	// RecurringHourlyAmount is the per-hour recurring charge (sum across
	// the SDK's RecurringCharges slice) — this is non-zero for
	// no-upfront and partial-upfront RIs where some of the cost is
	// billed hourly rather than as the upfront FixedPrice. Used by the
	// exchange-package dollar-units calculation.
	RecurringHourlyAmount float64 `json:"recurring_hourly_amount,omitempty"`
}

// ListConvertibleReservedInstances returns all active convertible RIs in the region.
func (c *Client) ListConvertibleReservedInstances(ctx context.Context) ([]ConvertibleRI, error) {
	input := &ec2.DescribeReservedInstancesInput{
		Filters: []types.Filter{
			{
				Name:   aws.String("state"),
				Values: []string{"active"},
			},
			{
				Name:   aws.String("offering-class"),
				Values: []string{"convertible"},
			},
		},
	}

	resp, err := c.client.DescribeReservedInstances(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to describe convertible reserved instances: %w", err)
	}

	result := make([]ConvertibleRI, 0, len(resp.ReservedInstances))
	for _, ri := range resp.ReservedInstances {
		instanceType := string(ri.InstanceType)
		normFactor := normalizationFactorForInstanceType(instanceType)

		var recurringHourly float64
		for _, rc := range ri.RecurringCharges {
			recurringHourly += aws.ToFloat64(rc.Amount)
		}
		result = append(result, ConvertibleRI{
			ReservedInstanceID:    aws.ToString(ri.ReservedInstancesId),
			InstanceType:          instanceType,
			AvailabilityZone:      aws.ToString(ri.AvailabilityZone),
			InstanceCount:         aws.ToInt32(ri.InstanceCount),
			Start:                 aws.ToTime(ri.Start),
			End:                   aws.ToTime(ri.End),
			OfferingType:          string(ri.OfferingType),
			FixedPrice:            float64(aws.ToFloat32(ri.FixedPrice)),
			UsagePrice:            float64(aws.ToFloat32(ri.UsagePrice)),
			State:                 string(ri.State),
			NormalizationFactor:   normFactor,
			ProductDescription:    string(ri.ProductDescription),
			InstanceTenancy:       string(ri.InstanceTenancy),
			Scope:                 string(ri.Scope),
			Duration:              aws.ToInt64(ri.Duration),
			CurrencyCode:          string(ri.CurrencyCode),
			RecurringHourlyAmount: recurringHourly,
		})
	}

	return result, nil
}

// FindConvertibleOfferingParams holds the parameters for finding a convertible RI offering.
type FindConvertibleOfferingParams struct {
	InstanceType       string
	ProductDescription string
	Tenancy            string
	Scope              string
	Duration           int64
}

// FindConvertibleOffering finds a convertible RI offering ID for the given parameters.
func (c *Client) FindConvertibleOffering(ctx context.Context, params FindConvertibleOfferingParams) (string, error) {
	tenancy := params.Tenancy
	if tenancy == "" {
		tenancy = "default"
	}
	scope := params.Scope
	if scope == "" {
		scope = "Region"
	}
	duration := params.Duration
	if duration == 0 {
		duration = OneYearSeconds
	}
	productDesc := params.ProductDescription
	if productDesc == "" {
		productDesc = "Linux/UNIX"
	}

	filters := []types.Filter{
		{Name: aws.String("instance-type"), Values: []string{params.InstanceType}},
		{Name: aws.String("product-description"), Values: []string{productDesc}},
		{Name: aws.String("instance-tenancy"), Values: []string{tenancy}},
		{Name: aws.String("scope"), Values: []string{scope}},
		{Name: aws.String("duration"), Values: []string{fmt.Sprintf("%d", duration)}},
		{Name: aws.String("offering-class"), Values: []string{"convertible"}},
	}

	input := &ec2.DescribeReservedInstancesOfferingsInput{
		Filters:            filters,
		IncludeMarketplace: aws.Bool(false),
		MaxResults:         aws.Int32(20),
	}

	result, err := c.client.DescribeReservedInstancesOfferings(ctx, input)
	if err != nil {
		return "", fmt.Errorf("failed to describe convertible offerings: %w", err)
	}

	if len(result.ReservedInstancesOfferings) == 0 {
		return "", fmt.Errorf("no convertible offering found for %s (%s, %s, %s)", params.InstanceType, productDesc, tenancy, scope)
	}

	return aws.ToString(result.ReservedInstancesOfferings[0].ReservedInstancesOfferingId), nil
}

// normalizationFactorForInstanceType extracts the size from an instance type
// (e.g., "m5.xlarge" → "xlarge") and returns the AWS normalization factor.
func normalizationFactorForInstanceType(instanceType string) float64 {
	parts := strings.SplitN(instanceType, ".", 2)
	if len(parts) != 2 {
		return 0
	}
	return exchange.NormalizationFactorForSize(parts[1])
}
