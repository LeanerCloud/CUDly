package purchase

import (
	"testing"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Guard reach vs dispatch reach (issue #1668) ------------------------
//
// A CodeRabbit finding on PR #1713 proposed normalizing rec.Service before the
// Azure savings-plans re-drive guard, on the grounds that exact matching lets
// variants like "SavingsPlans" or "savings_plans" through.
//
// They do get past the guard. They cannot reach a purchase, which is the
// property that matters, and this test pins it:
//
//  1. mapServiceType is the ONLY thing standing between rec.Service and the
//     provider's service client (execution.go executeSinglePurchase calls
//     mapServiceType then GetServiceClient with the result).
//  2. Azure routes exactly ONE ServiceType to its savings-plans client:
//     `case common.ServiceSavingsPlansAll` in newServiceClientForSubscription
//     (providers/azure/provider.go). That switch does no normalization of its
//     own, and NewSavingsPlansClient is constructed nowhere else in the Azure
//     provider. Every other value lands in `default:` and returns
//     "unsupported service: <x>" without purchasing anything.
//
// So a value only reaches an Azure savings-plans purchase if
// mapServiceType(value) == ServiceSavingsPlansAll. The assertion below is that
// this is true for EXACTLY the values the guard refuses -- an iff, not a
// one-way implication. "SavingsPlans" is not refused by the guard AND does not
// dispatch, so it spends nothing.
//
// Adding a normalizer would WIDEN what the guard accepts as a savings plan
// while the dispatch axis stayed exact, creating a second normalization axis
// that would have to be kept in lockstep with the first forever. This repo has
// been bitten by exactly that shape. This test is the cheaper guarantee: if
// either axis ever moves, the iff breaks here.
func TestRedriveGuardReachMatchesDispatchReach(t *testing.T) {
	m := NewManager(ManagerConfig{})

	candidates := []string{
		// Every key of mapSavingsPlansSlug (execution.go). Only the first two
		// map to ServiceSavingsPlansAll; the rest are AWS plan-type slugs.
		"savings-plans", "savingsplans",
		"savings-plans-compute", "savingsplans-compute",
		"savings-plans-ec2instance", "savingsplans-ec2instance",
		"savings-plans-sagemaker", "savingsplans-sagemaker",
		"savings-plans-database", "savingsplans-database",

		// Every key of mapServiceSlug (execution.go).
		"compute", "relational-db", "cache", "search", "data-warehouse",
		"ec2", "rds", "elasticache", "opensearch", "redshift", "memorydb",

		// The literal value of all 20 common.ServiceType constants, so a value
		// that bypasses both slug maps and passes through verbatim is covered.
		string(common.ServiceCompute), string(common.ServiceRelationalDB),
		string(common.ServiceNoSQL), string(common.ServiceCache),
		string(common.ServiceSearch), string(common.ServiceDataWarehouse),
		string(common.ServiceStorage), string(common.ServiceSavingsPlansAll),
		string(common.ServiceSavingsPlansCompute), string(common.ServiceSavingsPlansEC2Instance),
		string(common.ServiceSavingsPlansSageMaker), string(common.ServiceSavingsPlansDatabase),
		string(common.ServiceCommitments), string(common.ServiceOther),
		string(common.ServiceEC2), string(common.ServiceRDS),
		string(common.ServiceElastiCache), string(common.ServiceOpenSearch),
		string(common.ServiceRedshift), string(common.ServiceMemoryDB),

		// The variants the finding named, plus neighboring mutations: case,
		// separator, whitespace, and near-miss spellings.
		"SavingsPlans", "SAVINGSPLANS", "SavingsPlansAll", "savingsPlans",
		"Savings-Plans", "SAVINGS-PLANS",
		"savings_plans", "savings_plans_compute",
		" savingsplans", "savingsplans ", "\tsavingsplans", "savings plans",
		"savingsplan", "saving-plans", "savingsplans\n",

		// Not a service at all.
		"", "unknown", "azure-savings-plans",
	}

	for _, service := range candidates {
		service := service
		t.Run("service="+service, func(t *testing.T) {
			rec := config.RecommendationRecord{Provider: "azure", Service: service}

			// Can this value reach Azure's savings-plans client at all?
			dispatchesToSavingsPlans := m.mapServiceType(service) == common.ServiceSavingsPlansAll
			// Does the money guard refuse it?
			guardRefuses := recRedriveRefusalReason(rec) != ""

			assert.Equal(t, dispatchesToSavingsPlans, guardRefuses,
				"guard reach and dispatch reach must be identical for %q: dispatchesToSavingsPlans=%v guardRefuses=%v. "+
					"A value that dispatches but is not refused is a double-purchase hole; a value that is refused but "+
					"cannot dispatch is an unretryable row for no reason",
				service, dispatchesToSavingsPlans, guardRefuses)
		})
	}
}

// TestRedriveGuardRefusalSetIsExactlyTheDispatchableSpellings states the same
// property as a closed set, so a change that widens EITHER axis is visible as a
// diff to this list rather than only as a failure in the loop above.
func TestRedriveGuardRefusalSetIsExactlyTheDispatchableSpellings(t *testing.T) {
	m := NewManager(ManagerConfig{})

	// The only two spellings that reach Azure's savings-plans client.
	for _, service := range []string{"savingsplans", "savings-plans"} {
		require.Equal(t, common.ServiceSavingsPlansAll, m.mapServiceType(service),
			"%q must still dispatch to the Azure savings-plans client", service)
		assert.NotEmpty(t, recRedriveRefusalReason(config.RecommendationRecord{Provider: "azure", Service: service}),
			"%q dispatches to a savings-plans purchase, so the re-drive guard must refuse it", service)
	}

	// Azure reservations must stay retryable: the guard has to be as narrow as
	// the provider gap, or legitimate retries are stranded.
	for _, service := range []string{"compute", "relational-db", "cache", "nosql", "memorydb", "search", "data-warehouse"} {
		assert.Empty(t, recRedriveRefusalReason(config.RecommendationRecord{Provider: "azure", Service: service}),
			"%q goes through DoIdempotentPurchaseTwoStep (#729) and must remain retryable", service)
	}
}
