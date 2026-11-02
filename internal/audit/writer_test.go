package audit

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/company/api-key/internal/crypto"
	"github.com/company/api-key/internal/dlp"
	"github.com/company/api-key/internal/store"
)

var testAuditTime = time.Date(2026, 11, 2, 10, 0, 0, 0, shanghaiLocation())

func testCipher(t *testing.T) *crypto.Cipher {
	t.Helper()
	cipher, err := crypto.NewCipher(bytes.Repeat([]byte{0x42}, 32), nil)
	if err != nil {
		t.Fatal(err)
	}
	return cipher
}

func testEvent(id string) Event {
	return Event{
		RequestID:         "req-" + id,
		EventTime:         testAuditTime,
		ClientIP:          "203.0.113.5",
		Method:            "POST",
		Path:              "/v1/chat/completions",
		Model:             "gpt-4o",
		StatusCode:        200,
		LatencyMS:         12,
		UpstreamLatencyMS: 10,
		PromptTokens:      3,
		CompletionTokens:  2,
		TotalTokens:       5,
		Prompt:            []byte("secret prompt " + id),
		Response:          []byte("secret response " + id),
		DLPAction:         "mask",
		DLPHits: []dlp.Hit{{
			RuleID:       uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			RuleName:     "phone",
			Action:       "mask",
			AlertEnabled: true,
			Start:        5,
			End:          16,
		}},
	}
}

type captureStore struct {
	mu       sync.Mutex
	records  []*store.AuditRecord
	calls    int
	failures int
	err      error
}

func (s *captureStore) InsertBatch(_ context.Context, records []*store.AuditRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.failures > 0 {
		s.failures--
		return errors.New("insert failed")
	}
	if s.err != nil {
		return s.err
	}
	for _, record := range records {
		copyRecord := *record
		copyRecord.PromptEnc = append([]byte(nil), record.PromptEnc...)
		copyRecord.ResponseEnc = append([]byte(nil), record.ResponseEnc...)
		copyRecord.DLPHits = append([]byte(nil), record.DLPHits...)
		s.records = append(s.records, &copyRecord)
	}
	return nil
}

func (s *captureStore) snapshot() []*store.AuditRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*store.AuditRecord(nil), s.records...)
}

func (s *captureStore) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

type blockingStore struct {
	entered sync.Once
	started chan struct{}
	release chan struct{}
}

func (s *blockingStore) InsertBatch(ctx context.Context, _ []*store.AuditRecord) error {
	s.entered.Do(func() { close(s.started) })
	select {
	case <-s.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestEnqueueNeverBlocksWhenQueueFull(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	blocking := &blockingStore{started: make(chan struct{}), release: make(chan struct{})}
	w := NewWriter(WriterConfig{
		Store: blocking, Cipher: testCipher(t),
		QueueSize: 1, BatchSize: 1, FlushInterval: time.Hour,
		SpoolDir: t.TempDir(),
	})
	w.Start(ctx)
	defer func() {
		close(blocking.release)
		_ = w.Close(ctx)
	}()

	if !w.Enqueue(testEvent("1")) {
		t.Fatal("first event was not queued")
	}
	select {
	case <-blocking.started:
	case <-time.After(time.Second):
		t.Fatal("worker did not start inserting")
	}

	done := make(chan bool, 1)
	go func() { done <- w.Enqueue(testEvent("2")) }()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Enqueue blocked when the queue was full")
	}
}

func TestWriterEncryptsPromptAndResponse(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sink := &captureStore{}
	cipher := testCipher(t)
	w := NewWriter(WriterConfig{
		Store: sink, Cipher: cipher, SpoolDir: t.TempDir(),
		QueueSize: 8, BatchSize: 2, FlushInterval: time.Hour,
	})
	w.Start(ctx)
	if !w.Enqueue(testEvent("1")) {
		t.Fatal("event was not queued")
	}
	if err := w.Close(ctx); err != nil {
		t.Fatal(err)
	}

	records := sink.snapshot()
	if len(records) != 1 {
		t.Fatalf("records %d", len(records))
	}
	record := records[0]
	if bytes.Contains(record.PromptEnc, []byte("secret")) {
		t.Fatal("prompt was stored in plaintext")
	}
	if bytes.Contains(record.ResponseEnc, []byte("secret")) {
		t.Fatal("response was stored in plaintext")
	}
	prompt, err := cipher.Open(record.PromptEnc, "req-1:prompt")
	if err != nil {
		t.Fatal(err)
	}
	if string(prompt) != "secret prompt 1" {
		t.Fatalf("prompt %q", prompt)
	}
	response, err := cipher.Open(record.ResponseEnc, "req-1:response")
	if err != nil {
		t.Fatal(err)
	}
	if string(response) != "secret response 1" {
		t.Fatalf("response %q", response)
	}
	if !bytes.Contains(record.DLPHits, []byte(`"rule_name":"phone"`)) ||
		!bytes.Contains(record.DLPHits, []byte(`"action":"mask"`)) {
		t.Fatalf("hits %s", record.DLPHits)
	}
	if record.DLPAction != "mask" || record.StatusCode != 200 || record.Model != "gpt-4o" {
		t.Fatalf("record %+v", record)
	}
}

func TestWriterTruncatesLargeBodies(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sink := &captureStore{}
	cipher := testCipher(t)
	w := NewWriter(WriterConfig{Store: sink, Cipher: cipher, SpoolDir: t.TempDir()})
	w.Start(ctx)
	event := testEvent("1")
	event.Prompt = []byte(strings.Repeat("p", MaxAuditTextBytes+100))
	event.Response = []byte(strings.Repeat("r", MaxAuditTextBytes+100))
	if !w.Enqueue(event) {
		t.Fatal("event was not queued")
	}
	if err := w.Close(ctx); err != nil {
		t.Fatal(err)
	}

	record := sink.snapshot()[0]
	if !record.PromptTruncated || !record.ResponseTruncated {
		t.Fatalf("truncation flags: %+v", record)
	}
	prompt, err := cipher.Open(record.PromptEnc, "req-1:prompt")
	if err != nil {
		t.Fatal(err)
	}
	if len(prompt) != MaxAuditTextBytes {
		t.Fatalf("prompt length %d", len(prompt))
	}
	response, err := cipher.Open(record.ResponseEnc, "req-1:response")
	if err != nil {
		t.Fatal(err)
	}
	if len(response) != MaxAuditTextBytes {
		t.Fatalf("response length %d", len(response))
	}
}

func TestWriterRetriesTransientDatabaseFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sink := &captureStore{failures: 1}
	w := NewWriter(WriterConfig{
		Store: sink, Cipher: testCipher(t), SpoolDir: t.TempDir(),
		retryDelays: []time.Duration{time.Millisecond},
	})
	w.Start(ctx)
	w.Enqueue(testEvent("1"))
	if err := w.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if sink.callCount() != 2 {
		t.Fatalf("insert calls %d", sink.callCount())
	}
	if len(sink.snapshot()) != 1 || w.Spooled() != 0 {
		t.Fatalf("records %d spooled %d", len(sink.snapshot()), w.Spooled())
	}
}

func TestWriterSpoolsFailedBatchAndReplayInsertsIt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	spoolDir := t.TempDir()
	w := NewWriter(WriterConfig{
		Store: &captureStore{err: errors.New("database down")}, Cipher: testCipher(t),
		SpoolDir: spoolDir, retryDelays: []time.Duration{time.Millisecond},
	})
	w.Start(ctx)
	w.Enqueue(testEvent("1"))
	if err := w.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if w.Spooled() != 1 || w.Dropped() != 0 {
		t.Fatalf("spooled %d dropped %d", w.Spooled(), w.Dropped())
	}

	sink := &captureStore{}
	n, err := Replay(ctx, spoolDir, testCipher(t), sink)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || len(sink.snapshot()) != 1 {
		t.Fatalf("replayed %d records %d", n, len(sink.snapshot()))
	}
	if sink.snapshot()[0].RequestID != "req-1" {
		t.Fatalf("request %q", sink.snapshot()[0].RequestID)
	}
}

func TestWriterCloseDrainsWhenStartWasNotCalled(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sink := &captureStore{}
	w := NewWriter(WriterConfig{Store: sink, Cipher: testCipher(t), SpoolDir: t.TempDir()})
	if !w.Enqueue(testEvent("1")) {
		t.Fatal("event was not queued")
	}
	if err := w.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if len(sink.snapshot()) != 1 {
		t.Fatalf("records %d", len(sink.snapshot()))
	}
}

func TestWriterRejectsEnqueueAfterClose(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	w := NewWriter(WriterConfig{Store: &captureStore{}, Cipher: testCipher(t), SpoolDir: t.TempDir()})
	w.Start(ctx)
	if err := w.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if w.Enqueue(testEvent("late")) {
		t.Fatal("accepted event after Close")
	}
	if w.Dropped() != 1 {
		t.Fatalf("dropped %d", w.Dropped())
	}
}

func TestWriterDropsWhenSpoolIsUnavailable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// A file where a directory is expected makes both queue-overflow spooling
	// and batch spooling fail.
	path := t.TempDir() + "/not-a-directory"
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	w := NewWriter(WriterConfig{
		Store: &captureStore{err: errors.New("database down")}, Cipher: testCipher(t),
		SpoolDir: path, retryDelays: []time.Duration{time.Millisecond},
	})
	w.Start(ctx)
	w.Enqueue(testEvent("1"))
	if err := w.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if w.Dropped() != 1 || w.Spooled() != 0 {
		t.Fatalf("dropped %d spooled %d", w.Dropped(), w.Spooled())
	}
}
