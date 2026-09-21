package api

// Scenarios: spec/export.md (EXPT-01..06, EXPT-08)
//
// EXPT-07 needs a second workspace, so it lives in export_team_test.go.

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Ucok23/rekam/internal/db"
	"github.com/Ucok23/rekam/internal/engine"
	_ "modernc.org/sqlite"
)

// readExportZip runs GET /export and returns the archive as filename → contents.
func readExportZip(t *testing.T, rr *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	if rr.Code != http.StatusOK {
		t.Fatalf("export: want 200, got %d: %s", rr.Code, rr.Body)
	}
	body := rr.Body.Bytes()
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("export is not a readable zip: %v", err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s in archive: %v", f.Name, err)
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("read %s in archive: %v", f.Name, err)
		}
		out[f.Name] = string(b)
	}
	return out
}

// A user taking their records elsewhere downloads one archive and gets a
// browsable folder tree of Markdown files: the taxonomy becomes directories, the
// title becomes the filename, and each file carries enough front-matter to be
// re-imported or read in any editor.
func TestExportGivesBackAWholeBrowsableCorpus(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	doRequest(t, srv, "POST", "/memory", key, map[string]any{
		"title": "Deploy Runbook", "taxonomy": "work.ops", "content": "Step 1. Push.",
	})
	doRequest(t, srv, "POST", "/memory", key, map[string]any{
		"title": "Savings Overview", "taxonomy": "finance.accounts", "content": "BankX.",
	})

	files := readExportZip(t, doRequest(t, srv, "GET", "/export", key, nil))
	// EXPT-01
	if len(files) != 2 {
		t.Fatalf("export should hold every record, got %d: %v", len(files), files)
	}

	runbook, ok := files["work/ops/deploy-runbook.md"]
	if !ok {
		t.Fatalf("taxonomy should become nested folders; archive held: %v", keysOf(files))
	}
	if _, ok := files["finance/accounts/savings-overview.md"]; !ok {
		t.Errorf("missing second record; archive held: %v", keysOf(files))
	}

	// EXPT-02: the file is self-describing: front-matter, then the record.
	for _, want := range []string{"---", "title: Deploy Runbook", "taxonomy: work.ops", "# Deploy Runbook", "Step 1. Push."} {
		if !strings.Contains(runbook, want) {
			t.Errorf("exported file missing %q:\n%s", want, runbook)
		}
	}

	// EXPT-01: it is offered as a download, not rendered in the browser.
	rr := doRequest(t, srv, "GET", "/export", key, nil)
	if ct := rr.Header().Get("Content-Type"); ct != "application/zip" {
		t.Errorf("Content-Type = %q, want application/zip", ct)
	}
	if cd := rr.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") || !strings.Contains(cd, ".zip") {
		t.Errorf("Content-Disposition = %q, want a .zip attachment", cd)
	}
}

// Two records that happen to share a title in the same branch are distinct
// records, and the export must not silently drop one by writing both to the same
// path — that would be quiet data loss in the user's backup.
// Two live memories sharing a title can no longer be created through the
// normal write path (MEM-22 — [[links]] would have nothing deterministic to
// resolve to). But a tenant file can still hold such a pair from before that
// constraint existed, so export must keep handling the case defensively
// rather than assume it can't happen; this seeds the pair directly at the db
// layer (bypassing Engine.WriteMemory's guard) to simulate exactly that.
func TestExportKeepsRecordsWithClashingTitles(t *testing.T) {
	regF, err := os.CreateTemp("", "export-clash-reg-*.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	regF.Close()
	defer os.Remove(regF.Name())
	memF, err := os.CreateTemp("", "export-clash-mem-*.memory")
	if err != nil {
		t.Fatal(err)
	}
	memF.Close()
	defer os.Remove(memF.Name())

	reg, err := db.OpenRegistry(regF.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	key := "clash-key"
	if _, err := reg.Create("alice", key, memF.Name(), true); err != nil {
		t.Fatal(err)
	}

	// Run the schema migrations by opening normally, then close and reopen a
	// raw connection to insert the clashing pair directly — Engine.WriteMemory
	// and MemoryDB.Insert both now enforce MEM-22, so a legacy pair like this
	// can only be simulated by writing under them, the same way
	// TestMigrateAddFormatColumnBackfill (internal/db/memory_test.go) pokes a
	// pre-migration schema directly rather than going through the API.
	mdb, err := db.OpenMemory(memF.Name())
	if err != nil {
		t.Fatal(err)
	}
	if err := mdb.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", memF.Name())
	if err != nil {
		t.Fatal(err)
	}
	now := "2020-01-01T00:00:00Z"
	for i, content := range []string{"first note", "second note"} {
		if _, err := raw.Exec(`
			INSERT INTO memories (id, title, content, taxonomy, format, created_at, updated_at, version)
			VALUES (?, 'Notes', ?, 'work.ops', 'markdown', ?, ?, 1)`,
			fmt.Sprintf("clash-%d", i), content, now, now,
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	srv := NewServer(engine.New(reg, 6000), ":0", "", "")

	files := readExportZip(t, doRequest(t, srv, "GET", "/export", key, nil))
	// EXPT-03
	if len(files) != 2 {
		t.Fatalf("both same-titled records must survive the export, got %d: %v", len(files), keysOf(files))
	}
	var bodies []string
	for _, c := range files {
		bodies = append(bodies, c)
	}
	if strings.Contains(bodies[0], "first note") == strings.Contains(bodies[1], "first note") {
		t.Errorf("the two archive entries hold the same record; one was overwritten:\n%v", files)
	}
}

// A title containing YAML syntax must not corrupt the front-matter block —
// otherwise the export is unparseable by anything that reads it back.
func TestExportQuotesTitlesThatWouldBreakFrontMatter(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	doRequest(t, srv, "POST", "/memory", key, map[string]any{
		"title": `Outage: "db down" #1`, "taxonomy": "work.ops", "content": "postmortem",
	})

	files := readExportZip(t, doRequest(t, srv, "GET", "/export", key, nil))
	var doc string
	for _, c := range files {
		doc = c
	}
	line := frontMatterLine(doc, "title:")
	// EXPT-04
	if !strings.HasPrefix(line, `title: "`) || !strings.HasSuffix(line, `"`) {
		t.Errorf("a title with YAML-significant characters must be quoted, got: %s", line)
	}
	if !strings.Contains(line, `\"db down\"`) {
		t.Errorf("embedded quotes must be escaped, got: %s", line)
	}
}

// A diagram stored as mermaid is not markdown; the export wraps it in a tagged
// fence so it still renders wherever the user opens it.
func TestExportFencesNonMarkdownRecords(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	rr := doRequest(t, srv, "POST", "/memory", key, map[string]any{
		"title": "Service Map", "taxonomy": "work.arch",
		"format": "mermaid", "content": "graph TD;\n  a-->b;",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("write mermaid record: %d: %s", rr.Code, rr.Body)
	}

	files := readExportZip(t, doRequest(t, srv, "GET", "/export", key, nil))
	doc := files["work/arch/service-map.md"]
	if doc == "" {
		t.Fatalf("expected work/arch/service-map.md, got %v", keysOf(files))
	}
	// EXPT-05
	if !strings.Contains(doc, "format: mermaid") {
		t.Errorf("front-matter should record the format:\n%s", doc)
	}
	if !strings.Contains(doc, "```mermaid\ngraph TD;") || !strings.HasSuffix(strings.TrimSpace(doc), "```") {
		t.Errorf("mermaid body should be wrapped in a tagged fence:\n%s", doc)
	}
}

// Downloading a single record gives a Markdown file named after the record, in
// the same format as the archive entries.
func TestExportSingleRecordAsMarkdown(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	write := doRequest(t, srv, "POST", "/memory", key, map[string]any{
		"title": "Deploy Runbook", "taxonomy": "work.ops", "content": "Step 1. Push.",
	})
	var mem struct {
		ID string `json:"id"`
	}
	decodeJSON(t, write, &mem)

	rr := doRequest(t, srv, "GET", "/export/"+mem.ID, key, nil)
	// EXPT-06
	if rr.Code != http.StatusOK {
		t.Fatalf("export one: want 200, got %d: %s", rr.Code, rr.Body)
	}
	if cd := rr.Header().Get("Content-Disposition"); !strings.Contains(cd, `filename="deploy-runbook.md"`) {
		t.Errorf("Content-Disposition = %q, want a deploy-runbook.md attachment", cd)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/markdown") {
		t.Errorf("Content-Type = %q, want text/markdown", ct)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "title: Deploy Runbook") || !strings.Contains(body, "Step 1. Push.") {
		t.Errorf("unexpected single-record export:\n%s", body)
	}

	// EXPT-06: exporting a record that does not exist is a 404, not an empty file.
	if miss := doRequest(t, srv, "GET", "/export/nope", key, nil); miss.Code != http.StatusNotFound {
		t.Errorf("export of unknown id: want 404, got %d", miss.Code)
	}
}

// Nobody gets a corpus dump without credentials.
func TestExportRequiresAuthentication(t *testing.T) {
	srv, key, cleanup := testServer(t)
	defer cleanup()

	doRequest(t, srv, "POST", "/memory", key, map[string]any{"title": "Secret", "taxonomy": "work"})

	// EXPT-08
	if rr := doRequest(t, srv, "GET", "/export", "", nil); rr.Code != http.StatusUnauthorized {
		t.Errorf("anonymous export: want 401, got %d: %s", rr.Code, rr.Body)
	}
	if rr := doRequest(t, srv, "GET", "/export", "not-a-real-key", nil); rr.Code != http.StatusUnauthorized {
		t.Errorf("bad-key export: want 401, got %d: %s", rr.Code, rr.Body)
	}
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// frontMatterLine returns the front-matter line starting with prefix.
func frontMatterLine(doc, prefix string) string {
	for line := range strings.SplitSeq(doc, "\n") {
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	return ""
}
