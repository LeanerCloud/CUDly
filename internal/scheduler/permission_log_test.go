package scheduler

import (
	"errors"
	"fmt"
	"testing"

	"google.golang.org/api/googleapi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestIsAccountPermissionError verifies that the scheduler routes
// per-account collection errors to the correct log severity. Issue #57:
// GCP 403 / PermissionDenied must downgrade to WARN so a single
// misconfigured account doesn't drown out other log signals; non-GCP
// providers and non-permission errors must keep the existing ERROR
// behaviour until analogous predicates are added.
func TestIsAccountPermissionError(t *testing.T) {
	tests := []struct {
		name          string
		providerLabel string
		err           error
		want          bool
	}{
		{
			name:          "GCP googleapi 403 → WARN",
			providerLabel: "GCP",
			err:           &googleapi.Error{Code: 403, Message: "Required 'compute.regions.list' permission"},
			want:          true,
		},
		{
			name:          "GCP wrapped 403 → WARN",
			providerLabel: "GCP",
			err: fmt.Errorf("get recommendations: failed to get regions: %w",
				&googleapi.Error{Code: 403, Message: "missing role"}),
			want: true,
		},
		{
			name:          "GCP grpc PermissionDenied → WARN",
			providerLabel: "GCP",
			err:           status.Error(codes.PermissionDenied, "missing role"),
			want:          true,
		},
		{
			name:          "GCP googleapi 500 → ERROR (genuine failure)",
			providerLabel: "GCP",
			err:           &googleapi.Error{Code: 500, Message: "internal"},
			want:          false,
		},
		{
			name:          "GCP generic network error → ERROR",
			providerLabel: "GCP",
			err:           errors.New("connection refused"),
			want:          false,
		},
		{
			name:          "AWS 403-like error → ERROR (predicate not yet wired for AWS)",
			providerLabel: "AWS",
			err:           &googleapi.Error{Code: 403, Message: "ignored for AWS"},
			want:          false,
		},
		{
			name:          "Azure 403-like error → ERROR (predicate not yet wired for Azure)",
			providerLabel: "Azure",
			err:           status.Error(codes.PermissionDenied, "ignored for Azure"),
			want:          false,
		},
		{
			name:          "nil error → false",
			providerLabel: "GCP",
			err:           nil,
			want:          false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isAccountPermissionError(tt.providerLabel, tt.err)
			if got != tt.want {
				t.Errorf("isAccountPermissionError(%q, %v) = %v, want %v",
					tt.providerLabel, tt.err, got, tt.want)
			}
		})
	}
}
