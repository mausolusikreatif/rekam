package api

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// Scenarios: spec/docs.md (DOCS-01, DOCS-05, DOCS-06)

// The public docs corpus is the only surface rekam exposes with no account
// behind it. Enabling and seeding it is engine-level work that compiles into
// every edition, and that is what is tested here. Actually serving /docs is
// managed-only — see public_serving_managed_test.go for what an anonymous
// caller must not be able to reach.

// docsTestServer builds a server with the public docs corpus enabled, seeded
// from the embedded markdown. alice keeps her own separate home corpus, which
// is what the isolation tests read against.
func docsTestServer(t *testing.T) (srv *Server, aliceKey string, cleanup func()) {
	t.Helper()
	srv, aliceKey, cleanup = serverWithUsersDir(t)
	if err := srv.EnableDocs(filepath.Join(srv.usersDir, "docs.rekam")); err != nil {
		cleanup()
		t.Fatalf("EnableDocs: %v", err)
	}
	return srv, aliceKey, cleanup
}

// anonRequest issues a request with no credentials of any kind — the state a
// stranger opening /docs is in.
func anonRequest(t *testing.T, srv *Server, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	return rr
}

func memoryTitles(t *testing.T, rr *httptest.ResponseRecorder) []string {
	t.Helper()
	var list struct {
		Memories []struct {
			Title string `json:"title"`
		} `json:"memories"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	out := make([]string, 0, len(list.Memories))
	for _, m := range list.Memories {
		out = append(out, m.Title)
	}
	return out
}

func slicesContain(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// Seeding runs on every boot, so it has to be idempotent — otherwise each
// restart would rewrite every record and bloat revision history.
// DOCS-06
func TestPublicDocsSyncIsIdempotent(t *testing.T) {
	srv, _, cleanup := docsTestServer(t)
	defer cleanup()

	ins, upd, rem := srv.DocsSyncCounts()
	if ins == 0 {
		t.Fatalf("first sync inserted nothing (%d/%d/%d)", ins, upd, rem)
	}

	if err := srv.EnableDocs(filepath.Join(srv.usersDir, "docs.rekam")); err != nil {
		t.Fatalf("second EnableDocs: %v", err)
	}
	ins, upd, rem = srv.DocsSyncCounts()
	if ins != 0 || upd != 0 || rem != 0 {
		t.Fatalf("re-syncing unchanged docs wrote: %d added, %d updated, %d removed", ins, upd, rem)
	}
}

// The docs account is created read-only and nothing may quietly grant it write.
// DOCS-05
func TestPublicDocsIdentityIsReadOnly(t *testing.T) {
	srv, _, cleanup := docsTestServer(t)
	defer cleanup()

	ids, err := srv.eng.AllIdentities()
	if err != nil {
		t.Fatalf("list identities: %v", err)
	}
	var found bool
	for _, id := range ids {
		if id.Name != docsIdentityName {
			continue
		}
		found = true
		if id.AllowWrite {
			t.Error("docs identity has write permission")
		}
	}
	if !found {
		t.Fatalf("no %q identity was created", docsIdentityName)
	}
}

// DOCS-01
func TestEnableDocsRequiresACorpusPath(t *testing.T) {
	srv, _, cleanup := serverWithUsersDir(t)
	defer cleanup()

	if err := srv.EnableDocs(""); err == nil {
		t.Fatal("EnableDocs accepted an empty corpus path")
	}
	if srv.DocsEnabled() {
		t.Fatal("docs must stay disabled after a rejected configuration")
	}
}
