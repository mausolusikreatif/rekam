package api

import (
	"net/http"
	"strings"
	"testing"
)

// GET / used to 302 to /ui/ before a first-time visitor's landing page ever
// painted — see server.go's comment on spaShell for why that changed. This
// locks the new contract in: root serves the SPA shell directly at 200, no
// redirect, and /ui/ itself keeps working unchanged (still the PWA's
// canonical install URL — nothing here removes that route).
func TestRootServesTheShellDirectly(t *testing.T) {
	srv, _, _, _, cleanup := oauthServer(t)
	defer cleanup()

	rr := doRequest(t, srv, "GET", "/", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /: want 200 (no redirect), got %d", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "" {
		t.Errorf("GET / set a Location header (%q) — it should serve in place, not redirect", loc)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("GET / content-type = %q, want text/html", ct)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `id="app"`) {
		t.Error("GET / body doesn't look like the SPA shell (no #app mount point)")
	}

	// /ui/ itself is untouched — still reachable, still the PWA's canonical URL.
	rr2 := doRequest(t, srv, "GET", "/ui/", "", nil)
	if rr2.Code != http.StatusOK {
		t.Errorf("GET /ui/: want 200, got %d", rr2.Code)
	}
}
