package api

import (
	"fmt"
	"net/http"

	"github.com/Ucok23/rekam/internal/db"
	"github.com/Ucok23/rekam/internal/docs"
)

// The public docs corpus — one team, served read-only to anyone at /public/*,
// with the SPA rendering it at /docs. It exists so a stranger can read rekam's
// documentation in the real product UI without an account, which is both the
// docs and the demo.
//
// Everything here is built to fail closed. The corpus is off unless explicitly
// configured; configuration is rejected unless the identity is read-only by two
// independent measures (see EnableDocs); and only the five handlers listed
// in registerPublicRoutes are ever reachable. There is deliberately no code
// path from /public/* to a write. See spec/docs.md.

// docsCorpus is the resolved configuration for the public docs team. A nil
// *docsCorpus on the Server means the feature is disabled and every /public/*
// route 404s, which is the state of any deployment that switched it off with
// REKAM_DOCS=off or whose corpus failed to build.
type docsCorpus struct {
	// bearer is a team-bound session token for the viewer identity, minted once
	// at configuration time. It never leaves the process: publicRead stamps it
	// onto the request just before the handler runs, replacing whatever the
	// caller sent.
	bearer string

	identityID string
	path       string

	// Sync counts from the last boot, reported by cmd/rekam so a deploy says
	// what it published.
	Inserted, Updated, Removed int
}

// docsIdentityName is the registry name of the account the public surface reads
// as. It is a fixed, recognisable name so an operator listing identities can
// tell what it is, and so EnableDocs finds the same one on every boot instead
// of creating a new account per restart.
const docsIdentityName = "rekam-docs"

// EnableDocs builds the public docs corpus and starts serving it.
//
// The documentation is authored as markdown in internal/docs/corpus and shipped
// inside the binary, so this is not an operator setup step — it is closer to a
// migration. On every boot it:
//
//  1. finds or creates a dedicated read-only identity whose home corpus is
//     corpusPath (a file rekam owns, separate from every user and team);
//  2. syncs the embedded markdown into that corpus, so deploying the binary is
//     what publishes a docs change; and
//  3. mints the bearer publicRead stamps onto anonymous requests.
//
// The identity is created with allow_write false and is never granted write.
// That is what makes the account safe to expose: even if a write route were
// mounted under /public by mistake, engine.resolve refuses the moment
// requireWrite is set. Seeding does not go through it — Sync writes to the
// database directly (see internal/docs), so the public account has no write
// path at any layer.
//
// A corpus with no members treats its sole identity as Owner, so reads cover
// the whole file. That is the intent here: everything in this corpus is
// documentation, published on purpose, with nothing private to partition off.
func (s *Server) EnableDocs(corpusPath string) error {
	if corpusPath == "" {
		return fmt.Errorf("no corpus path configured")
	}

	identity, err := s.docsIdentity(corpusPath)
	if err != nil {
		return err
	}
	if identity.AllowWrite {
		return fmt.Errorf("docs identity %s has write permission; it must be read-only", identity.ID)
	}

	mdb, err := s.eng.GetOrOpenMem(corpusPath)
	if err != nil {
		return fmt.Errorf("open docs corpus %s: %w", corpusPath, err)
	}
	inserted, updated, removed, err := docs.Sync(mdb, identity.ID)
	if err != nil {
		return fmt.Errorf("sync docs corpus: %w", err)
	}

	s.docs = &docsCorpus{
		bearer:     s.eng.SessionToken(identity.ID),
		identityID: identity.ID,
		path:       corpusPath,
		Inserted:   inserted,
		Updated:    updated,
		Removed:    removed,
	}
	return nil
}

// docsIdentity returns the read-only account the docs corpus is served as,
// creating it on first boot. Lookup is by name rather than by a stored id so
// there is nothing to configure and nothing to lose: the account is derivable
// from the corpus itself.
func (s *Server) docsIdentity(corpusPath string) (*db.Identity, error) {
	existing, err := s.eng.AllIdentities()
	if err != nil {
		return nil, fmt.Errorf("list identities: %w", err)
	}
	for _, id := range existing {
		if id.Name == docsIdentityName {
			// Keep the path current so moving the corpus file is just a config
			// change rather than a stale account pointing at nothing.
			if id.MemoryPath != corpusPath {
				if err := s.eng.SetIdentityMemoryPath(id.ID, corpusPath); err != nil {
					return nil, fmt.Errorf("repoint docs identity: %w", err)
				}
				id.MemoryPath = corpusPath
			}
			return id, nil
		}
	}

	// The key is never used: the public surface authenticates with a session
	// token minted from the identity id, not with this key, and nothing prints
	// it. It exists because the registry requires one.
	key, err := randomHex(32)
	if err != nil {
		return nil, fmt.Errorf("mint docs key: %w", err)
	}
	id, err := s.eng.CreateIdentity(docsIdentityName, key, corpusPath, false)
	if err != nil {
		return nil, fmt.Errorf("create docs identity: %w", err)
	}
	return id, nil
}

// DocsEnabled reports whether a public docs corpus is configured. cmd/rekam
// logs this at startup so an operator can tell at a glance whether /docs will
// serve anything.
func (s *Server) DocsEnabled() bool { return s.docs != nil }

// registerPublicRoutes mounts the unauthenticated read-only mirror.
//
// The prefix is /public rather than /docs because /docs belongs to the SPA:
// the shell is served there so visitors get clean, shareable URLs
// (/docs, /docs/<record id>), and an API route under the same prefix would
// collide with them.
//
// This list is the entire public surface: browse the taxonomy, list and read
// records, search, and read a record's revisions. Adding a line here is a
// deliberate widening of what an anonymous caller can reach, so keep the set
// minimal and GET-only.
//
// Revisions are here because the docs describe version history, and
// documentation that describes a feature it cannot show is the weaker half of
// a demo. Reading history is a read; restoring one is a write, and there is no
// route for it here (the UI hides the control too, via canWrite).
func (s *Server) registerPublicRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /public/catalog", s.publicRead(s.handleCatalog))
	mux.HandleFunc("GET /public/memories", s.publicRead(s.handleListMemories))
	mux.HandleFunc("GET /public/memory/{id}", s.publicRead(s.handleGetMemory))
	mux.HandleFunc("GET /public/memory/{id}/revisions", s.publicRead(s.handleMemoryHistory))
	mux.HandleFunc("GET /public/search", s.publicRead(s.handleSearch))
}

// publicReadRate is the per-IP budget for anonymous docs reads. Browsing one
// page costs a handful of requests (catalog, list, the record itself), so this
// sits well above a reader's pace and is aimed at scripted scraping.
const publicReadRate = 120

// publicRead turns an ordinary authenticated read handler into an anonymous
// one by substituting the docs bearer for whatever the caller supplied.
//
// The substitution is unconditional, and that is the security property: a
// caller cannot reach any corpus but the docs team, whether they send their own
// Authorization header, an X-Rekam-Team selector, a ?team= parameter, or a
// session cookie for an account with far more access. All of it is discarded
// here, after teamSelector has already run, so nothing downstream can be
// steered by the request.
func (s *Server) publicRead(next http.HandlerFunc) http.HandlerFunc {
	guarded := s.rateLimit(s.publicLimiter, "public", next)
	return func(w http.ResponseWriter, r *http.Request) {
		if s.docs == nil {
			http.NotFound(w, r)
			return
		}

		// Drop every caller-supplied way of naming an identity or a corpus,
		// then name both ourselves. Order matters only for readability; the
		// Set below is what actually decides the tenant.
		r.Header.Del(teamHeader)
		if q := r.URL.Query(); q.Has("team") {
			q.Del("team")
			r.URL.RawQuery = q.Encode()
		}
		r.Header.Set("Authorization", "Bearer "+s.docs.bearer)

		// Docs are edited through the normal app, so a short shared cache
		// keeps a busy public page cheap without making edits feel stuck.
		w.Header().Set("Cache-Control", "public, max-age=60")
		guarded(w, r)
	}
}

// DocsSyncCounts reports what the last docs sync changed, so a deploy can say
// what it published rather than only that it started.
func (s *Server) DocsSyncCounts() (inserted, updated, removed int) {
	if s.docs == nil {
		return 0, 0, 0
	}
	return s.docs.Inserted, s.docs.Updated, s.docs.Removed
}
