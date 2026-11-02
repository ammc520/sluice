package proxy

import (
	"net/http"
	"strings"
)

// hopByHopHeaders are connection-specific and must not be forwarded.
var hopByHopHeaders = map[string]bool{
	"Connection":          true,
	"Proxy-Connection":    true,
	"Keep-Alive":          true,
	"Proxy-Authenticate":  true,
	"Proxy-Authorization": true,
	"Te":                  true,
	"Trailer":             true,
	"Transfer-Encoding":   true,
	"Upgrade":             true,
	"Content-Length":      true,
}

// prepareHeaders builds upstream request headers: client credentials and
// hop-by-hop headers are dropped, then the provider's credential is injected.
func prepareHeaders(client http.Header, target Target, apiKey string) http.Header {
	upstream := make(http.Header, len(client)+2)
	for name, values := range client {
		lower := strings.ToLower(name)
		switch lower {
		case "authorization", "x-api-key", "host", "cookie", "accept-encoding":
			continue
		}
		if hopByHopHeaders[http.CanonicalHeaderKey(name)] {
			continue
		}
		if strings.HasPrefix(lower, "proxy-") {
			continue
		}
		for _, value := range values {
			upstream.Add(name, value)
		}
	}
	if target.Protocol == "anthropic" {
		upstream.Set("x-api-key", apiKey)
		if upstream.Get("anthropic-version") == "" {
			upstream.Set("anthropic-version", "2023-06-01")
		}
	} else {
		upstream.Set("Authorization", "Bearer "+apiKey)
	}
	return upstream
}

// copyResponseHeaders copies safe upstream headers to the client response.
func copyResponseHeaders(dst, src http.Header) {
	for name, values := range src {
		canonical := http.CanonicalHeaderKey(name)
		if hopByHopHeaders[canonical] || strings.HasPrefix(strings.ToLower(name), "proxy-") {
			continue
		}
		switch strings.ToLower(name) {
		case "set-cookie", "content-length", "x-request-id":
			// The gateway owns the client-facing request ID; the upstream ID
			// is captured separately in Result.UpstreamRequestID.
			continue
		}
		for _, value := range values {
			dst.Add(name, value)
		}
	}
}
