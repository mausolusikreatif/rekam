//go:build !solo

// Scenarios: spec/admin.md (ADMN-05..10)

// Creating and managing the users who fill a shared corpus comes with
// registerTeamRoutes, so a solo build has none of it. That a solo binary
// serves no /admin/users is asserted by editions_test.go instead.

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

// loginAs authenticates a user and returns their browser session cookie. Each
// call comes from its own client IP so a test doing several logins is not
// throttled by the per-IP auth limiter.
func loginAs(t *testing.T, srv *Server, email, password, ip string) *http.Cookie {
	t.Helper()
	rr := postJSON(t, srv, "POST", "/ui/login",
		`{"email":"`+email+`","password":"`+password+`"}`, fromIP(ip))
	if rr.Code != http.StatusOK {
		t.Fatalf("login %s: want 200, got %d: %s", email, rr.Code, rr.Body)
	}
	return sessionCookieFrom(t, rr)
}

// asUser issues an API request carrying a browser session cookie.
func asUser(t *testing.T, srv *Server, method, path string, cookie *http.Cookie, body string) *httptest.ResponseRecorder {
	t.Helper()
	return postJSON(t, srv, method, path, body, withCookie(cookie))
}

// A deployment with no users directory configured (testServer's default) can
// authenticate as admin but has nowhere to write a new account's .rekam file.
func TestAdminUserCreationRequiresUsersDir(t *testing.T) {
	// ADMN-06
	regF, err := os.CreateTemp("", "admin-nodir-reg-*.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(regF.Name())
	regF.Close()
	reg, err := db.OpenRegistry(regF.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()

	adminKey := "admin-nodir-key"
	memF, err := os.CreateTemp("", "admin-nodir-mem-*.memory")
	if err != nil {
		t.Fatal(err)
	}
	memF.Close()
	defer os.Remove(memF.Name())
	if _, err := reg.Create("owner", adminKey, memF.Name(), true); err != nil {
		t.Fatal(err)
	}
	eng := engine.New(reg, 6000)
	srv := NewServer(eng, ":0", adminKey, "") // usersDir deliberately empty

	rr := doRequest(t, srv, "POST", "/admin/users", adminKey, map[string]any{
		"email": "nobody@example.com", "password": "irrelevant1",
	})
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("create user with no users directory: want 503, got %d: %s", rr.Code, rr.Body)
	}
}

// An admin provisions an account and the new user can immediately sign in and
// use their own memory — the whole point of admin-side account creation.
func TestAdminProvisionedUserCanSignInAndWrite(t *testing.T) {
	srv, _, adminKey, _, cleanup := oauthServer(t)
	defer cleanup()

	created := doRequest(t, srv, "POST", "/admin/users", adminKey, map[string]any{
		"email": "alice@example.com", "password": "alice-password", "name": "Alice",
	})
	if created.Code != http.StatusOK {
		t.Fatalf("create user: %d: %s", created.Code, created.Body)
	}
	var resp struct {
		User struct {
			ID         string `json:"id"`
			Email      string `json:"email"`
			AllowWrite bool   `json:"allow_write"`
			IsAdmin    bool   `json:"is_admin"`
		} `json:"user"`
	}
	decodeJSON(t, created, &resp)
	// ADMN-05
	if resp.User.Email != "alice@example.com" || !resp.User.AllowWrite || resp.User.IsAdmin {
		t.Fatalf("unexpected new user: %+v", resp.User)
	}

	// Admin-created accounts skip the confirmation step, so the user can log in.
	cookie := loginAs(t, srv, "alice@example.com", "alice-password", "10.5.0.1")

	if rr := asUser(t, srv, "POST", "/memory", cookie, `{"title":"My Note","taxonomy":"work.ops"}`); rr.Code != http.StatusCreated {
		t.Fatalf("new user write: want 201, got %d: %s", rr.Code, rr.Body)
	}
	// It lands in her own corpus, not somebody else's.
	var search struct {
		Results []map[string]any `json:"results"`
	}
	decodeJSON(t, asUser(t, srv, "GET", "/search?q=Note", cookie, ""), &search)
	if len(search.Results) != 1 {
		t.Errorf("the user should see her own record, got %d", len(search.Results))
	}
	var adminSide struct {
		Results []map[string]any `json:"results"`
	}
	decodeJSON(t, doRequest(t, srv, "GET", "/search?q=Note", adminKey, nil), &adminSide)
	if len(adminSide.Results) != 0 {
		t.Errorf("a new user's records must not appear in another corpus, got %d", len(adminSide.Results))
	}
}

// Revoking write access downgrades an account to read-only: existing records
// stay readable, but nothing new can be written or changed. This is the lever an
// admin pulls to freeze an account without destroying it.
func TestAdminCanMakeAnAccountReadOnly(t *testing.T) {
	srv, _, adminKey, _, cleanup := oauthServer(t)
	defer cleanup()

	created := doRequest(t, srv, "POST", "/admin/users", adminKey, map[string]any{
		"email": "alice@example.com", "password": "alice-password",
	})
	var resp struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	decodeJSON(t, created, &resp)

	cookie := loginAs(t, srv, "alice@example.com", "alice-password", "10.5.1.1")
	write := asUser(t, srv, "POST", "/memory", cookie, `{"title":"Before","taxonomy":"work.ops"}`)
	if write.Code != http.StatusCreated {
		t.Fatalf("initial write: %d: %s", write.Code, write.Body)
	}
	var mem struct {
		ID string `json:"id"`
	}
	decodeJSON(t, write, &mem)

	// The admin revokes write access.
	patch := doRequest(t, srv, "PATCH", "/admin/users/"+resp.User.ID, adminKey, map[string]any{"allow_write": false})
	if patch.Code != http.StatusOK {
		t.Fatalf("patch: %d: %s", patch.Code, patch.Body)
	}
	var patched struct {
		User struct {
			AllowWrite bool `json:"allow_write"`
		} `json:"user"`
	}
	decodeJSON(t, patch, &patched)
	// ADMN-08
	if patched.User.AllowWrite {
		t.Error("the response should reflect the revoked write access")
	}

	// Reads still work — the account is frozen, not blinded.
	if rr := asUser(t, srv, "GET", "/memory/"+mem.ID, cookie, ""); rr.Code != http.StatusOK {
		t.Errorf("a read-only account should still read: %d: %s", rr.Code, rr.Body)
	}
	// Writes of every kind are refused.
	if rr := asUser(t, srv, "POST", "/memory", cookie, `{"title":"After","taxonomy":"work.ops"}`); rr.Code < 400 {
		t.Errorf("a read-only account must not create records, got %d", rr.Code)
	}
	if rr := asUser(t, srv, "PATCH", "/memory/"+mem.ID, cookie, `{"content":"edited"}`); rr.Code < 400 {
		t.Errorf("a read-only account must not edit records, got %d", rr.Code)
	}
	if rr := asUser(t, srv, "DELETE", "/memory/"+mem.ID, cookie, ""); rr.Code < 400 {
		t.Errorf("a read-only account must not delete records, got %d", rr.Code)
	}

	// ADMN-08: restoring access puts things back, with no re-login needed.
	if rr := doRequest(t, srv, "PATCH", "/admin/users/"+resp.User.ID, adminKey, map[string]any{"allow_write": true}); rr.Code != http.StatusOK {
		t.Fatalf("restore write: %d: %s", rr.Code, rr.Body)
	}
	if rr := asUser(t, srv, "POST", "/memory", cookie, `{"title":"After","taxonomy":"work.ops"}`); rr.Code != http.StatusCreated {
		t.Errorf("restoring write access should take effect on the live session, got %d: %s", rr.Code, rr.Body)
	}
}

// Promoting a user grants the admin console; demoting takes it back. An admin
// flag is the difference between seeing your own memory and seeing the roster.
func TestAdminCanPromoteAndDemoteAnotherUser(t *testing.T) {
	srv, _, adminKey, _, cleanup := oauthServer(t)
	defer cleanup()

	created := doRequest(t, srv, "POST", "/admin/users", adminKey, map[string]any{
		"email": "alice@example.com", "password": "alice-password",
	})
	var resp struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	decodeJSON(t, created, &resp)
	cookie := loginAs(t, srv, "alice@example.com", "alice-password", "10.5.2.1")

	// ADMN-09: a plain user cannot see the roster, nor promote themselves.
	if rr := asUser(t, srv, "GET", "/admin/users", cookie, ""); rr.Code != http.StatusUnauthorized {
		t.Fatalf("non-admin on the console: want 401, got %d", rr.Code)
	}
	if rr := asUser(t, srv, "PATCH", "/admin/users/"+resp.User.ID, cookie, `{"is_admin":true}`); rr.Code != http.StatusUnauthorized {
		t.Fatalf("self-promotion must be refused: got %d: %s", rr.Code, rr.Body)
	}

	// ADMN-08: the admin promotes her.
	if rr := doRequest(t, srv, "PATCH", "/admin/users/"+resp.User.ID, adminKey, map[string]any{"is_admin": true}); rr.Code != http.StatusOK {
		t.Fatalf("promote: %d: %s", rr.Code, rr.Body)
	}
	if rr := asUser(t, srv, "GET", "/admin/users", cookie, ""); rr.Code != http.StatusOK {
		t.Errorf("a promoted user should reach the console: %d: %s", rr.Code, rr.Body)
	}

	// And demotes her again.
	if rr := doRequest(t, srv, "PATCH", "/admin/users/"+resp.User.ID, adminKey, map[string]any{"is_admin": false}); rr.Code != http.StatusOK {
		t.Fatalf("demote: %d: %s", rr.Code, rr.Body)
	}
	if rr := asUser(t, srv, "GET", "/admin/users", cookie, ""); rr.Code != http.StatusUnauthorized {
		t.Errorf("a demoted user must lose console access, got %d", rr.Code)
	}
}

// An admin resetting a locked-out user's password replaces the old one outright.
func TestAdminResetsAUsersPassword(t *testing.T) {
	srv, _, adminKey, _, cleanup := oauthServer(t)
	defer cleanup()

	created := doRequest(t, srv, "POST", "/admin/users", adminKey, map[string]any{
		"email": "alice@example.com", "password": "alice-password",
	})
	var resp struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	decodeJSON(t, created, &resp)

	if rr := doRequest(t, srv, "PATCH", "/admin/users/"+resp.User.ID, adminKey, map[string]any{
		"password": "issued-by-admin",
	}); rr.Code != http.StatusOK {
		t.Fatalf("password reset: %d: %s", rr.Code, rr.Body)
	}

	// ADMN-10
	loginAs(t, srv, "alice@example.com", "issued-by-admin", "10.5.3.1")
	if rr := postJSON(t, srv, "POST", "/ui/login",
		`{"email":"alice@example.com","password":"alice-password"}`, fromIP("10.5.3.2")); rr.Code != http.StatusUnauthorized {
		t.Errorf("the old password must stop working after an admin reset, got %d", rr.Code)
	}

	// ADMN-10: a password below the minimum is refused rather than silently accepted.
	if rr := doRequest(t, srv, "PATCH", "/admin/users/"+resp.User.ID, adminKey, map[string]any{
		"password": "tiny",
	}); rr.Code != http.StatusBadRequest {
		t.Errorf("a too-short password should be rejected: got %d: %s", rr.Code, rr.Body)
	}
	// ...and the working password is unchanged.
	loginAs(t, srv, "alice@example.com", "issued-by-admin", "10.5.3.3")
}

// Creating an account with credentials that cannot work is refused up front,
// rather than producing an account nobody can sign into.
func TestAdminUserCreationValidatesCredentials(t *testing.T) {
	srv, _, adminKey, _, cleanup := oauthServer(t)
	defer cleanup()

	doRequest(t, srv, "POST", "/admin/users", adminKey, map[string]any{
		"email": "taken@example.com", "password": "alice-password",
	})

	cases := []struct {
		name string
		body map[string]any
	}{
		{"missing email", map[string]any{"password": "alice-password"}},
		{"malformed email", map[string]any{"email": "nope", "password": "alice-password"}},
		{"short password", map[string]any{"email": "bob@example.com", "password": "tiny"}},
		{"duplicate email", map[string]any{"email": "taken@example.com", "password": "alice-password"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// ADMN-07
			if rr := doRequest(t, srv, "POST", "/admin/users", adminKey, tc.body); rr.Code < 400 {
				t.Fatalf("want a client error, got %d: %s", rr.Code, rr.Body)
			}
		})
	}
}

// ADMN-04: storage_bytes in the admin user listing reflects the identity's
// actual tenant file size — not a placeholder — and grows as they write.
func TestAdminUserListingReportsStorageBytes(t *testing.T) {
	srv, masterKey, adminKey, _, cleanup := oauthServer(t)
	defer cleanup()

	before := storageBytesFor(t, srv, adminKey, "owner")

	// Write enough content that the file's on-disk size visibly changes.
	if rr := doRequest(t, srv, "POST", "/memory", masterKey, map[string]any{
		"title": "Bulk", "taxonomy": "work", "content": strings.Repeat("x", 100_000),
	}); rr.Code != http.StatusCreated {
		t.Fatalf("write: %d: %s", rr.Code, rr.Body)
	}

	after := storageBytesFor(t, srv, adminKey, "owner")
	if after <= before {
		t.Errorf("storage_bytes after a 100KB write = %v, want > %v (before)", after, before)
	}
}

// storageBytesFor finds a named user in GET /admin/users and returns their
// reported storage_bytes.
func storageBytesFor(t *testing.T, srv *Server, adminKey, name string) float64 {
	t.Helper()
	var body struct {
		Users []struct {
			Name         string  `json:"name"`
			StorageBytes float64 `json:"storage_bytes"`
		} `json:"users"`
	}
	decodeJSON(t, doRequest(t, srv, "GET", "/admin/users", adminKey, nil), &body)
	for _, u := range body.Users {
		if u.Name == name {
			return u.StorageBytes
		}
	}
	t.Fatalf("user %q not found in admin listing", name)
	return 0
}
