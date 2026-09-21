package db

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// LinkSuggestion is a pair of memories that look related (high text similarity)
// but aren't yet connected by a [[wiki-link]] in either direction. Score is the
// FTS relevance of Target to Source's title (higher = stronger candidate).
type LinkSuggestion struct {
	Source ScopedMemory `json:"source"`
	Target ScopedMemory `json:"target"`
	Score  float64      `json:"score"`
}

// candidatesPerSource bounds how many FTS matches each memory contributes, so a
// single hub title can't flood the suggestion list before global ranking.
const candidatesPerSource = 5

// SuggestLinks finds memory pairs that share strong textual overlap but have no
// edge between them — the inverse of dangling links, pointing at connections the
// graph is missing. Each memory's title is matched against the FTS index; matches
// that are already linked (either direction) or self are skipped, and the best
// score per unordered pair is kept.
func (m *MemoryDB) SuggestLinks(scope Scope, limit int) ([]LinkSuggestion, error) {
	if limit <= 0 {
		limit = 20
	}

	linked, err := m.linkedPairs()
	if err != nil {
		return nil, err
	}

	// A suggestion is advice to link two memories, so both ends must be readable.
	// Confining the source query and the target FTS query to readable prefixes
	// means a restricted memory is never named as either half of a pair.
	srcClause, srcArgs := scope.readClause("taxonomy")
	srcQuery := `SELECT id, title, taxonomy FROM memories`
	if srcClause != "" {
		srcQuery += ` WHERE ` + srcClause
	}
	srcRows, err := m.db.Query(srcQuery, srcArgs...)
	if err != nil {
		return nil, fmt.Errorf("suggest sources: %w", err)
	}
	type src struct {
		id, title string
		tax       sql.NullString
	}
	var sources []src
	for srcRows.Next() {
		var s src
		if err := srcRows.Scan(&s.id, &s.title, &s.tax); err != nil {
			srcRows.Close()
			return nil, err
		}
		sources = append(sources, s)
	}
	srcRows.Close()
	if err := srcRows.Err(); err != nil {
		return nil, err
	}

	best := map[string]*LinkSuggestion{} // pairKey -> best suggestion
	for _, s := range sources {
		// OR the title tokens: suggestions favor recall (any shared term) over the
		// strict AND used for direct search, since "related" is inherently fuzzy.
		match := ftsOrQuery(s.title)
		if match == "" {
			continue
		}
		tClause, tArgs := scope.readClause("m.taxonomy")
		tWhere := ""
		if tClause != "" {
			tWhere = " AND " + tClause
		}
		qArgs := append([]any{match, s.id}, tArgs...)
		qArgs = append(qArgs, candidatesPerSource)
		rows, err := m.db.Query(`
			SELECT m.id, m.title, m.taxonomy, f.rank
			FROM memories_fts f
			JOIN memories m ON m.rowid = f.rowid
			WHERE memories_fts MATCH ? AND m.id != ?`+tWhere+`
			ORDER BY f.rank
			LIMIT ?`, qArgs...)
		if err != nil {
			return nil, fmt.Errorf("suggest match: %w", err)
		}
		for rows.Next() {
			var tID, tTitle string
			var tTax sql.NullString
			var rank float64
			if err := rows.Scan(&tID, &tTitle, &tTax, &rank); err != nil {
				rows.Close()
				return nil, err
			}
			key := pairKey(s.id, tID)
			if linked[key] {
				continue
			}
			score := -rank // bm25 rank is negative; flip so higher = more similar
			if cur, ok := best[key]; ok {
				if score <= cur.Score {
					continue
				}
			}
			best[key] = &LinkSuggestion{
				Source: ScopedMemory{ID: s.id, Title: s.title, Taxonomy: s.tax.String},
				Target: ScopedMemory{ID: tID, Title: tTitle, Taxonomy: tTax.String},
				Score:  score,
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	out := make([]LinkSuggestion, 0, len(best))
	for _, s := range best {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Source.ID < out[j].Source.ID // stable tiebreak
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// linkedPairs returns the set of unordered memory-id pairs already joined by a
// resolved edge, so suggestions never repeat an existing connection.
func (m *MemoryDB) linkedPairs() (map[string]bool, error) {
	rows, err := m.db.Query(`SELECT src_id, dst_id FROM edges WHERE dst_id IS NOT NULL`)
	if err != nil {
		return nil, fmt.Errorf("linked pairs: %w", err)
	}
	defer rows.Close()
	set := map[string]bool{}
	for rows.Next() {
		var a, b string
		if err := rows.Scan(&a, &b); err != nil {
			return nil, err
		}
		set[pairKey(a, b)] = true
	}
	return set, rows.Err()
}

// ftsOrQuery turns text into an FTS5 MATCH that ORs each quoted word token, so a
// candidate matches on any shared term. Returns "" when there are no tokens.
func ftsOrQuery(s string) string {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	if len(fields) == 0 {
		return ""
	}
	for i, f := range fields {
		fields[i] = `"` + f + `"`
	}
	return strings.Join(fields, " OR ")
}

// pairKey is an order-independent key for a memory pair.
func pairKey(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + "\x00" + b
}
