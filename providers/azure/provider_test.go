package azure

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
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

// TestNewAzureProvider_TokenCredentialInjection verifies that a pre-resolved
// azcore.TokenCredential supplied via config.AzureTokenCredential is installed
// on the provider so subsequent client builds skip the DefaultAzureCredential
// lazy initialisation path.
func TestNewAzureProvider_TokenCredentialInjection(t *testing.T) {
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
		// (the project has no log-capture harness); the behavioural assertion
		// is unchanged: p.cred stays nil and NewAzureProvider doesn't error.
		p, err := NewAzureProvider(&provider.ProviderConfig{
			AzureSubscriptionID:  "sub-1",
			AzureTokenCredential: "not-a-credential",
		})
		require.NoError(t, err)
		assert.Nil(t, p.cred)
	})
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
	assert.Contains(t, services, common.ServiceMemoryDB)
	assert.Contains(t, services, common.ServiceSavingsPlansAll)
	assert.Contains(t, services, common.ServiceSearch)
	assert.Contains(t, services, common.ServiceDataWarehouse)
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
	// Test GetCredentials when Azure is not configured. Inject a mock
	// credential provider that fails so IsConfigured() deterministically
	// returns false, instead of falling through to the real
	// DefaultAzureCredential lookup.
	p := &AzureProvider{}
	p.SetCredentialProvider(&mockCredentialProvider{
		cred: nil,
		err:  errors.New("no credentials"),
	})
	require.False(t, p.IsConfigured())

	_, err := p.GetCredentials()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "azure provider is not configured")
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
		assert.Contains(t, err.Error(), "azure credentials validation failed")
	})
}

func TestAzureProvider_GetServiceClient_NotConfigured(t *testing.T) {
	// Test GetServiceClient when Azure is not configured. Inject a mock
	// credential provider that fails so IsConfigured() deterministically
	// returns false, instead of falling through to the real
	// DefaultAzureCredential lookup.
	p := &AzureProvider{}
	p.SetCredentialProvider(&mockCredentialProvider{
		cred: nil,
		err:  errors.New("no credentials"),
	})
	require.False(t, p.IsConfigured())

	_, err := p.GetServiceClient(context.Background(), common.ServiceCompute, "eastus")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "azure provider is not configured")
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
		{common.ServiceNoSQL},
		{common.ServiceCache},
		{common.ServiceMemoryDB},
		{common.ServiceSavingsPlansAll},
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

func TestAzureProvider_GetRecommendationsClient_NotConfigured(t *testing.T) {
	// Test GetRecommendationsClient when Azure is not configured. Inject a
	// mock credential provider that fails so IsConfigured() deterministically
	// returns false, instead of falling through to the real
	// DefaultAzureCredential lookup.
	p := &AzureProvider{}
	p.SetCredentialProvider(&mockCredentialProvider{
		cred: nil,
		err:  errors.New("no credentials"),
	})
	require.False(t, p.IsConfigured())

	_, err := p.GetRecommendationsClient(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "azure provider is not configured")
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
		assert.Contains(t, err.Error(), "azure provider is not configured")
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

func TestAzureProvider_GetAccounts_IsDefault(t *testing.T) {
	t.Run("single subscription is marked IsDefault", func(t *testing.T) {
		t.Setenv("AZURE_SUBSCRIPTION_ID", "")
		subID := "only-sub"
		subName := "Only Sub"

		p := &AzureProvider{cred: &mockTokenCredential{}}
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
		p := &AzureProvider{
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
		p := &AzureProvider{cred: &mockTokenCredential{}}
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
		p := &AzureProvider{cred: &mockTokenCredential{}}
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

func TestAzureProvider_GetServiceClientForAccount(t *testing.T) {
	t.Run("returns client for explicit subscription", func(t *testing.T) {
		p := &AzureProvider{cred: &mockTokenCredential{}}

		services := []common.ServiceType{
			common.ServiceCompute,
			common.ServiceRelationalDB,
			common.ServiceNoSQL,
			common.ServiceCache,
			common.ServiceMemoryDB,
			common.ServiceSavingsPlansAll,
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
		p := &AzureProvider{cred: &mockTokenCredential{}}
		_, err := p.GetServiceClientForAccount(context.Background(), common.ServiceCompute, "eastus", "")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "subscriptionID must not be empty")
	})

	t.Run("returns error for unsupported service", func(t *testing.T) {
		p := &AzureProvider{cred: &mockTokenCredential{}}
		_, err := p.GetServiceClientForAccount(context.Background(), common.ServiceType("unknown"), "eastus", "sub-1")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported service")
	})

	t.Run("returns error when not configured", func(t *testing.T) {
		p := &AzureProvider{}
		p.SetCredentialProvider(&mockCredentialProvider{err: errors.New("no cred")})
		_, err := p.GetServiceClientForAccount(context.Background(), common.ServiceCompute, "eastus", "sub-1")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "azure provider is not configured")
	})
}

func TestAzureProvider_GetRecommendationsClientForAccount(t *testing.T) {
	t.Run("returns client for explicit subscription", func(t *testing.T) {
		p := &AzureProvider{cred: &mockTokenCredential{}}
		client, err := p.GetRecommendationsClientForAccount(context.Background(), "explicit-sub")
		require.NoError(t, err)
		require.NotNil(t, client)
	})

	t.Run("returns error for empty subscriptionID", func(t *testing.T) {
		p := &AzureProvider{cred: &mockTokenCredential{}}
		_, err := p.GetRecommendationsClientForAccount(context.Background(), "")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "subscriptionID must not be empty")
	})

	t.Run("returns error when not configured", func(t *testing.T) {
		p := &AzureProvider{}
		p.SetCredentialProvider(&mockCredentialProvider{err: errors.New("no cred")})
		_, err := p.GetRecommendationsClientForAccount(context.Background(), "sub-1")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "azure provider is not configured")
	})
}

// countingSubscriptionsClient wraps mockSubscriptionsClient and counts how
// many times NewListPager is invoked, so cache-hit tests can assert the
// underlying ARM API is only called once.
type countingSubscriptionsClient struct {
	*mockSubscriptionsClient
	calls atomic.Int64
}

func (c *countingSubscriptionsClient) NewListPager(options *armsubscriptions.ClientListOptions) SubscriptionsPager {
	c.calls.Add(1)
	return c.mockSubscriptionsClient.NewListPager(options)
}

// twoSubscriptionPages returns a mockSubscriptionsClient listing the same
// two fixed subscriptions ("sub-1"/"sub-2") every test in this file needs;
// none of the cache/fan-out tests care about the actual subscription
// identifiers, so a fixed pair keeps call sites short.
func twoSubscriptionPages() *mockSubscriptionsClient {
	sub1ID, sub1Name := "sub-1", "Subscription 1"
	sub2ID, sub2Name := "sub-2", "Subscription 2"
	return &mockSubscriptionsClient{
		listPagerFunc: func(options *armsubscriptions.ClientListOptions) SubscriptionsPager {
			return &mockSubscriptionsPager{
				pages: []armsubscriptions.ClientListResponse{
					{
						SubscriptionListResult: armsubscriptions.SubscriptionListResult{
							Value: []*armsubscriptions.Subscription{
								{SubscriptionID: &sub1ID, DisplayName: &sub1Name},
								{SubscriptionID: &sub2ID, DisplayName: &sub2Name},
							},
						},
					},
				},
			}
		},
	}
}

// clearAzureSubscriptionEnv neutralizes AZURE_SUBSCRIPTION_ID for the calling
// test. resolveDefaultSubscription reads it via os.Getenv, so a developer or
// CI runner with it exported would otherwise flip IsDefault on one of the
// fixture subscriptions and change what the cache/fan-out tests observe --
// failing them for a reason unrelated to the behavior they guard.
func clearAzureSubscriptionEnv(t *testing.T) {
	t.Helper()
	t.Setenv("AZURE_SUBSCRIPTION_ID", "")
}

func TestAzureProvider_GetAccounts_CacheHit(t *testing.T) {
	clearAzureSubscriptionEnv(t)
	counting := &countingSubscriptionsClient{mockSubscriptionsClient: twoSubscriptionPages()}

	p := &AzureProvider{cred: &mockTokenCredential{}}
	p.SetSubscriptionsClient(counting)

	first, err := p.GetAccounts(context.Background())
	require.NoError(t, err)
	require.Len(t, first, 2)
	assert.Equal(t, int64(1), counting.calls.Load(), "first GetAccounts call should hit the API once")

	second, err := p.GetAccounts(context.Background())
	require.NoError(t, err)
	require.Len(t, second, 2)
	assert.Equal(t, int64(1), counting.calls.Load(), "second GetAccounts call should be served from cache, not the API")
	assert.Equal(t, first, second)
}

func TestAzureProvider_GetAccounts_CacheHit_ReturnsIndependentCopies(t *testing.T) {
	clearAzureSubscriptionEnv(t)
	p := &AzureProvider{cred: &mockTokenCredential{}}
	p.SetSubscriptionsClient(twoSubscriptionPages())

	first, err := p.GetAccounts(context.Background())
	require.NoError(t, err)
	first[0].IsDefault = true // mutate the caller's copy

	second, err := p.GetAccounts(context.Background())
	require.NoError(t, err)
	assert.False(t, second[0].IsDefault, "mutating a returned slice must not corrupt the cache")
}

func TestAzureProvider_InvalidateAccountsCache(t *testing.T) {
	clearAzureSubscriptionEnv(t)
	counting := &countingSubscriptionsClient{mockSubscriptionsClient: twoSubscriptionPages()}

	p := &AzureProvider{cred: &mockTokenCredential{}}
	p.SetSubscriptionsClient(counting)

	_, err := p.GetAccounts(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(1), counting.calls.Load())

	p.InvalidateAccountsCache()

	_, err = p.GetAccounts(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(2), counting.calls.Load(), "GetAccounts after InvalidateAccountsCache should re-hit the API")
}

// gatedCountingSubscriptionsClient counts ARM list calls and holds the first
// one open until released, so a test can guarantee the single in-flight fetch
// is genuinely in progress while additional cold-cache callers pile up behind
// it. This is what makes the single-flight assertion deterministic for both a
// correct impl (exactly one call) and a naive per-caller fetch (several calls).
type gatedCountingSubscriptionsClient struct {
	inner         *mockSubscriptionsClient
	calls         atomic.Int64
	firstOnce     sync.Once
	firstInFlight chan struct{} // closed when the first fetch has entered
	release       chan struct{} // closed by the test to let held fetches proceed
}

func (c *gatedCountingSubscriptionsClient) NewListPager(options *armsubscriptions.ClientListOptions) SubscriptionsPager {
	c.calls.Add(1)
	c.firstOnce.Do(func() { close(c.firstInFlight) })
	<-c.release
	return c.inner.NewListPager(options)
}

func (c *gatedCountingSubscriptionsClient) NewListLocationsPager(subscriptionID string, options *armsubscriptions.ClientListLocationsOptions) LocationsPager {
	return c.inner.NewListLocationsPager(subscriptionID, options)
}

// TestAzureProvider_GetAccounts_ConcurrentColdCache_SingleARMCall guards the
// single-flight cold-cache contract: many goroutines hitting an empty cache at
// once must collapse into exactly one ARM subscriptions.List call, not one per
// caller.
//
// The staged launch makes this deterministic: the leader's fetch is held
// in-flight (blocked on release) before the followers start, so the followers
// hit a cold cache while the single fetch is open. A correct impl collapses
// them via single-flight (and the closure's cache re-check), yielding exactly
// one call regardless of scheduling; a naive "fetch outside the lock without
// single-flight" fix lets the followers each issue their own ARM call, which
// this test detects via the atomic counter. Run under -race to also catch any
// unguarded shared-state access on the cold-cache path.
func TestAzureProvider_GetAccounts_ConcurrentColdCache_SingleARMCall(t *testing.T) {
	clearAzureSubscriptionEnv(t)
	gated := &gatedCountingSubscriptionsClient{
		inner:         twoSubscriptionPages(),
		firstInFlight: make(chan struct{}),
		release:       make(chan struct{}),
	}

	p := &AzureProvider{cred: &mockTokenCredential{}}
	p.SetSubscriptionsClient(gated)

	const n = 10
	var wg sync.WaitGroup
	// followersReady counts down once per follower that has entered
	// GetAccounts. Waiting on it before releasing the held fetch is what makes
	// the assertion deterministic: with only a runtime.Gosched() nudge, a
	// follower that had not yet reached the cold-cache path when the leader
	// finished would find a warm cache and never issue its own ARM call --
	// so a genuinely broken (no single-flight) implementation could still
	// report exactly one call and pass.
	var followersReady sync.WaitGroup
	errs := make(chan error, n)
	results := make(chan []common.Account, n)
	worker := func(signalReady bool) {
		defer wg.Done()
		if signalReady {
			followersReady.Done()
		}
		accts, err := p.GetAccounts(context.Background())
		errs <- err
		results <- accts
	}

	// Launch the leader and wait until its ARM fetch is actually in-flight
	// before launching the followers, so they observe a cold cache.
	wg.Add(1)
	go worker(false)
	<-gated.firstInFlight

	followersReady.Add(n - 1)
	for i := 1; i < n; i++ {
		wg.Add(1)
		go worker(true)
	}
	// Wait until every follower goroutine is running and about to call
	// GetAccounts, then give the scheduler a chance to drive them into the
	// single-flight path before releasing the held fetch.
	followersReady.Wait()
	for i := 0; i < n; i++ {
		runtime.Gosched()
	}
	close(gated.release)

	wg.Wait()
	close(errs)
	close(results)

	// require.* only from the test goroutine, never the workers.
	for err := range errs {
		require.NoError(t, err)
	}
	for accts := range results {
		require.Len(t, accts, 2)
	}
	assert.Equal(t, int64(1), gated.calls.Load(),
		"concurrent cold-cache GetAccounts must issue exactly one ARM list call (single-flight)")
}

// TestAzureProvider_ConcurrentCredentialSwapAndFetch_NoDataRace guards the
// publication of the two swappable fields.
//
// SetCredential / SetSubscriptionsClient used to write p.cred and
// p.subscriptionsClient as plain assignments and only THEN take accountsMu to
// invalidate, while fetchAccounts read both from a goroutine running on behalf
// of every caller that joined the single-flight. That is an unsynchronized
// read/write pair, and the window it opens can hand the fetch a new credential
// paired with the old subscriptions client.
//
// The hammer below is deliberately free of happens-before edges between the
// swapping goroutines and the fetching ones -- an ordered handshake (e.g.
// waiting on firstInFlight before swapping) would establish exactly the
// ordering that makes the race invisible to -race. Value assertions are
// intentionally absent: this test's assertion IS the race detector, so it must
// run under `go test -race` to be meaningful.
func TestAzureProvider_ConcurrentCredentialSwapAndFetch_NoDataRace(t *testing.T) {
	clearAzureSubscriptionEnv(t)

	p := &AzureProvider{cred: &mockTokenCredential{}}
	p.SetSubscriptionsClient(twoSubscriptionPages())

	const (
		readers = 8
		rounds  = 25
	)

	stop := make(chan struct{})
	var swappers sync.WaitGroup
	swappers.Add(2)
	go func() {
		defer swappers.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			p.SetCredential(&mockTokenCredential{})
		}
	}()
	go func() {
		defer swappers.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			p.SetSubscriptionsClient(twoSubscriptionPages())
		}
	}()

	var wg sync.WaitGroup
	errs := make(chan error, readers*rounds)
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := 0; r < rounds; r++ {
				// Both readers of the swapped fields: GetAccounts reaches
				// fetchAccounts, GetServiceClientForAccount builds a client
				// straight from the credential.
				if _, err := p.GetAccounts(context.Background()); err != nil {
					errs <- err
					return
				}
				if _, err := p.GetServiceClientForAccount(context.Background(), common.ServiceCompute, "eastus", "sub-1"); err != nil {
					errs <- err
					return
				}
			}
		}()
	}

	wg.Wait()
	close(stop)
	swappers.Wait()
	close(errs)

	// require.* only from the test goroutine, never the workers.
	for err := range errs {
		require.NoError(t, err)
	}
}

// Swapping the credential or the subscriptions client must drop the cached
// subscription list. The cache records what the PREVIOUS credential/client
// could see; serving it afterwards would report subscriptions the new
// principal may have no access to, and GetRecommendationsClient would then
// fan out across them.
func TestAzureProvider_CacheDroppedOnCredentialOrClientSwap(t *testing.T) {
	clearAzureSubscriptionEnv(t)

	t.Run("SetCredential invalidates", func(t *testing.T) {
		counting := &countingSubscriptionsClient{mockSubscriptionsClient: twoSubscriptionPages()}
		p := &AzureProvider{cred: &mockTokenCredential{}}
		p.SetSubscriptionsClient(counting)

		_, err := p.GetAccounts(context.Background())
		require.NoError(t, err)
		require.Equal(t, int64(1), counting.calls.Load())

		p.SetCredential(&mockTokenCredential{})

		_, err = p.GetAccounts(context.Background())
		require.NoError(t, err)
		assert.Equal(t, int64(2), counting.calls.Load(),
			"a credential swap must re-resolve the subscription list, not reuse the old credential's")
	})

	t.Run("SetSubscriptionsClient invalidates", func(t *testing.T) {
		first := &countingSubscriptionsClient{mockSubscriptionsClient: twoSubscriptionPages()}
		p := &AzureProvider{cred: &mockTokenCredential{}}
		p.SetSubscriptionsClient(first)

		_, err := p.GetAccounts(context.Background())
		require.NoError(t, err)

		soloID, soloName := "sub-solo", "Solo Subscription"
		p.SetSubscriptionsClient(&mockSubscriptionsClient{
			listPagerFunc: func(_ *armsubscriptions.ClientListOptions) SubscriptionsPager {
				return &mockSubscriptionsPager{
					pages: []armsubscriptions.ClientListResponse{
						{SubscriptionListResult: armsubscriptions.SubscriptionListResult{
							Value: []*armsubscriptions.Subscription{{SubscriptionID: &soloID, DisplayName: &soloName}},
						}},
					},
				}
			},
		})

		accts, err := p.GetAccounts(context.Background())
		require.NoError(t, err)
		require.Len(t, accts, 1, "the swapped-in client's subscriptions must be returned, not the cached ones")
		assert.Equal(t, soloID, accts[0].ID)
	})
}

// gatedGenerationSubscriptionsClient holds its first ARM list call open (like
// gatedCountingSubscriptionsClient) but serves a DIFFERENT subscription on
// each call, so a test can tell a re-fetched result apart from a resurrected
// pre-invalidation snapshot.
type gatedGenerationSubscriptionsClient struct {
	calls         atomic.Int64
	firstOnce     sync.Once
	firstInFlight chan struct{}
	release       chan struct{}
}

func (c *gatedGenerationSubscriptionsClient) NewListPager(_ *armsubscriptions.ClientListOptions) SubscriptionsPager {
	n := c.calls.Add(1)
	c.firstOnce.Do(func() {
		close(c.firstInFlight)
		<-c.release
	})
	id := fmt.Sprintf("sub-gen-%d", n)
	name := fmt.Sprintf("Subscription generation %d", n)
	return &mockSubscriptionsPager{
		pages: []armsubscriptions.ClientListResponse{
			{SubscriptionListResult: armsubscriptions.SubscriptionListResult{
				Value: []*armsubscriptions.Subscription{{SubscriptionID: &id, DisplayName: &name}},
			}},
		},
	}
}

func (c *gatedGenerationSubscriptionsClient) NewListLocationsPager(_ string, _ *armsubscriptions.ClientListLocationsOptions) LocationsPager {
	return nil
}

// TestAzureProvider_InvalidateAccountsCache_DuringInFlightFetch guards the
// cache-generation check.
//
// Interleaving: a fetch is in flight (holding the pre-invalidation snapshot)
// when InvalidateAccountsCache lands. Without the generation gate the
// in-flight fetch publishes its now-stale snapshot after the invalidation, so
// the next read is served from cache and the caller is handed back exactly
// the data it asked to discard -- silently, with no second ARM call.
func TestAzureProvider_InvalidateAccountsCache_DuringInFlightFetch(t *testing.T) {
	clearAzureSubscriptionEnv(t)
	gated := &gatedGenerationSubscriptionsClient{
		firstInFlight: make(chan struct{}),
		release:       make(chan struct{}),
	}

	p := &AzureProvider{cred: &mockTokenCredential{}}
	p.SetSubscriptionsClient(gated)

	type fetchResult struct {
		accounts []common.Account
		err      error
	}
	done := make(chan fetchResult, 1)
	go func() {
		accts, err := p.GetAccounts(context.Background())
		done <- fetchResult{accounts: accts, err: err}
	}()

	// The first fetch is now blocked inside the ARM call, holding generation-1
	// data. Invalidate while it is still in flight.
	<-gated.firstInFlight
	p.InvalidateAccountsCache()
	close(gated.release)

	first := <-done
	require.NoError(t, first.err)
	require.Len(t, first.accounts, 1)
	assert.Equal(t, "sub-gen-1", first.accounts[0].ID)

	// The invalidated snapshot must not have been published: this read has to
	// re-hit ARM and observe the current subscription list.
	second, err := p.GetAccounts(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(2), gated.calls.Load(),
		"a fetch that started before InvalidateAccountsCache must not populate the cache")
	require.Len(t, second, 1)
	assert.Equal(t, "sub-gen-2", second[0].ID,
		"read after invalidation must see fresh data, not the resurrected pre-invalidation snapshot")
}

func TestAzureProvider_GetRecommendationsClient_MultiSubscriptionFanOut(t *testing.T) {
	clearAzureSubscriptionEnv(t)

	// Regression guard for the scoping rule: fan-out must be the fallback for
	// an ambiguous principal, never an upgrade applied to a caller that
	// already named its subscription. A principal that can see sub-1 and
	// sub-2 but pinned sub-2 via AZURE_SUBSCRIPTION_ID must keep getting
	// sub-2 only; returning the fan-out client here would hand that caller
	// sub-1's recommendations too.
	t.Run("AZURE_SUBSCRIPTION_ID still scopes to one subscription", func(t *testing.T) {
		t.Setenv("AZURE_SUBSCRIPTION_ID", "sub-2")
		p := &AzureProvider{cred: &mockTokenCredential{}}
		p.SetSubscriptionsClient(twoSubscriptionPages())

		client, err := p.GetRecommendationsClient(context.Background())
		require.NoError(t, err)
		require.IsType(t, &RecommendationsClientAdapter{}, client,
			"an env-pinned subscription must not be widened to an org-wide fan-out")
		assert.Equal(t, "sub-2", client.(*RecommendationsClientAdapter).subscriptionID)
	})

	// A configured AZURE_SUBSCRIPTION_ID that names a subscription this
	// principal cannot see is a misconfiguration. Answering it with an
	// org-wide fan-out would hand the caller every OTHER subscription's data
	// in response to a request that named one, so this must stay the hard
	// error it was before fan-out existed.
	t.Run("AZURE_SUBSCRIPTION_ID naming an invisible subscription errors", func(t *testing.T) {
		t.Setenv("AZURE_SUBSCRIPTION_ID", "sub-not-visible")
		p := &AzureProvider{cred: &mockTokenCredential{}}
		p.SetSubscriptionsClient(twoSubscriptionPages())

		client, err := p.GetRecommendationsClient(context.Background())
		require.Error(t, err, "an unresolvable explicit subscription must not silently widen to org-wide fan-out")
		assert.Nil(t, client)
		assert.Contains(t, err.Error(), "sub-not-visible")
		assert.Contains(t, err.Error(), "not among the 2 subscriptions visible")
	})

	// The single-visible-subscription case is the dangerous one, and the
	// reason the target has to be validated BEFORE getDefaultSubscriptionID:
	// resolveDefaultSubscription's rule 3 marks a lone subscription as the
	// default even when a target was configured and did not match it. Reading
	// that default first would resolve an invisible target to whichever one
	// subscription happens to be visible and collect against it silently.
	t.Run("AZURE_SUBSCRIPTION_ID invisible with one visible subscription errors", func(t *testing.T) {
		t.Setenv("AZURE_SUBSCRIPTION_ID", "sub-not-visible")
		soloID, soloName := "sub-solo", "Solo Subscription"
		p := &AzureProvider{cred: &mockTokenCredential{}}
		p.SetSubscriptionsClient(&mockSubscriptionsClient{
			listPagerFunc: func(_ *armsubscriptions.ClientListOptions) SubscriptionsPager {
				return &mockSubscriptionsPager{
					pages: []armsubscriptions.ClientListResponse{
						{SubscriptionListResult: armsubscriptions.SubscriptionListResult{
							Value: []*armsubscriptions.Subscription{{SubscriptionID: &soloID, DisplayName: &soloName}},
						}},
					},
				}
			},
		})

		client, err := p.GetRecommendationsClient(context.Background())
		require.Error(t, err,
			"an invisible configured subscription must not silently resolve to the one visible subscription")
		assert.Nil(t, client)
		assert.Contains(t, err.Error(), "sub-not-visible")
		assert.Contains(t, err.Error(), "not among the 1 subscriptions visible")
	})

	// The happy path for the same branch: a target the principal CAN see is
	// honoured, and scopes the client to exactly that subscription.
	t.Run("AZURE_SUBSCRIPTION_ID matching a visible subscription is honoured", func(t *testing.T) {
		t.Setenv("AZURE_SUBSCRIPTION_ID", "sub-1")
		p := &AzureProvider{cred: &mockTokenCredential{}}
		p.SetSubscriptionsClient(twoSubscriptionPages())

		client, err := p.GetRecommendationsClient(context.Background())
		require.NoError(t, err)
		require.IsType(t, &RecommendationsClientAdapter{}, client)
		assert.Equal(t, "sub-1", client.(*RecommendationsClientAdapter).subscriptionID)
	})

	t.Run("multi-subscription returns MultiSubscriptionRecommendationsClient", func(t *testing.T) {
		p := &AzureProvider{cred: &mockTokenCredential{}}
		p.SetSubscriptionsClient(twoSubscriptionPages())

		client, err := p.GetRecommendationsClient(context.Background())
		require.NoError(t, err)
		require.IsType(t, &MultiSubscriptionRecommendationsClient{}, client)
		assert.Len(t, client.(*MultiSubscriptionRecommendationsClient).subscriptions, 2)
	})

	t.Run("single discovered subscription returns RecommendationsClientAdapter", func(t *testing.T) {
		subID, subName := "sub-solo", "Solo Subscription"
		p := &AzureProvider{cred: &mockTokenCredential{}}
		p.SetSubscriptionsClient(&mockSubscriptionsClient{
			listPagerFunc: func(options *armsubscriptions.ClientListOptions) SubscriptionsPager {
				return &mockSubscriptionsPager{
					pages: []armsubscriptions.ClientListResponse{
						{SubscriptionListResult: armsubscriptions.SubscriptionListResult{
							Value: []*armsubscriptions.Subscription{{SubscriptionID: &subID, DisplayName: &subName}},
						}},
					},
				}
			},
		})

		client, err := p.GetRecommendationsClient(context.Background())
		require.NoError(t, err)
		require.IsType(t, &RecommendationsClientAdapter{}, client)
		assert.Equal(t, subID, client.(*RecommendationsClientAdapter).subscriptionID)
	})

	t.Run("pinned subscription always returns single adapter regardless of discovered count", func(t *testing.T) {
		p := &AzureProvider{cred: &mockTokenCredential{}, subscriptionID: "pinned-sub"}
		// Deliberately do not set a subscriptions client: a pinned subscription
		// must never trigger subscription discovery.
		client, err := p.GetRecommendationsClient(context.Background())
		require.NoError(t, err)
		require.IsType(t, &RecommendationsClientAdapter{}, client)
		assert.Equal(t, "pinned-sub", client.(*RecommendationsClientAdapter).subscriptionID)
	})

	// The zero-subscription "no Azure subscriptions found" case is already
	// covered by TestAzureProvider_GetRecommendationsClient_WithSubscriptionLookup.

	t.Run("subscription discovery failure is propagated", func(t *testing.T) {
		p := &AzureProvider{cred: &mockTokenCredential{}}
		p.SetSubscriptionsClient(&mockSubscriptionsClient{
			listPagerFunc: func(options *armsubscriptions.ClientListOptions) SubscriptionsPager {
				return &mockSubscriptionsPager{nextErr: errors.New("boom")}
			},
		})

		_, err := p.GetRecommendationsClient(context.Background())
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to resolve Azure subscriptions")
	})
}
