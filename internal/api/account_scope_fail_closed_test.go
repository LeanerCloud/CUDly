package api

import (
	"context"
	"errors"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// The API-side half of issue #1748.
//
// getAllowedAccounts returned (nil, nil) when h.auth was nil, and an empty
// list means UNRESTRICTED (IsUnrestrictedAccess) -- so a handler running
// without an auth service granted every caller access to every cloud account.
// Auth components must fail closed when nil, never fall through.

const (
	scopeSessionUser = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	scopeAcctA       = "11111111-1111-4111-8111-111111111111"
	scopeAcctB       = "22222222-2222-4222-8222-222222222222"
)

// ---------- the failure modes: each must REFUSE ----------

func TestGetAllowedAccounts_FailsClosedWhenAuthMissing(t *testing.T) {
	ctx := context.Background()
	h := &Handler{auth: nil}

	got, err := h.getAccountScope(ctx, &Session{UserID: scopeSessionUser})

	require.Error(t, err, "a nil auth service must refuse, not grant unrestricted access")
	assert.False(t, got.AllowsAll(), "the scope returned alongside the error must not be unrestricted")
	assert.False(t, got.Allows("any-account", ""), "and must grant no account at all")
	assert.Contains(t, err.Error(), "cannot establish account scope")
}

// The error from the resolver must reach the caller rather than being
// flattened into an empty (= unrestricted) list.
func TestGetAllowedAccounts_PropagatesResolverFailure(t *testing.T) {
	ctx := context.Background()
	m := new(MockAuthService)
	t.Cleanup(func() { m.AssertExpectations(t) })

	boom := errors.New("account scope could not be established for user: no group resolved")
	m.On("GetAllowedAccountsAPI", ctx, scopeSessionUser).Return([]string(nil), boom)

	h := &Handler{auth: m}
	got, err := h.getAccountScope(ctx, &Session{UserID: scopeSessionUser})

	require.Error(t, err)
	assert.False(t, got.AllowsAll(), "a failed resolution must not yield an unrestricted scope")
	assert.False(t, got.Allows("any-account", ""))
}

// End-to-end through the shared scoping seam every scoped handler uses: an
// unestablishable scope must not turn into access.
func TestRequireAccountAccess_RefusesWhenScopeUnestablishable(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	m := new(MockAuthService)
	t.Cleanup(func() { m.AssertExpectations(t) })

	mockStore.On("GetCloudAccount", ctx, scopeAcctB).
		Return(&config.CloudAccount{ID: scopeAcctB, Name: "other"}, nil)
	m.On("GetAllowedAccountsAPI", ctx, scopeSessionUser).
		Return([]string(nil), errors.New("no group resolved"))

	h := &Handler{config: mockStore, auth: m}
	got, err := h.requireAccountAccess(ctx, &Session{UserID: scopeSessionUser}, scopeAcctB)

	require.Error(t, err, "an unestablishable scope must not grant account access")
	assert.Nil(t, got)
}

// ---------- the controls: legitimate principals must still PASS ----------

// The stateless admin API key has no user row and is unrestricted by design.
// Its unrestricted-ness is expressed POSITIVELY (keyed on the sentinel), not
// by absence, so making absence fail closed must not break it.
func TestGetAllowedAccounts_AdminAPIKeyStillUnrestricted(t *testing.T) {
	ctx := context.Background()
	h := &Handler{auth: new(MockAuthService)}

	got, err := h.getAccountScope(ctx, &Session{UserID: apiKeyAdminUserID})

	require.NoError(t, err, "the admin API key must remain unrestricted")
	assert.True(t, got.AllowsAll(), "expressed as a SET flag, never as an empty list")
}

// The other two legitimate unrestricted principals, resolved through the auth
// service: an Administrators member carrying "*", and a group with no
// allowed_accounts configured (the backward-compat default). The third is the
// one that shares its representation with the failure modes, which is why it
// is pinned here.
func TestGetAllowedAccounts_LegitimateUnrestrictedPrincipalsPass(t *testing.T) {
	ctx := context.Background()

	for _, tc := range []struct {
		name     string
		resolved []string
	}{
		{"Administrators member carrying the * wildcard", []string{"*"}},
		{"group with no allowed_accounts configured", []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := new(MockAuthService)
			t.Cleanup(func() { m.AssertExpectations(t) })
			m.On("GetAllowedAccountsAPI", ctx, scopeSessionUser).Return(tc.resolved, nil)

			h := &Handler{auth: m}
			got, err := h.getAccountScope(ctx, &Session{UserID: scopeSessionUser})

			require.NoError(t, err, "a successfully resolved scope must not be refused")
			assert.True(t, got.AllowsAll(),
				"a successful resolution to an empty/wildcard scope still means all accounts")
		})
	}
}

// And the scoped principal still gets exactly its own accounts -- proving the
// fix did not collapse everything into either extreme.
func TestRequireAccountAccess_ScopedPrincipalUnchanged(t *testing.T) {
	ctx := context.Background()

	t.Run("in-scope account is reachable", func(t *testing.T) {
		mockStore := new(MockConfigStore)
		m := new(MockAuthService)
		t.Cleanup(func() { m.AssertExpectations(t) })
		mockStore.On("GetCloudAccount", ctx, scopeAcctA).
			Return(&config.CloudAccount{ID: scopeAcctA, Name: "mine"}, nil)
		m.On("GetAllowedAccountsAPI", ctx, scopeSessionUser).Return([]string{scopeAcctA}, nil)

		h := &Handler{config: mockStore, auth: m}
		got, err := h.requireAccountAccess(ctx, &Session{UserID: scopeSessionUser}, scopeAcctA)
		require.NoError(t, err)
		require.NotNil(t, got)
	})

	t.Run("out-of-scope account is refused", func(t *testing.T) {
		mockStore := new(MockConfigStore)
		m := new(MockAuthService)
		t.Cleanup(func() { m.AssertExpectations(t) })
		mockStore.On("GetCloudAccount", ctx, scopeAcctB).
			Return(&config.CloudAccount{ID: scopeAcctB, Name: "other"}, nil)
		m.On("GetAllowedAccountsAPI", ctx, scopeSessionUser).Return([]string{scopeAcctA}, nil)

		h := &Handler{config: mockStore, auth: m}
		got, err := h.requireAccountAccess(ctx, &Session{UserID: scopeSessionUser}, scopeAcctB)
		require.Error(t, err)
		assert.Nil(t, got)
		assert.ErrorIs(t, err, errNotFound)
	})

	_ = mock.Anything
}
