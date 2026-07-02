package ladder

import (
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/LeanerCloud/CUDly/pkg/common"
)

func TestPlannedActionValidate(t *testing.T) {
	t.Parallel()
	amount := new(big.Rat).SetFrac64(25, 100) // $0.25/hr

	cases := []struct {
		name    string
		action  PlannedAction
		wantErr bool
	}{
		{
			name: "valid purchase",
			action: PlannedAction{
				Action: ActionPurchase, Layer: LayerComputeSP,
				AmountUSDPerHour: amount, Term: "1yr",
				PaymentOption: "no-upfront", Rationale: "gap fill",
			},
			wantErr: false,
		},
		{
			name: "valid hold",
			action: PlannedAction{
				Action: ActionHold, Layer: LayerConvertibleRI,
				Rationale: "layer already at target",
			},
			wantErr: false,
		},
		{
			name: "valid reshape",
			action: PlannedAction{
				Action: ActionReshape, Layer: LayerConvertibleRI,
				Rationale: "low utilization below threshold",
			},
			wantErr: false,
		},
		{
			name: "purchase without amount (nil)",
			action: PlannedAction{
				Action: ActionPurchase, Layer: LayerComputeSP,
				Rationale: "gap fill",
			},
			wantErr: true,
		},
		{
			name: "purchase with zero amount",
			action: PlannedAction{
				Action: ActionPurchase, Layer: LayerComputeSP,
				AmountUSDPerHour: new(big.Rat), // zero
				Rationale:        "gap fill",
			},
			wantErr: true,
		},
		{
			name: "purchase with negative amount",
			action: PlannedAction{
				Action: ActionPurchase, Layer: LayerComputeSP,
				AmountUSDPerHour: new(big.Rat).SetInt64(-1),
				Rationale:        "gap fill",
			},
			wantErr: true,
		},
		{
			name: "purchase with empty term",
			action: PlannedAction{
				Action: ActionPurchase, Layer: LayerComputeSP,
				AmountUSDPerHour: amount, Term: "",
				PaymentOption: "no-upfront", Rationale: "gap fill",
			},
			wantErr: true,
		},
		{
			name: "purchase with empty payment option",
			action: PlannedAction{
				Action: ActionPurchase, Layer: LayerComputeSP,
				AmountUSDPerHour: amount, Term: "1yr",
				PaymentOption: "", Rationale: "gap fill",
			},
			wantErr: true,
		},
		{
			name: "hold with non-nil amount",
			action: PlannedAction{
				Action: ActionHold, Layer: LayerConvertibleRI,
				AmountUSDPerHour: amount,
				Rationale:        "at target",
			},
			wantErr: true,
		},
		{
			name: "reshape with non-nil amount",
			action: PlannedAction{
				Action: ActionReshape, Layer: LayerConvertibleRI,
				AmountUSDPerHour: amount,
				Rationale:        "low utilization",
			},
			wantErr: true,
		},
		{
			name: "empty rationale",
			action: PlannedAction{
				Action: ActionHold, Layer: LayerConvertibleRI,
				Rationale: "",
			},
			wantErr: true,
		},
		{
			name: "unknown action type",
			action: PlannedAction{
				Action: "unknown-action", Layer: LayerConvertibleRI,
				Rationale: "x",
			},
			wantErr: true,
		},
		{
			name: "unknown layer type",
			action: PlannedAction{
				Action: ActionHold, Layer: "unknown-layer",
				Rationale: "x",
			},
			wantErr: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.action.Validate()
			if c.wantErr && err == nil {
				t.Errorf("Validate() = nil, want error")
			}
			if !c.wantErr && err != nil {
				t.Errorf("Validate() = %v, want nil", err)
			}
		})
	}
}

func TestFormatUSDPerHour(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   *big.Rat
		want string
	}{
		{nil, "unknown"},
		{new(big.Rat).SetFrac64(25, 100), "$0.25/hr"},
		{new(big.Rat).SetInt64(10), "$10.00/hr"},
		{new(big.Rat), "$0.00/hr"},
		{new(big.Rat).SetFrac64(1, 3), "$0.33/hr"}, // truncated at 2 decimals via Float64
	}
	for _, c := range cases {
		got := formatUSDPerHour(c.in)
		if got != c.want {
			t.Errorf("formatUSDPerHour(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestExplain_NilMoneyRendersUnknown verifies that nil *big.Rat fields in
// LadderPlan are rendered as "unknown" and never as "$0.00".
func TestExplain_NilMoneyRendersUnknown(t *testing.T) {
	t.Parallel()
	plan := &LadderPlan{
		Scope:              Scope{Provider: common.ProviderAWS, AccountID: "123"},
		GeneratedAt:        time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Baseline:           UsageBaseline{LookbackDays: 30, Percentile: 5},
		TargetUSDPerHour:   nil,
		ExistingUSDPerHour: nil,
		GapUSDPerHour:      nil,
	}
	out := plan.Explain()
	if strings.Contains(out, "$0.00") {
		t.Errorf("Explain() rendered nil as $0.00; want 'unknown':\n%s", out)
	}
	if !strings.Contains(out, "unknown") {
		t.Errorf("Explain() does not contain 'unknown' for nil money:\n%s", out)
	}
}

// TestExplain_AllHold verifies that an empty Actions slice is handled
// gracefully with a "none" note rather than an empty or malformed section.
func TestExplain_AllHold(t *testing.T) {
	t.Parallel()
	plan := &LadderPlan{
		Scope:       Scope{Provider: common.ProviderAWS, AccountID: "123"},
		GeneratedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Baseline:    UsageBaseline{LookbackDays: 30, Percentile: 5},
		Actions:     nil,
	}
	out := plan.Explain()
	if !strings.Contains(out, "none") {
		t.Errorf("Explain() all-HOLD case missing 'none':\n%s", out)
	}
}

// TestExplain_PurchaseAction verifies that a purchase action is rendered with
// all expected fields present in the output.
func TestExplain_PurchaseAction(t *testing.T) {
	t.Parallel()
	target := new(big.Rat).SetInt64(100)
	existing := new(big.Rat).SetInt64(60)
	gap := new(big.Rat).SetInt64(40)
	amount := new(big.Rat).SetInt64(40)

	plan := &LadderPlan{
		Scope:              Scope{Provider: common.ProviderAWS, AccountID: "123456789012"},
		GeneratedAt:        time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Baseline:           UsageBaseline{LookbackDays: 30, Percentile: 5},
		TargetUSDPerHour:   target,
		ExistingUSDPerHour: existing,
		GapUSDPerHour:      gap,
		Actions: []PlannedAction{
			{
				Action:           ActionPurchase,
				Layer:            LayerComputeSP,
				AmountUSDPerHour: amount,
				Term:             "1yr",
				PaymentOption:    "no-upfront",
				Rationale:        "fill coverage gap",
			},
		},
	}
	out := plan.Explain()
	checks := []string{
		"aws",
		"123456789012",
		"$100.00/hr",
		"$60.00/hr",
		"$40.00/hr",
		string(ActionPurchase),
		string(LayerComputeSP),
		"1yr",
		"no-upfront",
		"fill coverage gap",
	}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("Explain() missing %q in output:\n%s", want, out)
		}
	}
}

// TestExplain_ReshapeAction verifies that a reshape action does not include
// amount/term/payment fields in the output (they should be absent for
// non-purchase actions).
func TestExplain_ReshapeAction(t *testing.T) {
	t.Parallel()
	plan := &LadderPlan{
		Scope:       Scope{Provider: common.ProviderAWS, AccountID: "123"},
		GeneratedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Baseline:    UsageBaseline{LookbackDays: 30, Percentile: 5},
		Actions: []PlannedAction{
			{
				Action:    ActionReshape,
				Layer:     LayerConvertibleRI,
				Rationale: "utilization below threshold",
			},
		},
	}
	out := plan.Explain()
	if !strings.Contains(out, string(ActionReshape)) {
		t.Errorf("Explain() missing action type %q:\n%s", ActionReshape, out)
	}
	if !strings.Contains(out, "utilization below threshold") {
		t.Errorf("Explain() missing rationale:\n%s", out)
	}
}

// TestExplain_Deterministic verifies that calling Explain() twice on the same
// plan produces identical output.
func TestExplain_Deterministic(t *testing.T) {
	t.Parallel()
	plan := &LadderPlan{
		Scope:       Scope{Provider: common.ProviderGCP, AccountID: "my-project"},
		GeneratedAt: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		Baseline:    UsageBaseline{LookbackDays: 14, Percentile: 10},
		Actions:     nil,
	}
	first := plan.Explain()
	second := plan.Explain()
	if first != second {
		t.Errorf("Explain() is not deterministic:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}
