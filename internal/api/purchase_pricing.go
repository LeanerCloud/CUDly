package api

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/LeanerCloud/CUDly/pkg/common"
)

// priceAndEnforcePurchaseConstraints replaces every rec's cost fields with
// values derived from the stored recommendation that matches it, then
// enforces the execute:purchases Constraints against those values. The
// client-supplied upfront_cost / monthly_cost / savings / details are never
// consulted for the spend cap, the execution row or the approval email
// (issue #1905, audit A01-001). Wrapped in one call so
// validateExecutePurchaseRequest keeps its gocyclo score (it is at the
// pre-commit ceiling of 10).
func (h *Handler) priceAndEnforcePurchaseConstraints(ctx context.Context, session *Session, recs []config.RecommendationRecord) error {
	if err := h.priceRecommendationsFromStore(ctx, recs); err != nil {
		return err
	}
	return h.enforcePurchaseConstraints(ctx, session, recs)
}

// priceRecommendationsFromStore rewrites recs in place. Each rec must match
// a row of the stored recommendation set on its identity tuple
// (recIdentityKey); unmatched recs are refused rather than priced from the
// request. Matching by tuple instead of by rec.ID lets the purchase modal's
// term/payment change (#111, #197, #1903) resolve to the stored variant the
// user actually chose: AWS stores every (term, payment) combination and
// Azure stores both payment variants, each under its own id.
func (h *Handler) priceRecommendationsFromStore(ctx context.Context, recs []config.RecommendationRecord) error {
	stored, err := h.loadStoredRecommendationIndex(ctx, recs)
	if err != nil {
		return err
	}
	for i := range recs {
		match, ok := stored[recIdentityKey(&recs[i])]
		if !ok {
			return NewClientError(409, fmt.Sprintf(
				"recommendation %d (%s) is not in the current recommendation set; refresh the recommendations and try again",
				i, describeRec(&recs[i])))
		}
		priced, err := priceFromStored(&recs[i], &match, i)
		if err != nil {
			return err
		}
		recs[i] = priced
	}
	return nil
}

// loadStoredRecommendationIndex reads the stored rows for every provider in
// the batch (one query per distinct provider, at most three) and indexes
// them by identity tuple. The store's unique index on the same tuple
// (migration 000043) guarantees one row per key.
func (h *Handler) loadStoredRecommendationIndex(ctx context.Context, recs []config.RecommendationRecord) (map[string]config.RecommendationRecord, error) {
	index := make(map[string]config.RecommendationRecord)
	seen := make(map[string]bool)
	for i := range recs {
		provider := recs[i].Provider
		if seen[provider] {
			continue
		}
		seen[provider] = true
		rows, err := h.config.ListStoredRecommendations(ctx, config.RecommendationFilter{Provider: provider})
		if err != nil {
			return nil, fmt.Errorf("load stored recommendations for %s: %w", provider, err)
		}
		for j := range rows {
			index[recIdentityKey(&rows[j])] = rows[j]
		}
	}
	return index, nil
}

// recIdentityKey is the tuple a price is a function of: the same eight
// components the scheduler encodes into RecommendationRecord.ID, with a nil
// and an empty CloudAccountID collapsed together (as purchaseConstraintSets
// and the store's account_key both do). Provider and payment are lowercased
// because validatePurchaseRecommendation canonicalises the request side.
func recIdentityKey(rec *config.RecommendationRecord) string {
	account := ""
	if rec.CloudAccountID != nil {
		account = *rec.CloudAccountID
	}
	return strings.Join([]string{
		strings.ToLower(rec.Provider), account, rec.Service, rec.Region, rec.ResourceType,
		rec.Engine, strconv.Itoa(rec.Term), strings.ToLower(rec.Payment),
	}, "\x1f")
}

// priceFromStored returns a copy of stored carrying the request's Count,
// RecommendedCount and Selected, with UpfrontCost, MonthlyCost, Savings and
// OnDemandCost scaled by Count/stored.Count (stored costs are totals for
// stored.Count; RI/CUD/Azure prices are linear in count). Savings Plans are
// not count-denominated, so their Count must equal the stored placeholder.
// A stored row that cannot yield a positive price is refused: the cap must
// never be evaluated against a zero or negative commitment.
func priceFromStored(req, stored *config.RecommendationRecord, idx int) (config.RecommendationRecord, error) {
	if stored.Count <= 0 {
		return config.RecommendationRecord{}, NewClientError(409, fmt.Sprintf(
			"recommendation %d (%s): stored recommendation has count %d, cannot derive a per-unit price",
			idx, describeRec(req), stored.Count))
	}
	if stored.UpfrontCost < 0 || (stored.MonthlyCost != nil && *stored.MonthlyCost < 0) || recTotalCommitment(stored) <= 0 {
		return config.RecommendationRecord{}, NewClientError(409, fmt.Sprintf(
			"recommendation %d (%s): stored recommendation carries no usable price (upfront %.2f, monthly %s); the spend cap cannot be enforced against it",
			idx, describeRec(req), stored.UpfrontCost, formatOptionalCost(stored.MonthlyCost)))
	}
	if common.IsSavingsPlan(common.ServiceType(stored.Service)) && req.Count != stored.Count {
		return config.RecommendationRecord{}, NewClientError(409, fmt.Sprintf(
			"recommendation %d (%s): savings plan count %d must equal the recommended count %d; savings plans are priced by hourly commitment, not by count",
			idx, describeRec(req), req.Count, stored.Count))
	}
	ratio := float64(req.Count) / float64(stored.Count)
	out := *stored
	out.Count = req.Count
	out.RecommendedCount = req.RecommendedCount
	out.Selected = req.Selected
	out.UpfrontCost = stored.UpfrontCost * ratio
	out.Savings = stored.Savings * ratio
	out.MonthlyCost = scaledCost(stored.MonthlyCost, ratio)
	out.OnDemandCost = scaledCost(stored.OnDemandCost, ratio)
	return out, nil
}

func scaledCost(v *float64, ratio float64) *float64 {
	if v == nil {
		return nil
	}
	s := *v * ratio
	return &s
}

func formatOptionalCost(v *float64) string {
	if v == nil {
		return "absent"
	}
	return fmt.Sprintf("%.2f", *v)
}

func describeRec(rec *config.RecommendationRecord) string {
	return fmt.Sprintf("%s/%s %s %s engine=%q term=%d payment=%s account=%s",
		rec.Provider, rec.Service, rec.Region, rec.ResourceType, rec.Engine, rec.Term, rec.Payment, derefStringOrEmpty(rec.CloudAccountID))
}
