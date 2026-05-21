// Package memorydb provides AWS MemoryDB Reserved Nodes client
package memorydb

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/memorydb"
	"github.com/aws/aws-sdk-go-v2/service/memorydb/types"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/providers/aws/internal/tagging"
)

// MemoryDBAPI defines the interface for MemoryDB operations (enables mocking)
type MemoryDBAPI interface {
	PurchaseReservedNodesOffering(ctx context.Context, params *memorydb.PurchaseReservedNodesOfferingInput, optFns ...func(*memorydb.Options)) (*memorydb.PurchaseReservedNodesOfferingOutput, error)
	DescribeReservedNodesOfferings(ctx context.Context, params *memorydb.DescribeReservedNodesOfferingsInput, optFns ...func(*memorydb.Options)) (*memorydb.DescribeReservedNodesOfferingsOutput, error)
	DescribeReservedNodes(ctx context.Context, params *memorydb.DescribeReservedNodesInput, optFns ...func(*memorydb.Options)) (*memorydb.DescribeReservedNodesOutput, error)
}

// Client handles AWS MemoryDB Reserved Nodes
type Client struct {
	client MemoryDBAPI
	region string
}

// NewClient creates a new MemoryDB client
func NewClient(cfg aws.Config) *Client {
	return &Client{
		client: memorydb.NewFromConfig(cfg),
		region: cfg.Region,
	}
}

// SetMemoryDBAPI sets a custom MemoryDB API client (for testing)
func (c *Client) SetMemoryDBAPI(api MemoryDBAPI) {
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

// GetRecommendations returns empty as MemoryDB uses centralized Cost Explorer recommendations
func (c *Client) GetRecommendations(ctx context.Context, params common.RecommendationParams) ([]common.Recommendation, error) {
	return []common.Recommendation{}, nil
}

// GetExistingCommitments retrieves existing MemoryDB Reserved Nodes
func (c *Client) GetExistingCommitments(ctx context.Context) ([]common.Commitment, error) {
	commitments := make([]common.Commitment, 0)
	var nextToken *string

	for {
		input := &memorydb.DescribeReservedNodesInput{
			NextToken:  nextToken,
			MaxResults: aws.Int32(100),
		}

		response, err := c.client.DescribeReservedNodes(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("failed to describe reserved nodes: %w", err)
		}

		for _, node := range response.ReservedNodes {
			state := aws.ToString(node.State)
			if state != "active" && state != "payment-pending" {
				continue
			}

			termMonths := getTermMonthsFromDuration(node.Duration)

			commitment := common.Commitment{
				Provider:       common.ProviderAWS,
				CommitmentID:   aws.ToString(node.ReservationId),
				CommitmentType: common.CommitmentReservedInstance,
				Service:        common.ServiceMemoryDB,
				Region:         c.region,
				ResourceType:   aws.ToString(node.NodeType),
				Count:          int(node.NodeCount),
				State:          state,
				StartDate:      aws.ToTime(node.StartTime),
				EndDate:        aws.ToTime(node.StartTime).AddDate(0, termMonths, 0),
			}

			commitments = append(commitments, commitment)
		}

		if response.NextToken == nil || aws.ToString(response.NextToken) == "" {
			break
		}
		nextToken = response.NextToken
	}

	return commitments, nil
}

// PurchaseCommitment purchases a MemoryDB Reserved Node
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
	// ReservationId and MemoryDB rejects the duplicate server-side
	// (ReservedNodeAlreadyExistsFault). Otherwise keep the prior timestamp-based
	// ID (non-idempotent path).
	reservationID := common.IdempotentReservationID("memorydb-id-", opts.IdempotencyToken)
	if reservationID == "" {
		reservationID = common.SanitizeReservationID(fmt.Sprintf("memorydb-%s-%d", rec.ResourceType, time.Now().Unix()), "memorydb-reserved-")
	}

	// Idempotency dedupe guard (issue #641): short-circuit if a reserved node
	// already exists under the derived ID; fail loud on lookup error.
	if existingID, shortCircuit, guardErr := c.idempotencyGuard(ctx, opts.IdempotencyToken, reservationID); guardErr != nil {
		result.Error = guardErr
		return result, result.Error
	} else if shortCircuit {
		result.Success = true
		result.CommitmentID = existingID
		return result, nil
	}

	input := &memorydb.PurchaseReservedNodesOfferingInput{
		ReservedNodesOfferingId: aws.String(offeringID),
		ReservationId:           aws.String(reservationID),
		NodeCount:               aws.Int32(int32(rec.Count)),
		Tags:                    c.createPurchaseTags(rec, opts.Source),
	}

	response, err := c.client.PurchaseReservedNodesOffering(ctx, input)
	if err != nil {
		if existingID, recovered := c.recoverAlreadyExists(ctx, opts.IdempotencyToken, reservationID, err); recovered {
			result.Success = true
			result.CommitmentID = existingID
			return result, nil
		}
		result.Error = fmt.Errorf("failed to purchase MemoryDB Reserved Nodes: %w", err)
		return result, result.Error
	}

	if response.ReservedNode != nil {
		result.Success = true
		result.CommitmentID = aws.ToString(response.ReservedNode.ReservationId)
		result.Cost = response.ReservedNode.FixedPrice
	} else {
		result.Error = fmt.Errorf("purchase response was empty")
		return result, result.Error
	}

	return result, nil
}

// findReservationByID looks for an active or payment-pending MemoryDB reserved
// node with the given ReservationId (issue #641), so a re-driven purchase can
// short-circuit instead of buying a second node. Retired/expired nodes are
// excluded (same state filter as GetExistingCommitments).
func (c *Client) findReservationByID(ctx context.Context, reservationID string) (string, bool, error) {
	response, err := c.client.DescribeReservedNodes(ctx, &memorydb.DescribeReservedNodesInput{
		ReservationId: aws.String(reservationID),
	})
	if err != nil {
		// MemoryDB returns ReservedNodeNotFoundFault for an unknown reservation
		// ID; treat that as "not found" (no existing reservation), not a lookup
		// failure, so a first-time purchase is not blocked.
		var notFound *types.ReservedNodeNotFoundFault
		if errors.As(err, &notFound) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("failed to describe reserved nodes for idempotency check: %w", err)
	}
	for _, node := range response.ReservedNodes {
		state := aws.ToString(node.State)
		if state != "active" && state != "payment-pending" {
			continue
		}
		if node.ReservationId != nil {
			return aws.ToString(node.ReservationId), true, nil
		}
	}
	return "", false, nil
}

// idempotencyGuard short-circuits a re-drive (issue #641): when token is set, it
// reports (existingID, true, nil) if a reserved node already exists under
// reservationID, ("", false, nil) for a first-time purchase, or a fail-loud
// error on lookup failure. With an empty token it is a no-op.
func (c *Client) idempotencyGuard(ctx context.Context, token, reservationID string) (string, bool, error) {
	if token == "" {
		return "", false, nil
	}
	existingID, found, lookupErr := c.findReservationByID(ctx, reservationID)
	if lookupErr != nil {
		return "", false, fmt.Errorf("idempotency lookup failed before MemoryDB purchase (refusing to purchase to avoid a possible double-buy): %w", lookupErr)
	}
	if found {
		log.Printf("MemoryDB reservation for idempotency token %s already exists (%s); skipping purchase (issue #641 re-drive)", token, existingID)
		return existingID, true, nil
	}
	return "", false, nil
}

// recoverAlreadyExists handles the native server-side dedupe backstop (issue
// #641): if the by-ID guard missed the existing reservation but AWS rejected the
// duplicate ID with ReservedNodeAlreadyExistsFault, it re-Describes by ID and
// returns (existingID, true) so the re-drive recovers it instead of erroring.
func (c *Client) recoverAlreadyExists(ctx context.Context, token, reservationID string, purchaseErr error) (string, bool) {
	if token == "" {
		return "", false
	}
	var already *types.ReservedNodeAlreadyExistsFault
	if !errors.As(purchaseErr, &already) {
		return "", false
	}
	existingID, found, lookupErr := c.findReservationByID(ctx, reservationID)
	if lookupErr == nil && found {
		log.Printf("MemoryDB reservation %s already existed at purchase time; treating as idempotent re-drive (issue #641)", existingID)
		return existingID, true
	}
	return "", false
}

// findOfferingID finds the appropriate Reserved Node offering ID
func (c *Client) findOfferingID(ctx context.Context, rec common.Recommendation) (string, error) {
	requiredMonths := c.getTermMonthsFromString(rec.Term)
	var nextToken *string

	for {
		input := &memorydb.DescribeReservedNodesOfferingsInput{
			NodeType:   aws.String(rec.ResourceType),
			MaxResults: aws.Int32(100),
			NextToken:  nextToken,
		}

		result, err := c.client.DescribeReservedNodesOfferings(ctx, input)
		if err != nil {
			return "", fmt.Errorf("failed to describe offerings: %w", err)
		}

		for _, offering := range result.ReservedNodesOfferings {
			if offering.NodeType != nil && *offering.NodeType == rec.ResourceType {
				if c.matchesDuration(offering.Duration, requiredMonths) &&
					c.matchesOfferingType(offering.OfferingType, rec.PaymentOption) {
					return aws.ToString(offering.ReservedNodesOfferingId), nil
				}
			}
		}

		if result.NextToken == nil || aws.ToString(result.NextToken) == "" {
			break
		}
		nextToken = result.NextToken
	}

	return "", fmt.Errorf("no offerings found for %s", rec.ResourceType)
}

// matchesDuration checks if the offering duration matches
func (c *Client) matchesDuration(offeringDuration int32, requiredMonths int) bool {
	offeringMonths := offeringDuration / 2592000
	return int(offeringMonths) >= requiredMonths-1 && int(offeringMonths) <= requiredMonths+1
}

// matchesOfferingType checks if the offering type matches
func (c *Client) matchesOfferingType(offeringType *string, paymentOption string) bool {
	if offeringType == nil {
		return false
	}

	switch paymentOption {
	case "all-upfront":
		return *offeringType == "All Upfront"
	case "partial-upfront":
		return *offeringType == "Partial Upfront"
	case "no-upfront":
		return *offeringType == "No Upfront"
	default:
		return false
	}
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

	input := &memorydb.DescribeReservedNodesOfferingsInput{
		ReservedNodesOfferingId: aws.String(offeringID),
		MaxResults:              aws.Int32(1),
	}

	result, err := c.client.DescribeReservedNodesOfferings(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to get offering details: %w", err)
	}

	if len(result.ReservedNodesOfferings) == 0 {
		return nil, fmt.Errorf("offering not found: %s", offeringID)
	}

	offering := result.ReservedNodesOfferings[0]

	details := &common.OfferingDetails{
		OfferingID:    aws.ToString(offering.ReservedNodesOfferingId),
		ResourceType:  aws.ToString(offering.NodeType),
		Term:          fmt.Sprintf("%d", offering.Duration),
		PaymentOption: aws.ToString(offering.OfferingType),
		UpfrontCost:   offering.FixedPrice,
		Currency:      "USD",
	}

	for _, charge := range offering.RecurringCharges {
		if charge.RecurringChargeFrequency != nil {
			if aws.ToString(charge.RecurringChargeFrequency) == "Hourly" {
				details.RecurringCost = charge.RecurringChargeAmount
			}
		}
	}

	return details, nil
}

// GetValidResourceTypes returns valid MemoryDB node types by querying the API
func (c *Client) GetValidResourceTypes(ctx context.Context) ([]string, error) {
	nodeTypesMap := make(map[string]bool)
	var nextToken *string

	for {
		input := &memorydb.DescribeReservedNodesOfferingsInput{
			NextToken:  nextToken,
			MaxResults: aws.Int32(100),
		}

		result, err := c.client.DescribeReservedNodesOfferings(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("failed to describe MemoryDB offerings: %w", err)
		}

		for _, offering := range result.ReservedNodesOfferings {
			if offering.NodeType != nil {
				nodeTypesMap[*offering.NodeType] = true
			}
		}

		if result.NextToken == nil || *result.NextToken == "" {
			break
		}
		nextToken = result.NextToken
	}

	nodeTypes := make([]string, 0, len(nodeTypesMap))
	for nodeType := range nodeTypesMap {
		nodeTypes = append(nodeTypes, nodeType)
	}

	sort.Strings(nodeTypes)
	return nodeTypes, nil
}

// createPurchaseTags creates standard tags for the purchase. The tag shape
// is shared across RDS/ElastiCache/MemoryDB via tagging.PurchasePairs; the
// only per-service customizations are the Purpose string and the AWS
// convention for the instance-type tag key.
func (c *Client) createPurchaseTags(rec common.Recommendation, source string) []types.Tag {
	pairs := tagging.PurchasePairs(rec, "Reserved Node Purchase", "NodeType", source)
	out := make([]types.Tag, len(pairs))
	for i, p := range pairs {
		out[i] = types.Tag{Key: aws.String(p.Key), Value: aws.String(p.Value)}
	}
	return out
}

// getTermMonthsFromDuration converts duration in seconds to months
func getTermMonthsFromDuration(duration int32) int {
	offeringMonths := duration / 2592000
	if offeringMonths >= 30 {
		return 36
	}
	return 12
}

// getTermMonthsFromString converts term string to months
func (c *Client) getTermMonthsFromString(term string) int {
	switch term {
	case "3yr", "3", "36":
		return 36
	default:
		return 12
	}
}
