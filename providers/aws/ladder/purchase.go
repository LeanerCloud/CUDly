package ladder

import (
	"context"
	"fmt"
	"math"

	"github.com/LeanerCloud/CUDly/pkg/common"
	"github.com/LeanerCloud/CUDly/pkg/ladder"
)

// PurchaseLayer buys a commitment for the given layer by dispatching to the
// injected purchase client:
//
//   - LayerConvertibleRI  -> riPurchaser (EC2 PurchaseReservedInstancesOffering
//     with the idempotency-tag dedupe guard)
//   - LayerEC2InstanceSP  -> spPurchaser with an EC2Instance-plan recommendation
//   - LayerComputeSP      -> spPurchaser with a Compute-plan recommendation
//
// Boundary validation happens BEFORE any client call (this is a money path;
// nothing is bought on malformed input):
//
//   - layer must be one of the three supported AWS layers (unknown -> error);
//   - opts.IdempotencyToken must be non-empty: idempotency is mandatory on
//     this purchase path so a re-driven execution can never double-buy
//     (non-empty guard for idempotency-source fields at the function boundary);
//   - rec must carry what the target client needs (see validateRIPurchaseRec /
//     validateSPPurchaseRec).
//
// Client errors are wrapped with layer context via %w, so a client that
// returns common.ErrCommitmentPurchaseNotSupported still satisfies
// errors.Is(err, common.ErrCommitmentPurchaseNotSupported) at the engine.
// The client's PurchaseResult is returned alongside the error because the
// concrete clients populate result.Error and partial state on failure.
func (a *AWSLadder) PurchaseLayer(ctx context.Context, layer ladder.LayerType, rec common.Recommendation, opts common.PurchaseOptions) (common.PurchaseResult, error) {
	if a.riPurchase == nil || a.spPurchase == nil {
		return common.PurchaseResult{}, fmt.Errorf("PurchaseLayer: %w", errWriteNotWired)
	}

	var planType string
	switch layer {
	case ladder.LayerConvertibleRI:
		// planType stays empty; the RI path validates differently below.
	case ladder.LayerEC2InstanceSP:
		planType = spPlanTypeEC2Instance
	case ladder.LayerComputeSP:
		planType = spPlanTypeCompute
	default:
		return common.PurchaseResult{}, fmt.Errorf("PurchaseLayer: layer %q is not a supported AWS ladder layer (want %s, %s, or %s)",
			layer, ladder.LayerConvertibleRI, ladder.LayerEC2InstanceSP, ladder.LayerComputeSP)
	}

	if opts.IdempotencyToken == "" {
		return common.PurchaseResult{}, fmt.Errorf(
			"PurchaseLayer(%s): opts.IdempotencyToken must not be empty: idempotency is mandatory on the ladder purchase path so re-driven executions cannot double-buy",
			layer)
	}

	if layer == ladder.LayerConvertibleRI {
		return a.purchaseRI(ctx, &rec, opts)
	}
	return a.purchaseSP(ctx, layer, planType, &rec, opts)
}

// purchaseRI validates and executes an EC2 convertible RI purchase. rec is a
// pointer to avoid re-copying the large Recommendation struct internally; the
// client call dereferences it to match the ServiceClient value contract.
func (a *AWSLadder) purchaseRI(ctx context.Context, rec *common.Recommendation, opts common.PurchaseOptions) (common.PurchaseResult, error) {
	if err := validateRIPurchaseRec(rec); err != nil {
		return common.PurchaseResult{}, fmt.Errorf("PurchaseLayer(%s): %w", ladder.LayerConvertibleRI, err)
	}
	result, err := a.riPurchase.PurchaseCommitment(ctx, *rec, opts)
	if err != nil {
		return result, fmt.Errorf("PurchaseLayer(%s): EC2 convertible RI purchase failed: %w", ladder.LayerConvertibleRI, err)
	}
	return result, nil
}

// purchaseSP validates and executes a Savings Plan purchase for the given
// layer/plan type pair. rec is a pointer for the same reason as purchaseRI.
func (a *AWSLadder) purchaseSP(ctx context.Context, layer ladder.LayerType, planType string, rec *common.Recommendation, opts common.PurchaseOptions) (common.PurchaseResult, error) {
	if err := validateSPPurchaseRec(rec, planType); err != nil {
		return common.PurchaseResult{}, fmt.Errorf("PurchaseLayer(%s): %w", layer, err)
	}
	result, err := a.spPurchase.PurchaseCommitment(ctx, *rec, opts)
	if err != nil {
		return result, fmt.Errorf("PurchaseLayer(%s): %s Savings Plan purchase failed: %w", layer, planType, err)
	}
	return result, nil
}

// validateRIPurchaseRec checks that rec carries everything the EC2 client's
// PurchaseCommitment needs: ComputeDetails (offering lookup uses
// InstanceType/Platform/Tenancy/Scope from it), a positive instance count
// (PurchaseReservedInstancesOffering InstanceCount), and the term/payment
// option strings the offering query converts.
//
// Platform, Tenancy, and Scope are REQUIRED non-empty (no-silent-fallback
// rule): the ec2 client silently defaults an empty Tenancy to "default" and
// an empty Scope to "Regional", which could buy a default-tenancy RI from a
// recommendation that meant dedicated tenancy. On this money path the intent
// must be explicit, so empties are rejected here before any AWS call.
func validateRIPurchaseRec(rec *common.Recommendation) error {
	details, ok := rec.Details.(*common.ComputeDetails)
	if !ok || details == nil {
		return fmt.Errorf("recommendation Details must be *common.ComputeDetails for an EC2 RI purchase, got %T", rec.Details)
	}
	if rec.Count <= 0 {
		return fmt.Errorf("recommendation Count must be > 0 for an EC2 RI purchase, got %d", rec.Count)
	}
	if details.InstanceType == "" {
		return fmt.Errorf("ComputeDetails.InstanceType must not be empty for an EC2 RI purchase")
	}
	if details.Platform == "" {
		return fmt.Errorf("ComputeDetails.Platform must not be empty for an EC2 RI purchase (offering lookup matches on it)")
	}
	if details.Tenancy == "" {
		return fmt.Errorf("ComputeDetails.Tenancy must not be empty for an EC2 RI purchase (the ec2 client would silently default it to %q)", "default")
	}
	if details.Scope == "" {
		return fmt.Errorf("ComputeDetails.Scope must not be empty for an EC2 RI purchase (the ec2 client would silently default it to %q)", "Regional")
	}
	return validateTermAndPayment(rec)
}

// validateSPPurchaseRec checks that rec carries everything the Savings Plans
// client's PurchaseCommitment needs: SavingsPlanDetails with a positive,
// finite HourlyCommitment (CreateSavingsPlan Commitment) and a PlanType
// matching the dispatched layer, plus the term/payment option strings the
// offering query converts. The plan-type match is enforced here in addition
// to the scoped client's own check so a mislabeled recommendation fails with
// layer context before any AWS call.
func validateSPPurchaseRec(rec *common.Recommendation, wantPlanType string) error {
	details, ok := rec.Details.(*common.SavingsPlanDetails)
	if !ok || details == nil {
		return fmt.Errorf("recommendation Details must be *common.SavingsPlanDetails for a Savings Plan purchase, got %T", rec.Details)
	}
	if details.PlanType != wantPlanType {
		return fmt.Errorf("recommendation plan type %q does not match the dispatched layer's plan type %q", details.PlanType, wantPlanType)
	}
	if math.IsNaN(details.HourlyCommitment) || math.IsInf(details.HourlyCommitment, 0) || details.HourlyCommitment <= 0 {
		return fmt.Errorf("SavingsPlanDetails.HourlyCommitment must be a positive finite value, got %g", details.HourlyCommitment)
	}
	return validateTermAndPayment(rec)
}

// validateTermAndPayment checks the two offering-query fields shared by both
// purchase paths. Both clients convert these strings (convertTermToSeconds /
// convertPaymentOption and the EC2 equivalents); empty values would fail
// deeper with a less actionable error.
func validateTermAndPayment(rec *common.Recommendation) error {
	if rec.Term == "" {
		return fmt.Errorf("recommendation Term must not be empty (offering lookup needs it)")
	}
	if rec.PaymentOption == "" {
		return fmt.Errorf("recommendation PaymentOption must not be empty (offering lookup needs it)")
	}
	return nil
}
