package audit

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/google/uuid"

	"github.com/company/sluice/internal/crypto"
	"github.com/company/sluice/internal/store"
)

const (
	maxSpoolFileBytes = 10 * 1024 * 1024
	maxSpoolLineBytes = 16 * 1024 * 1024
	activeSpoolName   = "audit-spool.log"
	sealedSpoolGlob   = "audit-spool-*.log"
)

// spoolMu serializes rotation and appends so the active file cannot be renamed
// while a record is being appended. It is not held during database inserts.
var spoolMu sync.Mutex

// replayMu ensures only one replay pass processes a spool directory at a time.
var replayMu sync.Mutex

type spoolEnvelope struct {
	RequestID string `json:"request_id"`
	Data      []byte `json:"data"`
}

// Write appends one encrypted audit record to the spool directory. The file is
// rotated at 10 MiB and is only readable by the gateway account.
func Write(dir string, cipher *crypto.Cipher, record *store.AuditRecord) error {
	if cipher == nil {
		return errors.New("audit spool: cipher is required")
	}
	if record == nil {
		return errors.New("audit spool: record is required")
	}
	if record.RequestID == "" {
		return errors.New("audit spool: request id is required")
	}
	if dir == "" {
		return errors.New("audit spool: directory is required")
	}

	payload, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("audit spool: marshal record: %w", err)
	}
	sealed, err := cipher.Seal(payload, "spool:"+record.RequestID)
	if err != nil {
		return fmt.Errorf("audit spool: encrypt record: %w", err)
	}
	line, err := json.Marshal(spoolEnvelope{RequestID: record.RequestID, Data: sealed})
	if err != nil {
		return fmt.Errorf("audit spool: marshal envelope: %w", err)
	}
	encoded := base64.StdEncoding.EncodeToString(line) + "\n"
	if len(encoded) > maxSpoolLineBytes {
		return errors.New("audit spool: record is too large")
	}

	spoolMu.Lock()
	defer spoolMu.Unlock()

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("audit spool: create directory: %w", err)
	}
	active := filepath.Join(dir, activeSpoolName)
	if info, err := os.Stat(active); err == nil && info.Size() > 0 &&
		info.Size()+int64(len(encoded)) > maxSpoolFileBytes {
		if err := rotateLocked(dir); err != nil {
			return err
		}
	}

	file, err := os.OpenFile(active, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("audit spool: open: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return fmt.Errorf("audit spool: chmod: %w", err)
	}
	if _, err := file.WriteString(encoded); err != nil {
		_ = file.Close()
		return fmt.Errorf("audit spool: write: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("audit spool: sync: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("audit spool: close: %w", err)
	}
	return nil
}

// Replay decrypts every sealed spool file and inserts its records through
// sink. Files are removed only after all their batches are accepted. The
// active file is rotated first so replay never competes with appends.
func Replay(ctx context.Context, dir string, cipher *crypto.Cipher, sink AuditBatchStore) (int, error) {
	if cipher == nil {
		return 0, errors.New("audit spool: cipher is required")
	}
	if sink == nil {
		return 0, errors.New("audit spool: sink is required")
	}
	if dir == "" {
		return 0, errors.New("audit spool: directory is required")
	}

	replayMu.Lock()
	defer replayMu.Unlock()

	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}

	// Take a snapshot of sealed files. New appends go to a fresh active file
	// after rotation and are picked up by the next replay pass.
	spoolMu.Lock()
	if err := rotateLocked(dir); err != nil {
		spoolMu.Unlock()
		return 0, err
	}
	files, err := filepath.Glob(filepath.Join(dir, sealedSpoolGlob))
	spoolMu.Unlock()
	if err != nil {
		return 0, fmt.Errorf("audit spool: list: %w", err)
	}
	sort.Strings(files)

	total := 0
	for _, path := range files {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		records, err := readSpoolFile(path, cipher)
		if err != nil {
			return total, err
		}
		for start := 0; start < len(records); start += 200 {
			end := start + 200
			if end > len(records) {
				end = len(records)
			}
			if err := sink.InsertBatch(ctx, records[start:end]); err != nil {
				return total, fmt.Errorf("audit spool: insert %s: %w", filepath.Base(path), err)
			}
			total += end - start
		}
		if err := os.Remove(path); err != nil {
			return total, fmt.Errorf("audit spool: remove %s: %w", filepath.Base(path), err)
		}
	}
	return total, nil
}

func rotateLocked(dir string) error {
	active := filepath.Join(dir, activeSpoolName)
	info, err := os.Stat(active)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("audit spool: stat: %w", err)
	}
	if info.Size() == 0 {
		return nil
	}
	sealed := filepath.Join(dir, "audit-spool-"+uuid.NewString()+".log")
	if err := os.Rename(active, sealed); err != nil {
		return fmt.Errorf("audit spool: rotate: %w", err)
	}
	return nil
}

func readSpoolFile(path string, cipher *crypto.Cipher) ([]*store.AuditRecord, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("audit spool: open %s: %w", filepath.Base(path), err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), maxSpoolLineBytes)
	records := []*store.AuditRecord{}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		record, err := decodeSpoolLine(line, cipher)
		if err != nil {
			return nil, fmt.Errorf("audit spool: %s: %w", filepath.Base(path), err)
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("audit spool: read %s: %w", filepath.Base(path), err)
	}
	return records, nil
}

func decodeSpoolLine(line string, cipher *crypto.Cipher) (*store.AuditRecord, error) {
	raw, err := base64.StdEncoding.DecodeString(line)
	if err != nil {
		return nil, errors.New("invalid base64")
	}
	var envelope spoolEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, errors.New("invalid envelope")
	}
	if envelope.RequestID == "" {
		return nil, errors.New("missing request id")
	}
	payload, err := cipher.Open(envelope.Data, "spool:"+envelope.RequestID)
	if err != nil {
		return nil, errors.New("decryption failed")
	}
	var record store.AuditRecord
	if err := json.Unmarshal(payload, &record); err != nil {
		return nil, errors.New("invalid record")
	}
	if record.RequestID != envelope.RequestID {
		return nil, errors.New("request id mismatch")
	}
	return &record, nil
}
