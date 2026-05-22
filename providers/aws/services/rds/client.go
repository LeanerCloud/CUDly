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

// NewClient creates a new RDS client
func NewClient(cfg aws.Config) *Client {
	return &Client{
		client: rds.NewFromConfig(cfg),
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
func (c *Client) GetRecommendations(ctx context.Context, params common.RecommendationParams) ([]common.Recommendation, error) {
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
func (c *Client) PurchaseCommitment(ctx context.Context, rec common.Recommendation, opts common.PurchaseOptions) (common.PurchaseResult, error) {
	result := common.PurchaseResult{
		Recommendation: rec,
		DryRun:         false,
		Success:        false,
		Timestamp:      time.Now(),
	}

	// Find the offering ID
	offeringID, err := c.findOfferingID(ctx, rec)
	if err != nil {
		result.Error = fmt.Errorf("failed to find offering: %w", err)
		return result, result.Error
	}

	reservationID := c.deriveReservationID(rec, opts)

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
		Tags:                          c.createPurchaseTags(rec, opts.Source),
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
// caller-supplied descriptive ID and falls back to a generic timestamped one
// (prior non-idempotent behaviour).
func (c *Client) deriveReservationID(rec common.Recommendation, opts common.PurchaseOptions) string {
	if id := common.IdempotentReservationID("rds-id-", opts.IdempotencyToken); id != "" {
		return id
	}
	rawID := opts.ReservationID
	if rawID == "" {
		rawID = fmt.Sprintf("rds-%s-%d", rec.ResourceType, time.Now().Unix())
	}
	return common.SanitizeReservationID(rawID, "rds-reserved-")
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

// findOfferingID finds the appropriate RDS Reserved Instance offering ID
func (c *Client) findOfferingID(ctx context.Context, rec common.Recommendation) (string, error) {
	details, ok := rec.Details.(*common.DatabaseDetails)
	if !ok || details == nil {
		return "", fmt.Errorf("invalid service details for RDS")
	}
	offeringType, err := c.convertPaymentOption(rec.PaymentOption)
	if err != nil {
		return "", fmt.Errorf("invalid payment option: %w", err)
	}
	return c.paginateRDSOfferings(ctx, rec, details, offeringType)
}

// paginateRDSOfferings walks DescribeReservedDBInstancesOfferings pages and returns
// the first matching offering ID. It caps at maxOfferingPages to prevent Lambda
// timeout exhaustion (issue #688).
func (c *Client) paginateRDSOfferings(ctx context.Context, rec common.Recommendation, details *common.DatabaseDetails, offeringType string) (string, error) {
	multiAZ := details.AZConfig == "multi-az"
	normalizedEngine := c.normalizeEngineName(details.Engine)
	duration := c.getDurationString(rec.Term)

	var marker *string
	page := 0
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		page++
		if page > maxOfferingPages {
			return "", fmt.Errorf("pagination cap reached after %d pages for RDS %s %s multi-az=%v %s (issue #688)",
				maxOfferingPages, rec.ResourceType, details.Engine, multiAZ, rec.PaymentOption)
		}
		input := &rds.DescribeReservedDBInstancesOfferingsInput{
			DBInstanceClass:    aws.String(rec.ResourceType),
			ProductDescription: aws.String(normalizedEngine),
			MultiAZ:            aws.Bool(multiAZ),
			Duration:           aws.String(duration),
			OfferingType:       aws.String(offeringType),
			MaxRecords:         aws.Int32(100),
			Marker:             marker,
		}
		pageStart := time.Now()
		result, err := c.client.DescribeReservedDBInstancesOfferings(ctx, input)
		if err != nil {
			return "", fmt.Errorf("failed to describe offerings: %w", err)
		}
		log.Printf("RDS findOfferingID page %d: %d offerings in %s",
			page, len(result.ReservedDBInstancesOfferings), time.Since(pageStart))
		if id, scanErr := scanRDSOfferingPage(result.ReservedDBInstancesOfferings, rec, offeringType); scanErr != nil {
			return "", scanErr
		} else if id != "" {
			return id, nil
		}
		if result.Marker == nil {
			break
		}
		marker = result.Marker
	}
	return "", fmt.Errorf("no offerings found for RDS %s %s multi-az=%v %s after %d page(s) (issue #688)",
		rec.ResourceType, details.Engine, multiAZ, rec.PaymentOption, page)
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

// normalizeEngineName converts engine names to AWS API format
func (c *Client) normalizeEngineName(engine string) string {
	engineLower := strings.ToLower(engine)

	if strings.Contains(engineLower, "aurora") {
		if strings.Contains(engineLower, "mysql") {
			return "aurora-mysql"
		}
		if strings.Contains(engineLower, "postgres") {
			return "aurora-postgresql"
		}
		log.Printf("WARNING: Unknown Aurora variant %q, defaulting to aurora-mysql", engine)
		return "aurora-mysql"
	}

	if strings.Contains(engineLower, "mysql") {
		return "mysql"
	}
	if strings.Contains(engineLower, "postgres") {
		return "postgresql"
	}
	if strings.Contains(engineLower, "mariadb") {
		return "mariadb"
	}
	if strings.Contains(engineLower, "oracle") {
		return "oracle-se2"
	}
	if strings.Contains(engineLower, "sqlserver") || strings.Contains(engineLower, "sql-server") {
		return "sqlserver-se"
	}

	return engineLower
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
