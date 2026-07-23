package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/provider"
)

const azureComputeRIPurchaseName = "cudly_azure_compute_ri_purchase"

// azureComputeRIPurchaseDescription flags a real, pre-existing gap in
// Azure's purchase API (found while wiring this tool, not introduced by it):
// Azure's purchase body (providers/azure/services/compute/client.go:
// buildReservationBody) never sends a billingPlanType, so every purchase
// uses Azure's default (upfront) billing plan regardless of the
// payment_option requested here. Rather than silently accepting a
// payment_option Azure will not actually honor -- and purchasing under a
// different billing schedule than the caller chose -- this tool requires
// payment_option=all-upfront for a real purchase (dry_run=false,
// confirm=true) and rejects any other value there with an explicit error
// (fail loud, not a silent mismatch; see azureComputeRecommendationFromArgs).
// A dry_run=true preview still validates other payment_option values so a
// caller can rehearse parameters without hitting this rejection.
const azureComputeRIPurchaseDescription = "Purchase an Azure VM Reserved Instance. THIS SPENDS REAL MONEY when " +
	"dry_run=false and confirm=true. Always call with dry_run=true first (the default) to validate your " +
	"parameters before committing; a dry_run response never contacts Azure and never spends money. CAVEAT: " +
	"Azure Reserved Instances only support all-upfront billing -- Azure's purchase API has no billing-plan " +
	"parameter and always bills upfront -- so payment_option must be all-upfront for a real purchase " +
	"(dry_run=false, confirm=true); any other value is rejected there with an error rather than silently " +
	"purchased under a schedule Azure will not honor. A dry_run=true preview still validates other " +
	"payment_option values so a caller can rehearse parameters before learning that."

// azureComputeRIPurchaseArgs is the input schema for
// cudly_azure_compute_ri_purchase. Unlike EC2, Azure's purchase body needs no
// Recommendation.Details -- providers/azure/services/compute/client.go's
// buildReservationBody only reads Region/ResourceType/Count/Term.
type azureComputeRIPurchaseArgs struct {
	Region              string `json:"region" jsonschema:"Azure region, e.g. eastus"`
	VMSize              string `json:"vm_size" jsonschema:"Azure VM size (SKU), e.g. Standard_D2s_v3"`
	Count               int    `json:"count" jsonschema:"number of VM instances to reserve, must be > 0"`
	TermYears           int    `json:"term_years" jsonschema:"commitment length in years"`
	PaymentOption       string `json:"payment_option" jsonschema:"payment schedule; Azure only honors all-upfront, see the CAVEAT in this tool's description"`
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

func azureComputeRecommendationFromArgs(args azureComputeRIPurchaseArgs) (rec common.Recommendation, dryRun, confirm bool, err error) {
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

	dryRun, confirm = true, false
	if args.DryRun != nil {
		dryRun = *args.DryRun
	}
	if args.Confirm != nil {
		confirm = *args.Confirm
	}

	// Azure's purchase API has no billing-plan parameter and always bills
	// upfront (see the azureComputeRIPurchaseDescription doc comment), so a
	// payment_option other than all-upfront would be silently purchased
	// under a schedule the caller never chose. Reusing decidePurchaseMode
	// (the same real-purchase-vs-preview gate ExecutePurchase applies) scopes
	// this rejection to a call that would actually spend money: a preview
	// (dry_run=true) still validates every other parameter so a caller can
	// rehearse a no-upfront/partial-upfront request before learning it can
	// never be honored for real, and a call that's merely missing confirm
	// surfaces that error from ExecutePurchase's shared gate instead of this
	// Azure-specific one.
	if azureRealPurchaseRequiresAllUpfront(dryRun, confirm, paymentOption) {
		return common.Recommendation{}, false, false, fmt.Errorf(
			"azure reserved instances only support all-upfront billing for a real purchase (got payment_option=%q): "+
				"azure's purchase API has no billing-plan parameter and always bills upfront, so any other "+
				"payment_option would be purchased under a different schedule than requested rather than honored",
			paymentOption)
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

// azureRealPurchaseRequiresAllUpfront reports whether the given dry_run and
// confirm flags would drive a real purchase (mode == modeExecute) for a
// paymentOption other than all-upfront, the one case
// azureComputeRecommendationFromArgs must reject (see its doc comment). When
// decidePurchaseMode itself refuses the call (dry_run=false, confirm=false),
// gateErr is non-nil and this reports false: that refusal already surfaces
// from ExecutePurchase's shared gate, so this Azure-specific check must not
// also report a (misleading) all-upfront violation for it.
func azureRealPurchaseRequiresAllUpfront(dryRun, confirm bool, paymentOption PaymentOption) bool {
	mode, gateErr := decidePurchaseMode(dryRun, confirm)
	return gateErr == nil && mode == modeExecute && paymentOption != PaymentOptionAllUpfront
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
