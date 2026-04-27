// Package api provides the HTTP API handlers for the CUDly dashboard.
package api

import (
	"context"
	"fmt"
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
//
// TermSeconds is derived from rec.Term (years) using the canonical
// AWS RI duration constant 31_536_000s/year — the same value the AWS
// SDK reports on ec2.ReservedInstance.Duration so the term-match guard
// in pkg/exchange.fillAlternativesFromRecs can compare apples-to-apples
// against RIInfo.TermSeconds populated from ec2.ConvertibleRI.Duration.
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
	var termSeconds int64
	if rec.Term > 0 {
		termSeconds = int64(rec.Term) * secondsPerYear
	}
	return exchange.OfferingOption{
		InstanceType:         rec.ResourceType,
		OfferingID:           rec.ID,
		EffectiveMonthlyCost: monthly,
		NormalizationFactor:  exchange.NormalizationFactorForSize(size),
		CurrencyCode:         currencyCode,
		TermSeconds:          termSeconds,
	}
}

// secondsPerYear is the AWS-canonical RI duration constant for a 1-year
// term (365 × 86400). Matches the value the AWS SDK reports on
// ec2.ReservedInstance.Duration and the value
// ec2.ConvertibleRI.Duration carries — used so OfferingOption.TermSeconds
// can be compared directly against RIInfo.TermSeconds.
const secondsPerYear int64 = 365 * 24 * 60 * 60

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
// table. Returns ("", nil) for the intentionally-unregistered cases:
//
//   - AWS SDK config could not load (deployment is running on an
//     Azure / GCP host with no AWS context at all — resolveAWSAccountID
//     returns ("", nil) for this case);
//   - ListCloudAccounts returned no rows (no CloudAccounts registered
//     at all);
//   - no CloudAccount row matches the running ExternalID (the operator
//     hasn't registered THIS account yet — common on first-run / dev).
//
// All three are clean "no scope filter" signals — purchaseRecLookupFromStore
// treats ("", nil) as "skip the AccountIDs filter" so a deployment with
// ambient credentials and no registered CloudAccounts still sees
// alternatives, not a permanently empty list. Once the operator
// registers the account the filter engages automatically.
//
// FAIL CLOSED on real failures:
//   - resolveAWSAccountID returns a non-nil error (STS GetCallerIdentity
//     denied, transient AWS API failure, token expiry) — propagated so
//     the caller aborts the lookup rather than silently falling through
//     to an unscoped query that could leak another tenant's recs.
//   - ListCloudAccounts returns an error (DB outage, permissions) —
//     same treatment.
func (h *Handler) resolveAWSCloudAccountID(ctx context.Context) (string, error) {
	awsAccountID, err := h.resolveAWSAccountID(ctx)
	if err != nil {
		// STS reachable-but-failed: must NOT fall through to an
		// unscoped read. A transient STS error in a multi-tenant
		// deployment would otherwise surface another tenant's recs.
		return "", fmt.Errorf("resolve source aws account for reshape scope: %w", err)
	}
	if awsAccountID == "" {
		// Genuine "no AWS context" (Azure/GCP host, or unregistered).
		return "", nil
	}
	provider := "aws"
	accounts, err := h.config.ListCloudAccounts(ctx, config.CloudAccountFilter{Provider: &provider})
	if err != nil {
		return "", fmt.Errorf("list cloud accounts for reshape scope: %w", err)
	}
	for _, a := range accounts {
		if a.ExternalID == awsAccountID {
			return a.ID, nil
		}
	}
	return "", nil
}
