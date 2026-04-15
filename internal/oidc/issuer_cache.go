package oidc

import "sync"

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
	mu  sync.RWMutex
	url string
}

var globalIssuer issuerCache

// SetIssuerURL stores the deployment's OIDC issuer URL. No-op when
// called with an empty string, so callers can unconditionally forward
// whatever the env var resolved to without checking first.
func SetIssuerURL(url string) {
	if url == "" {
		return
	}
	globalIssuer.mu.Lock()
	defer globalIssuer.mu.Unlock()
	globalIssuer.url = url
}

// IssuerURL returns whatever was last stored via SetIssuerURL, or the
// empty string if none was.
func IssuerURL() string {
	globalIssuer.mu.RLock()
	defer globalIssuer.mu.RUnlock()
	return globalIssuer.url
}
