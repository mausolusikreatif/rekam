# Plan: wiki-links → edges table → authority re-ranking

## Goal
Let memories link to each other via inline `[[...]]` markers in markdown, materialize
those links into a derived `edges` table, and fold backlink authority into search
ranking. No LLM in the path; same "rebuildable index" pattern as `memories_fts`.

## Design decisions (settled)
- **Authoring surface:** explicit `[[...]]` in the memory `content`. *Not* metadata,
  *not* auto-linking on bare word match (that floods the graph and destroys the
  authority signal).
- **Storage:** a separate, derived `edges` table — not a JSON column on `memories`.
  Rebuildable from content, droppable, joinable for re-ranking.
- **Link target:** resolved by **title** at write time, stored as the target **UUID**
  plus the raw link text. Unresolved links are kept as *dangling* edges (`dst_id NULL`)
  and re-resolved as new titles appear.

---

## 1. Schema + migration (`internal/db/memory.go`)
Add alongside the FTS migration in `EnsureSchema`:

```sql
CREATE TABLE IF NOT EXISTS edges (
    src_id  TEXT NOT NULL,            -- memory whose content holds the [[link]]
    dst_id  TEXT,                     -- resolved target UUID; NULL = dangling
    raw     TEXT NOT NULL,            -- text inside [[ ]], lowercased/trimmed
    rel     TEXT,                     -- reserved for typed edges (NULL for now)
    PRIMARY KEY (src_id, raw),
    FOREIGN KEY (src_id) REFERENCES memories(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_edges_dst ON edges(dst_id);
CREATE INDEX IF NOT EXISTS idx_edges_raw ON edges(raw);
```

Notes:
- No FK on `dst_id` (it can be NULL / point at a not-yet-existing title).
- `ON DELETE CASCADE` only cleans outgoing edges; incoming edges to a deleted memory
  are handled in the delete path (below) by nulling them back to dangling.

## 2. Link parser (`internal/db/links.go`, new)
```go
var wikiLinkRE = regexp.MustCompile(`\[\[([^\]]+)\]\]`)

// parseLinks extracts unique, normalized [[targets]] from markdown content.
func parseLinks(content string) []string
```
- Normalize: trim, collapse internal whitespace, lowercase for matching.
- De-dupe within a single memory (PK is `(src_id, raw)` anyway).
- Ignore links inside fenced code blocks (```), so example syntax in a memory about
  this feature doesn't create real edges. Strip code fences before matching.

## 3. Resolution + sync (`internal/db/memory.go`)
One helper, called from Insert and Update, inside the same tx:

```go
// syncEdges replaces all outgoing edges for src and re-resolves dangling
// edges that this memory's title now satisfies.
func (m *MemoryDB) syncEdges(tx *sql.Tx, srcID, title, content string) error
```
Steps:
1. `DELETE FROM edges WHERE src_id = ?` (replace-all; simplest correct semantics).
2. For each parsed link, resolve `raw` → `dst_id` via
   `SELECT id FROM memories WHERE lower(title) = ?` (first match; log/ignore
   ambiguous multi-match for v1). Insert edge with `dst_id` or NULL.
3. After writing this memory, re-point any **dangling** edges that match this
   memory's title:
   `UPDATE edges SET dst_id = ? WHERE dst_id IS NULL AND raw = ?`.

Wire into existing methods (convert Insert/Update to use a tx):
- **Insert:** after the `INSERT INTO memories`, call `syncEdges` (parses content,
  resolves, plus dangling re-resolution for the new title).
- **Update:** after the `UPDATE memories`, call `syncEdges` with the *post-update*
  title+content. (Title change → its content re-parsed; dangling re-resolution
  picks up the new title. Outgoing edges fully rebuilt.)
- **Delete:** before/after `DELETE FROM memories`, run
  `UPDATE edges SET dst_id = NULL WHERE dst_id = ?` so inbound links become dangling
  again rather than pointing at a tombstone. (Outgoing edges drop via CASCADE.)

## 4. Authority re-ranking (`Search` in `internal/db/memory.go`)
Backlink count = authority. Blend with FTS `rank` (BM25, lower = better) as a
tiebreak/boost rather than replacing it — keep lexical relevance primary.

```sql
SELECT m.id, m.title, m.content, m.taxonomy, m.created_at, m.updated_at,
       (SELECT COUNT(*) FROM edges e WHERE e.dst_id = m.id) AS backlinks
FROM memories_fts f
JOIN memories m ON m.rowid = f.rowid
WHERE memories_fts MATCH ?
  AND (... taxonomy clause ...)
ORDER BY f.rank - (0.0 + min(backlinks, CAP)) * BOOST   -- BM25 is negative; nudge up
```
- Start conservative: `CAP = 5`, `BOOST` tuned so a well-linked memory edges past a
  near-tie, never overrides a clearly stronger lexical match. Make both constants
  named consts so they're easy to tune from tests.
- Keep `backlinks` out of the returned `Memory` struct for now, or add it to
  `SearchResult` per-hit as evidence (see step 6).

## 5. Surface backlinks on read (`GetMemory` / engine)
In `get_memory`, optionally include backlinks so the agent can traverse the graph:
```sql
SELECT m.id, m.title, m.taxonomy FROM edges e
JOIN memories m ON m.id = e.src_id WHERE e.dst_id = ?
```
Return as `linked_from: [{id,title,taxonomy}]` on the memory payload. Cheap, and it
makes the MCP "narrow before concluding" loop graph-aware.

## 6. Explainable evidence tags (small, high-value add-on)
Per search hit, attach why it ranked: `matched: ["title"|"content"]`,
`backlinks: N`. Lives in `SearchResult.Results` as a parallel struct or extra fields.
Helps the agent's `omitted_count`/narrowing decisions.

## 7. RebuildFTS → also rebuild edges
Extend the existing `RebuildFTS` (or add `RebuildEdges`) to:
`DELETE FROM edges; ` then re-run `syncEdges` over every memory. Gives a recovery
path and a one-shot migration for existing stores. Expose via the existing
`rebuild_fts` MCP tool or a sibling.

---

## Touch list
- `internal/db/memory.go` — schema, migration, Insert/Update/Delete tx + syncEdges,
  Search re-rank, RebuildFTS extension.
- `internal/db/links.go` (new) — parser + normalization.
- `internal/db/memory_test.go` — see tests below.
- `internal/engine/engine.go` — pass through backlinks on GetMemory/Search (thin).
- `internal/mcp/server.go` + `SKILL.md` — document `[[...]]` authoring + `linked_from`.

## Tests (`internal/db/memory_test.go`)
- parseLinks: basic, multiple, dupes, code-fence exclusion, normalization.
- Insert with `[[Existing Title]]` → resolved edge with correct dst_id.
- Insert with `[[Future Title]]` → dangling edge; later inserting that title
  re-resolves it.
- Update changing content → outgoing edges rebuilt (old links gone, new present).
- Delete target → inbound edges go dangling, not orphaned.
- Search: two near-tie lexical hits, the more-linked one ranks first; a strong
  lexical hit still beats a weakly-relevant but heavily-linked one (BOOST sanity).
- RebuildEdges reconstructs identical edge set from scratch.

## Sequencing
1. Schema + parser + tests (no behavior change yet).
2. Wire Insert/Update/Delete sync + tests.
3. Search re-rank + tuning consts + tests.
4. GetMemory backlinks + evidence tags.
5. Rebuild path + SKILL.md/MCP docs.

## Explicitly deferred
- Typed edges (`> rel: target`) — `rel` column reserved; layer on later.
- Hash/vector hybrid search — separate spike; this plan is graph-only.
- Slug-based linking — title resolution is enough for v1; revisit if renames churn.
