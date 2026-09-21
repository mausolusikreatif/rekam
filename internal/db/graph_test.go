package db

import "testing"

// Scenarios: spec/links-graph.md (LINK-10..12)

// nodeByID indexes a graph's nodes for assertions.
func nodeByID(g *Graph) map[string]GraphNode {
	m := map[string]GraphNode{}
	for _, n := range g.Nodes {
		m[n.ID] = n
	}
	return m
}

// hasEdge reports whether g contains an edge src->dst with rel.
func hasEdge(g *Graph, src, dst, rel string) bool {
	for _, e := range g.Edges {
		if e.Src == src && e.Dst == dst && e.Rel == rel {
			return true
		}
	}
	return false
}

// LINK-10
func TestGraphCorpus(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()

	hub := &Memory{Title: "Hub", Taxonomy: "work", Content: "links [[Leaf]] and [[depends-on::Missing Doc]]"}
	leaf := &Memory{Title: "Leaf", Taxonomy: "work", Content: "leaf content"}
	mustInsert(t, mdb, hub, leaf)

	g, err := mdb.Graph(FullScope(), "", 0, "")
	if err != nil {
		t.Fatal(err)
	}

	nodes := nodeByID(g)
	if _, ok := nodes[hub.ID]; !ok {
		t.Fatal("hub node missing")
	}
	if _, ok := nodes[leaf.ID]; !ok {
		t.Fatal("leaf node missing")
	}
	// The dangling link becomes a ghost node.
	ghost := danglingPrefix + "missing doc"
	if gn, ok := nodes[ghost]; !ok || !gn.Dangling {
		t.Fatalf("expected dangling ghost node %q, got %+v", ghost, gn)
	}
	if !hasEdge(g, hub.ID, leaf.ID, "relates") {
		t.Error("missing resolved edge hub->leaf")
	}
	if !hasEdge(g, hub.ID, ghost, "depends-on") {
		t.Error("missing dangling edge hub->ghost")
	}
	// Leaf has one inbound boosting edge, so authority 1.
	if nodes[leaf.ID].Authority != 1 {
		t.Errorf("leaf authority = %d, want 1", nodes[leaf.ID].Authority)
	}
}

func TestGraphCorpusTaxonomyFilter(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()

	a := &Memory{Title: "Work Doc", Taxonomy: "work.projects", Content: "x"}
	b := &Memory{Title: "Home Doc", Taxonomy: "home", Content: "y"}
	mustInsert(t, mdb, a, b)

	g, err := mdb.Graph(FullScope(), "", 0, "work")
	if err != nil {
		t.Fatal(err)
	}
	nodes := nodeByID(g)
	if _, ok := nodes[a.ID]; !ok {
		t.Error("work doc should be included")
	}
	if _, ok := nodes[b.ID]; ok {
		t.Error("home doc should be excluded by taxonomy filter")
	}
}

// LINK-11
func TestGraphEgoDepth(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()

	// Chain: A -> B -> C -> D
	d := &Memory{Title: "D", Taxonomy: "t", Content: "end"}
	c := &Memory{Title: "C", Taxonomy: "t", Content: "to [[D]]"}
	b := &Memory{Title: "B", Taxonomy: "t", Content: "to [[C]]"}
	a := &Memory{Title: "A", Taxonomy: "t", Content: "to [[B]]"}
	mustInsert(t, mdb, d, c, b, a)

	g, err := mdb.Graph(FullScope(), a.ID, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	nodes := nodeByID(g)
	if _, ok := nodes[a.ID]; !ok {
		t.Error("center A missing")
	}
	if _, ok := nodes[b.ID]; !ok {
		t.Error("B (1 hop) missing")
	}
	if _, ok := nodes[c.ID]; ok {
		t.Error("C (2 hops) should be excluded at depth 1")
	}
	if g.Center != a.ID {
		t.Errorf("center = %q, want %q", g.Center, a.ID)
	}

	// Depth 2 reaches C but not D.
	g2, err := mdb.Graph(FullScope(), a.ID, 2, "")
	if err != nil {
		t.Fatal(err)
	}
	nodes2 := nodeByID(g2)
	if _, ok := nodes2[c.ID]; !ok {
		t.Error("C (2 hops) should be included at depth 2")
	}
	if _, ok := nodes2[d.ID]; ok {
		t.Error("D (3 hops) should be excluded at depth 2")
	}
}

func TestGraphEgoUndirected(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()

	// B links TO center; center must still discover B (incoming edge).
	center := &Memory{Title: "Center", Taxonomy: "t", Content: "no outgoing"}
	mustInsert(t, mdb, center)
	b := &Memory{Title: "Pointer", Taxonomy: "t", Content: "see [[Center]]"}
	mustInsert(t, mdb, b)

	g, err := mdb.Graph(FullScope(), center.ID, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := nodeByID(g)[b.ID]; !ok {
		t.Error("incoming neighbor not discovered via undirected BFS")
	}
}

// LINK-12
func TestGraphEgoNotFound(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()
	if _, err := mdb.Graph(FullScope(), "nope", 1, ""); err == nil {
		t.Error("expected error for unknown center id")
	}
}
