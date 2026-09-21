package db

import "testing"

// Scenarios: spec/links-graph.md (LINK-16)

func TestSuggestLinks(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()

	// Two records about the same topic, unlinked — should be suggested.
	mustInsert(t, mdb,
		&Memory{Title: "Postgres Connection Pooling", Taxonomy: "work", Content: "tuning the postgres connection pool size"},
		&Memory{Title: "Database Pool Tuning", Taxonomy: "work", Content: "how to tune a postgres connection pool"},
		&Memory{Title: "Lunch Spots", Taxonomy: "personal", Content: "tacos and ramen near the office"},
	)

	got, err := mdb.SuggestLinks(FullScope(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatal("expected at least one suggestion")
	}
	// LINK-16
	// Top suggestion should pair the two database records, not the lunch note.
	top := got[0]
	pair := map[string]bool{top.Source.Title: true, top.Target.Title: true}
	if !pair["Postgres Connection Pooling"] || !pair["Database Pool Tuning"] {
		t.Errorf("top suggestion = %q + %q, want the two pooling records", top.Source.Title, top.Target.Title)
	}
}

// LINK-16
func TestSuggestLinksExcludesExistingEdges(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()

	target := &Memory{Title: "Database Pool Tuning", Taxonomy: "work", Content: "tune the postgres connection pool"}
	mustInsert(t, mdb, target)
	// This one already links to the target, so the pair must not be suggested.
	mustInsert(t, mdb, &Memory{
		Title: "Postgres Connection Pooling", Taxonomy: "work",
		Content: "postgres connection pool, see [[Database Pool Tuning]]",
	})

	got, err := mdb.SuggestLinks(FullScope(), 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range got {
		titles := map[string]bool{s.Source.Title: true, s.Target.Title: true}
		if titles["Database Pool Tuning"] && titles["Postgres Connection Pooling"] {
			t.Errorf("already-linked pair should be excluded, got %+v", s)
		}
	}
}
