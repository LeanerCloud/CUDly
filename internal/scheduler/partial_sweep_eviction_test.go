package scheduler

import (
	"context"
	"errors"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/pkg/common"
	azureprovider "github.com/LeanerCloud/CUDly/providers/azure"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// A partially-swept account must never authorize stale-row eviction.
//
// UpsertRecommendations deletes an account's previous-cycle rows with
//
//	DELETE FROM recommendations
//	 WHERE collected_at < $1 AND (provider, account_key) IN (…)
//
// where the roster comes from the succeeded-account IDs these collect
// functions return. That predicate is keyed on (provider, account_key) --
// the CUDly account UUID -- but an Azure sweep fails one SUBSCRIPTION at a
// time. A sweep that never queried subscription B returns no rows for B, so
// listing the account as succeeded deletes B's previous rows: its savings
// opportunities vanish and the dashboard reads "nothing to buy" rather than
// "collection incomplete".
//
// The existing account-level tests cannot see this. They assert that a
// collection which returns an *error* is excluded, and a partial sweep
// deliberately returns data plus *PartialSubscriptionFailureError -- the one
// shape that is neither a clean success nor a failure. That is the whole
// defect, and why these tests drive the sub-account granularity directly.
//
// Note the assertions are on the roster, NOT on the recommendations: a
// partial sweep's rows must still be collected and upserted, so the
// subscriptions that did succeed get fresh data. Only the authority to
// DELETE is withheld. Failing the account instead would blank a whole
// account's dashboard over one flaky subscription, which is the opposite
// failure and the invariant SuccessfulCollect exists to protect.

// partialSweepErr builds the error the Azure fan-out returns when some
// subscriptions could not be queried. Mirrors the real construction site,
// providers/azure/recommendations_multi_subscription.go.
func partialSweepErr(failedSubscriptionIDs ...string) *azureprovider.PartialSubscriptionFailureError {
	failures := make([]azureprovider.SubscriptionFailure, 0, len(failedSubscriptionIDs))
	for _, id := range failedSubscriptionIDs {
		failures = append(failures, azureprovider.SubscriptionFailure{
			SubscriptionID: id,
			Err:            errors.New("403 authorization failed"),
		})
	}
	return &azureprovider.PartialSubscriptionFailureError{
		Attempted: len(failedSubscriptionIDs) + 1,
		Succeeded: 1,
		Failed:    failures,
	}
}

// newPartialSweepScheduler wires a Scheduler whose Azure provider returns
// recs plus a partial-subscription failure, exercising the ambient path
// (no registered accounts + AZURE_SUBSCRIPTION_ID set).
func newPartialSweepScheduler(t *testing.T, sweepErr error) *Scheduler {
	t.Helper()

	recClient := new(MockRecommendationsClient)
	t.Cleanup(func() { recClient.AssertExpectations(t) })
	recClient.On("GetAllRecommendations", mock.Anything).
		Return([]common.Recommendation{{
			Service:       common.ServiceEC2,
			Region:        "westeurope",
			ResourceType:  "Standard_D2s_v3",
			Count:         3,
			Term:          "1yr",
			PaymentOption: "all-upfront",
		}}, sweepErr).Once()

	prov := new(MockProvider)
	t.Cleanup(func() { prov.AssertExpectations(t) })
	prov.On("GetRecommendationsClient", mock.Anything).Return(recClient, nil).Once()

	factory := new(MockProviderFactory)
	t.Cleanup(func() { factory.AssertExpectations(t) })
	factory.On("CreateAndValidateProvider", mock.Anything, "azure", mock.Anything).
		Return(prov, nil).Once()

	store := new(MockConfigStore)
	t.Cleanup(func() { store.AssertExpectations(t) })
	// No registered Azure accounts -> ambient path.
	store.On("ListCloudAccounts", mock.Anything, mock.Anything).
		Return([]config.CloudAccount{}, nil).Maybe()
	// Ambient account resolution is best-effort; leave it unresolved so the
	// roster carries the nil-CloudAccountID sentinel and the assertion is
	// about roster membership rather than which ID it holds.
	store.On("GetCloudAccountByExternalID", mock.Anything, "azure", mock.Anything).
		Return(nil, nil).Maybe()

	return &Scheduler{config: store, providerFactory: factory}
}

// TestCollectAzure_PartialSweepIsNotEvictionEligible is the regression for
// #1652. It fails on the pre-fix code, where tolerateIncompleteSweep returns
// nil and the account is reported as fully succeeded.
func TestCollectAzure_PartialSweepIsNotEvictionEligible(t *testing.T) {
	t.Setenv("AZURE_SUBSCRIPTION_ID", "sub-a")

	s := newPartialSweepScheduler(t, partialSweepErr("sub-b", "sub-c"))

	recs, succeededAccountIDs, err := s.collectAzureRecommendations(context.Background(), &config.GlobalConfig{})

	require.NoError(t, err,
		"a partial sweep must not fail the account: one flaky subscription cannot blank the whole account's dashboard")
	assert.NotEmpty(t, recs,
		"the subscriptions that DID succeed must still contribute rows to be upserted")
	assert.Emptyf(t, succeededAccountIDs,
		"a partial sweep must not enter the eviction roster, got %#v: 2 subscription(s) were never "+
			"queried, so their previous-cycle rows would be deleted by the (provider, account_key) DELETE",
		succeededAccountIDs)
}

// TestCollectAzure_CompleteSweepStaysEvictionEligible is the control. The
// fix must withhold eviction ONLY for partial sweeps -- if a clean sweep
// also stopped evicting, stale rows would accumulate forever and rows for
// genuinely-gone resources would never disappear. Under-evicting is a
// quieter bug than over-evicting, so it gets its own assertion.
func TestCollectAzure_CompleteSweepStaysEvictionEligible(t *testing.T) {
	t.Setenv("AZURE_SUBSCRIPTION_ID", "sub-a")

	s := newPartialSweepScheduler(t, nil)

	recs, succeededAccountIDs, err := s.collectAzureRecommendations(context.Background(), &config.GlobalConfig{})

	require.NoError(t, err)
	assert.NotEmpty(t, recs)
	assert.NotEmpty(t, succeededAccountIDs,
		"a complete sweep must still authorize eviction, otherwise stale rows accumulate forever")
}

// TestFanOutPerAccount_PartialSweepNotInSucceededAccountIDs covers the
// registered-account path, which is the common production shape and does not
// go through the ambient collector the tests above drive. It is the partial
// sibling of TestFanOutPerAccount_FailedCollectionNotInSucceededAccountIDs:
// that one pins the FAILED account, this one pins the account that returned
// data from an incomplete sweep -- neither a clean success nor a failure, and
// the case the account-level tests never exercised.
func TestFanOutPerAccount_PartialSweepNotInSucceededAccountIDs(t *testing.T) {
	accounts := []config.CloudAccount{
		{ID: "acct-complete", Name: "acct-complete", ExternalID: "e-ok"},
		{ID: "acct-partial", Name: "acct-partial", ExternalID: "e-partial"},
	}
	fn := func(_ context.Context, acct config.CloudAccount) ([]config.RecommendationRecord, bool, error) {
		// Both return rows; only one swept every subscription.
		return []config.RecommendationRecord{{ID: acct.ID, Provider: "azure"}},
			acct.ID == "acct-complete", nil
	}

	recs, outcome := fanOutPerAccount(context.Background(), "Azure", accounts, fn)

	assert.Len(t, recs, 2,
		"a partial sweep's rows must still be collected and upserted -- only eviction is withheld")
	assert.Equal(t, []string{"acct-complete"}, outcome.SucceededAccountIDs,
		"only the fully-swept account may authorize stale-row eviction")
	assert.Equal(t, []string{"acct-partial"}, outcome.IncompleteAccountIDs)
	assert.Equal(t, 2, outcome.SucceededCount,
		"a partial sweep still counts as a success, so one flaky subscription cannot trip the all-accounts-failed guard")
	assert.Zero(t, outcome.FailedCount)
}

// TestCollectAzure_HardSweepErrorStillFailsTheAccount pins that the fix did
// not soften genuine failures into partial ones: a non-partial error must
// still propagate, keeping the account out of the roster via the error path.
func TestCollectAzure_HardSweepErrorStillFailsTheAccount(t *testing.T) {
	t.Setenv("AZURE_SUBSCRIPTION_ID", "sub-a")

	s := newPartialSweepScheduler(t, errors.New("429 too many requests"))

	_, succeededAccountIDs, err := s.collectAzureRecommendations(context.Background(), &config.GlobalConfig{})

	require.Error(t, err, "a hard collection error must still fail the account")
	assert.Empty(t, succeededAccountIDs)
}
