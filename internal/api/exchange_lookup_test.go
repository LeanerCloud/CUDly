package api

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// failingRoundTripper is an http.RoundTripper that fails every request
// with a fixed error. Used to inject an STS GetCallerIdentity failure
// into a Handler's pre-seeded aws.Config without dialing the network
// or relying on real AWS credentials.
type failingRoundTripper struct{ err error }

func (f failingRoundTripper) RoundTrip(_ *http.Request) (*http.Response, error) {
	return nil, f.err
}

// fakeRecsLister captures the filter passed to ListStoredRecommendations
// so tests can assert region / account / provider scoping landed in the
// SQL query. Returns a configurable result set or error.
type fakeRecsLister struct {
	gotFilter config.RecommendationFilter
	calls     int
	out       []config.RecommendationRecord
	err       error
}

func (f *fakeRecsLister) ListStoredRecommendations(_ context.Context, filter config.RecommendationFilter) ([]config.RecommendationRecord, error) {
	f.calls++
	f.gotFilter = filter
	return f.out, f.err
}

// TestPurchaseRecLookupFromStore_RegionFilter pins that the closure
// pushes the requested region into the SQL filter so Postgres prunes
// rows by region rather than the Go layer doing it after the fact.
func TestPurchaseRecLookupFromStore_RegionFilter(t *testing.T) {
	t.Parallel()
	store := &fakeRecsLister{}
	lookup := purchaseRecLookupFromStore(store, "")
	_, err := lookup(context.Background(), "eu-west-1", "USD")
	require.NoError(t, err)
	assert.Equal(t, 1, store.calls, "lookup must invoke the store exactly once")
	assert.Equal(t, "aws", store.gotFilter.Provider, "must scope to AWS recs")
	assert.Equal(t, "ec2", store.gotFilter.Service, "must scope to EC2 recs (no RDS / opensearch leakage)")
	assert.Equal(t, "eu-west-1", store.gotFilter.Region, "region must thread through to SQL")
}

// TestPurchaseRecLookupFromStore_AccountFilter pins the cross-account
// leak guard: when an account UUID is supplied, the filter restricts
// the query to that single account so the reshape page can't surface
// another tenant's recommendations.
func TestPurchaseRecLookupFromStore_AccountFilter(t *testing.T) {
	t.Parallel()
	store := &fakeRecsLister{}
	lookup := purchaseRecLookupFromStore(store, "acct-uuid-123")
	_, err := lookup(context.Background(), "us-east-1", "USD")
	require.NoError(t, err)
	require.Len(t, store.gotFilter.AccountIDs, 1, "non-empty account UUID must populate AccountIDs filter")
	assert.Equal(t, "acct-uuid-123", store.gotFilter.AccountIDs[0])
}

// TestPurchaseRecLookupFromStore_NoAccountFilterWhenEmpty pins the
// degraded-mode contract: when the caller can't resolve an account
// UUID (ambient credentials, account not registered yet), the lookup
// returns whatever recs exist in the region rather than blanking the
// page. The operator can register the account later to engage scoping.
func TestPurchaseRecLookupFromStore_NoAccountFilterWhenEmpty(t *testing.T) {
	t.Parallel()
	store := &fakeRecsLister{}
	lookup := purchaseRecLookupFromStore(store, "")
	_, err := lookup(context.Background(), "us-east-1", "USD")
	require.NoError(t, err)
	assert.Empty(t, store.gotFilter.AccountIDs, "empty account UUID must NOT add an AccountIDs filter")
}

// TestPurchaseRecLookupFromStore_NoRecsReturnsEmpty pins the
// cold-cache contract: zero recs in the region → empty slice (not
// nil-error). The downstream AnalyzeReshapingWithRecs treats an empty
// slice the same as "no alternatives, primary target intact".
func TestPurchaseRecLookupFromStore_NoRecsReturnsEmpty(t *testing.T) {
	t.Parallel()
	store := &fakeRecsLister{out: nil}
	lookup := purchaseRecLookupFromStore(store, "")
	got, err := lookup(context.Background(), "us-east-1", "USD")
	require.NoError(t, err)
	assert.Empty(t, got, "empty recs → empty offerings, no error")
}

// TestPurchaseRecLookupFromStore_StoreErrorPropagates pins the error
// path: an underlying SQL failure surfaces back to the caller.
// AnalyzeReshapingWithRecs handles the error by falling back to base
// recs (graceful degradation), so the closure just needs to forward
// the error verbatim.
func TestPurchaseRecLookupFromStore_StoreErrorPropagates(t *testing.T) {
	t.Parallel()
	store := &fakeRecsLister{err: errors.New("postgres timeout")}
	lookup := purchaseRecLookupFromStore(store, "")
	_, err := lookup(context.Background(), "us-east-1", "USD")
	require.Error(t, err, "store errors must propagate so the caller can fall back")
}

// TestPurchaseRecLookupFromStore_MapsFields pins the
// RecommendationRecord → OfferingOption mapping shape so the dollar-
// units pre-filter and the UI both see consistent data:
//   - InstanceType comes from ResourceType.
//   - OfferingID = rec.ID (stable handle).
//   - EffectiveMonthlyCost = UpfrontCost / (Term * 12) + MonthlyCost.
//   - NormalizationFactor resolved from the size (here "large" → 4).
//   - CurrencyCode propagated from the lookup's currencyCode arg.
func TestPurchaseRecLookupFromStore_MapsFields(t *testing.T) {
	t.Parallel()
	store := &fakeRecsLister{
		out: []config.RecommendationRecord{
			{
				ID:           "rec-1",
				Provider:     "aws",
				Service:      "ec2",
				Region:       "us-east-1",
				ResourceType: "m6i.large",
				Term:         1,               // 1 year term
				UpfrontCost:  120,             // 120 / 12 = 10/mo amortized
				MonthlyCost:  aws.Float64(20), // + 20/mo recurring = 30
			},
			{
				// Term=0 → no upfront amortization; effective = MonthlyCost only.
				ID:           "rec-2",
				Provider:     "aws",
				Service:      "ec2",
				Region:       "us-east-1",
				ResourceType: "c5.xlarge",
				Term:         0,
				UpfrontCost:  500, // ignored when Term == 0
				MonthlyCost:  aws.Float64(50),
			},
		},
	}
	lookup := purchaseRecLookupFromStore(store, "")
	got, err := lookup(context.Background(), "us-east-1", "EUR")
	require.NoError(t, err)
	require.Len(t, got, 2)

	assert.Equal(t, "m6i.large", got[0].InstanceType)
	assert.Equal(t, "rec-1", got[0].OfferingID)
	assert.InDelta(t, 30.0, got[0].EffectiveMonthlyCost, 0.001,
		"upfront 120 over 12 months = 10/mo + recurring 20/mo = 30/mo")
	assert.InDelta(t, 4.0, got[0].NormalizationFactor, 0.001, "large → NF 4")
	assert.Equal(t, "EUR", got[0].CurrencyCode, "currency must be propagated from caller")

	assert.Equal(t, "c5.xlarge", got[1].InstanceType)
	assert.InDelta(t, 50.0, got[1].EffectiveMonthlyCost, 0.001,
		"Term==0 means upfront cannot be amortized; fall back to MonthlyCost")
	assert.InDelta(t, 8.0, got[1].NormalizationFactor, 0.001, "xlarge → NF 8")

	// Term plumbing: 1y rec → 31_536_000 seconds (AWS canonical RI
	// duration); Term==0 → TermSeconds==0 (the reshape term-match guard
	// then falls back to "skip the gate" rather than blocking the rec).
	assert.Equal(t, int64(365*24*60*60), got[0].TermSeconds,
		"1-year rec must serialize to 31_536_000s for the term-match guard")
	assert.Equal(t, int64(0), got[1].TermSeconds,
		"Term==0 rec must not synthesize a fake duration — TermSeconds stays zero")
}

// TestPurchaseRecLookupFromStore_ThreeYearTerm pins the multi-year
// path: rec.Term=3 must serialize to exactly 3 × 31_536_000s so the
// reshape term-match guard treats it as 3y rather than rounding it
// onto a 1y surface.
func TestPurchaseRecLookupFromStore_ThreeYearTerm(t *testing.T) {
	t.Parallel()
	store := &fakeRecsLister{
		out: []config.RecommendationRecord{
			{
				ID: "rec-3y", Provider: "aws", Service: "ec2", Region: "us-east-1",
				ResourceType: "m5.large", Term: 3, MonthlyCost: aws.Float64(10),
			},
		},
	}
	lookup := purchaseRecLookupFromStore(store, "")
	got, err := lookup(context.Background(), "us-east-1", "USD")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, int64(3*365*24*60*60), got[0].TermSeconds,
		"3-year rec must serialize to 3 × 31_536_000s for the term-match guard")
}

// TestPurchaseRecLookupFromStore_NilMonthlyCost pins the nil-path: when
// MonthlyCost is nil (provider didn't expose a monthly breakdown) the lookup
// must not panic and must compute EffectiveMonthlyCost from amortized upfront
// alone.
func TestPurchaseRecLookupFromStore_NilMonthlyCost(t *testing.T) {
	t.Parallel()
	store := &fakeRecsLister{
		out: []config.RecommendationRecord{
			{
				ID:           "rec-nil-monthly",
				Provider:     "aws",
				Service:      "ec2",
				Region:       "us-east-1",
				ResourceType: "m5.large",
				Term:         1, // 1 year: 120 / 12 = 10/mo amortized
				UpfrontCost:  120,
				MonthlyCost:  nil, // provider API didn't return monthly breakdown
			},
		},
	}
	lookup := purchaseRecLookupFromStore(store, "")
	got, err := lookup(context.Background(), "us-east-1", "USD")
	require.NoError(t, err)
	require.Len(t, got, 1)
	// No recurring monthly charge (nil → 0), so effective cost = amortized upfront only.
	assert.InDelta(t, 10.0, got[0].EffectiveMonthlyCost, 0.001,
		"nil MonthlyCost + 120 upfront / 12mo = 10/mo effective")
}

// TestResolveAWSCloudAccountID_FailsClosedOnSTSError pins the
// multi-tenant safety contract introduced by commit f44b06567: when
// STS GetCallerIdentity fails (transient AWS error, denied IAM perm,
// expired token), resolveAWSCloudAccountID MUST propagate the error
// rather than returning ("", nil) — the latter would have
// purchaseRecLookupFromStore skip the AccountIDs filter and serve up
// another tenant's recs to the caller.
//
// Mock injection strategy: pre-seal h.awsCfg with an aws.Config whose
// HTTPClient fails every request. CUDLY_SOURCE_CLOUD=aws forces
// resolveAWSCallerIdentity past its non-AWS short-circuit, so STS
// GetCallerIdentity is the next (and only) HTTP call — guaranteed
// to fail via the transport stub. Static credentials + an explicit
// region are required because the SDK signs the request before
// invoking the transport; without them the SDK errors with
// MissingRegion / NoCredentialProviders before the failing transport
// runs and the test would pass for the wrong reason.
//
// Asserts BOTH the error wraps the production fix's identifying
// prefix ("resolve source aws account for reshape scope") AND that
// ListCloudAccounts was never invoked — proving the fail-closed
// branch aborted the resolver before the DB read. Reverting the
// f44b06567 fix (changing the STS-error branch back to `return "",
// nil`) makes this test fail loudly.
func TestResolveAWSCloudAccountID_FailsClosedOnSTSError(t *testing.T) {
	// Force resolveAWSCallerIdentity past its sourceCloud()!="aws"
	// short-circuit so STS is actually invoked.
	t.Setenv("CUDLY_SOURCE_CLOUD", "aws")

	// MockConfigStore with a ListCloudAccountsFn sentinel: if the
	// resolver ever reaches the DB read, the flag flips to true and
	// the test fails. The fail-closed branch must abort BEFORE this
	// point.
	var listCloudAccountsCalled bool
	mockStore := &MockConfigStore{
		ListCloudAccountsFn: func(_ context.Context, _ config.CloudAccountFilter) ([]config.CloudAccount, error) {
			listCloudAccountsCalled = true
			return nil, nil
		},
	}

	stsErr := errors.New("simulated sts get-caller-identity failure")
	h := &Handler{config: mockStore}
	// Pre-seal awsCfgOnce so resolveAWSCallerIdentity skips
	// LoadDefaultConfig and uses our injected config directly.
	h.awsCfgOnce.Do(func() {
		h.awsCfg = aws.Config{
			Region: "us-east-1",
			// Static dummy creds so the SDK's signer doesn't probe
			// IMDS / shared-config files before sending the (doomed)
			// HTTP request through the failing transport.
			Credentials: credentials.NewStaticCredentialsProvider("AKIA-test", "test-secret", ""),
			HTTPClient:  &http.Client{Transport: failingRoundTripper{err: stsErr}},
		}
	})

	id, err := h.resolveAWSCloudAccountID(context.Background())
	require.Error(t, err, "STS failure must propagate — fail-closed contract for multi-tenant scope filter")
	assert.Empty(t, id, "fail-closed sentinel: empty account ID with non-nil error")
	assert.Contains(t, err.Error(), "resolve source aws account for reshape scope",
		"error must wrap the resolver's production prefix so the cause stays attributable")
	assert.False(t, listCloudAccountsCalled,
		"ListCloudAccounts must NOT be invoked after an STS error — the resolver must short-circuit "+
			"before the DB read or the unscoped query path could leak another tenant's recs")
}

// TestSplitInstanceType pins the local instance-type parser used by
// the mapping helper. Mirrors the pkg/exchange parser to avoid
// exporting a general-purpose helper this package doesn't need.
func TestSplitInstanceType(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in         string
		wantFamily string
		wantSize   string
	}{
		{"m5.large", "m5", "large"},
		{"m7g.metal", "m7g", "metal"},
		{"r6i.16xlarge", "r6i", "16xlarge"},
		{"", "", ""},
		{"malformed", "", ""},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			t.Parallel()
			f, s := splitInstanceType(c.in)
			assert.Equal(t, c.wantFamily, f)
			assert.Equal(t, c.wantSize, s)
		})
	}
}
