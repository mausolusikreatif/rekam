package api

import (
	"net/http"
	"strings"
	"testing"
)

// Scenarios: spec/editions.md (EDTN-01..06), spec/identity.md (IDNT-15)

// The one test that runs in every build. Most of this package's suite is
// split by build tag — a solo binary is compiled without the team and
// managed handlers, so their tests cannot run against it — and a tagged-out
// test is indistinguishable from a passing one in the summary line. This is
// what keeps "0 failures" honest: whatever was tagged away, the routes it
// covered are asserted here to be absent, and the core surface to be present.
//
// Absence is checked as 404/405 rather than "not 200", because a route that
// exists but rejects an anonymous caller answers 401 — which is a *present*
// route and would otherwise read as proof of gating that isn't there.

// probe reports whether the mux routes a path at all, ignoring what the
// handler then decides. An unrouted path is 404 (the edition's not-found
// message) or 405 when the SPA catch-all claims the path for GET only.
func routed(t *testing.T, srv *Server, method, path string) bool {
	t.Helper()
	rr := doRequest(t, srv, method, path, "", nil)
	return rr.Code != http.StatusNotFound && rr.Code != http.StatusMethodNotAllowed
}

func TestEditionServesOnlyItsOwnRoutes(t *testing.T) {
	srv, _, cleanup := testServer(t)
	defer cleanup()

	type route struct{ method, path string }

	// Reading and writing memory, search, OAuth, and signing in to the web
	// UI. Every edition has these; a self-hosted binary that lost one would
	// be broken, not merely smaller.
	core := []route{
		{"GET", "/me"},
		{"GET", "/catalog"},
		{"GET", "/search"},
		{"GET", "/memories"},
		{"GET", "/export"},
		{"GET", "/review"},
		{"POST", "/memory"},
		{"POST", "/register"},
		{"GET", "/authorize"},
		{"POST", "/token"},
		{"POST", "/ui/login"},
		{"GET", "/ui/session"},
		{"POST", "/files"},
	}
	// Shared corpora and the operator surface that populates them.
	team := []route{
		{"GET", "/teams"},
		{"POST", "/teams"},
		{"GET", "/team/members"},
		{"POST", "/team/invites"},
		{"GET", "/admin/users"},
		{"POST", "/admin/users"},
	}
	// The hosted control plane: selling accounts, and standing above the
	// tenants. This is the surface a self-hosted build must not ship.
	managed := []route{
		{"POST", "/ui/signup"},
		{"GET", "/ui/confirm"},
		{"GET", "/admin/stats"},
		{"GET", "/admin/dashboard"},
		{"GET", "/admin/grants"},
		{"GET", "/admin/teams"},
		{"GET", "/admin/entitlements/someone"},
		{"GET", "/docs"},
	}

	want := map[string]struct{ team, managed bool }{
		"solo":    {team: false, managed: false},
		"team":    {team: true, managed: false},
		"managed": {team: true, managed: true},
	}[Edition]

	// EDTN-01
	for _, r := range core {
		if !routed(t, srv, r.method, r.path) {
			t.Errorf("%s edition: %s %s is core and must be served", Edition, r.method, r.path)
		}
	}
	// EDTN-02, EDTN-03
	for _, r := range team {
		if got := routed(t, srv, r.method, r.path); got != want.team {
			t.Errorf("%s edition: %s %s routed=%v, want %v", Edition, r.method, r.path, got, want.team)
		}
	}
	for _, r := range managed {
		if got := routed(t, srv, r.method, r.path); got != want.managed {
			t.Errorf("%s edition: %s %s routed=%v, want %v", Edition, r.method, r.path, got, want.managed)
		}
	}
}

// EDTN-04. An unexpected 404 should tell a client which build refused it,
// rather than leaving "this edition doesn't have it" indistinguishable from
// a mistyped path.
func TestUnroutedPathNamesTheEdition(t *testing.T) {
	srv, _, cleanup := testServer(t)
	defer cleanup()

	rr := doRequest(t, srv, "GET", "/definitely-not-a-route", "", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unrouted path: want 404, got %d: %s", rr.Code, rr.Body)
	}
	var body struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	decodeJSON(t, rr, &body)
	if body.Error != "not_found" {
		t.Errorf("error = %q, want not_found", body.Error)
	}
	if !strings.Contains(body.Message, Edition) {
		t.Errorf("the 404 should name the edition (%s), got %q", Edition, body.Message)
	}

	// The SPA's own document paths are not API paths: a person visiting the
	// site gets the app, not this JSON.
	if rr := doRequest(t, srv, "GET", "/", "", nil); rr.Code != http.StatusOK {
		t.Errorf("GET / should still serve the app shell, got %d", rr.Code)
	}
}

// EDTN-05, IDNT-15. A client pointed at a server it did not choose — the
// native desktop app, above all — should be able to ask what this build is
// instead of probing for 404s.
func TestMeReportsTheEdition(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	rr := doRequest(t, srv, "GET", "/me", key, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("me: want 200, got %d: %s", rr.Code, rr.Body)
	}
	var me struct {
		ID      string `json:"id"`
		Edition string `json:"edition"`
	}
	decodeJSON(t, rr, &me)
	if me.ID == "" {
		t.Error("/me should still carry the caller's identity")
	}
	if me.Edition != Edition {
		t.Errorf("/me edition = %q, want %q", me.Edition, Edition)
	}
	if me.Edition != "solo" && me.Edition != "team" && me.Edition != "managed" {
		t.Errorf("edition should be one of solo/team/managed, got %q", me.Edition)
	}
}

// EDTN-06. The boundary is a compile-time fact: no credential, not even the
// legacy operator key, reaches a handler this build does not contain.
func TestNoCredentialReachesAnotherEditionsRoutes(t *testing.T) {
	if Edition == "managed" {
		t.Skip("managed serves every route; there is no absent surface to try a credential against")
	}
	srv, _, adminKey, _, cleanup := oauthServer(t)
	defer cleanup()

	absent := []string{"/admin/stats", "/admin/grants", "/admin/teams"}
	if Edition == "solo" {
		absent = append(absent, "/teams", "/admin/users")
	}
	for _, path := range absent {
		rr := doRequest(t, srv, "GET", path, adminKey, nil)
		if rr.Code != http.StatusNotFound {
			t.Errorf("%s edition: GET %s with the admin key = %d, want 404 — "+
				"the handler should not be in this binary at all", Edition, path, rr.Code)
		}
	}
}
