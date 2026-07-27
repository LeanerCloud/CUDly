package azure

import (
	"context"
	"errors"
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
	recs      []common.Recommendation
	err       error
	gotParams *common.RecommendationParams
}

func (f *fakeRecommendationsClient) GetRecommendations(ctx context.Context, params *common.RecommendationParams) ([]common.Recommendation, error) {
	f.gotParams = params
	if err := ctx.Err(); err != nil {
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
	withFakeSubscriptionClients(t, map[string]*fakeRecommendationsClient{
		"sub-1": {recs: []common.Recommendation{{Account: "sub-1"}}},
		"sub-2": {recs: []common.Recommendation{{Account: "sub-2"}}},
	})

	client, err := NewMultiSubscriptionRecommendationsClient(&mockAzureTokenCredential{}, accounts)
	require.NoError(t, err)
	require.Len(t, client.subscriptions, 2)
	assert.Equal(t, "sub-1", client.subscriptions[0].subscriptionID)
	assert.Equal(t, "sub-2", client.subscriptions[1].subscriptionID)
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

func TestMultiSubscriptionRecommendationsClient_GetRecommendations_PropagatesContextCancellation(t *testing.T) {
	accounts := twoTestAccounts()
	withFakeSubscriptionClients(t, map[string]*fakeRecommendationsClient{
		"sub-1": {recs: []common.Recommendation{{Account: "sub-1"}}},
		"sub-2": {recs: []common.Recommendation{{Account: "sub-2"}}},
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
