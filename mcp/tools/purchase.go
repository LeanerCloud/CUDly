package tools

import (
	"context"
	"fmt"
	"strconv"
	"strings"
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

	// Nonce is optional. When non-empty, this call is treated as a
	// DISTINCT purchase from an otherwise-identical one (authorizes a
	// deliberate repeat, e.g. "buy 3 more RIs" on top of an earlier "buy 3
	// RIs" with the same parameters). When empty (the default), identical
	// purchases dedupe so retries never double-buy. See idempotencyKeyFor.
	Nonce string
}

// PurchaseResponse is the structured result returned to the MCP caller for
// both preview and real-purchase outcomes. Error is a string (not the Go
// error) because it crosses the MCP JSON-RPC boundary as tool output, not a
// protocol-level error -- ExecutePurchase itself still returns a Go error
// for gate refusals and provider-call failures.
//
// Cost/OnDemandCost/EstimatedSavings/SavingsPercentage are pointers with
// omitempty: none of the *FromArgs constructors in this package populate
// Recommendation's cost fields (they build a fresh Recommendation from the
// caller's typed args, not from a priced search result), and some provider
// clients (e.g. AWS EC2 RIs, Savings Plans) never populate
// PurchaseResult.Cost either. A plain float64 could not distinguish "not
// known" from "genuinely $0", so every response reported 0 for money fields
// it never actually priced. A pointer that's nil (and omitted from the JSON
// payload entirely) when no real value exists lets a caller tell "unknown"
// apart from "confirmed zero" (feedback_nullable_not_zero).
type PurchaseResponse struct {
	Success           bool     `json:"success"`
	DryRun            bool     `json:"dry_run"`
	CommitmentID      string   `json:"commitment_id,omitempty"`
	Cost              *float64 `json:"cost,omitempty"`
	OnDemandCost      *float64 `json:"on_demand_cost,omitempty"`
	EstimatedSavings  *float64 `json:"estimated_savings,omitempty"`
	SavingsPercentage *float64 `json:"savings_percentage,omitempty"`
	EffectiveDate     string   `json:"effective_date,omitempty"`
	TermYears         int      `json:"term_years,omitempty"`
	Error             string   `json:"error,omitempty"`
}

// termYearsFromRecommendationTerm extracts the integer commitment length in
// years from a Recommendation.Term string in the "<N>yr" format every
// *FromArgs constructor in this package writes via
// TermYears.RecommendationTerm() (enums.go). Returns 0 when term does not
// match that format (e.g. an empty Term), so PurchaseResponse.TermYears is
// simply omitted (it has `omitempty`) rather than reporting a fabricated
// value.
func termYearsFromRecommendationTerm(term string) int {
	years, err := strconv.Atoi(strings.TrimSuffix(term, "yr"))
	if err != nil {
		return 0
	}
	return years
}

// nonZeroCostPtr returns a pointer to v, or nil when v is exactly zero. Cost
// and savings fields on common.Recommendation and common.PurchaseResult are
// plain (unpointered) float64s that upstream code sometimes never populates
// (see the PurchaseResponse doc comment above); this treats an unpopulated
// zero as "unknown" rather than fabricating a real $0 figure the caller
// never priced.
func nonZeroCostPtr(v float64) *float64 {
	if v == 0 {
		return nil
	}
	return &v
}

// idempotencyKeyFor derives a stable per-request key from every field that
// identifies what is being bought: provider, region, service, resource type,
// count, term, payment option, plus every service-specific dimension held in
// rec.Details (see detailsKeyComponent) -- platform/tenancy/scope for EC2
// RIs, engine/az_config for RDS RIs, engine for ElastiCache RIs, hourly
// commitment/instance family for Savings Plans, memory for GCP CUDs. A
// caller re-driving the exact same tool call (e.g. after a network timeout)
// reuses the same common.DeriveIdempotencyToken output and the provider
// dedupes the retry instead of double-purchasing; a request that differs in
// ANY price- or identity-affecting dimension derives a different key instead
// of silently colliding with an unrelated purchase (issue found in review:
// a $5/hr and $50/hr Compute Savings Plan previously shared a token because
// only HourlyCommitment differed and Details was never consulted). This is a
// request-scoped substitute for the purchase_executions row that the CLI/web
// paths use as their idempotency anchor (pkg/common/tokens.go) -- the MCP
// server has no such row, so the request's own identifying fields play that
// role.
//
// rec.Account is deliberately excluded: no *FromArgs constructor in this
// package populates it today, so folding it in would add an always-empty,
// misleading key component rather than real discrimination.
//
// This function is deliberately fail-safe with respect to time: it folds in
// no clock reading of any kind. When nonce is empty (the default), two calls
// with identical dimensions ALWAYS derive the same key, no matter how far
// apart in time they happen -- so a retry that straddles any time boundary
// still dedupes at the provider instead of risking a double purchase. An
// earlier version of this function instead folded in an automatic hourly
// time bucket to distinguish "buy 3 RIs now" from a genuinely separate "buy
// 3 more next week" with identical parameters; that inverted the safety
// direction of this money path, because a retry that happened to straddle
// an hour boundary (e.g. a slow request issued at 12:59:58 retried at
// 13:00:02) derived a different key and could double-buy. The worst case of
// the current, fail-safe default is a skipped intentional repeat -- a
// caller who genuinely wants a second, identical purchase must say so
// explicitly. nonce is that explicit opt-in: when the caller supplies a
// non-empty nonce, it is folded into the key so an otherwise-identical
// purchase becomes a distinct one (e.g. "buy 3 now" then "buy 3 more next
// week" by passing a fresh nonce on the second call); the same nonce plus
// the same dimensions still dedupes a nonce'd retry.
func idempotencyKeyFor(region string, rec common.Recommendation, nonce string) string {
	return fmt.Sprintf("mcp:%s:%s:%s:%s:%d:%s:%s:%s:%s",
		rec.Provider, region, rec.Service, rec.ResourceType, rec.Count, rec.Term, rec.PaymentOption,
		detailsKeyComponent(rec.Details), nonce)
}

// detailsKeyComponent returns a canonical, deterministic encoding of every
// field in rec.Details that the purchase tools in this package populate, so
// idempotencyKeyFor can fold service-specific price-affecting dimensions
// into the token. Each case lists every field of its concrete Details type
// explicitly (not a hand-picked subset) so a field added to one of these
// types later shows up here as a visible diff rather than a silent key gap.
// Returns "" for nil or an unrecognized Details (e.g.
// azure_compute_ri.go's tool, whose recommendation carries no Details at
// all).
func detailsKeyComponent(details common.ServiceDetails) string {
	switch d := details.(type) {
	case *common.ComputeDetails:
		return computeDetailsKey(d)
	case common.ComputeDetails:
		return computeDetailsKey(&d)
	case *common.DatabaseDetails:
		return databaseDetailsKey(d)
	case *common.CacheDetails:
		return cacheDetailsKey(d)
	case *common.SavingsPlanDetails:
		return savingsPlanDetailsKey(d)
	default:
		return ""
	}
}

func computeDetailsKey(d *common.ComputeDetails) string {
	if d == nil {
		return ""
	}
	return fmt.Sprintf("instance_type=%s;platform=%s;tenancy=%s;scope=%s;vcpu=%d;memory_gb=%g",
		d.InstanceType, d.Platform, d.Tenancy, d.Scope, d.VCPU, d.MemoryGB)
}

func databaseDetailsKey(d *common.DatabaseDetails) string {
	if d == nil {
		return ""
	}
	return fmt.Sprintf("engine=%s;engine_version=%s;az_config=%s;instance_class=%s;deployment=%s",
		d.Engine, d.EngineVersion, d.AZConfig, d.InstanceClass, d.Deployment)
}

func cacheDetailsKey(d *common.CacheDetails) string {
	if d == nil {
		return ""
	}
	return fmt.Sprintf("engine=%s;node_type=%s;shards=%d", d.Engine, d.NodeType, d.Shards)
}

func savingsPlanDetailsKey(d *common.SavingsPlanDetails) string {
	if d == nil {
		return ""
	}
	return fmt.Sprintf("plan_type=%s;hourly_commitment=%g;coverage=%s;instance_family=%s;region=%s;offering_id=%s",
		d.PlanType, d.HourlyCommitment, d.Coverage, d.InstanceFamily, d.Region, d.OfferingID)
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
			Cost:              nonZeroCostPtr(rec.CommitmentCost),
			OnDemandCost:      nonZeroCostPtr(rec.OnDemandCost),
			EstimatedSavings:  nonZeroCostPtr(rec.EstimatedSavings),
			SavingsPercentage: nonZeroCostPtr(rec.SavingsPercentage),
			TermYears:         termYearsFromRecommendationTerm(rec.Term),
		}, nil
	}

	if req.ResolveClient == nil {
		return nil, fmt.Errorf("internal error: no ResolveClient configured for real purchase")
	}
	client, err := req.ResolveClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve %s service client: %w", rec.Provider, err)
	}

	token := common.DeriveIdempotencyToken(idempotencyKeyFor(req.Region, rec, req.Nonce), 0)
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
		Cost:              nonZeroCostPtr(result.Cost),
		OnDemandCost:      nonZeroCostPtr(rec.OnDemandCost),
		EstimatedSavings:  nonZeroCostPtr(rec.EstimatedSavings),
		SavingsPercentage: nonZeroCostPtr(rec.SavingsPercentage),
		EffectiveDate:     result.Timestamp.Format(time.RFC3339),
		TermYears:         termYearsFromRecommendationTerm(rec.Term),
	}
	if result.Error != nil {
		resp.Error = result.Error.Error()
	}
	return resp, nil
}
