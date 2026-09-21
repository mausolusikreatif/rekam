package api

import (
	"net/http"
	"strings"
	"testing"
)

// The /metrics surface isn't covered by a spec/*.md scenario (it's an ops
// endpoint, not user-facing behavior), but it's still gated and observable
// like the rest of the admin surface, so it gets a direct test here.
func TestMetricsEndpoint(t *testing.T) {
	srv, masterKey, adminKey, _, cleanup := oauthServer(t)
	defer cleanup()

	// No credential: refused, same as any other /admin/* route.
	if rr := doRequest(t, srv, "GET", "/metrics", "", nil); rr.Code != http.StatusUnauthorized {
		t.Fatalf("no credential: want 401, got %d: %s", rr.Code, rr.Body)
	}

	// A request before the scrape gives the route-labeled counter a sample to
	// assert on — /metrics reflects prior requests, not itself mid-flight (the
	// increment for this /catalog call happens in instrumentMetrics after the
	// handler returns, so it's on the books before we hit /metrics next).
	// masterKey (a real registered identity), not adminKey (the admin-gate-only
	// Bearer, which resolves no identity) — /catalog needs an identity to answer.
	if rr := doRequest(t, srv, "GET", "/catalog", masterKey, nil); rr.Code != http.StatusOK {
		t.Fatalf("seed request: want 200, got %d: %s", rr.Code, rr.Body)
	}

	rr := doRequest(t, srv, "GET", "/metrics", adminKey, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("with admin key: want 200, got %d: %s", rr.Code, rr.Body)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "rekam_http_requests_total{") {
		t.Error("missing rekam_http_requests_total series")
	}
	if !strings.Contains(body, `route="/catalog"`) {
		t.Errorf("expected a /catalog sample with the route label (not the raw path with method mixed in), body:\n%s", body)
	}
	if !strings.Contains(body, "rekam_manager_open_handles") {
		t.Error("missing rekam_manager_open_handles gauge")
	}
	// The unmatched-route bucket must stay a single bounded label value, not
	// grow with every distinct bogus path a caller probes. POST (not GET —
	// "GET /" is the SPA fallback, a deliberate catch-all) has no registered
	// catch-all, so an unknown POST path genuinely resolves to no pattern.
	doRequest(t, srv, "POST", "/this-route-does-not-exist", adminKey, nil)
	doRequest(t, srv, "POST", "/nor-does-this-one", adminKey, nil)
	rr2 := doRequest(t, srv, "GET", "/metrics", adminKey, nil)
	body2 := rr2.Body.String()
	if strings.Count(body2, `route="unmatched"`) < 1 {
		t.Errorf("expected an unmatched-route sample, body:\n%s", body2)
	}
	if strings.Contains(body2, "/this-route-does-not-exist") || strings.Contains(body2, "/nor-does-this-one") {
		t.Error("a bogus path leaked into a metric label instead of collapsing to \"unmatched\"")
	}
}
