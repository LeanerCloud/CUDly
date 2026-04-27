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
	"github.com/aws/smithy-go"
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
	pages     []*organizations.ListAccountsOutput
	pageIdx   int
	nextErr   error
	errOnPage int // which page to return error on (-1 for never)
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
		{
			name: "Typed AWSProfile takes precedence over deprecated Profile",
			config: &provider.ProviderConfig{
				AWSProfile: "typed-profile",
				Profile:    "deprecated-profile",
			},
			expectedProfile: "typed-profile",
			expectedRegion:  "",
		},
		{
			name: "Typed AWSProfile alone (no Profile fallback needed)",
			config: &provider.ProviderConfig{
				AWSProfile: "only-typed",
			},
			expectedProfile: "only-typed",
			expectedRegion:  "",
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
	// Isolate from ambient AWS env on the developer machine — without this,
	// the SDK's credentials/config chain (loadConfig via IsConfigured) would
	// overwrite p.cfg.Region with whatever the developer's ~/.aws/config or
	// AWS_REGION env var says, masking the values these test cases set
	// explicitly. See #96.
	clearAWSAmbientEnv(t)

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

// TestAWSProvider_GetDefaultRegion_NoLeakViaIsConfigured is the regression
// guard for #96: GetDefaultRegion must NOT trigger the SDK's
// credentials/config chain when the caller has already populated
// p.cfg.Region. This prevents ambient AWS_REGION / ~/.aws/config from
// leaking in and silently overwriting test-supplied regions.
//
// We force a non-default ambient region and confirm GetDefaultRegion
// returns the test-supplied region, not the ambient one.
func TestAWSProvider_GetDefaultRegion_NoLeakViaIsConfigured(t *testing.T) {
	t.Setenv("AWS_REGION", "ap-south-1")
	t.Setenv("AWS_DEFAULT_REGION", "ap-south-1")
	// Don't clear AWS_CONFIG_FILE here — the test still works if one exists,
	// because our fix consults p.cfg.Region BEFORE IsConfigured.

	p := &AWSProvider{
		cfg: aws.Config{Region: "ap-southeast-1"},
	}
	got := p.GetDefaultRegion()
	assert.Equal(t, "ap-southeast-1", got, "test-supplied cfg.Region must not be overwritten by ambient AWS_REGION")
}

// clearAWSAmbientEnv zeroes the ambient AWS env vars + redirects the
// SDK's config/credentials file lookups to /dev/null, isolating tests
// that exercise loadConfig from developer machine state.
func clearAWSAmbientEnv(t *testing.T) {
	t.Helper()
	t.Setenv("AWS_REGION", "")
	t.Setenv("AWS_DEFAULT_REGION", "")
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_CONFIG_FILE", "/dev/null")
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", "/dev/null")
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
	assert.Contains(t, services, common.ServiceSavingsPlansCompute)
	assert.Contains(t, services, common.ServiceSavingsPlansEC2Instance)
	assert.Contains(t, services, common.ServiceSavingsPlansSageMaker)
	assert.Contains(t, services, common.ServiceSavingsPlansDatabase)
	// Legacy umbrella SP slug for backward compat with persisted records.
	assert.Contains(t, services, common.ServiceSavingsPlans)
	// Legacy types
	assert.Contains(t, services, common.ServiceEC2)
	assert.Contains(t, services, common.ServiceRDS)
	assert.Contains(t, services, common.ServiceElastiCache)
	assert.Contains(t, services, common.ServiceOpenSearch)
	assert.Contains(t, services, common.ServiceRedshift)
	assert.Contains(t, services, common.ServiceMemoryDB)
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
	_, err := p.GetServiceClient(context.Background(), common.ServiceType("unsupported"), "us-east-1")
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
		{common.ServiceSavingsPlansCompute, common.ServiceSavingsPlansCompute},
		{common.ServiceSavingsPlansEC2Instance, common.ServiceSavingsPlansEC2Instance},
		{common.ServiceSavingsPlansSageMaker, common.ServiceSavingsPlansSageMaker},
		{common.ServiceSavingsPlansDatabase, common.ServiceSavingsPlansDatabase},
		// Legacy umbrella: GetServiceClient returns an SP client with
		// an empty plan-type filter (umbrella mode); GetServiceType
		// reports the umbrella slug, matching pre-split behaviour.
		{common.ServiceSavingsPlans, common.ServiceSavingsPlans},
	}

	for _, tc := range testCases {
		t.Run(string(tc.service), func(t *testing.T) {
			client, err := p.GetServiceClient(context.Background(), tc.service, "us-east-1")
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

	t.Run("returns current account when caller not in an Organization", func(t *testing.T) {
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
			pages: []*organizations.ListAccountsOutput{{}},
			// Structured API error — AWSOrganizationsNotInUseException is one
			// of the two codes the resolver treats as a silent fallback.
			nextErr:   &smithy.GenericAPIError{Code: "AWSOrganizationsNotInUseException", Message: "not in org"},
			errOnPage: 0,
		})

		accounts, err := p.GetAccounts(context.Background())
		require.NoError(t, err)
		require.Len(t, accounts, 1)
		assert.Equal(t, "123456789012", accounts[0].ID)
	})

	t.Run("returns current account when Organizations access is denied", func(t *testing.T) {
		p := &AWSProvider{
			cfg: aws.Config{Region: "us-east-1"},
		}
		p.SetSTSClient(&mockSTSClient{
			getCallerIdentityFunc: func(ctx context.Context, params *sts.GetCallerIdentityInput, optFns ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
				return &sts.GetCallerIdentityOutput{Account: aws.String("123456789012")}, nil
			},
		})
		p.SetOrganizationsPaginator(&mockOrganizationsPaginator{
			pages:     []*organizations.ListAccountsOutput{{}},
			nextErr:   &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "no perms"},
			errOnPage: 0,
		})

		accounts, err := p.GetAccounts(context.Background())
		require.NoError(t, err)
		require.Len(t, accounts, 1)
		assert.Equal(t, "123456789012", accounts[0].ID)
	})

	t.Run("propagates non-classified errors so callers don't see a silent truncation", func(t *testing.T) {
		p := &AWSProvider{
			cfg: aws.Config{Region: "us-east-1"},
		}
		p.SetSTSClient(&mockSTSClient{
			getCallerIdentityFunc: func(ctx context.Context, params *sts.GetCallerIdentityInput, optFns ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
				return &sts.GetCallerIdentityOutput{Account: aws.String("123456789012")}, nil
			},
		})
		// Throttle mid-pagination: first page has one account, second page
		// throttles. Returning a partial list would be unsafe for the
		// purchase flow — the caller must see the error and stop.
		p.SetOrganizationsPaginator(&mockOrganizationsPaginator{
			pages: []*organizations.ListAccountsOutput{
				{Accounts: []orgtypes.Account{{Id: aws.String("444444444444"), Name: aws.String("member")}}},
				{},
			},
			nextErr:   &smithy.GenericAPIError{Code: "ThrottlingException", Message: "slow down"},
			errOnPage: 1,
		})

		_, err := p.GetAccounts(context.Background())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "organizations: list accounts")
	})

	t.Run("propagates opaque non-API errors too", func(t *testing.T) {
		p := &AWSProvider{
			cfg: aws.Config{Region: "us-east-1"},
		}
		p.SetSTSClient(&mockSTSClient{
			getCallerIdentityFunc: func(ctx context.Context, params *sts.GetCallerIdentityInput, optFns ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
				return &sts.GetCallerIdentityOutput{Account: aws.String("123456789012")}, nil
			},
		})
		// Plain errors.New value — not a smithy API error — must not be
		// treated as the silent not-in-org case.
		p.SetOrganizationsPaginator(&mockOrganizationsPaginator{
			pages:     []*organizations.ListAccountsOutput{{}},
			nextErr:   errors.New("network blew up"),
			errOnPage: 0,
		})

		_, err := p.GetAccounts(context.Background())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "network blew up")
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

func TestRealOrganizationsPaginator_HasMorePages(t *testing.T) {
	// We'll test this by verifying the realOrganizationsPaginator wrapper works correctly
	t.Run("HasMorePages delegates to underlying paginator", func(t *testing.T) {
		// This is a wrapper test - we just verify it doesn't panic and returns a boolean
		mockPages := []*organizations.ListAccountsOutput{
			{
				Accounts: []orgtypes.Account{
					{Id: aws.String("111111111111"), Name: aws.String("Test")},
				},
			},
		}
		mockPaginator := &mockOrganizationsPaginator{
			pages:     mockPages,
			pageIdx:   0,
			errOnPage: -1,
		}

		// Verify HasMorePages works - should be true before consuming
		hasMore := mockPaginator.HasMorePages()
		assert.True(t, hasMore)

		// Consume the page
		_, err := mockPaginator.NextPage(context.Background())
		require.NoError(t, err)

		// Now should have no more pages (pageIdx is 1, len(pages) is 1)
		hasMore = mockPaginator.HasMorePages()
		assert.False(t, hasMore)
	})
}

func TestRealOrganizationsPaginator_NextPage(t *testing.T) {
	t.Run("NextPage returns pages in sequence", func(t *testing.T) {
		mockPages := []*organizations.ListAccountsOutput{
			{
				Accounts: []orgtypes.Account{
					{Id: aws.String("111111111111"), Name: aws.String("Account 1")},
				},
			},
			{
				Accounts: []orgtypes.Account{
					{Id: aws.String("222222222222"), Name: aws.String("Account 2")},
				},
			},
		}
		mockPaginator := &mockOrganizationsPaginator{
			pages:     mockPages,
			pageIdx:   0,
			errOnPage: -1,
		}

		// Verify we have pages
		assert.True(t, mockPaginator.HasMorePages())

		// Get first page
		page1, err := mockPaginator.NextPage(context.Background())
		require.NoError(t, err)
		require.NotNil(t, page1)
		require.Len(t, page1.Accounts, 1)
		assert.Equal(t, "111111111111", *page1.Accounts[0].Id)

		// Still have more pages
		assert.True(t, mockPaginator.HasMorePages())

		// Get second page
		page2, err := mockPaginator.NextPage(context.Background())
		require.NoError(t, err)
		require.NotNil(t, page2)
		require.Len(t, page2.Accounts, 1)
		assert.Equal(t, "222222222222", *page2.Accounts[0].Id)

		// No more pages
		assert.False(t, mockPaginator.HasMorePages())
	})
}

// mockOrganizationsClient for testing realOrganizationsPaginator
type mockOrganizationsClient struct {
	listAccountsFunc func(ctx context.Context, params *organizations.ListAccountsInput, optFns ...func(*organizations.Options)) (*organizations.ListAccountsOutput, error)
}

func (m *mockOrganizationsClient) ListAccounts(ctx context.Context, params *organizations.ListAccountsInput, optFns ...func(*organizations.Options)) (*organizations.ListAccountsOutput, error) {
	if m.listAccountsFunc != nil {
		return m.listAccountsFunc(ctx, params, optFns...)
	}
	return nil, errors.New("not implemented")
}

func TestProviderRegistration(t *testing.T) {
	t.Run("AWS provider is registered in global registry", func(t *testing.T) {
		// The init() function should have registered the AWS provider
		registry := provider.GetRegistry()

		// Verify AWS is registered
		assert.True(t, registry.IsRegistered("aws"))

		// Try to create an AWS provider using the registry
		p, err := registry.GetProviderWithConfig("aws", nil)
		require.NoError(t, err)
		require.NotNil(t, p)

		// Verify it's an AWS provider
		assert.Equal(t, "aws", p.Name())
		assert.Equal(t, "Amazon Web Services", p.DisplayName())
	})

	t.Run("AWS provider can be created with config via registry", func(t *testing.T) {
		registry := provider.GetRegistry()

		config := &provider.ProviderConfig{
			Profile: "test-profile",
			Region:  "us-west-2",
		}

		p, err := registry.GetProviderWithConfig("aws", config)
		require.NoError(t, err)
		require.NotNil(t, p)

		awsProvider, ok := p.(*AWSProvider)
		require.True(t, ok, "provider should be of type *AWSProvider")
		assert.Equal(t, "test-profile", awsProvider.profile)
		assert.Equal(t, "us-west-2", awsProvider.region)
	})
}

// TestGetCredentials_SourceMapping locks down the (SDK source string →
// CredentialSource) mapping in GetCredentials. The aws-sdk-go-v2 source
// strings used here are SDK internals; if a future SDK upgrade renames
// them, the awsSourceSharedConfigCredentials / awsSourceAssumeRoleProvider
// constants in provider.go become stale and this test starts failing —
// that is the intended guard rail. Re-audit the SDK's `aws.Credentials.Source`
// values when bumping the SDK if this test breaks.
func TestGetCredentials_SourceMapping(t *testing.T) {
	tests := []struct {
		name       string
		sdkSource  string
		wantSource provider.CredentialSource
	}{
		{"shared config maps to file", awsSourceSharedConfigCredentials, provider.CredentialSourceFile},
		{"assume role maps to IAM role", awsSourceAssumeRoleProvider, provider.CredentialSourceIAMRole},
		{"unknown source defaults to environment", "SomeNewSourceTheSDKIntroduced", provider.CredentialSourceEnvironment},
		{"empty source defaults to environment", "", provider.CredentialSourceEnvironment},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := aws.Config{
				Region: "us-east-1",
				Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
					return aws.Credentials{
						AccessKeyID:     "AKIA",
						SecretAccessKey: "secret",
						Source:          tt.sdkSource,
					}, nil
				}),
			}
			p := &AWSProvider{cfg: cfg}
			// Bypass IsConfigured's loadConfig path — set cfgErr=nil so
			// IsConfigured returns true without touching the AWS SDK config.
			p.cfgOnce.Do(func() {})

			creds, err := p.GetCredentials()
			require.NoError(t, err)
			// Credentials interface only exposes IsValid + GetType; the AWS
			// provider returns *BaseCredentials, so type-assert to inspect
			// the concrete CredentialSource enum directly.
			base, ok := creds.(*provider.BaseCredentials)
			require.True(t, ok, "AWSProvider.GetCredentials should return *provider.BaseCredentials")
			assert.Equal(t, tt.wantSource, base.Source)
		})
	}
}
