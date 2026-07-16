package server

import (
	"context"
	"errors"
	"fmt"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"

	"github.com/LeanerCloud/CUDly/pkg/exchange"
	pkgladder "github.com/LeanerCloud/CUDly/pkg/ladder"
	awsprovider "github.com/LeanerCloud/CUDly/providers/aws"
	awsladder "github.com/LeanerCloud/CUDly/providers/aws/ladder"
	ec2svc "github.com/LeanerCloud/CUDly/providers/aws/services/ec2"
)

// exchangeRunnerAdapter bridges internal/server wiring (exchange store, EC2 exchange
// client, offering lookup) with the exchangeRunner seam expected by AWSLadder. It
// satisfies the unexported providers/aws/ladder.exchangeRunner interface via Go
// structural typing: the concrete RunAutoExchange method signature matches the
// interface definition, so the compiler accepts this type wherever exchangeRunner
// is expected without the caller naming the interface.
//
// The adapter owns the full client construction and conversion that
// executeRIExchangeReshape performs for the standalone RI-exchange path, adapted
// for ladder runs: LadderRunID and DryRun are forwarded from the seam arguments
// directly into RunAutoExchangeParams.
type exchangeRunnerAdapter struct {
	app       *Application
	region    string
	accountID string
}

// RunAutoExchange implements providers/aws/ladder.exchangeRunner. It constructs
// fresh AWS clients, lists convertible RIs and utilization, converts them for the
// exchange package, then delegates to exchange.RunAutoExchange.
//
// ladderRunID is forwarded to RunAutoExchangeParams.LadderRunID so exchange scopes
// its pending-cancellation to the ladder origin (issue #1348 / gap G10). DryRun
// is forwarded so the exchange engine skips all mutations and returns Simulated
// outcomes when the ladder run was started in dry-run mode.
func (a *exchangeRunnerAdapter) RunAutoExchange(ctx context.Context, cfg exchange.RIExchangeConfig, ladderRunID *string, dryRun bool) (*exchange.AutoExchangeResult, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(a.region))
	if err != nil {
		return nil, fmt.Errorf("exchangeRunnerAdapter: load AWS config: %w", err)
	}

	ec2Client := awsprovider.NewEC2ClientDirect(awsCfg)
	recsClient := awsprovider.NewRecommendationsClientDirect(awsCfg)

	instances, err := ec2Client.ListConvertibleReservedInstances(ctx)
	if err != nil {
		return nil, fmt.Errorf("exchangeRunnerAdapter: list convertible RIs: %w", err)
	}
	utilData, err := recsClient.GetRIUtilization(ctx, cfg.LookbackDays)
	if err != nil {
		return nil, fmt.Errorf("exchangeRunnerAdapter: get RI utilization: %w", err)
	}

	riInfos, utilInfos, riMetadata := convertForAutoExchange(instances, utilData)
	store := newConfigExchangeStoreAdapter(a.app.Config)

	lookupOffering := func(ctx context.Context, instanceType, productDesc, tenancy, scope string, duration int64) (string, error) {
		return ec2Client.FindConvertibleOffering(ctx, ec2svc.FindConvertibleOfferingParams{
			InstanceType:       instanceType,
			ProductDescription: productDesc,
			Tenancy:            tenancy,
			Scope:              scope,
			Duration:           duration,
		})
	}

	return exchange.RunAutoExchange(ctx, exchange.RunAutoExchangeParams{
		Store:          store,
		ExchangeClient: exchange.NewExchangeClient(awsCfg),
		LookupOffering: lookupOffering,
		RIs:            riInfos,
		Utilization:    utilInfos,
		Config:         cfg,
		AccountID:      a.accountID,
		Region:         a.region,
		DashboardURL:   a.app.appConfig.DashboardURL,
		RIMetadata:     riMetadata,
		LadderRunID:    ladderRunID,
		DryRun:         dryRun,
	})
}

// buildAndWireCapability constructs a LadderCapability via the factory and wires its
// write side. Extracted from processOneLadderConfig to keep that function's cyclomatic
// complexity below the project threshold (10). The returned error carries no config
// ID: the sole caller already prefixes its log line with the config ID, so repeating
// it here would duplicate it in the output.
func (app *Application) buildAndWireCapability(ctx context.Context, region, accountID string, executionEnabled bool) (pkgladder.LadderCapability, error) {
	if app.LadderCapabilityFactory == nil {
		return nil, errors.New("LadderCapabilityFactory is nil (not wired)")
	}
	capability, err := app.LadderCapabilityFactory(ctx, region, accountID)
	if err != nil {
		return nil, fmt.Errorf("failed to build ladder capability: %w", err)
	}
	return app.wireLadderWriteSide(ctx, executionEnabled, region, accountID, capability)
}

// wireLadderWriteSide wires the write side of a LadderCapability if and only if
// cap is a *awsladder.AWSLadder. For test fakes (non-AWSLadder implementations)
// it returns cap unchanged so existing handler tests continue to work without
// modifying their fake capabilities.
//
// When executionEnabled is false, WireWriteSideDisabled is called: the ladder
// accepts PurchaseLayer / ReshapeBuffer calls but immediately returns
// ErrLadderExecutionDisabled without touching any AWS API. When executionEnabled
// is true, WireWriteSide wires real EC2 and Savings Plans clients plus the
// exchangeRunnerAdapter that forwards ladderRunID and dryRun to the exchange
// package's seam.
func (app *Application) wireLadderWriteSide(
	ctx context.Context,
	executionEnabled bool,
	region, accountID string,
	capability pkgladder.LadderCapability,
) (pkgladder.LadderCapability, error) {
	l, ok := capability.(*awsladder.AWSLadder)
	if !ok {
		// Test fake or non-AWS capability: plan-only invariant holds without wiring.
		return capability, nil
	}
	if !executionEnabled {
		wired, err := awsladder.WireWriteSideDisabled(l)
		if err != nil {
			return nil, fmt.Errorf("wireLadderWriteSide: disabled: %w", err)
		}
		return wired, nil
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("wireLadderWriteSide: load AWS config: %w", err)
	}
	wired, err := awsladder.WireWriteSide(l, awsCfg, &exchangeRunnerAdapter{app: app, region: region, accountID: accountID})
	if err != nil {
		return nil, fmt.Errorf("wireLadderWriteSide: %w", err)
	}
	return wired, nil
}
