package tools

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/provider"
)

// TestAccountLevelSavingsPlanRegionCannotForkIdempotencyToken is the
// regression guard for the double-purchase path CodeRabbit found: region is
// documented as "ignored for account-level plan types", but it still flowed
// into rec.Region and from there into idempotencyKeyFor. A model that bought
// a $10/hr Compute SP once without region and then re-issued the identical
// call with region="eu-west-1" (a self-correction, or a retry that fills the
// field in) derived two different tokens, so Savings Plans' ClientToken
// dedupe missed and a SECOND plan was purchased.
//
// An input the tool declares it ignores must not be able to change purchase
// identity, so both calls must produce byte-identical tokens.
//
// Every account-level plan type is covered, not a representative one. Database
// needs its own term/payment because validateDatabaseSavingsPlan
// (aws_savingsplans.go) permits only 1yr/no-upfront, which is why the shared
// 3yr fixture cannot carry it. Today the canonicalization branches on
// `spType == SPTypeEC2Instance`, so Database is account-level by construction
// and cannot regress on its own; the case exists so that a refactor which
// instead enumerates the account-level types explicitly cannot quietly leave
// Database out of the list.
func TestAccountLevelSavingsPlanRegionCannotForkIdempotencyToken(t *testing.T) {
	t.Parallel()

	cases := []struct {
		spType        SPType
		termYears     int
		paymentOption PaymentOption
	}{
		{spType: SPTypeCompute, termYears: int(TermThreeYear), paymentOption: PaymentOptionNoUpfront},
		{spType: SPTypeSageMaker, termYears: int(TermThreeYear), paymentOption: PaymentOptionNoUpfront},
		{spType: SPTypeDatabase, termYears: int(TermOneYear), paymentOption: PaymentOptionNoUpfront},
	}

	for _, tc := range cases {
		t.Run(string(tc.spType), func(t *testing.T) {
			t.Parallel()

			argsFor := func(region string) savingsPlansPurchaseArgs {
				args := validSavingsPlansArgs()
				args.SPType = string(tc.spType)
				args.TermYears = tc.termYears
				args.PaymentOption = string(tc.paymentOption)
				args.Region = region
				return args
			}

			withoutRegion := argsFor("")
			withRegion := argsFor("eu-west-1")

			recA, regionA, _, _, err := savingsPlanRecommendationFromArgs(withoutRegion)
			require.NoError(t, err)
			recB, regionB, _, _, err := savingsPlanRecommendationFromArgs(withRegion)
			require.NoError(t, err)

			assert.Equal(t, savingsPlansAccountLevelRegion, regionA)
			assert.Equal(t, savingsPlansAccountLevelRegion, regionB,
				"an ignored region must not change the resolved region for an account-level plan")

			scope := CredentialScope(withRegion.AWSProfile, "AWS_PROFILE")
			tokenA := idempotencyKeyFor(regionA, recA, scope, "")
			tokenB := idempotencyKeyFor(regionB, recB, scope, "")
			assert.Equal(t, tokenA, tokenB,
				"supplying an ignored region must not fork the idempotency token -- a forked token means the retry does not dedupe and buys a second plan")
		})
	}
}

// TestEC2InstanceSavingsPlanStillHonorsRegion pins the other side of the
// canonicalization: EC2Instance plans ARE region-scoped, so region must still
// reach both the resolved region and the token. Without this, pinning the
// account-level region for every plan type would silently buy an EC2Instance
// plan in the wrong region.
//
// The token assertion is the load-bearing one. Checking only rec.Region and
// details.Region would leave the claim "the token is region-scoped" untested,
// so a future change that canonicalized region for EC2Instance too would keep
// those field assertions green while collapsing two genuinely different
// purchases (m5 in eu-west-1 vs m5 in us-east-1) onto one token -- at which
// point the second, legitimately distinct purchase would dedupe away and
// never happen.
func TestEC2InstanceSavingsPlanStillHonorsRegion(t *testing.T) {
	t.Parallel()

	ec2InstanceArgs := func(region string) savingsPlansPurchaseArgs {
		args := validSavingsPlansArgs()
		args.SPType = string(SPTypeEC2Instance)
		args.InstanceFamily = "m5"
		args.Region = region
		return args
	}

	rec, region, _, _, err := savingsPlanRecommendationFromArgs(ec2InstanceArgs("eu-west-1"))
	require.NoError(t, err)
	assert.Equal(t, "eu-west-1", region)
	assert.Equal(t, "eu-west-1", rec.Region)

	details, ok := rec.Details.(*common.SavingsPlanDetails)
	require.True(t, ok)
	assert.Equal(t, "eu-west-1", details.Region)

	otherRec, otherRegion, _, _, err := savingsPlanRecommendationFromArgs(ec2InstanceArgs("us-east-1"))
	require.NoError(t, err)
	assert.Equal(t, "us-east-1", otherRegion)

	scope := CredentialScope(validSavingsPlansArgs().AWSProfile, "AWS_PROFILE")
	assert.NotEqual(t,
		idempotencyKeyFor(region, rec, scope, ""),
		idempotencyKeyFor(otherRegion, otherRec, scope, ""),
		"an EC2Instance plan is region-scoped, so two regions must derive DIFFERENT tokens -- collapsing them would dedupe away a legitimately distinct purchase")
}

// TestResolveClientTrimsCredentialScope is the regression guard for the
// second CodeRabbit finding: every resolveClient closure forwarded the RAW
// credential argument to ProviderConfig while CredentialScope trimmed the
// same value for the idempotency token. A padded " test-profile " therefore
// named one account in the token and a different (or, for a profile that
// does not exist, no) account in the provider config that actually
// authenticates the purchase.
//
// Routing ProviderConfig through CredentialScope -- the very function the
// token uses -- is what makes the two incapable of diverging, so this asserts
// the config sees the normalized value.
// The table covers every resolveClient in this package rather than one
// representative per provider: the bug was seven independent copies of the
// same raw-forwarding expression, so a guard on a subset would let a future
// edit reintroduce it in the files it skipped.
func TestResolveClientTrimsCredentialScope(t *testing.T) {
	t.Parallel()

	// got extracts the credential field the given tool is responsible for, so
	// each case asserts on the one field its ProviderConfig actually sets.
	cases := []struct {
		name    string
		resolve func(createProvider func(string, *provider.ProviderConfig) (provider.Provider, error)) ResolveClientFunc
		got     func(cfg *provider.ProviderConfig) string
		want    string
	}{
		{
			name: "aws ec2",
			resolve: func(cp func(string, *provider.ProviderConfig) (provider.Provider, error)) ResolveClientFunc {
				return (&awsEC2RIPurchaseTool{createProvider: cp}).
					resolveClient(ec2RIPurchaseArgs{AWSProfile: "  padded-profile  "}, "us-east-1")
			},
			got:  func(c *provider.ProviderConfig) string { return c.AWSProfile },
			want: "padded-profile",
		},
		{
			name: "aws elasticache",
			resolve: func(cp func(string, *provider.ProviderConfig) (provider.Provider, error)) ResolveClientFunc {
				return (&awsElastiCacheRIPurchaseTool{createProvider: cp}).
					resolveClient(elasticacheRIPurchaseArgs{AWSProfile: "  padded-profile  "}, "us-east-1")
			},
			got:  func(c *provider.ProviderConfig) string { return c.AWSProfile },
			want: "padded-profile",
		},
		{
			name: "aws rds",
			resolve: func(cp func(string, *provider.ProviderConfig) (provider.Provider, error)) ResolveClientFunc {
				return (&awsRDSRIPurchaseTool{createProvider: cp}).
					resolveClient(rdsRIPurchaseArgs{AWSProfile: "  padded-profile  "}, "us-east-1")
			},
			got:  func(c *provider.ProviderConfig) string { return c.AWSProfile },
			want: "padded-profile",
		},
		{
			name: "aws simple ri",
			resolve: func(cp func(string, *provider.ProviderConfig) (provider.Provider, error)) ResolveClientFunc {
				return (&simpleAWSRIPurchaseTool{createProvider: cp}).
					resolveClient(simpleAWSRIPurchaseArgs{AWSProfile: "  padded-profile  "}, "us-east-1")
			},
			got:  func(c *provider.ProviderConfig) string { return c.AWSProfile },
			want: "padded-profile",
		},
		{
			name: "aws savings plans",
			resolve: func(cp func(string, *provider.ProviderConfig) (provider.Provider, error)) ResolveClientFunc {
				return (&awsSavingsPlansPurchaseTool{createProvider: cp}).
					resolveClient(savingsPlansPurchaseArgs{AWSProfile: "  padded-profile  "}, "us-east-1", common.ServiceSavingsPlansCompute)
			},
			got:  func(c *provider.ProviderConfig) string { return c.AWSProfile },
			want: "padded-profile",
		},
		{
			name: "azure compute",
			resolve: func(cp func(string, *provider.ProviderConfig) (provider.Provider, error)) ResolveClientFunc {
				return (&azureComputeRIPurchaseTool{createProvider: cp}).
					resolveClient(azureComputeRIPurchaseArgs{AzureSubscriptionID: "  sub-x  "}, "eastus")
			},
			got:  func(c *provider.ProviderConfig) string { return c.AzureSubscriptionID },
			want: "sub-x",
		},
		{
			name: "gcp compute engine",
			resolve: func(cp func(string, *provider.ProviderConfig) (provider.Provider, error)) ResolveClientFunc {
				return (&gcpComputeEngineCUDPurchaseTool{createProvider: cp}).
					resolveClient(gcpComputeEngineCUDPurchaseArgs{GCPProjectID: "  proj-a  "}, "us-central1")
			},
			got:  func(c *provider.ProviderConfig) string { return c.GCPProjectID },
			want: "proj-a",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var gotCfg *provider.ProviderConfig
			// The factory returns an error so the closure stops right after
			// building the config: this asserts on what would have been used
			// to authenticate, without needing a live provider.
			resolve := tc.resolve(func(_ string, cfg *provider.ProviderConfig) (provider.Provider, error) {
				gotCfg = cfg
				return nil, assert.AnError
			})
			_, _ = resolve(context.Background())
			require.NotNil(t, gotCfg, "createProvider must have been called with a config")
			assert.Equal(t, tc.want, tc.got(gotCfg),
				"ProviderConfig must see the same normalized credential the idempotency token scopes on")
		})
	}
}

// azurePurchaseIdentity drives a REAL Azure purchase through the tool's own
// handler and reports the two things that decide purchase identity: the
// idempotency token the provider would dedupe on, and the subscription the
// ProviderConfig would authenticate with.
//
// It goes through handle() rather than re-deriving the scope expression
// because that expression is exactly what is under test: a test that recomputed
// azureCredentialScope(...) itself would stay green if handle() stopped calling
// it.
func azurePurchaseIdentity(t *testing.T, subscriptionID string) (token, cfgSubscriptionID string) {
	t.Helper()

	fake := &fakeServiceClient{purchaseResult: common.PurchaseResult{Success: true}}
	var gotCfg *provider.ProviderConfig
	tool := &azureComputeRIPurchaseTool{
		createProvider: func(_ string, cfg *provider.ProviderConfig) (provider.Provider, error) {
			gotCfg = cfg
			return &recordingProvider{
				fakeProvider: &fakeProvider{name: "azure"},
				client:       fake,
				gotService:   new(common.ServiceType),
				gotRegion:    new(string),
			}, nil
		},
	}

	args := validAzureComputeArgs()
	args.AzureSubscriptionID = subscriptionID
	args.DryRun = boolPtr(false)
	args.Confirm = boolPtr(true)

	_, resp, err := tool.handle(context.Background(), nil, args)
	require.NoError(t, err)
	require.True(t, resp.Success)
	require.NotNil(t, gotCfg, "createProvider must have been called with a config")
	return fake.lastOpts.IdempotencyToken, gotCfg.AzureSubscriptionID
}

// TestAzureSubscriptionCaseCannotForkIdempotencyToken is the regression guard
// for a double-spend on the Azure purchase path.
//
// ARM subscription IDs are case-insensitive GUIDs, and nothing in
// providers/azure canonicalizes them. So a purchase issued with
// azure_subscription_id="ABC12345-..." (the spelling the Azure portal hands
// you) that times out, then retried with the override omitted so the value
// comes from a lower-case AZURE_SUBSCRIPTION_ID, named the SAME subscription
// through two different idempotency tokens. Azure's dedupe
// (reservations.FindReservationOrderByIdempotencyToken) matches on the token
// tag across the tenant-wide order list, so the retry's lookup missed the
// first order and bought a SECOND reservation.
//
// This is the same shape as the untrimmed-credential fork fixed in 44b6094 --
// purchase identity forked by a difference the provider does not recognize --
// and it fails on the pre-fix code, which folded the verbatim string into the
// token.
func TestAzureSubscriptionCaseCannotForkIdempotencyToken(t *testing.T) {
	t.Parallel()

	// Same subscription, two spellings: as pasted from the portal, and as
	// AZURE_SUBSCRIPTION_ID conventionally holds it.
	const (
		portalCase = "ABC12345-1234-1234-1234-1234567890AB"
		envCase    = "abc12345-1234-1234-1234-1234567890ab"
		// A genuinely different subscription, as the control: the fix must
		// canonicalize case without collapsing distinct accounts onto one
		// token, which would dedupe away a legitimate second purchase.
		otherSubscription = "99999999-9999-9999-9999-999999999999"
	)

	portalToken, portalCfg := azurePurchaseIdentity(t, portalCase)
	envToken, envCfg := azurePurchaseIdentity(t, envCase)
	otherToken, _ := azurePurchaseIdentity(t, otherSubscription)

	assert.Equal(t, portalToken, envToken,
		"the same subscription in two cases must derive ONE token -- two tokens means the retry misses Azure's tenant-wide lookup and buys a second reservation")
	assert.NotEqual(t, portalToken, otherToken,
		"two genuinely different subscriptions must still derive different tokens")

	assert.Equal(t, envCase, portalCfg,
		"ProviderConfig must see the same canonicalized subscription the token scopes on")
	assert.Equal(t, envCase, envCfg)
}

// TestAWSProfileCaseIsPreserved pins the boundary of the Azure fix: AWS named
// profiles are section names in ~/.aws/config and ARE case-sensitive, so
// "Prod" and "prod" are two different profiles that may authenticate as two
// different accounts.
//
// Without this, a later "just normalize credential scope everywhere" cleanup
// would look harmless and would both send a real purchase to a profile that
// may not exist and collapse two genuinely distinct accounts onto one
// idempotency token.
func TestAWSProfileCaseIsPreserved(t *testing.T) {
	t.Parallel()

	purchaseAs := func(profile string) (token, cfgProfile string) {
		fake := &fakeServiceClient{purchaseResult: common.PurchaseResult{Success: true}}
		var gotCfg *provider.ProviderConfig
		tool := &awsEC2RIPurchaseTool{
			createProvider: func(_ string, cfg *provider.ProviderConfig) (provider.Provider, error) {
				gotCfg = cfg
				return &recordingProvider{
					fakeProvider: &fakeProvider{name: "aws"},
					client:       fake,
					gotService:   new(common.ServiceType),
					gotRegion:    new(string),
				}, nil
			},
		}

		args := validEC2Args()
		args.AWSProfile = profile
		args.DryRun = boolPtr(false)
		args.Confirm = boolPtr(true)

		_, resp, err := tool.handle(context.Background(), nil, args)
		require.NoError(t, err)
		require.True(t, resp.Success)
		require.NotNil(t, gotCfg, "createProvider must have been called with a config")
		return fake.lastOpts.IdempotencyToken, gotCfg.AWSProfile
	}

	upperToken, upperCfg := purchaseAs("Prod")
	lowerToken, lowerCfg := purchaseAs("prod")

	assert.Equal(t, "Prod", upperCfg, "an AWS profile name must reach the provider config verbatim")
	assert.Equal(t, "prod", lowerCfg)
	assert.NotEqual(t, upperToken, lowerToken,
		"AWS profile names are case-sensitive, so two cases are two accounts and must keep two tokens -- the Azure case-folding fix must not leak here")
}
