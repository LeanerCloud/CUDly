// Package provider defines the core abstractions for multi-cloud support
package provider

import (
	"context"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/aws/aws-sdk-go-v2/aws"
)

// Provider represents a cloud provider (AWS, Azure, GCP)
type Provider interface {
	// Identity
	Name() string        // "aws", "azure", "gcp"
	DisplayName() string // "Amazon Web Services", "Microsoft Azure", "Google Cloud Platform"

	// Authentication
	IsConfigured() bool                            // Check if credentials are available
	GetCredentials() (Credentials, error)          // Get current credentials
	ValidateCredentials(ctx context.Context) error // Validate credentials are working

	// Accounts/Projects/Subscriptions
	GetAccounts(ctx context.Context) ([]common.Account, error) // List all accessible accounts

	// Regions
	GetRegions(ctx context.Context) ([]common.Region, error) // List all available regions
	GetDefaultRegion() string                                // Get default region for this provider

	// Services
	GetSupportedServices() []common.ServiceType // List services supported by this provider
	GetServiceClient(ctx context.Context, service common.ServiceType, region string) (ServiceClient, error)

	// Recommendations
	GetRecommendationsClient(ctx context.Context) (RecommendationsClient, error)
}

// ServiceClient handles operations for a specific service in a specific region
type ServiceClient interface {
	// Service identity
	GetServiceType() common.ServiceType
	GetRegion() string

	// Recommendations
	GetRecommendations(ctx context.Context, params common.RecommendationParams) ([]common.Recommendation, error)

	// Commitments (RI/SP/CUD/etc)
	GetExistingCommitments(ctx context.Context) ([]common.Commitment, error)
	PurchaseCommitment(ctx context.Context, rec common.Recommendation, opts common.PurchaseOptions) (common.PurchaseResult, error)
	ValidateOffering(ctx context.Context, rec common.Recommendation) error
	GetOfferingDetails(ctx context.Context, rec common.Recommendation) (*common.OfferingDetails, error)

	// Resource validation
	GetValidResourceTypes(ctx context.Context) ([]string, error)
}

// RecommendationsClient provides centralized recommendations across all services
type RecommendationsClient interface {
	// Get recommendations with filtering
	GetRecommendations(ctx context.Context, params common.RecommendationParams) ([]common.Recommendation, error)

	// Get recommendations for a specific service
	GetRecommendationsForService(ctx context.Context, service common.ServiceType) ([]common.Recommendation, error)

	// Get recommendations for all supported services
	GetAllRecommendations(ctx context.Context) ([]common.Recommendation, error)
}

// Credentials represents cloud provider credentials
type Credentials interface {
	IsValid() bool
	GetType() string // "environment", "file", "iam-role", "msi", "adc", etc.
}

// ProviderConfig represents configuration for a provider
type ProviderConfig struct {
	Name string

	// Deprecated: Profile is overloaded with provider-specific semantics
	// (AWS: named profile, Azure: subscription ID, GCP: project ID). Prefer
	// the typed AWSProfile/AzureSubscriptionID/GCPProjectID fields below;
	// when both are set, the typed field wins.
	Profile string

	// Typed per-provider identity fields. When set, these take precedence
	// over Profile. Each provider only reads its own field and ignores the
	// others, so a single ProviderConfig can be reused across providers.
	AWSProfile          string // AWS named profile from ~/.aws/credentials or ~/.aws/config
	AzureSubscriptionID string // Azure subscription ID (UUID)
	GCPProjectID        string // GCP project ID (e.g. "my-project")

	Region         string
	CredentialPath string
	Endpoint       string // For custom endpoints

	// Optional pre-resolved credentials. Each field is typed as `any` to
	// keep this module free of Azure/GCP SDK dependencies; providers
	// type-assert to their expected concrete type and ignore mismatches:
	//   - AzureTokenCredential: github.com/Azure/azure-sdk-for-go/sdk/azcore.TokenCredential
	//   - GCPTokenSource:        golang.org/x/oauth2.TokenSource
	// When unset (nil), providers fall back to ambient credentials
	// (DefaultAzureCredential / Application Default Credentials).
	AWSCredentialsProvider aws.CredentialsProvider // optional: override ambient AWS credentials
	AzureTokenCredential   any
	GCPTokenSource         any

	// ProviderOverride, if non-nil, is returned directly by CreateProvider without
	// going through the registry. Use this to inject a pre-built, pre-authenticated
	// provider when the typed credential slots above aren't expressive enough.
	ProviderOverride Provider
}
