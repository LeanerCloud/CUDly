package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/provider"
)

const awsElastiCacheRIPurchaseName = "cudly_aws_elasticache_ri_purchase"

const awsElastiCacheRIPurchaseDescription = "Purchase AWS ElastiCache Reserved Cache Nodes. THIS SPENDS REAL " +
	"MONEY when dry_run=false and confirm=true. Always call with dry_run=true first (the default) to validate " +
	"your parameters before committing; a dry_run response never contacts AWS and never spends money."

// elasticacheRIPurchaseArgs is the input schema for
// cudly_aws_elasticache_ri_purchase. engine maps onto common.CacheDetails,
// which providers/aws/services/elasticache/client.go:273-283 requires for
// the offering lookup.
type elasticacheRIPurchaseArgs struct {
	Region           string `json:"region" jsonschema:"AWS region, e.g. us-east-1"`
	NodeType         string `json:"node_type" jsonschema:"ElastiCache cache node type, e.g. cache.r6g.large"`
	Count            int    `json:"count" jsonschema:"number of cache nodes to reserve, must be > 0"`
	TermYears        int    `json:"term_years" jsonschema:"commitment length in years"`
	PaymentOption    string `json:"payment_option" jsonschema:"payment schedule"`
	Engine           string `json:"engine" jsonschema:"cache engine"`
	AWSProfile       string `json:"aws_profile,omitempty" jsonschema:"AWS named profile override (~/.aws/config); default uses ambient credentials"`
	DryRun           *bool  `json:"dry_run,omitempty" jsonschema:"preview only, no purchase; defaults to true"`
	Confirm          *bool  `json:"confirm,omitempty" jsonschema:"required (with dry_run=false) to execute a real purchase; defaults to false"`
	IdempotencyNonce string `json:"idempotency_nonce,omitempty" jsonschema:"optional caller-chosen token; passing the SAME value on a retry of this exact call forces the same idempotency key so the provider dedupes it as a retry (e.g. after a network timeout); omitting it (the default) means two calls with otherwise-identical parameters are treated as genuinely separate purchases and get distinct keys"`
}

type awsElastiCacheRIPurchaseTool struct {
	createProvider func(name string, cfg *provider.ProviderConfig) (provider.Provider, error)
}

// NewAWSElastiCacheRIPurchaseTool builds the cudly_aws_elasticache_ri_purchase tool.
func NewAWSElastiCacheRIPurchaseTool() Registration {
	return &awsElastiCacheRIPurchaseTool{createProvider: provider.CreateProvider}
}

func (t *awsElastiCacheRIPurchaseTool) Descriptor() Descriptor {
	return Descriptor{
		Name:                awsElastiCacheRIPurchaseName,
		Provider:            "aws",
		Product:             "elasticache",
		Action:              "ri_purchase",
		Description:         awsElastiCacheRIPurchaseDescription,
		RealPurchaseEnabled: true,
		ExamplePrompts: []string{
			"Preview buying 3 cache.r6g.large redis ElastiCache RIs in us-east-1 for 1 year",
			"Buy an ElastiCache Reserved Cache Node for memcached in eu-west-1 for real",
		},
	}
}

func (t *awsElastiCacheRIPurchaseTool) Register(s *mcp.Server) error {
	schema, err := BuildInputSchema[elasticacheRIPurchaseArgs](map[string]FieldOverride{
		"term_years":     {Enum: []any{int(TermOneYear), int(TermThreeYear)}},
		"payment_option": {Enum: []any{string(PaymentOptionAllUpfront), string(PaymentOptionPartialUpfront), string(PaymentOptionNoUpfront)}},
		"engine":         {Enum: []any{string(CacheEngineRedis), string(CacheEngineMemcached)}},
		"dry_run":        {Default: true},
		"confirm":        {Default: false},
	})
	if err != nil {
		return err
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        awsElastiCacheRIPurchaseName,
		Description: awsElastiCacheRIPurchaseDescription,
		InputSchema: schema,
	}, t.handle)
	return nil
}

func (t *awsElastiCacheRIPurchaseTool) handle(ctx context.Context, _ *mcp.CallToolRequest, args elasticacheRIPurchaseArgs) (*mcp.CallToolResult, PurchaseResponse, error) {
	rec, dryRun, confirm, err := elasticacheRecommendationFromArgs(args)
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

func elasticacheRIPurchaseRequiredFields(args elasticacheRIPurchaseArgs) error {
	if args.Region == "" {
		return fmt.Errorf("region is required")
	}
	if args.NodeType == "" {
		return fmt.Errorf("node_type is required")
	}
	if args.Count <= 0 {
		return fmt.Errorf("count must be > 0, got %d", args.Count)
	}
	return nil
}

// elasticacheRecommendationFromArgs validates args and builds the
// common.Recommendation to purchase, plus the effective dry_run/confirm
// booleans.
func elasticacheRecommendationFromArgs(args elasticacheRIPurchaseArgs) (rec common.Recommendation, dryRun, confirm bool, err error) {
	if fieldErr := elasticacheRIPurchaseRequiredFields(args); fieldErr != nil {
		return common.Recommendation{}, false, false, fieldErr
	}
	term, err := ValidateTermYears(args.TermYears)
	if err != nil {
		return common.Recommendation{}, false, false, err
	}
	paymentOption, err := ValidatePaymentOption(args.PaymentOption)
	if err != nil {
		return common.Recommendation{}, false, false, err
	}
	engine, err := ValidateCacheEngine(args.Engine)
	if err != nil {
		return common.Recommendation{}, false, false, err
	}

	rec = common.Recommendation{
		Provider:       common.ProviderAWS,
		Service:        common.ServiceElastiCache,
		Region:         args.Region,
		ResourceType:   args.NodeType,
		Count:          args.Count,
		CommitmentType: common.CommitmentReservedInstance,
		Term:           term.RecommendationTerm(),
		PaymentOption:  string(paymentOption),
		Details: &common.CacheDetails{
			Engine:   string(engine),
			NodeType: args.NodeType,
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

func (t *awsElastiCacheRIPurchaseTool) resolveClient(args elasticacheRIPurchaseArgs) ResolveClientFunc {
	return func(ctx context.Context) (provider.ServiceClient, error) {
		cfg := &provider.ProviderConfig{Name: string(common.ProviderAWS), AWSProfile: args.AWSProfile, Region: args.Region}
		prov, err := t.createProvider(string(common.ProviderAWS), cfg)
		if err != nil {
			return nil, err
		}
		return prov.GetServiceClient(ctx, common.ServiceElastiCache, args.Region)
	}
}
