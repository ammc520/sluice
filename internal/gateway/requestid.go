package gateway

import (
	"net/http"
	"regexp"

	"github.com/google/uuid"
)

// RequestIDHeader carries the gateway request identifier.
const RequestIDHeader = "X-Request-Id"

// ClientRequestIDHeader echoes a sanitized client correlation value. It is
// never used as the audit key because clients could otherwise suppress or
// collide with audit records by reusing an identifier.
const ClientRequestIDHeader = "X-Client-Request-Id"

var clientRequestIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

// requestID always generates the server-side audit identity.
func requestID() string {
	return uuid.NewString()
}

// clientRequestID returns a safe client-supplied correlation value, or "".
func clientRequestID(r *http.Request) string {
	value := r.Header.Get(RequestIDHeader)
	if clientRequestIDPattern.MatchString(value) {
		return value
	}
	return ""
}
