package db

import (
	"database/sql"
	"fmt"
	"strings"
)

// GraphNode is one vertex in the link-graph view — either a real memory or a
// "ghost" standing in for an unresolved [[wiki-link]] target (Dangling=true).
type GraphNode struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Taxonomy   string `json:"taxonomy,omitempty"`
	Authority  int    `json:"authority"`            // inbound boosting edges (relates / depends-on)
	Superseded bool   `json:"superseded,omitempty"` // another memory supersedes this one
	Dangling   bool   `json:"dangling,omitempty"`   // synthetic node for a not-yet-written target
	// Deleted marks a tombstone: a target that existed and was removed. Unlike a
	// dangling node it has a real id and taxonomy, but it is a leaf — it has no
	// edges of its own, so the graph never routes through it.
	Deleted   bool   `json:"deleted,omitempty"`
	DeletedAt string `json:"deleted_at,omitempty"`
	DeletedBy string `json:"deleted_by,omitempty"`
	// HiddenContent marks a node the caller may know exists (title-visible branch)
	// but may not read. It appears as a leaf with no authority signal, so nothing
	// about the restricted memory beyond its title and branch is disclosed.
	HiddenContent bool `json:"hidden_content,omitempty"`
}

// GraphEdge is one typed [[link]] from Src to Dst. For a dangling link Dst is the
// id of the synthetic ghost node ("dangling:" + normalized target).
type GraphEdge struct {
	Src string `json:"src"`
	Dst string `json:"dst"`
	Rel string `json:"rel"`
}

// Graph is a slice of the link graph — the typed [[wiki-link]] edges between
// memories, as opposed to the taxonomy hierarchy. It powers the mindmap view.
type Graph struct {
	Center string      `json:"center,omitempty"` // ego-graph focus id, "" for a corpus graph
	Nodes  []GraphNode `json:"nodes"`
	Edges  []GraphEdge `json:"edges"`
}

const danglingPrefix = "dangling:"

// maxGraphDepth bounds ego-graph expansion so a hub memory can't pull in the
// whole store; corpus graphs are unbounded (the caller scopes by taxonomy).
const maxGraphDepth = 4

// Graph builds a link-graph slice. With center set, it returns the ego graph:
// memories reachable from center within depth hops over resolved edges (traversed
// in both directions), plus any dangling links hanging off the reached nodes.
// With center empty, it returns the corpus graph, optionally restricted to a
// taxonomy prefix. Either way, dangling links surface as ghost nodes so the
// not-yet-written targets are visible.
func (m *MemoryDB) Graph(scope Scope, center string, depth int, taxonomy string) (*Graph, error) {
	meta, err := m.graphNodeMeta()
	if err != nil {
		return nil, err
	}
	edges, err := m.allEdges()
	if err != nil {
		return nil, err
	}

	// leaves are nodes that may be an edge endpoint but never a traversal hop, so
	// they cannot bridge two clusters the caller may otherwise see. Two kinds:
	// tombstones (deleted targets), and — under a restricted scope — title-only
	// nodes, memories in a title-visible branch whose content stays hidden.
	// Everything not readable and not a leaf is erased from meta entirely, so a
	// fully restricted branch leaves no trace in the graph's shape.
	leaves, err := m.tombstoneNodes(scope)
	if err != nil {
		return nil, err
	}
	if !scope.Unrestricted() {
		for id, n := range meta {
			if scope.CanRead(n.Taxonomy) {
				continue
			}
			if scope.TitleVisible(n.Taxonomy) {
				// Project to a bare leaf: title and branch only, no authority
				// signal (which would count possibly-hidden inbound edges).
				leaves[id] = GraphNode{ID: id, Title: n.Title, Taxonomy: n.Taxonomy, HiddenContent: true}
			}
			delete(meta, id) // hidden, or now represented as a leaf
		}
	}
	if center != "" {
		if _, ok := meta[center]; !ok {
			// Hidden or nonexistent centers are indistinguishable on purpose.
			return nil, fmt.Errorf("memory not found: %s", center)
		}
	}

	included := m.includedNodes(center, depth, taxonomy, meta, edges)

	g := &Graph{Center: center, Nodes: []GraphNode{}, Edges: []GraphEdge{}}
	for id := range included {
		g.Nodes = append(g.Nodes, meta[id])
	}

	ghosts := map[string]bool{}
	for _, e := range edges {
		if !included[e.src] {
			continue
		}
		if e.dst != "" {
			// A resolved edge into a leaf (tombstone or title-only node): attach
			// the leaf on demand and draw the edge, so the reference stays visible
			// but the leaf never participates in traversal.
			if leafNode, ok := leaves[e.dst]; ok {
				if !ghosts[e.dst] {
					ghosts[e.dst] = true
					g.Nodes = append(g.Nodes, leafNode)
				}
				g.Edges = append(g.Edges, GraphEdge{Src: e.src, Dst: e.dst, Rel: e.rel})
				continue
			}
			if !included[e.dst] {
				continue // resolved target outside the included slice
			}
			g.Edges = append(g.Edges, GraphEdge{Src: e.src, Dst: e.dst, Rel: e.rel})
			continue
		}
		// Dangling link → synthesize a ghost node keyed by its target text.
		gid := danglingPrefix + e.raw
		if !ghosts[gid] {
			ghosts[gid] = true
			g.Nodes = append(g.Nodes, GraphNode{ID: gid, Title: e.raw, Dangling: true})
		}
		g.Edges = append(g.Edges, GraphEdge{Src: e.src, Dst: gid, Rel: e.rel})
	}
	return g, nil
}

// rawEdge is an edge row as stored: dst is "" for a dangling link.
type rawEdge struct {
	src, dst, raw, rel string
}

func (m *MemoryDB) allEdges() ([]rawEdge, error) {
	rows, err := m.db.Query(`SELECT src_id, dst_id, raw, rel FROM edges`)
	if err != nil {
		return nil, fmt.Errorf("graph edges: %w", err)
	}
	defer rows.Close()

	var out []rawEdge
	for rows.Next() {
		var e rawEdge
		var dst sql.NullString
		if err := rows.Scan(&e.src, &dst, &e.raw, &e.rel); err != nil {
			return nil, err
		}
		e.dst = dst.String
		out = append(out, e)
	}
	return out, rows.Err()
}

// tombstoneNodes loads every tombstone as a leaf graph node keyed by id.
func (m *MemoryDB) tombstoneNodes(scope Scope) (map[string]GraphNode, error) {
	tombstones, err := m.Tombstones(scope, "", 0)
	if err != nil {
		return nil, err
	}
	out := make(map[string]GraphNode, len(tombstones))
	for _, t := range tombstones {
		out[t.MemoryID] = GraphNode{
			ID:        t.MemoryID,
			Title:     t.Title,
			Taxonomy:  t.Taxonomy,
			Deleted:   true,
			DeletedAt: t.DeletedAt,
			DeletedBy: t.DeletedBy,
		}
	}
	return out, nil
}

// graphNodeMeta loads every memory keyed by id, with authority and superseded
// flags computed the same way Search ranks them, so node weight is consistent.
func (m *MemoryDB) graphNodeMeta() (map[string]GraphNode, error) {
	rows, err := m.db.Query(`SELECT m.id, m.title, m.taxonomy, ` +
		authorityExpr + `, ` + supersededExpr + ` FROM memories m`)
	if err != nil {
		return nil, fmt.Errorf("graph nodes: %w", err)
	}
	defer rows.Close()

	out := map[string]GraphNode{}
	for rows.Next() {
		var n GraphNode
		var tax sql.NullString
		if err := rows.Scan(&n.ID, &n.Title, &tax, &n.Authority, &n.Superseded); err != nil {
			return nil, err
		}
		n.Taxonomy = tax.String
		out[n.ID] = n
	}
	return out, rows.Err()
}

// includedNodes returns the set of real memory ids that belong in the graph.
func (m *MemoryDB) includedNodes(center string, depth int, taxonomy string, meta map[string]GraphNode, edges []rawEdge) map[string]bool {
	if center == "" {
		set := map[string]bool{}
		for id, n := range meta {
			if taxonomy == "" || n.Taxonomy == taxonomy || strings.HasPrefix(n.Taxonomy, taxonomy+".") {
				set[id] = true
			}
		}
		return set
	}

	if depth <= 0 {
		depth = 2
	}
	if depth > maxGraphDepth {
		depth = maxGraphDepth
	}

	// Adjacency over resolved edges, undirected for neighborhood exploration.
	// Only edges between two full participants (both endpoints in meta) form a
	// hop. That single rule stops every kind of non-participant from bridging:
	// tombstones and title-only leaves (never in meta), and fully hidden nodes
	// (removed from meta) — none can pull unrelated neighbours into each other's
	// ego graphs.
	adj := map[string][]string{}
	for _, e := range edges {
		if e.dst == "" {
			continue
		}
		if _, ok := meta[e.src]; !ok {
			continue
		}
		if _, ok := meta[e.dst]; !ok {
			continue
		}
		adj[e.src] = append(adj[e.src], e.dst)
		adj[e.dst] = append(adj[e.dst], e.src)
	}

	visited := map[string]bool{center: true}
	frontier := []string{center}
	for d := 0; d < depth && len(frontier) > 0; d++ {
		var next []string
		for _, id := range frontier {
			for _, nb := range adj[id] {
				if !visited[nb] {
					visited[nb] = true
					next = append(next, nb)
				}
			}
		}
		frontier = next
	}
	return visited
}
