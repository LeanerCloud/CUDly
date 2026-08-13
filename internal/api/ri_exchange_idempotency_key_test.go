package api

// ri_exchange_idempotency_key_test.go -- what the RI exchange submit
// fingerprint is made of (issue #1642). Split from
// ri_exchange_idempotency_test.go to keep each file under the project's
// 500-line limit; the handler-level tests that consume these keys live there.

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// --- key composition ---
//
// The fingerprint has to survive aliasing in BOTH directions, and the two
// directions fail differently:
//
//   - two tokens standing for one purchase lets a retry through and spends
//     twice (the #1642 double spend);
//   - one token standing for two purchases silently swallows a genuinely
//     distinct second exchange behind a 409.

func azureKeyBody() AzureExecuteExchangeRequestBody {
	return AzureExecuteExchangeRequestBody{
		SubscriptionID: "sub-1",
		Sources:        []AzureExchangeSourceBody{{ReservationID: "res-1", Quantity: 4}},
		Targets:        []AzureExchangeTargetBody{{SKU: "Standard_D8s_v3", Location: "eastus", Term: "P1Y", Quantity: 4}},
		MaxPaymentDue:  "500.00",
		Currency:       "USD",
	}
}

// TestAzureExchangeIdempotencyKey_SubscriptionScoped is the #1495 precedent
// applied here: ListExchangeableReservations is TENANT-wide, so two
// subscriptions can legitimately present the same reservation and target
// shapes. A scope-blind fingerprint would let one subscription's exchange
// suppress another's.
func TestAzureExchangeIdempotencyKey_SubscriptionScoped(t *testing.T) {
	a := azureKeyBody()
	b := azureKeyBody()
	b.SubscriptionID = "sub-2"
	assert.NotEqual(t, azureExchangeIdempotencyKey(a), azureExchangeIdempotencyKey(b),
		"a fingerprint blind to subscription_id aliases across the tenant")
}

// TestAzureExchangeIdempotencyKey_StableAcrossNonPurchaseFields pins the other
// direction: everything that does not change WHAT is bought must leave the
// fingerprint alone, or a retry mints a fresh key and commits a second time.
func TestAzureExchangeIdempotencyKey_StableAcrossNonPurchaseFields(t *testing.T) {
	base := azureKeyBody()
	base.Sources = append(base.Sources, AzureExchangeSourceBody{ReservationID: "res-2", Quantity: 1})
	base.Targets = append(base.Targets, AzureExchangeTargetBody{SKU: "Standard_D2s_v3", Location: "eastus", Term: "P3Y", Quantity: 1})
	want := azureExchangeIdempotencyKey(base)

	variants := map[string]func(*AzureExecuteExchangeRequestBody){
		"a raised spend cap": func(b *AzureExecuteExchangeRequestBody) {
			b.MaxPaymentDue = "9999.00"
		},
		"a differently spelled currency": func(b *AzureExecuteExchangeRequestBody) {
			b.Currency = "usd"
		},
		"the billing scope spelled out rather than omitted": func(b *AzureExecuteExchangeRequestBody) {
			for i := range b.Targets {
				b.Targets[i].BillingScopeID = "/subscriptions/sub-1"
			}
		},
		"ARM identifiers in a different case": func(b *AzureExecuteExchangeRequestBody) {
			b.SubscriptionID = strings.ToUpper(b.SubscriptionID)
			b.Sources[0].ReservationID = strings.ToUpper(b.Sources[0].ReservationID)
			b.Targets[0].SKU = strings.ToUpper(b.Targets[0].SKU)
			b.Targets[0].Location = "EastUS"
			b.Targets[0].Term = "p1y"
		},
		"surrounding whitespace": func(b *AzureExecuteExchangeRequestBody) {
			b.SubscriptionID = " sub-1 "
			b.Sources[0].ReservationID = "res-1 "
			b.Targets[0].SKU = " Standard_D8s_v3"
		},
		"sources and targets sent in the opposite order": func(b *AzureExecuteExchangeRequestBody) {
			b.Sources[0], b.Sources[1] = b.Sources[1], b.Sources[0]
			b.Targets[0], b.Targets[1] = b.Targets[1], b.Targets[0]
		},
	}
	for name, mutate := range variants {
		t.Run(name, func(t *testing.T) {
			b := azureKeyBody()
			b.Sources = append(b.Sources, AzureExchangeSourceBody{ReservationID: "res-2", Quantity: 1})
			b.Targets = append(b.Targets, AzureExchangeTargetBody{SKU: "Standard_D2s_v3", Location: "eastus", Term: "P3Y", Quantity: 1})
			mutate(&b)
			assert.Equal(t, want, azureExchangeIdempotencyKey(b),
				"%s does not change what the exchange buys, so it must not mint a fresh key", name)
		})
	}
}

// TestAzureExchangeIdempotencyKey_DistinguishesEveryPurchaseField walks every
// field that DOES change what is bought and requires each to move the
// fingerprint. A field missing here would let a distinct exchange be swallowed
// by an earlier one's claim.
func TestAzureExchangeIdempotencyKey_DistinguishesEveryPurchaseField(t *testing.T) {
	// Keyed by fingerprint so a collision with ANY earlier variant is caught,
	// not just with the baseline.
	seen := map[string]string{azureExchangeIdempotencyKey(azureKeyBody()): "the baseline exchange"}

	variants := map[string]func(*AzureExecuteExchangeRequestBody){
		"a different source reservation": func(b *AzureExecuteExchangeRequestBody) {
			b.Sources[0].ReservationID = "res-9"
		},
		"a different source quantity": func(b *AzureExecuteExchangeRequestBody) {
			b.Sources[0].Quantity = 5
		},
		"an extra source": func(b *AzureExecuteExchangeRequestBody) {
			b.Sources = append(b.Sources, AzureExchangeSourceBody{ReservationID: "res-2", Quantity: 1})
		},
		"a different target SKU": func(b *AzureExecuteExchangeRequestBody) {
			b.Targets[0].SKU = "Standard_D16s_v3"
		},
		"a different target location": func(b *AzureExecuteExchangeRequestBody) {
			b.Targets[0].Location = "westeurope"
		},
		"a different target term": func(b *AzureExecuteExchangeRequestBody) {
			b.Targets[0].Term = "P3Y"
		},
		"a different target quantity": func(b *AzureExecuteExchangeRequestBody) {
			b.Targets[0].Quantity = 8
		},
		"an extra target": func(b *AzureExecuteExchangeRequestBody) {
			b.Targets = append(b.Targets, AzureExchangeTargetBody{SKU: "Standard_D2s_v3", Location: "eastus", Term: "P1Y", Quantity: 1})
		},
	}
	for name, mutate := range variants {
		b := azureKeyBody()
		mutate(&b)
		key := azureExchangeIdempotencyKey(b)
		if other, clash := seen[key]; clash {
			t.Errorf("%q fingerprints the same as %q; the second exchange would be silently refused", name, other)
			continue
		}
		seen[key] = name
	}
}

func awsKeyBody() ExchangeExecuteRequestBody {
	return ExchangeExecuteRequestBody{
		RIIDs:            []string{"ri-123"},
		Targets:          []ExchangeTargetBody{{OfferingID: "off-1", Count: 2}},
		MaxPaymentDueUSD: "250.50",
		Region:           "eu-central-1",
	}
}

// TestAwsExchangeIdempotencyKey_ScopedToAccountAndRegion pins the AWS scope
// dimensions. RI exchanges are region-scoped and act on the RIs of whichever
// account the deployment resolves to, so both belong in the fingerprint: two
// accounts, or two regions of one account, can hold same-named RI ids.
func TestAwsExchangeIdempotencyKey_ScopedToAccountAndRegion(t *testing.T) {
	body := awsKeyBody()
	base := awsExchangeIdempotencyKey("acct-1", body)

	otherRegion := body
	otherRegion.Region = "us-east-1"

	assert.NotEqual(t, base, awsExchangeIdempotencyKey("acct-2", body),
		"a fingerprint blind to the cloud account aliases across accounts")
	assert.NotEqual(t, base, awsExchangeIdempotencyKey("acct-1", otherRegion),
		"a fingerprint blind to the region aliases across regions")
	assert.NotEqual(t, base, awsExchangeIdempotencyKey(unattributedAccountConstraint, body),
		"the unattributed sentinel is its own scope, not a wildcard")
}

// TestAwsExchangeIdempotencyKey_StableAcrossNonPurchaseFields mirrors the
// Azure stability test. The legacy/array equivalence matters most: the two
// spellings build the identical AWS request (pkg/exchange.buildTargetConfigs),
// so a retry that switches spelling must not mint a fresh key.
func TestAwsExchangeIdempotencyKey_StableAcrossNonPurchaseFields(t *testing.T) {
	want := awsExchangeIdempotencyKey("acct-1", awsKeyBody())

	variants := map[string]func(*ExchangeExecuteRequestBody){
		"a raised spend cap": func(b *ExchangeExecuteRequestBody) {
			b.MaxPaymentDueUSD = "9999.00"
		},
		"the legacy singleton spelling of the same target": func(b *ExchangeExecuteRequestBody) {
			b.Targets = nil
			b.TargetOfferingID = "off-1"
			b.TargetCount = 2
		},
		"legacy fields shadowed by an equivalent targets[]": func(b *ExchangeExecuteRequestBody) {
			b.TargetOfferingID = "off-ignored"
			b.TargetCount = 99
		},
		"identifiers in a different case, with whitespace": func(b *ExchangeExecuteRequestBody) {
			b.RIIDs = []string{" RI-123"}
			b.Targets = []ExchangeTargetBody{{OfferingID: "OFF-1 ", Count: 2}}
			b.Region = "EU-Central-1"
		},
	}
	for name, mutate := range variants {
		t.Run(name, func(t *testing.T) {
			b := awsKeyBody()
			mutate(&b)
			assert.Equal(t, want, awsExchangeIdempotencyKey("acct-1", b),
				"%s does not change what the exchange buys, so it must not mint a fresh key", name)
		})
	}
}

// TestAwsExchangeIdempotencyKey_DistinguishesEveryPurchaseField is the
// no-swallowing direction for AWS.
func TestAwsExchangeIdempotencyKey_DistinguishesEveryPurchaseField(t *testing.T) {
	seen := map[string]string{awsExchangeIdempotencyKey("acct-1", awsKeyBody()): "the baseline exchange"}

	variants := map[string]func(*ExchangeExecuteRequestBody){
		"a different source RI": func(b *ExchangeExecuteRequestBody) {
			b.RIIDs = []string{"ri-999"}
		},
		"an extra source RI": func(b *ExchangeExecuteRequestBody) {
			b.RIIDs = append(b.RIIDs, "ri-456")
		},
		"the same source RI listed twice": func(b *ExchangeExecuteRequestBody) {
			b.RIIDs = append(b.RIIDs, "ri-123")
		},
		"a different target offering": func(b *ExchangeExecuteRequestBody) {
			b.Targets[0].OfferingID = "off-9"
		},
		"a different target count": func(b *ExchangeExecuteRequestBody) {
			b.Targets[0].Count = 3
		},
		"an extra target": func(b *ExchangeExecuteRequestBody) {
			b.Targets = append(b.Targets, ExchangeTargetBody{OfferingID: "off-2", Count: 1})
		},
	}
	for name, mutate := range variants {
		b := awsKeyBody()
		mutate(&b)
		key := awsExchangeIdempotencyKey("acct-1", b)
		if other, clash := seen[key]; clash {
			t.Errorf("%q fingerprints the same as %q; the second exchange would be silently refused", name, other)
			continue
		}
		seen[key] = name
	}
}

// TestExchangeIdempotencyKey_ProviderScopesNeverCollide feeds the AWS and
// Azure derivations component lists that are identical by construction. The
// provider scope is what keeps them apart, and it must, because the two share
// one claim table.
func TestExchangeIdempotencyKey_ProviderScopesNeverCollide(t *testing.T) {
	sources := []string{"same-source"}
	targets := []string{"same-target"}
	assert.NotEqual(t,
		exchangeIdempotencyKey(azureExchangeIdempotencyScope, "same-scope", sources, targets),
		exchangeIdempotencyKey(awsExchangeIdempotencyScope, "same-scope", sources, targets))
}

// TestExchangeIdempotencyKey_FieldBoundariesAreUnambiguous pins the
// length-prefixed encoding. Without it a value carrying the separator could be
// crafted to shift a field boundary, making two genuinely different exchanges
// fingerprint alike -- which suppresses the second one.
func TestExchangeIdempotencyKey_FieldBoundariesAreUnambiguous(t *testing.T) {
	cases := map[string][2]string{
		"a character moved across the scope boundary": {
			exchangeIdempotencyKey("ab", "c", nil, nil),
			exchangeIdempotencyKey("a", "bc", nil, nil),
		},
		"an element moved from sources to targets": {
			exchangeIdempotencyKey("s", "a", []string{"x", "y"}, nil),
			exchangeIdempotencyKey("s", "a", []string{"x"}, []string{"y"}),
		},
		"an element carrying the separator": {
			exchangeIdempotencyKey("s", "a", []string{"x|y"}, nil),
			exchangeIdempotencyKey("s", "a", []string{"x", "y"}, nil),
		},
		"an empty element versus no element": {
			exchangeIdempotencyKey("s", "a", []string{""}, nil),
			exchangeIdempotencyKey("s", "a", nil, nil),
		},
	}
	for name, pair := range cases {
		assert.NotEqual(t, pair[0], pair[1], "%s must change the fingerprint", name)
	}
}

// TestClaimExchangeSubmit_Outcomes covers the three answers the ledger can
// give, independently of either handler.
