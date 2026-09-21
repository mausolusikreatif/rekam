package engine

import (
	"errors"
	"os"
	"testing"

	"github.com/Ucok23/rekam/internal/db"
)

func setupEngine(t *testing.T) (*Engine, func()) {
	t.Helper()

	regFile, err := os.CreateTemp("", "registry-*.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	regFile.Close()

	memFile, err := os.CreateTemp("", "engine-*.memory")
	if err != nil {
		os.Remove(regFile.Name())
		t.Fatal(err)
	}
	memFile.Close()

	reg, err := db.OpenRegistry(regFile.Name())
	if err != nil {
		t.Fatal(err)
	}

	if _, err := reg.Create("alice", testKey, memFile.Name(), true); err != nil {
		t.Fatal(err)
	}

	eng := New(reg, 6000)
	cleanup := func() {
		reg.Close()
		os.Remove(regFile.Name())
		os.Remove(memFile.Name())
	}
	return eng, cleanup
}

const testKey = "test-key"

// Scenarios: spec/memory.md (MEM-01, MEM-02, MEM-03, MEM-04, MEM-07, MEM-12,
// MEM-22, MEM-23)
// Scenarios: spec/search-taxonomy.md (SRCH-01, SRCH-07)

// SRCH-07
func TestEngineWriteAndCatalog(t *testing.T) {
	eng, cleanup := setupEngine(t)
	defer cleanup()

	_, err := eng.WriteMemory(testKey, WriteInput{
		Title:    "Savings overview",
		Taxonomy: "finance.accounts",
	})
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	// Top level rolls up to the first segment and marks it expandable.
	top, err := eng.Catalog(testKey, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(top) != 1 || top[0].Path != "finance" || !top[0].Expandable {
		t.Errorf("unexpected top-level catalog: %v", top)
	}

	// Drilling into the prefix reveals the full leaf path.
	sub, err := eng.Catalog(testKey, "finance", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(sub) != 1 || sub[0].Path != "finance.accounts" || sub[0].Expandable {
		t.Errorf("unexpected drill-down catalog: %v", sub)
	}
}

// The storage ceiling is an abuse backstop (see maxTenantBytes's doc comment),
// not something a test should exercise by actually writing gigabytes of data —
// maxTenantBytes is a var specifically so this can shrink it instead.
func TestStorageCeilingBlocksWrites(t *testing.T) {
	eng, cleanup := setupEngine(t)
	defer cleanup()

	old := maxTenantBytes
	defer func() { maxTenantBytes = old }()
	maxTenantBytes = 1 // a fresh tenant file is already bigger than this

	_, err := eng.WriteMemory(testKey, WriteInput{Title: "Too big", Taxonomy: "ops"})
	var ae *ActionableError
	if !errors.As(err, &ae) || ae.Code != "storage_limit" {
		t.Fatalf("write over the storage ceiling: want storage_limit error, got %v", err)
	}

	// Raising it back (what an actual, generous ceiling looks like) lets
	// ordinary writes through again — this isn't a permanently wedged tenant.
	maxTenantBytes = old
	if _, err := eng.WriteMemory(testKey, WriteInput{Title: "Fine now", Taxonomy: "ops"}); err != nil {
		t.Errorf("write under the ceiling: %v", err)
	}
}

// SRCH-01
func TestEngineSearch(t *testing.T) {
	eng, cleanup := setupEngine(t)
	defer cleanup()

	if _, err := eng.WriteMemory(testKey, WriteInput{
		Title:    "monthly budget",
		Taxonomy: "finance.accounts",
		Content:  "Track monthly spending.",
	}); err != nil {
		t.Fatal(err)
	}

	res, err := eng.Search(testKey, "budget", "finance", 0)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(res.Results) != 1 {
		t.Errorf("expected 1 result, got %d", len(res.Results))
	}
}

// MEM-02, MEM-03
func TestEngineValidation(t *testing.T) {
	eng, cleanup := setupEngine(t)
	defer cleanup()

	// MEM-02
	_, err := eng.WriteMemory(testKey, WriteInput{Taxonomy: "finance"})
	if err == nil {
		t.Error("expected error for missing title")
	}

	// MEM-03
	_, err = eng.WriteMemory(testKey, WriteInput{Title: "x", Taxonomy: "bad taxonomy!"})
	if err == nil {
		t.Error("expected error for invalid taxonomy")
	}

	_, err = eng.WriteMemory("nope", WriteInput{Title: "x", Taxonomy: "finance"})
	if err == nil {
		t.Error("expected error for bad API key")
	}
}

// MEM-07
func TestEngineUpdate(t *testing.T) {
	eng, cleanup := setupEngine(t)
	defer cleanup()

	mem, _ := eng.WriteMemory(testKey, WriteInput{Title: "draft", Taxonomy: "work.projects"})
	updated, err := eng.UpdateMemory(testKey, mem.ID, UpdateInput{Title: strPtr("final")})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Title != "final" {
		t.Errorf("want 'final', got %s", updated.Title)
	}
}

// MEM-12
func TestEngineDelete(t *testing.T) {
	eng, cleanup := setupEngine(t)
	defer cleanup()

	mem, _ := eng.WriteMemory(testKey, WriteInput{Title: "temp", Taxonomy: "work"})
	if err := eng.DeleteMemory(testKey, mem.ID, false); err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, err := eng.GetMemory(testKey, mem.ID)
	if err == nil {
		t.Error("expected error after delete")
	}
}

// MEM-22: title collisions are rejected at write time, tenant-wide (not
// scoped to a taxonomy branch), since that's how [[link]] resolution
// actually works.
func TestEngineTitleConflict(t *testing.T) {
	eng, cleanup := setupEngine(t)
	defer cleanup()

	if _, err := eng.WriteMemory(testKey, WriteInput{Title: "Alpha", Taxonomy: "work"}); err != nil {
		t.Fatal(err)
	}

	// A second memory with the same title, even in a different branch, collides.
	_, err := eng.WriteMemory(testKey, WriteInput{Title: "  alpha  ", Taxonomy: "personal"})
	if ae, ok := err.(*ActionableError); !ok || ae.Code != "title_conflict" {
		t.Fatalf("create with colliding title = %v, want title_conflict", err)
	}

	// Renaming an unrelated memory onto the taken title also collides.
	other, err := eng.WriteMemory(testKey, WriteInput{Title: "Beta", Taxonomy: "work"})
	if err != nil {
		t.Fatal(err)
	}
	newTitle := "Alpha"
	_, err = eng.UpdateMemory(testKey, other.ID, UpdateInput{Title: &newTitle})
	if ae, ok := err.(*ActionableError); !ok || ae.Code != "title_conflict" {
		t.Fatalf("rename onto colliding title = %v, want title_conflict", err)
	}

	// Renaming a memory to its own current title (a no-op) is not a conflict.
	sameTitle := "Beta"
	if _, err := eng.UpdateMemory(testKey, other.ID, UpdateInput{Title: &sameTitle}); err != nil {
		t.Fatalf("renaming to its own title should not conflict: %v", err)
	}
}

// MEM-23: deleting a memory other memories link to is refused unless forced.
func TestEngineDeleteGuardsInboundLinks(t *testing.T) {
	eng, cleanup := setupEngine(t)
	defer cleanup()

	target, err := eng.WriteMemory(testKey, WriteInput{Title: "Target", Taxonomy: "work"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.WriteMemory(testKey, WriteInput{
		Title: "Linker", Taxonomy: "work", Content: "see [[Target]]",
	}); err != nil {
		t.Fatal(err)
	}

	err = eng.DeleteMemory(testKey, target.ID, false)
	ae, ok := err.(*ActionableError)
	if !ok || ae.Code != "has_inbound_links" {
		t.Fatalf("delete with inbound links = %v, want has_inbound_links", err)
	}

	// force=true deletes anyway.
	if err := eng.DeleteMemory(testKey, target.ID, true); err != nil {
		t.Fatalf("forced delete: %v", err)
	}
	if _, err := eng.GetMemory(testKey, target.ID); err == nil {
		t.Error("expected error after forced delete")
	}

	// A memory nothing links to deletes normally with no flag.
	lonely, err := eng.WriteMemory(testKey, WriteInput{Title: "Lonely", Taxonomy: "work"})
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.DeleteMemory(testKey, lonely.ID, false); err != nil {
		t.Fatalf("delete of an unlinked memory should not require force: %v", err)
	}
}

func TestEngineGetMemory(t *testing.T) {
	eng, cleanup := setupEngine(t)
	defer cleanup()

	mem, _ := eng.WriteMemory(testKey, WriteInput{Title: "note", Taxonomy: "work.projects"})
	got, err := eng.GetMemory(testKey, mem.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != mem.ID {
		t.Errorf("id mismatch: %s vs %s", got.ID, mem.ID)
	}
}

func TestGetOrOpenMem(t *testing.T) {
	eng, cleanup := setupEngine(t)
	defer cleanup()

	identity, _ := eng.registry.Resolve(testKey)
	memPath := identity.MemoryPath

	mdb, err := eng.GetOrOpenMem(memPath)
	if err != nil {
		t.Fatalf("GetOrOpenMem: %v", err)
	}
	if mdb == nil {
		t.Error("expected non-nil MemoryDB")
	}

	mdb2, err := eng.GetOrOpenMem(memPath)
	if err != nil {
		t.Fatal(err)
	}
	if mdb != mdb2 {
		t.Error("expected same cached MemoryDB instance from pool")
	}
}

func strPtr(s string) *string { return &s }

// MEM-04
func TestWriteMemoryFormat(t *testing.T) {
	eng, cleanup := setupEngine(t)
	defer cleanup()

	// Valid non-default format is stored and normalized (case-insensitive).
	mem, err := eng.WriteMemory(testKey, WriteInput{
		Title: "Diagram", Taxonomy: "docs", Content: "graph TD; A-->B", Format: "MERMAID",
	})
	if err != nil {
		t.Fatalf("write mermaid: %v", err)
	}
	if mem.Format != "mermaid" {
		t.Errorf("format = %q, want mermaid", mem.Format)
	}

	// MEM-04: unknown format is rejected with an actionable error.
	_, err = eng.WriteMemory(testKey, WriteInput{Title: "X", Taxonomy: "docs", Format: "pdf"})
	ae, ok := err.(*ActionableError)
	if !ok || ae.Code != "invalid_format" {
		t.Fatalf("expected invalid_format error, got %v", err)
	}

	// Update can change the format.
	updated, err := eng.UpdateMemory(testKey, mem.ID, UpdateInput{Format: strPtr("code")})
	if err != nil {
		t.Fatalf("update format: %v", err)
	}
	if updated.Format != "code" {
		t.Errorf("updated format = %q, want code", updated.Format)
	}
}
