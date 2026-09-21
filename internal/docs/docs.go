// Package docs turns the markdown files in corpus/ into a real rekam corpus.
//
// The documentation is authored as files so it lives in the repo, ships with
// the binary, and changes through the same review a code change does. At
// startup Sync writes those files into a dedicated .rekam database, which the
// server then serves read-only at /docs (see internal/api/public.go). The docs
// are therefore not a rendering of some markdown next to the app — they are
// records in rekam, read through rekam, which is the point: the documentation
// and the demo are the same artifact.
package docs

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/Ucok23/rekam/internal/db"
)

//go:embed corpus/*.md
var corpusFS embed.FS

// Doc is one documentation record, parsed from one file.
type Doc struct {
	Title    string
	Taxonomy string
	Format   string
	Content  string
	// File is the source path, used only in error messages so a malformed
	// document names itself.
	File string
}

// Load parses every embedded markdown file. It fails on the first malformed
// document rather than skipping it: a docs file that silently doesn't publish
// is worse than a server that says why at boot.
func Load() ([]Doc, error) {
	entries, err := fs.ReadDir(corpusFS, "corpus")
	if err != nil {
		return nil, fmt.Errorf("read docs corpus: %w", err)
	}

	docs := make([]Doc, 0, len(entries))
	seen := make(map[string]string, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := "corpus/" + e.Name()
		raw, err := corpusFS.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		doc, err := parse(string(raw), path)
		if err != nil {
			return nil, err
		}
		// Titles are the corpus's natural key — rekam enforces uniqueness on
		// them, and Sync matches files to records by title — so a duplicate
		// has to be caught here, where the file names are still around to say
		// which two collided.
		if prev, dup := seen[doc.Title]; dup {
			return nil, fmt.Errorf("%s: title %q already used by %s", path, doc.Title, prev)
		}
		seen[doc.Title] = path
		docs = append(docs, doc)
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].File < docs[j].File })
	return docs, nil
}

// parse reads the leading `---` frontmatter block and treats the rest as the
// body. Frontmatter is required: title and taxonomy are what make the file a
// record rather than a page, and guessing either from the filename would put
// the corpus's shape outside the author's control.
func parse(raw, path string) (Doc, error) {
	body := strings.ReplaceAll(raw, "\r\n", "\n")
	if !strings.HasPrefix(body, "---\n") {
		return Doc{}, fmt.Errorf("%s: missing --- frontmatter block", path)
	}
	end := strings.Index(body[4:], "\n---\n")
	if end < 0 {
		return Doc{}, fmt.Errorf("%s: frontmatter block is not closed with ---", path)
	}
	head, rest := body[4:4+end], body[4+end+5:]

	doc := Doc{File: path, Content: strings.TrimSpace(rest)}
	for _, line := range strings.Split(head, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			return Doc{}, fmt.Errorf("%s: frontmatter line %q is not key: value", path, line)
		}
		value = strings.TrimSpace(value)
		switch strings.TrimSpace(key) {
		case "title":
			doc.Title = value
		case "taxonomy":
			doc.Taxonomy = value
		case "format":
			doc.Format = value
		default:
			return Doc{}, fmt.Errorf("%s: unknown frontmatter key %q", path, key)
		}
	}
	if doc.Title == "" {
		return Doc{}, fmt.Errorf("%s: frontmatter has no title", path)
	}
	if doc.Taxonomy == "" {
		return Doc{}, fmt.Errorf("%s: frontmatter has no taxonomy", path)
	}
	if doc.Content == "" {
		return Doc{}, fmt.Errorf("%s: document body is empty", path)
	}
	return doc, nil
}

// Sync makes mdb match the embedded files exactly: documents are inserted or
// updated, and records whose file is gone are removed.
//
// It runs on every boot and must therefore be idempotent — an unchanged file
// produces no write, so restarting the server doesn't churn revision history.
// Matching is by title because that is the key rekam itself enforces, and it is
// what a [[wiki-link]] resolves against.
func Sync(mdb *db.MemoryDB, author string) (inserted, updated, removed int, err error) {
	docs, err := Load()
	if err != nil {
		return 0, 0, 0, err
	}

	existing, _, err := mdb.List(db.FullScope(), "", 0, 0)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("list docs corpus: %w", err)
	}
	byTitle := make(map[string]*db.Memory, len(existing))
	for _, m := range existing {
		byTitle[m.Title] = m
	}

	wanted := make(map[string]bool, len(docs))
	for _, doc := range docs {
		wanted[doc.Title] = true
		format := doc.Format
		if format == "" {
			format = "markdown"
		}

		current, ok := byTitle[doc.Title]
		if !ok {
			mem := &db.Memory{Title: doc.Title, Content: doc.Content, Taxonomy: doc.Taxonomy, Format: format}
			if err := mdb.Insert(mem, author, "docs-sync"); err != nil {
				return inserted, updated, removed, fmt.Errorf("insert %s: %w", doc.File, err)
			}
			inserted++
			continue
		}
		if current.Content == doc.Content && current.Taxonomy == doc.Taxonomy && current.Format == format {
			continue
		}
		updates := map[string]any{"content": doc.Content, "taxonomy": doc.Taxonomy, "format": format}
		if _, err := mdb.Update(current.ID, updates, author, "docs-sync", nil); err != nil {
			return inserted, updated, removed, fmt.Errorf("update %s: %w", doc.File, err)
		}
		updated++
	}

	for title, mem := range byTitle {
		if wanted[title] {
			continue
		}
		if err := mdb.Delete(mem.ID, author); err != nil {
			return inserted, updated, removed, fmt.Errorf("remove %q: %w", title, err)
		}
		removed++
	}

	// [[wiki-links]] resolve against titles that exist at write time, so a
	// document linking to one inserted after it would land dangling. Rebuilding
	// once at the end makes the graph independent of file order.
	if err := mdb.RebuildEdges(); err != nil {
		return inserted, updated, removed, fmt.Errorf("rebuild docs edges: %w", err)
	}
	return inserted, updated, removed, nil
}
