package deploy

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/cloudfront"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	"github.com/aws/aws-sdk-go-v2/service/ecrpublic"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// MockECRClient is a mock implementation of ECRClient.
type MockECRClient struct {
	DescribeRepositoriesFunc  func(ctx context.Context, params *ecr.DescribeRepositoriesInput, optFns ...func(*ecr.Options)) (*ecr.DescribeRepositoriesOutput, error)
	CreateRepositoryFunc      func(ctx context.Context, params *ecr.CreateRepositoryInput, optFns ...func(*ecr.Options)) (*ecr.CreateRepositoryOutput, error)
	GetAuthorizationTokenFunc func(ctx context.Context, params *ecr.GetAuthorizationTokenInput, optFns ...func(*ecr.Options)) (*ecr.GetAuthorizationTokenOutput, error)
}

func (m *MockECRClient) DescribeRepositories(ctx context.Context, params *ecr.DescribeRepositoriesInput, optFns ...func(*ecr.Options)) (*ecr.DescribeRepositoriesOutput, error) {
	if m.DescribeRepositoriesFunc != nil {
		return m.DescribeRepositoriesFunc(ctx, params, optFns...)
	}
	return &ecr.DescribeRepositoriesOutput{}, nil
}

func (m *MockECRClient) CreateRepository(ctx context.Context, params *ecr.CreateRepositoryInput, optFns ...func(*ecr.Options)) (*ecr.CreateRepositoryOutput, error) {
	if m.CreateRepositoryFunc != nil {
		return m.CreateRepositoryFunc(ctx, params, optFns...)
	}
	return &ecr.CreateRepositoryOutput{}, nil
}

func (m *MockECRClient) GetAuthorizationToken(ctx context.Context, params *ecr.GetAuthorizationTokenInput, optFns ...func(*ecr.Options)) (*ecr.GetAuthorizationTokenOutput, error) {
	if m.GetAuthorizationTokenFunc != nil {
		return m.GetAuthorizationTokenFunc(ctx, params, optFns...)
	}
	return &ecr.GetAuthorizationTokenOutput{}, nil
}

// MockECRPublicClient is a mock implementation of ECRPublicClient.
type MockECRPublicClient struct {
	GetAuthorizationTokenFunc func(ctx context.Context, params *ecrpublic.GetAuthorizationTokenInput, optFns ...func(*ecrpublic.Options)) (*ecrpublic.GetAuthorizationTokenOutput, error)
}

func (m *MockECRPublicClient) GetAuthorizationToken(ctx context.Context, params *ecrpublic.GetAuthorizationTokenInput, optFns ...func(*ecrpublic.Options)) (*ecrpublic.GetAuthorizationTokenOutput, error) {
	if m.GetAuthorizationTokenFunc != nil {
		return m.GetAuthorizationTokenFunc(ctx, params, optFns...)
	}
	return &ecrpublic.GetAuthorizationTokenOutput{}, nil
}

// MockS3Client is a mock implementation of S3Client.
type MockS3Client struct {
	PutObjectFunc     func(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	DeleteObjectsFunc func(ctx context.Context, params *s3.DeleteObjectsInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error)
	ListObjectsV2Func func(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
}

func (m *MockS3Client) PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	if m.PutObjectFunc != nil {
		return m.PutObjectFunc(ctx, params, optFns...)
	}
	return &s3.PutObjectOutput{}, nil
}

func (m *MockS3Client) DeleteObjects(ctx context.Context, params *s3.DeleteObjectsInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error) {
	if m.DeleteObjectsFunc != nil {
		return m.DeleteObjectsFunc(ctx, params, optFns...)
	}
	return &s3.DeleteObjectsOutput{}, nil
}

func (m *MockS3Client) ListObjectsV2(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	if m.ListObjectsV2Func != nil {
		return m.ListObjectsV2Func(ctx, params, optFns...)
	}
	return &s3.ListObjectsV2Output{}, nil
}

// MockCloudFrontClient is a mock implementation of CloudFrontClient.
type MockCloudFrontClient struct {
	ListDistributionsFunc  func(ctx context.Context, params *cloudfront.ListDistributionsInput, optFns ...func(*cloudfront.Options)) (*cloudfront.ListDistributionsOutput, error)
	CreateInvalidationFunc func(ctx context.Context, params *cloudfront.CreateInvalidationInput, optFns ...func(*cloudfront.Options)) (*cloudfront.CreateInvalidationOutput, error)
}

func (m *MockCloudFrontClient) ListDistributions(ctx context.Context, params *cloudfront.ListDistributionsInput, optFns ...func(*cloudfront.Options)) (*cloudfront.ListDistributionsOutput, error) {
	if m.ListDistributionsFunc != nil {
		return m.ListDistributionsFunc(ctx, params, optFns...)
	}
	return &cloudfront.ListDistributionsOutput{}, nil
}

func (m *MockCloudFrontClient) CreateInvalidation(ctx context.Context, params *cloudfront.CreateInvalidationInput, optFns ...func(*cloudfront.Options)) (*cloudfront.CreateInvalidationOutput, error) {
	if m.CreateInvalidationFunc != nil {
		return m.CreateInvalidationFunc(ctx, params, optFns...)
	}
	return &cloudfront.CreateInvalidationOutput{}, nil
}

// MockCommandRunner is a mock implementation of CommandRunner.
type MockCommandRunner struct {
	RunFunc          func(name string, args ...string) error
	RunWithStdinFunc func(name string, stdin string, args ...string) error
	Commands         [][]string // Records all commands run
}

func (m *MockCommandRunner) Run(name string, args ...string) error {
	cmd := append([]string{name}, args...)
	m.Commands = append(m.Commands, cmd)
	if m.RunFunc != nil {
		return m.RunFunc(name, args...)
	}
	return nil
}

func (m *MockCommandRunner) RunWithStdin(name string, stdin string, args ...string) error {
	cmd := append([]string{name}, args...)
	m.Commands = append(m.Commands, cmd)
	if m.RunWithStdinFunc != nil {
		return m.RunWithStdinFunc(name, stdin, args...)
	}
	return nil
}
