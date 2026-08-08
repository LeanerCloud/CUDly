package api

import (
	"context"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// ladderReq builds an authenticated PUT request carrying body.
func ladderReq(body string) *events.LambdaFunctionURLRequest {
	return &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
		Body:    body,
	}
}

// newLadderHandler wires a Handler with an admin session and returns the mock
// store plus a pointer that captures the config handed to UpsertLadderConfig
// (nil until the store is actually reached).
func newLadderHandler(t *testing.T) (*Handler, *MockConfigStore, **config.LadderConfigDB) {
	t.Helper()
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	t.Cleanup(func() { mockStore.AssertExpectations(t); mockAuth.AssertExpectations(t) })

	mockAuth.On("ValidateSession", ctx, "admin-token").
		Return(&Session{UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", Email: "admin@example.com"}, nil)
	mockAuth.grantAdmin()

	var captured *config.LadderConfigDB
	// .Maybe() so the 400-path test (store never reached) doesn't trip
	// AssertExpectations on an unmet expectation. The success tests prove the
	// store was reached by asserting *captured is non-nil (set only in Run).
	mockStore.On("UpsertLadderConfig", ctx, mock.AnythingOfType("*config.LadderConfigDB")).
		Run(func(args mock.Arguments) {
			captured = args.Get(1).(*config.LadderConfigDB)
		}).
		Return(&config.LadderConfigDB{}, nil).
		Maybe()

	return &Handler{config: mockStore, auth: mockAuth}, mockStore, &captured
}

const ladderValidRamp = `"ramp_schedule":{"steps":[{"after_days":0,"fraction":1.0}]}`

// TestUpsertLadderConfig_BufferFractionExplicitZeroSurvives asserts that an
// explicit buffer_fraction:0 ("no buffer") reaches the store unchanged rather
// than being silently rewritten to DefaultLadderBufferFraction.
func TestUpsertLadderConfig_BufferFractionExplicitZeroSurvives(t *testing.T) {
	ctx := context.Background()
	handler, _, captured := newLadderHandler(t)

	body := `{"cloud_account_id":"acct-1","provider":"aws","mode":"email_approval","cadence":"daily","buffer_fraction":0,` + ladderValidRamp + `}`
	_, err := handler.upsertLadderConfig(ctx, ladderReq(body))
	require.NoError(t, err)
	require.NotNil(t, *captured, "store should have been reached")
	assert.Equal(t, 0.0, (*captured).BufferFraction,
		"explicit buffer_fraction:0 must survive to the store unchanged")
}

// TestUpsertLadderConfig_BufferFractionOmittedDefaults asserts that an omitted
// buffer_fraction key defaults to DefaultLadderBufferFraction.
func TestUpsertLadderConfig_BufferFractionOmittedDefaults(t *testing.T) {
	ctx := context.Background()
	handler, _, captured := newLadderHandler(t)

	body := `{"cloud_account_id":"acct-1","provider":"aws","mode":"email_approval","cadence":"daily",` + ladderValidRamp + `}`
	_, err := handler.upsertLadderConfig(ctx, ladderReq(body))
	require.NoError(t, err)
	require.NotNil(t, *captured, "store should have been reached")
	assert.Equal(t, config.DefaultLadderBufferFraction, (*captured).BufferFraction,
		"omitted buffer_fraction must default to DefaultLadderBufferFraction")
}

// TestUpsertLadderConfig_ExplicitZeroTargetCoverageRejected asserts the FIX 1
// contract: an explicit out-of-range target_coverage:0 returns 400 and never
// reaches the store (it is not silently defaulted).
func TestUpsertLadderConfig_ExplicitZeroTargetCoverageRejected(t *testing.T) {
	ctx := context.Background()
	handler, mockStore, _ := newLadderHandler(t)

	body := `{"cloud_account_id":"acct-1","provider":"aws","mode":"email_approval","cadence":"daily","target_coverage":0,` + ladderValidRamp + `}`
	result, err := handler.upsertLadderConfig(ctx, ladderReq(body))
	require.Error(t, err)
	assert.Nil(t, result)
	ce, ok := IsClientError(err)
	require.True(t, ok, "expected ClientError, got %T: %v", err, err)
	assert.Equal(t, 400, ce.code)
	mockStore.AssertNotCalled(t, "UpsertLadderConfig", mock.Anything, mock.Anything)
}

// TestUpsertLadderConfig_MultiStepRampAccepted is the F1 end-to-end regression:
// a multi-step ramp (after_days 0 -> 30 -> 60) must be ACCEPTED and round-trip
// to the store unchanged. Against the pre-tag pkg/ladder code every AfterDays
// decoded to 0, so Validate rejected the ramp (not strictly ascending) and this
// PUT returned 400.
func TestUpsertLadderConfig_MultiStepRampAccepted(t *testing.T) {
	ctx := context.Background()
	handler, _, captured := newLadderHandler(t)

	const ramp = `"ramp_schedule":{"steps":[{"after_days":0,"fraction":0.5},{"after_days":30,"fraction":0.3},{"after_days":60,"fraction":0.2}]}`
	body := `{"cloud_account_id":"acct-1","provider":"aws","mode":"email_approval","cadence":"daily",` + ramp + `}`
	result, err := handler.upsertLadderConfig(ctx, ladderReq(body))
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, *captured, "store should have been reached")
	// The multi-step ramp round-trips to the store unchanged (raw JSONB).
	assert.JSONEq(t,
		`{"steps":[{"after_days":0,"fraction":0.5},{"after_days":30,"fraction":0.3},{"after_days":60,"fraction":0.2}]}`,
		string((*captured).RampSchedule))
}

// TestUpsertLadderConfig_UnknownFieldRejected is F5: a typo'd key must be
// rejected with 400 (DisallowUnknownFields), not silently dropped -- a mistyped
// max_hourly_commit_per_run would otherwise decode to nil = no spend cap.
func TestUpsertLadderConfig_UnknownFieldRejected(t *testing.T) {
	ctx := context.Background()
	handler, mockStore, _ := newLadderHandler(t)

	body := `{"cloud_account_id":"acct-1","provider":"aws","mode":"email_approval","cadence":"daily","max_hourly_commit_per_runn":5,` + ladderValidRamp + `}`
	result, err := handler.upsertLadderConfig(ctx, ladderReq(body))
	require.Error(t, err)
	assert.Nil(t, result)
	ce, ok := IsClientError(err)
	require.True(t, ok, "expected ClientError, got %T: %v", err, err)
	assert.Equal(t, 400, ce.code)
	mockStore.AssertNotCalled(t, "UpsertLadderConfig", mock.Anything, mock.Anything)
}

// TestUpsertLadderConfig_ProviderMismatchRejected is F5: the request provider
// must match the cloud account's actual provider. The default GetCloudAccount
// mock returns provider "aws"; a body claiming "azure" must 400.
func TestUpsertLadderConfig_ProviderMismatchRejected(t *testing.T) {
	ctx := context.Background()
	handler, mockStore, _ := newLadderHandler(t)

	body := `{"cloud_account_id":"acct-1","provider":"azure","mode":"email_approval","cadence":"daily",` + ladderValidRamp + `}`
	result, err := handler.upsertLadderConfig(ctx, ladderReq(body))
	require.Error(t, err)
	assert.Nil(t, result)
	ce, ok := IsClientError(err)
	require.True(t, ok, "expected ClientError, got %T: %v", err, err)
	assert.Equal(t, 400, ce.code)
	mockStore.AssertNotCalled(t, "UpsertLadderConfig", mock.Anything, mock.Anything)
}

// TestUpsertLadderConfig_NonexistentAccountRejected is F5: a cloud_account_id
// that does not resolve must be rejected cleanly rather than reaching the store
// and surfacing a raw 500 from the FK constraint.
//
// The refusal is errNotFound (404) rather than the earlier 400 "cloud account
// does not exist" (issue #1539). requireAccountAccess answers "no such account"
// and "not yours" identically on purpose: a distinguishable 400 would let a
// scoped caller enumerate the account table by probing ids and reading which
// error came back.
func TestUpsertLadderConfig_NonexistentAccountRejected(t *testing.T) {
	ctx := context.Background()
	handler, mockStore, _ := newLadderHandler(t)
	mockStore.On("GetCloudAccount", ctx, "ghost").Return(nil, nil)

	body := `{"cloud_account_id":"ghost","provider":"aws","mode":"email_approval","cadence":"daily",` + ladderValidRamp + `}`
	result, err := handler.upsertLadderConfig(ctx, ladderReq(body))
	require.Error(t, err)
	assert.Nil(t, result)
	assert.ErrorIs(t, err, errNotFound)
	mockStore.AssertNotCalled(t, "UpsertLadderConfig", mock.Anything, mock.Anything)
}

// --- issue #1539: allowed_accounts scoping on the ladder config WRITE path ---
//
// getLadderConfigs has filtered on allowed_accounts since migration 000088
// opened view:config to scoped users; upsertLadderConfig never did. A caller
// scoped to one account could therefore write (and, because the store upserts
// on UNIQUE(cloud_account_id, provider), OVERWRITE) the ladder config of any
// other account -- and the GET then filtered the row back out, so the change
// was invisible to its author afterwards.
//
// Both directions are covered deliberately. A refusal-only test passes just as
// well against a handler that refuses everyone, which would hide the scope
// check having been wired to reject in-scope writes too.

// ladderScopedReq builds an authenticated PUT carrying body, for the principal
// scopedHandler wires (restricted to scopedInAccount).
func ladderScopedReq(body string) *events.LambdaFunctionURLRequest {
	return &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer " + scopedToken},
		Body:    body,
	}
}

// ladderScopedBody builds a valid ladder config body targeting accountID.
// mode=auto_approve with max_hourly_commit_per_run omitted (nil => no cap) is
// the shape from the issue's failure scenario.
func ladderScopedBody(accountID string) string {
	return `{"cloud_account_id":"` + accountID + `","provider":"aws","enabled":true,` +
		`"mode":"auto_approve","cadence":"daily",` + ladderValidRamp + `}`
}

// ladderScopedHandler wires a handler whose session is restricted to
// scopedInAccount, with both cloud accounts resolvable so the refusal comes
// from the scope check rather than from a failed lookup.
func ladderScopedHandler(t *testing.T) (*Handler, *MockConfigStore) {
	t.Helper()
	h, mockStore := scopedHandler(t, scopedInAccount)
	mockStore.On("GetCloudAccount", mock.Anything, scopedInAccount).
		Return(&config.CloudAccount{ID: scopedInAccount, Name: "in-scope", Provider: "aws"}, nil).Maybe()
	mockStore.On("GetCloudAccount", mock.Anything, scopedOutAccount).
		Return(&config.CloudAccount{ID: scopedOutAccount, Name: "victim-account", Provider: "aws"}, nil).Maybe()
	return h, mockStore
}

// TestUpsertLadderConfig_OutOfScopeAccountRefused is the guard: this is the
// assertion that fails if the requireAccountAccess call is removed.
func TestUpsertLadderConfig_OutOfScopeAccountRefused(t *testing.T) {
	ctx := context.Background()
	handler, mockStore := ladderScopedHandler(t)

	result, err := handler.upsertLadderConfig(ctx, ladderScopedReq(ladderScopedBody(scopedOutAccount)))

	require.Error(t, err, "a caller scoped to another account must not write this config (#1539)")
	assert.Nil(t, result)
	assert.ErrorIs(t, err, errNotFound,
		"must be the enumeration-safe not-found, not a 403 that confirms the account exists")
	mockStore.AssertNotCalled(t, "UpsertLadderConfig", mock.Anything, mock.Anything)
}

// TestUpsertLadderConfig_InScopeAccountStillAllowed is the other direction:
// the same restricted principal, the account it IS entitled to. Without this,
// a handler that refused every write would satisfy the test above.
func TestUpsertLadderConfig_InScopeAccountStillAllowed(t *testing.T) {
	ctx := context.Background()
	handler, mockStore := ladderScopedHandler(t)
	mockStore.On("UpsertLadderConfig", mock.Anything, mock.AnythingOfType("*config.LadderConfigDB")).
		Return(&config.LadderConfigDB{ID: "written-row", CloudAccountID: scopedInAccount}, nil)

	result, err := handler.upsertLadderConfig(ctx, ladderScopedReq(ladderScopedBody(scopedInAccount)))

	require.NoError(t, err, "a caller scoped to this account must still be able to write its config")
	require.NotNil(t, result)
	mockStore.AssertCalled(t, "UpsertLadderConfig")
}

// TestUpsertLadderConfig_UnrestrictedSessionUnaffected pins that the refusal
// above is driven by the caller's SCOPE and not by anything about the account
// itself: an unrestricted session writes that same account unchanged.
func TestUpsertLadderConfig_UnrestrictedSessionUnaffected(t *testing.T) {
	ctx := context.Background()
	handler, mockStore := scopedHandler(t) // no accounts => grantAdmin => unrestricted
	mockStore.On("GetCloudAccount", mock.Anything, scopedOutAccount).
		Return(&config.CloudAccount{ID: scopedOutAccount, Name: "victim-account", Provider: "aws"}, nil)
	mockStore.On("UpsertLadderConfig", mock.Anything, mock.AnythingOfType("*config.LadderConfigDB")).
		Return(&config.LadderConfigDB{ID: "written-row", CloudAccountID: scopedOutAccount}, nil)

	result, err := handler.upsertLadderConfig(ctx, ladderScopedReq(ladderScopedBody(scopedOutAccount)))

	require.NoError(t, err, "an unrestricted session must be unaffected by the #1539 scope check")
	require.NotNil(t, result)
	mockStore.AssertCalled(t, "UpsertLadderConfig")
}
