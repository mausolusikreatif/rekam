package db

import "testing"

func TestScopePredicates(t *testing.T) {
	full := FullScope()
	if !full.CanRead("anything") || !full.CanWrite("x.y") || !full.TitleVisible("z") {
		t.Error("FullScope must permit everything")
	}
	if !full.Unrestricted() {
		t.Error("FullScope must report Unrestricted")
	}

	s := NewScope(
		[]string{"eng", "docs.public"}, // read
		[]string{"eng.notes"},          // write
		[]string{"finance"},            // titles only
	)
	// Read: prefix and exact match, dot-boundary respected.
	if !s.CanRead("eng") || !s.CanRead("eng.notes") || !s.CanRead("docs.public") {
		t.Error("readable prefixes must match themselves and descendants")
	}
	if s.CanRead("engineering") {
		t.Error("prefix must not match across a non-dot boundary (eng vs engineering)")
	}
	if s.CanRead("docs") || s.CanRead("finance") {
		t.Error("non-readable branches must not be readable")
	}
	// Write is independent of read and narrower here.
	if !s.CanWrite("eng.notes.deploy") {
		t.Error("write prefix must match descendants")
	}
	if s.CanWrite("eng") {
		t.Error("eng is readable but not writable")
	}
	// Title visibility = read ∪ titlesVisible.
	if !s.TitleVisible("eng") || !s.TitleVisible("finance.q3") {
		t.Error("title-visible = readable plus titles-only branches")
	}
	if s.TitleVisible("secret") {
		t.Error("an unlisted branch must not be title-visible")
	}
	// finance is title-visible but NOT readable — the core of the flag.
	if s.CanRead("finance") {
		t.Error("titles-only branch must not grant content read")
	}
}

func TestScopeZeroValueDeniesAll(t *testing.T) {
	var s Scope // zero value: not FullScope
	if s.Unrestricted() {
		t.Fatal("zero-value scope must not be unrestricted — fail closed")
	}
	if s.CanRead("anything") || s.CanWrite("anything") || s.TitleVisible("anything") {
		t.Error("zero-value scope must deny everything")
	}
	// And its SQL clause must be the never-true predicate, not empty.
	clause, args := s.readClause("taxonomy")
	if clause != "0" || args != nil {
		t.Errorf("zero-value readClause = %q/%v, want \"0\"/nil (deny)", clause, args)
	}
}

func TestScopeClauseSQL(t *testing.T) {
	// Unrestricted → no fragment at all.
	if c, a := FullScope().readClause("m.taxonomy"); c != "" || a != nil {
		t.Errorf("FullScope clause = %q/%v, want empty", c, a)
	}
	// Restricted → one (= OR LIKE) group per prefix, args paired.
	s := NewScope([]string{"a", "b"}, nil, nil)
	c, a := s.readClause("taxonomy")
	if c == "" {
		t.Fatal("restricted clause must be non-empty")
	}
	if len(a) != 4 {
		t.Errorf("args = %v, want 4 (two per prefix)", a)
	}
	if a[0] != "a" || a[1] != "a.%" || a[2] != "b" || a[3] != "b.%" {
		t.Errorf("args = %v, want [a a.%% b b.%%]", a)
	}
}
