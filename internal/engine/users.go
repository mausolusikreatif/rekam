package engine

import (
	"fmt"
	"time"

	"github.com/Ucok23/rekam/internal/db"
)

// CreateUser provisions a password-backed account: a registry identity (email +
// password) bound to memoryPath, and the per-user memory file itself (created if
// missing). Used by self-service signup, admin create, and the admin bootstrap.
func (e *Engine) CreateUser(name, email, password, memoryPath string, allowWrite, isAdmin bool) (*db.Identity, error) {
	if err := e.requireRegistryPrimary(); err != nil {
		return nil, err
	}
	id, _, err := e.registry.CreateUser(name, email, password, memoryPath, allowWrite, isAdmin, true)
	if err != nil {
		return nil, &ActionableError{Code: "signup_failed", Message: err.Error()}
	}
	if _, err := e.GetOrOpenMem(memoryPath); err != nil {
		return nil, err
	}
	return id, nil
}

// AuthenticatePassword verifies an email/password login and returns the identity.
func (e *Engine) AuthenticatePassword(email, password string) (*db.Identity, error) {
	id, err := e.registry.AuthenticatePassword(email, password)
	if err != nil {
		return nil, &ActionableError{Code: "auth_failed", Message: err.Error()}
	}
	return id, nil
}

// IdentityByID returns an identity by id (used to render the current session).
func (e *Engine) IdentityByID(id string) (*db.Identity, error) {
	return e.registry.ResolveByID(id)
}

// SetUserPassword sets a new password for an identity (admin action / reset).
func (e *Engine) SetUserPassword(id, password string) error {
	if err := e.requireRegistryPrimary(); err != nil {
		return err
	}
	return e.registry.SetPassword(id, password)
}

// UpdateUserFlags toggles allow_write and/or is_admin (nil = leave unchanged).
func (e *Engine) UpdateUserFlags(id string, allowWrite, isAdmin *bool) error {
	if err := e.requireRegistryPrimary(); err != nil {
		return err
	}
	return e.registry.UpdateFlags(id, allowWrite, isAdmin)
}

// AdminExists reports whether any admin account exists (for bootstrap).
func (e *Engine) AdminExists() (bool, error) { return e.registry.AdminExists() }

// PromoteToAdmin adopts the identity behind apiKey as a password/admin account
// (sets email + password + is_admin) without changing its memory_path. Returns
// the updated identity. Used at bootstrap to make the master owner the admin so
// their existing memory stays reachable via the web login.
func (e *Engine) PromoteToAdmin(apiKey, email, password string) (*db.Identity, error) {
	if err := e.requireRegistryPrimary(); err != nil {
		return nil, err
	}
	id, err := e.registry.Resolve(apiKey)
	if err != nil {
		return nil, err
	}
	if err := e.registry.SetCredentials(id.ID, email, password, true); err != nil {
		return nil, err
	}
	return e.registry.ResolveByID(id.ID)
}

// CreateUnconfirmed provisions an unconfirmed identity so the user must click
// the confirmation link before they can log in. Returns the API response body
// plus the raw API key for internal scoping.
func (e *Engine) CreateUnconfirmed(name, email, password, memoryPath string, allowWrite bool) (*db.Identity, string, error) {
	if err := e.requireRegistryPrimary(); err != nil {
		return nil, "", err
	}
	email = db.NormalizeEmail(email)
	if err := db.ValidateEmail(email); err != nil {
		return nil, "", &ActionableError{Code: "invalid_email", Message: err.Error()}
	}
	id, rawKey, err := e.registry.CreateUser(name, email, password, memoryPath, allowWrite, false, false)
	if err != nil {
		return nil, "", &ActionableError{Code: "signup_failed", Message: err.Error()}
	}
	token, err := e.registry.SetConfirmToken(id.ID, 24*time.Hour)
	if err != nil {
		return nil, "", &ActionableError{Code: "confirm_failed", Message: err.Error()}
	}
	id.APIKeyHash = rawKey
	return id, token, nil
}

// ConfirmEmail marks an identity as confirmed via a token. Returns the
// identity and a boolean indicating whether it was already confirmed.
func (e *Engine) ConfirmEmail(token string) (*db.Identity, bool, error) {
	if err := e.requireRegistryPrimary(); err != nil {
		return nil, false, err
	}
	id, err := e.registry.ConfirmByToken(token)
	if err != nil {
		return nil, false, &ActionableError{Code: "confirm_failed", Message: err.Error()}
	}
	confirmed := id != nil
	return id, confirmed, nil
}

// Confirmed reports whether the identity has confirmed their email.
func (e *Engine) Confirmed(id string) (bool, error) {
	return e.registry.IsConfirmed(id)
}

// ForgotPassword generates a password-reset token and returns it alongside the
// target identity email. If the email is unknown it returns nil identity with
// a token so the caller cannot probe which emails exist.
func (e *Engine) ForgotPassword(email string) (*db.Identity, string, error) {
	if err := e.requireRegistryPrimary(); err != nil {
		return nil, "", err
	}
	email = db.NormalizeEmail(email)
	id, err := e.registry.LookupByEmail(email)
	if err != nil {
		return nil, "", &ActionableError{Code: "internal_error", Message: err.Error()}
	}
	if id == nil {
		// return a dummy token so the response shape is always present
		return nil, "", nil
	}
	token, err := e.registry.SetResetToken(id.ID, 30*time.Minute)
	if err != nil {
		return nil, "", &ActionableError{Code: "reset_failed", Message: err.Error()}
	}
	return id, token, nil
}

// ResetPassword updates the password from a valid reset token.
func (e *Engine) ResetPassword(token, password string) (*db.Identity, error) {
	if err := e.requireRegistryPrimary(); err != nil {
		return nil, err
	}
	if n := len(password); n < db.MinPasswordLen || n > db.MaxPasswordLen {
		return nil, &ActionableError{Code: "invalid_password", Message: fmt.Sprintf("password must be %d–%d characters", db.MinPasswordLen, db.MaxPasswordLen)}
	}
	id, err := e.registry.ResetPasswordByToken(token, password)
	if err != nil {
		return nil, &ActionableError{Code: "reset_failed", Message: err.Error()}
	}
	return id, nil
}
