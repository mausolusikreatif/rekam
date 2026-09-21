package db

import "strings"

// Scope is the taxonomy authorization an identity carries into a shared corpus:
// which branches it may read, which it may write, and which restricted branches
// it may at least see the existence of. The zero value grants nothing, so a
// scope that is never populated denies everything — a forgotten wiring fails
// closed, never open.
//
// Personal, single-user rekam uses FullScope (all=true), which short-circuits
// every check and every SQL clause to "no restriction". The prefix lists only
// matter in team mode, where an identity is deliberately confined.
type Scope struct {
	all           bool
	read          []string // taxonomy prefixes whose memories may be read in full
	write         []string // prefixes that may be written; expected subset of read
	titlesVisible []string // prefixes whose existence/titles may show though content stays hidden
}

// FullScope grants unrestricted access. It is what personal rekam always uses
// and what a team identity with no scope rows falls back to.
func FullScope() Scope { return Scope{all: true} }

// NewScope builds a restricted scope from explicit prefix lists. read gates
// content, write gates edits, titlesVisible opts branches into existence
// disclosure without content. Callers are responsible for keeping write ⊆ read;
// the predicates do not assume it, but a writable-yet-unreadable branch would be
// an odd policy.
func NewScope(read, write, titlesVisible []string) Scope {
	return Scope{read: read, write: write, titlesVisible: titlesVisible}
}

// Unrestricted reports whether this scope imposes no limits (personal mode).
func (s Scope) Unrestricted() bool { return s.all }

func prefixMatch(prefixes []string, taxonomy string) bool {
	for _, p := range prefixes {
		if taxonomy == p || strings.HasPrefix(taxonomy, p+".") {
			return true
		}
	}
	return false
}

// CanRead reports whether the memory at taxonomy may be read in full.
func (s Scope) CanRead(taxonomy string) bool {
	return s.all || prefixMatch(s.read, taxonomy)
}

// CanWrite reports whether a memory may be created or edited at taxonomy.
func (s Scope) CanWrite(taxonomy string) bool {
	return s.all || prefixMatch(s.write, taxonomy)
}

// TitleVisible reports whether the existence and title of a memory at taxonomy
// may be shown. Readability implies title visibility; the titlesVisible list
// adds branches that are known-to-exist but content-hidden.
func (s Scope) TitleVisible(taxonomy string) bool {
	return s.all || prefixMatch(s.read, taxonomy) || prefixMatch(s.titlesVisible, taxonomy)
}

// clauseFor builds a SQL predicate confining the taxonomy column `col` to the
// given prefixes, returning the predicate text and its bind args. An unrestricted
// scope returns ("", nil) — no WHERE fragment at all. A restricted scope with an
// empty prefix set returns ("0", nil), a predicate that is never true, so the
// query returns nothing: default deny expressed in SQL rather than in Go, which
// keeps hidden rows from ever entering a result set (and thus a token budget or
// a COUNT) in the first place.
func (s Scope) clauseFor(col string, prefixes []string) (string, []any) {
	if s.all {
		return "", nil
	}
	if len(prefixes) == 0 {
		return "0", nil
	}
	parts := make([]string, 0, len(prefixes))
	args := make([]any, 0, len(prefixes)*2)
	for _, p := range prefixes {
		parts = append(parts, "("+col+" = ? OR "+col+" LIKE ?)")
		args = append(args, p, p+".%")
	}
	return "(" + strings.Join(parts, " OR ") + ")", args
}

// readClause restricts col to readable prefixes; see clauseFor.
func (s Scope) readClause(col string) (string, []any) {
	return s.clauseFor(col, s.read)
}

// titleClause restricts col to prefixes whose existence may be disclosed — the
// readable branches plus the title-visible ones. Used by existence-level views
// (catalog, backlinks, graph nodes) as opposed to content-level ones (search).
func (s Scope) titleClause(col string) (string, []any) {
	if s.all {
		return "", nil
	}
	return s.clauseFor(col, append(append([]string{}, s.read...), s.titlesVisible...))
}
