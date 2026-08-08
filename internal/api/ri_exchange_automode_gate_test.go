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

// Coverage for the auto-mode write gate (issue #1765).
//
// execute:ri-exchange is carved out of admin:* (issue #1644, PR #1758) so a
// compromised admin alone cannot drain commitments. update:config is NOT carved
// out, by design -- but a single update:config write could set ri_exchange_mode
// "auto" plus ri_exchange_enabled, after which the scheduled
// TaskRIExchangeReshape executed exchanges against the provider with no
// execute:ri-exchange check anywhere on that path.
//
// Three things these tests exist to pin, each of which a plausible suite misses:
//
//  1. Routes, not handlers. Everything below goes through Router.Route. The
//     API-key bypass these tests now guard was invisible to a handler-level
//     suite, because it lives in how the ROUTE resolves a principal.
//
//  2. Both write paths. PUT /api/config unmarshals onto the whole GlobalConfig,
//     so it reaches ri_exchange_mode / ri_exchange_enabled exactly as the
//     dedicated PUT /api/ri-exchange/config does.
//
//  3. Both credential types. The two routes differ in reach -- /api/config is
//     AuthAdmin and unreachable to a user API key, so the dedicated route
//     carries the key axis alone. "Both handlers covered" was necessary and not
//     sufficient; the axis that mattered was credential, not route.
//
// And all four quadrants, because a refusal-only suite passes just as well
// against a gate that refuses every config write -- which would silently strip
// the "configures but does not execute" operator role this fix preserves.

const (
	autoGateToken  = "auto-gate-token"
	autoGateAPIKey = "auto-gate-api-key"
	autoGateUserID = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	autoGateKeyID  = "kkkkkkkk-kkkk-kkkk-kkkk-kkkkkkkkkkkk"

	dedicatedPath = "/api/ri-exchange/config"
	genericPath   = "/api/config"
)

var (
	// admin:* alone: holds update:config, denied execute:ri-exchange by the
	// carve-out. This is the #1765 principal.
	autoGateAdminOnly = []auth.Permission{
		{Action: auth.ActionAdmin, Resource: auth.ResourceAll},
	}
	// The same admin who is also an RI Exchanger.
	autoGateAdminPlusExchange = []auth.Permission{
		{Action: auth.ActionAdmin, Resource: auth.ResourceAll},
		{Action: auth.ActionExecute, Resource: auth.ResourceRIExchange},
	}
)

func baseValidConfig() config.GlobalConfig {
	return config.GlobalConfig{
		EnabledProviders:            []string{"aws"},
		DefaultTerm:                 3,
		DefaultCoverage:             80,
		RIExchangeMaxPerExchangeUSD: 100,
		RIExchangeMaxDailyUSD:       500,
	}
}

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

// autoGateStore seeds the pre-write config and records whether the write landed.
func autoGateStore(t *testing.T, stored config.GlobalConfig) *mocks.MockConfigStore {
	t.Helper()
	m := new(MockConfigStore)
	seeded := stored
	m.On("GetGlobalConfig", mock.Anything).Return(&seeded, nil).Maybe()
	m.On("SaveGlobalConfig", mock.Anything, mock.Anything).Return(nil).Maybe()
	m.On("ListServiceConfigs", mock.Anything).Return([]config.ServiceConfig{}, nil).Maybe()
	return m
}

// autoGateSessionRouter wires a Router whose caller presents a bearer token for
// a principal holding exactly perms.
func autoGateSessionRouter(t *testing.T, perms []auth.Permission, stored config.GlobalConfig) (*Router, *mocks.MockConfigStore) {
	t.Helper()
	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", mock.Anything, autoGateToken).
		Return(&Session{UserID: autoGateUserID}, nil).Maybe()
	mockAuth.grantPermissions(perms)
	store := autoGateStore(t, stored)
	return NewRouter(&Handler{config: store, auth: mockAuth}), store
}

// autoGateKeyRouter wires a Router whose caller presents a USER API KEY. keyPerms
// are the key's effective permissions; userPerms are the owning user's group
// permissions. The two differ on purpose: the gate must read the key's.
func autoGateKeyRouter(t *testing.T, keyPerms, userPerms []auth.Permission, stored config.GlobalConfig) (*Router, *mocks.MockConfigStore) {
	t.Helper()
	mockAuth := new(MockAuthService)

	mockAuth.On("ValidateUserAPIKeyAPI", mock.Anything, autoGateAPIKey).
		Return(&auth.UserAPIKey{ID: autoGateKeyID}, &auth.User{ID: autoGateUserID}, nil).Maybe()

	// The mock's HasAPIKeyPermissionAPI reads its returns via args.String, which
	// cannot take function values, so each verb is registered with literal
	// results. Registered per (action, resource) so the key's answer for
	// execute:ri-exchange is independent of its answer for update:config.
	keyCtx := &auth.AuthContext{Permissions: keyPerms}
	for _, vr := range [][2]string{
		{auth.ActionUpdate, auth.ResourceConfig},
		{auth.ActionExecute, auth.ResourceRIExchange},
	} {
		if keyCtx.HasPermission(vr[0], vr[1]) {
			mockAuth.On("HasAPIKeyPermissionAPI", mock.Anything, autoGateAPIKey, vr[0], vr[1]).
				Return(autoGateUserID, autoGateKeyID, true, nil).Maybe()
			continue
		}
		mockAuth.On("HasAPIKeyPermissionAPI", mock.Anything, autoGateAPIKey, vr[0], vr[1]).
			Return("", "", false, nil).Maybe()
	}
	// The owning user's groups are BROADER than the key: they include
	// execute:ri-exchange. A gate that resolved the verb against the USER would
	// pass here, where the key must not.
	// Signature must be exactly func(action, resource string) bool: that is what
	// permissionDecision type-asserts. Any other shape falls through to
	// args.Bool(0) and panics on the func value, so a failing case would die by
	// panic instead of by its own require.Error -- red either way, but a kill
	// that evaporates the moment someone adds a permissive stub.
	userCtx := &auth.AuthContext{Permissions: userPerms}
	mockAuth.On("HasPermissionAPI", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(func(action, resource string) bool {
			return userCtx.HasPermission(action, resource)
		}, nil).Maybe()
	mockAuth.On("GetAllowedAccountsAPI", mock.Anything, mock.Anything).Return([]string{"*"}, nil).Maybe()

	store := autoGateStore(t, stored)
	return NewRouter(&Handler{config: store, auth: mockAuth}), store
}

func autoGateTokenReq(body string) *events.LambdaFunctionURLRequest {
	return &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer " + autoGateToken},
		Body:    body,
	}
}

func autoGateKeyReq(body string) *events.LambdaFunctionURLRequest {
	return &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"x-api-key": autoGateAPIKey},
		Body:    body,
	}
}

// dedicatedBody builds a body for PUT /api/ri-exchange/config, whose validate()
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

func wrote(m *mocks.MockConfigStore) bool {
	for _, c := range m.Calls {
		if c.Method == "SaveGlobalConfig" {
			return true
		}
	}
	return false
}

// assertGate checks the outcome AND whether the write reached the store.
// Asserting only the error would pass against a handler that returned one after
// already saving.
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
	require.NoError(t, err, "this transition must remain available")
	assert.True(t, wrote(store), "an allowed write must actually land")
}

// TestRIExchangeAutoModeGate covers the four quadrants against BOTH routes,
// driven through Router.Route. The principal is admin:* -- which retains
// update:config and is denied execute:ri-exchange by the carve-out -- because
// that is the #1765 threat model and because /api/config is AuthAdmin.
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
			name:  "arming auto without execute:ri-exchange is refused",
			perms: autoGateAdminOnly, stored: disarmedConfig(),
			mode: "auto", enabled: true, perExchange: 100, daily: 500,
			wantRefused: true,
		},
		{
			name:  "arming auto with execute:ri-exchange is allowed",
			perms: autoGateAdminPlusExchange, stored: disarmedConfig(),
			mode: "auto", enabled: true, perExchange: 100, daily: 500,
			wantRefused: false,
		},
		{
			name:  "setting manual is still allowed",
			perms: autoGateAdminOnly, stored: disarmedConfig(),
			mode: "manual", enabled: false, perExchange: 100, daily: 500,
			wantRefused: false,
		},
		{
			name:  "enabling while mode stays manual is still allowed",
			perms: autoGateAdminOnly, stored: disarmedConfig(),
			mode: "manual", enabled: true, perExchange: 100, daily: 500,
			wantRefused: false,
		},
		{
			name:  "raising the per-exchange cap while already armed is refused",
			perms: autoGateAdminOnly, stored: armedConfig(),
			mode: "auto", enabled: true, perExchange: 100_000, daily: 500,
			wantRefused: true,
		},
		{
			name:  "raising the daily cap while already armed is refused",
			perms: autoGateAdminOnly, stored: armedConfig(),
			mode: "auto", enabled: true, perExchange: 100, daily: 100_000,
			wantRefused: true,
		},
		{
			name:  "lowering a cap while armed is a de-escalation and stays allowed",
			perms: autoGateAdminOnly, stored: armedConfig(),
			mode: "auto", enabled: true, perExchange: 10, daily: 50,
			wantRefused: false,
		},
		{
			name:  "disarming back to manual while armed stays allowed",
			perms: autoGateAdminOnly, stored: armedConfig(),
			mode: "manual", enabled: true, perExchange: 100, daily: 500,
			wantRefused: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Run("dedicated PUT "+dedicatedPath, func(t *testing.T) {
				r, store := autoGateSessionRouter(t, tt.perms, tt.stored)
				_, err := r.Route(context.Background(), "PUT", dedicatedPath,
					autoGateTokenReq(dedicatedBody(tt.mode, tt.enabled, tt.perExchange, tt.daily)))
				assertGate(t, err, store, tt.wantRefused)
			})

			t.Run("generic PUT "+genericPath, func(t *testing.T) {
				r, store := autoGateSessionRouter(t, tt.perms, tt.stored)
				_, err := r.Route(context.Background(), "PUT", genericPath,
					autoGateTokenReq(genericBody(tt.mode, tt.enabled, tt.perExchange, tt.daily)))
				assertGate(t, err, store, tt.wantRefused)
			})
		})
	}
}

// TestRIExchangeAutoModeGate_UserAPIKeyUsesKeyPermissions is the regression for
// the credential axis.
//
// The gate authorizes through requirePermission, which for a user API key
// resolves the verb against the KEY's effective permissions. Resolving it
// against the session's user id instead read the OWNING USER's group
// permissions, so a CI key correctly denied execute:ri-exchange could still arm
// the scheduler to execute every exchange -- the same inheritance bug
// requirePermissionConstraints guards against one function above.
//
// Only the dedicated route is exercised: /api/config is AuthAdmin and a user
// API key cannot reach it, which is why this axis is not visible from the
// generic route at all.
func TestRIExchangeAutoModeGate_UserAPIKeyUsesKeyPermissions(t *testing.T) {
	// The owning user is a full admin AND an RI Exchanger; the key is not.
	ownerPerms := autoGateAdminPlusExchange

	t.Run("key without execute:ri-exchange is refused", func(t *testing.T) {
		keyPerms := []auth.Permission{{Action: auth.ActionUpdate, Resource: auth.ResourceConfig}}
		r, store := autoGateKeyRouter(t, keyPerms, ownerPerms, disarmedConfig())

		_, err := r.Route(context.Background(), "PUT", dedicatedPath,
			autoGateKeyReq(dedicatedBody("auto", true, 100, 500)))

		require.Error(t, err,
			"a key denied execute:ri-exchange must not arm the scheduler by inheriting its owner's groups")
		ce, ok := IsClientError(err)
		require.True(t, ok, "expected a client error, got %v", err)
		assert.Equal(t, 403, ce.code)
		assert.False(t, wrote(store), "a refused write must not reach the store")
	})

	t.Run("key holding execute:ri-exchange is allowed", func(t *testing.T) {
		keyPerms := []auth.Permission{
			{Action: auth.ActionUpdate, Resource: auth.ResourceConfig},
			{Action: auth.ActionExecute, Resource: auth.ResourceRIExchange},
		}
		r, store := autoGateKeyRouter(t, keyPerms, ownerPerms, disarmedConfig())

		_, err := r.Route(context.Background(), "PUT", dedicatedPath,
			autoGateKeyReq(dedicatedBody("auto", true, 100, 500)))

		require.NoError(t, err, "a key that legitimately holds the verb must still arm auto mode")
		assert.True(t, wrote(store), "the allowed write must land")
	})
}

// TestRIExchangeAutoModeGate_NonManualModeIsArmed is the case the obvious
// predicate misses.
//
// pkg/exchange/auto.go's processRecommendation routes to processManualExchange
// only on the literal "manual" and sends EVERY other value to
// processAutoExchange. GlobalConfig.Validate never constrains RIExchangeMode,
// and PUT /api/config unmarshals onto the struct wholesale -- so a mode of
// "Auto", "automatic" or "" arms unattended execution just as effectively as
// "auto", while a gate written as `mode == "auto"` would wave it through.
//
// Only the generic route is exercised: the dedicated one's validate() restricts
// mode to manual|auto, so this shape cannot reach it. That asymmetry is exactly
// why the gate reads the field the scheduler reads.
func TestRIExchangeAutoModeGate_NonManualModeIsArmed(t *testing.T) {
	for _, mode := range []string{"Auto", "AUTO", "automatic", "", "enabled"} {
		t.Run("mode="+mode, func(t *testing.T) {
			r, store := autoGateSessionRouter(t, autoGateAdminOnly, disarmedConfig())

			_, err := r.Route(context.Background(), "PUT", genericPath,
				autoGateTokenReq(genericBody(mode, true, 100, 500)))

			require.Error(t, err,
				"mode %q is not \"manual\", so the scheduler runs it as auto; the gate must see the same", mode)
			ce, ok := IsClientError(err)
			require.True(t, ok, "expected a client error, got %v", err)
			assert.Equal(t, 403, ce.code)
			assert.False(t, wrote(store), "a refused write must not reach the store")
		})
	}
}

// TestRIExchangeAutoModeGate_UnrelatedConfigWriteUnaffected is the scope
// control: the gate must touch exactly the arming transition, not the rest of
// the settings surface.
func TestRIExchangeAutoModeGate_UnrelatedConfigWriteUnaffected(t *testing.T) {
	r, store := autoGateSessionRouter(t, autoGateAdminOnly, disarmedConfig())

	_, err := r.Route(context.Background(), "PUT", genericPath,
		autoGateTokenReq(`{"laddering_enabled":true}`))

	require.NoError(t, err, "a config write that does not arm auto-exchange must be unaffected")
	assert.True(t, wrote(store), "the unrelated write must land")
}

// TestRIExchangeAutoModeGate_ArmedIdempotentRewriteAllowed pins that an
// unchanged re-save of an already-armed config is not an escalation. Without
// this, the gate would lock an operator out of every subsequent settings save
// the moment auto mode was legitimately enabled by someone else.
func TestRIExchangeAutoModeGate_ArmedIdempotentRewriteAllowed(t *testing.T) {
	r, store := autoGateSessionRouter(t, autoGateAdminOnly, armedConfig())

	_, err := r.Route(context.Background(), "PUT", genericPath,
		autoGateTokenReq(genericBody("auto", true, 100, 500)))

	require.NoError(t, err, "an idempotent re-write of an already-armed config is not an escalation")
	assert.True(t, wrote(store), "the write must land")
}
