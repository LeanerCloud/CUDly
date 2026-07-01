// Package elasticache provides AWS ElastiCache Reserved Cache Nodes client
package elasticache

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/elasticache"
	"github.com/aws/aws-sdk-go-v2/service/elasticache/types"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/providers/aws/internal/purchasecfg"
	"github.com/LeanerCloud/CUDly/providers/aws/internal/tagging"
)

// ElastiCacheAPI defines the interface for ElastiCache operations (enables mocking)
type ElastiCacheAPI interface {
	DescribeReservedCacheNodesOfferings(ctx context.Context, params *elasticache.DescribeReservedCacheNodesOfferingsInput, optFns ...func(*elasticache.Options)) (*elasticache.DescribeReservedCacheNodesOfferingsOutput, error)
	PurchaseReservedCacheNodesOffering(ctx context.Context, params *elasticache.PurchaseReservedCacheNodesOfferingInput, optFns ...func(*elasticache.Options)) (*elasticache.PurchaseReservedCacheNodesOfferingOutput, error)
	DescribeReservedCacheNodes(ctx context.Context, params *elasticache.DescribeReservedCacheNodesInput, optFns ...func(*elasticache.Options)) (*elasticache.DescribeReservedCacheNodesOutput, error)
}

// Client handles AWS ElastiCache Reserved Cache Nodes
type Client struct {
	client ElastiCacheAPI
	region string
}

// NewClient creates a new ElastiCache client with purchase-path retry/timeout
// settings. See purchasecfg for rationale.
func NewClient(cfg aws.Config) *Client {
	pcfg := purchasecfg.NewConfig(cfg)
	return &Client{
		client: elasticache.NewFromConfig(pcfg),
		region: cfg.Region,
	}
}

// SetElastiCacheAPI sets a custom ElastiCache API client (for testing)
func (c *Client) SetElastiCacheAPI(api ElastiCacheAPI) {
	c.client = api
}

// GetServiceType returns the service type
func (c *Client) GetServiceType() common.ServiceType {
	return common.ServiceCache
}

// GetRegion returns the region
func (c *Client) GetRegion() string {
	return c.region
}

// GetRecommendations returns empty as ElastiCache uses centralized Cost Explorer recommendations
func (c *Client) GetRecommendations(_ context.Context, _ *common.RecommendationParams) ([]common.Recommendation, error) {
	return []common.Recommendation{}, nil
}

// GetExistingCommitments retrieves existing ElastiCache Reserved Cache Nodes
func (c *Client) GetExistingCommitments(ctx context.Context) ([]common.Commitment, error) {
	commitments := make([]common.Commitment, 0)
	var marker *string

	for {
		input := &elasticache.DescribeReservedCacheNodesInput{
			Marker:     marker,
			MaxRecords: aws.Int32(100),
		}

		response, err := c.client.DescribeReservedCacheNodes(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("failed to describe reserved cache nodes: %w", err)
		}

		for _, node := range response.ReservedCacheNodes {
			state := aws.ToString(node.State)
			if state != "active" && state != "payment-pending" {
				continue
			}

			duration := aws.ToInt32(node.Duration)
			termMonths := 12
			if duration == ThreeYearSeconds {
				termMonths = 36
			}

			commitment := common.Commitment{
				Provider:       common.ProviderAWS,
				CommitmentID:   aws.ToString(node.ReservedCacheNodeId),
				CommitmentType: common.CommitmentReservedInstance,
				Service:        common.ServiceCache,
				Region:         c.region,
				ResourceType:   aws.ToString(node.CacheNodeType),
				Count:          int(aws.ToInt32(node.CacheNodeCount)),
				State:          state,
				StartDate:      aws.ToTime(node.StartTime),
				EndDate:        aws.ToTime(node.StartTime).AddDate(0, termMonths, 0),
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

// PurchaseCommitment purchases an ElastiCache Reserved Cache Node
func (c *Client) PurchaseCommitment(ctx context.Context, rec common.Recommendation, opts common.PurchaseOptions) (common.PurchaseResult, error) {
	result := common.PurchaseResult{
		Recommendation: rec,
		DryRun:         false,
		Success:        false,
		Timestamp:      time.Now(),
	}

	offeringID, err := c.findOfferingID(ctx, rec, opts.ExecutionID)
	if err != nil {
		result.Error = fmt.Errorf("failed to find offering: %w", err)
		return result, result.Error
	}

	// When an idempotency token is supplied (issue #641) the reservation ID is
	// derived deterministically from it, so a re-drive sends the identical
	// ReservedCacheNodeId and ElastiCache rejects the duplicate server-side
	// (ReservedCacheNodeAlreadyExistsFault). On the no-token CLI path
	// (issue #687) compose a rich, self-describing identifier matching the
	// Azure DisplayName format so operators can identify the reservation in
	// the AWS console without cross-referencing CUDly's purchase audit log.
	reservationID := common.IdempotentReservationID("elasticache-id-", opts.IdempotencyToken)
	if reservationID == "" {
		reservationID = common.BuildReservationName(common.ReservationNameFields{
			Service:      "cache",
			Region:       rec.Region,
			ResourceType: rec.ResourceType,
			Count:        rec.Count,
			Term:         rec.Term,
			Payment:      rec.PaymentOption,
			Now:          time.Now(),
		}, "elasticache-reserved-")
	}

	// Idempotency dedupe guard (issue #641): short-circuit if a reservation
	// already exists under the derived ID; fail loud on lookup error.
	if existingID, shortCircuit, guardErr := c.idempotencyGuard(ctx, opts.IdempotencyToken, reservationID); guardErr != nil {
		result.Error = guardErr
		return result, result.Error
	} else if shortCircuit {
		result.Success = true
		result.CommitmentID = existingID
		return result, nil
	}

	input := &elasticache.PurchaseReservedCacheNodesOfferingInput{
		ReservedCacheNodesOfferingId: aws.String(offeringID),
		CacheNodeCount:               aws.Int32(int32(rec.Count)),
		ReservedCacheNodeId:          aws.String(reservationID),
		Tags:                         c.createPurchaseTags(rec, opts.Source),
	}

	response, err := c.client.PurchaseReservedCacheNodesOffering(ctx, input)
	if err != nil {
		if existingID, recovered := c.recoverAlreadyExists(ctx, opts.IdempotencyToken, reservationID, err); recovered {
			result.Success = true
			result.CommitmentID = existingID
			return result, nil
		}
		result.Error = fmt.Errorf("failed to purchase Reserved Cache Node: %w", err)
		return result, result.Error
	}

	if response.ReservedCacheNode != nil {
		result.Success = true
		result.CommitmentID = aws.ToString(response.ReservedCacheNode.ReservedCacheNodeId)
		if response.ReservedCacheNode.FixedPrice != nil {
			result.Cost = *response.ReservedCacheNode.FixedPrice
		}
	} else {
		result.Error = fmt.Errorf("purchase response was empty")
		return result, result.Error
	}

	return result, nil
}

// findReservationByID looks for an active or payment-pending reserved cache node
// with the given ReservedCacheNodeId (issue #641), so a re-driven purchase can
// short-circuit instead of buying a second node. Retired/expired nodes are
// excluded (same state filter as GetExistingCommitments).
func (c *Client) findReservationByID(ctx context.Context, reservationID string) (string, bool, error) {
	response, err := c.client.DescribeReservedCacheNodes(ctx, &elasticache.DescribeReservedCacheNodesInput{
		ReservedCacheNodeId: aws.String(reservationID),
	})
	if err != nil {
		// ElastiCache returns ReservedCacheNodeNotFound for an unknown reservation
		// ID; treat that as "not found" (a first-time purchase), not a lookup
		// failure. Any other error is a genuine failure.
		var notFound *types.ReservedCacheNodeNotFoundFault
		if errors.As(err, &notFound) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("failed to describe reserved cache nodes for idempotency check: %w", err)
	}
	for _, node := range response.ReservedCacheNodes {
		state := aws.ToString(node.State)
		if state != "active" && state != "payment-pending" {
			continue
		}
		if node.ReservedCacheNodeId != nil {
			return aws.ToString(node.ReservedCacheNodeId), true, nil
		}
	}
	return "", false, nil
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
		return "", false, fmt.Errorf("idempotency lookup failed before ElastiCache purchase (refusing to purchase to avoid a possible double-buy): %w", lookupErr)
	}
	if found {
		log.Printf("ElastiCache reservation for idempotency token %s already exists (%s); skipping purchase (issue #641 re-drive)", common.MaskToken(token), existingID)
		return existingID, true, nil
	}
	return "", false, nil
}

// recoverAlreadyExists handles the native server-side dedupe backstop (issue
// #641): if the by-ID guard missed the existing reservation but AWS rejected the
// duplicate ID with ReservedCacheNodeAlreadyExistsFault, it re-Describes by ID
// and returns (existingID, true) so the re-drive recovers it instead of erroring.
func (c *Client) recoverAlreadyExists(ctx context.Context, token, reservationID string, purchaseErr error) (string, bool) {
	if token == "" {
		return "", false
	}
	var already *types.ReservedCacheNodeAlreadyExistsFault
	if !errors.As(purchaseErr, &already) {
		return "", false
	}
	existingID, found, lookupErr := c.findReservationByID(ctx, reservationID)
	if lookupErr == nil && found {
		log.Printf("ElastiCache reservation %s already existed at purchase time; treating as idempotent re-drive (issue #641)", existingID)
		return existingID, true
	}
	return "", false
}

// maxOfferingPages is the maximum number of DescribeReservedCacheNodesOfferings
// pages to walk before giving up. At MaxRecords=100 per page this caps the
// search at 500 offerings. Exceeding the cap returns a diagnostic error instead
// of timing out the Lambda budget (issue #688).
const maxOfferingPages = 5

// findOfferingID finds the appropriate Reserved Cache Node offering ID.
// execID is the purchase execution UUID for log correlation; pass "" when
// calling outside of a purchase flow (ValidateOffering, GetOfferingDetails).
func (c *Client) findOfferingID(ctx context.Context, rec common.Recommendation, execID string) (string, error) {
	details, ok := rec.Details.(*common.CacheDetails)
	if !ok || details == nil {
		return "", fmt.Errorf("invalid service details for ElastiCache")
	}
	offeringType, err := c.convertPaymentOption(rec.PaymentOption)
	if err != nil {
		return "", err
	}
	return c.paginateElastiCacheOfferings(ctx, rec, details, offeringType, execID)
}

// paginateElastiCacheOfferings walks DescribeReservedCacheNodesOfferings pages and returns
// the first matching offering ID. It caps at maxOfferingPages to prevent Lambda
// timeout exhaustion (issue #688).
func (c *Client) paginateElastiCacheOfferings(ctx context.Context, rec common.Recommendation, details *common.CacheDetails, offeringType, execID string) (string, error) {
	duration := c.getDurationString(rec.Term)
	tag := execID
	if tag == "" {
		tag = "no-exec"
	}
	t0 := time.Now()
	log.Printf("purchase[%s]: ElastiCache findOfferingID starting (nodeType=%s engine=%s duration=%s payment=%s)",
		tag, rec.ResourceType, details.Engine, duration, offeringType)

	var marker *string
	page := 0
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		page++
		if page > maxOfferingPages {
			return "", fmt.Errorf("pagination cap reached after %d pages for ElastiCache %s %s %s (issue #688)",
				maxOfferingPages, rec.ResourceType, details.Engine, rec.PaymentOption)
		}
		input := &elasticache.DescribeReservedCacheNodesOfferingsInput{
			CacheNodeType:      aws.String(rec.ResourceType),
			ProductDescription: aws.String(details.Engine),
			Duration:           aws.String(duration),
			OfferingType:       aws.String(offeringType),
			MaxRecords:         aws.Int32(100),
			Marker:             marker,
		}
		pageStart := time.Now()
		result, err := c.client.DescribeReservedCacheNodesOfferings(ctx, input)
		if err != nil {
			log.Printf("purchase[%s]: ElastiCache findOfferingID page %d failed after %s (total %s): %v",
				tag, page, time.Since(pageStart), time.Since(t0), err)
			return "", fmt.Errorf("failed to describe offerings: %w", err)
		}
		log.Printf("purchase[%s]: ElastiCache findOfferingID page %d: %d offerings in %s",
			tag, page, len(result.ReservedCacheNodesOfferings), time.Since(pageStart))
		if id, scanErr := scanElastiCacheOfferingPage(result.ReservedCacheNodesOfferings, rec, offeringType); scanErr != nil {
			return "", scanErr
		} else if id != "" {
			log.Printf("purchase[%s]: ElastiCache findOfferingID found match on page %d after %s total",
				tag, page, time.Since(t0))
			return id, nil
		}
		if result.Marker == nil || aws.ToString(result.Marker) == "" {
			break
		}
		marker = result.Marker
	}
	log.Printf("purchase[%s]: ElastiCache findOfferingID exhausted %d page(s) in %s -- no match",
		tag, page, time.Since(t0))
	return "", fmt.Errorf("no offerings found for ElastiCache %s %s %s after %d page(s) (issue #688)",
		rec.ResourceType, details.Engine, rec.PaymentOption, page)
}

// scanElastiCacheOfferingPage finds a matching offering in a single page of results.
// Returns ("", nil) when no match is found on the page so the caller can continue paginating.
func scanElastiCacheOfferingPage(offerings []types.ReservedCacheNodesOffering, rec common.Recommendation, wantType string) (string, error) {
	for _, o := range offerings {
		got := aws.ToString(o.OfferingType)
		if got != wantType {
			return "", fmt.Errorf("ElastiCache offering %s has payment option %q, want %q (rec: %s %s) -- API filter mismatch",
				aws.ToString(o.ReservedCacheNodesOfferingId), got, wantType,
				rec.ResourceType, rec.PaymentOption)
		}
		return aws.ToString(o.ReservedCacheNodesOfferingId), nil
	}
	return "", nil
}

// ValidateOffering checks if an offering exists without purchasing
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

	input := &elasticache.DescribeReservedCacheNodesOfferingsInput{
		ReservedCacheNodesOfferingId: aws.String(offeringID),
	}

	result, err := c.client.DescribeReservedCacheNodesOfferings(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to get offering details: %w", err)
	}

	if len(result.ReservedCacheNodesOfferings) == 0 {
		return nil, fmt.Errorf("offering not found: %s", offeringID)
	}

	offering := result.ReservedCacheNodesOfferings[0]

	details := &common.OfferingDetails{
		OfferingID:    aws.ToString(offering.ReservedCacheNodesOfferingId),
		ResourceType:  aws.ToString(offering.CacheNodeType),
		Term:          fmt.Sprintf("%d", aws.ToInt32(offering.Duration)),
		PaymentOption: aws.ToString(offering.OfferingType),
		UpfrontCost:   aws.ToFloat64(offering.FixedPrice),
		RecurringCost: aws.ToFloat64(offering.UsagePrice),
		Currency:      "USD",
	}

	return details, nil
}

// GetValidResourceTypes returns valid ElastiCache node types
func (c *Client) GetValidResourceTypes(ctx context.Context) ([]string, error) {
	instanceTypesMap := make(map[string]bool)
	var marker *string

	for {
		input := &elasticache.DescribeReservedCacheNodesOfferingsInput{
			Marker:     marker,
			MaxRecords: aws.Int32(100),
		}

		result, err := c.client.DescribeReservedCacheNodesOfferings(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("failed to describe ElastiCache offerings: %w", err)
		}

		for _, offering := range result.ReservedCacheNodesOfferings {
			if offering.CacheNodeType != nil {
				instanceTypesMap[*offering.CacheNodeType] = true
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

// getDurationString converts term string to duration string
func (c *Client) getDurationString(term string) string {
	if term == "3yr" || term == "3" {
		return fmt.Sprintf("%d", ThreeYearSeconds)
	}
	return fmt.Sprintf("%d", OneYearSeconds)
}

// convertPaymentOption converts payment option to AWS string.
// Returns an error on unknown values so unsupported payment options surface
// at the API boundary instead of being silently coerced to "Partial Upfront"
// and committing the buyer to the wrong payment terms.
func (c *Client) convertPaymentOption(option string) (string, error) {
	switch option {
	case "all-upfront":
		return "All Upfront", nil
	case "partial-upfront":
		return "Partial Upfront", nil
	case "no-upfront":
		return "No Upfront", nil
	default:
		return "", fmt.Errorf("unsupported ElastiCache payment option %q (want all-upfront, partial-upfront, or no-upfront)", option)
	}
}

// createPurchaseTags creates standard tags for the purchase. The tag shape
// is shared across RDS/ElastiCache/MemoryDB via tagging.PurchasePairs; the
// only per-service customizations are the Purpose string and the AWS
// convention for the instance-type tag key.
func (c *Client) createPurchaseTags(rec common.Recommendation, source string) []types.Tag {
	pairs := tagging.PurchasePairs(rec, "Reserved Cache Node Purchase", "NodeType", source)
	out := make([]types.Tag, len(pairs))
	for i, p := range pairs {
		out[i] = types.Tag{Key: aws.String(p.Key), Value: aws.String(p.Value)}
	}
	return out
}
