// Package opensearch provides AWS OpenSearch Reserved Instances client
package opensearch

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/opensearch"
	"github.com/aws/aws-sdk-go-v2/service/opensearch/types"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

// OpenSearchAPI defines the interface for OpenSearch operations (enables mocking)
type OpenSearchAPI interface {
	PurchaseReservedInstanceOffering(ctx context.Context, params *opensearch.PurchaseReservedInstanceOfferingInput, optFns ...func(*opensearch.Options)) (*opensearch.PurchaseReservedInstanceOfferingOutput, error)
	DescribeReservedInstanceOfferings(ctx context.Context, params *opensearch.DescribeReservedInstanceOfferingsInput, optFns ...func(*opensearch.Options)) (*opensearch.DescribeReservedInstanceOfferingsOutput, error)
	DescribeReservedInstances(ctx context.Context, params *opensearch.DescribeReservedInstancesInput, optFns ...func(*opensearch.Options)) (*opensearch.DescribeReservedInstancesOutput, error)
}

// Client handles AWS OpenSearch Reserved Instances
type Client struct {
	client OpenSearchAPI
	region string
}

// NewClient creates a new OpenSearch client
func NewClient(cfg aws.Config) *Client {
	return &Client{
		client: opensearch.NewFromConfig(cfg),
		region: cfg.Region,
	}
}

// SetOpenSearchAPI sets a custom OpenSearch API client (for testing)
func (c *Client) SetOpenSearchAPI(api OpenSearchAPI) {
	c.client = api
}

// GetServiceType returns the service type
func (c *Client) GetServiceType() common.ServiceType {
	return common.ServiceSearch
}

// GetRegion returns the region
func (c *Client) GetRegion() string {
	return c.region
}

// GetRecommendations returns empty as OpenSearch uses centralized Cost Explorer recommendations
func (c *Client) GetRecommendations(ctx context.Context, params common.RecommendationParams) ([]common.Recommendation, error) {
	return []common.Recommendation{}, nil
}

// GetExistingCommitments retrieves existing OpenSearch Reserved Instances
func (c *Client) GetExistingCommitments(ctx context.Context) ([]common.Commitment, error) {
	commitments := make([]common.Commitment, 0)
	var nextToken *string

	for {
		input := &opensearch.DescribeReservedInstancesInput{
			NextToken:  nextToken,
			MaxResults: 100,
		}

		response, err := c.client.DescribeReservedInstances(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("failed to describe reserved instances: %w", err)
		}

		for _, ri := range response.ReservedInstances {
			state := aws.ToString(ri.State)
			if state != "active" && state != "payment-pending" {
				continue
			}

			termMonths := getTermMonthsFromDuration(ri.Duration)

			commitment := common.Commitment{
				Provider:       common.ProviderAWS,
				CommitmentID:   aws.ToString(ri.ReservedInstanceId),
				CommitmentType: common.CommitmentReservedInstance,
				Service:        common.ServiceSearch,
				Region:         c.region,
				ResourceType:   string(ri.InstanceType),
				Count:          int(ri.InstanceCount),
				State:          state,
				StartDate:      aws.ToTime(ri.StartTime),
				EndDate:        aws.ToTime(ri.StartTime).AddDate(0, termMonths, 0),
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

// PurchaseCommitment purchases an OpenSearch Reserved Instance
func (c *Client) PurchaseCommitment(ctx context.Context, rec common.Recommendation) (common.PurchaseResult, error) {
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

	reservationID := common.SanitizeReservationID(fmt.Sprintf("opensearch-%s-%d", rec.ResourceType, time.Now().Unix()), "opensearch-reserved-")

	input := &opensearch.PurchaseReservedInstanceOfferingInput{
		ReservedInstanceOfferingId: aws.String(offeringID),
		ReservationName:            aws.String(reservationID),
		InstanceCount:              aws.Int32(int32(rec.Count)),
	}

	response, err := c.client.PurchaseReservedInstanceOffering(ctx, input)
	if err != nil {
		result.Error = fmt.Errorf("failed to purchase OpenSearch RI: %w", err)
		return result, result.Error
	}

	if response.ReservedInstanceId != nil {
		result.Success = true
		result.CommitmentID = aws.ToString(response.ReservedInstanceId)
	} else {
		result.Error = fmt.Errorf("purchase response was empty")
		return result, result.Error
	}

	return result, nil
}

// findOfferingID finds the appropriate Reserved Instance offering ID
func (c *Client) findOfferingID(ctx context.Context, rec common.Recommendation) (string, error) {
	var nextToken *string
	for {
		input := &opensearch.DescribeReservedInstanceOfferingsInput{
			MaxResults: 100,
			NextToken:  nextToken,
		}

		result, err := c.client.DescribeReservedInstanceOfferings(ctx, input)
		if err != nil {
			return "", fmt.Errorf("failed to describe offerings: %w", err)
		}

		for _, offering := range result.ReservedInstanceOfferings {
			if string(offering.InstanceType) == rec.ResourceType {
				if c.matchesPaymentOption(offering.PaymentOption, rec.PaymentOption) &&
					c.matchesDuration(offering.Duration, rec.Term) {
					return aws.ToString(offering.ReservedInstanceOfferingId), nil
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

// matchesPaymentOption checks if the offering payment option matches
func (c *Client) matchesPaymentOption(offeringOption types.ReservedInstancePaymentOption, required string) bool {
	switch required {
	case "all-upfront":
		return offeringOption == types.ReservedInstancePaymentOptionAllUpfront
	case "partial-upfront":
		return offeringOption == types.ReservedInstancePaymentOptionPartialUpfront
	case "no-upfront":
		return offeringOption == types.ReservedInstancePaymentOptionNoUpfront
	default:
		return false
	}
}

// matchesDuration checks if the offering duration matches
func (c *Client) matchesDuration(offeringDuration int32, term string) bool {
	offeringMonths := offeringDuration / 2592000 // 30 days in seconds
	requiredMonths := 12
	if term == "3yr" || term == "3" {
		requiredMonths = 36
	}
	return int(offeringMonths) >= requiredMonths-1 && int(offeringMonths) <= requiredMonths+1
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

	input := &opensearch.DescribeReservedInstanceOfferingsInput{
		ReservedInstanceOfferingId: aws.String(offeringID),
		MaxResults:                 1,
	}

	result, err := c.client.DescribeReservedInstanceOfferings(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to get offering details: %w", err)
	}

	if len(result.ReservedInstanceOfferings) == 0 {
		return nil, fmt.Errorf("offering not found: %s", offeringID)
	}

	offering := result.ReservedInstanceOfferings[0]

	details := &common.OfferingDetails{
		OfferingID:    aws.ToString(offering.ReservedInstanceOfferingId),
		ResourceType:  string(offering.InstanceType),
		Term:          fmt.Sprintf("%d", offering.Duration),
		PaymentOption: string(offering.PaymentOption),
		UpfrontCost:   aws.ToFloat64(offering.FixedPrice),
		RecurringCost: aws.ToFloat64(offering.UsagePrice),
		Currency:      aws.ToString(offering.CurrencyCode),
	}

	return details, nil
}

// GetValidResourceTypes returns valid OpenSearch instance types (static list)
func (c *Client) GetValidResourceTypes(ctx context.Context) ([]string, error) {
	return []string{
		"t2.small.search",
		"t2.medium.search",
		"t3.small.search",
		"t3.medium.search",
		"m5.large.search",
		"m5.xlarge.search",
		"m5.2xlarge.search",
		"m5.4xlarge.search",
		"m5.12xlarge.search",
		"m6g.large.search",
		"m6g.xlarge.search",
		"m6g.2xlarge.search",
		"m6g.4xlarge.search",
		"m6g.8xlarge.search",
		"m6g.12xlarge.search",
		"c5.large.search",
		"c5.xlarge.search",
		"c5.2xlarge.search",
		"c5.4xlarge.search",
		"c5.9xlarge.search",
		"c5.18xlarge.search",
		"c6g.large.search",
		"c6g.xlarge.search",
		"c6g.2xlarge.search",
		"c6g.4xlarge.search",
		"c6g.8xlarge.search",
		"c6g.12xlarge.search",
		"r5.large.search",
		"r5.xlarge.search",
		"r5.2xlarge.search",
		"r5.4xlarge.search",
		"r5.12xlarge.search",
		"r6g.large.search",
		"r6g.xlarge.search",
		"r6g.2xlarge.search",
		"r6g.4xlarge.search",
		"r6g.8xlarge.search",
		"r6g.12xlarge.search",
		"r6gd.large.search",
		"r6gd.xlarge.search",
		"r6gd.2xlarge.search",
		"r6gd.4xlarge.search",
		"r6gd.8xlarge.search",
		"r6gd.12xlarge.search",
		"i3.large.search",
		"i3.xlarge.search",
		"i3.2xlarge.search",
		"i3.4xlarge.search",
		"i3.8xlarge.search",
		"i3.16xlarge.search",
	}, nil
}

// getTermMonthsFromDuration converts duration in seconds to months
func getTermMonthsFromDuration(duration int32) int {
	offeringMonths := duration / 2592000
	if offeringMonths >= 30 {
		return 36
	}
	return 12
}
