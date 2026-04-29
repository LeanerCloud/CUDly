package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockOverrideStore is a small in-memory stand-in for the scheduler's
// StoreInterface, exercising only the methods that ListRecommendations'
// override path touches. Mirrors the pattern used by mockSuppressionStore
// in scheduler_suppressions_test.go to keep tests self-contained.
type mockOverrideStore struct {
	MockConfigStore
	recs           []config.RecommendationRecord
	globals        map[string]*config.ServiceConfig          // key: provider|service
	overrides      map[string]*config.AccountServiceOverride // key: account|provider|service
	getGlobalErr   error
	getOverrideErr error
}

func (m *mockOverrideStore) ListStoredRecommendations(_ context.Context, _ config.RecommendationFilter) ([]config.RecommendationRecord, error) {
	return m.recs, nil
}
func (m *mockOverrideStore) ListActiveSuppressions(_ context.Context) ([]config.PurchaseSuppression, error) {
	return nil, nil
}
func (m *mockOverrideStore) GetRecommendationsFreshness(_ context.Context) (*config.RecommendationsFreshness, error) {
	now := time.Now()
	return &config.RecommendationsFreshness{LastCollectedAt: &now}, nil
}
func (m *mockOverrideStore) GetServiceConfig(_ context.Context, provider, service string) (*config.ServiceConfig, error) {
	if m.getGlobalErr != nil {
		return nil, m.getGlobalErr
	}
	return m.globals[provider+"|"+service], nil
}
func (m *mockOverrideStore) GetAccountServiceOverride(_ context.Context, accountID, provider, service string) (*config.AccountServiceOverride, error) {
	if m.getOverrideErr != nil {
		return nil, m.getOverrideErr
	}
	return m.overrides[accountID+"|"+provider+"|"+service], nil
}

func boolPtr(b bool) *bool { return &b }

// rdsRec returns a rec for the given account/region/engine with sensible defaults.
func rdsRec(account, region, engine string) config.RecommendationRecord {
	a := account
	return config.RecommendationRecord{
		ID:             account + "/" + region + "/" + engine,
		Provider:       "aws",
		Service:        "rds",
		Region:         region,
		Engine:         engine,
		ResourceType:   "db.t3.medium",
		Count:          1,
		CloudAccountID: &a,
	}
}

func TestApplyAccountOverrides_DisabledOverride_DropsAccountSvcRecs(t *testing.T) {
	ctx := context.Background()
	store := &mockOverrideStore{
		recs: []config.RecommendationRecord{
			rdsRec("acct-A", "us-east-1", "mysql"),
			rdsRec("acct-B", "us-east-1", "mysql"),
		},
		globals: map[string]*config.ServiceConfig{
			"aws|rds": {Provider: "aws", Service: "rds", Enabled: true},
		},
		overrides: map[string]*config.AccountServiceOverride{
			"acct-A|aws|rds": {Enabled: boolPtr(false)},
		},
	}
	s := &Scheduler{config: store}

	recs, err := s.ListRecommendations(ctx, config.RecommendationFilter{})
	require.NoError(t, err)
	require.Len(t, recs, 1, "acct-A's rec dropped; acct-B's kept")
	assert.Equal(t, "acct-B", *recs[0].CloudAccountID)
}

func TestApplyAccountOverrides_GlobalDisabled_DropsAllRecsForService(t *testing.T) {
	ctx := context.Background()
	store := &mockOverrideStore{
		recs: []config.RecommendationRecord{
			rdsRec("acct-A", "us-east-1", "mysql"),
			rdsRec("acct-B", "us-east-1", "mysql"),
		},
		globals: map[string]*config.ServiceConfig{
			"aws|rds": {Provider: "aws", Service: "rds", Enabled: false},
		},
	}
	s := &Scheduler{config: store}

	recs, err := s.ListRecommendations(ctx, config.RecommendationFilter{})
	require.NoError(t, err)
	assert.Empty(t, recs, "global Enabled=false drops all per-account recs for the service")
}

func TestApplyAccountOverrides_NoGlobalConfig_RecsPassThrough(t *testing.T) {
	ctx := context.Background()
	store := &mockOverrideStore{
		recs: []config.RecommendationRecord{rdsRec("acct-A", "us-east-1", "mysql")},
		// globals empty — GetServiceConfig returns nil
	}
	s := &Scheduler{config: store}

	recs, err := s.ListRecommendations(ctx, config.RecommendationFilter{})
	require.NoError(t, err)
	assert.Len(t, recs, 1, "no global config -> no per-account policy applies -> rec passes through")
}

func TestApplyAccountOverrides_NilCloudAccountID_PassesThrough(t *testing.T) {
	ctx := context.Background()
	rec := config.RecommendationRecord{
		ID: "ambient", Provider: "aws", Service: "ec2",
		Region: "us-east-1", ResourceType: "t4g.nano", Count: 1,
		CloudAccountID: nil, // ambient AWS credentials path
	}
	store := &mockOverrideStore{
		recs: []config.RecommendationRecord{rec},
		globals: map[string]*config.ServiceConfig{
			"aws|ec2": {Provider: "aws", Service: "ec2", Enabled: false},
		},
	}
	s := &Scheduler{config: store}

	recs, err := s.ListRecommendations(ctx, config.RecommendationFilter{})
	require.NoError(t, err)
	assert.Len(t, recs, 1, "nil CloudAccountID recs are not subject to per-account override policy")
}

func TestApplyAccountOverrides_IncludeEngineMatch(t *testing.T) {
	ctx := context.Background()
	store := &mockOverrideStore{
		recs: []config.RecommendationRecord{
			rdsRec("acct-A", "us-east-1", "mysql"),
			rdsRec("acct-A", "us-east-1", "postgres"),
		},
		globals: map[string]*config.ServiceConfig{
			"aws|rds": {Provider: "aws", Service: "rds", Enabled: true},
		},
		overrides: map[string]*config.AccountServiceOverride{
			"acct-A|aws|rds": {IncludeEngines: []string{"mysql"}},
		},
	}
	s := &Scheduler{config: store}

	recs, err := s.ListRecommendations(ctx, config.RecommendationFilter{})
	require.NoError(t, err)
	require.Len(t, recs, 1)
	assert.Equal(t, "mysql", recs[0].Engine, "non-matching engine filtered out")
}

func TestApplyAccountOverrides_ExcludeEngine(t *testing.T) {
	ctx := context.Background()
	store := &mockOverrideStore{
		recs: []config.RecommendationRecord{
			rdsRec("acct-A", "us-east-1", "mysql"),
			rdsRec("acct-A", "us-east-1", "postgres"),
		},
		globals: map[string]*config.ServiceConfig{
			"aws|rds": {Provider: "aws", Service: "rds", Enabled: true},
		},
		overrides: map[string]*config.AccountServiceOverride{
			"acct-A|aws|rds": {ExcludeEngines: []string{"postgres"}},
		},
	}
	s := &Scheduler{config: store}

	recs, err := s.ListRecommendations(ctx, config.RecommendationFilter{})
	require.NoError(t, err)
	require.Len(t, recs, 1)
	assert.Equal(t, "mysql", recs[0].Engine)
}

func TestApplyAccountOverrides_RegionAndTypeFilters(t *testing.T) {
	ctx := context.Background()
	mkRec := func(account, region, rtype string) config.RecommendationRecord {
		a := account
		return config.RecommendationRecord{
			ID: account + "/" + region + "/" + rtype, Provider: "aws", Service: "rds",
			Region: region, ResourceType: rtype, Count: 1, CloudAccountID: &a,
		}
	}
	store := &mockOverrideStore{
		recs: []config.RecommendationRecord{
			mkRec("acct-A", "us-east-1", "db.t3.small"),
			mkRec("acct-A", "us-east-1", "db.t3.large"),
			mkRec("acct-A", "eu-west-1", "db.t3.small"),
		},
		globals: map[string]*config.ServiceConfig{
			"aws|rds": {Provider: "aws", Service: "rds", Enabled: true},
		},
		overrides: map[string]*config.AccountServiceOverride{
			"acct-A|aws|rds": {
				IncludeRegions: []string{"us-east-1"},
				ExcludeTypes:   []string{"db.t3.small"},
			},
		},
	}
	s := &Scheduler{config: store}

	recs, err := s.ListRecommendations(ctx, config.RecommendationFilter{})
	require.NoError(t, err)
	require.Len(t, recs, 1)
	assert.Equal(t, "us-east-1", recs[0].Region)
	assert.Equal(t, "db.t3.large", recs[0].ResourceType)
}

func TestApplyAccountOverrides_EmptyEngine_NotFilteredByIncludeEngines(t *testing.T) {
	// Savings Plans / Compute recs carry no engine; an IncludeEngines list
	// added on another service for the same account must NOT silently drop
	// engine-less recs.
	ctx := context.Background()
	mkSP := func(account string) config.RecommendationRecord {
		a := account
		return config.RecommendationRecord{
			ID: account + "/sp", Provider: "aws", Service: "savings-plans",
			Region: "us-east-1", ResourceType: "Compute", Count: 1, CloudAccountID: &a,
		}
	}
	store := &mockOverrideStore{
		recs: []config.RecommendationRecord{mkSP("acct-A")},
		globals: map[string]*config.ServiceConfig{
			"aws|savings-plans": {Provider: "aws", Service: "savings-plans", Enabled: true},
		},
		overrides: map[string]*config.AccountServiceOverride{
			"acct-A|aws|savings-plans": {
				IncludeEngines: []string{"mysql"}, // misconfigured but must not blank the page
			},
		},
	}
	s := &Scheduler{config: store}

	recs, err := s.ListRecommendations(ctx, config.RecommendationFilter{})
	require.NoError(t, err)
	assert.Len(t, recs, 1, "engine-less rec not filtered by IncludeEngines")
}

func TestApplyAccountOverrides_LookupError_PassesThrough(t *testing.T) {
	// Mirrors the applySuppressions over-show-vs-under-show trade-off.
	// A store error must not blank the recommendations page.
	ctx := context.Background()
	store := &mockOverrideStore{
		recs:         []config.RecommendationRecord{rdsRec("acct-A", "us-east-1", "mysql")},
		getGlobalErr: errors.New("postgres timeout"),
	}
	s := &Scheduler{config: store}

	recs, err := s.ListRecommendations(ctx, config.RecommendationFilter{})
	require.NoError(t, err, "ListRecommendations swallows the override-resolver error")
	assert.Len(t, recs, 1, "un-filtered list returned on lookup failure")
}

func TestApplyAccountOverrides_OverrideLookupError_PassesThrough(t *testing.T) {
	// Mirrors the global-config lookup failure contract: if the per-account
	// override lookup fails, the page should over-show rather than blank.
	ctx := context.Background()
	store := &mockOverrideStore{
		recs: []config.RecommendationRecord{rdsRec("acct-A", "us-east-1", "mysql")},
		globals: map[string]*config.ServiceConfig{
			"aws|rds": {Provider: "aws", Service: "rds", Enabled: true},
		},
		getOverrideErr: errors.New("override lookup timeout"),
	}
	s := &Scheduler{config: store}

	recs, err := s.ListRecommendations(ctx, config.RecommendationFilter{})
	require.NoError(t, err, "ListRecommendations swallows the override-resolver error")
	assert.Len(t, recs, 1, "un-filtered list returned on override lookup failure")
}

// Issue #196 acceptance criterion (mirrored from issue #111):
//
// Seed a global ServiceConfig + a per-account override that disables one
// account; confirm the listed recs reflect the override.
func TestApplyAccountOverrides_AcceptanceCriterion_Issue196(t *testing.T) {
	ctx := context.Background()
	store := &mockOverrideStore{
		recs: []config.RecommendationRecord{
			rdsRec("acct-A", "us-east-1", "mysql"),
			rdsRec("acct-B", "us-east-1", "mysql"),
		},
		globals: map[string]*config.ServiceConfig{
			"aws|rds": {Provider: "aws", Service: "rds", Enabled: true, Term: 3, Payment: "all_upfront"},
		},
		overrides: map[string]*config.AccountServiceOverride{
			"acct-A|aws|rds": {Enabled: boolPtr(false)},
		},
	}
	s := &Scheduler{config: store}

	recs, err := s.ListRecommendations(ctx, config.RecommendationFilter{})
	require.NoError(t, err)
	require.Len(t, recs, 1, "acct-A's recs hidden by the override")
	assert.Equal(t, "acct-B", *recs[0].CloudAccountID)
}
