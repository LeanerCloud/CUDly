package deploy

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	ecrtypes "github.com/aws/aws-sdk-go-v2/service/ecr/types"
	"github.com/aws/aws-sdk-go-v2/service/ecrpublic"
)

// ECRService handles ECR operations.
type ECRService struct {
	Client       ECRClient
	PublicClient ECRPublicClient
	CmdRunner    CommandRunner
}

// NewECRService creates a new ECRService.
func NewECRService(client ECRClient, publicClient ECRPublicClient, cmdRunner CommandRunner) *ECRService {
	return &ECRService{
		Client:       client,
		PublicClient: publicClient,
		CmdRunner:    cmdRunner,
	}
}

// EnsureRepository ensures the ECR repository exists, creating it if necessary.
// Returns the repository URI.
func (s *ECRService) EnsureRepository(ctx context.Context, repoName, accountID, region string) (string, error) {
	uri, err := s.describeRepository(ctx, repoName)
	if err != nil {
		return "", err
	}
	if uri != "" {
		return uri, nil
	}
	return s.createRepository(ctx, repoName, accountID, region)
}

// describeRepository returns the URI of an existing ECR repository, or "" if it doesn't exist.
// Returns an error for any failure other than RepositoryNotFoundException.
func (s *ECRService) describeRepository(ctx context.Context, repoName string) (string, error) {
	out, err := s.Client.DescribeRepositories(ctx, &ecr.DescribeRepositoriesInput{
		RepositoryNames: []string{repoName},
	})
	if err != nil {
		var notFound *ecrtypes.RepositoryNotFoundException
		if errors.As(err, &notFound) {
			return "", nil // repository does not exist yet
		}
		return "", fmt.Errorf("failed to describe ECR repository: %w", err)
	}
	if out != nil && len(out.Repositories) > 0 && out.Repositories[0].RepositoryUri != nil {
		return *out.Repositories[0].RepositoryUri, nil
	}
	return "", nil
}

// createRepository creates an ECR repository and returns its URI.
func (s *ECRService) createRepository(ctx context.Context, repoName, accountID, region string) (string, error) {
	log.Printf("Creating ECR repository: %s", repoName)
	out, err := s.Client.CreateRepository(ctx, &ecr.CreateRepositoryInput{
		RepositoryName: aws.String(repoName),
		ImageScanningConfiguration: &ecrtypes.ImageScanningConfiguration{
			ScanOnPush: true,
		},
	})
	var repoExists *ecrtypes.RepositoryAlreadyExistsException
	if err != nil && !errors.As(err, &repoExists) {
		return "", fmt.Errorf("failed to create ECR repository: %w", err)
	}
	if out != nil && out.Repository != nil && out.Repository.RepositoryUri != nil {
		return *out.Repository.RepositoryUri, nil
	}
	return fmt.Sprintf("%s.dkr.ecr.%s.amazonaws.com/%s", accountID, region, repoName), nil
}

// LoginToPublicECR authenticates to public ECR for pulling base images.
func (s *ECRService) LoginToPublicECR(ctx context.Context) error {
	result, err := s.PublicClient.GetAuthorizationToken(ctx, &ecrpublic.GetAuthorizationTokenInput{})
	if err != nil {
		return fmt.Errorf("failed to get public ECR auth token: %w", err)
	}

	if result.AuthorizationData == nil || result.AuthorizationData.AuthorizationToken == nil {
		return fmt.Errorf("no authorization data returned from public ECR")
	}

	// Decode the base64 token
	tokenBytes, err := base64.StdEncoding.DecodeString(*result.AuthorizationData.AuthorizationToken)
	if err != nil {
		return fmt.Errorf("failed to decode auth token: %w", err)
	}

	// Token format is "AWS:<password>"
	parts := strings.SplitN(string(tokenBytes), ":", 2)
	if len(parts) != 2 {
		return fmt.Errorf("unexpected token format")
	}
	password := parts[1]

	// Login to Docker using the token
	if err := s.CmdRunner.RunWithStdin("docker", password, "login", "--username", "AWS", "--password-stdin", "public.ecr.aws"); err != nil {
		return fmt.Errorf("docker login failed: %w", err)
	}

	return nil
}

// LoginToECR authenticates to private ECR.
func (s *ECRService) LoginToECR(ctx context.Context, accountID, region string) error {
	result, err := s.Client.GetAuthorizationToken(ctx, &ecr.GetAuthorizationTokenInput{})
	if err != nil {
		return fmt.Errorf("failed to get ECR auth token: %w", err)
	}

	if len(result.AuthorizationData) == 0 {
		return fmt.Errorf("no authorization data returned")
	}

	if result.AuthorizationData[0].AuthorizationToken == nil {
		return fmt.Errorf("authorization token is nil in ECR response")
	}

	authToken := *result.AuthorizationData[0].AuthorizationToken
	registryURL := fmt.Sprintf("%s.dkr.ecr.%s.amazonaws.com", accountID, region)

	password, err := decodeBase64Token(authToken)
	if err != nil {
		return fmt.Errorf("failed to decode ECR auth token: %w", err)
	}

	if err := s.CmdRunner.RunWithStdin("docker", password, "login", "--username", "AWS", "--password-stdin", registryURL); err != nil {
		return fmt.Errorf("docker login failed: %w", err)
	}

	return nil
}

// decodeBase64Token decodes a base64-encoded auth token and returns the password.
func decodeBase64Token(token string) (string, error) {
	decoded, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return "", fmt.Errorf("failed to decode base64 token: %w", err)
	}
	parts := strings.SplitN(string(decoded), ":", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid token format: expected 'username:password'")
	}
	return parts[1], nil
}
