// Package gcp provides Google Cloud Platform provider implementation
package gcp

import (
	"context"
	"errors"
	"fmt"
	"os"

	"cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"cloud.google.com/go/resourcemanager/apiv3"
	"cloud.google.com/go/resourcemanager/apiv3/resourcemanagerpb"
	"golang.org/x/oauth2"
	"google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/logging"
	"github.com/LeanerCloud/CUDly/pkg/provider"
	"github.com/LeanerCloud/CUDly/providers/gcp/services/cloudsql"
	"github.com/LeanerCloud/CUDly/providers/gcp/services/cloudstorage"
	"github.com/LeanerCloud/CUDly/providers/gcp/services/computeengine"
	"github.com/LeanerCloud/CUDly/providers/gcp/services/memorystore"
)

// ProjectsClient interface for project operations (enables mocking)
type ProjectsClient interface {
	GetProject(ctx context.Context, req *resourcemanagerpb.GetProjectRequest) (*resourcemanagerpb.Project, error)
	Close() error
}

// RegionsClient interface for regions operations (enables mocking)
type RegionsClient interface {
	List(ctx context.Context, req *computepb.ListRegionsRequest) RegionsIterator
	Close() error
}

// RegionsIterator interface for regions iteration (enables mocking)
type RegionsIterator interface {
	Next() (*computepb.Region, error)
}

// ResourceManagerService interface for resource manager operations (enables mocking)
type ResourceManagerService interface {
	ListProjects(ctx context.Context) ([]*cloudresourcemanager.Project, error)
}

// realProjectsClient wraps the real resourcemanager.ProjectsClient
type realProjectsClient struct {
	client *resourcemanager.ProjectsClient
}

func (r *realProjectsClient) GetProject(ctx context.Context, req *resourcemanagerpb.GetProjectRequest) (*resourcemanagerpb.Project, error) {
	return r.client.GetProject(ctx, req)
}

func (r *realProjectsClient) Close() error {
	return r.client.Close()
}

// realRegionsClient wraps the real compute.RegionsClient
type realRegionsClient struct {
	client *compute.RegionsClient
}

func (r *realRegionsClient) List(ctx context.Context, req *computepb.ListRegionsRequest) RegionsIterator {
	return r.client.List(ctx, req)
}

func (r *realRegionsClient) Close() error {
	return r.client.Close()
}

// realResourceManagerService wraps the real cloudresourcemanager service
type realResourceManagerService struct {
	service *cloudresourcemanager.Service
}

func (r *realResourceManagerService) ListProjects(ctx context.Context) ([]*cloudresourcemanager.Project, error) {
	projects := make([]*cloudresourcemanager.Project, 0)
	req := r.service.Projects.List()
	if err := req.Pages(ctx, func(page *cloudresourcemanager.ListProjectsResponse) error {
		projects = append(projects, page.Projects...)
		return nil
	}); err != nil {
		return nil, err
	}
	return projects, nil
}

// GCPProvider implements the Provider interface for Google Cloud Platform
type GCPProvider struct {
	ctx                    context.Context
	projectID              string
	clientOpts             []option.ClientOption
	projectsClient         ProjectsClient
	regionsClient          RegionsClient
	resourceManagerService ResourceManagerService
}

// NewProvider creates a new GCP provider.
//
// Project resolution order:
//  1. config.GCPProjectID (typed field, preferred)
//  2. config.Profile (deprecated overload — kept for backwards compatibility)
//  3. Application Default Credentials' default project
//
// Credential resolution: if config.GCPTokenSource is a non-nil
// oauth2.TokenSource, it is installed via option.WithTokenSource so all
// downstream clients use those credentials. Otherwise, clients fall back
// to Application Default Credentials.
func NewProvider(config *provider.ProviderConfig) (*GCPProvider, error) {
	ctx := context.Background()

	projectID := resolveGCPProjectID(config)
	clientOpts := []option.ClientOption{}
	if config != nil && config.GCPTokenSource != nil {
		if ts, ok := config.GCPTokenSource.(oauth2.TokenSource); ok {
			clientOpts = append(clientOpts, option.WithTokenSource(ts))
		} else {
			// Non-nil but wrong-typed slot: log so mis-wirings (wrong
			// concrete type passed to the `any`-typed slot) surface
			// instead of being silently ignored. Falls back to ADC.
			logging.Warnf("gcp provider: config.GCPTokenSource is %T, expected oauth2.TokenSource — falling back to ambient credentials", config.GCPTokenSource)
		}
	}

	if projectID == "" {
		// Token source (when supplied) is forwarded to getDefaultProject so
		// the lookup uses the caller-provided credentials, not ambient ADC.
		var err error
		projectID, err = getDefaultProject(ctx, clientOpts...)
		if err != nil {
			return nil, fmt.Errorf("failed to get default GCP project: %w", err)
		}
	}

	return &GCPProvider{
		ctx:        ctx,
		projectID:  projectID,
		clientOpts: clientOpts,
	}, nil
}

// resolveGCPProjectID picks the project ID from the typed field, falling
// back to the deprecated Profile field. Returns "" if neither is set, which
// signals the caller to consult ADC.
func resolveGCPProjectID(config *provider.ProviderConfig) string {
	if config == nil {
		return ""
	}
	if config.GCPProjectID != "" {
		return config.GCPProjectID
	}
	return config.Profile
}

// NewProviderWithProject creates a new GCP provider with a specific project
func NewProviderWithProject(ctx context.Context, projectID string, opts ...option.ClientOption) *GCPProvider {
	return &GCPProvider{
		ctx:        ctx,
		projectID:  projectID,
		clientOpts: opts,
	}
}

// NewProviderWithCredentials creates a GCP provider that uses the supplied token source
// instead of Application Default Credentials. Use this for service account key or
// workload identity federation modes.
func NewProviderWithCredentials(ctx context.Context, projectID string, ts oauth2.TokenSource) *GCPProvider {
	return NewProviderWithProject(ctx, projectID, option.WithTokenSource(ts))
}

// SetProjectsClient sets the projects client (for testing)
func (p *GCPProvider) SetProjectsClient(client ProjectsClient) {
	p.projectsClient = client
}

// SetRegionsClient sets the regions client (for testing)
func (p *GCPProvider) SetRegionsClient(client RegionsClient) {
	p.regionsClient = client
}

// SetResourceManagerService sets the resource manager service (for testing)
func (p *GCPProvider) SetResourceManagerService(svc ResourceManagerService) {
	p.resourceManagerService = svc
}

// Name returns the provider name
func (p *GCPProvider) Name() string {
	return string(common.ProviderGCP)
}

// DisplayName returns the provider display name
func (p *GCPProvider) DisplayName() string {
	return "Google Cloud Platform"
}

// IsConfigured checks if GCP credentials are configured.
//
// When a client has been injected via SetProjectsClient (test path), the
// injector owns its lifecycle and we must NOT Close() it here — otherwise
// subsequent calls in the same test hit a closed connection. In production
// the client is constructed internally and we retain Close responsibility.
func (p *GCPProvider) IsConfigured() bool {
	ctx := context.Background()

	// Use injected client if available (for testing)
	var projectsClient ProjectsClient
	injected := p.projectsClient != nil
	if injected {
		projectsClient = p.projectsClient
	} else {
		// Try to create a simple client to test credentials
		client, err := resourcemanager.NewProjectsClient(ctx, p.clientOpts...)
		if err != nil {
			return false
		}
		projectsClient = &realProjectsClient{client: client}
	}
	if !injected {
		defer projectsClient.Close()
	}

	// Try to get the project to verify credentials work
	_, err := projectsClient.GetProject(ctx, &resourcemanagerpb.GetProjectRequest{
		Name: fmt.Sprintf("projects/%s", p.projectID),
	})

	return err == nil
}

// ValidateCredentials validates that GCP credentials are valid.
// Same injected-client ownership rule as IsConfigured — see that godoc.
func (p *GCPProvider) ValidateCredentials(ctx context.Context) error {
	// Use injected client if available (for testing)
	var projectsClient ProjectsClient
	injected := p.projectsClient != nil
	if injected {
		projectsClient = p.projectsClient
	} else {
		client, err := resourcemanager.NewProjectsClient(ctx, p.clientOpts...)
		if err != nil {
			return fmt.Errorf("failed to create resource manager client: %w", err)
		}
		projectsClient = &realProjectsClient{client: client}
	}
	if !injected {
		defer projectsClient.Close()
	}

	// Verify we can access the project
	project, err := projectsClient.GetProject(ctx, &resourcemanagerpb.GetProjectRequest{
		Name: fmt.Sprintf("projects/%s", p.projectID),
	})
	if err != nil {
		return fmt.Errorf("failed to get project %s: %w", p.projectID, err)
	}

	if project.State != resourcemanagerpb.Project_ACTIVE {
		return fmt.Errorf("project %s is not active (state: %v)", p.projectID, project.State)
	}

	return nil
}

// GetCredentials returns the current GCP credentials information
func (p *GCPProvider) GetCredentials() (provider.Credentials, error) {
	if !p.IsConfigured() {
		return nil, fmt.Errorf("GCP is not configured")
	}

	// GCP uses Application Default Credentials (ADC)
	// The actual credentials could come from:
	// - GOOGLE_APPLICATION_CREDENTIALS env var (service account JSON file)
	// - gcloud CLI configuration
	// - Compute Engine/GKE metadata service
	// - Cloud Shell

	credType := provider.CredentialSourceADC // Application Default Credentials

	// Try to determine the source more specifically
	if _, ok := os.LookupEnv("GOOGLE_APPLICATION_CREDENTIALS"); ok {
		credType = provider.CredentialSourceFile
	}

	return &provider.BaseCredentials{
		Source: credType,
		Valid:  true,
	}, nil
}

// GetDefaultRegion returns the default GCP region
func (p *GCPProvider) GetDefaultRegion() string {
	// GCP doesn't have a concept of "default region" like AWS
	// Common defaults are us-central1 (Iowa) or us-east1 (South Carolina)
	return "us-central1"
}

// GetAccounts returns all accessible GCP projects
func (p *GCPProvider) GetAccounts(ctx context.Context) ([]common.Account, error) {
	accounts := make([]common.Account, 0)

	// Use injected service if available (for testing)
	var rmService ResourceManagerService
	if p.resourceManagerService != nil {
		rmService = p.resourceManagerService
	} else {
		// For GCP, accounts are projects
		service, err := cloudresourcemanager.NewService(ctx, p.clientOpts...)
		if err != nil {
			return nil, fmt.Errorf("failed to create resource manager service: %w", err)
		}
		rmService = &realResourceManagerService{service: service}
	}

	// List all projects the credentials have access to
	projects, err := rmService.ListProjects(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list projects: %w", err)
	}

	for _, project := range projects {
		if project.LifecycleState == "ACTIVE" {
			accounts = append(accounts, common.Account{
				Provider:  common.ProviderGCP,
				ID:        project.ProjectId,
				Name:      project.Name,
				IsDefault: project.ProjectId == p.projectID,
			})
		}
	}

	// If no projects found, return at least the default project
	if len(accounts) == 0 {
		accounts = append(accounts, common.Account{
			Provider:  common.ProviderGCP,
			ID:        p.projectID,
			Name:      p.projectID,
			IsDefault: true,
		})
	}

	return accounts, nil
}

// GetRegions returns all available GCP regions using Compute Engine API
func (p *GCPProvider) GetRegions(ctx context.Context) ([]common.Region, error) {
	regClient, err := p.createRegionsClient(ctx)
	if err != nil {
		return nil, err
	}
	defer regClient.Close()

	regions, err := p.collectActiveRegions(ctx, regClient)
	if err != nil {
		return nil, err
	}

	if len(regions) == 0 {
		return nil, fmt.Errorf("no active regions found for project %s", p.projectID)
	}

	return regions, nil
}

func (p *GCPProvider) createRegionsClient(ctx context.Context) (RegionsClient, error) {
	// Use injected client if available (for testing)
	if p.regionsClient != nil {
		return p.regionsClient, nil
	}

	client, err := compute.NewRegionsRESTClient(ctx, p.clientOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create compute client: %w", err)
	}
	return &realRegionsClient{client: client}, nil
}

func (p *GCPProvider) collectActiveRegions(ctx context.Context, regClient RegionsClient) ([]common.Region, error) {
	req := &computepb.ListRegionsRequest{
		Project: p.projectID,
	}

	regions := make([]common.Region, 0)
	it := regClient.List(ctx, req)

	for {
		region, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to list regions: %w", err)
		}

		if convertedRegion := convertGCPRegion(region); convertedRegion != nil {
			regions = append(regions, *convertedRegion)
		}
	}

	return regions, nil
}

func convertGCPRegion(region *computepb.Region) *common.Region {
	if region.Name == nil || region.Status == nil || *region.Status != "UP" {
		return nil
	}

	displayName := *region.Name
	if region.Description != nil {
		displayName = *region.Description
	}

	return &common.Region{
		ID:          *region.Name,
		DisplayName: displayName,
	}
}

// GetSupportedServices returns the list of supported GCP services
func (p *GCPProvider) GetSupportedServices() []common.ServiceType {
	return []common.ServiceType{
		common.ServiceCompute,
		common.ServiceRelationalDB,
		common.ServiceCache,
		common.ServiceStorage,
	}
}

// GetServiceClient creates a service client for the specified service and region
func (p *GCPProvider) GetServiceClient(ctx context.Context, service common.ServiceType, region string) (provider.ServiceClient, error) {
	switch service {
	case common.ServiceCompute:
		return computeengine.NewClient(ctx, p.projectID, region, p.clientOpts...)
	case common.ServiceRelationalDB:
		return cloudsql.NewClient(ctx, p.projectID, region, p.clientOpts...)
	case common.ServiceCache:
		return memorystore.NewClient(ctx, p.projectID, region, p.clientOpts...)
	case common.ServiceStorage:
		return cloudstorage.NewClient(ctx, p.projectID, region, p.clientOpts...)
	default:
		return nil, fmt.Errorf("unsupported service type for GCP: %s", service)
	}
}

// GetRecommendationsClient creates a recommendations client
func (p *GCPProvider) GetRecommendationsClient(ctx context.Context) (provider.RecommendationsClient, error) {
	return &RecommendationsClientAdapter{
		ctx:        ctx,
		projectID:  p.projectID,
		clientOpts: p.clientOpts,
	}, nil
}

// errStopProjectPagination is a sentinel used by Pages() to short-circuit
// iteration as soon as getDefaultProject finds its first ACTIVE project,
// avoiding unnecessary page fetches in large organisations.
var errStopProjectPagination = errors.New("stop pagination: found active project")

// listProjectsForDefault walks all resource-manager project pages, calling
// cb for each. Package-level var so tests can swap in a fake that simulates
// "no ACTIVE projects across every page" without standing up a real service.
var listProjectsForDefault = func(ctx context.Context, opts []option.ClientOption, cb func(*cloudresourcemanager.ListProjectsResponse) error) error {
	service, err := cloudresourcemanager.NewService(ctx, opts...)
	if err != nil {
		return fmt.Errorf("failed to create resource manager service: %w", err)
	}
	return service.Projects.List().Pages(ctx, cb)
}

// getDefaultProject attempts to get the default GCP project from environment
// or ADC. In organisations with more than one page of projects (~500 per
// page), this walks pages via Pages() until the first ACTIVE project is
// found — a single req.Do() would only see page 1 and falsely report
// "no active GCP projects found" if the active one sat on a later page.
//
// opts is forwarded to cloudresourcemanager.NewService so callers can inject
// option.WithTokenSource (or similar) to use non-ambient credentials. With
// no opts the lookup uses Application Default Credentials.
func getDefaultProject(ctx context.Context, opts ...option.ClientOption) (string, error) {
	var foundID string
	err := listProjectsForDefault(ctx, opts, func(resp *cloudresourcemanager.ListProjectsResponse) error {
		return findActiveProjectInPage(&foundID, resp)
	})
	if err != nil && !errors.Is(err, errStopProjectPagination) {
		return "", fmt.Errorf("failed to list projects: %w", err)
	}
	if foundID == "" {
		return "", fmt.Errorf("no active GCP projects found")
	}
	return foundID, nil
}

// findActiveProjectInPage is the per-page callback for getDefaultProject's
// `Pages()` walk. It sets *out to the first ACTIVE project's ID it sees and
// returns errStopProjectPagination to short-circuit iteration; for pages
// containing no ACTIVE projects it leaves *out untouched and returns nil so
// the walk continues to the next page. Extracted so unit tests can cover the
// pagination/short-circuit semantics without standing up a real
// cloudresourcemanager service.
func findActiveProjectInPage(out *string, page *cloudresourcemanager.ListProjectsResponse) error {
	for _, project := range page.Projects {
		if project.LifecycleState == "ACTIVE" {
			*out = project.ProjectId
			return errStopProjectPagination
		}
	}
	return nil
}

func init() {
	// Register GCP provider in the global registry
	provider.RegisterProvider("gcp", func(config *provider.ProviderConfig) (provider.Provider, error) {
		return NewProvider(config)
	})
}
