package api

import (
	"fmt"
	"net/http"
	"testing"
)

// Scenarios: spec/memory.md (MEM-14..MEM-20)
// Scenarios: spec/revisions.md (REV-03, REV-09, REV-10, REV-14)
// Scenarios: spec/search-taxonomy.md (SRCH-05, SRCH-08, SRCH-09, SRCH-10, SRCH-11, SRCH-12, SRCH-13, SRCH-15)
// Scenarios: spec/scope.md (SCOP-01, SCOP-02, SCOP-03, SCOP-07)
// Scenarios: spec/admin.md (ADMN-17, ADMN-18)

// seedRecords writes n numbered records into taxonomy and returns their ids in
// the order they were created. Titles fold in the taxonomy so calling this
// more than once against the same server (different branches, same tenant)
// never collides — titles are unique tenant-wide, not just per branch (MEM-22).
func seedRecords(t *testing.T, srv *Server, key, taxonomy string, n int) []string {
	t.Helper()
	ids := make([]string, 0, n)
	for i := range n {
		rr := doRequest(t, srv, "POST", "/memory", key, map[string]any{
			"title":    fmt.Sprintf("%s Record %02d", taxonomy, i),
			"taxonomy": taxonomy,
			"content":  fmt.Sprintf("body of record %02d", i),
		})
		if rr.Code != http.StatusCreated {
			t.Fatalf("seed %d: %d: %s", i, rr.Code, rr.Body)
		}
		var mem struct {
			ID string `json:"id"`
		}
		decodeJSON(t, rr, &mem)
		ids = append(ids, mem.ID)
	}
	return ids
}

type listResponse struct {
	Memories []struct {
		ID       string `json:"id"`
		Title    string `json:"title"`
		Taxonomy string `json:"taxonomy"`
	} `json:"memories"`
	Total int `json:"total"`
}

// Browsing the library is paged: the user gets a window of records plus the true
// total, so the UI can show "showing 5 of 12" and page through without ever
// seeing the same record twice or missing one.
// MEM-16
func TestBrowsingTheLibraryPagesThroughEverything(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	seedRecords(t, srv, key, "work.ops", 12)

	var first listResponse
	decodeJSON(t, doRequest(t, srv, "GET", "/memories?limit=5", key, nil), &first)
	if first.Total != 12 {
		t.Errorf("total should count the whole corpus, not the page: got %d", first.Total)
	}
	if len(first.Memories) != 5 {
		t.Fatalf("limit=5 should return 5 records, got %d", len(first.Memories))
	}

	// Paging forward continues where the first page stopped.
	var second listResponse
	decodeJSON(t, doRequest(t, srv, "GET", "/memories?limit=5&offset=5", key, nil), &second)
	if len(second.Memories) != 5 {
		t.Fatalf("second page should hold 5 records, got %d", len(second.Memories))
	}
	seen := map[string]bool{}
	for _, m := range first.Memories {
		seen[m.ID] = true
	}
	for _, m := range second.Memories {
		if seen[m.ID] {
			t.Errorf("record %s appeared on two pages", m.ID)
		}
		seen[m.ID] = true
	}

	// The last page is partial, not padded.
	var last listResponse
	decodeJSON(t, doRequest(t, srv, "GET", "/memories?limit=5&offset=10", key, nil), &last)
	if len(last.Memories) != 2 {
		t.Errorf("final page should hold the remaining 2, got %d", len(last.Memories))
	}

	// Paging past the end is empty, not an error.
	var beyond listResponse
	rr := doRequest(t, srv, "GET", "/memories?limit=5&offset=999", key, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("paging past the end should be 200, got %d", rr.Code)
	}
	decodeJSON(t, rr, &beyond)
	if len(beyond.Memories) != 0 {
		t.Errorf("expected no records past the end, got %d", len(beyond.Memories))
	}
}

// Narrowing the library to one branch shows that branch and its sub-branches,
// and nothing from a sibling.
// MEM-17
func TestBrowsingNarrowsToABranch(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	seedRecords(t, srv, key, "work.ops", 3)
	seedRecords(t, srv, key, "work.design", 2)
	seedRecords(t, srv, key, "finance.accounts", 4)

	var ops listResponse
	decodeJSON(t, doRequest(t, srv, "GET", "/memories?taxonomy=work.ops", key, nil), &ops)
	if ops.Total != 3 {
		t.Errorf("work.ops should hold 3 records, got %d", ops.Total)
	}

	// A parent branch rolls its children up.
	var work listResponse
	decodeJSON(t, doRequest(t, srv, "GET", "/memories?taxonomy=work", key, nil), &work)
	if work.Total != 5 {
		t.Errorf("work should roll up ops + design = 5, got %d", work.Total)
	}
	for _, m := range work.Memories {
		if m.Taxonomy == "finance.accounts" {
			t.Errorf("a sibling branch leaked into the work listing: %+v", m)
		}
	}

	// MEM-18: an empty branch is an empty list, not a 404.
	rr := doRequest(t, srv, "GET", "/memories?taxonomy=nonexistent", key, nil)
	if rr.Code != http.StatusOK {
		t.Errorf("unknown branch should be 200 with no records, got %d: %s", rr.Code, rr.Body)
	}
}

// MEM-19: nonsense paging parameters are rejected up front rather than being
// coerced into a surprising query.
func TestBrowsingRejectsInvalidPaging(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	for _, q := range []string{"limit=abc", "limit=-1", "offset=-5", "offset=xyz"} {
		if rr := doRequest(t, srv, "GET", "/memories?"+q, key, nil); rr.Code != http.StatusBadRequest {
			t.Errorf("%s: want 400, got %d: %s", q, rr.Code, rr.Body)
		}
	}
}

// Loading a branch into an agent's context is budgeted: asking for a small token
// budget returns less than asking for a large one, so a big branch can't blow up
// the context window.
// SRCH-08, SRCH-10; SCOP-01, SCOP-02, SCOP-03
func TestLoadingABranchRespectsATokenBudget(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	for i := range 20 {
		doRequest(t, srv, "POST", "/memory", key, map[string]any{
			"title":    fmt.Sprintf("Runbook %02d", i),
			"taxonomy": "work.ops",
			"content":  "a reasonably long body that consumes tokens when loaded into context, repeated for weight. " + fmt.Sprint(i),
		})
	}

	type scopeResponse struct {
		Results []struct {
			Title    string `json:"title"`
			Taxonomy string `json:"taxonomy"`
			Content  string `json:"content"`
		} `json:"results"`
		OmittedCount int `json:"omitted_count"`
	}

	var full scopeResponse
	// SCOP-01
	rr := doRequest(t, srv, "GET", "/scope?taxonomy=work.ops", key, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("scope: %d: %s", rr.Code, rr.Body)
	}
	decodeJSON(t, rr, &full)
	if len(full.Results) != 20 {
		t.Fatalf("a generous budget should list the whole branch, got %d", len(full.Results))
	}
	// SCOP-01: browsing is a title-level view; bodies are fetched per record on demand.
	for _, m := range full.Results {
		if m.Content != "" {
			t.Errorf("browse should not ship record bodies: %+v", m)
			break
		}
	}

	var budgeted scopeResponse
	rr = doRequest(t, srv, "GET", "/scope?taxonomy=work.ops&token_budget=30", key, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("budgeted scope: %d: %s", rr.Code, rr.Body)
	}
	decodeJSON(t, rr, &budgeted)

	// SCOP-03
	if len(budgeted.Results) >= len(full.Results) {
		t.Errorf("a tight token budget should return fewer records: budgeted %d, full %d",
			len(budgeted.Results), len(full.Results))
	}
	// SCOP-03: the caller is told what was left out, so it knows to narrow the
	// query rather than concluding the branch is small.
	if budgeted.OmittedCount != len(full.Results)-len(budgeted.Results) {
		t.Errorf("omitted_count should account for everything truncated: omitted %d, returned %d of %d",
			budgeted.OmittedCount, len(budgeted.Results), len(full.Results))
	}

	// SRCH-09, SCOP-02: browsing without naming a branch is a usable error, not the whole corpus.
	if rr := doRequest(t, srv, "GET", "/scope", key, nil); rr.Code < 400 {
		t.Errorf("scope without a taxonomy should be refused, got %d: %s", rr.Code, rr.Body)
	}
	// SRCH-05, SCOP-03
	if rr := doRequest(t, srv, "GET", "/scope?taxonomy=work.ops&token_budget=-1", key, nil); rr.Code != http.StatusBadRequest {
		t.Errorf("a negative budget should be rejected, got %d", rr.Code)
	}
}

// A user who edits a record and regrets it can see the record's history and put
// an earlier version back, all from the web surface.
// REV-03, REV-09, REV-10
func TestUserRestoresAnEarlierVersionFromTheWeb(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	write := doRequest(t, srv, "POST", "/memory", key, map[string]any{
		"title": "Runbook", "taxonomy": "work.ops", "content": "the good content",
	})
	var mem struct {
		ID string `json:"id"`
	}
	decodeJSON(t, write, &mem)

	doRequest(t, srv, "PATCH", "/memory/"+mem.ID, key, map[string]any{"content": "bad paste"})

	var hist struct {
		Revisions []struct {
			Version int    `json:"version"`
			Content string `json:"content"`
		} `json:"revisions"`
	}
	rr := doRequest(t, srv, "GET", "/memory/"+mem.ID+"/revisions", key, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("history: %d: %s", rr.Code, rr.Body)
	}
	decodeJSON(t, rr, &hist)
	if len(hist.Revisions) < 2 {
		t.Fatalf("history should show the original and the edit, got %d", len(hist.Revisions))
	}

	// REV-09
	rr = doRequest(t, srv, "POST", "/memory/"+mem.ID+"/restore/1", key, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("restore: %d: %s", rr.Code, rr.Body)
	}
	var restored struct {
		Content string `json:"content"`
	}
	decodeJSON(t, rr, &restored)
	if restored.Content != "the good content" {
		t.Errorf("restore should bring back version 1, got %q", restored.Content)
	}

	// REV-10: a version that never existed, or a non-numeric one, is an error not a no-op.
	if rr := doRequest(t, srv, "POST", "/memory/"+mem.ID+"/restore/999", key, nil); rr.Code < 400 {
		t.Errorf("restoring a nonexistent version should fail, got %d", rr.Code)
	}
	if rr := doRequest(t, srv, "POST", "/memory/"+mem.ID+"/restore/abc", key, nil); rr.Code != http.StatusBadRequest {
		t.Errorf("a non-numeric version should be 400, got %d", rr.Code)
	}
	// History for a record that does not exist is an error, not an empty list.
	if rr := doRequest(t, srv, "GET", "/memory/no-such-id/revisions", key, nil); rr.Code < 400 {
		t.Errorf("history of an unknown record should fail, got %d", rr.Code)
	}
}

// Deleted records leave a visible trace the user can review, filtered to the
// branch they are looking at.
// MEM-14, MEM-15
func TestDeletedRecordsAreListedForReview(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	ops := seedRecords(t, srv, key, "work.ops", 2)
	fin := seedRecords(t, srv, key, "finance.accounts", 1)

	doRequest(t, srv, "DELETE", "/memory/"+ops[0], key, nil)
	doRequest(t, srv, "DELETE", "/memory/"+fin[0], key, nil)

	var all struct {
		Deleted []struct {
			Title    string `json:"title"`
			Taxonomy string `json:"taxonomy"`
		} `json:"deleted"`
	}
	decodeJSON(t, doRequest(t, srv, "GET", "/deleted", key, nil), &all)
	if len(all.Deleted) != 2 {
		t.Fatalf("expected 2 tombstones, got %d: %+v", len(all.Deleted), all.Deleted)
	}

	var scoped struct {
		Deleted []struct {
			Taxonomy string `json:"taxonomy"`
		} `json:"deleted"`
	}
	decodeJSON(t, doRequest(t, srv, "GET", "/deleted?taxonomy=work", key, nil), &scoped)
	if len(scoped.Deleted) != 1 || scoped.Deleted[0].Taxonomy != "work.ops" {
		t.Errorf("deleted list should filter by branch, got %+v", scoped.Deleted)
	}
}

// A new corpus is not a blank page: it offers a starter taxonomy the user can
// file into. Saving their own replaces it, and clearing it reverts to the default
// — so a customisation is never a one-way door.
// SRCH-11, SRCH-12, SRCH-13
func TestTaxonomyTemplateStartsWithADefaultAndCanBeCustomised(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	var initial templateResponse
	rr := doRequest(t, srv, "GET", "/taxonomy/template", key, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("template: %d: %s", rr.Code, rr.Body)
	}
	decodeJSON(t, rr, &initial)
	if len(initial.Branches) == 0 {
		t.Fatal("a fresh corpus should still offer a starter taxonomy")
	}
	// SRCH-11
	if initial.Source != "default" {
		t.Errorf("an untouched corpus should report the shipped default, got source=%q", initial.Source)
	}
	if initial.Kind != "personal" {
		t.Errorf("a personal corpus should get the personal scaffold, got kind=%q", initial.Kind)
	}
	if !initial.CanEdit {
		t.Error("the owner of their own corpus should be allowed to edit its template")
	}
	for _, b := range initial.Branches {
		if b.Description == "" {
			t.Errorf("every suggested branch needs a 'what goes here' note: %+v", b)
		}
	}

	// SRCH-12: the user replaces it with their own vocabulary.
	rr = doRequest(t, srv, "PUT", "/taxonomy/template", key, map[string]any{
		"branches": []map[string]any{
			{"path": "clients", "description": "One record per client"},
			{"path": "clients.contracts", "description": "Signed agreements"},
		},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("save template: %d: %s", rr.Code, rr.Body)
	}

	var saved templateResponse
	decodeJSON(t, doRequest(t, srv, "GET", "/taxonomy/template", key, nil), &saved)
	if len(saved.Branches) != 2 || saved.Branches[0].Path != "clients" {
		t.Fatalf("the saved template should be what is served back, got %+v", saved.Branches)
	}
	if saved.Source != "custom" {
		t.Errorf("a saved template should be reported as custom, got source=%q", saved.Source)
	}

	// SRCH-13: clearing it falls back to the shipped default rather than leaving nothing.
	rr = doRequest(t, srv, "PUT", "/taxonomy/template", key, map[string]any{"branches": []map[string]any{}})
	if rr.Code != http.StatusOK {
		t.Fatalf("clear template: %d: %s", rr.Code, rr.Body)
	}
	var reverted templateResponse
	decodeJSON(t, doRequest(t, srv, "GET", "/taxonomy/template", key, nil), &reverted)
	if reverted.Source != "default" || len(reverted.Branches) != len(initial.Branches) {
		t.Errorf("clearing should revert to the shipped default, got source=%q with %d branches",
			reverted.Source, len(reverted.Branches))
	}
}

type templateResponse struct {
	Branches []struct {
		Path        string `json:"path"`
		Description string `json:"description"`
		Sort        int    `json:"sort"`
	} `json:"branches"`
	Source  string `json:"source"`
	Kind    string `json:"kind"`
	CanEdit bool   `json:"can_edit"`
}

// Rebuilding the FTS index is, despite living under /admin/, a caller-scoped
// maintenance action available to any authenticated identity — not gated by
// is_admin the way the rest of /admin/* is (see spec/admin.md's model note).
func TestRebuildFTSIsCallerScopedNotAdminGated(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()
	seedRecords(t, srv, key, "work.ops", 1)

	// ADMN-16
	if rr := doRequest(t, srv, "POST", "/admin/rebuild-fts", key, nil); rr.Code != http.StatusOK {
		t.Fatalf("rebuild-fts with an ordinary (non-admin) key: want 200, got %d: %s", rr.Code, rr.Body)
	}
	var status struct {
		Status string `json:"status"`
	}
	decodeJSON(t, doRequest(t, srv, "POST", "/admin/rebuild-fts", key, nil), &status)
	if status.Status != "ok" {
		t.Errorf("rebuild-fts status = %q, want ok", status.Status)
	}
	// Search over the just-rebuilt index still finds the caller's own record.
	var search struct {
		Results []map[string]any `json:"results"`
	}
	decodeJSON(t, doRequest(t, srv, "GET", "/search?q=Record", key, nil), &search)
	if len(search.Results) == 0 {
		t.Error("search should still find records after a rebuild")
	}

	// ADMN-16: anonymous is still rejected — only the admin *gate* is skipped,
	// not authentication itself.
	if rr := doRequest(t, srv, "POST", "/admin/rebuild-fts", "", nil); rr.Code != http.StatusUnauthorized {
		t.Errorf("rebuild-fts anonymous: want 401, got %d: %s", rr.Code, rr.Body)
	}
}

// Link health is a maintenance view: it tells the user which links point at
// nothing so they can fix them.
func TestLinkHealthReportsBrokenLinks(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	doRequest(t, srv, "POST", "/memory", key, map[string]any{
		"title": "Postgres", "taxonomy": "work.infra", "content": "the database",
	})
	doRequest(t, srv, "POST", "/memory", key, map[string]any{
		"title": "API", "taxonomy": "work.infra", "content": "uses [[Postgres]] and [[Redis]]",
	})

	var health struct {
		TotalEdges int `json:"total_edges"`
		Resolved   int `json:"resolved"`
		Dangling   int `json:"dangling"`
	}
	// ADMN-17
	rr := doRequest(t, srv, "GET", "/admin/edge-health", key, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("edge health: %d: %s", rr.Code, rr.Body)
	}
	decodeJSON(t, rr, &health)
	if health.TotalEdges != 2 || health.Resolved != 1 || health.Dangling != 1 {
		t.Errorf("expected 2 edges, 1 resolved, 1 dangling; got %+v", health)
	}

	// ADMN-18: writing the missing record heals the link with no edit to the source.
	doRequest(t, srv, "POST", "/memory", key, map[string]any{
		"title": "Redis", "taxonomy": "work.infra", "content": "the cache",
	})
	decodeJSON(t, doRequest(t, srv, "GET", "/admin/edge-health", key, nil), &health)
	if health.Dangling != 0 || health.Resolved != 2 {
		t.Errorf("the link should heal once the target exists, got %+v", health)
	}
}

// Every library route requires credentials — none of them may answer to an
// anonymous caller.
// MEM-20 (/memories, /deleted, /memory/{id}); REV-14 (/memory/{id}/revisions);
// SRCH-15 (/scope, /catalog, /search, /taxonomy/template); SCOP-07 (/scope);
// ADMN-16/17 (/admin/edge-health requires auth, though not admin specifically)
func TestLibraryRoutesRejectAnonymousCallers(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()
	ids := seedRecords(t, srv, key, "work.ops", 1)

	routes := []struct{ method, path string }{
		{"GET", "/memories"},
		{"GET", "/scope?taxonomy=work"},
		{"GET", "/deleted"},
		{"GET", "/catalog"},
		{"GET", "/search?q=x"},
		{"GET", "/taxonomy/template"},
		{"GET", "/admin/edge-health"},
		{"GET", "/memory/" + ids[0]},
		{"GET", "/memory/" + ids[0] + "/revisions"},
	}
	for _, rt := range routes {
		if rr := doRequest(t, srv, rt.method, rt.path, "", nil); rr.Code != http.StatusUnauthorized {
			t.Errorf("%s %s: want 401 for an anonymous caller, got %d: %s", rt.method, rt.path, rr.Code, rr.Body)
		}
	}
}
