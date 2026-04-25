// Package aws provides AWS cloud provider implementation
package aws

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/organizations"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/smithy-go"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/logging"
	"github.com/LeanerCloud/CUDly/pkg/provider"
	"github.com/LeanerCloud/CUDly/providers/aws/services/savingsplans"
)

// AWS SDK v2 credential source identifiers.
//
// These are NOT part of the aws-sdk-go-v2 public API — they're the string
// values the SDK stamps into aws.Credentials.Source for each credential
// chain. We map them back to our provider.CredentialSource enum in
// GetCredentials. If the SDK ever renames them on upgrade,
// TestGetCredentials_SourceMapping will flag the mismatch at test time.
const (
	awsSourceSharedConfigCredentials = "SharedConfigCredentials"
	awsSourceAssumeRoleProvider      = "AssumeRoleProvider"
)

// STSClient interface for STS operations (enables mocking)
type STSClient interface {
	GetCallerIdentity(ctx context.Context, params *sts.GetCallerIdentityInput, optFns ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error)
}

// OrganizationsClient interface for Organizations operations (enables mocking)
type OrganizationsClient interface {
	ListAccounts(ctx context.Context, params *organizations.ListAccountsInput, optFns ...func(*organizations.Options)) (*organizations.ListAccountsOutput, error)
}

// EC2Client interface for EC2 operations (enables mocking)
type EC2Client interface {
	DescribeRegions(ctx context.Context, params *ec2.DescribeRegionsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeRegionsOutput, error)
}

// ConfigLoader interface for loading AWS config (enables mocking)
type ConfigLoader interface {
	LoadDefaultConfig(ctx context.Context, optFns ...func(*config.LoadOptions) error) (aws.Config, error)
}

// realConfigLoader implements ConfigLoader using the real AWS SDK
type realConfigLoader struct{}

func (r *realConfigLoader) LoadDefaultConfig(ctx context.Context, optFns ...func(*config.LoadOptions) error) (aws.Config, error) {
	return config.LoadDefaultConfig(ctx, optFns...)
}

// OrganizationsPaginator interface for Organizations pagination (enables mocking)
type OrganizationsPaginator interface {
	HasMorePages() bool
	NextPage(ctx context.Context, optFns ...func(*organizations.Options)) (*organizations.ListAccountsOutput, error)
}

// realOrganizationsPaginator wraps the real paginator
type realOrganizationsPaginator struct {
	paginator *organizations.ListAccountsPaginator
}

func (r *realOrganizationsPaginator) HasMorePages() bool {
	return r.paginator.HasMorePages()
}

func (r *realOrganizationsPaginator) NextPage(ctx context.Context, optFns ...func(*organizations.Options)) (*organizations.ListAccountsOutput, error) {
	return r.paginator.NextPage(ctx, optFns...)
}

// AWSProvider implements the Provider interface for AWS
type AWSProvider struct {
	cfg                 aws.Config
	cfgOnce             sync.Once
	cfgErr              error // non-nil if config loading failed
	profile             string
	region              string
	configLoader        ConfigLoader
	stsClient           STSClient
	ec2Client           EC2Client
	orgPaginator        OrganizationsPaginator
	credentialsProvider aws.CredentialsProvider // optional override for per-account execution
}

// NewAWSProvider creates a new AWS provider instance.
//
// Profile resolution order:
//  1. config.AWSProfile (typed field, preferred)
//  2. config.Profile (deprecated overload — kept for backwards compatibility)
func NewAWSProvider(config *provider.ProviderConfig) (*AWSProvider, error) {
	p := &AWSProvider{}

	if config != nil {
		p.profile = resolveAWSProfile(config)
		p.region = config.Region
		p.credentialsProvider = config.AWSCredentialsProvider
	}

	return p, nil
}

// resolveAWSProfile picks the AWS profile name from the typed field,
// falling back to the deprecated Profile field.
func resolveAWSProfile(config *provider.ProviderConfig) string {
	if config.AWSProfile != "" {
		return config.AWSProfile
	}
	return config.Profile
}

// SetConfigLoader sets the config loader (for testing)
func (p *AWSProvider) SetConfigLoader(loader ConfigLoader) {
	p.configLoader = loader
}

// SetSTSClient sets the STS client (for testing)
func (p *AWSProvider) SetSTSClient(client STSClient) {
	p.stsClient = client
}

// SetEC2Client sets the EC2 client (for testing)
func (p *AWSProvider) SetEC2Client(client EC2Client) {
	p.ec2Client = client
}

// SetOrganizationsPaginator sets the organizations paginator (for testing)
func (p *AWSProvider) SetOrganizationsPaginator(paginator OrganizationsPaginator) {
	p.orgPaginator = paginator
}

// Name returns the provider name
func (p *AWSProvider) Name() string {
	return "aws"
}

// DisplayName returns the human-readable provider name
func (p *AWSProvider) DisplayName() string {
	return "Amazon Web Services"
}

// IsConfigured checks if AWS credentials are available. The config is loaded at most
// once (thread-safe via sync.Once) to avoid data races on concurrent calls.
func (p *AWSProvider) IsConfigured() bool {
	p.cfgOnce.Do(func() {
		p.cfgErr = p.loadConfig()
	})
	return p.cfgErr == nil
}

// loadConfig loads the AWS SDK config. Called at most once via cfgOnce.
func (p *AWSProvider) loadConfig() error {
	ctx := context.Background()

	var opts []func(*config.LoadOptions) error
	if p.profile != "" {
		opts = append(opts, config.WithSharedConfigProfile(p.profile))
	}
	if p.region != "" {
		opts = append(opts, config.WithRegion(p.region))
	}
	if p.credentialsProvider != nil {
		opts = append(opts, config.WithCredentialsProvider(p.credentialsProvider))
	}

	var loader ConfigLoader
	if p.configLoader != nil {
		loader = p.configLoader
	} else {
		loader = &realConfigLoader{}
	}

	cfg, err := loader.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return err
	}
	p.cfg = cfg
	return nil
}

// GetCredentials returns AWS credentials
func (p *AWSProvider) GetCredentials() (provider.Credentials, error) {
	if !p.IsConfigured() {
		return nil, fmt.Errorf("AWS is not configured")
	}

	creds, err := p.cfg.Credentials.Retrieve(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve AWS credentials: %w", err)
	}

	credType := provider.CredentialSourceEnvironment
	if creds.Source != "" {
		// These string constants are AWS SDK v2 internal implementation
		// details, not a stable public contract. If the SDK renames them
		// on upgrade, TestGetCredentials_SourceMapping in provider_test.go
		// starts failing — that is the guard rail. Keep this list in sync
		// with the SDK when bumping aws-sdk-go-v2.
		switch creds.Source {
		case awsSourceSharedConfigCredentials:
			credType = provider.CredentialSourceFile
		case awsSourceAssumeRoleProvider:
			credType = provider.CredentialSourceIAMRole
		}
	}

	return &provider.BaseCredentials{
		Source: credType,
		Valid:  true,
	}, nil
}

// ValidateCredentials validates that AWS credentials are working
func (p *AWSProvider) ValidateCredentials(ctx context.Context) error {
	if !p.IsConfigured() {
		return fmt.Errorf("AWS is not configured")
	}

	// Use injected STS client if available (for testing)
	var stsClient STSClient
	if p.stsClient != nil {
		stsClient = p.stsClient
	} else {
		stsClient = sts.NewFromConfig(p.cfg)
	}

	_, err := stsClient.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return fmt.Errorf("AWS credentials validation failed: %w", err)
	}

	return nil
}

// orgListAccountsSilentErrorCodes are the AWS Organizations ListAccounts
// error codes that represent "expected" fallback conditions: the caller is
// not part of an organization, or lacks the Organizations permission. For
// those codes we return the accumulated accounts (just the caller's own
// account from the earlier STS GetCallerIdentity call) with no error.
//
// Any OTHER error (throttling, network, auth) is a real failure — returning
// a silently-truncated list is unsafe for the purchase flow that consumes
// GetAccounts, so we log and propagate so the caller can decide.
var orgListAccountsSilentErrorCodes = map[string]struct{}{
	"AWSOrganizationsNotInUseException": {},
	"AccessDeniedException":             {},
}

// appendOrgAccounts adds organization member accounts to the slice, skipping
// the current account (already added as the default) and suspended accounts
// with nil fields.
//
// Error classification: returns the accumulated accounts with nil error only
// for the expected fallback cases (not in an org, permission denied). Any
// other mid-pagination error (throttling, network, SDK bug) is returned to
// the caller so an incomplete list can't silently slip into the purchase
// flow as if it were complete. See orgListAccountsSilentErrorCodes above.
func (p *AWSProvider) appendOrgAccounts(ctx context.Context, accounts []common.Account, currentAccountID string) ([]common.Account, error) {
	var paginator OrganizationsPaginator
	if p.orgPaginator != nil {
		paginator = p.orgPaginator
	} else {
		orgClient := organizations.NewFromConfig(p.cfg)
		paginator = &realOrganizationsPaginator{
			paginator: organizations.NewListAccountsPaginator(orgClient, &organizations.ListAccountsInput{}),
		}
	}

	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			var apiErr smithy.APIError
			if errors.As(err, &apiErr) {
				if _, silent := orgListAccountsSilentErrorCodes[apiErr.ErrorCode()]; silent {
					return accounts, nil
				}
			}
			logging.Warnf("AWS Organizations ListAccounts pagination failed mid-run: %v", err)
			return accounts, fmt.Errorf("organizations: list accounts: %w", err)
		}
		for _, acc := range output.Accounts {
			if acc.Id == nil || acc.Name == nil || *acc.Id == currentAccountID {
				continue
			}
			accounts = append(accounts, common.Account{
				Provider:    common.ProviderAWS,
				ID:          *acc.Id,
				Name:        *acc.Name,
				DisplayName: *acc.Name,
				IsDefault:   false,
			})
		}
	}
	return accounts, nil
}

// GetAccounts returns all accessible AWS accounts
func (p *AWSProvider) GetAccounts(ctx context.Context) ([]common.Account, error) {
	if !p.IsConfigured() {
		return nil, fmt.Errorf("AWS is not configured")
	}

	var stsClient STSClient
	if p.stsClient != nil {
		stsClient = p.stsClient
	} else {
		stsClient = sts.NewFromConfig(p.cfg)
	}

	identity, err := stsClient.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return nil, fmt.Errorf("failed to get current account: %w", err)
	}
	if identity.Account == nil {
		return nil, fmt.Errorf("STS GetCallerIdentity returned nil account")
	}

	accounts := []common.Account{{
		Provider:    common.ProviderAWS,
		ID:          *identity.Account,
		Name:        *identity.Account,
		DisplayName: *identity.Account,
		IsDefault:   true,
	}}

	return p.appendOrgAccounts(ctx, accounts, *identity.Account)
}

// GetRegions returns all available AWS regions using EC2 DescribeRegions API
func (p *AWSProvider) GetRegions(ctx context.Context) ([]common.Region, error) {
	if !p.IsConfigured() {
		return nil, fmt.Errorf("AWS is not configured")
	}

	// Use injected EC2 client if available (for testing)
	var client EC2Client
	if p.ec2Client != nil {
		client = p.ec2Client
	} else {
		client = ec2.NewFromConfig(p.cfg)
	}

	result, err := client.DescribeRegions(ctx, &ec2.DescribeRegionsInput{
		AllRegions: aws.Bool(false), // Only return enabled regions
	})
	if err != nil {
		return nil, fmt.Errorf("failed to describe AWS regions: %w", err)
	}

	regions := make([]common.Region, 0, len(result.Regions))
	for _, region := range result.Regions {
		if region.RegionName == nil {
			continue
		}

		displayName := *region.RegionName
		if region.OptInStatus != nil {
			displayName = fmt.Sprintf("%s (%s)", *region.RegionName, *region.OptInStatus)
		}

		regions = append(regions, common.Region{
			Provider:    common.ProviderAWS,
			ID:          *region.RegionName,
			Name:        *region.RegionName,
			DisplayName: displayName,
		})
	}

	return regions, nil
}

// GetDefaultRegion returns the default AWS region
func (p *AWSProvider) GetDefaultRegion() string {
	if p.region != "" {
		return p.region
	}
	if p.IsConfigured() && p.cfg.Region != "" {
		return p.cfg.Region
	}
	return "us-east-1"
}

// GetSupportedServices returns the list of services supported by AWS provider.
// Savings Plans are exposed as four distinct services (one per AWS plan type)
// so users can configure term/payment defaults per plan type via ServiceConfig.
func (p *AWSProvider) GetSupportedServices() []common.ServiceType {
	return []common.ServiceType{
		common.ServiceCompute,
		common.ServiceRelationalDB,
		common.ServiceCache,
		common.ServiceSearch,
		common.ServiceDataWarehouse,
		common.ServiceSavingsPlansCompute,
		common.ServiceSavingsPlansEC2Instance,
		common.ServiceSavingsPlansSageMaker,
		common.ServiceSavingsPlansDatabase,
		// Legacy service types for backward compatibility
		common.ServiceEC2,
		common.ServiceRDS,
		common.ServiceElastiCache,
		common.ServiceOpenSearch,
		common.ServiceRedshift,
		common.ServiceMemoryDB,
	}
}

// GetServiceClient returns a service client for the specified service and region
func (p *AWSProvider) GetServiceClient(ctx context.Context, service common.ServiceType, region string) (provider.ServiceClient, error) {
	if !p.IsConfigured() {
		return nil, fmt.Errorf("AWS is not configured")
	}

	// Create a regional config
	regionalCfg := p.cfg.Copy()
	regionalCfg.Region = region

	switch service {
	case common.ServiceCompute, common.ServiceEC2:
		return NewEC2Client(regionalCfg), nil
	case common.ServiceRelationalDB, common.ServiceRDS:
		return NewRDSClient(regionalCfg), nil
	case common.ServiceCache, common.ServiceElastiCache:
		return NewElastiCacheClient(regionalCfg), nil
	case common.ServiceSearch, common.ServiceOpenSearch:
		return NewOpenSearchClient(regionalCfg), nil
	case common.ServiceDataWarehouse, common.ServiceRedshift:
		return NewRedshiftClient(regionalCfg), nil
	case common.ServiceMemoryDB:
		return NewMemoryDBClient(regionalCfg), nil
	case common.ServiceSavingsPlansCompute,
		common.ServiceSavingsPlansEC2Instance,
		common.ServiceSavingsPlansSageMaker,
		common.ServiceSavingsPlansDatabase:
		pt, _ := savingsplans.PlanTypeForServiceType(service)
		return NewSavingsPlansClient(regionalCfg, pt), nil
	default:
		return nil, fmt.Errorf("unsupported service: %s", service)
	}
}

// GetRecommendationsClient returns a recommendations client
func (p *AWSProvider) GetRecommendationsClient(ctx context.Context) (provider.RecommendationsClient, error) {
	if !p.IsConfigured() {
		return nil, fmt.Errorf("AWS is not configured")
	}

	return NewRecommendationsClient(p.cfg), nil
}

// Register the AWS provider with the global registry
func init() {
	provider.RegisterProvider("aws", func(config *provider.ProviderConfig) (provider.Provider, error) {
		return NewAWSProvider(config)
	})
}
