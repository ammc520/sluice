package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/company/sluice/internal/crypto"
	"github.com/company/sluice/internal/metrics"
	"github.com/company/sluice/internal/store"
)

// MaxAuditTextBytes bounds each stored prompt or response. Anything larger is
// truncated and flagged in the audit record.
const MaxAuditTextBytes = 256 * 1024

// AuditBatchStore is the write side of the audit store used by the writer and
// by spool replay.
type AuditBatchStore interface {
	InsertBatch(ctx context.Context, records []*store.AuditRecord) error
}

// WriterConfig configures the asynchronous audit writer.
type WriterConfig struct {
	Store         AuditBatchStore
	Cipher        *crypto.Cipher
	QueueSize     int
	BatchSize     int
	FlushInterval time.Duration
	SpoolDir      string
	Logger        *slog.Logger
	Metrics       *metrics.Registry

	// retryDelays is overridable by tests; production uses the defaults.
	retryDelays []time.Duration
}

var defaultRetryDelays = []time.Duration{
	100 * time.Millisecond,
	500 * time.Millisecond,
	2 * time.Second,
}

const (
	defaultAuditQueueSize = 4096
	defaultAuditBatchSize = 200
	defaultAuditFlush     = time.Second
	auditInsertTimeout    = 10 * time.Second
	replayInterval        = 30 * time.Second
	replayTimeout         = 30 * time.Second
)

// Writer receives audit events without blocking the request path, encrypts
// prompt and response bodies, and batches inserts into PostgreSQL. Records
// that cannot be queued or written are sealed into an on-disk spool.
type Writer struct {
	cfg WriterConfig

	ch   chan Event
	done chan struct{}
	stop chan struct{}

	closeOnce sync.Once
	started   atomic.Bool
	mu        sync.RWMutex
	closed    bool

	dropped atomic.Int64
	spooled atomic.Int64
}

// NewWriter creates a writer. Start must be called (or Close, which drains
// synchronously when Start was never called).
func NewWriter(cfg WriterConfig) *Writer {
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = defaultAuditQueueSize
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = defaultAuditBatchSize
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = defaultAuditFlush
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.retryDelays == nil {
		cfg.retryDelays = defaultRetryDelays
	}
	return &Writer{
		cfg:  cfg,
		ch:   make(chan Event, cfg.QueueSize),
		done: make(chan struct{}),
		stop: make(chan struct{}),
	}
}

// Start launches the batch writer and the periodic spool replay loop. It is
// safe to call more than once.
func (w *Writer) Start(ctx context.Context) {
	if !w.started.CompareAndSwap(false, true) {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	w.observeMetrics()
	go w.run()
	go w.replayLoop(ctx)
}

// Enqueue accepts an event without blocking. It returns false when the event
// was spooled instead of queued, or when it could not be accepted at all.
func (w *Writer) Enqueue(event Event) bool {
	// Only the queue length is touched here; scanning the spool directory on
	// the request path could block. Spool counts are refreshed by the writer
	// and replay goroutines, and on the rare queue-full spool fallback.
	defer w.observeQueueDepth()
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.closed || w.cfg.Cipher == nil {
		w.dropped.Add(1)
		return false
	}

	event.Prompt, event.PromptTruncated = capAuditText(event.Prompt, event.PromptTruncated)
	event.Response, event.ResponseTruncated = capAuditText(event.Response, event.ResponseTruncated)
	if event.EventTime.IsZero() {
		event.EventTime = time.Now()
	}

	select {
	case w.ch <- event:
		return true
	default:
	}

	record, err := eventToRecord(event, w.cfg.Cipher)
	if err != nil {
		w.dropped.Add(1)
		w.cfg.Logger.Error("audit event could not be converted", "request_id", event.RequestID, "error", err)
		return false
	}
	if err := w.spoolRecord(record); err != nil {
		w.cfg.Logger.Error("audit event could not be spooled", "request_id", event.RequestID, "error", err)
		return false
	}
	w.observeSpoolFiles()
	return false
}

// Dropped reports events that could not be queued, converted, or spooled.
func (w *Writer) Dropped() int64 { return w.dropped.Load() }

// Spooled reports events written to the encrypted on-disk spool.
func (w *Writer) Spooled() int64 { return w.spooled.Load() }

// Close stops accepting events, drains the queue, and waits for pending
// batches. If ctx expires first, the in-flight write is abandoned to the
// process shutdown but the writer remains closed.
func (w *Writer) Close(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	w.closeOnce.Do(func() {
		w.mu.Lock()
		w.closed = true
		close(w.ch)
		close(w.stop)
		w.mu.Unlock()

		// If Start was never called, drain the closed channel here.
		if w.started.CompareAndSwap(false, true) {
			go w.run()
		}
	})

	select {
	case <-w.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *Writer) run() {
	defer close(w.done)
	ticker := time.NewTicker(w.cfg.FlushInterval)
	defer ticker.Stop()

	batch := make([]Event, 0, w.cfg.BatchSize)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		w.persist(batch)
		batch = batch[:0]
		w.observeMetrics()
	}

	for {
		select {
		case event, ok := <-w.ch:
			if !ok {
				flush()
				return
			}
			batch = append(batch, event)
			w.observeQueueDepth()
			if len(batch) >= w.cfg.BatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

func (w *Writer) persist(events []Event) {
	records := make([]*store.AuditRecord, 0, len(events))
	for _, event := range events {
		record, err := eventToRecord(event, w.cfg.Cipher)
		if err != nil {
			w.dropped.Add(1)
			w.cfg.Logger.Error("audit event could not be converted", "request_id", event.RequestID, "error", err)
			continue
		}
		records = append(records, record)
	}
	if len(records) == 0 {
		return
	}

	if w.cfg.Store != nil {
		delays := w.cfg.retryDelays
		for attempt := 0; ; attempt++ {
			ctx, cancel := context.WithTimeout(context.Background(), auditInsertTimeout)
			err := w.cfg.Store.InsertBatch(ctx, records)
			cancel()
			if err == nil {
				return
			}
			w.cfg.Logger.Warn("audit batch insert failed",
				"attempt", attempt+1, "records", len(records), "error", err)
			if attempt >= len(delays) {
				break
			}
			time.Sleep(delays[attempt])
		}
	}

	for _, record := range records {
		if err := w.spoolRecord(record); err != nil {
			w.cfg.Logger.Error("audit record could not be spooled",
				"request_id", record.RequestID, "error", err)
		}
	}
	w.observeSpoolFiles()
}

// observeMetrics publishes queue depth and spool file count. Both values are
// gauges; they are recomputed rather than cached so a missed transition cannot
// leave the metric stale.
func (w *Writer) observeMetrics() {
	w.observeQueueDepth()
	w.observeSpoolFiles()
}

func (w *Writer) observeQueueDepth() {
	w.cfg.Metrics.SetAuditQueueDepth(len(w.ch))
}

func (w *Writer) observeSpoolFiles() {
	if w.cfg.SpoolDir == "" {
		return
	}
	files, err := filepath.Glob(filepath.Join(w.cfg.SpoolDir, "audit-spool*.log"))
	if err != nil {
		return
	}
	w.cfg.Metrics.SetSpoolFiles(len(files))
}

func (w *Writer) spoolRecord(record *store.AuditRecord) error {
	if err := Write(w.cfg.SpoolDir, w.cfg.Cipher, record); err != nil {
		w.dropped.Add(1)
		return err
	}
	w.spooled.Add(1)
	return nil
}

func (w *Writer) replayLoop(ctx context.Context) {
	w.replayOnce(ctx)
	ticker := time.NewTicker(replayInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stop:
			return
		case <-ticker.C:
			w.replayOnce(ctx)
		}
	}
}

func (w *Writer) replayOnce(parent context.Context) {
	if w.cfg.Store == nil || w.cfg.SpoolDir == "" || w.cfg.Cipher == nil {
		return
	}
	ctx, cancel := context.WithTimeout(parent, replayTimeout)
	defer cancel()
	n, err := Replay(ctx, w.cfg.SpoolDir, w.cfg.Cipher, w.cfg.Store)
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		w.cfg.Logger.Warn("audit spool replay failed", "error", err)
	}
	if n > 0 {
		w.cfg.Logger.Info("audit spool replayed", "records", n)
	}
	w.observeSpoolFiles()
}

type auditDLPHit struct {
	RuleID       string `json:"rule_id"`
	RuleName     string `json:"rule_name"`
	Action       string `json:"action"`
	AlertEnabled bool   `json:"alert_enabled"`
	Start        int    `json:"start"`
	End          int    `json:"end"`
}

func eventToRecord(event Event, cipher *crypto.Cipher) (*store.AuditRecord, error) {
	if cipher == nil {
		return nil, errors.New("audit: cipher is required")
	}
	if event.RequestID == "" {
		return nil, errors.New("audit: request id is required")
	}
	if event.EventTime.IsZero() {
		event.EventTime = time.Now()
	}

	event.Prompt, event.PromptTruncated = capAuditText(event.Prompt, event.PromptTruncated)
	event.Response, event.ResponseTruncated = capAuditText(event.Response, event.ResponseTruncated)

	var promptEnc, responseEnc []byte
	if len(event.Prompt) > 0 {
		sealed, err := cipher.Seal(event.Prompt, event.RequestID+":prompt")
		if err != nil {
			return nil, fmt.Errorf("audit: encrypt prompt: %w", err)
		}
		promptEnc = sealed
	}
	if len(event.Response) > 0 {
		sealed, err := cipher.Seal(event.Response, event.RequestID+":response")
		if err != nil {
			return nil, fmt.Errorf("audit: encrypt response: %w", err)
		}
		responseEnc = sealed
	}

	hits := make([]auditDLPHit, 0, len(event.DLPHits))
	for _, hit := range event.DLPHits {
		hits = append(hits, auditDLPHit{
			RuleID:       hit.RuleID.String(),
			RuleName:     hit.RuleName,
			Action:       hit.Action,
			AlertEnabled: hit.AlertEnabled,
			Start:        hit.Start,
			End:          hit.End,
		})
	}
	hitsJSON, err := json.Marshal(hits)
	if err != nil {
		return nil, fmt.Errorf("audit: encode dlp hits: %w", err)
	}

	clientIP := event.ClientIP
	if clientIP != "" && net.ParseIP(clientIP) == nil {
		clientIP = ""
	}
	dlpAction := event.DLPAction
	switch dlpAction {
	case "none", "block", "mask", "audit":
	case "":
		dlpAction = "none"
	default:
		dlpAction = "none"
	}
	statusCode := event.StatusCode
	if statusCode == 0 {
		statusCode = 500
	}

	return &store.AuditRecord{
		EventTime:          event.EventTime,
		RequestID:          event.RequestID,
		TokenID:            event.TokenID,
		EmployeeID:         event.EmployeeID,
		DepartmentID:       event.DepartmentID,
		ClientIP:           clientIP,
		Method:             truncateString(event.Method, 8),
		Path:               event.Path,
		Model:              event.Model,
		ProviderID:         event.ProviderID,
		StatusCode:         statusCode,
		ErrorCode:          truncateString(event.ErrorCode, 64),
		LatencyMS:          event.LatencyMS,
		UpstreamLatencyMS:  event.UpstreamLatencyMS,
		FirstByteMS:        event.FirstByteMS,
		PromptTokens:       event.PromptTokens,
		CompletionTokens:   event.CompletionTokens,
		TotalTokens:        event.TotalTokens,
		UsageEstimated:     event.UsageEstimated,
		PromptEnc:          promptEnc,
		ResponseEnc:        responseEnc,
		PromptTruncated:    event.PromptTruncated,
		ResponseTruncated:  event.ResponseTruncated,
		DLPAction:          dlpAction,
		DLPHits:            hitsJSON,
		ClientDisconnected: event.ClientDisconnected,
		UpstreamRequestID:  event.UpstreamRequestID,
	}, nil
}

func capAuditText(data []byte, alreadyTruncated bool) ([]byte, bool) {
	if len(data) <= MaxAuditTextBytes {
		return data, alreadyTruncated
	}
	out := make([]byte, MaxAuditTextBytes)
	copy(out, data[:MaxAuditTextBytes])
	return out, true
}

func truncateString(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max]
}
