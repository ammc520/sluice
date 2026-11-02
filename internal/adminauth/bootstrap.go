package adminauth

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/company/api-key/internal/crypto"
	"github.com/company/api-key/internal/store"
)

// EnsureBootstrapAdmin creates the initial super administrator when the
// administrators table is empty. It never resets or overwrites an existing
// account, so the bootstrap password can be removed from configuration after
// the first successful start.
func EnsureBootstrapAdmin(ctx context.Context, admins store.AdminStore, username, password string) (bool, error) {
	if admins == nil {
		return false, errors.New("adminauth: admin store is required")
	}
	if strings.TrimSpace(password) == "" {
		return false, nil
	}
	users, err := admins.List(ctx)
	if err != nil {
		return false, err
	}
	if len(users) > 0 {
		return false, nil
	}

	username = strings.TrimSpace(username)
	if username == "" {
		username = "admin"
	}
	if utf8.RuneCountInString(username) < 3 || utf8.RuneCountInString(username) > 64 {
		return false, errors.New("adminauth: bootstrap username must be 3-64 characters")
	}
	if utf8.RuneCountInString(password) < 12 {
		return false, errors.New("adminauth: bootstrap password must be at least 12 characters")
	}
	hash, err := crypto.HashPassword(password)
	if err != nil {
		return false, err
	}
	admin := &store.AdminUser{
		ID:           uuid.New(),
		Username:     username,
		PasswordHash: hash,
		Role:         "super_admin",
		Status:       1,
	}
	if err := admins.Create(ctx, admin); err != nil {
		return false, err
	}
	return true, nil
}
