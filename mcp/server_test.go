package mcp

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LeanerCloud/CUDly/mcp/tools"
)

func TestNewServerBuildsWithoutError(t *testing.T) {
	t.Parallel()
	s, err := NewServer("test")
	require.NoError(t, err)
	require.NotNil(t, s)
}

// TestRegistryNonEmpty proves the tool registry is never accidentally empty:
// cudly_list_commitment_actions is always present, even before any
// purchase/search tool has been registered.
func TestRegistryNonEmpty(t *testing.T) {
	t.Parallel()
	regs := registrations()
	descriptors := make([]tools.Descriptor, 0, len(regs)+1)
	for _, r := range regs {
		descriptors = append(descriptors, r.Descriptor())
	}
	listTool := tools.NewListCommitmentActions(descriptors)
	descriptors = append(descriptors, listTool.Descriptor())

	require.NotEmpty(t, descriptors)
	names := make(map[string]bool, len(descriptors))
	for _, d := range descriptors {
		names[d.Name] = true
	}
	assert.True(t, names["cudly_list_commitment_actions"])
}

// TestRealPurchaseToolsDocumentMoneyImpactAndDryRun proves every tool that
// can execute a real purchase leads its description with a money-impact
// statement and a dry-run recommendation (design doc §3/§5) -- so any future
// purchase tool that forgets one fails this test rather than shipping a
// tool description that quietly omits the safety framing every other
// purchase tool carries.
func TestRealPurchaseToolsDocumentMoneyImpactAndDryRun(t *testing.T) {
	t.Parallel()
	for _, r := range registrations() {
		d := r.Descriptor()
		if !d.RealPurchaseEnabled {
			continue
		}
		lower := strings.ToLower(d.Description)
		assert.Truef(t, strings.Contains(lower, "real money") || strings.Contains(lower, "spends money"),
			"tool %q description must state its money impact: %q", d.Name, d.Description)
		assert.Truef(t, strings.Contains(lower, "dry_run") || strings.Contains(lower, "dry run"),
			"tool %q description must recommend dry_run first: %q", d.Name, d.Description)
	}
}
