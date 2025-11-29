// Package azure provides Azure cloud provider implementation
package azure

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"

	"github.com/LeanerCloud/CUDly/pkg/common"
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
	cred               azcore.TokenCredential
	subscriptionID     string
	region             string // Default region for operations
	subscriptionsClient SubscriptionsClient
	credProvider       CredentialProvider
}

// NewAzureProvider creates a new Azure provider instance
func NewAzureProvider(config *provider.ProviderConfig) (*AzureProvider, error) {
	p := &AzureProvider{}

	if config != nil {
		p.region = config.Region
		// In Azure, Profile maps to subscription ID
		p.subscriptionID = config.Profile
	}

	return p, nil
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

// IsConfigured checks if Azure credentials are available
func (p *AzureProvider) IsConfigured() bool {
	// If credential is already set, we're configured
	if p.cred != nil {
		return true
	}

	// Use injected credential provider if available (for testing)
	var credProvider CredentialProvider
	if p.credProvider != nil {
		credProvider = p.credProvider
	} else {
		credProvider = &realCredentialProvider{}
	}

	// Try to create default Azure credential
	cred, err := credProvider.NewDefaultAzureCredential()
	if err != nil {
		return false
	}

	p.cred = cred
	return true
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

// GetAccounts returns all accessible Azure subscriptions
func (p *AzureProvider) GetAccounts(ctx context.Context) ([]common.Account, error) {
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
				// Azure doesn't have a clear "default" subscription concept
				// Users can set AZURE_SUBSCRIPTION_ID environment variable to specify which to use
				IsDefault: false,
			})
		}
	}

	return accounts, nil
}

// GetRegions returns all available Azure regions using the Subscriptions API
func (p *AzureProvider) GetRegions(ctx context.Context) ([]common.Region, error) {
	// Get first subscription to query available locations
	accounts, err := p.GetAccounts(ctx)
	if err != nil || len(accounts) == 0 {
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

// GetServiceClient returns a service client for the specified service and region
func (p *AzureProvider) GetServiceClient(ctx context.Context, service common.ServiceType, region string) (provider.ServiceClient, error) {
	if !p.IsConfigured() {
		return nil, fmt.Errorf("Azure is not configured")
	}

	// Get subscription ID (use first available if not set)
	subscriptionID := p.subscriptionID
	if subscriptionID == "" {
		accounts, err := p.GetAccounts(ctx)
		if err != nil || len(accounts) == 0 {
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
	default:
		return nil, fmt.Errorf("unsupported service: %s", service)
	}
}

// GetRecommendationsClient returns a recommendations client
func (p *AzureProvider) GetRecommendationsClient(ctx context.Context) (provider.RecommendationsClient, error) {
	if !p.IsConfigured() {
		return nil, fmt.Errorf("Azure is not configured")
	}

	// Get subscription ID
	subscriptionID := p.subscriptionID
	if subscriptionID == "" {
		accounts, err := p.GetAccounts(ctx)
		if err != nil || len(accounts) == 0 {
			return nil, fmt.Errorf("no Azure subscriptions found")
		}
		subscriptionID = accounts[0].ID
	}

	return NewRecommendationsClient(p.cred, subscriptionID), nil
}

// Register the Azure provider with the global registry
func init() {
	provider.RegisterProvider("azure", func(config *provider.ProviderConfig) (provider.Provider, error) {
		return NewAzureProvider(config)
	})
}
