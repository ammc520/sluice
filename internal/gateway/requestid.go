package gateway

import (
	"net/http"
	"regexp"

	"github.com/google/uuid"
)

// RequestIDHeader carries the gateway request identifier.
const RequestIDHeader = "X-Request-Id"

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

// requestID accepts a safe client-supplied identifier or generates a UUID.
func requestID(r *http.Request) string {
	if value := r.Header.Get(RequestIDHeader); requestIDPattern.MatchString(value) {
		return value
	}
	return uuid.NewString()
}
