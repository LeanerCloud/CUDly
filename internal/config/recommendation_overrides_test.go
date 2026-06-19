package config

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

// fakeAccountConfigReader counts calls to satisfy the per-call cache assertions.
type fakeAccountConfigReader struct {
	globals       map[string]*ServiceConfig          // key: provider|service
	overrides     map[string]*AccountServiceOverride // key: account|provider|service
	globalErr     error
	overrideErr   error
	globalCalls   int
	overrideCalls int
}

func (f *fakeAccountConfigReader) GetServiceConfig(_ context.Context, provider, service string) (*ServiceConfig, error) {
	f.globalCalls++
	if f.globalErr != nil {
		return nil, f.globalErr
	}
	return f.globals[provider+"|"+service], nil
}

func (f *fakeAccountConfigReader) GetAccountServiceOverride(_ context.Context, accountID, provider, service string) (*AccountServiceOverride, error) {
	f.overrideCalls++
	if f.overrideErr != nil {
		return nil, f.overrideErr
	}
	return f.overrides[accountID+"|"+provider+"|"+service], nil
}

func acctRec(account, provider, service string) RecommendationRecord { //nolint:unparam // provider is always "aws" in current tests but the parameter is kept for future multi-provider test cases
	return RecommendationRecord{
		Provider:       provider,
		Service:        service,
		CloudAccountID: &account,
	}
}

func TestResolveAccountConfigsForRecs_EmptyRecs(t *testing.T) {
	reader := &fakeAccountConfigReader{}
	got, err := ResolveAccountConfigsForRecs(context.Background(), reader, nil)
	assert.NoError(t, err)
	assert.Empty(t, got)
	assert.Zero(t, reader.globalCalls)
	assert.Zero(t, reader.overrideCalls)
}

func TestResolveAccountConfigsForRecs_NilCloudAccountSkipped(t *testing.T) {
	reader := &fakeAccountConfigReader{}
	recs := []RecommendationRecord{
		{Provider: "aws", Service: "rds", CloudAccountID: nil}, // ambient
	}
	got, err := ResolveAccountConfigsForRecs(context.Background(), reader, recs)
	assert.NoError(t, err)
	assert.Empty(t, got, "nil CloudAccountID recs are skipped")
	assert.Zero(t, reader.globalCalls)
}

func TestResolveAccountConfigsForRecs_OverridePresent_ResolvedConfigReflectsOverride(t *testing.T) {
	reader := &fakeAccountConfigReader{
		globals: map[string]*ServiceConfig{
			"aws|rds": {Provider: "aws", Service: "rds", Enabled: true, Term: 3, Coverage: 80},
		},
		overrides: map[string]*AccountServiceOverride{
			"acct-A|aws|rds": {Enabled: boolPtr(false), Coverage: float64Ptr(50)},
		},
	}
	recs := []RecommendationRecord{acctRec("acct-A", "aws", "rds")}

	got, err := ResolveAccountConfigsForRecs(context.Background(), reader, recs)
	assert.NoError(t, err)

	resolved := got[AccountConfigKey("acct-A", "aws", "rds")]
	assert.NotNil(t, resolved)
	assert.False(t, resolved.Enabled, "override Enabled=false applied")
	assert.Equal(t, 50.0, resolved.Coverage, "override Coverage=50 applied")
	assert.Equal(t, 3, resolved.Term, "global Term inherited (override Term unset)")
}

func TestResolveAccountConfigsForRecs_OverrideAbsent_GlobalReturned(t *testing.T) {
	reader := &fakeAccountConfigReader{
		globals: map[string]*ServiceConfig{
			"aws|rds": {Provider: "aws", Service: "rds", Enabled: true, Coverage: 80},
		},
	}
	recs := []RecommendationRecord{acctRec("acct-A", "aws", "rds")}

	got, err := ResolveAccountConfigsForRecs(context.Background(), reader, recs)
	assert.NoError(t, err)

	resolved := got[AccountConfigKey("acct-A", "aws", "rds")]
	assert.NotNil(t, resolved)
	assert.True(t, resolved.Enabled, "global config returned when override absent")
}

// TestResolveAccountConfigsForRecs_NoGlobalNoOverride_TripleSkipped verifies that a
// triple is omitted from the result when neither a global ServiceConfig nor a
// per-account override exists — there is nothing to apply.
func TestResolveAccountConfigsForRecs_NoGlobalNoOverride_TripleSkipped(t *testing.T) {
	reader := &fakeAccountConfigReader{}
	recs := []RecommendationRecord{acctRec("acct-A", "aws", "rds")}

	got, err := ResolveAccountConfigsForRecs(context.Background(), reader, recs)
	assert.NoError(t, err)
	assert.NotContains(t, got, AccountConfigKey("acct-A", "aws", "rds"),
		"neither global nor override -> no map entry; callers treat as 'no filter applies'")
}

// TestResolveAccountConfigsForRecs_OverrideWithoutGlobal_OverrideApplied is the
// regression test for issue #202: a per-account override must take effect even
// when no global ServiceConfig exists for the (provider, service) pair.
func TestResolveAccountConfigsForRecs_OverrideWithoutGlobal_OverrideApplied(t *testing.T) {
	reader := &fakeAccountConfigReader{
		overrides: map[string]*AccountServiceOverride{
			"acct-A|aws|rds": {Enabled: boolPtr(false), Coverage: float64Ptr(70)},
		},
	}
	recs := []RecommendationRecord{acctRec("acct-A", "aws", "rds")}

	got, err := ResolveAccountConfigsForRecs(context.Background(), reader, recs)
	assert.NoError(t, err)

	resolved := got[AccountConfigKey("acct-A", "aws", "rds")]
	assert.NotNil(t, resolved, "override-without-global entry must be in the map")
	assert.False(t, resolved.Enabled, "override Enabled=false applied against synthesized baseline")
	assert.Equal(t, 70.0, resolved.Coverage, "override Coverage=70 applied")
	assert.Equal(t, "aws", resolved.Provider, "Provider set from rec")
	assert.Equal(t, "rds", resolved.Service, "Service set from rec")
}

// TestResolveAccountConfigsForRecs_OverrideWithGlobal_MergesCorrectly is a
// regression test for the existing #200 merge path: override on top of global.
func TestResolveAccountConfigsForRecs_OverrideWithGlobal_MergesCorrectly(t *testing.T) {
	reader := &fakeAccountConfigReader{
		globals: map[string]*ServiceConfig{
			"aws|rds": {Provider: "aws", Service: "rds", Enabled: true, Term: 3, Coverage: 80},
		},
		overrides: map[string]*AccountServiceOverride{
			"acct-B|aws|rds": {Coverage: float64Ptr(50)},
		},
	}
	recs := []RecommendationRecord{acctRec("acct-B", "aws", "rds")}

	got, err := ResolveAccountConfigsForRecs(context.Background(), reader, recs)
	assert.NoError(t, err)

	resolved := got[AccountConfigKey("acct-B", "aws", "rds")]
	assert.NotNil(t, resolved)
	assert.True(t, resolved.Enabled, "global Enabled=true inherited")
	assert.Equal(t, 3, resolved.Term, "global Term=3 inherited")
	assert.Equal(t, 50.0, resolved.Coverage, "override Coverage=50 applied")
}

func TestResolveAccountConfigsForRecs_DedupesPerTriple(t *testing.T) {
	reader := &fakeAccountConfigReader{
		globals: map[string]*ServiceConfig{
			"aws|rds": {Provider: "aws", Service: "rds", Enabled: true},
		},
	}
	recs := []RecommendationRecord{
		acctRec("acct-A", "aws", "rds"),
		acctRec("acct-A", "aws", "rds"), // same triple
		acctRec("acct-A", "aws", "rds"),
	}

	got, err := ResolveAccountConfigsForRecs(context.Background(), reader, recs)
	assert.NoError(t, err)
	assert.Len(t, got, 1)
	assert.Equal(t, 1, reader.overrideCalls, "per-triple lookup deduped")
	assert.Equal(t, 1, reader.globalCalls, "per-(provider,service) global lookup deduped")
}

func TestResolveAccountConfigsForRecs_GlobalCachedAcrossAccounts(t *testing.T) {
	reader := &fakeAccountConfigReader{
		globals: map[string]*ServiceConfig{
			"aws|rds": {Provider: "aws", Service: "rds", Enabled: true},
		},
	}
	recs := []RecommendationRecord{
		acctRec("acct-A", "aws", "rds"),
		acctRec("acct-B", "aws", "rds"), // same (provider, service), different account
	}

	got, err := ResolveAccountConfigsForRecs(context.Background(), reader, recs)
	assert.NoError(t, err)
	assert.Len(t, got, 2)
	assert.Equal(t, 2, reader.overrideCalls, "override lookup runs once per account")
	assert.Equal(t, 1, reader.globalCalls, "global lookup is cached across accounts")
}

func TestResolveAccountConfigsForRecs_GlobalAbsentCachedNegative(t *testing.T) {
	// When GetServiceConfig returns nil, subsequent recs for the same
	// (provider, service) must not re-fetch the global — but override lookups
	// still run once per account because the override may exist without a global.
	reader := &fakeAccountConfigReader{}
	recs := []RecommendationRecord{
		acctRec("acct-A", "aws", "rds"),
		acctRec("acct-B", "aws", "rds"),
		acctRec("acct-C", "aws", "rds"),
	}

	_, err := ResolveAccountConfigsForRecs(context.Background(), reader, recs)
	assert.NoError(t, err)
	assert.Equal(t, 1, reader.globalCalls, "missing global cached as 'absent' — fetched only once")
	assert.Equal(t, 3, reader.overrideCalls, "override lookup runs once per account even when global absent")
}

// TestResolveAccountConfigsForRecs_NoGlobalNoOverride_DedupedCorrectly verifies
// that duplicate recs for the same triple with no global and no override do not
// trigger redundant store lookups. Previously the resolved map was used as the
// seen-set, so absent triples were never recorded and overrideCalls grew with
// every duplicate rec.
func TestResolveAccountConfigsForRecs_NoGlobalNoOverride_DedupedCorrectly(t *testing.T) {
	reader := &fakeAccountConfigReader{} // no globals, no overrides
	recs := []RecommendationRecord{
		acctRec("acct-A", "aws", "rds"),
		acctRec("acct-A", "aws", "rds"), // duplicate
		acctRec("acct-A", "aws", "rds"), // duplicate
	}

	got, err := ResolveAccountConfigsForRecs(context.Background(), reader, recs)
	assert.NoError(t, err)
	assert.Empty(t, got, "no entry when both absent")
	assert.Equal(t, 1, reader.globalCalls, "global lookup deduped even when both absent")
	assert.Equal(t, 1, reader.overrideCalls, "override lookup deduped even when both absent")
}

func TestResolveAccountConfigsForRecs_GlobalErrorPropagates(t *testing.T) {
	reader := &fakeAccountConfigReader{globalErr: errors.New("boom")}
	recs := []RecommendationRecord{acctRec("acct-A", "aws", "rds")}

	got, err := ResolveAccountConfigsForRecs(context.Background(), reader, recs)
	assert.Error(t, err)
	assert.Empty(t, got, "no entry written before the error")
}

func TestResolveAccountConfigsForRecs_OverrideErrorPropagates(t *testing.T) {
	reader := &fakeAccountConfigReader{
		globals: map[string]*ServiceConfig{
			"aws|rds": {Provider: "aws", Service: "rds", Enabled: true},
		},
		overrideErr: errors.New("boom"),
	}
	recs := []RecommendationRecord{acctRec("acct-A", "aws", "rds")}

	got, err := ResolveAccountConfigsForRecs(context.Background(), reader, recs)
	assert.Error(t, err)
	assert.Empty(t, got)
}
