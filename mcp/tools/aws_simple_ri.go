package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/provider"
)

// simpleAWSRIPurchaseSpec configures a region+resource_type+count+term+
// payment_option AWS RI purchase tool -- the shape shared by OpenSearch,
// Redshift, and MemoryDB. None of their PurchaseCommitment implementations
// read rec.Details (providers/aws/services/{opensearch,redshift,memorydb}/
// client.go), unlike EC2 (ComputeDetails) or RDS/ElastiCache (Database/
// CacheDetails), so one generic tool type serves all three rather than
// three near-identical copies.
type simpleAWSRIPurchaseSpec struct {
	name             string
	product          string
	displayName      string // human-readable product name for the tool description, e.g. "OpenSearch"
	service          common.ServiceType
	resourceTypeDesc string // jsonschema description for the resource_type field
	examplePrompts   []string
}

// simpleAWSRIPurchaseArgs is the input schema shared by every
// simpleAWSRIPurchaseTool instance.
type simpleAWSRIPurchaseArgs struct {
	Region           string `json:"region" jsonschema:"AWS region, e.g. us-east-1"`
	ResourceType     string `json:"resource_type" jsonschema:"resource/node type to reserve"`
	Count            int    `json:"count" jsonschema:"number of nodes/instances to reserve, must be > 0"`
	TermYears        int    `json:"term_years" jsonschema:"commitment length in years"`
	PaymentOption    string `json:"payment_option" jsonschema:"payment schedule"`
	AWSProfile       string `json:"aws_profile,omitempty" jsonschema:"AWS named profile override (~/.aws/config); default uses ambient credentials"`
	DryRun           *bool  `json:"dry_run,omitempty" jsonschema:"preview only, no purchase; defaults to true"`
	Confirm          *bool  `json:"confirm,omitempty" jsonschema:"required (with dry_run=false) to execute a real purchase; defaults to false"`
	IdempotencyNonce string `json:"idempotency_nonce,omitempty" jsonschema:"optional caller-chosen token; passing the SAME value on a retry of this exact call forces the same idempotency key so the provider dedupes it as a retry (e.g. after a network timeout); omitting it (the default) means two calls with otherwise-identical parameters are treated as genuinely separate purchases and get distinct keys"`
}

type simpleAWSRIPurchaseTool struct {
	spec           simpleAWSRIPurchaseSpec
	createProvider func(name string, cfg *provider.ProviderConfig) (provider.Provider, error)
}

// newSimpleAWSRIPurchaseTool builds a Registration for spec.
func newSimpleAWSRIPurchaseTool(spec simpleAWSRIPurchaseSpec) Registration {
	return &simpleAWSRIPurchaseTool{spec: spec, createProvider: provider.CreateProvider}
}

// NewAWSOpenSearchRIPurchaseTool builds cudly_aws_opensearch_ri_purchase.
func NewAWSOpenSearchRIPurchaseTool() Registration {
	return newSimpleAWSRIPurchaseTool(simpleAWSRIPurchaseSpec{
		name:             "cudly_aws_opensearch_ri_purchase",
		product:          "opensearch",
		displayName:      "OpenSearch",
		service:          common.ServiceOpenSearch,
		resourceTypeDesc: "OpenSearch instance type, e.g. r6g.large.search",
		examplePrompts: []string{
			"Preview buying 2 r6g.large.search OpenSearch RIs in us-east-1 for 1 year",
			"Buy an OpenSearch Reserved Instance in eu-west-1 for real",
		},
	})
}

// NewAWSRedshiftRIPurchaseTool builds cudly_aws_redshift_ri_purchase.
func NewAWSRedshiftRIPurchaseTool() Registration {
	return newSimpleAWSRIPurchaseTool(simpleAWSRIPurchaseSpec{
		name:             "cudly_aws_redshift_ri_purchase",
		product:          "redshift",
		displayName:      "Redshift",
		service:          common.ServiceRedshift,
		resourceTypeDesc: "Redshift node type, e.g. dc2.large",
		examplePrompts: []string{
			"Preview buying 4 dc2.large Redshift RIs in us-east-1 for 3 years, all-upfront",
		},
	})
}

// NewAWSMemoryDBRIPurchaseTool builds cudly_aws_memorydb_ri_purchase.
func NewAWSMemoryDBRIPurchaseTool() Registration {
	return newSimpleAWSRIPurchaseTool(simpleAWSRIPurchaseSpec{
		name:             "cudly_aws_memorydb_ri_purchase",
		product:          "memorydb",
		displayName:      "MemoryDB",
		service:          common.ServiceMemoryDB,
		resourceTypeDesc: "MemoryDB node type, e.g. db.r6g.large",
		examplePrompts: []string{
			"Preview buying 2 db.r6g.large MemoryDB RIs in us-east-1",
		},
	})
}

func (t *simpleAWSRIPurchaseTool) Descriptor() Descriptor {
	return Descriptor{
		Name:     t.spec.name,
		Provider: "aws",
		Product:  t.spec.product,
		Action:   "ri_purchase",
		Description: fmt.Sprintf(
			"Purchase AWS %s Reserved Instances. THIS SPENDS REAL MONEY when dry_run=false and confirm=true. "+
				"Always call with dry_run=true first (the default) to validate your parameters before "+
				"committing; a dry_run response never contacts AWS and never spends money.",
			t.spec.displayName),
		RealPurchaseEnabled: true,
		ExamplePrompts:      t.spec.examplePrompts,
	}
}

func (t *simpleAWSRIPurchaseTool) Register(s *mcp.Server) error {
	desc := t.Descriptor().Description
	schema, err := BuildInputSchema[simpleAWSRIPurchaseArgs](map[string]FieldOverride{
		"term_years":     {Enum: []any{int(TermOneYear), int(TermThreeYear)}},
		"payment_option": {Enum: []any{string(PaymentOptionAllUpfront), string(PaymentOptionPartialUpfront), string(PaymentOptionNoUpfront)}},
		"dry_run":        {Default: true},
		"confirm":        {Default: false},
	})
	if err != nil {
		return err
	}
	// resource_type's description is spec-specific (differs per product),
	// so it is set directly rather than through a generic FieldOverride.
	if prop, ok := schema.Properties["resource_type"]; ok {
		prop.Description = t.spec.resourceTypeDesc
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        t.spec.name,
		Description: desc,
		InputSchema: schema,
	}, t.handle)
	return nil
}

func (t *simpleAWSRIPurchaseTool) handle(ctx context.Context, _ *mcp.CallToolRequest, args simpleAWSRIPurchaseArgs) (*mcp.CallToolResult, PurchaseResponse, error) {
	rec, dryRun, confirm, err := t.recommendationFromArgs(args)
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

func (t *simpleAWSRIPurchaseTool) recommendationFromArgs(args simpleAWSRIPurchaseArgs) (rec common.Recommendation, dryRun, confirm bool, err error) {
	if args.Region == "" {
		return common.Recommendation{}, false, false, fmt.Errorf("region is required")
	}
	if args.ResourceType == "" {
		return common.Recommendation{}, false, false, fmt.Errorf("resource_type is required")
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

	rec = common.Recommendation{
		Provider:       common.ProviderAWS,
		Service:        t.spec.service,
		Region:         args.Region,
		ResourceType:   args.ResourceType,
		Count:          args.Count,
		CommitmentType: common.CommitmentReservedInstance,
		Term:           term.RecommendationTerm(),
		PaymentOption:  string(paymentOption),
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

func (t *simpleAWSRIPurchaseTool) resolveClient(args simpleAWSRIPurchaseArgs) ResolveClientFunc {
	return func(ctx context.Context) (provider.ServiceClient, error) {
		cfg := &provider.ProviderConfig{Name: string(common.ProviderAWS), AWSProfile: args.AWSProfile, Region: args.Region}
		prov, err := t.createProvider(string(common.ProviderAWS), cfg)
		if err != nil {
			return nil, err
		}
		return prov.GetServiceClient(ctx, t.spec.service, args.Region)
	}
}
