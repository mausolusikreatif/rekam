package api

import (
	"archive/zip"
	"fmt"
	"net/http"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/Ucok23/rekam/internal/db"
)

// handleExport streams a zip archive of every memory the identity can read.
// Each record becomes a Markdown file with YAML front-matter; the dot-notation
// taxonomy is expanded into nested folders, so work.auth → work/auth/<slug>.md.
func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	key := apiKeyFromRequest(r)

	// limit 0 = no LIMIT clause → every record, newest first.
	result, err := s.eng.ListMemories(key, "", 0, 0)
	if err != nil {
		s.respondErr(w, r, err)
		return
	}

	filename := "rekam-export-" + time.Now().Format("2006-01-02") + ".zip"
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)

	zw := zip.NewWriter(w)
	defer zw.Close()

	used := map[string]bool{}
	for _, m := range result.Memories {
		name := exportPath(m, used)
		f, err := zw.Create(name)
		if err != nil {
			return // header already sent; nothing useful to report
		}
		if _, err := f.Write([]byte(renderMarkdown(m))); err != nil {
			return
		}
	}
}

// handleExportMemory streams a single memory as a Markdown file with the same
// YAML front-matter format as the zip export, so one record can be downloaded
// on its own from the memory view.
func (s *Server) handleExportMemory(w http.ResponseWriter, r *http.Request) {
	key := apiKeyFromRequest(r)
	id := r.PathValue("id")

	mem, err := s.eng.GetMemory(key, id)
	if err != nil {
		s.respondErr(w, r, err)
		return
	}

	base := slugify(mem.Title)
	if base == "" {
		base = "untitled"
	}

	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+base+`.md"`)
	w.Write([]byte(renderMarkdown(mem)))
}

var slugStrip = regexp.MustCompile(`[^a-z0-9]+`)

// exportPath maps a memory to a unique "folder/folder/slug.md" path inside the
// archive, deriving folders from the taxonomy segments and the filename from a
// slug of the title (falling back to the id). Collisions get a short id suffix.
func exportPath(m *db.Memory, used map[string]bool) string {
	var dirs []string
	for _, seg := range strings.Split(m.Taxonomy, ".") {
		if s := slugify(seg); s != "" {
			dirs = append(dirs, s)
		}
	}
	if len(dirs) == 0 {
		dirs = []string{"unclassified"}
	}

	base := slugify(m.Title)
	if base == "" {
		base = "untitled"
	}

	full := path.Join(append(dirs, base+".md")...)
	if used[full] {
		short := m.ID
		if len(short) > 8 {
			short = short[:8]
		}
		full = path.Join(append(dirs, base+"-"+short+".md")...)
	}
	used[full] = true
	return full
}

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = slugStrip.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 80 {
		s = strings.Trim(s[:80], "-")
	}
	return s
}

// renderMarkdown writes a record as a Markdown document with YAML front-matter.
func renderMarkdown(m *db.Memory) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "id: %s\n", m.ID)
	fmt.Fprintf(&b, "title: %s\n", yamlString(m.Title))
	fmt.Fprintf(&b, "taxonomy: %s\n", m.Taxonomy)
	if m.Format != "" && m.Format != "markdown" {
		fmt.Fprintf(&b, "format: %s\n", m.Format)
	}
	if m.CreatedAt != "" {
		fmt.Fprintf(&b, "created_at: %s\n", m.CreatedAt)
	}
	if m.UpdatedAt != "" {
		fmt.Fprintf(&b, "updated_at: %s\n", m.UpdatedAt)
	}
	b.WriteString("---\n\n")
	b.WriteString("# ")
	b.WriteString(m.Title)
	b.WriteString("\n\n")
	if m.Content != "" {
		// Non-markdown bodies are wrapped in a fenced code block tagged with the
		// format, so the export renders in any markdown viewer (mermaid diagrams
		// included) and round-trips back via ingestion.
		fence := m.Format != "" && m.Format != "markdown"
		if fence {
			b.WriteString("```")
			b.WriteString(m.Format)
			b.WriteString("\n")
		}
		b.WriteString(m.Content)
		if !strings.HasSuffix(m.Content, "\n") {
			b.WriteString("\n")
		}
		if fence {
			b.WriteString("```\n")
		}
	}
	return b.String()
}

// yamlString quotes a scalar when it could be misread as YAML structure.
func yamlString(s string) string {
	if s == "" {
		return `""`
	}
	if strings.ContainsAny(s, ":#\"'\n") || strings.HasPrefix(s, " ") || strings.HasSuffix(s, " ") {
		return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
	}
	return s
}
