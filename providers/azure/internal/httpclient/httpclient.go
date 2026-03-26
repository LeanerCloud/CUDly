// Package httpclient provides a hardened HTTP client for Azure provider use.
// It blocks requests to the Instance Metadata Service (IMDS) endpoints to
// prevent SSRF attacks that could leak cloud credentials.
package httpclient

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"
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
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		},
	}
	transport := &http.Transport{
		DialContext:         dialer.DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	return &http.Client{
		Timeout:   30 * time.Second,
		Transport: transport,
	}
}
