package azure

import (
	"context"
	"errors"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/provider"
)

// mockSubscriptionsClient implements SubscriptionsClient for testing.
type mockSubscriptionsClient struct {
	listPagerFunc          func(options *armsubscriptions.ClientListOptions) SubscriptionsPager
	listLocationsPagerFunc func(subscriptionID string, options *armsubscriptions.ClientListLocationsOptions) LocationsPager
}

func (m *mockSubscriptionsClient) NewListPager(options *armsubscriptions.ClientListOptions) SubscriptionsPager {
	if m.listPagerFunc != nil {
		return m.listPagerFunc(options)
	}
	return nil
}

func (m *mockSubscriptionsClient) NewListLocationsPager(subscriptionID string, options *armsubscriptions.ClientListLocationsOptions) LocationsPager {
	if m.listLocationsPagerFunc != nil {
		return m.listLocationsPagerFunc(subscriptionID, options)
	}
	return nil
}

// mockSubscriptionsPager implements SubscriptionsPager for testing.
type mockSubscriptionsPager struct {
	nextErr     error
	pages       []armsubscriptions.ClientListResponse
	pageIdx     int
	errReturned bool
}

func (m *mockSubscriptionsPager) More() bool {
	// If nextErr is set and not yet returned, return true so NextPage gets called
	if m.nextErr != nil && !m.errReturned {
		return true
	}
	return m.pageIdx < len(m.pages)
}

func (m *mockSubscriptionsPager) NextPage(ctx context.Context) (armsubscriptions.ClientListResponse, error) {
	if m.nextErr != nil {
		m.errReturned = true
		return armsubscriptions.ClientListResponse{}, m.nextErr
	}
	if m.pageIdx >= len(m.pages) {
		return armsubscriptions.ClientListResponse{}, errors.New("no more pages")
	}
	page := m.pages[m.pageIdx]
	m.pageIdx++
	return page, nil
}

// mockLocationsPager implements LocationsPager for testing.
type mockLocationsPager struct {
	nextErr     error
	pages       []armsubscriptions.ClientListLocationsResponse
	pageIdx     int
	errReturned bool
}

func (m *mockLocationsPager) More() bool {
	// If nextErr is set and not yet returned, return true so NextPage gets called
	if m.nextErr != nil && !m.errReturned {
		return true
	}
	return m.pageIdx < len(m.pages)
}

func (m *mockLocationsPager) NextPage(ctx context.Context) (armsubscriptions.ClientListLocationsResponse, error) {
	if m.nextErr != nil {
		m.errReturned = true
		return armsubscriptions.ClientListLocationsResponse{}, m.nextErr
	}
	if m.pageIdx >= len(m.pages) {
		return armsubscriptions.ClientListLocationsResponse{}, errors.New("no more pages")
	}
	page := m.pages[m.pageIdx]
	m.pageIdx++
	return page, nil
}

// mockCredentialProvider implements CredentialProvider for testing.
type mockCredentialProvider struct {
	cred azcore.TokenCredential
	err  error
}

func (m *mockCredentialProvider) NewDefaultAzureCredential() (azcore.TokenCredential, error) {
	return m.cred, m.err
}

func TestNewAzureProvider(t *testing.T) {
	tests := []struct {
		name           string
		config         *provider.ProviderConfig
		expectedRegion string
		expectedSubID  string
	}{
		{
			name:           "Nil config",
			config:         nil,
			expectedRegion: "",
			expectedSubID:  "",
		},
		{
			name: "With region only",
			config: &provider.ProviderConfig{
				Region: "westus2",
			},
			expectedRegion: "westus2",
			expectedSubID:  "",
		},
		{
			name: "With profile (subscription ID)",
			config: &provider.ProviderConfig{
				Profile: "subscription-id-123",
			},
			expectedRegion: "",
			expectedSubID:  "subscription-id-123",
		},
		{
			name: "With both region and profile",
			config: &provider.ProviderConfig{
				Region:  "eastus",
				Profile: "my-subscription",
			},
			expectedRegion: "eastus",
			expectedSubID:  "my-subscription",
		},
		{
			name: "Typed AzureSubscriptionID takes precedence over deprecated Profile",
			config: &provider.ProviderConfig{
				AzureSubscriptionID: "typed-sub-id",
				Profile:             "deprecated-sub-id",
			},
			expectedRegion: "",
			expectedSubID:  "typed-sub-id",
		},
		{
			name: "Typed AzureSubscriptionID alone (no Profile fallback needed)",
			config: &provider.ProviderConfig{
				AzureSubscriptionID: "only-typed",
			},
			expectedRegion: "",
			expectedSubID:  "only-typed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := NewAzureProvider(tt.config)
			require.NoError(t, err)
			require.NotNil(t, p)

			assert.Equal(t, tt.expectedRegion, p.region)
			assert.Equal(t, tt.expectedSubID, p.subscriptionID)
		})
	}
}

// TestNewProvider_TokenCredentialInjection verifies that a pre-resolved
// azcore.TokenCredential supplied via config.AzureTokenCredential is installed
// on the provider so subsequent client builds skip the DefaultAzureCredential
// lazy initialisation path.
func TestNewProvider_TokenCredentialInjection(t *testing.T) {
	t.Run("Nil credential leaves cred unset", func(t *testing.T) {
		p, err := NewAzureProvider(&provider.ProviderConfig{
			AzureSubscriptionID: "sub-1",
		})
		require.NoError(t, err)
		assert.Nil(t, p.cred)
	})

	t.Run("Non-nil credential is stored on the provider", func(t *testing.T) {
		fake := &mockTokenCredential{}
		p, err := NewAzureProvider(&provider.ProviderConfig{
			AzureSubscriptionID:  "sub-1",
			AzureTokenCredential: fake,
		})
		require.NoError(t, err)
		assert.Equal(t, azcore.TokenCredential(fake), p.cred)
	})

	t.Run("Wrong-typed credential falls back to ambient + logs warning (defensive type assertion)", func(t *testing.T) {
		// The wrong-typed slot is now logged via logging.Warnf so mis-wirings
		// surface in production logs rather than producing a confusing
		// "ADC unavailable" error. We don't capture the log output here
		// (the project has no log-capture harness); the behavioral assertion
		// is unchanged: p.cred stays nil and NewProvider doesn't error.
		p, err := NewAzureProvider(&provider.ProviderConfig{
			AzureSubscriptionID:  "sub-1",
			AzureTokenCredential: "not-a-credential",
		})
		require.NoError(t, err)
		assert.Nil(t, p.cred)
	})
}

func TestProvider_Name(t *testing.T) {
	p := &Provider{}
	assert.Equal(t, "azure", p.Name())
}

func TestProvider_DisplayName(t *testing.T) {
	p := &Provider{}
	assert.Equal(t, "Microsoft Azure", p.DisplayName())
}

func TestProvider_GetDefaultRegion(t *testing.T) {
	tests := []struct {
		name           string
		provider       *Provider
		expectedRegion string
	}{
		{
			name:           "No region set - returns default",
			provider:       &Provider{},
			expectedRegion: "eastus",
		},
		{
			name:           "Empty region - returns default",
			provider:       &Provider{region: ""},
			expectedRegion: "eastus",
		},
		{
			name:           "Region set - returns configured",
			provider:       &Provider{region: "westeurope"},
			expectedRegion: "westeurope",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expectedRegion, tt.provider.GetDefaultRegion())
		})
	}
}

func TestProvider_GetSupportedServices(t *testing.T) {
	p := &Provider{}
	services := p.GetSupportedServices()

	require.NotEmpty(t, services)
	assert.Contains(t, services, common.ServiceCompute)
	assert.Contains(t, services, common.ServiceRelationalDB)
	assert.Contains(t, services, common.ServiceNoSQL)
	assert.Contains(t, services, common.ServiceCache)
	assert.Contains(t, services, common.ServiceMemoryDB)
	assert.Contains(t, services, common.ServiceSavingsPlans)
	assert.Contains(t, services, common.ServiceSearch)
	assert.Contains(t, services, common.ServiceDataWarehouse)
}

func TestProvider_IsConfigured(t *testing.T) {
	t.Run("returns true when credential is already set", func(t *testing.T) {
		p := &Provider{
			cred: &mockTokenCredential{},
		}
		assert.True(t, p.IsConfigured())
	})

	t.Run("returns true when credential provider succeeds", func(t *testing.T) {
		p := &Provider{}
		p.SetCredentialProvider(&mockCredentialProvider{
			cred: &mockTokenCredential{},
			err:  nil,
		})
		assert.True(t, p.IsConfigured())
		// Verify credential was set
		assert.NotNil(t, p.cred)
	})

	t.Run("returns false when credential provider fails", func(t *testing.T) {
		p := &Provider{}
		p.SetCredentialProvider(&mockCredentialProvider{
			cred: nil,
			err:  errors.New("no credentials"),
		})
		assert.False(t, p.IsConfigured())
	})
}

func TestProvider_GetCredentials_NotConfigured(t *testing.T) {
	// Test GetCredentials when Azure is not configured
	p := &Provider{}
	// If IsConfigured returns false, GetCredentials should return error
	if !p.IsConfigured() {
		_, err := p.GetCredentials()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "azure provider is not configured")
	}
}

func TestProvider_ValidateCredentials(t *testing.T) {
	t.Run("returns error when not configured", func(t *testing.T) {
		p := &Provider{}
		p.SetCredentialProvider(&mockCredentialProvider{
			cred: nil,
			err:  errors.New("no credentials"),
		})
		err := p.ValidateCredentials(context.Background())
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "azure provider is not configured")
	})

	t.Run("success with mock subscriptions client", func(t *testing.T) {
		subID := "test-subscription-id"
		subName := "Test Subscription"

		mockClient := &mockSubscriptionsClient{
			listPagerFunc: func(options *armsubscriptions.ClientListOptions) SubscriptionsPager {
				return &mockSubscriptionsPager{
					pages: []armsubscriptions.ClientListResponse{
						{
							SubscriptionListResult: armsubscriptions.SubscriptionListResult{
								Value: []*armsubscriptions.Subscription{
									{
										SubscriptionID: &subID,
										DisplayName:    &subName,
									},
								},
							},
						},
					},
				}
			},
		}

		p := &Provider{
			cred: &mockTokenCredential{},
		}
		p.SetSubscriptionsClient(mockClient)

		err := p.ValidateCredentials(context.Background())
		assert.NoError(t, err)
	})

	t.Run("returns error when subscription list fails", func(t *testing.T) {
		mockClient := &mockSubscriptionsClient{
			listPagerFunc: func(options *armsubscriptions.ClientListOptions) SubscriptionsPager {
				return &mockSubscriptionsPager{
					nextErr: errors.New("API error"),
				}
			},
		}

		p := &Provider{
			cred: &mockTokenCredential{},
		}
		p.SetSubscriptionsClient(mockClient)

		err := p.ValidateCredentials(context.Background())
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "azure credentials validation failed")
	})
}

func TestProvider_GetServiceClient_NotConfigured(t *testing.T) {
	// Test GetServiceClient when Azure is not configured
	p := &Provider{}
	if !p.IsConfigured() {
		_, err := p.GetServiceClient(context.Background(), common.ServiceCompute, "eastus")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "azure provider is not configured")
	}
}

func TestProvider_GetServiceClient_UnsupportedService(t *testing.T) {
	// Create a mock credential for testing
	p := &Provider{
		cred:           &mockTokenCredential{},
		subscriptionID: "test-subscription",
		region:         "eastus",
	}

	// Test unsupported service type
	_, err := p.GetServiceClient(context.Background(), common.ServiceType("unsupported"), "eastus")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported service")
}

func TestProvider_GetServiceClient_AllServiceTypes(t *testing.T) {
	// Create a provider with mock credentials
	p := &Provider{
		cred:           &mockTokenCredential{},
		subscriptionID: "test-subscription",
		region:         "eastus",
	}

	testCases := []struct {
		service common.ServiceType
	}{
		{common.ServiceCompute},
		{common.ServiceRelationalDB},
		{common.ServiceNoSQL},
		{common.ServiceCache},
		{common.ServiceMemoryDB},
		{common.ServiceSavingsPlans},
		{common.ServiceSearch},
		{common.ServiceDataWarehouse},
	}

	for _, tc := range testCases {
		t.Run(string(tc.service), func(t *testing.T) {
			client, err := p.GetServiceClient(context.Background(), tc.service, "eastus")
			require.NoError(t, err)
			require.NotNil(t, client)
		})
	}
}

func TestProvider_GetRecommendationsClient_NotConfigured(t *testing.T) {
	// Test GetRecommendationsClient when Azure is not configured
	p := &Provider{}
	if !p.IsConfigured() {
		_, err := p.GetRecommendationsClient(context.Background())
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "azure provider is not configured")
	}
}

func TestProvider_GetRecommendationsClient(t *testing.T) {
	// Create a provider with mock credentials
	p := &Provider{
		cred:           &mockTokenCredential{},
		subscriptionID: "test-subscription",
	}

	client, err := p.GetRecommendationsClient(context.Background())
	require.NoError(t, err)
	require.NotNil(t, client)
}

// mockTokenCredential implements azcore.TokenCredential for testing.
type mockTokenCredential struct{}

func (m *mockTokenCredential) GetToken(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "mock-token"}, nil
}

func TestProvider_GetAccounts(t *testing.T) {
	t.Run("success with single subscription", func(t *testing.T) {
		subID := "test-subscription-id"
		subName := "Test Subscription"

		mockClient := &mockSubscriptionsClient{
			listPagerFunc: func(options *armsubscriptions.ClientListOptions) SubscriptionsPager {
				return &mockSubscriptionsPager{
					pages: []armsubscriptions.ClientListResponse{
						{
							SubscriptionListResult: armsubscriptions.SubscriptionListResult{
								Value: []*armsubscriptions.Subscription{
									{
										SubscriptionID: &subID,
										DisplayName:    &subName,
									},
								},
							},
						},
					},
				}
			},
		}

		p := &Provider{
			cred: &mockTokenCredential{},
		}
		p.SetSubscriptionsClient(mockClient)

		accounts, err := p.GetAccounts(context.Background())
		require.NoError(t, err)
		require.Len(t, accounts, 1)
		assert.Equal(t, subID, accounts[0].ID)
		assert.Equal(t, subName, accounts[0].Name)
		assert.Equal(t, common.ProviderAzure, accounts[0].Provider)
	})

	t.Run("success with multiple subscriptions across pages", func(t *testing.T) {
		subID1 := "sub-1"
		subName1 := "Subscription 1"
		subID2 := "sub-2"
		subName2 := "Subscription 2"

		mockClient := &mockSubscriptionsClient{
			listPagerFunc: func(options *armsubscriptions.ClientListOptions) SubscriptionsPager {
				return &mockSubscriptionsPager{
					pages: []armsubscriptions.ClientListResponse{
						{
							SubscriptionListResult: armsubscriptions.SubscriptionListResult{
								Value: []*armsubscriptions.Subscription{
									{
										SubscriptionID: &subID1,
										DisplayName:    &subName1,
									},
								},
							},
						},
						{
							SubscriptionListResult: armsubscriptions.SubscriptionListResult{
								Value: []*armsubscriptions.Subscription{
									{
										SubscriptionID: &subID2,
										DisplayName:    &subName2,
									},
								},
							},
						},
					},
				}
			},
		}

		p := &Provider{
			cred: &mockTokenCredential{},
		}
		p.SetSubscriptionsClient(mockClient)

		accounts, err := p.GetAccounts(context.Background())
		require.NoError(t, err)
		require.Len(t, accounts, 2)
		assert.Equal(t, subID1, accounts[0].ID)
		assert.Equal(t, subID2, accounts[1].ID)
	})

	t.Run("skips subscriptions with nil ID or name", func(t *testing.T) {
		validID := "valid-sub"
		validName := "Valid Subscription"

		mockClient := &mockSubscriptionsClient{
			listPagerFunc: func(options *armsubscriptions.ClientListOptions) SubscriptionsPager {
				return &mockSubscriptionsPager{
					pages: []armsubscriptions.ClientListResponse{
						{
							SubscriptionListResult: armsubscriptions.SubscriptionListResult{
								Value: []*armsubscriptions.Subscription{
									{SubscriptionID: nil, DisplayName: &validName},      // nil ID
									{SubscriptionID: &validID, DisplayName: nil},        // nil name
									{SubscriptionID: &validID, DisplayName: &validName}, // valid
								},
							},
						},
					},
				}
			},
		}

		p := &Provider{
			cred: &mockTokenCredential{},
		}
		p.SetSubscriptionsClient(mockClient)

		accounts, err := p.GetAccounts(context.Background())
		require.NoError(t, err)
		require.Len(t, accounts, 1)
		assert.Equal(t, validID, accounts[0].ID)
	})

	t.Run("returns error on API failure", func(t *testing.T) {
		mockClient := &mockSubscriptionsClient{
			listPagerFunc: func(options *armsubscriptions.ClientListOptions) SubscriptionsPager {
				return &mockSubscriptionsPager{
					nextErr: errors.New("API error"),
				}
			},
		}

		p := &Provider{
			cred: &mockTokenCredential{},
		}
		p.SetSubscriptionsClient(mockClient)

		_, err := p.GetAccounts(context.Background())
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to list subscriptions")
	})
}

func TestProvider_GetRegions(t *testing.T) {
	t.Run("success with locations", func(t *testing.T) {
		subID := "test-subscription"
		subName := "Test Sub"
		locName := "eastus"
		locDisplayName := "East US"

		mockClient := &mockSubscriptionsClient{
			listPagerFunc: func(options *armsubscriptions.ClientListOptions) SubscriptionsPager {
				return &mockSubscriptionsPager{
					pages: []armsubscriptions.ClientListResponse{
						{
							SubscriptionListResult: armsubscriptions.SubscriptionListResult{
								Value: []*armsubscriptions.Subscription{
									{SubscriptionID: &subID, DisplayName: &subName},
								},
							},
						},
					},
				}
			},
			listLocationsPagerFunc: func(subscriptionID string, options *armsubscriptions.ClientListLocationsOptions) LocationsPager {
				return &mockLocationsPager{
					pages: []armsubscriptions.ClientListLocationsResponse{
						{
							LocationListResult: armsubscriptions.LocationListResult{
								Value: []*armsubscriptions.Location{
									{
										Name:        &locName,
										DisplayName: &locDisplayName,
									},
								},
							},
						},
					},
				}
			},
		}

		p := &Provider{
			cred: &mockTokenCredential{},
		}
		p.SetSubscriptionsClient(mockClient)

		regions, err := p.GetRegions(context.Background())
		require.NoError(t, err)
		require.Len(t, regions, 1)
		assert.Equal(t, locName, regions[0].ID)
		assert.Equal(t, locName, regions[0].Name)
		assert.Equal(t, locDisplayName, regions[0].DisplayName)
		assert.Equal(t, common.ProviderAzure, regions[0].Provider)
	})

	t.Run("uses location name when display name is nil", func(t *testing.T) {
		subID := "test-subscription"
		subName := "Test Sub"
		locName := "westus2"

		mockClient := &mockSubscriptionsClient{
			listPagerFunc: func(options *armsubscriptions.ClientListOptions) SubscriptionsPager {
				return &mockSubscriptionsPager{
					pages: []armsubscriptions.ClientListResponse{
						{
							SubscriptionListResult: armsubscriptions.SubscriptionListResult{
								Value: []*armsubscriptions.Subscription{
									{SubscriptionID: &subID, DisplayName: &subName},
								},
							},
						},
					},
				}
			},
			listLocationsPagerFunc: func(subscriptionID string, options *armsubscriptions.ClientListLocationsOptions) LocationsPager {
				return &mockLocationsPager{
					pages: []armsubscriptions.ClientListLocationsResponse{
						{
							LocationListResult: armsubscriptions.LocationListResult{
								Value: []*armsubscriptions.Location{
									{
										Name:        &locName,
										DisplayName: nil,
									},
								},
							},
						},
					},
				}
			},
		}

		p := &Provider{
			cred: &mockTokenCredential{},
		}
		p.SetSubscriptionsClient(mockClient)

		regions, err := p.GetRegions(context.Background())
		require.NoError(t, err)
		require.Len(t, regions, 1)
		assert.Equal(t, locName, regions[0].DisplayName)
	})

	t.Run("skips locations with nil name", func(t *testing.T) {
		subID := "test-subscription"
		subName := "Test Sub"
		validLoc := "validregion"

		mockClient := &mockSubscriptionsClient{
			listPagerFunc: func(options *armsubscriptions.ClientListOptions) SubscriptionsPager {
				return &mockSubscriptionsPager{
					pages: []armsubscriptions.ClientListResponse{
						{
							SubscriptionListResult: armsubscriptions.SubscriptionListResult{
								Value: []*armsubscriptions.Subscription{
									{SubscriptionID: &subID, DisplayName: &subName},
								},
							},
						},
					},
				}
			},
			listLocationsPagerFunc: func(subscriptionID string, options *armsubscriptions.ClientListLocationsOptions) LocationsPager {
				return &mockLocationsPager{
					pages: []armsubscriptions.ClientListLocationsResponse{
						{
							LocationListResult: armsubscriptions.LocationListResult{
								Value: []*armsubscriptions.Location{
									{Name: nil},
									{Name: &validLoc},
								},
							},
						},
					},
				}
			},
		}

		p := &Provider{
			cred: &mockTokenCredential{},
		}
		p.SetSubscriptionsClient(mockClient)

		regions, err := p.GetRegions(context.Background())
		require.NoError(t, err)
		require.Len(t, regions, 1)
		assert.Equal(t, validLoc, regions[0].ID)
	})

	t.Run("returns error when no subscriptions found", func(t *testing.T) {
		mockClient := &mockSubscriptionsClient{
			listPagerFunc: func(options *armsubscriptions.ClientListOptions) SubscriptionsPager {
				return &mockSubscriptionsPager{
					pages: []armsubscriptions.ClientListResponse{
						{
							SubscriptionListResult: armsubscriptions.SubscriptionListResult{
								Value: []*armsubscriptions.Subscription{},
							},
						},
					},
				}
			},
		}

		p := &Provider{
			cred: &mockTokenCredential{},
		}
		p.SetSubscriptionsClient(mockClient)

		_, err := p.GetRegions(context.Background())
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no Azure subscriptions found")
	})

	t.Run("returns error on locations API failure", func(t *testing.T) {
		subID := "test-subscription"
		subName := "Test Sub"

		mockClient := &mockSubscriptionsClient{
			listPagerFunc: func(options *armsubscriptions.ClientListOptions) SubscriptionsPager {
				return &mockSubscriptionsPager{
					pages: []armsubscriptions.ClientListResponse{
						{
							SubscriptionListResult: armsubscriptions.SubscriptionListResult{
								Value: []*armsubscriptions.Subscription{
									{SubscriptionID: &subID, DisplayName: &subName},
								},
							},
						},
					},
				}
			},
			listLocationsPagerFunc: func(subscriptionID string, options *armsubscriptions.ClientListLocationsOptions) LocationsPager {
				return &mockLocationsPager{
					nextErr: errors.New("locations API error"),
				}
			},
		}

		p := &Provider{
			cred: &mockTokenCredential{},
		}
		p.SetSubscriptionsClient(mockClient)

		_, err := p.GetRegions(context.Background())
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to list Azure locations")
	})
}

func TestProvider_GetCredentials(t *testing.T) {
	t.Run("returns error when not configured", func(t *testing.T) {
		p := &Provider{}
		p.SetCredentialProvider(&mockCredentialProvider{
			cred: nil,
			err:  errors.New("no credentials"),
		})
		_, err := p.GetCredentials()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "azure provider is not configured")
	})

	t.Run("success returns credentials info", func(t *testing.T) {
		p := &Provider{
			cred: &mockTokenCredential{},
		}
		creds, err := p.GetCredentials()
		require.NoError(t, err)
		require.NotNil(t, creds)
		assert.True(t, creds.IsValid())
	})
}

func TestProvider_SetterMethods(t *testing.T) {
	t.Run("SetSubscriptionsClient", func(t *testing.T) {
		p := &Provider{}
		mockClient := &mockSubscriptionsClient{}
		p.SetSubscriptionsClient(mockClient)
		assert.NotNil(t, p.subscriptionsClient)
	})

	t.Run("SetCredentialProvider", func(t *testing.T) {
		p := &Provider{}
		mockProvider := &mockCredentialProvider{}
		p.SetCredentialProvider(mockProvider)
		assert.NotNil(t, p.credProvider)
	})

	t.Run("SetCredential", func(t *testing.T) {
		p := &Provider{}
		mockCred := &mockTokenCredential{}
		p.SetCredential(mockCred)
		assert.NotNil(t, p.cred)
	})
}

func TestProvider_GetServiceClient_WithSubscriptionLookup(t *testing.T) {
	t.Run("fetches subscription when subscriptionID not set", func(t *testing.T) {
		subID := "fetched-subscription"
		subName := "Fetched Sub"

		mockClient := &mockSubscriptionsClient{
			listPagerFunc: func(options *armsubscriptions.ClientListOptions) SubscriptionsPager {
				return &mockSubscriptionsPager{
					pages: []armsubscriptions.ClientListResponse{
						{
							SubscriptionListResult: armsubscriptions.SubscriptionListResult{
								Value: []*armsubscriptions.Subscription{
									{SubscriptionID: &subID, DisplayName: &subName},
								},
							},
						},
					},
				}
			},
		}

		p := &Provider{
			cred:           &mockTokenCredential{},
			subscriptionID: "", // Not set - should fetch from accounts
		}
		p.SetSubscriptionsClient(mockClient)

		client, err := p.GetServiceClient(context.Background(), common.ServiceCompute, "eastus")
		require.NoError(t, err)
		require.NotNil(t, client)
	})

	t.Run("returns error when no subscriptions found for service client", func(t *testing.T) {
		mockClient := &mockSubscriptionsClient{
			listPagerFunc: func(options *armsubscriptions.ClientListOptions) SubscriptionsPager {
				return &mockSubscriptionsPager{
					pages: []armsubscriptions.ClientListResponse{
						{
							SubscriptionListResult: armsubscriptions.SubscriptionListResult{
								Value: []*armsubscriptions.Subscription{},
							},
						},
					},
				}
			},
		}

		p := &Provider{
			cred:           &mockTokenCredential{},
			subscriptionID: "",
		}
		p.SetSubscriptionsClient(mockClient)

		_, err := p.GetServiceClient(context.Background(), common.ServiceCompute, "eastus")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no Azure subscriptions found")
	})

	t.Run("returns error when GetAccounts fails", func(t *testing.T) {
		mockClient := &mockSubscriptionsClient{
			listPagerFunc: func(options *armsubscriptions.ClientListOptions) SubscriptionsPager {
				return &mockSubscriptionsPager{
					nextErr: errors.New("API failure"),
				}
			},
		}

		p := &Provider{
			cred:           &mockTokenCredential{},
			subscriptionID: "",
		}
		p.SetSubscriptionsClient(mockClient)

		_, err := p.GetServiceClient(context.Background(), common.ServiceCompute, "eastus")
		assert.Error(t, err)
	})
}

func TestProvider_GetRecommendationsClient_WithSubscriptionLookup(t *testing.T) {
	t.Run("fetches subscription when subscriptionID not set", func(t *testing.T) {
		subID := "fetched-subscription"
		subName := "Fetched Sub"

		mockClient := &mockSubscriptionsClient{
			listPagerFunc: func(options *armsubscriptions.ClientListOptions) SubscriptionsPager {
				return &mockSubscriptionsPager{
					pages: []armsubscriptions.ClientListResponse{
						{
							SubscriptionListResult: armsubscriptions.SubscriptionListResult{
								Value: []*armsubscriptions.Subscription{
									{SubscriptionID: &subID, DisplayName: &subName},
								},
							},
						},
					},
				}
			},
		}

		p := &Provider{
			cred:           &mockTokenCredential{},
			subscriptionID: "", // Not set - should fetch from accounts
		}
		p.SetSubscriptionsClient(mockClient)

		client, err := p.GetRecommendationsClient(context.Background())
		require.NoError(t, err)
		require.NotNil(t, client)
	})

	t.Run("returns error when no subscriptions found", func(t *testing.T) {
		mockClient := &mockSubscriptionsClient{
			listPagerFunc: func(options *armsubscriptions.ClientListOptions) SubscriptionsPager {
				return &mockSubscriptionsPager{
					pages: []armsubscriptions.ClientListResponse{
						{
							SubscriptionListResult: armsubscriptions.SubscriptionListResult{
								Value: []*armsubscriptions.Subscription{},
							},
						},
					},
				}
			},
		}

		p := &Provider{
			cred:           &mockTokenCredential{},
			subscriptionID: "",
		}
		p.SetSubscriptionsClient(mockClient)

		_, err := p.GetRecommendationsClient(context.Background())
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no Azure subscriptions found")
	})
}

// makeSubscriptionsPager is a test helper that returns a pager with the given
// subscription IDs and display names (paired by index).
func makeSubscriptionsPager(ids, names []string) SubscriptionsPager {
	if len(ids) != len(names) {
		panic("makeSubscriptionsPager: ids and names length mismatch")
	}
	subs := make([]*armsubscriptions.Subscription, len(ids))
	for i := range ids {
		id := ids[i]
		name := names[i]
		subs[i] = &armsubscriptions.Subscription{SubscriptionID: &id, DisplayName: &name}
	}
	return &mockSubscriptionsPager{
		pages: []armsubscriptions.ClientListResponse{
			{SubscriptionListResult: armsubscriptions.SubscriptionListResult{Value: subs}},
		},
	}
}

func TestResolveDefaultSubscription(t *testing.T) {
	t.Run("empty list is a no-op", func(t *testing.T) {
		accounts := []common.Account{}
		resolveDefaultSubscription(accounts, "")
		assert.Empty(t, accounts)
	})

	t.Run("explicit sub ID matched", func(t *testing.T) {
		accounts := []common.Account{
			{ID: "sub-1"},
			{ID: "sub-2"},
		}
		resolveDefaultSubscription(accounts, "sub-2")
		assert.False(t, accounts[0].IsDefault)
		assert.True(t, accounts[1].IsDefault)
	})

	t.Run("explicit sub ID not in list falls back to single-subscription rule", func(t *testing.T) {
		accounts := []common.Account{{ID: "sub-1"}}
		resolveDefaultSubscription(accounts, "sub-missing")
		// Single subscription fallback.
		assert.True(t, accounts[0].IsDefault)
	})

	t.Run("explicit sub ID not in list with multiple subscriptions leaves all non-default", func(t *testing.T) {
		accounts := []common.Account{{ID: "sub-1"}, {ID: "sub-2"}}
		resolveDefaultSubscription(accounts, "sub-missing")
		assert.False(t, accounts[0].IsDefault)
		assert.False(t, accounts[1].IsDefault)
	})

	t.Run("single subscription gets IsDefault when no explicit ID", func(t *testing.T) {
		t.Setenv("AZURE_SUBSCRIPTION_ID", "")
		accounts := []common.Account{{ID: "only-sub"}}
		resolveDefaultSubscription(accounts, "")
		assert.True(t, accounts[0].IsDefault)
	})

	t.Run("multiple subscriptions with no explicit ID stay non-default", func(t *testing.T) {
		t.Setenv("AZURE_SUBSCRIPTION_ID", "")
		accounts := []common.Account{{ID: "sub-1"}, {ID: "sub-2"}}
		resolveDefaultSubscription(accounts, "")
		assert.False(t, accounts[0].IsDefault)
		assert.False(t, accounts[1].IsDefault)
	})
}

func TestProvider_GetAccounts_IsDefault(t *testing.T) {
	t.Run("single subscription is marked IsDefault", func(t *testing.T) {
		t.Setenv("AZURE_SUBSCRIPTION_ID", "")
		subID := "only-sub"
		subName := "Only Sub"

		p := &Provider{cred: &mockTokenCredential{}}
		p.SetSubscriptionsClient(&mockSubscriptionsClient{
			listPagerFunc: func(_ *armsubscriptions.ClientListOptions) SubscriptionsPager {
				return makeSubscriptionsPager([]string{subID}, []string{subName})
			},
		})

		accounts, err := p.GetAccounts(context.Background())
		require.NoError(t, err)
		require.Len(t, accounts, 1)
		assert.True(t, accounts[0].IsDefault)
	})

	t.Run("configured subscriptionID is marked IsDefault among many", func(t *testing.T) {
		t.Setenv("AZURE_SUBSCRIPTION_ID", "sub-env")
		p := &Provider{
			cred:           &mockTokenCredential{},
			subscriptionID: "sub-2",
		}
		p.SetSubscriptionsClient(&mockSubscriptionsClient{
			listPagerFunc: func(_ *armsubscriptions.ClientListOptions) SubscriptionsPager {
				return makeSubscriptionsPager(
					[]string{"sub-1", "sub-2", "sub-3"},
					[]string{"Sub 1", "Sub 2", "Sub 3"},
				)
			},
		})

		accounts, err := p.GetAccounts(context.Background())
		require.NoError(t, err)
		require.Len(t, accounts, 3)
		assert.False(t, accounts[0].IsDefault, "sub-1 should not be default")
		assert.True(t, accounts[1].IsDefault, "sub-2 should be default")
		assert.False(t, accounts[2].IsDefault, "sub-3 should not be default")
	})

	t.Run("multiple subscriptions without explicit config all non-default", func(t *testing.T) {
		t.Setenv("AZURE_SUBSCRIPTION_ID", "")
		p := &Provider{cred: &mockTokenCredential{}}
		p.SetSubscriptionsClient(&mockSubscriptionsClient{
			listPagerFunc: func(_ *armsubscriptions.ClientListOptions) SubscriptionsPager {
				return makeSubscriptionsPager(
					[]string{"sub-1", "sub-2"},
					[]string{"Sub 1", "Sub 2"},
				)
			},
		})

		accounts, err := p.GetAccounts(context.Background())
		require.NoError(t, err)
		require.Len(t, accounts, 2)
		assert.False(t, accounts[0].IsDefault)
		assert.False(t, accounts[1].IsDefault)
	})

	t.Run("env subscriptionID is marked IsDefault when config is empty", func(t *testing.T) {
		t.Setenv("AZURE_SUBSCRIPTION_ID", "sub-2")
		p := &Provider{cred: &mockTokenCredential{}}
		p.SetSubscriptionsClient(&mockSubscriptionsClient{
			listPagerFunc: func(_ *armsubscriptions.ClientListOptions) SubscriptionsPager {
				return makeSubscriptionsPager(
					[]string{"sub-1", "sub-2", "sub-3"},
					[]string{"Sub 1", "Sub 2", "Sub 3"},
				)
			},
		})

		accounts, err := p.GetAccounts(context.Background())
		require.NoError(t, err)
		require.Len(t, accounts, 3)
		assert.False(t, accounts[0].IsDefault)
		assert.True(t, accounts[1].IsDefault)
		assert.False(t, accounts[2].IsDefault)
	})
}

func TestGetDefaultSubscriptionID(t *testing.T) {
	t.Run("returns IsDefault account when present", func(t *testing.T) {
		accounts := []common.Account{
			{ID: "sub-1", IsDefault: false},
			{ID: "sub-2", IsDefault: true},
			{ID: "sub-3", IsDefault: false},
		}
		assert.Equal(t, "sub-2", getDefaultSubscriptionID(accounts))
	})

	t.Run("returns empty string when none marked default", func(t *testing.T) {
		accounts := []common.Account{
			{ID: "sub-1", IsDefault: false},
			{ID: "sub-2", IsDefault: false},
		}
		assert.Equal(t, "", getDefaultSubscriptionID(accounts))
	})
}

func TestProvider_GetServiceClientForAccount(t *testing.T) {
	t.Run("returns client for explicit subscription", func(t *testing.T) {
		p := &Provider{cred: &mockTokenCredential{}}

		services := []common.ServiceType{
			common.ServiceCompute,
			common.ServiceRelationalDB,
			common.ServiceNoSQL,
			common.ServiceCache,
			common.ServiceMemoryDB,
			common.ServiceSavingsPlans,
			common.ServiceSearch,
			common.ServiceDataWarehouse,
		}
		for _, svc := range services {
			t.Run(string(svc), func(t *testing.T) {
				client, err := p.GetServiceClientForAccount(context.Background(), svc, "eastus", "explicit-sub")
				require.NoError(t, err)
				require.NotNil(t, client)
			})
		}
	})

	t.Run("returns error for empty subscriptionID", func(t *testing.T) {
		p := &Provider{cred: &mockTokenCredential{}}
		_, err := p.GetServiceClientForAccount(context.Background(), common.ServiceCompute, "eastus", "")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "subscriptionID must not be empty")
	})

	t.Run("returns error for unsupported service", func(t *testing.T) {
		p := &Provider{cred: &mockTokenCredential{}}
		_, err := p.GetServiceClientForAccount(context.Background(), common.ServiceType("unknown"), "eastus", "sub-1")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported service")
	})

	t.Run("returns error when not configured", func(t *testing.T) {
		p := &Provider{}
		p.SetCredentialProvider(&mockCredentialProvider{err: errors.New("no cred")})
		_, err := p.GetServiceClientForAccount(context.Background(), common.ServiceCompute, "eastus", "sub-1")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "azure provider is not configured")
	})
}

func TestProvider_GetRecommendationsClientForAccount(t *testing.T) {
	t.Run("returns client for explicit subscription", func(t *testing.T) {
		p := &Provider{cred: &mockTokenCredential{}}
		client, err := p.GetRecommendationsClientForAccount(context.Background(), "explicit-sub")
		require.NoError(t, err)
		require.NotNil(t, client)
	})

	t.Run("returns error for empty subscriptionID", func(t *testing.T) {
		p := &Provider{cred: &mockTokenCredential{}}
		_, err := p.GetRecommendationsClientForAccount(context.Background(), "")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "subscriptionID must not be empty")
	})

	t.Run("returns error when not configured", func(t *testing.T) {
		p := &Provider{}
		p.SetCredentialProvider(&mockCredentialProvider{err: errors.New("no cred")})
		_, err := p.GetRecommendationsClientForAccount(context.Background(), "sub-1")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "azure provider is not configured")
	})
}
