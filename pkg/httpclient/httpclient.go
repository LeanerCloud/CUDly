// Package httpclient provides a hardened HTTP client for outbound
// requests. It blocks connections to the cloud Instance Metadata
// Service (IMDS) endpoints to prevent SSRF attacks that could leak
// cloud credentials, and applies sane dial/TLS/overall timeouts so a
// misbehaving endpoint cannot hang a caller indefinitely.
//
// This is the single shared implementation; the Azure provider's
// internal httpclient package delegates here so every module uses the
// same hardening.
package httpclient

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"
)

// Timeouts applied by New. Exported indirectly via the constructed
// client; named here so the values are not magic numbers.
const (
	dialTimeout         = 10 * time.Second
	keepAliveInterval   = 30 * time.Second
	tlsHandshakeTimeout = 10 * time.Second
	requestTimeout      = 30 * time.Second
)

// imdsAddresses are the well-known metadata service addresses that must never
// be reachable from application-level HTTP clients.
var imdsAddresses = map[string]bool{
	"169.254.169.254": true, // AWS/Azure/GCP link-local IMDS (IPv4)
	"fd00:ec2::254":   true, // AWS IMDS (IPv6)
}

// blockIMDSDialer wraps net.Dialer and rejects connections to IMDS addresses.
type blockIMDSDialer struct {
	inner net.Dialer
}

func (d *blockIMDSDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if imdsAddresses[host] {
		return nil, fmt.Errorf("connection to metadata endpoint %s is blocked", host)
	}
	return d.inner.DialContext(ctx, network, addr)
}

// New returns an *http.Client with a 30-second timeout and IMDS blocking.
func New() *http.Client {
	dialer := &blockIMDSDialer{
		inner: net.Dialer{
			Timeout:   dialTimeout,
			KeepAlive: keepAliveInterval,
		},
	}
	transport := &http.Transport{
		DialContext:         dialer.DialContext,
		TLSHandshakeTimeout: tlsHandshakeTimeout,
	}
	return &http.Client{
		Timeout:   requestTimeout,
		Transport: transport,
	}
}
