package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/provider"
)

const gcpComputeEngineCUDPurchaseName = "cudly_gcp_computeengine_cud_purchase"

const gcpComputeEngineCUDPurchaseDescription = "Purchase a GCP Compute Engine Committed Use Discount (CUD). THIS " +
	"SPENDS REAL MONEY when dry_run=false and confirm=true. Always call with dry_run=true first (the default) " +
	"to validate your parameters before committing; a dry_run response never contacts GCP and never spends " +
	"money. A CUD commits vCPUs and memory directly (not an instance count): vcpu_count is the number of vCPUs " +
	"and memory_gb is the amount of memory to commit. There is no payment_option parameter: GCP Compute Engine " +
	"CUDs are billed monthly over the term, with no upfront option."

// gcpPaymentOption is the only payment schedule GCP Compute Engine CUDs
// offer, and the only value config.ValidPaymentOptionsByProvider["gcp"]
// accepts. Named here rather than written as a bare literal at the one use
// site so the constraint is stated once, next to its rationale.
const gcpPaymentOption = "monthly"

// gcpComputeEngineCUDPurchaseArgs is the input schema for
// cudly_gcp_computeengine_cud_purchase. memory_gb is required: unlike AWS/
// Azure, providers/gcp/services/computeengine/client.go's buildInsertRequest
// reads Recommendation.Details as a *value* common.ComputeDetails (not a
// pointer, unlike every AWS Details assertion) and hard-errors when
// MemoryGB is absent or <= 0 rather than guessing a vCPU:memory ratio.
type gcpComputeEngineCUDPurchaseArgs struct {
	Region           string  `json:"region" jsonschema:"GCP region, e.g. us-central1"`
	MachineType      string  `json:"machine_type" jsonschema:"GCP machine type family for the commitment, e.g. n2-standard-4"`
	VCPUCount        int     `json:"vcpu_count" jsonschema:"number of vCPUs to commit, must be > 0"`
	MemoryGB         float64 `json:"memory_gb" jsonschema:"amount of memory (GB) to commit, must be > 0"`
	TermYears        int     `json:"term_years" jsonschema:"commitment length in years"`
	GCPProjectID     string  `json:"gcp_project_id,omitempty" jsonschema:"GCP project ID to buy for. Optional for a dry_run preview (the ambient project is used); REQUIRED for a real purchase (dry_run=false, confirm=true), which is refused without it because GCP has no ambient project variable that reliably names the target project"`
	DryRun           *bool   `json:"dry_run,omitempty" jsonschema:"preview only, no purchase; defaults to true"`
	Confirm          *bool   `json:"confirm,omitempty" jsonschema:"required (with dry_run=false) to execute a real purchase; defaults to false"`
	IdempotencyNonce string  `json:"idempotency_nonce,omitempty" jsonschema:"optional; set to a fresh value to authorize a purchase that is otherwise identical to a previous one (e.g. buy 3 more RIs with the same parameters); leave empty (the default) so retries with identical parameters dedupe and never double-buy"`
}

type gcpComputeEngineCUDPurchaseTool struct {
	createProvider func(name string, cfg *provider.ProviderConfig) (provider.Provider, error)
}

// NewGCPComputeEngineCUDPurchaseTool builds the cudly_gcp_computeengine_cud_purchase tool.
func NewGCPComputeEngineCUDPurchaseTool() Registration {
	return &gcpComputeEngineCUDPurchaseTool{createProvider: provider.CreateProvider}
}

func (t *gcpComputeEngineCUDPurchaseTool) Descriptor() Descriptor {
	return Descriptor{
		Name:                gcpComputeEngineCUDPurchaseName,
		Provider:            "gcp",
		Product:             "computeengine",
		Action:              "cud_purchase",
		Description:         gcpComputeEngineCUDPurchaseDescription,
		RealPurchaseEnabled: true,
		ExamplePrompts: []string{
			"Preview a 3-year CUD for 8 vCPUs and 32 GB memory in us-central1",
			"Buy a 1-year Compute Engine CUD for real: 4 vCPUs, 16 GB memory",
		},
	}
}

func (t *gcpComputeEngineCUDPurchaseTool) Register(s *mcp.Server) error {
	schema, err := BuildInputSchema[gcpComputeEngineCUDPurchaseArgs](map[string]FieldOverride{
		"term_years": {Enum: []any{int(TermOneYear), int(TermThreeYear)}},
		"dry_run":    {Default: true},
		"confirm":    {Default: false},
	})
	if err != nil {
		return err
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        gcpComputeEngineCUDPurchaseName,
		Description: gcpComputeEngineCUDPurchaseDescription,
		InputSchema: schema,
	}, t.handle)
	return nil
}

func (t *gcpComputeEngineCUDPurchaseTool) handle(ctx context.Context, _ *mcp.CallToolRequest, args gcpComputeEngineCUDPurchaseArgs) (*mcp.CallToolResult, PurchaseResponse, error) {
	rec, region, dryRun, confirm, err := gcpComputeEngineRecommendationFromArgs(args)
	if err != nil {
		return nil, PurchaseResponse{}, err
	}

	resp, err := ExecutePurchase(ctx, PurchaseRequest{
		Region:         region,
		Recommendation: rec,
		DryRun:         dryRun,
		Confirm:        confirm,
		ResolveClient:  t.resolveClient(args, region),
		Nonce:          args.IdempotencyNonce,
		// No ambient environment fallback here, deliberately, and unlike the
		// AWS ("AWS_PROFILE") and Azure ("AZURE_SUBSCRIPTION_ID") tools. Do
		// not "fix" this asymmetry by adding one: CredentialScope's contract
		// is that its fallback names the SAME variable the provider factory
		// itself consults, so that naming an account explicitly and letting
		// it resolve ambiently derive the same idempotency token. GCP has no
		// such variable. providers/gcp/provider.go's resolveGCPProjectID
		// reads only config.GCPProjectID and the deprecated config.Profile,
		// and when both are empty NewProvider falls through to
		// getDefaultProject, which picks the first ACTIVE project returned by
		// cloudresourcemanager's paginated ListProjects.
		//
		// Adding e.g. GOOGLE_CLOUD_PROJECT to this call alone would make the
		// token LIE: the scope would read that variable while the purchase
		// still landed in whatever project getDefaultProject resolved, so the
		// same target reached two ways would derive two different tokens and
		// double-buy. That is exactly the hazard requireCredentialScope
		// exists to close. Teaching the factory to read it too would fix the
		// divergence but change project selection for every other consumer of
		// providers/gcp (CLI, web, scheduler), and GOOGLE_CLOUD_PROJECT
		// conventionally names the project a process RUNS IN, not the one it
		// should buy for, so on a hosted runtime that silently redirects
		// purchases. Requiring gcp_project_id explicitly is the safe reading:
		// it fails closed with a message naming the argument to pass.
		CredentialScope: CredentialScope(args.GCPProjectID),
	})
	if err != nil {
		return nil, PurchaseResponse{}, err
	}
	return nil, *resp, nil
}

// gcpComputeEngineRecommendationFromArgs validates args and builds the
// common.Recommendation to purchase, the effective region (trimmed of any
// surrounding whitespace), and the effective dry_run/confirm booleans.
// Details is set as a value (common.ComputeDetails{}), not a pointer, to
// match the value type assertion in
// providers/gcp/services/computeengine/client.go's memoryMBFromDetails.
func gcpComputeEngineRecommendationFromArgs(args gcpComputeEngineCUDPurchaseArgs) (rec common.Recommendation, region string, dryRun, confirm bool, err error) {
	region, err = requireNonBlank("region", args.Region)
	if err != nil {
		return common.Recommendation{}, "", false, false, err
	}
	machineType, err := requireNonBlank("machine_type", args.MachineType)
	if err != nil {
		return common.Recommendation{}, "", false, false, err
	}
	if args.VCPUCount <= 0 {
		return common.Recommendation{}, "", false, false, fmt.Errorf("vcpu_count must be > 0, got %d", args.VCPUCount)
	}
	if args.MemoryGB <= 0 {
		return common.Recommendation{}, "", false, false, fmt.Errorf("memory_gb must be > 0, got %v", args.MemoryGB)
	}
	term, err := ValidateTermYears(args.TermYears)
	if err != nil {
		return common.Recommendation{}, "", false, false, err
	}

	rec = common.Recommendation{
		Provider:       common.ProviderGCP,
		Service:        common.ServiceCompute,
		Region:         region,
		ResourceType:   machineType,
		Count:          args.VCPUCount,
		CommitmentType: common.CommitmentCUD,
		Term:           term.RecommendationTerm(),
		// GCP Compute Engine CUDs have exactly one billing schedule: monthly
		// over the term, with no upfront option. There is therefore no
		// payment_option argument on this tool, but PaymentOption is still
		// set explicitly rather than left "", because "" is not neutral
		// downstream: providers/gcp/services/computeengine/client.go's
		// offering-details switch falls through to `default: upfrontCost =
		// totalCost`, reporting the whole commitment as an upfront charge
		// for an empty payment option. gcpPaymentOption is the single value
		// config.ValidPaymentOptionsByProvider["gcp"] recognizes.
		PaymentOption: gcpPaymentOption,
		Details: common.ComputeDetails{
			InstanceType: machineType,
			MemoryGB:     args.MemoryGB,
		},
	}

	dryRun, confirm = ResolveDryRunConfirm(args.DryRun, args.Confirm)
	return rec, region, dryRun, confirm, nil
}

// resolveClient returns the ResolveClientFunc that ExecutePurchase invokes
// only for a real purchase. region is the effective, already-validated-and-
// trimmed region returned by gcpComputeEngineRecommendationFromArgs -- not
// args.Region -- so a real purchase never resolves the provider/service
// client against a raw, un-trimmed value.
func (t *gcpComputeEngineCUDPurchaseTool) resolveClient(args gcpComputeEngineCUDPurchaseArgs, region string) ResolveClientFunc {
	return func(ctx context.Context) (provider.ServiceClient, error) {
		cfg := &provider.ProviderConfig{Name: string(common.ProviderGCP), GCPProjectID: args.GCPProjectID, Region: region}
		prov, err := t.createProvider(string(common.ProviderGCP), cfg)
		if err != nil {
			return nil, err
		}
		return prov.GetServiceClient(ctx, common.ServiceCompute, region)
	}
}
