package api

import "net/http"

// Route groups, split by edition. Which of these a binary registers is
// decided at build time — see edition_*.go — so a solo build does not merely
// disable the control plane, it is compiled without the code that would
// serve it.
//
// The default (no tags) is the **managed** edition, deliberately: that is
// what the deploy targets build and what the hosted service runs, and a
// forgotten tag there would silently ship customers a binary with no signup,
// teams or admin. The smaller editions are opt-in:
//
//	go build                 → managed  (everything)
//	go build -tags team      → team     (shared corpora, no control plane)
//	go build -tags solo      → solo     (one person, no teams)
//
// It also keeps `go test ./...` meaningful, since the suite exercises the
// full route surface.

// registerCoreRoutes is everything every edition has: reading and writing
// memory, search, OAuth (needed by MCP clients regardless of edition),
// signing in to the web UI, files, and the public docs mirror.
func (s *Server) registerCoreRoutes(mux *http.ServeMux) {
	// OAuth 2.0 endpoints (required for claude.ai remote MCP)
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", s.handleOAuthMeta)
	mux.HandleFunc("POST /register", s.handleOAuthRegister)
	mux.HandleFunc("GET /authorize", s.handleAuthorizeForm)
	mux.HandleFunc("POST /authorize", s.rateLimit(s.authLimiter, "auth", s.handleAuthorizeSubmit))
	// Second consent step. Not rate-limited as an auth attempt: it carries a
	// single-use ticket, not a password.
	mux.HandleFunc("POST /authorize/workspace", s.handleAuthorizeWorkspace)
	mux.HandleFunc("POST /token", s.rateLimit(s.authLimiter, "auth", s.handleOAuthToken))

	// Browser session (httpOnly cookie) — self-service signup + email/password
	// login. The session maps to the user's own identity; no raw key in the browser.
	// Unauthenticated credential/account endpoints are rate-limited per client IP.
	mux.HandleFunc("POST /ui/login", s.rateLimit(s.authLimiter, "auth", s.handleUILogin))
	mux.HandleFunc("GET /ui/session", s.handleUISession)
	mux.HandleFunc("POST /ui/logout", s.handleUILogout)
	mux.HandleFunc("POST /ui/forgot", s.rateLimit(s.signupLimiter, "signup", s.handleUIForgot))
	mux.HandleFunc("POST /ui/reset", s.rateLimit(s.authLimiter, "auth", s.handleUIReset))

	// Prometheus scrape target — see metrics.go. Same requireAdmin gate; a
	// scrape_config points its `authorization` credentials at REKAM_ADMIN_KEY.
	mux.HandleFunc("GET /metrics", s.handleMetrics)

	// API routes
	mux.HandleFunc("GET /scope", s.handleScope)
	mux.HandleFunc("POST /admin/rebuild-fts", s.handleRebuildFTS)
	mux.HandleFunc("GET /me", s.handleMe)
	mux.HandleFunc("GET /identities", s.handleListIdentities)
	mux.HandleFunc("DELETE /identities/{id}", s.handleRevokeIdentity)
	mux.HandleFunc("GET /memories", s.handleListMemories)
	mux.HandleFunc("GET /catalog", s.handleCatalog)
	mux.HandleFunc("GET /taxonomy/template", s.handleTaxonomyTemplate)
	mux.HandleFunc("PUT /taxonomy/template", s.handleSetTaxonomyTemplate)
	mux.HandleFunc("GET /admin/edge-health", s.handleEdgeHealth)
	mux.HandleFunc("GET /graph", s.handleGraph)
	mux.HandleFunc("GET /suggest-links", s.handleSuggestLinks)
	mux.HandleFunc("GET /review", s.handleReviewDue)
	mux.HandleFunc("POST /review/{id}", s.handleAddReview)
	mux.HandleFunc("DELETE /review/{id}", s.handleRemoveReview)
	mux.HandleFunc("POST /review/{id}/grade", s.handleGradeReview)
	mux.HandleFunc("GET /export", s.handleExport)
	mux.HandleFunc("GET /export/{id}", s.handleExportMemory)
	mux.HandleFunc("GET /search", s.handleSearch)
	mux.HandleFunc("GET /memory/{id}", s.handleGetMemory)
	mux.HandleFunc("POST /memory", s.handleWriteMemory)
	mux.HandleFunc("PATCH /memory/{id}", s.handleUpdateMemory)
	mux.HandleFunc("DELETE /memory/{id}", s.handleDeleteMemory)
	mux.HandleFunc("GET /deleted", s.handleDeletedMemories)
	mux.HandleFunc("GET /memory/{id}/revisions", s.handleMemoryHistory)
	mux.HandleFunc("POST /memory/{id}/restore/{version}", s.handleRestoreRevision)

	// File blob store (uploaded images) — kept in a separate *.files SQLite.
	// GET is a capability URL (unguessable identity/file UUID pair), so it must
	// stay reachable without a session (see sessionAuth's public-path allowance).
	mux.HandleFunc("POST /files", s.handleUploadFile)
	mux.HandleFunc("GET /files/{identity}/{id}", s.handleGetFile)

}
