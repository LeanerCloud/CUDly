package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeEngineName_CaseInsensitiveRecognizedAliases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		engine string
		want   string
	}{
		{name: "uppercase aurora postgresql", engine: "AURORA POSTGRESQL", want: "aurora-postgresql"},
		{name: "title case aurora postgresql", engine: "Aurora PostgreSQL", want: "aurora-postgresql"},
		{name: "uppercase postgres alias", engine: "POSTGRES", want: "postgresql"},
		{name: "title case postgres alias", engine: "Postgres", want: "postgresql"},
		{name: "uppercase sql server", engine: "SQL SERVER", want: "sqlserver"},
		{name: "uppercase oracle ee", engine: "ORACLE-EE", want: "oracle"},
		{name: "uppercase sqlserver web", engine: "SQLSERVER-WEB", want: "sqlserver"},
		{name: "unknown engine preserves fallback shape", engine: " Custom Engine ", want: " custom engine "},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, NormalizeEngineName(tt.engine))
		})
	}
}

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
