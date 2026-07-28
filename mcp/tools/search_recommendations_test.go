package tools

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/provider"
)

// fakeRecommendationsClient is a minimal provider.RecommendationsClient test
// double; only GetRecommendations is exercised by search_recommendations.
type fakeRecommendationsClient struct {
	lastParams *common.RecommendationParams
	recs       []common.Recommendation
	err        error
	calls      int

	// allParams records the params of EVERY call, not just the last, so
	// fan-out tests can assert the exact set of (term, payment) combos a
	// search issued rather than only the combo that happened to run last.
	allParams []common.RecommendationParams

	// errOnCall, when non-zero, makes the 1-based call with that index fail
	// with errForCombo. Used to prove one failing combo fails the whole
	// search instead of silently returning the combos that succeeded.
	errOnCall   int
	errForCombo error
}

func (f *fakeRecommendationsClient) GetRecommendations(_ context.Context, params *common.RecommendationParams) ([]common.Recommendation, error) {
	f.calls++
	f.lastParams = params
	f.allParams = append(f.allParams, *params)
	if f.errOnCall != 0 && f.calls == f.errOnCall {
		return nil, f.errForCombo
	}
	return f.recs, f.err
}

// combos returns the (term, payment) pairs recorded across every call, in
// call order, for concise assertions in the fan-out tests.
func (f *fakeRecommendationsClient) combos() []searchCombo {
	got := make([]searchCombo, 0, len(f.allParams))
	for _, p := range f.allParams {
		got = append(got, searchCombo{term: p.Term, payment: p.PaymentOption})
	}
	return got
}
func (f *fakeRecommendationsClient) GetRecommendationsForService(_ context.Context, _ common.ServiceType) ([]common.Recommendation, error) {
	return f.recs, f.err
}
func (f *fakeRecommendationsClient) GetAllRecommendations(_ context.Context) ([]common.Recommendation, error) {
	return f.recs, f.err
}

var _ provider.RecommendationsClient = (*fakeRecommendationsClient)(nil)

// fakeProvider is a minimal provider.Provider test double.
type fakeProvider struct {
	name      string
	services  []common.ServiceType
	recClient provider.RecommendationsClient
	recErr    error
}

func (f *fakeProvider) Name() string        { return f.name }
func (f *fakeProvider) DisplayName() string { return f.name }
func (f *fakeProvider) IsConfigured() bool  { return true }
func (f *fakeProvider) GetCredentials() (provider.Credentials, error) {
	return nil, nil
}
func (f *fakeProvider) ValidateCredentials(_ context.Context) error { return nil }
func (f *fakeProvider) GetAccounts(_ context.Context) ([]common.Account, error) {
	return nil, nil
}
func (f *fakeProvider) GetRegions(_ context.Context) ([]common.Region, error) {
	return nil, nil
}
func (f *fakeProvider) GetDefaultRegion() string { return "us-east-1" }
func (f *fakeProvider) GetSupportedServices() []common.ServiceType {
	return f.services
}
func (f *fakeProvider) GetServiceClient(_ context.Context, _ common.ServiceType, _ string) (provider.ServiceClient, error) {
	return nil, nil
}
func (f *fakeProvider) GetRecommendationsClient(_ context.Context) (provider.RecommendationsClient, error) {
	return f.recClient, f.recErr
}

var _ provider.Provider = (*fakeProvider)(nil)

func newTestSearchTool(fp *fakeProvider) *searchRecommendationsTool {
	return &searchRecommendationsTool{
		createProvider: func(_ string, _ *provider.ProviderConfig) (provider.Provider, error) {
			return fp, nil
		},
	}
}

func TestSearchRecommendationsHappyPath(t *testing.T) {
	t.Parallel()
	recs := []common.Recommendation{{Provider: common.ProviderAWS, ResourceType: "m5.large", Count: 2}}
	client := &fakeRecommendationsClient{recs: recs}
	fp := &fakeProvider{name: "aws", services: []common.ServiceType{common.ServiceEC2}, recClient: client}
	tool := newTestSearchTool(fp)

	// term_years/payment_option are supplied so this stays a single-combo
	// search: the fan-out that omitting them triggers has its own dedicated
	// tests below, and pinning one combo here keeps this focused on "the
	// basic search works and forwards its params".
	_, result, err := tool.handle(context.Background(), nil, searchRecommendationsArgs{
		Provider:      "aws",
		Service:       "ec2",
		Region:        "us-east-1",
		TermYears:     1,
		PaymentOption: "all-upfront",
	})

	require.NoError(t, err)
	assert.Equal(t, 1, client.calls)
	assert.Equal(t, 1, result.Count)
	assert.Equal(t, recs, result.Recommendations)
	require.NotNil(t, client.lastParams)
	assert.Equal(t, common.ServiceEC2, client.lastParams.Service)
	assert.Equal(t, "us-east-1", client.lastParams.Region)
	assert.Equal(t, "1yr", client.lastParams.Term)
	assert.Equal(t, "all-upfront", client.lastParams.PaymentOption)
}

// TestSearchRecommendationsForwardsRegionFilters proves finding B of the
// CodeRabbit review: common.RecommendationParams has IncludeRegions and
// ExcludeRegions, but the tool neither accepted nor forwarded them, so a
// caller could not restrict a search to (or exclude) specific regions the
// way the CLI's config supports. include_regions/exclude_regions must reach
// the underlying RecommendationsClient call unchanged.
func TestSearchRecommendationsForwardsRegionFilters(t *testing.T) {
	t.Parallel()
	client := &fakeRecommendationsClient{}
	fp := &fakeProvider{name: "aws", services: []common.ServiceType{common.ServiceEC2}, recClient: client}
	tool := newTestSearchTool(fp)

	_, _, err := tool.handle(context.Background(), nil, searchRecommendationsArgs{
		Provider:       "aws",
		Service:        "ec2",
		IncludeRegions: []string{"us-east-1", "us-west-2"},
		ExcludeRegions: []string{"eu-west-1"},
	})

	require.NoError(t, err)
	require.NotNil(t, client.lastParams)
	assert.Equal(t, []string{"us-east-1", "us-west-2"}, client.lastParams.IncludeRegions)
	assert.Equal(t, []string{"eu-west-1"}, client.lastParams.ExcludeRegions)
}

// TestSearchRecommendationsTrimsRegionFilters is the regression guard for
// the CodeRabbit finding: region (and include_regions/exclude_regions/
// account_filter) were passed straight through to ProviderConfig.Region and
// RecommendationParams with no trim, unlike the purchase tools'
// requireNonBlank. " us-east-1 " must forward as "us-east-1" to both the
// provider config used to resolve the client and the params sent to the
// recommendations client -- region is optional here, so trimming (not
// rejecting) surrounding whitespace is the correct behavior.
func TestSearchRecommendationsTrimsRegionFilters(t *testing.T) {
	t.Parallel()
	client := &fakeRecommendationsClient{}
	fp := &fakeProvider{name: "aws", services: []common.ServiceType{common.ServiceEC2}, recClient: client}
	var gotCfg *provider.ProviderConfig
	tool := &searchRecommendationsTool{
		createProvider: func(_ string, cfg *provider.ProviderConfig) (provider.Provider, error) {
			gotCfg = cfg
			return fp, nil
		},
	}

	_, _, err := tool.handle(context.Background(), nil, searchRecommendationsArgs{
		Provider:       "aws",
		Service:        "ec2",
		Region:         " us-east-1 ",
		IncludeRegions: []string{" us-east-1 ", " us-west-2 "},
		ExcludeRegions: []string{" eu-west-1 "},
		AccountFilter:  []string{" 123456789012 "},
	})

	require.NoError(t, err)
	require.NotNil(t, gotCfg)
	assert.Equal(t, "us-east-1", gotCfg.Region, "ProviderConfig.Region must be trimmed")
	require.NotNil(t, client.lastParams)
	assert.Equal(t, "us-east-1", client.lastParams.Region, "RecommendationParams.Region must be trimmed")
	assert.Equal(t, []string{"us-east-1", "us-west-2"}, client.lastParams.IncludeRegions, "IncludeRegions entries must be trimmed")
	assert.Equal(t, []string{"eu-west-1"}, client.lastParams.ExcludeRegions, "ExcludeRegions entries must be trimmed")
	assert.Equal(t, []string{"123456789012"}, client.lastParams.AccountFilter, "AccountFilter entries must be trimmed")
}

// TestSearchRecommendationsTrimsCredentialOverrides covers the three
// credential override fields, which trimSearchArgsIdentifiers normalized
// every other identifier except. They select the account/subscription/
// project the search runs against, so a padded " my-profile " reached
// ProviderConfig raw and failed credential resolution for a reason the
// resulting error never mentions -- reading as "these credentials are
// broken" rather than "this name has a stray space". Asserted on
// ProviderConfig itself, since that is what actually authenticates.
func TestSearchRecommendationsTrimsCredentialOverrides(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		provider string
		service  common.ServiceType
		args     searchRecommendationsArgs
		got      func(cfg *provider.ProviderConfig) string
		want     string
	}{
		{
			name:     "aws profile",
			provider: "aws",
			service:  common.ServiceEC2,
			args:     searchRecommendationsArgs{Provider: "aws", Service: "ec2", AWSProfile: "  my-profile  "},
			got:      func(c *provider.ProviderConfig) string { return c.AWSProfile },
			want:     "my-profile",
		},
		{
			name:     "azure subscription id",
			provider: "azure",
			service:  common.ServiceCompute,
			args:     searchRecommendationsArgs{Provider: "azure", Service: "compute", AzureSubscriptionID: "  sub-x  "},
			got:      func(c *provider.ProviderConfig) string { return c.AzureSubscriptionID },
			want:     "sub-x",
		},
		{
			name:     "gcp project id",
			provider: "gcp",
			service:  common.ServiceCompute,
			args:     searchRecommendationsArgs{Provider: "gcp", Service: "compute", GCPProjectID: "  proj-a  "},
			got:      func(c *provider.ProviderConfig) string { return c.GCPProjectID },
			want:     "proj-a",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			client := &fakeRecommendationsClient{}
			fp := &fakeProvider{name: tc.provider, services: []common.ServiceType{tc.service}, recClient: client}
			var gotCfg *provider.ProviderConfig
			tool := &searchRecommendationsTool{
				createProvider: func(_ string, cfg *provider.ProviderConfig) (provider.Provider, error) {
					gotCfg = cfg
					return fp, nil
				},
			}

			_, _, err := tool.handle(context.Background(), nil, tc.args)
			require.NoError(t, err)
			require.NotNil(t, gotCfg)
			assert.Equal(t, tc.want, tc.got(gotCfg),
				"credential overrides must reach ProviderConfig trimmed, like every other identifier")
		})
	}
}

func TestSearchRecommendationsInvalidProvider(t *testing.T) {
	t.Parallel()
	tool := newTestSearchTool(&fakeProvider{})
	_, _, err := tool.handle(context.Background(), nil, searchRecommendationsArgs{
		Provider: "openstack",
		Service:  "ec2",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid provider")
}

func TestSearchRecommendationsUnsupportedService(t *testing.T) {
	t.Parallel()
	fp := &fakeProvider{name: "aws", services: []common.ServiceType{common.ServiceEC2, common.ServiceRDS}}
	tool := newTestSearchTool(fp)

	_, _, err := tool.handle(context.Background(), nil, searchRecommendationsArgs{
		Provider: "aws",
		Service:  "cosmosdb", // not an AWS service
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid service")
}

func TestSearchRecommendationsInvalidTermYears(t *testing.T) {
	t.Parallel()
	fp := &fakeProvider{name: "aws", services: []common.ServiceType{common.ServiceEC2}}
	tool := newTestSearchTool(fp)

	_, _, err := tool.handle(context.Background(), nil, searchRecommendationsArgs{
		Provider:  "aws",
		Service:   "ec2",
		TermYears: 2,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid term_years")
}

// TestSearchRecommendationsInvalidLookbackPeriod is the regression guard for
// the CodeRabbit finding: lookback_period was constrained only by the
// advertised MCP jsonschema enum (Register's BuildInputSchema override), not
// re-validated in the handler, so a direct MCP call bypassing schema
// enforcement could pass an unsupported value through to the provider.
func TestSearchRecommendationsInvalidLookbackPeriod(t *testing.T) {
	t.Parallel()
	fp := &fakeProvider{name: "aws", services: []common.ServiceType{common.ServiceEC2}}
	tool := newTestSearchTool(fp)

	_, _, err := tool.handle(context.Background(), nil, searchRecommendationsArgs{
		Provider:       "aws",
		Service:        "ec2",
		LookbackPeriod: "90d",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid lookback_period")
}

func TestSearchRecommendationsInvalidSPType(t *testing.T) {
	t.Parallel()
	fp := &fakeProvider{name: "aws", services: []common.ServiceType{common.ServiceSavingsPlansAll}}
	tool := newTestSearchTool(fp)

	_, _, err := tool.handle(context.Background(), nil, searchRecommendationsArgs{
		Provider:       "aws",
		Service:        string(common.ServiceSavingsPlansAll),
		IncludeSPTypes: []string{"NotARealType"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "include_sp_types")
}

func TestSearchRecommendationsProviderErrorSurfaced(t *testing.T) {
	t.Parallel()
	tool := &searchRecommendationsTool{
		createProvider: func(_ string, _ *provider.ProviderConfig) (provider.Provider, error) {
			return nil, errors.New("no AWS credentials found")
		},
	}
	_, _, err := tool.handle(context.Background(), nil, searchRecommendationsArgs{
		Provider: "aws",
		Service:  "ec2",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no AWS credentials found")
}

func TestSearchRecommendationsClientErrorSurfaced(t *testing.T) {
	t.Parallel()
	client := &fakeRecommendationsClient{err: errors.New("Cost Explorer API throttled")}
	fp := &fakeProvider{name: "aws", services: []common.ServiceType{common.ServiceEC2}, recClient: client}
	tool := newTestSearchTool(fp)

	_, _, err := tool.handle(context.Background(), nil, searchRecommendationsArgs{
		Provider: "aws",
		Service:  "ec2",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Cost Explorer API throttled")
}

// TestSearchRecommendationsSPDefaultsAppliedWhenOmitted is the regression
// guard for issue #1506: AWS's GetSavingsPlansPurchaseRecommendation
// requires term/payment_option/lookback_period on every call, unlike
// GetReservationPurchaseRecommendation (EC2/RDS/etc), which defaults them
// server-side. Before the fix, an SP search omitting these three fields
// forwarded them blank and only succeeds after AWS rejects the request (in
// production, that means a caller must retry with each field filled in, one
// at a time). This proves a single search call now builds the
// no-upfront/1yr/30d defaults and reaches the provider exactly once.
func TestSearchRecommendationsSPDefaultsAppliedWhenOmitted(t *testing.T) {
	t.Parallel()
	client := &fakeRecommendationsClient{}
	fp := &fakeProvider{name: "aws", services: []common.ServiceType{common.ServiceSavingsPlansAll}, recClient: client}
	tool := newTestSearchTool(fp)

	_, _, err := tool.handle(context.Background(), nil, searchRecommendationsArgs{
		Provider: "aws",
		Service:  string(common.ServiceSavingsPlansAll),
	})

	require.NoError(t, err)
	assert.Equal(t, 1, client.calls, "must reach the provider exactly once")
	require.NotNil(t, client.lastParams)
	assert.Equal(t, "1yr", client.lastParams.Term)
	assert.Equal(t, "no-upfront", client.lastParams.PaymentOption)
	assert.Equal(t, "30d", client.lastParams.LookbackPeriod)
}

// TestSearchRecommendationsSPExplicitTermYearsPreserved proves the SP
// defaults never override an explicitly supplied value: term_years=3 must
// reach the provider as "3yr", not the 1yr default.
func TestSearchRecommendationsSPExplicitTermYearsPreserved(t *testing.T) {
	t.Parallel()
	client := &fakeRecommendationsClient{}
	fp := &fakeProvider{name: "aws", services: []common.ServiceType{common.ServiceSavingsPlansAll}, recClient: client}
	tool := newTestSearchTool(fp)

	_, _, err := tool.handle(context.Background(), nil, searchRecommendationsArgs{
		Provider:  "aws",
		Service:   string(common.ServiceSavingsPlansAll),
		TermYears: 3,
	})

	require.NoError(t, err)
	require.NotNil(t, client.lastParams)
	assert.Equal(t, "3yr", client.lastParams.Term)
	assert.Equal(t, "no-upfront", client.lastParams.PaymentOption, "payment_option still defaults when omitted")
	assert.Equal(t, "30d", client.lastParams.LookbackPeriod, "lookback_period still defaults when omitted")
}

// TestSearchRecommendationsEC2NoDefaultsInjected proves the Savings Plans
// defaults are scoped to AWS Savings Plans searches only: an EC2
// (reservation) search omitting term/payment/lookback must never be narrowed
// to the SP trio of 1yr / no-upfront / 30d.
//
// lookback_period is the field that must still arrive BLANK on every call:
// unlike term and payment option (which the fan-out now supplies concretely,
// see TestSearchRecommendationsFansOutAllCombos...), Cost Explorer applies
// its own server-side default for the lookback window, and injecting 30d
// here would silently change the usage evidence behind every reservation
// recommendation.
func TestSearchRecommendationsEC2NoDefaultsInjected(t *testing.T) {
	t.Parallel()
	client := &fakeRecommendationsClient{}
	fp := &fakeProvider{name: "aws", services: []common.ServiceType{common.ServiceEC2}, recClient: client}
	tool := newTestSearchTool(fp)

	_, _, err := tool.handle(context.Background(), nil, searchRecommendationsArgs{
		Provider: "aws",
		Service:  "ec2",
	})

	require.NoError(t, err)
	require.NotEmpty(t, client.allParams)
	for i, p := range client.allParams {
		assert.Emptyf(t, p.LookbackPeriod, "call %d must leave lookback_period to AWS's own default", i)
	}
	// The SP defaulting combo (1yr + no-upfront) is only ever reached here as
	// one cell of the full reservation fan-out, never as an injected default
	// that collapses the search to a single narrowed result.
	assert.Greater(t, client.calls, 1, "an EC2 search must not collapse to one narrowed combo")
}

// TestSearchRecommendationsMultipleInvalidFieldsJoinedInOneError is the
// validate-all-at-once safety net (issue #1506 change 2): a request with
// several invalid fields must name all of them in one returned error instead
// of surfacing only the first one found.
func TestSearchRecommendationsMultipleInvalidFieldsJoinedInOneError(t *testing.T) {
	t.Parallel()
	fp := &fakeProvider{name: "aws", services: []common.ServiceType{common.ServiceEC2}}
	tool := newTestSearchTool(fp)

	_, _, err := tool.handle(context.Background(), nil, searchRecommendationsArgs{
		Provider:       "aws",
		Service:        "ec2",
		PaymentOption:  "not-a-real-option",
		LookbackPeriod: "90d",
		IncludeSPTypes: []string{"NotARealType"},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid payment_option")
	assert.Contains(t, err.Error(), "invalid lookback_period")
	assert.Contains(t, err.Error(), "include_sp_types")
}

// TestSearchBlankFilterListEntriesRejected is the regression guard for the
// silent empty-result path found in review. A blank entry cannot match any
// real region code or account ID, but a non-empty list still switches the
// corresponding filter ON: include_regions=["  "] activated region
// filtering with a set matching nothing and returned zero recommendations,
// which reads as "your account has nothing worth buying" rather than "your
// filter was malformed".
func TestSearchBlankFilterListEntriesRejected(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		mutate  func(*searchRecommendationsArgs)
		wantSub string
	}{
		{"blank include_regions entry", func(a *searchRecommendationsArgs) {
			a.IncludeRegions = []string{"  "}
		}, "include_regions[0] is blank"},
		{"blank exclude_regions entry among valid ones", func(a *searchRecommendationsArgs) {
			a.ExcludeRegions = []string{"us-east-1", ""}
		}, "exclude_regions[1] is blank"},
		{"blank account_filter entry", func(a *searchRecommendationsArgs) {
			a.AccountFilter = []string{"111111111111", "\t"}
		}, "account_filter[1] is blank"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			args := searchRecommendationsArgs{Provider: string(common.ProviderAWS), Service: "ec2"}
			tc.mutate(&args)

			tool := &searchRecommendationsTool{
				createProvider: func(_ string, _ *provider.ProviderConfig) (provider.Provider, error) {
					t.Fatal("provider must not be created for malformed filter input")
					return nil, nil
				},
			}
			_, _, err := tool.handle(context.Background(), nil, args)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantSub)
		})
	}
}

// TestSearchAcceptsWhitespacePaddedService pins that service is trimmed
// before it is matched, like every other identifier field. Untrimmed, a
// " savingsplans-compute" both missed the AWS Savings Plans defaulting
// branch (isAWSSavingsPlansSearch) and failed the supported-service check
// for a reason the error text would not have explained.
func TestSearchAcceptsWhitespacePaddedService(t *testing.T) {
	t.Parallel()
	args := searchRecommendationsArgs{Provider: string(common.ProviderAWS), Service: "  ec2  "}

	recClient := &fakeRecommendationsClient{}
	tool := &searchRecommendationsTool{
		createProvider: func(_ string, _ *provider.ProviderConfig) (provider.Provider, error) {
			return &fakeProvider{
				name:      "aws",
				services:  []common.ServiceType{common.ServiceEC2},
				recClient: recClient,
			}, nil
		},
	}

	_, _, err := tool.handle(context.Background(), nil, args)
	require.NoError(t, err)
	require.NotNil(t, recClient.lastParams)
	assert.Equal(t, common.ServiceEC2, recClient.lastParams.Service,
		"service must be trimmed before it reaches the provider")
}
