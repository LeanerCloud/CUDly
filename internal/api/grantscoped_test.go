package api

import (
	"context"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// Restricted-account coverage (issue #1596).
//
// grantAdmin pins GetAllowedAccountsAPI to nil, which the API layer reads as
// unrestricted, so before grantScoped existed NO test in this package could
// exercise a restricted allow-list. That is the structural reason the
// #950/#956 account-filter regressions survived four rounds of "fixed, tests
// are green": the suite had no restriction to enforce.
//
// These pin the shared seam every scoped handler funnels through
// (getAccountScope -> requireAccountAccess / requirePlanAccess), so a
// regression in the seam fails here rather than silently in production.

const (
	scopedInAccount  = "11111111-1111-4111-8111-111111111111"
	scopedOutAccount = "22222222-2222-4222-8222-222222222222"
	scopedToken      = "scoped-token"
	scopedUserID     = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
)

func scopedHandler(t *testing.T, accounts ...string) (*Handler, *MockConfigStore) {
	t.Helper()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })

	mockAuth.On("ValidateSession", mock.Anything, scopedToken).
		Return(&Session{UserID: scopedUserID}, nil).Maybe()
	if len(accounts) == 0 {
		mockAuth.grantAdmin()
	} else {
		mockAuth.grantScoped(accounts...)
	}
	return &Handler{config: mockStore, auth: mockAuth}, mockStore
}

// TestGrantScoped_RestrictsAccountAccess is the core assertion: a principal
// scoped to one account cannot reach another, and the refusal is the
// enumeration-safe errNotFound rather than a 403 that would confirm the
// account exists.
func TestGrantScoped_RestrictsAccountAccess(t *testing.T) {
	ctx := context.Background()
	h, mockStore := scopedHandler(t, scopedInAccount)

	other := &config.CloudAccount{ID: scopedOutAccount, Name: "other-account"}
	mockStore.On("GetCloudAccount", ctx, scopedOutAccount).Return(other, nil)

	got, err := h.requireAccountAccess(ctx, &Session{UserID: scopedUserID}, scopedOutAccount)

	require.Error(t, err, "an account outside the allow-list must not be reachable")
	assert.Nil(t, got)
	assert.ErrorIs(t, err, errNotFound)
}

// Negative control: the same handler, the same code path, an account that IS
// in the allow-list. Without this, a seam that refused everything would pass
// the test above.
func TestGrantScoped_AllowsInScopeAccount(t *testing.T) {
	ctx := context.Background()
	h, mockStore := scopedHandler(t, scopedInAccount)

	mine := &config.CloudAccount{ID: scopedInAccount, Name: "my-account"}
	mockStore.On("GetCloudAccount", ctx, scopedInAccount).Return(mine, nil)

	got, err := h.requireAccountAccess(ctx, &Session{UserID: scopedUserID}, scopedInAccount)

	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, scopedInAccount, got.ID)
}

// Second negative control: an UNRESTRICTED admin still reaches the same
// account grantScoped refuses. This is what proves the refusal above comes
// from the allow-list and not from some unrelated failure in the fixture.
func TestGrantAdmin_UnrestrictedReachesAnyAccount(t *testing.T) {
	ctx := context.Background()
	h, mockStore := scopedHandler(t) // no accounts -> grantAdmin, unrestricted

	other := &config.CloudAccount{ID: scopedOutAccount, Name: "other-account"}
	mockStore.On("GetCloudAccount", ctx, scopedOutAccount).Return(other, nil)

	got, err := h.requireAccountAccess(ctx, &Session{UserID: scopedUserID}, scopedOutAccount)

	require.NoError(t, err, "an unrestricted admin must still reach any account")
	require.NotNil(t, got)
}

// The allow-list matches on display name as well as ID (auth.MatchesAccount),
// so a scoped principal named by account NAME resolves too. Pinned because a
// regression here silently widens or narrows every scoped handler at once.
func TestGrantScoped_MatchesByAccountName(t *testing.T) {
	ctx := context.Background()
	h, mockStore := scopedHandler(t, "prod-account")

	mine := &config.CloudAccount{ID: scopedInAccount, Name: "prod-account"}
	mockStore.On("GetCloudAccount", ctx, scopedInAccount).Return(mine, nil)

	got, err := h.requireAccountAccess(ctx, &Session{UserID: scopedUserID}, scopedInAccount)

	require.NoError(t, err)
	require.NotNil(t, got)
}
