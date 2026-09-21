package api

import (
	"compress/gzip"
	"io/fs"
	"net/http"
	"net/netip"
	"strings"
	"time"

	dodopayments "github.com/dodopayments/dodopayments-go"

	"github.com/Ucok23/rekam/internal/email"
	"github.com/Ucok23/rekam/internal/engine"
)

// Server is the HTTP API server.
type Server struct {
	eng      *engine.Engine
	addr     string
	srv      *http.Server
	adminKey string // accepted (as Bearer) for /admin/* — legacy CLI/curl access
	usersDir string // directory where self-service signups create their .rekam files

	// Per-IP brute-force protection on the unauthenticated auth surface.
	// authLimiter guards credential checks (login, OAuth password/token, reset);
	// signupLimiter guards the costlier account-creation / email-sending paths.
	authLimiter   *rateLimiter
	signupLimiter *rateLimiter
	// publicLimiter guards the anonymous read-only docs mirror at /public/*,
	// which is the one surface with no account behind it at all.
	publicLimiter *rateLimiter

	// trustedProxies are the peers whose forwarding headers clientIP believes.
	trustedProxies []netip.Prefix

	// docs is the public read-only corpus served at /public/* and rendered by
	// the SPA at /docs. nil unless EnableDocs succeeded — see public.go.
	docs *docsCorpus

	// forwardClient sends a write rejected with not_primary on to the tenant's
	// current lease-holder — see forward.go. Always set (defaultForwardClient);
	// a field mainly so tests can substitute a client pointed at a local test
	// server instead of the real network.
	forwardClient *http.Client

	// emailer sends the confirm/reset/invite emails minted in session.go and
	// teams_handlers.go. Defaults to email.Noop (see SetEmailer) — every
	// existing test, which never calls SetEmailer, keeps seeing the exact
	// token-in-response fallback behavior it always has.
	emailer email.Emailer

	// dodo is the Dodo Payments (Merchant of Record) client used to open
	// checkout sessions and verify webhook signatures — see payments_dodo.go.
	// nil unless SetDodoClient is called (cmd/rekam/main.go, when
	// DODO_PAYMENTS_API_KEY is set). Like docs (EnableDocs), billing is a
	// managed-service concern by nature — nobody self-hosting has anyone to
	// bill — but that's enforced by registerManagedRoutes never being
	// compiled into a solo/team binary, not by tagging this field.
	dodo *dodopayments.Client
	// dodoPlans maps a Dodo product_id to what buying it grants. Wired
	// alongside dodo by SetDodoProducts.
	dodoPlans map[string]DodoPlan

	// githubReleaseToken authenticates the entitlement-gated Team-edition
	// download (downloads_team.go) against this repo's GitHub Releases API.
	// "" makes handleDownloadTeam respond 503. See SetGithubReleaseToken.
	githubReleaseToken string
}

// DodoPlan is what buying one Dodo product grants: an entitlement kind
// (db.EntitlementManaged or db.EntitlementSelfHostTeam) and the seats that
// come with it while the subscription stays active. Seats is meaningless for
// EntitlementSelfHostTeam, which gates downloads rather than seats — see #34.
type DodoPlan struct {
	Kind  string
	Seats int
}

// SetDodoClient wires a Dodo Payments client into the server — cmd/rekam/main.go
// calls this after NewServer when DODO_PAYMENTS_API_KEY is configured. Tests
// use it the same way to inject a client pointed at a local test server. nil
// (the default) makes handleCreateCheckout respond 503 rather than panic.
func (s *Server) SetDodoClient(c *dodopayments.Client) {
	s.dodo = c
}

// SetDodoProducts wires the product_id → plan mapping used to build checkout
// sessions and to interpret Dodo webhooks.
func (s *Server) SetDodoProducts(plans map[string]DodoPlan) {
	s.dodoPlans = plans
}

// SetGithubReleaseToken wires the token downloads_team.go uses to read
// release assets from this (private) repo's GitHub Releases API —
// cmd/rekam/main.go calls this when GITHUB_RELEASE_TOKEN is set. A
// fine-grained PAT scoped to Contents: read on this repo is all it needs.
func (s *Server) SetGithubReleaseToken(token string) {
	s.githubReleaseToken = token
}

// SetEmailer wires a real Emailer into the server — cmd/rekam/main.go calls
// this after NewServer when RESEND_API_KEY/RESEND_FROM are configured. Tests
// use it the same way to inject a fake and assert on what got sent. Not a
// NewServer parameter: unlike adminKey/usersDir, most callers (every test but
// the ones about email) don't care, and Noop is a correct, safe default.
func (s *Server) SetEmailer(e email.Emailer) {
	s.emailer = e
}

// NewServer creates the HTTP server and registers all routes.
func NewServer(eng *engine.Engine, addr, adminKey, usersDir string) *Server {
	s := &Server{
		eng:      eng,
		addr:     addr,
		adminKey: adminKey,
		usersDir: usersDir,
		// 10 credential attempts/min burst, then 10/min sustained per IP.
		authLimiter: newRateLimiter(10, time.Minute),
		// 5 account-creation / password-reset requests/min per IP.
		signupLimiter:  newRateLimiter(5, time.Minute),
		publicLimiter:  newRateLimiter(publicReadRate, time.Minute),
		trustedProxies: defaultTrustedProxies(),
		forwardClient:  defaultForwardClient(),
		emailer:        email.Noop{},
	}

	mux := http.NewServeMux()

	// Route registration is split by edition — see routes_core.go and the
	// edition_*.go files. A solo build is compiled without the team and
	// control-plane groups entirely.
	s.registerCoreRoutes(mux)
	s.registerEditionRoutes(mux)

	// Static UI at /ui/ — the asset server (JS/CSS/icons/manifest/service
	// worker). Vite's build base is /ui/, so every asset reference the shell
	// emits is already an absolute /ui/... path; that stays true regardless of
	// what URL served the HTML document itself, which is what lets root below
	// serve that same document in place rather than needing its own copy of
	// the asset tree.
	sub, _ := fs.Sub(webFS, "web")
	uiFiles := gzipMiddleware(noCacheIndex(http.StripPrefix("/ui", http.FileServer(http.FS(sub)))))

	// Root serves the SPA shell directly — no redirect to /ui/. That redirect
	// used to be deliberate (a single canonical URL for the installed PWA's
	// scope/start_url, both pinned to /ui/ — see frontend/vite.config.js), but
	// it means every first-time visitor's landing page watches the address
	// bar jump from "/" to "/ui/" before anything paints, which reads badly
	// for a page whose whole job is a stranger's first impression. /ui/ itself
	// is untouched — still the PWA's canonical install URL, and still fine to
	// link directly — this just stops forcing everyone through it. Reads the
	// embedded index.html fresh (rather than hardcoding asset hashes) because
	// Vite regenerates those on every build.
	spaShell := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only the SPA's own document paths get the shell. The app routes on
		// the hash (#/memory/...), so those are just "/" plus the docs
		// mirror; everything else reaching here is an API path that was not
		// registered. Serving HTML for those would answer 200 to an endpoint
		// that does not exist — which matters most across editions, where a
		// solo build genuinely lacks routes a managed one has, and a client
		// asking for /admin/stats should be told "no" rather than handed a
		// web page.
		if !isSPADocumentPath(r.URL.Path) {
			respond(w, http.StatusNotFound, map[string]string{
				"error":   "not_found",
				"message": "no such endpoint in the " + Edition + " edition",
			})
			return
		}
		html, err := fs.ReadFile(sub, "index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(html)
	})

	// Without this, GET /robots.txt falls through to the "/" SPA shell below
	// and crawlers get back index.html instead of a robots file.
	// Allow public landing page and docs while disallowing private tenant, admin, and MCP endpoints.
	mux.HandleFunc("GET /robots.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte("User-agent: *\nAllow: /\nAllow: /docs\nAllow: /ui/\nDisallow: /admin/\nDisallow: /files/\nDisallow: /mcp/\nDisallow: /api/\n"))
	})

	mux.Handle("GET /ui/", uiFiles)
	mux.Handle("GET /", spaShell)

	// instrumentMetrics wraps the whole chain, outermost, so its latency
	// measurement covers everything a request actually pays for (headers,
	// session/team resolution, the handler itself) — but it consults mux
	// directly, ahead of time, only to resolve the route-pattern label; see
	// metrics.go for why that's safe and why it's decoupled from `inner`.
	inner := securityHeaders(s.sessionAuth(s.teamSelector(bufferBody(mux))))
	s.srv = &http.Server{
		Addr: addr,
		// teamSelector runs after sessionAuth so it can rebind the resolved bearer
		// to the team named by the request's selector before any handler runs.
		// bufferBody wraps innermost, right around mux, so it buffers exactly
		// the body the handler itself sees (post team/auth rebinding doesn't
		// touch the body) — see forward.go.
		Handler: s.instrumentMetrics(mux, inner),
	}
	return s
}

// securityHeaders applies baseline hardening headers to every response.
// rekam is always reached over HTTPS (Cloudflare terminates TLS in front), so
// HSTS pins clients to it; no-referrer keeps the authorization code in the
// /authorize redirect from leaking via the Referer header to downstream logs.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// Handler returns the underlying http.Handler for embedding in a larger mux.
func (s *Server) Handler() http.Handler { return s.srv.Handler }

// ListenAndServe starts the HTTP server (blocking).
func (s *Server) ListenAndServe() error {
	return s.srv.ListenAndServe()
}

func noCacheIndex(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/ui")
		if p == "" || p == "/" || strings.HasSuffix(p, "index.html") {
			w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
			w.Header().Set("Pragma", "no-cache")
		}
		next.ServeHTTP(w, r)
	})
}

func gzipMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") || r.Header.Get("Range") != "" {
			next.ServeHTTP(w, r)
			return
		}
		gz, err := gzip.NewWriterLevel(w, gzip.BestSpeed)
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}
		defer gz.Close()
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Vary", "Accept-Encoding")
		next.ServeHTTP(&gzipResponseWriter{ResponseWriter: w, Writer: gz}, r)
	})
}

type gzipResponseWriter struct {
	http.ResponseWriter
	Writer *gzip.Writer
}

func (g *gzipResponseWriter) WriteHeader(code int) {
	g.Header().Del("Content-Length")
	g.ResponseWriter.WriteHeader(code)
}

func (g *gzipResponseWriter) Write(b []byte) (int, error) {
	return g.Writer.Write(b)
}

// isSPADocumentPath reports whether a GET should be answered with the SPA
// shell. Mirrors frontend/src/lib/docs-route.js's isDocsPath: /docs and
// /docs/<anything>, but not /docsomething.
func isSPADocumentPath(p string) bool {
	p = strings.TrimRight(p, "/")
	if p == "" {
		return true
	}
	// /docs only exists where the corpus does — see registerManagedRoutes.
	return Edition == "managed" && (p == "/docs" || strings.HasPrefix(p, "/docs/"))
}
