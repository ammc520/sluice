package proxy

import "errors"

// Errors returned by the resolver and proxy. Callers translate these into
// gateway responses and audit error codes.
var (
	ErrUnsupportedEndpoint = errors.New("proxy: unsupported endpoint")
	ErrNoRoute             = errors.New("proxy: no matching provider route")
	ErrProviderConfig      = errors.New("proxy: provider credentials unavailable")
	ErrUpstreamUnavailable = errors.New("proxy: upstream unavailable")
	ErrUpstreamTimeout     = errors.New("proxy: upstream timeout")
	ErrClientDisconnected  = errors.New("proxy: client disconnected")
)
