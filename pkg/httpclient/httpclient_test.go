package httpclient

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// New must hand back a dedicated hardened client, never the process-wide
// default client/transport (SEC-04 regression guard).
func TestNew_NotDefaultClient(t *testing.T) {
	c := New()

	if c == http.DefaultClient {
		t.Fatalf("New() must not return http.DefaultClient")
	}
	if c.Timeout == 0 {
		t.Fatalf("New() must set an overall request timeout")
	}
	if c.Transport == nil || c.Transport == http.DefaultTransport {
		t.Fatalf("New() must install a dedicated hardened transport")
	}
}

func TestNew_BlocksIMDS(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{name: "ipv4 link-local IMDS", url: "http://169.254.169.254/latest/meta-data/"},
		{name: "ipv6 AWS IMDS", url: "http://[fd00:ec2::254]/latest/meta-data/"},
	}

	c := New()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := c.Get(tt.url)
			if err == nil {
				resp.Body.Close()
				t.Fatalf("request to %s must be blocked", tt.url)
			}
			if !strings.Contains(err.Error(), "blocked") {
				t.Fatalf("expected IMDS-blocked error, got: %v", err)
			}
		})
	}
}

func TestNew_AllowsRegularEndpoints(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	resp, err := New().Get(srv.URL)
	if err != nil {
		t.Fatalf("request to non-IMDS endpoint failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}
