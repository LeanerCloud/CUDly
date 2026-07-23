package tools

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/provider"
)

// validAzureComputeArgs uses payment_option=all-upfront. Azure Reserved
// Instances honor both all-upfront and no-upfront (see
// TestAzureComputeRecommendationFromArgsAcceptsNoUpfront); all-upfront is
// used here just to keep the baseline args deterministic across tests that
// don't care which honored schedule they exercise.
func validAzureComputeArgs() azureComputeRIPurchaseArgs {
	return azureComputeRIPurchaseArgs{
		Region:        "eastus",
		VMSize:        "Standard_D2s_v3",
		Count:         2,
		TermYears:     3,
		PaymentOption: "all-upfront",
	}
}

func TestAzureComputeRecommendationFromArgs(t *testing.T) {
	t.Parallel()
	rec, dryRun, confirm, err := azureComputeRecommendationFromArgs(validAzureComputeArgs())
	require.NoError(t, err)
	assert.True(t, dryRun)
	assert.False(t, confirm)
	assert.Equal(t, common.ProviderAzure, rec.Provider)
	assert.Equal(t, common.ServiceCompute, rec.Service)
	assert.Equal(t, "Standard_D2s_v3", rec.ResourceType)
	assert.Equal(t, 2, rec.Count)
	assert.Equal(t, "3yr", rec.Term)
	assert.Nil(t, rec.Details, "Azure VM purchase reads no Recommendation.Details")
}

func TestAzureComputeRecommendationFromArgsInvalid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		mutate func(*azureComputeRIPurchaseArgs)
		errSub string
	}{
		{"missing region", func(a *azureComputeRIPurchaseArgs) { a.Region = "" }, "region is required"},
		{"missing vm_size", func(a *azureComputeRIPurchaseArgs) { a.VMSize = "" }, "vm_size is required"},
		{"zero count", func(a *azureComputeRIPurchaseArgs) { a.Count = 0 }, "count must be"},
		{"invalid term", func(a *azureComputeRIPurchaseArgs) { a.TermYears = 2 }, "invalid term_years"},
		{"invalid payment option", func(a *azureComputeRIPurchaseArgs) { a.PaymentOption = "bogus" }, "invalid payment_option"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := validAzureComputeArgs()
			tc.mutate(&args)
			_, _, _, err := azureComputeRecommendationFromArgs(args)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.errSub)
		})
	}
}

// TestAzureComputeRecommendationFromArgsRejectsPartialUpfront proves Azure's
// billing-plan contract has exactly two members (Upfront, Monthly -- see
// providers/azure/services/internal/reservations.BillingPlanForPaymentOption):
// partial-upfront has no Azure equivalent at any layer, so it must be
// rejected with an explicit error rather than silently purchased under
// all-upfront or no-upfront instead. Unlike the former all-upfront-only gate
// (removed once billingPlan wiring landed), this rejection is unconditional:
// it fires for a dry_run preview too, because Azure can never honor
// partial-upfront for real, not just a gap in this tool's own behavior.
func TestAzureComputeRecommendationFromArgsRejectsPartialUpfront(t *testing.T) {
	t.Parallel()
	for _, dryRun := range []bool{true, false} {
		t.Run(fmt.Sprintf("dry_run=%v", dryRun), func(t *testing.T) {
			args := validAzureComputeArgs()
			args.PaymentOption = "partial-upfront"
			args.DryRun = boolPtr(dryRun)
			args.Confirm = boolPtr(true)
			_, _, _, err := azureComputeRecommendationFromArgs(args)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "partial-upfront")
			assert.Contains(t, err.Error(), "no-upfront")
		})
	}
}

// TestAzureComputeRecommendationFromArgsAcceptsAllUpfront proves the
// all-upfront billing plan is honored for real (dry_run=false, confirm=true).
func TestAzureComputeRecommendationFromArgsAcceptsAllUpfront(t *testing.T) {
	t.Parallel()
	args := validAzureComputeArgs()
	args.PaymentOption = "all-upfront"
	args.DryRun = boolPtr(false)
	args.Confirm = boolPtr(true)
	rec, dryRun, confirm, err := azureComputeRecommendationFromArgs(args)
	require.NoError(t, err)
	assert.False(t, dryRun)
	assert.True(t, confirm)
	assert.Equal(t, "all-upfront", rec.PaymentOption)
}

// TestAzureComputeRecommendationFromArgsAcceptsNoUpfront proves the
// no-upfront billing plan (armreservations.ReservationBillingPlanMonthly) is
// honored for real, not just at preview time -- the gap this PR closes.
func TestAzureComputeRecommendationFromArgsAcceptsNoUpfront(t *testing.T) {
	t.Parallel()
	args := validAzureComputeArgs()
	args.PaymentOption = "no-upfront"
	args.DryRun = boolPtr(false)
	args.Confirm = boolPtr(true)
	rec, dryRun, confirm, err := azureComputeRecommendationFromArgs(args)
	require.NoError(t, err)
	assert.False(t, dryRun)
	assert.True(t, confirm)
	assert.Equal(t, "no-upfront", rec.PaymentOption)
}

// TestAzureComputeRecommendationFromArgsDefaultsToNoUpfront proves omitting
// payment_option defaults to no-upfront (matching the CLI's --payment
// default, cmd/main.go), not to Azure's raw API default (all-upfront) and
// not to an error.
func TestAzureComputeRecommendationFromArgsDefaultsToNoUpfront(t *testing.T) {
	t.Parallel()
	args := validAzureComputeArgs()
	args.PaymentOption = ""
	rec, _, _, err := azureComputeRecommendationFromArgs(args)
	require.NoError(t, err)
	assert.Equal(t, "no-upfront", rec.PaymentOption)
}

func TestAzureComputeRIPurchaseHandleConfirmFalseRefuses(t *testing.T) {
	t.Parallel()
	resolveCalled := false
	tool := &azureComputeRIPurchaseTool{
		createProvider: func(_ string, _ *provider.ProviderConfig) (provider.Provider, error) {
			resolveCalled = true
			return nil, nil
		},
	}
	args := validAzureComputeArgs()
	args.DryRun = boolPtr(false)
	args.Confirm = boolPtr(false)

	_, _, err := tool.handle(context.Background(), nil, args)
	require.Error(t, err)
	assert.False(t, resolveCalled)
	assert.Contains(t, err.Error(), "confirm=true")
}

// TestAzureComputeRIPurchaseHandleDryRunAcceptsNoUpfront proves a preview
// validates and accepts no-upfront (it is now an honored billing plan, not
// merely tolerated at preview time).
func TestAzureComputeRIPurchaseHandleDryRunAcceptsNoUpfront(t *testing.T) {
	t.Parallel()
	resolveCalled := false
	tool := &azureComputeRIPurchaseTool{
		createProvider: func(_ string, _ *provider.ProviderConfig) (provider.Provider, error) {
			resolveCalled = true
			return nil, nil
		},
	}
	args := validAzureComputeArgs()
	args.PaymentOption = "no-upfront"
	args.DryRun = boolPtr(true)

	_, resp, err := tool.handle(context.Background(), nil, args)
	require.NoError(t, err)
	assert.False(t, resolveCalled)
	assert.True(t, resp.DryRun)
}

// TestAzureComputeRIPurchaseHandleRealPurchaseRejectsPartialUpfront proves
// the one payment_option Azure cannot express (partial-upfront) is still
// rejected for a real purchase after the billingPlan wiring landed.
func TestAzureComputeRIPurchaseHandleRealPurchaseRejectsPartialUpfront(t *testing.T) {
	t.Parallel()
	resolveCalled := false
	tool := &azureComputeRIPurchaseTool{
		createProvider: func(_ string, _ *provider.ProviderConfig) (provider.Provider, error) {
			resolveCalled = true
			return nil, nil
		},
	}
	args := validAzureComputeArgs()
	args.PaymentOption = "partial-upfront"
	args.DryRun = boolPtr(false)
	args.Confirm = boolPtr(true)

	_, _, err := tool.handle(context.Background(), nil, args)
	require.Error(t, err)
	assert.False(t, resolveCalled)
	assert.Contains(t, err.Error(), "partial-upfront")
}

// TestAzureComputeRIPurchaseHandleRealPurchaseNoUpfront proves a real
// purchase (dry_run=false, confirm=true) with payment_option=no-upfront
// reaches the provider -- the core gap this PR closes: before billingPlan
// wiring, only all-upfront could execute for real.
func TestAzureComputeRIPurchaseHandleRealPurchaseNoUpfront(t *testing.T) {
	t.Parallel()
	fake := &fakeServiceClient{purchaseResult: common.PurchaseResult{Success: true, CommitmentID: "azure-res-monthly"}}
	tool := &azureComputeRIPurchaseTool{
		createProvider: func(_ string, _ *provider.ProviderConfig) (provider.Provider, error) {
			return &recordingProvider{
				fakeProvider: &fakeProvider{name: "azure"},
				client:       fake,
				gotService:   new(common.ServiceType),
				gotRegion:    new(string),
			}, nil
		},
	}
	args := validAzureComputeArgs()
	args.PaymentOption = "no-upfront"
	args.DryRun = boolPtr(false)
	args.Confirm = boolPtr(true)

	_, resp, err := tool.handle(context.Background(), nil, args)
	require.NoError(t, err)
	assert.True(t, resp.Success)
	assert.Equal(t, "no-upfront", fake.purchaseResult.Recommendation.PaymentOption)
}

// TestAzureComputeRIPurchaseHandleOmittedPaymentOptionDefaultsToNoUpfront
// proves the tool-level default (payment_option omitted from the request)
// flows through the handler the same way an explicit no-upfront does.
func TestAzureComputeRIPurchaseHandleOmittedPaymentOptionDefaultsToNoUpfront(t *testing.T) {
	t.Parallel()
	fake := &fakeServiceClient{purchaseResult: common.PurchaseResult{Success: true, CommitmentID: "azure-res-default"}}
	tool := &azureComputeRIPurchaseTool{
		createProvider: func(_ string, _ *provider.ProviderConfig) (provider.Provider, error) {
			return &recordingProvider{
				fakeProvider: &fakeProvider{name: "azure"},
				client:       fake,
				gotService:   new(common.ServiceType),
				gotRegion:    new(string),
			}, nil
		},
	}
	args := validAzureComputeArgs()
	args.PaymentOption = ""
	args.DryRun = boolPtr(false)
	args.Confirm = boolPtr(true)

	_, resp, err := tool.handle(context.Background(), nil, args)
	require.NoError(t, err)
	assert.True(t, resp.Success)
	assert.Equal(t, "no-upfront", fake.purchaseResult.Recommendation.PaymentOption)
}

func TestAzureComputeRIPurchaseHandleDryRunNeverCallsProvider(t *testing.T) {
	t.Parallel()
	resolveCalled := false
	tool := &azureComputeRIPurchaseTool{
		createProvider: func(_ string, _ *provider.ProviderConfig) (provider.Provider, error) {
			resolveCalled = true
			return nil, nil
		},
	}
	args := validAzureComputeArgs()
	args.Confirm = boolPtr(true)

	_, resp, err := tool.handle(context.Background(), nil, args)
	require.NoError(t, err)
	assert.False(t, resolveCalled)
	assert.True(t, resp.DryRun)
}

func TestAzureComputeRIPurchaseHandleRealPurchase(t *testing.T) {
	t.Parallel()
	fake := &fakeServiceClient{purchaseResult: common.PurchaseResult{Success: true, CommitmentID: "azure-res-1"}}
	var gotService common.ServiceType
	tool := &azureComputeRIPurchaseTool{
		createProvider: func(_ string, _ *provider.ProviderConfig) (provider.Provider, error) {
			return &recordingProvider{
				fakeProvider: &fakeProvider{name: "azure"},
				client:       fake,
				gotService:   &gotService,
				gotRegion:    new(string),
			}, nil
		},
	}
	args := validAzureComputeArgs()
	args.DryRun = boolPtr(false)
	args.Confirm = boolPtr(true)

	_, resp, err := tool.handle(context.Background(), nil, args)
	require.NoError(t, err)
	assert.True(t, resp.Success)
	assert.Equal(t, common.ServiceCompute, gotService)
	assert.Equal(t, common.PurchaseSourceMCP, fake.lastOpts.Source)
}
