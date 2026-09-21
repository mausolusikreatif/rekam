package db

import "testing"

// seedScopeCorpus builds a store with memories across three branches and returns
// it. eng is readable, finance is title-visible-only, secret is fully hidden —
// the standard fixture for the enforcement tests below.
func seedScopeCorpus(t *testing.T) (*MemoryDB, func()) {
	t.Helper()
	mdb, cleanup := openTestMemory(t)
	mems := []*Memory{
		{Title: "Deploy Guide", Content: "how to deploy the widget", Taxonomy: "eng.ops"},
		{Title: "Onboarding", Content: "welcome aboard the widget team", Taxonomy: "eng.hr"},
		{Title: "Q3 Revenue", Content: "widget revenue numbers", Taxonomy: "finance.q3"},
		{Title: "Acquisition Terms", Content: "secret widget acquisition", Taxonomy: "secret.mna"},
	}
	for _, m := range mems {
		if err := mdb.Insert(m, "admin", ""); err != nil {
			cleanup()
			t.Fatal(err)
		}
	}
	return mdb, cleanup
}

// engScope reads eng, sees finance titles, and cannot touch secret at all.
func engScope() Scope {
	return NewScope([]string{"eng"}, []string{"eng"}, []string{"finance"})
}

func TestSearchExcludesUnreadableFromResultsAndBudget(t *testing.T) {
	mdb, cleanup := seedScopeCorpus(t)
	defer cleanup()

	// "widget" matches all four memories; scope must cut it to the two eng ones.
	res, err := mdb.Search(engScope(), "widget", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Results) != 2 {
		t.Fatalf("results = %d, want 2 (eng only)", len(res.Results))
	}
	for _, m := range res.Results {
		if m.Taxonomy != "eng.ops" && m.Taxonomy != "eng.hr" {
			t.Errorf("leaked result from %s", m.Taxonomy)
		}
	}

	// The leak that matters: a tiny budget must be spent on readable rows only.
	// If hidden rows were filtered post-ranking they could consume budget and
	// inflate OmittedCount, disclosing that restricted matches exist.
	tight, err := mdb.Search(engScope(), "widget", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	// Both eng rows exceed a 1-token budget, so both are omitted — but only the
	// two eng rows were ever candidates, so OmittedCount can be at most 2.
	if tight.OmittedCount > 2 {
		t.Errorf("OmittedCount = %d, want <= 2; hidden rows must not reach the budget", tight.OmittedCount)
	}
}

func TestCatalogHidesUnreadableBranchesButShowsTitleVisible(t *testing.T) {
	mdb, cleanup := seedScopeCorpus(t)
	defer cleanup()

	entries, err := mdb.Catalog(engScope())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, e := range entries {
		got[e.Path] = e.Count
	}
	if _, ok := got["secret.mna"]; ok {
		t.Error("catalog leaked a fully hidden branch")
	}
	// finance is title-visible, so its existence and count may show.
	if got["finance.q3"] != 1 {
		t.Errorf("finance.q3 count = %d, want 1 (title-visible)", got["finance.q3"])
	}
	if got["eng.ops"] != 1 || got["eng.hr"] != 1 {
		t.Errorf("readable branches missing from catalog: %v", got)
	}
}

func TestListRespectsScopeInPageAndTotal(t *testing.T) {
	mdb, cleanup := seedScopeCorpus(t)
	defer cleanup()

	mems, total, err := mdb.List(engScope(), "", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	// List is content-level, so title-visible finance is excluded too — only the
	// two readable eng memories, in both the page and the count.
	if total != 2 {
		t.Errorf("total = %d, want 2 (count must not include hidden rows)", total)
	}
	if len(mems) != 2 {
		t.Errorf("page = %d, want 2", len(mems))
	}
}

// Scenarios: spec/scope.md (SCOP-04, SCOP-05)
func TestScopeMemoriesBrowseUsesTitleVisibility(t *testing.T) {
	mdb, cleanup := seedScopeCorpus(t)
	defer cleanup()

	// SCOP-04: browsing finance (title-visible) returns its titles — no
	// content leak, but existence is permitted by the flag.
	res, err := mdb.Scope(engScope(), "finance", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Results) != 1 || res.Results[0].Title != "Q3 Revenue" {
		t.Errorf("finance browse = %+v, want the title-visible Q3 Revenue", res.Results)
	}

	// SCOP-05: browsing secret (fully hidden) returns nothing.
	res, err = mdb.Scope(engScope(), "secret", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Results) != 0 {
		t.Errorf("secret browse leaked %d entries", len(res.Results))
	}
}

func TestBacklinksDropHiddenSources(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()

	target := &Memory{Title: "Shared Target", Content: "t", Taxonomy: "eng.ops"}
	if err := mdb.Insert(target, "admin", ""); err != nil {
		t.Fatal(err)
	}
	// One readable and one hidden memory both link the target.
	if err := mdb.Insert(&Memory{Title: "Eng Ref", Content: "see [[Shared Target]]", Taxonomy: "eng.hr"}, "admin", ""); err != nil {
		t.Fatal(err)
	}
	if err := mdb.Insert(&Memory{Title: "Secret Ref", Content: "see [[Shared Target]]", Taxonomy: "secret.mna"}, "admin", ""); err != nil {
		t.Fatal(err)
	}

	links, err := mdb.Backlinks(engScope(), target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 || links[0].Title != "Eng Ref" {
		t.Errorf("backlinks = %+v, want only the readable Eng Ref", links)
	}
}

// Scenarios: spec/links-graph.md (LINK-19)
func TestEdgeHealthDropsHiddenSources(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()

	// One dangling link from a readable branch, one from a fully hidden branch.
	if err := mdb.Insert(&Memory{Title: "Eng Ref", Content: "see [[Missing Doc]]", Taxonomy: "eng.ops"}, "admin", ""); err != nil {
		t.Fatal(err)
	}
	if err := mdb.Insert(&Memory{Title: "Secret Ref", Content: "see [[Other Missing Doc]]", Taxonomy: "secret.mna"}, "admin", ""); err != nil {
		t.Fatal(err)
	}

	full, err := mdb.EdgeHealth(FullScope())
	if err != nil {
		t.Fatal(err)
	}
	if full.TotalEdges != 2 || full.Dangling != 2 {
		t.Fatalf("sanity check: full scope = %+v, want 2 edges total", full)
	}

	h, err := mdb.EdgeHealth(engScope())
	if err != nil {
		t.Fatal(err)
	}
	// LINK-19: the aggregate counts must not include the hidden branch's edge.
	if h.TotalEdges != 1 || h.Dangling != 1 {
		t.Errorf("scoped edge health = %+v, want counts covering only the eng edge", h)
	}
	if len(h.DanglingByRel) != 1 {
		t.Errorf("DanglingByRel = %+v, want only the eng edge's rel", h.DanglingByRel)
	}
	// Nor may the hidden source's id/title/taxonomy appear in the breakdown.
	for _, d := range h.TopDangling {
		for _, s := range d.Sources {
			if s.Taxonomy == "secret.mna" {
				t.Errorf("edge health leaked a hidden-branch source: %+v", s)
			}
		}
	}
}

func TestTombstonesScopedToVisibleBranches(t *testing.T) {
	mdb, cleanup := seedScopeCorpus(t)
	defer cleanup()

	// Delete one memory in each branch, then check the audit view is scoped.
	all, _, err := mdb.List(FullScope(), "", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range all {
		if err := mdb.Delete(m.ID, "admin"); err != nil {
			t.Fatal(err)
		}
	}

	ts, err := mdb.Tombstones(engScope(), "", 0)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, tomb := range ts {
		seen[tomb.Taxonomy] = true
	}
	if seen["secret.mna"] {
		t.Error("tombstone audit leaked a fully hidden branch")
	}
	// eng (readable) and finance (title-visible) tombstones are allowed.
	if !seen["eng.ops"] || !seen["finance.q3"] {
		t.Errorf("expected eng and finance tombstones, got %v", seen)
	}
}

// Scenarios: spec/links-graph.md (LINK-17)
func TestSuggestLinksNeverPairsHiddenMemories(t *testing.T) {
	mdb, cleanup := seedScopeCorpus(t)
	defer cleanup()

	sugg, err := mdb.SuggestLinks(engScope(), 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sugg {
		if s.Source.Taxonomy == "secret.mna" || s.Target.Taxonomy == "secret.mna" ||
			s.Source.Taxonomy == "finance.q3" || s.Target.Taxonomy == "finance.q3" {
			t.Errorf("suggestion exposed a non-readable memory: %+v", s)
		}
	}
}

// TestGraphStopsAtScopeBoundary is the traversal-leak test: a hidden hub must not
// bridge two readable memories, and a title-visible node appears only as a leaf.
// Scenarios: spec/links-graph.md (LINK-13, LINK-14)
func TestGraphStopsAtScopeBoundary(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()

	// left ── hub(secret) ── right, plus left ── fin(finance, title-visible).
	hub := &Memory{Title: "Secret Hub", Content: "hidden", Taxonomy: "secret.mna"}
	fin := &Memory{Title: "Finance Leaf", Content: "numbers", Taxonomy: "finance.q3"}
	for _, m := range []*Memory{hub, fin} {
		if err := mdb.Insert(m, "admin", ""); err != nil {
			t.Fatal(err)
		}
	}
	left := &Memory{Title: "Left", Content: "see [[Secret Hub]] and [[Finance Leaf]]", Taxonomy: "eng.ops"}
	right := &Memory{Title: "Right", Content: "see [[Secret Hub]]", Taxonomy: "eng.hr"}
	for _, m := range []*Memory{left, right} {
		if err := mdb.Insert(m, "admin", ""); err != nil {
			t.Fatal(err)
		}
	}

	g, err := mdb.Graph(engScope(), left.ID, 3, "")
	if err != nil {
		t.Fatal(err)
	}
	nodes := map[string]GraphNode{}
	for _, n := range g.Nodes {
		nodes[n.ID] = n
	}
	// LINK-13
	// The fully hidden hub must not appear at all — not even as a leaf.
	if _, ok := nodes[hub.ID]; ok {
		t.Error("fully hidden hub leaked into the graph")
	}
	// So Right, reachable only through that hub, must be unreachable.
	if _, ok := nodes[right.ID]; ok {
		t.Error("Right reached through a hidden hub; traversal must stop at the boundary")
	}
	// LINK-14
	// The finance node is title-visible: present, but as a content-hidden leaf.
	fn, ok := nodes[fin.ID]
	if !ok {
		t.Fatal("title-visible finance node should appear as a leaf")
	}
	if !fn.HiddenContent {
		t.Error("title-visible node must be marked HiddenContent")
	}
	if fn.Authority != 0 {
		t.Error("title-visible leaf must not expose an authority signal")
	}
}

// TestGraphCenterOnHiddenNodeLooksMissing: centering on an unreadable memory is
// indistinguishable from centering on one that does not exist.
// Scenarios: spec/links-graph.md (LINK-12)
func TestGraphCenterOnHiddenNodeLooksMissing(t *testing.T) {
	mdb, cleanup := seedScopeCorpus(t)
	defer cleanup()

	all, _, _ := mdb.List(FullScope(), "secret", 0, 0)
	if len(all) == 0 {
		t.Fatal("fixture missing secret memory")
	}
	if _, err := mdb.Graph(engScope(), all[0].ID, 2, ""); err == nil {
		t.Error("centering on a hidden node must error as not-found, not render it")
	}
}
