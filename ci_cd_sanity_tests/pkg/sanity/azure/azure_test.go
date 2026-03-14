package azure

import (
	"encoding/json"
	"testing"

	"github.com/LeanerCloud/CUDly/ci_cd_sanity_tests/pkg/sanity/report"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
