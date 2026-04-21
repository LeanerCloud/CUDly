package ec2

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// TestFindConvertibleOfferings_ReturnsRankedByEffectiveCost verifies that
// FindConvertibleOfferings batches its call, picks the cheapest offering
// per instance type, and sorts ascending by EffectiveMonthlyCost.
func TestFindConvertibleOfferings_ReturnsRankedByEffectiveCost(t *testing.T) {
	t.Parallel()
	mockEC2 := new(MockEC2Client)
	client := &Client{client: mockEC2, region: "us-east-1"}

	// AWS returns 4 offerings: 2 for m5.large at different payment
	// options, 1 for m6i.large, 1 for m7g.large. The method must
	// collapse the two m5.large rows to the cheaper of the two and
	// sort the result ascending by monthly cost.
	//
	// Costs chosen so sort order is m7g (cheapest) → m6i → m5 cheap
	// variant → dropped m5 expensive variant. With 1-year term
	// (31536000s = 8760 hours) and 730 hrs/month:
	//   m5.large cheap: FixedPrice=0,   Usage=0.04  → 0.04 * 730 = 29.20
	//   m5.large dear:  FixedPrice=876, Usage=0    → 876/8760 * 730 = 73.00
	//   m6i.large:      FixedPrice=0,   Usage=0.03  → 21.90
	//   m7g.large:      FixedPrice=0,   Usage=0.02  → 14.60
	mockEC2.On("DescribeReservedInstancesOfferings", mock.Anything,
		mock.MatchedBy(func(in *ec2.DescribeReservedInstancesOfferingsInput) bool {
			// Assert the filter carries all three instance types in one call.
			for _, f := range in.Filters {
				if aws.ToString(f.Name) == "instance-type" {
					if len(f.Values) != 3 {
						return false
					}
				}
			}
			return true
		})).Return(&ec2.DescribeReservedInstancesOfferingsOutput{
		ReservedInstancesOfferings: []types.ReservedInstancesOffering{
			{
				ReservedInstancesOfferingId: aws.String("off-m5-cheap"),
				InstanceType:                "m5.large",
				Duration:                    aws.Int64(OneYearSeconds),
				FixedPrice:                  aws.Float32(0),
				UsagePrice:                  aws.Float32(0.04),
			},
			{
				ReservedInstancesOfferingId: aws.String("off-m5-dear"),
				InstanceType:                "m5.large",
				Duration:                    aws.Int64(OneYearSeconds),
				FixedPrice:                  aws.Float32(876),
				UsagePrice:                  aws.Float32(0),
			},
			{
				ReservedInstancesOfferingId: aws.String("off-m6i"),
				InstanceType:                "m6i.large",
				Duration:                    aws.Int64(OneYearSeconds),
				FixedPrice:                  aws.Float32(0),
				UsagePrice:                  aws.Float32(0.03),
			},
			{
				ReservedInstancesOfferingId: aws.String("off-m7g"),
				InstanceType:                "m7g.large",
				Duration:                    aws.Int64(OneYearSeconds),
				FixedPrice:                  aws.Float32(0),
				UsagePrice:                  aws.Float32(0.02),
			},
		},
	}, nil)

	got, err := client.FindConvertibleOfferings(context.Background(),
		[]string{"m5.large", "m6i.large", "m7g.large"})
	assert.NoError(t, err)
	if len(got) != 3 {
		t.Fatalf("expected 3 offerings (one per instance type, expensive m5 dropped); got %d", len(got))
	}

	// Ascending by monthly cost.
	assert.Equal(t, "m7g.large", got[0].InstanceType)
	assert.Equal(t, "off-m7g", got[0].OfferingID)
	assert.InDelta(t, 14.60, got[0].EffectiveMonthlyCost, 0.01)

	assert.Equal(t, "m6i.large", got[1].InstanceType)
	assert.InDelta(t, 21.90, got[1].EffectiveMonthlyCost, 0.01)

	assert.Equal(t, "m5.large", got[2].InstanceType)
	assert.Equal(t, "off-m5-cheap", got[2].OfferingID, "cheaper m5.large variant must win over the expensive one")
	assert.InDelta(t, 29.20, got[2].EffectiveMonthlyCost, 0.01)

	// Single batched API call, not one per instance type.
	mockEC2.AssertNumberOfCalls(t, "DescribeReservedInstancesOfferings", 1)
}

// TestFindConvertibleOfferings_EmptyInputMakesNoCall guards against
// fan-out when the caller passes an empty list (shouldn't happen in
// practice but the helper is library-public).
func TestFindConvertibleOfferings_EmptyInputMakesNoCall(t *testing.T) {
	t.Parallel()
	mockEC2 := new(MockEC2Client)
	client := &Client{client: mockEC2}
	got, err := client.FindConvertibleOfferings(context.Background(), nil)
	assert.NoError(t, err)
	assert.Nil(t, got)
	mockEC2.AssertNotCalled(t, "DescribeReservedInstancesOfferings", mock.Anything, mock.Anything)
}
