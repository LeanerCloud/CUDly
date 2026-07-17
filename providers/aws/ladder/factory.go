package ladder

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	sdksp "github.com/aws/aws-sdk-go-v2/service/savingsplans"
	sptypes "github.com/aws/aws-sdk-go-v2/service/savingsplans/types"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/exchange"
	pkgladder "github.com/LeanerCloud/CUDly/pkg/ladder"
	"github.com/LeanerCloud/CUDly/providers/aws/recommendations"
	ec2svc "github.com/LeanerCloud/CUDly/providers/aws/services/ec2"
	savingsplansvc "github.com/LeanerCloud/CUDly/providers/aws/services/savingsplans"
)

// NewFromAWSConfig constructs a fully wired AWSLadder for the given region and
// accountID. All seven read-side data-source adapters are wired to real AWS
// clients so that scheduled ladder runs produce meaningful plans.
//
// Pre-L2 behaviour: every source was a no-op stub; GetUsageBaseline always
// errored; every run was recorded as Errored with no plan produced.
//
// Client wiring (7 sources):
//
//   - riLister          : ec2svc.Client.ListConvertibleReservedInstances
//   - spLister          : spListerAdapter wrapping *sdksp.Client.DescribeSavingsPlans
//   - riCoverageSource  : recommendations.Client.GetRICoverageMap
//   - onDemandSeries    : onDemandSeriesAdapter wrapping recommendations.Client.GetOnDemandSeries
//     (CE GetCostAndUsage; maps recommendations.DailyCost to DailyPoint)
//   - utilizationSource : recommendations.Client.GetRIUtilization
//   - spCoverageSource  : spCoverageAdapter wrapping recommendations.Client.GetSPCoverageSummary
//   - spUtilizationSource: spUtilizationAdapter wrapping recommendations.Client.GetSPUtilization
//
// The write side (PurchaseLayer / ReshapeBuffer) remains unwired; all writes
// are rejected with errWriteNotWired. Write-side wiring arrives in L6.
//
// NewFromAWSConfig matches the LadderCapabilityFactory type on Application so
// it can be assigned directly:
//
//	app.LadderCapabilityFactory = awsladder.NewFromAWSConfig
func NewFromAWSConfig(ctx context.Context, region, accountID string) (pkgladder.LadderCapability, error) {
	if region == "" {
		return nil, fmt.Errorf("awsladder.NewFromAWSConfig: region must not be empty")
	}
	if accountID == "" {
		return nil, fmt.Errorf("awsladder.NewFromAWSConfig: accountID must not be empty")
	}

	// Load the ambient AWS credentials (Lambda execution role or env vars).
	// The ladder region is set here so EC2 and Savings Plans clients use the
	// correct regional endpoint. recommendations.NewClient overrides the region
	// for Cost Explorer (always us-east-1) internally.
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(region),
	)
	if err != nil {
		return nil, fmt.Errorf("awsladder.NewFromAWSConfig: load AWS config: %w", err)
	}

	// recommendations.Client satisfies riCoverageSource and utilizationSource
	// directly, and is the underlying client for the on-demand series and SP
	// coverage/utilization adapters.
	recoClient := recommendations.NewClient(&awsCfg)

	// ec2svc.Client satisfies riLister (ListConvertibleReservedInstances).
	ec2Client := ec2svc.NewClient(awsCfg)

	// sdksp.Client satisfies activeSPListAPI (DescribeSavingsPlans), which is the
	// narrow interface spListerAdapter expects (interface-segregation: we do not
	// need the offering and purchase methods the full SavingsPlansAPI exposes).
	spSDKClient := sdksp.NewFromConfig(awsCfg)

	cfg := Config{
		Region:    region,
		AccountID: accountID,
	}
	l, err := New(
		cfg,
		ec2Client, // riLister
		&spListerAdapter{api: spSDKClient, region: region}, // spLister (region-scoped)
		recoClient, // riCoverageSource
		&onDemandSeriesAdapter{client: recoClient}, // onDemandSeriesSource
		recoClient,                                // utilizationSource
		&spCoverageAdapter{client: recoClient},    // spCoverageSource
		&spUtilizationAdapter{client: recoClient}, // spUtilizationSource
	)
	if err != nil {
		return nil, fmt.Errorf("awsladder.NewFromAWSConfig: %w", err)
	}
	return l, nil
}

// disabledPurchaser is the riPurchaser / spPurchaser implementation used when
// ladder_execution_enabled=false. Every call returns ErrLadderExecutionDisabled
// without touching any AWS API.
type disabledPurchaser struct{}

func (disabledPurchaser) PurchaseCommitment(_ context.Context, _ common.Recommendation, _ common.PurchaseOptions) (common.PurchaseResult, error) {
	return common.PurchaseResult{}, fmt.Errorf("%w: purchase blocked by kill-switch", ErrLadderExecutionDisabled)
}

// disabledExchangeRunner is the exchangeRunner implementation used when
// ladder_execution_enabled=false. Every call returns ErrLadderExecutionDisabled
// without touching any AWS or DB API.
type disabledExchangeRunner struct{}

func (disabledExchangeRunner) RunAutoExchange(_ context.Context, _ exchange.RIExchangeConfig, _ *string, _ bool) (*exchange.AutoExchangeResult, error) {
	return nil, fmt.Errorf("%w: exchange blocked by kill-switch", ErrLadderExecutionDisabled)
}

// WireWriteSideDisabled wires the ladder's write side with disabled
// implementations that return ErrLadderExecutionDisabled on every call.
// Use this when ladder_execution_enabled=false in global_config: the
// ladder is wired (errWriteNotWired is not returned) but executes nothing
// and allows callers to detect the disabled state via errors.Is.
func WireWriteSideDisabled(l *AWSLadder) (*AWSLadder, error) {
	return l.WithWriteSide(disabledPurchaser{}, disabledPurchaser{}, disabledExchangeRunner{})
}

// WireWriteSide wires the ladder's write side with real AWS SDK clients.
// Use this when ladder_execution_enabled=true in global_config.
// ex is the exchangeRunner (typically *internal/server.exchangeRunnerAdapter)
// which owns the exchange store, EC2 exchange client, and offering lookup.
// The savingsplans client is created in umbrella mode (empty planType) so a
// single purchaser serves both EC2Instance and Compute SP layers; plan-type
// routing happens at purchase time via resolveSPPlanType.
func WireWriteSide(l *AWSLadder, awsCfg aws.Config, ex exchangeRunner) (*AWSLadder, error) {
	riP := ec2svc.NewClient(awsCfg)
	spP := savingsplansvc.NewClient(awsCfg, sptypes.SavingsPlanType(""))
	return l.WithWriteSide(riP, spP, ex)
}
