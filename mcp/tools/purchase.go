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

	// Nonce is optional; when non-empty it is used verbatim as the
	// idempotency discriminator instead of the automatic time bucket,
	// letting a caller force two calls to dedupe as the same purchase
	// regardless of elapsed time. When empty (the default), the
	// discriminator is derived automatically from the current time bucket
	// so identical-looking-but-actually-separate purchases don't silently
	// collide. See idempotencyKeyFor.
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

// idempotencyBucket is the width of the automatic time-based discriminator
// folded into the idempotency key when the caller does not supply an
// explicit idempotency_nonce (see idempotencyKeyFor). Wide enough that a
// caller's own rapid retry of a recent call (e.g. after a network timeout --
// this guard's original purpose) still lands in the same bucket and dedupes
// as before; narrow enough that two genuinely separate purchases made hours
// or days apart (e.g. "buy 3 RIs now, buy 3 more next week" -- the
// adversarial review finding this constant fixes) never collide by
// accident.
const idempotencyBucket = time.Hour

// idempotencyClock is a seam so tests can freeze "now" and assert exact
// bucket-boundary behavior deterministically instead of depending on
// wall-clock timing.
var idempotencyClock = time.Now

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
// A second issue found in adversarial review: every field above identifies
// WHAT is being bought, not WHEN -- so two genuinely distinct purchases with
// identical parameters (a $5/hr Compute Savings Plan followed a week later
// by a genuinely separate $5/hr purchase) previously collided on the same
// token, and providers/aws/services/ec2/client.go's findRIByIdempotencyToken
// silently treated the second, real purchase as a retry of the first and
// skipped it. nonce and idempotencyBucket fix this: an explicit
// caller-supplied nonce is used verbatim as the discriminator when present
// (letting a caller force strict, long-lived dedup across an arbitrary gap);
// otherwise the discriminator falls back to the current idempotencyBucket-
// wide time bucket, so a rapid retry within the same bucket still dedupes as
// before, but two calls separated by more than a bucket width derive
// different keys and both purchases go through.
func idempotencyKeyFor(region string, rec common.Recommendation, nonce string) string {
	discriminator := nonce
	if discriminator == "" {
		discriminator = idempotencyClock().UTC().Truncate(idempotencyBucket).Format(time.RFC3339)
	}
	return fmt.Sprintf("mcp:%s:%s:%s:%s:%d:%s:%s:%s:%s",
		rec.Provider, region, rec.Service, rec.ResourceType, rec.Count, rec.Term, rec.PaymentOption,
		detailsKeyComponent(rec.Details), discriminator)
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
	}
	if result.Error != nil {
		resp.Error = result.Error.Error()
	}
	return resp, nil
}
