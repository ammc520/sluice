package alert

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	gomail "github.com/wneessen/go-mail"
)

const (
	smtpTimeout          = 10 * time.Second
	defaultAlertQueue    = 100
	defaultDedupWindow   = 5 * time.Minute
	defaultRetryDelayOne = time.Second
	defaultRetryDelayTwo = 5 * time.Second
	defaultRetryDelay3   = 30 * time.Second
)

// SMTPConfig is the runtime SMTP configuration. Password is plaintext only for
// the lifetime of a single send; LoadSMTP decrypts it on demand.
type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	To       []string
	UseTLS   bool
}

// Mailer sends a plain-text email.
type Mailer interface {
	Send(ctx context.Context, subject, body string) error
}

// NewMailer builds an SMTP mailer. Invalid configuration is reported by Send
// so callers can store the mailer without a second error path.
func NewMailer(cfg SMTPConfig) Mailer {
	if err := validateSMTPConfig(cfg); err != nil {
		return &smtpMailer{cfg: cfg, err: err}
	}
	return &smtpMailer{cfg: cfg}
}

type smtpMailer struct {
	cfg SMTPConfig
	err error
}

// ValidateSMTPConfig checks a configuration without attempting a connection.
// Administrative settings endpoints use it to reject invalid saves early.
func ValidateSMTPConfig(cfg SMTPConfig) error { return validateSMTPConfig(cfg) }

func validateSMTPConfig(cfg SMTPConfig) error {
	if strings.TrimSpace(cfg.Host) == "" || strings.ContainsAny(cfg.Host, " \t\r\n/:") {
		return errors.New("alert: SMTP host is invalid")
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return errors.New("alert: SMTP port is invalid")
	}
	if _, err := mail.ParseAddress(cfg.From); err != nil {
		return errors.New("alert: SMTP from address is invalid")
	}
	if len(cfg.To) == 0 {
		return errors.New("alert: SMTP recipient list is empty")
	}
	for _, recipient := range cfg.To {
		if _, err := mail.ParseAddress(recipient); err != nil {
			return errors.New("alert: SMTP recipient address is invalid")
		}
	}
	if cfg.Username != "" && cfg.Password == "" {
		return errors.New("alert: SMTP password is missing")
	}
	if cfg.Password != "" && cfg.Username == "" {
		return errors.New("alert: SMTP username is missing")
	}
	if cfg.Username != "" && !cfg.UseTLS {
		return errors.New("alert: SMTP credentials require TLS")
	}
	return nil
}

func (m *smtpMailer) Send(ctx context.Context, subject, body string) error {
	if m.err != nil {
		return m.err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	message := gomail.NewMsg()
	if err := message.From(m.cfg.From); err != nil {
		return fmt.Errorf("alert: build from address: %w", err)
	}
	if err := message.To(m.cfg.To...); err != nil {
		return fmt.Errorf("alert: build recipients: %w", err)
	}
	message.Subject(sanitizeHeader(subject))
	message.SetBodyString(gomail.TypeTextPlain, body)

	options := []gomail.Option{
		gomail.WithPort(m.cfg.Port),
		gomail.WithTimeout(smtpTimeout),
	}
	if m.cfg.UseTLS {
		if m.cfg.Port == 465 {
			options = append(options, gomail.WithSSLPort(false))
		} else {
			options = append(options, gomail.WithTLSPortPolicy(gomail.TLSMandatory))
		}
	} else {
		options = append(options, gomail.WithTLSPortPolicy(gomail.NoTLS))
	}
	if m.cfg.Username != "" {
		options = append(options,
			gomail.WithSMTPAuth(gomail.SMTPAuthPlain),
			gomail.WithUsername(m.cfg.Username),
			gomail.WithPassword(m.cfg.Password),
		)
	} else {
		// go-mail otherwise probes for server-side AUTH even when no
		// credentials are configured, which fails on open relays.
		options = append(options, gomail.WithSMTPAuthCustom(nil))
	}

	client, err := gomail.NewClient(m.cfg.Host, options...)
	if err != nil {
		return fmt.Errorf("alert: build SMTP client: %w", err)
	}
	if err := client.DialAndSendWithContext(ctx, message); err != nil {
		return fmt.Errorf("alert: SMTP send failed: %w", err)
	}
	return nil
}

func sanitizeHeader(value string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(value)
}

// Worker sends DLP alerts asynchronously with per-rule/employee deduplication.
type Worker struct {
	loader      func(context.Context) (Mailer, error)
	dedupWindow time.Duration
	clock       func() time.Time
	logger      *slog.Logger
	retryDelays []time.Duration

	queue chan Alert
	done  chan struct{}

	closeOnce sync.Once
	started   atomic.Bool
	mu        sync.Mutex
	closed    bool
	seen      map[string]time.Time
}

// NewWorker creates an alert worker. loader is called for every delivery so
// SMTP setting changes take effect immediately.
func NewWorker(loader func(context.Context) (Mailer, error), queueSize int, dedupWindow time.Duration, clock func() time.Time) *Worker {
	if queueSize <= 0 {
		queueSize = defaultAlertQueue
	}
	if dedupWindow <= 0 {
		dedupWindow = defaultDedupWindow
	}
	if clock == nil {
		clock = time.Now
	}
	return &Worker{
		loader:      loader,
		dedupWindow: dedupWindow,
		clock:       clock,
		logger:      slog.Default(),
		retryDelays: []time.Duration{defaultRetryDelayOne, defaultRetryDelayTwo, defaultRetryDelay3},
		queue:       make(chan Alert, queueSize),
		done:        make(chan struct{}),
		seen:        make(map[string]time.Time),
	}
}

// Enqueue accepts an alert without blocking and deduplicates it by rule and
// employee within the configured window.
func (w *Worker) Enqueue(ctx context.Context, event Alert) bool {
	if ctx != nil && ctx.Err() != nil {
		return false
	}
	now := w.clock()
	key := event.RuleName + ":" + event.Employee

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return false
	}
	w.pruneLocked(now)
	if last, ok := w.seen[key]; ok && now.Sub(last) < w.dedupWindow {
		return false
	}
	select {
	case w.queue <- event:
		w.seen[key] = now
		return true
	default:
		return false
	}
}

// Start launches the delivery goroutine. It is safe to call more than once.
func (w *Worker) Start(ctx context.Context) {
	if !w.started.CompareAndSwap(false, true) {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	go w.run(ctx)
}

// Close stops accepting alerts, drains the queue, and waits for delivery.
func (w *Worker) Close(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	w.closeOnce.Do(func() {
		w.mu.Lock()
		w.closed = true
		close(w.queue)
		w.mu.Unlock()
		if w.started.CompareAndSwap(false, true) {
			go w.run(context.Background())
		}
	})
	select {
	case <-w.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *Worker) run(ctx context.Context) {
	defer close(w.done)
	for event := range w.queue {
		if ctx.Err() != nil {
			continue
		}
		w.deliver(ctx, event)
	}
}

func (w *Worker) deliver(ctx context.Context, event Alert) {
	subject, body := formatAlert(event, w.clock())
	for attempt := 0; ; attempt++ {
		mailer, err := w.loader(ctx)
		if err == nil && mailer == nil {
			err = errors.New("alert: loader returned a nil mailer")
		}
		if err == nil {
			err = mailer.Send(ctx, subject, body)
		}
		if err == nil {
			return
		}
		w.logger.Warn("DLP alert delivery failed",
			"attempt", attempt+1, "rule", event.RuleName,
			"request_id", event.RequestID, "error", err)
		if attempt >= len(w.retryDelays) || ctx.Err() != nil {
			return
		}
		timer := time.NewTimer(w.retryDelays[attempt])
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		case <-timer.C:
		}
	}
}

func (w *Worker) pruneLocked(now time.Time) {
	for key, seen := range w.seen {
		if now.Sub(seen) >= w.dedupWindow {
			delete(w.seen, key)
		}
	}
}

func formatAlert(event Alert, now time.Time) (string, string) {
	action := sanitizeHeader(event.Action)
	rule := sanitizeHeader(event.RuleName)
	subject := fmt.Sprintf("[DLP] %s - %s", action, rule)

	at := event.At
	if at.IsZero() {
		at = now
	}
	body := fmt.Sprintf(
		"时间: %s\n员工: %s\n规则: %s\n动作: %s\n请求 ID: %s\n",
		at.Format("2006-01-02 15:04:05 -0700"),
		event.Employee, event.RuleName, event.Action, event.RequestID,
	)
	return subject, body
}
