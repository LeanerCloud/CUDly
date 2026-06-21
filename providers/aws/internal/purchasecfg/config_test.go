package purchasecfg

import (
	"net/http"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewConfig_SetsRetryMaxAttempts(t *testing.T) {
	t.Helper()
	base := aws.Config{
		Region:           "us-east-1",
		RetryMaxAttempts: 0, // SDK default (will be resolved to 3 at runtime)
	}

	cfg := NewConfig(&base)

	assert.Equal(t, MaxAttempts, cfg.RetryMaxAttempts,
		"purchase-path config must cap retries at %d to prevent Lambda budget exhaustion", MaxAttempts)
}

func TestNewConfig_SetsHTTPTimeout(t *testing.T) {
	t.Helper()
	base := aws.Config{Region: "us-east-1"}

	cfg := NewConfig(&base)

	require.NotNil(t, cfg.HTTPClient, "HTTPClient must not be nil after NewConfig")
	httpClient, ok := cfg.HTTPClient.(*http.Client)
	require.True(t, ok, "HTTPClient must be *http.Client")
	assert.Equal(t, HTTPTimeout, httpClient.Timeout,
		"per-request HTTP timeout must be %s to bound individual AWS API calls", HTTPTimeout)
}

func TestNewConfig_DoesNotMutateBase(t *testing.T) {
	t.Helper()
	base := aws.Config{
		Region:           "eu-west-1",
		RetryMaxAttempts: 5,
		HTTPClient:       &http.Client{Timeout: 60 * time.Second},
	}

	_ = NewConfig(&base)

	assert.Equal(t, 5, base.RetryMaxAttempts, "NewConfig must not modify the original config's RetryMaxAttempts")
	httpClient, ok := base.HTTPClient.(*http.Client)
	require.True(t, ok)
	assert.Equal(t, 60*time.Second, httpClient.Timeout, "NewConfig must not modify the original config's HTTPClient")
}

// TestNewConfig_PreservesCustomTransport verifies that a non-nil *http.Client
// supplied in the base config has its Transport kept intact in the result.
// NewConfig must not silently drop custom transports, TLS settings, or
// instrumentation hooks by replacing the whole client unconditionally.
func TestNewConfig_PreservesCustomTransport(t *testing.T) {
	t.Helper()
	customTransport := &http.Transport{MaxIdleConns: 42}
	base := aws.Config{
		Region:     "us-east-1",
		HTTPClient: &http.Client{Transport: customTransport, Timeout: 60 * time.Second},
	}

	cfg := NewConfig(&base)

	require.NotNil(t, cfg.HTTPClient, "HTTPClient must not be nil after NewConfig")
	httpClient, ok := cfg.HTTPClient.(*http.Client)
	require.True(t, ok, "HTTPClient must be *http.Client")
	assert.Equal(t, HTTPTimeout, httpClient.Timeout,
		"NewConfig must override the Timeout to HTTPTimeout even when cloning a caller-provided client")
	assert.Same(t, customTransport, httpClient.Transport,
		"NewConfig must preserve the caller-provided Transport on the cloned client")
}

func TestNewConfig_PreservesRegion(t *testing.T) {
	t.Helper()
	base := aws.Config{Region: "ap-southeast-1"}

	cfg := NewConfig(&base)

	assert.Equal(t, "ap-southeast-1", cfg.Region, "NewConfig must preserve the original region")
}

// TestNewConfig_WallClockBound documents the design invariant: with MaxAttempts=2
// and HTTPTimeout=15s, the worst-case total for purchase-path API calls is
// 2*15s=30s, well under the 300s Lambda function timeout.
func TestNewConfig_WallClockBound(t *testing.T) {
	t.Helper()
	worstCaseWallClock := time.Duration(MaxAttempts) * HTTPTimeout
	lambdaTimeout := 300 * time.Second

	assert.Less(t, worstCaseWallClock, lambdaTimeout,
		"worst-case purchase SDK wall clock (%s) must be less than Lambda timeout (%s)",
		worstCaseWallClock, lambdaTimeout)
}
