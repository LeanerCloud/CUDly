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
	"github.com/LeanerCloud/CUDly/providers/aws/internal/purchasecfg"
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

// NewClient creates a new MemoryDB client with purchase-path retry/timeout
// settings. See purchasecfg for rationale.
func NewClient(cfg aws.Config) *Client {
	pcfg := purchasecfg.NewConfig(cfg)
	return &Client{
		client: memorydb.NewFromConfig(pcfg),
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

	offeringID, err := c.findOfferingID(ctx, rec, opts.ExecutionID)
	if err != nil {
		result.Error = fmt.Errorf("failed to find offering: %w", err)
		return result, result.Error
	}

	// When an idempotency token is supplied (issue #641) the reservation ID is
	// derived deterministically from it, so a re-drive sends the identical
	// ReservationId and MemoryDB rejects the duplicate server-side
	// (ReservedNodeAlreadyExistsFault). On the no-token CLI path (issue #687)
	// compose a rich, self-describing identifier matching the Azure
	// DisplayName format so operators can identify the reservation in the
	// AWS console without cross-referencing CUDly's purchase audit log.
	reservationID := common.IdempotentReservationID("memorydb-id-", opts.IdempotencyToken)
	if reservationID == "" {
		reservationID = common.BuildReservationName(common.ReservationNameFields{
			Service:      "memdb",
			Region:       rec.Region,
			ResourceType: rec.ResourceType,
			Count:        rec.Count,
			Term:         rec.Term,
			Payment:      rec.PaymentOption,
			Now:          time.Now(),
		}, "memorydb-reserved-")
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
		log.Printf("MemoryDB reservation for idempotency token %s already exists (%s); skipping purchase (issue #641 re-drive)", common.MaskToken(token), existingID)
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

// maxOfferingPages is the maximum number of DescribeReservedNodesOfferings
// pages to walk before giving up. At MaxResults=100 per page this caps the
// search at 500 offerings. Exceeding the cap returns a diagnostic error instead
// of timing out the Lambda budget (issue #688).
const maxOfferingPages = 5

// convertMemoryDBPaymentOption maps a rec payment-option slug to the AWS
// DescribeReservedNodesOfferings OfferingType string value.
func convertMemoryDBPaymentOption(option string) (string, error) {
	switch option {
	case "all-upfront":
		return "All Upfront", nil
	case "partial-upfront":
		return "Partial Upfront", nil
	case "no-upfront":
		return "No Upfront", nil
	default:
		return "", fmt.Errorf("unsupported MemoryDB payment option: %s", option)
	}
}

// findOfferingID finds the appropriate Reserved Node offering ID.
// All supported narrow filters (NodeType, OfferingType, Duration) are set
// directly on the request to minimize the result set (issue #688).
//
// execID is the purchase execution UUID for log correlation; pass "" when
// calling outside of a purchase flow (ValidateOffering, GetOfferingDetails).
func (c *Client) findOfferingID(ctx context.Context, rec common.Recommendation, execID string) (string, error) {
	wantOfferingType, err := convertMemoryDBPaymentOption(rec.PaymentOption)
	if err != nil {
		return "", err
	}

	duration := c.getDurationStringForAPI(rec.Term)
	tag := execID
	if tag == "" {
		tag = "no-exec"
	}
	t0 := time.Now()
	log.Printf("purchase[%s]: MemoryDB findOfferingID starting (nodeType=%s term=%s payment=%s)",
		tag, rec.ResourceType, rec.Term, rec.PaymentOption)

	var nextToken *string
	page := 0
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}

		page++
		if page > maxOfferingPages {
			return "", fmt.Errorf("pagination cap reached after %d pages for MemoryDB %s %s (issue #688)",
				maxOfferingPages, rec.ResourceType, rec.PaymentOption)
		}

		input := &memorydb.DescribeReservedNodesOfferingsInput{
			NodeType:     aws.String(rec.ResourceType),
			OfferingType: aws.String(wantOfferingType),
			Duration:     aws.String(duration),
			MaxResults:   aws.Int32(100),
			NextToken:    nextToken,
		}

		pageStart := time.Now()
		result, err := c.client.DescribeReservedNodesOfferings(ctx, input)
		if err != nil {
			log.Printf("purchase[%s]: MemoryDB findOfferingID page %d failed after %s (total %s): %v",
				tag, page, time.Since(pageStart), time.Since(t0), err)
			return "", fmt.Errorf("failed to describe offerings: %w", err)
		}
		log.Printf("purchase[%s]: MemoryDB findOfferingID page %d: %d offerings in %s",
			tag, page, len(result.ReservedNodesOfferings), time.Since(pageStart))

		if id, err := scanMemoryDBOfferingPage(result.ReservedNodesOfferings, wantOfferingType, rec, tag, page, t0); err != nil {
			return "", err
		} else if id != "" {
			return id, nil
		}

		if isLastMemoryDBPage(result.NextToken) {
			break
		}
		nextToken = result.NextToken
	}

	log.Printf("purchase[%s]: MemoryDB findOfferingID exhausted %d page(s) in %s -- no match",
		tag, page, time.Since(t0))
	return "", fmt.Errorf("no offerings found for MemoryDB %s %s after %d page(s) (issue #688)",
		rec.ResourceType, rec.PaymentOption, page)
}

// scanMemoryDBOfferingPage inspects a single page of DescribeReservedNodesOfferings
// results and returns the first matching offering ID.
// It returns ("", nil) when the page is empty or no offering matched; it returns
// ("", err) on an API filter mismatch; it returns (id, nil) on a successful match.
//
// Pulled out of findOfferingID to keep that function under the cyclomatic limit.
func scanMemoryDBOfferingPage(offerings []types.ReservedNodesOffering, wantOfferingType string, rec common.Recommendation, tag string, page int, t0 time.Time) (string, error) {
	for _, o := range offerings {
		got := aws.ToString(o.OfferingType)
		if got != wantOfferingType {
			return "", fmt.Errorf("MemoryDB offering %s has payment option %q, want %q (rec: %s %s) -- API filter mismatch",
				aws.ToString(o.ReservedNodesOfferingId), got, wantOfferingType,
				rec.ResourceType, rec.PaymentOption)
		}
		log.Printf("purchase[%s]: MemoryDB findOfferingID found match on page %d after %s total",
			tag, page, time.Since(t0))
		return aws.ToString(o.ReservedNodesOfferingId), nil
	}
	return "", nil
}

// isLastMemoryDBPage reports whether a NextToken indicates the terminal page.
// The AWS SDK may return either nil or a pointer to an empty string for the
// last page; both must end pagination so the loop does not issue a redundant
// request (and risk a false page-cap error on borderline page counts).
//
// Pulled out of findOfferingID to keep that function under the cyclomatic limit.
func isLastMemoryDBPage(nextToken *string) bool {
	return nextToken == nil || aws.ToString(nextToken) == ""
}

// getDurationStringForAPI converts the term string to a duration value accepted
// by DescribeReservedNodesOfferings (seconds as a string or "1yr"/"3yr").
// The MemoryDB API accepts both numeric-seconds strings and year strings.
func (c *Client) getDurationStringForAPI(term string) string {
	if term == "3yr" || term == "3" || term == "36" {
		return "3yr"
	}
	return "1yr"
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
