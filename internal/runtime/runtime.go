// Package runtime holds small helpers that inspect the process's
// runtime environment. Kept deliberately minimal — this is a shared
// surface, not a grab-bag for utility code.
package runtime

import "os"

// IsLambda reports whether the current process is running inside an
// AWS Lambda execution environment. Detection is based on
// AWS_LAMBDA_RUNTIME_API, which the Lambda runtime always sets; it's
// absent on container images, local dev runs, and the long-running
// server deploys (Cloud Run / Container Apps).
//
// Callers that need to gate non-Lambda-only behaviour (e.g.
// background goroutines for stale-while-revalidate) should use this
// helper rather than reading the env var directly so the detection
// rule stays consistent across call sites.
func IsLambda() bool {
	return os.Getenv("AWS_LAMBDA_RUNTIME_API") != ""
}
