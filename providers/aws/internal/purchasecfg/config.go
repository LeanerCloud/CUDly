// Package purchasecfg provides a tightened AWS SDK config for purchase-path
// clients.
//
// Background: the AWS SDK v2 default retry policy (3 attempts, up to 30s per
// request) can consume up to ~90s wall-clock time when an API is slow. That
// budget exceeds the Lambda function timeout (previously 60s, now bumped to
// 300s). Purchase-path calls do not benefit from many retries because the
// errors that triggered this fix (context deadline exceeded) are caused by the
// Lambda budget itself running out -- retrying inside the same Lambda makes
// the situation worse, not better.
//
// Recommendation-collection clients deliberately keep the SDK default config;
// they call Cost Explorer and can tolerate more retries legitimately.
package purchasecfg

import (
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
)

const (
	// MaxAttempts is the maximum number of SDK-level retries for purchase-path
	// API calls. 2 total attempts (1 initial + 1 retry) means a worst-case
	// wall-clock of 2 * HTTPTimeout = 30s, well within the 300s Lambda budget.
	MaxAttempts = 2

	// HTTPTimeout is the per-request HTTP timeout for purchase-path API calls.
	// A single transient slow AWS API response will time out at 15s, allowing
	// the one retry to complete within 30s total.
	HTTPTimeout = 15 * time.Second
)

// NewConfig returns a copy of base with purchase-path-appropriate retry and
// HTTP timeout settings applied. The original base config is not modified.
//
// Applies:
//   - RetryMaxAttempts = 2 (overrides the SDK default of 3)
//   - HTTPClient with Timeout = 15s (overrides the SDK default of no timeout)
//
// If base.HTTPClient is an *http.Client, it is cloned and its Timeout is set
// to HTTPTimeout, preserving any custom Transport, Jar, or CheckRedirect the
// caller installed. If base.HTTPClient is nil or a non-*http.Client
// implementation, a fresh *http.Client{Timeout: HTTPTimeout} is used instead.
func NewConfig(base *aws.Config) aws.Config {
	cfg := base.Copy()
	cfg.RetryMaxAttempts = MaxAttempts
	if hc, ok := base.HTTPClient.(*http.Client); ok && hc != nil {
		clone := *hc // shallow copy preserves Transport, Jar, CheckRedirect
		clone.Timeout = HTTPTimeout
		cfg.HTTPClient = &clone
	} else {
		cfg.HTTPClient = &http.Client{Timeout: HTTPTimeout}
	}
	return cfg
}
