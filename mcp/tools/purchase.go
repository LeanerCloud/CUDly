package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/provider"
)

// purchaseMode is the outcome of the dry_run/confirm safety gate: either a
// local preview (no provider call) or a real execution (provider call with
// PurchaseSourceMCP + an idempotency token). There is deliberately no third
// "no-op" outcome -- an ambiguous combination of flags is always an error,
// never a silent do-nothing (feedback_no_silent_fallbacks).
type purchaseMode int

const (
	modePreview purchaseMode = iota
	modeExecute
)

// decidePurchaseMode applies the safety rail from the design doc (§7): a
// real purchase requires confirm=true AND dry_run=false. dry_run=true always
// wins and returns a preview, regardless of confirm, so a caller previewing
// a purchase can leave confirm at its default. The only refusal case is
// dry_run=false with confirm=false: the caller asked for a real purchase
// but did not confirm it, which must surface as an explicit error rather
// than silently downgrading to a preview or silently doing nothing.
func decidePurchaseMode(dryRun, confirm bool) (purchaseMode, error) {
	if dryRun {
		return modePreview, nil
	}
	if confirm {
		return modeExecute, nil
	}
	return 0, fmt.Errorf("refusing real purchase: dry_run=false requires confirm=true (got confirm=false); " +
		"set dry_run=true to preview this purchase instead, or confirm=true to execute it")
}

// ResolveClientFunc lazily resolves the provider.ServiceClient that will
// receive the real PurchaseCommitment call. It is a func, not an
// already-resolved client, so ExecutePurchase can prove (and tests can
// assert) that a preview never triggers provider/credential resolution --
// only modeExecute invokes it.
type ResolveClientFunc func(ctx context.Context) (provider.ServiceClient, error)

// PurchaseRequest is the provider-agnostic input to ExecutePurchase. Each
// per-service tool handler builds one after validating its own typed
// parameters and constructing the common.Recommendation.
type PurchaseRequest struct {
	Region         string
	Recommendation common.Recommendation
	DryRun         bool
	Confirm        bool
	ResolveClient  ResolveClientFunc
}

// PurchaseResponse is the structured result returned to the MCP caller for
// both preview and real-purchase outcomes. Error is a string (not the Go
// error) because it crosses the MCP JSON-RPC boundary as tool output, not a
// protocol-level error -- ExecutePurchase itself still returns a Go error
// for gate refusals and provider-call failures.
type PurchaseResponse struct {
	Success           bool    `json:"success"`
	DryRun            bool    `json:"dry_run"`
	CommitmentID      string  `json:"commitment_id,omitempty"`
	Cost              float64 `json:"cost"`
	OnDemandCost      float64 `json:"on_demand_cost"`
	EstimatedSavings  float64 `json:"estimated_savings"`
	SavingsPercentage float64 `json:"savings_percentage"`
	EffectiveDate     string  `json:"effective_date,omitempty"`
	TermYears         int     `json:"term_years,omitempty"`
	Error             string  `json:"error,omitempty"`
}

// idempotencyKeyFor derives a stable per-request key from the fields that
// identify what is being bought, so a caller re-driving the exact same tool
// call (e.g. after a network timeout) reuses the same
// common.DeriveIdempotencyToken output and the provider dedupes the retry
// instead of double-purchasing. A materially different request (different
// count, region, term, ...) always derives a different key. This is a
// request-scoped substitute for the purchase_executions row that the CLI/web
// paths use as their idempotency anchor (pkg/common/tokens.go) -- the MCP
// server has no such row, so the request's own identifying fields play that
// role.
func idempotencyKeyFor(region string, rec common.Recommendation) string {
	return fmt.Sprintf("mcp:%s:%s:%s:%s:%s:%d:%s:%s",
		rec.Provider, rec.Account, region, rec.Service, rec.ResourceType, rec.Count, rec.Term, rec.PaymentOption)
}

// ExecutePurchase runs the shared dry_run/confirm safety gate and, for a
// real purchase, resolves the service client and calls PurchaseCommitment
// with PurchaseSourceMCP and a derived idempotency token. It never calls
// ResolveClient in preview mode, so a preview makes zero provider/SDK calls.
func ExecutePurchase(ctx context.Context, req PurchaseRequest) (*PurchaseResponse, error) {
	mode, err := decidePurchaseMode(req.DryRun, req.Confirm)
	if err != nil {
		return nil, err
	}

	rec := req.Recommendation
	if mode == modePreview {
		return &PurchaseResponse{
			Success:           true,
			DryRun:            true,
			Cost:              rec.CommitmentCost,
			OnDemandCost:      rec.OnDemandCost,
			EstimatedSavings:  rec.EstimatedSavings,
			SavingsPercentage: rec.SavingsPercentage,
		}, nil
	}

	if req.ResolveClient == nil {
		return nil, fmt.Errorf("internal error: no ResolveClient configured for real purchase")
	}
	client, err := req.ResolveClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve %s service client: %w", rec.Provider, err)
	}

	token := common.DeriveIdempotencyToken(idempotencyKeyFor(req.Region, rec), 0)
	opts := common.PurchaseOptions{
		Source:           common.PurchaseSourceMCP,
		IdempotencyToken: token,
	}

	result, err := client.PurchaseCommitment(ctx, rec, opts)
	if err != nil {
		// Full provider error text surfaces to the caller (feedback:
		// providers must never swallow the underlying SDK/HTTP error).
		return nil, fmt.Errorf("purchase commitment failed: %w", err)
	}

	resp := &PurchaseResponse{
		Success:           result.Success,
		DryRun:            result.DryRun,
		CommitmentID:      result.CommitmentID,
		Cost:              result.Cost,
		OnDemandCost:      rec.OnDemandCost,
		EstimatedSavings:  rec.EstimatedSavings,
		SavingsPercentage: rec.SavingsPercentage,
		EffectiveDate:     result.Timestamp.Format(time.RFC3339),
	}
	if result.Error != nil {
		resp.Error = result.Error.Error()
	}
	return resp, nil
}
