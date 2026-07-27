package compute_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/reservations/armreservations"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LeanerCloud/CUDly/providers/azure/services/compute"
)

func validSource() compute.ExchangeableReservation {
	return compute.ExchangeableReservation{
		ReservationID: "/providers/Microsoft.Capacity/reservationOrders/order-1/reservations/res-1",
		Quantity:      2,
	}
}

func validTarget() compute.ExchangeTarget {
	return compute.ExchangeTarget{
		SKU:            "Standard_D4s_v3",
		Location:       "eastus",
		Term:           armreservations.ReservationTermP1Y,
		Quantity:       1,
		BillingScopeID: "/subscriptions/sub-1",
	}
}

func succeededResult(props *armreservations.CalculateExchangeResponseProperties) armreservations.CalculateExchangeOperationResultResponse {
	return armreservations.CalculateExchangeOperationResultResponse{
		Status:     to.Ptr(armreservations.CalculateExchangeOperationResultStatusSucceeded),
		Properties: props,
	}
}

// --- CalculateExchange validation ---

func TestCalculateExchange_ValidationSources(t *testing.T) {
	c := compute.NewClient(nil, "sub-1", "")
	callerInvoked := false
	c.SetCalculateExchangeCaller(func(_ context.Context, _ armreservations.CalculateExchangeRequest) (armreservations.CalculateExchangeOperationResultResponse, error) {
		callerInvoked = true
		return armreservations.CalculateExchangeOperationResultResponse{}, nil
	})

	tests := []struct {
		name    string
		sources []compute.ExchangeableReservation
		wantErr string
	}{
		{"no sources", nil, "at least one source reservation is required"},
		{"empty reservation id", []compute.ExchangeableReservation{{ReservationID: "", Quantity: 1}}, "sources[0].reservation_id is required"},
		{"zero quantity", []compute.ExchangeableReservation{{ReservationID: "res-1", Quantity: 0}}, "sources[0].quantity must be >= 1"},
		{"negative quantity", []compute.ExchangeableReservation{{ReservationID: "res-1", Quantity: -1}}, "sources[0].quantity must be >= 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			preview, offerings, err := c.CalculateExchange(context.Background(), tt.sources, []compute.ExchangeTarget{validTarget()})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.Nil(t, preview)
			assert.Nil(t, offerings)
			assert.False(t, callerInvoked, "caller must not be invoked when source validation fails")
		})
	}
}

func TestCalculateExchange_ValidationTargets(t *testing.T) {
	c := compute.NewClient(nil, "sub-1", "")
	callerInvoked := false
	c.SetCalculateExchangeCaller(func(_ context.Context, _ armreservations.CalculateExchangeRequest) (armreservations.CalculateExchangeOperationResultResponse, error) {
		callerInvoked = true
		return armreservations.CalculateExchangeOperationResultResponse{}, nil
	})

	base := validTarget()
	withSKU := base
	withSKU.SKU = ""
	withLocation := base
	withLocation.Location = ""
	withScope := base
	withScope.BillingScopeID = ""
	withQty := base
	withQty.Quantity = 0
	withTerm := base
	withTerm.Term = armreservations.ReservationTerm("P2Y")

	tests := []struct {
		name    string
		targets []compute.ExchangeTarget
		wantErr string
	}{
		{"no targets", nil, "at least one target is required"},
		{"missing sku", []compute.ExchangeTarget{withSKU}, "targets[0].sku is required"},
		{"missing location", []compute.ExchangeTarget{withLocation}, "targets[0].location is required"},
		{"missing billing scope", []compute.ExchangeTarget{withScope}, "targets[0].billing_scope_id is required"},
		{"zero quantity", []compute.ExchangeTarget{withQty}, "targets[0].quantity must be >= 1"},
		{"unsupported term", []compute.ExchangeTarget{withTerm}, `targets[0].term "P2Y" is not a supported reservation term`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			preview, offerings, err := c.CalculateExchange(context.Background(), []compute.ExchangeableReservation{validSource()}, tt.targets)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.Nil(t, preview)
			assert.Nil(t, offerings)
			assert.False(t, callerInvoked, "caller must not be invoked when target validation fails")
		})
	}
}

// --- CalculateExchange request-builder golden assertions ---

func TestCalculateExchange_RequestBuilderWiring(t *testing.T) {
	c := compute.NewClient(nil, "sub-1", "")
	var captured armreservations.CalculateExchangeRequest
	c.SetCalculateExchangeCaller(func(_ context.Context, body armreservations.CalculateExchangeRequest) (armreservations.CalculateExchangeOperationResultResponse, error) {
		captured = body
		return succeededResult(&armreservations.CalculateExchangeResponseProperties{
			SessionID: to.Ptr("session-abc"),
		}), nil
	})

	source := validSource()
	target := validTarget()
	_, _, err := c.CalculateExchange(context.Background(), []compute.ExchangeableReservation{source}, []compute.ExchangeTarget{target})
	require.NoError(t, err)

	require.NotNil(t, captured.Properties)
	require.Len(t, captured.Properties.ReservationsToExchange, 1)
	toReturn := captured.Properties.ReservationsToExchange[0]
	assert.Equal(t, source.ReservationID, *toReturn.ReservationID)
	assert.Equal(t, source.Quantity, *toReturn.Quantity)

	require.Len(t, captured.Properties.ReservationsToPurchase, 1)
	toPurchase := captured.Properties.ReservationsToPurchase[0]
	assert.Equal(t, target.Location, *toPurchase.Location)
	assert.Equal(t, target.SKU, *toPurchase.SKU.Name)
	require.NotNil(t, toPurchase.Properties)
	assert.Equal(t, armreservations.AppliedScopeTypeShared, *toPurchase.Properties.AppliedScopeType, "AppliedScopeType must default to Shared when unset")
	assert.Equal(t, armreservations.ReservationBillingPlanUpfront, *toPurchase.Properties.BillingPlan)
	assert.Equal(t, target.BillingScopeID, *toPurchase.Properties.BillingScopeID)
	assert.Equal(t, target.Quantity, *toPurchase.Properties.Quantity)
	assert.False(t, *toPurchase.Properties.Renew)
	assert.Equal(t, armreservations.ReservedResourceTypeVirtualMachines, *toPurchase.Properties.ReservedResourceType)
	assert.Equal(t, target.Term, *toPurchase.Properties.Term)
	require.NotNil(t, toPurchase.Properties.ReservedResourceProperties)
	assert.Equal(t, armreservations.InstanceFlexibilityOn, *toPurchase.Properties.ReservedResourceProperties.InstanceFlexibility)
}

func TestCalculateExchange_AppliedScopeTypeOverride(t *testing.T) {
	c := compute.NewClient(nil, "sub-1", "")
	var captured armreservations.CalculateExchangeRequest
	c.SetCalculateExchangeCaller(func(_ context.Context, body armreservations.CalculateExchangeRequest) (armreservations.CalculateExchangeOperationResultResponse, error) {
		captured = body
		return succeededResult(&armreservations.CalculateExchangeResponseProperties{SessionID: to.Ptr("session-abc")}), nil
	})

	single := armreservations.AppliedScopeTypeSingle
	target := validTarget()
	target.AppliedScopeType = &single

	_, _, err := c.CalculateExchange(context.Background(), []compute.ExchangeableReservation{validSource()}, []compute.ExchangeTarget{target})
	require.NoError(t, err)
	assert.Equal(t, armreservations.AppliedScopeTypeSingle, *captured.Properties.ReservationsToPurchase[0].Properties.AppliedScopeType)
}

// --- CalculateExchange response handling ---

func TestCalculateExchange_NilPropertiesError(t *testing.T) {
	c := compute.NewClient(nil, "sub-1", "")
	c.SetCalculateExchangeCaller(func(_ context.Context, _ armreservations.CalculateExchangeRequest) (armreservations.CalculateExchangeOperationResultResponse, error) {
		return succeededResult(nil), nil
	})

	preview, offerings, err := c.CalculateExchange(context.Background(), []compute.ExchangeableReservation{validSource()}, []compute.ExchangeTarget{validTarget()})
	require.Error(t, err, "a nil-properties response must not be fabricated into an empty success")
	assert.Contains(t, err.Error(), "no session id")
	assert.Nil(t, preview)
	assert.Nil(t, offerings)
}

func TestCalculateExchange_EmptySessionIDError(t *testing.T) {
	c := compute.NewClient(nil, "sub-1", "")
	c.SetCalculateExchangeCaller(func(_ context.Context, _ armreservations.CalculateExchangeRequest) (armreservations.CalculateExchangeOperationResultResponse, error) {
		return succeededResult(&armreservations.CalculateExchangeResponseProperties{SessionID: to.Ptr("")}), nil
	})

	_, _, err := c.CalculateExchange(context.Background(), []compute.ExchangeableReservation{validSource()}, []compute.ExchangeTarget{validTarget()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no session id")
}

func TestCalculateExchange_OperationError(t *testing.T) {
	c := compute.NewClient(nil, "sub-1", "")
	c.SetCalculateExchangeCaller(func(_ context.Context, _ armreservations.CalculateExchangeRequest) (armreservations.CalculateExchangeOperationResultResponse, error) {
		return armreservations.CalculateExchangeOperationResultResponse{
			Status: to.Ptr(armreservations.CalculateExchangeOperationResultStatusFailed),
			Error:  &armreservations.OperationResultError{Message: to.Ptr("cross billing account exchange not allowed")},
		}, nil
	})

	_, _, err := c.CalculateExchange(context.Background(), []compute.ExchangeableReservation{validSource()}, []compute.ExchangeTarget{validTarget()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cross billing account exchange not allowed")
}

func TestCalculateExchange_PolicyErrorsExtraction(t *testing.T) {
	c := compute.NewClient(nil, "sub-1", "")
	c.SetCalculateExchangeCaller(func(_ context.Context, _ armreservations.CalculateExchangeRequest) (armreservations.CalculateExchangeOperationResultResponse, error) {
		return succeededResult(&armreservations.CalculateExchangeResponseProperties{
			SessionID: to.Ptr("session-with-policy-errors"),
			PolicyResult: &armreservations.ExchangePolicyErrors{
				PolicyErrors: []*armreservations.ExchangePolicyError{
					{Code: to.Ptr("CrossBillingAccount"), Message: to.Ptr("reservations must share a billing account")},
				},
			},
		}), nil
	})

	preview, _, err := c.CalculateExchange(context.Background(), []compute.ExchangeableReservation{validSource()}, []compute.ExchangeTarget{validTarget()})
	require.NoError(t, err, "a policy-rejected combination is still a successful priced call")
	require.NotNil(t, preview)
	require.Len(t, preview.PolicyErrors, 1)
	assert.Equal(t, "reservations must share a billing account", preview.PolicyErrors[0])
}

func TestCalculateExchange_NilVsZeroMoneyFields(t *testing.T) {
	c := compute.NewClient(nil, "sub-1", "")
	c.SetCalculateExchangeCaller(func(_ context.Context, _ armreservations.CalculateExchangeRequest) (armreservations.CalculateExchangeOperationResultResponse, error) {
		return succeededResult(&armreservations.CalculateExchangeResponseProperties{
			SessionID: to.Ptr("session-no-net-payable"),
			// NetPayable intentionally omitted -- Azure did not report one.
		}), nil
	})

	preview, _, err := c.CalculateExchange(context.Background(), []compute.ExchangeableReservation{validSource()}, []compute.ExchangeTarget{validTarget()})
	require.NoError(t, err)
	assert.Nil(t, preview.NetPayable, "absent NetPayable must stay nil, never coerced to 0")

	c.SetCalculateExchangeCaller(func(_ context.Context, _ armreservations.CalculateExchangeRequest) (armreservations.CalculateExchangeOperationResultResponse, error) {
		return succeededResult(&armreservations.CalculateExchangeResponseProperties{
			SessionID:  to.Ptr("session-zero-net-payable"),
			NetPayable: &armreservations.Price{Amount: to.Ptr(0.0), CurrencyCode: to.Ptr("USD")},
		}), nil
	})
	preview, _, err = c.CalculateExchange(context.Background(), []compute.ExchangeableReservation{validSource()}, []compute.ExchangeTarget{validTarget()})
	require.NoError(t, err)
	require.NotNil(t, preview.NetPayable, "an explicit 0.0 amount must be preserved, not treated the same as absent")
	assert.InDelta(t, 0.0, *preview.NetPayable, 0.0001)
	assert.Equal(t, "USD", preview.NetPayableCurrency)
}

func TestCalculateExchange_OfferingsExtraction(t *testing.T) {
	c := compute.NewClient(nil, "sub-1", "")
	c.SetCalculateExchangeCaller(func(_ context.Context, _ armreservations.CalculateExchangeRequest) (armreservations.CalculateExchangeOperationResultResponse, error) {
		return succeededResult(&armreservations.CalculateExchangeResponseProperties{
			SessionID: to.Ptr("session-offerings"),
			ReservationsToPurchase: []*armreservations.ReservationToPurchaseCalculateExchange{
				{
					BillingCurrencyTotal: &armreservations.Price{Amount: to.Ptr(123.45), CurrencyCode: to.Ptr("EUR")},
					Properties: &armreservations.PurchaseRequest{
						Location: to.Ptr("westeurope"),
						SKU:      &armreservations.SKUName{Name: to.Ptr("Standard_D4s_v3")},
						Properties: &armreservations.PurchaseRequestProperties{
							Quantity: to.Ptr(int32(3)),
							Term:     to.Ptr(armreservations.ReservationTermP3Y),
						},
					},
				},
				nil, // defensive: a nil entry in the SDK slice must not panic the extractor
			},
		}), nil
	})

	_, offerings, err := c.CalculateExchange(context.Background(), []compute.ExchangeableReservation{validSource()}, []compute.ExchangeTarget{validTarget()})
	require.NoError(t, err)
	require.Len(t, offerings, 1)
	o := offerings[0]
	assert.Equal(t, "westeurope", o.Location)
	assert.Equal(t, "Standard_D4s_v3", o.SKU)
	assert.Equal(t, int32(3), o.Quantity)
	assert.Equal(t, "P3Y", o.Term)
	require.NotNil(t, o.BillingCurrencyTotal)
	assert.InDelta(t, 123.45, *o.BillingCurrencyTotal, 0.0001)
	assert.Equal(t, "EUR", o.CurrencyCode)
}

func TestCalculateExchange_CtxCancelPassthrough(t *testing.T) {
	c := compute.NewClient(nil, "sub-1", "")
	c.SetCalculateExchangeCaller(func(_ context.Context, _ armreservations.CalculateExchangeRequest) (armreservations.CalculateExchangeOperationResultResponse, error) {
		return armreservations.CalculateExchangeOperationResultResponse{}, context.Canceled
	})

	_, _, err := c.CalculateExchange(context.Background(), []compute.ExchangeableReservation{validSource()}, []compute.ExchangeTarget{validTarget()})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled, "context cancellation must propagate unwrapped, not be folded into a generic error string")
}

func TestCalculateExchange_CallerAPIError(t *testing.T) {
	c := compute.NewClient(nil, "sub-1", "")
	c.SetCalculateExchangeCaller(func(_ context.Context, _ armreservations.CalculateExchangeRequest) (armreservations.CalculateExchangeOperationResultResponse, error) {
		return armreservations.CalculateExchangeOperationResultResponse{}, errors.New("azure: CalculateExchange begin: transport error")
	})

	_, _, err := c.CalculateExchange(context.Background(), []compute.ExchangeableReservation{validSource()}, []compute.ExchangeTarget{validTarget()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "transport error")
}

// --- ExecuteExchange ---

func TestExecuteExchange_EmptySessionID(t *testing.T) {
	c := compute.NewClient(nil, "sub-1", "")
	c.SetDoExchangeCaller(func(_ context.Context, _ string) (armreservations.ExchangeOperationResultResponse, error) {
		t.Fatal("caller must not be invoked when session_id is empty")
		return armreservations.ExchangeOperationResultResponse{}, nil
	})

	res, err := c.ExecuteExchange(context.Background(), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "session_id is required")
	assert.Nil(t, res)
}

func TestExecuteExchange_NilPropertiesError(t *testing.T) {
	c := compute.NewClient(nil, "sub-1", "")
	c.SetDoExchangeCaller(func(_ context.Context, _ string) (armreservations.ExchangeOperationResultResponse, error) {
		return armreservations.ExchangeOperationResultResponse{
			Status: to.Ptr(armreservations.ExchangeOperationResultStatusSucceeded),
		}, nil
	})

	res, err := c.ExecuteExchange(context.Background(), "session-abc")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no properties")
	assert.Nil(t, res)
}

func TestExecuteExchange_OperationError(t *testing.T) {
	c := compute.NewClient(nil, "sub-1", "")
	c.SetDoExchangeCaller(func(_ context.Context, _ string) (armreservations.ExchangeOperationResultResponse, error) {
		return armreservations.ExchangeOperationResultResponse{
			Status: to.Ptr(armreservations.ExchangeOperationResultStatusFailed),
			Error:  &armreservations.OperationResultError{Message: to.Ptr("session expired")},
		}, nil
	})

	res, err := c.ExecuteExchange(context.Background(), "session-abc")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "session expired")
	assert.Nil(t, res)
}

func TestExecuteExchange_CtxCancelPassthrough(t *testing.T) {
	c := compute.NewClient(nil, "sub-1", "")
	c.SetDoExchangeCaller(func(_ context.Context, _ string) (armreservations.ExchangeOperationResultResponse, error) {
		return armreservations.ExchangeOperationResultResponse{}, context.DeadlineExceeded
	})

	_, err := c.ExecuteExchange(context.Background(), "session-abc")
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestExecuteExchange_HappyPath(t *testing.T) {
	c := compute.NewClient(nil, "sub-1", "")
	var capturedSessionID string
	c.SetDoExchangeCaller(func(_ context.Context, sessionID string) (armreservations.ExchangeOperationResultResponse, error) {
		capturedSessionID = sessionID
		return armreservations.ExchangeOperationResultResponse{
			Status: to.Ptr(armreservations.ExchangeOperationResultStatusSucceeded),
			Properties: &armreservations.ExchangeResponseProperties{
				NetPayable: &armreservations.Price{Amount: to.Ptr(42.5), CurrencyCode: to.Ptr("USD")},
			},
		}, nil
	})

	res, err := c.ExecuteExchange(context.Background(), "session-xyz")
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, "session-xyz", capturedSessionID, "the exact session id passed in must be the one sent to Azure")
	assert.Equal(t, "session-xyz", res.SessionID)
	require.NotNil(t, res.NetPayable)
	assert.InDelta(t, 42.5, *res.NetPayable, 0.0001)
	assert.Equal(t, "USD", res.NetPayableCurrency)
	assert.Equal(t, string(armreservations.ExchangeOperationResultStatusSucceeded), res.Status)
}
