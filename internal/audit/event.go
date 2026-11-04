// Package audit defines the gateway audit event and its asynchronous writer.
package audit

import (
	"time"

	"github.com/google/uuid"

	"github.com/company/sluice/internal/dlp"
)

// Event is one gateway request outcome. The async Writer encrypts Prompt and
// Response before persisting them; rejected requests produce events too.
type Event struct {
	RequestID string
	EventTime time.Time

	TokenID      *uuid.UUID
	EmployeeID   *uuid.UUID
	DepartmentID *int64
	ClientIP     string
	Method       string
	Path         string
	Model        string
	ProviderID   *uuid.UUID

	StatusCode        int
	ErrorCode         string
	LatencyMS         int
	UpstreamLatencyMS int
	FirstByteMS       int

	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	UsageEstimated   bool

	// Prompt is the original request body before DLP masking. Response is the
	// captured upstream response. Both are plaintext here and must only be
	// persisted through the encrypted audit Writer.
	Prompt            []byte
	Response          []byte
	PromptTruncated   bool
	ResponseTruncated bool

	DLPAction          string
	DLPHits            []dlp.Hit
	ClientDisconnected bool
	UpstreamRequestID  string
}
