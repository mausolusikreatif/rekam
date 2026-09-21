// Scenarios: spec/identity.md (IDNT-15..18), spec/admin.md (ADMN-03)
//
// The admin *console* scenarios that used to live here are split by edition:
// ADMN-01..04 and ADMN-11 come with the team build (admin_users_team_test.go),
// ADMN-12/13 with the managed one (admin_stats_managed_test.go). What is left
// is identity enumeration and revocation, plus the edition-aware ADMN-03,
// which every edition owes for the gated routes it does serve.

package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestListIdentitiesAdminOnly verifies enumeration requires the admin key: a
// plain valid key is rejected, the admin key succeeds.
func TestListIdentitiesAdminOnly(t *testing.T) {
	srv, masterKey, adminKey, _, cleanup := oauthServer(t)
	defer cleanup()

	// IDNT-16
	if rr := doRequest(t, srv, "GET", "/identities", masterKey, nil); rr.Code != http.StatusUnauthorized {
		t.Fatalf("identities with non-admin key: want 401, got %d: %s", rr.Code, rr.Body)
	}
	if rr := doRequest(t, srv, "GET", "/identities", adminKey, nil); rr.Code != http.StatusOK {
		t.Fatalf("identities with admin key: want 200, got %d: %s", rr.Code, rr.Body)
	}
}

// selfID resolves the identity id backing a key via GET /me.
// IDNT-15
func selfID(t *testing.T, srv *Server, key string) string {
	t.Helper()
	me := doRequest(t, srv, "GET", "/me", key, nil)
	if me.Code != http.StatusOK {
		t.Fatalf("me: want 200, got %d: %s", me.Code, me.Body)
	}
	var self struct {
		ID string `json:"id"`
	}
	decodeJSON(t, me, &self)
	return self.ID
}

// TestRevokeIdentityScope covers the revoke surface: a stranger is forbidden,
// the owner may revoke their own key (same-identity scope), and the admin may
// revoke any key.
func TestRevokeIdentityScope(t *testing.T) {
	// Same-identity scope: the owner revokes their own key.
	t.Run("self", func(t *testing.T) {
		srv, masterKey, _, _, cleanup := oauthServer(t)
		defer cleanup()
		id := selfID(t, srv, masterKey)

		// IDNT-17: a stranger key cannot revoke someone else's identity.
		if rr := doRequest(t, srv, "DELETE", "/identities/"+id, "bogus-key", nil); rr.Code != http.StatusUnauthorized {
			t.Fatalf("revoke with bogus key: want 401, got %d: %s", rr.Code, rr.Body)
		}
		// IDNT-17: the owner may revoke their own identity (same-identity scope).
		if rr := doRequest(t, srv, "DELETE", "/identities/"+id, masterKey, nil); rr.Code != http.StatusOK {
			t.Fatalf("self revoke: want 200, got %d: %s", rr.Code, rr.Body)
		}
		// The revoked key no longer resolves.
		if rr := doRequest(t, srv, "GET", "/me", masterKey, nil); rr.Code != http.StatusUnauthorized {
			t.Fatalf("me after self revoke: want 401, got %d: %s", rr.Code, rr.Body)
		}
	})

	// Admin scope: the admin key may revoke any identity.
	t.Run("admin", func(t *testing.T) {
		srv, masterKey, adminKey, _, cleanup := oauthServer(t)
		defer cleanup()
		id := selfID(t, srv, masterKey)

		// IDNT-18
		if rr := doRequest(t, srv, "DELETE", "/identities/"+id, adminKey, nil); rr.Code != http.StatusOK {
			t.Fatalf("admin revoke identity: want 200, got %d: %s", rr.Code, rr.Body)
		}
		if rr := doRequest(t, srv, "GET", "/me", masterKey, nil); rr.Code != http.StatusUnauthorized {
			t.Fatalf("me after admin revoke: want 401, got %d: %s", rr.Code, rr.Body)
		}
	})
}

// ADMN-03. The gated admin surface refuses anonymous, unrecognized and
// ordinary-but-valid credentials with 401 — not 403, and not a partial
// answer. This runs in every edition against whichever of those routes the
// edition actually serves: "rejects non-admins" is a promise the solo and
// team builds have to keep too, for the surface they do have, and pinning
// the test to the managed route list would have quietly excused them.
func TestAdminRoutesRejectNonAdmins(t *testing.T) {
	srv, masterKey, _, _, cleanup := oauthServer(t)
	defer cleanup()

	// Core in every edition.
	routes := []struct{ method, path, body string }{
		{"GET", "/identities", ""},
	}
	if Edition == "team" || Edition == "managed" {
		routes = append(routes,
			struct{ method, path, body string }{"GET", "/admin/users", ""},
			struct{ method, path, body string }{"POST", "/admin/users", `{"email":"x@example.com","password":"password12"}`},
			struct{ method, path, body string }{"PATCH", "/admin/users/some-id", `{"is_admin":true}`},
		)
	}
	if Edition == "managed" {
		routes = append(routes,
			struct{ method, path, body string }{"GET", "/admin/stats", ""},
			struct{ method, path, body string }{"GET", "/admin/grants", ""},
		)
	}

	for _, rt := range routes {
		for _, cred := range []struct{ name, key string }{
			{"anonymous", ""},
			{"ordinary key", masterKey},
			{"bogus key", "not-a-key"},
		} {
			var body any
			if rt.body != "" {
				body = json.RawMessage(rt.body)
			}
			rr := doRequest(t, srv, rt.method, rt.path, cred.key, body)
			if rr.Code != http.StatusUnauthorized {
				t.Errorf("%s edition: %s %s as %s: want 401, got %d: %s",
					Edition, rt.method, rt.path, cred.name, rr.Code, rr.Body)
			}
		}
	}
}
