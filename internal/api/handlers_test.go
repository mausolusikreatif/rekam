package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Ucok23/rekam/internal/db"
	"github.com/Ucok23/rekam/internal/engine"
)

func testServer(t *testing.T) (srv *Server, key string, cleanup func()) {
	t.Helper()

	regF, err := os.CreateTemp("", "api-reg-*.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	regF.Close()

	memF, err := os.CreateTemp("", "api-mem-*.memory")
	if err != nil {
		os.Remove(regF.Name())
		t.Fatal(err)
	}
	memF.Close()

	reg, err := db.OpenRegistry(regF.Name())
	if err != nil {
		t.Fatal(err)
	}

	key = "api-test-key"
	if _, err := reg.Create("alice", key, memF.Name(), true); err != nil {
		t.Fatal(err)
	}

	eng := engine.New(reg, 6000)
	srv = NewServer(eng, ":0", "", "")

	cleanup = func() {
		reg.Close()
		os.Remove(regF.Name())
		os.Remove(memF.Name())
	}
	return
}

func doRequest(t *testing.T, srv *Server, method, path, apiKey string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	return rr
}

func decodeJSON(t *testing.T, rr *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.NewDecoder(rr.Body).Decode(v); err != nil {
		t.Fatalf("decode response: %v (body: %s)", err, rr.Body.String())
	}
}

// Scenarios: spec/memory.md (MEM-01, MEM-02, MEM-03, MEM-05, MEM-06, MEM-07, MEM-12, MEM-14)
// Scenarios: spec/search-taxonomy.md (SRCH-01, SRCH-02, SRCH-06, SRCH-15)

func TestHandlerCatalogEmpty(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	// SRCH-06
	rr := doRequest(t, srv, "GET", "/catalog", key, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body)
	}
}

// SRCH-06
func TestHandlerCatalogAfterWrite(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	doRequest(t, srv, "POST", "/memory", key, map[string]any{
		"title": "Budget", "taxonomy": "finance.accounts",
	})

	rr := doRequest(t, srv, "GET", "/catalog", key, nil)
	var resp map[string][]map[string]any
	decodeJSON(t, rr, &resp)
	if len(resp["taxonomy"]) != 1 || resp["taxonomy"][0]["path"] != "finance.accounts" {
		t.Errorf("unexpected catalog: %v", resp["taxonomy"])
	}
}

// MEM-20: an identity without write permission cannot create a memory, even
// with an otherwise valid request.
func TestHandlerWriteRequiresWritePermission(t *testing.T) {
	regF, err := os.CreateTemp("", "api-reg-*.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	regF.Close()
	defer os.Remove(regF.Name())

	memF, err := os.CreateTemp("", "api-mem-*.memory")
	if err != nil {
		t.Fatal(err)
	}
	memF.Close()
	defer os.Remove(memF.Name())

	reg, err := db.OpenRegistry(regF.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()

	const readOnlyKey = "read-only-key"
	if _, err := reg.Create("bob", readOnlyKey, memF.Name(), false); err != nil {
		t.Fatal(err)
	}

	eng := engine.New(reg, 6000)
	srv := NewServer(eng, ":0", "", "")

	rr := doRequest(t, srv, "POST", "/memory", readOnlyKey, map[string]any{
		"title": "should not land", "taxonomy": "work",
	})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("want 403 for a read-only identity, got %d: %s", rr.Code, rr.Body)
	}
}

// MEM-01
func TestHandlerWriteMemory(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	rr := doRequest(t, srv, "POST", "/memory", key, map[string]any{
		"title":    "Monthly budget",
		"taxonomy": "finance.accounts",
		"content":  "Track monthly expenses.",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", rr.Code, rr.Body)
	}
	var mem map[string]any
	decodeJSON(t, rr, &mem)
	if mem["id"] == "" {
		t.Error("expected id in response")
	}
}

// MEM-02
func TestHandlerWriteMissingFields(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	rr := doRequest(t, srv, "POST", "/memory", key, map[string]any{"title": "no taxonomy"})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rr.Code)
	}
}

// MEM-03
func TestHandlerWriteInvalidTaxonomy(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	rr := doRequest(t, srv, "POST", "/memory", key, map[string]any{
		"title": "bad", "taxonomy": "not valid!",
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rr.Code)
	}
}

// SRCH-15
// Scenarios: spec/identity.md (IDNT-19)
func TestHandlerUnauthorized(t *testing.T) {
	srv, _, cleanup := testServer(t)
	defer cleanup()

	// IDNT-19
	rr := doRequest(t, srv, "GET", "/catalog", "bad-key", nil)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rr.Code)
	}
}

// MEM-05
func TestHandlerGetMemory(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	wr := doRequest(t, srv, "POST", "/memory", key, map[string]any{
		"title": "fetch me", "taxonomy": "work.projects",
	})
	var created map[string]any
	decodeJSON(t, wr, &created)
	id := created["id"].(string)

	rr := doRequest(t, srv, "GET", "/memory/"+id, key, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body)
	}
}

// MEM-06
func TestHandlerGetMemoryNotFound(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	rr := doRequest(t, srv, "GET", "/memory/00000000-0000-0000-0000-000000000000", key, nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", rr.Code)
	}
}

// SRCH-01, SRCH-02
func TestHandlerSearch(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	doRequest(t, srv, "POST", "/memory", key, map[string]any{
		"title": "invoice tracker", "taxonomy": "finance.transactions",
		"content": "invoice payment vendor details",
	})

	rr := doRequest(t, srv, "GET", "/search?q=invoice&taxonomy=finance", key, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body)
	}
	var resp map[string]any
	decodeJSON(t, rr, &resp)
	results := resp["results"].([]any)
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
}

// MEM-07
func TestHandlerUpdate(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	wr := doRequest(t, srv, "POST", "/memory", key, map[string]any{
		"title": "draft", "taxonomy": "work.projects",
	})
	var created map[string]any
	decodeJSON(t, wr, &created)
	id := created["id"].(string)

	rr := doRequest(t, srv, "PATCH", "/memory/"+id, key, map[string]any{
		"title": "final",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body)
	}
	var mem map[string]any
	decodeJSON(t, rr, &mem)
	if mem["title"] != "final" {
		t.Errorf("want 'final', got %v", mem["title"])
	}
}

// MEM-12
func TestHandlerDelete(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	wr := doRequest(t, srv, "POST", "/memory", key, map[string]any{
		"title": "to delete", "taxonomy": "temp",
	})
	var created map[string]any
	decodeJSON(t, wr, &created)
	id := created["id"].(string)

	rr := doRequest(t, srv, "DELETE", "/memory/"+id, key, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body)
	}

	// MEM-12: 410, not 404 — the address stays permanently reserved and reports
	// the tombstone, so a stale link says "removed" rather than "never existed".
	rr = doRequest(t, srv, "GET", "/memory/"+id, key, nil)
	if rr.Code != http.StatusGone {
		t.Fatalf("want 410 after delete, got %d: %s", rr.Code, rr.Body)
	}
	var gone map[string]any
	decodeJSON(t, rr, &gone)
	if gone["code"] != "deleted" {
		t.Errorf("code = %v, want deleted", gone["code"])
	}

	// MEM-06: a never-existing id is still a plain 404 — the two cases stay distinct.
	rr = doRequest(t, srv, "GET", "/memory/no-such-id", key, nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404 for unknown id, got %d", rr.Code)
	}

	// MEM-14: the deletion is auditable: title, deleter, and time survive the purge.
	rr = doRequest(t, srv, "GET", "/deleted", key, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200 from /deleted, got %d: %s", rr.Code, rr.Body)
	}
	var audit struct {
		Deleted []map[string]any `json:"deleted"`
	}
	decodeJSON(t, rr, &audit)
	if len(audit.Deleted) != 1 {
		t.Fatalf("tombstones = %d, want 1", len(audit.Deleted))
	}
	if audit.Deleted[0]["title"] != "to delete" {
		t.Errorf("tombstone title = %v, want 'to delete'", audit.Deleted[0]["title"])
	}
	if audit.Deleted[0]["deleted_by"] == "" || audit.Deleted[0]["deleted_at"] == "" {
		t.Error("tombstone must record who deleted it and when")
	}
}

func TestHandlerGraph(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	// Two linked records plus a dangling target.
	doRequest(t, srv, "POST", "/memory", key, map[string]any{
		"title": "Leaf", "taxonomy": "work", "content": "leaf",
	})
	rr := doRequest(t, srv, "POST", "/memory", key, map[string]any{
		"title": "Hub", "taxonomy": "work", "content": "see [[Leaf]] and [[depends-on::Ghost Doc]]",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create hub: %d (%s)", rr.Code, rr.Body.String())
	}
	var hub struct {
		ID string `json:"id"`
	}
	decodeJSON(t, rr, &hub)

	// Corpus graph: both records + the ghost node + two edges.
	rr = doRequest(t, srv, "GET", "/graph", key, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("corpus graph: %d (%s)", rr.Code, rr.Body.String())
	}
	var g struct {
		Nodes []struct {
			ID       string `json:"id"`
			Title    string `json:"title"`
			Dangling bool   `json:"dangling"`
		} `json:"nodes"`
		Edges []struct {
			Src, Dst, Rel string
		} `json:"edges"`
	}
	decodeJSON(t, rr, &g)
	if len(g.Nodes) != 3 {
		t.Errorf("corpus nodes = %d, want 3 (%+v)", len(g.Nodes), g.Nodes)
	}
	if len(g.Edges) != 2 {
		t.Errorf("corpus edges = %d, want 2", len(g.Edges))
	}
	var ghost bool
	for _, n := range g.Nodes {
		if n.Dangling && n.Title == "ghost doc" {
			ghost = true
		}
	}
	if !ghost {
		t.Error("expected a dangling ghost node for 'ghost doc'")
	}

	// Ego graph centered on the hub still resolves.
	rr = doRequest(t, srv, "GET", "/graph?center="+hub.ID+"&depth=1", key, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("ego graph: %d (%s)", rr.Code, rr.Body.String())
	}

	// Unknown center → 404.
	rr = doRequest(t, srv, "GET", "/graph?center=nope", key, nil)
	if rr.Code != http.StatusNotFound {
		t.Errorf("unknown center status = %d, want 404", rr.Code)
	}
}

func TestHandlerReviewLifecycle(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	rr := doRequest(t, srv, "POST", "/memory", key, map[string]any{
		"title": "Capital of France", "taxonomy": "trivia", "content": "Paris",
	})
	var mem struct {
		ID string `json:"id"`
	}
	decodeJSON(t, rr, &mem)

	// Not in any deck yet → nothing due.
	rr = doRequest(t, srv, "GET", "/review", key, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("review due: %d (%s)", rr.Code, rr.Body.String())
	}
	var due struct {
		Cards []map[string]any         `json:"cards"`
		Stats struct{ Total, Due int } `json:"stats"`
	}
	decodeJSON(t, rr, &due)
	if due.Stats.Total != 0 {
		t.Fatalf("deck should be empty, got %+v", due.Stats)
	}

	// Enroll → due immediately.
	if rr = doRequest(t, srv, "POST", "/review/"+mem.ID, key, nil); rr.Code != http.StatusOK {
		t.Fatalf("add review: %d (%s)", rr.Code, rr.Body.String())
	}
	rr = doRequest(t, srv, "GET", "/review", key, nil)
	decodeJSON(t, rr, &due)
	if due.Stats.Due != 1 || len(due.Cards) != 1 {
		t.Fatalf("expected 1 due card, got %+v", due.Stats)
	}

	// Grade well → scheduled forward, no longer due.
	if rr = doRequest(t, srv, "POST", "/review/"+mem.ID+"/grade", key, map[string]any{"grade": 5}); rr.Code != http.StatusOK {
		t.Fatalf("grade: %d (%s)", rr.Code, rr.Body.String())
	}
	rr = doRequest(t, srv, "GET", "/review", key, nil)
	decodeJSON(t, rr, &due)
	if due.Stats.Due != 0 {
		t.Errorf("card should be scheduled forward, due = %d", due.Stats.Due)
	}

	// Remove from deck.
	if rr = doRequest(t, srv, "DELETE", "/review/"+mem.ID, key, nil); rr.Code != http.StatusOK {
		t.Errorf("remove: %d", rr.Code)
	}
}

func TestHandlerSuggestLinks(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	doRequest(t, srv, "POST", "/memory", key, map[string]any{
		"title": "Postgres Connection Pooling", "taxonomy": "work", "content": "tuning the postgres connection pool",
	})
	doRequest(t, srv, "POST", "/memory", key, map[string]any{
		"title": "Database Pool Tuning", "taxonomy": "work", "content": "how to tune a postgres connection pool",
	})

	rr := doRequest(t, srv, "GET", "/suggest-links", key, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("suggest links: %d (%s)", rr.Code, rr.Body.String())
	}
	var resp struct {
		Suggestions []map[string]any `json:"suggestions"`
	}
	decodeJSON(t, rr, &resp)
	if len(resp.Suggestions) == 0 {
		t.Error("expected at least one suggestion for the two related records")
	}
}

// serverWithUsersDir is testServer with a real users directory, so anything
// that allocates a corpus file of its own — creating a team, seeding the
// public docs mirror — has somewhere to put it.
func serverWithUsersDir(t *testing.T) (srv *Server, key string, cleanup func()) {
	t.Helper()
	dir, err := os.MkdirTemp("", "api-teams-")
	if err != nil {
		t.Fatal(err)
	}
	reg, err := db.OpenRegistry(filepath.Join(dir, "registry.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	key = "alice-key"
	if _, err := reg.Create("alice", key, filepath.Join(dir, "alice.rekam"), true); err != nil {
		t.Fatal(err)
	}
	srv = NewServer(engine.New(reg, 6000), ":0", "", dir)
	cleanup = func() {
		reg.Close()
		os.RemoveAll(dir)
	}
	return
}
