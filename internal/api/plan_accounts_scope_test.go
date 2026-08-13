package api

import (
	"context"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Account-scope coverage for the plan↔account association endpoints
// (issue #1769).
//
// PUT /api/plans/:id/accounts gated only on update:plans, and GET on
// view:plans. Both verbs are DEFAULT Standard User grants, so the verb gate
// alone let any stock user re-point any plan at any account and read back the
// account roster of any plan by id. The two axes are independent: the plan
// being written and the accounts being attached.
//
// Every refusal below is paired with a control that must still be ALLOWED.
// A refusal-only suite passes just as well against a handler that refuses
// everyone, which is the failure mode these endpoints are one bad edit away
// from.

const (
	// The plan under test. Its CURRENT association set is what the plan-axis
	// guard reads, and is seeded per test.
	scopePlanID = "33333333-3333-4333-8333-333333333333"
	// A second account that is in neither the allow-list nor any fixture, used
	// to prove the write is refused before it reaches the store.
	scopeThirdAccount = "44444444-4444-4444-8444-444444444444"
)

// planAccountsWrite records what setPlanAccounts handed to the store.
type planAccountsWrite struct {
	called bool
	ids    []string
}

// seedPlanAccountsStore wires the store so the request would SUCCEED
// end-to-end if the scope guard were removed: the plan exists and derives the
// "aws" provider, every account id resolves to an existing aws account (so the
// issue-#209 provider validation passes), and SetPlanAccounts accepts the
// write.
//
// The permissive SetPlanAccounts stub is the load-bearing part. Without it a
// removed guard would fail these tests by an unstubbed-call panic, which is
// not evidence about the guard: mutation runs have to fail by ASSERTION.
//
// planAccounts is the plan's CURRENT association set, which is what
// requirePlanAccess reads to decide whether the caller may touch the plan at
// all.
func seedPlanAccountsStore(store *MockConfigStore, planAccounts []config.CloudAccount) *planAccountsWrite {
	write := &planAccountsWrite{}
	store.GetPurchasePlanFn = func(_ context.Context, id string) (*config.PurchasePlan, error) {
		return &config.PurchasePlan{
			ID:       id,
			Name:     "scoped plan",
			Services: map[string]config.ServiceConfig{"aws/ec2": {}},
		}, nil
	}
	store.GetPlanAccountsFn = func(_ context.Context, _ string) ([]config.CloudAccount, error) {
		return planAccounts, nil
	}
	store.GetCloudAccountFn = func(_ context.Context, id string) (*config.CloudAccount, error) {
		return &config.CloudAccount{ID: id, Name: "acct-" + id, Provider: "aws"}, nil
	}
	store.SetPlanAccountsFn = func(_ context.Context, _ string, ids []string) error {
		write.called = true
		write.ids = ids
		return nil
	}
	return write
}

// inScopeAccount / outOfScopeAccount are the two association fixtures: the
// account the scoped principal holds, and one it does not.
func inScopeAccount() config.CloudAccount {
	return config.CloudAccount{ID: scopedInAccount, Name: "acct-" + scopedInAccount, Provider: "aws"}
}

func outOfScopeAccount() config.CloudAccount {
	return config.CloudAccount{ID: scopedOutAccount, Name: "acct-" + scopedOutAccount, Provider: "aws"}
}

// ── setPlanAccounts: account axis ───────────────────────────────────────────

// A scoped caller may hold the plan and still not hold the account it is
// trying to attach. The write must be refused, and refused with the
// enumeration-safe not-found rather than a 403 that would confirm the account
// exists.
func TestSetPlanAccounts_ScopedCallerCannotAttachOutOfScopeAccount(t *testing.T) {
	ctx := context.Background()
	h, store := scopedHandler(t, scopedInAccount)
	t.Cleanup(func() { store.AssertExpectations(t) })
	// The plan itself IS in scope, so only the account axis can refuse this.
	write := seedPlanAccountsStore(store, []config.CloudAccount{inScopeAccount()})

	body := `{"account_ids":["` + scopedOutAccount + `"]}`
	_, err := h.setPlanAccounts(ctx, scopedRequest(body), scopePlanID)

	require.Error(t, err)
	assert.True(t, IsNotFoundError(err), "expected the enumeration-safe not-found refusal, got %v", err)
	assert.False(t, write.called, "SetPlanAccounts must not run for an out-of-scope account")
}

// The control for the test above: same handler, same code path, an account the
// caller DOES hold. Without this, a handler that refused every write would
// pass the refusal test.
func TestSetPlanAccounts_ScopedCallerCanAttachInScopeAccount(t *testing.T) {
	ctx := context.Background()
	h, store := scopedHandler(t, scopedInAccount)
	t.Cleanup(func() { store.AssertExpectations(t) })
	write := seedPlanAccountsStore(store, []config.CloudAccount{inScopeAccount()})

	body := `{"account_ids":["` + scopedInAccount + `"]}`
	_, err := h.setPlanAccounts(ctx, scopedRequest(body), scopePlanID)

	require.NoError(t, err)
	require.True(t, write.called, "an in-scope write must still reach the store")
	assert.Equal(t, []string{scopedInAccount}, write.ids)
}

// A batch is refused whole. One out-of-scope entry alongside an in-scope one
// must not write the in-scope half either, since SetPlanAccounts replaces the
// association set rather than adding to it.
func TestSetPlanAccounts_ScopedCallerMixedBatchRefusedWhole(t *testing.T) {
	ctx := context.Background()
	h, store := scopedHandler(t, scopedInAccount)
	t.Cleanup(func() { store.AssertExpectations(t) })
	write := seedPlanAccountsStore(store, []config.CloudAccount{inScopeAccount()})

	body := `{"account_ids":["` + scopedInAccount + `","` + scopeThirdAccount + `"]}`
	_, err := h.setPlanAccounts(ctx, scopedRequest(body), scopePlanID)

	require.Error(t, err)
	assert.True(t, IsNotFoundError(err), "expected not-found, got %v", err)
	assert.False(t, write.called, "a batch containing an out-of-scope account must not write at all")
}

// ── setPlanAccounts: plan axis ──────────────────────────────────────────────

// The other axis, isolated: every account in the BODY is one the caller holds,
// but the plan's current association set is entirely outside their scope. The
// account-axis check passes here, so only the plan-axis check can refuse it.
func TestSetPlanAccounts_ScopedCallerCannotRepointOutOfScopePlan(t *testing.T) {
	ctx := context.Background()
	h, store := scopedHandler(t, scopedInAccount)
	t.Cleanup(func() { store.AssertExpectations(t) })
	write := seedPlanAccountsStore(store, []config.CloudAccount{outOfScopeAccount()})

	body := `{"account_ids":["` + scopedInAccount + `"]}`
	_, err := h.setPlanAccounts(ctx, scopedRequest(body), scopePlanID)

	require.Error(t, err)
	assert.True(t, IsNotFoundError(err), "expected not-found, got %v", err)
	assert.False(t, write.called, "a plan outside the caller's scope must not be re-pointed")
}

// ── setPlanAccounts: unrestricted caller unchanged ──────────────────────────

// An unrestricted principal keeps the pre-fix behavior, and pays for no extra
// store round-trip: the guard resolves the scope once and returns before
// touching the store, which is what keeps admin-path fixtures unchanged.
func TestSetPlanAccounts_UnrestrictedCallerUnaffected(t *testing.T) {
	ctx := context.Background()
	h, store := scopedHandler(t) // no accounts -> grantAdmin -> unrestricted
	t.Cleanup(func() { store.AssertExpectations(t) })
	write := seedPlanAccountsStore(store, []config.CloudAccount{outOfScopeAccount()})

	body := `{"account_ids":["` + scopedOutAccount + `"]}`
	_, err := h.setPlanAccounts(ctx, scopedRequest(body), scopePlanID)

	require.NoError(t, err, "an unrestricted caller must still write any account to any plan")
	require.True(t, write.called)
	assert.Equal(t, []string{scopedOutAccount}, write.ids)
	store.AssertNotCalled(t, "GetPlanAccounts")
}

// ── listPlanAccounts ────────────────────────────────────────────────────────

// The read sibling. A plan the caller can reach through one of their accounts
// may carry accounts they hold no entitlement to; those must not come back in
// the response.
func TestListPlanAccounts_ScopedCallerSeesOnlyItsOwnAccounts(t *testing.T) {
	ctx := context.Background()
	h, store := scopedHandler(t, scopedInAccount)
	t.Cleanup(func() { store.AssertExpectations(t) })
	seedPlanAccountsStore(store, []config.CloudAccount{inScopeAccount(), outOfScopeAccount()})

	result, err := h.listPlanAccounts(ctx, scopedRequest(""), scopePlanID)

	require.NoError(t, err)
	got, ok := result.([]config.CloudAccount)
	require.True(t, ok, "expected []config.CloudAccount, got %T", result)
	require.Len(t, got, 1, "only the in-scope account may be disclosed")
	assert.Equal(t, scopedInAccount, got[0].ID)
}

// A plan with no in-scope account at all is not readable, and hides behind the
// same not-found the write path returns.
func TestListPlanAccounts_ScopedCallerCannotReadOutOfScopePlan(t *testing.T) {
	ctx := context.Background()
	h, store := scopedHandler(t, scopedInAccount)
	t.Cleanup(func() { store.AssertExpectations(t) })
	seedPlanAccountsStore(store, []config.CloudAccount{outOfScopeAccount()})

	result, err := h.listPlanAccounts(ctx, scopedRequest(""), scopePlanID)

	require.Error(t, err)
	assert.Nil(t, result)
	assert.True(t, IsNotFoundError(err), "expected not-found, got %v", err)
}

// The read-side control: an unrestricted caller still gets the full roster,
// so the filter above is a scope decision rather than a blanket narrowing.
func TestListPlanAccounts_UnrestrictedCallerSeesAllAccounts(t *testing.T) {
	ctx := context.Background()
	h, store := scopedHandler(t) // unrestricted
	t.Cleanup(func() { store.AssertExpectations(t) })
	seedPlanAccountsStore(store, []config.CloudAccount{inScopeAccount(), outOfScopeAccount()})

	result, err := h.listPlanAccounts(ctx, scopedRequest(""), scopePlanID)

	require.NoError(t, err)
	got, ok := result.([]config.CloudAccount)
	require.True(t, ok, "expected []config.CloudAccount, got %T", result)
	assert.Len(t, got, 2, "an unrestricted caller must still see every account of the plan")
}

// ── Real dispatch path ──────────────────────────────────────────────────────
//
// Handler-level tests have missed router-level bypasses on this repo before
// (#1757, #1773), so the two directions are re-run through Router.Route with
// the real route table and auth gate rather than by calling the handler.

func TestRouterDispatch_SetPlanAccounts_ScopedOutOfScopeAccountRefused(t *testing.T) {
	ctx := context.Background()
	h, store := scopedHandler(t, scopedInAccount)
	t.Cleanup(func() { store.AssertExpectations(t) })
	write := seedPlanAccountsStore(store, []config.CloudAccount{inScopeAccount()})

	body := `{"account_ids":["` + scopedOutAccount + `"]}`
	_, err := NewRouter(h).Route(ctx, "PUT", "/api/plans/"+scopePlanID+"/accounts", scopedRequest(body))

	require.Error(t, err)
	assert.True(t, IsNotFoundError(err), "expected not-found through the router, got %v", err)
	assert.False(t, write.called, "the router path must not reach the store either")
}

func TestRouterDispatch_SetPlanAccounts_ScopedInScopeAccountAllowed(t *testing.T) {
	ctx := context.Background()
	h, store := scopedHandler(t, scopedInAccount)
	t.Cleanup(func() { store.AssertExpectations(t) })
	write := seedPlanAccountsStore(store, []config.CloudAccount{inScopeAccount()})

	body := `{"account_ids":["` + scopedInAccount + `"]}`
	_, err := NewRouter(h).Route(ctx, "PUT", "/api/plans/"+scopePlanID+"/accounts", scopedRequest(body))

	require.NoError(t, err, "an in-scope write must still succeed through the router")
	require.True(t, write.called)
	assert.Equal(t, []string{scopedInAccount}, write.ids)
}

func TestRouterDispatch_ListPlanAccounts_ScopedCallerFiltered(t *testing.T) {
	ctx := context.Background()
	h, store := scopedHandler(t, scopedInAccount)
	t.Cleanup(func() { store.AssertExpectations(t) })
	seedPlanAccountsStore(store, []config.CloudAccount{inScopeAccount(), outOfScopeAccount()})

	result, err := NewRouter(h).Route(ctx, "GET", "/api/plans/"+scopePlanID+"/accounts", scopedRequest(""))

	require.NoError(t, err)
	got, ok := result.([]config.CloudAccount)
	require.True(t, ok, "expected []config.CloudAccount, got %T", result)
	require.Len(t, got, 1)
	assert.Equal(t, scopedInAccount, got[0].ID)
}
