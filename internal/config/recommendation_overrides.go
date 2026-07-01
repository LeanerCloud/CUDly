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

// globalConfigCache holds a fetched-once cache of global ServiceConfig values
// keyed by (provider, service).
type globalConfigCache struct {
	hit     map[string]*ServiceConfig // provider+"|"+service -> non-nil config
	missing map[string]bool           // true when GetServiceConfig returned nil
}

func newGlobalConfigCache() *globalConfigCache {
	return &globalConfigCache{
		hit:     make(map[string]*ServiceConfig),
		missing: make(map[string]bool),
	}
}

// lookup returns the cached global config for (provider, service), fetching it
// on the first call. Returns (nil, nil) when no global row exists.
func (c *globalConfigCache) lookup(ctx context.Context, store AccountConfigReader, provider, service string) (*ServiceConfig, error) {
	k := provider + "|" + service
	if c.missing[k] {
		return nil, nil
	}
	if g, ok := c.hit[k]; ok {
		return g, nil
	}
	fetched, err := store.GetServiceConfig(ctx, provider, service)
	if err != nil {
		return nil, fmt.Errorf("get service config %s/%s: %w", provider, service, err)
	}
	if fetched == nil {
		c.missing[k] = true
		return nil, nil
	}
	c.hit[k] = fetched
	return fetched, nil
}

// ResolveAccountConfigsForRecs walks the recs once, collects the unique
// (cloud_account_id, provider, service) triples, and resolves each via
// ResolveServiceConfig(provider, service, global, override). Returns a map
// keyed by AccountConfigKey -> resolved *ServiceConfig.
//
// Triples are skipped (not present in the map) when:
//   - rec.CloudAccountID is nil — no per-account override possible (e.g.
//     AWS ambient-credentials path).
//   - Neither a global ServiceConfig nor a per-account override exists for the
//     (provider, service) pair — no configuration to apply, so callers treat
//     the triple as "no filter applies".
//
// When a per-account override exists but no global ServiceConfig does, the
// override is applied against a synthesized default baseline (Enabled: true)
// so the operator's intent is honored even when a global row has not been
// created yet.
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

	// Cache global configs per (provider, service) — many recs share the same
	// pair so the lookup count is on the order of distinct services, not accounts.
	cache := newGlobalConfigCache()
	// seen tracks every (account, provider, service) triple we have already
	// processed. We cannot use resolved as the seen-set because triples where
	// both global and override are absent are skipped (nothing to write) yet
	// must still be remembered to avoid redundant store lookups on duplicates.
	seen := make(map[string]struct{})

	for i := range recs {
		rec := &recs[i]
		if rec.CloudAccountID == nil {
			continue
		}
		accountID := *rec.CloudAccountID
		key := AccountConfigKey(accountID, rec.Provider, rec.Service)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}

		global, err := cache.lookup(ctx, store, rec.Provider, rec.Service)
		if err != nil {
			return resolved, err
		}

		override, err := store.GetAccountServiceOverride(ctx, accountID, rec.Provider, rec.Service)
		if err != nil {
			return resolved, fmt.Errorf("get override %s/%s/%s: %w", accountID, rec.Provider, rec.Service, err)
		}

		// Skip the triple only when both global and override are absent — there is
		// genuinely nothing to apply.
		if global == nil && override == nil {
			continue
		}

		// override may be nil — ResolveServiceConfig returns global unchanged.
		// global may be nil — ResolveServiceConfig synthesizes a default baseline.
		resolved[key] = ResolveServiceConfig(rec.Provider, rec.Service, global, override)
	}

	return resolved, nil
}
