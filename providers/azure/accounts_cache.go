package azure

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

// accountsCacheSFKeyPrefix prefixes the singleflight.Group key this cache
// uses. There is exactly one cached value per AzureProvider (the org-wide
// subscription list), so the key only has to distinguish cache generations --
// see accountsSFKey.
const accountsCacheSFKeyPrefix = "accounts-gen-"

// azureSubscriptionIDEnv is the environment variable that pins the default
// subscription when ProviderConfig carries none. Named rather than repeated
// as a literal because both resolveDefaultSubscription (which consumes it)
// and GetRecommendationsClient (which reports it back in an error) depend on
// it being the same variable.
const azureSubscriptionIDEnv = "AZURE_SUBSCRIPTION_ID"

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
	if cached := p.readCachedAccounts(); cached != nil {
		return cached, nil
	}

	accounts, err := p.fetchAccountsShared(ctx)
	if err == nil {
		return cloneAccounts(accounts), nil
	}

	// singleflight hands the leader's error to every waiter, so a caller whose
	// own context is still live can inherit a cancellation raised by whichever
	// unrelated caller happened to win the leader election. That cancellation
	// is not ours to obey: retry once, as leader this time. Our OWN
	// cancellation stays terminal -- the ctx.Err() guard short-circuits it --
	// so this can never spin on a dead context, and the single retry bounds
	// the work at two attempts.
	if ctx.Err() != nil || !isContextError(err) {
		return nil, err
	}
	accounts, err = p.fetchAccountsShared(ctx)
	if err != nil {
		return nil, err
	}
	return cloneAccounts(accounts), nil
}

// isContextError reports whether err was produced by a context being
// cancelled or timing out.
func isContextError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// readCachedAccounts returns an independent clone of the cached subscription
// list, or nil when the cache has not been populated yet.
func (p *AzureProvider) readCachedAccounts() []common.Account {
	p.accountsMu.RLock()
	defer p.accountsMu.RUnlock()
	if p.cachedAccounts == nil {
		return nil
	}
	return cloneAccounts(p.cachedAccounts)
}

// accountsSFKey returns the singleflight key for cache generation gen.
//
// Keying the in-flight call on the generation is what makes
// InvalidateAccountsCache meaningful under concurrency: a caller arriving
// after an invalidation uses a fresh key, so it starts a new ARM call instead
// of joining one that began before the invalidation and being handed back
// exactly the snapshot it just asked to discard.
func accountsSFKey(gen uint64) string {
	return accountsCacheSFKeyPrefix + strconv.FormatUint(gen, 10)
}

// fetchAccountsShared collapses concurrent cold-cache callers onto a single
// in-flight ARM subscriptions.List and returns the SHARED (un-cloned) slice.
// Callers must clone before handing the result outside the package.
func (p *AzureProvider) fetchAccountsShared(ctx context.Context) ([]common.Account, error) {
	p.accountsMu.RLock()
	gen := p.accountsGen
	p.accountsMu.RUnlock()

	v, err, _ := p.accountsSF.Do(accountsSFKey(gen), func() (interface{}, error) {
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
		// Publish only when no InvalidateAccountsCache landed while the ARM
		// call was in flight. A fetch that started before an invalidation
		// carries a pre-invalidation snapshot; writing it now would silently
		// resurrect exactly the data the caller asked to discard.
		if p.accountsGen == gen {
			p.cachedAccounts = accounts
		}
		p.accountsMu.Unlock()

		return accounts, nil
	})
	if err != nil {
		return nil, err
	}
	accounts, ok := v.([]common.Account)
	if !ok {
		// singleflight.Do is untyped, so a future edit to the closure's return
		// type would otherwise turn into a panic here. Surface it as an error.
		return nil, fmt.Errorf("azure accounts cache: unexpected fetch result type %T", v)
	}
	return accounts, nil
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
//
// Bumping accountsGen is what makes this safe against a concurrent in-flight
// fetch: the generation both invalidates that fetch's right to publish its
// result and moves later callers onto a fresh singleflight key, so no caller
// can be served a snapshot taken before this call returned.
func (p *AzureProvider) InvalidateAccountsCache() {
	p.accountsMu.Lock()
	defer p.accountsMu.Unlock()
	p.invalidateAccountsCacheLocked()
}

// invalidateAccountsCacheLocked is InvalidateAccountsCache's body, for callers
// that already hold accountsMu for writing.
//
// It exists so SetCredential and SetSubscriptionsClient can publish the new
// field AND invalidate in one critical section: sync.RWMutex is not reentrant,
// so they cannot call InvalidateAccountsCache while holding the lock, and
// releasing it between the two steps would reopen the window where an
// in-flight fetch sees the new credential against the old cache generation.
func (p *AzureProvider) invalidateAccountsCacheLocked() {
	p.cachedAccounts = nil
	p.accountsGen++
}

// fetchAccounts performs the actual ARM subscriptions.List call and resolves
// the default subscription. It holds no lock across the network round-trip and
// runs on the caller's goroutine; getOrFetchAccounts serializes concurrent
// cold-cache callers via singleflight so this runs at most once per
// cache-population window.
//
// The credential and injected client are snapshotted together under a single
// read lock before the call. This runs on behalf of every caller that joined
// the singleflight, so a SetCredential/SetSubscriptionsClient from another
// goroutine would otherwise be an unsynchronized read -- and, worse, could
// pair the old client with the new credential mid-fetch.
func (p *AzureProvider) fetchAccounts(ctx context.Context) ([]common.Account, error) {
	// Use injected client if available (for testing)
	cred, subClient := p.credentialAndSubscriptionsClient()
	if subClient == nil {
		client, err := armsubscriptions.NewClient(cred, nil)
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
		target = os.Getenv(azureSubscriptionIDEnv)
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

// accountsContain reports whether id is one of the discovered subscriptions.
//
// Used to validate an explicitly configured subscription against what the
// principal can actually see, without going through IsDefault -- which
// resolveDefaultSubscription may have set on a DIFFERENT subscription via its
// single-visible-subscription rule.
func accountsContain(accounts []common.Account, id string) bool {
	for i := range accounts {
		if accounts[i].ID == id {
			return true
		}
	}
	return false
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
