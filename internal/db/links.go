package db

import (
	"regexp"
	"strings"
)

var (
	// The target excludes both brackets so an unclosed "[[" can't greedily span
	// across a later real "[[Title]]" — without this, a stray "[[" (e.g. in a
	// heading or a title quoted in prose) swallows everything up to the next "]]",
	// fabricating one giant dangling link and eating the real link after it.
	wikiLinkRE   = regexp.MustCompile(`\[\[([^\[\]]+)\]\]`)
	fencedCodeRE = regexp.MustCompile("(?s)```.*?```")
	inlineCodeRE = regexp.MustCompile("`[^`]*`")
	whitespaceRE = regexp.MustCompile(`\s+`)
)

// Relation types form a fixed, small vocabulary. The bracket prefix before "::"
// selects one; a bare [[Title]] (or an unknown prefix) is treated as relRelates.
const (
	relRelates     = "relates"     // generic association (default)
	relDependsOn   = "depends-on"  // src builds on / requires dst
	relSupersedes  = "supersedes"  // src replaces/obsoletes dst (dst is demoted)
	relContradicts = "contradicts" // src conflicts with dst
)

var validRels = map[string]bool{
	relRelates:     true,
	relDependsOn:   true,
	relSupersedes:  true,
	relContradicts: true,
}

// parsedLink is one resolved wiki-link: its relation type and normalized target.
type parsedLink struct {
	Rel string
	Raw string
}

// parseLinks extracts unique, normalized [[rel::target]] links from markdown.
// Links inside fenced (```) or inline (`) code spans are ignored so that
// documentation about this very syntax does not create real edges.
func parseLinks(content string) []parsedLink {
	stripped := fencedCodeRE.ReplaceAllString(content, "")
	stripped = inlineCodeRE.ReplaceAllString(stripped, "")

	seen := make(map[string]struct{})
	var links []parsedLink
	for _, m := range wikiLinkRE.FindAllStringSubmatch(stripped, -1) {
		rel, raw := splitRel(m[1])
		if raw == "" {
			continue
		}
		key := rel + "\x00" + raw
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		links = append(links, parsedLink{Rel: rel, Raw: raw})
	}
	return links
}

// splitRel separates an optional "rel::" prefix from the link target. If the
// prefix isn't a known relation, the whole string is treated as the target with
// the default relation, so a stray "::" in a title degrades gracefully.
func splitRel(inner string) (rel, raw string) {
	if prefix, rest, found := strings.Cut(inner, "::"); found {
		candidate := strings.ToLower(strings.TrimSpace(prefix))
		if validRels[candidate] {
			return candidate, normalizeLink(rest)
		}
	}
	return relRelates, normalizeLink(inner)
}

// normalizeLink lowercases, trims, and collapses internal whitespace so that
// link text and memory titles resolve consistently.
func normalizeLink(s string) string {
	s = whitespaceRE.ReplaceAllString(s, " ")
	return strings.ToLower(strings.TrimSpace(s))
}
