package ladder

import (
	"fmt"
	"math/big"
	"strings"
	"time"
)

// PlannedAction is a single commitment action proposed by the ladder engine.
//
// Every action must carry a non-empty Rationale for audit and approval-email
// readability -- automated money-path decisions must be explained so the
// human approver (or the audit log) can understand why each action was chosen.
type PlannedAction struct {
	Action ActionType
	Layer  LayerType
	// AmountUSDPerHour is the hourly commitment delta. It must be non-nil and
	// positive for ActionPurchase, and must be nil for ActionHold and
	// ActionReshape (whose financial impact is implicit in the underlying
	// exchange operation).
	AmountUSDPerHour *big.Rat
	// Term is the commitment term (e.g., "1yr", "3yr"). Populated for purchase
	// actions; empty for hold and reshape.
	Term string
	// PaymentOption is the payment structure (e.g., "all-upfront",
	// "no-upfront"). Populated for purchase actions; empty for hold and reshape.
	PaymentOption string
	// Rationale is a human-readable explanation of why this action was chosen.
	// Must be non-empty for all action types.
	Rationale string
	// DataSources lists the data feeds (Cost Explorer, CloudWatch, etc.) used
	// to derive this action. Aids auditability.
	DataSources []string
}

// Validate checks that the action is self-consistent:
//   - action type and layer type must be recognized
//   - rationale must be non-empty
//   - purchase actions require a positive AmountUSDPerHour and non-empty
//     Term and PaymentOption (money-shaping fields must be present)
//   - hold and reshape actions require a nil AmountUSDPerHour
func (a *PlannedAction) Validate() error {
	if err := a.Action.Validate(); err != nil {
		return fmt.Errorf("action: %w", err)
	}
	if err := a.Layer.Validate(); err != nil {
		return fmt.Errorf("layer: %w", err)
	}
	if a.Rationale == "" {
		return fmt.Errorf("rationale is required (money-path auditability)")
	}
	switch a.Action {
	case ActionPurchase:
		return a.validatePurchaseFields()
	case ActionHold, ActionReshape:
		if a.AmountUSDPerHour != nil {
			return fmt.Errorf("%s action requires nil AmountUSDPerHour, got %s",
				a.Action, a.AmountUSDPerHour.RatString())
		}
	}
	return nil
}

// validatePurchaseFields checks the money-shaping fields required for a
// purchase action. Split out of Validate to keep each function's cyclomatic
// complexity within the repo limit.
func (a *PlannedAction) validatePurchaseFields() error {
	if a.AmountUSDPerHour == nil {
		return fmt.Errorf("purchase action requires a non-nil AmountUSDPerHour")
	}
	if a.AmountUSDPerHour.Sign() <= 0 {
		return fmt.Errorf("purchase action requires a positive AmountUSDPerHour, got %s",
			a.AmountUSDPerHour.RatString())
	}
	// Term and PaymentOption shape the money committed; a purchase with
	// either missing would silently default at the provider boundary.
	// pkg/common has no typed Term/PaymentOption enums yet, so non-empty
	// is the strongest check available here.
	if a.Term == "" {
		return fmt.Errorf("purchase action requires a non-empty Term")
	}
	if a.PaymentOption == "" {
		return fmt.Errorf("purchase action requires a non-empty PaymentOption")
	}
	return nil
}

// LadderPlan is a complete, validated commitment plan for one scope and run.
// It captures the baseline, the monetary gap, and the ordered list of actions
// the engine proposes to close that gap.
//
//nolint:revive // Ladder* prefix is the spec-mandated public name (issue #1334); matches pkg/exchange's Exchange* convention.
type LadderPlan struct {
	Scope       Scope
	GeneratedAt time.Time
	// TargetUSDPerHour is the hourly commitment target derived from
	// Baseline * TargetCoveragePct. nil means it could not be computed.
	TargetUSDPerHour *big.Rat
	// ExistingUSDPerHour is the sum of hourly amortized costs across all
	// active commitment layers. nil means it could not be measured.
	ExistingUSDPerHour *big.Rat
	// GapUSDPerHour is TargetUSDPerHour - ExistingUSDPerHour. nil when either
	// input is nil.
	GapUSDPerHour *big.Rat
	Actions       []PlannedAction
	Baseline      UsageBaseline
}

// Validate checks that all PlannedActions in the plan are self-consistent.
func (p *LadderPlan) Validate() error {
	for i, a := range p.Actions {
		if err := a.Validate(); err != nil {
			return fmt.Errorf("action[%d]: %w", i, err)
		}
	}
	return nil
}

// formatUSDPerHour renders a *big.Rat as a fixed 2-decimal USD/hr string.
// nil is rendered as "unknown" -- never as "$0.00" -- to unambiguously
// distinguish absent data from a genuine zero commitment (project rule:
// absent numbers are nil, not zero).
func formatUSDPerHour(v *big.Rat) string {
	if v == nil {
		return "unknown"
	}
	f, _ := v.Float64()
	return fmt.Sprintf("$%.2f/hr", f)
}

// Explain returns a deterministic, human-readable multi-line summary of the
// plan. The output is suitable for use as an approval email body and is
// designed to be read by a non-technical budget owner.
//
// Layout:
//  1. Scope header and generation timestamp
//  2. Baseline parameters (lookback window, percentile)
//  3. Target / existing / gap hourly rates
//  4. Numbered action list (or "none" when all layers are at target)
func (p *LadderPlan) Explain() string {
	var b strings.Builder

	fmt.Fprintf(&b, "Scope: provider=%s account=%s\n", p.Scope.Provider, p.Scope.AccountID)
	fmt.Fprintf(&b, "Generated: %s\n", p.GeneratedAt.UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "Baseline: lookback=%dd percentile=%.0f\n",
		p.Baseline.LookbackDays, p.Baseline.Percentile)
	fmt.Fprintf(&b, "Target:   %s\n", formatUSDPerHour(p.TargetUSDPerHour))
	fmt.Fprintf(&b, "Existing: %s\n", formatUSDPerHour(p.ExistingUSDPerHour))
	fmt.Fprintf(&b, "Gap:      %s\n", formatUSDPerHour(p.GapUSDPerHour))

	if len(p.Actions) == 0 {
		fmt.Fprintf(&b, "Actions: none (all layers at target)\n")
		return b.String()
	}

	fmt.Fprintf(&b, "Actions (%d):\n", len(p.Actions))
	for i, a := range p.Actions {
		switch a.Action {
		case ActionPurchase:
			fmt.Fprintf(&b, "  %d. %s %s %s term=%s payment=%s -- %s\n",
				i+1, a.Action, a.Layer, formatUSDPerHour(a.AmountUSDPerHour),
				a.Term, a.PaymentOption, a.Rationale)
		default:
			fmt.Fprintf(&b, "  %d. %s %s -- %s\n",
				i+1, a.Action, a.Layer, a.Rationale)
		}
	}
	return b.String()
}
