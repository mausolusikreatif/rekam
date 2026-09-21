package db

import (
	"fmt"
	"time"
)

// TemplateBranch is one declared branch of a corpus's taxonomy template: a
// dot-notation path plus a short "what goes here" note. Sort orders siblings in
// the UI; equal sorts fall back to path order.
type TemplateBranch struct {
	Path        string `json:"path"`
	Description string `json:"description,omitempty"`
	Sort        int    `json:"sort"`
}

// TaxonomyTemplate returns the corpus's saved template branches ordered by
// (sort, path). An empty slice means no template has been saved — the caller
// decides whether to fall back to a shipped default.
func (m *MemoryDB) TaxonomyTemplate() ([]TemplateBranch, error) {
	rows, err := m.db.Query(`
		SELECT path, description, sort FROM taxonomy_template
		ORDER BY sort, path`)
	if err != nil {
		return nil, fmt.Errorf("taxonomy template: %w", err)
	}
	defer rows.Close()
	var out []TemplateBranch
	for rows.Next() {
		var b TemplateBranch
		if err := rows.Scan(&b.Path, &b.Description, &b.Sort); err != nil {
			return nil, fmt.Errorf("scan template branch: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// HasTaxonomyTemplate reports whether the corpus has any saved template rows, so
// callers can tell a customized template from the shipped default.
func (m *MemoryDB) HasTaxonomyTemplate() (bool, error) {
	var n int
	if err := m.db.QueryRow(`SELECT COUNT(*) FROM taxonomy_template`).Scan(&n); err != nil {
		return false, fmt.Errorf("count template: %w", err)
	}
	return n > 0, nil
}

// SetTaxonomyTemplate replaces the corpus's template with the given branches in
// one transaction: the template is a whole schema, so a partial write would
// leave a half-defined vocabulary. Passing an empty slice clears the template,
// reverting the corpus to the shipped default on the next read. Branch order in
// the slice becomes the sort order.
func (m *MemoryDB) SetTaxonomyTemplate(branches []TemplateBranch) error {
	tx, err := m.db.Begin()
	if err != nil {
		return fmt.Errorf("set template: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM taxonomy_template`); err != nil {
		return fmt.Errorf("clear template: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for i, b := range branches {
		if b.Path == "" {
			continue // a branch with no path is not addressable; skip it
		}
		if _, err := tx.Exec(`
			INSERT INTO taxonomy_template (path, description, sort, updated_at)
			VALUES (?, ?, ?, ?)
			ON CONFLICT(path) DO UPDATE SET description = excluded.description, sort = excluded.sort, updated_at = excluded.updated_at`,
			b.Path, b.Description, i, now); err != nil {
			return fmt.Errorf("insert template branch %q: %w", b.Path, err)
		}
	}
	return tx.Commit()
}
