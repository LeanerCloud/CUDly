package tools

import (
	"context"
	"fmt"
	"strings"

	spTypes "github.com/aws/aws-sdk-go-v2/service/savingsplans/types"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/provider"
	"github.com/LeanerCloud/CUDly/providers/aws/services/savingsplans"
)

const awsSavingsPlansPurchaseName = "cudly_aws_savingsplans_purchase"

const awsSavingsPlansPurchaseDescription = "Purchase an AWS Savings Plan (Compute, EC2Instance, SageMaker, or " +
	"Database). THIS SPENDS REAL MONEY when dry_run=false and confirm=true. Always call with dry_run=true " +
	"first (the default) to validate your parameters before committing; a dry_run response never contacts AWS " +
	"and never spends money. Unlike RI purchases this is dollar-denominated: you specify hourly_commitment " +
	"(USD/hour), not an instance count. CAVEAT: sp_type=Database only supports term_years=1 and " +
	"payment_option=no-upfront; AWS does not offer a 3-year Database Savings Plan or all-upfront/" +
	"partial-upfront billing for it."

// savingsPlansAccountLevelRegion is the region used to resolve the account-
// level Savings Plans service client when the caller omits region -- Compute,
// SageMaker, and Database plans are global, and cmd/multi_service_helpers.go
// already establishes this same "single query, us-east-1" convention for
// account-level Savings Plans recommendations.
const savingsPlansAccountLevelRegion = "us-east-1"

// savingsPlansPurchaseArgs is the input schema for
// cudly_aws_savingsplans_purchase. instance_family and region are only
// meaningful for EC2Instance plans (common.SavingsPlanDetails); Compute,
// SageMaker, and Database plans are family-agnostic and account-level.
type savingsPlansPurchaseArgs struct {
	SPType           string  `json:"sp_type" jsonschema:"AWS Savings Plans type"`
	HourlyCommitment float64 `json:"hourly_commitment" jsonschema:"USD/hour commitment amount, must be > 0"`
	TermYears        int     `json:"term_years" jsonschema:"commitment length in years"`
	PaymentOption    string  `json:"payment_option" jsonschema:"payment schedule"`
	InstanceFamily   string  `json:"instance_family,omitempty" jsonschema:"EC2 instance family, e.g. m5; only meaningful for sp_type=EC2Instance"`
	Region           string  `json:"region,omitempty" jsonschema:"AWS region; required for sp_type=EC2Instance, ignored for account-level plan types"`
	AWSProfile       string  `json:"aws_profile,omitempty" jsonschema:"AWS named profile override (~/.aws/config); default uses ambient credentials"`
	DryRun           *bool   `json:"dry_run,omitempty" jsonschema:"preview only, no purchase; defaults to true"`
	Confirm          *bool   `json:"confirm,omitempty" jsonschema:"required (with dry_run=false) to execute a real purchase; defaults to false"`
	IdempotencyNonce string  `json:"idempotency_nonce,omitempty" jsonschema:"optional; set to a fresh value to authorize a purchase that is otherwise identical to a previous one (e.g. buy 3 more RIs with the same parameters); leave empty (the default) so retries with identical parameters dedupe and never double-buy"`
}

type awsSavingsPlansPurchaseTool struct {
	createProvider func(name string, cfg *provider.ProviderConfig) (provider.Provider, error)
}

// NewAWSSavingsPlansPurchaseTool builds the cudly_aws_savingsplans_purchase tool.
func NewAWSSavingsPlansPurchaseTool() Registration {
	return &awsSavingsPlansPurchaseTool{createProvider: provider.CreateProvider}
}

func (t *awsSavingsPlansPurchaseTool) Descriptor() Descriptor {
	return Descriptor{
		Name:                awsSavingsPlansPurchaseName,
		Provider:            "aws",
		Product:             "savingsplans",
		Action:              "purchase",
		Description:         awsSavingsPlansPurchaseDescription,
		RealPurchaseEnabled: true,
		ExamplePrompts: []string{
			"Preview a $10/hour Compute Savings Plan, 3-year no-upfront",
			"Buy a $5/hour EC2Instance Savings Plan for the m5 family in us-east-1 for real",
		},
	}
}

func (t *awsSavingsPlansPurchaseTool) Register(s *mcp.Server) error {
	schema, err := BuildInputSchema[savingsPlansPurchaseArgs](map[string]FieldOverride{
		"sp_type": {Enum: []any{
			string(SPTypeCompute), string(SPTypeEC2Instance), string(SPTypeSageMaker), string(SPTypeDatabase),
		}},
		"term_years":     {Enum: []any{int(TermOneYear), int(TermThreeYear)}},
		"payment_option": {Enum: []any{string(PaymentOptionAllUpfront), string(PaymentOptionPartialUpfront), string(PaymentOptionNoUpfront)}},
		"dry_run":        {Default: true},
		"confirm":        {Default: false},
	})
	if err != nil {
		return err
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        awsSavingsPlansPurchaseName,
		Description: awsSavingsPlansPurchaseDescription,
		InputSchema: schema,
	}, t.handle)
	return nil
}

func (t *awsSavingsPlansPurchaseTool) handle(ctx context.Context, _ *mcp.CallToolRequest, args savingsPlansPurchaseArgs) (*mcp.CallToolResult, PurchaseResponse, error) {
	rec, region, dryRun, confirm, err := savingsPlanRecommendationFromArgs(args)
	if err != nil {
		return nil, PurchaseResponse{}, err
	}

	resp, err := ExecutePurchase(ctx, PurchaseRequest{
		Region:         region,
		Recommendation: rec,
		DryRun:         dryRun,
		Confirm:        confirm,
		ResolveClient:  t.resolveClient(args, region, rec.Service),
		Nonce:          args.IdempotencyNonce,
	})
	if err != nil {
		return nil, PurchaseResponse{}, err
	}
	return nil, *resp, nil
}

// validateSavingsPlanArgs validates every field of args that does not
// depend on the effective region, returning the typed sp_type, term, and
// payment_option. Split out of savingsPlanRecommendationFromArgs so that
// function's cyclomatic complexity stays under the repo's gocyclo gate as
// validation branches (e.g. validateDatabaseSPConstraints) are added.
func validateSavingsPlanArgs(args savingsPlansPurchaseArgs) (spType SPType, term TermYears, paymentOption PaymentOption, err error) {
	if args.HourlyCommitment <= 0 {
		return "", 0, "", fmt.Errorf("hourly_commitment must be > 0, got %v", args.HourlyCommitment)
	}
	spType, err = ValidateSPType(args.SPType)
	if err != nil {
		return "", 0, "", err
	}
	term, err = ValidateTermYears(args.TermYears)
	if err != nil {
		return "", 0, "", err
	}
	paymentOption, err = ValidatePaymentOption(args.PaymentOption)
	if err != nil {
		return "", 0, "", err
	}
	if spType == SPTypeEC2Instance && strings.TrimSpace(args.Region) == "" {
		return "", 0, "", fmt.Errorf("region is required for sp_type=%s", SPTypeEC2Instance)
	}
	// instance_family is the filter that stops DescribeSavingsPlansOfferings
	// from resolving to an arbitrary EC2Instance offering across every family
	// in the region. providers/aws/services/savingsplans/client.go's
	// lookupEC2OfferingIDStrict does fail loud when the resulting offerings
	// span more than one family, but that is defense in depth at the API
	// boundary; requiring the family here, at the tool boundary, catches the
	// missing value before a real purchase attempt is even made.
	if spType == SPTypeEC2Instance && strings.TrimSpace(args.InstanceFamily) == "" {
		return "", 0, "", fmt.Errorf("instance_family is required for sp_type=%s", SPTypeEC2Instance)
	}
	if err := validateDatabaseSPConstraints(spType, term, paymentOption); err != nil {
		return "", 0, "", err
	}
	return spType, term, paymentOption, nil
}

// savingsPlanRecommendationFromArgs validates args and builds the
// common.Recommendation to purchase, the effective region to resolve the
// service client against, and the effective dry_run/confirm booleans.
func savingsPlanRecommendationFromArgs(args savingsPlansPurchaseArgs) (rec common.Recommendation, region string, dryRun, confirm bool, err error) {
	spType, term, paymentOption, err := validateSavingsPlanArgs(args)
	if err != nil {
		return common.Recommendation{}, "", false, false, err
	}

	region = args.Region
	if strings.TrimSpace(region) == "" {
		region = savingsPlansAccountLevelRegion
	}

	// Resolve the precise per-plan-type ServiceType (e.g.
	// ServiceSavingsPlansCompute) rather than the ServiceSavingsPlansAll
	// umbrella sentinel, so GetServiceClient returns a client scoped to
	// spType: providers/aws/services/savingsplans/client.go's
	// resolveSPPlanType then rejects a mismatched Details.PlanType instead of
	// silently buying whatever plan type happens to be in Details (defense in
	// depth on top of the ValidateSPType check above).
	service := savingsplans.ServiceTypeForPlanType(spTypes.SavingsPlanType(spType))

	details := &common.SavingsPlanDetails{
		PlanType:         string(spType),
		HourlyCommitment: args.HourlyCommitment,
	}
	// InstanceFamily and Region are only meaningful for EC2Instance plans
	// (common.SavingsPlanDetails documents both as "only populated for
	// EC2Instance"); leaving them unset for Compute/SageMaker/Database keeps
	// that contract instead of leaking a caller-supplied region/family into
	// an account-level, family-agnostic plan's Details.
	if spType == SPTypeEC2Instance {
		details.InstanceFamily = args.InstanceFamily
		details.Region = args.Region
	}

	rec = common.Recommendation{
		Provider:       common.ProviderAWS,
		Service:        service,
		Region:         region,
		CommitmentType: common.CommitmentSavingsPlan,
		Term:           term.RecommendationTerm(),
		PaymentOption:  string(paymentOption),
		Details:        details,
	}

	dryRun, confirm = true, false
	if args.DryRun != nil {
		dryRun = *args.DryRun
	}
	if args.Confirm != nil {
		confirm = *args.Confirm
	}
	return rec, region, dryRun, confirm, nil
}

// validateDatabaseSPConstraints rejects a Database Savings Plan request
// AWS's purchase API would itself reject: per AWS's Database Savings Plans
// announcement (aws.amazon.com/about-aws/whats-new/2025/12/database-savings-plans-savings),
// Database Savings Plans support only a one-year term billed no-upfront --
// unlike Compute, EC2Instance, and SageMaker plans, there is no three-year
// term and no all-upfront/partial-upfront option. Failing loud here, before
// building the recommendation, surfaces AWS's real constraint instead of
// letting a real purchase reach AWS only to be rejected there.
func validateDatabaseSPConstraints(spType SPType, term TermYears, paymentOption PaymentOption) error {
	if spType != SPTypeDatabase {
		return nil
	}
	if term != TermOneYear {
		return fmt.Errorf("sp_type=%s only supports a %d-year term (got term_years=%d): "+
			"AWS Database Savings Plans do not offer a %d-year term",
			SPTypeDatabase, TermOneYear, term, TermThreeYear)
	}
	if paymentOption != PaymentOptionNoUpfront {
		return fmt.Errorf("sp_type=%s only supports payment_option=%s (got %q): "+
			"AWS Database Savings Plans do not offer all-upfront or partial-upfront billing",
			SPTypeDatabase, PaymentOptionNoUpfront, paymentOption)
	}
	return nil
}

func (t *awsSavingsPlansPurchaseTool) resolveClient(args savingsPlansPurchaseArgs, region string, service common.ServiceType) ResolveClientFunc {
	return func(ctx context.Context) (provider.ServiceClient, error) {
		cfg := &provider.ProviderConfig{Name: string(common.ProviderAWS), AWSProfile: args.AWSProfile, Region: region}
		prov, err := t.createProvider(string(common.ProviderAWS), cfg)
		if err != nil {
			return nil, err
		}
		return prov.GetServiceClient(ctx, service, region)
	}
}
