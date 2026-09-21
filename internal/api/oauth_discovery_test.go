package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// Scenarios: spec/oauth-discovery.md (OAUD-01..04)
// Scenarios: spec/oauth-registration.md (OAUR-01..07)
// Scenarios: spec/oauth-authorize.md (OAUA-01..03, OAUA-13, OAUA-14)

// Before a remote client can connect it must discover where to authorize and
// where to exchange codes. The metadata document is that first handshake: if any
// endpoint is missing or points at the wrong host, the connection never starts.
func TestClientDiscoversHowToConnect(t *testing.T) {
	srv, _, _, _, cleanup := oauthServer(t)
	defer cleanup()

	req := httptest.NewRequest("GET", "/.well-known/oauth-authorization-server", nil)
	req.Host = "memory.example.app"
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("discovery: want 200, got %d: %s", rr.Code, rr.Body)
	}
	var meta struct {
		Issuer                string   `json:"issuer"`
		AuthorizationEndpoint string   `json:"authorization_endpoint"`
		TokenEndpoint         string   `json:"token_endpoint"`
		RegistrationEndpoint  string   `json:"registration_endpoint"`
		ResponseTypes         []string `json:"response_types_supported"`
		GrantTypes            []string `json:"grant_types_supported"`
		CodeChallengeMethods  []string `json:"code_challenge_methods_supported"`
	}
	decodeJSON(t, rr, &meta)

	// Endpoints are absolute and rooted at the host the client actually reached,
	// so a tunnelled deployment advertises its public name, not localhost.
	base := "https://memory.example.app"
	// OAUD-01
	for name, got := range map[string]string{
		"issuer":                 meta.Issuer,
		"authorization_endpoint": meta.AuthorizationEndpoint,
		"token_endpoint":         meta.TokenEndpoint,
		"registration_endpoint":  meta.RegistrationEndpoint,
	} {
		if !strings.HasPrefix(got, base) {
			t.Errorf("%s = %q, want it rooted at %s", name, got, base)
		}
	}
	if meta.AuthorizationEndpoint != base+"/authorize" || meta.TokenEndpoint != base+"/token" {
		t.Errorf("unexpected endpoints: %+v", meta)
	}

	// PKCE with S256 must be advertised — a public client has no secret, so it
	// is the only thing binding the code to the client that requested it.
	// OAUD-04
	if len(meta.CodeChallengeMethods) == 0 || meta.CodeChallengeMethods[0] != "S256" {
		t.Errorf("S256 PKCE must be advertised, got %v", meta.CodeChallengeMethods)
	}
	if len(meta.GrantTypes) == 0 || meta.GrantTypes[0] != "authorization_code" {
		t.Errorf("authorization_code grant must be advertised, got %v", meta.GrantTypes)
	}
	if len(meta.ResponseTypes) == 0 || meta.ResponseTypes[0] != "code" {
		t.Errorf("the code response type must be advertised, got %v", meta.ResponseTypes)
	}

	// Discovery is public — it happens before anyone has a credential.
	// OAUD-03
	if rr.Code != http.StatusOK {
		t.Error("discovery must not require authentication")
	}
}

// Discovery follows the proxy's scheme, so a plain-HTTP local run does not hand
// out https URLs that cannot be reached.
func TestDiscoveryFollowsTheForwardedScheme(t *testing.T) {
	srv, _, _, _, cleanup := oauthServer(t)
	defer cleanup()

	req := httptest.NewRequest("GET", "/.well-known/oauth-authorization-server", nil)
	req.Host = "localhost:5000"
	req.Header.Set("X-Forwarded-Proto", "http")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	var meta struct {
		Issuer string `json:"issuer"`
	}
	decodeJSON(t, rr, &meta)
	// OAUD-02
	if meta.Issuer != "http://localhost:5000" {
		t.Errorf("issuer = %q, want the forwarded http scheme", meta.Issuer)
	}
}

// A client with no pre-arranged credentials registers itself and gets an id it
// can use for the rest of the flow.
func TestClientRegistersItself(t *testing.T) {
	srv, _, _, _, cleanup := oauthServer(t)
	defer cleanup()

	rr := postJSON(t, srv, "POST", "/register",
		`{"client_name":"claude.ai","redirect_uris":["https://claude.ai/api/mcp/auth_callback"]}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("register: want 201, got %d: %s", rr.Code, rr.Body)
	}
	var reg struct {
		ClientID     string   `json:"client_id"`
		RedirectURIs []string `json:"redirect_uris"`
		GrantTypes   []string `json:"grant_types"`
		AuthMethod   string   `json:"token_endpoint_auth_method"`
	}
	decodeJSON(t, rr, &reg)

	// OAUR-01
	if reg.ClientID == "" {
		t.Error("registration must return a client_id")
	}
	// The client's own redirect URIs are echoed back, so it can verify what was
	// registered before sending a user through the flow.
	// OAUR-03
	if len(reg.RedirectURIs) != 1 || reg.RedirectURIs[0] != "https://claude.ai/api/mcp/auth_callback" {
		t.Errorf("redirect_uris should be echoed back, got %v", reg.RedirectURIs)
	}
	// It is a public client: no secret is issued, so none can leak.
	// OAUR-02
	if reg.AuthMethod != "none" {
		t.Errorf("token_endpoint_auth_method = %q, want none for a public client", reg.AuthMethod)
	}

	// Two registrations are two distinct clients.
	// OAUR-04
	var second struct {
		ClientID string `json:"client_id"`
	}
	decodeJSON(t, postJSON(t, srv, "POST", "/register",
		`{"client_name":"other","redirect_uris":["https://other.example/cb"]}`), &second)
	if second.ClientID == "" || second.ClientID == reg.ClientID {
		t.Errorf("each registration should get its own client_id, got %q and %q", reg.ClientID, second.ClientID)
	}

	// A client that already has an id keeps it.
	// OAUR-05
	var reused struct {
		ClientID string `json:"client_id"`
	}
	decodeJSON(t, postJSON(t, srv, "POST", "/register",
		`{"client_id":"already-mine","redirect_uris":["https://mine.example/cb"]}`), &reused)
	if reused.ClientID != "already-mine" {
		t.Errorf("a supplied client_id should be honoured, got %q", reused.ClientID)
	}

	// A malformed registration is a 400, not a client with a broken identity.
	// OAUR-06
	if rr := postJSON(t, srv, "POST", "/register", `{not json`); rr.Code != http.StatusBadRequest {
		t.Errorf("malformed registration: want 400, got %d", rr.Code)
	}

	// A client that declares nowhere to send a code could never finish an
	// authorization: refuse it here rather than at /authorize, where the user
	// is already involved.
	// OAUR-07
	for _, body := range []string{`{}`, `{"redirect_uris":[]}`, `{"redirect_uris":"not-a-list"}`} {
		if rr := postJSON(t, srv, "POST", "/register", body); rr.Code != http.StatusBadRequest {
			t.Errorf("registration %s: want 400 for a client with no redirect_uris, got %d", body, rr.Code)
		}
	}
}

// The user lands on a consent page in a popup with no cookie, so it must render
// a login form that carries every OAuth parameter forward — losing one silently
// breaks the redirect back to the client.
func TestConsentPageCarriesTheRequestForward(t *testing.T) {
	srv, _, _, _, cleanup := oauthServer(t)
	defer cleanup()

	registerClient(t, srv, "client-abc", "https://claude.ai/api/mcp/auth_callback")
	params := url.Values{
		"redirect_uri":          {"https://claude.ai/api/mcp/auth_callback"},
		"state":                 {"opaque-state-value"},
		"code_challenge":        {"E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"},
		"code_challenge_method": {"S256"},
		"client_id":             {"client-abc"},
	}
	rr := postJSON(t, srv, "GET", "/authorize?"+params.Encode(), "")
	if rr.Code != http.StatusOK {
		t.Fatalf("consent page: want 200, got %d: %s", rr.Code, rr.Body)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("the consent page should be HTML, got %q", ct)
	}

	page := rr.Body.String()
	// It asks for credentials...
	// OAUA-01
	for _, want := range []string{`name="email"`, `name="password"`, `method="POST"`, `action="/authorize"`} {
		if !strings.Contains(page, want) {
			t.Errorf("the consent form is missing %s:\n%s", want, page)
		}
	}
	// ...and carries every parameter through as a hidden field.
	for key, vals := range params {
		if !strings.Contains(page, `name="`+key+`"`) {
			t.Errorf("the form drops %s, so the callback would break", key)
		}
		if !strings.Contains(page, vals[0]) {
			t.Errorf("the form does not carry the value of %s forward", key)
		}
	}

	// The authorization code must not leak to third parties via the Referer.
	// OAUA-13
	if rr.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Errorf("Referrer-Policy = %q, want no-referrer on the OAuth surface",
			rr.Header().Get("Referrer-Policy"))
	}
}

// Without a redirect_uri there is nowhere to send the user back to, so the flow
// must stop before collecting a password.
func TestConsentPageRefusesARequestWithNowhereToReturn(t *testing.T) {
	srv, _, _, _, cleanup := oauthServer(t)
	defer cleanup()

	rr := postJSON(t, srv, "GET", "/authorize?state=abc", "")
	// OAUA-02
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("authorize with no redirect_uri: want 400, got %d: %s", rr.Code, rr.Body)
	}
	if strings.Contains(rr.Body.String(), `name="password"`) {
		t.Error("a request with no redirect_uri must not present a password form")
	}
}

// The consent page interpolates client-supplied values; they must be escaped so
// a crafted authorize link cannot inject markup or script into the login form
// the user is about to type their password into.
func TestConsentPageEscapesClientSuppliedValues(t *testing.T) {
	srv, _, _, _, cleanup := oauthServer(t)
	defer cleanup()

	// The client registers the hostile URI as its own: OAUA-14 stops a *third
	// party* redirecting the code, not a client from choosing an ugly URI for
	// itself, so escaping still has to hold.
	const nasty = `https://evil.example/"><script>alert(1)</script>`
	registerClient(t, srv, "client-abc", nasty)
	params := url.Values{
		"redirect_uri":          {nasty},
		"state":                 {`"><img src=x onerror=alert(2)>`},
		"code_challenge":        {"abc"},
		"code_challenge_method": {"S256"},
		"client_id":             {"client-abc"},
	}
	rr := postJSON(t, srv, "GET", "/authorize?"+params.Encode(), "")
	if rr.Code != http.StatusOK {
		t.Fatalf("consent page: %d: %s", rr.Code, rr.Body)
	}
	page := rr.Body.String()
	// OAUA-03
	for _, injected := range []string{"<script>alert(1)</script>", "<img src=x onerror=alert(2)>"} {
		if strings.Contains(page, injected) {
			t.Errorf("client-supplied value was rendered unescaped: %s", injected)
		}
	}
}
