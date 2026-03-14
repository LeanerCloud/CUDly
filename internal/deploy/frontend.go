package deploy

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"log"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudfront"
	cftypes "github.com/aws/aws-sdk-go-v2/service/cloudfront/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// FrontendService handles frontend build and deployment operations.
type FrontendService struct {
	S3Client         S3Client
	CloudFrontClient CloudFrontClient
	CmdRunner        CommandRunner
}

// NewFrontendService creates a new FrontendService.
func NewFrontendService(s3Client S3Client, cfClient CloudFrontClient, cmdRunner CommandRunner) *FrontendService {
	return &FrontendService{
		S3Client:         s3Client,
		CloudFrontClient: cfClient,
		CmdRunner:        cmdRunner,
	}
}

// BuildAndUpload builds the frontend and uploads it to S3.
func (s *FrontendService) BuildAndUpload(ctx context.Context, bucketName, dashboardURL string) error {
	// Find frontend directory
	frontendDir, err := s.FindFrontendDir()
	if err != nil {
		return fmt.Errorf("frontend directory not found: %w", err)
	}

	// Run npm install
	log.Println("Running npm install...")
	if err := s.CmdRunner.Run("npm", "--prefix", frontendDir, "install"); err != nil {
		return fmt.Errorf("npm install failed: %w", err)
	}

	// Run npm run build
	log.Println("Running npm run build...")
	if err := s.CmdRunner.Run("npm", "--prefix", frontendDir, "run", "build"); err != nil {
		return fmt.Errorf("npm run build failed: %w", err)
	}

	// Upload dist folder to S3
	distDir := filepath.Join(frontendDir, "dist")
	log.Printf("Uploading frontend from %s to s3://%s/", distDir, bucketName)

	if err := s.uploadDirectory(ctx, distDir, bucketName); err != nil {
		return fmt.Errorf("failed to upload files: %w", err)
	}

	log.Println("Frontend uploaded successfully")

	// Invalidate CloudFront cache
	if err := s.InvalidateCloudFrontCache(ctx, bucketName); err != nil {
		log.Printf("Warning: CloudFront cache invalidation failed: %v", err)
		// Don't fail deployment for this
	}

	return nil
}

func (s *FrontendService) uploadDirectory(ctx context.Context, distDir, bucketName string) error {
	return filepath.WalkDir(distDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		// Get relative path
		relPath, err := filepath.Rel(distDir, path)
		if err != nil {
			return err
		}
		// Convert to forward slashes for S3 keys
		key := strings.ReplaceAll(relPath, string(filepath.Separator), "/")

		// Read file
		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", path, err)
		}

		// Determine content type
		contentType := mime.TypeByExtension(filepath.Ext(path))
		if contentType == "" {
			contentType = "application/octet-stream"
		}

		// Set cache control based on file type
		cacheControl := "max-age=31536000" // 1 year for assets
		if strings.HasSuffix(key, ".html") || strings.HasSuffix(key, ".json") || strings.HasSuffix(key, ".webmanifest") {
			cacheControl = "no-cache, no-store, must-revalidate"
		}

		// Upload to S3
		_, err = s.S3Client.PutObject(ctx, &s3.PutObjectInput{
			Bucket:       aws.String(bucketName),
			Key:          aws.String(key),
			Body:         bytes.NewReader(content),
			ContentType:  aws.String(contentType),
			CacheControl: aws.String(cacheControl),
		})
		if err != nil {
			return fmt.Errorf("failed to upload %s: %w", key, err)
		}

		return nil
	})
}

// FindFrontendDir finds the frontend directory.
func (s *FrontendService) FindFrontendDir() (string, error) {
	paths := []string{
		"frontend",
		"../frontend",
		"../../frontend",
	}

	if execPath, err := os.Executable(); err == nil {
		execDir := filepath.Dir(execPath)
		paths = append(paths,
			filepath.Join(execDir, "frontend"),
			filepath.Join(execDir, "../frontend"),
		)
	}

	for _, path := range paths {
		packageJSON := filepath.Join(path, "package.json")
		if _, err := os.Stat(packageJSON); err == nil {
			return filepath.Abs(path)
		}
	}

	return "", fmt.Errorf("frontend directory not found; please run from the CUDly directory")
}

// InvalidateCloudFrontCache invalidates the CloudFront cache for a bucket.
func (s *FrontendService) InvalidateCloudFrontCache(ctx context.Context, bucketName string) error {
	distributionID, err := s.findDistributionForBucket(ctx, bucketName)
	if err != nil {
		return err
	}

	if distributionID == "" {
		return nil // No distribution found for this bucket
	}

	return s.createCacheInvalidation(ctx, distributionID)
}

func (s *FrontendService) findDistributionForBucket(ctx context.Context, bucketName string) (string, error) {
	result, err := s.CloudFrontClient.ListDistributions(ctx, &cloudfront.ListDistributionsInput{})
	if err != nil {
		return "", fmt.Errorf("failed to list distributions: %w", err)
	}

	if result.DistributionList == nil || result.DistributionList.Items == nil {
		return "", nil
	}

	for _, dist := range result.DistributionList.Items {
		if distID := s.checkDistributionOrigins(dist, bucketName); distID != "" {
			return distID, nil
		}
	}

	return "", nil
}

func (s *FrontendService) checkDistributionOrigins(dist cftypes.DistributionSummary, bucketName string) string {
	if dist.Origins == nil || dist.Origins.Items == nil {
		return ""
	}

	for _, origin := range dist.Origins.Items {
		if origin.DomainName != nil && strings.Contains(*origin.DomainName, bucketName) {
			return *dist.Id
		}
	}

	return ""
}

func (s *FrontendService) createCacheInvalidation(ctx context.Context, distributionID string) error {
	log.Printf("Invalidating CloudFront distribution: %s", distributionID)

	_, err := s.CloudFrontClient.CreateInvalidation(ctx, &cloudfront.CreateInvalidationInput{
		DistributionId: aws.String(distributionID),
		InvalidationBatch: &cftypes.InvalidationBatch{
			CallerReference: aws.String(fmt.Sprintf("cudly-deploy-%d", time.Now().Unix())),
			Paths: &cftypes.Paths{
				Quantity: aws.Int32(1),
				Items:    []string{"/*"},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create invalidation: %w", err)
	}

	log.Println("CloudFront cache invalidation created")
	return nil
}

// EmptyBucket empties an S3 bucket (used before deletion).
func (s *FrontendService) EmptyBucket(ctx context.Context, bucketName string) error {
	// List all objects using pagination
	var continuationToken *string

	for {
		result, err := s.S3Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(bucketName),
			ContinuationToken: continuationToken,
		})
		if err != nil {
			return err
		}

		if len(result.Contents) == 0 {
			break
		}

		// Delete objects
		var objects []s3types.ObjectIdentifier
		for _, obj := range result.Contents {
			objects = append(objects, s3types.ObjectIdentifier{Key: obj.Key})
		}

		deleteResult, err := s.S3Client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(bucketName),
			Delete: &s3types.Delete{Objects: objects},
		})
		if err != nil {
			return err
		}

		// Check for per-object errors
		if len(deleteResult.Errors) > 0 {
			var errMsgs []string
			for _, delErr := range deleteResult.Errors {
				errMsgs = append(errMsgs, *delErr.Key+": "+*delErr.Message)
			}
			return fmt.Errorf("failed to delete some objects: %s", strings.Join(errMsgs, "; "))
		}

		if !aws.ToBool(result.IsTruncated) {
			break
		}
		continuationToken = result.NextContinuationToken
	}

	return nil
}
