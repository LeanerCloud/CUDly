package common

import "strings"

// NormalizeDeploymentName canonicalises an RDS deployment-option string
// (e.g. Cost Explorer's "Multi-AZ" or the parser's "multi-az") to a single
// lowercase, no-separator form. Mirrors
// providers/aws/recommendations/coverage.go's normaliseDeployment, which
// lives in the root module and can't be imported here (pkg is a separate
// Go module); keep the two in sync by hand if the vocabulary changes.
//
// An empty input stays "" rather than becoming a synthetic bucket, so
// non-RDS commitments/recommendations (which never populate a deployment)
// collapse onto the same key instead of colliding with a real
// single-az/multi-az bucket.
func NormalizeDeploymentName(deployment string) string {
	s := strings.ToLower(deployment)
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, "_", "")
	s = strings.ReplaceAll(s, "(", "")
	s = strings.ReplaceAll(s, ")", "")
	return s
}

// DeploymentFromDetails extracts the RDS deployment option (AZConfig) from
// recommendation details. Returns "" for non-database service types.
//
// The `d == nil` check after the type assertion is not redundant with the
// `details == nil` check above it: the first only catches an untyped nil, so
// an interface holding a typed nil pointer reaches the assertion, satisfies
// it, and would panic on the field read. Same guard as EngineFromDetails.
func DeploymentFromDetails(details ServiceDetails) string {
	if details == nil {
		return ""
	}
	d, ok := details.(*DatabaseDetails)
	if !ok || d == nil {
		return ""
	}
	return d.AZConfig
}
