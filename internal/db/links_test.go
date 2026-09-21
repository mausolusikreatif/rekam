package db

import (
	"database/sql"
	"reflect"
	"testing"
)

// Scenarios: spec/links-graph.md (LINK-01..09, LINK-18)

func TestParseLinks(t *testing.T) {
	r := func(rel, raw string) parsedLink { return parsedLink{Rel: rel, Raw: raw} }
	cases := []struct {
		name    string
		content string
		want    []parsedLink
	}{
		{"none", "plain text, no links", nil},
		{"single default rel", "see [[Acme Onboarding]] for details", []parsedLink{r(relRelates, "acme onboarding")}},
		{"multiple", "[[Alpha]] and [[Beta]]", []parsedLink{r(relRelates, "alpha"), r(relRelates, "beta")}},
		{"dupes", "[[Alpha]] then [[alpha]] again", []parsedLink{r(relRelates, "alpha")}},
		{"normalize ws", "[[  Acme   Onboarding  ]]", []parsedLink{r(relRelates, "acme onboarding")}},
		// LINK-04
		{"fenced code ignored", "```\nsee [[NotALink]]\n```\nbut [[Real]] counts", []parsedLink{r(relRelates, "real")}},
		// LINK-04
		{"inline code ignored", "use `[[NotALink]]` syntax, link [[Real]]", []parsedLink{r(relRelates, "real")}},
		{"empty brackets", "[[]] and [[ ]] and [[Real]]", []parsedLink{r(relRelates, "real")}},
		// LINK-02
		{"typed supersedes", "[[supersedes::Old Plan]]", []parsedLink{r(relSupersedes, "old plan")}},
		// LINK-02
		{"typed depends-on", "[[depends-on:: Q3 Launch ]]", []parsedLink{r(relDependsOn, "q3 launch")}},
		// LINK-02
		{"typed contradicts", "[[contradicts::Earlier Note]]", []parsedLink{r(relContradicts, "earlier note")}},
		// LINK-03
		{"unknown rel degrades", "[[needs::Foo]]", []parsedLink{r(relRelates, "needs::foo")}},
		{"same target two rels", "[[Foo]] and [[depends-on::Foo]]", []parsedLink{r(relRelates, "foo"), r(relDependsOn, "foo")}},
		{"unclosed [[ does not match", "# [[ autocomplete is stale\n\nplain text", nil},
		{"unclosed [[ does not swallow a later real link", "# [[ autocomplete is stale\n\nRelates to [[Real Doc]].", []parsedLink{r(relRelates, "real doc")}},
		{"nested brackets in title not matched as one link", "[[Outer [[Inner]]]]", []parsedLink{r(relRelates, "inner")}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseLinks(c.content)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("parseLinks(%q) = %v, want %v", c.content, got, c.want)
			}
		})
	}
}

// edgeDst returns the resolved dst_id (and existence) of a src's edge to raw.
func edgeDst(t *testing.T, mdb *MemoryDB, srcID, raw string) (sql.NullString, bool) {
	t.Helper()
	var dst sql.NullString
	err := mdb.db.QueryRow(`SELECT dst_id FROM edges WHERE src_id = ? AND raw = ?`, srcID, raw).Scan(&dst)
	if err == sql.ErrNoRows {
		return dst, false
	}
	if err != nil {
		t.Fatalf("edgeDst: %v", err)
	}
	return dst, true
}

func TestInsertReportsLinkResolution(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()

	target := &Memory{Title: "Acme Onboarding", Content: "how to onboard"}
	if err := mdb.Insert(target, "", ""); err != nil {
		t.Fatal(err)
	}
	src := &Memory{Title: "New Hire Checklist", Content: "read [[Acme Onboarding]] and [[supersedes::Ghost Doc]]"}
	if err := mdb.Insert(src, "", ""); err != nil {
		t.Fatal(err)
	}

	if len(src.Links) != 2 {
		t.Fatalf("expected 2 link statuses, got %d (%+v)", len(src.Links), src.Links)
	}
	byTarget := map[string]LinkStatus{}
	for _, l := range src.Links {
		byTarget[l.Target] = l
	}
	// LINK-01
	if got := byTarget["acme onboarding"]; !got.Resolved || got.Rel != relRelates {
		t.Errorf("acme onboarding = %+v, want resolved relates", got)
	}
	// LINK-05
	if got := byTarget["ghost doc"]; got.Resolved || got.Rel != relSupersedes {
		t.Errorf("ghost doc = %+v, want dangling supersedes", got)
	}
}

func TestUpdateReportsLinkResolution(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()

	target := &Memory{Title: "Q3 Launch", Content: "the launch"}
	mustInsert(t, mdb, target)
	src := &Memory{Title: "Notes", Content: "no links yet"}
	mustInsert(t, mdb, src)

	updated, err := mdb.Update(src.ID, map[string]any{"content": "now [[depends-on::Q3 Launch]]"}, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Links) != 1 {
		t.Fatalf("expected 1 link status, got %d", len(updated.Links))
	}
	// LINK-02
	if l := updated.Links[0]; !l.Resolved || l.Rel != relDependsOn || l.Target != "q3 launch" {
		t.Errorf("link = %+v, want resolved depends-on q3 launch", l)
	}
}

func TestEdgeHealth(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()

	target := &Memory{Title: "Real Target", Content: "exists"}
	mustInsert(t, mdb, target)
	// src1: one resolved link + one dangling. src2: same dangling target again.
	mustInsert(t, mdb,
		&Memory{Title: "Src One", Content: "[[Real Target]] and [[supersedes::Missing Doc]]"},
		&Memory{Title: "Src Two", Content: "also [[Missing Doc]]"},
	)

	h, err := mdb.EdgeHealth(FullScope())
	if err != nil {
		t.Fatal(err)
	}
	// LINK-18
	if h.TotalEdges != 3 {
		t.Errorf("TotalEdges = %d, want 3", h.TotalEdges)
	}
	if h.Resolved != 1 {
		t.Errorf("Resolved = %d, want 1", h.Resolved)
	}
	if h.Dangling != 2 {
		t.Errorf("Dangling = %d, want 2", h.Dangling)
	}
	if h.DanglingTargets != 1 {
		t.Errorf("DanglingTargets = %d, want 1 (both reference 'missing doc')", h.DanglingTargets)
	}
	if len(h.TopDangling) == 0 || h.TopDangling[0].Target != "missing doc" {
		t.Errorf("TopDangling = %+v, want 'missing doc' first", h.TopDangling)
	}
	// Each dangling entry must carry the source docs that reference it.
	var totalSources int
	for _, d := range h.TopDangling {
		if len(d.Sources) != d.Count {
			t.Errorf("target %q: %d sources but count %d", d.Target, len(d.Sources), d.Count)
		}
		for _, s := range d.Sources {
			if s.ID == "" || s.Title == "" {
				t.Errorf("target %q: source missing id/title: %+v", d.Target, s)
			}
		}
		totalSources += len(d.Sources)
	}
	if totalSources != 2 {
		t.Errorf("total dangling sources = %d, want 2", totalSources)
	}
}

func TestEdgeResolvesOnInsert(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()

	target := &Memory{Title: "Acme Onboarding", Content: "how to onboard"}
	if err := mdb.Insert(target, "", ""); err != nil {
		t.Fatal(err)
	}
	src := &Memory{Title: "New Hire Checklist", Content: "first read [[Acme Onboarding]]"}
	if err := mdb.Insert(src, "", ""); err != nil {
		t.Fatal(err)
	}

	// LINK-01
	dst, ok := edgeDst(t, mdb, src.ID, "acme onboarding")
	if !ok {
		t.Fatal("expected an edge from src")
	}
	if !dst.Valid || dst.String != target.ID {
		t.Errorf("edge dst = %v, want %s", dst, target.ID)
	}
}

func TestEdgeDanglingThenHeals(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()

	// Link to a title that doesn't exist yet -> dangling.
	src := &Memory{Title: "Roadmap", Content: "depends on [[Q3 Launch]]"}
	if err := mdb.Insert(src, "", ""); err != nil {
		t.Fatal(err)
	}
	// LINK-05
	dst, ok := edgeDst(t, mdb, src.ID, "q3 launch")
	if !ok {
		t.Fatal("expected dangling edge")
	}
	if dst.Valid {
		t.Errorf("expected dangling (NULL) dst, got %s", dst.String)
	}

	// Now create the target; the dangling edge should heal.
	target := &Memory{Title: "Q3 Launch", Content: "the launch plan"}
	if err := mdb.Insert(target, "", ""); err != nil {
		t.Fatal(err)
	}
	// LINK-06
	dst, _ = edgeDst(t, mdb, src.ID, "q3 launch")
	if !dst.Valid || dst.String != target.ID {
		t.Errorf("edge should have healed to %s, got %v", target.ID, dst)
	}
}

func TestEdgesRebuiltOnUpdate(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()

	a := &Memory{Title: "Alpha", Content: "a"}
	b := &Memory{Title: "Beta", Content: "b"}
	mustInsert(t, mdb, a, b)

	src := &Memory{Title: "Src", Content: "link [[Alpha]]"}
	if err := mdb.Insert(src, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := edgeDst(t, mdb, src.ID, "alpha"); !ok {
		t.Fatal("expected edge to alpha")
	}

	// Update content to point at Beta instead.
	if _, err := mdb.Update(src.ID, map[string]any{"content": "link [[Beta]]"}, "", "", nil); err != nil {
		t.Fatal(err)
	}
	// LINK-07
	if _, ok := edgeDst(t, mdb, src.ID, "alpha"); ok {
		t.Error("old edge to alpha should be gone")
	}
	if dst, ok := edgeDst(t, mdb, src.ID, "beta"); !ok || !dst.Valid || dst.String != b.ID {
		t.Errorf("expected resolved edge to beta, got ok=%v dst=%v", ok, dst)
	}
}

// TestDeleteKeepsInboundBoundToTombstone: an inbound edge stays bound to the
// deleted memory's id rather than going dangling, so the reference keeps naming
// what it meant and cannot be inherited by a later memory with the same title.
// LINK-08
func TestDeleteKeepsInboundBoundToTombstone(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()

	target := &Memory{Title: "Target", Content: "t"}
	if err := mdb.Insert(target, "", ""); err != nil {
		t.Fatal(err)
	}
	src := &Memory{Title: "Src", Content: "see [[Target]]"}
	if err := mdb.Insert(src, "", ""); err != nil {
		t.Fatal(err)
	}
	if dst, _ := edgeDst(t, mdb, src.ID, "target"); !dst.Valid {
		t.Fatal("precondition: edge should be resolved")
	}

	if err := mdb.Delete(target.ID, ""); err != nil {
		t.Fatal(err)
	}
	dst, ok := edgeDst(t, mdb, src.ID, "target")
	if !ok {
		t.Fatal("inbound edge should survive target deletion")
	}
	if !dst.Valid || dst.String != target.ID {
		t.Errorf("inbound edge dst = %v, want the burned id %s", dst, target.ID)
	}

	// A new memory taking the freed title must not inherit the old reference.
	replacement := &Memory{Title: "Target", Content: "unrelated new doc"}
	if err := mdb.Insert(replacement, "", ""); err != nil {
		t.Fatal(err)
	}
	dst, _ = edgeDst(t, mdb, src.ID, "target")
	if dst.String != target.ID {
		t.Errorf("edge re-homed to %s; must stay bound to the deleted %s", dst.String, target.ID)
	}

	// Nor may editing the linking memory re-resolve it by title.
	if _, err := mdb.Update(src.ID, map[string]any{"content": "still see [[Target]]"}, "", "", nil); err != nil {
		t.Fatal(err)
	}
	dst, _ = edgeDst(t, mdb, src.ID, "target")
	if dst.String != target.ID {
		t.Errorf("edge re-homed on edit to %s; must stay bound to %s", dst.String, target.ID)
	}
}

// LINK-08
func TestDeleteCascadesOutgoing(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()

	target := &Memory{Title: "Target", Content: "t"}
	src := &Memory{Title: "Src", Content: "see [[Target]]"}
	mustInsert(t, mdb, target, src)

	if err := mdb.Delete(src.ID, ""); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := mdb.db.QueryRow(`SELECT COUNT(*) FROM edges WHERE src_id = ?`, src.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("outgoing edges should cascade-delete, got %d", n)
	}
}

func TestBacklinks(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()

	target := &Memory{Title: "Hub", Content: "central"}
	if err := mdb.Insert(target, "", ""); err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"One", "Two"} {
		m := &Memory{Title: title, Content: "points to [[Hub]]"}
		if err := mdb.Insert(m, "", ""); err != nil {
			t.Fatal(err)
		}
	}

	links, err := mdb.Backlinks(FullScope(), target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 2 {
		t.Fatalf("want 2 backlinks, got %d", len(links))
	}
}

// LINK-09
func TestSelfLinkExcluded(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()

	m := &Memory{Title: "Recursive", Content: "I mention [[Recursive]] myself"}
	if err := mdb.Insert(m, "", ""); err != nil {
		t.Fatal(err)
	}
	links, err := mdb.Backlinks(FullScope(), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 0 {
		t.Errorf("self-links must not count as backlinks, got %d", len(links))
	}
}

func TestSearchRerankByAuthority(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()

	// Two memories with identical matching content -> equal BM25.
	alpha := &Memory{Title: "Alpha Report", Content: "the quarterly revenue summary"}
	beta := &Memory{Title: "Beta Report", Content: "the quarterly revenue summary"}
	mustInsert(t, mdb, alpha, beta)

	// Give Beta one backlink; the linker does not match the query.
	linker := &Memory{Title: "Index", Content: "see [[Beta Report]]"}
	if err := mdb.Insert(linker, "", ""); err != nil {
		t.Fatal(err)
	}

	res, err := mdb.Search(FullScope(), "quarterly", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Results) != 2 {
		t.Fatalf("want 2 hits, got %d", len(res.Results))
	}
	if res.Results[0].ID != beta.ID {
		t.Errorf("authority should rank Beta first, got %q", res.Results[0].Title)
	}
	if res.Results[0].Backlinks != 1 {
		t.Errorf("expected backlinks=1 evidence on top hit, got %d", res.Results[0].Backlinks)
	}
}

func TestSupersedeDemotes(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()

	// Both match the query; without ranking help, order would be arbitrary.
	old := &Memory{Title: "Old Budget", Content: "the 2026 quarterly budget plan"}
	if err := mdb.Insert(old, "", ""); err != nil {
		t.Fatal(err)
	}
	current := &Memory{Title: "New Budget", Content: "the 2026 quarterly budget plan [[supersedes::Old Budget]]"}
	if err := mdb.Insert(current, "", ""); err != nil {
		t.Fatal(err)
	}

	res, err := mdb.Search(FullScope(), "budget", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Results) != 2 {
		t.Fatalf("want 2 hits, got %d", len(res.Results))
	}
	if res.Results[0].ID != current.ID {
		t.Errorf("current memory should rank first, got %q", res.Results[0].Title)
	}
	if res.Results[1].ID != old.ID || !res.Results[1].Superseded {
		t.Errorf("superseded memory should rank last and be flagged, got %q superseded=%v",
			res.Results[1].Title, res.Results[1].Superseded)
	}
	if res.Results[0].Superseded {
		t.Error("current memory must not be flagged superseded")
	}
}

func TestDependsOnBoosts(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()

	foundation := &Memory{Title: "Auth Spec", Content: "the login token quarterly design"}
	other := &Memory{Title: "Misc Note", Content: "the login token quarterly design"}
	mustInsert(t, mdb, foundation, other)

	dependent := &Memory{Title: "Login Page", Content: "built on [[depends-on::Auth Spec]]"}
	if err := mdb.Insert(dependent, "", ""); err != nil {
		t.Fatal(err)
	}

	res, err := mdb.Search(FullScope(), "quarterly", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.Results[0].ID != foundation.ID {
		t.Errorf("depended-on memory should rank first, got %q", res.Results[0].Title)
	}
	if res.Results[0].Backlinks != 1 {
		t.Errorf("depends-on should count as authority, got backlinks=%d", res.Results[0].Backlinks)
	}
}

func TestContradictsIsNeutralAuthority(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()

	target := &Memory{Title: "Claim", Content: "the quarterly figure is 10"}
	if err := mdb.Insert(target, "", ""); err != nil {
		t.Fatal(err)
	}
	rebuttal := &Memory{Title: "Rebuttal", Content: "actually [[contradicts::Claim]]"}
	if err := mdb.Insert(rebuttal, "", ""); err != nil {
		t.Fatal(err)
	}

	res, err := mdb.Search(FullScope(), "quarterly", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res.Results {
		if r.ID == target.ID {
			if r.Backlinks != 0 {
				t.Errorf("contradicts must not count as authority, got backlinks=%d", r.Backlinks)
			}
			if r.Superseded {
				t.Error("contradicts must not flag superseded")
			}
		}
	}
}

func TestBacklinkCarriesRelation(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()

	target := &Memory{Title: "Target", Content: "t"}
	if err := mdb.Insert(target, "", ""); err != nil {
		t.Fatal(err)
	}
	src := &Memory{Title: "Src", Content: "[[supersedes::Target]]"}
	if err := mdb.Insert(src, "", ""); err != nil {
		t.Fatal(err)
	}

	links, err := mdb.Backlinks(FullScope(), target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 || links[0].Rel != relSupersedes {
		t.Fatalf("want one supersedes backlink, got %+v", links)
	}
}

func TestRebuildEdges(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()

	target := &Memory{Title: "Target", Content: "t"}
	src := &Memory{Title: "Src", Content: "see [[Target]]"}
	mustInsert(t, mdb, target, src)

	// Corrupt the edge graph, then rebuild.
	if _, err := mdb.db.Exec(`DELETE FROM edges`); err != nil {
		t.Fatal(err)
	}
	if err := mdb.RebuildEdges(); err != nil {
		t.Fatal(err)
	}
	dst, ok := edgeDst(t, mdb, src.ID, "target")
	if !ok || !dst.Valid || dst.String != target.ID {
		t.Errorf("rebuild should restore resolved edge, got ok=%v dst=%v", ok, dst)
	}
}

func TestSanitizeFTSQuery(t *testing.T) {
	cases := []struct{ in, want string }{
		{"hello world", `"hello" "world"`},
		{"Wiki-Links", `"Wiki" "Links"`},
		{"Linking Memories with Wiki-Links", `"Linking" "Memories" "with" "Wiki" "Links"`},
		{`weird: "quotes" (and) *stars*`, `"weird" "quotes" "and" "stars"`},
		{"   ", ""},
		{"-", ""},
		{"café 90日", `"café" "90日"`},
	}
	for _, c := range cases {
		if got := sanitizeFTSQuery(c.in); got != c.want {
			t.Errorf("sanitizeFTSQuery(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSearchHandlesSpecialChars(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()

	m := &Memory{Title: "Linking Memories with Wiki-Links", Content: "about [[ wiki-link syntax"}
	if err := mdb.Insert(m, "", ""); err != nil {
		t.Fatal(err)
	}

	// The hyphen used to crash FTS5 with a syntax/column error.
	res, err := mdb.Search(FullScope(), "Linking Memories with Wiki-Links", "", 0)
	if err != nil {
		t.Fatalf("search with hyphen should not error: %v", err)
	}
	if len(res.Results) != 1 || res.Results[0].ID != m.ID {
		t.Fatalf("expected to find the hyphenated-title memory, got %d results", len(res.Results))
	}

	// A query with no usable tokens returns empty, not an error.
	empty, err := mdb.Search(FullScope(), "  -:* ", "", 0)
	if err != nil {
		t.Fatalf("empty-token query should not error: %v", err)
	}
	if len(empty.Results) != 0 {
		t.Errorf("expected no results for token-less query, got %d", len(empty.Results))
	}
}

func mustInsert(t *testing.T, mdb *MemoryDB, mems ...*Memory) {
	t.Helper()
	for _, m := range mems {
		if err := mdb.Insert(m, "", ""); err != nil {
			t.Fatalf("insert %q: %v", m.Title, err)
		}
	}
}
