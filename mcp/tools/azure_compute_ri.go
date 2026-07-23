package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/provider"
)

const azureComputeRIPurchaseName = "cudly_azure_compute_ri_purchase"

// azureComputeRIPurchaseDescription documents Azure's actual billing-plan
// contract: providers/azure/services/compute/client.go's buildReservationBody
// sends properties.billingPlan (armreservations.ReservationBillingPlan --
// Upfront or Monthly), so both all-upfront and no-upfront purchases are
// honored for real. Monthly costs the same total as Upfront -- Azure has no
// premium for spreading payments -- but there is no partial-upfront billing
// plan at all, so that value is rejected with an explicit error rather than
// silently purchased under a different schedule (see
// azureComputeRecommendationFromArgs).
const azureComputeRIPurchaseDescription = "Purchase an Azure VM Reserved Instance. THIS SPENDS REAL MONEY when " +
	"dry_run=false and confirm=true. Always call with dry_run=true first (the default) to validate your " +
	"parameters before committing; a dry_run response never contacts Azure and never spends money. Azure " +
	"Reserved Instances support two billing plans: all-upfront and no-upfront (billed monthly, same total " +
	"price as all-upfront -- Azure charges no premium for spreading payments). payment_option defaults to " +
	"no-upfront when omitted. Azure has no partial-upfront billing plan, so that value is rejected with an " +
	"explicit error rather than silently purchased under all-upfront or no-upfront instead."

// azureComputeRIPurchaseArgs is the input schema for
// cudly_azure_compute_ri_purchase. Unlike EC2, Azure's purchase body needs no
// Recommendation.Details -- providers/azure/services/compute/client.go's
// buildReservationBody only reads Region/ResourceType/Count/Term/PaymentOption.
type azureComputeRIPurchaseArgs struct {
	Region              string `json:"region" jsonschema:"Azure region, e.g. eastus"`
	VMSize              string `json:"vm_size" jsonschema:"Azure VM size (SKU), e.g. Standard_D2s_v3"`
	Count               int    `json:"count" jsonschema:"number of VM instances to reserve, must be > 0"`
	TermYears           int    `json:"term_years" jsonschema:"commitment length in years"`
	PaymentOption       string `json:"payment_option,omitempty" jsonschema:"payment schedule; Azure honors all-upfront and no-upfront (monthly, same total price); no partial-upfront; defaults to no-upfront"`
	AzureSubscriptionID string `json:"azure_subscription_id,omitempty" jsonschema:"Azure subscription ID override; default uses AZURE_SUBSCRIPTION_ID"`
	DryRun              *bool  `json:"dry_run,omitempty" jsonschema:"preview only, no purchase; defaults to true"`
	Confirm             *bool  `json:"confirm,omitempty" jsonschema:"required (with dry_run=false) to execute a real purchase; defaults to false"`
	IdempotencyNonce    string `json:"idempotency_nonce,omitempty" jsonschema:"optional; set to a fresh value to authorize a purchase that is otherwise identical to a previous one (e.g. buy 3 more RIs with the same parameters); leave empty (the default) so retries with identical parameters dedupe and never double-buy"`
}

type azureComputeRIPurchaseTool struct {
	createProvider func(name string, cfg *provider.ProviderConfig) (provider.Provider, error)
}

// NewAzureComputeRIPurchaseTool builds the cudly_azure_compute_ri_purchase tool.
func NewAzureComputeRIPurchaseTool() Registration {
	return &azureComputeRIPurchaseTool{createProvider: provider.CreateProvider}
}

func (t *azureComputeRIPurchaseTool) Descriptor() Descriptor {
	return Descriptor{
		Name:                azureComputeRIPurchaseName,
		Provider:            "azure",
		Product:             "compute",
		Action:              "ri_purchase",
		Description:         azureComputeRIPurchaseDescription,
		RealPurchaseEnabled: true,
		ExamplePrompts: []string{
			"Preview buying 2 Standard_D2s_v3 Azure VM RIs in eastus for 3 years",
			"Buy an Azure VM Reserved Instance for real in westeurope",
		},
	}
}

func (t *azureComputeRIPurchaseTool) Register(s *mcp.Server) error {
	schema, err := BuildInputSchema[azureComputeRIPurchaseArgs](map[string]FieldOverride{
		"term_years": {Enum: []any{int(TermOneYear), int(TermThreeYear)}},
		// Azure has no partial-upfront billing plan (see
		// azureComputeRIPurchaseDescription and azureComputeRecommendationFromArgs
		// below), so this tool's schema advertises only the two values Azure
		// actually honors. The runtime check in azureComputeRecommendationFromArgs
		// still rejects partial-upfront explicitly, as defense in depth for a
		// caller that bypasses the schema.
		"payment_option": {Enum: []any{string(PaymentOptionAllUpfront), string(PaymentOptionNoUpfront)}, Default: string(PaymentOptionNoUpfront)},
		"dry_run":        {Default: true},
		"confirm":        {Default: false},
	})
	if err != nil {
		return err
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        azureComputeRIPurchaseName,
		Description: azureComputeRIPurchaseDescription,
		InputSchema: schema,
	}, t.handle)
	return nil
}

func (t *azureComputeRIPurchaseTool) handle(ctx context.Context, _ *mcp.CallToolRequest, args azureComputeRIPurchaseArgs) (*mcp.CallToolResult, PurchaseResponse, error) {
	rec, dryRun, confirm, err := azureComputeRecommendationFromArgs(args)
	if err != nil {
		return nil, PurchaseResponse{}, err
	}

	resp, err := ExecutePurchase(ctx, PurchaseRequest{
		Region:         args.Region,
		Recommendation: rec,
		DryRun:         dryRun,
		Confirm:        confirm,
		ResolveClient:  t.resolveClient(args),
		Nonce:          args.IdempotencyNonce,
	})
	if err != nil {
		return nil, PurchaseResponse{}, err
	}
	return nil, *resp, nil
}

func azureComputeRecommendationFromArgs(args azureComputeRIPurchaseArgs) (rec common.Recommendation, dryRun, confirm bool, err error) {
	if fieldErr := requireNonBlank("region", args.Region); fieldErr != nil {
		return common.Recommendation{}, false, false, fieldErr
	}
	if fieldErr := requireNonBlank("vm_size", args.VMSize); fieldErr != nil {
		return common.Recommendation{}, false, false, fieldErr
	}
	if args.Count <= 0 {
		return common.Recommendation{}, false, false, fmt.Errorf("count must be > 0, got %d", args.Count)
	}
	term, err := ValidateTermYears(args.TermYears)
	if err != nil {
		return common.Recommendation{}, false, false, err
	}

	// payment_option defaults to no-upfront (matching the CLI's --payment
	// default, cmd/main.go) when the caller omits it -- an omitted string
	// field arrives as "" and is never confused with an explicit,
	// unrecognized value (feedback_no_silent_fallbacks: the default is
	// applied here, explicitly, not fabricated deeper in the stack).
	paymentOptionStr := args.PaymentOption
	if paymentOptionStr == "" {
		paymentOptionStr = string(PaymentOptionNoUpfront)
	}
	paymentOption, err := ValidatePaymentOption(paymentOptionStr)
	if err != nil {
		return common.Recommendation{}, false, false, err
	}
	// Azure reservations support exactly two billing plans (Upfront,
	// Monthly -- see providers/azure/services/internal/reservations.
	// BillingPlanForPaymentOption); there is no partial-upfront at any
	// layer of Azure's API. Rejecting it here, unconditionally (not just
	// for a real purchase), means a dry_run preview never reports success
	// for a request that could never be honored for real.
	if paymentOption == PaymentOptionPartialUpfront {
		return common.Recommendation{}, false, false, fmt.Errorf(
			"azure reservations do not support payment_option=%q: azure billing plans are all-upfront or "+
				"no-upfront (monthly, same total price) only, with no partial-upfront option",
			paymentOption)
	}

	dryRun, confirm = true, false
	if args.DryRun != nil {
		dryRun = *args.DryRun
	}
	if args.Confirm != nil {
		confirm = *args.Confirm
	}

	rec = common.Recommendation{
		Provider:       common.ProviderAzure,
		Service:        common.ServiceCompute,
		Region:         args.Region,
		ResourceType:   args.VMSize,
		Count:          args.Count,
		CommitmentType: common.CommitmentReservedInstance,
		Term:           term.RecommendationTerm(),
		PaymentOption:  string(paymentOption),
	}

	return rec, dryRun, confirm, nil
}

func (t *azureComputeRIPurchaseTool) resolveClient(args azureComputeRIPurchaseArgs) ResolveClientFunc {
	return func(ctx context.Context) (provider.ServiceClient, error) {
		cfg := &provider.ProviderConfig{Name: string(common.ProviderAzure), AzureSubscriptionID: args.AzureSubscriptionID, Region: args.Region}
		prov, err := t.createProvider(string(common.ProviderAzure), cfg)
		if err != nil {
			return nil, err
		}
		return prov.GetServiceClient(ctx, common.ServiceCompute, args.Region)
	}
}
