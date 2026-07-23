package tools

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/provider"
)

func validSavingsPlansArgs() savingsPlansPurchaseArgs {
	return savingsPlansPurchaseArgs{
		SPType:           "Compute",
		HourlyCommitment: 10.50,
		TermYears:        3,
		PaymentOption:    "no-upfront",
	}
}

func TestSavingsPlanRecommendationFromArgsAccountLevel(t *testing.T) {
	t.Parallel()
	rec, region, dryRun, confirm, err := savingsPlanRecommendationFromArgs(validSavingsPlansArgs())
	require.NoError(t, err)
	assert.True(t, dryRun)
	assert.False(t, confirm)
	assert.Equal(t, savingsPlansAccountLevelRegion, region, "account-level plan defaults to the shared query region")
	assert.Equal(t, common.ServiceSavingsPlansCompute, rec.Service)
	assert.Equal(t, common.CommitmentSavingsPlan, rec.CommitmentType)
	assert.Equal(t, "3yr", rec.Term)
	details, ok := rec.Details.(*common.SavingsPlanDetails)
	require.True(t, ok)
	assert.Equal(t, "Compute", details.PlanType)
	assert.InDelta(t, 10.50, details.HourlyCommitment, 0.001)
}

// TestSavingsPlanRecommendationFromArgsAccountLevelWhitespaceRegion proves a
// whitespace-only region (e.g. "  ") for an account-level sp_type (Compute,
// SageMaker, Database) still falls back to savingsPlansAccountLevelRegion,
// the same as an empty region does. Before the fix, the fallback only
// triggered on region == "", so a whitespace-only region threaded the raw
// "  " value into resolveClient instead of the account-level default.
func TestSavingsPlanRecommendationFromArgsAccountLevelWhitespaceRegion(t *testing.T) {
	t.Parallel()
	args := validSavingsPlansArgs()
	args.Region = "  "
	rec, region, _, _, err := savingsPlanRecommendationFromArgs(args)
	require.NoError(t, err)
	assert.Equal(t, savingsPlansAccountLevelRegion, region,
		"whitespace-only region must resolve to the account-level default, not be threaded through as-is")
	assert.Equal(t, common.ServiceSavingsPlansCompute, rec.Service)
}

func TestSavingsPlanRecommendationFromArgsEC2InstanceRequiresRegion(t *testing.T) {
	t.Parallel()
	args := validSavingsPlansArgs()
	args.SPType = "EC2Instance"
	args.InstanceFamily = "m5"

	_, _, _, _, err := savingsPlanRecommendationFromArgs(args)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "region is required")

	args.Region = "us-east-1"
	rec, region, _, _, err := savingsPlanRecommendationFromArgs(args)
	require.NoError(t, err)
	assert.Equal(t, "us-east-1", region)
	assert.Equal(t, common.ServiceSavingsPlansEC2Instance, rec.Service)
	details, ok := rec.Details.(*common.SavingsPlanDetails)
	require.True(t, ok)
	assert.Equal(t, "m5", details.InstanceFamily)
	assert.Equal(t, "us-east-1", details.Region)
}

// TestSavingsPlanRecommendationFromArgsEC2InstanceRejectsWhitespaceOnly
// proves region and instance_family are rejected when they contain only
// whitespace, not just when they are the empty string: a bare `== ""` check
// would let "   " through to a real EC2Instance Savings Plan purchase.
func TestSavingsPlanRecommendationFromArgsEC2InstanceRejectsWhitespaceOnly(t *testing.T) {
	t.Parallel()

	t.Run("whitespace-only region", func(t *testing.T) {
		args := validSavingsPlansArgs()
		args.SPType = "EC2Instance"
		args.InstanceFamily = "m5"
		args.Region = "   "

		_, _, _, _, err := savingsPlanRecommendationFromArgs(args)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "region is required")
	})

	t.Run("whitespace-only instance_family", func(t *testing.T) {
		args := validSavingsPlansArgs()
		args.SPType = "EC2Instance"
		args.Region = "us-east-1"
		args.InstanceFamily = "\t "

		_, _, _, _, err := savingsPlanRecommendationFromArgs(args)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "instance_family is required")
	})
}

// TestSavingsPlanRecommendationFromArgsEC2InstanceRequiresInstanceFamily is
// the regression guard for the CodeRabbit money-path finding: omitting
// instance_family for sp_type=EC2Instance lets DescribeSavingsPlansOfferings
// resolve across every instance family in the region instead of the one
// Cost Explorer actually recommended, risking a real purchase for the wrong
// workload. instance_family must be required exactly like region already is.
func TestSavingsPlanRecommendationFromArgsEC2InstanceRequiresInstanceFamily(t *testing.T) {
	t.Parallel()
	args := validSavingsPlansArgs()
	args.SPType = "EC2Instance"
	args.Region = "us-east-1"

	_, _, _, _, err := savingsPlanRecommendationFromArgs(args)
	require.Error(t, err, "EC2Instance sp_type without instance_family must be rejected")
	assert.Contains(t, err.Error(), "instance_family is required")

	args.InstanceFamily = "m5"
	rec, _, _, _, err := savingsPlanRecommendationFromArgs(args)
	require.NoError(t, err, "EC2Instance sp_type with instance_family set must succeed")
	details, ok := rec.Details.(*common.SavingsPlanDetails)
	require.True(t, ok)
	assert.Equal(t, "m5", details.InstanceFamily)
}

// TestSavingsPlanRecommendationFromArgsInstanceFamilyOptionalForOtherTypes
// proves the new instance_family requirement is scoped to sp_type=EC2Instance
// only: Compute, SageMaker, and Database plans are family-agnostic and
// account-level, so instance_family stays optional (and ignored) for them.
func TestSavingsPlanRecommendationFromArgsInstanceFamilyOptionalForOtherTypes(t *testing.T) {
	t.Parallel()
	for _, spType := range []string{"Compute", "SageMaker"} {
		t.Run(spType, func(t *testing.T) {
			args := validSavingsPlansArgs()
			args.SPType = spType
			args.InstanceFamily = ""

			_, _, _, _, err := savingsPlanRecommendationFromArgs(args)
			require.NoError(t, err, "instance_family must remain optional for sp_type=%s", spType)
		})
	}

	t.Run("Database", func(t *testing.T) {
		args := validSavingsPlansArgs()
		args.SPType = "Database"
		args.TermYears = 1
		args.PaymentOption = "no-upfront"
		args.InstanceFamily = ""

		_, _, _, _, err := savingsPlanRecommendationFromArgs(args)
		require.NoError(t, err, "instance_family must remain optional for sp_type=Database")
	})
}

func TestSavingsPlanRecommendationFromArgsInvalid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		mutate func(*savingsPlansPurchaseArgs)
		errSub string
	}{
		{"zero hourly commitment", func(a *savingsPlansPurchaseArgs) { a.HourlyCommitment = 0 }, "hourly_commitment must be"},
		{"negative hourly commitment", func(a *savingsPlansPurchaseArgs) { a.HourlyCommitment = -5 }, "hourly_commitment must be"},
		{"invalid sp_type", func(a *savingsPlansPurchaseArgs) { a.SPType = "Storage" }, "invalid sp_type"},
		{"invalid term", func(a *savingsPlansPurchaseArgs) { a.TermYears = 2 }, "invalid term_years"},
		{"invalid payment option", func(a *savingsPlansPurchaseArgs) { a.PaymentOption = "bogus" }, "invalid payment_option"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := validSavingsPlansArgs()
			tc.mutate(&args)
			_, _, _, _, err := savingsPlanRecommendationFromArgs(args)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.errSub)
		})
	}
}

// TestSavingsPlanRecommendationFromArgsDatabaseConstraints proves the
// CodeRabbit-requested up-front validation: per AWS's Database Savings
// Plans announcement, sp_type=Database only supports a one-year term
// billed no-upfront -- unlike Compute/EC2Instance/SageMaker, there is no
// 3-year term and no all-upfront/partial-upfront option. A mismatched
// term_years or payment_option must be rejected before building the
// recommendation, not left for AWS's purchase API to reject.
func TestSavingsPlanRecommendationFromArgsDatabaseConstraints(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		mutate func(*savingsPlansPurchaseArgs)
		errSub string
	}{
		{"3yr term rejected", func(a *savingsPlansPurchaseArgs) { a.TermYears = 3 }, "only supports a 1-year term"},
		{"all-upfront rejected", func(a *savingsPlansPurchaseArgs) { a.PaymentOption = "all-upfront" }, "only supports payment_option=no-upfront"},
		{"partial-upfront rejected", func(a *savingsPlansPurchaseArgs) { a.PaymentOption = "partial-upfront" }, "only supports payment_option=no-upfront"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := validSavingsPlansArgs()
			args.SPType = "Database"
			args.TermYears = 1
			args.PaymentOption = "no-upfront"
			tc.mutate(&args)
			_, _, _, _, err := savingsPlanRecommendationFromArgs(args)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.errSub)
		})
	}
}

// TestSavingsPlanRecommendationFromArgsDatabaseAllowedCombo proves the one
// term/payment_option combination Database Savings Plans actually support
// still succeeds.
func TestSavingsPlanRecommendationFromArgsDatabaseAllowedCombo(t *testing.T) {
	t.Parallel()
	args := validSavingsPlansArgs()
	args.SPType = "Database"
	args.TermYears = 1
	args.PaymentOption = "no-upfront"

	rec, _, _, _, err := savingsPlanRecommendationFromArgs(args)
	require.NoError(t, err)
	assert.Equal(t, common.ServiceSavingsPlansDatabase, rec.Service)
	assert.Equal(t, "1yr", rec.Term)
}

// TestSavingsPlanRecommendationFromArgsNonDatabaseUnaffected proves the
// Database-only constraint does not leak onto other sp_types: Compute keeps
// supporting 3-year all-upfront, the combo Database rejects.
func TestSavingsPlanRecommendationFromArgsNonDatabaseUnaffected(t *testing.T) {
	t.Parallel()
	args := validSavingsPlansArgs()
	args.SPType = "Compute"
	args.TermYears = 3
	args.PaymentOption = "all-upfront"

	rec, _, _, _, err := savingsPlanRecommendationFromArgs(args)
	require.NoError(t, err)
	assert.Equal(t, "3yr", rec.Term)
	assert.Equal(t, "all-upfront", rec.PaymentOption)
}

func TestAWSSavingsPlansPurchaseHandleConfirmFalseRefuses(t *testing.T) {
	t.Parallel()
	resolveCalled := false
	tool := &awsSavingsPlansPurchaseTool{
		createProvider: func(_ string, _ *provider.ProviderConfig) (provider.Provider, error) {
			resolveCalled = true
			return nil, nil
		},
	}
	args := validSavingsPlansArgs()
	args.DryRun = boolPtr(false)
	args.Confirm = boolPtr(false)

	_, _, err := tool.handle(context.Background(), nil, args)
	require.Error(t, err)
	assert.False(t, resolveCalled)
	assert.Contains(t, err.Error(), "confirm=true")
}

func TestAWSSavingsPlansPurchaseHandleDryRunNeverCallsProvider(t *testing.T) {
	t.Parallel()
	resolveCalled := false
	tool := &awsSavingsPlansPurchaseTool{
		createProvider: func(_ string, _ *provider.ProviderConfig) (provider.Provider, error) {
			resolveCalled = true
			return nil, nil
		},
	}
	args := validSavingsPlansArgs()
	args.Confirm = boolPtr(true)

	_, resp, err := tool.handle(context.Background(), nil, args)
	require.NoError(t, err)
	assert.False(t, resolveCalled)
	assert.True(t, resp.DryRun)
}

func TestAWSSavingsPlansPurchaseHandleRealPurchaseUsesScopedService(t *testing.T) {
	t.Parallel()
	fake := &fakeServiceClient{purchaseResult: common.PurchaseResult{Success: true, CommitmentID: "sp-1"}}
	var gotService common.ServiceType
	tool := &awsSavingsPlansPurchaseTool{
		createProvider: func(_ string, _ *provider.ProviderConfig) (provider.Provider, error) {
			return &recordingProvider{
				fakeProvider: &fakeProvider{name: "aws"},
				client:       fake,
				gotService:   &gotService,
				gotRegion:    new(string),
			}, nil
		},
	}
	args := validSavingsPlansArgs()
	args.DryRun = boolPtr(false)
	args.Confirm = boolPtr(true)

	_, resp, err := tool.handle(context.Background(), nil, args)
	require.NoError(t, err)
	assert.True(t, resp.Success)
	assert.Equal(t, common.ServiceSavingsPlansCompute, gotService, "must resolve the plan-type-scoped client, not the umbrella sentinel")
	assert.Equal(t, common.PurchaseSourceMCP, fake.lastOpts.Source)
}
