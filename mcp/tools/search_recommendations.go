package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/provider"
)

const searchRecommendationsName = "cudly_search_recommendations"

const searchRecommendationsDescription = "Search for reserved-capacity purchase recommendations (RI/SP/CUD) " +
	"across AWS, Azure, or GCP. Read-only: makes no purchase and spends no money -- there is no dry_run or " +
	"confirm parameter because nothing is ever bought. Use this first to find what to buy, then feed a result's " +
	"region/resource_type/count into the matching cudly_<provider>_<product>_<action>_purchase tool."

// searchRecommendationsArgs mirrors common.RecommendationParams, adding the
// provider selector and the optional per-call credential overrides from the
// design doc's §4 config-exposure model (aws_profile /
// azure_subscription_id / gcp_project_id).
type searchRecommendationsArgs struct {
	Provider            string   `json:"provider" jsonschema:"cloud provider to search"`
	Service             string   `json:"service" jsonschema:"service to search, e.g. ec2, rds, elasticache, compute, computeengine"`
	Region              string   `json:"region,omitempty" jsonschema:"region to search; omit for account/global-level services such as Savings Plans"`
	IncludeRegions      []string `json:"include_regions,omitempty" jsonschema:"restrict the search to these regions, in addition to (or instead of) region"`
	ExcludeRegions      []string `json:"exclude_regions,omitempty" jsonschema:"exclude these regions from the search"`
	LookbackPeriod      string   `json:"lookback_period,omitempty" jsonschema:"cost/usage lookback window backing the recommendation"`
	TermYears           int      `json:"term_years,omitempty" jsonschema:"filter to a specific commitment term; omit to search all terms"`
	PaymentOption       string   `json:"payment_option,omitempty" jsonschema:"filter to a specific payment schedule; omit to search all"`
	AccountFilter       []string `json:"account_filter,omitempty" jsonschema:"restrict the search to these account/subscription/project IDs"`
	IncludeSPTypes      []string `json:"include_sp_types,omitempty" jsonschema:"AWS Savings Plans types to include; omit for all"`
	ExcludeSPTypes      []string `json:"exclude_sp_types,omitempty" jsonschema:"AWS Savings Plans types to exclude"`
	AWSProfile          string   `json:"aws_profile,omitempty" jsonschema:"AWS named profile override (~/.aws/config); default uses ambient credentials"`
	AzureSubscriptionID string   `json:"azure_subscription_id,omitempty" jsonschema:"Azure subscription ID override; default uses AZURE_SUBSCRIPTION_ID"`
	GCPProjectID        string   `json:"gcp_project_id,omitempty" jsonschema:"GCP project ID override; default uses ambient project"`
}

// searchRecommendationsResult is the tool's structured output.
type searchRecommendationsResult struct {
	Count           int                     `json:"count"`
	Recommendations []common.Recommendation `json:"recommendations"`
}

type searchRecommendationsTool struct {
	// createProvider is a seam over provider.CreateProvider so tests can
	// inject a fake Provider without resolving real cloud credentials.
	createProvider func(name string, cfg *provider.ProviderConfig) (provider.Provider, error)
}

// NewSearchRecommendationsTool builds the cudly_search_recommendations tool.
func NewSearchRecommendationsTool() Registration {
	return &searchRecommendationsTool{createProvider: provider.CreateProvider}
}

func (t *searchRecommendationsTool) Descriptor() Descriptor {
	return Descriptor{
		Name:        searchRecommendationsName,
		Description: searchRecommendationsDescription,
		Action:      "search",
		ExamplePrompts: []string{
			"Search for AWS EC2 RI recommendations in us-east-1",
			"What RDS Reserved Instance recommendations exist for account 123456789012?",
			"Find GCP Compute Engine committed-use discount recommendations",
		},
	}
}

func (t *searchRecommendationsTool) Register(s *mcp.Server) error {
	schema, err := BuildInputSchema[searchRecommendationsArgs](map[string]FieldOverride{
		"provider":        {Enum: []any{string(common.ProviderAWS), string(common.ProviderAzure), string(common.ProviderGCP)}},
		"lookback_period": {Enum: []any{"7d", "30d", "60d"}},
	})
	if err != nil {
		return err
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        searchRecommendationsName,
		Description: searchRecommendationsDescription,
		InputSchema: schema,
	}, t.handle)
	return nil
}

func (t *searchRecommendationsTool) handle(ctx context.Context, _ *mcp.CallToolRequest, args searchRecommendationsArgs) (*mcp.CallToolResult, searchRecommendationsResult, error) {
	providerType, term, err := validateSearchArgs(args)
	if err != nil {
		return nil, searchRecommendationsResult{}, err
	}

	prov, err := t.createProvider(string(providerType), providerConfigFromArgs(providerType, args))
	if err != nil {
		return nil, searchRecommendationsResult{}, fmt.Errorf("create %s provider: %w", providerType, err)
	}

	service, err := validateSupportedService(prov, args.Service)
	if err != nil {
		return nil, searchRecommendationsResult{}, err
	}

	recClient, err := prov.GetRecommendationsClient(ctx)
	if err != nil {
		return nil, searchRecommendationsResult{}, fmt.Errorf("get %s recommendations client: %w", providerType, err)
	}

	recs, err := recClient.GetRecommendations(ctx, recommendationParamsFromArgs(service, term, args))
	if err != nil {
		return nil, searchRecommendationsResult{}, fmt.Errorf("get recommendations: %w", err)
	}

	return nil, searchRecommendationsResult{Count: len(recs), Recommendations: recs}, nil
}

// validateSearchArgs validates every money-neutral-but-still-typed field on
// args that does not require a live provider (provider name, payment
// option, lookback_period, term, Savings Plans type filters), returning the
// typed provider name and the normalised Recommendation term string
// ("1yr"/"3yr", or "" when args.TermYears was omitted).
func validateSearchArgs(args searchRecommendationsArgs) (common.ProviderType, string, error) {
	providerType, err := validateProviderName(args.Provider)
	if err != nil {
		return "", "", err
	}

	if args.PaymentOption != "" {
		if _, err := ValidatePaymentOption(args.PaymentOption); err != nil {
			return "", "", err
		}
	}

	if _, err := ValidateLookbackPeriod(args.LookbackPeriod); err != nil {
		return "", "", err
	}

	term := ""
	if args.TermYears != 0 {
		ty, err := ValidateTermYears(args.TermYears)
		if err != nil {
			return "", "", err
		}
		term = ty.RecommendationTerm()
	}

	if err := validateSPTypeFilters(args.IncludeSPTypes, args.ExcludeSPTypes); err != nil {
		return "", "", err
	}

	return providerType, term, nil
}

// validateSPTypeFilters validates every entry of include/exclude against
// the AWS Savings Plans type enum, naming which filter a bad entry came
// from.
func validateSPTypeFilters(include, exclude []string) error {
	for _, sp := range include {
		if _, err := ValidateSPType(sp); err != nil {
			return fmt.Errorf("include_sp_types: %w", err)
		}
	}
	for _, sp := range exclude {
		if _, err := ValidateSPType(sp); err != nil {
			return fmt.Errorf("exclude_sp_types: %w", err)
		}
	}
	return nil
}

// providerConfigFromArgs builds the provider.ProviderConfig for the given
// provider from the tool's per-call credential override fields (design §4).
func providerConfigFromArgs(providerType common.ProviderType, args searchRecommendationsArgs) *provider.ProviderConfig {
	return &provider.ProviderConfig{
		Name:                string(providerType),
		AWSProfile:          args.AWSProfile,
		AzureSubscriptionID: args.AzureSubscriptionID,
		GCPProjectID:        args.GCPProjectID,
		Region:              args.Region,
	}
}

// recommendationParamsFromArgs builds the common.RecommendationParams for
// the already-validated service and term.
func recommendationParamsFromArgs(service common.ServiceType, term string, args searchRecommendationsArgs) *common.RecommendationParams {
	return &common.RecommendationParams{
		Service:        service,
		Region:         args.Region,
		LookbackPeriod: args.LookbackPeriod,
		Term:           term,
		PaymentOption:  args.PaymentOption,
		AccountFilter:  args.AccountFilter,
		IncludeRegions: args.IncludeRegions,
		ExcludeRegions: args.ExcludeRegions,
		IncludeSPTypes: args.IncludeSPTypes,
		ExcludeSPTypes: args.ExcludeSPTypes,
	}
}

// validateProviderName returns the typed common.ProviderType for s, or an
// explicit error when s is not aws, azure, or gcp.
func validateProviderName(s string) (common.ProviderType, error) {
	switch common.ProviderType(s) {
	case common.ProviderAWS, common.ProviderAzure, common.ProviderGCP:
		return common.ProviderType(s), nil
	default:
		return "", fmt.Errorf("invalid provider %q: must be one of %s, %s, %s",
			s, common.ProviderAWS, common.ProviderAzure, common.ProviderGCP)
	}
}

// validateSupportedService checks service against prov's own
// GetSupportedServices() -- the provider's live list, not a hardcoded
// mirror of it -- so this tool never drifts from what each provider
// actually supports.
func validateSupportedService(prov provider.Provider, service string) (common.ServiceType, error) {
	if service == "" {
		return "", fmt.Errorf("service is required")
	}
	want := common.ServiceType(service)
	supported := prov.GetSupportedServices()
	for _, s := range supported {
		if s == want {
			return want, nil
		}
	}
	names := make([]string, len(supported))
	for i, s := range supported {
		names[i] = s.String()
	}
	return "", fmt.Errorf("invalid service %q for provider %s: must be one of %s", service, prov.Name(), strings.Join(names, ", "))
}
