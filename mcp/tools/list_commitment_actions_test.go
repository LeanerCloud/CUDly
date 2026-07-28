package tools

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListCommitmentActionsReturnsCatalog(t *testing.T) {
	t.Parallel()
	descriptors := []Descriptor{
		{
			Name:                "cudly_aws_ec2_ri_purchase",
			Provider:            "aws",
			Product:             "ec2",
			Action:              "ri_purchase",
			Description:         "spends real money. dry_run recommended first.",
			RealPurchaseEnabled: true,
			ExamplePrompts:      []string{"buy 3 m5.large RIs in us-east-1"},
		},
		{
			Name:        "cudly_search_recommendations",
			Description: "read-only search, never spends money.",
		},
	}

	tool := NewListCommitmentActions(descriptors)
	impl, ok := tool.(*listCommitmentActionsTool)
	require.True(t, ok)

	_, result, err := impl.handle(context.Background(), nil, listCommitmentActionsArgs{})
	require.NoError(t, err)
	require.Len(t, result.Actions, 2)
	assert.Equal(t, "cudly_aws_ec2_ri_purchase", result.Actions[0].Name)
	assert.True(t, result.Actions[0].RealPurchaseEnabled)
	assert.False(t, result.Actions[1].RealPurchaseEnabled)
}

func TestListCommitmentActionsDescriptorItself(t *testing.T) {
	t.Parallel()
	tool := NewListCommitmentActions(nil)
	d := tool.Descriptor()
	assert.Equal(t, "cudly_list_commitment_actions", d.Name)
	assert.NotEmpty(t, d.Description)
	assert.NotEmpty(t, d.ExamplePrompts)
}
