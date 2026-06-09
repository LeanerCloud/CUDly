package commitmentopts

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/billingbenefits/armbillingbenefits"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeAzureSPValidate is a test double for AzureSPValidateAPI. The fn
// field receives the full request body so tests can assert that the prober
// passes the expected term / billing plan fields.
type fakeAzureSPValidate struct {
	fn func(body armbillingbenefits.SavingsPlanPurchaseValidateRequest) (armbillingbenefits.RPClientValidatePurchaseResponse, error)
}

func (f *fakeAzureSPValidate) ValidatePurchase(
	_ context.Context,
	body armbillingbenefits.SavingsPlanPurchaseValidateRequest,
	_ *armbillingbenefits.RPClientValidatePurchaseOptions,
) (armbillingbenefits.RPClientValidatePurchaseResponse, error) {
	return f.fn(body)
}

// validTrue returns an RPClientValidatePurchaseResponse with a single
// benefit whose Valid flag is true.
func validTrue() armbillingbenefits.RPClientValidatePurchaseResponse {
	v := true
	return armbillingbenefits.RPClientValidatePurchaseResponse{
		SavingsPlanValidateResponse: armbillingbenefits.SavingsPlanValidateResponse{
			Benefits: []*armbillingbenefits.SavingsPlanValidResponseProperty{
				{Valid: &v},
			},
		},
	}
}

// validFalse returns a response indicating the offering is not available.
func validFalse(reason string) armbillingbenefits.RPClientValidatePurchaseResponse {
	v := false
	return armbillingbenefits.RPClientValidatePurchaseResponse{
		SavingsPlanValidateResponse: armbillingbenefits.SavingsPlanValidateResponse{
			Benefits: []*armbillingbenefits.SavingsPlanValidResponseProperty{
				{Valid: &v, Reason: &reason},
			},
		},
	}
}

// newFakeProber wires a fakeAzureSPValidate into an AzureSPProber so tests
// never touch the real Azure RP.
func newFakeProber(fn func(armbillingbenefits.SavingsPlanPurchaseValidateRequest) (armbillingbenefits.RPClientValidatePurchaseResponse, error)) *AzureSPProber {
	fake := &fakeAzureSPValidate{fn: fn}
	return &AzureSPProber{
		NewClient: func(_ azcore.TokenCredential) (AzureSPValidateAPI, error) {
			return fake, nil
		},
	}
}

// ---------------------------------------------------------------------------
// AzureSPProber.Service
// ---------------------------------------------------------------------------

func TestAzureSPProber_Service(t *testing.T) {
	p := &AzureSPProber{}
	assert.Equal(t, "savingsplans", p.Service())
}

// ---------------------------------------------------------------------------
// AzureSPProber.ProbeAzure — happy paths
// ---------------------------------------------------------------------------

func TestAzureSPProber_AllValid(t *testing.T) {
	// All 6 candidate combos accepted — all 6 Combos returned.
	p := newFakeProber(func(_ armbillingbenefits.SavingsPlanPurchaseValidateRequest) (armbillingbenefits.RPClientValidatePurchaseResponse, error) {
		return validTrue(), nil
	})

	got, err := p.ProbeAzure(context.Background(), nil)
	require.NoError(t, err)
	assert.Len(t, got, 6)

	// All must carry provider=azure, service=savingsplans.
	for _, c := range got {
		assert.Equal(t, "azure", c.Provider)
		assert.Equal(t, "savingsplans", c.Service)
	}
}

func TestAzureSPProber_SomeValid(t *testing.T) {
	// Only the 1yr and 3yr combos are accepted; P5Y combos are rejected.
	// Simulates the live Azure state where P5Y SPs are not yet available.
	p := newFakeProber(func(body armbillingbenefits.SavingsPlanPurchaseValidateRequest) (armbillingbenefits.RPClientValidatePurchaseResponse, error) {
		if len(body.Benefits) == 0 || body.Benefits[0] == nil {
			return validFalse("no benefits"), nil
		}
		props := body.Benefits[0].Properties
		if props == nil || props.Term == nil {
			return validFalse("no term"), nil
		}
		if *props.Term == armbillingbenefits.TermP5Y {
			return validFalse("P5Y not available"), nil
		}
		return validTrue(), nil
	})

	got, err := p.ProbeAzure(context.Background(), nil)
	require.NoError(t, err)
	// 2 terms x 2 payment plans = 4 combos (P5Y x2 dropped)
	assert.Len(t, got, 4)

	terms := make(map[int]int)
	for _, c := range got {
		terms[c.TermYears]++
	}
	assert.Equal(t, 2, terms[1], "expected 2 combos for 1yr")
	assert.Equal(t, 2, terms[3], "expected 2 combos for 3yr")
	assert.Equal(t, 0, terms[5], "expected no combos for 5yr")
}

func TestAzureSPProber_NoneValid(t *testing.T) {
	// All combos rejected — empty slice, no error.
	p := newFakeProber(func(_ armbillingbenefits.SavingsPlanPurchaseValidateRequest) (armbillingbenefits.RPClientValidatePurchaseResponse, error) {
		return validFalse("not available"), nil
	})

	got, err := p.ProbeAzure(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestAzureSPProber_EmptyBenefitsSlice(t *testing.T) {
	// An API response with an empty benefits slice counts as "valid"
	// (no invalid flag returned) so the combo is included.
	p := newFakeProber(func(_ armbillingbenefits.SavingsPlanPurchaseValidateRequest) (armbillingbenefits.RPClientValidatePurchaseResponse, error) {
		return armbillingbenefits.RPClientValidatePurchaseResponse{}, nil
	})

	got, err := p.ProbeAzure(context.Background(), nil)
	require.NoError(t, err)
	assert.Len(t, got, 6, "empty benefits slice treated as valid")
}

func TestAzureSPProber_NilValidFlag(t *testing.T) {
	// A benefit with a nil Valid pointer is not invalid — treated as valid.
	p := newFakeProber(func(_ armbillingbenefits.SavingsPlanPurchaseValidateRequest) (armbillingbenefits.RPClientValidatePurchaseResponse, error) {
		return armbillingbenefits.RPClientValidatePurchaseResponse{
			SavingsPlanValidateResponse: armbillingbenefits.SavingsPlanValidateResponse{
				Benefits: []*armbillingbenefits.SavingsPlanValidResponseProperty{
					{Valid: nil},
				},
			},
		}, nil
	})

	got, err := p.ProbeAzure(context.Background(), nil)
	require.NoError(t, err)
	assert.Len(t, got, 6, "nil Valid flag treated as valid")
}

// ---------------------------------------------------------------------------
// AzureSPProber.ProbeAzure — error handling
// ---------------------------------------------------------------------------

func TestAzureSPProber_APIError_Propagates(t *testing.T) {
	// Any non-validation error from ValidatePurchase must bubble up and
	// abort the probe — we cannot distinguish "not offered" from
	// "auth failed" without the response.
	boom := errors.New("network failure")
	p := newFakeProber(func(_ armbillingbenefits.SavingsPlanPurchaseValidateRequest) (armbillingbenefits.RPClientValidatePurchaseResponse, error) {
		return armbillingbenefits.RPClientValidatePurchaseResponse{}, boom
	})

	_, err := p.ProbeAzure(context.Background(), nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, boom)
}

func TestAzureSPProber_APIError_FirstComboFails(t *testing.T) {
	// If the first combo call fails, the prober must return an error
	// immediately and not silently continue with the remaining combos.
	calls := 0
	boom := errors.New("auth denied")
	p := newFakeProber(func(_ armbillingbenefits.SavingsPlanPurchaseValidateRequest) (armbillingbenefits.RPClientValidatePurchaseResponse, error) {
		calls++
		return armbillingbenefits.RPClientValidatePurchaseResponse{}, boom
	})

	_, err := p.ProbeAzure(context.Background(), nil)
	require.Error(t, err)
	assert.Equal(t, 1, calls, "prober must stop after first error")
}

func TestAzureSPProber_ClientBuildError(t *testing.T) {
	// If NewClient itself fails (e.g. invalid credential format), the
	// error must propagate before any ValidatePurchase call is made.
	buildErr := errors.New("bad credential")
	p := &AzureSPProber{
		NewClient: func(_ azcore.TokenCredential) (AzureSPValidateAPI, error) {
			return nil, buildErr
		},
	}

	_, err := p.ProbeAzure(context.Background(), nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, buildErr)
}

// ---------------------------------------------------------------------------
// Combo content and deduplication
// ---------------------------------------------------------------------------

func TestAzureSPProber_ComboFields(t *testing.T) {
	// Verify that the returned Combos carry the correct provider, service,
	// termYears, and payment strings.
	p := newFakeProber(func(_ armbillingbenefits.SavingsPlanPurchaseValidateRequest) (armbillingbenefits.RPClientValidatePurchaseResponse, error) {
		return validTrue(), nil
	})

	got, err := p.ProbeAzure(context.Background(), nil)
	require.NoError(t, err)

	sort.Slice(got, func(i, j int) bool {
		if got[i].TermYears != got[j].TermYears {
			return got[i].TermYears < got[j].TermYears
		}
		return got[i].Payment < got[j].Payment
	})

	want := []Combo{
		{Provider: "azure", Service: "savingsplans", TermYears: 1, Payment: "all-upfront"},
		{Provider: "azure", Service: "savingsplans", TermYears: 1, Payment: "monthly"},
		{Provider: "azure", Service: "savingsplans", TermYears: 3, Payment: "all-upfront"},
		{Provider: "azure", Service: "savingsplans", TermYears: 3, Payment: "monthly"},
		{Provider: "azure", Service: "savingsplans", TermYears: 5, Payment: "all-upfront"},
		{Provider: "azure", Service: "savingsplans", TermYears: 5, Payment: "monthly"},
	}
	assert.Equal(t, want, got)
}

func TestAzureSPProber_RequestContainsTerm(t *testing.T) {
	// The probe request body must pass the correct term to ValidatePurchase
	// so the API can evaluate it against the actual catalog.
	seenTerms := make(map[armbillingbenefits.Term]int)
	p := newFakeProber(func(body armbillingbenefits.SavingsPlanPurchaseValidateRequest) (armbillingbenefits.RPClientValidatePurchaseResponse, error) {
		require.Len(t, body.Benefits, 1)
		require.NotNil(t, body.Benefits[0].Properties)
		require.NotNil(t, body.Benefits[0].Properties.Term)
		seenTerms[*body.Benefits[0].Properties.Term]++
		return validTrue(), nil
	})

	_, err := p.ProbeAzure(context.Background(), nil)
	require.NoError(t, err)

	assert.Equal(t, 2, seenTerms[armbillingbenefits.TermP1Y], "P1Y must appear twice (upfront + monthly)")
	assert.Equal(t, 2, seenTerms[armbillingbenefits.TermP3Y], "P3Y must appear twice")
	assert.Equal(t, 2, seenTerms[armbillingbenefits.TermP5Y], "P5Y must appear twice")
}

func TestAzureSPProber_RequestBillingPlan(t *testing.T) {
	// Upfront combos must omit BillingPlan (nil); monthly combos must
	// set BillingPlan = P1M. This drives the Azure API to evaluate the
	// correct payment schedule.
	type planKey struct {
		term armbillingbenefits.Term
		plan string // "nil" or "P1M"
	}
	seen := make(map[planKey]int)

	p := newFakeProber(func(body armbillingbenefits.SavingsPlanPurchaseValidateRequest) (armbillingbenefits.RPClientValidatePurchaseResponse, error) {
		props := body.Benefits[0].Properties
		plan := "nil"
		if props.BillingPlan != nil {
			plan = string(*props.BillingPlan)
		}
		seen[planKey{*props.Term, plan}]++
		return validTrue(), nil
	})

	_, err := p.ProbeAzure(context.Background(), nil)
	require.NoError(t, err)

	for _, term := range []armbillingbenefits.Term{armbillingbenefits.TermP1Y, armbillingbenefits.TermP3Y, armbillingbenefits.TermP5Y} {
		assert.Equal(t, 1, seen[planKey{term, "nil"}], "upfront combo must have nil BillingPlan for %s", term)
		assert.Equal(t, 1, seen[planKey{term, "P1M"}], "monthly combo must have P1M BillingPlan for %s", term)
	}
}

// ---------------------------------------------------------------------------
// DefaultAzureProbers
// ---------------------------------------------------------------------------

func TestDefaultAzureProbers(t *testing.T) {
	probers := DefaultAzureProbers()
	require.Len(t, probers, 1)
	assert.Equal(t, "savingsplans", probers[0].Service())
}
