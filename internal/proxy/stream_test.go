package proxy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type flushWriter struct {
	header     http.Header
	body       bytes.Buffer
	status     int
	flushes    int
	failWrites bool
}

func newFlushWriter() *flushWriter { return &flushWriter{header: http.Header{}} }

func (w *flushWriter) Header() http.Header { return w.header }

func (w *flushWriter) Write(p []byte) (int, error) {
	if w.failWrites {
		return 0, io.ErrClosedPipe
	}
	return w.body.Write(p)
}

func (w *flushWriter) WriteHeader(status int) { w.status = status }
func (w *flushWriter) Flush()                 { w.flushes++ }

func TestStreamFlushesEachChunk(t *testing.T) {
	acceptEncoding := make(chan string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		acceptEncoding <- r.Header.Get("Accept-Encoding")
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"he\"}}]}\n\n"))
		flusher.Flush()
		time.Sleep(30 * time.Millisecond)
		_, _ = w.Write([]byte("data: {\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":4,\"total_tokens\":7}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
	}))
	defer upstream.Close()

	c := testCipher(t)
	provider := encryptedProvider(t, c, "openai_compatible", upstream.URL, "upstream-key")
	p := New(Config{Cipher: c, Client: upstream.Client()})
	w := newFlushWriter()
	result, err := p.Stream(context.Background(), w, w.Flush, Request{
		Target: Target{Provider: provider, Protocol: "openai_compatible"},
		Path:   "/v1/chat/completions",
		Body:   []byte(`{"model":"gpt","stream":true}`),
		Header: http.Header{},
		Stream: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	if got := <-acceptEncoding; got != "identity" {
		t.Fatalf("accept-encoding %q", got)
	}
	if w.status != http.StatusOK {
		t.Fatalf("status %d", w.status)
	}
	if w.flushes < 2 {
		t.Fatalf("flush count %d", w.flushes)
	}
	if w.header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("content-type %q", w.header.Get("Content-Type"))
	}
	if w.header.Get("Cache-Control") != "no-cache" || w.header.Get("X-Accel-Buffering") != "no" {
		t.Fatalf("headers %v", w.header)
	}
	if !strings.Contains(w.body.String(), "he") || !strings.Contains(w.body.String(), "[DONE]") {
		t.Fatalf("body %q", w.body.String())
	}
	if result.Usage.TotalTokens != 7 || result.Usage.Estimated {
		t.Fatalf("usage %+v", result.Usage)
	}
	if result.StatusCode != http.StatusOK || result.ClientDisconnected {
		t.Fatalf("result %+v", result)
	}
}

func TestStreamCaptureLimitDoesNotTruncateClient(t *testing.T) {
	prefix := "data: {\"choices\":[{\"delta\":{\"content\":\"" + strings.Repeat("a", 100) + "\"}}]}\n\n"
	suffix := "data: {\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":1,\"total_tokens\":3}}\n\n"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(prefix))
		_, _ = w.Write([]byte(suffix))
	}))
	defer upstream.Close()

	c := testCipher(t)
	provider := encryptedProvider(t, c, "openai_compatible", upstream.URL, "key")
	p := New(Config{Cipher: c, Client: upstream.Client(), CaptureLimit: 25})
	w := newFlushWriter()
	result, err := p.Stream(context.Background(), w, w.Flush, Request{
		Target: Target{Provider: provider, Protocol: "openai_compatible"},
		Path:   "/v1/chat/completions",
		Body:   []byte(`{}`),
		Header: http.Header{},
		Stream: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if w.body.String() != prefix+suffix {
		t.Fatalf("client body truncated: %d bytes", w.body.Len())
	}
	if len(result.ResponseBody) != 25 {
		t.Fatalf("captured %d bytes", len(result.ResponseBody))
	}
	if result.Usage.TotalTokens != 3 || result.Usage.Estimated {
		t.Fatalf("usage %+v", result.Usage)
	}
}

func TestStreamClientWriteFailure(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[]}\n\n"))
	}))
	defer upstream.Close()

	c := testCipher(t)
	provider := encryptedProvider(t, c, "openai_compatible", upstream.URL, "key")
	p := New(Config{Cipher: c, Client: upstream.Client()})
	w := newFlushWriter()
	w.failWrites = true
	result, err := p.Stream(context.Background(), w, w.Flush, Request{
		Target: Target{Provider: provider, Protocol: "openai_compatible"},
		Path:   "/v1/chat/completions",
		Body:   []byte(`{}`),
		Header: http.Header{},
		Stream: true,
	})
	if !errors.Is(err, ErrClientDisconnected) || !result.ClientDisconnected {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestStreamClientCancellation(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		_, _ = w.Write([]byte("data: {\"choices\":[]}\n\n"))
		flusher.Flush()
		<-r.Context().Done()
	}))
	defer upstream.Close()

	c := testCipher(t)
	provider := encryptedProvider(t, c, "openai_compatible", upstream.URL, "key")
	p := New(Config{Cipher: c, Client: upstream.Client()})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	w := newFlushWriter()
	result, err := p.Stream(ctx, w, w.Flush, Request{
		Target: Target{Provider: provider, Protocol: "openai_compatible"},
		Path:   "/v1/chat/completions",
		Body:   []byte(`{}`),
		Header: http.Header{},
		Stream: true,
	})
	if !errors.Is(err, ErrClientDisconnected) || !result.ClientDisconnected {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
