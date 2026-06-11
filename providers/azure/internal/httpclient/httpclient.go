// Package httpclient provides a hardened HTTP client for Azure provider use.
// It blocks requests to the Instance Metadata Service (IMDS) endpoints to
// prevent SSRF attacks that could leak cloud credentials.
//
// The implementation lives in the shared pkg module so the root module
// (which cannot import this internal package across the module boundary)
// uses the exact same hardening; this package only delegates.
package httpclient

import (
	"net/http"

	"github.com/LeanerCloud/CUDly/pkg/httpclient"
)

// New returns an *http.Client with a 30-second timeout and IMDS blocking.
func New() *http.Client {
	return httpclient.New()
}
