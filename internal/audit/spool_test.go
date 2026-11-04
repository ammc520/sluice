package audit

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/company/sluice/internal/store"
)

func testRecord(id string) *store.AuditRecord {
	return &store.AuditRecord{
		EventTime:  testAuditTime,
		RequestID:  "req-" + id,
		ClientIP:   "203.0.113.5",
		Method:     "POST",
		Path:       "/v1/chat/completions",
		Model:      "confidential-model",
		StatusCode: 200,
		DLPAction:  "none",
		DLPHits:    []byte("[]"),
	}
}

func TestSpoolWritesEncryptedRecordWithPrivatePermissions(t *testing.T) {
	dir := t.TempDir()
	if err := Write(dir, testCipher(t), testRecord("1")); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(dir, activeSpoolName)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Windows does not expose POSIX permission bits through os.Stat; the
	// gateway still issues Chmod(0600) for deployment on Linux.
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("permissions %o", info.Mode().Perm())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("confidential-model")) {
		t.Fatal("record fields were written in plaintext")
	}
	if bytes.Contains(raw, []byte("203.0.113.5")) {
		t.Fatal("client IP was written in plaintext")
	}
}

func TestSpoolReplay(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	if err := Write(dir, testCipher(t), testRecord("1")); err != nil {
		t.Fatal(err)
	}

	sink := &captureStore{}
	n, err := Replay(ctx, dir, testCipher(t), sink)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || len(sink.snapshot()) != 1 {
		t.Fatalf("n=%d records=%d", n, len(sink.snapshot()))
	}
	record := sink.snapshot()[0]
	if record.RequestID != "req-1" || record.Model != "confidential-model" || record.ClientIP != "203.0.113.5" {
		t.Fatalf("record %+v", record)
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.log"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatalf("spool files remain: %v", files)
	}
}

func TestSpoolReplayKeepsFileWhenInsertFails(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	if err := Write(dir, testCipher(t), testRecord("1")); err != nil {
		t.Fatal(err)
	}

	if _, err := Replay(ctx, dir, testCipher(t), &captureStore{err: errors.New("down")}); err == nil {
		t.Fatal("expected replay error")
	}
	files, err := filepath.Glob(filepath.Join(dir, sealedSpoolGlob))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("sealed files: %v", files)
	}

	sink := &captureStore{}
	if n, err := Replay(ctx, dir, testCipher(t), sink); err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if len(sink.snapshot()) != 1 {
		t.Fatalf("records %d", len(sink.snapshot()))
	}
}

func TestSpoolWriteDoesNotWaitForSlowReplayInsert(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	dir := t.TempDir()
	if err := Write(dir, testCipher(t), testRecord("1")); err != nil {
		t.Fatal(err)
	}
	blocking := &blockingStore{started: make(chan struct{}), release: make(chan struct{})}
	replayDone := make(chan error, 1)
	go func() {
		_, err := Replay(ctx, dir, testCipher(t), blocking)
		replayDone <- err
	}()
	select {
	case <-blocking.started:
	case <-time.After(time.Second):
		t.Fatal("replay did not reach the insert")
	}

	writeDone := make(chan error, 1)
	go func() { writeDone <- Write(dir, testCipher(t), testRecord("2")) }()
	select {
	case err := <-writeDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("spool write blocked behind replay insert")
	}

	close(blocking.release)
	if err := <-replayDone; err != nil {
		t.Fatal(err)
	}
}

func TestSpoolRotatesAtSizeLimit(t *testing.T) {
	dir := t.TempDir()
	active := filepath.Join(dir, activeSpoolName)
	if err := os.WriteFile(active, bytes.Repeat([]byte("x"), maxSpoolFileBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Write(dir, testCipher(t), testRecord("1")); err != nil {
		t.Fatal(err)
	}
	sealed, err := filepath.Glob(filepath.Join(dir, sealedSpoolGlob))
	if err != nil {
		t.Fatal(err)
	}
	if len(sealed) != 1 {
		t.Fatalf("sealed files: %v", sealed)
	}
	info, err := os.Stat(active)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() == 0 || info.Size() >= maxSpoolFileBytes {
		t.Fatalf("active size %d", info.Size())
	}
}
