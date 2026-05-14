// Package api provides the HTTP API handlers for the CUDly dashboard.
package api

import (
	"context"
	"sort"
	"time"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/aws/aws-lambda-go/events"
)

// listActiveCommitments handles GET /api/inventory/commitments.
//
// Returns one row per *active* (non-expired) PurchaseHistoryRecord, with
// the account name joined in for display and expiry computed via the
// shared commitmentExpiry helper. Rows are sorted by EndDate ascending
// so the most-actionable (soonest-expiring) commitments float to the
// top — matches the dashboard's "what should I renew next?" framing
// without forcing the UI to re-sort client-side.
//
// Auth: `view:purchases`. Purchase history is the same source we already
// gate behind that permission for the /api/history page, so reusing the
// resource keeps the role matrix consistent. The session's
// allowed_accounts list is then applied via
// filterPurchaseHistoryByAllowedAccounts so a restricted-access user
// sees only the commitments belonging to accounts they're entitled to.
func (h *Handler) listActiveCommitments(ctx context.Context, req *events.LambdaFunctionURLRequest, params map[string]string) (any, error) {
	session, err := h.requirePermission(ctx, req, "view", "purchases")
	if err != nil {
		return nil, err
	}

	purchases, err := h.fetchCommitmentRecords(ctx, params)
	if err != nil {
		return nil, err
	}

	purchases, err = h.filterPurchaseHistoryByAllowedAccounts(ctx, session, purchases)
	if err != nil {
		return nil, err
	}

	nameByID := h.resolveAccountNamesByID(ctx)
	now := time.Now()

	commitments := make([]InventoryCommitment, 0, len(purchases))
	for _, p := range purchases {
		if !isActiveCommitment(p, now) {
			continue
		}
		commitments = append(commitments, buildInventoryCommitment(p, nameByID[p.AccountID]))
	}

	// Soonest-expiring first. The dashboard framing is "what do I need to
	// renew next" — surfacing the imminent end_date on top means the
	// frontend doesn't need to re-sort and the user's eye lands on the
	// most-actionable rows immediately.
	sort.SliceStable(commitments, func(i, j int) bool {
		return commitments[i].EndDate.Before(commitments[j].EndDate)
	})

	return InventoryCommitmentsResponse{Commitments: commitments}, nil
}

// fetchCommitmentRecords reads purchase history from the store, honouring
// an optional `account_id` query param the same way fetchPurchaseHistory
// does for /api/history. Limit defaults to MaxListLimit — commitments
// are a strict subset of purchase history (we drop expired rows before
// returning) so a high cap is appropriate; an over-truncation here
// would silently hide rows the user is entitled to see.
func (h *Handler) fetchCommitmentRecords(ctx context.Context, params map[string]string) ([]config.PurchaseHistoryRecord, error) {
	if accountID := params["account_id"]; accountID != "" {
		return h.config.GetPurchaseHistory(ctx, accountID, config.MaxListLimit)
	}
	return h.config.GetAllPurchaseHistory(ctx, config.MaxListLimit)
}

// buildInventoryCommitment maps a PurchaseHistoryRecord to the
// response-layer InventoryCommitment. The ID is namespaced by account so
// the JSON payload is globally unique without a DB schema change —
// purchase_id alone is only unique within an account.
func buildInventoryCommitment(p config.PurchaseHistoryRecord, accountName string) InventoryCommitment {
	return InventoryCommitment{
		ID:               p.AccountID + ":" + p.PurchaseID,
		Provider:         p.Provider,
		AccountID:        p.AccountID,
		AccountName:      accountName,
		Service:          p.Service,
		ResourceType:     p.ResourceType,
		Region:           p.Region,
		Count:            p.Count,
		TermYears:        p.Term,
		PaymentOption:    p.Payment,
		StartDate:        p.Timestamp,
		EndDate:          commitmentExpiry(p),
		UpfrontCost:      p.UpfrontCost,
		MonthlyCost:      p.MonthlyCost,
		EstimatedSavings: p.EstimatedSavings,
		Status:           "active",
	}
}
