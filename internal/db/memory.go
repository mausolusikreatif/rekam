package db

import (
	"database/sql"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
)

// sanitizeFTSQuery turns arbitrary user text into a safe FTS5 MATCH expression by
// extracting word tokens and quoting each as a string literal, AND-ed together.
// This neutralizes FTS5 operators (- : * " ( ) etc.) that would otherwise raise a
// syntax error or silently change semantics — e.g. a title like "Wiki-Links".
// Returns "" when there are no usable tokens, signalling the caller to skip the query.
func sanitizeFTSQuery(q string) string {
	fields := strings.FieldsFunc(q, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	if len(fields) == 0 {
		return ""
	}
	for i, f := range fields {
		fields[i] = `"` + f + `"`
	}
	return strings.Join(fields, " ")
}

// Memory represents a single memory record.
type Memory struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Content  string `json:"content,omitempty"`
	Taxonomy string `json:"taxonomy,omitempty"`
	// Format is the content type of Content: markdown (default), mermaid, code,
	// or table. It tells renderers/exporters how to interpret the text; Content
	// stays plain text so FTS and the link graph work across all formats.
	Format    string `json:"format,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	// Version counts committed edits, starting at 1 on insert. Callers pass the
	// version they read back on update to detect a concurrent write (see
	// VersionConflictError); revisions holds one snapshot row per version.
	Version int `json:"version,omitempty"`
	// Backlinks is the number of other memories linking to this one. Populated
	// by Search as a ranking-evidence signal; zero/omitted elsewhere.
	Backlinks int `json:"backlinks,omitempty"`
	// Superseded is true when another memory supersedes this one. Populated by
	// Search so the agent can tell a stale record from the current one.
	Superseded bool `json:"superseded,omitempty"`
	// LinkedFrom lists the memories that link to this one. Populated on demand
	// by the engine for get_memory; nil/omitted elsewhere.
	LinkedFrom []*ScopedMemory `json:"linked_from,omitempty"`
	// Links reports how this memory's outgoing [[wiki-links]] resolved. Populated
	// by Insert/Update so a writer gets immediate feedback on which links landed
	// on an existing memory and which are still dangling; omitted on read paths.
	Links []LinkStatus `json:"links,omitempty"`
	// InReview is true when this memory is enrolled in the spaced-repetition deck.
	// Populated on demand by GetMemory; omitted elsewhere.
	InReview bool `json:"in_review,omitempty"`
}

// LinkStatus reports the resolution of one outgoing wiki-link at write time. A
// dangling link (Resolved=false) is kept and resolves automatically once a memory
// with that title exists — but surfacing it lets the writer fix a typo'd title now.
type LinkStatus struct {
	Target   string `json:"target"`   // normalized link target (the bracketed title)
	Rel      string `json:"rel"`      // relation type: relates / depends-on / supersedes / contradicts
	Resolved bool   `json:"resolved"` // true if a memory with that title currently exists
	// Deleted is true when this link is still bound to a memory that has since
	// been deleted. Such a link is neither resolved (there is nothing to read)
	// nor dangling (the target is known) — it points at a tombstone.
	Deleted bool `json:"deleted,omitempty"`
}

// Revision is the full state of a memory at one version. Every version has
// exactly one row, including the current one, so history is a complete series
// rather than an undo log: restoring version N means copying its fields forward
// as a new version, never rewinding the counter.
type Revision struct {
	MemoryID string `json:"memory_id"`
	Version  int    `json:"version"`
	Title    string `json:"title"`
	Content  string `json:"content,omitempty"`
	Taxonomy string `json:"taxonomy,omitempty"`
	Format   string `json:"format,omitempty"`
	// AuthorID is the identity that committed this version. Empty for rows
	// backfilled by the migration that introduced versioning, where the writer
	// predates authorship tracking and is genuinely unknown.
	AuthorID string `json:"author_id,omitempty"`
	// Agent is the model or system that made this write, e.g. "claude-opus-5" —
	// self-reported by whichever caller invoked write_memory/update_memory, the
	// same trust model as a git commit's Co-Authored-By trailer: not verified,
	// but meaningful by convention. Empty reads as "a human wrote this
	// directly" (the web editor never sets it), not "unknown" — every non-MCP
	// write path naturally produces that value, so it's the right default.
	Agent     string `json:"agent,omitempty"`
	CreatedAt string `json:"created_at"`
}

// Tombstone is the permanent record left behind by a deleted memory. The
// content and every revision are purged, but the address stays reserved forever:
// the id never resolves to a live memory again, so a link or reference captured
// before the deletion can still be told "this existed and was removed" rather
// than the ambiguous "no such memory".
type Tombstone struct {
	MemoryID string `json:"memory_id"`
	// Title and Taxonomy are retained so the audit trail names what went; a
	// tombstone that only says "something was deleted somewhere" is not an audit.
	Title     string `json:"title"`
	Taxonomy  string `json:"taxonomy,omitempty"`
	CreatedAt string `json:"created_at"` // when the memory was originally written
	DeletedAt string `json:"deleted_at"`
	DeletedBy string `json:"deleted_by,omitempty"`
	// FinalVersion is the version the memory had when deleted. The revisions
	// themselves are gone; this records how much history was destroyed.
	FinalVersion int `json:"final_version"`
}

// DeletedError reports that an id addresses a deleted memory. It is distinct
// from a plain not-found: the address is known, permanently reserved, and the
// tombstone says who removed it and when.
type DeletedError struct {
	Tombstone *Tombstone
}

func (e *DeletedError) Error() string {
	return fmt.Sprintf("memory %s was deleted at %s and cannot be used again",
		e.Tombstone.MemoryID, e.Tombstone.DeletedAt)
}

// VersionConflictError reports that a memory changed between the read a caller
// based its edit on and the write. The caller should re-read and reapply rather
// than retry blindly, since the intervening version may conflict semantically.
type VersionConflictError struct {
	ID      string
	Base    int // the version the caller edited against
	Current int // the version actually in the database
}

func (e *VersionConflictError) Error() string {
	return fmt.Sprintf("memory %s changed since version %d (now %d)", e.ID, e.Base, e.Current)
}

// TitleConflictError reports that a write's title collides, case/whitespace-
// insensitively, with another live memory's title. syncEdges resolves a
// [[Title]] link with a single `LIMIT 1` query — once two memories share a
// title, which one a link actually hits is undefined, silently, forever
// after. Blocking the write at the source is the only fix that doesn't
// require guessing which memory a stale edge "really" meant.
type TitleConflictError struct {
	Title string // the title that collided
	ID    string // the other memory that already holds it
}

func (e *TitleConflictError) Error() string {
	return fmt.Sprintf("title %q is already used by memory %s — wiki-links resolve by title, so two memories cannot share one", e.Title, e.ID)
}

// titleConflict reports the id of another live memory (not excludeID)
// already holding title, normalized the same way syncEdges resolves
// [[links]] (lower-cased, trimmed) — so this check and that resolution never
// disagree about what counts as "the same title".
func titleConflict(q execQuerier, title, excludeID string) (string, bool, error) {
	var id string
	err := q.QueryRow(
		`SELECT id FROM memories WHERE lower(trim(title)) = ? AND id != ? LIMIT 1`,
		strings.ToLower(strings.TrimSpace(title)), excludeID,
	).Scan(&id)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return id, true, nil
}

// SearchResult bundles search hits with budget metadata.
type SearchResult struct {
	Results      []*Memory `json:"results"`
	OmittedCount int       `json:"omitted_count"`
}

// MemoryDB manages a single *.memory SQLite file.
type MemoryDB struct {
	db *sql.DB

	// path is the SQLite file this instance was opened from — recorded so
	// DiskSize can stat it (and its WAL/SHM siblings) without every caller
	// having to carry the path around separately.
	path string

	// externallyCheckpointed is true when a replicator owns this file's
	// checkpoint timing instead of Close — see Close and db.ReplicatedOpen.
	externallyCheckpointed bool
}

// OpenMemory opens (or creates) a *.memory SQLite file.
func OpenMemory(path string) (*MemoryDB, error) {
	db, err := openDB(path)
	if err != nil {
		return nil, fmt.Errorf("open rekam %s: %w", path, err)
	}
	m := &MemoryDB{db: db, path: path}
	if err := m.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate rekam %s: %w", path, err)
	}
	return m, nil
}

// DiskSize reports how many bytes this tenant's file occupies on disk —
// the main file plus its WAL/SHM siblings if present (WAL mode means a
// recent write can sit in "-wal" before checkpointing, so the main file
// alone would understate actual usage). Used as an abuse backstop: a ceiling
// on one tenant filling the disk, not a precise billing metric. A path that
// was never set (e.g. a MemoryDB opened before this field existed, though
// nothing in this codebase does that today) reports 0 rather than erroring.
func (m *MemoryDB) DiskSize() (int64, error) {
	return FileSize(m.path)
}

// FileSize reports how many bytes a *.rekam/*.memory file occupies on disk,
// including its WAL/SHM sidecars (WAL mode can leave a recent write sitting
// in "-wal" before checkpointing, so the main file alone would understate
// actual usage). Works from a bare path, without opening the database —
// DiskSize wraps this for an already-open handle; a caller that only has a
// path (e.g. an admin listing over many tenant files) can call it directly
// rather than paying to open/close each one just to stat it.
func FileSize(path string) (int64, error) {
	if path == "" {
		return 0, nil
	}
	var total int64
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		fi, err := os.Stat(p)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return 0, fmt.Errorf("stat %s: %w", p, err)
		}
		total += fi.Size()
	}
	return total, nil
}

func (m *MemoryDB) migrate() error {
	if err := m.migrateDropLegacyColumns(); err != nil {
		return fmt.Errorf("legacy column migration: %w", err)
	}
	if err := m.migrateFTSSchema(); err != nil {
		return fmt.Errorf("fts migration: %w", err)
	}
	if err := m.migrateEdgesSchema(); err != nil {
		return fmt.Errorf("edges migration: %w", err)
	}

	stmts := []string{
		`CREATE TABLE IF NOT EXISTS memories (
			id          TEXT PRIMARY KEY,
			title       TEXT NOT NULL,
			content     TEXT,
			taxonomy    TEXT,
			format      TEXT NOT NULL DEFAULT 'markdown',
			created_at  TEXT NOT NULL,
			updated_at  TEXT NOT NULL
		)`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS memories_fts USING fts5(
			title, content
		)`,
		`CREATE TRIGGER IF NOT EXISTS memories_ai AFTER INSERT ON memories BEGIN
			INSERT INTO memories_fts(rowid, title, content)
			VALUES (new.rowid, new.title, new.content);
		END`,
		`CREATE TRIGGER IF NOT EXISTS memories_ad AFTER DELETE ON memories BEGIN
			DELETE FROM memories_fts WHERE rowid = old.rowid;
		END`,
		`CREATE TRIGGER IF NOT EXISTS memories_au AFTER UPDATE ON memories BEGIN
			DELETE FROM memories_fts WHERE rowid = old.rowid;
			INSERT INTO memories_fts(rowid, title, content)
			VALUES (new.rowid, new.title, new.content);
		END`,
		`CREATE TABLE IF NOT EXISTS edges (
			src_id  TEXT NOT NULL,
			dst_id  TEXT,
			raw     TEXT NOT NULL,
			rel     TEXT NOT NULL DEFAULT 'relates',
			PRIMARY KEY (src_id, raw, rel),
			FOREIGN KEY (src_id) REFERENCES memories(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_edges_dst ON edges(dst_id)`,
		`CREATE INDEX IF NOT EXISTS idx_edges_raw ON edges(raw)`,
		`CREATE TABLE IF NOT EXISTS srs (
			memory_id     TEXT PRIMARY KEY,
			ease          REAL NOT NULL DEFAULT 2.5,
			interval_days INTEGER NOT NULL DEFAULT 0,
			reps          INTEGER NOT NULL DEFAULT 0,
			due_at        TEXT NOT NULL,
			last_reviewed TEXT,
			FOREIGN KEY (memory_id) REFERENCES memories(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_srs_due ON srs(due_at)`,
		`CREATE TABLE IF NOT EXISTS revisions (
			memory_id  TEXT NOT NULL,
			version    INTEGER NOT NULL,
			title      TEXT NOT NULL,
			content    TEXT,
			taxonomy   TEXT,
			format     TEXT NOT NULL DEFAULT 'markdown',
			author_id  TEXT,
			agent      TEXT,
			created_at TEXT NOT NULL,
			PRIMARY KEY (memory_id, version),
			FOREIGN KEY (memory_id) REFERENCES memories(id) ON DELETE CASCADE
		)`,
		// No foreign key to memories: a tombstone outlives the row it describes,
		// and must never be removed by the cascade that deletes it.
		`CREATE TABLE IF NOT EXISTS tombstones (
			memory_id     TEXT PRIMARY KEY,
			title         TEXT NOT NULL,
			taxonomy      TEXT,
			created_at    TEXT NOT NULL,
			deleted_at    TEXT NOT NULL,
			deleted_by    TEXT,
			final_version INTEGER NOT NULL
		)`,
		// taxonomy_template is the corpus's declared classification scaffold: a set
		// of branch paths with "what goes here" descriptions, surfaced to humans in
		// the picker and to agents as a fixed vocabulary. Empty means "use the
		// shipped default"; the first edit persists rows here and takes over.
		`CREATE TABLE IF NOT EXISTS taxonomy_template (
			path        TEXT PRIMARY KEY,
			description TEXT NOT NULL DEFAULT '',
			sort        INTEGER NOT NULL DEFAULT 0,
			updated_at  TEXT NOT NULL
		)`,
	}
	for _, s := range stmts {
		if _, err := m.db.Exec(s); err != nil {
			return fmt.Errorf("migrate statement %q: %w", s[:min(40, len(s))], err)
		}
	}
	if err := m.migrateAddFormatColumn(); err != nil {
		return err
	}
	if err := m.migrateAddVersionColumn(); err != nil {
		return err
	}
	if err := m.migrateAddAgentColumn(); err != nil {
		return err
	}
	return m.migrateMembers()
}

// migrateAddAgentColumn adds revisions.agent to pre-existing tables. New
// tables already declare it. Pre-existing revision rows carry a NULL agent —
// unreported, not "definitely human"; see Revision.Agent's doc comment.
func (m *MemoryDB) migrateAddAgentColumn() error {
	var count int
	err := m.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('revisions') WHERE name = 'agent'`).Scan(&count)
	if err != nil {
		return fmt.Errorf("check agent column: %w", err)
	}
	if count > 0 {
		return nil
	}
	if _, err := m.db.Exec(`ALTER TABLE revisions ADD COLUMN agent TEXT`); err != nil {
		return fmt.Errorf("add agent column: %w", err)
	}
	return nil
}

// migrateAddVersionColumn adds memories.version and seeds revisions with a
// version-1 snapshot for every pre-existing memory. Without the backfill the
// invariant "every version has a revision row" would not hold for rows written
// before versioning, leaving their history empty and restore with nothing to
// copy. Seeded rows carry a NULL author_id: the original writer is unknowable.
func (m *MemoryDB) migrateAddVersionColumn() error {
	var count int
	err := m.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('memories') WHERE name = 'version'`).Scan(&count)
	if err != nil {
		return fmt.Errorf("check version column: %w", err)
	}
	if count > 0 {
		return nil
	}
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`ALTER TABLE memories ADD COLUMN version INTEGER NOT NULL DEFAULT 1`); err != nil {
		return fmt.Errorf("add version column: %w", err)
	}
	if _, err := tx.Exec(`
		INSERT INTO revisions (memory_id, version, title, content, taxonomy, format, author_id, created_at)
		SELECT id, 1, title, content, taxonomy, format, NULL, updated_at FROM memories
	`); err != nil {
		return fmt.Errorf("backfill revisions: %w", err)
	}
	return tx.Commit()
}

// migrateAddFormatColumn adds the memories.format discriminator to pre-existing
// tables. New tables already declare it; this backfills old ones, where every
// row defaults to 'markdown' (the prior implicit content type).
func (m *MemoryDB) migrateAddFormatColumn() error {
	var count int
	err := m.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('memories') WHERE name = 'format'`).Scan(&count)
	if err != nil {
		return fmt.Errorf("check format column: %w", err)
	}
	if count > 0 {
		return nil
	}
	if _, err := m.db.Exec(`ALTER TABLE memories ADD COLUMN format TEXT NOT NULL DEFAULT 'markdown'`); err != nil {
		return fmt.Errorf("add format column: %w", err)
	}
	return nil
}

// migrateDropLegacyColumns detects the old schema (with description, keywords,
// status, state, created_by, updated_by) and migrates to the new lean schema.
func (m *MemoryDB) migrateDropLegacyColumns() error {
	var count int
	err := m.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('memories') WHERE name = 'status'`).Scan(&count)
	if err != nil || count == 0 {
		return nil // table doesn't exist yet or already migrated
	}

	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmts := []string{
		`CREATE TABLE memories_new (
			id         TEXT PRIMARY KEY,
			title      TEXT NOT NULL,
			content    TEXT,
			taxonomy   TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`INSERT INTO memories_new (id, title, content, taxonomy, created_at, updated_at)
		 SELECT id, title, content, taxonomy, created_at, updated_at FROM memories`,
		`DROP TABLE memories`,
		`ALTER TABLE memories_new RENAME TO memories`,
		`DROP TRIGGER IF EXISTS memories_ai`,
		`DROP TRIGGER IF EXISTS memories_ad`,
		`DROP TRIGGER IF EXISTS memories_au`,
		`DROP TABLE IF EXISTS memories_fts`,
		`DROP TABLE IF EXISTS memory_changelog`,
	}
	for _, s := range stmts {
		if _, err := tx.Exec(s); err != nil {
			return fmt.Errorf("legacy migration %q: %w", s[:min(40, len(s))], err)
		}
	}
	return tx.Commit()
}

// migrateFTSSchema detects an old FTS schema (with description, keywords columns
// or content-table mode) and rebuilds it as standalone with just title+content.
func (m *MemoryDB) migrateFTSSchema() error {
	var schema string
	err := m.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='memories_fts'`).Scan(&schema)
	if err == sql.ErrNoRows {
		return nil // table doesn't exist yet; migrate() will create it
	}
	if err != nil {
		return err
	}

	needsRebuild := strings.Contains(schema, "content=") ||
		strings.Contains(schema, "description") ||
		strings.Contains(schema, "keywords")

	if !needsRebuild {
		return nil
	}

	for _, drop := range []string{
		`DROP TRIGGER IF EXISTS memories_ai`,
		`DROP TRIGGER IF EXISTS memories_ad`,
		`DROP TRIGGER IF EXISTS memories_au`,
		`DROP TABLE IF EXISTS memories_fts`,
	} {
		if _, err := m.db.Exec(drop); err != nil {
			return err
		}
	}

	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`CREATE VIRTUAL TABLE memories_fts USING fts5(title, content)`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO memories_fts(rowid, title, content) SELECT rowid, title, content FROM memories`); err != nil {
		return err
	}
	return tx.Commit()
}

// migrateEdgesSchema drops a pre-typed-relations edges table (PK without rel) so
// the new schema is recreated. edges is a derived index; it is repopulated by
// subsequent writes or RebuildEdges (exposed via rebuild_fts).
func (m *MemoryDB) migrateEdgesSchema() error {
	var schema string
	err := m.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='edges'`).Scan(&schema)
	if err == sql.ErrNoRows {
		return nil // doesn't exist yet; migrate() creates it
	}
	if err != nil {
		return err
	}
	if strings.Contains(schema, "raw, rel") {
		return nil // already the typed-relations shape
	}
	_, err = m.db.Exec(`DROP TABLE IF EXISTS edges`)
	return err
}

// Insert writes a new memory at version 1, attributed to authorID (which may be
// empty in single-user contexts) and optionally agent (the model that made the
// call, self-reported — see Revision.Agent). The ID and timestamps are set here.
func (m *MemoryDB) Insert(mem *Memory, authorID, agent string) error {
	mem.ID = uuid.New().String()
	now := time.Now().UTC().Format(time.RFC3339)
	mem.CreatedAt = now
	mem.UpdatedAt = now
	mem.Version = 1

	tx, err := m.db.Begin()
	if err != nil {
		return fmt.Errorf("insert memory: %w", err)
	}
	defer tx.Rollback()

	if otherID, conflict, err := titleConflict(tx, mem.Title, mem.ID); err != nil {
		return fmt.Errorf("insert memory: %w", err)
	} else if conflict {
		return &TitleConflictError{Title: mem.Title, ID: otherID}
	}

	if _, err := tx.Exec(`
		INSERT INTO memories (id, title, content, taxonomy, format, created_at, updated_at, version)
		VALUES (?,?,?,?,?,?,?,?)
	`, mem.ID, mem.Title, nullStr(mem.Content), nullStr(mem.Taxonomy), formatOrDefault(mem.Format), now, now, mem.Version); err != nil {
		return fmt.Errorf("insert memory: %w", err)
	}
	if err := insertRevision(tx, mem, authorID, agent, now); err != nil {
		return fmt.Errorf("insert memory revision: %w", err)
	}
	links, err := syncEdges(tx, mem.ID, mem.Title, mem.Content)
	if err != nil {
		return fmt.Errorf("insert memory edges: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("insert memory: %w", err)
	}
	mem.Links = links
	return nil
}

// insertRevision snapshots a memory's durable columns as the row for its current
// version. Derived fields (backlinks, superseded, links) are deliberately not
// captured: they are functions of the graph at read time, not of this edit.
func insertRevision(tx *sql.Tx, mem *Memory, authorID, agent, at string) error {
	_, err := tx.Exec(`
		INSERT INTO revisions (memory_id, version, title, content, taxonomy, format, author_id, agent, created_at)
		VALUES (?,?,?,?,?,?,?,?,?)
	`, mem.ID, mem.Version, mem.Title, nullStr(mem.Content), nullStr(mem.Taxonomy),
		formatOrDefault(mem.Format), nullStr(authorID), nullStr(agent), at)
	return err
}

// Get fetches a single memory by ID. A deleted id yields *DeletedError rather
// than a plain miss, so callers can say "removed by X on Y" instead of leaving
// the reader to wonder whether the memory ever existed. The tombstone lookup
// costs an extra query only on the miss path.
func (m *MemoryDB) Get(id string) (*Memory, error) {
	row := m.db.QueryRow(selectMemory+` WHERE id = ?`, id)
	mem, err := scanMemory(row)
	if err != nil {
		if ts, tErr := m.Tombstone(id); tErr == nil && ts != nil {
			return nil, &DeletedError{Tombstone: ts}
		}
		return nil, fmt.Errorf("get rekam %s: %w", id, err)
	}
	return mem, nil
}

// Update applies partial changes to a memory, bumping its version and recording
// a revision snapshot attributed to authorID and optionally agent (see Insert).
//
// When base is non-nil the write is compare-and-swap: it fails with
// *VersionConflictError unless the stored version still equals *base, so a
// caller editing a stale copy cannot silently clobber the intervening write. A
// nil base skips the check (last-writer-wins), which is what single-user callers
// and clients predating versioning get; the revision is recorded either way, so
// the overwritten text remains recoverable.
func (m *MemoryDB) Update(id string, updates map[string]any, authorID, agent string, base *int) (*Memory, error) {
	tx, err := m.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("update rekam %s: %w", id, err)
	}
	defer tx.Rollback()

	var current int
	err = tx.QueryRow(`SELECT version FROM memories WHERE id = ?`, id).Scan(&current)
	if err == sql.ErrNoRows {
		// A burned address never becomes writable again.
		if ts, tErr := tombstoneTx(tx, id); tErr == nil && ts != nil {
			return nil, &DeletedError{Tombstone: ts}
		}
		return nil, fmt.Errorf("update rekam %s: memory not found", id)
	}
	if err != nil {
		return nil, fmt.Errorf("update rekam %s: %w", id, err)
	}
	if base != nil && *base != current {
		return nil, &VersionConflictError{ID: id, Base: *base, Current: current}
	}
	if newTitle, ok := updates["title"].(string); ok {
		if otherID, conflict, err := titleConflict(tx, newTitle, id); err != nil {
			return nil, fmt.Errorf("update rekam %s: %w", id, err)
		} else if conflict {
			return nil, &TitleConflictError{Title: newTitle, ID: otherID}
		}
	}

	now := time.Now().UTC().Format(time.RFC3339)
	setClauses := []string{"updated_at = ?", "version = version + 1"}
	args := []any{now}

	// Sort the columns so the generated statement is stable across calls; Go map
	// iteration order is randomized, which would otherwise emit a different SQL
	// string (and a different prepared statement) for the same logical update.
	cols := make([]string, 0, len(updates))
	for col := range updates {
		cols = append(cols, col)
	}
	sort.Strings(cols)
	for _, col := range cols {
		setClauses = append(setClauses, col+" = ?")
		args = append(args, updates[col])
	}
	args = append(args, id)

	q := "UPDATE memories SET " + strings.Join(setClauses, ", ") + " WHERE id = ?"
	if _, err := tx.Exec(q, args...); err != nil {
		return nil, fmt.Errorf("update rekam %s: %w", id, err)
	}

	// Re-parse outgoing edges from the post-update title+content, and let this
	// memory's (possibly new) title satisfy any dangling links pointing at it.
	mem, err := scanMemory(tx.QueryRow(selectMemory+` WHERE id = ?`, id))
	if err != nil {
		return nil, fmt.Errorf("update rekam %s: %w", id, err)
	}
	if err := insertRevision(tx, mem, authorID, agent, now); err != nil {
		return nil, fmt.Errorf("update rekam revision %s: %w", id, err)
	}
	links, err := syncEdges(tx, mem.ID, mem.Title, mem.Content)
	if err != nil {
		return nil, fmt.Errorf("update rekam edges %s: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("update rekam %s: %w", id, err)
	}
	mem.Links = links
	return mem, nil
}

// Revisions returns a memory's full history, newest version first.
func (m *MemoryDB) Revisions(memoryID string) ([]*Revision, error) {
	rows, err := m.db.Query(`
		SELECT memory_id, version, title, content, taxonomy, format, author_id, agent, created_at
		FROM revisions WHERE memory_id = ? ORDER BY version DESC`, memoryID)
	if err != nil {
		return nil, fmt.Errorf("revisions %s: %w", memoryID, err)
	}
	defer rows.Close()

	var out []*Revision
	for rows.Next() {
		rev, err := scanRevision(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("revisions %s: %w", memoryID, err)
		}
		out = append(out, rev)
	}
	return out, rows.Err()
}

// RevisionAt returns a single version of a memory.
func (m *MemoryDB) RevisionAt(memoryID string, version int) (*Revision, error) {
	row := m.db.QueryRow(`
		SELECT memory_id, version, title, content, taxonomy, format, author_id, agent, created_at
		FROM revisions WHERE memory_id = ? AND version = ?`, memoryID, version)
	rev, err := scanRevision(row.Scan)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("revision %d of %s not found", version, memoryID)
	}
	if err != nil {
		return nil, fmt.Errorf("revision %d of %s: %w", version, memoryID, err)
	}
	return rev, nil
}

// scanRevision reads one revision row from either a *sql.Row or *sql.Rows by
// taking their common Scan method.
func scanRevision(scan func(...any) error) (*Revision, error) {
	var r Revision
	var content, taxonomy, author, agent sql.NullString
	if err := scan(&r.MemoryID, &r.Version, &r.Title, &content, &taxonomy,
		&r.Format, &author, &agent, &r.CreatedAt); err != nil {
		return nil, err
	}
	r.Content = content.String
	r.Taxonomy = taxonomy.String
	r.AuthorID = author.String
	r.Agent = agent.String
	return &r, nil
}

// Delete removes a memory by ID. Outgoing edges are dropped via ON DELETE
// CASCADE; inbound edges are reset to dangling so they re-resolve if a memory
// with the same title is created later.
// Delete permanently removes a memory and its entire revision history, leaving a
// tombstone in their place. This is terminal: the content is unrecoverable and
// the id is burned, so the memory can never be restored or the address reused.
// Callers wanting a reversible retirement should write a `supersedes` link
// instead, which keeps the old record readable while demoting it.
func (m *MemoryDB) Delete(id, deletedBy string) error {
	tx, err := m.db.Begin()
	if err != nil {
		return fmt.Errorf("delete rekam %s: %w", id, err)
	}
	defer tx.Rollback()

	mem, err := scanMemory(tx.QueryRow(selectMemory+` WHERE id = ?`, id))
	if err != nil {
		// Distinguish "never existed" from "already deleted" — deleting a
		// tombstoned id is not a no-op the caller should shrug off.
		if ts, tErr := tombstoneTx(tx, id); tErr == nil && ts != nil {
			return &DeletedError{Tombstone: ts}
		}
		return fmt.Errorf("memory not found: %s", id)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := tx.Exec(`
		INSERT INTO tombstones (memory_id, title, taxonomy, created_at, deleted_at, deleted_by, final_version)
		VALUES (?,?,?,?,?,?,?)
	`, mem.ID, mem.Title, nullStr(mem.Taxonomy), mem.CreatedAt, now, nullStr(deletedBy), mem.Version); err != nil {
		return fmt.Errorf("delete rekam tombstone %s: %w", id, err)
	}
	// Purge history explicitly rather than relying on the cascade, so the intent
	// (deletion destroys the record's past, not just its present) is on the page.
	if _, err := tx.Exec(`DELETE FROM revisions WHERE memory_id = ?`, id); err != nil {
		return fmt.Errorf("delete rekam revisions %s: %w", id, err)
	}
	// Inbound edges deliberately keep pointing at the burned id. Nulling them
	// would collapse "referred to this specific memory, which was deleted" into
	// "never referred to anything", after which only the title could identify the
	// target — and a new memory taking that title would silently inherit the
	// reference. Since the address is reserved forever, the pointer stays
	// unambiguous and resolves to the tombstone.
	if _, err := tx.Exec(`DELETE FROM memories WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete rekam %s: %w", id, err)
	}
	return tx.Commit()
}

// Tombstone returns the tombstone for an id, or nil when the id was never
// deleted (whether or not it currently names a live memory).
func (m *MemoryDB) Tombstone(id string) (*Tombstone, error) {
	return tombstoneTx(m.db, id)
}

func tombstoneTx(q execQuerier, id string) (*Tombstone, error) {
	var ts Tombstone
	var taxonomy, by sql.NullString
	err := q.QueryRow(`
		SELECT memory_id, title, taxonomy, created_at, deleted_at, deleted_by, final_version
		FROM tombstones WHERE memory_id = ?`, id).Scan(
		&ts.MemoryID, &ts.Title, &taxonomy, &ts.CreatedAt, &ts.DeletedAt, &by, &ts.FinalVersion)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	ts.Taxonomy = taxonomy.String
	ts.DeletedBy = by.String
	return &ts, nil
}

// Tombstones lists deletions, most recent first, optionally scoped to a taxonomy
// prefix. This is the audit view: what was removed, by whom, and when.
func (m *MemoryDB) Tombstones(scope Scope, taxonomyPrefix string, limit int) ([]*Tombstone, error) {
	q := `SELECT memory_id, title, taxonomy, created_at, deleted_at, deleted_by, final_version
	      FROM tombstones`
	var where []string
	var args []any
	if taxonomyPrefix != "" {
		where = append(where, "(taxonomy = ? OR taxonomy LIKE ?)")
		args = append(args, taxonomyPrefix, taxonomyPrefix+".%")
	}
	// A tombstone is existence-level (title, who, when), so the title clause: the
	// audit trail for a branch you cannot see stays hidden.
	if clause, cargs := scope.titleClause("taxonomy"); clause != "" {
		where = append(where, clause)
		args = append(args, cargs...)
	}
	if len(where) > 0 {
		q += ` WHERE ` + strings.Join(where, " AND ")
	}
	q += ` ORDER BY deleted_at DESC`
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := m.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("tombstones: %w", err)
	}
	defer rows.Close()

	var out []*Tombstone
	for rows.Next() {
		var ts Tombstone
		var taxonomy, by sql.NullString
		if err := rows.Scan(&ts.MemoryID, &ts.Title, &taxonomy, &ts.CreatedAt,
			&ts.DeletedAt, &by, &ts.FinalVersion); err != nil {
			return nil, fmt.Errorf("tombstones: %w", err)
		}
		ts.Taxonomy = taxonomy.String
		ts.DeletedBy = by.String
		out = append(out, &ts)
	}
	return out, rows.Err()
}

// TaxonomyEntry describes a taxonomy path with memory count.
// Expandable is true when the path is a rolled-up branch that has deeper
// sub-paths beneath it — call catalog again with this path as prefix to drill in.
type TaxonomyEntry struct {
	Path       string `json:"path"`
	Count      int    `json:"count"`
	Expandable bool   `json:"expandable,omitempty"`
}

// Catalog returns all taxonomy paths with memory count.
func (m *MemoryDB) Catalog(scope Scope) ([]TaxonomyEntry, error) {
	// A catalog entry is exactly existence + count, so it uses the title clause:
	// readable and title-visible branches appear, everything else stays absent
	// (not zero-counted — absent), so the tree never discloses a hidden branch.
	where := "taxonomy IS NOT NULL AND taxonomy != ''"
	clause, args := scope.titleClause("taxonomy")
	if clause != "" {
		where += " AND " + clause
	}
	rows, err := m.db.Query(`
		SELECT taxonomy, COUNT(*) AS cnt
		FROM memories
		WHERE `+where+`
		GROUP BY taxonomy
		ORDER BY taxonomy
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []TaxonomyEntry
	for rows.Next() {
		var e TaxonomyEntry
		if err := rows.Scan(&e.Path, &e.Count); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// Ranking knobs. FTS5 rank is negative (more negative = better), so the final
// sort key is `f.rank - boost + penalty`: subtracting moves a memory earlier,
// adding moves it later.
//   - authorityCap bounds how many boosting backlinks count, and authorityBoost
//     is the per-backlink nudge (max upward shift = authorityCap * authorityBoost).
//   - supersedePenalty is a strong fixed demotion applied to any memory that has
//     been superseded, so the stale version sinks below its replacement (and out
//     of the token budget) instead of polluting context.
const (
	authorityCap     = 5
	authorityBoost   = 0.3
	supersedePenalty = 5.0
)

// authorityExpr counts inbound boosting edges (relates / depends-on), excluding self.
const authorityExpr = `(SELECT COUNT(*) FROM edges e
	WHERE e.dst_id = m.id AND e.src_id != m.id AND e.rel IN ('relates','depends-on'))`

// supersededExpr is 1 when another memory supersedes this one, else 0.
const supersededExpr = `(SELECT EXISTS(SELECT 1 FROM edges e
	WHERE e.dst_id = m.id AND e.src_id != m.id AND e.rel = 'supersedes'))`

// Search executes an FTS5 query scoped to taxonomy prefix, blending BM25 relevance
// with backlink authority and a supersede penalty, respecting a token budget.
func (m *MemoryDB) Search(scope Scope, query, taxonomyPrefix string, tokenBudget int) (*SearchResult, error) {
	match := sanitizeFTSQuery(query)
	if match == "" {
		return &SearchResult{Results: []*Memory{}, OmittedCount: 0}, nil
	}

	const selectCols = `SELECT m.id, m.title, m.content, m.taxonomy, m.format, m.created_at, m.updated_at, ` +
		authorityExpr + ` AS backlinks, ` + supersededExpr + ` AS superseded`
	const orderBy = ` ORDER BY f.rank - min(` + authorityExpr + `, ?) * ? + ` + supersededExpr + ` * ?`

	// Scope filtering happens in the WHERE clause, before ranking and the token
	// budget, so an unreadable memory never consumes budget or shifts a rank —
	// filtering after the fact would leak restricted rows through OmittedCount and
	// through the BM25 scores of the rows that survive.
	where := []string{"memories_fts MATCH ?"}
	args := []any{match}
	if taxonomyPrefix != "" {
		where = append(where, "(m.taxonomy = ? OR m.taxonomy LIKE ?)")
		args = append(args, taxonomyPrefix, taxonomyPrefix+".%")
	}
	if clause, cargs := scope.readClause("m.taxonomy"); clause != "" {
		where = append(where, clause)
		args = append(args, cargs...)
	}
	args = append(args, authorityCap, authorityBoost, supersedePenalty)

	rows, err := m.db.Query(selectCols+`
		FROM memories_fts f
		JOIN memories m ON m.rowid = f.rowid
		WHERE `+strings.Join(where, " AND ")+orderBy, args...)
	if err != nil {
		return nil, fmt.Errorf("fts search: %w", err)
	}
	defer rows.Close()

	var results []*Memory
	budgetUsed := 0
	omitted := 0

	for rows.Next() {
		var mem Memory
		var content, taxonomy sql.NullString
		if err := rows.Scan(&mem.ID, &mem.Title, &content, &taxonomy, &mem.Format,
			&mem.CreatedAt, &mem.UpdatedAt, &mem.Backlinks, &mem.Superseded); err != nil {
			return nil, err
		}
		mem.Content = content.String
		mem.Taxonomy = taxonomy.String

		tokens := memoryTokens(&mem)
		if tokenBudget > 0 && budgetUsed+tokens > tokenBudget {
			omitted++
			continue
		}
		budgetUsed += tokens
		results = append(results, &mem)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return &SearchResult{Results: results, OmittedCount: omitted}, nil
}

// List returns all memories ordered by updated_at desc, optionally filtered by taxonomy.
func (m *MemoryDB) List(scope Scope, taxonomy string, limit, offset int) ([]*Memory, int, error) {
	var where []string
	var args []any

	if taxonomy != "" {
		where = append(where, "(taxonomy = ? OR taxonomy LIKE ?)")
		args = append(args, taxonomy, taxonomy+".%")
	}
	// Applied to both the COUNT and the page query, so total never counts rows
	// the caller cannot see.
	if sc, sargs := scope.readClause("taxonomy"); sc != "" {
		where = append(where, sc)
		args = append(args, sargs...)
	}

	clause := ""
	if len(where) > 0 {
		clause = " WHERE " + strings.Join(where, " AND ")
	}

	var total int
	if err := m.db.QueryRow("SELECT COUNT(*) FROM memories"+clause, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count memories: %w", err)
	}

	q := selectMemory + clause + ` ORDER BY updated_at DESC`
	pageArgs := append([]any(nil), args...)
	if limit > 0 {
		q += ` LIMIT ? OFFSET ?`
		pageArgs = append(pageArgs, limit, offset)
	}

	rows, err := m.db.Query(q, pageArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("list memories: %w", err)
	}
	defer rows.Close()

	var mems []*Memory
	for rows.Next() {
		mem, err := scanMemoryRow(rows)
		if err != nil {
			return nil, 0, err
		}
		mems = append(mems, mem)
	}
	return mems, total, rows.Err()
}

// ScopedMemory is a trimmed memory for lightweight browsing (no content).
type ScopedMemory struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Taxonomy string `json:"taxonomy,omitempty"`
	// Rel is the relation type of the link, set when the scoped memory represents
	// a backlink (e.g. "relates", "supersedes"); omitted for plain browsing.
	Rel string `json:"rel,omitempty"`
}

// ScopeResult bundles scoped memories with budget metadata.
type ScopeResult struct {
	Results      []*ScopedMemory `json:"results"`
	OmittedCount int             `json:"omitted_count"`
}

// Scope returns memories under a taxonomy prefix, without content.
func (m *MemoryDB) Scope(auth Scope, taxonomy string, tokenBudget int) (*ScopeResult, error) {
	where := "(taxonomy = ? OR taxonomy LIKE ?)"
	args := []any{taxonomy, taxonomy + ".%"}
	// Title-level view (id/title/taxonomy, no content), so the title clause: a
	// browse over a branch the caller may not read returns nothing.
	if clause, cargs := auth.titleClause("taxonomy"); clause != "" {
		where += " AND " + clause
		args = append(args, cargs...)
	}
	rows, err := m.db.Query(`
		SELECT id, title, taxonomy
		FROM memories
		WHERE `+where+`
		ORDER BY updated_at DESC
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("scope: %w", err)
	}
	defer rows.Close()

	var results []*ScopedMemory
	budgetUsed, omitted := 0, 0

	for rows.Next() {
		var s ScopedMemory
		var tax sql.NullString
		if err := rows.Scan(&s.ID, &s.Title, &tax); err != nil {
			return nil, err
		}
		s.Taxonomy = tax.String

		tokens := CountTokens(s.ID) + CountTokens(s.Title) + CountTokens(s.Taxonomy)
		if tokenBudget > 0 && budgetUsed+tokens > tokenBudget {
			omitted++
			continue
		}
		budgetUsed += tokens
		results = append(results, &s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if results == nil {
		results = []*ScopedMemory{}
	}
	return &ScopeResult{Results: results, OmittedCount: omitted}, nil
}

// RebuildFTS drops and rebuilds the FTS5 index.
func (m *MemoryDB) RebuildFTS() error {
	_, err := m.db.Exec(`INSERT INTO memories_fts(memories_fts) VALUES('rebuild')`)
	return err
}

// execQuerier is satisfied by both *sql.DB and *sql.Tx, letting syncEdges run
// inside a caller's transaction.
type execQuerier interface {
	Exec(query string, args ...any) (sql.Result, error)
	QueryRow(query string, args ...any) *sql.Row
	Query(query string, args ...any) (*sql.Rows, error)
}

// deletedTargets returns src's existing edges that point at a deleted memory,
// keyed by target text and relation. syncEdges rebuilds edges from scratch, so
// without this the tombstone pointer would be re-derived from the title and lost
// the first time the linking memory is edited.
func deletedTargets(q execQuerier, srcID string) (map[string]string, error) {
	rows, err := q.Query(`
		SELECT e.raw, e.rel, e.dst_id
		FROM edges e JOIN tombstones t ON t.memory_id = e.dst_id
		WHERE e.src_id = ?`, srcID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]string{}
	for rows.Next() {
		var raw, rel, dst string
		if err := rows.Scan(&raw, &rel, &dst); err != nil {
			return nil, err
		}
		out[raw+"\x00"+rel] = dst
	}
	return out, rows.Err()
}

// syncEdges rebuilds all outgoing edges for src from its content, resolving each
// [[link]] to a target memory by title, then heals any previously dangling edges
// that src's (possibly new) title now satisfies.
func syncEdges(q execQuerier, srcID, title, content string) ([]LinkStatus, error) {
	deleted, err := deletedTargets(q, srcID)
	if err != nil {
		return nil, err
	}
	if _, err := q.Exec(`DELETE FROM edges WHERE src_id = ?`, srcID); err != nil {
		return nil, err
	}
	var statuses []LinkStatus
	for _, link := range parseLinks(content) {
		// A reference already bound to a deleted memory stays bound to it, and
		// takes precedence over a live memory that has since taken the same
		// title: the link meant that record, not that name.
		if dst, ok := deleted[link.Raw+"\x00"+link.Rel]; ok {
			if _, err := q.Exec(
				`INSERT INTO edges (src_id, dst_id, raw, rel) VALUES (?,?,?,?)`,
				srcID, dst, link.Raw, link.Rel,
			); err != nil {
				return nil, err
			}
			statuses = append(statuses, LinkStatus{Target: link.Raw, Rel: link.Rel, Deleted: true})
			continue
		}
		var dst sql.NullString
		err := q.QueryRow(
			`SELECT id FROM memories WHERE lower(trim(title)) = ? AND id != ? LIMIT 1`,
			link.Raw, srcID,
		).Scan(&dst)
		if err != nil && err != sql.ErrNoRows {
			return nil, err
		}
		var dstVal any
		if err == nil && dst.Valid {
			dstVal = dst.String
		}
		if _, err := q.Exec(
			`INSERT INTO edges (src_id, dst_id, raw, rel) VALUES (?,?,?,?)`,
			srcID, dstVal, link.Raw, link.Rel,
		); err != nil {
			return nil, err
		}
		statuses = append(statuses, LinkStatus{Target: link.Raw, Rel: link.Rel, Resolved: dstVal != nil})
	}
	// Heal dangling edges that point at this memory's title.
	if _, err := q.Exec(
		`UPDATE edges SET dst_id = ? WHERE dst_id IS NULL AND raw = ? AND src_id != ?`,
		srcID, normalizeLink(title), srcID,
	); err != nil {
		return nil, err
	}
	return statuses, nil
}

// TitleTaxonomy returns the taxonomy of the (first) memory whose normalized
// title matches raw, and whether such a memory exists. Used to decide whether a
// resolved link's target may be disclosed to a writer under their scope.
func (m *MemoryDB) TitleTaxonomy(raw string) (string, bool) {
	var tax sql.NullString
	err := m.db.QueryRow(
		`SELECT taxonomy FROM memories WHERE lower(trim(title)) = ? LIMIT 1`, raw,
	).Scan(&tax)
	if err != nil {
		return "", false
	}
	return tax.String, true
}

// Backlinks returns the memories that link to the given memory (excluding self).
// InboundLinkCount reports how many other live memories link to id (any
// rel), regardless of the caller's scope — used at delete time to decide
// whether the deletion is safe, not to disclose anything to a reader. A
// tombstone reference from a memory deleted earlier does not count: those
// edges already point at a burned id, so deleting this one adds nothing new
// to that damage.
func (m *MemoryDB) InboundLinkCount(id string) (int, error) {
	var n int
	err := m.db.QueryRow(`SELECT COUNT(*) FROM edges WHERE dst_id = ? AND src_id != ?`, id, id).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("inbound link count %s: %w", id, err)
	}
	return n, nil
}

func (m *MemoryDB) Backlinks(scope Scope, id string) ([]*ScopedMemory, error) {
	where := "e.dst_id = ? AND e.src_id != ?"
	args := []any{id, id}
	// A backlink reveals that some memory links here, plus its title — existence
	// level. Under default deny a link from a branch the caller cannot see is
	// dropped, so the graph does not disclose hidden references into this memory.
	if clause, cargs := scope.titleClause("s.taxonomy"); clause != "" {
		where += " AND " + clause
		args = append(args, cargs...)
	}
	rows, err := m.db.Query(`
		SELECT s.id, s.title, s.taxonomy, e.rel
		FROM edges e
		JOIN memories s ON s.id = e.src_id
		WHERE `+where+`
		ORDER BY s.updated_at DESC
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("backlinks %s: %w", id, err)
	}
	defer rows.Close()

	var out []*ScopedMemory
	for rows.Next() {
		var s ScopedMemory
		var tax sql.NullString
		if err := rows.Scan(&s.ID, &s.Title, &tax, &s.Rel); err != nil {
			return nil, err
		}
		s.Taxonomy = tax.String
		out = append(out, &s)
	}
	return out, rows.Err()
}

// EdgeHealth summarizes the link graph — a readout of how well linking is being
// used. A high dangling count means agents are linking but mistyping titles (or
// referencing memories that don't exist yet); near-zero TotalEdges means linking
// is being skipped entirely.
type EdgeHealth struct {
	TotalEdges      int            `json:"total_edges"`
	Resolved        int            `json:"resolved"`
	Dangling        int            `json:"dangling"`
	DanglingTargets int            `json:"dangling_targets"` // distinct missing titles
	DanglingByRel   map[string]int `json:"dangling_by_rel,omitempty"`
	TopDangling     []DanglingRef  `json:"top_dangling,omitempty"`
}

// DanglingRef is one unresolved link target, the memories that reference it (so
// the UI can jump to the docs that need fixing), and that source count.
type DanglingRef struct {
	Target  string          `json:"target"`
	Rel     string          `json:"rel"`
	Count   int             `json:"count"`
	Sources []*ScopedMemory `json:"sources,omitempty"`
}

// EdgeHealth computes link-graph statistics in a single pass plus a small
// breakdown, for a maintenance/adoption readout. An edge is disclosed at all —
// in the aggregate counts as well as the per-target breakdown — only when its
// source memory is at least title-visible under scope; a caller confined to
// one taxonomy branch must not learn about, let alone see the titles of,
// memories outside it just because they happen to hold a dangling link. This
// mirrors Backlinks/Graph/SuggestLinks, which all gate on the source's
// taxonomy the same way.
func (m *MemoryDB) EdgeHealth(scope Scope) (*EdgeHealth, error) {
	h := &EdgeHealth{DanglingByRel: map[string]int{}}

	scopedFrom := `edges e JOIN memories s ON s.id = e.src_id`
	scopeClause, scopeArgs := scope.titleClause("s.taxonomy")
	where := ""
	if scopeClause != "" {
		where = " WHERE " + scopeClause
	}

	err := m.db.QueryRow(`
		SELECT
			COUNT(*),
			COALESCE(SUM(CASE WHEN e.dst_id IS NOT NULL THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN e.dst_id IS NULL THEN 1 ELSE 0 END), 0),
			COUNT(DISTINCT CASE WHEN e.dst_id IS NULL THEN e.raw END)
		FROM `+scopedFrom+where,
		scopeArgs...,
	).Scan(&h.TotalEdges, &h.Resolved, &h.Dangling, &h.DanglingTargets)
	if err != nil {
		return nil, fmt.Errorf("edge health: %w", err)
	}

	relWhere := "e.dst_id IS NULL"
	relArgs := append([]any{}, scopeArgs...)
	if scopeClause != "" {
		relWhere += " AND " + scopeClause
	}
	relRows, err := m.db.Query(`
		SELECT e.rel, COUNT(*) FROM `+scopedFrom+` WHERE `+relWhere+` GROUP BY e.rel
	`, relArgs...)
	if err != nil {
		return nil, fmt.Errorf("edge health by rel: %w", err)
	}
	defer relRows.Close()
	for relRows.Next() {
		var rel string
		var n int
		if err := relRows.Scan(&rel, &n); err != nil {
			return nil, err
		}
		h.DanglingByRel[rel] = n
	}
	if err := relRows.Err(); err != nil {
		return nil, err
	}

	// One row per dangling edge with its source memory, grouped in Go so each
	// target carries the docs that reference it. Ordered by group size so the
	// most-referenced broken titles surface first.
	topWhere := "e.dst_id IS NULL"
	topArgs := append([]any{}, scopeArgs...)
	if scopeClause != "" {
		topWhere += " AND " + scopeClause
	}
	topRows, err := m.db.Query(`
		SELECT e.raw, e.rel, s.id, s.title, s.taxonomy
		FROM `+scopedFrom+`
		WHERE `+topWhere+`
		ORDER BY e.raw, e.rel, s.updated_at DESC
	`, topArgs...)
	if err != nil {
		return nil, fmt.Errorf("edge health top dangling: %w", err)
	}
	defer topRows.Close()

	idx := map[string]int{} // raw\x00rel -> position in h.TopDangling
	for topRows.Next() {
		var raw, rel string
		var src ScopedMemory
		var tax sql.NullString
		if err := topRows.Scan(&raw, &rel, &src.ID, &src.Title, &tax); err != nil {
			return nil, err
		}
		src.Taxonomy = tax.String
		key := raw + "\x00" + rel
		i, ok := idx[key]
		if !ok {
			i = len(h.TopDangling)
			idx[key] = i
			h.TopDangling = append(h.TopDangling, DanglingRef{Target: raw, Rel: rel})
		}
		h.TopDangling[i].Count++
		h.TopDangling[i].Sources = append(h.TopDangling[i].Sources, &src)
	}
	if err := topRows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(h.TopDangling, func(i, j int) bool {
		if h.TopDangling[i].Count != h.TopDangling[j].Count {
			return h.TopDangling[i].Count > h.TopDangling[j].Count
		}
		return h.TopDangling[i].Target < h.TopDangling[j].Target
	})
	if len(h.TopDangling) > 15 {
		h.TopDangling = h.TopDangling[:15]
	}
	return h, nil
}

// RebuildEdges drops and reconstructs the entire edge graph from memory content.
// Use after a bulk import or to recover from a corrupt edges table.
func (m *MemoryDB) RebuildEdges() error {
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM edges`); err != nil {
		return fmt.Errorf("rebuild edges clear: %w", err)
	}

	rows, err := tx.Query(`SELECT id, title, content FROM memories`)
	if err != nil {
		return fmt.Errorf("rebuild edges scan: %w", err)
	}
	type rec struct{ id, title, content string }
	var recs []rec
	for rows.Next() {
		var r rec
		var content sql.NullString
		if err := rows.Scan(&r.id, &r.title, &content); err != nil {
			rows.Close()
			return err
		}
		r.content = content.String
		recs = append(recs, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, r := range recs {
		if _, err := syncEdges(tx, r.id, r.title, r.content); err != nil {
			return fmt.Errorf("rebuild edges sync %s: %w", r.id, err)
		}
	}
	return tx.Commit()
}

// Close checkpoints and truncates the WAL, then closes the underlying DB so the
// -wal/-shm sidecar files are released rather than left on disk. Used by the
// Manager when an idle tenant handle is reaped.
func (m *MemoryDB) Close() error {
	// Best-effort: a failed checkpoint must not block the close. Skipped when
	// a replicator (e.g. litestream, via ReplicatedOpen) owns this file's
	// checkpoint timing — a TRUNCATE here would merge and truncate the WAL
	// out from under it before it gets a chance to sync those frames to the
	// replica, silently losing recent writes on the next reopen. Manager's
	// idle-reap calls this constantly by design, so this isn't a rare edge
	// case once replication is active — see db.ReplicatedOpen.
	if !m.externallyCheckpointed {
		_, _ = m.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE);`)
	}
	return m.db.Close()
}

// memoryTokens counts approximate tokens across all memory fields.
func memoryTokens(m *Memory) int {
	return CountTokens(m.ID) +
		CountTokens(m.Title) +
		CountTokens(m.Taxonomy) +
		CountTokens(m.Content)
}

const selectMemory = `
	SELECT id, title, content, taxonomy, format, created_at, updated_at, version
	FROM memories`

func scanMemory(row *sql.Row) (*Memory, error) {
	m, err := scanMemoryFrom(row.Scan)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("memory not found")
	}
	if err != nil {
		return nil, err
	}
	return m, nil
}

func scanMemoryRow(rows *sql.Rows) (*Memory, error) {
	return scanMemoryFrom(rows.Scan)
}

// scanMemoryFrom reads one selectMemory row from either a *sql.Row or *sql.Rows
// by taking their common Scan method, keeping the column list in one place.
func scanMemoryFrom(scan func(...any) error) (*Memory, error) {
	var m Memory
	var content, taxonomy sql.NullString
	err := scan(&m.ID, &m.Title, &content, &taxonomy, &m.Format, &m.CreatedAt, &m.UpdatedAt, &m.Version)
	if err != nil {
		return nil, err
	}
	m.Content = content.String
	m.Taxonomy = taxonomy.String
	return &m, nil
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// formatOrDefault coerces an empty format to 'markdown' so the NOT NULL column
// is always satisfied, independent of any higher-level validation.
func formatOrDefault(s string) string {
	if s == "" {
		return "markdown"
	}
	return s
}
