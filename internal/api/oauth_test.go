package api

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Ucok23/rekam/internal/db"
	"github.com/Ucok23/rekam/internal/engine"
)

// Scenarios: spec/oauth-authorize.md (OAUA-04..10, OAUA-12, OAUA-14)
// Scenarios: spec/oauth-token.md (OAUT-01, OAUT-05, OAUT-06, OAUT-10)

// registerClient does what a real client does before authorizing: declares
// where its authorization code may be delivered. Since OAUA-14 an authorize
// request naming an unregistered client (or destination) is refused, so a test
// that skips this step is testing the rejection, not the flow.
func registerClient(t *testing.T, srv *Server, clientID string, redirectURIs ...string) {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"client_id":     clientID,
		"redirect_uris": redirectURIs,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rr := postJSON(t, srv, "POST", "/register", string(body)); rr.Code != http.StatusCreated {
		t.Fatalf("register %s: want 201, got %d: %s", clientID, rr.Code, rr.Body)
	}
}

// oauthServer builds a server with a legacy admin key and a temp users directory
// for signups. masterKey is a raw Bearer identity used to exercise the
// unchanged API-key path; OAuth itself is per-user (email + password).
func oauthServer(t *testing.T) (srv *Server, masterKey, adminKey, usersDir string, cleanup func()) {
	t.Helper()

	regF, err := os.CreateTemp("", "oauth-reg-*.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	regF.Close()
	memF, err := os.CreateTemp("", "oauth-mem-*.memory")
	if err != nil {
		os.Remove(regF.Name())
		t.Fatal(err)
	}
	memF.Close()
	usersDir, err = os.MkdirTemp("", "oauth-users-*")
	if err != nil {
		os.Remove(regF.Name())
		os.Remove(memF.Name())
		t.Fatal(err)
	}

	reg, err := db.OpenRegistry(regF.Name())
	if err != nil {
		t.Fatal(err)
	}
	masterKey = "master-test-key"
	if _, err := reg.Create("owner", masterKey, memF.Name(), true); err != nil {
		t.Fatal(err)
	}

	adminKey = "admin-test-key"
	eng := engine.New(reg, 6000)
	srv = NewServer(eng, ":0", adminKey, usersDir)

	cleanup = func() {
		reg.Close()
		os.Remove(regF.Name())
		os.Remove(memF.Name())
		os.RemoveAll(usersDir)
	}
	return
}

// newConfirmedUser provisions a confirmed email+password account with its own
// memory store under usersDir, so it can complete the OAuth flow.
func newConfirmedUser(t *testing.T, srv *Server, usersDir, email, password string) {
	t.Helper()
	memPath := filepath.Join(usersDir, email+".rekam")
	_, token, err := srv.eng.CreateUnconfirmed(email, email, password, memPath, true)
	if err != nil {
		t.Fatalf("create user %s: %v", email, err)
	}
	if _, _, err := srv.eng.ConfirmEmail(token); err != nil {
		t.Fatalf("confirm %s: %v", email, err)
	}
}

func TestOAuthFlowEndToEnd(t *testing.T) {
	srv, masterKey, _, usersDir, cleanup := oauthServer(t)
	defer cleanup()

	const email, password = "alice@example.com", "correct-horse-battery"
	newConfirmedUser(t, srv, usersDir, email, password)

	// PKCE pair.
	verifier := "a-random-verifier-string-1234567890"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	redirectURI := "https://claude.ai/api/mcp/auth_callback"
	registerClient(t, srv, "claude-ai", redirectURI)

	authForm := func(em, pw string) *httptest.ResponseRecorder {
		form := url.Values{
			"redirect_uri":          {redirectURI},
			"state":                 {"xyz"},
			"code_challenge":        {challenge},
			"code_challenge_method": {"S256"},
			"client_id":             {"claude-ai"},
			"email":                 {em},
			"password":              {pw},
		}
		req := httptest.NewRequest("POST", "/authorize", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rr := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rr, req)
		return rr
	}

	// Wrong password → 401, no redirect.
	// OAUA-04
	if rr := authForm(email, "wrong"); rr.Code != http.StatusUnauthorized {
		t.Fatalf("bad password: want 401, got %d", rr.Code)
	}

	// Unconfirmed user → 401 (blocked by the confirmation gate).
	newUnconfirmed := func(em, pw string) {
		memPath := filepath.Join(usersDir, em+".rekam")
		if _, _, err := srv.eng.CreateUnconfirmed(em, em, pw, memPath, true); err != nil {
			t.Fatalf("create unconfirmed: %v", err)
		}
	}
	newUnconfirmed("bob@example.com", "bobs-password-1234")
	// OAUA-05
	if rr := authForm("bob@example.com", "bobs-password-1234"); rr.Code != http.StatusUnauthorized {
		t.Fatalf("unconfirmed user: want 401, got %d", rr.Code)
	}

	// 1. Correct email+password → 302 redirect carrying a code.
	// OAUA-06
	rr := authForm(email, password)
	if rr.Code != http.StatusFound {
		t.Fatalf("authorize: want 302, got %d: %s", rr.Code, rr.Body)
	}
	loc, err := url.Parse(rr.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse redirect: %v", err)
	}
	code := loc.Query().Get("code")
	if code == "" {
		t.Fatal("no code in redirect")
	}
	// OAUA-12
	if loc.Query().Get("state") != "xyz" {
		t.Fatal("state not echoed back")
	}

	// 2. POST /token → 200 with a fresh access token (not any static key).
	// OAUT-01
	tokForm := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"code_verifier": {verifier},
	}
	tokReq := httptest.NewRequest("POST", "/token", strings.NewReader(tokForm.Encode()))
	tokReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokRR := httptest.NewRecorder()
	srv.Handler().ServeHTTP(tokRR, tokReq)
	if tokRR.Code != http.StatusOK {
		t.Fatalf("token: want 200, got %d: %s", tokRR.Code, tokRR.Body)
	}
	if tokRR.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("token response Cache-Control = %q, want no-store", tokRR.Header().Get("Cache-Control"))
	}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	decodeJSON(t, tokRR, &tok)
	if tok.AccessToken == "" || tok.AccessToken == masterKey {
		t.Fatalf("expected a fresh minted token, got %q", tok.AccessToken)
	}

	// 3. The minted token authenticates API calls.
	// OAUT-05
	if rr := doRequest(t, srv, "GET", "/catalog", tok.AccessToken, nil); rr.Code != http.StatusOK {
		t.Fatalf("catalog with minted token: want 200, got %d: %s", rr.Code, rr.Body)
	}

	// 4. The grant is bound to alice, not to the master key it was minted
	// beside. Read that off the registry rather than /admin/grants: the grant
	// is core behaviour and must hold in every edition, while the admin
	// console that displays it exists only in the managed build (that surface
	// is TestAdminGrantListingAndRevocation's job).
	grants, err := srv.eng.ListGrants()
	if err != nil {
		t.Fatal(err)
	}
	if len(grants) != 1 {
		t.Fatalf("want 1 grant, got %d", len(grants))
	}
	if grants[0].IdentityName != email {
		t.Fatalf("grant should be bound to %s, got %q", email, grants[0].IdentityName)
	}

	// 5. Revoke the grant → the minted token stops working.
	// OAUT-10
	if err := srv.eng.RevokeGrant(grants[0].ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if rr := doRequest(t, srv, "GET", "/catalog", tok.AccessToken, nil); rr.Code != http.StatusUnauthorized {
		t.Fatalf("catalog after revoke: want 401, got %d: %s", rr.Code, rr.Body)
	}

	// The master key is unaffected — the raw Bearer path still works.
	if rr := doRequest(t, srv, "GET", "/catalog", masterKey, nil); rr.Code != http.StatusOK {
		t.Fatalf("master key after revoke: want 200, got %d: %s", rr.Code, rr.Body)
	}
}

// This is the whole point of persisting OAuth flow state in the registry
// instead of a process-local sync.Map: a code minted by one Server/Engine
// instance must be redeemable by a completely independent instance opened
// over the same registry file afterward — simulating a redeploy landing on a
// fresh process, or a second node behind a load balancer. A process-local map
// could never pass this test; that's exactly the bug this persisted.
func TestOAuthCodeSurvivesAFreshServerInstance(t *testing.T) {
	regF, err := os.CreateTemp("", "oauth-restart-reg-*.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	regF.Close()
	usersDir, err := os.MkdirTemp("", "oauth-restart-users-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		os.Remove(regF.Name())
		os.RemoveAll(usersDir)
	}()

	// First "instance": open the registry, create a user, mint a code.
	reg1, err := db.OpenRegistry(regF.Name())
	if err != nil {
		t.Fatal(err)
	}
	eng1 := engine.New(reg1, 6000)
	srv1 := NewServer(eng1, ":0", "admin-key", usersDir)

	const email, password = "alice@example.com", "correct-horse-battery"
	newConfirmedUser(t, srv1, usersDir, email, password)
	registerClient(t, srv1, "claude-ai", "https://claude.ai/api/mcp/auth_callback")

	verifier := "a-random-verifier-string-1234567890"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	form := url.Values{
		"redirect_uri":          {"https://claude.ai/api/mcp/auth_callback"},
		"state":                 {"xyz"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"client_id":             {"claude-ai"},
		"email":                 {email},
		"password":              {password},
	}
	req := httptest.NewRequest("POST", "/authorize", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	srv1.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusFound {
		t.Fatalf("authorize on first instance: want 302, got %d: %s", rr.Code, rr.Body)
	}
	loc, err := url.Parse(rr.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	code := loc.Query().Get("code")
	if code == "" {
		t.Fatal("no code in redirect")
	}
	reg1.Close() // the process that minted the code is entirely gone now

	// Second "instance": a brand new Registry/Engine/Server over the same file,
	// sharing no process memory with the first.
	reg2, err := db.OpenRegistry(regF.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer reg2.Close()
	eng2 := engine.New(reg2, 6000)
	srv2 := NewServer(eng2, ":0", "admin-key", usersDir)

	tokForm := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"code_verifier": {verifier},
	}
	tokReq := httptest.NewRequest("POST", "/token", strings.NewReader(tokForm.Encode()))
	tokReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokRR := httptest.NewRecorder()
	srv2.Handler().ServeHTTP(tokRR, tokReq)
	if tokRR.Code != http.StatusOK {
		t.Fatalf("token exchange on second instance: want 200, got %d: %s", tokRR.Code, tokRR.Body)
	}
}

// A browser session end to end: an account that exists can log in, gets an
// httpOnly cookie, and that cookie alone authenticates API calls.
//
// The account is created through the engine rather than POST /ui/signup,
// because self-service signup is the managed control plane while sessions
// are core — logging in has to work in a solo build too, and going through
// the HTTP signup route would have tied this test to an edition that isn't
// the one most self-hosters run. See TestSignupCreatesAnUnconfirmedAccount
// for the signup surface itself.
func TestBrowserSessionFlow(t *testing.T) {
	srv, _, _, usersDir, cleanup := oauthServer(t)
	defer cleanup()

	const email, password = "alice@example.com", "correct-horse"
	memPath := filepath.Join(usersDir, email+".rekam")
	_, confirmToken, err := srv.eng.CreateUnconfirmed(email, email, password, memPath, true)
	if err != nil {
		t.Fatalf("create account: %v", err)
	}

	// Before confirmation, login is refused (403).
	preLogin := httptest.NewRequest("POST", "/ui/login", strings.NewReader(`{"email":"alice@example.com","password":"correct-horse"}`))
	preLogin.Header.Set("Content-Type", "application/json")
	preRR := httptest.NewRecorder()
	srv.Handler().ServeHTTP(preRR, preLogin)
	if preRR.Code != http.StatusForbidden {
		t.Fatalf("login before confirm: want 403, got %d: %s", preRR.Code, preRR.Body)
	}

	if _, _, err := srv.eng.ConfirmEmail(confirmToken); err != nil {
		t.Fatalf("confirm: %v", err)
	}

	// Wrong password → 401.
	bad := httptest.NewRequest("POST", "/ui/login", strings.NewReader(`{"email":"alice@example.com","password":"nope"}`))
	bad.Header.Set("Content-Type", "application/json")
	badRR := httptest.NewRecorder()
	srv.Handler().ServeHTTP(badRR, bad)
	if badRR.Code != http.StatusUnauthorized {
		t.Fatalf("bad login: want 401, got %d", badRR.Code)
	}

	// Correct email+password → 200 + httpOnly session cookie.
	login := httptest.NewRequest("POST", "/ui/login", strings.NewReader(`{"email":"alice@example.com","password":"correct-horse"}`))
	login.Header.Set("Content-Type", "application/json")
	loginRR := httptest.NewRecorder()
	srv.Handler().ServeHTTP(loginRR, login)
	if loginRR.Code != http.StatusOK {
		t.Fatalf("login: want 200, got %d: %s", loginRR.Code, loginRR.Body)
	}
	var cookie *http.Cookie
	for _, c := range loginRR.Result().Cookies() {
		if c.Name == sessionCookie {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("no session cookie set")
	}
	if !cookie.HttpOnly {
		t.Fatal("session cookie must be HttpOnly")
	}

	// The cookie authenticates API calls with no Authorization header.
	catReq := httptest.NewRequest("GET", "/catalog", nil)
	catReq.AddCookie(cookie)
	catRR := httptest.NewRecorder()
	srv.Handler().ServeHTTP(catRR, catReq)
	if catRR.Code != http.StatusOK {
		t.Fatalf("catalog via cookie: want 200, got %d: %s", catRR.Code, catRR.Body)
	}

	// /ui/session reports authenticated with the right email.
	sessReq := httptest.NewRequest("GET", "/ui/session", nil)
	sessReq.AddCookie(cookie)
	sessRR := httptest.NewRecorder()
	srv.Handler().ServeHTTP(sessRR, sessReq)
	if sessRR.Code != http.StatusOK {
		t.Fatalf("session: want 200, got %d", sessRR.Code)
	}
	var st struct {
		Authenticated bool `json:"authenticated"`
		Identity      struct {
			Email string `json:"email"`
		} `json:"identity"`
	}
	decodeJSON(t, sessRR, &st)
	if !st.Authenticated || st.Identity.Email != "alice@example.com" {
		t.Fatalf("expected authenticated session for alice, got %+v", st)
	}
}

// A user with more than one workspace gets a picker instead of being silently
// dropped into personal memory, and the token that comes out is pinned to what
// they chose. This is the whole reason the flow has a second step.
func TestOAuthWorkspaceSelection(t *testing.T) {
	srv, _, _, usersDir, cleanup := oauthServer(t)
	defer cleanup()

	const email, password = "alice@example.com", "correct-horse-battery"
	newConfirmedUser(t, srv, usersDir, email, password)

	identity, err := srv.eng.AuthenticatePassword(email, password)
	if err != nil {
		t.Fatal(err)
	}
	userKey := srv.eng.SessionToken(identity.ID)
	team, err := srv.eng.CreateTeam(userKey, "Acme", filepath.Join(usersDir, "acme.rekam"))
	if err != nil {
		t.Fatal(err)
	}

	verifier := "a-random-verifier-string-1234567890"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	redirectURI := "https://claude.ai/api/mcp/auth_callback"
	registerClient(t, srv, "claude-ai", redirectURI)

	post := func(path string, form url.Values) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rr := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rr, req)
		return rr
	}

	// Step 1: password accepted. Two workspaces exist, so we get the picker
	// rather than a redirect.
	// OAUA-07
	rr := post("/authorize", url.Values{
		"redirect_uri":          {redirectURI},
		"state":                 {"xyz"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"client_id":             {"claude-ai"},
		"email":                 {email},
		"password":              {password},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("workspace step: want 200, got %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Choose a workspace") || !strings.Contains(body, "Acme") {
		t.Fatalf("expected a workspace picker listing Acme, got:\n%s", body)
	}
	ticket := betweenQuotes(t, body, `name="ticket" value="`)

	// A workspace that was never offered is refused.
	// OAUA-09
	if rr := post("/authorize/workspace", url.Values{
		"ticket": {ticket}, "team_id": {"some-other-team"},
	}); rr.Code != http.StatusBadRequest {
		t.Errorf("unknown workspace: want 400, got %d", rr.Code)
	}

	// That failed attempt consumed the single-use ticket; start over and choose
	// the team properly.
	rr = post("/authorize", url.Values{
		"redirect_uri": {redirectURI}, "state": {"xyz"},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"},
		"client_id": {"claude-ai"}, "email": {email}, "password": {password},
	})
	ticket = betweenQuotes(t, rr.Body.String(), `name="ticket" value="`)

	// OAUA-08
	rr = post("/authorize/workspace", url.Values{"ticket": {ticket}, "team_id": {team.ID}})
	if rr.Code != http.StatusFound {
		t.Fatalf("workspace choice: want 302, got %d (%s)", rr.Code, rr.Body.String())
	}
	loc, err := url.Parse(rr.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	code := loc.Query().Get("code")
	if code == "" {
		t.Fatal("no authorization code in redirect")
	}

	// Replaying the ticket must not mint a second code.
	// OAUA-10
	if rr := post("/authorize/workspace", url.Values{"ticket": {ticket}, "team_id": {team.ID}}); rr.Code != http.StatusBadRequest {
		t.Errorf("ticket replay: want 400, got %d", rr.Code)
	}

	// Exchange for a token, and confirm it acts inside the chosen workspace.
	rr = post("/token", url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {verifier},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("token exchange: want 200, got %d (%s)", rr.Code, rr.Body.String())
	}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &tok); err != nil {
		t.Fatal(err)
	}
	// OAUT-06
	me, err := srv.eng.MyMembership(tok.AccessToken)
	if err != nil {
		t.Fatalf("token should act inside the chosen team: %v", err)
	}
	if me.IdentityID != identity.ID {
		t.Errorf("token acts as %s, want the real user %s", me.IdentityID, identity.ID)
	}
	teams, err := srv.eng.MyTeams(tok.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range teams {
		if ref.Active && ref.ID != team.ID {
			t.Errorf("active workspace = %q, want the chosen team", ref.Name)
		}
	}
}

// betweenQuotes pulls the value following marker up to the next double quote.
func betweenQuotes(t *testing.T, body, marker string) string {
	t.Helper()
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatalf("marker %q not found in body", marker)
	}
	rest := body[i+len(marker):]
	j := strings.Index(rest, `"`)
	if j < 0 {
		t.Fatal("unterminated value")
	}
	return rest[:j]
}

// Scenarios: spec/oauth-token.md (OAUT-02..04)
// Scenarios: spec/oauth-authorize.md (OAUA-11)

// A code exchange presenting a code_verifier that doesn't hash to the
// original code_challenge must not yield a token — otherwise PKCE would
// protect nothing. Each exchange attempt also consumes the code (it is
// single-use the moment it's looked up, whether or not the verifier turns
// out to match), so a wrong-verifier attempt burns the code just like a
// successful one does.
func TestTokenExchangeRejectsTheWrongVerifier(t *testing.T) {
	srv, _, _, usersDir, cleanup := oauthServer(t)
	defer cleanup()

	const email, password = "alice@example.com", "correct-horse-battery"
	newConfirmedUser(t, srv, usersDir, email, password)
	registerClient(t, srv, "claude-ai", "https://claude.ai/api/mcp/auth_callback")

	verifier := "a-random-verifier-string-1234567890"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	authorize := func() string {
		form := url.Values{
			"redirect_uri":          {"https://claude.ai/api/mcp/auth_callback"},
			"state":                 {"xyz"},
			"code_challenge":        {challenge},
			"code_challenge_method": {"S256"},
			"client_id":             {"claude-ai"},
			"email":                 {email},
			"password":              {password},
		}
		req := httptest.NewRequest("POST", "/authorize", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rr := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusFound {
			t.Fatalf("authorize: want 302, got %d: %s", rr.Code, rr.Body)
		}
		loc, err := url.Parse(rr.Header().Get("Location"))
		if err != nil {
			t.Fatal(err)
		}
		return loc.Query().Get("code")
	}

	postToken := func(code, verifier string) *httptest.ResponseRecorder {
		tokForm := url.Values{
			"grant_type":    {"authorization_code"},
			"code":          {code},
			"code_verifier": {verifier},
		}
		tokReq := httptest.NewRequest("POST", "/token", strings.NewReader(tokForm.Encode()))
		tokReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		tokRR := httptest.NewRecorder()
		srv.Handler().ServeHTTP(tokRR, tokReq)
		return tokRR
	}

	// OAUT-02
	firstCode := authorize()
	tokRR := postToken(firstCode, "not-the-right-verifier")
	if tokRR.Code != http.StatusBadRequest {
		t.Fatalf("wrong verifier: want 400, got %d: %s", tokRR.Code, tokRR.Body)
	}
	var errResp struct {
		Error string `json:"error"`
	}
	decodeJSON(t, tokRR, &errResp)
	if errResp.Error != "invalid_grant" {
		t.Errorf("error = %q, want invalid_grant", errResp.Error)
	}

	// A fresh code with the matching verifier succeeds...
	secondCode := authorize()
	if tokRR := postToken(secondCode, verifier); tokRR.Code != http.StatusOK {
		t.Fatalf("correct verifier: want 200, got %d: %s", tokRR.Code, tokRR.Body)
	}
	// OAUT-03
	// ...but redeeming that same code again — even with the right verifier —
	// fails: the code was consumed by the successful exchange above.
	if tokRR := postToken(secondCode, verifier); tokRR.Code != http.StatusBadRequest {
		t.Errorf("replayed code: want 400, got %d: %s", tokRR.Code, tokRR.Body)
	}
}

// Scenarios: spec/oauth-token.md (OAUT-14)

// A code_challenge is mandatory at every step: GET /authorize rejects one
// missing before rendering the login form, and POST /authorize (direct submit,
// bypassing the GET step entirely) must not be able to mint a code either —
// otherwise a client could skip the GET request and still get PKCE-free codes.
func TestAuthorizeRequiresPKCE(t *testing.T) {
	srv, _, _, usersDir, cleanup := oauthServer(t)
	defer cleanup()

	const email, password = "alice@example.com", "correct-horse-battery"
	newConfirmedUser(t, srv, usersDir, email, password)

	// GET /authorize with no code_challenge at all.
	req := httptest.NewRequest("GET", "/authorize?redirect_uri="+url.QueryEscape("https://claude.ai/api/mcp/auth_callback")+"&state=xyz", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("GET /authorize with no code_challenge: want 400, got %d: %s", rr.Code, rr.Body)
	}

	// GET /authorize with a challenge but the wrong method.
	req = httptest.NewRequest("GET", "/authorize?redirect_uri="+url.QueryEscape("https://claude.ai/api/mcp/auth_callback")+"&code_challenge=abc&code_challenge_method=plain", nil)
	rr = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("GET /authorize with method=plain: want 400, got %d: %s", rr.Code, rr.Body)
	}

	// POST /authorize directly (as a malicious client skipping the GET form)
	// with valid credentials but no code_challenge must not redirect with a code.
	postNoChallenge := func() *httptest.ResponseRecorder {
		form := url.Values{
			"redirect_uri": {"https://claude.ai/api/mcp/auth_callback"},
			"state":        {"xyz"},
			"client_id":    {"claude-ai"},
			"email":        {email},
			"password":     {password},
		}
		req := httptest.NewRequest("POST", "/authorize", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rr := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rr, req)
		return rr
	}
	rr = postNoChallenge()
	if rr.Code == http.StatusFound {
		t.Fatalf("POST /authorize with no code_challenge must not mint a code, got 302 to %s", rr.Header().Get("Location"))
	}
	if rr.Code != http.StatusBadRequest {
		t.Errorf("POST /authorize with no code_challenge: want 400, got %d: %s", rr.Code, rr.Body)
	}

	// Even if a code were somehow minted without a challenge, /token must
	// refuse to redeem it — belt-and-suspenders on the redemption side.
	if err := srv.eng.StoreOAuthCode("no-pkce-code", db.OAuthCodeEntry{
		CodeChallenge: "",
		ClientID:      "claude-ai",
		IdentityID:    "whoever",
		Expiry:        time.Now().Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	tokForm := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {"no-pkce-code"},
		"code_verifier": {"anything"},
	}
	tokReq := httptest.NewRequest("POST", "/token", strings.NewReader(tokForm.Encode()))
	tokReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokRR := httptest.NewRecorder()
	srv.Handler().ServeHTTP(tokRR, tokReq)
	if tokRR.Code != http.StatusBadRequest {
		t.Errorf("token exchange for a challenge-less code: want 400, got %d: %s", tokRR.Code, tokRR.Body)
	}
}

// An unrecognized or expired code must not yield a token.
func TestTokenExchangeRejectsUnknownOrExpiredCode(t *testing.T) {
	srv, _, _, _, cleanup := oauthServer(t)
	defer cleanup()

	postToken := func(code string) *httptest.ResponseRecorder {
		tokForm := url.Values{
			"grant_type":    {"authorization_code"},
			"code":          {code},
			"code_verifier": {"whatever"},
		}
		tokReq := httptest.NewRequest("POST", "/token", strings.NewReader(tokForm.Encode()))
		tokReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		tokRR := httptest.NewRecorder()
		srv.Handler().ServeHTTP(tokRR, tokReq)
		return tokRR
	}

	// OAUT-04
	if rr := postToken("never-issued"); rr.Code != http.StatusBadRequest {
		t.Fatalf("unknown code: want 400, got %d: %s", rr.Code, rr.Body)
	}

	// A code stored past its expiry is rejected the same way, even though it's
	// otherwise a perfectly recognized entry.
	if err := srv.eng.StoreOAuthCode("stale-code", db.OAuthCodeEntry{
		ClientID: "claude-ai",
		Expiry:   time.Now().Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	// OAUT-04
	if rr := postToken("stale-code"); rr.Code != http.StatusBadRequest {
		t.Fatalf("expired code: want 400, got %d: %s", rr.Code, rr.Body)
	}
}

// A workspace-selection ticket older than its lifetime is refused just like an
// unrecognized one — a stale picker page left open in a browser tab cannot be
// used to authorize later.
func TestWorkspaceTicketExpires(t *testing.T) {
	srv, _, _, _, cleanup := oauthServer(t)
	defer cleanup()

	if err := srv.eng.StoreConsentTicket("stale-ticket", db.ConsentTicketEntry{
		IdentityID: "whoever",
		Teams:      []string{"team-1"},
		Expiry:     time.Now().Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	form := url.Values{"ticket": {"stale-ticket"}, "team_id": {"team-1"}}
	req := httptest.NewRequest("POST", "/authorize/workspace", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	// OAUA-11
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expired ticket: want 400, got %d: %s", rr.Code, rr.Body)
	}
}
