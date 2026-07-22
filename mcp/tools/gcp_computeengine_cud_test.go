package tools

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/provider"
)

func validGCPCUDArgs() gcpComputeEngineCUDPurchaseArgs {
	return gcpComputeEngineCUDPurchaseArgs{
		Region:      "us-central1",
		MachineType: "n2-standard-4",
		VCPUCount:   4,
		MemoryGB:    16,
		TermYears:   1,
	}
}

func TestGCPComputeEngineRecommendationFromArgs(t *testing.T) {
	t.Parallel()
	rec, dryRun, confirm, err := gcpComputeEngineRecommendationFromArgs(validGCPCUDArgs())
	require.NoError(t, err)
	assert.True(t, dryRun)
	assert.False(t, confirm)
	assert.Equal(t, common.ProviderGCP, rec.Provider)
	assert.Equal(t, common.ServiceCompute, rec.Service)
	assert.Equal(t, common.CommitmentCUD, rec.CommitmentType)
	assert.Equal(t, "n2-standard-4", rec.ResourceType)
	assert.Equal(t, 4, rec.Count)
	assert.Equal(t, "1yr", rec.Term)

	// Details MUST be a value common.ComputeDetails, not a pointer:
	// providers/gcp/services/computeengine/client.go's memoryMBFromDetails
	// type-asserts rec.Details.(common.ComputeDetails), unlike every AWS
	// Details assertion which expects a pointer.
	details, ok := rec.Details.(common.ComputeDetails)
	require.True(t, ok, "Details must be a value common.ComputeDetails, not *common.ComputeDetails")
	assert.InDelta(t, 16.0, details.MemoryGB, 0.001)
}

func TestGCPComputeEngineRecommendationFromArgsInvalid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		mutate func(*gcpComputeEngineCUDPurchaseArgs)
		errSub string
	}{
		{"missing region", func(a *gcpComputeEngineCUDPurchaseArgs) { a.Region = "" }, "region is required"},
		{"missing machine_type", func(a *gcpComputeEngineCUDPurchaseArgs) { a.MachineType = "" }, "machine_type is required"},
		{"zero vcpu_count", func(a *gcpComputeEngineCUDPurchaseArgs) { a.VCPUCount = 0 }, "vcpu_count must be"},
		{"zero memory_gb", func(a *gcpComputeEngineCUDPurchaseArgs) { a.MemoryGB = 0 }, "memory_gb must be"},
		{"negative memory_gb", func(a *gcpComputeEngineCUDPurchaseArgs) { a.MemoryGB = -1 }, "memory_gb must be"},
		{"invalid term", func(a *gcpComputeEngineCUDPurchaseArgs) { a.TermYears = 2 }, "invalid term_years"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := validGCPCUDArgs()
			tc.mutate(&args)
			_, _, _, err := gcpComputeEngineRecommendationFromArgs(args)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.errSub)
		})
	}
}

func TestGCPComputeEngineCUDPurchaseHandleConfirmFalseRefuses(t *testing.T) {
	t.Parallel()
	resolveCalled := false
	tool := &gcpComputeEngineCUDPurchaseTool{
		createProvider: func(_ string, _ *provider.ProviderConfig) (provider.Provider, error) {
			resolveCalled = true
			return nil, nil
		},
	}
	args := validGCPCUDArgs()
	args.DryRun = boolPtr(false)
	args.Confirm = boolPtr(false)

	_, _, err := tool.handle(context.Background(), nil, args)
	require.Error(t, err)
	assert.False(t, resolveCalled)
	assert.Contains(t, err.Error(), "confirm=true")
}

func TestGCPComputeEngineCUDPurchaseHandleDryRunNeverCallsProvider(t *testing.T) {
	t.Parallel()
	resolveCalled := false
	tool := &gcpComputeEngineCUDPurchaseTool{
		createProvider: func(_ string, _ *provider.ProviderConfig) (provider.Provider, error) {
			resolveCalled = true
			return nil, nil
		},
	}
	args := validGCPCUDArgs()
	args.Confirm = boolPtr(true)

	_, resp, err := tool.handle(context.Background(), nil, args)
	require.NoError(t, err)
	assert.False(t, resolveCalled)
	assert.True(t, resp.DryRun)
}

func TestGCPComputeEngineCUDPurchaseHandleRealPurchase(t *testing.T) {
	t.Parallel()
	fake := &fakeServiceClient{purchaseResult: common.PurchaseResult{Success: true, CommitmentID: "cud-1"}}
	var gotService common.ServiceType
	tool := &gcpComputeEngineCUDPurchaseTool{
		createProvider: func(_ string, _ *provider.ProviderConfig) (provider.Provider, error) {
			return &recordingProvider{
				fakeProvider: &fakeProvider{name: "gcp"},
				client:       fake,
				gotService:   &gotService,
				gotRegion:    new(string),
			}, nil
		},
	}
	args := validGCPCUDArgs()
	args.DryRun = boolPtr(false)
	args.Confirm = boolPtr(true)

	_, resp, err := tool.handle(context.Background(), nil, args)
	require.NoError(t, err)
	assert.True(t, resp.Success)
	assert.Equal(t, common.ServiceCompute, gotService)
	assert.Equal(t, common.PurchaseSourceMCP, fake.lastOpts.Source)
}
