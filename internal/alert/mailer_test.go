package alert

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/company/api-key/internal/crypto"
	"github.com/company/api-key/internal/store"
)

var alertTime = time.Date(2026, 11, 2, 10, 0, 0, 0, time.FixedZone("CST", 8*3600))

type fakeMailer struct {
	mu       sync.Mutex
	count    int
	subjects []string
	bodies   []string
	failures int
	started  chan struct{}
	release  chan struct{}
	once     sync.Once
}

func (m *fakeMailer) Send(ctx context.Context, subject, body string) error {
	m.mu.Lock()
	m.count++
	m.subjects = append(m.subjects, subject)
	m.bodies = append(m.bodies, body)
	fail := m.failures > 0
	if fail {
		m.failures--
	}
	m.mu.Unlock()

	if m.started != nil {
		m.once.Do(func() { close(m.started) })
	}
	if m.release != nil {
		select {
		case <-m.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if fail {
		return errors.New("send failed")
	}
	return nil
}

func (m *fakeMailer) stats() (int, []string, []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.count, append([]string(nil), m.subjects...), append([]string(nil), m.bodies...)
}

func TestAlertDedup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	mailer := &fakeMailer{}
	worker := NewWorker(func(context.Context) (Mailer, error) { return mailer, nil },
		10, 5*time.Minute, func() time.Time { return alertTime })
	worker.Start(ctx)
	if !worker.Enqueue(ctx, Alert{RuleName: "aws", Employee: "alice", Action: "block", RequestID: "1", At: alertTime}) {
		t.Fatal("first alert was not queued")
	}
	if worker.Enqueue(ctx, Alert{RuleName: "aws", Employee: "alice", Action: "block", RequestID: "2", At: alertTime.Add(time.Minute)}) {
		t.Fatal("duplicate alert was queued")
	}
	if err := worker.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if count, _, _ := mailer.stats(); count != 1 {
		t.Fatalf("sent %d", count)
	}
}

func TestAlertDedupWindowExpires(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	now := alertTime
	mailer := &fakeMailer{}
	worker := NewWorker(func(context.Context) (Mailer, error) { return mailer, nil },
		10, 5*time.Minute, func() time.Time { return now })
	worker.Start(ctx)
	event := Alert{RuleName: "aws", Employee: "alice", Action: "block", RequestID: "1", At: now}
	if !worker.Enqueue(ctx, event) {
		t.Fatal("first alert rejected")
	}
	now = now.Add(4 * time.Minute)
	if worker.Enqueue(ctx, event) {
		t.Fatal("alert inside the dedup window was queued")
	}
	now = now.Add(2 * time.Minute)
	if !worker.Enqueue(ctx, event) {
		t.Fatal("alert after the dedup window was rejected")
	}
	if err := worker.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if count, _, _ := mailer.stats(); count != 2 {
		t.Fatalf("sent %d", count)
	}
}

func TestAlertBodyHasNoPrompt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	mailer := &fakeMailer{}
	worker := NewWorker(func(context.Context) (Mailer, error) { return mailer, nil },
		10, 5*time.Minute, func() time.Time { return alertTime })
	worker.Start(ctx)
	worker.Enqueue(ctx, Alert{
		RuleName: "aws_access_key", Employee: "alice", Action: "block",
		RequestID: "req-1", At: alertTime,
	})
	if err := worker.Close(ctx); err != nil {
		t.Fatal(err)
	}

	count, subjects, bodies := mailer.stats()
	if count != 1 {
		t.Fatalf("sent %d", count)
	}
	if subjects[0] != "[DLP] block - aws_access_key" {
		t.Fatalf("subject %q", subjects[0])
	}
	body := bodies[0]
	for _, want := range []string{"aws_access_key", "alice", "req-1", "block", "2026-11-02"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q: %s", want, body)
		}
	}
	for _, forbidden := range []string{"secret prompt", "secret response"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("body contains %q: %s", forbidden, body)
		}
	}
}

func TestEnqueueNeverBlocksWhenAlertQueueIsFull(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	mailer := &fakeMailer{}
	worker := NewWorker(func(context.Context) (Mailer, error) { return mailer, nil },
		1, time.Minute, func() time.Time { return alertTime })
	if !worker.Enqueue(ctx, Alert{RuleName: "one", Employee: "alice", Action: "block", RequestID: "1"}) {
		t.Fatal("first alert rejected")
	}

	done := make(chan bool, 1)
	go func() {
		done <- worker.Enqueue(ctx, Alert{RuleName: "two", Employee: "alice", Action: "block", RequestID: "2"})
	}()
	select {
	case accepted := <-done:
		if accepted {
			t.Fatal("full queue accepted the alert")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Enqueue blocked on a full queue")
	}

	if err := worker.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerRetriesDelivery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	mailer := &fakeMailer{failures: 2}
	loaderCalls := 0
	worker := NewWorker(func(context.Context) (Mailer, error) {
		loaderCalls++
		return mailer, nil
	}, 1, time.Minute, func() time.Time { return alertTime })
	worker.retryDelays = []time.Duration{time.Millisecond, time.Millisecond}
	worker.Start(ctx)
	worker.Enqueue(ctx, Alert{RuleName: "aws", Employee: "alice", Action: "block", RequestID: "1"})
	if err := worker.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if count, _, _ := mailer.stats(); count != 3 {
		t.Fatalf("send attempts %d", count)
	}
	if loaderCalls != 3 {
		t.Fatalf("loader calls %d", loaderCalls)
	}
}

func TestMailerRejectsInvalidConfiguration(t *testing.T) {
	valid := SMTPConfig{
		Host: "smtp.example.com", Port: 587, From: "gateway@example.com",
		To: []string{"ops@example.com"}, UseTLS: true,
	}
	cases := map[string]func(*SMTPConfig){
		"empty host":       func(c *SMTPConfig) { c.Host = "" },
		"host with port":   func(c *SMTPConfig) { c.Host = "smtp.example.com:587" },
		"bad port":         func(c *SMTPConfig) { c.Port = 0 },
		"bad from":         func(c *SMTPConfig) { c.From = "not-an-address" },
		"no recipients":    func(c *SMTPConfig) { c.To = nil },
		"bad recipient":    func(c *SMTPConfig) { c.To = []string{"bad"} },
		"user no password": func(c *SMTPConfig) { c.Username = "u" },
		"password no user": func(c *SMTPConfig) { c.Password = "p" },
		"credentials no TLS": func(c *SMTPConfig) {
			c.Username, c.Password, c.UseTLS = "u", "p", false
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := valid
			cfg.To = append([]string(nil), valid.To...)
			mutate(&cfg)
			if err := NewMailer(cfg).Send(context.Background(), "subject", "body"); err == nil {
				t.Fatal("expected configuration error")
			}
		})
	}
}

func TestSMTPMailerSendsPlainText(t *testing.T) {
	host, port, messages := startSMTPServer(t)
	mailer := NewMailer(SMTPConfig{
		Host: host, Port: port, From: "gateway@example.com",
		To: []string{"ops@example.com"}, UseTLS: false,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := mailer.Send(ctx, "[DLP] block - aws", "rule: aws\nemployee: alice\n"); err != nil {
		t.Fatal(err)
	}

	select {
	case message := <-messages:
		for _, want := range []string{"Subject: [DLP] block - aws", "rule: aws", "employee: alice"} {
			if !strings.Contains(message, want) {
				t.Fatalf("message missing %q:\n%s", want, message)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("SMTP server did not receive the message")
	}
}

func TestLoadSMTPDecryptsPassword(t *testing.T) {
	cipher := testAlertCipher(t)
	sealed, err := cipher.Seal([]byte("smtp-secret"), smtpPasswordAAD)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{
		"host": "smtp.example.com", "port": 587, "username": "alice",
		"password_enc": base64.StdEncoding.EncodeToString(sealed),
		"from":         "gateway@example.com", "to": []string{"ops@example.com"}, "use_tls": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	settings := &fakeSettings{values: map[string]json.RawMessage{"smtp": raw}}
	cfg, err := LoadSMTP(context.Background(), settings, cipher)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Host != "smtp.example.com" || cfg.Port != 587 || cfg.Username != "alice" ||
		cfg.Password != "smtp-secret" || cfg.From != "gateway@example.com" ||
		len(cfg.To) != 1 || cfg.To[0] != "ops@example.com" || !cfg.UseTLS {
		t.Fatalf("config %+v", cfg)
	}
}

func TestLoadSMTPErrors(t *testing.T) {
	cipher := testAlertCipher(t)
	ctx := context.Background()

	if _, err := LoadSMTP(ctx, &fakeSettings{values: map[string]json.RawMessage{}}, cipher); !errors.Is(err, ErrSMTPNotConfigured) {
		t.Fatalf("not found: %v", err)
	}
	badJSON := &fakeSettings{values: map[string]json.RawMessage{"smtp": json.RawMessage(`{`)}}
	if _, err := LoadSMTP(ctx, badJSON, cipher); err == nil {
		t.Fatal("expected invalid JSON error")
	}
	badEncoding := &fakeSettings{values: map[string]json.RawMessage{
		"smtp": json.RawMessage(`{"password_enc":"not-base64!"}`),
	}}
	if _, err := LoadSMTP(ctx, badEncoding, cipher); err == nil {
		t.Fatal("expected invalid password encoding error")
	}
	otherCipher, err := crypto.NewCipher(bytes.Repeat([]byte{0x11}, 32), nil)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := otherCipher.Seal([]byte("wrong-key"), smtpPasswordAAD)
	if err != nil {
		t.Fatal(err)
	}
	wrongKey, err := json.Marshal(map[string]any{
		"host": "smtp.example.com", "port": 587,
		"password_enc": base64.StdEncoding.EncodeToString(sealed),
	})
	if err != nil {
		t.Fatal(err)
	}
	settings := &fakeSettings{values: map[string]json.RawMessage{"smtp": wrongKey}}
	if _, err := LoadSMTP(ctx, settings, cipher); err == nil || strings.Contains(err.Error(), "wrong-key") {
		t.Fatalf("wrong key error %v", err)
	}
}

func testAlertCipher(t *testing.T) *crypto.Cipher {
	t.Helper()
	cipher, err := crypto.NewCipher(bytes.Repeat([]byte{0x22}, 32), nil)
	if err != nil {
		t.Fatal(err)
	}
	return cipher
}

type fakeSettings struct {
	values map[string]json.RawMessage
	err    error
}

func (s *fakeSettings) Get(_ context.Context, key string) (json.RawMessage, error) {
	if s.err != nil {
		return nil, s.err
	}
	value, ok := s.values[key]
	if !ok {
		return nil, store.ErrNotFound
	}
	return value, nil
}

func (s *fakeSettings) Set(_ context.Context, key string, value json.RawMessage) error {
	if s.err != nil {
		return s.err
	}
	s.values[key] = value
	return nil
}

func startSMTPServer(t *testing.T) (string, int, <-chan string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	messages := make(chan string, 1)
	host, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	var port int
	if _, err := fmt.Sscanf(portText, "%d", &port); err != nil {
		t.Fatal(err)
	}

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		writer := bufio.NewWriter(conn)
		write := func(line string) {
			_, _ = writer.WriteString(line + "\r\n")
			_ = writer.Flush()
		}
		write("220 localhost ESMTP")
		inData := false
		var data strings.Builder
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if inData {
				if line == "." {
					messages <- data.String()
					write("250 OK")
					inData = false
					continue
				}
				data.WriteString(line)
				data.WriteString("\n")
				continue
			}
			upper := strings.ToUpper(line)
			switch {
			case strings.HasPrefix(upper, "EHLO"):
				write("250-localhost")
				write("250 8BITMIME")
			case strings.HasPrefix(upper, "HELO"):
				write("250 OK")
			case strings.HasPrefix(upper, "MAIL FROM"), strings.HasPrefix(upper, "RCPT TO"):
				write("250 OK")
			case upper == "DATA":
				write("354 End data with <CR><LF>.<CR><LF>")
				inData = true
			case upper == "QUIT":
				write("221 Bye")
			case upper == "RSET", upper == "NOOP":
				write("250 OK")
			default:
				write("250 OK")
			}
		}
	}()
	return host, port, messages
}
