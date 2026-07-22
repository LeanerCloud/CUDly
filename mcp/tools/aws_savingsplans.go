package tools

import (
	"context"
	"fmt"

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
	"(USD/hour), not an instance count."

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
	})
	if err != nil {
		return nil, PurchaseResponse{}, err
	}
	return nil, *resp, nil
}

// savingsPlanRecommendationFromArgs validates args and builds the
// common.Recommendation to purchase, the effective region to resolve the
// service client against, and the effective dry_run/confirm booleans.
func savingsPlanRecommendationFromArgs(args savingsPlansPurchaseArgs) (common.Recommendation, string, bool, bool, error) {
	if args.HourlyCommitment <= 0 {
		return common.Recommendation{}, "", false, false, fmt.Errorf("hourly_commitment must be > 0, got %v", args.HourlyCommitment)
	}
	spType, err := ValidateSPType(args.SPType)
	if err != nil {
		return common.Recommendation{}, "", false, false, err
	}
	term, err := ValidateTermYears(args.TermYears)
	if err != nil {
		return common.Recommendation{}, "", false, false, err
	}
	paymentOption, err := ValidatePaymentOption(args.PaymentOption)
	if err != nil {
		return common.Recommendation{}, "", false, false, err
	}
	if spType == SPTypeEC2Instance && args.Region == "" {
		return common.Recommendation{}, "", false, false, fmt.Errorf("region is required for sp_type=%s", SPTypeEC2Instance)
	}

	region := args.Region
	if region == "" {
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

	rec := common.Recommendation{
		Provider:       common.ProviderAWS,
		Service:        service,
		Region:         region,
		CommitmentType: common.CommitmentSavingsPlan,
		Term:           term.RecommendationTerm(),
		PaymentOption:  string(paymentOption),
		Details: &common.SavingsPlanDetails{
			PlanType:         string(spType),
			HourlyCommitment: args.HourlyCommitment,
			InstanceFamily:   args.InstanceFamily,
			Region:           args.Region,
		},
	}

	dryRun, confirm := true, false
	if args.DryRun != nil {
		dryRun = *args.DryRun
	}
	if args.Confirm != nil {
		confirm = *args.Confirm
	}
	return rec, region, dryRun, confirm, nil
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
