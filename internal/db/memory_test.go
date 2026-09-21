package db

import (
	"database/sql"
	"os"
	"testing"
)

// Scenarios: spec/memory.md (MEM-01, MEM-04..MEM-06, MEM-07, MEM-12)
// Scenarios: spec/search-taxonomy.md (SRCH-01, SRCH-02, SRCH-04, SRCH-06)

func TestFormatRoundTrip(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()

	mem := &Memory{Title: "Flow", Content: "graph TD; A-->B", Taxonomy: "docs", Format: "mermaid"}
	if err := mdb.Insert(mem, "", ""); err != nil {
		t.Fatal(err)
	}
	got, err := mdb.Get(mem.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Format != "mermaid" {
		t.Errorf("format = %q, want mermaid", got.Format)
	}

	// MEM-04: empty format defaults to markdown at the storage layer.
	plain := &Memory{Title: "Note", Content: "hi", Taxonomy: "docs"}
	if err := mdb.Insert(plain, "", ""); err != nil {
		t.Fatal(err)
	}
	got2, _ := mdb.Get(plain.ID)
	if got2.Format != "markdown" {
		t.Errorf("default format = %q, want markdown", got2.Format)
	}
}

// TestDiskSize is a real mechanical check (not the storage-ceiling policy
// itself — see engine.checkStorageCeiling and its own test, which shrinks the
// ceiling instead of writing gigabytes) that DiskSize actually stats the
// file: nonzero for a just-migrated database, and never shrinks after a write
// (WAL mode means it may grow via the "-wal" sidecar rather than the main
// file, which is exactly why DiskSize sums both instead of stat-ing one).
func TestDiskSize(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()

	before, err := mdb.DiskSize()
	if err != nil {
		t.Fatal(err)
	}
	if before == 0 {
		t.Error("a just-migrated database should already occupy some bytes on disk")
	}

	if err := mdb.Insert(&Memory{Title: "Note", Content: "hello", Taxonomy: "ops"}, "", ""); err != nil {
		t.Fatal(err)
	}
	after, err := mdb.DiskSize()
	if err != nil {
		t.Fatal(err)
	}
	if after < before {
		t.Errorf("DiskSize after a write = %d, want >= %d (before)", after, before)
	}
}

// TestMigrateAddFormatColumnBackfill verifies a pre-format database gains the
// column on open, with existing rows defaulting to markdown.
func TestMigrateAddFormatColumnBackfill(t *testing.T) {
	f, err := os.CreateTemp("", "legacy-*.memory")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	defer os.Remove(f.Name())

	// Build a legacy memories table without the format column.
	raw, err := sql.Open("sqlite", f.Name())
	if err != nil {
		t.Fatal(err)
	}
	_, err = raw.Exec(`
		CREATE TABLE memories (
			id TEXT PRIMARY KEY, title TEXT NOT NULL, content TEXT,
			taxonomy TEXT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
		INSERT INTO memories (id, title, content, taxonomy, created_at, updated_at)
		VALUES ('old1', 'Legacy', 'body', 'docs', '2020-01-01T00:00:00Z', '2020-01-01T00:00:00Z');`)
	if err != nil {
		t.Fatal(err)
	}
	raw.Close()

	mdb, err := OpenMemory(f.Name()) // runs migrations, incl. ADD COLUMN format
	if err != nil {
		t.Fatalf("open legacy: %v", err)
	}
	defer mdb.Close()

	got, err := mdb.Get("old1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Format != "markdown" {
		t.Errorf("backfilled format = %q, want markdown", got.Format)
	}
}

func openTestMemory(t *testing.T) (*MemoryDB, func()) {
	t.Helper()
	f, err := os.CreateTemp("", "memory-*.memory")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	mdb, err := OpenMemory(f.Name())
	if err != nil {
		os.Remove(f.Name())
		t.Fatalf("open: %v", err)
	}
	return mdb, func() {
		mdb.Close()
		os.Remove(f.Name())
	}
}

func TestMemoryInsertGet(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()

	mem := &Memory{
		Title:    "Budget 2025",
		Taxonomy: "finance.accounts",
	}
	// MEM-01
	if err := mdb.Insert(mem, "", ""); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if mem.ID == "" {
		t.Error("expected ID to be set after insert")
	}

	// MEM-05
	got, err := mdb.Get(mem.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Title != mem.Title {
		t.Errorf("title: want %q got %q", mem.Title, got.Title)
	}
	if got.Taxonomy != "finance.accounts" {
		t.Errorf("taxonomy: %q", got.Taxonomy)
	}
}

func TestMemoryGetNotFound(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()

	// MEM-06
	_, err := mdb.Get("00000000-0000-0000-0000-000000000000")
	if err == nil {
		t.Error("expected error for missing id")
	}
}

func TestMemoryCatalog(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()

	for _, mem := range []*Memory{
		{Title: "A", Taxonomy: "finance.accounts"},
		{Title: "B", Taxonomy: "finance.transactions"},
		{Title: "C", Taxonomy: "work.projects"},
		{Title: "D", Taxonomy: "finance.accounts"},
	} {
		if err := mdb.Insert(mem, "", ""); err != nil {
			t.Fatal(err)
		}
	}

	// SRCH-06
	paths, err := mdb.Catalog(FullScope())
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 3 {
		t.Errorf("want 3 catalog entries, got %d: %v", len(paths), paths)
	}
}

func TestMemorySearch(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()

	for _, mem := range []*Memory{
		{Title: "savings account balance", Content: "savings bank details", Taxonomy: "finance.accounts"},
		{Title: "groceries expense", Content: "food shopping list", Taxonomy: "finance.transactions"},
		{Title: "project alpha kickoff", Content: "kickoff meeting notes", Taxonomy: "work.projects"},
	} {
		if err := mdb.Insert(mem, "", ""); err != nil {
			t.Fatal(err)
		}
	}

	// SRCH-01, SRCH-02
	res, err := mdb.Search(FullScope(), "savings", "finance", 0)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(res.Results) != 1 {
		t.Errorf("want 1 result, got %d", len(res.Results))
	}

	res, err = mdb.Search(FullScope(), "kickoff", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Results) != 1 {
		t.Errorf("want 1 result, got %d", len(res.Results))
	}

	// SRCH-04
	res, err = mdb.Search(FullScope(), "finance OR savings OR groceries OR expense", "finance", 1)
	if err != nil {
		t.Fatal(err)
	}
	if res.OmittedCount == 0 && len(res.Results) > 1 {
		t.Errorf("expected budget to truncate results, omitted=%d results=%d", res.OmittedCount, len(res.Results))
	}
}

// MEM-07
func TestMemoryUpdate(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()

	mem := &Memory{Title: "Draft note", Taxonomy: "work.projects"}
	if err := mdb.Insert(mem, "", ""); err != nil {
		t.Fatal(err)
	}

	updated, err := mdb.Update(mem.ID, map[string]any{"title": "Final note"}, "", "", nil)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Title != "Final note" {
		t.Errorf("want 'Final note', got %s", updated.Title)
	}
}

// MEM-12
func TestMemoryDelete(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()

	mem := &Memory{Title: "To delete", Taxonomy: "temp"}
	if err := mdb.Insert(mem, "", ""); err != nil {
		t.Fatal(err)
	}

	if err := mdb.Delete(mem.ID, ""); err != nil {
		t.Fatalf("delete: %v", err)
	}

	_, err := mdb.Get(mem.ID)
	if err == nil {
		t.Error("expected error after delete")
	}
}

func TestTokenCounting(t *testing.T) {
	if CountTokens("") != 0 {
		t.Error("empty string should be 0 tokens")
	}
	got := CountTokens("hello world test")
	if got < 1 || got > 10 {
		t.Errorf("unexpected token count for short string: %d", got)
	}
}
