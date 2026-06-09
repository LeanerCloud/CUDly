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

// resolveSubscriptionIDFromCtx calls GetAccounts and returns the default
// subscription ID, or a descriptive error if none can be resolved.
func (p *AzureProvider) resolveSubscriptionIDFromCtx(ctx context.Context) (string, error) {
	accounts, err := p.GetAccounts(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to resolve default Azure subscription: %w", err)
	}
	if len(accounts) == 0 {
		return "", fmt.Errorf("no Azure subscriptions found")
	}
	id := getDefaultSubscriptionID(accounts)
	if id == "" {
		return "", fmt.Errorf("multiple Azure subscriptions found; set AzureSubscriptionID or AZURE_SUBSCRIPTION_ID")
	}
	return id, nil
}

// GetRegions returns all available Azure regions using the Subscriptions API
func (p *AzureProvider) GetRegions(ctx context.Context) ([]common.Region, error) {
	// Resolve the subscription to query available locations.
	subscriptionID, err := p.resolveSubscriptionIDFromCtx(ctx)
	if err != nil {
		return nil, fmt.Errorf("no Azure subscriptions found to query regions: %w", err)
	}

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
		common.ServiceMemoryDB,
		common.ServiceSavingsPlans,
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

	// Use explicit subscription ID if configured; otherwise resolve from accounts.
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
	switch service {
	case common.ServiceCompute:
		return NewComputeClient(p.cred, subscriptionID, region), nil
	case common.ServiceRelationalDB:
		return NewDatabaseClient(p.cred, subscriptionID, region), nil
	case common.ServiceCache:
		return NewCacheClient(p.cred, subscriptionID, region), nil
	case common.ServiceNoSQL:
		return NewCosmosDBClient(p.cred, subscriptionID, region), nil
	case common.ServiceMemoryDB:
		return NewManagedRedisClient(p.cred, subscriptionID, region), nil
	case common.ServiceSavingsPlans:
		return NewSavingsPlansClient(p.cred, subscriptionID, region), nil
	case common.ServiceSearch:
		return NewSearchClient(p.cred, subscriptionID, region), nil
	case common.ServiceDataWarehouse:
		return NewSynapseClient(p.cred, subscriptionID, region), nil
	default:
		return nil, fmt.Errorf("unsupported service: %s", service)
	}
}

// GetRecommendationsClient returns a recommendations client for the default
// subscription.
//
// When operating across multiple subscriptions (fan-out), prefer
// GetRecommendationsClientForAccount.
func (p *AzureProvider) GetRecommendationsClient(ctx context.Context) (provider.RecommendationsClient, error) {
	if !p.IsConfigured() {
		return nil, fmt.Errorf("azure provider is not configured")
	}

	// Use explicit subscription ID if configured; otherwise resolve from accounts.
	subscriptionID := p.subscriptionID
	if subscriptionID == "" {
		var err error
		subscriptionID, err = p.resolveSubscriptionIDFromCtx(ctx)
		if err != nil {
			return nil, err
		}
	}

	return NewRecommendationsClient(p.cred, subscriptionID)
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
	return NewRecommendationsClient(p.cred, subscriptionID)
}

// Register the Azure provider with the global registry
func init() {
	provider.RegisterProvider("azure", func(config *provider.ProviderConfig) (provider.Provider, error) {
		return NewAzureProvider(config)
	})
}
