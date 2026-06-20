// Package rds provides AWS RDS Reserved Instances client
package rds

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/aws/aws-sdk-go-v2/service/rds/types"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/providers/aws/internal/purchasecfg"
	"github.com/LeanerCloud/CUDly/providers/aws/internal/tagging"
)

// RDSAPI defines the interface for RDS operations (enables mocking)
type RDSAPI interface {
	DescribeReservedDBInstancesOfferings(ctx context.Context, params *rds.DescribeReservedDBInstancesOfferingsInput, optFns ...func(*rds.Options)) (*rds.DescribeReservedDBInstancesOfferingsOutput, error)
	PurchaseReservedDBInstancesOffering(ctx context.Context, params *rds.PurchaseReservedDBInstancesOfferingInput, optFns ...func(*rds.Options)) (*rds.PurchaseReservedDBInstancesOfferingOutput, error)
	DescribeReservedDBInstances(ctx context.Context, params *rds.DescribeReservedDBInstancesInput, optFns ...func(*rds.Options)) (*rds.DescribeReservedDBInstancesOutput, error)
}

// Client handles AWS RDS Reserved Instances
type Client struct {
	client RDSAPI
	region string
}

// NewClient creates a new RDS client with purchase-path retry/timeout settings.
// See purchasecfg for rationale.
func NewClient(cfg aws.Config) *Client {
	pcfg := purchasecfg.NewConfig(cfg)
	return &Client{
		client: rds.NewFromConfig(pcfg),
		region: cfg.Region,
	}
}

// SetRDSAPI sets a custom RDS API client (for testing)
func (c *Client) SetRDSAPI(api RDSAPI) {
	c.client = api
}

// GetServiceType returns the service type
func (c *Client) GetServiceType() common.ServiceType {
	return common.ServiceRelationalDB
}

// GetRegion returns the region
func (c *Client) GetRegion() string {
	return c.region
}

// GetRecommendations returns empty as RDS uses centralized Cost Explorer recommendations
func (c *Client) GetRecommendations(ctx context.Context, params *common.RecommendationParams) ([]common.Recommendation, error) {
	return []common.Recommendation{}, nil
}

// GetExistingCommitments retrieves existing RDS Reserved Instances
func (c *Client) GetExistingCommitments(ctx context.Context) ([]common.Commitment, error) {
	commitments := make([]common.Commitment, 0)
	var marker *string

	for {
		input := &rds.DescribeReservedDBInstancesInput{
			Marker:     marker,
			MaxRecords: aws.Int32(100),
		}

		response, err := c.client.DescribeReservedDBInstances(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("failed to describe reserved DB instances: %w", err)
		}

		for _, instance := range response.ReservedDBInstances {
			state := aws.ToString(instance.State)
			if state != "active" && state != "payment-pending" {
				continue
			}

			duration := aws.ToInt32(instance.Duration)
			termMonths := 12
			if duration == ThreeYearSeconds {
				termMonths = 36
			}

			// Deployment carries the same vocabulary as DatabaseDetails.AZConfig
			// on Recommendation so pool-key matching aligns deployment-wise
			// between recs and existing commitments (used by expiry adjustment).
			// AWS RDS SDK exposes MultiAZ as a *bool on ReservedDBInstance;
			// nil or false → "single-az", true → "multi-az".
			deployment := "single-az"
			if aws.ToBool(instance.MultiAZ) {
				deployment = "multi-az"
			}
			commitment := common.Commitment{
				Provider:       common.ProviderAWS,
				CommitmentID:   aws.ToString(instance.ReservedDBInstanceId),
				CommitmentType: common.CommitmentReservedInstance,
				Service:        common.ServiceRelationalDB,
				Region:         c.region,
				ResourceType:   aws.ToString(instance.DBInstanceClass),
				Engine:         aws.ToString(instance.ProductDescription), // Capture engine for accurate duplicate checking
				Deployment:     deployment,
				Count:          int(aws.ToInt32(instance.DBInstanceCount)),
				State:          state,
				StartDate:      aws.ToTime(instance.StartTime),
				EndDate:        aws.ToTime(instance.StartTime).AddDate(0, termMonths, 0),
			}

			commitments = append(commitments, commitment)
		}

		if response.Marker == nil || aws.ToString(response.Marker) == "" {
			break
		}
		marker = response.Marker
	}

	return commitments, nil
}

// PurchaseCommitment purchases an RDS Reserved Instance
func (c *Client) PurchaseCommitment(ctx context.Context, rec *common.Recommendation, opts common.PurchaseOptions) (common.PurchaseResult, error) {
	result := common.PurchaseResult{
		Recommendation: *rec,
		DryRun:         false,
		Success:        false,
		Timestamp:      time.Now(),
	}

	// Find the offering ID
	offeringID, err := c.findOfferingID(ctx, *rec, opts.ExecutionID)
	if err != nil {
		result.Error = fmt.Errorf("failed to find offering: %w", err)
		return result, result.Error
	}

	reservationID := c.deriveReservationID(*rec, opts)

	// Idempotency dedupe guard (issue #641). When a token is supplied, look for a
	// reservation already created under the derived ID before buying: if one
	// exists this is a re-drive that already succeeded, so short-circuit. A
	// lookup error must NOT fall through to a purchase — fail loud.
	if existingID, shortCircuit, guardErr := c.idempotencyGuard(ctx, opts.IdempotencyToken, reservationID); guardErr != nil {
		result.Error = guardErr
		return result, result.Error
	} else if shortCircuit {
		result.Success = true
		result.CommitmentID = existingID
		return result, nil
	}

	// Create the purchase request
	input := &rds.PurchaseReservedDBInstancesOfferingInput{
		ReservedDBInstancesOfferingId: aws.String(offeringID),
		ReservedDBInstanceId:          aws.String(reservationID),
		DBInstanceCount:               aws.Int32(int32(rec.Count)),
		Tags:                          c.createPurchaseTags(*rec, opts.Source),
	}

	response, err := c.client.PurchaseReservedDBInstancesOffering(ctx, input)
	if err != nil {
		if existingID, recovered := c.recoverAlreadyExists(ctx, opts.IdempotencyToken, reservationID, err); recovered {
			result.Success = true
			result.CommitmentID = existingID
			return result, nil
		}
		result.Error = fmt.Errorf("failed to purchase RDS RI: %w", err)
		return result, result.Error
	}

	if response.ReservedDBInstance != nil {
		result.Success = true
		result.CommitmentID = aws.ToString(response.ReservedDBInstance.ReservedDBInstanceId)
		if response.ReservedDBInstance.FixedPrice != nil {
			result.Cost = *response.ReservedDBInstance.FixedPrice
		}
	} else {
		result.Error = fmt.Errorf("purchase response was empty")
		return result, result.Error
	}

	return result, nil
}

// deriveReservationID returns the ReservedDBInstanceId to use. When an
// idempotency token is supplied (issue #641) the ID is derived deterministically
// from it, so a re-drive sends the identical ID and RDS rejects the duplicate
// server-side (ReservedDBInstanceAlreadyExistsFault). Otherwise it prefers the
// caller-supplied descriptive ID; if absent (the no-token CLI path, issue #687)
// it composes a rich, self-describing identifier matching the Azure DisplayName
// format so operators can identify the reservation in the AWS console without
// cross-referencing CUDly's purchase audit log.
func (c *Client) deriveReservationID(rec common.Recommendation, opts common.PurchaseOptions) string {
	if id := common.IdempotentReservationID("rds-id-", opts.IdempotencyToken); id != "" {
		return id
	}
	if opts.ReservationID != "" {
		return common.SanitizeReservationID(opts.ReservationID, "rds-reserved-")
	}
	return common.BuildReservationName(common.ReservationNameFields{
		Service:      "rds",
		Region:       rec.Region,
		ResourceType: rec.ResourceType,
		Count:        rec.Count,
		Term:         rec.Term,
		Payment:      rec.PaymentOption,
		Now:          time.Now(),
	}, "rds-reserved-")
}

// idempotencyGuard short-circuits a re-drive (issue #641): when token is set, it
// reports (existingID, true, nil) if a reservation already exists under
// reservationID, ("", false, nil) for a first-time purchase, or a fail-loud
// error on lookup failure. With an empty token it is a no-op.
func (c *Client) idempotencyGuard(ctx context.Context, token, reservationID string) (string, bool, error) {
	if token == "" {
		return "", false, nil
	}
	existingID, found, lookupErr := c.findReservationByID(ctx, reservationID)
	if lookupErr != nil {
		return "", false, fmt.Errorf("idempotency lookup failed before RDS RI purchase (refusing to purchase to avoid a possible double-buy): %w", lookupErr)
	}
	if found {
		log.Printf("RDS RI for idempotency token %s already exists (%s); skipping purchase (issue #641 re-drive)", common.MaskToken(token), existingID)
		return existingID, true, nil
	}
	return "", false, nil
}

// recoverAlreadyExists handles the native server-side dedupe backstop (issue
// #641): if the by-ID guard missed the existing reservation but AWS still
// rejected the duplicate ID with ReservedDBInstanceAlreadyExistsFault, it
// re-Describes by ID and returns (existingID, true) so the re-drive recovers the
// original reservation instead of erroring.
func (c *Client) recoverAlreadyExists(ctx context.Context, token, reservationID string, purchaseErr error) (string, bool) {
	if token == "" {
		return "", false
	}
	var already *types.ReservedDBInstanceAlreadyExistsFault
	if !errors.As(purchaseErr, &already) {
		return "", false
	}
	existingID, found, lookupErr := c.findReservationByID(ctx, reservationID)
	if lookupErr == nil && found {
		log.Printf("RDS RI %s already existed at purchase time; treating as idempotent re-drive (issue #641)", existingID)
		return existingID, true
	}
	return "", false
}

// findReservationByID looks for an active or payment-pending RDS reserved DB
// instance with the given ReservedDBInstanceId (issue #641). It returns the
// reservation ID and true when such a reservation exists, so a re-driven
// purchase can short-circuit. Retired/expired reservations are excluded (same
// state filter as GetExistingCommitments) so a returned reservation does not
// suppress a legitimate fresh purchase.
func (c *Client) findReservationByID(ctx context.Context, reservationID string) (string, bool, error) {
	response, err := c.client.DescribeReservedDBInstances(ctx, &rds.DescribeReservedDBInstancesInput{
		ReservedDBInstanceId: aws.String(reservationID),
	})
	if err != nil {
		// RDS returns ReservedDBInstanceNotFound for an unknown reservation ID;
		// treat that as "not found" (a first-time purchase), not a lookup
		// failure, so it is not blocked. Any other error is a genuine failure.
		var notFound *types.ReservedDBInstanceNotFoundFault
		if errors.As(err, &notFound) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("failed to describe reserved DB instances for idempotency check: %w", err)
	}
	for _, ri := range response.ReservedDBInstances {
		state := aws.ToString(ri.State)
		if state != "active" && state != "payment-pending" {
			continue
		}
		if ri.ReservedDBInstanceId != nil {
			return aws.ToString(ri.ReservedDBInstanceId), true, nil
		}
	}
	return "", false, nil
}

// maxOfferingPages is the maximum number of DescribeReservedDBInstancesOfferings
// pages to walk before giving up. At MaxRecords=100 per page this caps the
// search at 500 offerings. Exceeding the cap returns a diagnostic error instead
// of timing out the Lambda budget (issue #688).
const maxOfferingPages = 5

// findOfferingID finds the appropriate RDS Reserved Instance offering ID.
// execID is the purchase execution UUID for log correlation; pass "" when
// calling outside of a purchase flow (ValidateOffering, GetOfferingDetails).
func (c *Client) findOfferingID(ctx context.Context, rec common.Recommendation, execID string) (string, error) {
	details, ok := rec.Details.(*common.DatabaseDetails)
	if !ok || details == nil {
		return "", fmt.Errorf("invalid service details for RDS")
	}
	// AZConfig must be explicitly set: single-AZ and multi-AZ RDS RIs have
	// different prices and do not cover each other's demand. An empty AZConfig
	// means the CE recommendation omitted DeploymentOption; guessing single-az
	// risks buying the wrong RI class. Fail loud so the caller can decide.
	// Validate the full enum, not just the empty case: a non-empty typo would
	// otherwise fall through to multiAZ==false in paginateRDSOfferings and
	// silently drive a single-AZ lookup -- the same mis-buy class as the old
	// default (CR #1085).
	switch details.AZConfig {
	case "single-az", "multi-az":
		// valid
	case "":
		return "", fmt.Errorf("RDS AZConfig is empty: CE recommendation did not include DeploymentOption; " +
			"refusing to guess single-az vs multi-az (see M4 in 19-hardcoded-fallbacks-aws.md)")
	default:
		return "", fmt.Errorf("invalid RDS AZConfig %q: must be single-az or multi-az", details.AZConfig)
	}
	offeringType, err := c.convertPaymentOption(rec.PaymentOption)
	if err != nil {
		return "", fmt.Errorf("invalid payment option: %w", err)
	}
	return c.paginateRDSOfferings(ctx, rec, details, offeringType, execID)
}

// rdsOfferingPageResult holds the outcome of a single DescribeReservedDBInstancesOfferings page.
type rdsOfferingPageResult struct {
	id     string  // non-empty when a match was found
	marker *string // pagination cursor for the next page; nil when exhausted
}

// fetchRDSOfferingPage calls DescribeReservedDBInstancesOfferings for one page and
// scans the results. It returns a match ID when found, a non-nil marker when more
// pages remain, or an error on API/offering-validation failure.
func (c *Client) fetchRDSOfferingPage(ctx context.Context, baseInput *rds.DescribeReservedDBInstancesOfferingsInput, marker *string, rec common.Recommendation, offeringType string, tag string, page int, t0 time.Time) (rdsOfferingPageResult, error) {
	input := *baseInput
	input.Marker = marker

	pageStart := time.Now()
	result, err := c.client.DescribeReservedDBInstancesOfferings(ctx, &input)
	if err != nil {
		log.Printf("purchase[%s]: RDS findOfferingID page %d failed after %s (total %s): %v",
			tag, page, time.Since(pageStart), time.Since(t0), err)
		return rdsOfferingPageResult{}, fmt.Errorf("failed to describe offerings: %w", err)
	}
	log.Printf("purchase[%s]: RDS findOfferingID page %d: %d offerings in %s",
		tag, page, len(result.ReservedDBInstancesOfferings), time.Since(pageStart))

	id, scanErr := scanRDSOfferingPage(result.ReservedDBInstancesOfferings, rec, offeringType)
	if scanErr != nil {
		return rdsOfferingPageResult{}, scanErr
	}
	if id != "" {
		log.Printf("purchase[%s]: RDS findOfferingID found match on page %d after %s total", tag, page, time.Since(t0))
		return rdsOfferingPageResult{id: id}, nil
	}
	var nextMarker *string
	if result.Marker != nil && aws.ToString(result.Marker) != "" {
		nextMarker = result.Marker
	}
	return rdsOfferingPageResult{marker: nextMarker}, nil
}

// paginateRDSOfferings walks DescribeReservedDBInstancesOfferings pages and returns
// the first matching offering ID. It caps at maxOfferingPages to prevent Lambda
// timeout exhaustion (issue #688).
func (c *Client) paginateRDSOfferings(ctx context.Context, rec common.Recommendation, details *common.DatabaseDetails, offeringType string, execID string) (string, error) {
	multiAZ := details.AZConfig == "multi-az"
	normalizedEngine, err := c.normalizeEngineName(details.Engine)
	if err != nil {
		return "", fmt.Errorf("cannot look up RDS offering: %w", err)
	}
	duration := c.getDurationString(rec.Term)

	tag := execID
	if tag == "" {
		tag = "no-exec"
	}
	t0 := time.Now()
	log.Printf("purchase[%s]: RDS findOfferingID starting (class=%s engine=%s multi-az=%v duration=%s payment=%s)",
		tag, rec.ResourceType, normalizedEngine, multiAZ, duration, offeringType)

	baseInput := &rds.DescribeReservedDBInstancesOfferingsInput{
		DBInstanceClass:    aws.String(rec.ResourceType),
		ProductDescription: aws.String(normalizedEngine),
		MultiAZ:            aws.Bool(multiAZ),
		Duration:           aws.String(duration),
		OfferingType:       aws.String(offeringType),
		MaxRecords:         aws.Int32(100),
	}

	var marker *string
	for page := 1; ; page++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if page > maxOfferingPages {
			return "", fmt.Errorf("pagination cap reached after %d pages for RDS %s %s multi-az=%v %s (issue #688)",
				maxOfferingPages, rec.ResourceType, details.Engine, multiAZ, rec.PaymentOption)
		}
		pr, err := c.fetchRDSOfferingPage(ctx, baseInput, marker, rec, offeringType, tag, page, t0)
		if err != nil {
			return "", err
		}
		if pr.id != "" {
			return pr.id, nil
		}
		if pr.marker == nil {
			break
		}
		marker = pr.marker
	}
	log.Printf("purchase[%s]: RDS findOfferingID exhausted pages in %s -- no match", tag, time.Since(t0))
	return "", fmt.Errorf("no offerings found for RDS %s %s multi-az=%v %s after %d page(s) (issue #688)",
		rec.ResourceType, details.Engine, multiAZ, rec.PaymentOption, maxOfferingPages)
}

// scanRDSOfferingPage finds a matching offering in a single page of results.
// Returns ("", nil) when no match is found on the page so the caller can continue paginating.
func scanRDSOfferingPage(offerings []types.ReservedDBInstancesOffering, rec common.Recommendation, wantType string) (string, error) {
	for _, o := range offerings {
		got := aws.ToString(o.OfferingType)
		if got != wantType {
			return "", fmt.Errorf("RDS offering %s has payment option %q, want %q (rec: %s %s) -- API filter mismatch",
				aws.ToString(o.ReservedDBInstancesOfferingId), got, wantType,
				rec.ResourceType, rec.PaymentOption)
		}
		return aws.ToString(o.ReservedDBInstancesOfferingId), nil
	}
	return "", nil
}

// ValidateOffering checks if an offering exists without purchasing
func (c *Client) ValidateOffering(ctx context.Context, rec *common.Recommendation) error {
	_, err := c.findOfferingID(ctx, *rec, "")
	return err
}

// GetOfferingDetails retrieves offering details
func (c *Client) GetOfferingDetails(ctx context.Context, rec *common.Recommendation) (*common.OfferingDetails, error) {
	offeringID, err := c.findOfferingID(ctx, *rec, "")
	if err != nil {
		return nil, err
	}

	input := &rds.DescribeReservedDBInstancesOfferingsInput{
		ReservedDBInstancesOfferingId: aws.String(offeringID),
	}

	result, err := c.client.DescribeReservedDBInstancesOfferings(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to get offering details: %w", err)
	}

	if len(result.ReservedDBInstancesOfferings) == 0 {
		return nil, fmt.Errorf("offering not found: %s", offeringID)
	}

	offering := result.ReservedDBInstancesOfferings[0]

	var durationStr string
	if offering.Duration != nil {
		durationStr = strconv.Itoa(int(*offering.Duration))
	}

	var offeringTypeStr string
	if offering.OfferingType != nil {
		offeringTypeStr = *offering.OfferingType
	}

	details := &common.OfferingDetails{
		OfferingID:    aws.ToString(offering.ReservedDBInstancesOfferingId),
		ResourceType:  aws.ToString(offering.DBInstanceClass),
		Term:          durationStr,
		PaymentOption: offeringTypeStr,
		UpfrontCost:   aws.ToFloat64(offering.FixedPrice),
		RecurringCost: aws.ToFloat64(offering.UsagePrice),
		Currency:      aws.ToString(offering.CurrencyCode),
	}

	return details, nil
}

// GetValidResourceTypes returns valid RDS instance types
func (c *Client) GetValidResourceTypes(ctx context.Context) ([]string, error) {
	instanceTypesMap := make(map[string]bool)
	var marker *string

	for {
		input := &rds.DescribeReservedDBInstancesOfferingsInput{
			Marker:     marker,
			MaxRecords: aws.Int32(100),
		}

		result, err := c.client.DescribeReservedDBInstancesOfferings(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("failed to describe RDS offerings: %w", err)
		}

		for _, offering := range result.ReservedDBInstancesOfferings {
			if offering.DBInstanceClass != nil {
				instanceTypesMap[*offering.DBInstanceClass] = true
			}
		}

		if result.Marker == nil || aws.ToString(result.Marker) == "" {
			break
		}
		marker = result.Marker
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

// getDurationString converts term string to duration string for RDS API
func (c *Client) getDurationString(term string) string {
	if term == "3yr" || term == "3" {
		return fmt.Sprintf("%d", ThreeYearSeconds)
	}
	return fmt.Sprintf("%d", OneYearSeconds)
}

// convertPaymentOption converts payment option to AWS string
func (c *Client) convertPaymentOption(option string) (string, error) {
	switch option {
	case "all-upfront":
		return "All Upfront", nil
	case "partial-upfront":
		return "Partial Upfront", nil
	case "no-upfront":
		return "No Upfront", nil
	default:
		return "", fmt.Errorf("unsupported payment option: %s", option)
	}
}

// normalizeEngineName maps an RDS engine string to the exact product-description
// value required by DescribeReservedDBInstancesOfferings. It returns an error
// for engine names that are ambiguous (Oracle, SQL Server -- multiple editions
// exist at different prices) or that contain "aurora" without specifying a
// database engine (aurora-mysql vs aurora-postgresql), so the caller never
// silently buys an offering for the wrong engine edition.
//
// Unambiguous engines (mysql, postgresql, mariadb) are returned verbatim after
// case-normalisation. Engine strings that are already in the canonical
// lower-case AWS form (e.g. "aurora-mysql") pass through unchanged.
func (c *Client) normalizeEngineName(engine string) (string, error) {
	engineLower := strings.ToLower(engine)

	if strings.Contains(engineLower, "aurora") {
		if strings.Contains(engineLower, "mysql") {
			return "aurora-mysql", nil
		}
		if strings.Contains(engineLower, "postgres") {
			return "aurora-postgresql", nil
		}
		return "", fmt.Errorf(
			"ambiguous Aurora engine %q: CE must supply the specific variant "+
				"(aurora-mysql or aurora-postgresql); refusing to guess",
			engine,
		)
	}

	if strings.Contains(engineLower, "mysql") {
		return "mysql", nil
	}
	if strings.Contains(engineLower, "postgres") {
		return "postgresql", nil
	}
	if strings.Contains(engineLower, "mariadb") {
		return "mariadb", nil
	}
	// Only the bare family name is ambiguous (CE returns "Oracle" / "SQL Server"
	// with no edition). An edition-qualified token like oracle-se2 or
	// sqlserver-web is already a valid RDS ProductDescription, so pass it through
	// rather than rejecting it -- rejecting it contradicted the "supply the exact
	// edition" guidance in this very error message (CR #1085).
	if engineLower == "oracle" {
		return "", fmt.Errorf(
			"ambiguous Oracle engine %q: CE must supply the exact edition "+
				"(e.g. oracle-se2, oracle-ee); refusing to guess",
			engine,
		)
	}
	if engineLower == "sqlserver" || engineLower == "sql-server" {
		return "", fmt.Errorf(
			"ambiguous SQL Server engine %q: CE must supply the exact edition "+
				"(e.g. sqlserver-se, sqlserver-ee, sqlserver-web, sqlserver-ex); refusing to guess",
			engine,
		)
	}

	return engineLower, nil
}

// createPurchaseTags creates standard tags for the purchase. The tag shape
// is shared across RDS/ElastiCache/MemoryDB via tagging.PurchasePairs; the
// only per-service customizations are the Purpose string and the AWS
// convention for the instance-type tag key.
func (c *Client) createPurchaseTags(rec common.Recommendation, source string) []types.Tag {
	pairs := tagging.PurchasePairs(rec, "Reserved Instance Purchase", "ResourceType", source)
	out := make([]types.Tag, len(pairs))
	for i, p := range pairs {
		out[i] = types.Tag{Key: aws.String(p.Key), Value: aws.String(p.Value)}
	}
	return out
}
