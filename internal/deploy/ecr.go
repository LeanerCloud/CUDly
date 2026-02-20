package deploy

import (
	"context"
	"encoding/base64"
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
	// Check if repository exists
	_, err := s.Client.DescribeRepositories(ctx, &ecr.DescribeRepositoriesInput{
		RepositoryNames: []string{repoName},
	})

	if err != nil {
		// Create repository if it doesn't exist
		log.Printf("Creating ECR repository: %s", repoName)
		_, err = s.Client.CreateRepository(ctx, &ecr.CreateRepositoryInput{
			RepositoryName: aws.String(repoName),
			ImageScanningConfiguration: &ecrtypes.ImageScanningConfiguration{
				ScanOnPush: true,
			},
		})
		if err != nil && !strings.Contains(err.Error(), "RepositoryAlreadyExistsException") {
			return "", fmt.Errorf("failed to create ECR repository: %w", err)
		}
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
