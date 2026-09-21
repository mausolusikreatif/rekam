package db

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Password hashing uses PBKDF2-HMAC-SHA256 (stdlib crypto/pbkdf2). The stored
// form is self-describing so the work factor can be raised later without
// invalidating existing hashes: "pbkdf2_sha256$<iter>$<saltB64>$<hashB64>".
const (
	pbkdf2Iter    = 210000
	pbkdf2KeyLen  = 32
	pbkdf2SaltLen = 16
	minPassword   = 8
	maxPassword   = 1024
)

// PBKDF2Iter / PBKDF2KeyLen / PBKDF2SaltLen expose the work-factor constants
// so the engine layer can reference them instead of magic numbers.
const PBKDF2Iter = pbkdf2Iter
const PBKDF2KeyLen = pbkdf2KeyLen
const PBKDF2SaltLen = pbkdf2SaltLen

// MinPassword / MaxPasswordLen expose bounds without repeating literals.
const MinPasswordLen = minPassword
const MaxPasswordLen = maxPassword

// HashPassword returns an encoded PBKDF2 hash for a plaintext password.
func HashPassword(password string) (string, error) {
	if n := len(password); n < minPassword || n > maxPassword {
		return "", fmt.Errorf("password must be %d–%d characters", minPassword, maxPassword)
	}
	salt := make([]byte, pbkdf2SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	dk, err := pbkdf2.Key(sha256.New, password, salt, pbkdf2Iter, pbkdf2KeyLen)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("pbkdf2_sha256$%d$%s$%s", pbkdf2Iter,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(dk)), nil
}

// verifyPassword reports whether password matches the encoded hash, in constant
// time. A malformed or empty encoded hash always fails.
func verifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2_sha256" {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter <= 0 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iter, len(want))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}

// NormalizeEmail lower-cases and trims an email for storage and lookup.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// ValidateEmail applies a minimal shape check (presence of one @ with text on
// both sides). Full RFC validation is intentionally out of scope.
func ValidateEmail(email string) error {
	if email == "" {
		return fmt.Errorf("email is required")
	}
	if len(email) > 320 {
		return fmt.Errorf("email too long")
	}
	at := strings.IndexByte(email, '@')
	if at <= 0 || at == len(email)-1 || strings.Count(email, "@") != 1 {
		return fmt.Errorf("invalid email address")
	}
	return nil
}

// CreateUser inserts a password-backed identity (email + password_hash) with its
// own api_key and memory path. Returns the identity and the raw api_key (shown
// once, used internally to scope sessions). Emails are unique (case-insensitive).
func (r *Registry) CreateUser(name, email, password, memoryPath string, allowWrite, isAdmin, confirmed bool) (*Identity, string, error) {
	email = NormalizeEmail(email)
	if err := ValidateEmail(email); err != nil {
		return nil, "", err
	}
	if strings.TrimSpace(name) == "" {
		name = email[:strings.IndexByte(email, '@')]
	}
	if err := validateIdentityFields(name, memoryPath); err != nil {
		return nil, "", err
	}
	pwHash, err := HashPassword(password)
	if err != nil {
		return nil, "", err
	}
	rawKey, err := GenerateAPIKey()
	if err != nil {
		return nil, "", err
	}
	id := &Identity{
		ID:         uuid.New().String(),
		Name:       name,
		Email:      email,
		APIKeyHash: HashKey(rawKey),
		MemoryPath: memoryPath,
		AllowWrite: allowWrite,
		IsAdmin:    isAdmin,
		CreatedAt:  time.Now().UTC(),
	}
	// Admin-provisioned and bootstrap accounts pass confirmed=true (trusted).
	// Self-service signup passes confirmed=false and drives the email
	// confirmation flow via a token.
	_, err = r.db.Exec(`
		INSERT INTO identities (id, name, email, api_key, memory_path, allow_write, is_admin, password_hash, confirmed, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, id.ID, id.Name, id.Email, id.APIKeyHash, id.MemoryPath,
		boolToInt(id.AllowWrite), boolToInt(id.IsAdmin), pwHash, boolToInt(confirmed),
		id.CreatedAt.Format(time.RFC3339))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, "", fmt.Errorf("an account with that email already exists")
		}
		return nil, "", fmt.Errorf("create user: %w", err)
	}
	return id, rawKey, nil
}

// LookupByEmail returns the identity for an email, or nil if not found. The scan
// is inline rather than via scanIdentity because that helper folds a missing row
// into an "unknown API key" error; here a missing row is the ordinary not-found
// case and must come back as (nil, nil) so callers can distinguish it.
func (r *Registry) LookupByEmail(email string) (*Identity, error) {
	email = NormalizeEmail(email)
	row := r.db.QueryRow(`
		SELECT id, name, api_key, memory_path, allow_write, created_at, email, is_admin
		FROM identities WHERE email = ?
	`, email)
	var id Identity
	var createdAt string
	var allowWrite, isAdmin int
	var em sql.NullString
	err := row.Scan(&id.ID, &id.Name, &id.APIKeyHash, &id.MemoryPath,
		&allowWrite, &createdAt, &em, &isAdmin)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	id.AllowWrite = allowWrite == 1
	id.IsAdmin = isAdmin == 1
	id.Email = em.String
	id.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	return &id, nil
}

// AuthenticatePassword verifies email+password and returns the identity on
// success. The error is intentionally uniform to avoid leaking which of the two
// was wrong.
func (r *Registry) AuthenticatePassword(email, password string) (*Identity, error) {
	email = NormalizeEmail(email)
	row := r.db.QueryRow(`
		SELECT id, name, api_key, memory_path, allow_write, created_at, email, is_admin, COALESCE(password_hash, '')
		FROM identities WHERE email = ?
	`, email)
	var id Identity
	var createdAt, pwHash string
	var allowWrite, isAdmin int
	var em sql.NullString
	err := row.Scan(&id.ID, &id.Name, &id.APIKeyHash, &id.MemoryPath,
		&allowWrite, &createdAt, &em, &isAdmin, &pwHash)
	if err == sql.ErrNoRows || pwHash == "" || !verifyPassword(pwHash, password) {
		return nil, fmt.Errorf("invalid email or password")
	}
	if err != nil {
		return nil, err
	}
	id.AllowWrite = allowWrite == 1
	id.IsAdmin = isAdmin == 1
	id.Email = em.String
	id.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	return &id, nil
}

// ResolveByID returns an identity by its primary id. Used by session auth, which
// stores an identity id rather than a raw key.
func (r *Registry) ResolveByID(id string) (*Identity, error) {
	row := r.db.QueryRow(`
		SELECT id, name, api_key, memory_path, allow_write, created_at, email, is_admin
		FROM identities WHERE id = ?
	`, id)
	return scanIdentity(row)
}

// SetPassword updates the password hash for an identity.
func (r *Registry) SetPassword(id, password string) error {
	pwHash, err := HashPassword(password)
	if err != nil {
		return err
	}
	res, err := r.db.Exec(`UPDATE identities SET password_hash = ? WHERE id = ?`, pwHash, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("identity %s not found", id)
	}
	return nil
}

// UpdateFlags updates the allow_write and/or is_admin flags. Nil pointers leave
// the corresponding column unchanged.
func (r *Registry) UpdateFlags(id string, allowWrite, isAdmin *bool) error {
	sets := []string{}
	args := []any{}
	if allowWrite != nil {
		sets = append(sets, "allow_write = ?")
		args = append(args, boolToInt(*allowWrite))
	}
	if isAdmin != nil {
		sets = append(sets, "is_admin = ?")
		args = append(args, boolToInt(*isAdmin))
	}
	if len(sets) == 0 {
		return nil
	}
	args = append(args, id)
	res, err := r.db.Exec(`UPDATE identities SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("identity %s not found", id)
	}
	return nil
}

// AdminExists reports whether at least one identity has the admin flag set.
func (r *Registry) AdminExists() (bool, error) {
	var n int
	err := r.db.QueryRow(`SELECT COUNT(*) FROM identities WHERE is_admin = 1`).Scan(&n)
	return n > 0, err
}

// SetCredentials attaches an email + password (and admin flag) to an existing
// identity, preserving its memory_path. Used to adopt a legacy API-key identity
// (e.g. the master owner) as a password/admin account without moving its data.
func (r *Registry) SetCredentials(id, email, password string, isAdmin bool) error {
	email = NormalizeEmail(email)
	if err := ValidateEmail(email); err != nil {
		return err
	}
	pwHash, err := HashPassword(password)
	if err != nil {
		return err
	}
	res, err := r.db.Exec(
		`UPDATE identities SET email = ?, password_hash = ?, is_admin = ? WHERE id = ?`,
		email, pwHash, boolToInt(isAdmin), id)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return fmt.Errorf("email %q already in use", email)
		}
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("identity %s not found", id)
	}
	return nil
}

// IsConfirmed reports whether an identity has confirmed their email.
func (r *Registry) IsConfirmed(id string) (bool, error) {
	var c int
	if err := r.db.QueryRow(`SELECT confirmed FROM identities WHERE id = ?`, id).Scan(&c); err != nil {
		return false, err
	}
	return c == 1, nil
}

// randomHex returns n random bytes hex-encoded.
func (r *Registry) randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// SetConfirmToken generates a confirmation token for an identity and refreshes
// its expiry. Returns the raw token (surface once).
func (r *Registry) SetConfirmToken(id string, ttl time.Duration) (string, error) {
	token, err := r.randomHex(32)
	if err != nil {
		return "", err
	}
	_, err = r.db.Exec(
		`UPDATE identities SET confirm_token = ?, confirm_expires = ? WHERE id = ?`,
		token, time.Now().UTC().Add(ttl).Format(time.RFC3339), id)
	if err != nil {
		return "", err
	}
	return token, nil
}

// ConfirmByToken marks the identity as confirmed if the token matches and is
// not expired. Clears the confirmation fields on success.
func (r *Registry) ConfirmByToken(token string) (*Identity, error) {
	row := r.db.QueryRow(`
		SELECT id, name, api_key, memory_path, allow_write, created_at, email, is_admin
		FROM identities
		WHERE confirm_token = ? AND confirm_expires IS NOT NULL AND confirm_expires > ?
	`, token, time.Now().UTC().Format(time.RFC3339))
	id, err := scanIdentity(row)
	if err != nil {
		return nil, fmt.Errorf("invalid or expired confirmation token")
	}
	if _, err := r.db.Exec(`UPDATE identities SET confirmed = 1, confirm_token = NULL, confirm_expires = NULL WHERE id = ?`, id.ID); err != nil {
		return nil, err
	}
	return id, nil
}

// SetResetToken generates a password-reset token for an identity and refreshes
// its expiry. Returns the raw token (surface once).
func (r *Registry) SetResetToken(id string, ttl time.Duration) (string, error) {
	token, err := r.randomHex(32)
	if err != nil {
		return "", err
	}
	_, err = r.db.Exec(
		`UPDATE identities SET reset_token = ?, reset_expires = ? WHERE id = ?`,
		token, time.Now().UTC().Add(ttl).Format(time.RFC3339), id)
	if err != nil {
		return "", err
	}
	return token, nil
}

// ResetPasswordByToken updates the password hash if the token matches and is
// not expired. Clears the reset fields on success.
func (r *Registry) ResetPasswordByToken(token, password string) (*Identity, error) {
	pwHash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}
	row := r.db.QueryRow(`
		SELECT id, name, api_key, memory_path, allow_write, created_at, email, is_admin
		FROM identities
		WHERE reset_token = ? AND reset_expires IS NOT NULL AND reset_expires > ?
	`, token, time.Now().UTC().Format(time.RFC3339))
	id, err := scanIdentity(row)
	if err != nil {
		return nil, fmt.Errorf("invalid or expired reset token")
	}
	if _, err := r.db.Exec(
		`UPDATE identities SET password_hash = ?, reset_token = NULL, reset_expires = NULL WHERE id = ?`,
		pwHash, id.ID,
	); err != nil {
		return nil, err
	}
	return id, nil
}

// UnconfirmedIdentities returns identities that have not confirmed their email.
// Useful for admin hygiene and delayed cleanup.
func (r *Registry) UnconfirmedIdentities() ([]*Identity, error) {
	rows, err := r.db.Query(`
		SELECT id, name, api_key, memory_path, allow_write, created_at, email, is_admin
		FROM identities WHERE confirmed = 0
		ORDER BY created_at
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []*Identity
	for rows.Next() {
		id, err := scanIdentityRow(rows)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// PendingResets returns identities with a non-expired reset token currently set.
func (r *Registry) PendingResets() ([]*Identity, error) {
	rows, err := r.db.Query(`
		SELECT id, name, api_key, memory_path, allow_write, created_at, email, is_admin
		FROM identities WHERE reset_token IS NOT NULL AND reset_expires > ?
		ORDER BY created_at
	`, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []*Identity
	for rows.Next() {
		id, err := scanIdentityRow(rows)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
