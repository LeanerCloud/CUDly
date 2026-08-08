package api

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/LeanerCloud/CUDly/internal/auth"
	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/internal/mocks"
)

// Handler-level coverage for the auto-mode write gate (issue #1765).
//
// execute:ri-exchange is carved out of admin:* (issue #1644, PR #1758) so a
// compromised admin alone cannot drain commitments. update:config is NOT carved
// out, by design -- but a single update:config write could set mode "auto" plus
// auto_exchange_enabled, after which the scheduled TaskRIExchangeReshape
// executed exchanges against the provider with no execute:ri-exchange check
// anywhere on that path. The carve-out's guarantee did not hold via that route.
//
// Two things these tests exist to pin, both of which a naive suite would miss:
//
//  1. BOTH write paths. PUT /api/config unmarshals onto the whole GlobalConfig,
//     so it reaches ri_exchange_mode / ri_exchange_enabled exactly as the
//     dedicated PUT /api/ri-exchange/config does. Every case below runs against
//     both handlers; a gate on one alone would look complete and would not be.
//
//  2. All four quadrants. A refusal-only suite passes just as well against a
//     gate that refuses every config write, which would silently strip the
//     "configures but does not execute" operator role this fix is careful to
//     preserve.

const autoGateToken = "auto-gate-token"

var (
	autoGateConfigOnly = []auth.Permission{
		{Action: auth.ActionUpdate, Resource: auth.ResourceConfig},
	}
	autoGateConfigPlusExecute = []auth.Permission{
		{Action: auth.ActionUpdate, Resource: auth.ResourceConfig},
		{Action: auth.ActionExecute, Resource: auth.ResourceRIExchange},
	}
)

// autoGateHandler wires a handler whose principal holds exactly perms, over a
// store seeded with `stored` as the pre-write config.
func autoGateHandler(t *testing.T, perms []auth.Permission, stored config.GlobalConfig) (*Handler, *mocks.MockConfigStore) {
	t.Helper()
	mockAuth := new(MockAuthService)
	t.Cleanup(func() { mockAuth.AssertExpectations(t) })
	mockAuth.On("ValidateSession", mock.Anything, autoGateToken).
		Return(&Session{UserID: "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"}, nil).Maybe()
	mockAuth.grantPermissions(perms)

	mockStore := new(MockConfigStore)
	t.Cleanup(func() { mockStore.AssertExpectations(t) })
	seeded := stored
	mockStore.On("GetGlobalConfig", mock.Anything).Return(&seeded, nil).Maybe()
	mockStore.On("SaveGlobalConfig", mock.Anything, mock.Anything).Return(nil).Maybe()

	return &Handler{config: mockStore, auth: mockAuth}, mockStore
}

func autoGateReq(body string) *events.LambdaFunctionURLRequest {
	return &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer " + autoGateToken},
		Body:    body,
	}
}

// dedicatedBody builds a body for PUT /api/ri-exchange/config. Its validate()
// only accepts "manual" or "auto".
func dedicatedBody(mode string, enabled bool, perExchange, daily float64) string {
	b, _ := json.Marshal(map[string]any{
		"mode":                         mode,
		"auto_exchange_enabled":        enabled,
		"utilization_threshold":        50.0,
		"lookback_days":                30,
		"max_payment_per_exchange_usd": perExchange,
		"max_payment_daily_usd":        daily,
	})
	return string(b)
}

// genericBody builds a partial body for PUT /api/config carrying only the
// ri_exchange_* fields, which json.Unmarshal writes onto the stored config.
func genericBody(mode string, enabled bool, perExchange, daily float64) string {
	b, _ := json.Marshal(map[string]any{
		"ri_exchange_mode":                 mode,
		"ri_exchange_enabled":              enabled,
		"ri_exchange_max_per_exchange_usd": perExchange,
		"ri_exchange_max_daily_usd":        daily,
	})
	return string(b)
}

// armed is the stored state an attacker would be escalating FROM or operating
// within: unattended execution already switched on.
func armedConfig() config.GlobalConfig {
	cfg := baseValidConfig()
	cfg.RIExchangeEnabled = true
	cfg.RIExchangeMode = "auto"
	return cfg
}

func disarmedConfig() config.GlobalConfig {
	cfg := baseValidConfig()
	cfg.RIExchangeEnabled = false
	cfg.RIExchangeMode = "manual"
	return cfg
}

// baseValidConfig is the minimum that satisfies GlobalConfig.Validate, which
// PUT /api/config runs on the merged result. Without it every case here would
// fail on an unrelated validation error rather than on the gate.
func baseValidConfig() config.GlobalConfig {
	return config.GlobalConfig{
		EnabledProviders:            []string{"aws"},
		DefaultTerm:                 3,
		DefaultCoverage:             80,
		RIExchangeMaxPerExchangeUSD: 100,
		RIExchangeMaxDailyUSD:       500,
	}
}

// wrote reports whether the write actually landed in the store.
func wrote(m *mocks.MockConfigStore) bool {
	for _, c := range m.Calls {
		if c.Method == "SaveGlobalConfig" {
			return true
		}
	}
	return false
}

// TestRIExchangeAutoModeGate covers the full four-quadrant matrix against BOTH
// write paths. Each case names the state transition rather than the endpoint,
// because the security property is a property of the transition.
func TestRIExchangeAutoModeGate(t *testing.T) {
	tests := []struct {
		name        string
		perms       []auth.Permission
		stored      config.GlobalConfig
		mode        string
		enabled     bool
		perExchange float64
		daily       float64
		wantRefused bool
	}{
		{
			name:        "arming auto with update:config alone is refused",
			perms:       autoGateConfigOnly,
			stored:      disarmedConfig(),
			mode:        "auto",
			enabled:     true,
			perExchange: 100, daily: 500,
			wantRefused: true,
		},
		{
			name:        "arming auto with execute:ri-exchange is allowed",
			perms:       autoGateConfigPlusExecute,
			stored:      disarmedConfig(),
			mode:        "auto",
			enabled:     true,
			perExchange: 100, daily: 500,
			wantRefused: false,
		},
		{
			name:        "setting manual with update:config alone is still allowed",
			perms:       autoGateConfigOnly,
			stored:      disarmedConfig(),
			mode:        "manual",
			enabled:     false,
			perExchange: 100, daily: 500,
			wantRefused: false,
		},
		{
			name:        "enabling while mode stays manual is still allowed",
			perms:       autoGateConfigOnly,
			stored:      disarmedConfig(),
			mode:        "manual",
			enabled:     true,
			perExchange: 100, daily: 500,
			wantRefused: false,
		},
		{
			name:        "raising the per-exchange cap while already armed is refused",
			perms:       autoGateConfigOnly,
			stored:      armedConfig(),
			mode:        "auto",
			enabled:     true,
			perExchange: 100_000, daily: 500,
			wantRefused: true,
		},
		{
			name:        "raising the daily cap while already armed is refused",
			perms:       autoGateConfigOnly,
			stored:      armedConfig(),
			mode:        "auto",
			enabled:     true,
			perExchange: 100, daily: 100_000,
			wantRefused: true,
		},
		{
			name:        "lowering a cap while armed is a de-escalation and stays allowed",
			perms:       autoGateConfigOnly,
			stored:      armedConfig(),
			mode:        "auto",
			enabled:     true,
			perExchange: 10, daily: 50,
			wantRefused: false,
		},
		{
			name:        "disarming back to manual while armed stays allowed",
			perms:       autoGateConfigOnly,
			stored:      armedConfig(),
			mode:        "manual",
			enabled:     true,
			perExchange: 100, daily: 500,
			wantRefused: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Run("dedicated PUT /api/ri-exchange/config", func(t *testing.T) {
				h, store := autoGateHandler(t, tt.perms, tt.stored)
				_, err := h.updateRIExchangeConfig(context.Background(),
					autoGateReq(dedicatedBody(tt.mode, tt.enabled, tt.perExchange, tt.daily)))
				assertGate(t, err, store, tt.wantRefused)
			})

			t.Run("generic PUT /api/config", func(t *testing.T) {
				h, store := autoGateHandler(t, tt.perms, tt.stored)
				_, err := h.updateConfig(context.Background(),
					autoGateReq(genericBody(tt.mode, tt.enabled, tt.perExchange, tt.daily)))
				assertGate(t, err, store, tt.wantRefused)
			})
		})
	}
}

// assertGate checks the refusal AND that the write did or did not reach the
// store. Asserting only the error would pass against a handler that returned an
// error after already saving.
func assertGate(t *testing.T, err error, store *mocks.MockConfigStore, wantRefused bool) {
	t.Helper()
	if wantRefused {
		require.Error(t, err, "arming unattended exchange without execute:ri-exchange must be refused (#1765)")
		ce, ok := IsClientError(err)
		require.True(t, ok, "a carve-out denial must be a client error, not a 500: %v", err)
		assert.Equal(t, 403, ce.code)
		assert.Contains(t, err.Error(), auth.ResourceRIExchange)
		assert.False(t, wrote(store), "a refused write must not reach the store")
		return
	}
	require.NoError(t, err, "this transition must remain available to an update:config-only operator")
	assert.True(t, wrote(store), "an allowed write must actually land")
}

// TestRIExchangeAutoModeGate_NonManualModeIsArmed is the case the obvious
// predicate misses.
//
// pkg/exchange/auto.go's processRecommendation routes to processManualExchange
// only on the literal "manual" and sends EVERY other value to
// processAutoExchange. GlobalConfig.Validate never constrains RIExchangeMode,
// and PUT /api/config unmarshals onto the struct wholesale -- so a mode of
// "Auto", "automatic" or "" arms unattended execution just as effectively as
// "auto", while a gate written as `Mode == "auto"` would wave it through.
//
// Only the generic endpoint is exercised: the dedicated one's validate()
// restricts mode to manual|auto, so this shape cannot reach it. That asymmetry
// is precisely why the gate reads the same field the scheduler reads.
func TestRIExchangeAutoModeGate_NonManualModeIsArmed(t *testing.T) {
	for _, mode := range []string{"Auto", "AUTO", "automatic", "", "enabled"} {
		t.Run("mode="+mode, func(t *testing.T) {
			h, store := autoGateHandler(t, autoGateConfigOnly, disarmedConfig())
			_, err := h.updateConfig(context.Background(),
				autoGateReq(genericBody(mode, true, 100, 500)))

			require.Error(t, err,
				"mode %q is not %q, so the scheduler runs it as auto; the gate must see the same", mode, "manual")
			ce, ok := IsClientError(err)
			require.True(t, ok, "expected a client error, got %v", err)
			assert.Equal(t, 403, ce.code)
			assert.False(t, wrote(store), "a refused write must not reach the store")
		})
	}
}

// TestRIExchangeAutoModeGate_UnrelatedConfigWriteUnaffected is the scope
// control: the gate must touch exactly the arming transition, not the rest of
// the settings surface an update:config operator owns.
func TestRIExchangeAutoModeGate_UnrelatedConfigWriteUnaffected(t *testing.T) {
	h, store := autoGateHandler(t, autoGateConfigOnly, disarmedConfig())

	_, err := h.updateConfig(context.Background(), autoGateReq(`{"laddering_enabled":true}`))

	require.NoError(t, err, "a config write that does not arm auto-exchange must be unaffected")
	assert.True(t, wrote(store), "the unrelated write must land")
}

// TestRIExchangeAutoModeGate_ArmedIdempotentRewriteAllowed pins that an
// unchanged re-save of an already-armed config is not an escalation. Without
// this, the gate would lock an update:config operator out of every subsequent
// settings save the moment auto mode was legitimately enabled by someone else.
func TestRIExchangeAutoModeGate_ArmedIdempotentRewriteAllowed(t *testing.T) {
	h, store := autoGateHandler(t, autoGateConfigOnly, armedConfig())

	_, err := h.updateConfig(context.Background(),
		autoGateReq(genericBody("auto", true, 100, 500)))

	require.NoError(t, err, "an idempotent re-write of an already-armed config is not an escalation")
	assert.True(t, wrote(store), "the write must land")
}
