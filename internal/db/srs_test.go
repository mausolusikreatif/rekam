package db

import (
	"testing"
	"time"
)

// Scenarios: spec/review.md (RVW-01..06, RVW-09)

// RVW-01
func TestAddAndDueReview(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()

	mem := &Memory{Title: "Capital of France", Taxonomy: "trivia", Content: "Paris"}
	mustInsert(t, mdb, mem)

	if _, err := mdb.AddToReview(mem.ID); err != nil {
		t.Fatal(err)
	}
	// Freshly added cards are due immediately.
	due, err := mdb.DueReviews(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].ID != mem.ID {
		t.Fatalf("expected 1 due card for %s, got %+v", mem.ID, due)
	}
	if due[0].Content != "Paris" {
		t.Errorf("due card should carry content for the answer, got %q", due[0].Content)
	}

	stats, err := mdb.ReviewStats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.Total != 1 || stats.Due != 1 {
		t.Errorf("stats = %+v, want total 1 due 1", stats)
	}
}

// RVW-02
func TestAddToReviewIdempotent(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()
	mem := &Memory{Title: "X", Taxonomy: "t", Content: "y"}
	mustInsert(t, mdb, mem)

	if _, err := mdb.AddToReview(mem.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := mdb.Grade(mem.ID, 5); err != nil {
		t.Fatal(err)
	}
	// Re-adding must not reset an existing schedule.
	if _, err := mdb.AddToReview(mem.ID); err != nil {
		t.Fatal(err)
	}
	st, err := mdb.srsState(mem.ID)
	if err != nil {
		t.Fatal(err)
	}
	if st.Reps != 1 {
		t.Errorf("re-add reset schedule: reps = %d, want 1", st.Reps)
	}
}

// RVW-03
func TestGradeSchedulesForward(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()
	mem := &Memory{Title: "X", Taxonomy: "t", Content: "y"}
	mustInsert(t, mdb, mem)
	mustAdd(t, mdb, mem.ID)

	// First good grade → 1 day interval; card no longer due now.
	st, err := mdb.Grade(mem.ID, 4)
	if err != nil {
		t.Fatal(err)
	}
	if st.Reps != 1 || st.IntervalDays != 1 {
		t.Errorf("after first good grade: reps=%d interval=%d, want 1/1", st.Reps, st.IntervalDays)
	}
	due, _ := mdb.DueReviews(0)
	if len(due) != 0 {
		t.Errorf("card scheduled forward should not be due now, got %d due", len(due))
	}

	// Second good grade → 6 day interval.
	st, _ = mdb.Grade(mem.ID, 4)
	if st.IntervalDays != 6 {
		t.Errorf("second interval = %d, want 6", st.IntervalDays)
	}
	// Third → interval * ease.
	st, _ = mdb.Grade(mem.ID, 5)
	if st.IntervalDays <= 6 {
		t.Errorf("third interval = %d, want > 6", st.IntervalDays)
	}
}

// RVW-04
func TestGradeLapseResets(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()
	mem := &Memory{Title: "X", Taxonomy: "t", Content: "y"}
	mustInsert(t, mdb, mem)
	mustAdd(t, mdb, mem.ID)

	mdb.Grade(mem.ID, 4)
	mdb.Grade(mem.ID, 4)
	// A lapse resets reps and makes the card due again immediately.
	st, err := mdb.Grade(mem.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if st.Reps != 0 || st.IntervalDays != 0 {
		t.Errorf("lapse: reps=%d interval=%d, want 0/0", st.Reps, st.IntervalDays)
	}
	dueAt, _ := time.Parse(time.RFC3339, st.DueAt)
	if dueAt.After(time.Now().UTC().Add(time.Minute)) {
		t.Errorf("lapsed card should be due now, due_at = %s", st.DueAt)
	}
	due, _ := mdb.DueReviews(0)
	if len(due) != 1 {
		t.Errorf("lapsed card should reappear, got %d due", len(due))
	}
}

// RVW-05
func TestGradeEaseFloor(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()
	mem := &Memory{Title: "X", Taxonomy: "t", Content: "y"}
	mustInsert(t, mdb, mem)
	mustAdd(t, mdb, mem.ID)

	var st *SRSState
	for range 10 {
		st, _ = mdb.Grade(mem.ID, 3) // minimum passing grade repeatedly drives ease down
	}
	if st.Ease < minEase-1e-9 {
		t.Errorf("ease = %f, must not drop below floor %f", st.Ease, minEase)
	}
}

// RVW-06
func TestRemoveFromReview(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()
	mem := &Memory{Title: "X", Taxonomy: "t", Content: "y"}
	mustInsert(t, mdb, mem)
	mustAdd(t, mdb, mem.ID)

	if in, _ := mdb.InReview(mem.ID); !in {
		t.Fatal("should be in review after add")
	}
	if err := mdb.RemoveFromReview(mem.ID); err != nil {
		t.Fatal(err)
	}
	if in, _ := mdb.InReview(mem.ID); in {
		t.Error("should not be in review after remove")
	}
}

// RVW-09
func TestGradeNotEnrolled(t *testing.T) {
	mdb, cleanup := openTestMemory(t)
	defer cleanup()
	mem := &Memory{Title: "X", Taxonomy: "t", Content: "y"}
	mustInsert(t, mdb, mem)
	if _, err := mdb.Grade(mem.ID, 4); err == nil {
		t.Error("expected error grading a memory not in the deck")
	}
}

func mustAdd(t *testing.T, mdb *MemoryDB, id string) {
	t.Helper()
	if _, err := mdb.AddToReview(id); err != nil {
		t.Fatal(err)
	}
}
