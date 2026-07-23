package tools

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/provider"
)

func boolPtr(b bool) *bool { return &b }

func validEC2Args() ec2RIPurchaseArgs {
	return ec2RIPurchaseArgs{
		Region:        "us-east-1",
		InstanceType:  "m5.large",
		Count:         3,
		TermYears:     3,
		PaymentOption: "no-upfront",
	}
}

func TestEC2RecommendationFromArgsDefaults(t *testing.T) {
	t.Parallel()
	rec, dryRun, confirm, err := ec2RecommendationFromArgs(validEC2Args())
	require.NoError(t, err)
	assert.True(t, dryRun, "dry_run must default to true")
	assert.False(t, confirm, "confirm must default to false")
	assert.Equal(t, common.ProviderAWS, rec.Provider)
	assert.Equal(t, common.ServiceEC2, rec.Service)
	assert.Equal(t, "m5.large", rec.ResourceType)
	assert.Equal(t, 3, rec.Count)
	assert.Equal(t, "3yr", rec.Term)
	assert.Equal(t, "no-upfront", rec.PaymentOption)
	details, ok := rec.Details.(*common.ComputeDetails)
	require.True(t, ok, "Details must be *common.ComputeDetails")
	assert.Equal(t, "Linux/UNIX", details.Platform)
	assert.Equal(t, "default", details.Tenancy)
	assert.Equal(t, "region", details.Scope)
}

func TestEC2RecommendationFromArgsExplicitFlags(t *testing.T) {
	t.Parallel()
	args := validEC2Args()
	args.DryRun = boolPtr(false)
	args.Confirm = boolPtr(true)
	args.Platform = "Windows"
	args.Tenancy = "dedicated"
	args.Scope = "availability-zone"

	rec, dryRun, confirm, err := ec2RecommendationFromArgs(args)
	require.NoError(t, err)
	assert.False(t, dryRun)
	assert.True(t, confirm)
	details, ok := rec.Details.(*common.ComputeDetails)
	require.True(t, ok)
	assert.Equal(t, "Windows", details.Platform)
	assert.Equal(t, "dedicated", details.Tenancy)
	assert.Equal(t, "availability-zone", details.Scope)
}

func TestEC2RecommendationFromArgsMissingRequiredFields(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		mutate func(*ec2RIPurchaseArgs)
		errSub string
	}{
		{"missing region", func(a *ec2RIPurchaseArgs) { a.Region = "" }, "region is required"},
		{"whitespace-only region", func(a *ec2RIPurchaseArgs) { a.Region = "   " }, "region is required"},
		{"missing instance_type", func(a *ec2RIPurchaseArgs) { a.InstanceType = "" }, "instance_type is required"},
		{"whitespace-only instance_type", func(a *ec2RIPurchaseArgs) { a.InstanceType = "\t " }, "instance_type is required"},
		{"zero count", func(a *ec2RIPurchaseArgs) { a.Count = 0 }, "count must be"},
		{"negative count", func(a *ec2RIPurchaseArgs) { a.Count = -1 }, "count must be"},
		{"invalid term", func(a *ec2RIPurchaseArgs) { a.TermYears = 2 }, "invalid term_years"},
		{"invalid payment option", func(a *ec2RIPurchaseArgs) { a.PaymentOption = "bogus" }, "invalid payment_option"},
		{"invalid platform", func(a *ec2RIPurchaseArgs) { a.Platform = "MacOS" }, "invalid platform"},
		{"invalid tenancy", func(a *ec2RIPurchaseArgs) { a.Tenancy = "host" }, "invalid tenancy"},
		{"invalid scope", func(a *ec2RIPurchaseArgs) { a.Scope = "zonal" }, "invalid scope"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := validEC2Args()
			tc.mutate(&args)
			_, _, _, err := ec2RecommendationFromArgs(args)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.errSub)
		})
	}
}

// TestAWSEC2RIPurchaseHandleConfirmFalseRefuses proves the end-to-end tool
// handler refuses a dry_run=false, confirm=false call with a structured
// error, never touching the provider.
func TestAWSEC2RIPurchaseHandleConfirmFalseRefuses(t *testing.T) {
	t.Parallel()
	resolveCalled := false
	tool := &awsEC2RIPurchaseTool{
		createProvider: func(_ string, _ *provider.ProviderConfig) (provider.Provider, error) {
			resolveCalled = true
			return nil, nil
		},
	}
	args := validEC2Args()
	args.DryRun = boolPtr(false)
	args.Confirm = boolPtr(false)

	_, _, err := tool.handle(context.Background(), nil, args)
	require.Error(t, err)
	assert.False(t, resolveCalled)
	assert.Contains(t, err.Error(), "confirm=true")
}

// TestAWSEC2RIPurchaseHandleDryRunNeverCallsProvider proves the default
// dry_run=true path never resolves a provider, even with confirm=true.
func TestAWSEC2RIPurchaseHandleDryRunNeverCallsProvider(t *testing.T) {
	t.Parallel()
	resolveCalled := false
	tool := &awsEC2RIPurchaseTool{
		createProvider: func(_ string, _ *provider.ProviderConfig) (provider.Provider, error) {
			resolveCalled = true
			return nil, nil
		},
	}
	args := validEC2Args()
	args.Confirm = boolPtr(true) // dry_run stays at its true default

	_, resp, err := tool.handle(context.Background(), nil, args)
	require.NoError(t, err)
	assert.False(t, resolveCalled)
	assert.True(t, resp.DryRun)
	assert.True(t, resp.Success)
}

// TestAWSEC2RIPurchaseHandleInvalidArgsNeverCallsProvider proves a boundary
// validation failure (bad enum) short-circuits before any provider call.
func TestAWSEC2RIPurchaseHandleInvalidArgsNeverCallsProvider(t *testing.T) {
	t.Parallel()
	resolveCalled := false
	tool := &awsEC2RIPurchaseTool{
		createProvider: func(_ string, _ *provider.ProviderConfig) (provider.Provider, error) {
			resolveCalled = true
			return nil, nil
		},
	}
	args := validEC2Args()
	args.TermYears = 5 // invalid

	_, _, err := tool.handle(context.Background(), nil, args)
	require.Error(t, err)
	assert.False(t, resolveCalled)
	assert.Contains(t, err.Error(), "invalid term_years")
}

// TestAWSEC2RIPurchaseHandleRealPurchaseResolvesEC2Client proves a
// confirmed real purchase resolves the AWS EC2 service client for the
// requested region.
func TestAWSEC2RIPurchaseHandleRealPurchaseResolvesEC2Client(t *testing.T) {
	t.Parallel()
	fake := &fakeServiceClient{purchaseResult: common.PurchaseResult{Success: true, CommitmentID: "ri-abc"}}
	var gotService common.ServiceType
	var gotRegion string
	fp := &fakeProvider{
		name: "aws",
	}
	tool := &awsEC2RIPurchaseTool{
		createProvider: func(_ string, _ *provider.ProviderConfig) (provider.Provider, error) {
			return &recordingProvider{fakeProvider: fp, client: fake, gotService: &gotService, gotRegion: &gotRegion}, nil
		},
	}
	args := validEC2Args()
	args.DryRun = boolPtr(false)
	args.Confirm = boolPtr(true)

	_, resp, err := tool.handle(context.Background(), nil, args)
	require.NoError(t, err)
	assert.True(t, resp.Success)
	assert.Equal(t, "ri-abc", resp.CommitmentID)
	assert.Equal(t, common.ServiceEC2, gotService)
	assert.Equal(t, "us-east-1", gotRegion)
	assert.Equal(t, 1, fake.purchaseCalls)
	assert.Equal(t, common.PurchaseSourceMCP, fake.lastOpts.Source)
}

// recordingProvider wraps fakeProvider to capture the service/region passed
// to GetServiceClient and always return a fixed ServiceClient.
type recordingProvider struct {
	*fakeProvider
	client     provider.ServiceClient
	gotService *common.ServiceType
	gotRegion  *string
}

func (r *recordingProvider) GetServiceClient(_ context.Context, service common.ServiceType, region string) (provider.ServiceClient, error) {
	*r.gotService = service
	*r.gotRegion = region
	return r.client, nil
}
