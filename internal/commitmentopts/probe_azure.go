package commitmentopts

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/billingbenefits/armbillingbenefits"
)

// azureSpService is the service name written into Combo.Service for Azure
// Savings Plans. Matches the key used in the Options map.
const azureSpService = "savingsplans"

// azureSpProvider is the provider string for Azure commitment combos.
const azureSpProvider = "azure"

// azureProbeSubscriptionID is a placeholder subscription ID used in probe
// requests. ValidatePurchase only verifies term/billingPlan shape; it does
// not require a real billing scope to evaluate the (term, payment) tuple as
// structurally valid.
const azureProbeSubscriptionID = "00000000-0000-0000-0000-000000000000"

// azureProbeHourlyCommitment is the minimum non-zero hourly commitment used
// in ValidatePurchase probe requests. The value itself is irrelevant for
// structural validation (the API checks term + payment shape, not the dollar
// amount), but zero is rejected server-side so we use the documented minimum.
const azureProbeHourlyCommitment float64 = 0.001

// azureCandidateCombos lists every (term, billingPlan) pair that the Azure
// Savings Plans API publishes. The probe calls ValidatePurchase for each one
// and retains only those the API accepts as structurally valid.
//
// Azure SP payment model:
//   - nil BillingPlan   = full upfront ("all-upfront")
//   - BillingPlanP1M    = monthly installments ("monthly")
//
// Azure currently offers 1-year and 3-year terms for Compute SPs; P5Y is
// defined in the SDK constants but is not sold in practice. The probe
// includes P5Y and drops it if ValidatePurchase rejects it, so the persisted
// combos always reflect live API reality rather than the SDK enum.
var azureCandidateCombos = []struct {
	termYears   int
	azureTerm   armbillingbenefits.Term
	azurePlan   *armbillingbenefits.BillingPlan
	paymentName string
}{
	{1, armbillingbenefits.TermP1Y, nil, "all-upfront"},
	{1, armbillingbenefits.TermP1Y, billingPlanP1M(), "monthly"},
	{3, armbillingbenefits.TermP3Y, nil, "all-upfront"},
	{3, armbillingbenefits.TermP3Y, billingPlanP1M(), "monthly"},
	{5, armbillingbenefits.TermP5Y, nil, "all-upfront"},
	{5, armbillingbenefits.TermP5Y, billingPlanP1M(), "monthly"},
}

// billingPlanP1M returns a pointer to BillingPlanP1M. Using a function avoids
// taking the address of an unaddressable constant.
func billingPlanP1M() *armbillingbenefits.BillingPlan {
	p := armbillingbenefits.BillingPlanP1M
	return &p
}

// AzureSPValidateAPI is the minimal Azure Billing Benefits surface the probe
// needs. It matches the ValidatePurchase method on *armbillingbenefits.RPClient
// so tests can substitute a mock without importing the concrete SDK client.
//
// NOTE: providers/azure/services/savingsplans.RPValidateAPI defines the same
// one-method interface. The duplication is intentional: importing the provider
// package from internal/commitmentopts would create a circular dependency. The
// interface is tiny enough that duplicating it here is the cleanest option.
type AzureSPValidateAPI interface {
	ValidatePurchase(
		ctx context.Context,
		body armbillingbenefits.SavingsPlanPurchaseValidateRequest,
		options *armbillingbenefits.RPClientValidatePurchaseOptions,
	) (armbillingbenefits.RPClientValidatePurchaseResponse, error)
}

// AzureSPProber probes the Azure Billing Benefits ValidatePurchase endpoint for
// each candidate (term, payment) combination and returns those the API accepts
// as live Combos.
//
// Design notes:
//   - Azure has no "list offerings" catalog endpoint comparable to AWS
//     Describe*Offerings. ValidatePurchase is the closest live signal that a
//     given (term, billingPlan) configuration is currently accepted.
//   - The probe uses a zero-valued (placeholder) subscription and the minimum
//     non-zero hourly commitment so no real resource or billing impact occurs.
//   - A 422/invalid response for a given combo means it is not available; any
//     other error (network, auth, 5xx) is treated as a probe failure and
//     bubbled up so the Service can apply its all-or-nothing policy.
type AzureSPProber struct {
	// NewClient builds an AzureSPValidateAPI from a credential. Override
	// in tests to return a mock.
	NewClient func(cred azcore.TokenCredential) (AzureSPValidateAPI, error)
}

// Service returns "savingsplans".
func (p *AzureSPProber) Service() string { return azureSpService }

// ProbeAzure probes the Azure ValidatePurchase endpoint for each candidate
// (term, billingPlan) combo and returns Combos for those the API accepts.
//
// The method signature differs from the AWS Prober interface because Azure
// authentication uses azcore.TokenCredential rather than aws.Config. Callers
// use this method directly; Service.probeAndPersistAzure wires it up.
func (p *AzureSPProber) ProbeAzure(ctx context.Context, cred azcore.TokenCredential) ([]Combo, error) {
	client, err := p.client(cred)
	if err != nil {
		return nil, fmt.Errorf("savingsplans: create validate client: %w", err)
	}

	var combos []Combo
	for _, c := range azureCandidateCombos {
		ok, err := p.probeCombo(ctx, client, c.azureTerm, c.azurePlan)
		if err != nil {
			// Treat non-validation errors (auth, network, 5xx) as probe
			// failures — they prevent us from knowing whether the combo
			// is valid, so we must not silently drop it.
			return nil, fmt.Errorf("savingsplans: probe %dy %s: %w", c.termYears, c.paymentName, err)
		}
		if ok {
			combos = append(combos, Combo{
				Provider:  azureSpProvider,
				Service:   azureSpService,
				TermYears: c.termYears,
				Payment:   c.paymentName,
			})
		}
	}
	return combos, nil
}

// probeCombo calls ValidatePurchase for a single (term, billingPlan) pair and
// returns true if the API considers it a valid offering.
//
// A response where any benefit has Valid=false is treated as "not offered"
// (returns false, nil). Any other API error is returned as-is so the caller
// can decide whether it is a transient failure or a configuration problem.
func (p *AzureSPProber) probeCombo(
	ctx context.Context,
	client AzureSPValidateAPI,
	term armbillingbenefits.Term,
	billingPlan *armbillingbenefits.BillingPlan,
) (bool, error) {
	subscriptionID := azureProbeSubscriptionID
	billingScopeID := fmt.Sprintf("/subscriptions/%s", subscriptionID)
	grain := armbillingbenefits.CommitmentGrainHourly
	appliedScope := armbillingbenefits.AppliedScopeTypeShared
	hourlyAmount := azureProbeHourlyCommitment
	displayName := "cudly-probe"
	currencyCode := "USD"
	planType := "Compute"

	props := &armbillingbenefits.SavingsPlanOrderAliasProperties{
		DisplayName:      &displayName,
		BillingScopeID:   &billingScopeID,
		Term:             &term,
		AppliedScopeType: &appliedScope,
		Commitment: &armbillingbenefits.Commitment{
			Amount:       &hourlyAmount,
			CurrencyCode: &currencyCode,
			Grain:        &grain,
		},
	}
	if billingPlan != nil {
		props.BillingPlan = billingPlan
	}

	body := armbillingbenefits.SavingsPlanPurchaseValidateRequest{
		Benefits: []*armbillingbenefits.SavingsPlanOrderAliasModel{
			{
				SKU:        &armbillingbenefits.SKU{Name: &planType},
				Properties: props,
			},
		},
	}

	resp, err := client.ValidatePurchase(ctx, body, nil)
	if err != nil {
		return false, err
	}

	// The API returns a per-benefit valid flag. If all reported benefits are
	// valid (or the slice is empty, which can't happen for a single-item
	// request in practice), the combo is available.
	for _, b := range resp.Benefits {
		if b != nil && b.Valid != nil && !*b.Valid {
			return false, nil
		}
	}
	return true, nil
}

func (p *AzureSPProber) client(cred azcore.TokenCredential) (AzureSPValidateAPI, error) {
	if p.NewClient != nil {
		return p.NewClient(cred)
	}
	c, err := armbillingbenefits.NewRPClient(cred, nil)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// DefaultAzureProbers returns one prober instance for the Azure Savings Plans
// service. The Service wires these up by default for Azure probe runs.
func DefaultAzureProbers() []*AzureSPProber {
	return []*AzureSPProber{
		{},
	}
}
