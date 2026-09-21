package api

import (
	"net/http"
	"testing"
)

// Scenarios: spec/review.md (RVW-01..09, RVW-11)
//
// RVW-10 needs a second workspace, so it lives in review_team_test.go.

type srsState struct {
	MemoryID     string  `json:"memory_id"`
	Ease         float64 `json:"ease"`
	IntervalDays int     `json:"interval_days"`
	Reps         int     `json:"reps"`
	DueAt        string  `json:"due_at"`
}

type dueResponse struct {
	Cards []struct {
		ID           string `json:"id"`
		Title        string `json:"title"`
		IntervalDays int    `json:"interval_days"`
		Reps         int    `json:"reps"`
	} `json:"cards"`
	Stats struct {
		Total int `json:"total"`
		Due   int `json:"due"`
	} `json:"stats"`
}

func dueNow(t *testing.T, srv *Server, key string) dueResponse {
	t.Helper()
	rr := doRequest(t, srv, "GET", "/review", key, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("review due: %d: %s", rr.Code, rr.Body)
	}
	var out dueResponse
	decodeJSON(t, rr, &out)
	return out
}

// writeMemoryID writes a record and returns its id.
func writeMemoryID(t *testing.T, srv *Server, key, title, taxonomy, content string) string {
	t.Helper()
	rr := doRequest(t, srv, "POST", "/memory", key, map[string]any{
		"title": title, "taxonomy": taxonomy, "content": content,
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("write %q: %d: %s", title, rr.Code, rr.Body)
	}
	var mem struct {
		ID string `json:"id"`
	}
	decodeJSON(t, rr, &mem)
	return mem.ID
}

// The point of a review deck is that material you keep getting right comes back
// less and less often. Each confident recall must push the card further out,
// otherwise the deck never stops nagging and the user abandons it.
func TestRecallingWellPushesACardFurtherOut(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	id := writeMemoryID(t, srv, key, "Capital of France", "trivia", "Paris")
	// RVW-01
	doRequest(t, srv, "POST", "/review/"+id, key, nil)

	var intervals []int
	for range 3 {
		rr := doRequest(t, srv, "POST", "/review/"+id+"/grade", key, map[string]any{"grade": 5})
		if rr.Code != http.StatusOK {
			t.Fatalf("grade: %d: %s", rr.Code, rr.Body)
		}
		var st srsState
		decodeJSON(t, rr, &st)
		intervals = append(intervals, st.IntervalDays)
	}

	// RVW-03
	for i := 1; i < len(intervals); i++ {
		if intervals[i] <= intervals[i-1] {
			t.Errorf("each confident recall should lengthen the gap, got %v", intervals)
			break
		}
	}
	// And the card is not sitting in today's queue any more.
	if due := dueNow(t, srv, key); due.Stats.Due != 0 || due.Stats.Total != 1 {
		t.Errorf("a scheduled card should stay in the deck but not be due: %+v", due.Stats)
	}
}

// Forgetting is the case the deck exists for: a failed recall must bring the
// card straight back rather than scheduling it away.
func TestForgettingACardBringsItStraightBack(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	id := writeMemoryID(t, srv, key, "Capital of France", "trivia", "Paris")
	doRequest(t, srv, "POST", "/review/"+id, key, nil)

	// Build up a comfortable interval first.
	for range 3 {
		doRequest(t, srv, "POST", "/review/"+id+"/grade", key, map[string]any{"grade": 5})
	}
	var confident srsState
	decodeJSON(t, doRequest(t, srv, "POST", "/review/"+id+"/grade", key, map[string]any{"grade": 5}), &confident)

	// Then blank on it.
	rr := doRequest(t, srv, "POST", "/review/"+id+"/grade", key, map[string]any{"grade": 1})
	if rr.Code != http.StatusOK {
		t.Fatalf("failing grade: %d: %s", rr.Code, rr.Body)
	}
	var lapsed srsState
	decodeJSON(t, rr, &lapsed)

	// RVW-04
	if lapsed.IntervalDays >= confident.IntervalDays {
		t.Errorf("a lapse must shorten the interval: %d → %d", confident.IntervalDays, lapsed.IntervalDays)
	}
	if lapsed.Ease > confident.Ease {
		t.Errorf("a lapse should not make the card easier: %v → %v", confident.Ease, lapsed.Ease)
	}
	if due := dueNow(t, srv, key); due.Stats.Due != 1 {
		t.Errorf("a forgotten card should be due again now, got %+v", due.Stats)
	}
}

// Enrolling the same record twice must not reset the schedule the user has
// already built up — a stray double-click should cost nothing.
func TestReEnrollingKeepsTheExistingSchedule(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	id := writeMemoryID(t, srv, key, "Capital of France", "trivia", "Paris")
	doRequest(t, srv, "POST", "/review/"+id, key, nil)
	var graded srsState
	decodeJSON(t, doRequest(t, srv, "POST", "/review/"+id+"/grade", key, map[string]any{"grade": 5}), &graded)

	rr := doRequest(t, srv, "POST", "/review/"+id, key, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("re-enroll: %d: %s", rr.Code, rr.Body)
	}
	var again srsState
	decodeJSON(t, rr, &again)
	// RVW-02
	if again.Reps != graded.Reps || again.IntervalDays != graded.IntervalDays {
		t.Errorf("re-enrolling reset the schedule: %+v → %+v", graded, again)
	}
	if due := dueNow(t, srv, key); due.Stats.Total != 1 {
		t.Errorf("re-enrolling should not duplicate the card, got %+v", due.Stats)
	}
}

// Dropping a card takes it out of the rotation entirely, leaving the record
// itself alone.
func TestDroppingACardLeavesTheRecordAlone(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	id := writeMemoryID(t, srv, key, "Capital of France", "trivia", "Paris")
	doRequest(t, srv, "POST", "/review/"+id, key, nil)

	if rr := doRequest(t, srv, "DELETE", "/review/"+id, key, nil); rr.Code != http.StatusOK {
		t.Fatalf("remove: %d: %s", rr.Code, rr.Body)
	}
	// RVW-06
	if due := dueNow(t, srv, key); due.Stats.Total != 0 {
		t.Errorf("the deck should be empty after dropping the card, got %+v", due.Stats)
	}
	// The record is untouched.
	if rr := doRequest(t, srv, "GET", "/memory/"+id, key, nil); rr.Code != http.StatusOK {
		t.Errorf("dropping a card must not delete the record, got %d", rr.Code)
	}
	// Grading a card that is no longer enrolled is an error, not a silent re-add.
	if rr := doRequest(t, srv, "POST", "/review/"+id+"/grade", key, map[string]any{"grade": 5}); rr.Code < 400 {
		t.Errorf("grading a dropped card should fail, got %d: %s", rr.Code, rr.Body)
	}
}

// The deck header drives the UI badge, so it must count the whole deck and
// today's queue separately.
func TestDeckStatsSeparateTotalFromDue(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	ids := []string{
		writeMemoryID(t, srv, key, "One", "trivia", "1"),
		writeMemoryID(t, srv, key, "Two", "trivia", "2"),
		writeMemoryID(t, srv, key, "Three", "trivia", "3"),
	}
	for _, id := range ids {
		doRequest(t, srv, "POST", "/review/"+id, key, nil)
	}
	// RVW-07
	if due := dueNow(t, srv, key); due.Stats.Total != 3 || due.Stats.Due != 3 {
		t.Fatalf("freshly enrolled cards should all be due: %+v", due.Stats)
	}

	// Answer one well; it leaves today's queue but stays in the deck.
	doRequest(t, srv, "POST", "/review/"+ids[0]+"/grade", key, map[string]any{"grade": 5})
	due := dueNow(t, srv, key)
	// RVW-07
	if due.Stats.Total != 3 || due.Stats.Due != 2 {
		t.Errorf("expected 3 in the deck with 2 due, got %+v", due.Stats)
	}
	if len(due.Cards) != 2 {
		t.Errorf("the queue should hand back only the due cards, got %d", len(due.Cards))
	}
	// Cards come back with enough to review them without a second fetch.
	for _, c := range due.Cards {
		if c.Title == "" {
			t.Errorf("a review card needs its title: %+v", c)
		}
	}

	// A limit caps the session without changing the deck's true size.
	var capped dueResponse
	decodeJSON(t, doRequest(t, srv, "GET", "/review?limit=1", key, nil), &capped)
	if len(capped.Cards) != 1 || capped.Stats.Total != 3 {
		t.Errorf("limit should cap the batch, not the stats: %d cards, %+v", len(capped.Cards), capped.Stats)
	}
	if rr := doRequest(t, srv, "GET", "/review?limit=-1", key, nil); rr.Code != http.StatusBadRequest {
		t.Errorf("a negative limit should be rejected, got %d", rr.Code)
	}
}

// Grades outside the scale, and cards that do not exist, must be refused with an
// explanation rather than corrupting a schedule.
func TestReviewRejectsNonsenseInput(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	id := writeMemoryID(t, srv, key, "Capital of France", "trivia", "Paris")
	doRequest(t, srv, "POST", "/review/"+id, key, nil)

	// RVW-08
	for _, grade := range []any{-1, 6, 99} {
		if rr := doRequest(t, srv, "POST", "/review/"+id+"/grade", key, map[string]any{"grade": grade}); rr.Code < 400 {
			t.Errorf("grade %v should be refused, got %d", grade, rr.Code)
		}
	}
	// RVW-08
	// A non-numeric grade is a bad request, not a zero.
	if rr := doRequest(t, srv, "POST", "/review/"+id+"/grade", key, map[string]any{"grade": "five"}); rr.Code != http.StatusBadRequest {
		t.Errorf("a non-numeric grade should be 400, got %d", rr.Code)
	}
	// The schedule survived all of that.
	if due := dueNow(t, srv, key); due.Stats.Total != 1 {
		t.Errorf("rejected input must not disturb the deck, got %+v", due.Stats)
	}

	// RVW-09
	// Enrolling something that does not exist is a not-found, not a phantom card.
	if rr := doRequest(t, srv, "POST", "/review/no-such-record", key, nil); rr.Code != http.StatusNotFound {
		t.Errorf("enrolling an unknown record: want 404, got %d: %s", rr.Code, rr.Body)
	}
}

// Review routes are credentialed like everything else.
func TestReviewRoutesRejectAnonymousCallers(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()
	id := writeMemoryID(t, srv, key, "Capital of France", "trivia", "Paris")

	routes := []struct {
		method, path string
		body         any
	}{
		{"GET", "/review", nil},
		{"POST", "/review/" + id, nil},
		{"DELETE", "/review/" + id, nil},
		{"POST", "/review/" + id + "/grade", map[string]any{"grade": 5}},
	}
	// RVW-11
	for _, rt := range routes {
		if rr := doRequest(t, srv, rt.method, rt.path, "", rt.body); rr.Code != http.StatusUnauthorized {
			t.Errorf("%s %s: want 401, got %d: %s", rt.method, rt.path, rr.Code, rr.Body)
		}
	}
}
