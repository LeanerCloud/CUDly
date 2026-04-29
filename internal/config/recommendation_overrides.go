package config

import (
	"context"
	"fmt"
)

// AccountConfigReader is the minimal store surface needed by
// ResolveAccountConfigsForRecs. Both PostgresStore (production) and the
// scheduler's MockConfigStore (tests) already satisfy it via the broader
// StoreInterface — we narrow here so the helper can be unit-tested with
// a tiny ad-hoc fake.
type AccountConfigReader interface {
	GetServiceConfig(ctx context.Context, provider, service string) (*ServiceConfig, error)
	GetAccountServiceOverride(ctx context.Context, accountID, provider, service string) (*AccountServiceOverride, error)
}

// AccountConfigKey returns the map key used by ResolveAccountConfigsForRecs.
// Exposed so callers (scheduler filter, dashboard aggregator) can look up the
// resolved config for a given rec without re-implementing the format.
func AccountConfigKey(accountID, provider, service string) string {
	return accountID + "|" + provider + "|" + service
}

// ResolveAccountConfigsForRecs walks the recs once, collects the unique
// (cloud_account_id, provider, service) triples, and resolves each via
// ResolveServiceConfig(global, override). Returns a map keyed by
// AccountConfigKey -> resolved *ServiceConfig.
//
// Triples are skipped (not present in the map) when:
//   - rec.CloudAccountID is nil — no per-account override possible (e.g.
//     AWS ambient-credentials path).
//   - GetServiceConfig returns nil with no error — no global config to merge
//     against. Without a global, ResolveServiceConfig would deref nil; the
//     safe behaviour for callers is "no per-account filtering applies".
//
// Errors from either lookup are returned alongside the partial map so the
// caller decides whether to fail the whole operation or pass through. The
// scheduler / dashboard read paths choose pass-through (over-show vs.
// under-show), matching the precedent set by applySuppressions.
func ResolveAccountConfigsForRecs(
	ctx context.Context,
	store AccountConfigReader,
	recs []RecommendationRecord,
) (map[string]*ServiceConfig, error) {
	resolved := make(map[string]*ServiceConfig)
	if len(recs) == 0 {
		return resolved, nil
	}

	// Cache global configs per (provider, service) — many recs typically share
	// the same pair so the lookup count is on the order of distinct services,
	// not distinct accounts.
	type globalKey struct{ provider, service string }
	globalCache := make(map[globalKey]*ServiceConfig)
	globalMissing := make(map[globalKey]bool)

	for i := range recs {
		rec := &recs[i]
		if rec.CloudAccountID == nil {
			continue
		}
		accountID := *rec.CloudAccountID
		key := AccountConfigKey(accountID, rec.Provider, rec.Service)
		if _, seen := resolved[key]; seen {
			continue
		}

		gk := globalKey{rec.Provider, rec.Service}
		if globalMissing[gk] {
			continue
		}
		global, ok := globalCache[gk]
		if !ok {
			fetched, err := store.GetServiceConfig(ctx, rec.Provider, rec.Service)
			if err != nil {
				return resolved, fmt.Errorf("get service config %s/%s: %w", rec.Provider, rec.Service, err)
			}
			if fetched == nil {
				globalMissing[gk] = true
				continue
			}
			globalCache[gk] = fetched
			global = fetched
		}

		override, err := store.GetAccountServiceOverride(ctx, accountID, rec.Provider, rec.Service)
		if err != nil {
			return resolved, fmt.Errorf("get override %s/%s/%s: %w", accountID, rec.Provider, rec.Service, err)
		}
		// override may be nil — ResolveServiceConfig then returns global
		// unchanged, which still gives callers a baseline (e.g. they may
		// honour global Enabled or include/exclude lists).
		resolved[key] = ResolveServiceConfig(global, override)
	}

	return resolved, nil
}
