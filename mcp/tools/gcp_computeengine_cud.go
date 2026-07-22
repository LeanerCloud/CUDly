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
	"and memory_gb is the amount of memory to commit."

// gcpComputeEngineCUDPurchaseArgs is the input schema for
// cudly_gcp_computeengine_cud_purchase. memory_gb is required: unlike AWS/
// Azure, providers/gcp/services/computeengine/client.go's buildInsertRequest
// reads Recommendation.Details as a *value* common.ComputeDetails (not a
// pointer, unlike every AWS Details assertion) and hard-errors when
// MemoryGB is absent or <= 0 rather than guessing a vCPU:memory ratio.
type gcpComputeEngineCUDPurchaseArgs struct {
	Region       string  `json:"region" jsonschema:"GCP region, e.g. us-central1"`
	MachineType  string  `json:"machine_type" jsonschema:"GCP machine type family for the commitment, e.g. n2-standard-4"`
	VCPUCount    int     `json:"vcpu_count" jsonschema:"number of vCPUs to commit, must be > 0"`
	MemoryGB     float64 `json:"memory_gb" jsonschema:"amount of memory (GB) to commit, must be > 0"`
	TermYears    int     `json:"term_years" jsonschema:"commitment length in years"`
	GCPProjectID string  `json:"gcp_project_id,omitempty" jsonschema:"GCP project ID override; default uses ambient project"`
	DryRun       *bool   `json:"dry_run,omitempty" jsonschema:"preview only, no purchase; defaults to true"`
	Confirm      *bool   `json:"confirm,omitempty" jsonschema:"required (with dry_run=false) to execute a real purchase; defaults to false"`
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
	rec, dryRun, confirm, err := gcpComputeEngineRecommendationFromArgs(args)
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

func gcpCUDPurchaseRequiredFields(args gcpComputeEngineCUDPurchaseArgs) error {
	if args.Region == "" {
		return fmt.Errorf("region is required")
	}
	if args.MachineType == "" {
		return fmt.Errorf("machine_type is required")
	}
	if args.VCPUCount <= 0 {
		return fmt.Errorf("vcpu_count must be > 0, got %d", args.VCPUCount)
	}
	if args.MemoryGB <= 0 {
		return fmt.Errorf("memory_gb must be > 0, got %v", args.MemoryGB)
	}
	return nil
}

// gcpComputeEngineRecommendationFromArgs validates args and builds the
// common.Recommendation to purchase, plus the effective dry_run/confirm
// booleans. Details is set as a value (common.ComputeDetails{}), not a
// pointer, to match the value type assertion in
// providers/gcp/services/computeengine/client.go's memoryMBFromDetails.
func gcpComputeEngineRecommendationFromArgs(args gcpComputeEngineCUDPurchaseArgs) (rec common.Recommendation, dryRun, confirm bool, err error) {
	if fieldErr := gcpCUDPurchaseRequiredFields(args); fieldErr != nil {
		return common.Recommendation{}, false, false, fieldErr
	}
	term, err := ValidateTermYears(args.TermYears)
	if err != nil {
		return common.Recommendation{}, false, false, err
	}

	rec = common.Recommendation{
		Provider:       common.ProviderGCP,
		Service:        common.ServiceCompute,
		Region:         args.Region,
		ResourceType:   args.MachineType,
		Count:          args.VCPUCount,
		CommitmentType: common.CommitmentCUD,
		Term:           term.RecommendationTerm(),
		Details: common.ComputeDetails{
			InstanceType: args.MachineType,
			MemoryGB:     args.MemoryGB,
		},
	}

	dryRun, confirm = true, false
	if args.DryRun != nil {
		dryRun = *args.DryRun
	}
	if args.Confirm != nil {
		confirm = *args.Confirm
	}
	return rec, dryRun, confirm, nil
}

func (t *gcpComputeEngineCUDPurchaseTool) resolveClient(args gcpComputeEngineCUDPurchaseArgs) ResolveClientFunc {
	return func(ctx context.Context) (provider.ServiceClient, error) {
		cfg := &provider.ProviderConfig{Name: string(common.ProviderGCP), GCPProjectID: args.GCPProjectID, Region: args.Region}
		prov, err := t.createProvider(string(common.ProviderGCP), cfg)
		if err != nil {
			return nil, err
		}
		return prov.GetServiceClient(ctx, common.ServiceCompute, args.Region)
	}
}
