package aws

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/organizations"
	orgtypes "github.com/aws/aws-sdk-go-v2/service/organizations/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/provider"
)

// mockConfigLoader implements ConfigLoader for testing
type mockConfigLoader struct {
	cfg aws.Config
	err error
}

func (m *mockConfigLoader) LoadDefaultConfig(ctx context.Context, optFns ...func(*config.LoadOptions) error) (aws.Config, error) {
	return m.cfg, m.err
}

// mockSTSClient implements STSClient for testing
type mockSTSClient struct {
	getCallerIdentityFunc func(ctx context.Context, params *sts.GetCallerIdentityInput, optFns ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error)
}

func (m *mockSTSClient) GetCallerIdentity(ctx context.Context, params *sts.GetCallerIdentityInput, optFns ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
	if m.getCallerIdentityFunc != nil {
		return m.getCallerIdentityFunc(ctx, params, optFns...)
	}
	return nil, errors.New("not implemented")
}

// mockEC2Client implements EC2Client for testing
type mockEC2Client struct {
	describeRegionsFunc func(ctx context.Context, params *ec2.DescribeRegionsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeRegionsOutput, error)
}

func (m *mockEC2Client) DescribeRegions(ctx context.Context, params *ec2.DescribeRegionsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeRegionsOutput, error) {
	if m.describeRegionsFunc != nil {
		return m.describeRegionsFunc(ctx, params, optFns...)
	}
	return nil, errors.New("not implemented")
}

// mockOrganizationsPaginator implements OrganizationsPaginator for testing
type mockOrganizationsPaginator struct {
	pages      []*organizations.ListAccountsOutput
	pageIdx    int
	nextErr    error
	errOnPage  int // which page to return error on (-1 for never)
}

func (m *mockOrganizationsPaginator) HasMorePages() bool {
	return m.pageIdx < len(m.pages)
}

func (m *mockOrganizationsPaginator) NextPage(ctx context.Context, optFns ...func(*organizations.Options)) (*organizations.ListAccountsOutput, error) {
	if m.errOnPage >= 0 && m.pageIdx == m.errOnPage {
		return nil, m.nextErr
	}
	if m.pageIdx >= len(m.pages) {
		return nil, errors.New("no more pages")
	}
	page := m.pages[m.pageIdx]
	m.pageIdx++
	return page, nil
}

func TestNewAWSProvider(t *testing.T) {
	tests := []struct {
		name            string
		config          *provider.ProviderConfig
		expectedProfile string
		expectedRegion  string
	}{
		{
			name:            "Nil config",
			config:          nil,
			expectedProfile: "",
			expectedRegion:  "",
		},
		{
			name: "With region only",
			config: &provider.ProviderConfig{
				Region: "us-west-2",
			},
			expectedProfile: "",
			expectedRegion:  "us-west-2",
		},
		{
			name: "With profile only",
			config: &provider.ProviderConfig{
				Profile: "my-profile",
			},
			expectedProfile: "my-profile",
			expectedRegion:  "",
		},
		{
			name: "With both profile and region",
			config: &provider.ProviderConfig{
				Profile: "production",
				Region:  "eu-west-1",
			},
			expectedProfile: "production",
			expectedRegion:  "eu-west-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := NewAWSProvider(tt.config)
			require.NoError(t, err)
			require.NotNil(t, p)

			assert.Equal(t, tt.expectedProfile, p.profile)
			assert.Equal(t, tt.expectedRegion, p.region)
		})
	}
}

func TestAWSProvider_Name(t *testing.T) {
	p := &AWSProvider{}
	assert.Equal(t, "aws", p.Name())
}

func TestAWSProvider_DisplayName(t *testing.T) {
	p := &AWSProvider{}
	assert.Equal(t, "Amazon Web Services", p.DisplayName())
}

func TestAWSProvider_GetDefaultRegion(t *testing.T) {
	tests := []struct {
		name           string
		provider       *AWSProvider
		expectedRegion string
	}{
		{
			name:           "No region set - returns default us-east-1",
			provider:       &AWSProvider{},
			expectedRegion: "us-east-1",
		},
		{
			name:           "Provider region set",
			provider:       &AWSProvider{region: "eu-central-1"},
			expectedRegion: "eu-central-1",
		},
		{
			name: "Config region set (no provider region)",
			provider: &AWSProvider{
				cfg: aws.Config{Region: "ap-southeast-1"},
			},
			expectedRegion: "ap-southeast-1",
		},
		{
			name: "Provider region takes precedence over config",
			provider: &AWSProvider{
				region: "us-west-2",
				cfg:    aws.Config{Region: "ap-southeast-1"},
			},
			expectedRegion: "us-west-2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expectedRegion, tt.provider.GetDefaultRegion())
		})
	}
}

func TestAWSProvider_GetSupportedServices(t *testing.T) {
	p := &AWSProvider{}
	services := p.GetSupportedServices()

	require.NotEmpty(t, services)
	assert.Contains(t, services, common.ServiceCompute)
	assert.Contains(t, services, common.ServiceRelationalDB)
	assert.Contains(t, services, common.ServiceCache)
	assert.Contains(t, services, common.ServiceSearch)
	assert.Contains(t, services, common.ServiceDataWarehouse)
	assert.Contains(t, services, common.ServiceSavingsPlans)
	// Legacy types
	assert.Contains(t, services, common.ServiceEC2)
	assert.Contains(t, services, common.ServiceRDS)
	assert.Contains(t, services, common.ServiceElastiCache)
	assert.Contains(t, services, common.ServiceOpenSearch)
	assert.Contains(t, services, common.ServiceRedshift)
	assert.Contains(t, services, common.ServiceMemoryDB)
}

// Tests for service_client.go

func TestNewEC2Client(t *testing.T) {
	cfg := aws.Config{Region: "us-east-1"}
	client := NewEC2Client(cfg)
	require.NotNil(t, client)
	assert.Equal(t, common.ServiceCompute, client.GetServiceType())
	assert.Equal(t, "us-east-1", client.GetRegion())
}

func TestNewRDSClient(t *testing.T) {
	cfg := aws.Config{Region: "us-west-2"}
	client := NewRDSClient(cfg)
	require.NotNil(t, client)
	assert.Equal(t, common.ServiceRelationalDB, client.GetServiceType())
	assert.Equal(t, "us-west-2", client.GetRegion())
}

func TestNewElastiCacheClient(t *testing.T) {
	cfg := aws.Config{Region: "eu-west-1"}
	client := NewElastiCacheClient(cfg)
	require.NotNil(t, client)
	assert.Equal(t, common.ServiceCache, client.GetServiceType())
	assert.Equal(t, "eu-west-1", client.GetRegion())
}

func TestNewOpenSearchClient(t *testing.T) {
	cfg := aws.Config{Region: "ap-northeast-1"}
	client := NewOpenSearchClient(cfg)
	require.NotNil(t, client)
	assert.Equal(t, common.ServiceSearch, client.GetServiceType())
	assert.Equal(t, "ap-northeast-1", client.GetRegion())
}

func TestNewRedshiftClient(t *testing.T) {
	cfg := aws.Config{Region: "us-east-2"}
	client := NewRedshiftClient(cfg)
	require.NotNil(t, client)
	assert.Equal(t, common.ServiceDataWarehouse, client.GetServiceType())
	assert.Equal(t, "us-east-2", client.GetRegion())
}

func TestNewMemoryDBClient(t *testing.T) {
	cfg := aws.Config{Region: "eu-central-1"}
	client := NewMemoryDBClient(cfg)
	require.NotNil(t, client)
	assert.Equal(t, common.ServiceCache, client.GetServiceType())
	assert.Equal(t, "eu-central-1", client.GetRegion())
}

func TestNewSavingsPlansClient(t *testing.T) {
	cfg := aws.Config{Region: "us-east-1"}
	client := NewSavingsPlansClient(cfg)
	require.NotNil(t, client)
	assert.Equal(t, common.ServiceSavingsPlans, client.GetServiceType())
	assert.Equal(t, "us-east-1", client.GetRegion())
}

func TestNewRecommendationsClient(t *testing.T) {
	cfg := aws.Config{Region: "us-east-1"}
	client := NewRecommendationsClient(cfg)
	require.NotNil(t, client)

	// Verify it's the correct type
	adapter, ok := client.(*RecommendationsClientAdapter)
	assert.True(t, ok)
	assert.NotNil(t, adapter.client)
}

func TestRecommendationsClientAdapter_GetRecommendationsForService(t *testing.T) {
	// This test just verifies the adapter is wired correctly
	// Actual API calls would require credentials
	cfg := aws.Config{Region: "us-east-1"}
	client := NewRecommendationsClient(cfg)
	adapter, ok := client.(*RecommendationsClientAdapter)
	require.True(t, ok)
	require.NotNil(t, adapter.client)
}

func TestAWSProvider_IsConfigured(t *testing.T) {
	// Test with various configurations
	// Note: The actual result depends on the environment (AWS credentials may be present)
	p := &AWSProvider{}
	// Just verify it doesn't panic and returns a boolean
	_ = p.IsConfigured()
}

func TestAWSProvider_IsConfigured_WithProfile(t *testing.T) {
	p := &AWSProvider{
		profile: "test-profile",
	}
	// The result depends on whether this profile exists, but shouldn't panic
	_ = p.IsConfigured()
}

func TestAWSProvider_IsConfigured_WithRegion(t *testing.T) {
	p := &AWSProvider{
		region: "us-west-2",
	}
	// Test the region branch in IsConfigured
	_ = p.IsConfigured()
}

func TestAWSProvider_GetServiceClient_UnsupportedService(t *testing.T) {
	// Create a provider with a valid config
	p := &AWSProvider{
		cfg: aws.Config{Region: "us-east-1"},
	}

	// Test unsupported service type
	_, err := p.GetServiceClient(nil, common.ServiceType("unsupported"), "us-east-1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported service")
}

func TestAWSProvider_GetServiceClient_AllServiceTypes(t *testing.T) {
	// Create a provider with a valid config
	p := &AWSProvider{
		cfg: aws.Config{Region: "us-east-1"},
	}

	testCases := []struct {
		service      common.ServiceType
		expectedType common.ServiceType
	}{
		{common.ServiceCompute, common.ServiceCompute},
		{common.ServiceEC2, common.ServiceCompute},
		{common.ServiceRelationalDB, common.ServiceRelationalDB},
		{common.ServiceRDS, common.ServiceRelationalDB},
		{common.ServiceCache, common.ServiceCache},
		{common.ServiceElastiCache, common.ServiceCache},
		{common.ServiceSearch, common.ServiceSearch},
		{common.ServiceOpenSearch, common.ServiceSearch},
		{common.ServiceDataWarehouse, common.ServiceDataWarehouse},
		{common.ServiceRedshift, common.ServiceDataWarehouse},
		{common.ServiceMemoryDB, common.ServiceCache},
		{common.ServiceSavingsPlans, common.ServiceSavingsPlans},
	}

	for _, tc := range testCases {
		t.Run(string(tc.service), func(t *testing.T) {
			client, err := p.GetServiceClient(nil, tc.service, "us-east-1")
			require.NoError(t, err)
			require.NotNil(t, client)
			assert.Equal(t, tc.expectedType, client.GetServiceType())
		})
	}
}

func TestAWSProvider_GetRecommendationsClient(t *testing.T) {
	// Create a provider with a valid config
	p := &AWSProvider{
		cfg: aws.Config{Region: "us-east-1"},
	}

	client, err := p.GetRecommendationsClient(nil)
	require.NoError(t, err)
	require.NotNil(t, client)
}

func TestAWSProvider_IsConfigured_WithMock(t *testing.T) {
	t.Run("returns true when config loads successfully", func(t *testing.T) {
		p := &AWSProvider{}
		p.SetConfigLoader(&mockConfigLoader{
			cfg: aws.Config{Region: "us-east-1"},
			err: nil,
		})
		assert.True(t, p.IsConfigured())
		assert.Equal(t, "us-east-1", p.cfg.Region)
	})

	t.Run("returns false when config load fails", func(t *testing.T) {
		p := &AWSProvider{}
		p.SetConfigLoader(&mockConfigLoader{
			err: errors.New("failed to load config"),
		})
		assert.False(t, p.IsConfigured())
	})
}

func TestAWSProvider_ValidateCredentials_WithMock(t *testing.T) {
	t.Run("success with mock STS client", func(t *testing.T) {
		p := &AWSProvider{}
		p.SetConfigLoader(&mockConfigLoader{
			cfg: aws.Config{Region: "us-east-1"},
		})
		p.SetSTSClient(&mockSTSClient{
			getCallerIdentityFunc: func(ctx context.Context, params *sts.GetCallerIdentityInput, optFns ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
				return &sts.GetCallerIdentityOutput{
					Account: aws.String("123456789012"),
					Arn:     aws.String("arn:aws:iam::123456789012:user/test"),
					UserId:  aws.String("AIDATEST"),
				}, nil
			},
		})

		err := p.ValidateCredentials(context.Background())
		assert.NoError(t, err)
	})

	t.Run("returns error when not configured", func(t *testing.T) {
		p := &AWSProvider{}
		p.SetConfigLoader(&mockConfigLoader{
			err: errors.New("config error"),
		})

		err := p.ValidateCredentials(context.Background())
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "AWS is not configured")
	})

	t.Run("returns error when STS call fails", func(t *testing.T) {
		p := &AWSProvider{}
		p.SetConfigLoader(&mockConfigLoader{
			cfg: aws.Config{Region: "us-east-1"},
		})
		p.SetSTSClient(&mockSTSClient{
			getCallerIdentityFunc: func(ctx context.Context, params *sts.GetCallerIdentityInput, optFns ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
				return nil, errors.New("access denied")
			},
		})

		err := p.ValidateCredentials(context.Background())
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "AWS credentials validation failed")
	})
}

func TestAWSProvider_GetAccounts_WithMock(t *testing.T) {
	t.Run("returns current account only when no org accounts", func(t *testing.T) {
		p := &AWSProvider{
			cfg: aws.Config{Region: "us-east-1"},
		}
		p.SetSTSClient(&mockSTSClient{
			getCallerIdentityFunc: func(ctx context.Context, params *sts.GetCallerIdentityInput, optFns ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
				return &sts.GetCallerIdentityOutput{
					Account: aws.String("123456789012"),
				}, nil
			},
		})
		p.SetOrganizationsPaginator(&mockOrganizationsPaginator{
			pages: []*organizations.ListAccountsOutput{},
		})

		accounts, err := p.GetAccounts(context.Background())
		require.NoError(t, err)
		require.Len(t, accounts, 1)
		assert.Equal(t, "123456789012", accounts[0].ID)
		assert.True(t, accounts[0].IsDefault)
		assert.Equal(t, common.ProviderAWS, accounts[0].Provider)
	})

	t.Run("returns current account and org accounts", func(t *testing.T) {
		p := &AWSProvider{
			cfg: aws.Config{Region: "us-east-1"},
		}
		p.SetSTSClient(&mockSTSClient{
			getCallerIdentityFunc: func(ctx context.Context, params *sts.GetCallerIdentityInput, optFns ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
				return &sts.GetCallerIdentityOutput{
					Account: aws.String("111111111111"),
				}, nil
			},
		})
		p.SetOrganizationsPaginator(&mockOrganizationsPaginator{
			pages: []*organizations.ListAccountsOutput{
				{
					Accounts: []orgtypes.Account{
						{Id: aws.String("111111111111"), Name: aws.String("Current")},
						{Id: aws.String("222222222222"), Name: aws.String("Account 2")},
						{Id: aws.String("333333333333"), Name: aws.String("Account 3")},
					},
				},
			},
			errOnPage: -1,
		})

		accounts, err := p.GetAccounts(context.Background())
		require.NoError(t, err)
		require.Len(t, accounts, 3)
		assert.Equal(t, "111111111111", accounts[0].ID)
		assert.True(t, accounts[0].IsDefault)
		assert.Equal(t, "222222222222", accounts[1].ID)
		assert.False(t, accounts[1].IsDefault)
		assert.Equal(t, "333333333333", accounts[2].ID)
	})

	t.Run("returns current account when org list fails", func(t *testing.T) {
		p := &AWSProvider{
			cfg: aws.Config{Region: "us-east-1"},
		}
		p.SetSTSClient(&mockSTSClient{
			getCallerIdentityFunc: func(ctx context.Context, params *sts.GetCallerIdentityInput, optFns ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
				return &sts.GetCallerIdentityOutput{
					Account: aws.String("123456789012"),
				}, nil
			},
		})
		p.SetOrganizationsPaginator(&mockOrganizationsPaginator{
			pages:     []*organizations.ListAccountsOutput{{}},
			nextErr:   errors.New("access denied"),
			errOnPage: 0,
		})

		accounts, err := p.GetAccounts(context.Background())
		require.NoError(t, err)
		require.Len(t, accounts, 1)
		assert.Equal(t, "123456789012", accounts[0].ID)
	})

	t.Run("returns error when STS fails", func(t *testing.T) {
		p := &AWSProvider{
			cfg: aws.Config{Region: "us-east-1"},
		}
		p.SetSTSClient(&mockSTSClient{
			getCallerIdentityFunc: func(ctx context.Context, params *sts.GetCallerIdentityInput, optFns ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
				return nil, errors.New("STS error")
			},
		})

		_, err := p.GetAccounts(context.Background())
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get current account")
	})
}

func TestAWSProvider_GetRegions_WithMock(t *testing.T) {
	t.Run("success with regions", func(t *testing.T) {
		p := &AWSProvider{
			cfg: aws.Config{Region: "us-east-1"},
		}
		optIn := "opt-in-not-required"
		p.SetEC2Client(&mockEC2Client{
			describeRegionsFunc: func(ctx context.Context, params *ec2.DescribeRegionsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeRegionsOutput, error) {
				return &ec2.DescribeRegionsOutput{
					Regions: []ec2types.Region{
						{RegionName: aws.String("us-east-1"), OptInStatus: &optIn},
						{RegionName: aws.String("us-west-2"), OptInStatus: &optIn},
						{RegionName: aws.String("eu-west-1"), OptInStatus: nil},
					},
				}, nil
			},
		})

		regions, err := p.GetRegions(context.Background())
		require.NoError(t, err)
		require.Len(t, regions, 3)
		assert.Equal(t, "us-east-1", regions[0].ID)
		assert.Equal(t, "us-east-1 (opt-in-not-required)", regions[0].DisplayName)
		assert.Equal(t, common.ProviderAWS, regions[0].Provider)
		assert.Equal(t, "eu-west-1", regions[2].DisplayName) // No opt-in status
	})

	t.Run("skips regions with nil name", func(t *testing.T) {
		p := &AWSProvider{
			cfg: aws.Config{Region: "us-east-1"},
		}
		p.SetEC2Client(&mockEC2Client{
			describeRegionsFunc: func(ctx context.Context, params *ec2.DescribeRegionsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeRegionsOutput, error) {
				return &ec2.DescribeRegionsOutput{
					Regions: []ec2types.Region{
						{RegionName: nil},
						{RegionName: aws.String("us-east-1")},
					},
				}, nil
			},
		})

		regions, err := p.GetRegions(context.Background())
		require.NoError(t, err)
		require.Len(t, regions, 1)
		assert.Equal(t, "us-east-1", regions[0].ID)
	})

	t.Run("returns error on EC2 API failure", func(t *testing.T) {
		p := &AWSProvider{
			cfg: aws.Config{Region: "us-east-1"},
		}
		p.SetEC2Client(&mockEC2Client{
			describeRegionsFunc: func(ctx context.Context, params *ec2.DescribeRegionsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeRegionsOutput, error) {
				return nil, errors.New("EC2 API error")
			},
		})

		_, err := p.GetRegions(context.Background())
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to describe AWS regions")
	})
}

func TestAWSProvider_SetterMethods(t *testing.T) {
	t.Run("SetConfigLoader", func(t *testing.T) {
		p := &AWSProvider{}
		mockLoader := &mockConfigLoader{}
		p.SetConfigLoader(mockLoader)
		assert.NotNil(t, p.configLoader)
	})

	t.Run("SetSTSClient", func(t *testing.T) {
		p := &AWSProvider{}
		mockSTS := &mockSTSClient{}
		p.SetSTSClient(mockSTS)
		assert.NotNil(t, p.stsClient)
	})

	t.Run("SetEC2Client", func(t *testing.T) {
		p := &AWSProvider{}
		mockEC2 := &mockEC2Client{}
		p.SetEC2Client(mockEC2)
		assert.NotNil(t, p.ec2Client)
	})

	t.Run("SetOrganizationsPaginator", func(t *testing.T) {
		p := &AWSProvider{}
		mockPaginator := &mockOrganizationsPaginator{}
		p.SetOrganizationsPaginator(mockPaginator)
		assert.NotNil(t, p.orgPaginator)
	})
}

// mockCredentialsProvider implements aws.CredentialsProvider for testing
type mockCredentialsProvider struct {
	creds aws.Credentials
	err   error
}

func (m *mockCredentialsProvider) Retrieve(ctx context.Context) (aws.Credentials, error) {
	return m.creds, m.err
}

func TestAWSProvider_GetCredentials_WithMock(t *testing.T) {
	t.Run("returns error when not configured", func(t *testing.T) {
		p := &AWSProvider{}
		p.SetConfigLoader(&mockConfigLoader{
			err: errors.New("config error"),
		})
		_, err := p.GetCredentials()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "AWS is not configured")
	})

	t.Run("success with environment credentials", func(t *testing.T) {
		p := &AWSProvider{}
		p.SetConfigLoader(&mockConfigLoader{
			cfg: aws.Config{
				Region: "us-east-1",
				Credentials: &mockCredentialsProvider{
					creds: aws.Credentials{
						AccessKeyID:     "AKIA...",
						SecretAccessKey: "secret",
						Source:          "EnvConfigCredentials",
					},
				},
			},
		})
		// First, configure the provider
		p.IsConfigured()

		creds, err := p.GetCredentials()
		require.NoError(t, err)
		require.NotNil(t, creds)
		assert.True(t, creds.IsValid())
	})

	t.Run("success with shared config credentials", func(t *testing.T) {
		p := &AWSProvider{}
		p.SetConfigLoader(&mockConfigLoader{
			cfg: aws.Config{
				Region: "us-east-1",
				Credentials: &mockCredentialsProvider{
					creds: aws.Credentials{
						AccessKeyID:     "AKIA...",
						SecretAccessKey: "secret",
						Source:          "SharedConfigCredentials",
					},
				},
			},
		})
		p.IsConfigured()

		creds, err := p.GetCredentials()
		require.NoError(t, err)
		require.NotNil(t, creds)
		assert.True(t, creds.IsValid())
	})

	t.Run("success with assume role credentials", func(t *testing.T) {
		p := &AWSProvider{}
		p.SetConfigLoader(&mockConfigLoader{
			cfg: aws.Config{
				Region: "us-east-1",
				Credentials: &mockCredentialsProvider{
					creds: aws.Credentials{
						AccessKeyID:     "ASIA...",
						SecretAccessKey: "secret",
						Source:          "AssumeRoleProvider",
					},
				},
			},
		})
		p.IsConfigured()

		creds, err := p.GetCredentials()
		require.NoError(t, err)
		require.NotNil(t, creds)
		assert.True(t, creds.IsValid())
	})

	t.Run("returns error when credential retrieval fails", func(t *testing.T) {
		p := &AWSProvider{}
		p.SetConfigLoader(&mockConfigLoader{
			cfg: aws.Config{
				Region: "us-east-1",
				Credentials: &mockCredentialsProvider{
					err: errors.New("credential error"),
				},
			},
		})
		p.IsConfigured()

		_, err := p.GetCredentials()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to retrieve AWS credentials")
	})
}

func TestAWSProvider_GetRecommendationsClient_NotConfigured(t *testing.T) {
	p := &AWSProvider{}
	p.SetConfigLoader(&mockConfigLoader{
		err: errors.New("config error"),
	})

	_, err := p.GetRecommendationsClient(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "AWS is not configured")
}

func TestAWSProvider_GetServiceClient_NotConfigured(t *testing.T) {
	p := &AWSProvider{}
	p.SetConfigLoader(&mockConfigLoader{
		err: errors.New("config error"),
	})

	_, err := p.GetServiceClient(context.Background(), common.ServiceCompute, "us-east-1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "AWS is not configured")
}
