package tools

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/provider"
)

// simpleToolConstructors covers every simpleAWSRIPurchaseTool instance so
// the shared safety-rail behavior (confirm gate, dry_run gate, boundary
// validation, real-purchase wiring) is proven once per product rather than
// hand-copied three times.
func simpleToolConstructors() map[string]func() Registration {
	return map[string]func() Registration{
		"opensearch": NewAWSOpenSearchRIPurchaseTool,
		"redshift":   NewAWSRedshiftRIPurchaseTool,
		"memorydb":   NewAWSMemoryDBRIPurchaseTool,
	}
}

func validSimpleArgs() simpleAWSRIPurchaseArgs {
	return simpleAWSRIPurchaseArgs{
		Region:        "us-east-1",
		ResourceType:  "r6g.large",
		Count:         2,
		TermYears:     1,
		PaymentOption: "all-upfront",
	}
}

func TestSimpleAWSRIPurchaseDescriptorsAreDistinctAndRealPurchaseEnabled(t *testing.T) {
	t.Parallel()
	names := map[string]bool{}
	for product, ctor := range simpleToolConstructors() {
		d := ctor().Descriptor()
		assert.True(t, d.RealPurchaseEnabled, "%s must be real-purchase enabled", product)
		assert.False(t, names[d.Name], "duplicate tool name %q", d.Name)
		names[d.Name] = true
		assert.NotEmpty(t, d.ExamplePrompts, "%s must document example prompts", product)
	}
}

// TestSimpleAWSRIPurchaseDescriptorUsesProperlyCasedDisplayName proves the
// CodeRabbit finding: t.spec.product ("opensearch", "redshift", "memorydb")
// used to be interpolated raw into the human-readable description,
// producing "AWS opensearch Reserved Instances" instead of the properly
// cased "AWS OpenSearch Reserved Instances". The identifier used in API
// calls (spec.product) must stay lowercase; only the description text uses
// the display name.
func TestSimpleAWSRIPurchaseDescriptorUsesProperlyCasedDisplayName(t *testing.T) {
	t.Parallel()
	wantDisplayName := map[string]string{
		"opensearch": "OpenSearch",
		"redshift":   "Redshift",
		"memorydb":   "MemoryDB",
	}
	for product, ctor := range simpleToolConstructors() {
		t.Run(product, func(t *testing.T) {
			d := ctor().Descriptor()
			want := wantDisplayName[product]
			require.NotEmpty(t, want, "test table missing a display name for %s", product)
			assert.Contains(t, d.Description, want)
			assert.NotContains(t, d.Description, "AWS "+product+" ", "description must not use the raw lowercase identifier")
		})
	}
}

func TestSimpleAWSRIPurchaseRecommendationFromArgs(t *testing.T) {
	t.Parallel()
	for product, ctor := range simpleToolConstructors() {
		t.Run(product, func(t *testing.T) {
			tool := ctor().(*simpleAWSRIPurchaseTool)
			rec, region, dryRun, confirm, err := tool.recommendationFromArgs(validSimpleArgs())
			require.NoError(t, err)
			assert.Equal(t, "us-east-1", region)
			assert.True(t, dryRun)
			assert.False(t, confirm)
			assert.Equal(t, common.ProviderAWS, rec.Provider)
			assert.Equal(t, tool.spec.service, rec.Service)
			assert.Equal(t, "r6g.large", rec.ResourceType)
			assert.Equal(t, 2, rec.Count)
			assert.Equal(t, "1yr", rec.Term)
			assert.Equal(t, "all-upfront", rec.PaymentOption)
			assert.Nil(t, rec.Details, "%s must not require service Details", product)
		})
	}
}

func TestSimpleAWSRIPurchaseInvalidArgs(t *testing.T) {
	t.Parallel()
	tool := NewAWSOpenSearchRIPurchaseTool().(*simpleAWSRIPurchaseTool)
	cases := []struct {
		name   string
		mutate func(*simpleAWSRIPurchaseArgs)
		errSub string
	}{
		{"missing region", func(a *simpleAWSRIPurchaseArgs) { a.Region = "" }, "region is required"},
		{"whitespace-only region", func(a *simpleAWSRIPurchaseArgs) { a.Region = "   " }, "region is required"},
		{"missing resource_type", func(a *simpleAWSRIPurchaseArgs) { a.ResourceType = "" }, "resource_type is required"},
		{"whitespace-only resource_type", func(a *simpleAWSRIPurchaseArgs) { a.ResourceType = "\t " }, "resource_type is required"},
		{"zero count", func(a *simpleAWSRIPurchaseArgs) { a.Count = 0 }, "count must be"},
		{"invalid term", func(a *simpleAWSRIPurchaseArgs) { a.TermYears = 4 }, "invalid term_years"},
		{"invalid payment option", func(a *simpleAWSRIPurchaseArgs) { a.PaymentOption = "bogus" }, "invalid payment_option"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := validSimpleArgs()
			tc.mutate(&args)
			_, _, _, _, err := tool.recommendationFromArgs(args)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.errSub)
		})
	}
}

// TestSimpleAWSRIPurchaseRecommendationFromArgsTrimsSurroundingWhitespace is
// the regression guard for the CodeRabbit finding: requireNonBlank rejected
// an all-whitespace value but let a value with surrounding whitespace (e.g.
// " us-east-1 ") pass through unchanged into rec.Region/rec.ResourceType and
// the returned region (which resolveClient uses for ProviderConfig.Region
// and GetServiceClient).
func TestSimpleAWSRIPurchaseRecommendationFromArgsTrimsSurroundingWhitespace(t *testing.T) {
	t.Parallel()
	tool := NewAWSOpenSearchRIPurchaseTool().(*simpleAWSRIPurchaseTool)
	args := validSimpleArgs()
	args.Region = " us-east-1 "
	args.ResourceType = " r6g.large "

	rec, region, _, _, err := tool.recommendationFromArgs(args)
	require.NoError(t, err)
	assert.Equal(t, "us-east-1", region, "returned region must be trimmed")
	assert.Equal(t, "us-east-1", rec.Region, "rec.Region must be trimmed")
	assert.Equal(t, "r6g.large", rec.ResourceType, "rec.ResourceType must be trimmed")
}

func TestSimpleAWSRIPurchaseHandleConfirmFalseRefuses(t *testing.T) {
	t.Parallel()
	for product, ctor := range simpleToolConstructors() {
		t.Run(product, func(t *testing.T) {
			tool := ctor().(*simpleAWSRIPurchaseTool)
			resolveCalled := false
			tool.createProvider = func(_ string, _ *provider.ProviderConfig) (provider.Provider, error) {
				resolveCalled = true
				return nil, nil
			}
			args := validSimpleArgs()
			args.DryRun = boolPtr(false)
			args.Confirm = boolPtr(false)

			_, _, err := tool.handle(context.Background(), nil, args)
			require.Error(t, err)
			assert.False(t, resolveCalled)
			assert.Contains(t, err.Error(), "confirm=true")
		})
	}
}

func TestSimpleAWSRIPurchaseHandleDryRunNeverCallsProvider(t *testing.T) {
	t.Parallel()
	for product, ctor := range simpleToolConstructors() {
		t.Run(product, func(t *testing.T) {
			tool := ctor().(*simpleAWSRIPurchaseTool)
			resolveCalled := false
			tool.createProvider = func(_ string, _ *provider.ProviderConfig) (provider.Provider, error) {
				resolveCalled = true
				return nil, nil
			}
			args := validSimpleArgs()
			args.Confirm = boolPtr(true)

			_, resp, err := tool.handle(context.Background(), nil, args)
			require.NoError(t, err)
			assert.False(t, resolveCalled)
			assert.True(t, resp.DryRun)
		})
	}
}

func TestSimpleAWSRIPurchaseHandleRealPurchaseCallsCorrectService(t *testing.T) {
	t.Parallel()
	for product, ctor := range simpleToolConstructors() {
		t.Run(product, func(t *testing.T) {
			tool := ctor().(*simpleAWSRIPurchaseTool)
			fake := &fakeServiceClient{purchaseResult: common.PurchaseResult{Success: true, CommitmentID: "res-1"}}
			var gotService common.ServiceType
			tool.createProvider = func(_ string, _ *provider.ProviderConfig) (provider.Provider, error) {
				return &recordingProvider{
					fakeProvider: &fakeProvider{name: "aws"},
					client:       fake,
					gotService:   &gotService,
					gotRegion:    new(string),
				}, nil
			}
			args := validSimpleArgs()
			args.DryRun = boolPtr(false)
			args.Confirm = boolPtr(true)

			_, resp, err := tool.handle(context.Background(), nil, args)
			require.NoError(t, err)
			assert.True(t, resp.Success)
			assert.Equal(t, tool.spec.service, gotService)
			assert.Equal(t, common.PurchaseSourceMCP, fake.lastOpts.Source)
		})
	}
}
