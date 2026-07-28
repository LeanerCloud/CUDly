package tools

import (
	"encoding/json"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
)

// FieldOverride declares JSON Schema refinements -- an explicit enum
// membership and/or a documented default -- for one property of an
// otherwise auto-inferred schema. Centralizing this in one helper
// (BuildInputSchema) means every tool declares its enum/default once, next
// to its Go struct, instead of re-implementing schema post-processing per
// tool.
type FieldOverride struct {
	// Enum, when non-empty, restricts the property to these exact values.
	Enum []any
	// Default, when non-nil, is recorded on the schema as the property's
	// documented default so a caller inspecting the tool (or an MCP client
	// that surfaces schema defaults in its UI) can see it without reading
	// the tool description prose. It does NOT, by itself, cause the value to
	// be applied when the caller omits the field -- each tool's handler
	// applies its own default explicitly (see the dry_run/confirm pattern in
	// purchase.go) so "omitted" is never silently confused with "false".
	Default any
}

// BuildInputSchema infers the JSON Schema for T via jsonschema.For, then
// applies the given per-field overrides by JSON field name. It returns an
// error -- rather than silently skipping -- when an override names a field
// that does not exist on T, so a typo in the override map is caught at
// server-startup / test time instead of quietly shipping an unconstrained
// schema for a money-affecting field.
func BuildInputSchema[T any](overrides map[string]FieldOverride) (*jsonschema.Schema, error) {
	schema, err := jsonschema.For[T](nil)
	if err != nil {
		return nil, fmt.Errorf("infer schema for %T: %w", *new(T), err)
	}
	for field, ov := range overrides {
		prop, ok := schema.Properties[field]
		if !ok {
			return nil, fmt.Errorf("schema override for unknown field %q (does the json tag match?)", field)
		}
		if len(ov.Enum) > 0 {
			prop.Enum = ov.Enum
		}
		if ov.Default != nil {
			b, err := json.Marshal(ov.Default)
			if err != nil {
				return nil, fmt.Errorf("marshal default for field %q: %w", field, err)
			}
			prop.Default = b
		}
	}
	return schema, nil
}
