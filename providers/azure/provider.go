// Package azure provides Azure cloud provider implementation
package azure

import (
	"context"
	"fmt"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"

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

	// cachedAccounts caches the result of the ARM subscriptions list so that
	// GetServiceClient, GetRecommendationsClient, and GetRegions do not each
	// trigger a separate network round-trip. Protected by accountsMu.
	accountsMu     sync.RWMutex
	cachedAccounts []common.Account
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

// SetSubscriptionsClient sets the subscriptions client (for testing)
func (p *AzureProvider) SetSubscriptionsClient(client SubscriptionsClient) {
	p.subscriptionsClient = client
}

// SetCredentialProvider sets the credential provider (for testing)
func (p *AzureProvider) SetCredentialProvider(credProvider CredentialProvider) {
	p.credProvider = credProvider
}

// SetCredential sets the credential directly (for testing)
func (p *AzureProvider) SetCredential(cred azcore.TokenCredential) {
	p.cred = cred
}

// InvalidateAccountsCache clears the cached subscriptions list. This is
// primarily useful in tests that need to replace the subscriptionsClient
// after accounts have already been fetched.
func (p *AzureProvider) InvalidateAccountsCache() {
	p.accountsMu.Lock()
	p.cachedAccounts = nil
	p.accountsMu.Unlock()
}

// getOrFetchAccounts returns the cached subscription list, fetching it from
// the ARM subscriptions API on the first call (or after a cache invalidation).
// Thread-safe: uses a double-checked locking pattern so concurrent callers
// each receive the same list without triggering multiple API calls.
//
// Error policy: if the API call fails, the cache is NOT populated so that a
// subsequent call retries. Transient ARM errors therefore do not permanently
// disable subscription discovery for the lifetime of the provider.
func (p *AzureProvider) getOrFetchAccounts(ctx context.Context) ([]common.Account, error) {
	// Fast path: cache populated.
	p.accountsMu.RLock()
	if p.cachedAccounts != nil {
		accounts := p.cachedAccounts
		p.accountsMu.RUnlock()
		return accounts, nil
	}
	p.accountsMu.RUnlock()

	// Slow path: need to fetch. Acquire write lock and re-check.
	p.accountsMu.Lock()
	defer p.accountsMu.Unlock()

	if p.cachedAccounts != nil {
		return p.cachedAccounts, nil
	}

	accounts, err := p.fetchAccountsLocked(ctx)
	if err != nil {
		return nil, err
	}
	p.cachedAccounts = accounts
	return accounts, nil
}

// fetchAccountsLocked performs the actual ARM API call. Must be called with
// accountsMu write-locked.
func (p *AzureProvider) fetchAccountsLocked(ctx context.Context) ([]common.Account, error) {
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
				// Azure does not have a clear "default" subscription concept.
				// Users can set AZURE_SUBSCRIPTION_ID to specify which to use.
				IsDefault: false,
			})
		}
	}

	return accounts, nil
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
func (p *AzureProvider) IsConfigured() bool {
	// If credential was injected via SetCredential, skip the Once path.
	if p.cred != nil {
		return true
	}

	p.credOnce.Do(func() {
		var credProvider CredentialProvider
		if p.credProvider != nil {
			credProvider = p.credProvider
		} else {
			credProvider = &realCredentialProvider{}
		}
		cred, err := credProvider.NewDefaultAzureCredential()
		if err != nil {
			p.credErr = err
			return
		}
		p.cred = cred
	})
	return p.credErr == nil
}

// GetCredentials returns Azure credentials
func (p *AzureProvider) GetCredentials() (provider.Credentials, error) {
	if !p.IsConfigured() {
		return nil, fmt.Errorf("Azure is not configured")
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
		return fmt.Errorf("Azure is not configured")
	}

	// Use injected client if available (for testing)
	var subClient SubscriptionsClient
	if p.subscriptionsClient != nil {
		subClient = p.subscriptionsClient
	} else {
		client, err := armsubscriptions.NewClient(p.cred, nil)
		if err != nil {
			return fmt.Errorf("failed to create subscriptions client: %w", err)
		}
		subClient = &realSubscriptionsClient{client: client}
	}

	pager := subClient.NewListPager(nil)
	_, err := pager.NextPage(ctx)
	if err != nil {
		return fmt.Errorf("Azure credentials validation failed: %w", err)
	}

	return nil
}

// GetAccounts returns all accessible Azure subscriptions.
//
// Results are cached after the first successful call so that repeated
// invocations (e.g. from GetServiceClient, GetRecommendationsClient, and
// GetRegions in the same request) do not each trigger a separate ARM API
// round-trip. The cache is keyed to the provider lifetime; create a new
// provider to force a refresh.
func (p *AzureProvider) GetAccounts(ctx context.Context) ([]common.Account, error) {
	if !p.IsConfigured() {
		return nil, fmt.Errorf("Azure is not configured")
	}
	return p.getOrFetchAccounts(ctx)
}

// GetRegions returns all available Azure regions using the Subscriptions API
func (p *AzureProvider) GetRegions(ctx context.Context) ([]common.Region, error) {
	// Get first subscription to query available locations
	accounts, err := p.GetAccounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("no Azure subscriptions found to query regions: %w", err)
	}
	if len(accounts) == 0 {
		return nil, fmt.Errorf("no Azure subscriptions found to query regions")
	}

	subscriptionID := accounts[0].ID

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
	}
}

// GetServiceClient returns a service client for the specified service and region.
//
// Subscription resolution order:
//  1. p.subscriptionID (set from ProviderConfig.AzureSubscriptionID or .Profile)
//  2. First subscription returned by GetAccounts (cache-backed after the first call)
//
// For multi-subscription scenarios the caller should enumerate subscriptions
// via GetAccounts and create one provider per subscription (using
// ProviderConfig.AzureSubscriptionID), rather than relying on the fallback.
// The purchase execution path (internal/purchase) already does this.
func (p *AzureProvider) GetServiceClient(ctx context.Context, service common.ServiceType, region string) (provider.ServiceClient, error) {
	if !p.IsConfigured() {
		return nil, fmt.Errorf("Azure is not configured")
	}

	// Get subscription ID (use first available if not set).
	subscriptionID := p.subscriptionID
	if subscriptionID == "" {
		accounts, err := p.getOrFetchAccounts(ctx)
		if err != nil {
			return nil, fmt.Errorf("no Azure subscriptions found: %w", err)
		}
		if len(accounts) == 0 {
			return nil, fmt.Errorf("no Azure subscriptions found")
		}
		subscriptionID = accounts[0].ID
	}

	switch service {
	case common.ServiceCompute:
		return NewComputeClient(p.cred, subscriptionID, region), nil
	case common.ServiceRelationalDB:
		return NewDatabaseClient(p.cred, subscriptionID, region), nil
	case common.ServiceCache:
		return NewCacheClient(p.cred, subscriptionID, region), nil
	case common.ServiceNoSQL:
		return NewCosmosDBClient(p.cred, subscriptionID, region), nil
	default:
		return nil, fmt.Errorf("unsupported service: %s", service)
	}
}

// GetRecommendationsClient returns a recommendations client.
//
// When a subscription is pinned (ProviderConfig.AzureSubscriptionID / .Profile),
// a single-subscription RecommendationsClientAdapter is returned -- same
// behaviour as before this change.
//
// When no subscription is pinned, all accessible subscriptions are discovered
// via GetAccounts (cache-backed) and a MultiSubscriptionRecommendationsClient
// is returned so recommendations are collected across every subscription the
// authenticated principal can see. This matches the AWS behaviour where Cost
// Explorer automatically spans the whole organisation.
func (p *AzureProvider) GetRecommendationsClient(ctx context.Context) (provider.RecommendationsClient, error) {
	if !p.IsConfigured() {
		return nil, fmt.Errorf("Azure is not configured")
	}

	// Pinned subscription: return a single-subscription adapter.
	if p.subscriptionID != "" {
		return NewRecommendationsClient(p.cred, p.subscriptionID)
	}

	// No pinned subscription: discover all accessible subscriptions and fan
	// out across them.
	accounts, err := p.getOrFetchAccounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to discover Azure subscriptions: %w", err)
	}
	if len(accounts) == 0 {
		return nil, fmt.Errorf("no Azure subscriptions found")
	}

	if len(accounts) == 1 {
		// Optimise the common single-subscription case.
		return NewRecommendationsClient(p.cred, accounts[0].ID)
	}

	return NewMultiSubscriptionRecommendationsClient(p.cred, accounts)
}

// Register the Azure provider with the global registry
func init() {
	provider.RegisterProvider("azure", func(config *provider.ProviderConfig) (provider.Provider, error) {
		return NewAzureProvider(config)
	})
}
