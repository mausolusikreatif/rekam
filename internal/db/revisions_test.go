package db

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
)

// Scenarios: spec/revisions.md (REV-01..REV-08, REV-13, REV-15, REV-16)
// Scenarios: spec/memory.md (MEM-12, MEM-13)

// REV-01
func TestInsertRecordsFirstRevision(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()

	mem := &Memory{Title: "Runbook", Content: "step one", Taxonomy: "ops"}
	if err := mdb.Insert(mem, "alice", ""); err != nil {
		t.Fatal(err)
	}
	if mem.Version != 1 {
		t.Errorf("version after insert = %d, want 1", mem.Version)
	}

	revs, err := mdb.Revisions(mem.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(revs) != 1 {
		t.Fatalf("revisions = %d, want 1", len(revs))
	}
	if revs[0].Version != 1 || revs[0].AuthorID != "alice" || revs[0].Content != "step one" {
		t.Errorf("revision 1 = %+v, want version 1 by alice with %q", revs[0], "step one")
	}
}

// REV-02, REV-08
func TestUpdateRecordsRevisionAndBumpsVersion(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()

	mem := &Memory{Title: "Runbook", Content: "step one", Taxonomy: "ops"}
	if err := mdb.Insert(mem, "alice", ""); err != nil {
		t.Fatal(err)
	}
	updated, err := mdb.Update(mem.ID, map[string]any{"content": "step two"}, "bob", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Version != 2 {
		t.Errorf("version after update = %d, want 2", updated.Version)
	}

	revs, err := mdb.Revisions(mem.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(revs) != 2 {
		t.Fatalf("revisions = %d, want 2", len(revs))
	}
	// Newest first, and each version is attributed to whoever wrote it.
	if revs[0].Version != 2 || revs[0].AuthorID != "bob" || revs[0].Content != "step two" {
		t.Errorf("latest revision = %+v, want v2 by bob", revs[0])
	}
	// The whole point: the overwritten text is still recoverable.
	if revs[1].Content != "step one" || revs[1].AuthorID != "alice" {
		t.Errorf("prior revision = %+v, want v1 by alice with original content", revs[1])
	}
}

// REV-16
func TestAgentReportedOnWriteAndUpdate(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()

	mem := &Memory{Title: "Runbook", Content: "step one", Taxonomy: "ops"}
	if err := mdb.Insert(mem, "alice", "claude-opus-5"); err != nil {
		t.Fatal(err)
	}
	updated, err := mdb.Update(mem.ID, map[string]any{"content": "step two"}, "alice", "qwen-2.5-72b", nil)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Version != 2 {
		t.Fatalf("version after update = %d, want 2", updated.Version)
	}

	revs, err := mdb.Revisions(mem.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(revs) != 2 {
		t.Fatalf("revisions = %d, want 2", len(revs))
	}
	// Newest first: the update's agent differs from the create's, proving each
	// revision carries its own, not a value copied from the memory row.
	if revs[0].Agent != "qwen-2.5-72b" {
		t.Errorf("latest revision agent = %q, want qwen-2.5-72b", revs[0].Agent)
	}
	if revs[1].Agent != "claude-opus-5" {
		t.Errorf("first revision agent = %q, want claude-opus-5", revs[1].Agent)
	}

	// Omitting agent (the web-UI-edit case) leaves it empty, not some distinct
	// "unknown" sentinel — same value across the field's entire domain.
	mem2 := &Memory{Title: "Human note", Content: "typed in the browser", Taxonomy: "ops"}
	if err := mdb.Insert(mem2, "alice", ""); err != nil {
		t.Fatal(err)
	}
	revs2, err := mdb.Revisions(mem2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(revs2) != 1 || revs2[0].Agent != "" {
		t.Errorf("human-authored revision = %+v, want empty agent", revs2)
	}
}

// TestUpdateConflictOnStaleBase is the lost-update case: two writers read v1,
// both edit, and the second must be refused rather than silently clobbering.
// REV-05
func TestUpdateConflictOnStaleBase(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()

	mem := &Memory{Title: "Runbook", Content: "original", Taxonomy: "ops"}
	if err := mdb.Insert(mem, "alice", ""); err != nil {
		t.Fatal(err)
	}
	base := mem.Version // both writers read version 1

	if _, err := mdb.Update(mem.ID, map[string]any{"content": "alice edit"}, "alice", "", &base); err != nil {
		t.Fatalf("first write should succeed: %v", err)
	}

	_, err := mdb.Update(mem.ID, map[string]any{"content": "bob edit"}, "bob", "", &base)
	var conflict *VersionConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("second write error = %v, want *VersionConflictError", err)
	}
	if conflict.Base != 1 || conflict.Current != 2 {
		t.Errorf("conflict = base %d current %d, want base 1 current 2", conflict.Base, conflict.Current)
	}

	// Alice's write must survive intact, and no v3 may have been recorded.
	got, err := mdb.Get(mem.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "alice edit" {
		t.Errorf("content = %q, want alice edit preserved", got.Content)
	}
	if got.Version != 2 {
		t.Errorf("version = %d, want 2 (rejected write must not bump)", got.Version)
	}
	revs, _ := mdb.Revisions(mem.ID)
	if len(revs) != 2 {
		t.Errorf("revisions = %d, want 2 (rejected write must not record)", len(revs))
	}
}

// REV-07
func TestUpdateNilBaseSkipsConflictCheck(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()

	mem := &Memory{Title: "Runbook", Content: "original", Taxonomy: "ops"}
	if err := mdb.Insert(mem, "alice", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := mdb.Update(mem.ID, map[string]any{"content": "first"}, "alice", "", nil); err != nil {
		t.Fatal(err)
	}
	// A nil base is last-writer-wins — no conflict even though the caller is
	// two versions behind — but the history still captures what it replaced.
	updated, err := mdb.Update(mem.ID, map[string]any{"content": "second"}, "bob", "", nil)
	if err != nil {
		t.Fatalf("nil base should not conflict: %v", err)
	}
	if updated.Version != 3 {
		t.Errorf("version = %d, want 3", updated.Version)
	}
	revs, _ := mdb.Revisions(mem.ID)
	if len(revs) != 3 {
		t.Errorf("revisions = %d, want 3", len(revs))
	}
}

// TestConcurrentUpdatesExactlyOneWins exercises the CAS under real concurrency:
// N writers all edit from the same base version, and the guarantee is that
// exactly one commits while the rest are told they are stale.
// REV-06
func TestConcurrentUpdatesExactlyOneWins(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()

	mem := &Memory{Title: "Runbook", Content: "original", Taxonomy: "ops"}
	if err := mdb.Insert(mem, "alice", ""); err != nil {
		t.Fatal(err)
	}

	const writers = 8
	base := mem.Version
	start := make(chan struct{})
	results := make(chan error, writers)
	var wg sync.WaitGroup
	for i := range writers {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			<-start
			b := base
			_, err := mdb.Update(mem.ID, map[string]any{
				"content": fmt.Sprintf("edit from writer %d", n),
			}, fmt.Sprintf("writer-%d", n), "", &b)
			results <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)

	var won, conflicted int
	for err := range results {
		var conflict *VersionConflictError
		switch {
		case err == nil:
			won++
		case errors.As(err, &conflict):
			conflicted++
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	if won != 1 {
		t.Errorf("successful writes = %d, want exactly 1", won)
	}
	if conflicted != writers-1 {
		t.Errorf("conflicts = %d, want %d", conflicted, writers-1)
	}

	// Exactly one edit landed, so the memory advanced by exactly one version.
	got, err := mdb.Get(mem.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 2 {
		t.Errorf("version = %d, want 2", got.Version)
	}
	revs, _ := mdb.Revisions(mem.ID)
	if len(revs) != 2 {
		t.Errorf("revisions = %d, want 2", len(revs))
	}
}

// REV-04
func TestRevisionAtReturnsRequestedVersion(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()

	mem := &Memory{Title: "Runbook", Content: "v1 body", Taxonomy: "ops"}
	if err := mdb.Insert(mem, "alice", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := mdb.Update(mem.ID, map[string]any{"content": "v2 body"}, "bob", "", nil); err != nil {
		t.Fatal(err)
	}

	rev, err := mdb.RevisionAt(mem.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if rev.Content != "v1 body" {
		t.Errorf("revision 1 content = %q, want v1 body", rev.Content)
	}
	if _, err := mdb.RevisionAt(mem.ID, 99); err == nil {
		t.Error("expected error for a version that does not exist")
	}
}

func TestUpdateMissingMemory(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()

	if _, err := mdb.Update("no-such-id", map[string]any{"title": "x"}, "alice", "", nil); err == nil {
		t.Error("expected error updating a nonexistent memory")
	}
}

// TestMigrateAddVersionColumnBackfill verifies a pre-versioning database gains
// the column on open and has a version-1 revision seeded for each existing row,
// so history is never empty for memories written before this feature.
// REV-13
func TestMigrateAddVersionColumnBackfill(t *testing.T) {
	f, err := os.CreateTemp("", "preversion-*.memory")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	defer os.Remove(f.Name())

	raw, err := sql.Open("sqlite", f.Name())
	if err != nil {
		t.Fatal(err)
	}
	_, err = raw.Exec(`
		CREATE TABLE memories (
			id TEXT PRIMARY KEY, title TEXT NOT NULL, content TEXT,
			taxonomy TEXT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
		INSERT INTO memories (id, title, content, taxonomy, created_at, updated_at)
		VALUES ('old1', 'Legacy', 'body', 'docs', '2020-01-01T00:00:00Z', '2020-01-02T00:00:00Z');`)
	if err != nil {
		t.Fatal(err)
	}
	raw.Close()

	mdb, err := OpenMemory(f.Name())
	if err != nil {
		t.Fatalf("open pre-version db: %v", err)
	}
	defer mdb.Close()

	got, err := mdb.Get("old1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 1 {
		t.Errorf("backfilled version = %d, want 1", got.Version)
	}

	revs, err := mdb.Revisions("old1")
	if err != nil {
		t.Fatal(err)
	}
	if len(revs) != 1 {
		t.Fatalf("backfilled revisions = %d, want 1", len(revs))
	}
	if revs[0].Content != "body" {
		t.Errorf("backfilled content = %q, want body", revs[0].Content)
	}
	// The pre-versioning writer is genuinely unknown, not attributable to anyone.
	if revs[0].AuthorID != "" {
		t.Errorf("backfilled author = %q, want empty", revs[0].AuthorID)
	}

	// A later edit must continue the series rather than collide with the seed.
	updated, err := mdb.Update("old1", map[string]any{"content": "revised"}, "alice", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Version != 2 {
		t.Errorf("version after post-migration edit = %d, want 2", updated.Version)
	}
}

// TestDeleteLeavesTombstoneAndBurnsAddress covers the core deletion contract:
// content and history are purged, but the address survives as an audit record
// and can never be read, written, or reused again.
// MEM-12
func TestDeleteLeavesTombstoneAndBurnsAddress(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()

	mem := &Memory{Title: "Runbook", Content: "secret steps", Taxonomy: "ops.deploy"}
	if err := mdb.Insert(mem, "alice", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := mdb.Update(mem.ID, map[string]any{"content": "revised"}, "alice", "", nil); err != nil {
		t.Fatal(err)
	}

	if err := mdb.Delete(mem.ID, "bob"); err != nil {
		t.Fatal(err)
	}

	// REV-15: the history is destroyed, not merely hidden.
	revs, err := mdb.Revisions(mem.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(revs) != 0 {
		t.Errorf("revisions after delete = %d, want 0 (purged)", len(revs))
	}

	// The tombstone records what went, who removed it, and how much history died.
	ts, err := mdb.Tombstone(mem.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ts == nil {
		t.Fatal("no tombstone recorded")
	}
	if ts.Title != "Runbook" || ts.Taxonomy != "ops.deploy" || ts.DeletedBy != "bob" {
		t.Errorf("tombstone = %+v, want Runbook/ops.deploy deleted by bob", ts)
	}
	if ts.FinalVersion != 2 {
		t.Errorf("final version = %d, want 2", ts.FinalVersion)
	}

	// Every route back to the address is closed, and each says "deleted".
	var deleted *DeletedError
	if _, err := mdb.Get(mem.ID); !errors.As(err, &deleted) {
		t.Errorf("Get after delete = %v, want *DeletedError", err)
	}
	if _, err := mdb.Update(mem.ID, map[string]any{"title": "revived"}, "bob", "", nil); !errors.As(err, &deleted) {
		t.Errorf("Update after delete = %v, want *DeletedError", err)
	}
	// MEM-13
	if err := mdb.Delete(mem.ID, "bob"); !errors.As(err, &deleted) {
		t.Errorf("re-Delete = %v, want *DeletedError", err)
	}

	// A never-used id stays a plain miss, so the two cases remain tellable apart.
	if _, err := mdb.Get("no-such-id"); errors.As(err, &deleted) {
		t.Error("unknown id must not report as deleted")
	}
}

// TestDeleteKeepsReferenceToTombstone verifies a deleted memory does not take
// its backlinks down with it: the linking memory still records what it meant to
// reference, now pointing at a tombstone rather than at nothing.
func TestDeleteKeepsReferenceToTombstone(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()

	target := &Memory{Title: "Target", Content: "body", Taxonomy: "ops"}
	if err := mdb.Insert(target, "alice", ""); err != nil {
		t.Fatal(err)
	}
	src := &Memory{Title: "Source", Content: "see [[Target]]", Taxonomy: "ops"}
	if err := mdb.Insert(src, "alice", ""); err != nil {
		t.Fatal(err)
	}
	if err := mdb.Delete(target.ID, "bob"); err != nil {
		t.Fatal(err)
	}

	// The source survives with its link text intact but unresolved.
	got, err := mdb.Get(src.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "see [[Target]]" {
		t.Errorf("source content = %q, want the link text preserved", got.Content)
	}
	// The reference survives the deletion and still names the dead target.
	links, err := mdb.Backlinks(FullScope(), target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 || links[0].ID != src.ID {
		t.Errorf("backlinks to tombstone = %+v, want the linking source", links)
	}

	// The graph shows it as a leaf tombstone, not a dangling ghost, so a reader
	// can tell "this was removed" from "this was never written".
	g, err := mdb.Graph(FullScope(), "", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	var grave *GraphNode
	for i := range g.Nodes {
		if g.Nodes[i].ID == target.ID {
			grave = &g.Nodes[i]
		}
	}
	if grave == nil {
		t.Fatal("deleted target missing from graph")
	}
	if !grave.Deleted || grave.Dangling {
		t.Errorf("node = %+v, want Deleted and not Dangling", *grave)
	}
	if grave.Title != "Target" || grave.DeletedBy != "bob" {
		t.Errorf("node = %+v, want title Target deleted by bob", *grave)
	}
}

// TestGraphDoesNotRouteThroughTombstone: a deleted memory is a leaf, so two
// memories that only ever shared it as a target must not become neighbours.
func TestGraphDoesNotRouteThroughTombstone(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()

	hub := &Memory{Title: "Hub", Content: "hub", Taxonomy: "ops"}
	if err := mdb.Insert(hub, "alice", ""); err != nil {
		t.Fatal(err)
	}
	left := &Memory{Title: "Left", Content: "see [[Hub]]", Taxonomy: "ops"}
	right := &Memory{Title: "Right", Content: "see [[Hub]]", Taxonomy: "ops"}
	for _, m := range []*Memory{left, right} {
		if err := mdb.Insert(m, "alice", ""); err != nil {
			t.Fatal(err)
		}
	}

	// While the hub lives, Right is two hops from Left.
	g, err := mdb.Graph(FullScope(), left.ID, 2, "")
	if err != nil {
		t.Fatal(err)
	}
	if !graphHasNode(g, right.ID) {
		t.Fatal("precondition: Right should be reachable through the live hub")
	}

	if err := mdb.Delete(hub.ID, "bob"); err != nil {
		t.Fatal(err)
	}
	g, err = mdb.Graph(FullScope(), left.ID, 2, "")
	if err != nil {
		t.Fatal(err)
	}
	if graphHasNode(g, right.ID) {
		t.Error("Right reachable through a deleted hub; tombstones must be leaves")
	}
	// The tombstone itself still hangs off Left as a visible dead end.
	if !graphHasNode(g, hub.ID) {
		t.Error("tombstone should still appear as a leaf off the linking memory")
	}
}

func graphHasNode(g *Graph, id string) bool {
	for _, n := range g.Nodes {
		if n.ID == id {
			return true
		}
	}
	return false
}

// MEM-15
func TestTombstonesScopedByTaxonomy(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()

	for _, m := range []*Memory{
		{Title: "A", Taxonomy: "ops.deploy"},
		{Title: "B", Taxonomy: "ops.oncall"},
		{Title: "C", Taxonomy: "finance"},
	} {
		if err := mdb.Insert(m, "alice", ""); err != nil {
			t.Fatal(err)
		}
		if err := mdb.Delete(m.ID, "alice"); err != nil {
			t.Fatal(err)
		}
	}

	all, err := mdb.Tombstones(FullScope(), "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Errorf("all tombstones = %d, want 3", len(all))
	}
	ops, err := mdb.Tombstones(FullScope(), "ops", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 2 {
		t.Errorf("ops tombstones = %d, want 2", len(ops))
	}
}
