package recommendations

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockUtilizationCE extends the test mock with a configurable
// GetReservationUtilization response and captures the Filter from the most
// recent call so tests can assert the CE query was scoped correctly
// (PR #1361).
type mockUtilizationCE struct {
	mockCostExplorerAPI
	utilizationOutput *costexplorer.GetReservationUtilizationOutput
	lastFilter        *types.Expression
	calls             int
}

func (m *mockUtilizationCE) GetReservationUtilization(ctx context.Context, params *costexplorer.GetReservationUtilizationInput, optFns ...func(*costexplorer.Options)) (*costexplorer.GetReservationUtilizationOutput, error) {
	m.calls++
	if params != nil {
		m.lastFilter = params.Filter
	}
	return m.utilizationOutput, nil
}

// TestGetRIUtilization_ScopesToEC2AndRegion is the primary regression test
// for PR #1361: without a Filter, GetReservationUtilization blends
// utilization across every reserved-resource type (RDS, EC2, ...) and every
// region into one SUBSCRIPTION_ID-grouped number. It replicates the real
// failing scenario -- an RDS reservation with low utilization and an EC2
// convertible RI with high utilization returned side by side under the
// same account -- and asserts:
//  1. the outgoing CE request carries a Filter scoping SERVICE=EC2 and
//     REGION=region (pre-fix code sent no Filter at all).
//  2. the blended response is still parsed into per-RI entries so a
//     caller doing its own ID intersection (ladder layer_states.go) can
//     recover the EC2-only aggregate even though the mock doesn't
//     actually filter server-side.
func TestGetRIUtilization_ScopesToEC2AndRegion(t *testing.T) {
	mock := &mockUtilizationCE{
		utilizationOutput: &costexplorer.GetReservationUtilizationOutput{
			UtilizationsByTime: []types.UtilizationByTime{
				{
					Groups: []types.ReservationUtilizationGroup{
						{
							// RDS reservation: poorly utilized. Pre-fix, this
							// blends into the same aggregate an EC2-only
							// caller reads as "its own" utilization.
							Key: aws.String("rds-ri-1"),
							Utilization: &types.ReservationAggregates{
								PurchasedHours:   aws.String("100"),
								TotalActualHours: aws.String("10"),
							},
						},
						{
							// EC2 convertible RI: highly utilized.
							Key: aws.String("ec2-ri-1"),
							Utilization: &types.ReservationAggregates{
								PurchasedHours:   aws.String("100"),
								TotalActualHours: aws.String("95"),
							},
						},
					},
				},
			},
		},
	}
	client := NewClientWithAPI(mock, "us-east-1")

	got, err := client.GetRIUtilization(context.Background(), 30, "us-east-1")
	require.NoError(t, err)

	require.NotNil(t, mock.lastFilter, "GetRIUtilization must send a Filter (PR #1361: an absent Filter blends every reserved-resource type and region into one number)")
	require.NotNil(t, mock.lastFilter.And, "Filter must And together SERVICE and REGION dimensions")
	require.Len(t, mock.lastFilter.And, 2)
	assert.Equal(t, types.DimensionService, mock.lastFilter.And[0].Dimensions.Key)
	assert.Equal(t, []string{ec2ComputeService}, mock.lastFilter.And[0].Dimensions.Values)
	assert.Equal(t, types.DimensionRegion, mock.lastFilter.And[1].Dimensions.Key)
	assert.Equal(t, []string{"us-east-1"}, mock.lastFilter.And[1].Dimensions.Values)

	// Both entries still parse -- the ID intersection that scopes an EC2-only
	// caller down to its own convertible RIs happens one layer up in
	// providers/aws/ladder (utilsForConvertibleRIs), not inside this client.
	require.Len(t, got, 2)
	byID := make(map[string]RIUtilization, len(got))
	for _, u := range got {
		byID[u.ReservedInstanceID] = u
	}
	assert.InDelta(t, 10.0, byID["rds-ri-1"].UtilizationPercent, 0.001)
	assert.InDelta(t, 95.0, byID["ec2-ri-1"].UtilizationPercent, 0.001)
}

// TestGetRIUtilization_EmptyRegionOmitsRegionFilter confirms that an empty
// region argument (ambient-credentials callers that haven't resolved a
// specific region) still scopes to EC2 via SERVICE alone, rather than
// erroring or building a Filter with an empty REGION value.
func TestGetRIUtilization_EmptyRegionOmitsRegionFilter(t *testing.T) {
	mock := &mockUtilizationCE{utilizationOutput: &costexplorer.GetReservationUtilizationOutput{}}
	client := NewClientWithAPI(mock, "us-east-1")

	_, err := client.GetRIUtilization(context.Background(), 30, "")
	require.NoError(t, err)

	require.NotNil(t, mock.lastFilter)
	assert.Nil(t, mock.lastFilter.And, "no region supplied -- Filter must be SERVICE-only, not an And with an empty REGION value")
	require.NotNil(t, mock.lastFilter.Dimensions)
	assert.Equal(t, types.DimensionService, mock.lastFilter.Dimensions.Key)
	assert.Equal(t, []string{ec2ComputeService}, mock.lastFilter.Dimensions.Values)
}
