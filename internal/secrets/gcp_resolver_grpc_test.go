package secrets

import (
	"context"
	"fmt"
	"net"
	"testing"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// mockSecretManagerServer implements the SecretManagerServiceServer for testing.
type mockSecretManagerServer struct {
	secretmanagerpb.UnimplementedSecretManagerServiceServer

	accessSecretVersionFn func(ctx context.Context, req *secretmanagerpb.AccessSecretVersionRequest) (*secretmanagerpb.AccessSecretVersionResponse, error)
	listSecretsFn         func(ctx context.Context, req *secretmanagerpb.ListSecretsRequest) (*secretmanagerpb.ListSecretsResponse, error)
}

func (s *mockSecretManagerServer) AccessSecretVersion(ctx context.Context, req *secretmanagerpb.AccessSecretVersionRequest) (*secretmanagerpb.AccessSecretVersionResponse, error) {
	if s.accessSecretVersionFn != nil {
		return s.accessSecretVersionFn(ctx, req)
	}
	return nil, status.Errorf(codes.Unimplemented, "not configured")
}

func (s *mockSecretManagerServer) ListSecrets(ctx context.Context, req *secretmanagerpb.ListSecretsRequest) (*secretmanagerpb.ListSecretsResponse, error) {
	if s.listSecretsFn != nil {
		return s.listSecretsFn(ctx, req)
	}
	return nil, status.Errorf(codes.Unimplemented, "not configured")
}

// newTestGCPResolver creates a GCPResolver backed by a mock gRPC server.
func newTestGCPResolver(t *testing.T, mock *mockSecretManagerServer) (*GCPResolver, func()) {
	t.Helper()

	// Start a gRPC server on a random port
	lis, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)

	grpcServer := grpc.NewServer()
	secretmanagerpb.RegisterSecretManagerServiceServer(grpcServer, mock)
	go grpcServer.Serve(lis)

	// Create a gRPC client connection to the mock server
	conn, err := grpc.NewClient(
		lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)

	// Create the GCP secret manager client using the mock connection
	client, err := secretmanager.NewClient(context.Background(),
		option.WithGRPCConn(conn),
		option.WithoutAuthentication(),
	)
	require.NoError(t, err)

	resolver := &GCPResolver{
		client:    client,
		projectID: "test-project",
	}

	cleanup := func() {
		client.Close()
		grpcServer.Stop()
		lis.Close()
	}

	return resolver, cleanup
}

func TestGCPResolverReal_GetSecret_Success(t *testing.T) {
	mock := &mockSecretManagerServer{
		accessSecretVersionFn: func(ctx context.Context, req *secretmanagerpb.AccessSecretVersionRequest) (*secretmanagerpb.AccessSecretVersionResponse, error) {
			assert.Equal(t, "projects/test-project/secrets/my-secret/versions/latest", req.Name)
			return &secretmanagerpb.AccessSecretVersionResponse{
				Name: req.Name,
				Payload: &secretmanagerpb.SecretPayload{
					Data: []byte("my-gcp-secret-value"),
				},
			}, nil
		},
	}

	resolver, cleanup := newTestGCPResolver(t, mock)
	defer cleanup()

	ctx := context.Background()
	result, err := resolver.GetSecret(ctx, "my-secret")

	require.NoError(t, err)
	assert.Equal(t, "my-gcp-secret-value", result)
}

func TestGCPResolverReal_GetSecret_Error(t *testing.T) {
	mock := &mockSecretManagerServer{
		accessSecretVersionFn: func(ctx context.Context, req *secretmanagerpb.AccessSecretVersionRequest) (*secretmanagerpb.AccessSecretVersionResponse, error) {
			return nil, status.Errorf(codes.NotFound, "secret not found")
		},
	}

	resolver, cleanup := newTestGCPResolver(t, mock)
	defer cleanup()

	ctx := context.Background()
	result, err := resolver.GetSecret(ctx, "non-existent")

	require.Error(t, err)
	assert.Empty(t, result)
	assert.Contains(t, err.Error(), "failed to access secret")
}

func TestGCPResolverReal_GetSecretJSON_Success(t *testing.T) {
	mock := &mockSecretManagerServer{
		accessSecretVersionFn: func(ctx context.Context, req *secretmanagerpb.AccessSecretVersionRequest) (*secretmanagerpb.AccessSecretVersionResponse, error) {
			return &secretmanagerpb.AccessSecretVersionResponse{
				Name: req.Name,
				Payload: &secretmanagerpb.SecretPayload{
					Data: []byte(`{"username":"gcp-admin","password":"gcp-pass","port":5432}`),
				},
			}, nil
		},
	}

	resolver, cleanup := newTestGCPResolver(t, mock)
	defer cleanup()

	ctx := context.Background()
	result, err := resolver.GetSecretJSON(ctx, "json-secret")

	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "gcp-admin", result["username"])
	assert.Equal(t, "gcp-pass", result["password"])
	assert.Equal(t, float64(5432), result["port"])
}

func TestGCPResolverReal_GetSecretJSON_InvalidJSON(t *testing.T) {
	mock := &mockSecretManagerServer{
		accessSecretVersionFn: func(ctx context.Context, req *secretmanagerpb.AccessSecretVersionRequest) (*secretmanagerpb.AccessSecretVersionResponse, error) {
			return &secretmanagerpb.AccessSecretVersionResponse{
				Name: req.Name,
				Payload: &secretmanagerpb.SecretPayload{
					Data: []byte("not-valid-json"),
				},
			}, nil
		},
	}

	resolver, cleanup := newTestGCPResolver(t, mock)
	defer cleanup()

	ctx := context.Background()
	result, err := resolver.GetSecretJSON(ctx, "invalid-json")

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to parse secret as JSON")
}

func TestGCPResolverReal_GetSecretJSON_GetSecretError(t *testing.T) {
	mock := &mockSecretManagerServer{
		accessSecretVersionFn: func(ctx context.Context, req *secretmanagerpb.AccessSecretVersionRequest) (*secretmanagerpb.AccessSecretVersionResponse, error) {
			return nil, status.Errorf(codes.PermissionDenied, "access denied")
		},
	}

	resolver, cleanup := newTestGCPResolver(t, mock)
	defer cleanup()

	ctx := context.Background()
	result, err := resolver.GetSecretJSON(ctx, "forbidden-secret")

	require.Error(t, err)
	assert.Nil(t, result)
}

func TestGCPResolverReal_ListSecrets_Success(t *testing.T) {
	mock := &mockSecretManagerServer{
		listSecretsFn: func(ctx context.Context, req *secretmanagerpb.ListSecretsRequest) (*secretmanagerpb.ListSecretsResponse, error) {
			assert.Equal(t, "projects/test-project", req.Parent)
			return &secretmanagerpb.ListSecretsResponse{
				Secrets: []*secretmanagerpb.Secret{
					{Name: "projects/test-project/secrets/secret-1"},
					{Name: "projects/test-project/secrets/secret-2"},
					{Name: "projects/test-project/secrets/secret-3"},
				},
			}, nil
		},
	}

	resolver, cleanup := newTestGCPResolver(t, mock)
	defer cleanup()

	ctx := context.Background()
	result, err := resolver.ListSecrets(ctx, "")

	require.NoError(t, err)
	assert.Len(t, result, 3)
	assert.Equal(t, "secret-1", result[0])
	assert.Equal(t, "secret-2", result[1])
	assert.Equal(t, "secret-3", result[2])
}

func TestGCPResolverReal_ListSecrets_WithFilter(t *testing.T) {
	mock := &mockSecretManagerServer{
		listSecretsFn: func(ctx context.Context, req *secretmanagerpb.ListSecretsRequest) (*secretmanagerpb.ListSecretsResponse, error) {
			assert.Equal(t, "labels.env=prod", req.Filter)
			return &secretmanagerpb.ListSecretsResponse{
				Secrets: []*secretmanagerpb.Secret{
					{Name: "projects/test-project/secrets/prod-secret-1"},
				},
			}, nil
		},
	}

	resolver, cleanup := newTestGCPResolver(t, mock)
	defer cleanup()

	ctx := context.Background()
	result, err := resolver.ListSecrets(ctx, "labels.env=prod")

	require.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Contains(t, result[0], "prod-secret-1")
}

func TestGCPResolverReal_ListSecrets_Error(t *testing.T) {
	mock := &mockSecretManagerServer{
		listSecretsFn: func(ctx context.Context, req *secretmanagerpb.ListSecretsRequest) (*secretmanagerpb.ListSecretsResponse, error) {
			return nil, status.Errorf(codes.PermissionDenied, "access denied")
		},
	}

	resolver, cleanup := newTestGCPResolver(t, mock)
	defer cleanup()

	ctx := context.Background()
	result, err := resolver.ListSecrets(ctx, "")

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to list secrets")
}

func TestGCPResolverReal_ListSecrets_Empty(t *testing.T) {
	mock := &mockSecretManagerServer{
		listSecretsFn: func(ctx context.Context, req *secretmanagerpb.ListSecretsRequest) (*secretmanagerpb.ListSecretsResponse, error) {
			return &secretmanagerpb.ListSecretsResponse{
				Secrets: []*secretmanagerpb.Secret{},
			}, nil
		},
	}

	resolver, cleanup := newTestGCPResolver(t, mock)
	defer cleanup()

	ctx := context.Background()
	result, err := resolver.ListSecrets(ctx, "")

	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestGCPResolverReal_ListSecrets_Pagination(t *testing.T) {
	callCount := 0
	mock := &mockSecretManagerServer{
		listSecretsFn: func(ctx context.Context, req *secretmanagerpb.ListSecretsRequest) (*secretmanagerpb.ListSecretsResponse, error) {
			callCount++
			if callCount == 1 {
				return &secretmanagerpb.ListSecretsResponse{
					Secrets: []*secretmanagerpb.Secret{
						{Name: "projects/test-project/secrets/secret-1"},
						{Name: "projects/test-project/secrets/secret-2"},
					},
					NextPageToken: "page2",
				}, nil
			}
			return &secretmanagerpb.ListSecretsResponse{
				Secrets: []*secretmanagerpb.Secret{
					{Name: "projects/test-project/secrets/secret-3"},
				},
			}, nil
		},
	}

	resolver, cleanup := newTestGCPResolver(t, mock)
	defer cleanup()

	ctx := context.Background()
	result, err := resolver.ListSecrets(ctx, "")

	require.NoError(t, err)
	assert.Len(t, result, 3)
}

func TestGCPResolverReal_Close(t *testing.T) {
	mock := &mockSecretManagerServer{}
	resolver, cleanup := newTestGCPResolver(t, mock)
	defer cleanup()

	err := resolver.Close()
	assert.NoError(t, err)
}

func TestGCPResolverReal_SecretNameFormat(t *testing.T) {
	mock := &mockSecretManagerServer{
		accessSecretVersionFn: func(ctx context.Context, req *secretmanagerpb.AccessSecretVersionRequest) (*secretmanagerpb.AccessSecretVersionResponse, error) {
			expectedName := fmt.Sprintf("projects/test-project/secrets/%s/versions/latest", "my-special-secret")
			assert.Equal(t, expectedName, req.Name)
			return &secretmanagerpb.AccessSecretVersionResponse{
				Name: req.Name,
				Payload: &secretmanagerpb.SecretPayload{
					Data: []byte("value"),
				},
			}, nil
		},
	}

	resolver, cleanup := newTestGCPResolver(t, mock)
	defer cleanup()

	ctx := context.Background()
	result, err := resolver.GetSecret(ctx, "my-special-secret")

	require.NoError(t, err)
	assert.Equal(t, "value", result)
}
