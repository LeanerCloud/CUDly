package api

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestHandler_getConfig(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}

	globalCfg := &config.GlobalConfig{
		EnabledProviders: []string{"aws"},
		DefaultTerm:      3,
		DefaultCoverage:  80,
	}

	serviceConfigs := []config.ServiceConfig{
		{Provider: "aws", Service: "rds", Enabled: true},
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	mockStore.On("GetGlobalConfig", ctx).Return(globalCfg, nil)
	mockStore.On("ListServiceConfigs", ctx).Return(serviceConfigs, nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
	}
	result, err := handler.getConfig(ctx, req)
	require.NoError(t, err)

	assert.NotNil(t, result.Global)
	assert.NotNil(t, result.Services)
}

func TestHandler_updateConfig(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	mockStore.On("SaveGlobalConfig", ctx, mock.AnythingOfType("*config.GlobalConfig")).Return(nil)
	// Mock ListServiceConfigs for propagation of global defaults
	mockStore.On("ListServiceConfigs", ctx).Return([]config.ServiceConfig{}, nil)
	// updateConfig now calls GetGlobalConfig when recommendations_cache_stale_hours
	// or recommendations_lookback_days is omitted from the request body, so the
	// existing persisted value can be preserved rather than zeroed out (PR #308
	// CodeRabbit pass-2). The body in this test omits both fields.
	mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{
		RecommendationsCacheStaleHours: config.DefaultRecommendationsCacheStaleHours,
		RecommendationsLookbackDays:    config.DefaultRecommendationsLookbackDays,
	}, nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	body := `{"enabled_providers": ["aws", "azure"], "default_term": 3}`
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: body,
	}
	result, err := handler.updateConfig(ctx, req)
	require.NoError(t, err)

	assert.Equal(t, "updated", result.Status)
}

// TestHandler_updateConfig_LadderingEnabledPreservation asserts the
// kill-switch preservation contract added in the ladder-schema PR:
//   - a PUT that OMITS laddering_enabled must NOT reset the persisted value
//     (the General-settings panel never sends the field, so a plain save must
//     leave a previously-enabled kill-switch on).
//   - a PUT that sends laddering_enabled:false must flip it off.
//
// Both cases capture the *config.GlobalConfig handed to SaveGlobalConfig and
// assert on the final LadderingEnabled value the store would persist.
func TestHandler_updateConfig_LadderingEnabledPreservation(t *testing.T) {
	// newHandler builds a handler whose GetGlobalConfig returns a config seeded
	// with existingLaddering, and captures the config handed to SaveGlobalConfig
	// into the returned pointer so callers can assert the persisted value.
	newHandler := func(t *testing.T, existingLaddering bool) (*Handler, *config.GlobalConfig) {
		t.Helper()
		ctx := context.Background()
		mockStore := new(MockConfigStore)
		mockAuth := new(MockAuthService)
		t.Cleanup(func() { mockStore.AssertExpectations(t); mockAuth.AssertExpectations(t) })

		adminSession := &Session{
			UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
			Email:  "admin@example.com",
		}
		mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
		mockAuth.grantAdmin()

		// The body always omits the recommendations fields, so updateConfig
		// fetches the existing config to preserve them; seed LadderingEnabled
		// there so the preservation branch has a concrete value to keep.
		mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{
			RecommendationsCacheStaleHours: config.DefaultRecommendationsCacheStaleHours,
			RecommendationsLookbackDays:    config.DefaultRecommendationsLookbackDays,
			LadderingEnabled:               existingLaddering,
		}, nil)
		mockStore.On("ListServiceConfigs", ctx).Return([]config.ServiceConfig{}, nil)

		var saved config.GlobalConfig
		mockStore.On("SaveGlobalConfig", ctx, mock.AnythingOfType("*config.GlobalConfig")).
			Run(func(args mock.Arguments) {
				saved = *args.Get(1).(*config.GlobalConfig)
			}).Return(nil)

		return &Handler{config: mockStore, auth: mockAuth}, &saved
	}

	makeReq := func(body string) *events.LambdaFunctionURLRequest {
		return &events.LambdaFunctionURLRequest{
			Headers: map[string]string{"Authorization": "Bearer admin-token"},
			Body:    body,
		}
	}

	const bodyOmitted = `{"enabled_providers": ["aws"], "default_term": 3}`
	bodyWith := func(v bool) string {
		return fmt.Sprintf(`{"enabled_providers": ["aws"], "default_term": 3, "laddering_enabled": %t}`, v)
	}

	cases := []struct {
		name          string
		body          string
		existing      bool
		wantPersisted bool
	}{
		{"omitted preserves existing true", bodyOmitted, true, true},
		{"omitted preserves existing false", bodyOmitted, false, false},
		{"explicit false flips existing true off", bodyWith(false), true, false},
		{"explicit true flips existing false on", bodyWith(true), false, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			handler, saved := newHandler(t, tc.existing)

			result, err := handler.updateConfig(ctx, makeReq(tc.body))
			require.NoError(t, err)
			assert.Equal(t, "updated", result.Status)
			assert.Equal(t, tc.wantPersisted, saved.LadderingEnabled,
				"persisted laddering_enabled mismatch for case %q", tc.name)
		})
	}
}

// TestHandler_updateConfig_PartialPUTPreservesOmittedFields is the F2
// data-loss regression test. The laddering kill-switch toggle sends a partial
// PUT of only {"laddering_enabled": true}; before the merge fix, updateConfig
// unmarshalled that into a zero GlobalConfig and only restored the four
// recommendation/laddering fields, silently zeroing approval_required,
// default_ramp_schedule, and every ri_exchange_* field (wiping the user's
// RI-exchange automation). The merge must leave every omitted field at its
// stored value while applying the one field the body carries. Assert against
// the config handed to SaveGlobalConfig, not just a 200.
func TestHandler_updateConfig_PartialPUTPreservesOmittedFields(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	t.Cleanup(func() { mockStore.AssertExpectations(t); mockAuth.AssertExpectations(t) })

	mockAuth.On("ValidateSession", ctx, "admin-token").
		Return(&Session{UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", Email: "admin@example.com"}, nil)
	mockAuth.grantAdmin()

	notifyEmail := "ops@example.com"
	existing := &config.GlobalConfig{
		EnabledProviders:               []string{"aws"},
		NotificationEmail:              &notifyEmail,
		AutoCollect:                    true,
		CollectionSchedule:             "daily",
		NotificationDaysBefore:         3,
		ApprovalRequired:               true,
		DefaultTerm:                    3,
		DefaultPayment:                 "all-upfront",
		DefaultCoverage:                80,
		DefaultRampSchedule:            "immediate",
		RIExchangeEnabled:              true,
		RIExchangeMode:                 "automatic",
		RIExchangeUtilizationThreshold: 95,
		RIExchangeMaxPerExchangeUSD:    1000,
		RIExchangeMaxDailyUSD:          5000,
		RIExchangeLookbackDays:         30,
		RecommendationsCacheStaleHours: 24,
		RecommendationsLookbackDays:    7,
		PurchaseDelayHours:             48,
		LadderingEnabled:               false,
	}
	mockStore.On("GetGlobalConfig", ctx).Return(existing, nil)

	var saved config.GlobalConfig
	mockStore.On("SaveGlobalConfig", ctx, mock.AnythingOfType("*config.GlobalConfig")).
		Run(func(args mock.Arguments) { saved = *args.Get(1).(*config.GlobalConfig) }).
		Return(nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
		Body:    `{"laddering_enabled": true}`,
	}
	result, err := handler.updateConfig(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, "updated", result.Status)

	// F3: a PUT that carries none of the global defaults must NOT trigger the
	// service-config propagation loop (which would overwrite per-service
	// term/payment/coverage/ramp customizations).
	mockStore.AssertNotCalled(t, "ListServiceConfigs", mock.Anything)
	mockStore.AssertNotCalled(t, "SaveServiceConfig", mock.Anything, mock.Anything)

	// The one field the body carried was applied.
	assert.True(t, saved.LadderingEnabled, "laddering_enabled from the body must be applied")

	// Every omitted field must retain its stored value (the F2 bug zeroed these).
	assert.True(t, saved.ApprovalRequired, "approval_required must be preserved")
	assert.Equal(t, "immediate", saved.DefaultRampSchedule, "default_ramp_schedule must be preserved")
	assert.True(t, saved.RIExchangeEnabled, "ri_exchange_enabled must be preserved")
	assert.Equal(t, "automatic", saved.RIExchangeMode, "ri_exchange_mode must be preserved")
	assert.Equal(t, 95.0, saved.RIExchangeUtilizationThreshold, "ri_exchange_utilization_threshold must be preserved")
	assert.Equal(t, 1000.0, saved.RIExchangeMaxPerExchangeUSD, "ri_exchange_max_per_exchange_usd must be preserved")
	assert.Equal(t, 5000.0, saved.RIExchangeMaxDailyUSD, "ri_exchange_max_daily_usd must be preserved")
	assert.Equal(t, 30, saved.RIExchangeLookbackDays, "ri_exchange_lookback_days must be preserved")

	// Other scalars stay intact too.
	assert.Equal(t, []string{"aws"}, saved.EnabledProviders)
	assert.Equal(t, "all-upfront", saved.DefaultPayment)
	assert.Equal(t, 80.0, saved.DefaultCoverage)
	assert.Equal(t, 48, saved.PurchaseDelayHours)
	require.NotNil(t, saved.NotificationEmail)
	assert.Equal(t, "ops@example.com", *saved.NotificationEmail)
}

// TestHandler_updateConfig_UsesAtomicPath asserts updateConfig performs its
// read-modify-write through UpdateGlobalConfigAtomic (the serialized,
// advisory-locked store path) rather than a separate Get + Save, which is what
// prevents the concurrent-partial-PUT lost update. The explicit expectation
// means the mock dispatches through Called (it does NOT fall back to the
// Get+Save emulation), so a passing run proves the handler invoked the atomic
// method; the Run callback exercises the exact apply closure the handler builds.
func TestHandler_updateConfig_UsesAtomicPath(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	t.Cleanup(func() { mockStore.AssertExpectations(t); mockAuth.AssertExpectations(t) })

	mockAuth.On("ValidateSession", ctx, "admin-token").
		Return(&Session{UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", Email: "admin@example.com"}, nil)
	mockAuth.grantAdmin()

	// The config the atomic apply runs against (as if freshly read under lock).
	stored := &config.GlobalConfig{
		EnabledProviders:               []string{"aws"},
		ApprovalRequired:               true,
		DefaultTerm:                    3,
		DefaultPayment:                 "all-upfront",
		DefaultCoverage:                80,
		CollectionSchedule:             "daily",
		NotificationDaysBefore:         3,
		RIExchangeEnabled:              true,
		RIExchangeMode:                 "manual",
		RIExchangeLookbackDays:         30,
		RecommendationsCacheStaleHours: 24,
		RecommendationsLookbackDays:    7,
		LadderingEnabled:               false,
	}

	var merged config.GlobalConfig
	mockStore.On("UpdateGlobalConfigAtomic", ctx, mock.AnythingOfType("func(*config.GlobalConfig) error")).
		Run(func(args mock.Arguments) {
			apply := args.Get(1).(func(*config.GlobalConfig) error)
			cfg := *stored
			require.NoError(t, apply(&cfg))
			merged = cfg
		}).
		Return(&merged, nil)

	handler := &Handler{config: mockStore, auth: mockAuth}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
		Body:    `{"laddering_enabled": true}`,
	}
	result, err := handler.updateConfig(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, "updated", result.Status)

	// laddering-only PUT carries no global defaults, so propagation is skipped.
	mockStore.AssertNotCalled(t, "ListServiceConfigs", mock.Anything)

	// The apply closure merged the body over the stored config: the sent field
	// is applied and omitted fields are preserved.
	assert.True(t, merged.LadderingEnabled, "sent field applied")
	assert.True(t, merged.ApprovalRequired, "omitted approval_required preserved")
	assert.True(t, merged.RIExchangeEnabled, "omitted ri_exchange_enabled preserved")
}

// TestHandler_updateConfig_GracePeriodDaysReplaceNotMerge is the F4 regression.
// json.Unmarshal into a pre-populated (non-nil) map MERGES keys, so a PUT that
// carries grace_period_days could never delete a previously-set provider key.
// When the caller sends the key, updateConfig nils the stored map first so the
// body's map REPLACES it wholesale. Sending only { "aws": 10 } must drop the
// prior azure/gcp entries.
func TestHandler_updateConfig_GracePeriodDaysReplaceNotMerge(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)
	t.Cleanup(func() { mockStore.AssertExpectations(t); mockAuth.AssertExpectations(t) })

	mockAuth.On("ValidateSession", ctx, "admin-token").
		Return(&Session{UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", Email: "admin@example.com"}, nil)
	mockAuth.grantAdmin()

	mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{
		EnabledProviders:               []string{"aws"},
		DefaultTerm:                    3,
		DefaultPayment:                 "all-upfront",
		DefaultCoverage:                80,
		CollectionSchedule:             "daily",
		NotificationDaysBefore:         3,
		RecommendationsCacheStaleHours: 24,
		RecommendationsLookbackDays:    7,
		GracePeriodDays:                map[string]int{"aws": 7, "azure": 14, "gcp": 3},
	}, nil)

	var saved config.GlobalConfig
	mockStore.On("SaveGlobalConfig", ctx, mock.AnythingOfType("*config.GlobalConfig")).
		Run(func(args mock.Arguments) { saved = *args.Get(1).(*config.GlobalConfig) }).
		Return(nil)

	handler := &Handler{config: mockStore, auth: mockAuth}
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
		Body:    `{"grace_period_days": {"aws": 10}}`,
	}
	_, err := handler.updateConfig(ctx, req)
	require.NoError(t, err)

	assert.Equal(t, map[string]int{"aws": 10}, saved.GracePeriodDays,
		"grace_period_days must be replaced wholesale, not merged (azure/gcp dropped)")
	// grace_period_days is not a propagation default, so propagation is skipped.
	mockStore.AssertNotCalled(t, "ListServiceConfigs", mock.Anything)
}

// serializedConfigStore is a concurrency-test store double. Its
// UpdateGlobalConfigAtomic guards the read-modify-write with a real mutex,
// modeling the transaction-scoped advisory lock that
// PostgresStore.UpdateGlobalConfigAtomic takes. It embeds *MockConfigStore only
// to satisfy the rest of StoreInterface; updateConfig calls just the two
// methods overridden here.
type serializedConfigStore struct {
	*MockConfigStore
	stored *config.GlobalConfig
	mu     sync.Mutex
}

func (s *serializedConfigStore) UpdateGlobalConfigAtomic(_ context.Context, apply func(*config.GlobalConfig) error) (*config.GlobalConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg := *s.stored // read the current committed state under the lock
	if err := apply(&cfg); err != nil {
		return nil, err
	}
	s.stored = &cfg // commit before releasing the lock
	return &cfg, nil
}

func (s *serializedConfigStore) ListServiceConfigs(_ context.Context) ([]config.ServiceConfig, error) {
	return []config.ServiceConfig{}, nil
}

// TestHandler_updateConfig_ConcurrentPartialPUTsNoLostUpdate is the F2
// concurrency regression test (run under -race). Two overlapping partial PUTs
// touch DIFFERENT fields; with the serialized read-modify-write (the store's
// mutex models the advisory lock PostgresStore.UpdateGlobalConfigAtomic takes)
// BOTH updates must survive. A non-atomic Get + merge + Save (the pre-fix path)
// could read the same stale base in both goroutines and let the later save drop
// the earlier change. Coordination uses a WaitGroup + channel (no sleeps); only
// the test goroutine calls require.
func TestHandler_updateConfig_ConcurrentPartialPUTsNoLostUpdate(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	mockAuth.On("ValidateSession", ctx, "admin-token").
		Return(&Session{UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", Email: "admin@example.com"}, nil)
	mockAuth.grantAdmin()

	store := &serializedConfigStore{
		MockConfigStore: new(MockConfigStore),
		stored: &config.GlobalConfig{
			EnabledProviders:               []string{"aws"},
			ApprovalRequired:               true,
			DefaultTerm:                    3,
			DefaultPayment:                 "all-upfront",
			DefaultCoverage:                80,
			CollectionSchedule:             "daily",
			NotificationDaysBefore:         3,
			RIExchangeEnabled:              true,
			RIExchangeMode:                 "manual",
			RIExchangeLookbackDays:         30,
			RecommendationsCacheStaleHours: 24,
			RecommendationsLookbackDays:    7,
		},
	}
	handler := &Handler{config: store, auth: mockAuth}

	makeReq := func(body string) *events.LambdaFunctionURLRequest {
		return &events.LambdaFunctionURLRequest{
			Headers: map[string]string{"Authorization": "Bearer admin-token"},
			Body:    body,
		}
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 2)
	for _, body := range []string{
		`{"approval_required": false}`,
		`{"ri_exchange_max_daily_usd": 12345}`,
	} {
		wg.Add(1)
		go func(b string) {
			defer wg.Done()
			_, err := handler.updateConfig(ctx, makeReq(b))
			errCh <- err
		}(body)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		require.NoError(t, err)
	}

	// Both concurrent partial updates survived (no lost update).
	assert.False(t, store.stored.ApprovalRequired, "approval_required update survived")
	assert.Equal(t, 12345.0, store.stored.RIExchangeMaxDailyUSD, "ri_exchange update survived")
}

func TestHandler_updateConfig_InvalidBody(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()

	handler := &Handler{corsAllowedOrigin: "*", auth: mockAuth}

	body := `{invalid json}`
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: body,
	}
	result, err := handler.updateConfig(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "invalid request body")
}

func TestHandler_getServiceConfig(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}

	serviceCfg := &config.ServiceConfig{
		Provider: "aws",
		Service:  "rds",
		Enabled:  true,
		Term:     3,
		Coverage: 80,
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	mockStore.On("GetServiceConfig", ctx, "aws", "rds").Return(serviceCfg, nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
	}
	result, err := handler.getServiceConfig(ctx, req, "aws/rds")
	require.NoError(t, err)

	cfg := result.(*config.ServiceConfig)
	assert.Equal(t, "aws", cfg.Provider)
	assert.Equal(t, "rds", cfg.Service)
}

func TestHandler_getServiceConfig_InvalidFormat(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	adminSession := &Session{UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()

	handler := &Handler{corsAllowedOrigin: "*", auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
	}
	result, err := handler.getServiceConfig(ctx, req, "invalid-format")
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "invalid service path")
}

func TestHandler_getServiceConfig_NotFound(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	mockStore.On("GetServiceConfig", ctx, "aws", "unknown").Return(nil, nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
	}
	result, err := handler.getServiceConfig(ctx, req, "aws/unknown")
	require.NoError(t, err)

	// Returns empty response for not found
	_, ok := result.(*EmptyServiceConfigResponse)
	assert.True(t, ok)
}

func TestHandler_updateServiceConfig(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	mockStore.On("GetServiceConfig", ctx, "aws", "rds").Return(nil, nil)
	mockStore.On("SaveServiceConfig", ctx, mock.AnythingOfType("*config.ServiceConfig")).Return(nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	body := `{"enabled": true, "term": 3, "coverage": 80}`
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: body,
	}
	result, err := handler.updateServiceConfig(ctx, req, "aws/rds")
	require.NoError(t, err)

	assert.Equal(t, "updated", result.Status)
}

// TestMergeServiceConfig_PresenceAwareFilterOverlay verifies that the
// recommendation-filter fields are overlaid from the request only when the
// body actually carries the key, while the four scalar UI fields are always
// overlaid. A partial PUT that omits a filter must preserve the existing
// value; a PUT that includes it (even empty) must apply it.
func TestMergeServiceConfig_PresenceAwareFilterOverlay(t *testing.T) {
	ctx := context.Background()
	// Each subtest gets a fresh existing record: mergeServiceConfig overlays
	// onto the pointer returned by GetServiceConfig (the production postgres
	// store returns a fresh struct per call), so a shared fixture would leak
	// mutations across subtests.
	newExisting := func() *config.ServiceConfig {
		return &config.ServiceConfig{
			Provider: "aws", Service: "rds", Enabled: true, Term: 3,
			Payment: "all-upfront", Coverage: 50,
			IncludeEngines: []string{"mysql"},
			ExcludeTypes:   []string{"db.t2.micro"},
			MinCount:       4,
		}
	}

	t.Run("body includes filter fields -> overlaid", func(t *testing.T) {
		store := new(MockConfigStore)
		store.On("GetServiceConfig", ctx, "aws", "rds").Return(newExisting(), nil)
		t.Cleanup(func() { store.AssertExpectations(t) })

		req := config.ServiceConfig{
			Provider: "aws", Service: "rds", Enabled: false, Term: 1,
			Payment: "no-upfront", Coverage: 90,
			IncludeEngines: []string{"postgres"},
			MinCount:       7,
		}
		body := `{"enabled":false,"term":1,"payment":"no-upfront","coverage":90,"include_engines":["postgres"],"min_count":7}`

		merged, err := mergeServiceConfig(ctx, store, req, body)
		require.NoError(t, err)
		assert.False(t, merged.Enabled)
		assert.Equal(t, 1, merged.Term)
		assert.Equal(t, 90.0, merged.Coverage)
		assert.Equal(t, []string{"postgres"}, merged.IncludeEngines)
		assert.Equal(t, 7, merged.MinCount)
		// exclude_types absent from body -> preserved from existing
		assert.Equal(t, []string{"db.t2.micro"}, merged.ExcludeTypes)
	})

	t.Run("body omits filter fields -> preserved", func(t *testing.T) {
		store := new(MockConfigStore)
		store.On("GetServiceConfig", ctx, "aws", "rds").Return(newExisting(), nil)
		t.Cleanup(func() { store.AssertExpectations(t) })

		req := config.ServiceConfig{
			Provider: "aws", Service: "rds", Enabled: true, Term: 3,
			Payment: "all-upfront", Coverage: 80,
		}
		body := `{"enabled":true,"term":3,"payment":"all-upfront","coverage":80}`

		merged, err := mergeServiceConfig(ctx, store, req, body)
		require.NoError(t, err)
		assert.Equal(t, 80.0, merged.Coverage)
		assert.Equal(t, []string{"mysql"}, merged.IncludeEngines, "omitted filter must be preserved")
		assert.Equal(t, []string{"db.t2.micro"}, merged.ExcludeTypes)
		assert.Equal(t, 4, merged.MinCount, "omitted min_count must be preserved")
	})

	t.Run("body includes empty filter -> cleared", func(t *testing.T) {
		store := new(MockConfigStore)
		store.On("GetServiceConfig", ctx, "aws", "rds").Return(newExisting(), nil)
		t.Cleanup(func() { store.AssertExpectations(t) })

		req := config.ServiceConfig{
			Provider: "aws", Service: "rds", Enabled: true, Term: 3,
			Payment: "all-upfront", Coverage: 80,
			IncludeEngines: []string{},
		}
		body := `{"enabled":true,"term":3,"payment":"all-upfront","coverage":80,"include_engines":[]}`

		merged, err := mergeServiceConfig(ctx, store, req, body)
		require.NoError(t, err)
		assert.Empty(t, merged.IncludeEngines, "explicit empty list clears the filter")
	})
}

func TestHandler_updateServiceConfig_InvalidBody(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()

	handler := &Handler{corsAllowedOrigin: "*", auth: mockAuth}

	body := `{invalid json}`
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: body,
	}
	result, err := handler.updateServiceConfig(ctx, req, "aws/rds")
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "invalid request body")
}

func TestHandler_updateServiceConfig_CommitmentOptsReject(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	mockStore.On("GetServiceConfig", ctx, "aws", "rds").Return(nil, nil)

	// Probe data says RDS 3yr no-upfront doesn't exist. Save must 400.
	// SaveServiceConfig is NOT set up — asserting it's never called.
	handler := &Handler{
		config: mockStore,
		auth:   mockAuth,
		commitmentOpts: &stubCommitmentOpts{
			validateFn: func(_ context.Context, provider, service string, term int, payment string) (bool, error) {
				assert.Equal(t, "aws", provider)
				assert.Equal(t, "rds", service)
				assert.Equal(t, 3, term)
				assert.Equal(t, "no-upfront", payment)
				return false, nil
			},
		},
	}

	body := `{"enabled": true, "term": 3, "payment": "no-upfront", "coverage": 80}`
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
		Body:    body,
	}
	result, err := handler.updateServiceConfig(ctx, req, "aws/rds")

	require.Error(t, err)
	assert.Nil(t, result)
	ce, ok := IsClientError(err)
	require.True(t, ok)
	assert.Equal(t, 400, ce.code)
	assert.Contains(t, ce.message, "3yr no-upfront")
	mockStore.AssertNotCalled(t, "SaveServiceConfig", mock.Anything, mock.Anything)
}

func TestHandler_updateServiceConfig_CommitmentOptsAccept(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	mockStore.On("GetServiceConfig", ctx, "aws", "rds").Return(nil, nil)
	mockStore.On("SaveServiceConfig", ctx, mock.AnythingOfType("*config.ServiceConfig")).Return(nil)

	handler := &Handler{
		config: mockStore,
		auth:   mockAuth,
		commitmentOpts: &stubCommitmentOpts{
			validateFn: func(context.Context, string, string, int, string) (bool, error) {
				return true, nil
			},
		},
	}

	body := `{"enabled": true, "term": 1, "payment": "all-upfront", "coverage": 80}`
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
		Body:    body,
	}
	result, err := handler.updateServiceConfig(ctx, req, "aws/rds")

	require.NoError(t, err)
	assert.Equal(t, "updated", result.Status)
}

func TestHandler_updateServiceConfig_NoSlash(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()

	handler := &Handler{config: mockStore, auth: mockAuth}

	// Without proper format (no slash), provider won't be set and validation should fail
	body := `{"enabled": true}`
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: body,
	}
	result, err := handler.updateServiceConfig(ctx, req, "invalid")
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "invalid service path")
}

func TestHandler_getConfig_GlobalConfigError(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	mockStore.On("GetGlobalConfig", ctx).Return(nil, assert.AnError)

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
	}
	result, err := handler.getConfig(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestHandler_getConfig_ListServiceConfigsError(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()

	globalCfg := &config.GlobalConfig{
		EnabledProviders: []string{"aws"},
	}

	mockStore.On("GetGlobalConfig", ctx).Return(globalCfg, nil)
	mockStore.On("ListServiceConfigs", ctx).Return(nil, assert.AnError)

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
	}
	result, err := handler.getConfig(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
}

// Regression tests for issue #407: SourceIdentity (cloud account ID, Azure
// tenant ID) must only be included in responses for admin sessions.

func TestHandler_getConfig_SourceIdentity_AdminOnly(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{UserID: "admin-user"}
	userSession := &Session{UserID: "regular-user"}

	globalCfg := &config.GlobalConfig{EnabledProviders: []string{"aws"}}
	mockStore.On("GetGlobalConfig", ctx).Return(globalCfg, nil)
	mockStore.On("ListServiceConfigs", ctx).Return([]config.ServiceConfig{}, nil)
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.On("ValidateSession", ctx, "user-token").Return(userSession, nil)
	mockAuth.On("HasPermissionAPI", mock.Anything, "admin-user", mock.Anything, mock.Anything).Return(true, nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	t.Run("admin sees SourceIdentity", func(t *testing.T) {
		req := &events.LambdaFunctionURLRequest{
			Headers: map[string]string{"Authorization": "Bearer admin-token"},
		}
		result, err := handler.getConfig(ctx, req)
		require.NoError(t, err)
		// resolveSourceIdentity always returns a non-nil struct (best-effort,
		// returns an empty struct on failure). The key invariant is that admin
		// sessions receive the field and non-admin sessions do not.
		require.NotNil(t, result.SourceIdentity)
	})

	t.Run("regression #407: non-admin does not receive SourceIdentity", func(t *testing.T) {
		// The regular user has view:config permission (passes requirePermission)
		// but does not hold admin:* (requireAdmin fails), so SourceIdentity is
		// withheld. We match the specific permission verbs so the admin:* call
		// (action="admin", resource="*") still returns false.
		mockAuth.On("HasPermissionAPI", ctx, "regular-user", "view", "config").Return(true, nil)
		mockAuth.On("HasPermissionAPI", ctx, "regular-user", mock.AnythingOfType("string"), mock.AnythingOfType("string")).Return(false, nil).Maybe()
		req := &events.LambdaFunctionURLRequest{
			Headers: map[string]string{"Authorization": "Bearer user-token"},
		}
		result, err := handler.getConfig(ctx, req)
		require.NoError(t, err)
		assert.Nil(t, result.SourceIdentity,
			"SourceIdentity must be nil for non-admin sessions (issue #407)")
	})
}

func TestHandler_getServiceConfig_Error(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	mockStore.On("GetServiceConfig", ctx, "aws", "rds").Return(nil, assert.AnError)

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
	}
	result, err := handler.getServiceConfig(ctx, req, "aws/rds")
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestHandler_updateConfig_ValidationError(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	// updateConfig calls GetGlobalConfig before validation to preserve persisted
	// values for fields omitted from the request body (PR #308 CR pass-2). The
	// validation error fires after the merge, so the mock is still required.
	mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{
		RecommendationsCacheStaleHours: config.DefaultRecommendationsCacheStaleHours,
		RecommendationsLookbackDays:    config.DefaultRecommendationsLookbackDays,
	}, nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	// Invalid config - negative coverage percentage
	body := `{"enabled_providers": ["aws"], "default_coverage": -10}`
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: body,
	}
	result, err := handler.updateConfig(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "validation error")
}

func TestHandler_updateConfig_SaveError(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	mockStore.On("SaveGlobalConfig", ctx, mock.AnythingOfType("*config.GlobalConfig")).Return(assert.AnError)
	// updateConfig calls GetGlobalConfig before save to preserve persisted
	// values for fields omitted from the request body (PR #308 CR pass-2).
	mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{
		RecommendationsCacheStaleHours: config.DefaultRecommendationsCacheStaleHours,
		RecommendationsLookbackDays:    config.DefaultRecommendationsLookbackDays,
	}, nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	body := `{"enabled_providers": ["aws"], "default_term": 3}`
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: body,
	}
	result, err := handler.updateConfig(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestHandler_updateServiceConfig_SaveError(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	mockStore.On("GetServiceConfig", ctx, "aws", "rds").Return(nil, nil)
	mockStore.On("SaveServiceConfig", ctx, mock.AnythingOfType("*config.ServiceConfig")).Return(assert.AnError)

	handler := &Handler{config: mockStore, auth: mockAuth}

	body := `{"enabled": true, "term": 3, "coverage": 80}`
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: body,
	}
	result, err := handler.updateServiceConfig(ctx, req, "aws/rds")
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestHandler_getServiceConfig_InvalidProvider(t *testing.T) {
	ctx := context.Background()
	mockAuth := new(MockAuthService)
	adminSession := &Session{UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}
	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()

	handler := &Handler{auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer admin-token"},
	}
	result, err := handler.getServiceConfig(ctx, req, "invalid/rds")
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "invalid provider")
}

func TestHandler_updateConfig_WithPropagation(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}

	serviceConfigs := []config.ServiceConfig{
		{Provider: "aws", Service: "rds", Enabled: true},
		{Provider: "aws", Service: "ec2", Enabled: true},
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	mockStore.On("SaveGlobalConfig", ctx, mock.AnythingOfType("*config.GlobalConfig")).Return(nil)
	// updateConfig calls GetGlobalConfig before save to preserve persisted
	// values for fields omitted from the request body (PR #308 CR pass-2).
	mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{
		RecommendationsCacheStaleHours: config.DefaultRecommendationsCacheStaleHours,
		RecommendationsLookbackDays:    config.DefaultRecommendationsLookbackDays,
	}, nil)
	mockStore.On("ListServiceConfigs", ctx).Return(serviceConfigs, nil)
	mockStore.On("SaveServiceConfig", ctx, mock.AnythingOfType("*config.ServiceConfig")).Return(nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	body := `{"enabled_providers": ["aws"], "default_term": 3, "default_coverage": 80}`
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: body,
	}
	result, err := handler.updateConfig(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, "updated", result.Status)

	// Verify SaveServiceConfig was called for each service
	mockStore.AssertNumberOfCalls(t, "SaveServiceConfig", 2)
}

func TestHandler_updateConfig_PropagationServiceSaveError(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}

	serviceConfigs := []config.ServiceConfig{
		{Provider: "aws", Service: "rds", Enabled: true},
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	mockStore.On("SaveGlobalConfig", ctx, mock.AnythingOfType("*config.GlobalConfig")).Return(nil)
	// updateConfig calls GetGlobalConfig before save to preserve persisted
	// values for fields omitted from the request body (PR #308 CR pass-2).
	mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{
		RecommendationsCacheStaleHours: config.DefaultRecommendationsCacheStaleHours,
		RecommendationsLookbackDays:    config.DefaultRecommendationsLookbackDays,
	}, nil)
	mockStore.On("ListServiceConfigs", ctx).Return(serviceConfigs, nil)
	// Simulate failure when saving service config during propagation
	mockStore.On("SaveServiceConfig", ctx, mock.AnythingOfType("*config.ServiceConfig")).Return(assert.AnError)

	handler := &Handler{config: mockStore, auth: mockAuth}

	body := `{"enabled_providers": ["aws"], "default_term": 3}`
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: body,
	}
	// Should still succeed even if service config propagation fails
	result, err := handler.updateConfig(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, "updated", result.Status)
}

func TestHandler_updateConfig_PropagationListError(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	adminSession := &Session{
		UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Email:  "admin@example.com",
	}

	mockAuth.On("ValidateSession", ctx, "admin-token").Return(adminSession, nil)
	mockAuth.grantAdmin()
	mockStore.On("SaveGlobalConfig", ctx, mock.AnythingOfType("*config.GlobalConfig")).Return(nil)
	// updateConfig calls GetGlobalConfig before save to preserve persisted
	// values for fields omitted from the request body (PR #308 CR pass-2).
	mockStore.On("GetGlobalConfig", ctx).Return(&config.GlobalConfig{
		RecommendationsCacheStaleHours: config.DefaultRecommendationsCacheStaleHours,
		RecommendationsLookbackDays:    config.DefaultRecommendationsLookbackDays,
	}, nil)
	// Simulate failure when listing service configs for propagation
	mockStore.On("ListServiceConfigs", ctx).Return(nil, assert.AnError)

	handler := &Handler{config: mockStore, auth: mockAuth}

	body := `{"enabled_providers": ["aws"], "default_term": 3}`
	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{
			"Authorization": "Bearer admin-token",
		},
		Body: body,
	}
	// Should still succeed even if listing fails - global config was saved
	result, err := handler.updateConfig(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, "updated", result.Status)
}

// Regression tests for 02-M4: GET /api/config and GET /api/config/service/*
// must enforce requirePermission("view","config") and return 403 to callers
// who lack that permission, even though the route is only AuthUser-gated.

func TestHandler_getConfig_ViewConfigPermission_Enforced(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	userSession := &Session{
		UserID: "cccccccc-cccc-cccc-cccc-cccccccccccc",
		Email:  "viewer@example.com",
	}
	// User has a valid session but view:config is revoked.
	mockAuth.On("ValidateSession", ctx, "user-token").Return(userSession, nil)
	mockAuth.On("HasPermissionAPI", ctx, userSession.UserID, "view", "config").Return(false, nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer user-token"},
	}
	result, err := handler.getConfig(ctx, req)
	require.Error(t, err, "must be rejected when view:config is revoked")
	assert.Nil(t, result)
	ce, ok := IsClientError(err)
	require.True(t, ok, "expected ClientError, got %T: %v", err, err)
	assert.Equal(t, 403, ce.code, "expected 403, got %d", ce.code)
	mockStore.AssertNotCalled(t, "GetGlobalConfig", mock.Anything)
}

func TestHandler_getServiceConfig_ViewConfigPermission_Enforced(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockAuth := new(MockAuthService)

	userSession := &Session{
		UserID: "cccccccc-cccc-cccc-cccc-cccccccccccc",
		Email:  "viewer@example.com",
	}
	// User has a valid session but view:config is revoked.
	mockAuth.On("ValidateSession", ctx, "user-token").Return(userSession, nil)
	mockAuth.On("HasPermissionAPI", ctx, userSession.UserID, "view", "config").Return(false, nil)

	handler := &Handler{config: mockStore, auth: mockAuth}

	req := &events.LambdaFunctionURLRequest{
		Headers: map[string]string{"Authorization": "Bearer user-token"},
	}
	result, err := handler.getServiceConfig(ctx, req, "aws/rds")
	require.Error(t, err, "must be rejected when view:config is revoked")
	assert.Nil(t, result)
	ce, ok := IsClientError(err)
	require.True(t, ok, "expected ClientError, got %T: %v", err, err)
	assert.Equal(t, 403, ce.code, "expected 403, got %d", ce.code)
	mockStore.AssertNotCalled(t, "GetServiceConfig", mock.Anything, mock.Anything, mock.Anything)
}
