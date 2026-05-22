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

// NewClient creates a new ElastiCache client
func NewClient(cfg aws.Config) *Client {
	return &Client{
		client: elasticache.NewFromConfig(cfg),
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
func (c *Client) GetRecommendations(ctx context.Context, params common.RecommendationParams) ([]common.Recommendation, error) {
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

	offeringID, err := c.findOfferingID(ctx, rec)
	if err != nil {
		result.Error = fmt.Errorf("failed to find offering: %w", err)
		return result, result.Error
	}

	// When an idempotency token is supplied (issue #641) the reservation ID is
	// derived deterministically from it, so a re-drive sends the identical
	// ReservedCacheNodeId and ElastiCache rejects the duplicate server-side
	// (ReservedCacheNodeAlreadyExistsFault). Otherwise keep the prior
	// timestamp-based ID (non-idempotent path).
	reservationID := common.IdempotentReservationID("elasticache-id-", opts.IdempotencyToken)
	if reservationID == "" {
		reservationID = common.SanitizeReservationID(fmt.Sprintf("elasticache-%s-%d", rec.ResourceType, time.Now().Unix()), "elasticache-reserved-")
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

// findOfferingID finds the appropriate Reserved Cache Node offering ID
func (c *Client) findOfferingID(ctx context.Context, rec common.Recommendation) (string, error) {
	details, ok := rec.Details.(*common.CacheDetails)
	if !ok || details == nil {
		return "", fmt.Errorf("invalid service details for ElastiCache")
	}

	duration := c.getDurationString(rec.Term)
	offeringType := c.convertPaymentOption(rec.PaymentOption)

	var marker *string
	for {
		input := &elasticache.DescribeReservedCacheNodesOfferingsInput{
			CacheNodeType:      aws.String(rec.ResourceType),
			ProductDescription: aws.String(details.Engine),
			Duration:           aws.String(duration),
			OfferingType:       aws.String(offeringType),
			MaxRecords:         aws.Int32(100),
			Marker:             marker,
		}

		result, err := c.client.DescribeReservedCacheNodesOfferings(ctx, input)
		if err != nil {
			return "", fmt.Errorf("failed to describe offerings: %w", err)
		}

		if len(result.ReservedCacheNodesOfferings) > 0 {
			return aws.ToString(result.ReservedCacheNodesOfferings[0].ReservedCacheNodesOfferingId), nil
		}

		if result.Marker == nil || aws.ToString(result.Marker) == "" {
			break
		}
		marker = result.Marker
	}

	return "", fmt.Errorf("no offerings found for %s %s %s",
		rec.ResourceType, details.Engine, duration)
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

// convertPaymentOption converts payment option to AWS string
func (c *Client) convertPaymentOption(option string) string {
	switch option {
	case "all-upfront":
		return "All Upfront"
	case "partial-upfront":
		return "Partial Upfront"
	case "no-upfront":
		return "No Upfront"
	default:
		return "Partial Upfront"
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
