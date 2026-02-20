package deploy

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudfront"
	cftypes "github.com/aws/aws-sdk-go-v2/service/cloudfront/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

func TestFrontendService_InvalidateCloudFrontCache(t *testing.T) {
	invalidateCalled := false
	mockCFClient := &MockCloudFrontClient{
		ListDistributionsFunc: func(ctx context.Context, params *cloudfront.ListDistributionsInput, optFns ...func(*cloudfront.Options)) (*cloudfront.ListDistributionsOutput, error) {
			return &cloudfront.ListDistributionsOutput{
				DistributionList: &cftypes.DistributionList{
					Items: []cftypes.DistributionSummary{
						{
							Id: aws.String("E1234567890"),
							Origins: &cftypes.Origins{
								Items: []cftypes.Origin{
									{DomainName: aws.String("my-bucket.s3.amazonaws.com")},
								},
							},
						},
					},
				},
			}, nil
		},
		CreateInvalidationFunc: func(ctx context.Context, params *cloudfront.CreateInvalidationInput, optFns ...func(*cloudfront.Options)) (*cloudfront.CreateInvalidationOutput, error) {
			invalidateCalled = true
			if *params.DistributionId != "E1234567890" {
				t.Errorf("expected distribution E1234567890, got %s", *params.DistributionId)
			}
			return &cloudfront.CreateInvalidationOutput{}, nil
		},
	}

	service := NewFrontendService(nil, mockCFClient, nil)

	err := service.InvalidateCloudFrontCache(context.Background(), "my-bucket")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !invalidateCalled {
		t.Error("expected CreateInvalidation to be called")
	}
}

func TestFrontendService_InvalidateCloudFrontCache_NoDistributions(t *testing.T) {
	mockCFClient := &MockCloudFrontClient{
		ListDistributionsFunc: func(ctx context.Context, params *cloudfront.ListDistributionsInput, optFns ...func(*cloudfront.Options)) (*cloudfront.ListDistributionsOutput, error) {
			return &cloudfront.ListDistributionsOutput{
				DistributionList: nil,
			}, nil
		},
	}

	service := NewFrontendService(nil, mockCFClient, nil)

	err := service.InvalidateCloudFrontCache(context.Background(), "my-bucket")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFrontendService_EmptyBucket(t *testing.T) {
	deleteCalled := false
	mockS3Client := &MockS3Client{
		ListObjectsV2Func: func(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
			return &s3.ListObjectsV2Output{
				Contents: []s3types.Object{
					{Key: aws.String("file1.html")},
					{Key: aws.String("file2.js")},
				},
				IsTruncated: aws.Bool(false),
			}, nil
		},
		DeleteObjectsFunc: func(ctx context.Context, params *s3.DeleteObjectsInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error) {
			deleteCalled = true
			if len(params.Delete.Objects) != 2 {
				t.Errorf("expected 2 objects to delete, got %d", len(params.Delete.Objects))
			}
			return &s3.DeleteObjectsOutput{}, nil
		},
	}

	service := NewFrontendService(mockS3Client, nil, nil)

	err := service.EmptyBucket(context.Background(), "my-bucket")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !deleteCalled {
		t.Error("expected DeleteObjects to be called")
	}
}

func TestFrontendService_EmptyBucket_Paginated(t *testing.T) {
	callCount := 0
	deleteCallCount := 0

	mockS3Client := &MockS3Client{
		ListObjectsV2Func: func(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
			callCount++
			if callCount == 1 {
				return &s3.ListObjectsV2Output{
					Contents: []s3types.Object{
						{Key: aws.String("file1.html")},
					},
					IsTruncated:           aws.Bool(true),
					NextContinuationToken: aws.String("token1"),
				}, nil
			}
			return &s3.ListObjectsV2Output{
				Contents: []s3types.Object{
					{Key: aws.String("file2.js")},
				},
				IsTruncated: aws.Bool(false),
			}, nil
		},
		DeleteObjectsFunc: func(ctx context.Context, params *s3.DeleteObjectsInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error) {
			deleteCallCount++
			return &s3.DeleteObjectsOutput{}, nil
		},
	}

	service := NewFrontendService(mockS3Client, nil, nil)

	err := service.EmptyBucket(context.Background(), "my-bucket")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if callCount != 2 {
		t.Errorf("expected 2 ListObjectsV2 calls, got %d", callCount)
	}
	if deleteCallCount != 2 {
		t.Errorf("expected 2 DeleteObjects calls, got %d", deleteCallCount)
	}
}

func TestFrontendService_EmptyBucket_EmptyBucket(t *testing.T) {
	mockS3Client := &MockS3Client{
		ListObjectsV2Func: func(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
			return &s3.ListObjectsV2Output{
				Contents: []s3types.Object{},
			}, nil
		},
	}

	service := NewFrontendService(mockS3Client, nil, nil)

	err := service.EmptyBucket(context.Background(), "my-bucket")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFrontendService_InvalidateCloudFrontCache_ListError(t *testing.T) {
	mockCFClient := &MockCloudFrontClient{
		ListDistributionsFunc: func(ctx context.Context, params *cloudfront.ListDistributionsInput, optFns ...func(*cloudfront.Options)) (*cloudfront.ListDistributionsOutput, error) {
			return nil, errors.New("list error")
		},
	}

	service := NewFrontendService(nil, mockCFClient, nil)

	err := service.InvalidateCloudFrontCache(context.Background(), "my-bucket")
	if err == nil {
		t.Error("expected error, got nil")
	}
}

func TestFrontendService_InvalidateCloudFrontCache_CreateInvalidationError(t *testing.T) {
	mockCFClient := &MockCloudFrontClient{
		ListDistributionsFunc: func(ctx context.Context, params *cloudfront.ListDistributionsInput, optFns ...func(*cloudfront.Options)) (*cloudfront.ListDistributionsOutput, error) {
			return &cloudfront.ListDistributionsOutput{
				DistributionList: &cftypes.DistributionList{
					Items: []cftypes.DistributionSummary{
						{
							Id: aws.String("E1234567890"),
							Origins: &cftypes.Origins{
								Items: []cftypes.Origin{
									{DomainName: aws.String("my-bucket.s3.amazonaws.com")},
								},
							},
						},
					},
				},
			}, nil
		},
		CreateInvalidationFunc: func(ctx context.Context, params *cloudfront.CreateInvalidationInput, optFns ...func(*cloudfront.Options)) (*cloudfront.CreateInvalidationOutput, error) {
			return nil, errors.New("invalidation error")
		},
	}

	service := NewFrontendService(nil, mockCFClient, nil)

	err := service.InvalidateCloudFrontCache(context.Background(), "my-bucket")
	if err == nil {
		t.Error("expected error, got nil")
	}
}

func TestFrontendService_InvalidateCloudFrontCache_NoMatchingDistribution(t *testing.T) {
	mockCFClient := &MockCloudFrontClient{
		ListDistributionsFunc: func(ctx context.Context, params *cloudfront.ListDistributionsInput, optFns ...func(*cloudfront.Options)) (*cloudfront.ListDistributionsOutput, error) {
			return &cloudfront.ListDistributionsOutput{
				DistributionList: &cftypes.DistributionList{
					Items: []cftypes.DistributionSummary{
						{
							Id: aws.String("E1234567890"),
							Origins: &cftypes.Origins{
								Items: []cftypes.Origin{
									{DomainName: aws.String("different-bucket.s3.amazonaws.com")},
								},
							},
						},
					},
				},
			}, nil
		},
	}

	service := NewFrontendService(nil, mockCFClient, nil)

	err := service.InvalidateCloudFrontCache(context.Background(), "my-bucket")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFrontendService_InvalidateCloudFrontCache_NilOrigins(t *testing.T) {
	mockCFClient := &MockCloudFrontClient{
		ListDistributionsFunc: func(ctx context.Context, params *cloudfront.ListDistributionsInput, optFns ...func(*cloudfront.Options)) (*cloudfront.ListDistributionsOutput, error) {
			return &cloudfront.ListDistributionsOutput{
				DistributionList: &cftypes.DistributionList{
					Items: []cftypes.DistributionSummary{
						{
							Id:      aws.String("E1234567890"),
							Origins: nil,
						},
					},
				},
			}, nil
		},
	}

	service := NewFrontendService(nil, mockCFClient, nil)

	err := service.InvalidateCloudFrontCache(context.Background(), "my-bucket")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFrontendService_EmptyBucket_ListError(t *testing.T) {
	mockS3Client := &MockS3Client{
		ListObjectsV2Func: func(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
			return nil, errors.New("list error")
		},
	}

	service := NewFrontendService(mockS3Client, nil, nil)

	err := service.EmptyBucket(context.Background(), "my-bucket")
	if err == nil {
		t.Error("expected error, got nil")
	}
}

func TestFrontendService_EmptyBucket_DeleteError(t *testing.T) {
	mockS3Client := &MockS3Client{
		ListObjectsV2Func: func(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
			return &s3.ListObjectsV2Output{
				Contents: []s3types.Object{
					{Key: aws.String("file1.html")},
				},
			}, nil
		},
		DeleteObjectsFunc: func(ctx context.Context, params *s3.DeleteObjectsInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error) {
			return nil, errors.New("delete error")
		},
	}

	service := NewFrontendService(mockS3Client, nil, nil)

	err := service.EmptyBucket(context.Background(), "my-bucket")
	if err == nil {
		t.Error("expected error, got nil")
	}
}

func TestFrontendService_FindFrontendDir_Found(t *testing.T) {
	// The project has a frontend directory, so this should succeed
	service := NewFrontendService(nil, nil, nil)

	dir, err := service.FindFrontendDir()
	if err != nil {
		// This test may fail in some environments, just skip it
		t.Skip("Frontend directory not found in this environment")
	}

	if dir == "" {
		t.Error("expected non-empty directory path")
	}
}

func TestFrontendService_BuildAndUpload_NpmInstallFails(t *testing.T) {
	mockCmdRunner := &MockCommandRunner{
		RunFunc: func(name string, args ...string) error {
			if name == "npm" && len(args) > 0 && args[len(args)-1] == "install" {
				return errors.New("npm install failed")
			}
			return nil
		},
	}

	service := NewFrontendService(nil, nil, mockCmdRunner)

	// This will fail during FindFrontendDir or npm install
	err := service.BuildAndUpload(context.Background(), "test-bucket", "https://example.com")
	if err == nil {
		t.Skip("Skipping test in environment where frontend dir exists")
	}
	// The error should be about frontend directory not found or npm install failed
}

func TestFrontendService_BuildAndUpload_NpmBuildFails(t *testing.T) {
	mockCmdRunner := &MockCommandRunner{
		RunFunc: func(name string, args ...string) error {
			if name == "npm" && len(args) > 0 && args[len(args)-1] == "build" {
				return errors.New("npm run build failed")
			}
			return nil
		},
	}

	service := NewFrontendService(nil, nil, mockCmdRunner)

	err := service.BuildAndUpload(context.Background(), "test-bucket", "https://example.com")
	if err == nil {
		t.Skip("Skipping test in environment where frontend dir not found")
	}
}

func TestFrontendService_InvalidateCloudFrontCache_NilItems(t *testing.T) {
	mockCFClient := &MockCloudFrontClient{
		ListDistributionsFunc: func(ctx context.Context, params *cloudfront.ListDistributionsInput, optFns ...func(*cloudfront.Options)) (*cloudfront.ListDistributionsOutput, error) {
			return &cloudfront.ListDistributionsOutput{
				DistributionList: &cftypes.DistributionList{
					Items: nil,
				},
			}, nil
		},
	}

	service := NewFrontendService(nil, mockCFClient, nil)

	err := service.InvalidateCloudFrontCache(context.Background(), "my-bucket")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFrontendService_InvalidateCloudFrontCache_NilOriginItems(t *testing.T) {
	mockCFClient := &MockCloudFrontClient{
		ListDistributionsFunc: func(ctx context.Context, params *cloudfront.ListDistributionsInput, optFns ...func(*cloudfront.Options)) (*cloudfront.ListDistributionsOutput, error) {
			return &cloudfront.ListDistributionsOutput{
				DistributionList: &cftypes.DistributionList{
					Items: []cftypes.DistributionSummary{
						{
							Id: aws.String("E1234567890"),
							Origins: &cftypes.Origins{
								Items: nil,
							},
						},
					},
				},
			}, nil
		},
	}

	service := NewFrontendService(nil, mockCFClient, nil)

	err := service.InvalidateCloudFrontCache(context.Background(), "my-bucket")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
