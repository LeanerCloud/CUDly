// Package redshift provides AWS Redshift Reserved Nodes client
package redshift

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/redshift"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

// RedshiftAPI defines the interface for Redshift operations (enables mocking)
type RedshiftAPI interface {
	PurchaseReservedNodeOffering(ctx context.Context, params *redshift.PurchaseReservedNodeOfferingInput, optFns ...func(*redshift.Options)) (*redshift.PurchaseReservedNodeOfferingOutput, error)
	DescribeReservedNodeOfferings(ctx context.Context, params *redshift.DescribeReservedNodeOfferingsInput, optFns ...func(*redshift.Options)) (*redshift.DescribeReservedNodeOfferingsOutput, error)
	DescribeReservedNodes(ctx context.Context, params *redshift.DescribeReservedNodesInput, optFns ...func(*redshift.Options)) (*redshift.DescribeReservedNodesOutput, error)
}

// Client handles AWS Redshift Reserved Nodes
type Client struct {
	client RedshiftAPI
	region string
}

// NewClient creates a new Redshift client
func NewClient(cfg aws.Config) *Client {
	return &Client{
		client: redshift.NewFromConfig(cfg),
		region: cfg.Region,
	}
}

// SetRedshiftAPI sets a custom Redshift API client (for testing)
func (c *Client) SetRedshiftAPI(api RedshiftAPI) {
	c.client = api
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

// PurchaseCommitment purchases a Redshift Reserved Node
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

	return result, nil
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

// matchesOfferingType checks if the offering type matches
func (c *Client) matchesOfferingType(offeringType string, paymentOption string) bool {
	// Redshift uses "Regular" and "Upgradable" offering types
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

// GetValidResourceTypes returns valid Redshift node types (static list)
func (c *Client) GetValidResourceTypes(ctx context.Context) ([]string, error) {
	return []string{
		"dc2.large",
		"dc2.8xlarge",
		"ra3.xlplus",
		"ra3.4xlarge",
		"ra3.16xlarge",
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
