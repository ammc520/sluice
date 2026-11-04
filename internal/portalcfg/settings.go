// Package portalcfg owns the employee portal policy stored in the settings
// table. Both the portal API and the admin settings endpoint use these types
// so validation cannot drift between them.
package portalcfg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/company/api-key/internal/store"
)

const SettingsKey = "portal"

// Quota is the default token allowance granted through the portal.
type Quota struct {
	Type        string `json:"type"`
	Period      string `json:"period"`
	Total       int64  `json:"total"`
	RPM         int    `json:"rpm"`
	ExpiresDays int    `json:"expires_days"`
}

// Settings is the administrator-controlled portal policy.
type Settings struct {
	Enabled         bool  `json:"enabled"`
	DefaultQuota    Quota `json:"default_quota"`
	MaxRequestTotal int64 `json:"max_request_total"`
	RequestTTLDays  int   `json:"request_ttl_days"`
	ClaimTTLHours   int   `json:"claim_ttl_hours"`
	UsageMaxDays    int   `json:"usage_max_days"`

	// SelfServiceEnabled lets employees create their own keys. The remaining
	// switches gate rotation, revocation, and automatic quota approval.
	SelfServiceEnabled bool `json:"self_service_enabled"`
	MaxActiveTokens    int  `json:"max_active_tokens"`
	AllowSelfRotate    bool `json:"allow_self_rotate"`
	AllowSelfRevoke    bool `json:"allow_self_revoke"`
	AutoApproveQuota   bool `json:"auto_approve_quota"`
}

// Defaults is the shipping policy. The portal starts disabled so a migration
// never exposes a new login surface before an administrator enables it.
func Defaults() Settings {
	return Settings{
		Enabled: false,
		DefaultQuota: Quota{
			Type:        "tokens",
			Period:      "monthly",
			Total:       1_000_000,
			RPM:         60,
			ExpiresDays: 180,
		},
		MaxRequestTotal:    5_000_000,
		RequestTTLDays:     14,
		ClaimTTLHours:      72,
		UsageMaxDays:       90,
		SelfServiceEnabled: true,
		MaxActiveTokens:    5,
		AllowSelfRotate:    true,
		AllowSelfRevoke:    true,
	}
}

// UnmarshalJSON starts from Defaults so an absent boolean keeps its shipping
// default instead of silently becoming false. For example, a settings row
// written before self-service existed must not disable key creation, while an
// explicit "self_service_enabled": false is preserved.
func (s *Settings) UnmarshalJSON(data []byte) error {
	type alias Settings
	base := Defaults()
	var raw struct {
		*alias
		SelfServiceEnabled *bool `json:"self_service_enabled"`
		AllowSelfRotate    *bool `json:"allow_self_rotate"`
		AllowSelfRevoke    *bool `json:"allow_self_revoke"`
		AutoApproveQuota   *bool `json:"auto_approve_quota"`
		MaxActiveTokens    *int  `json:"max_active_tokens"`
	}
	raw.alias = (*alias)(&base)
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw.SelfServiceEnabled != nil {
		base.SelfServiceEnabled = *raw.SelfServiceEnabled
	}
	if raw.AllowSelfRotate != nil {
		base.AllowSelfRotate = *raw.AllowSelfRotate
	}
	if raw.AllowSelfRevoke != nil {
		base.AllowSelfRevoke = *raw.AllowSelfRevoke
	}
	if raw.AutoApproveQuota != nil {
		base.AutoApproveQuota = *raw.AutoApproveQuota
	}
	if raw.MaxActiveTokens != nil {
		base.MaxActiveTokens = *raw.MaxActiveTokens
	}
	*s = Settings(base)
	return nil
}

// Normalize fills empty fields with defaults. It is used after decoding a
// stored value so adding a field does not require rewriting existing rows.
func (s *Settings) Normalize() {
	def := Defaults()
	if s.DefaultQuota.Type == "" {
		s.DefaultQuota.Type = def.DefaultQuota.Type
	}
	if s.DefaultQuota.Period == "" {
		s.DefaultQuota.Period = def.DefaultQuota.Period
	}
	if s.DefaultQuota.RPM <= 0 {
		s.DefaultQuota.RPM = def.DefaultQuota.RPM
	}
	if s.DefaultQuota.ExpiresDays <= 0 {
		s.DefaultQuota.ExpiresDays = def.DefaultQuota.ExpiresDays
	}
	if s.DefaultQuota.Total < 0 {
		s.DefaultQuota.Total = 0
	}
	if s.MaxRequestTotal <= 0 {
		s.MaxRequestTotal = def.MaxRequestTotal
	}
	if s.RequestTTLDays <= 0 {
		s.RequestTTLDays = def.RequestTTLDays
	}
	if s.ClaimTTLHours <= 0 {
		s.ClaimTTLHours = def.ClaimTTLHours
	}
	if s.UsageMaxDays <= 0 {
		s.UsageMaxDays = def.UsageMaxDays
	}
	if s.MaxActiveTokens <= 0 {
		s.MaxActiveTokens = def.MaxActiveTokens
	}
}

// Validate rejects policy values that the portal cannot enforce safely.
func (s Settings) Validate() error {
	switch s.DefaultQuota.Type {
	case "requests", "tokens":
	default:
		return errors.New("default_quota.type must be requests or tokens")
	}
	switch s.DefaultQuota.Period {
	case "daily", "monthly", "total":
	default:
		return errors.New("default_quota.period must be daily, monthly, or total")
	}
	if s.DefaultQuota.Total < 0 {
		return errors.New("default_quota.total must be non-negative")
	}
	if s.DefaultQuota.RPM < 1 || s.DefaultQuota.RPM > 100000 {
		return errors.New("default_quota.rpm must be between 1 and 100000")
	}
	if s.DefaultQuota.ExpiresDays < 1 || s.DefaultQuota.ExpiresDays > 3650 {
		return errors.New("default_quota.expires_days must be between 1 and 3650")
	}
	if s.MaxRequestTotal < 1 || s.MaxRequestTotal > 1_000_000_000 {
		return errors.New("max_request_total must be between 1 and 1000000000")
	}
	if s.RequestTTLDays < 1 || s.RequestTTLDays > 90 {
		return errors.New("request_ttl_days must be between 1 and 90")
	}
	if s.ClaimTTLHours < 1 || s.ClaimTTLHours > 720 {
		return errors.New("claim_ttl_hours must be between 1 and 720")
	}
	if s.UsageMaxDays < 1 || s.UsageMaxDays > 365 {
		return errors.New("usage_max_days must be between 1 and 365")
	}
	if s.MaxActiveTokens < 1 || s.MaxActiveTokens > 50 {
		return errors.New("max_active_tokens must be between 1 and 50")
	}
	return nil
}

// Load reads the portal policy, falling back to defaults when the row has not
// been written yet.
func Load(ctx context.Context, settings store.SettingsStore) (Settings, error) {
	out := Defaults()
	raw, err := settings.Get(ctx, SettingsKey)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return out, nil
		}
		return out, fmt.Errorf("portalcfg: read settings: %w", err)
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return Defaults(), fmt.Errorf("portalcfg: decode settings: %w", err)
	}
	out.Normalize()
	return out, nil
}

// Provider caches the policy for a short window so the portal middleware does
// not hit PostgreSQL on every request. Administrator updates call Invalidate
// for an immediate effect; the TTL is only a safety net.
type Provider struct {
	settings store.SettingsStore
	ttl      time.Duration
	now      func() time.Time
	logger   *slog.Logger

	mu       sync.RWMutex
	current  Settings
	loadedAt time.Time
}

// NewProvider creates a cached policy reader.
func NewProvider(settings store.SettingsStore, ttl time.Duration, clock func() time.Time, logger *slog.Logger) *Provider {
	if clock == nil {
		clock = time.Now
	}
	if ttl <= 0 {
		ttl = 10 * time.Second
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Provider{
		settings: settings,
		ttl:      ttl,
		now:      clock,
		logger:   logger,
		current:  Defaults(),
	}
}

// Get returns the cached policy, refreshing it when stale. A read failure keeps
// the last known policy so a transient database error does not silently open
// or close the portal; authentication itself will still fail closed.
func (p *Provider) Get(ctx context.Context) Settings {
	p.mu.RLock()
	current, loadedAt := p.current, p.loadedAt
	p.mu.RUnlock()
	if !loadedAt.IsZero() && p.now().Sub(loadedAt) < p.ttl {
		return current
	}

	next, err := Load(ctx, p.settings)
	if err != nil {
		p.logger.Warn("portal settings refresh failed; using cached policy", "error", err)
		return current
	}
	p.mu.Lock()
	p.current = next
	p.loadedAt = p.now()
	p.mu.Unlock()
	return next
}

// Invalidate forces the next Get to reload.
func (p *Provider) Invalidate() {
	p.mu.Lock()
	p.loadedAt = time.Time{}
	p.mu.Unlock()
}
