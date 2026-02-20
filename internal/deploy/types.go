// Package deploy provides deployment functionality for CUDly.
package deploy

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/cloudfront"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	"github.com/aws/aws-sdk-go-v2/service/ecrpublic"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// Config holds configuration for the deployment.
type Config struct {
	StackName         string
	Email             string
	Term              int
	PaymentOption     string
	Coverage          float64
	RampSchedule      string
	NotifyDays        int
	EnableDashboard   bool
	DashboardDomain   string
	HostedZoneID      string
	Architecture      string
	MemorySize        int
	SkipBuild         bool
	SkipPush          bool
	SkipFrontend      bool
	SkipAdmin         bool
	ImageTag          string
	CORSAllowedOrigin string
	AdminEmail        string
	AdminPassword     string
}

// ECRClient interface for ECR operations.
type ECRClient interface {
	DescribeRepositories(ctx context.Context, params *ecr.DescribeRepositoriesInput, optFns ...func(*ecr.Options)) (*ecr.DescribeRepositoriesOutput, error)
	CreateRepository(ctx context.Context, params *ecr.CreateRepositoryInput, optFns ...func(*ecr.Options)) (*ecr.CreateRepositoryOutput, error)
	GetAuthorizationToken(ctx context.Context, params *ecr.GetAuthorizationTokenInput, optFns ...func(*ecr.Options)) (*ecr.GetAuthorizationTokenOutput, error)
}

// ECRPublicClient interface for public ECR operations.
type ECRPublicClient interface {
	GetAuthorizationToken(ctx context.Context, params *ecrpublic.GetAuthorizationTokenInput, optFns ...func(*ecrpublic.Options)) (*ecrpublic.GetAuthorizationTokenOutput, error)
}

// S3Client interface for S3 operations.
type S3Client interface {
	PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	DeleteObjects(ctx context.Context, params *s3.DeleteObjectsInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error)
	ListObjectsV2(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
}

// CloudFrontClient interface for CloudFront operations.
type CloudFrontClient interface {
	ListDistributions(ctx context.Context, params *cloudfront.ListDistributionsInput, optFns ...func(*cloudfront.Options)) (*cloudfront.ListDistributionsOutput, error)
	CreateInvalidation(ctx context.Context, params *cloudfront.CreateInvalidationInput, optFns ...func(*cloudfront.Options)) (*cloudfront.CreateInvalidationOutput, error)
}

// CommandRunner interface for running shell commands.
type CommandRunner interface {
	Run(name string, args ...string) error
	RunWithStdin(name string, stdin string, args ...string) error
}
