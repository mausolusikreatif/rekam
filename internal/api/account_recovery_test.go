package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Ucok23/rekam/internal/db"
	"github.com/Ucok23/rekam/internal/engine"
)

// Scenarios: spec/identity.md (IDNT-06..14, IDNT-20)
//
// An account's life once it exists: logging in, logging out, forgetting a
// password. Self-service signup and email confirmation are the managed
// control plane — see signup_managed_test.go for IDNT-01..05.

// postJSON issues a JSON request against the full handler chain, optionally
// carrying a session cookie and a client IP (the rate limiters bucket per IP, so
// a test that legitimately makes several auth attempts can spread them out).
func postJSON(t *testing.T, srv *Server, method, path, body string, opts ...func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	for _, o := range opts {
		o(r)
	}
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, r)
	return rr
}

func withCookie(c *http.Cookie) func(*http.Request) {
	return func(r *http.Request) { r.AddCookie(c) }
}

// fromIP makes a request look like it arrived from a particular client
// through the tunnel. The loopback RemoteAddr matters: clientIP only believes
// a forwarding header from a trusted peer, so a test that sets the header on a
// request from 192.0.2.1 (httptest's default) would be bucketed by that
// address instead, and every "different client" would share one bucket.
func fromIP(ip string) func(*http.Request) {
	return func(r *http.Request) {
		r.RemoteAddr = "127.0.0.1:41000"
		r.Header.Set("CF-Connecting-IP", ip)
	}
}

// sessionCookieFrom pulls the session cookie out of a login/confirm/reset
// response, failing the test if the response did not start a session.
func sessionCookieFrom(t *testing.T, rr *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range rr.Result().Cookies() {
		if c.Name == sessionCookie && c.Value != "" {
			return c
		}
	}
	t.Fatalf("no session cookie in response: %d %s", rr.Code, rr.Body)
	return nil
}

// A user who has forgotten their password gets back into their own account: the
// reset link logs them straight in, the new password works from then on, and the
// old one stops working. Locking the old password out is the point of a reset —
// if it kept working, a leaked password would survive the recovery.
func TestForgottenPasswordResetLetsUserBackIn(t *testing.T) {
	srv, _, _, usersDir, cleanup := oauthServer(t)
	defer cleanup()
	newConfirmedUser(t, srv, usersDir, "alice@example.com", "old-password-1")

	forgot := postJSON(t, srv, "POST", "/ui/forgot", `{"email":"alice@example.com"}`)
	if forgot.Code != http.StatusOK {
		t.Fatalf("forgot: want 200, got %d: %s", forgot.Code, forgot.Body)
	}
	var fr struct {
		Status    string `json:"status"`
		ResetCode string `json:"reset_code"`
		Email     string `json:"email"`
	}
	decodeJSON(t, forgot, &fr)
	if fr.Status != "ok" || fr.ResetCode == "" {
		t.Fatalf("expected a reset code for a known account, got %+v", fr)
	}

	// IDNT-12: following the reset link with a new password logs the user in
	// immediately.
	reset := postJSON(t, srv, "POST", "/ui/reset?token="+fr.ResetCode, `{"password":"brand-new-password"}`)
	if reset.Code != http.StatusOK {
		t.Fatalf("reset: want 200, got %d: %s", reset.Code, reset.Body)
	}
	cookie := sessionCookieFrom(t, reset)

	// IDNT-09
	sess := postJSON(t, srv, "GET", "/ui/session", "", withCookie(cookie))
	var st struct {
		Authenticated bool `json:"authenticated"`
		Identity      struct {
			Email string `json:"email"`
		} `json:"identity"`
	}
	decodeJSON(t, sess, &st)
	if !st.Authenticated || st.Identity.Email != "alice@example.com" {
		t.Fatalf("reset should leave alice signed in, got %+v", st)
	}

	// The new password is now the real one...
	ok := postJSON(t, srv, "POST", "/ui/login", `{"email":"alice@example.com","password":"brand-new-password"}`)
	if ok.Code != http.StatusOK {
		t.Fatalf("login with new password: want 200, got %d: %s", ok.Code, ok.Body)
	}
	// ...and the old one is dead (still IDNT-12).
	stale := postJSON(t, srv, "POST", "/ui/login", `{"email":"alice@example.com","password":"old-password-1"}`)
	if stale.Code != http.StatusUnauthorized {
		t.Errorf("old password must stop working after reset: got %d", stale.Code)
	}
}

// A reset link is single-use. Someone who later finds the link in a mailbox or a
// browser history cannot replay it to seize the account.
func TestResetLinkCannotBeReplayed(t *testing.T) {
	srv, _, _, usersDir, cleanup := oauthServer(t)
	defer cleanup()
	newConfirmedUser(t, srv, usersDir, "alice@example.com", "old-password-1")

	forgot := postJSON(t, srv, "POST", "/ui/forgot", `{"email":"alice@example.com"}`)
	var fr struct {
		ResetCode string `json:"reset_code"`
	}
	decodeJSON(t, forgot, &fr)

	if rr := postJSON(t, srv, "POST", "/ui/reset?token="+fr.ResetCode, `{"password":"first-new-password"}`); rr.Code != http.StatusOK {
		t.Fatalf("first reset: want 200, got %d: %s", rr.Code, rr.Body)
	}

	// IDNT-13
	replay := postJSON(t, srv, "POST", "/ui/reset?token="+fr.ResetCode, `{"password":"attacker-password"}`)
	if replay.Code < 400 {
		t.Fatalf("replayed reset token must be rejected, got %d: %s", replay.Code, replay.Body)
	}
	// The password the legitimate user set still stands.
	if rr := postJSON(t, srv, "POST", "/ui/login", `{"email":"alice@example.com","password":"first-new-password"}`); rr.Code != http.StatusOK {
		t.Errorf("legitimate password should survive the replay attempt: got %d", rr.Code)
	}
}

// The forgot-password form must not become an account-enumeration oracle: an
// unknown address gets the same 200 shape as a real one, with no token.
func TestForgotPasswordDoesNotRevealWhoHasAnAccount(t *testing.T) {
	srv, _, _, usersDir, cleanup := oauthServer(t)
	defer cleanup()
	newConfirmedUser(t, srv, usersDir, "alice@example.com", "old-password-1")

	// IDNT-11
	unknown := postJSON(t, srv, "POST", "/ui/forgot", `{"email":"nobody@example.com"}`)
	if unknown.Code != http.StatusOK {
		t.Fatalf("unknown email should still answer 200, got %d: %s", unknown.Code, unknown.Body)
	}
	var ur struct {
		Status    string `json:"status"`
		ResetCode string `json:"reset_code"`
		Email     string `json:"email"`
	}
	decodeJSON(t, unknown, &ur)
	if ur.ResetCode != "" {
		t.Error("no reset token may be issued for an unknown email")
	}
	if ur.Email != "" {
		t.Error("response must not echo an email for an account that does not exist")
	}
}

// A password reset with a garbage or missing token must not change anything.
func TestResetRejectsBadTokenAndWeakPassword(t *testing.T) {
	srv, _, _, usersDir, cleanup := oauthServer(t)
	defer cleanup()
	newConfirmedUser(t, srv, usersDir, "alice@example.com", "old-password-1")

	// IDNT-14
	if rr := postJSON(t, srv, "POST", "/ui/reset", `{"password":"brand-new-password"}`); rr.Code != http.StatusBadRequest {
		t.Errorf("missing token: want 400, got %d", rr.Code)
	}
	if rr := postJSON(t, srv, "POST", "/ui/reset?token=not-a-real-token", `{"password":"brand-new-password"}`); rr.Code < 400 {
		t.Errorf("bogus token must be rejected, got %d", rr.Code)
	}

	// A real token still can't be used to set a password below the minimum length.
	forgot := postJSON(t, srv, "POST", "/ui/forgot", `{"email":"alice@example.com"}`)
	var fr struct {
		ResetCode string `json:"reset_code"`
	}
	decodeJSON(t, forgot, &fr)
	weak := postJSON(t, srv, "POST", "/ui/reset?token="+fr.ResetCode, `{"password":"short"}`)
	if weak.Code < 400 {
		t.Errorf("weak password must be rejected, got %d: %s", weak.Code, weak.Body)
	}
	// The account is untouched: the original password still logs in.
	if rr := postJSON(t, srv, "POST", "/ui/login", `{"email":"alice@example.com","password":"old-password-1"}`); rr.Code != http.StatusOK {
		t.Errorf("failed reset must leave the old password intact: got %d", rr.Code)
	}
}

// Signing out ends the session server-side, not just in the browser: the same
// cookie value replayed afterwards must no longer authenticate anything.
func TestLogoutEndsTheSessionServerSide(t *testing.T) {
	srv, _, _, usersDir, cleanup := oauthServer(t)
	defer cleanup()
	newConfirmedUser(t, srv, usersDir, "alice@example.com", "correct-horse")

	login := postJSON(t, srv, "POST", "/ui/login", `{"email":"alice@example.com","password":"correct-horse"}`)
	cookie := sessionCookieFrom(t, login)

	// The cookie works before logout.
	if rr := postJSON(t, srv, "GET", "/catalog", "", withCookie(cookie)); rr.Code != http.StatusOK {
		t.Fatalf("catalog before logout: want 200, got %d", rr.Code)
	}

	// IDNT-10
	out := postJSON(t, srv, "POST", "/ui/logout", "", withCookie(cookie))
	if out.Code != http.StatusOK {
		t.Fatalf("logout: want 200, got %d: %s", out.Code, out.Body)
	}
	// The browser is told to drop the cookie.
	var cleared bool
	for _, c := range out.Result().Cookies() {
		if c.Name == sessionCookie && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Error("logout must expire the session cookie in the browser")
	}

	// And replaying the old cookie is no longer a session.
	sess := postJSON(t, srv, "GET", "/ui/session", "", withCookie(cookie))
	var st struct {
		Authenticated bool `json:"authenticated"`
	}
	decodeJSON(t, sess, &st)
	if st.Authenticated {
		t.Error("session must be invalidated server-side, not only in the browser")
	}
}

// A forged or stale session cookie is simply not a session — it must not fall
// back to any identity.
func TestUnknownSessionCookieIsNotAuthenticated(t *testing.T) {
	srv, _, _, _, cleanup := oauthServer(t)
	defer cleanup()

	// IDNT-09
	forged := &http.Cookie{Name: sessionCookie, Value: "deadbeefdeadbeefdeadbeef"}
	sess := postJSON(t, srv, "GET", "/ui/session", "", withCookie(forged))
	var st struct {
		Authenticated bool `json:"authenticated"`
	}
	decodeJSON(t, sess, &st)
	if st.Authenticated {
		t.Fatal("a forged cookie must not authenticate")
	}

	if rr := postJSON(t, srv, "GET", "/catalog", "", withCookie(forged)); rr.Code != http.StatusUnauthorized {
		t.Errorf("forged cookie on an API route: want 401, got %d: %s", rr.Code, rr.Body)
	}
}

// Signup is also reachable from a plain HTML form post, not just JSON — the
// login page posts form-encoded when JavaScript is unavailable.
func TestLoginAcceptsFormEncodedPost(t *testing.T) {
	srv, _, _, usersDir, cleanup := oauthServer(t)
	defer cleanup()
	newConfirmedUser(t, srv, usersDir, "alice@example.com", "correct-horse")

	// IDNT-06
	form := httptest.NewRequest("POST", "/ui/login", strings.NewReader("email=alice@example.com&password=correct-horse"))
	form.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, form)
	if rr.Code != http.StatusOK {
		t.Fatalf("form login: want 200, got %d: %s", rr.Code, rr.Body)
	}
	sessionCookieFrom(t, rr)
}

// Brute-forcing a password from one client is throttled: after the burst is
// spent the endpoint answers 429 with a Retry-After hint instead of continuing
// to check credentials.
func TestRepeatedFailedLoginsGetThrottled(t *testing.T) {
	srv, _, _, usersDir, cleanup := oauthServer(t)
	defer cleanup()
	newConfirmedUser(t, srv, usersDir, "alice@example.com", "correct-horse")

	// IDNT-08
	var throttled *httptest.ResponseRecorder
	for range 20 {
		rr := postJSON(t, srv, "POST", "/ui/login", `{"email":"alice@example.com","password":"guess"}`, fromIP("203.0.113.9"))
		if rr.Code == http.StatusTooManyRequests {
			throttled = rr
			break
		}
	}
	if throttled == nil {
		t.Fatal("expected repeated failed logins from one IP to be rate limited")
	}
	if throttled.Header().Get("Retry-After") == "" {
		t.Error("a 429 should tell the client when to retry")
	}

	// A different client is unaffected — the limit is per-IP, not global.
	other := postJSON(t, srv, "POST", "/ui/login", `{"email":"alice@example.com","password":"correct-horse"}`, fromIP("198.51.100.4"))
	if other.Code != http.StatusOK {
		t.Errorf("an unrelated client must not be locked out: got %d: %s", other.Code, other.Body)
	}
}

// An account that signed up but never followed its confirmation link cannot
// log in — the confirmation gate applies to the login endpoint itself, not
// just to whatever the client does with the response.
func TestLoginRejectsUnconfirmedAccount(t *testing.T) {
	srv, _, _, usersDir, cleanup := oauthServer(t)
	defer cleanup()

	memPath := usersDir + "/bob.rekam"
	if _, _, err := srv.eng.CreateUnconfirmed("bob", "bob@example.com", "correct-horse", memPath, true); err != nil {
		t.Fatalf("create unconfirmed: %v", err)
	}

	// IDNT-07
	rr := postJSON(t, srv, "POST", "/ui/login", `{"email":"bob@example.com","password":"correct-horse"}`)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("login with unconfirmed account: want 403, got %d: %s", rr.Code, rr.Body)
	}
	var body struct {
		Resend string `json:"resend"`
	}
	decodeJSON(t, rr, &body)
	if body.Resend == "" {
		t.Error("a 403 for an unconfirmed account should point back at /ui/confirm")
	}
	if len(rr.Result().Cookies()) != 0 {
		t.Error("an unconfirmed login must not set a session cookie")
	}
}

// A session cookie is only as durable as wherever the server records it. This
// simulates a restart — the exact scenario that used to silently sign every
// browser out on every deploy — by closing the registry a session was minted
// against and reopening a brand-new Server on the same file, standing in for
// a fresh process (or a second node) rather than the one that issued the
// cookie.
func TestSessionSurvivesServerRestart(t *testing.T) {
	regF, err := os.CreateTemp("", "restart-reg-*.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	regF.Close()
	defer os.Remove(regF.Name())
	usersDir, err := os.MkdirTemp("", "restart-users-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(usersDir)

	reg1, err := db.OpenRegistry(regF.Name())
	if err != nil {
		t.Fatal(err)
	}
	srv1 := NewServer(engine.New(reg1, 6000), ":0", "admin-test-key", usersDir)
	newConfirmedUser(t, srv1, usersDir, "carol@example.com", "correct-horse-battery")

	rr := postJSON(t, srv1, "POST", "/ui/login", `{"email":"carol@example.com","password":"correct-horse-battery"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("login: want 200, got %d: %s", rr.Code, rr.Body)
	}
	cookie := sessionCookieFrom(t, rr)
	reg1.Close() // the process that minted the session is gone

	// IDNT-20: a fresh Server (standing in for a restarted process, or a
	// second node) opened against the same registry still honors the cookie.
	reg2, err := db.OpenRegistry(regF.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer reg2.Close()
	srv2 := NewServer(engine.New(reg2, 6000), ":0", "admin-test-key", usersDir)

	rr = postJSON(t, srv2, "GET", "/ui/session", "", withCookie(cookie))
	var body struct {
		Authenticated bool `json:"authenticated"`
		Identity      struct {
			Email string `json:"email"`
		} `json:"identity"`
	}
	decodeJSON(t, rr, &body)
	if !body.Authenticated {
		t.Fatalf("session should survive a restart: %d %s", rr.Code, rr.Body)
	}
	if body.Identity.Email != "carol@example.com" {
		t.Errorf("session resolved to wrong identity: got %q", body.Identity.Email)
	}
}
