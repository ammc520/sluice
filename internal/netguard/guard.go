// Package netguard builds HTTP clients for upstream providers with egress
// protections. Cloud metadata and link-local destinations are always blocked.
// Cleartext HTTP is only allowed to private/loopback addresses, so admin-set
// provider credentials are never sent in the clear to the public internet.
package netguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

// ErrBlocked reports a destination rejected by the egress policy.
var ErrBlocked = errors.New("netguard: destination blocked")

type schemeKey struct{}

var blockedNets = mustNets(
	"169.254.0.0/16", // link-local, includes cloud metadata endpoints
	"fe80::/10",
	"ff00::/8", // multicast
	"0.0.0.0/8",
	"100.100.100.200/32", // Alibaba Cloud metadata
)

var privateNets = mustNets(
	"10.0.0.0/8",
	"172.16.0.0/12",
	"192.168.0.0/16",
	"127.0.0.0/8",
	"100.64.0.0/10", // carrier-grade NAT / some private cloud networks
	"fc00::/7",
	"::1/128",
)

func mustNets(cidrs ...string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, raw := range cidrs {
		_, network, err := net.ParseCIDR(raw)
		if err != nil {
			panic(err)
		}
		out = append(out, network)
	}
	return out
}

// NewClient returns an HTTP client whose connections are validated against
// the egress policy. timeout=0 disables the overall request timeout (used for
// streaming); responseHeaderTimeout bounds waiting for upstream headers.
func NewClient(timeout, responseHeaderTimeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		DialContext:           guardedDialContext(dialer),
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   32,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
		ResponseHeaderTimeout: responseHeaderTimeout,
	}
	return &http.Client{
		Transport: &policyTransport{base: transport},
		Timeout:   timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

type policyTransport struct {
	base *http.Transport
}

func (t *policyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := context.WithValue(req.Context(), schemeKey{}, req.URL.Scheme)
	return t.base.RoundTrip(req.WithContext(ctx))
}

func (t *policyTransport) CloseIdleConnections() {
	t.base.CloseIdleConnections()
}

func guardedDialContext(dialer *net.Dialer) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("netguard: invalid address: %w", err)
		}
		scheme, _ := ctx.Value(schemeKey{}).(string)
		ips, err := resolve(ctx, host)
		if err != nil {
			return nil, err
		}
		if err := checkDestination(scheme, ips); err != nil {
			return nil, err
		}

		var lastErr error
		for _, ip := range ips {
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		return nil, lastErr
	}
}

func resolve(ctx context.Context, host string) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, nil
	}
	addrs, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("netguard: resolve %s: %w", host, err)
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("netguard: resolve %s: no addresses", host)
	}
	return addrs, nil
}

func checkDestination(scheme string, ips []net.IP) error {
	switch scheme {
	case "http", "https":
	default:
		return fmt.Errorf("%w: unsupported scheme %q", ErrBlocked, scheme)
	}
	for _, ip := range ips {
		if isBlocked(ip) {
			return fmt.Errorf("%w: %s", ErrBlocked, ip)
		}
		if scheme == "http" && !isPrivate(ip) {
			return fmt.Errorf("%w: cleartext HTTP to public address %s", ErrBlocked, ip)
		}
	}
	return nil
}

func isBlocked(ip net.IP) bool {
	if ip == nil || ip.IsUnspecified() {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	for _, network := range blockedNets {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func isPrivate(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	for _, network := range privateNets {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}
