package azure

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/provider"
)

// fakeRecommendationsClient implements provider.RecommendationsClient for
// fan-out tests, letting each fake subscription's response be controlled
// independently of the others.
type fakeRecommendationsClient struct {
	recs []common.Recommendation
	err  error
	// ignoreCtx makes the fake return its canned response even for a
	// cancelled context, modelling a per-subscription client that fails to
	// observe cancellation (an SDK layer that answers from its own cache, or
	// a call that already completed before the parent context died). This is
	// the only shape in which the fan-out's own post-Wait ctx.Err() check is
	// load-bearing: a fake that returns ctx.Err() itself makes every
	// subscription fail, and the all-subscriptions-failed guard produces a
	// context.Canceled error whether or not that check exists.
	ignoreCtx bool
	gotParams *common.RecommendationParams
	calls     atomic.Int64
}

func (f *fakeRecommendationsClient) GetRecommendations(ctx context.Context, params *common.RecommendationParams) ([]common.Recommendation, error) {
	f.calls.Add(1)
	f.gotParams = params
	if err := ctx.Err(); err != nil && !f.ignoreCtx {
		// Respect cancellation like a real ARM client would (the SDK's
		// underlying HTTP transport checks ctx before issuing the request).
		return nil, err
	}
	return f.recs, f.err
}

func (f *fakeRecommendationsClient) GetRecommendationsForService(ctx context.Context, service common.ServiceType) ([]common.Recommendation, error) {
	return f.GetRecommendations(ctx, &common.RecommendationParams{Service: service})
}

func (f *fakeRecommendationsClient) GetAllRecommendations(ctx context.Context) ([]common.Recommendation, error) {
	return f.GetRecommendations(ctx, &common.RecommendationParams{})
}

// withFakeSubscriptionClients overrides newSubscriptionRecommendationsClientFn
// to hand out the given fakes in order (one per NewMultiSubscriptionRecommendationsClient
// account, in the order accounts are passed) and restores the original on
// cleanup.
func withFakeSubscriptionClients(t *testing.T, fakes map[string]*fakeRecommendationsClient) {
	t.Helper()
	orig := newSubscriptionRecommendationsClientFn
	t.Cleanup(func() { newSubscriptionRecommendationsClientFn = orig })
	newSubscriptionRecommendationsClientFn = func(_ azcore.TokenCredential, subscriptionID string) (provider.RecommendationsClient, error) {
		fake, ok := fakes[subscriptionID]
		if !ok {
			return nil, errors.New("unexpected subscriptionID: " + subscriptionID)
		}
		return fake, nil
	}
}

func twoTestAccounts() []common.Account {
	return []common.Account{
		{Provider: common.ProviderAzure, ID: "sub-1", Name: "Subscription 1"},
		{Provider: common.ProviderAzure, ID: "sub-2", Name: "Subscription 2"},
	}
}

func TestNewMultiSubscriptionRecommendationsClient_EmptyAccounts(t *testing.T) {
	client, err := NewMultiSubscriptionRecommendationsClient(&mockAzureTokenCredential{}, nil)
	require.Error(t, err)
	assert.Nil(t, client)
	assert.Contains(t, err.Error(), "at least one subscription is required")
}

func TestNewMultiSubscriptionRecommendationsClient_BuildsClientsPerAccount(t *testing.T) {
	accounts := twoTestAccounts()
	fake1 := &fakeRecommendationsClient{recs: []common.Recommendation{{Account: "sub-1"}}}
	fake2 := &fakeRecommendationsClient{recs: []common.Recommendation{{Account: "sub-2"}}}
	withFakeSubscriptionClients(t, map[string]*fakeRecommendationsClient{
		"sub-1": fake1,
		"sub-2": fake2,
	})

	client, err := NewMultiSubscriptionRecommendationsClient(&mockAzureTokenCredential{}, accounts)
	require.NoError(t, err)
	require.Len(t, client.subscriptions, 2)
	assert.Equal(t, "sub-1", client.subscriptions[0].subscriptionID)
	assert.Equal(t, "sub-2", client.subscriptions[1].subscriptionID)
	// Assert the built client is actually stored against its subscription --
	// without this the test passes even if the client field is never set.
	assert.Same(t, fake1, client.subscriptions[0].client)
	assert.Same(t, fake2, client.subscriptions[1].client)
}

func TestNewMultiSubscriptionRecommendationsClient_ClientConstructionFailurePropagates(t *testing.T) {
	orig := newSubscriptionRecommendationsClientFn
	t.Cleanup(func() { newSubscriptionRecommendationsClientFn = orig })
	newSubscriptionRecommendationsClientFn = func(_ azcore.TokenCredential, subscriptionID string) (provider.RecommendationsClient, error) {
		return nil, errors.New("boom")
	}

	client, err := NewMultiSubscriptionRecommendationsClient(&mockAzureTokenCredential{}, twoTestAccounts())
	require.Error(t, err)
	assert.Nil(t, client)
	assert.Contains(t, err.Error(), "failed to build client for subscription")
}

func TestMultiSubscriptionRecommendationsClient_GetRecommendations_MergesAcrossSubscriptions(t *testing.T) {
	accounts := twoTestAccounts()
	withFakeSubscriptionClients(t, map[string]*fakeRecommendationsClient{
		"sub-1": {recs: []common.Recommendation{{Account: "sub-1", Service: common.ServiceCompute}}},
		"sub-2": {recs: []common.Recommendation{{Account: "sub-2", Service: common.ServiceCache}}},
	})

	client, err := NewMultiSubscriptionRecommendationsClient(&mockAzureTokenCredential{}, accounts)
	require.NoError(t, err)

	recs, err := client.GetAllRecommendations(context.Background())
	require.NoError(t, err)
	assert.ElementsMatch(t, []common.Recommendation{
		{Account: "sub-1", Service: common.ServiceCompute},
		{Account: "sub-2", Service: common.ServiceCache},
	}, recs)
}

// One subscription failing must not discard the others' results. It does,
// however, produce a PartialSubscriptionFailureError so the caller knows the
// sweep was incomplete -- see
// TestMultiSubscriptionRecommendationsClient_PartialFailureIsDistinguishable.
func TestMultiSubscriptionRecommendationsClient_GetRecommendations_PartialFailureStillReturnsData(t *testing.T) {
	accounts := twoTestAccounts()
	withFakeSubscriptionClients(t, map[string]*fakeRecommendationsClient{
		"sub-1": {err: errors.New("sub-1 unreachable")},
		"sub-2": {recs: []common.Recommendation{{Account: "sub-2", Service: common.ServiceCache}}},
	})

	client, err := NewMultiSubscriptionRecommendationsClient(&mockAzureTokenCredential{}, accounts)
	require.NoError(t, err)

	recs, err := client.GetAllRecommendations(context.Background())
	var partial *PartialSubscriptionFailureError
	require.ErrorAs(t, err, &partial, "a partial sweep must be reported as partial, not as success")
	assert.Equal(t, []common.Recommendation{{Account: "sub-2", Service: common.ServiceCache}}, recs,
		"one subscription failing must not discard the others' recommendations")
}

// TestMultiSubscriptionRecommendationsClient_PartialFailureIsDistinguishable
// is the regression test for silent partial success.
//
// The two scenarios below produce IDENTICAL recommendation slices. The only
// thing that can tell them apart is the error: a sweep where a subscription
// was never successfully queried must not look like a complete sweep that
// happened to find nothing there. Persisting the first as if it were the
// second turns a failed collection into an apparently shrinking savings
// opportunity.
func TestMultiSubscriptionRecommendationsClient_PartialFailureIsDistinguishable(t *testing.T) {
	newClient := func(t *testing.T, fakes map[string]*fakeRecommendationsClient) *MultiSubscriptionRecommendationsClient {
		t.Helper()
		withFakeSubscriptionClients(t, fakes)
		c, err := NewMultiSubscriptionRecommendationsClient(&mockAzureTokenCredential{}, twoTestAccounts())
		require.NoError(t, err)
		return c
	}

	// Scenario A: sub-1 could not be queried, sub-2 returned nothing.
	t.Run("partial failure reports which subscriptions were not queried", func(t *testing.T) {
		boom := errors.New("sub-1 unreachable")
		client := newClient(t, map[string]*fakeRecommendationsClient{
			"sub-1": {err: boom},
			"sub-2": {recs: nil},
		})

		recs, err := client.GetAllRecommendations(context.Background())
		require.Error(t, err, "an incomplete sweep must not be reported as a complete one")

		var partial *PartialSubscriptionFailureError
		require.ErrorAs(t, err, &partial, "the error must be inspectable, not just a log line")
		assert.Equal(t, 2, partial.Attempted)
		assert.Equal(t, 1, partial.Succeeded)
		require.Len(t, partial.Failed, 1)
		assert.Equal(t, "sub-1", partial.Failed[0].SubscriptionID)
		assert.ErrorIs(t, err, boom, "the underlying cause must remain matchable")

		// The successful subscription's data is still returned, so a caller
		// that inspects the error can keep it.
		assert.Empty(t, recs)
	})

	// Scenario B: both subscriptions answered, neither had recommendations.
	t.Run("complete sweep finding nothing is not an error", func(t *testing.T) {
		client := newClient(t, map[string]*fakeRecommendationsClient{
			"sub-1": {recs: nil},
			"sub-2": {recs: nil},
		})

		recs, err := client.GetAllRecommendations(context.Background())
		require.NoError(t, err, "a complete sweep that found nothing must not look like a failure")
		assert.Empty(t, recs)
	})
}

// A partial failure must still hand back the successful subscriptions' data,
// so a caller that inspects the error loses nothing.
func TestMultiSubscriptionRecommendationsClient_PartialFailureKeepsSuccessfulResults(t *testing.T) {
	withFakeSubscriptionClients(t, map[string]*fakeRecommendationsClient{
		"sub-1": {err: errors.New("sub-1 unreachable")},
		"sub-2": {recs: []common.Recommendation{{Account: "sub-2", Service: common.ServiceCache}}},
	})

	client, err := NewMultiSubscriptionRecommendationsClient(&mockAzureTokenCredential{}, twoTestAccounts())
	require.NoError(t, err)

	recs, err := client.GetAllRecommendations(context.Background())

	var partial *PartialSubscriptionFailureError
	require.ErrorAs(t, err, &partial)
	assert.Equal(t, 1, partial.Succeeded)
	assert.Equal(t, []common.Recommendation{{Account: "sub-2", Service: common.ServiceCache}}, recs,
		"the subscriptions that succeeded must still be returned alongside the partial-failure error")
}

// PartialSubscriptionFailureError and its fields are exported, so a
// zero-value or externally-constructed instance is reachable. Formatting one
// must not panic on Failed[0] -- an error type that crashes when logged fails
// at exactly the moment the operator needs its message.
func TestPartialSubscriptionFailureError_ErrorWithNoFailures(t *testing.T) {
	var zero PartialSubscriptionFailureError

	require.NotPanics(t, func() { _ = fmt.Sprintf("%v", &zero) },
		"formatting a zero-value PartialSubscriptionFailureError must not panic")
	assert.Contains(t, zero.Error(), "no failures recorded")
	assert.Empty(t, zero.FailedSubscriptionIDs())
	assert.Empty(t, zero.Unwrap())
}

func TestMultiSubscriptionRecommendationsClient_GetRecommendations_AllFail(t *testing.T) {
	accounts := twoTestAccounts()
	withFakeSubscriptionClients(t, map[string]*fakeRecommendationsClient{
		"sub-1": {err: errors.New("sub-1 unreachable")},
		"sub-2": {err: errors.New("sub-2 unreachable")},
	})

	client, err := NewMultiSubscriptionRecommendationsClient(&mockAzureTokenCredential{}, accounts)
	require.NoError(t, err)

	recs, err := client.GetAllRecommendations(context.Background())
	require.Error(t, err)
	assert.Nil(t, recs)
	assert.Contains(t, err.Error(), "all 2 Azure subscriptions failed")
}

func TestMultiSubscriptionRecommendationsClient_GetRecommendations_NilParams(t *testing.T) {
	client := &MultiSubscriptionRecommendationsClient{}
	recs, err := client.GetRecommendations(context.Background(), nil)
	require.EqualError(t, err, "params cannot be nil")
	assert.Nil(t, recs)
}

// TestMultiSubscriptionRecommendationsClient_GetRecommendations_PropagatesContextCancellation
// guards the post-Wait ctx.Err() check specifically.
//
// The fakes here deliberately IGNORE the cancelled context and return
// results, so the only thing that can turn this call into an error is the
// fan-out's own ctx.Err() check. Deleting that check makes this test fail
// with a nil error and two merged recommendations -- which is exactly the
// bug it exists to catch: a cancelled request quietly yielding data as if it
// had completed normally.
func TestMultiSubscriptionRecommendationsClient_GetRecommendations_PropagatesContextCancellation(t *testing.T) {
	accounts := twoTestAccounts()
	withFakeSubscriptionClients(t, map[string]*fakeRecommendationsClient{
		"sub-1": {recs: []common.Recommendation{{Account: "sub-1"}}, ignoreCtx: true},
		"sub-2": {recs: []common.Recommendation{{Account: "sub-2"}}, ignoreCtx: true},
	})

	client, err := NewMultiSubscriptionRecommendationsClient(&mockAzureTokenCredential{}, accounts)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	recs, err := client.GetAllRecommendations(ctx)
	require.Error(t, err, "expected context.Canceled to propagate from GetRecommendations")
	assert.ErrorIs(t, err, context.Canceled)
	assert.Nil(t, recs)
}

// TestMultiSubscriptionRecommendationsClient_GetRecommendations_AccountFilterScopesFanOut
// is the regression test for the cross-subscription bleed: a caller that
// scopes a request to one subscription must not receive another
// subscription's recommendations, and the unasked-for subscription must not
// even be queried.
func TestMultiSubscriptionRecommendationsClient_GetRecommendations_AccountFilterScopesFanOut(t *testing.T) {
	accounts := twoTestAccounts()
	fake1 := &fakeRecommendationsClient{recs: []common.Recommendation{{Account: "sub-1", Service: common.ServiceCompute}}}
	fake2 := &fakeRecommendationsClient{recs: []common.Recommendation{{Account: "sub-2", Service: common.ServiceCache}}}
	withFakeSubscriptionClients(t, map[string]*fakeRecommendationsClient{
		"sub-1": fake1,
		"sub-2": fake2,
	})

	client, err := NewMultiSubscriptionRecommendationsClient(&mockAzureTokenCredential{}, accounts)
	require.NoError(t, err)

	recs, err := client.GetRecommendations(context.Background(), &common.RecommendationParams{
		AccountFilter: []string{"sub-1"},
	})
	require.NoError(t, err)
	assert.Equal(t, []common.Recommendation{{Account: "sub-1", Service: common.ServiceCompute}}, recs,
		"only the filtered subscription's recommendations may be returned")
	assert.Equal(t, int64(1), fake1.calls.Load(), "the filtered-in subscription must be queried")
	assert.Equal(t, int64(0), fake2.calls.Load(), "a subscription outside the filter must not be queried at all")
}

// An AccountFilter naming no accessible subscription must error rather than
// return an empty slice: an empty result is indistinguishable from "these
// subscriptions have no savings available".
func TestMultiSubscriptionRecommendationsClient_GetRecommendations_AccountFilterMatchesNothing(t *testing.T) {
	accounts := twoTestAccounts()
	fake1 := &fakeRecommendationsClient{recs: []common.Recommendation{{Account: "sub-1"}}}
	fake2 := &fakeRecommendationsClient{recs: []common.Recommendation{{Account: "sub-2"}}}
	withFakeSubscriptionClients(t, map[string]*fakeRecommendationsClient{
		"sub-1": fake1,
		"sub-2": fake2,
	})

	client, err := NewMultiSubscriptionRecommendationsClient(&mockAzureTokenCredential{}, accounts)
	require.NoError(t, err)

	recs, err := client.GetRecommendations(context.Background(), &common.RecommendationParams{
		AccountFilter: []string{"sub-not-visible"},
	})
	require.Error(t, err)
	assert.Nil(t, recs)
	assert.Contains(t, err.Error(), "matches none of the 2 accessible subscriptions")
	assert.Equal(t, int64(0), fake1.calls.Load())
	assert.Equal(t, int64(0), fake2.calls.Load())
}

// TestMultiSubscriptionRecommendationsClient_GetRecommendations_AccountFilterPartiallyMatches
// is the regression test for a silently narrowed sweep.
//
// The all-miss case above errors loudly, but a filter matching SOME of its
// entries used to drop the misses on the floor and return a nil error. The
// scenario that makes that dangerous: an operator scopes a sweep to sub-1 and
// sub-2, the principal has since lost Reader on sub-2 so it is absent from the
// discovered list, and the sweep returns only sub-1's rows with err == nil.
// sub-2 then reads as "no savings available" -- indistinguishable from a
// genuine empty result, and persisted as a shrinking opportunity.
//
// The requested-but-unreachable subscription must therefore appear in the
// partial-failure error, while the subscription that DID answer still returns
// its data.
func TestMultiSubscriptionRecommendationsClient_GetRecommendations_AccountFilterPartiallyMatches(t *testing.T) {
	accounts := twoTestAccounts()
	fake1 := &fakeRecommendationsClient{recs: []common.Recommendation{{Account: "sub-1", Service: common.ServiceCompute}}}
	fake2 := &fakeRecommendationsClient{recs: []common.Recommendation{{Account: "sub-2", Service: common.ServiceCache}}}
	withFakeSubscriptionClients(t, map[string]*fakeRecommendationsClient{
		"sub-1": fake1,
		"sub-2": fake2,
	})

	client, err := NewMultiSubscriptionRecommendationsClient(&mockAzureTokenCredential{}, accounts)
	require.NoError(t, err)

	recs, err := client.GetRecommendations(context.Background(), &common.RecommendationParams{
		AccountFilter: []string{"sub-1", "sub-not-visible"},
	})

	var partial *PartialSubscriptionFailureError
	require.ErrorAs(t, err, &partial,
		"a filter entry matching no accessible subscription must not be silently dropped")
	assert.Equal(t, 2, partial.Attempted,
		"a requested-but-unreachable subscription still counts towards the sweep's intended scope")
	assert.Equal(t, 1, partial.Succeeded)
	require.Len(t, partial.Failed, 1)
	assert.Equal(t, "sub-not-visible", partial.Failed[0].SubscriptionID)
	assert.ErrorIs(t, err, ErrSubscriptionNotAccessible,
		"the cause must be distinguishable from a transient ARM failure")

	// The subscription that DID match still returns its data, so one revoked
	// subscription does not become a total collection outage.
	assert.Equal(t, []common.Recommendation{{Account: "sub-1", Service: common.ServiceCompute}}, recs)
	assert.Equal(t, int64(1), fake1.calls.Load(), "the matched subscription must be queried")
	assert.Equal(t, int64(0), fake2.calls.Load(), "a subscription outside the filter must not be queried")
}

// A duplicate AccountFilter entry must not query its subscription twice nor
// inflate Attempted -- the filter is a set, and double-counting would
// double-count that subscription's recommendations.
func TestMultiSubscriptionRecommendationsClient_GetRecommendations_AccountFilterDeduplicates(t *testing.T) {
	accounts := twoTestAccounts()
	fake1 := &fakeRecommendationsClient{recs: []common.Recommendation{{Account: "sub-1", Service: common.ServiceCompute}}}
	fake2 := &fakeRecommendationsClient{}
	withFakeSubscriptionClients(t, map[string]*fakeRecommendationsClient{
		"sub-1": fake1,
		"sub-2": fake2,
	})

	client, err := NewMultiSubscriptionRecommendationsClient(&mockAzureTokenCredential{}, accounts)
	require.NoError(t, err)

	recs, err := client.GetRecommendations(context.Background(), &common.RecommendationParams{
		AccountFilter: []string{"sub-1", "sub-1"},
	})
	require.NoError(t, err)
	assert.Equal(t, []common.Recommendation{{Account: "sub-1", Service: common.ServiceCompute}}, recs)
	assert.Equal(t, int64(1), fake1.calls.Load(), "a duplicated filter entry must be queried once")
}

// An empty AccountFilter keeps the org-wide default: every visible
// subscription is queried.
func TestMultiSubscriptionRecommendationsClient_GetRecommendations_EmptyAccountFilterFansOutToAll(t *testing.T) {
	accounts := twoTestAccounts()
	fake1 := &fakeRecommendationsClient{recs: []common.Recommendation{{Account: "sub-1"}}}
	fake2 := &fakeRecommendationsClient{recs: []common.Recommendation{{Account: "sub-2"}}}
	withFakeSubscriptionClients(t, map[string]*fakeRecommendationsClient{
		"sub-1": fake1,
		"sub-2": fake2,
	})

	client, err := NewMultiSubscriptionRecommendationsClient(&mockAzureTokenCredential{}, accounts)
	require.NoError(t, err)

	recs, err := client.GetRecommendations(context.Background(), &common.RecommendationParams{})
	require.NoError(t, err)
	assert.ElementsMatch(t, []common.Recommendation{{Account: "sub-1"}, {Account: "sub-2"}}, recs)
	assert.Equal(t, int64(1), fake1.calls.Load())
	assert.Equal(t, int64(1), fake2.calls.Load())
}

func TestMultiSubscriptionRecommendationsClient_GetRecommendationsForService_PassesServiceFilter(t *testing.T) {
	accounts := twoTestAccounts()
	fake1 := &fakeRecommendationsClient{}
	fake2 := &fakeRecommendationsClient{}
	withFakeSubscriptionClients(t, map[string]*fakeRecommendationsClient{
		"sub-1": fake1,
		"sub-2": fake2,
	})

	client, err := NewMultiSubscriptionRecommendationsClient(&mockAzureTokenCredential{}, accounts)
	require.NoError(t, err)

	_, err = client.GetRecommendationsForService(context.Background(), common.ServiceCompute)
	require.NoError(t, err)
	require.NotNil(t, fake1.gotParams)
	require.NotNil(t, fake2.gotParams)
	assert.Equal(t, common.ServiceCompute, fake1.gotParams.Service)
	assert.Equal(t, common.ServiceCompute, fake2.gotParams.Service)
}
