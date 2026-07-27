package azure

import (
	"context"
	"fmt"
	"os"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

// accountsCacheSFKey is the single singleflight.Group key this cache ever
// uses. There is exactly one cached value per AzureProvider (the org-wide
// subscription list), so a constant key is sufficient to coalesce every
// concurrent cold-cache caller onto the same in-flight fetch.
const accountsCacheSFKey = "accounts"

// getOrFetchAccounts returns the cached subscription list, populating it via
// fetchAccounts on first use. Safe for concurrent callers:
//
//   - Fast path: a read lock serves the already-populated cache.
//   - Slow path: singleflight.Group collapses concurrent cold-cache callers
//     into a single in-flight ARM subscriptions.List call -- the network
//     round-trip runs OUTSIDE accountsMu, so a slow or hung lookup never
//     blocks readers of the cache. accountsMu is only ever held briefly to
//     read or populate cachedAccounts, never across the fetch.
//   - Every caller receives an independent clone so a returned slice can't be
//     mutated into the shared cache.
//
// Callers must have already verified IsConfigured(); this method assumes a
// usable credential is present (mirrors GetAccounts, its only production
// caller alongside GetServiceClient/GetRecommendationsClient which check
// IsConfigured() themselves before resolving accounts).
func (p *AzureProvider) getOrFetchAccounts(ctx context.Context) ([]common.Account, error) {
	p.accountsMu.RLock()
	cached := p.cachedAccounts
	p.accountsMu.RUnlock()
	if cached != nil {
		return cloneAccounts(cached), nil
	}

	v, err, _ := p.accountsSF.Do(accountsCacheSFKey, func() (interface{}, error) {
		// Re-check: another goroutine's fetch may have populated the cache
		// between our RLock check above and this closure actually running
		// (e.g. it lost the race to become the singleflight leader).
		p.accountsMu.RLock()
		cached := p.cachedAccounts
		p.accountsMu.RUnlock()
		if cached != nil {
			return cached, nil
		}

		accounts, err := p.fetchAccounts(ctx)
		if err != nil {
			return nil, err
		}

		p.accountsMu.Lock()
		p.cachedAccounts = accounts
		p.accountsMu.Unlock()

		return accounts, nil
	})
	if err != nil {
		return nil, err
	}
	return cloneAccounts(v.([]common.Account)), nil
}

// cloneAccounts returns a shallow copy of accounts backed by a fresh array.
// common.Account has no nested slices/maps, so a shallow per-element copy is
// sufficient to stop a caller mutating a returned slice (e.g. flipping
// IsDefault) from corrupting the shared cache -- the same class of bug
// flagged for getters returning nested state.
func cloneAccounts(accounts []common.Account) []common.Account {
	out := make([]common.Account, len(accounts))
	copy(out, accounts)
	return out
}

// InvalidateAccountsCache clears the cached subscription list so the next
// getOrFetchAccounts call re-fetches from the ARM subscriptions API. Exposed
// for tests that need to assert cache-miss behavior; production callers
// currently rely on the cache living for the lifetime of the AzureProvider
// instance (one instance is constructed per collection/purchase run).
func (p *AzureProvider) InvalidateAccountsCache() {
	p.accountsMu.Lock()
	defer p.accountsMu.Unlock()
	p.cachedAccounts = nil
}

// fetchAccounts performs the actual ARM subscriptions.List call and resolves
// the default subscription. It holds no lock and issues the network round-trip
// on the caller's goroutine; getOrFetchAccounts serializes concurrent cold-cache
// callers via singleflight so this runs at most once per cache-population window.
func (p *AzureProvider) fetchAccounts(ctx context.Context) ([]common.Account, error) {
	// Use injected client if available (for testing)
	var subClient SubscriptionsClient
	if p.subscriptionsClient != nil {
		subClient = p.subscriptionsClient
	} else {
		client, err := armsubscriptions.NewClient(p.cred, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create subscriptions client: %w", err)
		}
		subClient = &realSubscriptionsClient{client: client}
	}

	accounts := make([]common.Account, 0)
	pager := subClient.NewListPager(nil)

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list subscriptions: %w", err)
		}

		for _, sub := range page.Value {
			if sub.SubscriptionID == nil || sub.DisplayName == nil {
				continue
			}

			accounts = append(accounts, common.Account{
				Provider:    common.ProviderAzure,
				ID:          *sub.SubscriptionID,
				Name:        *sub.DisplayName,
				DisplayName: *sub.DisplayName,
				// IsDefault resolved below once the full list is available.
				IsDefault: false,
			})
		}
	}

	// Resolve which subscription is the default.
	resolveDefaultSubscription(accounts, p.subscriptionID)

	return accounts, nil
}

// resolveDefaultSubscription sets IsDefault on the matching account in-place.
//
// Priority:
//  1. explicitSubID (from ProviderConfig.AzureSubscriptionID / Profile).
//  2. AZURE_SUBSCRIPTION_ID environment variable.
//  3. When exactly one subscription is visible, mark it default (mirrors AWS
//     behaviour where the STS-identified account is always the default).
func resolveDefaultSubscription(accounts []common.Account, explicitSubID string) {
	if len(accounts) == 0 {
		return
	}

	target := explicitSubID
	if target == "" {
		target = os.Getenv("AZURE_SUBSCRIPTION_ID")
	}

	if target != "" {
		for i := range accounts {
			if accounts[i].ID == target {
				accounts[i].IsDefault = true
				return
			}
		}
		// target was configured but not found in the visible subscriptions;
		// fall through to the single-subscription rule rather than leaving
		// all accounts as non-default.
	}

	// Rule 3: single visible subscription.
	if len(accounts) == 1 {
		accounts[0].IsDefault = true
	}
}

// getDefaultSubscriptionID returns the ID of the default subscription from a
// pre-fetched account list, or an empty string when no account is marked
// default (e.g. ambiguous multi-subscription tenants with no explicit config).
func getDefaultSubscriptionID(accounts []common.Account) string {
	if len(accounts) == 0 {
		return ""
	}
	for _, a := range accounts {
		if a.IsDefault {
			return a.ID
		}
	}
	return ""
}
