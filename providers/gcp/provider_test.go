package gcp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"cloud.google.com/go/compute/apiv1/computepb"
	"cloud.google.com/go/resourcemanager/apiv3/resourcemanagerpb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/provider"
)

// MockProjectsClient mocks the ProjectsClient interface
type MockProjectsClient struct {
	project *resourcemanagerpb.Project
	err     error
	closed  bool
}

func (m *MockProjectsClient) GetProject(ctx context.Context, req *resourcemanagerpb.GetProjectRequest) (*resourcemanagerpb.Project, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.project, nil
}

func (m *MockProjectsClient) Close() error {
	m.closed = true
	return nil
}

// MockRegionsClient mocks the RegionsClient interface
type MockRegionsClient struct {
	regions []*computepb.Region
	err     error
	closed  bool
}

func (m *MockRegionsClient) List(ctx context.Context, req *computepb.ListRegionsRequest) RegionsIterator {
	return &MockRegionsIterator{regions: m.regions, err: m.err}
}

func (m *MockRegionsClient) Close() error {
	m.closed = true
	return nil
}

// MockRegionsIterator mocks the RegionsIterator interface
type MockRegionsIterator struct {
	regions []*computepb.Region
	index   int
	err     error
}

func (m *MockRegionsIterator) Next() (*computepb.Region, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.index >= len(m.regions) {
		return nil, iterator.Done
	}
	r := m.regions[m.index]
	m.index++
	return r, nil
}

// MockResourceManagerService mocks the ResourceManagerService interface
type MockResourceManagerService struct {
	projects []*cloudresourcemanager.Project
	err      error
}

func (m *MockResourceManagerService) ListProjects(ctx context.Context) ([]*cloudresourcemanager.Project, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.projects, nil
}

// TestFindActiveProjectInPage covers the page callback used by
// `getDefaultProject`'s `Pages()` walk. The Pages() machinery itself is
// well-tested in the cloudresourcemanager SDK; what matters here is that
// the callback (a) finds the first ACTIVE project on a page, (b) returns
// the sentinel to short-circuit further pages, and (c) leaves *out
// untouched on pages with no ACTIVE projects so the walk continues.
//
// This pins the pagination contract documented in the function godoc
// (related issue: 11_gcp_provider.md LOW "Missing Test Coverage for
// getDefaultProject Pagination").
func TestFindActiveProjectInPage(t *testing.T) {
	t.Run("page with one ACTIVE project — sets out and short-circuits", func(t *testing.T) {
		var out string
		err := findActiveProjectInPage(&out, &cloudresourcemanager.ListProjectsResponse{
			Projects: []*cloudresourcemanager.Project{
				{ProjectId: "proj-active", LifecycleState: "ACTIVE"},
			},
		})
		require.ErrorIs(t, err, errStopProjectPagination)
		assert.Equal(t, "proj-active", out)
	})

	t.Run("page with no ACTIVE projects — returns nil, leaves out untouched", func(t *testing.T) {
		var out string
		err := findActiveProjectInPage(&out, &cloudresourcemanager.ListProjectsResponse{
			Projects: []*cloudresourcemanager.Project{
				{ProjectId: "proj-pending", LifecycleState: "DELETE_REQUESTED"},
				{ProjectId: "proj-deleted", LifecycleState: "DELETE_IN_PROGRESS"},
			},
		})
		require.NoError(t, err)
		assert.Empty(t, out)
	})

	t.Run("empty page — returns nil, leaves out untouched", func(t *testing.T) {
		var out string
		err := findActiveProjectInPage(&out, &cloudresourcemanager.ListProjectsResponse{})
		require.NoError(t, err)
		assert.Empty(t, out)
	})

	t.Run("first ACTIVE wins, later projects on same page are not inspected", func(t *testing.T) {
		var out string
		err := findActiveProjectInPage(&out, &cloudresourcemanager.ListProjectsResponse{
			Projects: []*cloudresourcemanager.Project{
				{ProjectId: "proj-1", LifecycleState: "ACTIVE"},
				{ProjectId: "proj-2", LifecycleState: "ACTIVE"},
			},
		})
		require.ErrorIs(t, err, errStopProjectPagination)
		assert.Equal(t, "proj-1", out)
	})

	t.Run("multi-page simulation — page 1 has none, page 2 has ACTIVE", func(t *testing.T) {
		// Simulates Pages() invoking the callback per page. The callback
		// holds onto the same *out across calls; finding ACTIVE on page 2
		// must populate it correctly.
		var out string

		err := findActiveProjectInPage(&out, &cloudresourcemanager.ListProjectsResponse{
			Projects: []*cloudresourcemanager.Project{
				{ProjectId: "p1-pending", LifecycleState: "DELETE_REQUESTED"},
			},
		})
		require.NoError(t, err)
		assert.Empty(t, out, "page 1 has no ACTIVE projects, out must stay empty")

		err = findActiveProjectInPage(&out, &cloudresourcemanager.ListProjectsResponse{
			Projects: []*cloudresourcemanager.Project{
				{ProjectId: "p2-active", LifecycleState: "ACTIVE"},
			},
		})
		require.ErrorIs(t, err, errStopProjectPagination)
		assert.Equal(t, "p2-active", out, "page 2 has the ACTIVE project, out must be set")
	})
}

// TestGetDefaultProject_NoActiveProjects exercises the "lister returned
// pages but none contained an ACTIVE project" error path. The per-page
// callback has unit coverage (TestFindActiveProjectInPage), but the
// end-to-end "no active GCP projects found" error was previously only
// covered by the ADC-dependent path, which doesn't run in CI.
func TestGetDefaultProject_NoActiveProjects(t *testing.T) {
	originalLister := listProjectsForDefault
	t.Cleanup(func() { listProjectsForDefault = originalLister })

	listProjectsForDefault = func(ctx context.Context, opts []option.ClientOption, cb func(*cloudresourcemanager.ListProjectsResponse) error) error {
		return cb(&cloudresourcemanager.ListProjectsResponse{
			Projects: []*cloudresourcemanager.Project{
				{ProjectId: "p1", LifecycleState: "DELETE_REQUESTED"},
				{ProjectId: "p2", LifecycleState: "DELETE_IN_PROGRESS"},
			},
		})
	}

	id, err := getDefaultProject(context.Background())
	require.Error(t, err)
	assert.Equal(t, "", id)
	assert.Contains(t, err.Error(), "no active GCP projects found")
}

// TestGetDefaultProject_ListerError verifies that errors from the underlying
// lister (other than the internal errStopProjectPagination sentinel) are
// wrapped and returned, not swallowed.
func TestGetDefaultProject_ListerError(t *testing.T) {
	originalLister := listProjectsForDefault
	t.Cleanup(func() { listProjectsForDefault = originalLister })

	listProjectsForDefault = func(ctx context.Context, opts []option.ClientOption, cb func(*cloudresourcemanager.ListProjectsResponse) error) error {
		return errors.New("cloudresourcemanager: transient failure")
	}

	id, err := getDefaultProject(context.Background())
	require.Error(t, err)
	assert.Equal(t, "", id)
	assert.Contains(t, err.Error(), "failed to list projects")
	assert.Contains(t, err.Error(), "cloudresourcemanager: transient failure")
}

// TestNewProvider_ProjectIDResolution verifies the precedence chain when
// resolving the project ID: typed GCPProjectID > deprecated Profile. The
// "fall through to ADC" branch is exercised separately because it requires
// ambient credentials.
func TestNewProvider_ProjectIDResolution(t *testing.T) {
	tests := []struct {
		name     string
		config   *provider.ProviderConfig
		expected string
	}{
		{
			name: "Typed GCPProjectID takes precedence over deprecated Profile",
			config: &provider.ProviderConfig{
				GCPProjectID: "typed-project",
				Profile:      "deprecated-project",
			},
			expected: "typed-project",
		},
		{
			name: "Typed GCPProjectID alone (no Profile fallback needed)",
			config: &provider.ProviderConfig{
				GCPProjectID: "only-typed",
			},
			expected: "only-typed",
		},
		{
			name: "Deprecated Profile is honoured when typed field is empty",
			config: &provider.ProviderConfig{
				Profile: "legacy-project",
			},
			expected: "legacy-project",
		},
		{
			name:     "Nil config resolves to empty (caller falls through to ADC)",
			config:   nil,
			expected: "",
		},
		{
			name:     "Empty config resolves to empty (caller falls through to ADC)",
			config:   &provider.ProviderConfig{},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, resolveGCPProjectID(tt.config))
		})
	}
}

func TestNewProviderWithProject(t *testing.T) {
	ctx := context.Background()
	provider := NewProviderWithProject(ctx, "test-project")

	require.NotNil(t, provider)
	assert.Equal(t, "test-project", provider.projectID)
	assert.Equal(t, ctx, provider.ctx)
	// clientOpts is nil when no options are passed (not an error - it's expected)
}

func TestGCPProvider_Name(t *testing.T) {
	provider := &GCPProvider{}
	assert.Equal(t, "gcp", provider.Name())
}

func TestGCPProvider_DisplayName(t *testing.T) {
	provider := &GCPProvider{}
	assert.Equal(t, "Google Cloud Platform", provider.DisplayName())
}

func TestGCPProvider_GetDefaultRegion(t *testing.T) {
	provider := &GCPProvider{}
	// GCP defaults to us-central1
	assert.Equal(t, "us-central1", provider.GetDefaultRegion())
}

func TestGCPProvider_GetSupportedServices(t *testing.T) {
	provider := &GCPProvider{}
	services := provider.GetSupportedServices()

	require.NotEmpty(t, services)
	assert.Contains(t, services, common.ServiceCompute)
	assert.Contains(t, services, common.ServiceRelationalDB)
}

func TestGCPProvider_GetServiceClient_UnsupportedService(t *testing.T) {
	ctx := context.Background()
	provider := NewProviderWithProject(ctx, "test-project")

	// Use a service type that is genuinely not in the GCP provider's switch
	// statement. The previous version of this test used ServiceCache, which
	// is now supported (Memorystore) — see issue #251.
	_, err := provider.GetServiceClient(ctx, common.ServiceType("unsupported-service-xyz"), "us-central1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported service type")
}

func TestGCPProvider_GetServiceClient_ServiceCache(t *testing.T) {
	ctx := context.Background()
	provider := NewProviderWithProject(ctx, "test-project")

	// ServiceCache is supported via Memorystore — verify NewClient succeeds
	// (it only allocates a struct, no network call).
	client, err := provider.GetServiceClient(ctx, common.ServiceCache, "us-central1")
	require.NoError(t, err)
	require.NotNil(t, client)
}

func TestGCPProvider_GetRecommendationsClient(t *testing.T) {
	ctx := context.Background()
	provider := NewProviderWithProject(ctx, "test-project")

	client, err := provider.GetRecommendationsClient(ctx)
	require.NoError(t, err)
	require.NotNil(t, client)

	// Verify it's the right type
	adapter, ok := client.(*RecommendationsClientAdapter)
	assert.True(t, ok)
	assert.Equal(t, "test-project", adapter.projectID)
}

func TestGCPProvider_Fields(t *testing.T) {
	ctx := context.Background()
	provider := NewProviderWithProject(ctx, "my-gcp-project")

	assert.Equal(t, "my-gcp-project", provider.projectID)
	assert.Equal(t, ctx, provider.ctx)
	assert.Empty(t, provider.clientOpts)
}

func TestNewProviderWithProject_WithEmptyProject(t *testing.T) {
	ctx := context.Background()
	provider := NewProviderWithProject(ctx, "")

	require.NotNil(t, provider)
	assert.Equal(t, "", provider.projectID)
}

func TestGCPProvider_GetServiceClient_Compute(t *testing.T) {
	ctx := context.Background()
	p := NewProviderWithProject(ctx, "test-project")

	// GetServiceClient creates client but may succeed even without credentials
	// The error would occur when actually using the client
	client, err := p.GetServiceClient(ctx, common.ServiceCompute, "us-central1")
	// May succeed in creation - tests the branch coverage
	if err == nil {
		require.NotNil(t, client)
		assert.Equal(t, common.ServiceCompute, client.GetServiceType())
		assert.Equal(t, "us-central1", client.GetRegion())
	}
}

func TestGCPProvider_GetServiceClient_RelationalDB(t *testing.T) {
	ctx := context.Background()
	p := NewProviderWithProject(ctx, "test-project")

	// GetServiceClient creates client but may succeed even without credentials
	// The error would occur when actually using the client
	client, err := p.GetServiceClient(ctx, common.ServiceRelationalDB, "us-central1")
	// May succeed in creation - tests the branch coverage
	if err == nil {
		require.NotNil(t, client)
		assert.Equal(t, common.ServiceRelationalDB, client.GetServiceType())
		assert.Equal(t, "us-central1", client.GetRegion())
	}
}

func TestNewProvider_WithConfig(t *testing.T) {
	// Test NewProvider with a config containing a project ID
	config := &provider.ProviderConfig{
		Profile: "test-project-id",
	}

	p, err := NewProvider(config)
	// Error is expected since we don't have real GCP credentials
	// but the function should handle it gracefully
	if err != nil {
		// Expected - no GCP credentials
		assert.Contains(t, err.Error(), "failed to get default GCP project")
	} else {
		require.NotNil(t, p)
		assert.Equal(t, "test-project-id", p.projectID)
	}
}

func TestNewProvider_NilConfig(t *testing.T) {
	// Test NewProvider with nil config
	_, err := NewProvider(nil)
	// Error is expected since we need to detect the default project
	// which requires GCP credentials
	if err != nil {
		assert.Contains(t, err.Error(), "failed to get default GCP project")
	}
}

func TestGCPProvider_GetCredentials_WithEnvVar(t *testing.T) {
	// GOOGLE_APPLICATION_CREDENTIALS set -> source is File, no network call.
	clearGCPCredEnv(t)
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "/path/to/creds.json")

	p := &GCPProvider{projectID: "test-project"}

	creds, err := p.GetCredentials()
	require.NoError(t, err)
	baseCreds, ok := creds.(*provider.BaseCredentials)
	require.True(t, ok)
	assert.Equal(t, provider.CredentialSourceFile, baseCreds.Source)
}

func TestGCPProvider_GetCredentials_ADCFileSource(t *testing.T) {
	// gcloud ADC file present (via CLOUDSDK_CONFIG) -> source is CLI, no env var.
	clearGCPCredEnv(t)
	cfgDir := t.TempDir()
	t.Setenv("CLOUDSDK_CONFIG", cfgDir)
	adcPath := filepath.Join(cfgDir, "application_default_credentials.json")
	require.NoError(t, os.WriteFile(adcPath, []byte(`{"type":"authorized_user"}`), 0o600))

	p := &GCPProvider{projectID: "test-project"}

	creds, err := p.GetCredentials()
	require.NoError(t, err)
	baseCreds, ok := creds.(*provider.BaseCredentials)
	require.True(t, ok)
	assert.Equal(t, provider.CredentialSourceCLI, baseCreds.Source)
}

func TestGCPProvider_SetterMethods(t *testing.T) {
	ctx := context.Background()
	p := NewProviderWithProject(ctx, "test-project")

	// Test SetProjectsClient
	mockProjects := &MockProjectsClient{}
	p.SetProjectsClient(mockProjects)
	assert.Equal(t, mockProjects, p.projectsClient)

	// Test SetRegionsClient
	mockRegions := &MockRegionsClient{}
	p.SetRegionsClient(mockRegions)
	assert.Equal(t, mockRegions, p.regionsClient)

	// Test SetResourceManagerService
	mockRM := &MockResourceManagerService{}
	p.SetResourceManagerService(mockRM)
	assert.Equal(t, mockRM, p.resourceManagerService)
}

func TestGCPProvider_IsConfigured_WithMock(t *testing.T) {
	ctx := context.Background()
	p := NewProviderWithProject(ctx, "test-project")

	mockClient := &MockProjectsClient{
		project: &resourcemanagerpb.Project{
			Name:  "projects/test-project",
			State: resourcemanagerpb.Project_ACTIVE,
		},
	}
	p.SetProjectsClient(mockClient)

	result := p.IsConfigured()
	assert.True(t, result)
	// Injected clients are not closed by IsConfigured — the injector owns the
	// lifecycle. See the IsConfigured godoc (issue #251).
	assert.False(t, mockClient.closed)
}

func TestGCPProvider_IsConfigured_Error(t *testing.T) {
	ctx := context.Background()
	p := NewProviderWithProject(ctx, "test-project")

	mockClient := &MockProjectsClient{
		err: errors.New("API error"),
	}
	p.SetProjectsClient(mockClient)

	result := p.IsConfigured()
	assert.False(t, result)
}

func TestGCPProvider_ValidateCredentials_WithMock(t *testing.T) {
	ctx := context.Background()
	p := NewProviderWithProject(ctx, "test-project")

	mockClient := &MockProjectsClient{
		project: &resourcemanagerpb.Project{
			Name:  "projects/test-project",
			State: resourcemanagerpb.Project_ACTIVE,
		},
	}
	p.SetProjectsClient(mockClient)

	err := p.ValidateCredentials(ctx)
	assert.NoError(t, err)
	// Injected clients are not closed by ValidateCredentials — the injector
	// owns the lifecycle. See the ValidateCredentials godoc (issue #251).
	assert.False(t, mockClient.closed)
}

func TestGCPProvider_ValidateCredentials_Error(t *testing.T) {
	ctx := context.Background()
	p := NewProviderWithProject(ctx, "test-project")

	mockClient := &MockProjectsClient{
		err: errors.New("API error"),
	}
	p.SetProjectsClient(mockClient)

	err := p.ValidateCredentials(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get project")
}

func TestGCPProvider_ValidateCredentials_InactiveProject(t *testing.T) {
	ctx := context.Background()
	p := NewProviderWithProject(ctx, "test-project")

	mockClient := &MockProjectsClient{
		project: &resourcemanagerpb.Project{
			Name:  "projects/test-project",
			State: resourcemanagerpb.Project_DELETE_REQUESTED,
		},
	}
	p.SetProjectsClient(mockClient)

	err := p.ValidateCredentials(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "is not active")
}

func TestGCPProvider_GetAccounts_WithMock(t *testing.T) {
	ctx := context.Background()
	p := NewProviderWithProject(ctx, "test-project")

	mockService := &MockResourceManagerService{
		projects: []*cloudresourcemanager.Project{
			{
				ProjectId:      "project-1",
				Name:           "Project 1",
				LifecycleState: "ACTIVE",
			},
			{
				ProjectId:      "project-2",
				Name:           "Project 2",
				LifecycleState: "ACTIVE",
			},
			{
				ProjectId:      "project-deleted",
				Name:           "Deleted Project",
				LifecycleState: "DELETE_REQUESTED",
			},
		},
	}
	p.SetResourceManagerService(mockService)

	accounts, err := p.GetAccounts(ctx)
	require.NoError(t, err)
	assert.Len(t, accounts, 2)
	assert.Equal(t, "project-1", accounts[0].ID)
	assert.Equal(t, "Project 1", accounts[0].Name)
	assert.Equal(t, "project-2", accounts[1].ID)
}

func TestGCPProvider_GetAccounts_Empty(t *testing.T) {
	ctx := context.Background()
	p := NewProviderWithProject(ctx, "default-project")

	mockService := &MockResourceManagerService{
		projects: []*cloudresourcemanager.Project{},
	}
	p.SetResourceManagerService(mockService)

	accounts, err := p.GetAccounts(ctx)
	require.NoError(t, err)
	// Should return the default project when no projects found
	assert.Len(t, accounts, 1)
	assert.Equal(t, "default-project", accounts[0].ID)
}

func TestGCPProvider_GetAccounts_Error(t *testing.T) {
	ctx := context.Background()
	p := NewProviderWithProject(ctx, "test-project")

	mockService := &MockResourceManagerService{
		err: errors.New("API error"),
	}
	p.SetResourceManagerService(mockService)

	_, err := p.GetAccounts(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to list projects")
}

func TestGCPProvider_GetRegions_WithMock(t *testing.T) {
	ctx := context.Background()
	p := NewProviderWithProject(ctx, "test-project")

	upStatus := "UP"
	name1, name2 := "us-central1", "us-east1"
	desc1, desc2 := "Iowa", "South Carolina"

	mockClient := &MockRegionsClient{
		regions: []*computepb.Region{
			{
				Name:        &name1,
				Description: &desc1,
				Status:      &upStatus,
			},
			{
				Name:        &name2,
				Description: &desc2,
				Status:      &upStatus,
			},
		},
	}
	p.SetRegionsClient(mockClient)

	regions, err := p.GetRegions(ctx)
	require.NoError(t, err)
	assert.Len(t, regions, 2)
	assert.Equal(t, "us-central1", regions[0].ID)
	assert.Equal(t, "Iowa", regions[0].DisplayName)
	assert.Equal(t, "us-east1", regions[1].ID)
	assert.True(t, mockClient.closed)
}

func TestGCPProvider_GetRegions_WithoutDescription(t *testing.T) {
	ctx := context.Background()
	p := NewProviderWithProject(ctx, "test-project")

	upStatus := "UP"
	name := "us-central1"

	mockClient := &MockRegionsClient{
		regions: []*computepb.Region{
			{
				Name:        &name,
				Description: nil, // No description
				Status:      &upStatus,
			},
		},
	}
	p.SetRegionsClient(mockClient)

	regions, err := p.GetRegions(ctx)
	require.NoError(t, err)
	assert.Len(t, regions, 1)
	assert.Equal(t, "us-central1", regions[0].ID)
	assert.Equal(t, "us-central1", regions[0].DisplayName) // Should use name as fallback
}

func TestGCPProvider_GetRegions_FilterDownRegions(t *testing.T) {
	ctx := context.Background()
	p := NewProviderWithProject(ctx, "test-project")

	upStatus := "UP"
	downStatus := "DOWN"
	name1, name2 := "us-central1", "us-down1"

	mockClient := &MockRegionsClient{
		regions: []*computepb.Region{
			{
				Name:   &name1,
				Status: &upStatus,
			},
			{
				Name:   &name2,
				Status: &downStatus,
			},
		},
	}
	p.SetRegionsClient(mockClient)

	regions, err := p.GetRegions(ctx)
	require.NoError(t, err)
	// Should only return UP regions
	assert.Len(t, regions, 1)
	assert.Equal(t, "us-central1", regions[0].ID)
}

func TestGCPProvider_GetRegions_Empty(t *testing.T) {
	ctx := context.Background()
	p := NewProviderWithProject(ctx, "test-project")

	mockClient := &MockRegionsClient{
		regions: []*computepb.Region{},
	}
	p.SetRegionsClient(mockClient)

	_, err := p.GetRegions(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no active regions found")
}

func TestGCPProvider_GetRegions_Error(t *testing.T) {
	ctx := context.Background()
	p := NewProviderWithProject(ctx, "test-project")

	mockClient := &MockRegionsClient{
		err: errors.New("API error"),
	}
	p.SetRegionsClient(mockClient)

	_, err := p.GetRegions(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to list regions")
}

// clearGCPCredEnv makes credential-source detection deterministic by clearing
// the credential env vars and pointing CLOUDSDK_CONFIG at an empty temp dir so
// no real gcloud ADC file on the test machine is picked up. t.Setenv auto-
// restores after the test.
func clearGCPCredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "")
	os.Unsetenv("GOOGLE_APPLICATION_CREDENTIALS")
	t.Setenv("CLOUDSDK_CONFIG", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())
}

func TestGCPProvider_GetCredentials_NotConfigured(t *testing.T) {
	clearGCPCredEnv(t)

	// No credential env var, no ADC file, and no project ID: detection finds
	// no usable source. GetCredentials reports this WITHOUT any network call
	// (10-H3) -- the projectsClient mock must never be invoked.
	mockClient := &MockProjectsClient{
		err: errors.New("network call must not happen"),
	}
	p := &GCPProvider{projectID: ""}
	p.SetProjectsClient(mockClient)

	_, err := p.GetCredentials()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "GCP is not configured")
	assert.False(t, mockClient.closed, "GetCredentials must not open/close a network client (no RPC)")
}

func TestGCPProvider_GetCredentials_Configured(t *testing.T) {
	clearGCPCredEnv(t)

	// A configured project ID is enough for ADC (metadata-server fallback) to be
	// the reported source, with no network call required.
	mockClient := &MockProjectsClient{
		err: errors.New("network call must not happen"),
	}
	p := &GCPProvider{projectID: "test-project"}
	p.SetProjectsClient(mockClient)

	creds, err := p.GetCredentials()
	require.NoError(t, err)
	require.NotNil(t, creds)

	baseCreds, ok := creds.(*provider.BaseCredentials)
	require.True(t, ok)
	assert.Equal(t, provider.CredentialSourceADC, baseCreds.Source)
	assert.False(t, mockClient.closed, "GetCredentials must not issue a GetProject RPC")
}

func TestGCPProvider_GetCredentials_WithFileSource(t *testing.T) {
	clearGCPCredEnv(t)
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "/path/to/creds.json")

	mockClient := &MockProjectsClient{
		err: errors.New("network call must not happen"),
	}
	p := &GCPProvider{projectID: "test-project"}
	p.SetProjectsClient(mockClient)

	creds, err := p.GetCredentials()
	require.NoError(t, err)
	require.NotNil(t, creds)

	baseCreds, ok := creds.(*provider.BaseCredentials)
	require.True(t, ok)
	assert.Equal(t, provider.CredentialSourceFile, baseCreds.Source)
	assert.False(t, mockClient.closed, "GetCredentials must not issue a GetProject RPC")
}

func TestGCPProvider_GetCredentials_EmptyEnvVarNotFile(t *testing.T) {
	// GOOGLE_APPLICATION_CREDENTIALS set to an empty string must NOT be detected as
	// CredentialSourceFile. os.LookupEnv returns ok=true for an empty string, so the
	// check must use os.Getenv(...) != "" instead.
	clearGCPCredEnv(t)
	// Explicitly set the variable to an empty string (clearGCPCredEnv unsets it, so
	// restore it as empty to reproduce the bug scenario).
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "")

	p := &GCPProvider{projectID: "test-project"}

	creds, err := p.GetCredentials()
	require.NoError(t, err)
	baseCreds, ok := creds.(*provider.BaseCredentials)
	require.True(t, ok)
	// An empty path is not a file credential; ADC (metadata fallback via projectID)
	// is the expected source.
	assert.Equal(t, provider.CredentialSourceADC, baseCreds.Source,
		"empty GOOGLE_APPLICATION_CREDENTIALS must not be detected as CredentialSourceFile")
}
