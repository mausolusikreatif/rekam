package api

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// Scenarios: spec/oauth-authorize.md (OAUA-14, OAUA-15, OAUA-16)

// The attack this closes: /authorize used to send the code wherever the
// request asked, so a crafted link plus a user who logs in handed an attacker
// a token for that user's memory. PKCE is no defence — whoever starts the flow
// picks the challenge and keeps the verifier.
func TestAuthorizeRefusesAnUnregisteredRedirect(t *testing.T) {
	srv, _, _, usersDir, cleanup := oauthServer(t)
	defer cleanup()

	const email, password = "alice@example.com", "correct-horse-battery"
	newConfirmedUser(t, srv, usersDir, email, password)
	registerClient(t, srv, "claude-ai", "https://claude.ai/api/mcp/auth_callback")

	sum := sha256.Sum256([]byte("a-random-verifier-string-1234567890"))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	params := func(clientID, redirectURI string) url.Values {
		return url.Values{
			"redirect_uri":          {redirectURI},
			"state":                 {"xyz"},
			"code_challenge":        {challenge},
			"code_challenge_method": {"S256"},
			"client_id":             {clientID},
		}
	}

	cases := []struct {
		name               string
		clientID, redirect string
	}{
		{"a destination the client never registered", "claude-ai", "https://evil.example/cb"},
		{"an invented client_id", "not-registered", "https://evil.example/cb"},
		{"no client_id at all", "", "https://evil.example/cb"},
		{"a path bolted onto a registered URI", "claude-ai",
			"https://claude.ai/api/mcp/auth_callback/../../evil"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// OAUA-14: rejected before a password field is ever rendered.
			rr := postJSON(t, srv, "GET", "/authorize?"+params(c.clientID, c.redirect).Encode(), "")
			if rr.Code != http.StatusBadRequest {
				t.Errorf("GET /authorize: want 400, got %d: %s", rr.Code, rr.Body)
			}
			if strings.Contains(rr.Body.String(), `name="password"`) {
				t.Error("an unregistered redirect must not get a password prompt")
			}

			// And again at the mint point, for a client that skips the form
			// and POSTs valid credentials directly.
			form := params(c.clientID, c.redirect)
			form.Set("email", email)
			form.Set("password", password)
			req := httptest.NewRequest("POST", "/authorize", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rr = httptest.NewRecorder()
			srv.Handler().ServeHTTP(rr, req)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("POST /authorize: want 400, got %d: %s", rr.Code, rr.Body)
			}
			// The refusal is rendered here. Redirecting to the rejected
			// destination — even just to report the error — would hand the
			// attacker the referral it wanted (RFC 6749 §4.1.2.1).
			if loc := rr.Header().Get("Location"); loc != "" {
				t.Errorf("a rejected redirect_uri must not be navigated to, got Location: %s", loc)
			}
		})
	}
}

// RFC 8252 §7.3. The native desktop client registers http://127.0.0.1:0 and
// then listens on whatever ephemeral port it is given, so an exact-match rule
// would lock it out entirely.
func TestAuthorizeAllowsAnyLoopbackPort(t *testing.T) {
	srv, _, _, usersDir, cleanup := oauthServer(t)
	defer cleanup()

	const email, password = "alice@example.com", "correct-horse-battery"
	newConfirmedUser(t, srv, usersDir, email, password)
	registerClient(t, srv, "rekam-native", "http://127.0.0.1:0/callback")

	sum := sha256.Sum256([]byte("a-random-verifier-string-1234567890"))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	form := url.Values{
		"redirect_uri":          {"http://127.0.0.1:41234/callback"},
		"state":                 {"xyz"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"client_id":             {"rekam-native"},
		"email":                 {email},
		"password":              {password},
	}

	// OAUA-15
	req := httptest.NewRequest("POST", "/authorize", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusFound {
		t.Fatalf("loopback authorize: want 302, got %d: %s", rr.Code, rr.Body)
	}
	loc, err := url.Parse(rr.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if loc.Port() != "41234" {
		t.Errorf("the code should come back on the port the app is listening on, got %q", loc.Host)
	}
	if loc.Query().Get("code") == "" {
		t.Error("no authorization code in the redirect")
	}

	// The exemption is for the port and nothing else: a different path on
	// loopback is a different application.
	form.Set("redirect_uri", "http://127.0.0.1:41234/steal")
	req = httptest.NewRequest("POST", "/authorize", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("loopback with an unregistered path: want 400, got %d", rr.Code)
	}
}

// A user typing their password deserves to know who is about to get access.
func TestConsentPageNamesTheDestination(t *testing.T) {
	srv, _, _, _, cleanup := oauthServer(t)
	defer cleanup()

	sum := sha256.Sum256([]byte("a-random-verifier-string-1234567890"))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	page := func(clientID, redirectURI string) string {
		params := url.Values{
			"redirect_uri":          {redirectURI},
			"code_challenge":        {challenge},
			"code_challenge_method": {"S256"},
			"client_id":             {clientID},
		}
		rr := postJSON(t, srv, "GET", "/authorize?"+params.Encode(), "")
		if rr.Code != http.StatusOK {
			t.Fatalf("consent page: %d: %s", rr.Code, rr.Body)
		}
		return rr.Body.String()
	}

	// OAUA-16
	registerClient(t, srv, "claude-ai", "https://claude.ai/api/mcp/auth_callback")
	if body := page("claude-ai", "https://claude.ai/api/mcp/auth_callback"); !strings.Contains(body, "claude.ai") {
		t.Errorf("the consent page should name the destination host:\n%s", body)
	}

	registerClient(t, srv, "rekam-native", "http://127.0.0.1:0/callback")
	body := page("rekam-native", "http://127.0.0.1:41234/callback")
	if !strings.Contains(body, "an application on this device") {
		t.Errorf("a loopback destination should be described in plain words:\n%s", body)
	}
	if strings.Contains(body, ">127.0.0.1<") {
		t.Error("a raw loopback address tells the reader nothing")
	}
}
