// Package gcp provides helpers for classifying GCP API errors.
package gcp

import (
	"errors"

	"google.golang.org/api/googleapi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// IsPermissionError reports whether err represents a GCP permission /
// authorization failure — typically a missing IAM role on the deploy
// service account. The GCP collector uses this to downgrade per-account
// log severity from ERROR to WARN: the operator can fix the gap by
// granting the missing role, so it is not a genuine collector error and
// should not drown out other signals in the log stream.
//
// It returns true for:
//   - *googleapi.Error with Code == 403 (REST surface, e.g. compute REST
//     client returns "googleapi: Error 403: Required '...' permission")
//   - gRPC status with codes.PermissionDenied (gRPC surface, e.g. when
//     a client uses the gRPC transport rather than REST)
//
// Wrapped errors (via fmt.Errorf("...: %w", err)) are unwrapped so the
// scheduler's wrap-and-rethrow chain still classifies correctly.
//
// Returns false for nil and for any other error.
func IsPermissionError(err error) bool {
	if err == nil {
		return false
	}

	var gerr *googleapi.Error
	if errors.As(err, &gerr) && gerr.Code == 403 {
		return true
	}

	if s, ok := status.FromError(err); ok && s.Code() == codes.PermissionDenied {
		return true
	}

	return false
}
