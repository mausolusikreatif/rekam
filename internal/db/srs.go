package db

import (
	"database/sql"
	"fmt"
	"math"
	"time"
)

// SRS (spaced repetition) turns memories into a review deck. A memory is added to
// the deck explicitly; reviews are scheduled with the SM-2 algorithm so well-recalled
// records resurface less often and shaky ones come back soon. This makes rekam an
// active memory aid, not just storage.

// minEase is the SM-2 floor; below it intervals collapse and a card churns forever.
const minEase = 1.3

// ReviewCard is a memory plus its scheduling state, returned for the review queue.
type ReviewCard struct {
	*Memory
	Ease         float64 `json:"ease"`
	IntervalDays int     `json:"interval_days"`
	Reps         int     `json:"reps"`
	DueAt        string  `json:"due_at"`
	LastReviewed string  `json:"last_reviewed,omitempty"`
}

// SRSState is the scheduling row returned after a grade or add.
type SRSState struct {
	MemoryID     string  `json:"memory_id"`
	Ease         float64 `json:"ease"`
	IntervalDays int     `json:"interval_days"`
	Reps         int     `json:"reps"`
	DueAt        string  `json:"due_at"`
	LastReviewed string  `json:"last_reviewed,omitempty"`
}

// ReviewStats summarizes the deck for badges and the review header.
type ReviewStats struct {
	Total int `json:"total"` // memories in the deck
	Due   int `json:"due"`   // due now (due_at <= now)
}

// AddToReview enrolls a memory in the review deck, due immediately. No-op if it's
// already enrolled (its schedule is preserved).
func (m *MemoryDB) AddToReview(id string) (*SRSState, error) {
	var exists bool
	if err := m.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM memories WHERE id = ?)`, id).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("memory not found: %s", id)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := m.db.Exec(
		`INSERT OR IGNORE INTO srs (memory_id, ease, interval_days, reps, due_at) VALUES (?, 2.5, 0, 0, ?)`,
		id, now,
	); err != nil {
		return nil, fmt.Errorf("add to review: %w", err)
	}
	return m.srsState(id)
}

// RemoveFromReview drops a memory from the deck. No-op if it wasn't enrolled.
func (m *MemoryDB) RemoveFromReview(id string) error {
	if _, err := m.db.Exec(`DELETE FROM srs WHERE memory_id = ?`, id); err != nil {
		return fmt.Errorf("remove from review: %w", err)
	}
	return nil
}

// DueReviews returns enrolled memories due now (due_at <= now), soonest first,
// with full content so the review card can show the answer.
func (m *MemoryDB) DueReviews(limit int) ([]*ReviewCard, error) {
	if limit <= 0 {
		limit = 50
	}
	now := time.Now().UTC().Format(time.RFC3339)
	rows, err := m.db.Query(`
		SELECT m.id, m.title, m.content, m.taxonomy, m.created_at, m.updated_at,
		       s.ease, s.interval_days, s.reps, s.due_at, s.last_reviewed
		FROM srs s
		JOIN memories m ON m.id = s.memory_id
		WHERE s.due_at <= ?
		ORDER BY s.due_at ASC
		LIMIT ?`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("due reviews: %w", err)
	}
	defer rows.Close()

	var out []*ReviewCard
	for rows.Next() {
		mem := &Memory{}
		card := &ReviewCard{Memory: mem}
		var content, tax, last sql.NullString
		if err := rows.Scan(&mem.ID, &mem.Title, &content, &tax, &mem.CreatedAt, &mem.UpdatedAt,
			&card.Ease, &card.IntervalDays, &card.Reps, &card.DueAt, &last); err != nil {
			return nil, err
		}
		mem.Content = content.String
		mem.Taxonomy = tax.String
		card.LastReviewed = last.String
		out = append(out, card)
	}
	if out == nil {
		out = []*ReviewCard{}
	}
	return out, rows.Err()
}

// Grade applies an SM-2 update for a review grade in [0,5] and reschedules the
// card. Grades below 3 are lapses: the card resets and comes back this session.
func (m *MemoryDB) Grade(id string, grade int) (*SRSState, error) {
	if grade < 0 || grade > 5 {
		return nil, fmt.Errorf("grade must be between 0 and 5, got %d", grade)
	}
	st, err := m.srsState(id)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	if grade < 3 {
		// Lapse: relearn from scratch, due again immediately.
		st.Reps = 0
		st.IntervalDays = 0
		st.DueAt = now.Format(time.RFC3339)
	} else {
		st.Reps++
		switch st.Reps {
		case 1:
			st.IntervalDays = 1
		case 2:
			st.IntervalDays = 6
		default:
			st.IntervalDays = max(int(math.Round(float64(st.IntervalDays)*st.Ease)), 1)
		}
		st.DueAt = now.AddDate(0, 0, st.IntervalDays).Format(time.RFC3339)
	}

	// SM-2 ease adjustment, floored at minEase.
	q := float64(grade)
	st.Ease += 0.1 - (5-q)*(0.08+(5-q)*0.02)
	if st.Ease < minEase {
		st.Ease = minEase
	}
	st.LastReviewed = now.Format(time.RFC3339)

	if _, err := m.db.Exec(`
		UPDATE srs SET ease = ?, interval_days = ?, reps = ?, due_at = ?, last_reviewed = ?
		WHERE memory_id = ?`,
		st.Ease, st.IntervalDays, st.Reps, st.DueAt, st.LastReviewed, id,
	); err != nil {
		return nil, fmt.Errorf("grade update: %w", err)
	}
	return st, nil
}

// ReviewStats reports deck size and how many cards are due now.
func (m *MemoryDB) ReviewStats() (*ReviewStats, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	s := &ReviewStats{}
	if err := m.db.QueryRow(`
		SELECT COUNT(*), COALESCE(SUM(CASE WHEN due_at <= ? THEN 1 ELSE 0 END), 0)
		FROM srs`, now).Scan(&s.Total, &s.Due); err != nil {
		return nil, fmt.Errorf("review stats: %w", err)
	}
	return s, nil
}

// InReview reports whether a memory is enrolled in the review deck.
func (m *MemoryDB) InReview(id string) (bool, error) {
	var ok bool
	err := m.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM srs WHERE memory_id = ?)`, id).Scan(&ok)
	return ok, err
}

// srsState loads one scheduling row, erroring if the memory isn't enrolled.
func (m *MemoryDB) srsState(id string) (*SRSState, error) {
	st := &SRSState{MemoryID: id}
	var last sql.NullString
	err := m.db.QueryRow(`
		SELECT ease, interval_days, reps, due_at, last_reviewed FROM srs WHERE memory_id = ?`, id,
	).Scan(&st.Ease, &st.IntervalDays, &st.Reps, &st.DueAt, &last)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("memory not in review deck: %s", id)
	}
	if err != nil {
		return nil, err
	}
	st.LastReviewed = last.String
	return st, nil
}
