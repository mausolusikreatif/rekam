package db

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

// Identity field defaults and bounds, enforced server-side so no caller (CLI,
// OAuth mint, HTTP) can persist a malformed or oversized row. SQL placeholders
// already prevent injection; these guard against empty/junk values and against
// control characters that would mangle logs or the admin dashboard HTML.
const (
	// DefaultAllowWrite is the write permission a new identity receives when the
	// caller does not specify one. Read-only by default (least privilege).
	DefaultAllowWrite = false

	maxIdentityNameLen = 200
	maxMemoryPathLen   = 4096
)

// validateIdentityFields rejects empty/oversized names or memory paths and any
// control characters before a row is written.
func validateIdentityFields(name, memoryPath string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("identity name is required")
	}
	if len(name) > maxIdentityNameLen {
		return fmt.Errorf("identity name too long (max %d bytes)", maxIdentityNameLen)
	}
	if strings.TrimSpace(memoryPath) == "" {
		return fmt.Errorf("memory_path is required")
	}
	if len(memoryPath) > maxMemoryPathLen {
		return fmt.Errorf("memory_path too long (max %d bytes)", maxMemoryPathLen)
	}
	if strings.ContainsFunc(name+memoryPath, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return fmt.Errorf("identity fields must not contain control characters")
	}
	return nil
}

// Identity is a record in registry.sqlite.
type Identity struct {
	ID         string
	Name       string
	Email      string // optional; set for self-service / password accounts, unique when present
	APIKeyHash string
	MemoryPath string
	AllowWrite bool
	IsAdmin    bool
	CreatedAt  time.Time
}

// Grant records an OAuth authorization: a per-client API key that can be revoked
// without disturbing the shared master key.
//
// Two shapes exist. A delegated grant (api_key set) holds its own key hash and
// points IdentityID at the *real* user, so the token authenticates as that person
// — which is what lets an OAuth connection carry their team memberships. A legacy
// grant (api_key NULL) instead points at a hidden identity minted for it, which
// can only ever reach that identity's own file. Legacy grants keep working; new
// ones are always delegated.
type Grant struct {
	ID           string     `json:"id"`
	ClientID     string     `json:"client_id"`
	IdentityID   string     `json:"identity_id"`
	IdentityName string     `json:"identity_name"`
	TeamID       string     `json:"team_id,omitempty"`   // "" = the identity's personal corpus
	TeamName     string     `json:"team_name,omitempty"` // resolved for display; empty for a deleted team
	CreatedAt    time.Time  `json:"created_at"`
	RevokedAt    *time.Time `json:"revoked_at,omitempty"`
}

// Registry manages access to registry.sqlite.
type Registry struct {
	db *sql.DB
}

// OpenRegistry opens (or creates) the registry SQLite file.
func OpenRegistry(path string) (*Registry, error) {
	db, err := openDB(path)
	if err != nil {
		return nil, fmt.Errorf("open registry %s: %w", path, err)
	}
	r := &Registry{db: db}
	if err := r.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate registry: %w", err)
	}
	return r, nil
}

func (r *Registry) migrate() error {
	_, err := r.db.Exec(`
		CREATE TABLE IF NOT EXISTS identities (
			id           TEXT PRIMARY KEY,
			name         TEXT NOT NULL,
			api_key      TEXT NOT NULL UNIQUE,
			memory_path  TEXT NOT NULL,
			allow_write  INTEGER NOT NULL DEFAULT 0,
			created_at   TEXT NOT NULL
		)
	`)
	if err != nil {
		return err
	}
	// grants: OAuth authorizations. Each active grant points at an identity row
	// whose api_key is a freshly minted per-client key. Revoking deletes that
	// identity (so the key stops resolving) and stamps revoked_at here.
	_, err = r.db.Exec(`
		CREATE TABLE IF NOT EXISTS grants (
			id           TEXT PRIMARY KEY,
			client_id    TEXT NOT NULL,
			identity_id  TEXT NOT NULL,
			created_at   TEXT NOT NULL,
			revoked_at   TEXT
		)
	`)
	if err != nil {
		return err
	}
	// teams is the central directory that routes a team id to its corpus file,
	// and team_members is a discovery index of which identities can reach which
	// teams. Neither stores roles or scope — those live authoritatively inside
	// each tenant .rekam file (see db/members.go). The index only powers "list my
	// teams" and id→path routing; the tenant's member row remains the gate.
	_, err = r.db.Exec(`
		CREATE TABLE IF NOT EXISTS teams (
			id         TEXT PRIMARY KEY,
			name       TEXT NOT NULL,
			path       TEXT NOT NULL,
			created_by TEXT,
			created_at TEXT NOT NULL
		)`)
	if err != nil {
		return err
	}
	_, err = r.db.Exec(`
		CREATE TABLE IF NOT EXISTS team_members (
			team_id     TEXT NOT NULL,
			identity_id TEXT NOT NULL,
			added_at    TEXT NOT NULL,
			PRIMARY KEY (team_id, identity_id),
			FOREIGN KEY (team_id) REFERENCES teams(id) ON DELETE CASCADE
		)`)
	if err != nil {
		return err
	}
	// oauth_codes and oauth_consents hold the short-lived (~10min), single-use
	// state for an in-flight /authorize -> /token exchange. This used to live in
	// a process-local sync.Map, which only worked because exactly one rekam
	// process ever served it; that broke silently the moment a second instance
	// (a redeploy landing on a new process, or more than one node) served the
	// second half of the flow. Persisting it here — alongside everything else
	// that answers "who is this and what have they been granted" — means the
	// flow survives a restart and works across instances, same as a grant does.
	_, err = r.db.Exec(`
		CREATE TABLE IF NOT EXISTS oauth_codes (
			code                  TEXT PRIMARY KEY,
			code_challenge        TEXT NOT NULL,
			client_id             TEXT NOT NULL,
			identity_id           TEXT NOT NULL,
			team_id               TEXT NOT NULL DEFAULT '',
			expires_at            TEXT NOT NULL
		)`)
	if err != nil {
		return err
	}
	_, err = r.db.Exec(`
		CREATE TABLE IF NOT EXISTS oauth_consents (
			ticket                TEXT PRIMARY KEY,
			identity_id           TEXT NOT NULL,
			teams_json            TEXT NOT NULL,
			redirect_uri          TEXT NOT NULL,
			state                 TEXT NOT NULL DEFAULT '',
			code_challenge        TEXT NOT NULL,
			code_challenge_method TEXT NOT NULL,
			client_id             TEXT NOT NULL,
			expires_at            TEXT NOT NULL
		)`)
	if err != nil {
		return err
	}
	// sessions holds browser login sessions (the rekam_session cookie value,
	// hashed the same way an API key is — a registry dump shouldn't hand out
	// live sessions any more than it hands out live keys). This used to live in
	// a process-local sync.Map (see internal/api/session.go's sessionStore),
	// which meant every process restart — i.e. every deploy — logged everyone
	// out. Same fix, same rationale as oauth_codes/oauth_consents above:
	// persisting it here means a login survives a restart and is visible to
	// every node, not just the one that minted it.
	_, err = r.db.Exec(`
		CREATE TABLE IF NOT EXISTS sessions (
			id_hash     TEXT PRIMARY KEY,
			identity_id TEXT NOT NULL,
			expires_at  TEXT NOT NULL
		)`)
	if err != nil {
		return err
	}
	// team_invites lets a manager add someone who has no rekam account yet — the
	// gap SetMember/LookupMemberByEmail can't cover, since both require the
	// target identity to already exist. An invite is scoped to an email, not an
	// identity, keyed by a token hashed the same way a session id is; accepting
	// it (Engine.AcceptInvite) is what actually creates the membership, and only
	// once the accepting identity's own email matches.
	_, err = r.db.Exec(`
		CREATE TABLE IF NOT EXISTS team_invites (
			token_hash   TEXT PRIMARY KEY,
			team_id      TEXT NOT NULL,
			email        TEXT NOT NULL,
			role         TEXT NOT NULL,
			read_grants  TEXT NOT NULL DEFAULT '[]',
			title_grants TEXT NOT NULL DEFAULT '[]',
			invited_by   TEXT,
			created_at   TEXT NOT NULL,
			expires_at   TEXT NOT NULL
		)`)
	if err != nil {
		return err
	}
	if _, err := r.db.Exec(`CREATE INDEX IF NOT EXISTS idx_team_invites_email ON team_invites (email)`); err != nil {
		return err
	}
	// Registered OAuth clients and the redirect URIs they declared. Without
	// this, /authorize has nothing to check a redirect against — see #22.
	_, err = r.db.Exec(`
		CREATE TABLE IF NOT EXISTS oauth_clients (
			client_id     TEXT PRIMARY KEY,
			redirect_uris TEXT NOT NULL,
			created_at    TEXT NOT NULL
		)`)
	if err != nil {
		return err
	}

	// What a person has bought. Deliberately keyed on the identity rather than
	// held as columns on it: one person can hold a managed subscription and a
	// self-hosted licence at the same time, and a lapsed-then-resumed
	// subscription wants history rather than an overwritten field. A team's
	// seat limit is derived from its owner's entitlement (see
	// Engine.SyncSeatsFromEntitlements) — teams.plan/seat_limit remain the
	// enforced values, they just stop being the source of truth.
	_, err = r.db.Exec(`
		CREATE TABLE IF NOT EXISTS entitlements (
			id           TEXT PRIMARY KEY,
			identity_id  TEXT NOT NULL,
			kind         TEXT NOT NULL,
			status       TEXT NOT NULL,
			seats        INTEGER NOT NULL DEFAULT 0,
			expires_at   TEXT,
			external_ref TEXT,
			created_at   TEXT NOT NULL,
			updated_at   TEXT NOT NULL
		)`)
	if err != nil {
		return err
	}
	if _, err := r.db.Exec(`CREATE INDEX IF NOT EXISTS idx_entitlements_identity ON entitlements (identity_id)`); err != nil {
		return err
	}
	// One live row per (person, kind): a second active managed subscription for
	// the same account is a bug, not a use case. Cancelled rows keep their
	// history because the uniqueness only covers active ones.
	if _, err := r.db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_entitlements_live
		ON entitlements (identity_id, kind) WHERE status != 'cancelled'`); err != nil {
		return err
	}

	// Taxonomy authorization is NOT stored here: an identity's read/write scope
	// comes from its role in the tenant it is accessing, which lives inside the
	// tenant .rekam file. The registry answers "who are you" and "which teams";
	// the tenant answers "what may you do here".
	if err := r.migrateAddTeamColumns(); err != nil {
		return err
	}
	if err := r.migrateAddGrantColumns(); err != nil {
		return err
	}
	if err := r.migrateDropLegacyColumns(); err != nil {
		return err
	}
	return r.migrateAddUserColumns()
}

// migrateAddUserColumns adds the email/password_hash/is_admin/confirmed/reset
// columns used by self-service password accounts. Additive and idempotent:
// legacy API-key rows keep working with these columns NULL/0. Existing
// password-backed identities are stamped confirmed=1 so migration doesn't
// lock out existing users.
func (r *Registry) migrateAddUserColumns() error {
	add := []struct{ col, ddl string }{
		{"email", `ALTER TABLE identities ADD COLUMN email TEXT`},
		{"password_hash", `ALTER TABLE identities ADD COLUMN password_hash TEXT`},
		{"is_admin", `ALTER TABLE identities ADD COLUMN is_admin INTEGER NOT NULL DEFAULT 0`},
		{"confirmed", `ALTER TABLE identities ADD COLUMN confirmed INTEGER NOT NULL DEFAULT 0`},
		{"confirm_token", `ALTER TABLE identities ADD COLUMN confirm_token TEXT`},
		{"confirm_expires", `ALTER TABLE identities ADD COLUMN confirm_expires TEXT`},
		{"reset_token", `ALTER TABLE identities ADD COLUMN reset_token TEXT`},
		{"reset_expires", `ALTER TABLE identities ADD COLUMN reset_expires TEXT`},
	}
	for _, a := range add {
		var n int
		if err := r.db.QueryRow(
			`SELECT COUNT(*) FROM pragma_table_info('identities') WHERE name = ?`, a.col,
		).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			if _, err := r.db.Exec(a.ddl); err != nil {
				return fmt.Errorf("add column %s: %w", a.col, err)
			}
		}
	}
	_, err := r.db.Exec(
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_identities_email
		 ON identities(email) WHERE email IS NOT NULL AND email <> ''`)
	if err != nil {
		return err
	}
	// Backfill: existing password-backed identities are treated as confirmed so
	// the new regime doesn't lock out production users on first restart.
	r.db.Exec(`UPDATE identities SET confirmed = 1 WHERE password_hash <> '' AND confirm_token IS NULL`)
	return nil
}

// migrateDropLegacyColumns removes type and auto_approve columns if they exist.
func (r *Registry) migrateDropLegacyColumns() error {
	var count int
	err := r.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('identities') WHERE name = 'type'`).Scan(&count)
	if err != nil || count == 0 {
		return nil
	}

	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmts := []string{
		`CREATE TABLE identities_new (
			id           TEXT PRIMARY KEY,
			name         TEXT NOT NULL,
			api_key      TEXT NOT NULL UNIQUE,
			memory_path  TEXT NOT NULL,
			allow_write  INTEGER NOT NULL DEFAULT 0,
			created_at   TEXT NOT NULL
		)`,
		`INSERT INTO identities_new (id, name, api_key, memory_path, allow_write, created_at)
		 SELECT id, name, api_key, memory_path, allow_write, created_at FROM identities`,
		`DROP TABLE identities`,
		`ALTER TABLE identities_new RENAME TO identities`,
	}
	for _, s := range stmts {
		if _, err := tx.Exec(s); err != nil {
			return fmt.Errorf("registry migration: %w", err)
		}
	}
	return tx.Commit()
}

// HashKey returns the SHA-256 hex hash of an API key.
func HashKey(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

// Resolve looks up an identity by its raw API key.
func (r *Registry) Resolve(rawKey string) (*Identity, error) {
	hashed := HashKey(rawKey)
	row := r.db.QueryRow(`
		SELECT id, name, api_key, memory_path, allow_write, created_at, email, is_admin
		FROM identities WHERE api_key = ?
	`, hashed)
	return scanIdentity(row)
}

// Create inserts a new identity.
func (r *Registry) Create(name, rawKey, memoryPath string, allowWrite bool) (*Identity, error) {
	if err := validateIdentityFields(name, memoryPath); err != nil {
		return nil, err
	}
	id := &Identity{
		ID:         uuid.New().String(),
		Name:       name,
		APIKeyHash: HashKey(rawKey),
		MemoryPath: memoryPath,
		AllowWrite: allowWrite,
		CreatedAt:  time.Now().UTC(),
	}
	_, err := r.db.Exec(`
		INSERT INTO identities (id, name, api_key, memory_path, allow_write, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, id.ID, id.Name, id.APIKeyHash, id.MemoryPath,
		boolToInt(id.AllowWrite),
		id.CreatedAt.Format(time.RFC3339))
	if err != nil {
		return nil, fmt.Errorf("create identity: %w", err)
	}
	return id, nil
}

// SetMemoryPath repoints an identity at a different tenant store. This is the
// provisioning half of adding someone to a team: their home corpus becomes the
// team's file, so they resolve into it. Membership (their role there) is set
// separately in the tenant. Guarded by validation to reject empty/oversized paths.
func (r *Registry) SetMemoryPath(id, memoryPath string) error {
	if err := validateIdentityFields("x", memoryPath); err != nil {
		return err
	}
	res, err := r.db.Exec(`UPDATE identities SET memory_path = ? WHERE id = ?`, memoryPath, id)
	if err != nil {
		return fmt.Errorf("set memory_path: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("identity %s not found", id)
	}
	return nil
}

// migrateAddTeamColumns adds the soft-delete stamp to the teams directory.
// Additive and idempotent; existing rows migrate as live (deleted_at NULL).
func (r *Registry) migrateAddTeamColumns() error {
	var n int
	if err := r.db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('teams') WHERE name = 'deleted_at'`,
	).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		if _, err := r.db.Exec(`ALTER TABLE teams ADD COLUMN deleted_at TEXT`); err != nil {
			return fmt.Errorf("add column deleted_at: %w", err)
		}
	}
	if err := r.db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('teams') WHERE name = 'plan'`,
	).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		if _, err := r.db.Exec(`ALTER TABLE teams ADD COLUMN plan TEXT NOT NULL DEFAULT 'free'`); err != nil {
			return fmt.Errorf("add column plan: %w", err)
		}
	}
	if err := r.db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('teams') WHERE name = 'seat_limit'`,
	).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		if _, err := r.db.Exec(`ALTER TABLE teams ADD COLUMN seat_limit INTEGER NOT NULL DEFAULT 1`); err != nil {
			return fmt.Errorf("add column seat_limit: %w", err)
		}
	}
	return nil
}

// Team is a routing record: a named corpus (path) that identities join with a
// role stored inside that corpus. The registry knows the team exists and where
// its file is; it does not know anyone's role — that lives in the tenant.
type Team struct {
	ID        string
	Name      string
	Path      string
	CreatedBy string
	CreatedAt time.Time
	// Plan and SeatLimit gate paid capacity. Every team starts "free"/1 on
	// creation; a Paddle subscription raises both (that webhook isn't wired
	// yet — this is the field it will write to). See Engine.checkSeatLimit.
	Plan      string
	SeatLimit int
}

// ErrTeamLimit is returned by CreateTeam when createdBy already owns maxOwned
// non-deleted teams. Deliberately opaque (no count in the error itself) — the
// caller already knows maxOwned; Engine.CreateTeam attaches the human-facing
// message and context.
var ErrTeamLimit = errors.New("team limit reached")

// CreateTeam registers a new team directory entry, atomically refusing if
// createdBy already owns maxOwned non-deleted teams. The count check and the
// insert are one statement (INSERT ... SELECT ... WHERE), not a separate
// COUNT(*) followed by a separate INSERT — the previous two-step version let
// concurrent calls each read "one seat free" before either committed, so two
// (or more) racing requests could all succeed and push the owner over
// maxOwned. Seeding the creator's Owner role in the tenant file and indexing
// their access is the engine's job; this only records the id→path mapping.
func (r *Registry) CreateTeam(name, path, createdBy string, maxOwned int) (*Team, error) {
	if err := validateIdentityFields(name, path); err != nil {
		return nil, err
	}
	t := &Team{
		ID:        uuid.New().String(),
		Name:      name,
		Path:      path,
		CreatedBy: createdBy,
		CreatedAt: time.Now().UTC(),
	}
	res, err := r.db.Exec(`
		INSERT INTO teams (id, name, path, created_by, created_at)
		SELECT ?, ?, ?, ?, ?
		WHERE (SELECT COUNT(*) FROM teams WHERE created_by = ? AND deleted_at IS NULL) < ?
	`, t.ID, t.Name, t.Path, nullStr(t.CreatedBy), t.CreatedAt.Format(time.RFC3339), createdBy, maxOwned)
	if err != nil {
		return nil, fmt.Errorf("create team: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return nil, fmt.Errorf("create team: %w", err)
	} else if n == 0 {
		return nil, ErrTeamLimit
	}
	return t, nil
}

// TeamByID returns a team's routing record. Soft-deleted teams are invisible
// here, so id→path routing stops resolving the moment a team is deleted and
// every request that names it falls back to an ordinary "not found".
func (r *Registry) TeamByID(id string) (*Team, error) {
	var t Team
	var createdBy sql.NullString
	var createdAt string
	err := r.db.QueryRow(
		`SELECT id, name, path, created_by, created_at, plan, seat_limit FROM teams
		 WHERE id = ? AND deleted_at IS NULL`, id,
	).Scan(&t.ID, &t.Name, &t.Path, &createdBy, &createdAt, &t.Plan, &t.SeatLimit)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("team %s not found", id)
	}
	if err != nil {
		return nil, fmt.Errorf("team %s: %w", id, err)
	}
	t.CreatedBy = createdBy.String
	t.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	return &t, nil
}

// SetTeamPlan updates a team's plan and seat_limit — the write side of
// Engine.checkSeatLimit's read. Not gated here (no HTTP route calls this
// directly): the intended callers are a Paddle subscription webhook, once
// wired, and test fixtures that need a multi-seat team; either way the
// caller owns authorizing the change, not this method.
func (r *Registry) SetTeamPlan(teamID, plan string, seatLimit int) error {
	res, err := r.db.Exec(
		`UPDATE teams SET plan = ?, seat_limit = ? WHERE id = ? AND deleted_at IS NULL`,
		plan, seatLimit, teamID)
	if err != nil {
		return fmt.Errorf("set team plan %s: %w", teamID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("set team plan %s: %w", teamID, err)
	}
	if n == 0 {
		return fmt.Errorf("team %s not found", teamID)
	}
	return nil
}

// CountOwnedTeams returns how many non-deleted teams identityID created — used
// to enforce a free-tier cap on team creation itself. Without this, a seat
// cap on individual teams is trivially routed around by creating more of
// them: a "1 seat per team" limit means nothing if nothing limits how many
// teams one identity can own.
func (r *Registry) CountOwnedTeams(identityID string) (int, error) {
	var n int
	err := r.db.QueryRow(
		`SELECT COUNT(*) FROM teams WHERE created_by = ? AND deleted_at IS NULL`, identityID,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count owned teams for %s: %w", identityID, err)
	}
	return n, nil
}

// TeamsForIdentity lists the teams an identity has been indexed into — the teams
// it can discover and select, distinct from its personal home corpus.
func (r *Registry) TeamsForIdentity(identityID string) ([]*Team, error) {
	rows, err := r.db.Query(`
		SELECT t.id, t.name, t.path, t.created_by, t.created_at, t.plan, t.seat_limit
		FROM teams t JOIN team_members tm ON tm.team_id = t.id
		WHERE tm.identity_id = ? AND t.deleted_at IS NULL
		ORDER BY t.created_at`, identityID)
	if err != nil {
		return nil, fmt.Errorf("teams for %s: %w", identityID, err)
	}
	defer rows.Close()
	var out []*Team
	for rows.Next() {
		var t Team
		var createdBy sql.NullString
		var createdAt string
		if err := rows.Scan(&t.ID, &t.Name, &t.Path, &createdBy, &createdAt, &t.Plan, &t.SeatLimit); err != nil {
			return nil, err
		}
		t.CreatedBy = createdBy.String
		t.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		out = append(out, &t)
	}
	return out, rows.Err()
}

// TeamSummary is a team plus a member count from the discovery index
// (team_members) — enough for an admin directory view without opening every
// tenant file. The count tracks IndexTeamMember/UnindexTeamMember, which is
// best-effort discovery bookkeeping, not the authoritative roster (that's the
// tenant file's own members table) — good enough to browse and decide what to
// delete, not to enforce anything against.
type TeamSummary struct {
	*Team
	MemberCount int
}

// AllTeams returns every non-deleted team in the registry, for an admin
// directory view — unlike TeamsForIdentity, which is scoped to what one
// identity can see.
func (r *Registry) AllTeams() ([]*TeamSummary, error) {
	rows, err := r.db.Query(`
		SELECT t.id, t.name, t.path, t.created_by, t.created_at, t.plan, t.seat_limit,
			(SELECT COUNT(*) FROM team_members tm WHERE tm.team_id = t.id)
		FROM teams t WHERE t.deleted_at IS NULL ORDER BY t.created_at`)
	if err != nil {
		return nil, fmt.Errorf("all teams: %w", err)
	}
	defer rows.Close()
	var out []*TeamSummary
	for rows.Next() {
		var t Team
		var createdBy sql.NullString
		var createdAt string
		var memberCount int
		if err := rows.Scan(&t.ID, &t.Name, &t.Path, &createdBy, &createdAt, &t.Plan, &t.SeatLimit, &memberCount); err != nil {
			return nil, err
		}
		t.CreatedBy = createdBy.String
		t.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		out = append(out, &TeamSummary{Team: &t, MemberCount: memberCount})
	}
	return out, rows.Err()
}

// IndexTeamMember records that an identity can reach a team (for discovery and
// routing). Idempotent. The authoritative membership is the tenant's member row.
func (r *Registry) IndexTeamMember(teamID, identityID string) error {
	_, err := r.db.Exec(
		`INSERT OR IGNORE INTO team_members (team_id, identity_id, added_at) VALUES (?,?,?)`,
		teamID, identityID, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("index team member: %w", err)
	}
	return nil
}

// RenameTeam changes a team's display name. The name is cosmetic — routing is
// by id — so this touches nothing else.
func (r *Registry) RenameTeam(id, name string) error {
	if err := validateIdentityFields(name, "x"); err != nil {
		return err
	}
	res, err := r.db.Exec(
		`UPDATE teams SET name = ? WHERE id = ? AND deleted_at IS NULL`, name, id)
	if err != nil {
		return fmt.Errorf("rename team: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("team %s not found", id)
	}
	return nil
}

// SoftDeleteTeam stamps a team deleted and drops its discovery index, which
// together make it unreachable: TeamsForIdentity stops listing it and TeamByID
// stops resolving it to a path, so no bearer can be rebound into it again.
//
// The corpus file itself is deliberately left on disk. Nothing here unlinks it,
// so a deletion is recoverable by clearing the stamp, and no open handle in the
// instance manager is invalidated underneath a request in flight. Reclaiming the
// bytes is a separate operator concern.
func (r *Registry) SoftDeleteTeam(id string) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.Exec(`UPDATE teams SET deleted_at = ? WHERE id = ? AND deleted_at IS NULL`,
		time.Now().UTC().Format(time.RFC3339), id)
	if err != nil {
		return fmt.Errorf("delete team: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("team %s not found", id)
	}
	// Drop the discovery index so the team disappears from every member's
	// switcher. Membership rows inside the tenant file are left untouched: they
	// are the record of who was on the team, and restoring means re-indexing them.
	if _, err := tx.Exec(`DELETE FROM team_members WHERE team_id = ?`, id); err != nil {
		return fmt.Errorf("unindex team: %w", err)
	}
	return tx.Commit()
}

// UnindexTeamMember removes the discovery-index row for a member.
func (r *Registry) UnindexTeamMember(teamID, identityID string) error {
	_, err := r.db.Exec(
		`DELETE FROM team_members WHERE team_id = ? AND identity_id = ?`, teamID, identityID)
	return err
}

// Delete removes an identity by ID.
func (r *Registry) Delete(id string) error {
	res, err := r.db.Exec(`DELETE FROM identities WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("identity %s not found", id)
	}
	return nil
}

// List returns all identities ordered by creation time.
func (r *Registry) List() ([]*Identity, error) {
	rows, err := r.db.Query(`
		SELECT id, name, api_key, memory_path, allow_write, created_at, email, is_admin
		FROM identities ORDER BY created_at
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

// GenerateAPIKey returns a fresh random API key with the mkey_ prefix.
func GenerateAPIKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "mkey_" + hex.EncodeToString(b), nil
}

// MintGrant creates a fresh hidden identity bound to memoryPath/allowWrite and
// records a grant linking it to clientID. Returns the grant and the raw
// (unhashed) key — the caller must surface the raw key once and never persist it.
// migrateAddGrantColumns gives grants their own key hash and team binding, which
// is what turns a grant from "a hidden identity" into "a delegated key for a real
// user, scoped to one workspace". Additive and idempotent: existing rows migrate
// with both NULL and keep resolving through their hidden identity as before.
func (r *Registry) migrateAddGrantColumns() error {
	add := []struct{ col, ddl string }{
		{"api_key", `ALTER TABLE grants ADD COLUMN api_key TEXT`},
		{"team_id", `ALTER TABLE grants ADD COLUMN team_id TEXT`},
	}
	for _, a := range add {
		var n int
		if err := r.db.QueryRow(
			`SELECT COUNT(*) FROM pragma_table_info('grants') WHERE name = ?`, a.col,
		).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			if _, err := r.db.Exec(a.ddl); err != nil {
				return fmt.Errorf("add column %s: %w", a.col, err)
			}
		}
	}
	_, err := r.db.Exec(
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_grants_api_key
		 ON grants(api_key) WHERE api_key IS NOT NULL`)
	return err
}

// MintDelegatedGrant issues a revocable key that authenticates as identityID —
// the real user — optionally pinned to one team. No hidden identity is created,
// so the key carries the user's own memberships and roles wherever it is used,
// and revoking it later touches nothing but the grant row.
//
// teamID is recorded, not verified: whether the user may act in that team is
// decided at every request by their member row in the tenant file, which is the
// only authority that can answer it. Binding a team here narrows a token, it
// never widens one.
func (r *Registry) MintDelegatedGrant(clientID, identityID, teamID string) (*Grant, string, error) {
	rawKey, err := GenerateAPIKey()
	if err != nil {
		return nil, "", err
	}
	now := time.Now().UTC()
	g := &Grant{
		ID:         uuid.New().String(),
		ClientID:   clientID,
		IdentityID: identityID,
		TeamID:     teamID,
		CreatedAt:  now,
	}
	_, err = r.db.Exec(`
		INSERT INTO grants (id, client_id, identity_id, created_at, api_key, team_id)
		VALUES (?, ?, ?, ?, ?, ?)
	`, g.ID, clientID, identityID, now.Format(time.RFC3339), HashKey(rawKey), nullStr(teamID))
	if err != nil {
		return nil, "", fmt.Errorf("mint delegated grant: %w", err)
	}
	return g, rawKey, nil
}

// ResolveGrant resolves a delegated grant key to the identity it acts as and the
// team it is pinned to. ok is false for any key that is not a live delegated
// grant — unknown, revoked, or legacy — so callers fall through to the ordinary
// identity lookup.
func (r *Registry) ResolveGrant(rawKey string) (identity *Identity, teamID string, ok bool, err error) {
	var identityID string
	var team sql.NullString
	err = r.db.QueryRow(`
		SELECT identity_id, team_id FROM grants
		WHERE api_key = ? AND revoked_at IS NULL
	`, HashKey(rawKey)).Scan(&identityID, &team)
	if err == sql.ErrNoRows {
		return nil, "", false, nil
	}
	if err != nil {
		return nil, "", false, fmt.Errorf("resolve grant: %w", err)
	}
	identity, err = r.ResolveByID(identityID)
	if err != nil {
		// The grant outlived the account it delegated for; treat it as dead.
		return nil, "", false, nil
	}
	return identity, team.String, true, nil
}

func (r *Registry) MintGrant(clientID, name, memoryPath string, allowWrite bool) (*Grant, string, error) {
	if err := validateIdentityFields(name, memoryPath); err != nil {
		return nil, "", err
	}
	rawKey, err := GenerateAPIKey()
	if err != nil {
		return nil, "", err
	}
	now := time.Now().UTC()
	identityID := uuid.New().String()
	grantID := uuid.New().String()

	tx, err := r.db.Begin()
	if err != nil {
		return nil, "", err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
		INSERT INTO identities (id, name, api_key, memory_path, allow_write, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, identityID, name, HashKey(rawKey), memoryPath, boolToInt(allowWrite), now.Format(time.RFC3339)); err != nil {
		return nil, "", fmt.Errorf("mint grant identity: %w", err)
	}
	if _, err := tx.Exec(`
		INSERT INTO grants (id, client_id, identity_id, created_at)
		VALUES (?, ?, ?, ?)
	`, grantID, clientID, identityID, now.Format(time.RFC3339)); err != nil {
		return nil, "", fmt.Errorf("record grant: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, "", err
	}
	return &Grant{ID: grantID, ClientID: clientID, IdentityID: identityID, IdentityName: name, CreatedAt: now}, rawKey, nil
}

// RevokeGrant deletes the grant's minted identity (so its key stops resolving)
// and stamps the grant revoked. Idempotency is not assumed: revoking an unknown
// or already-revoked grant returns an error.
func (r *Registry) RevokeGrant(id string) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var identityID string
	var revokedAt, apiKey sql.NullString
	err = tx.QueryRow(`SELECT identity_id, revoked_at, api_key FROM grants WHERE id = ?`, id).
		Scan(&identityID, &revokedAt, &apiKey)
	if err == sql.ErrNoRows {
		return fmt.Errorf("grant %s not found", id)
	}
	if err != nil {
		return err
	}
	if revokedAt.Valid {
		return fmt.Errorf("grant %s already revoked", id)
	}
	// A legacy grant owns its hidden identity, and deleting it is what stops the
	// key resolving. A delegated grant points at a real person — deleting that
	// identity would delete the user's account. Stamping revoked_at is enough:
	// ResolveGrant only matches live rows.
	if !apiKey.Valid {
		if _, err := tx.Exec(`DELETE FROM identities WHERE id = ?`, identityID); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`UPDATE grants SET revoked_at = ? WHERE id = ?`,
		time.Now().UTC().Format(time.RFC3339), id); err != nil {
		return err
	}
	return tx.Commit()
}

// ListGrants returns all grants (active and revoked) ordered by creation time.
func (r *Registry) ListGrants() ([]*Grant, error) {
	rows, err := r.db.Query(`
		SELECT g.id, g.client_id, g.identity_id, g.created_at, g.revoked_at,
		       COALESCE(i.name, ''), COALESCE(g.team_id, ''), COALESCE(t.name, '')
		FROM grants g
		LEFT JOIN identities i ON i.id = g.identity_id
		LEFT JOIN teams t ON t.id = g.team_id AND t.deleted_at IS NULL
		ORDER BY g.created_at
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Grant
	for rows.Next() {
		var g Grant
		var createdAt string
		var revokedAt sql.NullString
		if err := rows.Scan(&g.ID, &g.ClientID, &g.IdentityID, &createdAt, &revokedAt,
			&g.IdentityName, &g.TeamID, &g.TeamName); err != nil {
			return nil, err
		}
		g.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		if revokedAt.Valid {
			t, _ := time.Parse(time.RFC3339, revokedAt.String)
			g.RevokedAt = &t
		}
		out = append(out, &g)
	}
	return out, rows.Err()
}

// Stats is a point-in-time summary of the registry for the admin dashboard.
type Stats struct {
	Identities   int `json:"identities"`
	Grants       int `json:"grants"`
	ActiveGrants int `json:"active_grants"`
	MemoryPaths  int `json:"memory_paths"`
}

// Stats returns aggregate counts across the registry: total identities (human +
// minted OAuth), total grants, grants that are still active (not revoked), and
// the number of distinct memory paths in use.
// OAuthCodeEntry is a minted authorization code awaiting exchange at /token.
// Persisted in the registry rather than held in process memory, so redemption
// works whether it lands on the same rekam process that minted it or not —
// see the oauth_codes/oauth_consents comment in migrate().
type OAuthCodeEntry struct {
	CodeChallenge string
	ClientID      string
	IdentityID    string
	TeamID        string
	Expiry        time.Time
}

// StoreOAuthCode persists a freshly minted authorization code. It opportunistically
// sweeps already-expired codes first, so an abandoned /authorize flow (password
// entered, code minted, redirect never followed) doesn't accumulate forever.
func (r *Registry) StoreOAuthCode(code string, e OAuthCodeEntry) error {
	if _, err := r.db.Exec(`DELETE FROM oauth_codes WHERE expires_at < ?`, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	_, err := r.db.Exec(`
		INSERT INTO oauth_codes (code, code_challenge, client_id, identity_id, team_id, expires_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, code, e.CodeChallenge, e.ClientID, e.IdentityID, e.TeamID, e.Expiry.UTC().Format(time.RFC3339))
	return err
}

// TakeOAuthCode atomically loads and deletes a code in one statement — the same
// single-use guarantee sync.Map.LoadAndDelete gave when this lived in memory,
// now enforced by SQLite rather than by "only one process ever touches it".
// ok is false for an unknown, already-redeemed, or expired code; an expired
// code is still consumed (so a retry can't succeed either) but reported not ok.
func (r *Registry) TakeOAuthCode(code string) (OAuthCodeEntry, bool, error) {
	row := r.db.QueryRow(`
		DELETE FROM oauth_codes WHERE code = ?
		RETURNING code_challenge, client_id, identity_id, team_id, expires_at
	`, code)
	var e OAuthCodeEntry
	var expiresAt string
	if err := row.Scan(&e.CodeChallenge, &e.ClientID, &e.IdentityID, &e.TeamID, &expiresAt); err != nil {
		if err == sql.ErrNoRows {
			return OAuthCodeEntry{}, false, nil
		}
		return OAuthCodeEntry{}, false, err
	}
	e.Expiry, _ = time.Parse(time.RFC3339, expiresAt)
	if time.Now().After(e.Expiry) {
		return OAuthCodeEntry{}, false, nil
	}
	return e, true, nil
}

// ConsentTicketEntry is the half-finished authorization between "password
// accepted" and "workspace chosen", for an identity with more than one
// workspace to pick from. Persisted for the same reason as OAuthCodeEntry.
type ConsentTicketEntry struct {
	IdentityID          string
	Teams               []string // team ids offered, so the submitted choice can be checked against them
	RedirectURI         string
	State               string
	CodeChallenge       string
	CodeChallengeMethod string
	ClientID            string
	Expiry              time.Time
}

// StoreConsentTicket persists the workspace-picker ticket; see StoreOAuthCode
// for why (and for the same expired-row sweep).
func (r *Registry) StoreConsentTicket(ticket string, e ConsentTicketEntry) error {
	if _, err := r.db.Exec(`DELETE FROM oauth_consents WHERE expires_at < ?`, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	teamsJSON, err := json.Marshal(e.Teams)
	if err != nil {
		return err
	}
	_, err = r.db.Exec(`
		INSERT INTO oauth_consents
			(ticket, identity_id, teams_json, redirect_uri, state, code_challenge, code_challenge_method, client_id, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, ticket, e.IdentityID, string(teamsJSON), e.RedirectURI, e.State, e.CodeChallenge, e.CodeChallengeMethod, e.ClientID, e.Expiry.UTC().Format(time.RFC3339))
	return err
}

// TakeConsentTicket atomically loads and deletes a ticket — single-use, so a
// replayed workspace-picker form can't mint a second code. See TakeOAuthCode.
func (r *Registry) TakeConsentTicket(ticket string) (ConsentTicketEntry, bool, error) {
	row := r.db.QueryRow(`
		DELETE FROM oauth_consents WHERE ticket = ?
		RETURNING identity_id, teams_json, redirect_uri, state, code_challenge, code_challenge_method, client_id, expires_at
	`, ticket)
	var e ConsentTicketEntry
	var teamsJSON, expiresAt string
	if err := row.Scan(&e.IdentityID, &teamsJSON, &e.RedirectURI, &e.State, &e.CodeChallenge, &e.CodeChallengeMethod, &e.ClientID, &expiresAt); err != nil {
		if err == sql.ErrNoRows {
			return ConsentTicketEntry{}, false, nil
		}
		return ConsentTicketEntry{}, false, err
	}
	if err := json.Unmarshal([]byte(teamsJSON), &e.Teams); err != nil {
		return ConsentTicketEntry{}, false, err
	}
	e.Expiry, _ = time.Parse(time.RFC3339, expiresAt)
	if time.Now().After(e.Expiry) {
		return ConsentTicketEntry{}, false, nil
	}
	return e, true, nil
}

// StoreSession persists a browser session, keyed by the SHA-256 hash of its
// opaque id (the raw id is what's in the cookie; it's never itself stored).
// Opportunistically sweeps already-expired sessions first, the same way
// StoreOAuthCode does, so logged-out-by-expiry sessions don't accumulate.
func (r *Registry) StoreSession(rawID, identityID string, expiry time.Time) error {
	if _, err := r.db.Exec(`DELETE FROM sessions WHERE expires_at < ?`, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	_, err := r.db.Exec(`
		INSERT INTO sessions (id_hash, identity_id, expires_at) VALUES (?, ?, ?)
		ON CONFLICT (id_hash) DO UPDATE SET identity_id = excluded.identity_id, expires_at = excluded.expires_at
	`, HashKey(rawID), identityID, expiry.UTC().Format(time.RFC3339))
	return err
}

// ResolveSession looks up a session by its raw (cookie) id. Unlike
// TakeOAuthCode this does not delete on read — a session is meant to be reused
// until logout or expiry, not single-use. ok is false for an unknown or
// expired session; an expired row is left for the next StoreSession/DeleteSession
// sweep rather than deleted here, keeping this a plain read.
func (r *Registry) ResolveSession(rawID string) (identityID string, ok bool, err error) {
	var expiresAt string
	row := r.db.QueryRow(`SELECT identity_id, expires_at FROM sessions WHERE id_hash = ?`, HashKey(rawID))
	if err := row.Scan(&identityID, &expiresAt); err != nil {
		if err == sql.ErrNoRows {
			return "", false, nil
		}
		return "", false, err
	}
	expiry, _ := time.Parse(time.RFC3339, expiresAt)
	if time.Now().After(expiry) {
		return "", false, nil
	}
	return identityID, true, nil
}

// DeleteSession drops a session server-side (logout). Deleting an unknown id
// is not an error — logout is idempotent.
func (r *Registry) DeleteSession(rawID string) error {
	_, err := r.db.Exec(`DELETE FROM sessions WHERE id_hash = ?`, HashKey(rawID))
	return err
}

// Invite is a pending team invitation for an email that may not have an
// identity yet. ReadGrants/TitleGrants mirror Member's grants (see
// db.Member) — they're staged here and only take effect once PutMember runs
// at accept time.
type Invite struct {
	TeamID      string
	Email       string
	Role        Role
	ReadGrants  []string
	TitleGrants []string
	InvitedBy   string
	CreatedAt   time.Time
	Expiry      time.Time
}

// StoreInvite generates and persists a pending invite, keyed by the SHA-256
// hash of the raw token it returns (the raw token itself is never stored,
// same as a session id — surfaced once, like SetConfirmToken/SetResetToken).
// Opportunistically sweeps already-expired invites first, same as
// StoreSession/StoreOAuthCode.
func (r *Registry) StoreInvite(teamID, email string, role Role, readGrants, titleGrants []string, invitedBy string, ttl time.Duration) (string, error) {
	if _, err := r.db.Exec(`DELETE FROM team_invites WHERE expires_at < ?`, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return "", err
	}
	token, err := r.randomHex(24)
	if err != nil {
		return "", err
	}
	readJSON, err := json.Marshal(readGrants)
	if err != nil {
		return "", err
	}
	titleJSON, err := json.Marshal(titleGrants)
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	_, err = r.db.Exec(`
		INSERT INTO team_invites (token_hash, team_id, email, role, read_grants, title_grants, invited_by, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, HashKey(token), teamID, email, string(role), string(readJSON), string(titleJSON), nullStr(invitedBy),
		now.Format(time.RFC3339), now.Add(ttl).Format(time.RFC3339))
	if err != nil {
		return "", err
	}
	return token, nil
}

func scanInvite(row interface{ Scan(...any) error }) (*Invite, error) {
	var inv Invite
	var role, readJSON, titleJSON, createdAt, expiresAt string
	var invitedBy sql.NullString
	if err := row.Scan(&inv.TeamID, &inv.Email, &role, &readJSON, &titleJSON, &invitedBy, &createdAt, &expiresAt); err != nil {
		return nil, err
	}
	inv.Role = Role(role)
	inv.InvitedBy = invitedBy.String
	if err := json.Unmarshal([]byte(readJSON), &inv.ReadGrants); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(titleJSON), &inv.TitleGrants); err != nil {
		return nil, err
	}
	inv.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	inv.Expiry, _ = time.Parse(time.RFC3339, expiresAt)
	return &inv, nil
}

// ResolveInvite looks up a pending invite by its raw token. Unlike
// TakeOAuthCode this does not delete on read — the token is looked up once to
// show the invite (e.g. team name) and again to accept it; DeleteInvite is the
// single-use step. ok is false for an unknown or expired invite.
func (r *Registry) ResolveInvite(rawToken string) (*Invite, bool, error) {
	row := r.db.QueryRow(`
		SELECT team_id, email, role, read_grants, title_grants, invited_by, created_at, expires_at
		FROM team_invites WHERE token_hash = ?
	`, HashKey(rawToken))
	inv, err := scanInvite(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, false, nil
		}
		return nil, false, err
	}
	if time.Now().After(inv.Expiry) {
		return nil, false, nil
	}
	return inv, true, nil
}

// DeleteInvite removes a pending invite (accepted, or revoked by a manager).
// Deleting an unknown token is not an error.
func (r *Registry) DeleteInvite(rawToken string) error {
	_, err := r.db.Exec(`DELETE FROM team_invites WHERE token_hash = ?`, HashKey(rawToken))
	return err
}

func (r *Registry) Stats() (*Stats, error) {
	var st Stats
	queries := []struct {
		sql  string
		dest *int
	}{
		{`SELECT COUNT(*) FROM identities`, &st.Identities},
		{`SELECT COUNT(*) FROM grants`, &st.Grants},
		{`SELECT COUNT(*) FROM grants WHERE revoked_at IS NULL`, &st.ActiveGrants},
		{`SELECT COUNT(DISTINCT memory_path) FROM identities`, &st.MemoryPaths},
	}
	for _, q := range queries {
		if err := r.db.QueryRow(q.sql).Scan(q.dest); err != nil {
			return nil, err
		}
	}
	return &st, nil
}

// Close closes the underlying database connection.
func (r *Registry) Close() error { return r.db.Close() }

func scanIdentity(s interface{ Scan(...any) error }) (*Identity, error) {
	var id Identity
	var createdAt string
	var allowWrite, isAdmin int
	var email sql.NullString
	err := s.Scan(&id.ID, &id.Name, &id.APIKeyHash, &id.MemoryPath,
		&allowWrite, &createdAt, &email, &isAdmin)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("unknown API key")
	}
	if err != nil {
		return nil, err
	}
	id.AllowWrite = allowWrite == 1
	id.IsAdmin = isAdmin == 1
	id.Email = email.String
	id.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	return &id, nil
}

func scanIdentityRow(rows *sql.Rows) (*Identity, error) {
	var id Identity
	var createdAt string
	var allowWrite, isAdmin int
	var email sql.NullString
	err := rows.Scan(&id.ID, &id.Name, &id.APIKeyHash, &id.MemoryPath,
		&allowWrite, &createdAt, &email, &isAdmin)
	if err != nil {
		return nil, err
	}
	id.AllowWrite = allowWrite == 1
	id.IsAdmin = isAdmin == 1
	id.Email = email.String
	id.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	return &id, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
