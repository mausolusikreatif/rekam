package db

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Entitlement kinds. A person may hold one of each at once.
const (
	// EntitlementManaged is a subscription to the hosted service; Seats feeds
	// the seat limit of the teams that person owns.
	EntitlementManaged = "managed"
	// EntitlementSelfHostTeam is a licence to download the team-edition
	// binary. It gates downloads only — a binary already in someone's hands
	// keeps working indefinitely, by design. See issue #34.
	EntitlementSelfHostTeam = "self_host_team"
)

// Entitlement statuses.
const (
	StatusActive    = "active"
	StatusPastDue   = "past_due"
	StatusCancelled = "cancelled"
)

// Entitlement is what one person has bought.
type Entitlement struct {
	ID          string
	IdentityID  string
	Kind        string
	Status      string
	Seats       int
	ExpiresAt   *time.Time
	ExternalRef string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Entitles reports whether this row currently confers anything. past_due
// still entitles: a failed card should not lock someone out mid-cycle, and
// cancelling is an explicit act.
func (e *Entitlement) Entitles() bool {
	if e == nil || e.Status == StatusCancelled {
		return false
	}
	if e.ExpiresAt != nil && time.Now().After(*e.ExpiresAt) {
		return false
	}
	return true
}

// PutEntitlement creates or replaces the live row for (identity, kind).
// Idempotent on external_ref so a webhook redelivery does not duplicate.
func (r *Registry) PutEntitlement(e Entitlement) (*Entitlement, error) {
	if e.IdentityID == "" || e.Kind == "" {
		return nil, errors.New("entitlement needs an identity and a kind")
	}
	if e.Status == "" {
		e.Status = StatusActive
	}
	now := time.Now().UTC()

	existing, err := r.Entitlement(e.IdentityID, e.Kind)
	if err != nil {
		return nil, err
	}
	var expires any
	if e.ExpiresAt != nil {
		expires = e.ExpiresAt.UTC().Format(time.RFC3339)
	}

	if existing != nil {
		_, err = r.db.Exec(`
			UPDATE entitlements
			   SET status = ?, seats = ?, expires_at = ?, external_ref = ?, updated_at = ?
			 WHERE id = ?`,
			e.Status, e.Seats, expires, e.ExternalRef, now.Format(time.RFC3339), existing.ID)
		if err != nil {
			return nil, err
		}
		return r.Entitlement(e.IdentityID, e.Kind)
	}

	_, err = r.db.Exec(`
		INSERT INTO entitlements
			(id, identity_id, kind, status, seats, expires_at, external_ref, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		uuid.New().String(), e.IdentityID, e.Kind, e.Status, e.Seats, expires, e.ExternalRef,
		now.Format(time.RFC3339), now.Format(time.RFC3339))
	if err != nil {
		return nil, fmt.Errorf("insert entitlement: %w", err)
	}
	return r.Entitlement(e.IdentityID, e.Kind)
}

// Entitlement returns the live row for (identity, kind), or nil. Cancelled
// rows are history and are not returned.
func (r *Registry) Entitlement(identityID, kind string) (*Entitlement, error) {
	row := r.db.QueryRow(`
		SELECT id, identity_id, kind, status, seats, expires_at, external_ref, created_at, updated_at
		  FROM entitlements
		 WHERE identity_id = ? AND kind = ? AND status != ?`,
		identityID, kind, StatusCancelled)
	e, err := scanEntitlement(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return e, err
}

// CancelEntitlement marks the live row cancelled, keeping it as history.
func (r *Registry) CancelEntitlement(identityID, kind string) error {
	_, err := r.db.Exec(`
		UPDATE entitlements SET status = ?, updated_at = ?
		 WHERE identity_id = ? AND kind = ? AND status != ?`,
		StatusCancelled, time.Now().UTC().Format(time.RFC3339),
		identityID, kind, StatusCancelled)
	return err
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanEntitlement(row rowScanner) (*Entitlement, error) {
	var e Entitlement
	var expires, ref sql.NullString
	var created, updated string
	if err := row.Scan(&e.ID, &e.IdentityID, &e.Kind, &e.Status, &e.Seats,
		&expires, &ref, &created, &updated); err != nil {
		return nil, err
	}
	if expires.Valid && expires.String != "" {
		if t, err := time.Parse(time.RFC3339, expires.String); err == nil {
			e.ExpiresAt = &t
		}
	}
	e.ExternalRef = ref.String
	e.CreatedAt, _ = time.Parse(time.RFC3339, created)
	e.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
	return &e, nil
}

// TeamsOwnedBy lists the ids of live teams this identity created. Ownership
// is what a managed entitlement's seats apply to.
func (r *Registry) TeamsOwnedBy(identityID string) ([]string, error) {
	rows, err := r.db.Query(
		`SELECT id FROM teams WHERE created_by = ? AND deleted_at IS NULL`, identityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
