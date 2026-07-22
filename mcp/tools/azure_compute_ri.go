package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/provider"
)

const azureComputeRIPurchaseName = "cudly_azure_compute_ri_purchase"

// azureComputeRIPurchaseDescription flags a real, currently-unfixed gap
// (found while wiring this tool, not introduced by it): Azure's purchase
// body (providers/azure/services/compute/client.go:buildReservationBody)
// never sends a billingPlanType, so every purchase uses Azure's default
// (upfront) billing plan regardless of the payment_option requested here --
// payment_option only affects the displayed cost estimate, not the actual
// invoice. Flagged rather than silently fixed (out of scope for this PR).
const azureComputeRIPurchaseDescription = "Purchase an Azure VM Reserved Instance. THIS SPENDS REAL MONEY when " +
	"dry_run=false and confirm=true. Always call with dry_run=true first (the default) to validate your " +
	"parameters before committing; a dry_run response never contacts Azure and never spends money. CAVEAT: " +
	"Azure's purchase API always uses the default (upfront) billing plan today -- payment_option only affects " +
	"the cost estimate shown here, not the actual invoice; this is a pre-existing gap, not something this tool " +
	"controls."

// azureComputeRIPurchaseArgs is the input schema for
// cudly_azure_compute_ri_purchase. Unlike EC2, Azure's purchase body needs no
// Recommendation.Details -- providers/azure/services/compute/client.go's
// buildReservationBody only reads Region/ResourceType/Count/Term.
type azureComputeRIPurchaseArgs struct {
	Region              string `json:"region" jsonschema:"Azure region, e.g. eastus"`
	VMSize              string `json:"vm_size" jsonschema:"Azure VM size (SKU), e.g. Standard_D2s_v3"`
	Count               int    `json:"count" jsonschema:"number of VM instances to reserve, must be > 0"`
	TermYears           int    `json:"term_years" jsonschema:"commitment length in years"`
	PaymentOption       string `json:"payment_option" jsonschema:"payment schedule; see the CAVEAT in this tool's description"`
	AzureSubscriptionID string `json:"azure_subscription_id,omitempty" jsonschema:"Azure subscription ID override; default uses AZURE_SUBSCRIPTION_ID"`
	DryRun              *bool  `json:"dry_run,omitempty" jsonschema:"preview only, no purchase; defaults to true"`
	Confirm             *bool  `json:"confirm,omitempty" jsonschema:"required (with dry_run=false) to execute a real purchase; defaults to false"`
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
		"term_years":     {Enum: []any{int(TermOneYear), int(TermThreeYear)}},
		"payment_option": {Enum: []any{string(PaymentOptionAllUpfront), string(PaymentOptionPartialUpfront), string(PaymentOptionNoUpfront)}},
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
	})
	if err != nil {
		return nil, PurchaseResponse{}, err
	}
	return nil, *resp, nil
}

func azureComputeRecommendationFromArgs(args azureComputeRIPurchaseArgs) (common.Recommendation, bool, bool, error) {
	if args.Region == "" {
		return common.Recommendation{}, false, false, fmt.Errorf("region is required")
	}
	if args.VMSize == "" {
		return common.Recommendation{}, false, false, fmt.Errorf("vm_size is required")
	}
	if args.Count <= 0 {
		return common.Recommendation{}, false, false, fmt.Errorf("count must be > 0, got %d", args.Count)
	}
	term, err := ValidateTermYears(args.TermYears)
	if err != nil {
		return common.Recommendation{}, false, false, err
	}
	paymentOption, err := ValidatePaymentOption(args.PaymentOption)
	if err != nil {
		return common.Recommendation{}, false, false, err
	}

	rec := common.Recommendation{
		Provider:       common.ProviderAzure,
		Service:        common.ServiceCompute,
		Region:         args.Region,
		ResourceType:   args.VMSize,
		Count:          args.Count,
		CommitmentType: common.CommitmentReservedInstance,
		Term:           term.RecommendationTerm(),
		PaymentOption:  string(paymentOption),
	}

	dryRun, confirm := true, false
	if args.DryRun != nil {
		dryRun = *args.DryRun
	}
	if args.Confirm != nil {
		confirm = *args.Confirm
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
