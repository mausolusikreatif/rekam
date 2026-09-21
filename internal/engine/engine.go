package engine

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/Ucok23/rekam/internal/db"
	"github.com/google/uuid"
)

// sessionTokenPrefix marks an internal, server-minted bearer that carries an
// identity id instead of a raw API key. These are produced only by SessionToken
// (from a validated httpOnly session) and injected server-side; they are HMAC
// signed with a per-process secret so a client cannot forge one.
const sessionTokenPrefix = "sess_"

// Engine is the core business logic layer.
type Engine struct {
	registry      *db.Registry
	manager       *db.Manager
	tokenBudget   int
	sessionSecret []byte // signs internal session tokens; random per process

	// replication, when set, gates writes on holding a tenant's write lease —
	// see db.Replication and docs/plan-distributed-tenants.md. nil (the
	// default) preserves today's single-writer-per-process behavior: any node
	// with the file open may write to it.
	replication *db.Replication
	// registryPath is the logical path registration.EnsureActive was called
	// with for registry.sqlite itself (see cmd/rekam/main.go's cmdServe) — the
	// key requireRegistryPrimary checks the write lease against. Unused when
	// replication is nil.
	registryPath string
}

// New creates an Engine backed by the given registry, using default Manager
// lifecycle settings (idle handles released after 2m, no fd cap).
func New(reg *db.Registry, defaultTokenBudget int) *Engine {
	return NewWithManager(reg, defaultTokenBudget, db.ManagerOptions{})
}

// NewWithManager creates an Engine with explicit per-tenant lifecycle options.
func NewWithManager(reg *db.Registry, defaultTokenBudget int, opts db.ManagerOptions) *Engine {
	return newEngine(reg, defaultTokenBudget, opts, nil, "")
}

// NewWithReplication creates an Engine whose writes — both tenant writes and
// registry mutations (new identity, revoke, team create/rename/delete, grant
// mint/revoke, ...) — are gated by repl's write leases (see db.Replication)
// in addition to the usual role/permission checks. opts.DefaultOpen should
// normally be db.ReplicatedOpen(repl), so a tenant's replication is
// guaranteed active before resolveTenant checks its lease. registryPath must
// be the same logical path repl.EnsureActive was called with for the
// registry file itself.
func NewWithReplication(reg *db.Registry, defaultTokenBudget int, opts db.ManagerOptions, repl *db.Replication, registryPath string) *Engine {
	return newEngine(reg, defaultTokenBudget, opts, repl, registryPath)
}

func newEngine(reg *db.Registry, defaultTokenBudget int, opts db.ManagerOptions, repl *db.Replication, registryPath string) *Engine {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		panic("engine: cannot read random session secret: " + err.Error())
	}
	return &Engine{
		registry:      reg,
		manager:       db.NewManager(opts),
		tokenBudget:   defaultTokenBudget,
		sessionSecret: secret,
		replication:   repl,
		registryPath:  registryPath,
	}
}

// requireRegistryPrimary gates a registry mutation the same way resolveTenant
// gates a tenant write — only meaningful once replication is configured; nil
// replication (today's default) is a no-op, preserving existing behavior for
// every caller and test that doesn't touch replication at all.
func (e *Engine) requireRegistryPrimary() error {
	if e.replication == nil {
		return nil
	}
	if err := e.replication.RequirePrimary(e.registryPath); err != nil {
		var notPrimary *db.ErrNotPrimary
		if errors.As(err, &notPrimary) {
			return &ActionableError{
				Code:    "not_primary",
				Message: "this node is not the write-primary for the registry right now",
				Context: map[string]string{"owner": notPrimary.Owner},
			}
		}
		return fmt.Errorf("check registry write lease: %w", err)
	}
	return nil
}

// SessionToken mints an internal, signed bearer for a browser session, bound to
// the identity's home tenant. It is never exposed to the client (the browser
// holds only the opaque httpOnly session cookie).
func (e *Engine) SessionToken(identityID string) string {
	return e.sessionTokenFor(identityID, "")
}

// BearerForTeam rebinds an incoming bearer (raw API key or session token) to a
// team-bound session token, so a request carrying a team selector is routed into
// that team. It resolves the bearer to its identity and re-signs with the team;
// an empty teamID returns the bearer unchanged (home routing). This is the one
// call the transport layer makes to turn a (bearer, team-header) pair into the
// single bearer the rest of the engine understands. It does not check team
// membership — resolveTenant remains the authoritative gate.
func (e *Engine) BearerForTeam(bearer, teamID string) (string, error) {
	if teamID == "" {
		return bearer, nil
	}
	identity, _, err := e.resolveBearer(bearer)
	if err != nil {
		return "", err
	}
	return e.SessionTokenForTeam(identity.ID, teamID), nil
}

// SessionTokenForTeam mints a session bearer that also selects a team: resolve()
// will route the request into that team's corpus instead of the identity's home.
// This is how a team selector reaches the engine without threading a team id
// through every method — the transport layer mints a team-bound token from a
// (bearer, team) pair, and everything downstream treats it as one bearer.
func (e *Engine) SessionTokenForTeam(identityID, teamID string) string {
	return e.sessionTokenFor(identityID, teamID)
}

// sessionTokenFor signs "<id>" or "<id>|<team>" so the team selector cannot be
// forged or swapped without invalidating the signature.
func (e *Engine) sessionTokenFor(identityID, teamID string) string {
	body := identityID
	if teamID != "" {
		body += "|" + teamID
	}
	mac := hmac.New(sha256.New, e.sessionSecret)
	mac.Write([]byte(body))
	return sessionTokenPrefix + body + "." + hex.EncodeToString(mac.Sum(nil))
}

// identityFromSessionToken verifies a session token's signature and returns the
// embedded identity id and (optional) selected team id. ok is false for any
// non-session or tampered token.
func (e *Engine) identityFromSessionToken(token string) (id, teamID string, ok bool) {
	if !strings.HasPrefix(token, sessionTokenPrefix) {
		return "", "", false
	}
	body := strings.TrimPrefix(token, sessionTokenPrefix)
	dot := strings.LastIndexByte(body, '.')
	if dot <= 0 {
		return "", "", false
	}
	signed, sig := body[:dot], body[dot+1:]
	want, err := hex.DecodeString(sig)
	if err != nil {
		return "", "", false
	}
	mac := hmac.New(sha256.New, e.sessionSecret)
	mac.Write([]byte(signed))
	if subtle.ConstantTimeCompare(want, mac.Sum(nil)) != 1 {
		return "", "", false
	}
	if bar := strings.IndexByte(signed, '|'); bar >= 0 {
		return signed[:bar], signed[bar+1:], true
	}
	return signed, "", true
}

// Shutdown drains every open tenant handle and stops the lifecycle reaper.
func (e *Engine) Shutdown() { e.manager.Shutdown() }

// Manager exposes the tenant lifecycle manager (lifecycle metrics, tests).
func (e *Engine) Manager() *db.Manager { return e.manager }

// WriteInput holds caller-supplied fields for a new memory.
type WriteInput struct {
	Title    string `json:"title"`
	Content  string `json:"content"`
	Taxonomy string `json:"taxonomy"`
	Format   string `json:"format,omitempty"`
	// Agent is the model or system making this write, self-reported — see
	// db.Revision.Agent. Empty (the REST/UI path never sets it) reads as "a
	// human wrote this directly."
	Agent string `json:"agent,omitempty"`
}

// UpdateInput holds fields that may be changed after creation.
type UpdateInput struct {
	Title    *string `json:"title,omitempty"`
	Content  *string `json:"content,omitempty"`
	Taxonomy *string `json:"taxonomy,omitempty"`
	Format   *string `json:"format,omitempty"`
	// BaseVersion is the version the caller read before editing. When set, the
	// write is rejected with version_conflict if someone else has since written,
	// instead of silently overwriting them. Omitted means last-writer-wins.
	BaseVersion *int `json:"base_version,omitempty"`
	// Agent: see WriteInput.Agent.
	Agent string `json:"agent,omitempty"`
}

// allowedFormats is the closed set of content types a memory may carry. All are
// plain text, so FTS and the link graph keep working regardless of format.
var allowedFormats = []string{"markdown", "mermaid", "code", "table"}

// normalizeFormat lower-cases and validates a caller-supplied format. An empty
// value defaults to "markdown". Returns ok=false for an unknown format.
func normalizeFormat(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "markdown", true
	}
	return s, slices.Contains(allowedFormats, s)
}

// invalidFormatErr builds the actionable error listing the accepted formats.
func invalidFormatErr(s string) *ActionableError {
	return &ActionableError{
		Code:    "invalid_format",
		Message: fmt.Sprintf("format %q is not supported", s),
		Context: map[string]any{"allowed_formats": allowedFormats},
	}
}

// ActionableError wraps an error with context the caller can act on.
type ActionableError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Context any    `json:"context,omitempty"`
}

func (e *ActionableError) Error() string { return e.Message }

// Catalog returns taxonomy paths rolled up to a depth, scoped to an optional prefix.
//
// Rather than returning every distinct leaf path (which grows with the store), the
// catalog folds paths to `depth` dot-segments and aggregates their counts, so the
// agent sees a shallow tree and drills in by re-calling with a deeper prefix.
//
//	prefix="", depth=0   → top-level branches (depth 1)
//	prefix="work"        → one level under work (depth 2)
//	prefix="work", depth=3 → two levels under work
func (e *Engine) Catalog(apiKey, prefix string, depth int) ([]db.TaxonomyEntry, error) {
	_, mdb, scope, release, err := e.resolve(apiKey, false)
	defer release()
	if err != nil {
		return nil, err
	}
	leaves, err := mdb.Catalog(scope)
	if err != nil {
		return nil, err
	}
	entries := rollupCatalog(leaves, prefix, depth)
	if entries == nil {
		entries = []db.TaxonomyEntry{}
	}
	return entries, nil
}

// rollupCatalog folds full leaf paths into a shallow tree scoped to prefix.
// A default depth of one level deeper than the prefix is used when depth <= 0.
func rollupCatalog(leaves []db.TaxonomyEntry, prefix string, depth int) []db.TaxonomyEntry {
	if depth <= 0 {
		if prefix == "" {
			depth = 1
		} else {
			depth = strings.Count(prefix, ".") + 2 // prefix segments + one level deeper
		}
	}

	agg := map[string]*db.TaxonomyEntry{}
	var order []string
	for _, leaf := range leaves {
		if prefix != "" && leaf.Path != prefix && !strings.HasPrefix(leaf.Path, prefix+".") {
			continue
		}
		segs := strings.Split(leaf.Path, ".")
		key := leaf.Path
		expandable := false
		if len(segs) > depth {
			key = strings.Join(segs[:depth], ".")
			expandable = true
		}
		entry, ok := agg[key]
		if !ok {
			entry = &db.TaxonomyEntry{Path: key}
			agg[key] = entry
			order = append(order, key)
		}
		entry.Count += leaf.Count
		if expandable {
			entry.Expandable = true
		}
	}

	sort.Strings(order)
	out := make([]db.TaxonomyEntry, 0, len(order))
	for _, k := range order {
		out = append(out, *agg[k])
	}
	return out
}

// Search executes an FTS5 query.
func (e *Engine) Search(apiKey, query, taxonomy string, tokenBudget int) (*db.SearchResult, error) {
	if query == "" {
		return nil, &ActionableError{Code: "missing_query", Message: "query parameter q is required"}
	}
	_, mdb, scope, release, err := e.resolve(apiKey, false)
	defer release()
	if err != nil {
		return nil, err
	}
	budget := e.tokenBudget
	if tokenBudget > 0 {
		budget = tokenBudget
	}
	return mdb.Search(scope, query, taxonomy, budget)
}

// GetMemory fetches a single memory by ID.
func (e *Engine) GetMemory(apiKey, id string) (*db.Memory, error) {
	_, mdb, scope, release, err := e.resolve(apiKey, false)
	defer release()
	if err != nil {
		return nil, err
	}
	mem, err := mdb.Get(id)
	if err != nil {
		if ae := asActionable(err); ae != err {
			return nil, ae // deleted: the tombstone says more than "not found"
		}
		if strings.Contains(err.Error(), "memory not found") {
			return nil, &ActionableError{Code: "not_found", Message: "memory not found: " + id}
		}
		return nil, err
	}
	// A memory outside the read scope is reported as absent, not forbidden:
	// confirming "forbidden" would disclose that a restricted memory exists at
	// this id. Default deny means unreadable is indistinguishable from missing.
	if !scope.CanRead(mem.Taxonomy) {
		return nil, &ActionableError{Code: "not_found", Message: "memory not found: " + id}
	}
	// Surface backlinks so the agent can traverse the graph from a single memory.
	if links, lerr := mdb.Backlinks(scope, id); lerr == nil {
		mem.LinkedFrom = links
	}
	if inReview, rerr := mdb.InReview(id); rerr == nil {
		mem.InReview = inReview
	}
	return mem, nil
}

// SuggestLinks returns memory pairs that look related but aren't yet linked —
// the connections the graph is missing.
func (e *Engine) SuggestLinks(apiKey string, limit int) ([]db.LinkSuggestion, error) {
	_, mdb, scope, release, err := e.resolve(apiKey, false)
	defer release()
	if err != nil {
		return nil, err
	}
	out, err := mdb.SuggestLinks(scope, limit)
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = []db.LinkSuggestion{}
	}
	return out, nil
}

// AddToReview enrolls a memory in the spaced-repetition deck.
func (e *Engine) AddToReview(apiKey, id string) (*db.SRSState, error) {
	_, mdb, scope, release, err := e.resolve(apiKey, true)
	defer release()
	if err != nil {
		return nil, err
	}
	// Enrolling requires being able to read the memory, so an unreadable id is
	// reported absent rather than silently added to a deck the caller can't see.
	if mem, gerr := mdb.Get(id); gerr != nil || !scope.CanRead(mem.Taxonomy) {
		return nil, &ActionableError{Code: "not_found", Message: "memory not found: " + id}
	}
	st, err := mdb.AddToReview(id)
	if err != nil && strings.Contains(err.Error(), "memory not found") {
		return nil, &ActionableError{Code: "not_found", Message: err.Error()}
	}
	return st, err
}

// RemoveFromReview drops a memory from the review deck.
func (e *Engine) RemoveFromReview(apiKey, id string) error {
	_, mdb, _, release, err := e.resolve(apiKey, true)
	defer release()
	if err != nil {
		return err
	}
	return mdb.RemoveFromReview(id)
}

// DueReviews returns the memories due for review now, with deck stats.
func (e *Engine) DueReviews(apiKey string, limit int) ([]*db.ReviewCard, *db.ReviewStats, error) {
	_, mdb, _, release, err := e.resolve(apiKey, false)
	defer release()
	if err != nil {
		return nil, nil, err
	}
	cards, err := mdb.DueReviews(limit)
	if err != nil {
		return nil, nil, err
	}
	stats, err := mdb.ReviewStats()
	if err != nil {
		return nil, nil, err
	}
	return cards, stats, nil
}

// GradeReview records a review grade [0,5] and reschedules the card.
func (e *Engine) GradeReview(apiKey, id string, grade int) (*db.SRSState, error) {
	_, mdb, _, release, err := e.resolve(apiKey, true)
	defer release()
	if err != nil {
		return nil, err
	}
	st, err := mdb.Grade(id, grade)
	if err != nil && strings.Contains(err.Error(), "not in review deck") {
		return nil, &ActionableError{Code: "not_found", Message: err.Error()}
	}
	if err != nil && strings.Contains(err.Error(), "grade must be") {
		return nil, &ActionableError{Code: "invalid_field", Message: err.Error()}
	}
	return st, err
}

// WriteMemory validates then stores a new memory.
func (e *Engine) WriteMemory(apiKey string, input WriteInput) (*db.Memory, error) {
	identity, mdb, scope, release, err := e.resolve(apiKey, true)
	defer release()
	if err != nil {
		return nil, err
	}

	var missing []string
	if input.Title == "" {
		missing = append(missing, "title")
	}
	if input.Taxonomy == "" {
		missing = append(missing, "taxonomy")
	}
	if len(missing) > 0 {
		return nil, &ActionableError{
			Code:    "missing_fields",
			Message: fmt.Sprintf("required fields missing: %v", missing),
			Context: map[string]any{"missing_fields": missing},
		}
	}

	if !validTaxonomy(input.Taxonomy) {
		catalog, _ := mdb.Catalog(scope)
		return nil, &ActionableError{
			Code:    "invalid_taxonomy",
			Message: fmt.Sprintf("taxonomy %q is not valid dot-notation", input.Taxonomy),
			Context: map[string]any{"current_catalog": catalog},
		}
	}
	if !scope.CanWrite(input.Taxonomy) {
		return nil, errTaxonomyForbidden(input.Taxonomy)
	}

	format, ok := normalizeFormat(input.Format)
	if !ok {
		return nil, invalidFormatErr(input.Format)
	}

	if err := e.checkStorageCeiling(mdb); err != nil {
		return nil, err
	}

	mem := &db.Memory{
		Title:    input.Title,
		Content:  input.Content,
		Taxonomy: input.Taxonomy,
		Format:   format,
	}

	if err := mdb.Insert(mem, identity.ID, input.Agent); err != nil {
		if ae := asActionable(err); ae != err {
			return nil, ae
		}
		return nil, fmt.Errorf("insert: %w", err)
	}
	redactLinkResolution(mdb, scope, mem)
	return mem, nil
}

// redactLinkResolution hides, from the writer's link feedback, whether a link
// resolved to a memory in a branch they may not even know exists. A resolved
// link whose target is not title-visible is reported as if it were dangling —
// otherwise the LinkStatus would be an existence oracle: type the exact secret
// title, learn from Resolved=true that it is real. The stored edge is untouched
// and stays truthful; only what this writer is told changes.
func redactLinkResolution(mdb *db.MemoryDB, scope db.Scope, mem *db.Memory) {
	if scope.Unrestricted() {
		return
	}
	for i, l := range mem.Links {
		if !l.Resolved {
			continue
		}
		if tax, ok := mdb.TitleTaxonomy(l.Target); ok && !scope.TitleVisible(tax) {
			mem.Links[i].Resolved = false
		}
	}
}

// UpdateMemory changes fields on an existing memory.
func (e *Engine) UpdateMemory(apiKey, id string, input UpdateInput) (*db.Memory, error) {
	identity, mdb, scope, release, err := e.resolve(apiKey, true)
	defer release()
	if err != nil {
		return nil, err
	}

	// Editing requires write on where the memory currently lives. Fetch first so
	// an unreadable target is reported absent (not forbidden), and a writable
	// check runs against its real taxonomy rather than the caller's assertion.
	existing, err := mdb.Get(id)
	if err != nil {
		if ae := asActionable(err); ae != err {
			return nil, ae
		}
		return nil, &ActionableError{Code: "not_found", Message: "memory not found: " + id}
	}
	if !scope.CanRead(existing.Taxonomy) {
		return nil, &ActionableError{Code: "not_found", Message: "memory not found: " + id}
	}
	if !scope.CanWrite(existing.Taxonomy) {
		return nil, errTaxonomyForbidden(existing.Taxonomy)
	}

	updates := map[string]any{}
	if input.Title != nil {
		if strings.TrimSpace(*input.Title) == "" {
			return nil, &ActionableError{Code: "invalid_field", Message: "title cannot be empty"}
		}
		updates["title"] = *input.Title
	}
	if input.Content != nil {
		updates["content"] = *input.Content
	}
	if input.Taxonomy != nil {
		if !validTaxonomy(*input.Taxonomy) {
			catalog, _ := mdb.Catalog(scope)
			return nil, &ActionableError{
				Code:    "invalid_taxonomy",
				Message: fmt.Sprintf("taxonomy %q is not valid dot-notation", *input.Taxonomy),
				Context: map[string]any{"current_catalog": catalog},
			}
		}
		// Moving a memory into a branch also requires write on the destination,
		// so a memory cannot be relocated into a branch the caller cannot write.
		if !scope.CanWrite(*input.Taxonomy) {
			return nil, errTaxonomyForbidden(*input.Taxonomy)
		}
		updates["taxonomy"] = *input.Taxonomy
	}
	if input.Format != nil {
		format, ok := normalizeFormat(*input.Format)
		if !ok {
			return nil, invalidFormatErr(*input.Format)
		}
		updates["format"] = format
	}
	if len(updates) == 0 {
		return nil, &ActionableError{Code: "no_changes", Message: "no fields to update"}
	}

	if err := e.checkStorageCeiling(mdb); err != nil {
		return nil, err
	}

	mem, err := mdb.Update(id, updates, identity.ID, input.Agent, input.BaseVersion)
	if ae := asActionable(err); ae != err {
		return nil, ae // the address is burned; no edit can revive it
	}
	var conflict *db.VersionConflictError
	if errors.As(err, &conflict) {
		// Hand back the current version so the client can re-read, reapply, and
		// retry without a second round trip to discover what it should have sent.
		return nil, &ActionableError{
			Code:    "version_conflict",
			Message: conflict.Error(),
			Context: map[string]any{
				"base_version":    conflict.Base,
				"current_version": conflict.Current,
			},
		}
	}
	if err == nil {
		redactLinkResolution(mdb, scope, mem)
	}
	return mem, err
}

// RevisionView is a revision resolved to its author's display identity. A
// history of bare uuids answers "what changed" but not "who changed it", which
// is the question history exists to answer — the same reasoning as TeamMember.
// An author the registry no longer knows (a deleted account) keeps its id and
// simply gets no name, rather than the row being dropped or blanked: a change
// made by someone since removed is exactly what an audit needs to still see.
type RevisionView struct {
	*db.Revision
	AuthorName  string `json:"author_name,omitempty"`
	AuthorEmail string `json:"author_email,omitempty"`
}

// MemoryHistory returns a memory's revision history, newest first, each row
// resolved to its author's name.
func (e *Engine) MemoryHistory(apiKey, id string) ([]*RevisionView, error) {
	_, mdb, scope, release, err := e.resolve(apiKey, false)
	defer release()
	if err != nil {
		return nil, err
	}
	mem, err := mdb.Get(id)
	if err != nil {
		if ae := asActionable(err); ae != err {
			return nil, ae // deleted memories have no history left to show
		}
		return nil, &ActionableError{Code: "not_found", Message: err.Error()}
	}
	if !scope.CanRead(mem.Taxonomy) {
		return nil, &ActionableError{Code: "not_found", Message: "memory not found: " + id}
	}
	revs, err := mdb.Revisions(id)
	if err != nil {
		return nil, err
	}
	// A long history is usually a handful of people editing repeatedly, so
	// resolve each distinct author once rather than per row.
	type author struct{ name, email string }
	seen := make(map[string]author)
	out := make([]*RevisionView, 0, len(revs))
	for _, rev := range revs {
		row := &RevisionView{Revision: rev}
		if rev.AuthorID != "" {
			a, ok := seen[rev.AuthorID]
			if !ok {
				if identity, rerr := e.registry.ResolveByID(rev.AuthorID); rerr == nil {
					a = author{identity.Name, identity.Email}
				}
				seen[rev.AuthorID] = a
			}
			row.AuthorName, row.AuthorEmail = a.name, a.email
		}
		out = append(out, row)
	}
	return out, nil
}

// RestoreRevision rolls a memory's content back to an earlier version by writing
// those fields forward as a new version. History is append-only: the restore is
// itself a revision, attributed to whoever performed it, so a rollback is as
// auditable (and as reversible) as any other edit.
func (e *Engine) RestoreRevision(apiKey, id string, version int) (*db.Memory, error) {
	identity, mdb, scope, release, err := e.resolve(apiKey, true)
	defer release()
	if err != nil {
		return nil, err
	}
	// Check the memory first: a deleted address must report the tombstone rather
	// than a bare missing-revision error, since its history was purged with it.
	existing, err := mdb.Get(id)
	if err != nil {
		if ae := asActionable(err); ae != err {
			return nil, ae
		}
		return nil, &ActionableError{Code: "not_found", Message: err.Error()}
	}
	if !scope.CanRead(existing.Taxonomy) {
		return nil, &ActionableError{Code: "not_found", Message: "memory not found: " + id}
	}
	if !scope.CanWrite(existing.Taxonomy) {
		return nil, errTaxonomyForbidden(existing.Taxonomy)
	}
	rev, err := mdb.RevisionAt(id, version)
	if err != nil {
		return nil, &ActionableError{Code: "not_found", Message: err.Error()}
	}
	updates := map[string]any{
		"title":    rev.Title,
		"content":  rev.Content,
		"taxonomy": rev.Taxonomy,
		"format":   rev.Format,
	}
	// Agent is left unreported here: RestoreRevision has no Agent input of its
	// own (out of this sketch's scope — only write_memory/update_memory report
	// one), and the restored content's own original agent already lives on the
	// version being copied forward (rev.Agent), unaffected by this new row.
	return mdb.Update(id, updates, identity.ID, "", nil)
}

// DeleteMemory removes a memory by ID.
// DeleteMemory permanently removes a memory. force must be true to delete a
// memory that other live memories still link to (see MEM-23) — otherwise the
// call is refused with has_inbound_links, since deleting it would leave every
// one of those links pointing at a burned id with no recovery, exactly what
// supersedes exists to avoid.
func (e *Engine) DeleteMemory(apiKey, id string, force bool) error {
	identity, mdb, scope, release, err := e.resolve(apiKey, true)
	defer release()
	if err != nil {
		return err
	}
	// Deleting is terminal and requires write on the memory's branch. Gate on its
	// real taxonomy, reporting an unreadable target as absent rather than forbidden.
	mem, err := mdb.Get(id)
	if err != nil {
		if ae := asActionable(err); ae != err {
			return ae
		}
		return &ActionableError{Code: "not_found", Message: "memory not found: " + id}
	}
	if !scope.CanRead(mem.Taxonomy) {
		return &ActionableError{Code: "not_found", Message: "memory not found: " + id}
	}
	if !scope.CanWrite(mem.Taxonomy) {
		return errTaxonomyForbidden(mem.Taxonomy)
	}
	if !force {
		n, err := mdb.InboundLinkCount(id)
		if err != nil {
			return err
		}
		if n > 0 {
			return &ActionableError{
				Code:    "has_inbound_links",
				Message: fmt.Sprintf("%d memories link here — deleting would leave them dangling; use `supersedes` instead, or pass force=true to delete anyway", n),
				Context: map[string]any{"inbound_links": n},
			}
		}
	}
	return asActionable(mdb.Delete(id, identity.ID))
}

// DeletedMemories lists tombstones — what was deleted, by whom, and when —
// optionally scoped to a taxonomy prefix. Deletion is terminal, so this is the
// only remaining trace of the removed records.
func (e *Engine) DeletedMemories(apiKey, taxonomy string, limit int) ([]*db.Tombstone, error) {
	_, mdb, scope, release, err := e.resolve(apiKey, false)
	defer release()
	if err != nil {
		return nil, err
	}
	return mdb.Tombstones(scope, taxonomy, limit)
}

// ── team membership ─────────────────────────────────────────────────────────

// MyMembership returns the caller's own role and scope in the tenant they are
// accessing. Any member may see their own standing.
func (e *Engine) MyMembership(apiKey string) (*db.Member, error) {
	identity, mdb, _, release, err := e.resolve(apiKey, false)
	defer release()
	if err != nil {
		return nil, err
	}
	return e.memberOf(mdb, identity.ID)
}

// TeamMember is a roster row joined to the identity it names. The split is
// structural: roles live in the tenant file (next to the corpus they govern)
// while names and emails live in the central registry, so a raw member row
// carries nothing a human can recognise. This is where the two halves meet.
type TeamMember struct {
	*db.Member
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
}

// TeamMembers lists the tenant's roster. Any member may see who else is on the
// team and their roles; scope grants are included so managers can review them.
// Each row is resolved to its display identity — a roster of bare uuids is
// unusable for the one job it has, deciding who to promote or remove. An id the
// registry no longer knows (a deleted account) still lists, with no name, rather
// than vanishing: a stale membership row is exactly what a manager needs to see.
func (e *Engine) TeamMembers(apiKey string) ([]*TeamMember, error) {
	_, mdb, _, release, err := e.resolve(apiKey, false)
	defer release()
	if err != nil {
		return nil, err
	}
	members, err := mdb.ListMembers()
	if err != nil {
		return nil, err
	}
	out := make([]*TeamMember, 0, len(members))
	for _, m := range members {
		row := &TeamMember{Member: m}
		if id, err := e.registry.ResolveByID(m.IdentityID); err == nil {
			row.Name, row.Email = id.Name, id.Email
		}
		out = append(out, row)
	}
	return out, nil
}

// SetMember adds a member or changes an existing member's role and scope grants.
// It requires the caller to hold a management role, and enforces the safety
// rules that keep a team well-formed and prevent privilege escalation:
//   - only an Owner may grant or modify the Owner role (an Admin cannot mint
//     Owners, nor demote/re-scope one);
//   - the target identity must exist in the registry, so membership never
//     references a phantom account.
//
// Inviting is additive: it adds a membership row to the team being acted in and
// indexes the target for discovery, without touching the target's home corpus or
// any other team they belong to. A person can be in several teams at once, each
// with its own role and its own memory.
func (e *Engine) SetMember(apiKey, targetID string, role db.Role, readGrants, titleGrants []string) (*db.Member, error) {
	identity, mdb, _, teamID, path, release, err := e.resolveTenant(apiKey, false)
	defer release()
	if err != nil {
		return nil, err
	}
	caller, err := e.memberOf(mdb, identity.ID)
	if err != nil {
		return nil, err
	}
	if !caller.Role.CanManageMembers() {
		return nil, &ActionableError{Code: "forbidden", Message: "your role cannot manage members"}
	}
	// resolveTenant was called with requireWrite=false (CanManageMembers is
	// the real gate, not AllowWrite) but PutMember below still mutates the
	// tenant file, so check the write lease explicitly.
	if err := e.requireTenantPrimary(path); err != nil {
		return nil, err
	}
	if !db.ValidRole(role) {
		return nil, &ActionableError{Code: "invalid_role", Message: fmt.Sprintf("unknown role %q", role)}
	}
	if _, err := e.registry.ResolveByID(targetID); err != nil {
		return nil, &ActionableError{Code: "not_found", Message: "no such identity: " + targetID}
	}
	existing, _ := mdb.Member(targetID)
	// Only an Owner may create or alter Owners. Guards both directions: granting
	// the Owner role, and modifying someone who already holds it.
	if !caller.Role.CanManageTeam() {
		if role == db.RoleOwner {
			return nil, &ActionableError{Code: "forbidden", Message: "only an owner can grant the owner role"}
		}
		if existing != nil && existing.Role == db.RoleOwner {
			return nil, &ActionableError{Code: "forbidden", Message: "only an owner can change an owner"}
		}
	}
	// Demoting the last Owner would leave the team unmanageable.
	if existing != nil && existing.Role == db.RoleOwner && role != db.RoleOwner {
		if err := e.guardLastOwner(mdb); err != nil {
			return nil, err
		}
	}
	// Seat cap: only bites a genuinely new member (existing == nil) — a role or
	// grant change for someone already on the roster isn't adding a seat. Home/
	// unregistered shared files (teamID == "") have no team record and thus no
	// seat concept; only a registered team can be over its limit.
	if teamID != "" && existing == nil {
		if err := e.checkSeatLimit(mdb, teamID); err != nil {
			return nil, err
		}
	}
	// Checked before mutating the tenant file below: IndexTeamMember touches
	// the registry too, and failing that check after PutMember has already
	// committed would leave the two out of sync.
	if teamID != "" {
		if err := e.requireRegistryPrimary(); err != nil {
			return nil, err
		}
	}
	if err := mdb.PutMember(targetID, role, readGrants, titleGrants, identity.ID); err != nil {
		return nil, fmt.Errorf("set member: %w", err)
	}
	// Index the target for discovery when acting in a registered team, so the
	// team shows up in their "my teams" list. Home/unregistered shared files have
	// no team id and simply skip the index — the tenant member row still governs.
	if teamID != "" {
		if err := e.registry.IndexTeamMember(teamID, targetID); err != nil {
			return nil, fmt.Errorf("index member: %w", err)
		}
	}
	return mdb.Member(targetID)
}

// RemoveMember revokes a member's access to the tenant. Requires a management
// role; an Admin cannot remove an Owner, and the last Owner cannot be removed.
// This only drops the membership row — it does not delete the identity's account
// or repoint its home tenant.
func (e *Engine) RemoveMember(apiKey, targetID string) error {
	identity, mdb, _, teamID, path, release, err := e.resolveTenant(apiKey, false)
	defer release()
	if err != nil {
		return err
	}
	caller, err := e.memberOf(mdb, identity.ID)
	if err != nil {
		return err
	}
	if !caller.Role.CanManageMembers() {
		return &ActionableError{Code: "forbidden", Message: "your role cannot manage members"}
	}
	target, err := mdb.Member(targetID)
	if err != nil {
		return fmt.Errorf("remove member: %w", err)
	}
	if target == nil {
		return &ActionableError{Code: "not_found", Message: "not a member: " + targetID}
	}
	if target.Role == db.RoleOwner {
		if !caller.Role.CanManageTeam() {
			return &ActionableError{Code: "forbidden", Message: "only an owner can remove an owner"}
		}
		if err := e.guardLastOwner(mdb); err != nil {
			return err
		}
	}
	// resolveTenant was called with requireWrite=false, and this mutates both
	// the tenant file and (in a team context) the registry index — check both
	// leases before mutating either, same reasoning as SetMember.
	if err := e.requireTenantPrimary(path); err != nil {
		return err
	}
	if teamID != "" {
		if err := e.requireRegistryPrimary(); err != nil {
			return err
		}
	}
	if err := mdb.RemoveMember(targetID); err != nil {
		return err
	}
	if teamID != "" {
		if err := e.registry.UnindexTeamMember(teamID, targetID); err != nil {
			return fmt.Errorf("unindex member: %w", err)
		}
	}
	return nil
}

// MemberCandidate is the minimal identity info a manager needs to invite someone
// by email — enough to confirm they matched the right person, and nothing more:
// no API key, no home path, no admin flag.
type MemberCandidate struct {
	IdentityID string `json:"identity_id"`
	Name       string `json:"name,omitempty"`
	Email      string `json:"email,omitempty"`
}

// LookupMemberByEmail resolves an exact email to an identity a manager can then
// invite. It is deliberately an exact-match confirm, never a search or a listing:
// the caller must be a manager acting in a real team, and no partial match or
// enumeration is possible, so it can't be turned into a directory-harvesting
// oracle beyond confirming an address a manager already knows. Returns not_found
// for both an unknown and a malformed email, so the two are indistinguishable.
func (e *Engine) LookupMemberByEmail(apiKey, email string) (*MemberCandidate, error) {
	identity, mdb, _, teamID, _, release, err := e.resolveTenant(apiKey, false)
	defer release()
	if err != nil {
		return nil, err
	}
	// Only meaningful inside a real team: a personal corpus resolves the caller to
	// a synthetic owner, which would otherwise hand every logged-in user an
	// account-existence oracle. Require an actual team context.
	if teamID == "" {
		return nil, &ActionableError{Code: "forbidden", Message: "select a team to look up members"}
	}
	caller, err := e.memberOf(mdb, identity.ID)
	if err != nil {
		return nil, err
	}
	if !caller.Role.CanManageMembers() {
		return nil, &ActionableError{Code: "forbidden", Message: "your role cannot manage members"}
	}
	target, err := e.registry.LookupByEmail(email)
	if err != nil {
		return nil, fmt.Errorf("lookup by email: %w", err)
	}
	if target == nil {
		return nil, &ActionableError{Code: "not_found", Message: "no account with that email"}
	}
	return &MemberCandidate{IdentityID: target.ID, Name: target.Name, Email: target.Email}, nil
}

// inviteTTL bounds how long a pending team invite stays acceptable.
const inviteTTL = 7 * 24 * time.Hour

// InviteMember creates a pending invite for an email that has no rekam
// account yet — the gap SetMember/LookupMemberByEmail can't cover, since both
// require the target identity to already exist (see IDNT-20's sibling gap,
// filed as "Team invites require the invitee to already have an account").
// Returns the token and the team's name (so the transport layer can put a
// human-readable team name in the invite email without a second lookup); the
// caller is responsible for delivering the token, exactly as with
// CreateUnconfirmed/ForgotPassword's tokens. AcceptInvite is the other half.
func (e *Engine) InviteMember(apiKey, email string, role db.Role, readGrants, titleGrants []string) (token, teamName string, err error) {
	identity, mdb, _, teamID, _, release, err := e.resolveTenant(apiKey, false)
	defer release()
	if err != nil {
		return "", "", err
	}
	if teamID == "" {
		return "", "", &ActionableError{Code: "forbidden", Message: "select a team to invite into"}
	}
	caller, err := e.memberOf(mdb, identity.ID)
	if err != nil {
		return "", "", err
	}
	if !caller.Role.CanManageMembers() {
		return "", "", &ActionableError{Code: "forbidden", Message: "your role cannot manage members"}
	}
	if !db.ValidRole(role) {
		return "", "", &ActionableError{Code: "invalid_role", Message: fmt.Sprintf("unknown role %q", role)}
	}
	// Same rule as SetMember: only an Owner may mint another Owner.
	if role == db.RoleOwner && !caller.Role.CanManageTeam() {
		return "", "", &ActionableError{Code: "forbidden", Message: "only an owner can invite an owner"}
	}
	email = db.NormalizeEmail(email)
	if err := db.ValidateEmail(email); err != nil {
		return "", "", &ActionableError{Code: "invalid_email", Message: err.Error()}
	}
	if existing, err := e.registry.LookupByEmail(email); err != nil {
		return "", "", fmt.Errorf("lookup by email: %w", err)
	} else if existing != nil {
		return "", "", &ActionableError{Code: "already_registered", Message: "that email already has an account — look it up and add it directly instead"}
	}
	// Best-effort here (an invite is not yet a seat); AcceptInvite re-checks
	// authoritatively, since another member could fill the last seat between
	// invite and accept.
	if err := e.checkSeatLimit(mdb, teamID); err != nil {
		return "", "", err
	}
	team, err := e.registry.TeamByID(teamID)
	if err != nil {
		return "", "", fmt.Errorf("look up team: %w", err)
	}
	token, err = e.registry.StoreInvite(teamID, email, role, readGrants, titleGrants, identity.ID, inviteTTL)
	if err != nil {
		return "", "", fmt.Errorf("store invite: %w", err)
	}
	return token, team.Name, nil
}

// AcceptInvite completes a pending invite for the signed-in caller, creating
// their membership in the invited team. The caller's own identity email must
// match the invite's — the token alone moving a forwarded/leaked invite onto
// a different account is exactly what this guards against.
func (e *Engine) AcceptInvite(apiKey, token string) (*db.Member, error) {
	identity, _, err := e.resolveBearer(apiKey)
	if err != nil {
		return nil, &ActionableError{Code: "auth_failed", Message: err.Error()}
	}
	inv, ok, err := e.registry.ResolveInvite(token)
	if err != nil {
		return nil, fmt.Errorf("resolve invite: %w", err)
	}
	if !ok {
		return nil, &ActionableError{Code: "not_found", Message: "invalid or expired invite"}
	}
	if db.NormalizeEmail(identity.Email) != inv.Email {
		return nil, &ActionableError{Code: "forbidden", Message: "this invite was sent to a different email address"}
	}
	team, err := e.registry.TeamByID(inv.TeamID)
	if err != nil {
		return nil, &ActionableError{Code: "not_found", Message: "no such team"}
	}
	if err := e.requireTenantPrimary(team.Path); err != nil {
		return nil, err
	}
	if err := e.requireRegistryPrimary(); err != nil {
		return nil, err
	}
	mdb, release, err := e.manager.Instance(team.Path).Acquire()
	if err != nil {
		return nil, fmt.Errorf("open team: %w", err)
	}
	defer release()
	if existing, _ := mdb.Member(identity.ID); existing == nil {
		if err := e.checkSeatLimit(mdb, inv.TeamID); err != nil {
			return nil, err
		}
	}
	if err := mdb.PutMember(identity.ID, inv.Role, inv.ReadGrants, inv.TitleGrants, inv.InvitedBy); err != nil {
		return nil, fmt.Errorf("accept invite: %w", err)
	}
	if err := e.registry.IndexTeamMember(inv.TeamID, identity.ID); err != nil {
		return nil, fmt.Errorf("index member: %w", err)
	}
	if err := e.registry.DeleteInvite(token); err != nil {
		return nil, fmt.Errorf("delete invite: %w", err)
	}
	return mdb.Member(identity.ID)
}

// TeamRef is a team the caller can act in, as returned by MyTeams.
type TeamRef struct {
	ID   string  `json:"id"`   // "" for the personal home corpus
	Name string  `json:"name"` // "Personal" for home; the team name otherwise
	Home bool    `json:"home,omitempty"`
	Role db.Role `json:"role"`
	// Active marks the workspace this particular bearer is acting in. An OAuth
	// connection is pinned to one for its whole life, so an agent reading this
	// list needs to know which entry it is actually talking to.
	Active bool `json:"active,omitempty"`
}

// MyTeams lists the corpora the caller can act in: their personal home first,
// then every team they have been added to. Each carries the caller's role there,
// so a client can render a team switcher without a second round trip.
func (e *Engine) MyTeams(apiKey string) ([]*TeamRef, error) {
	identity, activeTeam, err := e.resolveBearer(apiKey)
	if err != nil {
		return nil, &ActionableError{Code: "auth_failed", Message: err.Error()}
	}
	out := []*TeamRef{}

	// Home: the identity's own corpus. Its role is whatever EffectiveMember says
	// there — Owner for a personal (member-less) file, or a real row if home is
	// itself a shared file.
	if home, err := e.GetOrOpenMem(identity.MemoryPath); err == nil {
		if m, denied, merr := home.EffectiveMember(identity.ID); merr == nil && !denied {
			out = append(out, &TeamRef{Name: "Personal", Home: true, Role: m.Role, Active: activeTeam == ""})
		}
	}

	teams, err := e.registry.TeamsForIdentity(identity.ID)
	if err != nil {
		return nil, err
	}
	for _, t := range teams {
		ref := &TeamRef{ID: t.ID, Name: t.Name, Role: db.RoleViewer, Active: t.ID == activeTeam}
		// The registry index says the team exists and is joinable; the role is
		// authoritative in the tenant file.
		if mdb, err := e.GetOrOpenMem(t.Path); err == nil {
			if m, denied, merr := mdb.EffectiveMember(identity.ID); merr == nil && !denied {
				ref.Role = m.Role
			}
		}
		out = append(out, ref)
	}
	return out, nil
}

// maxOwnedTeams caps how many teams a single identity may create — the seat
// cap on an individual team (Engine.checkSeatLimit) means nothing if nothing
// limits how many teams the same identity can spin up around it. Not a
// per-plan value (there is no per-identity plan, only a per-team one): a flat
// ceiling on team creation, independent of what plan each team then carries.
// An account that legitimately needs more is a manual/support case for now,
// not a self-service upgrade.
const maxOwnedTeams = 2

// CreateTeam creates a new shared corpus and makes the caller its Owner. The
// team is a fresh file (its own memory), distinct from the caller's personal
// home, so "different memory in different team" holds by construction. path is
// supplied by the caller (the transport layer owns filesystem layout, as with
// CreateUser). A team-bound session token for the returned id routes subsequent
// requests into it.
func (e *Engine) CreateTeam(apiKey, name, path string) (*db.Team, error) {
	identity, _, err := e.resolveBearer(apiKey)
	if err != nil {
		return nil, &ActionableError{Code: "auth_failed", Message: err.Error()}
	}
	if strings.TrimSpace(name) == "" {
		return nil, &ActionableError{Code: "missing_fields", Message: "team name is required"}
	}
	if err := e.requireRegistryPrimary(); err != nil {
		return nil, err
	}
	// The limit is enforced inside CreateTeam itself (one atomic INSERT ...
	// WHERE COUNT(...) < maxOwned) — a separate CountOwnedTeams pre-check
	// here would just recreate the race it closes: two concurrent calls could
	// each read "one seat free" before either commits. See db.ErrTeamLimit.
	team, err := e.registry.CreateTeam(name, path, identity.ID, maxOwnedTeams)
	if err != nil {
		if errors.Is(err, db.ErrTeamLimit) {
			owned, _ := e.registry.CountOwnedTeams(identity.ID)
			return nil, &ActionableError{
				Code:    "team_limit",
				Message: fmt.Sprintf("you already own %d teams, the current limit — contact support to raise it", maxOwnedTeams),
				Context: map[string]any{"owned_teams": owned, "limit": maxOwnedTeams},
			}
		}
		return nil, fmt.Errorf("create team: %w", err)
	}
	// Seed the creator as Owner in the tenant file and index their access. From
	// this point the file HAS members, so the solo-owner fallback no longer
	// applies and non-members are denied.
	mdb, err := e.GetOrOpenMem(path)
	if err != nil {
		return nil, fmt.Errorf("open new team store: %w", err)
	}
	if err := mdb.PutMember(identity.ID, db.RoleOwner, nil, nil, identity.ID); err != nil {
		return nil, fmt.Errorf("seed owner: %w", err)
	}
	if err := e.registry.IndexTeamMember(team.ID, identity.ID); err != nil {
		return nil, fmt.Errorf("index owner: %w", err)
	}
	return team, nil
}

// RenameTeam changes the display name of the team the caller is acting in.
// Managers (Owner and Admin) may rename; the name is a label, not a capability,
// so it does not need the stricter team-ownership gate that deletion does.
func (e *Engine) RenameTeam(apiKey, name string) (*db.Team, error) {
	_, teamID, caller, err := e.resolveTeamManager(apiKey)
	if err != nil {
		return nil, err
	}
	if !caller.Role.CanManageMembers() {
		return nil, &ActionableError{Code: "forbidden", Message: "your role cannot rename the team"}
	}
	if strings.TrimSpace(name) == "" {
		return nil, &ActionableError{Code: "missing_fields", Message: "team name is required"}
	}
	if err := e.requireRegistryPrimary(); err != nil {
		return nil, err
	}
	if err := e.registry.RenameTeam(teamID, strings.TrimSpace(name)); err != nil {
		return nil, fmt.Errorf("rename team: %w", err)
	}
	return e.registry.TeamByID(teamID)
}

// DeleteTeam soft-deletes the team the caller is acting in: it disappears from
// every member's switcher and can no longer be selected, while its corpus file
// stays on disk (see Registry.SoftDeleteTeam). Owners only — an Admin manages
// people within a team, but disposing of the team itself belongs to whoever owns
// it. There is no last-owner guard here; unlike removing an owner, deleting is
// the deliberate end of the team, not an accident that strands it.
func (e *Engine) DeleteTeam(apiKey string) error {
	_, teamID, caller, err := e.resolveTeamManager(apiKey)
	if err != nil {
		return err
	}
	if !caller.Role.CanManageTeam() {
		return &ActionableError{Code: "forbidden", Message: "only an owner can delete the team"}
	}
	if err := e.requireRegistryPrimary(); err != nil {
		return err
	}
	return e.registry.SoftDeleteTeam(teamID)
}

// resolveTeamManager resolves the caller inside the team named by their selector
// and returns their membership there. It refuses a personal corpus: home has no
// team record to rename or delete, and its synthetic owner role would otherwise
// pass every capability check.
func (e *Engine) resolveTeamManager(apiKey string) (identityID, teamID string, caller *db.Member, err error) {
	identity, mdb, _, teamID, _, release, err := e.resolveTenant(apiKey, false)
	defer release()
	if err != nil {
		return "", "", nil, err
	}
	if teamID == "" {
		return "", "", nil, &ActionableError{Code: "forbidden", Message: "select a team first"}
	}
	caller, err = e.memberOf(mdb, identity.ID)
	if err != nil {
		return "", "", nil, err
	}
	return identity.ID, teamID, caller, nil
}

// guardLastOwner returns an error when the tenant has only one Owner, blocking
// the removal or demotion that would leave it without one.
func (e *Engine) guardLastOwner(mdb *db.MemoryDB) error {
	n, err := mdb.CountOwners()
	if err != nil {
		return err
	}
	if n <= 1 {
		return &ActionableError{Code: "last_owner", Message: "the team must keep at least one owner"}
	}
	return nil
}

// maxTenantBytes is an abuse backstop, not a billing feature: a ceiling on how
// large a single tenant file (personal or team, any plan) may grow before new
// writes are refused, protecting the disk from one runaway account. Generous
// relative to this schema's actual footprint — this is not meant to be a real
// constraint on legitimate use. A var, not a const, so a test can shrink it
// temporarily rather than writing 2 GiB of real data to exercise the ceiling.
var maxTenantBytes int64 = 2 << 30 // 2 GiB

// checkStorageCeiling refuses a write once the tenant file is already at or
// over maxTenantBytes. Called from WriteMemory/UpdateMemory, both of which can
// grow the file; a stat failure is treated as a server error rather than
// silently allowed through, since it means the ceiling can't be verified.
func (e *Engine) checkStorageCeiling(mdb *db.MemoryDB) error {
	size, err := mdb.DiskSize()
	if err != nil {
		return fmt.Errorf("check storage ceiling: %w", err)
	}
	if size >= maxTenantBytes {
		return &ActionableError{
			Code:    "storage_limit",
			Message: fmt.Sprintf("this workspace has reached its %d GiB storage limit — contact support", maxTenantBytes>>30),
			Context: map[string]any{"limit_bytes": maxTenantBytes, "current_bytes": size},
		}
	}
	return nil
}

// SetTeamPlan is Registry.SetTeamPlan, exposed here so a caller outside the db
// package (a future Paddle webhook handler in internal/api, or a test
// fixture) can raise a team's seat_limit without reaching into e.registry
// directly. No auth of its own — same as the registry method it wraps, the
// caller (the webhook verifying Paddle's signature, or a trusted test) owns
// authorizing the change.
func (e *Engine) SetTeamPlan(teamID, plan string, seatLimit int) error {
	return e.registry.SetTeamPlan(teamID, plan, seatLimit)
}

// AdminTeam is the admin-console projection of a team: everything
// TeamSummary carries plus its creator resolved to a display name/email, the
// same "id alone is unusable, resolve it" reasoning as TeamMember.
type AdminTeam struct {
	*db.TeamSummary
	CreatedByName  string `json:"created_by_name,omitempty"`
	CreatedByEmail string `json:"created_by_email,omitempty"`
}

// AllTeams lists every non-deleted team in the registry, for the admin
// console's team directory. Auth is the caller's job (requireAdmin at the
// transport layer) — this has no notion of "acting in a team" to check
// against, unlike TeamMembers/MyTeams.
func (e *Engine) AllTeams() ([]*AdminTeam, error) {
	teams, err := e.registry.AllTeams()
	if err != nil {
		return nil, err
	}
	out := make([]*AdminTeam, 0, len(teams))
	for _, t := range teams {
		row := &AdminTeam{TeamSummary: t}
		if t.CreatedBy != "" {
			if id, err := e.registry.ResolveByID(t.CreatedBy); err == nil {
				row.CreatedByName, row.CreatedByEmail = id.Name, id.Email
			}
		}
		out = append(out, row)
	}
	return out, nil
}

// AdminDeleteTeam soft-deletes any team by id, bypassing the Owner-only,
// acting-in-it check DeleteTeam enforces for a team's own member — the admin
// console operates over the registry directly, the same way
// handleRevokeIdentity's admin path does for identities.
func (e *Engine) AdminDeleteTeam(teamID string) error {
	if err := e.requireRegistryPrimary(); err != nil {
		return err
	}
	if err := e.registry.SoftDeleteTeam(teamID); err != nil {
		return &ActionableError{Code: "not_found", Message: err.Error()}
	}
	return nil
}

// CreateSession, ResolveSession, and DeleteSession wrap the registry's
// persisted session store (see db.Registry.StoreSession) for the transport
// layer (internal/api/session.go), which owns the cookie itself but not where
// it's recorded — see IDNT-20.
func (e *Engine) CreateSession(rawID, identityID string, ttl time.Duration) error {
	return e.registry.StoreSession(rawID, identityID, time.Now().Add(ttl))
}

func (e *Engine) ResolveSession(rawID string) (identityID string, ok bool, err error) {
	return e.registry.ResolveSession(rawID)
}

func (e *Engine) DeleteSession(rawID string) error {
	return e.registry.DeleteSession(rawID)
}

// checkSeatLimit returns an error when teamID's roster is already at its plan's
// seat_limit — called only for a genuinely new member (see SetMember), never a
// role change for someone already seated. A team the registry can't resolve
// (shouldn't happen for a live teamID, but cheaper to fail open here than to
// turn a lookup hiccup into a false seat-limit rejection) is let through
// without a check; PutMember's own validation still applies downstream.
func (e *Engine) checkSeatLimit(mdb *db.MemoryDB, teamID string) error {
	team, err := e.registry.TeamByID(teamID)
	if err != nil {
		return nil
	}
	n, err := mdb.CountMembers()
	if err != nil {
		return err
	}
	if n >= team.SeatLimit {
		return &ActionableError{
			Code:    "seat_limit",
			Message: fmt.Sprintf("this team is at its %d-seat limit — upgrade the plan to add more members", team.SeatLimit),
			Context: map[string]any{"seat_limit": team.SeatLimit, "current_seats": n},
		}
	}
	return nil
}

// errTaxonomyForbidden is returned when an identity tries to write, move, or
// delete in a taxonomy outside its write scope. Unlike a read denial (which
// masquerades as not_found to avoid disclosing existence), a write denial is
// explicit: the caller already named the branch, so there is nothing to hide.
func errTaxonomyForbidden(taxonomy string) *ActionableError {
	return &ActionableError{
		Code:    "taxonomy_forbidden",
		Message: fmt.Sprintf("no write permission for taxonomy %q", taxonomy),
		Context: map[string]any{"taxonomy": taxonomy},
	}
}

// asActionable converts a *db.DeletedError or *db.TitleConflictError into the
// wire-level error shape — the former carrying the tombstone so the caller
// learns who deleted the memory and when without a second request, the
// latter carrying the id of the memory that already holds the title so the
// caller can rename either side. Other errors pass through untouched.
func asActionable(err error) error {
	var deleted *db.DeletedError
	if errors.As(err, &deleted) {
		return &ActionableError{
			Code:    "deleted",
			Message: deleted.Error(),
			Context: map[string]any{"tombstone": deleted.Tombstone},
		}
	}
	var titleConflict *db.TitleConflictError
	if errors.As(err, &titleConflict) {
		return &ActionableError{
			Code:    "title_conflict",
			Message: titleConflict.Error(),
			Context: map[string]any{"title": titleConflict.Title, "conflicts_with": titleConflict.ID},
		}
	}
	return err
}

// noop is the release func returned by resolve on the error path, so callers
// can `defer release()` unconditionally.
func noop() {}

// resolve looks up the identity and acquires a lease on the tenant it is acting
// in. The returned release func MUST be called (typically via defer). It is the
// wrapper the read/write methods use; resolveTenant additionally exposes the
// team id for the team-management methods.
func (e *Engine) resolve(apiKey string, requireWrite bool) (*db.Identity, *db.MemoryDB, db.Scope, func(), error) {
	identity, mdb, scope, _, _, release, err := e.resolveTenant(apiKey, requireWrite)
	return identity, mdb, scope, release, err
}

// resolveTenant is resolve with the selected team id exposed. The bearer may
// carry a team selector (a team-bound session token); when it does, the request
// is routed into that team's corpus instead of the identity's home. Either way
// authorization comes from the identity's role in whichever tenant is opened:
// a personal file with no members treats its sole user as Owner (FullScope); a
// team file requires an explicit membership, and a non-member is refused rather
// than defaulted to any access. teamID is "" for the home/personal corpus.
func (e *Engine) resolveTenant(apiKey string, requireWrite bool) (*db.Identity, *db.MemoryDB, db.Scope, string, string, func(), error) {
	identity, teamID, err := e.resolveBearer(apiKey)
	if err != nil {
		return nil, nil, db.Scope{}, "", "", noop, &ActionableError{Code: "auth_failed", Message: err.Error()}
	}
	if requireWrite && !identity.AllowWrite {
		return nil, nil, db.Scope{}, "", "", noop, &ActionableError{
			Code:    "write_not_allowed",
			Message: fmt.Sprintf("identity %q (%s) does not have write permission", identity.Name, identity.ID),
		}
	}
	// Route to the selected team's corpus, or the identity's home when none is
	// selected. A bad/forbidden selector cannot leak: an unknown team id 404s,
	// and a team the identity is not a member of is caught by the membership
	// check below, which is the authoritative gate.
	path := identity.MemoryPath
	if teamID != "" {
		team, err := e.registry.TeamByID(teamID)
		if err != nil {
			return nil, nil, db.Scope{}, "", "", noop, &ActionableError{Code: "not_found", Message: "no such team"}
		}
		path = team.Path
	}
	mdb, release, err := e.manager.Instance(path).Acquire()
	if err != nil {
		return nil, nil, db.Scope{}, "", "", noop, fmt.Errorf("open memory store: %w", err)
	}
	member, denied, err := mdb.EffectiveMember(identity.ID)
	if err != nil {
		release()
		return nil, nil, db.Scope{}, "", "", noop, fmt.Errorf("resolve membership: %w", err)
	}
	if denied {
		release()
		return nil, nil, db.Scope{}, "", "", noop, &ActionableError{
			Code:    "not_a_member",
			Message: "you are not a member of this team",
		}
	}
	if requireWrite && !member.Role.CanWriteAny() {
		release()
		return nil, nil, db.Scope{}, "", "", noop, &ActionableError{
			Code:    "write_not_allowed",
			Message: fmt.Sprintf("role %q is read-only", member.Role),
		}
	}
	// Role permission is necessary but not sufficient once replication is
	// configured: this node must also currently hold the tenant's write
	// lease. A read request never checks this — every node may always read
	// its local (possibly slightly stale) replica. See db.Replication.
	//
	// This only covers requireWrite=true callers (the generic content-write
	// API, gated on identity.AllowWrite + role.CanWriteAny()). A few methods
	// resolve with requireWrite=false — a narrower permission check governs
	// them (e.g. CanManageMembers) — but still mutate the tenant file; those
	// call requireTenantPrimary(path) themselves. See SetMember, RemoveMember,
	// SetTaxonomyTemplate.
	if requireWrite {
		if err := e.requireTenantPrimary(path); err != nil {
			release()
			return nil, nil, db.Scope{}, "", "", noop, err
		}
	}
	return identity, mdb, member.Scope(), teamID, path, release, nil
}

// requireTenantPrimary gates a tenant-file mutation on holding path's write
// lease — resolveTenant's own requireWrite path calls this, and so does any
// caller that resolves with requireWrite=false (a different, narrower
// permission check gates it) but still mutates the tenant file. A nil
// replication (today's default) is a no-op.
func (e *Engine) requireTenantPrimary(path string) error {
	if e.replication == nil {
		return nil
	}
	if err := e.replication.RequirePrimary(path); err != nil {
		var notPrimary *db.ErrNotPrimary
		if errors.As(err, &notPrimary) {
			return &ActionableError{
				Code:    "not_primary",
				Message: "this node is not the write-primary for this workspace right now",
				Context: map[string]string{"owner": notPrimary.Owner},
			}
		}
		return fmt.Errorf("check write lease: %w", err)
	}
	return nil
}

// memberOf returns the caller's effective membership in an already-acquired
// tenant, for the capability checks the team-management methods run. It shares
// EffectiveMember's solo-owner fallback, so the sole user of a personal file can
// manage their team-of-one.
func (e *Engine) memberOf(mdb *db.MemoryDB, identityID string) (*db.Member, error) {
	member, denied, err := mdb.EffectiveMember(identityID)
	if err != nil {
		return nil, err
	}
	if denied {
		return nil, &ActionableError{Code: "not_a_member", Message: "you are not a member of this team"}
	}
	return member, nil
}

// PutFile stores a binary blob (an uploaded image) in the tenant's file store
// and returns its generated id plus the owning identity id. The pair forms the
// capability URL /files/<identity>/<id>. Requires write permission. The bearer
// may be a raw API key or a browser session token (resolveIdentity handles both).
func (e *Engine) PutFile(apiKey, mime string, data []byte) (fileID, identityID string, err error) {
	identity, err := e.resolveIdentity(apiKey)
	if err != nil {
		return "", "", &ActionableError{Code: "auth_failed", Message: err.Error()}
	}
	if !identity.AllowWrite {
		return "", "", &ActionableError{
			Code:    "write_not_allowed",
			Message: fmt.Sprintf("identity %q (%s) does not have write permission", identity.Name, identity.ID),
		}
	}
	fdb, release, err := e.filesStore(identity, true)
	defer release()
	if err != nil {
		return "", "", err
	}
	id := uuid.New().String()
	if err := fdb.PutFile(id, mime, data); err != nil {
		return "", "", err
	}
	return id, identity.ID, nil
}

// GetFile returns a stored blob given the capability URL parts: the owning
// identity id and the file id. No API key — the UUID pair is the capability.
func (e *Engine) GetFile(identityID, fileID string) (*db.File, error) {
	identity, err := e.registry.ResolveByID(identityID)
	if err != nil {
		return nil, &ActionableError{Code: "not_found", Message: "file not found"}
	}
	fdb, release, err := e.filesStore(identity, false)
	defer release()
	if err != nil {
		return nil, err
	}
	f, err := fdb.GetFile(fileID)
	if err != nil && strings.Contains(err.Error(), "file not found") {
		return nil, &ActionableError{Code: "not_found", Message: "file not found"}
	}
	if err != nil {
		return nil, err
	}
	return f, nil
}

// filesStore acquires the *.files SQLite handle for an identity. Blobs live in a
// separate DB (own file + connection) so binary I/O never contends with the
// FTS-triggered memories connection.
func (e *Engine) filesStore(identity *db.Identity, requireWrite bool) (*db.MemoryDB, func(), error) {
	// Sibling of the memory file with a .files extension: default.rekam →
	// default.files, alice.memory → alice.files.
	mp := identity.MemoryPath
	path := strings.TrimSuffix(mp, filepath.Ext(mp)) + ".files"
	// InstanceWith always takes an explicit opener (unlike Instance, which
	// picks up ManagerOptions.DefaultOpen automatically), so replication has
	// to be threaded through here too — see db.ReplicatedOpenWith.
	open := db.OpenFileStore
	if e.replication != nil {
		open = db.ReplicatedOpenWith(e.replication, db.OpenFileStore)
	}
	fdb, release, err := e.manager.InstanceWith(path, open).Acquire()
	if err != nil {
		return nil, noop, fmt.Errorf("open file store: %w", err)
	}
	// Mirrors resolveTenant's write-lease gate: a read (GetFile) never checks
	// this, every node may always serve its local (possibly slightly stale)
	// blob replica.
	if requireWrite && e.replication != nil {
		if err := e.replication.RequirePrimary(path); err != nil {
			release()
			var notPrimary *db.ErrNotPrimary
			if errors.As(err, &notPrimary) {
				return nil, noop, &ActionableError{
					Code:    "not_primary",
					Message: "this node is not the write-primary for this workspace right now",
					Context: map[string]string{"owner": notPrimary.Owner},
				}
			}
			return nil, noop, fmt.Errorf("check write lease: %w", err)
		}
	}
	return fdb, release, nil
}

// resolveIdentity maps a bearer (either a raw API key or a signed session token)
// to its identity, without touching the memory store. Any team selector on the
// bearer is ignored; use resolveBearer when the team matters.
func (e *Engine) resolveIdentity(apiKey string) (*db.Identity, error) {
	identity, _, err := e.resolveBearer(apiKey)
	return identity, err
}

// resolveBearer maps a bearer to its identity and any team it selects. A raw API
// key selects no team (home); a team-bound session token carries one. The
// selector is only a routing hint — access to the team is still gated by the
// membership check in resolveTenant.
func (e *Engine) resolveBearer(apiKey string) (*db.Identity, string, error) {
	if id, teamID, ok := e.identityFromSessionToken(apiKey); ok {
		identity, err := e.registry.ResolveByID(id)
		return identity, teamID, err
	}
	// A delegated OAuth grant authenticates as the real user and carries the team
	// it was authorized for, so an MCP connection lands in the chosen workspace
	// with that person's own role there. Checked before the plain identity lookup;
	// legacy grants own a hidden identity and fall through to it.
	if identity, teamID, ok, err := e.registry.ResolveGrant(apiKey); err != nil {
		return nil, "", err
	} else if ok {
		return identity, teamID, nil
	}
	identity, err := e.registry.Resolve(apiKey)
	return identity, "", err
}

// GetOrOpenMem returns the MemoryDB at path, opening it if needed. The handle is
// released immediately, so it remains subject to idle reaping.
func (e *Engine) GetOrOpenMem(path string) (*db.MemoryDB, error) {
	mdb, release, err := e.manager.Instance(path).Acquire()
	if err != nil {
		return nil, err
	}
	release()
	return mdb, nil
}

// ListResult holds a page of memories and the total matching count.
type ListResult struct {
	Memories []*db.Memory
	Total    int
}

// ListMemories returns a page of memories for the authenticated identity.
func (e *Engine) ListMemories(apiKey, taxonomy string, limit, offset int) (*ListResult, error) {
	_, mdb, scope, release, err := e.resolve(apiKey, false)
	defer release()
	if err != nil {
		return nil, err
	}
	mems, total, err := mdb.List(scope, taxonomy, limit, offset)
	if err != nil {
		return nil, err
	}
	if mems == nil {
		mems = []*db.Memory{}
	}
	return &ListResult{Memories: mems, Total: total}, nil
}

// GetCurrentIdentity returns the identity associated with the given API key.
func (e *Engine) GetCurrentIdentity(apiKey string) (*db.Identity, error) {
	identity, _, _, release, err := e.resolve(apiKey, false)
	defer release()
	if err != nil {
		return nil, err
	}
	return identity, nil
}

// ScopeMemories returns trimmed memories (no content) within a taxonomy prefix.
func (e *Engine) ScopeMemories(apiKey, taxonomy string, tokenBudget int) (*db.ScopeResult, error) {
	if taxonomy == "" {
		return nil, &ActionableError{Code: "missing_taxonomy", Message: "taxonomy is required"}
	}
	_, mdb, scope, release, err := e.resolve(apiKey, false)
	defer release()
	if err != nil {
		return nil, err
	}
	budget := 2000
	if tokenBudget > 0 {
		budget = tokenBudget
	}
	return mdb.Scope(scope, taxonomy, budget)
}

// GetSkillsManifest returns skills (taxonomy prefix "skills") without content.
func (e *Engine) GetSkillsManifest(apiKey string) (*db.ScopeResult, error) {
	_, mdb, scope, release, err := e.resolve(apiKey, false)
	defer release()
	if err != nil {
		return nil, err
	}
	return mdb.Scope(scope, "skills", 0)
}

// RebuildFTS rebuilds the FTS5 index and link graph for the authenticated
// identity's memory store.
func (e *Engine) RebuildFTS(apiKey string) error {
	_, mdb, _, release, err := e.resolve(apiKey, false)
	defer release()
	if err != nil {
		return err
	}
	if err := mdb.RebuildFTS(); err != nil {
		return err
	}
	return mdb.RebuildEdges()
}

// EdgeHealth returns link-graph statistics (resolved vs. dangling edges) for the
// authenticated identity's memory store — a readout of how well linking is used.
func (e *Engine) EdgeHealth(apiKey string) (*db.EdgeHealth, error) {
	_, mdb, scope, release, err := e.resolve(apiKey, false)
	defer release()
	if err != nil {
		return nil, err
	}
	return mdb.EdgeHealth(scope)
}

// Graph returns a slice of the link graph for the authenticated identity's store.
// With center set it returns the ego graph (neighborhood within depth hops);
// otherwise the corpus graph, optionally scoped to a taxonomy prefix. This drives
// the mindmap view over the typed [[wiki-link]] edges.
func (e *Engine) Graph(apiKey, center string, depth int, taxonomy string) (*db.Graph, error) {
	_, mdb, scope, release, err := e.resolve(apiKey, false)
	defer release()
	if err != nil {
		return nil, err
	}
	g, err := mdb.Graph(scope, center, depth, taxonomy)
	if err != nil && strings.Contains(err.Error(), "memory not found") {
		return nil, &ActionableError{Code: "not_found", Message: err.Error()}
	}
	if err != nil {
		return nil, err
	}
	return g, nil
}

// AllIdentities returns every identity in the registry. Authorization is the
// caller's responsibility — the HTTP layer gates this behind the admin key, so
// the admin key need not itself be a stored identity.
func (e *Engine) AllIdentities() ([]*db.Identity, error) {
	return e.registry.List()
}

// CreateIdentity registers a bare identity — no email, no password, no login.
// The account exists only so something inside the process can act as it, which
// today means the read-only account serving the public docs corpus
// (api.EnableDocs). Human accounts go through CreateUser.
func (e *Engine) CreateIdentity(name, rawKey, memoryPath string, allowWrite bool) (*db.Identity, error) {
	if err := e.requireRegistryPrimary(); err != nil {
		return nil, err
	}
	return e.registry.Create(name, rawKey, memoryPath, allowWrite)
}

// SetIdentityMemoryPath repoints an identity at a different corpus file.
func (e *Engine) SetIdentityMemoryPath(id, memoryPath string) error {
	if err := e.requireRegistryPrimary(); err != nil {
		return err
	}
	return e.registry.SetMemoryPath(id, memoryPath)
}

// MintGrant issues a fresh per-client API key for the OAuth flow. baseKey is the
// server's master key; the minted key inherits its memory_path and write
// permission but is independently revocable. Returns the raw key (surface once)
// and the grant id.
func (e *Engine) MintGrant(baseKey, clientID string) (rawKey, grantID string, err error) {
	if err := e.requireRegistryPrimary(); err != nil {
		return "", "", err
	}
	base, err := e.registry.Resolve(baseKey)
	if err != nil {
		return "", "", &ActionableError{Code: "auth_failed", Message: "oauth base key is not configured or invalid"}
	}
	if clientID == "" {
		clientID = "unknown"
	}
	g, key, err := e.registry.MintGrant(clientID, "oauth:"+clientID, base.MemoryPath, base.AllowWrite)
	if err != nil {
		return "", "", err
	}
	return key, g.ID, nil
}

// MintGrantByID issues a fresh per-client API key bound to an already-resolved
// identity (by id). Used so the OAuth flow can mint against the currently
// logged-in browser user instead of the server master key.
// The grant is delegated: it authenticates as the user rather than as a hidden
// identity of its own, which is what lets the connection see their teams at all.
// teamID pins it to one workspace ("" for personal memory); the pin is a ceiling,
// not a permission — membership is still checked on every request.
func (e *Engine) MintGrantByID(identityID, clientID, teamID string) (rawKey, grantID string, err error) {
	if err := e.requireRegistryPrimary(); err != nil {
		return "", "", err
	}
	if _, err := e.registry.ResolveByID(identityID); err != nil {
		return "", "", &ActionableError{Code: "auth_failed", Message: "oauth identity not found"}
	}
	if clientID == "" {
		clientID = "unknown"
	}
	// Refuse to bind a workspace the user is not actually in, so a tampered
	// consent form cannot mint a token aimed at someone else's team. Requests
	// would be denied anyway; failing here makes it visible at authorize time.
	if teamID != "" {
		if err := e.assertMemberOfTeam(identityID, teamID); err != nil {
			return "", "", err
		}
	}
	g, key, err := e.registry.MintDelegatedGrant(clientID, identityID, teamID)
	if err != nil {
		return "", "", err
	}
	return key, g.ID, nil
}

// assertMemberOfTeam reports whether an identity holds a member row in a team.
func (e *Engine) assertMemberOfTeam(identityID, teamID string) error {
	team, err := e.registry.TeamByID(teamID)
	if err != nil {
		return &ActionableError{Code: "not_found", Message: "no such team"}
	}
	mdb, err := e.GetOrOpenMem(team.Path)
	if err != nil {
		return fmt.Errorf("open team store: %w", err)
	}
	m, denied, err := mdb.EffectiveMember(identityID)
	if err != nil {
		return err
	}
	if denied || m == nil {
		return &ActionableError{Code: "forbidden", Message: "you are not a member of this team"}
	}
	return nil
}

// StoreOAuthCode, TakeOAuthCode, StoreConsentTicket, and TakeConsentTicket are
// thin passthroughs to the registry's persisted OAuth flow state (see
// db.Registry — oauth_codes/oauth_consents). Gated the same way every other
// registry mutation is: a no-op check when replication is off, and refused
// with a forwarding hint on a non-primary node when it's on, so the flow
// doesn't half-complete against a node that can't durably record it.

func (e *Engine) StoreOAuthCode(code string, entry db.OAuthCodeEntry) error {
	if err := e.requireRegistryPrimary(); err != nil {
		return err
	}
	return e.registry.StoreOAuthCode(code, entry)
}

// TakeOAuthCode is gated the same as Store: consuming a code is a DELETE, and
// under replication a non-primary node's local registry file is a read-only
// replica of the leaseholder's — writing to it locally would silently diverge
// from what litestream is actually replicating, not just fail loudly.
func (e *Engine) TakeOAuthCode(code string) (db.OAuthCodeEntry, bool, error) {
	if err := e.requireRegistryPrimary(); err != nil {
		return db.OAuthCodeEntry{}, false, err
	}
	return e.registry.TakeOAuthCode(code)
}

func (e *Engine) StoreConsentTicket(ticket string, entry db.ConsentTicketEntry) error {
	if err := e.requireRegistryPrimary(); err != nil {
		return err
	}
	return e.registry.StoreConsentTicket(ticket, entry)
}

// TakeConsentTicket: see TakeOAuthCode for why redemption is gated too.
func (e *Engine) TakeConsentTicket(ticket string) (db.ConsentTicketEntry, bool, error) {
	if err := e.requireRegistryPrimary(); err != nil {
		return db.ConsentTicketEntry{}, false, err
	}
	return e.registry.TakeConsentTicket(ticket)
}

// PutOAuthClient records a dynamic client registration (RFC 7591) so
// /authorize has something to validate a redirect_uri against. Gated like
// every other registry mutation: see StoreOAuthCode above.
func (e *Engine) PutOAuthClient(clientID string, redirectURIs []string) error {
	if err := e.requireRegistryPrimary(); err != nil {
		return err
	}
	return e.registry.PutOAuthClient(clientID, redirectURIs)
}

// OAuthClient returns a registration, or nil when the client is unknown. A
// read, so it is not gated on being the registry primary — a replica can
// answer it from its own copy.
func (e *Engine) OAuthClient(clientID string) (*db.OAuthClient, error) {
	return e.registry.OAuthClient(clientID)
}

// RevokeGrant revokes a previously issued OAuth grant by id.
func (e *Engine) RevokeGrant(grantID string) error {
	if err := e.requireRegistryPrimary(); err != nil {
		return err
	}
	return e.registry.RevokeGrant(grantID)
}

// ListGrants returns all OAuth grants (active and revoked).
func (e *Engine) ListGrants() ([]*db.Grant, error) { return e.registry.ListGrants() }

// RegistryStats returns aggregate registry counts for the admin dashboard.
func (e *Engine) RegistryStats() (*db.Stats, error) { return e.registry.Stats() }

// DeleteIdentity removes an identity (API key) from the registry by id. The
// caller is responsible for authorizing this (admin or same-identity scope).
func (e *Engine) DeleteIdentity(id string) error {
	if err := e.requireRegistryPrimary(); err != nil {
		return err
	}
	return e.registry.Delete(id)
}

var taxonomyRE = regexp.MustCompile(`^[a-zA-Z0-9_]+(\.[a-zA-Z0-9_]+)*$`)

func validTaxonomy(t string) bool { return taxonomyRE.MatchString(t) }
