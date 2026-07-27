package tools

import (
	"context"
	"errors"
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
	LookbackPeriod      string   `json:"lookback_period,omitempty" jsonschema:"cost/usage lookback window backing the recommendation; AWS Savings Plans searches default to 30d when omitted (AWS requires a value); reservation searches (EC2/RDS/etc) leave it omitted to search all lookback windows"`
	TermYears           int      `json:"term_years,omitempty" jsonschema:"commitment term filter; AWS Savings Plans searches default to 1 (1yr, no-upfront) when omitted (AWS requires a value); reservation searches (EC2/RDS/etc) leave it omitted to search all terms"`
	PaymentOption       string   `json:"payment_option,omitempty" jsonschema:"payment schedule filter; AWS Savings Plans searches default to no-upfront when omitted (AWS requires a value); reservation searches (EC2/RDS/etc) leave it omitted to search all payment options"`
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
	// Trim BEFORE validating: validateSearchArgs matches service against the
	// Savings Plans family predicate and rejects blank filter-list entries,
	// both of which must see the normalized values the rest of the call will
	// actually use.
	args = trimSearchArgsIdentifiers(args)
	providerType, term, args, err := validateSearchArgs(args)
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
// typed provider name, the normalised Recommendation term string
// ("1yr"/"3yr", or "" when args.TermYears was omitted), and args with
// applySavingsPlansSearchDefaults applied.
//
// Every invalid or missing field is collected and joined into a single
// error rather than returning on the first one found, so a caller missing
// several required fields (see requireSavingsPlansSearchFields) sees all of
// them at once instead of fixing them one call at a time.
func validateSearchArgs(args searchRecommendationsArgs) (common.ProviderType, string, searchRecommendationsArgs, error) {
	providerType, err := validateProviderName(args.Provider)
	if err != nil {
		return "", "", args, err
	}
	args = applySavingsPlansSearchDefaults(providerType, args)

	term, errs := collectSearchFieldErrors(providerType, args)
	if len(errs) > 0 {
		return "", "", args, errors.Join(errs...)
	}

	return providerType, term, args, nil
}

// collectSearchFieldErrors validates every per-field constraint on args
// (payment option, lookback window, term, Savings Plans type filters, blank
// filter-list entries, and the Savings Plans required-field safety net) and
// returns the normalised Recommendation term alongside EVERY error found,
// rather than stopping at the first. A caller that got several fields wrong
// then fixes them in one round instead of one call at a time.
//
// Split out of validateSearchArgs so that function stays under the repo's
// gocyclo gate (the pre-commit hook is -over 10, stricter than golangci's
// min-complexity 15).
func collectSearchFieldErrors(providerType common.ProviderType, args searchRecommendationsArgs) (string, []error) {
	var errs []error

	if args.PaymentOption != "" {
		if _, err := ValidatePaymentOption(args.PaymentOption); err != nil {
			errs = append(errs, err)
		}
	}

	if _, err := ValidateLookbackPeriod(args.LookbackPeriod); err != nil {
		errs = append(errs, err)
	}

	term := ""
	if args.TermYears != 0 {
		if ty, err := ValidateTermYears(args.TermYears); err != nil {
			errs = append(errs, err)
		} else {
			term = ty.RecommendationTerm()
		}
	}

	if err := validateSPTypeFilters(args.IncludeSPTypes, args.ExcludeSPTypes); err != nil {
		errs = append(errs, err)
	}

	if err := validateFilterListEntries(args); err != nil {
		errs = append(errs, err)
	}

	if err := requireSavingsPlansSearchFields(providerType, args); err != nil {
		errs = append(errs, err)
	}

	return term, errs
}

// isAWSSavingsPlansSearch reports whether args targets an AWS Savings Plans
// search -- the only combination where AWS's
// GetSavingsPlansPurchaseRecommendation requires term_years, payment_option,
// and lookback_period on every call. GetReservationPurchaseRecommendation
// (EC2/RDS/etc) defaults these server-side when omitted, so detection must
// never broaden past the Savings Plans family. Checked straight off the raw
// args -- no live provider needed -- via the same common.IsSavingsPlan
// family predicate the AWS recommendations client itself dispatches on
// (providers/aws/recommendations/client.go).
func isAWSSavingsPlansSearch(providerType common.ProviderType, service string) bool {
	return providerType == common.ProviderAWS && common.IsSavingsPlan(common.ServiceType(service))
}

// applySavingsPlansSearchDefaults defaults payment_option, term_years, and
// lookback_period to no-upfront/1yr/30d when args targets an AWS Savings
// Plans search and the caller omitted them. Without this, an AWS SP search
// with these fields blank fails against Cost Explorer one field at a time
// (issue #1506): GetSavingsPlansPurchaseRecommendation requires all three,
// unlike the EC2/RDS reservation path this tool otherwise shares, which
// defaults them server-side. A caller-supplied value always wins -- this
// only fills in what was left blank. No-op for every other provider/service
// combination.
func applySavingsPlansSearchDefaults(providerType common.ProviderType, args searchRecommendationsArgs) searchRecommendationsArgs {
	if !isAWSSavingsPlansSearch(providerType, args.Service) {
		return args
	}
	if args.PaymentOption == "" {
		args.PaymentOption = string(PaymentOptionNoUpfront)
	}
	if args.TermYears == 0 {
		args.TermYears = int(TermOneYear)
	}
	if args.LookbackPeriod == "" {
		args.LookbackPeriod = string(LookbackPeriod30Days)
	}
	return args
}

// requireSavingsPlansSearchFields is a validate-all-at-once safety net: after
// applySavingsPlansSearchDefaults runs, an AWS Savings Plans search should
// never still be missing payment_option/term_years/lookback_period. If a
// future change to the defaulting logic leaves one blank anyway, this fails
// loud -- naming every still-missing field in one error -- rather than
// letting the request reach Cost Explorer and fail one field at a time (the
// cascade issue #1506 fixes).
func requireSavingsPlansSearchFields(providerType common.ProviderType, args searchRecommendationsArgs) error {
	if !isAWSSavingsPlansSearch(providerType, args.Service) {
		return nil
	}
	var missing []string
	if args.PaymentOption == "" {
		missing = append(missing, "payment_option")
	}
	if args.TermYears == 0 {
		missing = append(missing, "term_years")
	}
	if args.LookbackPeriod == "" {
		missing = append(missing, "lookback_period")
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("savings plans search missing required field(s): %s", strings.Join(missing, ", "))
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

// trimSearchArgsIdentifiers returns args with surrounding whitespace
// stripped from every free-text identifier field that flows into
// providerConfigFromArgs/recommendationParamsFromArgs (service, region,
// include_regions, exclude_regions, account_filter). Unlike the purchase
// tools' requireNonBlank, region is optional here, so trimming rather than
// rejecting a blank/whitespace value is correct: " us-east-1 " must resolve
// and search the same region as "us-east-1" instead of silently searching
// the wrong (or account-default) region.
//
// This runs before validateSearchArgs so service is already trimmed when
// isAWSSavingsPlansSearch and validateSupportedService match on it: without
// that, " savingsplans-compute" would both miss the Savings Plans defaulting
// branch and fail the supported-service check for a reason (a stray space)
// the error message would not explain.
func trimSearchArgsIdentifiers(args searchRecommendationsArgs) searchRecommendationsArgs {
	args.Service = strings.TrimSpace(args.Service)
	args.Region = strings.TrimSpace(args.Region)
	args.IncludeRegions = trimAll(args.IncludeRegions)
	args.ExcludeRegions = trimAll(args.ExcludeRegions)
	args.AccountFilter = trimAll(args.AccountFilter)
	return args
}

// trimAll returns a copy of ss with strings.TrimSpace applied to every
// entry, preserving a nil input as nil.
func trimAll(ss []string) []string {
	if ss == nil {
		return nil
	}
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = strings.TrimSpace(s)
	}
	return out
}

// rejectBlankListEntries fails loud when any entry of a supplied filter list
// is empty or whitespace-only. A blank entry cannot match a real region code
// or account ID, but a non-empty list still switches the corresponding
// filter ON: include_regions=["  "] would activate region filtering with a
// set that matches nothing and silently return zero recommendations, which
// reads as "your account has nothing to buy" rather than "your filter was
// malformed". Naming the offending index makes the malformed entry
// actionable instead of leaving the caller to guess which one it was.
func rejectBlankListEntries(field string, values []string) error {
	for i, v := range values {
		if v == "" {
			return fmt.Errorf("%s[%d] is blank: every entry must be a non-empty identifier", field, i)
		}
	}
	return nil
}

// validateFilterListEntries runs rejectBlankListEntries over every
// caller-supplied filter list. Split out of validateSearchArgs to keep that
// function under the repo's gocyclo gate (the pre-commit hook is -over 10,
// stricter than golangci's min-complexity 15).
func validateFilterListEntries(args searchRecommendationsArgs) error {
	lists := []struct {
		field  string
		values []string
	}{
		{"include_regions", args.IncludeRegions},
		{"exclude_regions", args.ExcludeRegions},
		{"account_filter", args.AccountFilter},
	}
	for _, list := range lists {
		if err := rejectBlankListEntries(list.field, list.values); err != nil {
			return err
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
