package reservations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSanitizeDisplayName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "already conformant",
			input: "VM_Reservation_Standard_D2a_v4",
			want:  "VM_Reservation_Standard_D2a_v4",
		},
		{
			name:  "spaces replaced by underscore",
			input: "VM Reservation Standard D2s v3",
			want:  "VM_Reservation_Standard_D2s_v3",
		},
		{
			name:  "special chars replaced",
			input: "Redis@Cache#Reservation!foo",
			want:  "Redis_Cache_Reservation_foo",
		},
		{
			name:  "runs of non-conforming chars collapsed to single underscore",
			input: "foo  bar!!baz",
			want:  "foo_bar_baz",
		},
		{
			name:  "empty input",
			input: "",
			want:  "",
		},
		{
			name:  "exact 64 chars unchanged",
			input: strings.Repeat("a", 64),
			want:  strings.Repeat("a", 64),
		},
		{
			name:  "65 chars truncated to 64",
			input: strings.Repeat("b", 65),
			want:  strings.Repeat("b", 64),
		},
		{
			name:  "100 chars truncated to 64",
			input: strings.Repeat("c", 100),
			want:  strings.Repeat("c", 64),
		},
		{
			name:  "hyphens preserved",
			input: "Standard-D2a-v4",
			want:  "Standard-D2a-v4",
		},
		{
			name:  "mixed case preserved",
			input: "Redis_Cache_Reservation_Standard_D2s_v3",
			want:  "Redis_Cache_Reservation_Standard_D2s_v3",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SanitizeDisplayName(tc.input)
			assert.Equal(t, tc.want, got)
			// Output must always match the Azure allowlist.
			if got != "" {
				assert.Regexp(t, `^[A-Za-z0-9_-]{1,64}$`, got)
			}
		})
	}
}
