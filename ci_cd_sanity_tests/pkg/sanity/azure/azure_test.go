package azure

import (
	"encoding/json"
	"testing"

	"github.com/LeanerCloud/CUDly/ci_cd_sanity_tests/pkg/sanity/report"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEncodeAccountJSON verifies that encodeAccountJSON produces bytes that are
// accepted by validateAccountExpectations without error, and that the ID /
// TenantID fields survive the round-trip.  This guards the SDK->JSON->validate
// path introduced when replacing the "az account show" CLI call.
func TestEncodeAccountJSON(t *testing.T) {
	info := azureSubscriptionInfo{
		ID:       "aaaabbbb-1111-2222-3333-ccccddddeeee",
		TenantID: "ffffgggg-5555-6666-7777-hhhh88889999",
		Name:     "My Test Sub",
		State:    "Enabled",
	}

	encoded := encodeAccountJSON(info)
	require.NotEmpty(t, encoded, "encoded JSON must not be empty")

	// Must parse back as azAccountShow without error.
	var parsed azAccountShow
	require.NoError(t, json.Unmarshal(encoded, &parsed))
	assert.Equal(t, info.ID, parsed.ID)
	assert.Equal(t, info.TenantID, parsed.TenantID)
	assert.Equal(t, info.Name, parsed.Name)
	assert.Equal(t, info.State, parsed.State)

	// Round-trip through validateAccountExpectations with matching expectations.
	opts := Options{
		ExpectedSubID:    info.ID,
		ExpectedTenantID: info.TenantID,
	}
	result := validateAccountExpectations(opts, encoded)
	assert.Equal(t, report.StatusPass, result.Status, "expected PASS for matching IDs, got: %s", result.Message)
}

// TestTruncate verifies truncate boundary conditions.
func TestTruncate(t *testing.T) {
	assert.Equal(t, "ab", truncate("ab", 5))
	assert.Equal(t, "abcde", truncate("abcde", 5))
	assert.Equal(t, "abcde...(truncated)", truncate("abcdef", 5))
}

func accountJSON(id, tenantID, name, state string) []byte {
	b, err := json.Marshal(azAccountShow{
		ID:       id,
		TenantID: tenantID,
		Name:     name,
		State:    state,
	})
	if err != nil {
		panic(err)
	}
	return b
}

func TestValidateAccountExpectations(t *testing.T) {
	tests := []struct {
		name        string
		opts        Options
		accountOut  []byte
		wantStatus  report.Status
		wantMsgPart string // substring expected in Message when non-empty
	}{
		{
			name: "valid json, no expectations",
			opts: Options{},
			accountOut: accountJSON(
				"sub-123", "tenant-456", "My Sub", "Enabled",
			),
			wantStatus: report.StatusPass,
		},
		{
			name: "matching subscription and tenant",
			opts: Options{
				ExpectedSubID:    "sub-123",
				ExpectedTenantID: "tenant-456",
			},
			accountOut: accountJSON(
				"sub-123", "tenant-456", "My Sub", "Enabled",
			),
			wantStatus: report.StatusPass,
		},
		{
			name: "mismatched subscription",
			opts: Options{
				ExpectedSubID: "sub-expected",
			},
			accountOut: accountJSON(
				"sub-actual", "tenant-456", "My Sub", "Enabled",
			),
			wantStatus:  report.StatusFail,
			wantMsgPart: "unexpected subscription",
		},
		{
			name: "mismatched tenant",
			opts: Options{
				ExpectedTenantID: "tenant-expected",
			},
			accountOut: accountJSON(
				"sub-123", "tenant-actual", "My Sub", "Enabled",
			),
			wantStatus:  report.StatusFail,
			wantMsgPart: "unexpected tenant",
		},
		{
			name:        "invalid json",
			opts:        Options{},
			accountOut:  []byte(`not valid json`),
			wantStatus:  report.StatusFail,
			wantMsgPart: "failed to parse az account show JSON",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := validateAccountExpectations(tt.opts, tt.accountOut)
			assert.Equal(t, tt.wantStatus, result.Status)
			if tt.wantMsgPart != "" {
				require.Contains(t, result.Message, tt.wantMsgPart)
			}
			assert.False(t, result.StartedAt.IsZero(), "StartedAt should be set")
			assert.False(t, result.EndedAt.IsZero(), "EndedAt should be set")
		})
	}
}
