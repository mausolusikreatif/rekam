package db

import (
	"database/sql"
	"fmt"
	"time"
)

// Role is a member's opinionated role within a tenant. It fixes two things: the
// management capabilities the member has over the team, and the posture its
// taxonomy scope takes (full, read-write within grants, or read-only within
// grants). The set is deliberately small and closed — this is the "opinionated
// for teams" layer, not a general RBAC engine.
type Role string

const (
	// RoleOwner has full control and owns the team: unrestricted scope, manages
	// members and roles, and may transfer or delete the team. A tenant always has
	// at least one Owner.
	RoleOwner Role = "owner"
	// RoleAdmin manages members and taxonomy policy with unrestricted scope, but
	// cannot transfer or delete the team, nor touch an Owner.
	RoleAdmin Role = "admin"
	// RoleEditor reads and writes within its granted taxonomy branches. No team
	// management.
	RoleEditor Role = "editor"
	// RoleViewer reads within its granted branches. No writes, no management.
	RoleViewer Role = "viewer"
)

// validRoles is the closed set accepted on write.
var validRoles = map[Role]bool{RoleOwner: true, RoleAdmin: true, RoleEditor: true, RoleViewer: true}

// ValidRole reports whether r is a known role.
func ValidRole(r Role) bool { return validRoles[r] }

// CanWriteAny reports whether the role permits writing at all (before per-branch
// scope narrows where). Viewers cannot; everyone else can.
func (r Role) CanWriteAny() bool { return r != RoleViewer }

// CanManageMembers reports whether the role may add, remove, or re-role members
// and set their scope grants. Owners and Admins can.
func (r Role) CanManageMembers() bool { return r == RoleOwner || r == RoleAdmin }

// CanManageTeam reports whether the role may transfer ownership or delete the
// team. Owners only.
func (r Role) CanManageTeam() bool { return r == RoleOwner }

// Member is an identity's membership in one tenant: its role plus, for the
// scoped roles, the taxonomy branches it may reach. Owners and Admins ignore the
// grants (their scope is full); Editors and Viewers are confined to them.
type Member struct {
	IdentityID  string   `json:"identity_id"`
	Role        Role     `json:"role"`
	ReadGrants  []string `json:"read_grants,omitempty"`  // branches an Editor/Viewer may read (Editors also write)
	TitleGrants []string `json:"title_grants,omitempty"` // branches whose titles show though content stays hidden
	AddedAt     string   `json:"added_at"`
	AddedBy     string   `json:"added_by,omitempty"`
}

// Scope projects a member's role and grants onto the enforcement Scope used
// throughout the read/write paths. This is the single place role semantics turn
// into concrete taxonomy authorization:
//   - Owner/Admin  → FullScope (unrestricted)
//   - Editor       → read+write on ReadGrants, titles on TitleGrants
//   - Viewer       → read-only on ReadGrants, titles on TitleGrants
func (m *Member) Scope() Scope {
	switch m.Role {
	case RoleOwner, RoleAdmin:
		return FullScope()
	case RoleEditor:
		return NewScope(m.ReadGrants, m.ReadGrants, m.TitleGrants)
	default: // viewer and any unknown role fail closed to read-only-in-grants
		return NewScope(m.ReadGrants, nil, m.TitleGrants)
	}
}

// migrateMembers creates the tenant-local membership tables. members carries no
// foreign key to any identity table: the identity ids it references live in the
// central registry, a separate database, so referential integrity is enforced
// at the application layer (AddMember requires an existing identity).
func (m *MemoryDB) migrateMembers() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS members (
			identity_id TEXT PRIMARY KEY,
			role        TEXT NOT NULL,
			added_at    TEXT NOT NULL,
			added_by    TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS member_scopes (
			identity_id TEXT NOT NULL,
			kind        TEXT NOT NULL,  -- 'read' | 'titles'
			prefix      TEXT NOT NULL,
			PRIMARY KEY (identity_id, kind, prefix),
			FOREIGN KEY (identity_id) REFERENCES members(identity_id) ON DELETE CASCADE
		)`,
	}
	for _, s := range stmts {
		if _, err := m.db.Exec(s); err != nil {
			return fmt.Errorf("migrate members: %w", err)
		}
	}
	return nil
}

// HasMembers reports whether the tenant has any membership rows. A tenant with
// none is a personal file that predates (or never used) teams; its sole user is
// treated as its Owner. Once any member exists the tenant is a team, and access
// requires an explicit membership row.
func (m *MemoryDB) HasMembers() (bool, error) {
	var n int
	if err := m.db.QueryRow(`SELECT COUNT(*) FROM members`).Scan(&n); err != nil {
		return false, fmt.Errorf("count members: %w", err)
	}
	return n > 0, nil
}

// Member returns the membership for an identity, or (nil, nil) if none exists.
func (m *MemoryDB) Member(identityID string) (*Member, error) {
	var mem Member
	var addedBy sql.NullString
	err := m.db.QueryRow(
		`SELECT identity_id, role, added_at, added_by FROM members WHERE identity_id = ?`,
		identityID,
	).Scan(&mem.IdentityID, &mem.Role, &mem.AddedAt, &addedBy)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("member %s: %w", identityID, err)
	}
	mem.AddedBy = addedBy.String
	if err := m.loadGrants(&mem); err != nil {
		return nil, err
	}
	return &mem, nil
}

func (m *MemoryDB) loadGrants(mem *Member) error {
	rows, err := m.db.Query(
		`SELECT kind, prefix FROM member_scopes WHERE identity_id = ?`, mem.IdentityID)
	if err != nil {
		return fmt.Errorf("member grants %s: %w", mem.IdentityID, err)
	}
	defer rows.Close()
	for rows.Next() {
		var kind, prefix string
		if err := rows.Scan(&kind, &prefix); err != nil {
			return err
		}
		switch kind {
		case "read":
			mem.ReadGrants = append(mem.ReadGrants, prefix)
		case "titles":
			mem.TitleGrants = append(mem.TitleGrants, prefix)
		}
	}
	return rows.Err()
}

// EffectiveMember resolves an identity's membership, applying the solo-owner
// fallback so a personal file keeps working without any team setup. The returned
// denied flag is the security-critical case: a tenant that HAS members but not
// this one is a team the identity does not belong to, and must be refused —
// never silently defaulted to Owner.
//
//	real member          → (member, false, nil)
//	no members at all     → (synthetic Owner, false, nil)   // personal file
//	members but not this  → (nil, true, nil)                // access denied
func (m *MemoryDB) EffectiveMember(identityID string) (*Member, bool, error) {
	member, err := m.Member(identityID)
	if err != nil {
		return nil, false, err
	}
	if member != nil {
		return member, false, nil
	}
	hasMembers, err := m.HasMembers()
	if err != nil {
		return nil, false, err
	}
	if hasMembers {
		return nil, true, nil // team file, non-member → denied
	}
	// Personal file: its sole user is the Owner. Synthesized, not stored, so the
	// file stays a zero-config personal store until someone is explicitly added.
	return &Member{IdentityID: identityID, Role: RoleOwner}, false, nil
}

// ListMembers returns every membership in the tenant, Owners first then by
// add time, so the roster reads top-down.
func (m *MemoryDB) ListMembers() ([]*Member, error) {
	rows, err := m.db.Query(`SELECT identity_id, role, added_at, added_by FROM members
		ORDER BY CASE role WHEN 'owner' THEN 0 WHEN 'admin' THEN 1 WHEN 'editor' THEN 2 ELSE 3 END, added_at`)
	if err != nil {
		return nil, fmt.Errorf("list members: %w", err)
	}
	defer rows.Close()
	var out []*Member
	for rows.Next() {
		var mem Member
		var addedBy sql.NullString
		if err := rows.Scan(&mem.IdentityID, &mem.Role, &mem.AddedAt, &addedBy); err != nil {
			return nil, err
		}
		mem.AddedBy = addedBy.String
		out = append(out, &mem)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, mem := range out {
		if err := m.loadGrants(mem); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// CountOwners returns how many Owners the tenant has, used to prevent removing
// or demoting the last one.
func (m *MemoryDB) CountOwners() (int, error) {
	var n int
	err := m.db.QueryRow(`SELECT COUNT(*) FROM members WHERE role = ?`, RoleOwner).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count owners: %w", err)
	}
	return n, nil
}

// CountMembers returns the tenant's full roster size, used to enforce a
// team's plan seat_limit (see Engine.checkSeatLimit).
func (m *MemoryDB) CountMembers() (int, error) {
	var n int
	err := m.db.QueryRow(`SELECT COUNT(*) FROM members`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count members: %w", err)
	}
	return n, nil
}

// PutMember upserts a membership row and replaces its scope grants atomically.
// It performs no capability or safety checks; the engine layer owns those. read
// and titles grants are only meaningful for the scoped roles but are stored
// regardless so a later promotion/demotion keeps them.
func (m *MemoryDB) PutMember(identityID string, role Role, read, titles []string, addedBy string) error {
	if !ValidRole(role) {
		return fmt.Errorf("unknown role %q", role)
	}
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Preserve the original added_at/added_by on an update; only insert stamps them.
	res, err := tx.Exec(`UPDATE members SET role = ? WHERE identity_id = ?`, role, identityID)
	if err != nil {
		return fmt.Errorf("put member: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		if _, err := tx.Exec(
			`INSERT INTO members (identity_id, role, added_at, added_by) VALUES (?,?,?,?)`,
			identityID, role, time.Now().UTC().Format(time.RFC3339), nullStr(addedBy)); err != nil {
			return fmt.Errorf("put member: %w", err)
		}
	}
	if _, err := tx.Exec(`DELETE FROM member_scopes WHERE identity_id = ?`, identityID); err != nil {
		return err
	}
	insert := func(kind string, prefixes []string) error {
		for _, p := range prefixes {
			if _, err := tx.Exec(
				`INSERT OR IGNORE INTO member_scopes (identity_id, kind, prefix) VALUES (?,?,?)`,
				identityID, kind, p); err != nil {
				return err
			}
		}
		return nil
	}
	if err := insert("read", read); err != nil {
		return err
	}
	if err := insert("titles", titles); err != nil {
		return err
	}
	return tx.Commit()
}

// RemoveMember deletes a membership (its scope grants cascade). No safety checks;
// the engine guards against removing the last Owner.
func (m *MemoryDB) RemoveMember(identityID string) error {
	res, err := m.db.Exec(`DELETE FROM members WHERE identity_id = ?`, identityID)
	if err != nil {
		return fmt.Errorf("remove member: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("member %s not found", identityID)
	}
	return nil
}
