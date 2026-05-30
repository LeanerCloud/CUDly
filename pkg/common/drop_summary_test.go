package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDropSummary_Add_And_Total(t *testing.T) {
	d := NewDropSummary()
	assert.Equal(t, 0, d.Total())
	assert.True(t, d.IsEmpty())

	d.Add(DropMinPoolSize, 8)
	d.Add(DropTargetAlreadyMet, 4)
	d.Add(DropDuplicateDedup, 2)

	assert.Equal(t, 14, d.Total())
	assert.False(t, d.IsEmpty())
}

func TestDropSummary_Add_Zero_NoOp(t *testing.T) {
	d := NewDropSummary()
	d.Add(DropMinPoolSize, 0)
	assert.True(t, d.IsEmpty())
}

func TestDropSummary_NilReceiver_Safe(t *testing.T) {
	var d *DropSummary
	d.Add(DropMinPoolSize, 3) // must not panic
	assert.Equal(t, 0, d.Total())
	assert.True(t, d.IsEmpty())
	assert.Equal(t, "", d.FormatOneLine())
}

// Regression: a zero-value DropSummary (declared without NewDropSummary)
// must not panic on Add because the internal map is lazily initialized.
func TestDropSummary_ZeroValue_Safe(t *testing.T) {
	var d DropSummary
	d.Add(DropMinPoolSize, 3) // must not panic on nil map
	d.Add(DropTargetAlreadyMet, 2)
	assert.Equal(t, 5, d.Total())
	assert.False(t, d.IsEmpty())
	assert.Equal(t, "Dropped 5 recs: --min-pool-size=3, target-already-met=2", d.FormatOneLine())
}

func TestDropSummary_FormatOneLine(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(*DropSummary)
		expected string
	}{
		{
			name:     "empty returns blank string",
			setup:    func(_ *DropSummary) {},
			expected: "",
		},
		{
			name: "single reason",
			setup: func(d *DropSummary) {
				d.Add(DropMinPoolSize, 3)
			},
			expected: "Dropped 3 recs: --min-pool-size=3",
		},
		{
			name: "multiple reasons sorted deterministically",
			setup: func(d *DropSummary) {
				d.Add(DropMinPoolSize, 8)
				d.Add(DropTargetAlreadyMet, 4)
				d.Add(DropDuplicateDedup, 2)
			},
			// Alphabetically: --min-pool-size, duplicate-dedup, target-already-met
			expected: "Dropped 14 recs: --min-pool-size=8, duplicate-dedup=2, target-already-met=4",
		},
		{
			name: "all drop categories present",
			setup: func(d *DropSummary) {
				d.Add(DropMinPoolSize, 1)
				d.Add(DropExtendedSupport, 2)
				d.Add(DropTargetAlreadyMet, 3)
				d.Add(DropTargetSizedToZero, 4)
				d.Add(DropFamilyAlreadyAtTarget, 5)
				d.Add(DropFamilySizedToZero, 6)
				d.Add(DropDuplicateDedup, 7)
			},
			expected: "Dropped 28 recs: --include-extended-support=2, --min-pool-size=1, " +
				"duplicate-dedup=7, family-nu-already-at-target=5, family-nu-sized-to-zero=6, " +
				"target-already-met=3, target-sized-to-zero=4",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := NewDropSummary()
			tt.setup(d)
			assert.Equal(t, tt.expected, d.FormatOneLine())
		})
	}
}
