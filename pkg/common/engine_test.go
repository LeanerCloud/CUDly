package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestEngineFromDetails covers the pointer-only dispatch documented in
// service_details_codec.go's package doc, plus the typed-nil guard.
//
// The typed-nil cases are the regression bar: a (*DatabaseDetails)(nil)
// stored in the ServiceDetails interface is NOT caught by the
// `details == nil` check (the interface itself is non-nil once it carries a
// type), so it reaches the type switch and the field read panics without the
// per-case guard. Matches() calls EngineFromDetails on every
// recommendation/commitment pair, so a panic here takes down coverage
// matching for the whole run rather than skipping one row.
func TestEngineFromDetails(t *testing.T) {
	t.Parallel()

	tests := []struct {
		details ServiceDetails
		name    string
		want    string
	}{
		{name: "nil interface", details: nil, want: ""},
		{name: "*DatabaseDetails", details: &DatabaseDetails{Engine: "PostgreSQL"}, want: "postgresql"},
		{name: "*CacheDetails", details: &CacheDetails{Engine: "Redis"}, want: "redis"},
		{name: "*ComputeDetails is not a DB/cache type", details: &ComputeDetails{Platform: "Linux/UNIX"}, want: ""},
		{name: "typed nil *DatabaseDetails", details: (*DatabaseDetails)(nil), want: ""},
		{name: "typed nil *CacheDetails", details: (*CacheDetails)(nil), want: ""},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.NotPanics(t, func() {
				assert.Equal(t, tt.want, EngineFromDetails(tt.details))
			})
		})
	}
}
