package docs

import (
	"regexp"
	"strings"
	"testing"
)

// Load runs at boot and a failure takes the docs page down, so the shipped
// corpus has to stay parseable and internally consistent.
func TestShippedCorpusLoads(t *testing.T) {
	loaded, err := Load()
	if err != nil {
		t.Fatalf("shipped docs corpus does not load: %v", err)
	}
	if len(loaded) == 0 {
		t.Fatal("no documents were embedded")
	}

	titles := make(map[string]bool, len(loaded))
	for _, d := range loaded {
		titles[d.Title] = true
	}
	// /docs opens on this record, and the landing page links straight to it.
	if !titles["What rekam is"] {
		t.Error(`the corpus has no "What rekam is" record to open on`)
	}

	// A [[wiki-link]] to a title no document defines renders as a dangling
	// link. That is a legitimate state in a user's corpus — it marks a record
	// worth writing — but in shipped documentation it is a typo.
	for _, d := range loaded {
		for _, target := range wikiLinkTargets(d.Content) {
			if !titles[target] {
				t.Errorf("%s links to [[%s]], which no document defines", d.File, target)
			}
		}
	}
}

// wikiLinkTargets pulls the titles out of [[...]] markers, mirroring what
// internal/db/links.go actually links: fenced and inline code are stripped
// first, so syntax examples in the docs create no edges and are not checked
// here either.
var (
	fencedCode = regexp.MustCompile("(?s)```.*?```")
	inlineCode = regexp.MustCompile("`[^`]*`")
	wikiLink   = regexp.MustCompile(`\[\[([^\[\]]+?)\]\]`)
)

func wikiLinkTargets(content string) []string {
	stripped := fencedCode.ReplaceAllString(content, "")
	stripped = inlineCode.ReplaceAllString(stripped, "")

	var out []string
	for _, m := range wikiLink.FindAllStringSubmatch(stripped, -1) {
		inner := m[1]
		if i := strings.Index(inner, "::"); i >= 0 {
			inner = inner[i+2:]
		}
		out = append(out, strings.TrimSpace(inner))
	}
	return out
}

func TestParseRequiresFrontmatter(t *testing.T) {
	cases := []struct {
		name, raw, want string
	}{
		{"no frontmatter", "# Just markdown\n", "missing --- frontmatter"},
		{"unclosed", "---\ntitle: X\n\nbody\n", "not closed"},
		{"no title", "---\ntaxonomy: docs.a\n---\n\nbody\n", "no title"},
		{"no taxonomy", "---\ntitle: X\n---\n\nbody\n", "no taxonomy"},
		{"empty body", "---\ntitle: X\ntaxonomy: docs.a\n---\n\n", "body is empty"},
		{"unknown key", "---\ntitle: X\ntaxonomy: docs.a\nauthor: me\n---\n\nbody\n", "unknown frontmatter key"},
		{"malformed line", "---\ntitle: X\ntaxonomy: docs.a\nnonsense\n---\n\nbody\n", "not key: value"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parse(tc.raw, "corpus/test.md")
			if err == nil {
				t.Fatalf("want an error mentioning %q, got none", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("want an error mentioning %q, got %v", tc.want, err)
			}
		})
	}
}

func TestParseReadsFrontmatterAndBody(t *testing.T) {
	doc, err := parse("---\ntitle: A record\ntaxonomy: docs.a\nformat: markdown\n---\n\nBody with [[A link]].\n", "corpus/a.md")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if doc.Title != "A record" || doc.Taxonomy != "docs.a" || doc.Format != "markdown" {
		t.Errorf("frontmatter not read: %+v", doc)
	}
	if doc.Content != "Body with [[A link]]." {
		t.Errorf("body not trimmed to the content: %q", doc.Content)
	}
}

// CRLF files are what a Windows editor produces; the frontmatter delimiter
// still has to be recognised.
func TestParseHandlesCRLF(t *testing.T) {
	doc, err := parse("---\r\ntitle: A record\r\ntaxonomy: docs.a\r\n---\r\n\r\nBody.\r\n", "corpus/a.md")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if doc.Title != "A record" || doc.Content != "Body." {
		t.Errorf("CRLF not normalised: %+v", doc)
	}
}
