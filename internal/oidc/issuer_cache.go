package oidc

import (
	"fmt"
	"net/url"
	"strings"
	"sync"
)

// globalIssuer is a process-wide cache of the OIDC issuer URL this
// deployment publishes. It exists because the AWS Lambda Function URL
// attribute can't be wired into the Lambda's own env vars (Terraform
// would report a cycle), so the issuer URL has to be discovered from
// the first inbound request's DomainName and shared between the HTTP
// handlers (which publish the Discovery document) and the credential
// resolver (which uses the same string as the iss claim when minting
// JWTs for Azure AD).
//
// Set via SetIssuerURL, read via IssuerURL. Safe for concurrent use.
type issuerCache struct {
	url string
	mu  sync.RWMutex
}

var globalIssuer issuerCache

// SetIssuerURL stores the deployment's OIDC issuer URL. The URL must be an
// absolute https:// URL; non-https or relative URLs are rejected and the
// cache is left unchanged (03-L4). No-op when called with an empty string.
func SetIssuerURL(rawURL string) error {
	if rawURL == "" {
		return nil
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("oidc: invalid issuer URL %q: %w", rawURL, err)
	}
	if !parsed.IsAbs() || !strings.EqualFold(parsed.Scheme, "https") {
		return fmt.Errorf("oidc: issuer URL must be an absolute https:// URL, got %q", rawURL)
	}
	globalIssuer.mu.Lock()
	defer globalIssuer.mu.Unlock()
	globalIssuer.url = rawURL
	return nil
}

// IssuerURL returns whatever was last stored via SetIssuerURL, or the
// empty string if none was.
func IssuerURL() string {
	globalIssuer.mu.RLock()
	defer globalIssuer.mu.RUnlock()
	return globalIssuer.url
}
