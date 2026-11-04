package adminapi

import (
	cryptorand "crypto/rand"
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/company/sluice/internal/crypto"
	"github.com/company/sluice/internal/store"
)

const (
	initialPasswordLength = 16
	// Ambiguous characters (0/O, 1/I/l) are omitted so administrators can
	// read the one-time password aloud when handing it over.
	passwordAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789!@#$%^&*"
)

// PortalAccountHandler provisions and resets employee portal credentials.
type PortalAccountHandler struct {
	accounts       store.EmployeeAccountStore
	employees      store.EmployeeStore
	admins         store.AdminStore
	trustedProxies []string
}

// NewPortalAccountHandler creates the account provisioning handler.
func NewPortalAccountHandler(accounts store.EmployeeAccountStore, employees store.EmployeeStore, admins store.AdminStore, trustedProxies []string) *PortalAccountHandler {
	return &PortalAccountHandler{
		accounts:       accounts,
		employees:      employees,
		admins:         admins,
		trustedProxies: append([]string(nil), trustedProxies...),
	}
}

// Get handles GET /admin/api/employees/:id/portal-account.
func (h *PortalAccountHandler) Get(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	account, err := h.accounts.GetAccount(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			OK(c, http.StatusOK, gin.H{"exists": false})
			return
		}
		InternalError(c)
		return
	}
	OK(c, http.StatusOK, gin.H{
		"exists":               true,
		"must_change_password": account.MustChangePassword,
		"last_login_at":        account.LastLoginAt,
		"created_at":           account.CreatedAt,
		"updated_at":           account.UpdatedAt,
	})
}

// Create handles POST /admin/api/employees/:id/portal-account. The initial
// password is returned exactly once and never logged.
func (h *PortalAccountHandler) Create(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	if _, err := h.employees.Get(ctx, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			Fail(c, http.StatusNotFound, "not_found", "employee not found")
			return
		}
		InternalError(c)
		return
	}

	password, err := randomPassword(initialPasswordLength)
	if err != nil {
		InternalError(c)
		return
	}
	hash, err := crypto.HashPassword(password)
	if err != nil {
		InternalError(c)
		return
	}
	account := &store.EmployeeAccount{
		EmployeeID:         id,
		PasswordHash:       hash,
		MustChangePassword: true,
	}
	if err := h.accounts.CreateAccount(ctx, account); err != nil {
		if errors.Is(err, store.ErrConflict) {
			Fail(c, http.StatusConflict, "account_exists", "portal account already exists")
			return
		}
		writeStoreError(c, err, "employee not found", "portal account already exists", "employee not found")
		return
	}

	_ = recordAdminAudit(c, h.admins, h.trustedProxies, "create_portal_account", "employee", id.String(), nil)
	noStore(c)
	OK(c, http.StatusCreated, gin.H{
		"employee_id":      id.String(),
		"initial_password": password,
	})
}

// ResetPassword handles POST /admin/api/employees/:id/portal-account/reset-password.
func (h *PortalAccountHandler) ResetPassword(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	password, err := randomPassword(initialPasswordLength)
	if err != nil {
		InternalError(c)
		return
	}
	hash, err := crypto.HashPassword(password)
	if err != nil {
		InternalError(c)
		return
	}
	// Field-scoped update: a full-row write here could resurrect a password
	// or failure counter read before a concurrent login attempt.
	if err := h.accounts.ReplacePassword(ctx, id, hash, true); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			Fail(c, http.StatusNotFound, "not_found", "portal account not found")
			return
		}
		InternalError(c)
		return
	}
	// A reset must invalidate every existing portal session.
	if err := h.accounts.DeleteSessionsByEmployeeID(ctx, id); err != nil {
		InternalError(c)
		return
	}

	_ = recordAdminAudit(c, h.admins, h.trustedProxies, "reset_portal_password", "employee", id.String(), nil)
	noStore(c)
	OK(c, http.StatusOK, gin.H{
		"employee_id":      id.String(),
		"initial_password": password,
	})
}

// Delete handles DELETE /admin/api/employees/:id/portal-account.
func (h *PortalAccountHandler) Delete(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.accounts.DeleteAccount(c.Request.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			Fail(c, http.StatusNotFound, "not_found", "portal account not found")
			return
		}
		InternalError(c)
		return
	}
	_ = recordAdminAudit(c, h.admins, h.trustedProxies, "disable_portal_account", "employee", id.String(), nil)
	OK(c, http.StatusOK, gin.H{"disabled": true})
}

// randomPassword generates a printable one-time password using rejection
// sampling to avoid modulo bias.
func randomPassword(length int) (string, error) {
	if length <= 0 {
		length = initialPasswordLength
	}
	alphabetLen := len(passwordAlphabet)
	limit := 256 - (256 % alphabetLen)
	out := make([]byte, 0, length)
	buf := make([]byte, 32)
	for len(out) < length {
		if _, err := io.ReadFull(cryptorand.Reader, buf); err != nil {
			return "", err
		}
		for _, b := range buf {
			if int(b) >= limit {
				continue
			}
			out = append(out, passwordAlphabet[int(b)%alphabetLen])
			if len(out) == length {
				break
			}
		}
	}
	return string(out), nil
}

func noStore(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
}
