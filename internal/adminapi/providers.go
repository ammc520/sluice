package adminapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/company/api-key/internal/crypto"
	"github.com/company/api-key/internal/netguard"
	"github.com/company/api-key/internal/store"
)

// ProviderHandler serves upstream provider administration endpoints.
type ProviderHandler struct {
	providers    store.ProviderStore
	cipher       *crypto.Cipher
	client       *http.Client
	routeRefresh func(context.Context)
}

// NewProviderHandler creates the provider handler. refresh is called after any
// mutation so the proxy resolver can reload routes and provider settings.
func NewProviderHandler(providers store.ProviderStore, cipher *crypto.Cipher, client *http.Client, refresh func(context.Context)) *ProviderHandler {
	if client == nil {
		client = netguard.NewClient(10*time.Second, 10*time.Second)
	}
	return &ProviderHandler{providers: providers, cipher: cipher, client: client, routeRefresh: refresh}
}

type providerDTO struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Protocol       string    `json:"protocol"`
	BaseURL        string    `json:"base_url"`
	APIKeyLast4    string    `json:"api_key_last4"`
	IsDefault      bool      `json:"is_default"`
	Enabled        bool      `json:"enabled"`
	TimeoutSeconds int       `json:"timeout_seconds"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func (h *ProviderHandler) toDTO(p *store.Provider) providerDTO {
	last4 := ""
	if h.cipher != nil {
		if plain, err := h.cipher.Open(p.APIKeyEnc, providerAAD(p.ID)); err == nil {
			last4 = suffix4(string(plain))
		}
	}
	return providerDTO{
		ID:             p.ID.String(),
		Name:           p.Name,
		Protocol:       p.Protocol,
		BaseURL:        p.BaseURL,
		APIKeyLast4:    last4,
		IsDefault:      p.IsDefault,
		Enabled:        p.Enabled,
		TimeoutSeconds: p.TimeoutSeconds,
		CreatedAt:      p.CreatedAt,
		UpdatedAt:      p.UpdatedAt,
	}
}

func providerAAD(id uuid.UUID) string { return "provider:" + id.String() }

func suffix4(s string) string {
	if len(s) < 4 {
		return ""
	}
	return s[len(s)-4:]
}

type providerPayload struct {
	Name           string `json:"name"`
	Protocol       string `json:"protocol"`
	BaseURL        string `json:"base_url"`
	APIKey         string `json:"api_key"`
	IsDefault      *bool  `json:"is_default"`
	Enabled        *bool  `json:"enabled"`
	TimeoutSeconds *int   `json:"timeout_seconds"`
}

func (p *providerPayload) normalize(requireKey bool) string {
	p.Name = strings.TrimSpace(p.Name)
	p.Protocol = strings.TrimSpace(p.Protocol)
	p.BaseURL = strings.TrimRight(strings.TrimSpace(p.BaseURL), "/")
	p.APIKey = strings.TrimSpace(p.APIKey)

	switch {
	case p.Name == "":
		return "name is required"
	case len(p.Name) > 128:
		return "name is too long"
	case p.Protocol != "openai_compatible" && p.Protocol != "anthropic":
		return "protocol must be openai_compatible or anthropic"
	case requireKey && p.APIKey == "":
		return "api_key is required"
	}

	u, err := url.Parse(p.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "base_url must be a valid http(s) URL"
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "base_url must not contain credentials, query, or fragment"
	}
	if p.TimeoutSeconds != nil && (*p.TimeoutSeconds < 1 || *p.TimeoutSeconds > 600) {
		return "timeout_seconds must be between 1 and 600"
	}
	return ""
}

// List handles GET /admin/api/providers.
func (h *ProviderHandler) List(c *gin.Context) {
	items, err := h.providers.List(c.Request.Context())
	if err != nil {
		InternalError(c)
		return
	}
	out := make([]providerDTO, 0, len(items))
	for i := range items {
		out = append(out, h.toDTO(&items[i]))
	}
	OK(c, http.StatusOK, gin.H{"items": out})
}

// Create handles POST /admin/api/providers.
func (h *ProviderHandler) Create(c *gin.Context) {
	var payload providerPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		BadRequest(c, "invalid JSON body")
		return
	}
	if msg := payload.normalize(true); msg != "" {
		BadRequest(c, msg)
		return
	}
	if h.cipher == nil {
		InternalError(c)
		return
	}

	id := uuid.New()
	enc, err := h.cipher.Seal([]byte(payload.APIKey), providerAAD(id))
	if err != nil {
		InternalError(c)
		return
	}
	enabled := true
	if payload.Enabled != nil {
		enabled = *payload.Enabled
	}
	timeout := 300
	if payload.TimeoutSeconds != nil {
		timeout = *payload.TimeoutSeconds
	}
	p := &store.Provider{
		ID:             id,
		Name:           payload.Name,
		Protocol:       payload.Protocol,
		BaseURL:        payload.BaseURL,
		APIKeyEnc:      enc,
		IsDefault:      payload.IsDefault != nil && *payload.IsDefault,
		Enabled:        enabled,
		TimeoutSeconds: timeout,
	}
	if err := h.providers.Create(c.Request.Context(), p); err != nil {
		writeStoreError(c, err, "provider not found", "provider name already exists", "invalid provider")
		return
	}
	h.refresh(c.Request.Context())
	OK(c, http.StatusCreated, h.toDTO(p))
}

// Update handles PUT /admin/api/providers/:id. An empty api_key keeps the
// existing secret.
func (h *ProviderHandler) Update(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var payload providerPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		BadRequest(c, "invalid JSON body")
		return
	}
	if msg := payload.normalize(false); msg != "" {
		BadRequest(c, msg)
		return
	}

	ctx := c.Request.Context()
	p, err := h.providers.Get(ctx, id)
	if err != nil {
		writeStoreError(c, err, "provider not found", "provider name already exists", "invalid provider")
		return
	}
	// Never repoint a stored credential at a new host without the admin
	// re-entering the key; otherwise an operator could exfiltrate it.
	if payload.BaseURL != p.BaseURL && payload.APIKey == "" {
		BadRequest(c, "api_key is required when changing base_url")
		return
	}
	if payload.APIKey != "" {
		if h.cipher == nil {
			InternalError(c)
			return
		}
		enc, err := h.cipher.Seal([]byte(payload.APIKey), providerAAD(id))
		if err != nil {
			InternalError(c)
			return
		}
		p.APIKeyEnc = enc
	}
	p.Name = payload.Name
	p.Protocol = payload.Protocol
	p.BaseURL = payload.BaseURL
	if payload.IsDefault != nil {
		p.IsDefault = *payload.IsDefault
	}
	if payload.Enabled != nil {
		p.Enabled = *payload.Enabled
	}
	if payload.TimeoutSeconds != nil {
		p.TimeoutSeconds = *payload.TimeoutSeconds
	}
	if err := h.providers.Update(ctx, p); err != nil {
		writeStoreError(c, err, "provider not found", "provider name already exists", "invalid provider")
		return
	}
	h.refresh(ctx)
	OK(c, http.StatusOK, h.toDTO(p))
}

// Delete handles DELETE /admin/api/providers/:id.
func (h *ProviderHandler) Delete(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	err := h.providers.Delete(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrInvalidReference) {
			Fail(c, http.StatusConflict, "conflict", "provider is still referenced by model routes")
			return
		}
		writeStoreError(c, err, "provider not found", "provider name already exists", "invalid provider")
		return
	}
	h.refresh(c.Request.Context())
	OK(c, http.StatusOK, gin.H{"deleted": true})
}

// Test handles POST /admin/api/providers/:id/test.
func (h *ProviderHandler) Test(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	p, err := h.providers.Get(ctx, id)
	if err != nil {
		writeStoreError(c, err, "provider not found", "provider name already exists", "invalid provider")
		return
	}
	if h.cipher == nil {
		InternalError(c)
		return
	}
	plain, err := h.cipher.Open(p.APIKeyEnc, providerAAD(p.ID))
	if err != nil {
		InternalError(c)
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsURL(p.BaseURL), nil)
	if err != nil {
		Fail(c, http.StatusBadRequest, "invalid_request", "invalid base_url")
		return
	}
	if p.Protocol == "anthropic" {
		req.Header.Set("x-api-key", string(plain))
		req.Header.Set("anthropic-version", "2023-06-01")
	} else {
		req.Header.Set("Authorization", "Bearer "+string(plain))
	}

	resp, err := h.client.Do(req)
	if err != nil {
		OK(c, http.StatusOK, gin.H{
			"reachable": false,
			"message":   "connection failed",
		})
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))

	reachable := (resp.StatusCode >= 200 && resp.StatusCode < 300) || resp.StatusCode == http.StatusUnauthorized
	message := ""
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		message = "reachable, but the API key may be invalid"
	case !reachable:
		message = "upstream returned an unexpected status"
	}
	OK(c, http.StatusOK, gin.H{
		"reachable":   reachable,
		"status_code": resp.StatusCode,
		"message":     message,
	})
}

func modelsURL(base string) string {
	base = strings.TrimRight(base, "/")
	if strings.HasSuffix(base, "/v1") {
		return base + "/models"
	}
	return base + "/v1/models"
}

func (h *ProviderHandler) refresh(ctx context.Context) {
	if h.routeRefresh != nil {
		h.routeRefresh(ctx)
	}
}
