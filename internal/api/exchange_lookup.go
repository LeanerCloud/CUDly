// Package api provides the HTTP API handlers for the CUDly dashboard.
package api

import (
	"context"
	"strings"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/pkg/exchange"
)

// recsLister is the narrow slice of config.StoreInterface that the
// reshape lookup needs. Scoped here so the closure stays unit-testable
// against a tiny fake instead of the full StoreInterface mock.
type recsLister interface {
	ListStoredRecommendations(ctx context.Context, filter config.RecommendationFilter) ([]config.RecommendationRecord, error)
}

// purchaseRecLookupFromStore builds an exchange.PurchaseRecLookup that
// reads the cached AWS Cost Explorer purchase recommendations out of
// Postgres for a given (region, currency) pair. Each
// RecommendationRecord is mapped to an OfferingOption with:
//
//   - InstanceType   = rec.ResourceType        (e.g. "m6i.large")
//   - OfferingID     = rec.ID                  (UUID; the UI uses this as a stable handle)
//   - EffectiveMonthlyCost = (UpfrontCost / termMonths) + MonthlyCost
//   - NormalizationFactor  = exchange.NormalizationFactorForSize(size)
//   - CurrencyCode   = currencyCode            (propagated; recs don't carry it)
//
// Where termMonths = rec.Term × 12 (rec.Term is in years, AWS standard
// for RIs / Savings Plans). Term ≤ 0 means we can't amortise upfront,
// so we fall back to MonthlyCost alone — the dollar-units check will
// then accept or reject based on monthly recurring vs. source.
//
// AccountID scoping (cross-account leak guard): when accountID is
// non-empty we restrict the query to that single CloudAccount UUID so
// reshape can't surface another tenant's recs. Empty accountID means
// "no filter" — used for ambient-credentials deployments where the
// caller couldn't (or chose not to) resolve the source account.
func purchaseRecLookupFromStore(store recsLister, accountID string) exchange.PurchaseRecLookup {
	return func(ctx context.Context, region, currencyCode string) ([]exchange.OfferingOption, error) {
		filter := config.RecommendationFilter{
			Provider: "aws",
			Service:  "ec2",
			Region:   region,
		}
		if accountID != "" {
			filter.AccountIDs = []string{accountID}
		}
		recs, err := store.ListStoredRecommendations(ctx, filter)
		if err != nil {
			return nil, err
		}
		out := make([]exchange.OfferingOption, 0, len(recs))
		for _, rec := range recs {
			out = append(out, recommendationToOffering(rec, currencyCode))
		}
		return out, nil
	}
}

// recommendationToOffering maps a single cached Cost Explorer purchase
// recommendation to the OfferingOption shape the reshape layer
// consumes. Pulled out so the mapping can be unit-tested in isolation
// (no DB / no ctx required).
func recommendationToOffering(rec config.RecommendationRecord, currencyCode string) exchange.OfferingOption {
	monthly := rec.MonthlyCost
	if rec.Term > 0 {
		// rec.Term is in years; canonical AWS RI/SP amortisation uses
		// 12 months per year regardless of leap years.
		termMonths := float64(rec.Term * 12)
		if termMonths > 0 {
			monthly += rec.UpfrontCost / termMonths
		}
	}
	_, size := splitInstanceType(rec.ResourceType)
	return exchange.OfferingOption{
		InstanceType:         rec.ResourceType,
		OfferingID:           rec.ID,
		EffectiveMonthlyCost: monthly,
		NormalizationFactor:  exchange.NormalizationFactorForSize(size),
		CurrencyCode:         currencyCode,
	}
}

// splitInstanceType splits "m5.xlarge" into ("m5", "xlarge"). Returns
// empty strings if the format is unrecognized. Mirrors the helper in
// pkg/exchange/reshape.go but kept local to avoid exporting a
// general-purpose parser the caller doesn't need.
func splitInstanceType(instanceType string) (family, size string) {
	parts := strings.SplitN(instanceType, ".", 2)
	if len(parts) != 2 {
		return "", ""
	}
	return parts[0], parts[1]
}

// resolveAWSCloudAccountID maps the running AWS account ID (raw, from
// STS) to the registered CloudAccount UUID so the reshape lookup can
// scope its query against the correct row in the recommendations
// table. Returns "" when:
//   - no AWS account ID could be resolved (STS denied / ambient creds
//     not available);
//   - no CloudAccount row exists with that ExternalID (the operator
//     hasn't registered this account yet — common on first-run / dev).
//
// Empty UUID propagates to purchaseRecLookupFromStore as "skip the
// AccountIDs filter", which causes the lookup to return whatever recs
// exist in the region. That's intentional: a deployment with one
// ambient-credentials account and no registered CloudAccounts should
// still see alternatives, not a permanently empty list. Once the
// operator registers the account the filter engages automatically.
func (h *Handler) resolveAWSCloudAccountID(ctx context.Context) string {
	awsAccountID := h.resolveAWSAccountID(ctx)
	if awsAccountID == "" {
		return ""
	}
	provider := "aws"
	accounts, err := h.config.ListCloudAccounts(ctx, config.CloudAccountFilter{Provider: &provider})
	if err != nil || len(accounts) == 0 {
		return ""
	}
	for _, a := range accounts {
		if a.ExternalID == awsAccountID {
			return a.ID
		}
	}
	return ""
}
