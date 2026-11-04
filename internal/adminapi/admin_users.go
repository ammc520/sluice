package adminapi

import (
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/company/sluice/internal/crypto"
	"github.com/company/sluice/internal/store"
)

const (
	minAdminPasswordLength = 12
	maxAdminPasswordLength = 128
)

var adminUsernamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{3,64}$`)

// AdminUsersHandler serves administrator account management endpoints. Only
// super administrators may use these routes.
type AdminUsersHandler struct {
	admins         store.AdminStore
	trustedProxies []string
}

// NewAdminUsersHandler creates the administrator account handler.
func NewAdminUsersHandler(admins store.AdminStore, trustedProxies []string) *AdminUsersHandler {
	return &AdminUsersHandler{
		admins:         admins,
		trustedProxies: append([]string(nil), trustedProxies...),
	}
}

func validAdminRole(role string) bool {
	switch role {
	case "super_admin", "operator", "auditor":
		return true
	default:
		return false
	}
}

func validateAdminUsername(username string) string {
	if !adminUsernamePattern.MatchString(username) {
		return "username must be 3-64 characters using letters, digits, dot, underscore, or hyphen"
	}
	return ""
}

func validateAdminPassword(password string) string {
	length := utf8.RuneCountInString(password)
	if length < minAdminPasswordLength {
		return "password must be at least 12 characters"
	}
	if length > maxAdminPasswordLength {
		return "password is too long"
	}
	return ""
}

type adminUserCreatePayload struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Role     string `json:"role"`
	Status   *int16 `json:"status"`
}

type adminUserUpdatePayload struct {
	Username string `json:"username"`
	Role     string `json:"role"`
	Status   *int16 `json:"status"`
}

type resetPasswordPayload struct {
	Password string `json:"password"`
}

// List handles GET /admin/api/admin-users.
func (h *AdminUsersHandler) List(c *gin.Context) {
	if !requireSuperAdmin(c) {
		return
	}
	users, err := h.admins.List(c.Request.Context())
	if err != nil {
		InternalError(c)
		return
	}
	items := make([]adminUserDTO, 0, len(users))
	for i := range users {
		items = append(items, toAdminUserDTO(&users[i]))
	}
	OK(c, http.StatusOK, gin.H{"items": items})
}

// Create handles POST /admin/api/admin-users.
func (h *AdminUsersHandler) Create(c *gin.Context) {
	if !requireSuperAdmin(c) {
		return
	}
	var payload adminUserCreatePayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		BadRequest(c, "invalid JSON body")
		return
	}
	payload.Username = strings.TrimSpace(payload.Username)
	payload.Role = strings.TrimSpace(payload.Role)
	if msg := validateAdminUsername(payload.Username); msg != "" {
		BadRequest(c, msg)
		return
	}
	if msg := validateAdminPassword(payload.Password); msg != "" {
		BadRequest(c, msg)
		return
	}
	if payload.Role == "" {
		BadRequest(c, "role is required")
		return
	}
	if !validAdminRole(payload.Role) {
		BadRequest(c, "role must be super_admin, operator, or auditor")
		return
	}
	status := int16(1)
	if payload.Status != nil {
		status = *payload.Status
	}
	if status != 0 && status != 1 {
		BadRequest(c, "status must be 0 or 1")
		return
	}
	hash, err := crypto.HashPassword(payload.Password)
	if err != nil {
		InternalError(c)
		return
	}
	user := &store.AdminUser{
		ID:           uuid.New(),
		Username:     payload.Username,
		PasswordHash: hash,
		Role:         payload.Role,
		Status:       status,
	}
	if err := h.admins.Create(c.Request.Context(), user); err != nil {
		writeStoreError(c, err, "administrator not found", "username already exists", "invalid administrator reference")
		return
	}
	if err := recordAdminAudit(c, h.admins, h.trustedProxies, "create_admin_user", "admin_user", user.ID.String(), map[string]any{
		"username": user.Username,
		"role":     user.Role,
		"status":   user.Status,
	}); err != nil {
		InternalError(c)
		return
	}
	OK(c, http.StatusCreated, toAdminUserDTO(user))
}

// Update handles PUT /admin/api/admin-users/:id. Omitted fields keep their
// current values.
func (h *AdminUsersHandler) Update(c *gin.Context) {
	if !requireSuperAdmin(c) {
		return
	}
	id, ok := pathID(c)
	if !ok {
		return
	}
	var payload adminUserUpdatePayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		BadRequest(c, "invalid JSON body")
		return
	}
	payload.Username = strings.TrimSpace(payload.Username)
	payload.Role = strings.TrimSpace(payload.Role)

	ctx := c.Request.Context()
	user, err := h.admins.Get(ctx, id)
	if err != nil {
		writeStoreError(c, err, "administrator not found", "username already exists", "invalid administrator reference")
		return
	}
	wasEnabled := user.Status == 1
	if payload.Username != "" {
		if msg := validateAdminUsername(payload.Username); msg != "" {
			BadRequest(c, msg)
			return
		}
		user.Username = payload.Username
	}
	if payload.Role != "" {
		if !validAdminRole(payload.Role) {
			BadRequest(c, "role must be super_admin, operator, or auditor")
			return
		}
		user.Role = payload.Role
	}
	if payload.Status != nil {
		if *payload.Status != 0 && *payload.Status != 1 {
			BadRequest(c, "status must be 0 or 1")
			return
		}
		user.Status = *payload.Status
	}

	if err := h.admins.Update(ctx, user); err != nil {
		writeStoreError(c, err, "administrator not found", "username already exists", "invalid administrator reference")
		return
	}
	// Disabling an account must invalidate its existing sessions so a later
	// re-enable cannot revive stale credentials. Do this only after the store
	// accepts the update: a rejected edit (for example, removing the last
	// super administrator) must not log the caller out.
	if wasEnabled && user.Status == 0 {
		if err := h.admins.DeleteSessionsByAdminID(ctx, user.ID); err != nil {
			InternalError(c)
			return
		}
	}
	if err := recordAdminAudit(c, h.admins, h.trustedProxies, "update_admin_user", "admin_user", user.ID.String(), map[string]any{
		"username": user.Username,
		"role":     user.Role,
		"status":   user.Status,
	}); err != nil {
		InternalError(c)
		return
	}
	OK(c, http.StatusOK, toAdminUserDTO(user))
}

// ResetPassword handles POST /admin/api/admin-users/:id/reset-password.
func (h *AdminUsersHandler) ResetPassword(c *gin.Context) {
	if !requireSuperAdmin(c) {
		return
	}
	id, ok := pathID(c)
	if !ok {
		return
	}
	var payload resetPasswordPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		BadRequest(c, "invalid JSON body")
		return
	}
	if msg := validateAdminPassword(payload.Password); msg != "" {
		BadRequest(c, msg)
		return
	}
	ctx := c.Request.Context()
	user, err := h.admins.Get(ctx, id)
	if err != nil {
		writeStoreError(c, err, "administrator not found", "username already exists", "invalid administrator reference")
		return
	}
	hash, err := crypto.HashPassword(payload.Password)
	if err != nil {
		InternalError(c)
		return
	}
	// Revoke every existing session before rotating the credential. Doing
	// this first means a failed password write still fails closed.
	if err := h.admins.DeleteSessionsByAdminID(ctx, user.ID); err != nil {
		InternalError(c)
		return
	}
	user.PasswordHash = hash
	if err := h.admins.Update(ctx, user); err != nil {
		writeStoreError(c, err, "administrator not found", "username already exists", "invalid administrator reference")
		return
	}
	if err := recordAdminAudit(c, h.admins, h.trustedProxies, "reset_admin_password", "admin_user", user.ID.String(), map[string]any{
		"username": user.Username,
	}); err != nil {
		InternalError(c)
		return
	}
	OK(c, http.StatusOK, gin.H{"reset": true})
}
