package auth

import (
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/company/api-key/internal/crypto"
	"github.com/company/api-key/internal/store"
)

var (
	ErrMissingToken     = errors.New("auth: missing token")
	ErrInvalidToken     = errors.New("auth: invalid token")
	ErrTokenExpired     = errors.New("auth: token expired")
	ErrTokenDisabled    = errors.New("auth: token disabled")
	ErrIPNotAllowed     = errors.New("auth: client IP not allowed")
	ErrEmployeeDisabled = errors.New("auth: employee disabled")
)

// Principal is an authenticated token and its employee.
type Principal struct {
	Token    store.Token
	Employee store.Employee
}

// PrincipalCacheTTL is how long a valid principal is cached.
const PrincipalCacheTTL = 30 * time.Second

// Service authenticates company tokens on gateway requests.
type Service struct {
	tokens    store.TokenStore
	employees store.EmployeeStore
	cache     Cache
	trusted   []net.IPNet
	clock     func() time.Time
}

// New creates a token authentication service. cache may be nil, in which case
// an in-process cache is used.
func New(tokens store.TokenStore, employees store.EmployeeStore, cache Cache, trustedProxies []string, clock func() time.Time) *Service {
	if cache == nil {
		cache = NewMemoryCache()
	}
	if clock == nil {
		clock = time.Now
	}
	return &Service{
		tokens:    tokens,
		employees: employees,
		cache:     cache,
		trusted:   parseTrusted(trustedProxies),
		clock:     clock,
	}
}

// Authenticate resolves the request's company token to a principal.
func (s *Service) Authenticate(r *http.Request) (*Principal, error) {
	plain, err := extractToken(r)
	if err != nil {
		return nil, err
	}
	hash := crypto.HashToken(plain)
	ip := clientIP(r, s.trusted)

	if p, err := s.cache.Get(r.Context(), hash); err == nil && p != nil {
		if err := s.validate(p, ip); err != nil {
			return nil, err
		}
		return p, nil
	}

	ctx := r.Context()
	tok, err := s.tokens.FindByHash(ctx, hash)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrInvalidToken
		}
		return nil, err
	}
	emp, err := s.employees.Get(ctx, tok.EmployeeID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrInvalidToken
		}
		return nil, err
	}

	p := &Principal{Token: *tok, Employee: *emp}
	if err := s.validate(p, ip); err != nil {
		return nil, err
	}
	// Cache failures must never reject an otherwise valid request.
	_ = s.cache.Set(ctx, hash, p, PrincipalCacheTTL)
	return p, nil
}

func (s *Service) validate(p *Principal, ip string) error {
	if p.Token.Status != 1 {
		return ErrTokenDisabled
	}
	if p.Employee.Status != 1 {
		return ErrEmployeeDisabled
	}
	if p.Token.ExpiresAt != nil && !s.clock().Before(*p.Token.ExpiresAt) {
		return ErrTokenExpired
	}
	if !ipAllowed(p.Token.IPAllowlist, ip) {
		return ErrIPNotAllowed
	}
	return nil
}

func extractToken(r *http.Request) (string, error) {
	authorization := strings.TrimSpace(r.Header.Get("Authorization"))
	xAPIKey := strings.TrimSpace(r.Header.Get("x-api-key"))

	var bearer string
	if authorization != "" {
		parts := strings.Fields(authorization)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			return "", ErrInvalidToken
		}
		bearer = parts[1]
	}
	if bearer != "" && xAPIKey != "" && bearer != xAPIKey {
		return "", ErrInvalidToken
	}

	token := bearer
	if token == "" {
		token = xAPIKey
	}
	if token == "" {
		return "", ErrMissingToken
	}
	if !crypto.IsCompanyToken(token) {
		return "", ErrInvalidToken
	}
	return token, nil
}

func ipAllowed(allowlist []string, clientIP string) bool {
	if len(allowlist) == 0 {
		return true
	}
	ip := net.ParseIP(clientIP)
	if ip == nil {
		return false
	}
	for _, entry := range allowlist {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if strings.Contains(entry, "/") {
			_, network, err := net.ParseCIDR(entry)
			if err == nil && network.Contains(ip) {
				return true
			}
			continue
		}
		allowed := net.ParseIP(entry)
		if allowed != nil && allowed.Equal(ip) {
			return true
		}
	}
	return false
}

// ClientIP returns the client address, trusting forwarded headers only when
// the direct peer is in trusted.
func ClientIP(r *http.Request, trusted []string) string {
	return clientIP(r, parseTrusted(trusted))
}

func clientIP(r *http.Request, trusted []net.IPNet) string {
	remote := parseRemoteIP(r.RemoteAddr)
	if remote == nil {
		return ""
	}
	if !isTrusted(remote, trusted) {
		return remote.String()
	}

	forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For"))
	if forwarded == "" {
		if realIP := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Real-IP"))); realIP != nil {
			return realIP.String()
		}
		return remote.String()
	}

	parts := strings.Split(forwarded, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		ip := net.ParseIP(strings.TrimSpace(parts[i]))
		if ip == nil || isTrusted(ip, trusted) {
			continue
		}
		return ip.String()
	}
	return remote.String()
}

func parseRemoteIP(remoteAddr string) net.IP {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	host = strings.Trim(host, "[]")
	if i := strings.IndexByte(host, '%'); i >= 0 {
		host = host[:i]
	}
	return net.ParseIP(host)
}

func parseTrusted(entries []string) []net.IPNet {
	out := make([]net.IPNet, 0, len(entries))
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if strings.Contains(entry, "/") {
			_, network, err := net.ParseCIDR(entry)
			if err == nil {
				out = append(out, *network)
			}
			continue
		}
		ip := net.ParseIP(entry)
		if ip == nil {
			continue
		}
		if v4 := ip.To4(); v4 != nil {
			out = append(out, net.IPNet{IP: v4, Mask: net.CIDRMask(32, 32)})
		} else {
			out = append(out, net.IPNet{IP: ip, Mask: net.CIDRMask(128, 128)})
		}
	}
	return out
}

func isTrusted(ip net.IP, networks []net.IPNet) bool {
	for i := range networks {
		if networks[i].Contains(ip) {
			return true
		}
	}
	return false
}
