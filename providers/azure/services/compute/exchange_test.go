package compute_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/reservations/armreservations"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LeanerCloud/CUDly/providers/azure/services/compute"
)

// --- mock pager helpers ---

// staticExchangeablePager returns the provided pages in order and then
// reports More() == false. Used for all tests that don't need to exercise
// error paths.
type staticExchangeablePager struct {
	pages []*armreservations.ListResult
	idx   int
}

func (p *staticExchangeablePager) More() bool {
	return p.idx < len(p.pages)
}

func (p *staticExchangeablePager) NextPage(_ context.Context) (armreservations.ReservationClientListAllResponse, error) {
	if p.idx >= len(p.pages) {
		return armreservations.ReservationClientListAllResponse{}, errors.New("no more pages")
	}
	lr := p.pages[p.idx]
	p.idx++
	resp := armreservations.ReservationClientListAllResponse{}
	resp.ListResult = *lr
	return resp, nil
}

// errorExchangeablePager always returns the provided error from NextPage.
type errorExchangeablePager struct {
	err error
}

func (p *errorExchangeablePager) More() bool { return true }
func (p *errorExchangeablePager) NextPage(_ context.Context) (armreservations.ReservationClientListAllResponse, error) {
	return armreservations.ReservationClientListAllResponse{}, p.err
}

// --- builders ---

func provStateSuc() *armreservations.ProvisioningState {
	s := armreservations.ProvisioningStateSucceeded
	return &s
}

func provStatePend() *armreservations.ProvisioningState {
	s := armreservations.ProvisioningStatePendingBilling
	return &s
}

func ifOn() *armreservations.InstanceFlexibility {
	v := armreservations.InstanceFlexibilityOn
	return &v
}

func ifOff() *armreservations.InstanceFlexibility {
	v := armreservations.InstanceFlexibilityOff
	return &v
}

func resTypeVM() *armreservations.ReservedResourceType {
	v := armreservations.ReservedResourceTypeVirtualMachines
	return &v
}

func resTypeSQL() *armreservations.ReservedResourceType {
	v := armreservations.ReservedResourceTypeSQLDatabases
	return &v
}

func makeReservation(id, sku string, qty int32, provState *armreservations.ProvisioningState, resType *armreservations.ReservedResourceType, instanceFlex *armreservations.InstanceFlexibility) *armreservations.ReservationResponse {
	return &armreservations.ReservationResponse{
		ID:       to.Ptr(id),
		Location: to.Ptr("eastus"),
		SKU:      &armreservations.SKUName{Name: to.Ptr(sku)},
		Properties: &armreservations.Properties{
			ReservedResourceType: resType,
			ProvisioningState:    provState,
			InstanceFlexibility:  instanceFlex,
			Quantity:             to.Ptr(qty),
			DisplayName:          to.Ptr("test-reservation"),
			Term:                 to.Ptr(armreservations.ReservationTermP1Y),
			ExpiryDate:           to.Ptr(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)),
		},
	}
}

const vmID1 = "/providers/Microsoft.Capacity/reservationOrders/order-1111/reservations/res-aaaa"
const vmID2 = "/providers/Microsoft.Capacity/reservationOrders/order-2222/reservations/res-bbbb"
const sqlID = "/providers/Microsoft.Capacity/reservationOrders/order-3333/reservations/res-cccc"

func newClient() *compute.Client {
	// nil credential is fine -- tests inject a pager so no real API call
	// is ever made.
	return compute.NewClient(nil, "test-sub", "eastus")
}

// --- tests ---

func TestListExchangeableReservations_Empty(t *testing.T) {
	t.Parallel()
	c := newClient()
	c.SetExchangeablePager(&staticExchangeablePager{
		pages: []*armreservations.ListResult{
			{Value: []*armreservations.ReservationResponse{}},
		},
	})

	result, err := c.ListExchangeableReservations(context.Background())
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestListExchangeableReservations_NonVMFiltered(t *testing.T) {
	t.Parallel()
	c := newClient()
	c.SetExchangeablePager(&staticExchangeablePager{
		pages: []*armreservations.ListResult{
			{Value: []*armreservations.ReservationResponse{
				makeReservation(sqlID, "Standard_D2s_v3", 1, provStateSuc(), resTypeSQL(), ifOn()),
			}},
		},
	})

	result, err := c.ListExchangeableReservations(context.Background())
	require.NoError(t, err)
	assert.Empty(t, result, "non-VM reservation must be filtered out")
}

func TestListExchangeableReservations_FlexOffFiltered(t *testing.T) {
	t.Parallel()
	c := newClient()
	c.SetExchangeablePager(&staticExchangeablePager{
		pages: []*armreservations.ListResult{
			{Value: []*armreservations.ReservationResponse{
				makeReservation(vmID1, "Standard_D2s_v3", 1, provStateSuc(), resTypeVM(), ifOff()),
			}},
		},
	})

	result, err := c.ListExchangeableReservations(context.Background())
	require.NoError(t, err)
	assert.Empty(t, result, "InstanceFlexibility==Off must be filtered out")
}

func TestListExchangeableReservations_NilFlexFiltered(t *testing.T) {
	t.Parallel()
	c := newClient()
	c.SetExchangeablePager(&staticExchangeablePager{
		pages: []*armreservations.ListResult{
			{Value: []*armreservations.ReservationResponse{
				makeReservation(vmID1, "Standard_D2s_v3", 1, provStateSuc(), resTypeVM(), nil),
			}},
		},
	})

	result, err := c.ListExchangeableReservations(context.Background())
	require.NoError(t, err)
	assert.Empty(t, result, "nil InstanceFlexibility must be filtered out")
}

func TestListExchangeableReservations_ProvisioningNotSucceededFiltered(t *testing.T) {
	t.Parallel()
	c := newClient()
	c.SetExchangeablePager(&staticExchangeablePager{
		pages: []*armreservations.ListResult{
			{Value: []*armreservations.ReservationResponse{
				makeReservation(vmID1, "Standard_D2s_v3", 1, provStatePend(), resTypeVM(), ifOn()),
			}},
		},
	})

	result, err := c.ListExchangeableReservations(context.Background())
	require.NoError(t, err)
	assert.Empty(t, result, "non-Succeeded provisioning state must be filtered out")
}

func TestListExchangeableReservations_EligibleIncluded(t *testing.T) {
	t.Parallel()
	c := newClient()
	c.SetExchangeablePager(&staticExchangeablePager{
		pages: []*armreservations.ListResult{
			{Value: []*armreservations.ReservationResponse{
				makeReservation(vmID1, "Standard_D2s_v3", 2, provStateSuc(), resTypeVM(), ifOn()),
			}},
		},
	})

	result, err := c.ListExchangeableReservations(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 1)
	r := result[0]
	assert.Equal(t, "order-1111", r.ReservationOrderID)
	assert.Equal(t, vmID1, r.ReservationID)
	assert.Equal(t, "Standard_D2s_v3", r.SKU)
	assert.Equal(t, int32(2), r.Quantity)
	assert.Equal(t, "eastus", r.Region)
	assert.Equal(t, "P1Y", r.Term)
	assert.Equal(t, "On", r.InstanceFlexibility)
	assert.Equal(t, "test-reservation", r.DisplayName)
	assert.Equal(t, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), r.ExpiryDate)
}

func TestListExchangeableReservations_MultiPage(t *testing.T) {
	t.Parallel()
	c := newClient()
	c.SetExchangeablePager(&staticExchangeablePager{
		pages: []*armreservations.ListResult{
			{Value: []*armreservations.ReservationResponse{
				makeReservation(vmID1, "Standard_D2s_v3", 1, provStateSuc(), resTypeVM(), ifOn()),
			}},
			{Value: []*armreservations.ReservationResponse{
				makeReservation(vmID2, "Standard_F4s_v2", 3, provStateSuc(), resTypeVM(), ifOn()),
			}},
		},
	})

	result, err := c.ListExchangeableReservations(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 2)
	assert.Equal(t, "order-1111", result[0].ReservationOrderID)
	assert.Equal(t, "order-2222", result[1].ReservationOrderID)
}

func TestListExchangeableReservations_MixedEligibility(t *testing.T) {
	t.Parallel()
	c := newClient()
	c.SetExchangeablePager(&staticExchangeablePager{
		pages: []*armreservations.ListResult{
			{Value: []*armreservations.ReservationResponse{
				makeReservation(vmID1, "Standard_D2s_v3", 1, provStateSuc(), resTypeVM(), ifOn()),  // eligible
				makeReservation(sqlID, "Standard_D2s_v3", 1, provStateSuc(), resTypeSQL(), ifOn()), // not VM
				makeReservation(vmID2, "Standard_F4s_v2", 2, provStateSuc(), resTypeVM(), ifOff()), // flex off
			}},
		},
	})

	result, err := c.ListExchangeableReservations(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, vmID1, result[0].ReservationID)
}

func TestListExchangeableReservations_PagerError(t *testing.T) {
	t.Parallel()
	c := newClient()
	wantErr := errors.New("azure api error")
	c.SetExchangeablePager(&errorExchangeablePager{err: wantErr})

	_, err := c.ListExchangeableReservations(context.Background())
	require.Error(t, err)
	assert.ErrorContains(t, err, "azure api error")
}
