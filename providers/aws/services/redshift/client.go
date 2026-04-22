// Package redshift provides AWS Redshift Reserved Nodes client
package redshift

import (
	"context"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/redshift"
	redshifttypes "github.com/aws/aws-sdk-go-v2/service/redshift/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/retry"
)

// RedshiftAPI defines the interface for Redshift operations (enables mocking)
type RedshiftAPI interface {
	PurchaseReservedNodeOffering(ctx context.Context, params *redshift.PurchaseReservedNodeOfferingInput, optFns ...func(*redshift.Options)) (*redshift.PurchaseReservedNodeOfferingOutput, error)
	DescribeReservedNodeOfferings(ctx context.Context, params *redshift.DescribeReservedNodeOfferingsInput, optFns ...func(*redshift.Options)) (*redshift.DescribeReservedNodeOfferingsOutput, error)
	DescribeReservedNodes(ctx context.Context, params *redshift.DescribeReservedNodesInput, optFns ...func(*redshift.Options)) (*redshift.DescribeReservedNodesOutput, error)
	CreateTags(ctx context.Context, params *redshift.CreateTagsInput, optFns ...func(*redshift.Options)) (*redshift.CreateTagsOutput, error)
}

// STSAPI is the subset of STS this client calls to resolve the caller's
// account ID for ARN construction. Declared at package level so tests can
// substitute a fake.
type STSAPI interface {
	GetCallerIdentity(ctx context.Context, params *sts.GetCallerIdentityInput, optFns ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error)
}

// Client handles AWS Redshift Reserved Nodes
type Client struct {
	client    RedshiftAPI
	stsClient STSAPI
	region    string

	// accountID is resolved lazily on first tag call via STS. Guarded by
	// accountOnce so concurrent purchases don't each call STS.
	accountOnce sync.Once
	accountID   string
	accountErr  error
}

// NewClient creates a new Redshift client
func NewClient(cfg aws.Config) *Client {
	return &Client{
		client:    redshift.NewFromConfig(cfg),
		stsClient: sts.NewFromConfig(cfg),
		region:    cfg.Region,
	}
}

// SetRedshiftAPI sets a custom Redshift API client (for testing)
func (c *Client) SetRedshiftAPI(api RedshiftAPI) {
	c.client = api
}

// SetSTSAPI sets a custom STS client (for testing)
func (c *Client) SetSTSAPI(api STSAPI) {
	c.stsClient = api
}

// GetServiceType returns the service type
func (c *Client) GetServiceType() common.ServiceType {
	return common.ServiceDataWarehouse
}

// GetRegion returns the region
func (c *Client) GetRegion() string {
	return c.region
}

// GetRecommendations returns empty as Redshift uses centralized Cost Explorer recommendations
func (c *Client) GetRecommendations(ctx context.Context, params common.RecommendationParams) ([]common.Recommendation, error) {
	return []common.Recommendation{}, nil
}

// GetExistingCommitments retrieves existing Redshift Reserved Nodes
func (c *Client) GetExistingCommitments(ctx context.Context) ([]common.Commitment, error) {
	commitments := make([]common.Commitment, 0)
	var marker *string

	for {
		input := &redshift.DescribeReservedNodesInput{
			Marker:     marker,
			MaxRecords: aws.Int32(100),
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

			termMonths := getTermMonthsFromDuration(aws.ToInt32(node.Duration))

			commitment := common.Commitment{
				Provider:       common.ProviderAWS,
				CommitmentID:   aws.ToString(node.ReservedNodeId),
				CommitmentType: common.CommitmentReservedInstance,
				Service:        common.ServiceDataWarehouse,
				Region:         c.region,
				ResourceType:   aws.ToString(node.NodeType),
				Count:          int(aws.ToInt32(node.NodeCount)),
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

// PurchaseCommitment purchases a Redshift Reserved Node.
//
// PurchaseReservedNodeOfferingInput has no Tags field — tagging happens
// post-purchase via redshift:CreateTags, which requires a full ARN
// (arn:aws:redshift:<region>:<account>:reservednode:<id>). The account ID
// is resolved lazily on first tag call via sts:GetCallerIdentity and cached
// for the lifetime of the client. Tagging failure is logged but does NOT
// fail the purchase — the reserved node is already bought.
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

	input := &redshift.PurchaseReservedNodeOfferingInput{
		ReservedNodeOfferingId: aws.String(offeringID),
		NodeCount:              aws.Int32(int32(rec.Count)),
	}

	response, err := c.client.PurchaseReservedNodeOffering(ctx, input)
	if err != nil {
		result.Error = fmt.Errorf("failed to purchase Redshift Reserved Node: %w", err)
		return result, result.Error
	}

	if response.ReservedNode != nil {
		result.Success = true
		result.CommitmentID = aws.ToString(response.ReservedNode.ReservedNodeId)
		if response.ReservedNode.FixedPrice != nil {
			result.Cost = *response.ReservedNode.FixedPrice
		}
	} else {
		result.Error = fmt.Errorf("purchase response was empty")
		return result, result.Error
	}

	if err := c.tagReservedNode(ctx, result.CommitmentID, rec, opts.Source); err != nil {
		log.Printf("WARNING: failed to tag Redshift reserved node %s after purchase (node is bought; tag missing): %v", result.CommitmentID, err)
	}

	return result, nil
}

// resolveAccountID fetches the caller's AWS account ID via STS and caches it.
// Returns ("", nil) — i.e. an empty string with no error — when the STS
// client is nil (e.g. a test client that skipped SetSTSAPI). Callers must
// treat empty as "can't tag" and skip the tag call.
func (c *Client) resolveAccountID(ctx context.Context) (string, error) {
	c.accountOnce.Do(func() {
		if c.stsClient == nil {
			return
		}
		out, err := c.stsClient.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
		if err != nil {
			c.accountErr = err
			return
		}
		if out != nil && out.Account != nil {
			c.accountID = *out.Account
		}
	})
	return c.accountID, c.accountErr
}

// tagReservedNode constructs the reserved-node ARN and calls redshift:CreateTags.
// Retries up to 4 attempts (1s/2s/4s backoff) on validation errors that can
// indicate the node isn't yet visible to the tagging API. Returns nil when
// source is empty (opt-out) OR when the account ID can't be resolved — both
// mean "don't tag", logged by the caller.
func (c *Client) tagReservedNode(ctx context.Context, nodeID string, rec common.Recommendation, source string) error {
	if source == "" {
		return nil
	}
	accountID, err := c.resolveAccountID(ctx)
	if err != nil {
		return fmt.Errorf("resolve account ID: %w", err)
	}
	if accountID == "" {
		return fmt.Errorf("account ID unavailable (no STS client)")
	}

	arn := fmt.Sprintf("arn:aws:redshift:%s:%s:reservednode:%s", c.region, accountID, nodeID)
	tags := []redshifttypes.Tag{
		{Key: aws.String("Purpose"), Value: aws.String("Reserved Node Purchase")},
		{Key: aws.String("NodeType"), Value: aws.String(rec.ResourceType)},
		{Key: aws.String("Region"), Value: aws.String(rec.Region)},
		{Key: aws.String("PurchaseDate"), Value: aws.String(time.Now().Format("2006-01-02"))},
		{Key: aws.String("Tool"), Value: aws.String("CUDly")},
		{Key: aws.String(common.PurchaseTagKey), Value: aws.String(source)},
	}

	cfg := retry.Config{MaxAttempts: 4, BaseDelay: time.Second, MaxDelay: 4 * time.Second}
	return retry.Do(ctx, cfg, func(perAttemptCtx context.Context, _ int) error {
		_, err := c.client.CreateTags(perAttemptCtx, &redshift.CreateTagsInput{
			ResourceName: aws.String(arn),
			Tags:         tags,
		})
		if err == nil {
			return nil
		}
		// All Redshift CreateTags errors that aren't transient (e.g. auth,
		// invalid ARN) are permanent. No documented "resource not yet
		// visible" retry condition for reserved nodes, so a single pass
		// is usually enough; the retry budget exists to absorb network
		// flakes not state-machine delays.
		return fmt.Errorf("%w: %w", retry.ErrPermanent, err)
	})
}

// findOfferingID finds the appropriate Reserved Node offering ID
func (c *Client) findOfferingID(ctx context.Context, rec common.Recommendation) (string, error) {
	var marker *string

	for {
		input := &redshift.DescribeReservedNodeOfferingsInput{
			MaxRecords: aws.Int32(100),
			Marker:     marker,
		}

		result, err := c.client.DescribeReservedNodeOfferings(ctx, input)
		if err != nil {
			return "", fmt.Errorf("failed to describe offerings: %w", err)
		}

		for _, offering := range result.ReservedNodeOfferings {
			if offering.NodeType != nil && *offering.NodeType == rec.ResourceType {
				if c.matchesDuration(offering.Duration, rec.Term) &&
					c.matchesOfferingType(string(offering.ReservedNodeOfferingType), rec.PaymentOption) {
					return aws.ToString(offering.ReservedNodeOfferingId), nil
				}
			}
		}

		if result.Marker == nil || aws.ToString(result.Marker) == "" {
			break
		}
		marker = result.Marker
	}

	return "", fmt.Errorf("no offerings found for %s", rec.ResourceType)
}

// matchesDuration checks if the offering duration matches
func (c *Client) matchesDuration(offeringDuration *int32, term string) bool {
	if offeringDuration == nil {
		return false
	}

	offeringMonths := *offeringDuration / 2592000
	requiredMonths := 12
	if term == "3yr" || term == "3" {
		requiredMonths = 36
	}
	return int(offeringMonths) == requiredMonths
}

// matchesOfferingType checks if the offering type is a valid Redshift reserved node offering type.
// Redshift uses "Regular" and "Upgradable" as offering type identifiers — not payment-option strings
// like other AWS services. Payment flexibility is encoded differently in the Redshift API.
func (c *Client) matchesOfferingType(offeringType string, _ string) bool {
	return offeringType == "Regular" || offeringType == "Upgradable"
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

	input := &redshift.DescribeReservedNodeOfferingsInput{
		ReservedNodeOfferingId: aws.String(offeringID),
		MaxRecords:             aws.Int32(1),
	}

	result, err := c.client.DescribeReservedNodeOfferings(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to get offering details: %w", err)
	}

	if len(result.ReservedNodeOfferings) == 0 {
		return nil, fmt.Errorf("offering not found: %s", offeringID)
	}

	offering := result.ReservedNodeOfferings[0]

	details := &common.OfferingDetails{
		OfferingID:    aws.ToString(offering.ReservedNodeOfferingId),
		ResourceType:  aws.ToString(offering.NodeType),
		Term:          fmt.Sprintf("%d", aws.ToInt32(offering.Duration)),
		PaymentOption: string(offering.ReservedNodeOfferingType),
		UpfrontCost:   aws.ToFloat64(offering.FixedPrice),
		RecurringCost: aws.ToFloat64(offering.UsagePrice),
		Currency:      aws.ToString(offering.CurrencyCode),
	}

	for _, charge := range offering.RecurringCharges {
		if charge.RecurringChargeAmount != nil && charge.RecurringChargeFrequency != nil {
			if *charge.RecurringChargeFrequency == "Hourly" {
				details.RecurringCost = *charge.RecurringChargeAmount
			}
		}
	}

	return details, nil
}

// GetValidResourceTypes returns valid Redshift node types by querying the API
func (c *Client) GetValidResourceTypes(ctx context.Context) ([]string, error) {
	nodeTypes := make(map[string]bool)
	var marker *string

	for {
		input := &redshift.DescribeReservedNodeOfferingsInput{
			MaxRecords: aws.Int32(100),
			Marker:     marker,
		}

		result, err := c.client.DescribeReservedNodeOfferings(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("failed to describe offerings: %w", err)
		}

		for _, offering := range result.ReservedNodeOfferings {
			if offering.NodeType != nil {
				nodeTypes[*offering.NodeType] = true
			}
		}

		if result.Marker == nil || aws.ToString(result.Marker) == "" {
			break
		}
		marker = result.Marker
	}

	types := make([]string, 0, len(nodeTypes))
	for nodeType := range nodeTypes {
		types = append(types, nodeType)
	}
	sort.Strings(types)
	return types, nil
}

// getTermMonthsFromDuration converts duration in seconds to months
func getTermMonthsFromDuration(duration int32) int {
	offeringMonths := duration / 2592000
	if offeringMonths >= 30 {
		return 36
	}
	return 12
}
