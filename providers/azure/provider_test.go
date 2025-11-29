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

// mockSubscriptionsClient implements SubscriptionsClient for testing
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

// mockSubscriptionsPager implements SubscriptionsPager for testing
type mockSubscriptionsPager struct {
	pages       []armsubscriptions.ClientListResponse
	pageIdx     int
	nextErr     error
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

// mockLocationsPager implements LocationsPager for testing
type mockLocationsPager struct {
	pages       []armsubscriptions.ClientListLocationsResponse
	pageIdx     int
	nextErr     error
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

// mockCredentialProvider implements CredentialProvider for testing
type mockCredentialProvider struct {
	cred azcore.TokenCredential
	err  error
}

func (m *mockCredentialProvider) NewDefaultAzureCredential() (azcore.TokenCredential, error) {
	return m.cred, m.err
}

// Helper function to create a string pointer
func stringPtr(s string) *string {
	return &s
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

func TestAzureProvider_Name(t *testing.T) {
	p := &AzureProvider{}
	assert.Equal(t, "azure", p.Name())
}

func TestAzureProvider_DisplayName(t *testing.T) {
	p := &AzureProvider{}
	assert.Equal(t, "Microsoft Azure", p.DisplayName())
}

func TestAzureProvider_GetDefaultRegion(t *testing.T) {
	tests := []struct {
		name           string
		provider       *AzureProvider
		expectedRegion string
	}{
		{
			name:           "No region set - returns default",
			provider:       &AzureProvider{},
			expectedRegion: "eastus",
		},
		{
			name:           "Empty region - returns default",
			provider:       &AzureProvider{region: ""},
			expectedRegion: "eastus",
		},
		{
			name:           "Region set - returns configured",
			provider:       &AzureProvider{region: "westeurope"},
			expectedRegion: "westeurope",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expectedRegion, tt.provider.GetDefaultRegion())
		})
	}
}

func TestAzureProvider_GetSupportedServices(t *testing.T) {
	p := &AzureProvider{}
	services := p.GetSupportedServices()

	require.NotEmpty(t, services)
	assert.Contains(t, services, common.ServiceCompute)
	assert.Contains(t, services, common.ServiceRelationalDB)
	assert.Contains(t, services, common.ServiceNoSQL)
	assert.Contains(t, services, common.ServiceCache)
}

func TestAzureProvider_IsConfigured(t *testing.T) {
	t.Run("returns true when credential is already set", func(t *testing.T) {
		p := &AzureProvider{
			cred: &mockTokenCredential{},
		}
		assert.True(t, p.IsConfigured())
	})

	t.Run("returns true when credential provider succeeds", func(t *testing.T) {
		p := &AzureProvider{}
		p.SetCredentialProvider(&mockCredentialProvider{
			cred: &mockTokenCredential{},
			err:  nil,
		})
		assert.True(t, p.IsConfigured())
		// Verify credential was set
		assert.NotNil(t, p.cred)
	})

	t.Run("returns false when credential provider fails", func(t *testing.T) {
		p := &AzureProvider{}
		p.SetCredentialProvider(&mockCredentialProvider{
			cred: nil,
			err:  errors.New("no credentials"),
		})
		assert.False(t, p.IsConfigured())
	})
}

func TestAzureProvider_GetCredentials_NotConfigured(t *testing.T) {
	// Test GetCredentials when Azure is not configured
	p := &AzureProvider{}
	// If IsConfigured returns false, GetCredentials should return error
	if !p.IsConfigured() {
		_, err := p.GetCredentials()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "Azure is not configured")
	}
}

func TestAzureProvider_ValidateCredentials(t *testing.T) {
	t.Run("returns error when not configured", func(t *testing.T) {
		p := &AzureProvider{}
		p.SetCredentialProvider(&mockCredentialProvider{
			cred: nil,
			err:  errors.New("no credentials"),
		})
		err := p.ValidateCredentials(context.Background())
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "Azure is not configured")
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

		p := &AzureProvider{
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

		p := &AzureProvider{
			cred: &mockTokenCredential{},
		}
		p.SetSubscriptionsClient(mockClient)

		err := p.ValidateCredentials(context.Background())
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "Azure credentials validation failed")
	})
}

func TestAzureProvider_GetServiceClient_NotConfigured(t *testing.T) {
	// Test GetServiceClient when Azure is not configured
	p := &AzureProvider{}
	if !p.IsConfigured() {
		_, err := p.GetServiceClient(context.Background(), common.ServiceCompute, "eastus")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "Azure is not configured")
	}
}

func TestAzureProvider_GetServiceClient_UnsupportedService(t *testing.T) {
	// Create a mock credential for testing
	p := &AzureProvider{
		cred:           &mockTokenCredential{},
		subscriptionID: "test-subscription",
		region:         "eastus",
	}

	// Test unsupported service type
	_, err := p.GetServiceClient(context.Background(), common.ServiceType("unsupported"), "eastus")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported service")
}

func TestAzureProvider_GetServiceClient_AllServiceTypes(t *testing.T) {
	// Create a provider with mock credentials
	p := &AzureProvider{
		cred:           &mockTokenCredential{},
		subscriptionID: "test-subscription",
		region:         "eastus",
	}

	testCases := []struct {
		service common.ServiceType
	}{
		{common.ServiceCompute},
		{common.ServiceRelationalDB},
		{common.ServiceCache},
	}

	for _, tc := range testCases {
		t.Run(string(tc.service), func(t *testing.T) {
			client, err := p.GetServiceClient(context.Background(), tc.service, "eastus")
			require.NoError(t, err)
			require.NotNil(t, client)
		})
	}
}

func TestAzureProvider_GetRecommendationsClient_NotConfigured(t *testing.T) {
	// Test GetRecommendationsClient when Azure is not configured
	p := &AzureProvider{}
	if !p.IsConfigured() {
		_, err := p.GetRecommendationsClient(context.Background())
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "Azure is not configured")
	}
}

func TestAzureProvider_GetRecommendationsClient(t *testing.T) {
	// Create a provider with mock credentials
	p := &AzureProvider{
		cred:           &mockTokenCredential{},
		subscriptionID: "test-subscription",
	}

	client, err := p.GetRecommendationsClient(context.Background())
	require.NoError(t, err)
	require.NotNil(t, client)
}

// mockTokenCredential implements azcore.TokenCredential for testing
type mockTokenCredential struct{}

func (m *mockTokenCredential) GetToken(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "mock-token"}, nil
}

func TestAzureProvider_GetAccounts(t *testing.T) {
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

		p := &AzureProvider{
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

		p := &AzureProvider{
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
									{SubscriptionID: nil, DisplayName: &validName},    // nil ID
									{SubscriptionID: &validID, DisplayName: nil},       // nil name
									{SubscriptionID: &validID, DisplayName: &validName}, // valid
								},
							},
						},
					},
				}
			},
		}

		p := &AzureProvider{
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

		p := &AzureProvider{
			cred: &mockTokenCredential{},
		}
		p.SetSubscriptionsClient(mockClient)

		_, err := p.GetAccounts(context.Background())
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to list subscriptions")
	})
}

func TestAzureProvider_GetRegions(t *testing.T) {
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

		p := &AzureProvider{
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

		p := &AzureProvider{
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

		p := &AzureProvider{
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

		p := &AzureProvider{
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

		p := &AzureProvider{
			cred: &mockTokenCredential{},
		}
		p.SetSubscriptionsClient(mockClient)

		_, err := p.GetRegions(context.Background())
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to list Azure locations")
	})
}

func TestAzureProvider_GetCredentials(t *testing.T) {
	t.Run("returns error when not configured", func(t *testing.T) {
		p := &AzureProvider{}
		p.SetCredentialProvider(&mockCredentialProvider{
			cred: nil,
			err:  errors.New("no credentials"),
		})
		_, err := p.GetCredentials()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "Azure is not configured")
	})

	t.Run("success returns credentials info", func(t *testing.T) {
		p := &AzureProvider{
			cred: &mockTokenCredential{},
		}
		creds, err := p.GetCredentials()
		require.NoError(t, err)
		require.NotNil(t, creds)
		assert.True(t, creds.IsValid())
	})
}

func TestAzureProvider_SetterMethods(t *testing.T) {
	t.Run("SetSubscriptionsClient", func(t *testing.T) {
		p := &AzureProvider{}
		mockClient := &mockSubscriptionsClient{}
		p.SetSubscriptionsClient(mockClient)
		assert.NotNil(t, p.subscriptionsClient)
	})

	t.Run("SetCredentialProvider", func(t *testing.T) {
		p := &AzureProvider{}
		mockProvider := &mockCredentialProvider{}
		p.SetCredentialProvider(mockProvider)
		assert.NotNil(t, p.credProvider)
	})

	t.Run("SetCredential", func(t *testing.T) {
		p := &AzureProvider{}
		mockCred := &mockTokenCredential{}
		p.SetCredential(mockCred)
		assert.NotNil(t, p.cred)
	})
}

func TestAzureProvider_GetServiceClient_WithSubscriptionLookup(t *testing.T) {
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

		p := &AzureProvider{
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

		p := &AzureProvider{
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

		p := &AzureProvider{
			cred:           &mockTokenCredential{},
			subscriptionID: "",
		}
		p.SetSubscriptionsClient(mockClient)

		_, err := p.GetServiceClient(context.Background(), common.ServiceCompute, "eastus")
		assert.Error(t, err)
	})
}

func TestAzureProvider_GetRecommendationsClient_WithSubscriptionLookup(t *testing.T) {
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

		p := &AzureProvider{
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

		p := &AzureProvider{
			cred:           &mockTokenCredential{},
			subscriptionID: "",
		}
		p.SetSubscriptionsClient(mockClient)

		_, err := p.GetRecommendationsClient(context.Background())
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no Azure subscriptions found")
	})
}
