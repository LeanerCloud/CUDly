package deploy

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	ecrtypes "github.com/aws/aws-sdk-go-v2/service/ecr/types"
	"github.com/aws/aws-sdk-go-v2/service/ecrpublic"
	ecrpublictypes "github.com/aws/aws-sdk-go-v2/service/ecrpublic/types"
)

func TestECRService_EnsureRepository_Exists(t *testing.T) {
	mockClient := &MockECRClient{
		DescribeRepositoriesFunc: func(ctx context.Context, params *ecr.DescribeRepositoriesInput, optFns ...func(*ecr.Options)) (*ecr.DescribeRepositoriesOutput, error) {
			return &ecr.DescribeRepositoriesOutput{
				Repositories: []ecrtypes.Repository{
					{RepositoryName: aws.String("test-repo")},
				},
			}, nil
		},
	}

	service := NewECRService(mockClient, nil, nil)
	uri, err := service.EnsureRepository(context.Background(), "test-repo", "123456789012", "us-east-1")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "123456789012.dkr.ecr.us-east-1.amazonaws.com/test-repo"
	if uri != expected {
		t.Errorf("expected URI %s, got %s", expected, uri)
	}
}

func TestECRService_EnsureRepository_Creates(t *testing.T) {
	createCalled := false
	mockClient := &MockECRClient{
		DescribeRepositoriesFunc: func(ctx context.Context, params *ecr.DescribeRepositoriesInput, optFns ...func(*ecr.Options)) (*ecr.DescribeRepositoriesOutput, error) {
			return nil, errors.New("RepositoryNotFoundException")
		},
		CreateRepositoryFunc: func(ctx context.Context, params *ecr.CreateRepositoryInput, optFns ...func(*ecr.Options)) (*ecr.CreateRepositoryOutput, error) {
			createCalled = true
			if *params.RepositoryName != "test-repo" {
				t.Errorf("expected repo name test-repo, got %s", *params.RepositoryName)
			}
			return &ecr.CreateRepositoryOutput{}, nil
		},
	}

	service := NewECRService(mockClient, nil, nil)
	uri, err := service.EnsureRepository(context.Background(), "test-repo", "123456789012", "us-east-1")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !createCalled {
		t.Error("expected CreateRepository to be called")
	}

	expected := "123456789012.dkr.ecr.us-east-1.amazonaws.com/test-repo"
	if uri != expected {
		t.Errorf("expected URI %s, got %s", expected, uri)
	}
}

func TestECRService_LoginToPublicECR(t *testing.T) {
	token := base64.StdEncoding.EncodeToString([]byte("AWS:testpassword"))
	mockPublicClient := &MockECRPublicClient{
		GetAuthorizationTokenFunc: func(ctx context.Context, params *ecrpublic.GetAuthorizationTokenInput, optFns ...func(*ecrpublic.Options)) (*ecrpublic.GetAuthorizationTokenOutput, error) {
			return &ecrpublic.GetAuthorizationTokenOutput{
				AuthorizationData: &ecrpublictypes.AuthorizationData{
					AuthorizationToken: aws.String(token),
				},
			}, nil
		},
	}

	mockCmdRunner := &MockCommandRunner{}
	service := NewECRService(nil, mockPublicClient, mockCmdRunner)

	err := service.LoginToPublicECR(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify docker login was called
	if len(mockCmdRunner.Commands) != 1 {
		t.Fatalf("expected 1 command, got %d", len(mockCmdRunner.Commands))
	}

	cmd := mockCmdRunner.Commands[0]
	if cmd[0] != "docker" || cmd[1] != "login" {
		t.Errorf("expected docker login command, got %v", cmd)
	}
}

func TestECRService_LoginToECR(t *testing.T) {
	token := base64.StdEncoding.EncodeToString([]byte("AWS:testpassword"))
	mockClient := &MockECRClient{
		GetAuthorizationTokenFunc: func(ctx context.Context, params *ecr.GetAuthorizationTokenInput, optFns ...func(*ecr.Options)) (*ecr.GetAuthorizationTokenOutput, error) {
			return &ecr.GetAuthorizationTokenOutput{
				AuthorizationData: []ecrtypes.AuthorizationData{
					{AuthorizationToken: aws.String(token)},
				},
			}, nil
		},
	}

	mockCmdRunner := &MockCommandRunner{}
	service := NewECRService(mockClient, nil, mockCmdRunner)

	err := service.LoginToECR(context.Background(), "123456789012", "us-east-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify docker login was called with correct registry
	if len(mockCmdRunner.Commands) != 1 {
		t.Fatalf("expected 1 command, got %d", len(mockCmdRunner.Commands))
	}

	cmd := mockCmdRunner.Commands[0]
	if cmd[0] != "docker" || cmd[1] != "login" {
		t.Errorf("expected docker login command, got %v", cmd)
	}
}

func TestDecodeBase64Token(t *testing.T) {
	tests := []struct {
		name        string
		token       string
		expected    string
		expectError bool
	}{
		{
			name:        "valid token",
			token:       base64.StdEncoding.EncodeToString([]byte("AWS:mypassword")),
			expected:    "mypassword",
			expectError: false,
		},
		{
			name:        "invalid base64",
			token:       "not-valid-base64!!!",
			expected:    "",
			expectError: true,
		},
		{
			name:        "missing colon",
			token:       base64.StdEncoding.EncodeToString([]byte("nopassword")),
			expected:    "",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := decodeBase64Token(tt.token)
			if tt.expectError && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if result != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, result)
			}
		})
	}
}

func TestECRService_LoginToPublicECR_AuthError(t *testing.T) {
	mockPublicClient := &MockECRPublicClient{
		GetAuthorizationTokenFunc: func(ctx context.Context, params *ecrpublic.GetAuthorizationTokenInput, optFns ...func(*ecrpublic.Options)) (*ecrpublic.GetAuthorizationTokenOutput, error) {
			return nil, errors.New("auth error")
		},
	}

	service := NewECRService(nil, mockPublicClient, nil)
	err := service.LoginToPublicECR(context.Background())
	if err == nil {
		t.Error("expected error, got nil")
	}
}

func TestECRService_LoginToPublicECR_DecodeError(t *testing.T) {
	mockPublicClient := &MockECRPublicClient{
		GetAuthorizationTokenFunc: func(ctx context.Context, params *ecrpublic.GetAuthorizationTokenInput, optFns ...func(*ecrpublic.Options)) (*ecrpublic.GetAuthorizationTokenOutput, error) {
			return &ecrpublic.GetAuthorizationTokenOutput{
				AuthorizationData: &ecrpublictypes.AuthorizationData{
					AuthorizationToken: aws.String("invalid-base64!!!"),
				},
			}, nil
		},
	}

	service := NewECRService(nil, mockPublicClient, nil)
	err := service.LoginToPublicECR(context.Background())
	if err == nil {
		t.Error("expected error, got nil")
	}
}

func TestECRService_LoginToPublicECR_DockerLoginError(t *testing.T) {
	token := base64.StdEncoding.EncodeToString([]byte("AWS:testpassword"))
	mockPublicClient := &MockECRPublicClient{
		GetAuthorizationTokenFunc: func(ctx context.Context, params *ecrpublic.GetAuthorizationTokenInput, optFns ...func(*ecrpublic.Options)) (*ecrpublic.GetAuthorizationTokenOutput, error) {
			return &ecrpublic.GetAuthorizationTokenOutput{
				AuthorizationData: &ecrpublictypes.AuthorizationData{
					AuthorizationToken: aws.String(token),
				},
			}, nil
		},
	}

	mockCmdRunner := &MockCommandRunner{
		RunWithStdinFunc: func(name string, stdinInput string, args ...string) error {
			return errors.New("docker login failed")
		},
	}
	service := NewECRService(nil, mockPublicClient, mockCmdRunner)

	err := service.LoginToPublicECR(context.Background())
	if err == nil {
		t.Error("expected error, got nil")
	}
}

func TestECRService_LoginToECR_AuthError(t *testing.T) {
	mockClient := &MockECRClient{
		GetAuthorizationTokenFunc: func(ctx context.Context, params *ecr.GetAuthorizationTokenInput, optFns ...func(*ecr.Options)) (*ecr.GetAuthorizationTokenOutput, error) {
			return nil, errors.New("auth error")
		},
	}

	service := NewECRService(mockClient, nil, nil)
	err := service.LoginToECR(context.Background(), "123456789012", "us-east-1")
	if err == nil {
		t.Error("expected error, got nil")
	}
}

func TestECRService_LoginToECR_NoAuthData(t *testing.T) {
	mockClient := &MockECRClient{
		GetAuthorizationTokenFunc: func(ctx context.Context, params *ecr.GetAuthorizationTokenInput, optFns ...func(*ecr.Options)) (*ecr.GetAuthorizationTokenOutput, error) {
			return &ecr.GetAuthorizationTokenOutput{
				AuthorizationData: []ecrtypes.AuthorizationData{},
			}, nil
		},
	}

	service := NewECRService(mockClient, nil, nil)
	err := service.LoginToECR(context.Background(), "123456789012", "us-east-1")
	if err == nil {
		t.Error("expected error, got nil")
	}
}

func TestECRService_LoginToECR_DecodeError(t *testing.T) {
	mockClient := &MockECRClient{
		GetAuthorizationTokenFunc: func(ctx context.Context, params *ecr.GetAuthorizationTokenInput, optFns ...func(*ecr.Options)) (*ecr.GetAuthorizationTokenOutput, error) {
			return &ecr.GetAuthorizationTokenOutput{
				AuthorizationData: []ecrtypes.AuthorizationData{
					{AuthorizationToken: aws.String("invalid-base64!!!")},
				},
			}, nil
		},
	}

	service := NewECRService(mockClient, nil, nil)
	err := service.LoginToECR(context.Background(), "123456789012", "us-east-1")
	if err == nil {
		t.Error("expected error, got nil")
	}
}

func TestECRService_LoginToECR_DockerLoginError(t *testing.T) {
	token := base64.StdEncoding.EncodeToString([]byte("AWS:testpassword"))
	mockClient := &MockECRClient{
		GetAuthorizationTokenFunc: func(ctx context.Context, params *ecr.GetAuthorizationTokenInput, optFns ...func(*ecr.Options)) (*ecr.GetAuthorizationTokenOutput, error) {
			return &ecr.GetAuthorizationTokenOutput{
				AuthorizationData: []ecrtypes.AuthorizationData{
					{AuthorizationToken: aws.String(token)},
				},
			}, nil
		},
	}

	mockCmdRunner := &MockCommandRunner{
		RunWithStdinFunc: func(name string, stdinInput string, args ...string) error {
			return errors.New("docker login failed")
		},
	}
	service := NewECRService(mockClient, nil, mockCmdRunner)

	err := service.LoginToECR(context.Background(), "123456789012", "us-east-1")
	if err == nil {
		t.Error("expected error, got nil")
	}
}

func TestECRService_EnsureRepository_CreateError(t *testing.T) {
	mockClient := &MockECRClient{
		DescribeRepositoriesFunc: func(ctx context.Context, params *ecr.DescribeRepositoriesInput, optFns ...func(*ecr.Options)) (*ecr.DescribeRepositoriesOutput, error) {
			return nil, errors.New("RepositoryNotFoundException")
		},
		CreateRepositoryFunc: func(ctx context.Context, params *ecr.CreateRepositoryInput, optFns ...func(*ecr.Options)) (*ecr.CreateRepositoryOutput, error) {
			return nil, errors.New("create failed")
		},
	}

	service := NewECRService(mockClient, nil, nil)
	_, err := service.EnsureRepository(context.Background(), "test-repo", "123456789012", "us-east-1")
	if err == nil {
		t.Error("expected error, got nil")
	}
}

func TestECRService_LoginToPublicECR_NilAuthData(t *testing.T) {
	mockPublicClient := &MockECRPublicClient{
		GetAuthorizationTokenFunc: func(ctx context.Context, params *ecrpublic.GetAuthorizationTokenInput, optFns ...func(*ecrpublic.Options)) (*ecrpublic.GetAuthorizationTokenOutput, error) {
			return &ecrpublic.GetAuthorizationTokenOutput{
				AuthorizationData: nil,
			}, nil
		},
	}

	service := NewECRService(nil, mockPublicClient, nil)
	err := service.LoginToPublicECR(context.Background())
	if err == nil {
		t.Error("expected error for nil AuthorizationData, got nil")
	}
	if err.Error() != "no authorization data returned from public ECR" {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestECRService_LoginToPublicECR_NilAuthToken(t *testing.T) {
	mockPublicClient := &MockECRPublicClient{
		GetAuthorizationTokenFunc: func(ctx context.Context, params *ecrpublic.GetAuthorizationTokenInput, optFns ...func(*ecrpublic.Options)) (*ecrpublic.GetAuthorizationTokenOutput, error) {
			return &ecrpublic.GetAuthorizationTokenOutput{
				AuthorizationData: &ecrpublictypes.AuthorizationData{
					AuthorizationToken: nil,
				},
			}, nil
		},
	}

	service := NewECRService(nil, mockPublicClient, nil)
	err := service.LoginToPublicECR(context.Background())
	if err == nil {
		t.Error("expected error for nil AuthorizationToken, got nil")
	}
	if err.Error() != "no authorization data returned from public ECR" {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestECRService_LoginToPublicECR_InvalidTokenFormat(t *testing.T) {
	// Valid base64 but no colon in the decoded string
	token := base64.StdEncoding.EncodeToString([]byte("invalidformat"))
	mockPublicClient := &MockECRPublicClient{
		GetAuthorizationTokenFunc: func(ctx context.Context, params *ecrpublic.GetAuthorizationTokenInput, optFns ...func(*ecrpublic.Options)) (*ecrpublic.GetAuthorizationTokenOutput, error) {
			return &ecrpublic.GetAuthorizationTokenOutput{
				AuthorizationData: &ecrpublictypes.AuthorizationData{
					AuthorizationToken: aws.String(token),
				},
			}, nil
		},
	}

	service := NewECRService(nil, mockPublicClient, nil)
	err := service.LoginToPublicECR(context.Background())
	if err == nil {
		t.Error("expected error for invalid token format, got nil")
	}
	if err.Error() != "unexpected token format" {
		t.Errorf("unexpected error message: %v", err)
	}
}
