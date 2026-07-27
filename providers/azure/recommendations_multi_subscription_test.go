package azure

import (
	"context"
	"errors"
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

func TestMultiSubscriptionRecommendationsClient_GetRecommendations_PartialFailureStillSucceeds(t *testing.T) {
	accounts := twoTestAccounts()
	withFakeSubscriptionClients(t, map[string]*fakeRecommendationsClient{
		"sub-1": {err: errors.New("sub-1 unreachable")},
		"sub-2": {recs: []common.Recommendation{{Account: "sub-2", Service: common.ServiceCache}}},
	})

	client, err := NewMultiSubscriptionRecommendationsClient(&mockAzureTokenCredential{}, accounts)
	require.NoError(t, err)

	recs, err := client.GetAllRecommendations(context.Background())
	require.NoError(t, err, "one subscription failing must not fail the whole fan-out")
	assert.Equal(t, []common.Recommendation{{Account: "sub-2", Service: common.ServiceCache}}, recs)
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
