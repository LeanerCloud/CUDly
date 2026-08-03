// Package azure provides Azure cloud provider implementation
package azure

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
	"golang.org/x/sync/singleflight"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/logging"
	"github.com/LeanerCloud/CUDly/pkg/provider"
)

// SubscriptionsClient interface for subscription operations (enables mocking)
type SubscriptionsClient interface {
	NewListPager(options *armsubscriptions.ClientListOptions) SubscriptionsPager
	NewListLocationsPager(subscriptionID string, options *armsubscriptions.ClientListLocationsOptions) LocationsPager
}

// SubscriptionsPager interface for subscription pagination (enables mocking)
type SubscriptionsPager interface {
	More() bool
	NextPage(ctx context.Context) (armsubscriptions.ClientListResponse, error)
}

// LocationsPager interface for locations pagination (enables mocking)
type LocationsPager interface {
	More() bool
	NextPage(ctx context.Context) (armsubscriptions.ClientListLocationsResponse, error)
}

// CredentialProvider interface for credential creation (enables mocking)
type CredentialProvider interface {
	NewDefaultAzureCredential() (azcore.TokenCredential, error)
}

// realSubscriptionsClient wraps the real armsubscriptions.Client
type realSubscriptionsClient struct {
	client *armsubscriptions.Client
}

func (r *realSubscriptionsClient) NewListPager(options *armsubscriptions.ClientListOptions) SubscriptionsPager {
	return &realSubscriptionsPager{pager: r.client.NewListPager(options)}
}

func (r *realSubscriptionsClient) NewListLocationsPager(subscriptionID string, options *armsubscriptions.ClientListLocationsOptions) LocationsPager {
	return &realLocationsPager{pager: r.client.NewListLocationsPager(subscriptionID, options)}
}

// realSubscriptionsPager wraps the real subscription pager
type realSubscriptionsPager struct {
	pager *runtime.Pager[armsubscriptions.ClientListResponse]
}

func (r *realSubscriptionsPager) More() bool {
	return r.pager.More()
}

func (r *realSubscriptionsPager) NextPage(ctx context.Context) (armsubscriptions.ClientListResponse, error) {
	return r.pager.NextPage(ctx)
}

// realLocationsPager wraps the real locations pager
type realLocationsPager struct {
	pager *runtime.Pager[armsubscriptions.ClientListLocationsResponse]
}

func (r *realLocationsPager) More() bool {
	return r.pager.More()
}

func (r *realLocationsPager) NextPage(ctx context.Context) (armsubscriptions.ClientListLocationsResponse, error) {
	return r.pager.NextPage(ctx)
}

// realCredentialProvider provides real Azure credentials
type realCredentialProvider struct{}

func (r *realCredentialProvider) NewDefaultAzureCredential() (azcore.TokenCredential, error) {
	return azidentity.NewDefaultAzureCredential(nil)
}

// AzureProvider implements the Provider interface for Azure
type AzureProvider struct {
	cred                azcore.TokenCredential
	credOnce            sync.Once
	credErr             error
	subscriptionID      string
	region              string // Default region for operations
	subscriptionsClient SubscriptionsClient
	credProvider        CredentialProvider

	// accountsMu guards cachedAccounts. GetAccounts, GetServiceClient, and
	// GetRecommendationsClient all resolve the subscription list on the hot
	// path; without caching, a single logical operation (e.g. a
	// multi-subscription recommendations sweep) would re-issue the ARM
	// subscriptions.List call once per internal caller. cachedAccounts is
	// nil until the first successful fetch; InvalidateAccountsCache resets
	// it so tests (and long-lived callers that expect subscription
	// membership to change) can force a refresh. The mutex is held only to
	// read/write cachedAccounts, never across the ARM network round-trip.
	//
	// accountsSF collapses concurrent cold-cache callers into a single
	// in-flight subscriptions.List: the ARM call runs once (not once per
	// caller) and, crucially, outside accountsMu so a slow or hung lookup
	// can never block readers of the cache.
	// accountsGen is bumped by InvalidateAccountsCache. It keys the
	// singleflight call and gates the cache write, so an ARM fetch that
	// started before an invalidation can neither publish its stale snapshot
	// nor be joined by a caller that arrived after it.
	accountsMu     sync.RWMutex
	cachedAccounts []common.Account
	accountsGen    uint64
	accountsSF     singleflight.Group

	// cache subsystem implementation (getOrFetchAccounts, fetchAccounts,
	// cloneAccounts, InvalidateAccountsCache) lives in accounts_cache.go.
}

// NewAzureProvider creates a new Azure provider instance.
//
// Subscription resolution order:
//  1. config.AzureSubscriptionID (typed field, preferred)
//  2. config.Profile (deprecated overload — kept for backwards compatibility)
//
// Credential resolution: if config.AzureTokenCredential is a non-nil
// azcore.TokenCredential, it is installed directly so all downstream clients
// use those credentials. Otherwise, GetCredentials lazily falls back to
// DefaultAzureCredential.
func NewAzureProvider(config *provider.ProviderConfig) (*AzureProvider, error) {
	p := &AzureProvider{}

	if config != nil {
		p.region = config.Region
		p.subscriptionID = resolveAzureSubscriptionID(config)
		if config.AzureTokenCredential != nil {
			if cred, ok := config.AzureTokenCredential.(azcore.TokenCredential); ok {
				p.cred = cred
			} else {
				// Non-nil but wrong-typed slot: log so mis-wirings (wrong
				// concrete type passed to the `any`-typed slot) surface
				// instead of being silently ignored. Falls back to
				// DefaultAzureCredential via GetCredentials.
				logging.Warnf("azure provider: config.AzureTokenCredential is %T, expected azcore.TokenCredential — falling back to ambient credentials", config.AzureTokenCredential)
			}
		}
	}

	return p, nil
}

// resolveAzureSubscriptionID picks the subscription ID from the typed field,
// falling back to the deprecated Profile field.
func resolveAzureSubscriptionID(config *provider.ProviderConfig) string {
	if config.AzureSubscriptionID != "" {
		return config.AzureSubscriptionID
	}
	return config.Profile
}

// SetSubscriptionsClient sets the subscriptions client (for testing).
//
// Drops any cached subscription list: the cache holds what the PREVIOUS
// client returned, and serving that after the client is swapped would answer
// with a different source's subscriptions.
//
// The swap and the invalidation happen under a single accountsMu write so a
// concurrent fetch can never observe the new client alongside the old cache
// generation -- see the accountsMu comment on AzureProvider.
func (p *AzureProvider) SetSubscriptionsClient(client SubscriptionsClient) {
	p.accountsMu.Lock()
	defer p.accountsMu.Unlock()
	p.subscriptionsClient = client
	p.invalidateAccountsCacheLocked()
}

// SetCredentialProvider sets the credential provider (for testing).
//
// Published under accountsMu -- the same lock every other swappable field on
// the provider is published under -- because IsConfigured reads credProvider
// on its lazy ambient-credential path. Leaving this one field outside the lock
// would make that read an unsynchronized data race.
//
// Unlike SetCredential it does not invalidate the accounts cache: credProvider
// only feeds IsConfigured's lazy resolution, which runs at most once and only
// when no credential was installed at all, so at the moment it is read there is
// no cached subscription list resolved under a different credential.
func (p *AzureProvider) SetCredentialProvider(credProvider CredentialProvider) {
	p.accountsMu.Lock()
	defer p.accountsMu.Unlock()
	p.credProvider = credProvider
}

// credentialProvider returns the injected credential provider, or nil when
// none was installed -- in which case callers fall back to
// realCredentialProvider.
//
// Reads go through accountsMu, the same lock SetCredentialProvider publishes
// under, so a swap concurrent with IsConfigured's lazy resolution is a defined
// handoff rather than a data race.
func (p *AzureProvider) credentialProvider() CredentialProvider {
	p.accountsMu.RLock()
	defer p.accountsMu.RUnlock()
	return p.credProvider
}

// SetCredential sets the credential directly.
//
// Also used in production (the scheduler and purchase-execution paths
// construct the provider and then install per-account federated credentials),
// so it must drop any cached subscription list: that cache is the set of
// subscriptions the PREVIOUS credential could see. Serving it to the new
// credential would report subscriptions this principal may have no access to,
// and -- via GetRecommendationsClient's fan-out -- fan out across them.
// Today every caller installs the credential before the first accounts fetch,
// so this is a guard against a future reordering rather than a live leak.
//
// The credential is published under accountsMu, the same lock fetchAccounts
// snapshots it under: writing it outside the lock and only then taking the
// lock to invalidate leaves a window in which an in-flight fetch pairs the new
// credential with the old subscriptions client.
func (p *AzureProvider) SetCredential(cred azcore.TokenCredential) {
	p.accountsMu.Lock()
	defer p.accountsMu.Unlock()
	p.cred = cred
	p.invalidateAccountsCacheLocked()
}

// credential returns the installed credential.
//
// Reads go through accountsMu -- the same lock SetCredential publishes under
// -- so a credential swap concurrent with client construction is a defined
// handoff rather than a data race. Returns nil when no credential has been
// installed yet; every client-construction caller runs after an IsConfigured()
// check, which is what guarantees a non-nil result there.
func (p *AzureProvider) credential() azcore.TokenCredential {
	p.accountsMu.RLock()
	defer p.accountsMu.RUnlock()
	return p.cred
}

// credentialAndSubscriptionsClient snapshots both swappable fields under a
// single read lock.
//
// Taking them together matters: reading them under two separate locks could
// pair a credential with a subscriptions client installed by a different
// SetX call, which is precisely the mixed-state hazard the locking exists to
// prevent. The returned client is nil when none was injected, in which case
// the caller builds a real one from cred.
func (p *AzureProvider) credentialAndSubscriptionsClient() (azcore.TokenCredential, SubscriptionsClient) {
	p.accountsMu.RLock()
	defer p.accountsMu.RUnlock()
	return p.cred, p.subscriptionsClient
}

// publishCredential installs cred under accountsMu without touching the
// accounts cache.
//
// Used by IsConfigured's lazy ambient-credential resolution, which only runs
// when no credential was installed at all. Unlike SetCredential it does not
// invalidate: there is no previous credential whose subscription list could
// have been cached under it.
func (p *AzureProvider) publishCredential(cred azcore.TokenCredential) {
	p.accountsMu.Lock()
	defer p.accountsMu.Unlock()
	p.cred = cred
}

// Name returns the provider name
func (p *AzureProvider) Name() string {
	return "azure"
}

// DisplayName returns the human-readable provider name
func (p *AzureProvider) DisplayName() string {
	return "Microsoft Azure"
}

// IsConfigured checks if Azure credentials are available. Thread-safe via sync.Once.
//
// Sticky-failure contract: when ambient credential resolution fails (e.g. a
// transient IMDS timeout during startup), the error is memoised under
// sync.Once. Subsequent calls return false for the process lifetime with no
// retry. For long-lived server deployments this means a transient auth failure
// at boot requires a process restart to recover. This is acceptable for the
// current Lambda/container deployment model where restarts are cheap; if a
// long-lived daemon pattern is introduced, replace the sync.Once with a
// time-bounded cache or single-flight retry.
func (p *AzureProvider) IsConfigured() bool {
	// If credential was injected via SetCredential, skip the Once path.
	if p.credential() != nil {
		return true
	}

	p.credOnce.Do(func() {
		credProvider := p.credentialProvider()
		if credProvider == nil {
			credProvider = &realCredentialProvider{}
		}
		cred, err := credProvider.NewDefaultAzureCredential()
		if err != nil {
			p.credErr = err
			return
		}
		p.publishCredential(cred)
	})
	return p.credErr == nil
}

// GetCredentials returns Azure credentials
func (p *AzureProvider) GetCredentials() (provider.Credentials, error) {
	if !p.IsConfigured() {
		return nil, fmt.Errorf("azure provider is not configured")
	}

	// DefaultAzureCredential can use multiple sources
	credType := provider.CredentialSourceEnvironment // Default assumption

	return &provider.BaseCredentials{
		Source: credType,
		Valid:  true,
	}, nil
}

// ValidateCredentials validates that Azure credentials are working
func (p *AzureProvider) ValidateCredentials(ctx context.Context) error {
	if !p.IsConfigured() {
		return fmt.Errorf("azure provider is not configured")
	}

	// Use injected client if available (for testing)
	cred, subClient := p.credentialAndSubscriptionsClient()
	if subClient == nil {
		client, err := armsubscriptions.NewClient(cred, nil)
		if err != nil {
			return fmt.Errorf("failed to create subscriptions client: %w", err)
		}
		subClient = &realSubscriptionsClient{client: client}
	}

	t0 := time.Now()
	logging.Infof("purchase[Azure]: subscriptions.NewListPager/NextPage starting (subscription=%s)", p.subscriptionID)
	pager := subClient.NewListPager(nil)
	_, err := pager.NextPage(ctx)
	if err != nil {
		logging.Errorf("purchase[Azure]: subscriptions.NextPage failed after %s: %v", time.Since(t0), err)
		return fmt.Errorf("azure credentials validation failed: %w", err)
	}
	logging.Infof("purchase[Azure]: subscriptions.NextPage returned in %s", time.Since(t0))
	return nil
}

// GetAccounts returns all accessible Azure subscriptions.
//
// IsDefault is set to true for the subscription that matches (in priority order):
//  1. The AzureSubscriptionID set in ProviderConfig (or the Profile fallback).
//  2. The AZURE_SUBSCRIPTION_ID environment variable.
//  3. The sole subscription, when exactly one is visible (mirrors AWS behaviour
//     where the STS-identified account is always the default).
func (p *AzureProvider) GetAccounts(ctx context.Context) ([]common.Account, error) {
	if !p.IsConfigured() {
		return nil, fmt.Errorf("azure provider is not configured")
	}
	return p.getOrFetchAccounts(ctx)
}

// configuredSubscriptionTarget returns the subscription this provider was
// explicitly told to operate on, together with a label naming the knob it came
// from so an error can tell the operator what to fix. It mirrors
// resolveDefaultSubscription's priority: the ProviderConfig field first (which
// resolveAzureSubscriptionID may itself have taken from the deprecated Profile
// overload, hence the generic label), then AZURE_SUBSCRIPTION_ID. Both return
// values are empty when nothing was configured.
//
// p.subscriptionID is written once in NewAzureProvider and never mutated
// afterwards, so it needs no lock (unlike cred/subscriptionsClient/credProvider,
// which have SetX swappers).
func (p *AzureProvider) configuredSubscriptionTarget() (target, source string) {
	if p.subscriptionID != "" {
		return p.subscriptionID, "the configured Azure subscription ID"
	}
	if env := os.Getenv(azureSubscriptionIDEnv); env != "" {
		return env, azureSubscriptionIDEnv
	}
	return "", ""
}

// validateConfiguredSubscription checks an explicitly configured subscription
// target against the subscriptions the principal can actually see, returning
// the validated target -- or ("", nil) when nothing was configured at all.
//
// Every caller must run this BEFORE consulting getDefaultSubscriptionID, and
// that ordering is load-bearing. getDefaultSubscriptionID reads the IsDefault
// flags set by resolveDefaultSubscription, whose rule 3 marks a lone visible
// subscription as the default even when a target was configured and did not
// match it. Consulting that result first would let an invisible target -- an
// operator typo, or a credential that lost access to the intended subscription
// -- silently resolve to whichever single subscription happens to be visible,
// answering a misconfiguration with a plausible wrong subscription instead of
// an error. Validating the target up front makes it fail loud regardless of how
// many subscriptions are visible.
func (p *AzureProvider) validateConfiguredSubscription(accounts []common.Account) (string, error) {
	target, source := p.configuredSubscriptionTarget()
	if target == "" {
		return "", nil
	}
	if !accountsContain(accounts, target) {
		return "", fmt.Errorf(
			"%s is set to %q, which is not among the %d subscriptions visible to this principal",
			source, target, len(accounts))
	}
	return target, nil
}

// resolveSubscriptionIDFromCtx calls GetAccounts and returns the subscription
// to operate on, or a descriptive error if none can be resolved.
//
// This backs GetRegions and GetServiceClient, which means it is on the path
// that enumerates the resources recommendations are computed from -- so it
// applies the same validate-then-default ordering GetRecommendationsClient
// does. Without it the same misconfiguration would error on the
// recommendations path and silently retarget on this one.
func (p *AzureProvider) resolveSubscriptionIDFromCtx(ctx context.Context) (string, error) {
	accounts, err := p.GetAccounts(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to resolve default Azure subscription: %w", err)
	}
	if len(accounts) == 0 {
		return "", fmt.Errorf("no Azure subscriptions found")
	}
	target, err := p.validateConfiguredSubscription(accounts)
	if err != nil {
		return "", err
	}
	if target != "" {
		return target, nil
	}
	id := getDefaultSubscriptionID(accounts)
	if id == "" {
		return "", fmt.Errorf("multiple Azure subscriptions found; set AzureSubscriptionID or AZURE_SUBSCRIPTION_ID")
	}
	return id, nil
}

// GetRegions returns all available Azure regions using the Subscriptions API
func (p *AzureProvider) GetRegions(ctx context.Context) ([]common.Region, error) {
	// Resolve the subscription to query available locations. The wrapper stays
	// neutral about WHY resolution failed: resolveSubscriptionIDFromCtx now
	// also rejects a configured subscription the principal cannot see, and
	// prefixing that with "no Azure subscriptions found" would contradict the
	// inner error, which fires precisely when subscriptions were found.
	subscriptionID, err := p.resolveSubscriptionIDFromCtx(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve the Azure subscription to query regions: %w", err)
	}

	// Use injected client if available (for testing)
	cred, subClient := p.credentialAndSubscriptionsClient()
	if subClient == nil {
		client, err := armsubscriptions.NewClient(cred, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create subscriptions client: %w", err)
		}
		subClient = &realSubscriptionsClient{client: client}
	}

	regions := make([]common.Region, 0)
	pager := subClient.NewListLocationsPager(subscriptionID, nil)

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list Azure locations: %w", err)
		}

		for _, location := range page.Value {
			if location.Name == nil {
				continue
			}

			displayName := *location.Name
			if location.DisplayName != nil {
				displayName = *location.DisplayName
			}

			regions = append(regions, common.Region{
				Provider:    common.ProviderAzure,
				ID:          *location.Name,
				Name:        *location.Name,
				DisplayName: displayName,
			})
		}
	}

	return regions, nil
}

// GetDefaultRegion returns the default Azure region
func (p *AzureProvider) GetDefaultRegion() string {
	if p.region != "" {
		return p.region
	}
	// Default to East US if not specified
	return "eastus"
}

// GetSupportedServices returns the list of services supported by Azure provider
func (p *AzureProvider) GetSupportedServices() []common.ServiceType {
	return []common.ServiceType{
		common.ServiceCompute,
		common.ServiceRelationalDB,
		common.ServiceNoSQL,
		common.ServiceCache,
		common.ServiceMemoryDB,
		common.ServiceSavingsPlansAll,
		common.ServiceSearch,
		common.ServiceDataWarehouse,
	}
}

// GetServiceClient returns a service client for the specified service and region,
// using the default subscription.
//
// When operating across multiple subscriptions (fan-out), prefer
// GetServiceClientForAccount: it accepts an explicit subscriptionID and avoids
// an extra GetAccounts round-trip per iteration.
func (p *AzureProvider) GetServiceClient(ctx context.Context, service common.ServiceType, region string) (provider.ServiceClient, error) {
	if !p.IsConfigured() {
		return nil, fmt.Errorf("azure provider is not configured")
	}

	// Use explicit subscription ID if configured; otherwise resolve from
	// accounts. A pinned subscription is taken on trust and NOT validated
	// against the visible list -- same contract as GetRecommendationsClient's
	// pinned branch. Validating it would force an ARM subscriptions.List on
	// every pinned call (this is the purchase-execution path), and a pinned
	// subscription the principal cannot reach fails loud at the first ARM call
	// anyway. The unpinned branch below is the one that needed a guard,
	// because there a bad target resolved to a plausible wrong subscription
	// instead of failing.
	subscriptionID := p.subscriptionID
	if subscriptionID == "" {
		var err error
		subscriptionID, err = p.resolveSubscriptionIDFromCtx(ctx)
		if err != nil {
			return nil, err
		}
	}

	return p.newServiceClientForSubscription(service, subscriptionID, region)
}

// GetServiceClientForAccount returns a service client for the specified service,
// region, and subscription ID. Use this when iterating over all subscriptions
// returned by GetAccounts to avoid O(n) redundant API calls.
func (p *AzureProvider) GetServiceClientForAccount(ctx context.Context, service common.ServiceType, region, subscriptionID string) (provider.ServiceClient, error) {
	if !p.IsConfigured() {
		return nil, fmt.Errorf("azure provider is not configured")
	}
	if subscriptionID == "" {
		return nil, fmt.Errorf("subscriptionID must not be empty")
	}
	return p.newServiceClientForSubscription(service, subscriptionID, region)
}

// newServiceClientForSubscription constructs the concrete service client for
// the given subscription and region. It is the shared backend for both
// GetServiceClient and GetServiceClientForAccount.
func (p *AzureProvider) newServiceClientForSubscription(service common.ServiceType, subscriptionID, region string) (provider.ServiceClient, error) {
	// Snapshot once so every branch below builds its client from the same
	// credential, even if a SetCredential lands mid-call.
	cred := p.credential()
	switch service {
	case common.ServiceCompute:
		return NewComputeClient(cred, subscriptionID, region), nil
	case common.ServiceRelationalDB:
		return NewDatabaseClient(cred, subscriptionID, region), nil
	case common.ServiceCache:
		return NewCacheClient(cred, subscriptionID, region), nil
	case common.ServiceNoSQL:
		return NewCosmosDBClient(cred, subscriptionID, region), nil
	case common.ServiceMemoryDB:
		return NewManagedRedisClient(cred, subscriptionID, region), nil
	case common.ServiceSavingsPlansAll:
		return NewSavingsPlansClient(cred, subscriptionID, region), nil
	case common.ServiceSearch:
		return NewSearchClient(cred, subscriptionID, region), nil
	case common.ServiceDataWarehouse:
		return NewSynapseClient(cred, subscriptionID, region), nil
	default:
		return nil, fmt.Errorf("unsupported service: %s", service)
	}
}

// GetRecommendationsClient returns a recommendations client.
//
// When a subscription is pinned (p.subscriptionID set, e.g. by the scheduler
// or purchase-execution paths that always operate on one registered
// account), the returned client is scoped to that single subscription --
// unchanged from previous behavior.
//
// When no subscription is pinned, GetRecommendationsClient discovers every
// subscription accessible to the authenticated principal (via the cached
// getOrFetchAccounts) and then narrows in the same order the rest of the
// provider does, so widening the scope is never a side effect of adding
// fan-out:
//
//  1. A default subscription resolvable from the discovered list -- the
//     AZURE_SUBSCRIPTION_ID environment variable, or a lone visible
//     subscription (see resolveDefaultSubscription) -- still scopes the
//     client to that single subscription. Env-pinned callers keep the exact
//     scope they had before org-wide fan-out existed; broadening them to
//     every visible subscription would leak other subscriptions' data into a
//     request that named one.
//  2. A configured AZURE_SUBSCRIPTION_ID that names a subscription this
//     principal cannot see is an error, not a request for org-wide coverage.
//  3. Only when NO subscription was named at all -- an ambiguous
//     multi-subscription principal with nothing configured, which previously
//     produced the hard "multiple Azure subscriptions found; set
//     AzureSubscriptionID or AZURE_SUBSCRIPTION_ID" error -- does the fan-out
//     engage via MultiSubscriptionRecommendationsClient.
//
// Azure has no organization-wide equivalent of AWS Cost Explorer's
// AccountScope=Linked -- the Consumption Reservation Recommendations and
// Advisor APIs are subscription-scoped -- so this client-side fan-out is what
// brings Azure to parity with the AWS provider's automatic whole-organization
// coverage.
func (p *AzureProvider) GetRecommendationsClient(ctx context.Context) (provider.RecommendationsClient, error) {
	if !p.IsConfigured() {
		return nil, fmt.Errorf("azure provider is not configured")
	}

	if p.subscriptionID != "" {
		return NewRecommendationsClient(p.credential(), p.subscriptionID)
	}

	accounts, err := p.getOrFetchAccounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve Azure subscriptions: %w", err)
	}
	if len(accounts) == 0 {
		return nil, fmt.Errorf("no Azure subscriptions found")
	}

	// Snapshot after the accounts resolve, so the three branches below all
	// build their client from one credential rather than re-reading it per
	// branch. A SetCredential landing between the fetch and this read would
	// still pair a fresh credential with a list resolved under the previous
	// one; SetCredential invalidates the cache precisely so the NEXT call
	// re-resolves, which is the guarantee this layer offers.
	cred := p.credential()
	// Step 1: an explicitly configured target is validated against the
	// discovered subscriptions FIRST, before any default resolution -- see
	// validateConfiguredSubscription for why that ordering is load-bearing.
	// p.subscriptionID is empty on this path (the pinned case returned above),
	// so the target here is always AZURE_SUBSCRIPTION_ID.
	target, err := p.validateConfiguredSubscription(accounts)
	if err != nil {
		return nil, err
	}
	if target != "" {
		return NewRecommendationsClient(cred, target)
	}

	// Step 2: no explicit target, but a default may still resolve -- notably
	// the single-discovered-subscription case, which resolveDefaultSubscription
	// marks as the default.
	if defaultID := getDefaultSubscriptionID(accounts); defaultID != "" {
		return NewRecommendationsClient(cred, defaultID)
	}

	// Step 3: scope is genuinely ambiguous -- fan out across the whole org.
	client, err := NewMultiSubscriptionRecommendationsClient(cred, accounts)
	if err != nil {
		return nil, err
	}
	return client, nil
}

// GetRecommendationsClientForAccount returns a recommendations client scoped to
// the given subscription ID. Use this when iterating over all subscriptions
// returned by GetAccounts to avoid O(n) redundant API calls.
func (p *AzureProvider) GetRecommendationsClientForAccount(ctx context.Context, subscriptionID string) (provider.RecommendationsClient, error) {
	if !p.IsConfigured() {
		return nil, fmt.Errorf("azure provider is not configured")
	}
	if subscriptionID == "" {
		return nil, fmt.Errorf("subscriptionID must not be empty")
	}
	return NewRecommendationsClient(p.credential(), subscriptionID)
}

// Register the Azure provider with the global registry
func init() {
	if err := provider.RegisterProvider("azure", func(config *provider.ProviderConfig) (provider.Provider, error) {
		return NewAzureProvider(config)
	}); err != nil {
		panic("failed to register Azure provider: " + err.Error())
	}
}
