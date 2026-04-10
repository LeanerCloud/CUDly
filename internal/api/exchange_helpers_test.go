package api

// exchange_helpers_test.go — tests for checkDailyCap and federation IaC helpers.

import (
	"context"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// checkDailyCap
// ---------------------------------------------------------------------------

func TestCheckDailyCap_WithinCap(t *testing.T) {
	// $100 daily spend + $50 payment = $150, cap is $200 → allowed
	reason := checkDailyCap("100.00", "50.00", 200.0)
	assert.Equal(t, "", reason, "expected no reason when within cap")
}

func TestCheckDailyCap_ExceedsCap(t *testing.T) {
	// $150 spent + $100 payment = $250 > $200 cap → blocked
	reason := checkDailyCap("150.00", "100.00", 200.0)
	assert.NotEmpty(t, reason)
	assert.Contains(t, reason, "daily cap exceeded")
}

func TestCheckDailyCap_InvalidDailySpend(t *testing.T) {
	// Unparseable daily spend → fail-safe block
	reason := checkDailyCap("not-a-number", "50.00", 200.0)
	assert.NotEmpty(t, reason)
	assert.Contains(t, reason, "daily spend check failed")
}

func TestCheckDailyCap_InvalidPaymentDue(t *testing.T) {
	// Unparseable payment due → treated as $0, within cap
	reason := checkDailyCap("100.00", "not-a-number", 500.0)
	// $100 + $0 = $100 < $500 → allowed
	assert.Equal(t, "", reason)
}

func TestCheckDailyCap_ExactlyAtCap(t *testing.T) {
	// $100 spent + $100 payment = $200 == $200 cap → allowed (not strictly greater)
	reason := checkDailyCap("100.00", "100.00", 200.0)
	assert.Equal(t, "", reason)
}

func TestCheckDailyCap_ZeroSpend(t *testing.T) {
	reason := checkDailyCap("0.00", "50.00", 200.0)
	assert.Equal(t, "", reason)
}

// ---------------------------------------------------------------------------
// getFederationIaC — accessible validation paths (no DB required)
// ---------------------------------------------------------------------------

func TestHandler_getFederationIaC_MissingTarget(t *testing.T) {
	ctx := context.Background()
	h := &Handler{}
	req := &events.LambdaFunctionURLRequest{
		QueryStringParameters: map[string]string{
			"source":     "aws",
			"account_id": "11111111-1111-1111-1111-111111111111",
		},
	}
	_, err := h.getFederationIaC(ctx, req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "target")
}

func TestHandler_getFederationIaC_MissingAccountID(t *testing.T) {
	ctx := context.Background()
	h := &Handler{}
	req := &events.LambdaFunctionURLRequest{
		QueryStringParameters: map[string]string{
			"target": "aws",
			"source": "gcp",
		},
	}
	_, err := h.getFederationIaC(ctx, req)
	assert.Error(t, err)
}

func TestHandler_getFederationIaC_AccountNotFound(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)
	mockStore.GetCloudAccountFn = func(_ context.Context, _ string) (*config.CloudAccount, error) {
		return nil, nil
	}

	h := &Handler{config: mockStore}
	req := &events.LambdaFunctionURLRequest{
		QueryStringParameters: map[string]string{
			"target":     "aws",
			"source":     "gcp",
			"account_id": "11111111-1111-1111-1111-111111111111",
		},
	}
	_, err := h.getFederationIaC(ctx, req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "account not found")
}

func TestRouter_getFederationIaCHandler(t *testing.T) {
	ctx := context.Background()
	h := &Handler{}
	r := newTestRouter(h)

	req := &events.LambdaFunctionURLRequest{
		QueryStringParameters: map[string]string{
			"source": "aws",
		},
	}
	// Missing target → error (but the handler is exercised)
	_, err := r.getFederationIaCHandler(ctx, req, nil)
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// slugify
// ---------------------------------------------------------------------------

func TestSlugify(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"My Account Name", "my-account-name"},
		{"account_123", "account-123"},
		{"  spaces  ", "spaces"},
		{"", ""},
		{"UPPER-CASE", "upper-case"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, slugify(tt.input))
		})
	}
}

// ---------------------------------------------------------------------------
// awsOIDCIssuer and gcpOIDCIssuerURI helpers
// ---------------------------------------------------------------------------

func TestAwsOIDCIssuer(t *testing.T) {
	assert.Contains(t, awsOIDCIssuer("azure", "tenant-id"), "login.microsoftonline.com/tenant-id")
	assert.Contains(t, awsOIDCIssuer("azure", ""), "AZURE_TENANT_ID")
	assert.Equal(t, "https://accounts.google.com", awsOIDCIssuer("gcp", ""))
	assert.Equal(t, "", awsOIDCIssuer("unknown", ""))
}

func TestGcpOIDCIssuerURI(t *testing.T) {
	assert.Contains(t, gcpOIDCIssuerURI("azure", "my-tenant"), "my-tenant")
	assert.Contains(t, gcpOIDCIssuerURI("azure", ""), "AZURE_TENANT_ID")
	assert.Equal(t, "", gcpOIDCIssuerURI("gcp", ""))
}
