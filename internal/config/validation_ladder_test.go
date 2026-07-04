package config

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// validLadderConfigDB returns a LadderConfigDB that passes Validate(). Each
// bounds test below clones this and mutates exactly one field so a failure
// pinpoints the single offending invariant.
func validLadderConfigDB() LadderConfigDB {
	return LadderConfigDB{
		CloudAccountID:             "11111111-1111-1111-1111-111111111111",
		Provider:                   "aws",
		Enabled:                    true,
		Mode:                       "email_approval",
		Cadence:                    "daily",
		TargetCoverage:             100,
		BufferFraction:             0.10,
		BaselinePercentile:         5,
		LookbackDays:               30,
		BufferUtilizationThreshold: 90,
		MaxActionsPerRun:           10,
		MaxHourlyCommitPerRun:      nil, // nil = no cap (valid)
		RampSchedule:               json.RawMessage(`{"steps":[{"after_days":0,"fraction":1.0}]}`),
	}
}

// TestLadderConfigDB_Validate_Bounds exercises every numeric bound of
// LadderConfigDB.Validate() at below-min / at-lower-bound / at-upper-bound /
// above-max / NaN (for float fields), asserting error vs nil per the documented
// ranges:
//
//	target_coverage              (0, 100]
//	baseline_percentile          (0, 50]
//	buffer_fraction              [0, 1)     -- note: 0 is VALID ("no buffer")
//	buffer_utilization_threshold (0, 100]
//	lookback_days                > 0
//	max_actions_per_run          > 0 and <= MaxLadderActionsPerRun (50)
//
// Struct field order (func, string, bool) is chosen for govet fieldalignment;
// the keyed literals below keep the human-readable name/mutate/wantErr order.
func TestLadderConfigDB_Validate_Bounds(t *testing.T) {
	cases := []struct {
		mutate  func(c *LadderConfigDB)
		name    string
		wantErr bool
	}{
		// baseline valid
		{name: "valid baseline", mutate: func(c *LadderConfigDB) {}, wantErr: false},

		// target_coverage (0, 100]
		{name: "target_coverage 0 (below min)", mutate: func(c *LadderConfigDB) { c.TargetCoverage = 0 }, wantErr: true},
		{name: "target_coverage 0.01 (just above lower)", mutate: func(c *LadderConfigDB) { c.TargetCoverage = 0.01 }, wantErr: false},
		{name: "target_coverage 100 (upper bound)", mutate: func(c *LadderConfigDB) { c.TargetCoverage = 100 }, wantErr: false},
		{name: "target_coverage 100.01 (above max)", mutate: func(c *LadderConfigDB) { c.TargetCoverage = 100.01 }, wantErr: true},
		{name: "target_coverage NaN", mutate: func(c *LadderConfigDB) { c.TargetCoverage = math.NaN() }, wantErr: true},

		// baseline_percentile (0, 50]
		{name: "baseline_percentile 0 (below min)", mutate: func(c *LadderConfigDB) { c.BaselinePercentile = 0 }, wantErr: true},
		{name: "baseline_percentile 0.01 (just above lower)", mutate: func(c *LadderConfigDB) { c.BaselinePercentile = 0.01 }, wantErr: false},
		{name: "baseline_percentile 50 (upper bound)", mutate: func(c *LadderConfigDB) { c.BaselinePercentile = 50 }, wantErr: false},
		{name: "baseline_percentile 50.01 (above max)", mutate: func(c *LadderConfigDB) { c.BaselinePercentile = 50.01 }, wantErr: true},
		{name: "baseline_percentile NaN", mutate: func(c *LadderConfigDB) { c.BaselinePercentile = math.NaN() }, wantErr: true},

		// buffer_fraction [0, 1) -- 0 is valid, 1 is excluded
		{name: "buffer_fraction 0 (valid lower bound, no buffer)", mutate: func(c *LadderConfigDB) { c.BufferFraction = 0 }, wantErr: false},
		{name: "buffer_fraction -0.01 (below min)", mutate: func(c *LadderConfigDB) { c.BufferFraction = -0.01 }, wantErr: true},
		{name: "buffer_fraction 0.999 (just below upper)", mutate: func(c *LadderConfigDB) { c.BufferFraction = 0.999 }, wantErr: false},
		{name: "buffer_fraction 1.0 (upper bound excluded)", mutate: func(c *LadderConfigDB) { c.BufferFraction = 1.0 }, wantErr: true},
		{name: "buffer_fraction NaN", mutate: func(c *LadderConfigDB) { c.BufferFraction = math.NaN() }, wantErr: true},

		// buffer_utilization_threshold (0, 100]
		{name: "buffer_utilization_threshold 0 (below min)", mutate: func(c *LadderConfigDB) { c.BufferUtilizationThreshold = 0 }, wantErr: true},
		{name: "buffer_utilization_threshold 0.01 (just above lower)", mutate: func(c *LadderConfigDB) { c.BufferUtilizationThreshold = 0.01 }, wantErr: false},
		{name: "buffer_utilization_threshold 100 (upper bound)", mutate: func(c *LadderConfigDB) { c.BufferUtilizationThreshold = 100 }, wantErr: false},
		{name: "buffer_utilization_threshold 100.01 (above max)", mutate: func(c *LadderConfigDB) { c.BufferUtilizationThreshold = 100.01 }, wantErr: true},
		{name: "buffer_utilization_threshold NaN", mutate: func(c *LadderConfigDB) { c.BufferUtilizationThreshold = math.NaN() }, wantErr: true},

		// lookback_days > 0
		{name: "lookback_days 0 (below min)", mutate: func(c *LadderConfigDB) { c.LookbackDays = 0 }, wantErr: true},
		{name: "lookback_days -1 (negative)", mutate: func(c *LadderConfigDB) { c.LookbackDays = -1 }, wantErr: true},
		{name: "lookback_days 1 (lower bound)", mutate: func(c *LadderConfigDB) { c.LookbackDays = 1 }, wantErr: false},

		// max_actions_per_run > 0 and <= 50
		{name: "max_actions_per_run 0 (below min)", mutate: func(c *LadderConfigDB) { c.MaxActionsPerRun = 0 }, wantErr: true},
		{name: "max_actions_per_run 1 (lower bound)", mutate: func(c *LadderConfigDB) { c.MaxActionsPerRun = 1 }, wantErr: false},
		{name: "max_actions_per_run 50 (upper bound)", mutate: func(c *LadderConfigDB) { c.MaxActionsPerRun = MaxLadderActionsPerRun }, wantErr: false},
		{name: "max_actions_per_run 51 (above max)", mutate: func(c *LadderConfigDB) { c.MaxActionsPerRun = MaxLadderActionsPerRun + 1 }, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := validLadderConfigDB()
			tc.mutate(&c)
			err := c.Validate()
			if tc.wantErr {
				require.Error(t, err, "expected Validate() to reject case %q", tc.name)
			} else {
				require.NoError(t, err, "expected Validate() to accept case %q", tc.name)
			}
		})
	}
}

// TestLadderConfigDB_Validate_MaxHourlyCommitPerRun covers the pointer-typed
// optional cap: nil = no cap (valid), a positive value is valid, and
// non-positive / NaN / Inf are rejected.
func TestLadderConfigDB_Validate_MaxHourlyCommitPerRun(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	cases := []struct {
		val     *float64
		name    string
		wantErr bool
	}{
		{name: "nil (no cap)", val: nil, wantErr: false},
		{name: "positive", val: f(12.5), wantErr: false},
		{name: "zero", val: f(0), wantErr: true},
		{name: "negative", val: f(-1), wantErr: true},
		{name: "NaN", val: f(math.NaN()), wantErr: true},
		{name: "positive Inf", val: f(math.Inf(1)), wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := validLadderConfigDB()
			c.MaxHourlyCommitPerRun = tc.val
			err := c.Validate()
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestLadderConfigDB_Validate_EnumsAndProvider covers the delegated enum checks
// (mode/cadence via pkg/ladder Parse*) and the provider allow-list.
func TestLadderConfigDB_Validate_EnumsAndProvider(t *testing.T) {
	cases := []struct {
		mutate  func(c *LadderConfigDB)
		name    string
		wantErr bool
	}{
		{name: "unknown provider", mutate: func(c *LadderConfigDB) { c.Provider = "digitalocean" }, wantErr: true},
		{name: "empty provider", mutate: func(c *LadderConfigDB) { c.Provider = "" }, wantErr: true},
		{name: "gcp provider", mutate: func(c *LadderConfigDB) { c.Provider = "gcp" }, wantErr: false},
		{name: "unknown mode", mutate: func(c *LadderConfigDB) { c.Mode = "yolo_approve" }, wantErr: true},
		{name: "auto_approve mode", mutate: func(c *LadderConfigDB) { c.Mode = "auto_approve" }, wantErr: false},
		{name: "unknown cadence", mutate: func(c *LadderConfigDB) { c.Cadence = "hourly" }, wantErr: true},
		{name: "weekly cadence", mutate: func(c *LadderConfigDB) { c.Cadence = "weekly" }, wantErr: false},
		{name: "empty ramp_schedule", mutate: func(c *LadderConfigDB) { c.RampSchedule = nil }, wantErr: true},
		{name: "malformed ramp_schedule JSON", mutate: func(c *LadderConfigDB) { c.RampSchedule = json.RawMessage(`not-json`) }, wantErr: true},
		{name: "ramp fractions not summing to 1", mutate: func(c *LadderConfigDB) {
			c.RampSchedule = json.RawMessage(`{"steps":[{"after_days":0,"fraction":0.5}]}`)
		}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := validLadderConfigDB()
			tc.mutate(&c)
			err := c.Validate()
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
