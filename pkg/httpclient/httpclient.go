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

// imdsAddresses are the well-known metadata service addresses that must
// never be reachable from application-level HTTP clients. Compared by
// net.IP.Equal so alternative textual encodings of the same address
// (e.g. the expanded IPv6 form "fd00:ec2:0:0:0:0:0:254") are blocked
// equally: a literal-string match against the canonical short form
// would let the expanded form through.
var imdsAddresses = []net.IP{
	net.ParseIP("169.254.169.254"), // AWS/Azure/GCP link-local IMDS (IPv4)
	net.ParseIP("fd00:ec2::254"),   // AWS IMDS (IPv6)
}

// isIMDS reports whether host (a literal IP, as parsed off the dial
// address) is one of the blocked metadata endpoints. Returns false for
// hostnames — those are out of scope for this block-list because dial-
// time DNS resolution happens inside the inner dialer.
func isIMDS(host string) bool {
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	for _, blocked := range imdsAddresses {
		if ip.Equal(blocked) {
			return true
		}
	}
	return false
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
	if isIMDS(host) {
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
