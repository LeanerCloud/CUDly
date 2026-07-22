package tools

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type schemaTestArgs struct {
	Region        string `json:"region" jsonschema:"AWS region"`
	TermYears     int    `json:"term_years" jsonschema:"commitment term in years"`
	PaymentOption string `json:"payment_option" jsonschema:"payment schedule"`
	DryRun        *bool  `json:"dry_run,omitempty" jsonschema:"preview only, no purchase"`
}

func TestBuildInputSchemaAppliesEnumAndDefault(t *testing.T) {
	t.Parallel()
	trueDefault := true
	schema, err := BuildInputSchema[schemaTestArgs](map[string]FieldOverride{
		"term_years":     {Enum: []any{1, 3}},
		"payment_option": {Enum: []any{"all-upfront", "partial-upfront", "no-upfront"}},
		"dry_run":        {Default: trueDefault},
	})
	require.NoError(t, err)

	require.Contains(t, schema.Properties, "term_years")
	assert.Equal(t, []any{1, 3}, schema.Properties["term_years"].Enum)

	require.Contains(t, schema.Properties, "payment_option")
	assert.Equal(t, []any{"all-upfront", "partial-upfront", "no-upfront"}, schema.Properties["payment_option"].Enum)

	require.Contains(t, schema.Properties, "dry_run")
	var gotDefault bool
	require.NoError(t, json.Unmarshal(schema.Properties["dry_run"].Default, &gotDefault))
	assert.True(t, gotDefault)

	// region carries no override and must stay unconstrained.
	require.Contains(t, schema.Properties, "region")
	assert.Empty(t, schema.Properties["region"].Enum)
}

func TestBuildInputSchemaUnknownFieldErrors(t *testing.T) {
	t.Parallel()
	_, err := BuildInputSchema[schemaTestArgs](map[string]FieldOverride{
		"instance_type": {Enum: []any{"m5.large"}}, // not a field on schemaTestArgs
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "instance_type")
}

func TestBuildInputSchemaNilOverridesIsNoOp(t *testing.T) {
	t.Parallel()
	schema, err := BuildInputSchema[schemaTestArgs](nil)
	require.NoError(t, err)
	require.Contains(t, schema.Properties, "region")
}
