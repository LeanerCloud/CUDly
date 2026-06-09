package gcp

import (
	"errors"
	"fmt"
	"testing"

	"google.golang.org/api/googleapi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestIsPermissionError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
		{
			name: "googleapi 403 direct",
			err:  &googleapi.Error{Code: 403, Message: "Required 'compute.regions.list' permission"},
			want: true,
		},
		{
			name: "googleapi 403 wrapped via fmt.Errorf",
			err: fmt.Errorf("failed to list regions: %w",
				&googleapi.Error{Code: 403, Message: "permission denied"}),
			want: true,
		},
		{
			name: "googleapi 404 not a permission error",
			err:  &googleapi.Error{Code: 404, Message: "not found"},
			want: false,
		},
		{
			name: "googleapi 500 not a permission error",
			err:  &googleapi.Error{Code: 500, Message: "internal"},
			want: false,
		},
		{
			name: "grpc PermissionDenied direct",
			err:  status.Error(codes.PermissionDenied, "missing role"),
			want: true,
		},
		{
			name: "grpc PermissionDenied wrapped via fmt.Errorf",
			err: fmt.Errorf("get recommendations: %w",
				status.Error(codes.PermissionDenied, "missing role")),
			want: true,
		},
		{
			name: "grpc NotFound not a permission error",
			err:  status.Error(codes.NotFound, "no such project"),
			want: false,
		},
		{
			name: "grpc Unavailable not a permission error",
			err:  status.Error(codes.Unavailable, "transient"),
			want: false,
		},
		{
			name: "generic error",
			err:  errors.New("network is unreachable"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsPermissionError(tt.err)
			if got != tt.want {
				t.Errorf("IsPermissionError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
