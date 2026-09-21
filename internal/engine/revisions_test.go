package engine

import (
	"path/filepath"
	"testing"

	"github.com/Ucok23/rekam/internal/db"
)

// Scenarios: spec/revisions.md (REV-08, REV-09, REV-10, REV-11)
// Scenarios: spec/memory.md (MEM-11)

// identityID resolves the test key to its identity id, which is what the engine
// stamps on revisions as the author.
func identityID(t *testing.T, eng *Engine) string {
	t.Helper()
	identity, _, _, release, err := eng.resolve(testKey, false)
	defer release()
	if err != nil {
		t.Fatal(err)
	}
	return identity.ID
}

// REV-08
func TestEngineAttributesRevisionsToWriter(t *testing.T) {
	eng, cleanup := setupEngine(t)
	defer cleanup()

	mem, err := eng.WriteMemory(testKey, WriteInput{
		Title: "Deploy runbook", Content: "original", Taxonomy: "ops",
	})
	if err != nil {
		t.Fatal(err)
	}
	if mem.Version != 1 {
		t.Errorf("version = %d, want 1", mem.Version)
	}

	revs, err := eng.MemoryHistory(testKey, mem.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(revs) != 1 {
		t.Fatalf("revisions = %d, want 1", len(revs))
	}
	if want := identityID(t, eng); revs[0].AuthorID != want {
		t.Errorf("author = %q, want the writing identity %q", revs[0].AuthorID, want)
	}
}

// MEM-11
func TestEngineUpdateVersionConflict(t *testing.T) {
	eng, cleanup := setupEngine(t)
	defer cleanup()

	mem, err := eng.WriteMemory(testKey, WriteInput{
		Title: "Deploy runbook", Content: "original", Taxonomy: "ops",
	})
	if err != nil {
		t.Fatal(err)
	}
	stale := mem.Version

	newContent := "first edit"
	if _, err := eng.UpdateMemory(testKey, mem.ID, UpdateInput{
		Content: &newContent, BaseVersion: &stale,
	}); err != nil {
		t.Fatalf("first update: %v", err)
	}

	second := "second edit"
	_, err = eng.UpdateMemory(testKey, mem.ID, UpdateInput{
		Content: &second, BaseVersion: &stale,
	})
	ae, ok := err.(*ActionableError)
	if !ok {
		t.Fatalf("error = %T (%v), want *ActionableError", err, err)
	}
	if ae.Code != "version_conflict" {
		t.Errorf("code = %q, want version_conflict", ae.Code)
	}
	// The client needs the current version to re-read and retry in one hop.
	ctx, ok := ae.Context.(map[string]any)
	if !ok {
		t.Fatalf("context = %T, want map[string]any", ae.Context)
	}
	if ctx["current_version"] != 2 {
		t.Errorf("context current_version = %v, want 2", ctx["current_version"])
	}
}

// REV-09
func TestEngineRestoreRevision(t *testing.T) {
	eng, cleanup := setupEngine(t)
	defer cleanup()

	mem, err := eng.WriteMemory(testKey, WriteInput{
		Title: "Deploy runbook", Content: "original", Taxonomy: "ops",
	})
	if err != nil {
		t.Fatal(err)
	}
	replaced := "rewritten"
	if _, err := eng.UpdateMemory(testKey, mem.ID, UpdateInput{Content: &replaced}); err != nil {
		t.Fatal(err)
	}

	restored, err := eng.RestoreRevision(testKey, mem.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Content != "original" {
		t.Errorf("content = %q, want original", restored.Content)
	}
	// History is append-only: restoring moves forward to v3, it does not rewind.
	if restored.Version != 3 {
		t.Errorf("version = %d, want 3", restored.Version)
	}
	revs, err := eng.MemoryHistory(testKey, mem.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(revs) != 3 {
		t.Errorf("revisions = %d, want 3", len(revs))
	}
	// The rewrite that was rolled back is itself still recoverable.
	if revs[1].Content != "rewritten" {
		t.Errorf("revision 2 = %q, want the rewritten text retained", revs[1].Content)
	}

	// REV-10
	if _, err := eng.RestoreRevision(testKey, mem.ID, 99); err == nil {
		t.Error("expected error restoring a version that does not exist")
	}
}

func TestEngineMemoryHistoryUnknownMemory(t *testing.T) {
	eng, cleanup := setupEngine(t)
	defer cleanup()

	if _, err := eng.MemoryHistory(testKey, "no-such-id"); err == nil {
		t.Error("expected error for unknown memory")
	}
}

// REV-11: a viewer can read a memory's history but cannot restore an earlier
// version — restoring is a write, gated exactly like any other edit.
func TestEngineRestoreRequiresWriteScope(t *testing.T) {
	eng, aliceKey, dir, cleanup := setupMultiTeam(t)
	defer cleanup()

	team, err := eng.CreateTeam(aliceKey, "Acme", filepath.Join(dir, "acme.rekam"))
	if err != nil {
		t.Fatal(err)
	}
	bumpSeats(t, eng, team.ID)
	aliceTok := teamToken(eng, idOf(t, eng, aliceKey), team.ID)

	mem, err := eng.WriteMemory(aliceTok, WriteInput{
		Title: "Runbook", Content: "original", Taxonomy: "product",
	})
	if err != nil {
		t.Fatal(err)
	}
	replaced := "rewritten"
	if _, err := eng.UpdateMemory(aliceTok, mem.ID, UpdateInput{Content: &replaced}); err != nil {
		t.Fatal(err)
	}

	bob, _, err := eng.registry.CreateUser("Bob", "bob@acme.test", "password123",
		filepath.Join(dir, "bob.rekam"), true, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.SetMember(aliceTok, bob.ID, db.RoleViewer, []string{"product"}, nil); err != nil {
		t.Fatal(err)
	}
	bobTok := teamToken(eng, bob.ID, team.ID)

	// The viewer can still read the history.
	if _, err := eng.MemoryHistory(bobTok, mem.ID); err != nil {
		t.Fatalf("viewer should be able to read history: %v", err)
	}

	// But restoring is a write, and a viewer role permits none at all.
	_, err = eng.RestoreRevision(bobTok, mem.ID, 1)
	ae, ok := err.(*ActionableError)
	if !ok || ae.Code != "write_not_allowed" {
		t.Fatalf("restore by a viewer: err = %v, want write_not_allowed", err)
	}
}

// A history of bare uuids answers "what changed" but not "who changed it", so
// each row is resolved to the author's display identity.
func TestEngineResolvesRevisionAuthorNames(t *testing.T) {
	eng, cleanup := setupEngine(t)
	defer cleanup()

	mem, err := eng.WriteMemory(testKey, WriteInput{
		Title: "Deploy runbook", Content: "original", Taxonomy: "ops",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.UpdateMemory(testKey, mem.ID, UpdateInput{Content: strPtr("revised")}); err != nil {
		t.Fatal(err)
	}

	revs, err := eng.MemoryHistory(testKey, mem.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(revs) != 2 {
		t.Fatalf("want 2 revisions, got %d", len(revs))
	}
	for _, rev := range revs {
		if rev.AuthorName != "alice" {
			t.Errorf("v%d author_name = %q, want %q", rev.Version, rev.AuthorName, "alice")
		}
		if rev.AuthorID == "" {
			t.Errorf("v%d lost its author_id; the name is added, not a replacement", rev.Version)
		}
	}
}

// An account removed after it wrote something must still appear in the history
// — a change made by someone since deleted is exactly what an audit needs to
// see. It keeps its id and simply carries no name.
func TestEngineRevisionKeepsDeletedAuthor(t *testing.T) {
	eng, cleanup := setupEngine(t)
	defer cleanup()

	mem, err := eng.WriteMemory(testKey, WriteInput{
		Title: "Deploy runbook", Content: "original", Taxonomy: "ops",
	})
	if err != nil {
		t.Fatal(err)
	}
	author := identityID(t, eng)

	// A second identity sharing the same corpus, so history stays readable
	// after the original author's account is gone.
	if _, err := eng.registry.Create("bob", "bob-key", memoryPathOf(t, eng), true); err != nil {
		t.Fatal(err)
	}
	if err := eng.registry.Delete(author); err != nil {
		t.Fatal(err)
	}

	revs, err := eng.MemoryHistory("bob-key", mem.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(revs) != 1 {
		t.Fatalf("want 1 revision, got %d", len(revs))
	}
	if revs[0].AuthorID != author {
		t.Errorf("author_id = %q, want the deleted author's id %q", revs[0].AuthorID, author)
	}
	if revs[0].AuthorName != "" {
		t.Errorf("author_name = %q, want empty for an account the registry no longer knows", revs[0].AuthorName)
	}
}

// memoryPathOf reports the corpus file the test identity is bound to, so a
// second identity can be pointed at the same one.
func memoryPathOf(t *testing.T, eng *Engine) string {
	t.Helper()
	identity, err := eng.registry.Resolve(testKey)
	if err != nil {
		t.Fatal(err)
	}
	return identity.MemoryPath
}
