package main

import "testing"

// TestEffectiveDryRun documents the flag contract for real purchases and guards
// the regression where `--purchase` alone silently stayed in dry-run because
// `--dry-run` defaulted to true (effectiveDryRun = !ActualPurchase || DryRun,
// so !true || true == true). The default for --dry-run is now false; safety is
// preserved by ActualPurchase defaulting to false (a bare run is still dry-run).
func TestEffectiveDryRun(t *testing.T) {
	tests := []struct {
		name           string
		actualPurchase bool // --purchase
		dryRun         bool // --dry-run (default false)
		want           bool
	}{
		{name: "bare invocation is dry-run", actualPurchase: false, dryRun: false, want: true},
		{name: "--purchase executes real purchases", actualPurchase: true, dryRun: false, want: false},
		{name: "--purchase --dry-run forces dry-run (explicit safety override)", actualPurchase: true, dryRun: true, want: true},
		{name: "--dry-run without --purchase stays dry-run", actualPurchase: false, dryRun: true, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := effectiveDryRun(Config{ActualPurchase: tt.actualPurchase, DryRun: tt.dryRun})
			if got != tt.want {
				t.Errorf("effectiveDryRun(ActualPurchase=%v, DryRun=%v) = %v, want %v",
					tt.actualPurchase, tt.dryRun, got, tt.want)
			}
		})
	}
}

// TestDryRunFlagDefaultIsFalse guards the actual fix: the --dry-run flag must
// default to false. When it defaulted to true, `--purchase` alone resolved to
// effectiveDryRun == true (!ActualPurchase || DryRun => !true || true), so real
// purchases never executed. A bare run stays dry-run via ActualPurchase's own
// false default, so flipping this default is safe.
func TestDryRunFlagDefaultIsFalse(t *testing.T) {
	f := rootCmd.Flags().Lookup("dry-run")
	if f == nil {
		t.Fatal("--dry-run flag not registered")
	}
	if f.DefValue != "false" {
		t.Errorf("--dry-run default = %q, want \"false\" (so --purchase alone executes real purchases)", f.DefValue)
	}
}
