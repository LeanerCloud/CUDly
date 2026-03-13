package secrets

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
)

// AWSResolver implements Resolver for AWS Secrets Manager
type AWSResolver struct {
	client *secretsmanager.Client
	region string
}

// NewAWSResolver creates a new AWS Secrets Manager resolver
func NewAWSResolver(ctx context.Context, region string) (*AWSResolver, error) {
	// Load AWS config
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	// Create Secrets Manager client
	client := secretsmanager.NewFromConfig(cfg)

	return &AWSResolver{
		client: client,
		region: region,
	}, nil
}

// GetSecret retrieves a secret from AWS Secrets Manager
func (r *AWSResolver) GetSecret(ctx context.Context, secretID string) (string, error) {
	input := &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(secretID),
	}

	result, err := r.client.GetSecretValue(ctx, input)
	if err != nil {
		return "", fmt.Errorf("failed to get secret %s: %w", secretID, err)
	}

	// Return the secret string
	if result.SecretString != nil {
		return *result.SecretString, nil
	}

	return "", fmt.Errorf("secret %s has no string value (binary secrets not supported by this resolver)", secretID)
}

// PutSecret creates or updates a secret value in AWS Secrets Manager
func (r *AWSResolver) PutSecret(ctx context.Context, secretID string, value string) error {
	input := &secretsmanager.PutSecretValueInput{
		SecretId:     aws.String(secretID),
		SecretString: aws.String(value),
	}

	_, err := r.client.PutSecretValue(ctx, input)
	if err != nil {
		return fmt.Errorf("failed to put secret %s: %w", secretID, err)
	}

	return nil
}

// GetSecretJSON retrieves and parses a JSON secret
func (r *AWSResolver) GetSecretJSON(ctx context.Context, secretID string) (map[string]any, error) {
	secretString, err := r.GetSecret(ctx, secretID)
	if err != nil {
		return nil, err
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(secretString), &result); err != nil {
		return nil, fmt.Errorf("failed to parse secret as JSON: %w", err)
	}

	return result, nil
}

// ListSecrets lists secrets in AWS Secrets Manager
func (r *AWSResolver) ListSecrets(ctx context.Context, filter string) ([]string, error) {
	input := &secretsmanager.ListSecretsInput{}

	// Add filter if provided
	if filter != "" {
		input.Filters = []types.Filter{
			{
				Key:    types.FilterNameStringTypeName,
				Values: []string{filter},
			},
		}
	}

	result, err := r.client.ListSecrets(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to list secrets: %w", err)
	}

	secrets := make([]string, 0, len(result.SecretList))
	for _, secret := range result.SecretList {
		if secret.Name != nil {
			secrets = append(secrets, *secret.Name)
		}
	}

	return secrets, nil
}

// Close cleans up resources (no-op for AWS)
func (r *AWSResolver) Close() error {
	return nil
}
