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
// that does not resolve must 400 (clean rejection) rather than reaching the
// store and surfacing a raw 500 from the FK constraint.
func TestUpsertLadderConfig_NonexistentAccountRejected(t *testing.T) {
	ctx := context.Background()
	handler, mockStore, _ := newLadderHandler(t)
	mockStore.On("GetCloudAccount", ctx, "ghost").Return(nil, nil)

	body := `{"cloud_account_id":"ghost","provider":"aws","mode":"email_approval","cadence":"daily",` + ladderValidRamp + `}`
	result, err := handler.upsertLadderConfig(ctx, ladderReq(body))
	require.Error(t, err)
	assert.Nil(t, result)
	ce, ok := IsClientError(err)
	require.True(t, ok, "expected ClientError, got %T: %v", err, err)
	assert.Equal(t, 400, ce.code)
	mockStore.AssertNotCalled(t, "UpsertLadderConfig", mock.Anything, mock.Anything)
}
