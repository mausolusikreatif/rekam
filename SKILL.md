---
name: rekam
version: 2.1.0
description: Skill for interacting with rekam via MCP.
tools:
  - catalog
  - memory_scope
  - search
  - get_memory
  - write_memory
  - update_memory
  - delete_memory
  - edge_health
  - graph
  - suggest_links
  - rebuild_fts
---

# rekam — Agent Skill

You have access to a persistent memory store via MCP tools. Follow the procedures below exactly.

---

## Retrieval procedure

1. **If you already know the taxonomy**, skip catalog and go straight to `search` with that prefix.
2. **Otherwise discover with `catalog`** — call it with no args for top-level branches, then call it again with a `prefix` to drill into any branch marked `expandable: true`. It rolls the taxonomy into a shallow tree, so it stays cheap as memory grows.
3. `search` with `q` and `taxonomy` (a prefix from catalog). If a branch is small, `memory_scope` it first to browse titles.
4. Reason over results — synthesise; do not blindly trust all returned memories.

**Rules:**
- `catalog` is for discovery only — don't re-call it when the taxonomy is already known
- Never call `search` without a `taxonomy` unless the question explicitly spans all memory
- Use `memory_scope` to browse a taxonomy before deciding what to query — it returns id and title only (no content)
- If `omitted_count > 0`, your results are incomplete — narrow the query or taxonomy before concluding
- Use `get_memory` to fetch full content when a title looks relevant

### The `rekam://catalog` resource

The top-level taxonomy tree is also published as an MCP **resource** at `rekam://catalog` (`application/json`, same shape as the `catalog` tool with no args).

- If your client supports MCP resources, read `rekam://catalog` for the top-level branches instead of calling the `catalog` tool — resource reads can be cached/subscribed by the client, so the tree isn't re-injected into context on every turn.
- Still use the `catalog` **tool** with a `prefix` to drill into a branch (`expandable: true`) — the resource only carries the top level.
- Clients without resource support lose nothing: the `catalog` tool with no args returns the same top-level tree.

---

## Write procedure

1. Classify taxonomy — pick from `catalog` or create a new dot-notation path (e.g. `finance.accounts`)
2. Write a concise, scannable `title`
3. Write `content` in markdown — include enough context for a future reader
4. Link related memories inline with `[[Exact Title]]` (see below) — a memory with no links is an island that recall can't reach from anything else, so treat linking as part of writing
5. Call `write_memory`, then check the `links` array in the response — each entry reports whether that `[[link]]` resolved

**Rules:**
- Taxonomy must be dot-notation: `work.projects`, `finance.accounts`, `personal.health`
- If the API returns a taxonomy error, it will include the current catalog — pick from it or create a valid new path
- `links` entries with `resolved: false` are dangling — usually a typo'd title (fix it) or an intentional forward-reference that will resolve once the target exists
- **Always pass `agent`** with your own model identity (e.g. `"claude-opus-5"`) on `write_memory` and `update_memory`. It's self-reported and shows up in the memory's revision history — that's what lets a team see, at a glance, which AI (or human, when it's empty) actually wrote each piece of what they know.

### Linking memories with `[[...]]`

Reference another memory inside `content` with a wiki-link: `[[Acme Onboarding]]`.
The bracketed text is matched against memory **titles** (case-insensitive).

**You can only link to a memory you know exists.** Link from titles already in your
context (from `search`/`catalog` results this session). If you're writing cold with
nothing related in context, run one `memory_scope` on the target taxonomy first — it
returns titles only (cheap) — then link the relevant ones by exact title. Skipping this
is the main reason memories end up unlinked: an agent can't link to what it hasn't seen.

Optionally type the link with a `rel::` prefix from this fixed vocabulary:

| Link | Meaning | Effect on ranking |
|---|---|---|
| `[[Title]]` or `[[relates::Title]]` | generic association (default) | boosts the target's authority |
| `[[depends-on::Title]]` | this memory builds on / requires the target | boosts the target (foundational) |
| `[[supersedes::Title]]` | this memory replaces an older one | **demotes the target** — the stale memory sinks below this one in search |
| `[[contradicts::Title]]` | this memory conflicts with the target | no score change; flags the conflict |

Rules:
- Link intentionally — only where you mean a real connection. Do **not** bracket every
  keyword; noise weakens ranking.
- Use `supersedes` when you write a record that makes an older one obsolete — this is
  how you keep stale memories from polluting future context.
- An unknown prefix (e.g. `[[needs::X]]`) is treated as a plain `relates` link.
- Links to a title that doesn't exist yet are kept *dangling* and resolve automatically
  once that memory is created. Links inside code fences/spans are ignored.
- **Reading:** `search` results carry `backlinks` (authority) and `superseded` (stale)
  evidence. `get_memory` returns `linked_from` — each entry includes its `rel`, so a
  memory marked `supersedes` by another is visibly stale.

---

## Exploring & maintaining the link graph

Three read tools let you navigate and repair the connections between memories.

### `graph` — traverse the link graph

Returns the typed `[[wiki-link]]` edges (not the taxonomy hierarchy).

- **Ego mode** — pass `center` (a memory id) to get that memory's neighborhood out to `depth` hops (default 2, max 4), edges traversed both directions. Use to answer "what does this connect to?" or to pull a `depends-on` subtree.
- **Corpus mode** — omit `center` for the whole graph, optionally scoped to a `taxonomy` prefix.

Dangling links appear as nodes with `dangling: true`. Node `authority` (inbound boosting edges) and `superseded` flags match how `search` ranks.

### `edge_health` — link-graph readout

Total / resolved / dangling link counts plus the top unresolved targets. A high dangling count means links are being made but titles are mistyped (or point at not-yet-written memories); a near-zero total means linking is being skipped. Run it as a maintenance check.

### `suggest_links` — find missing connections

The inverse of dangling links: memory pairs with strong text overlap but **no** edge between them. Use it to discover connections the graph is missing, then add a link by `update_memory`-ing the source's content with `[[Target Title]]`. Returns `source`/`target` pairs with a similarity `score` (higher = stronger candidate).

---

## Update procedure

Use `update_memory` to change fields on an existing memory without losing its ID.

**Fields you can update:** `title`, `content`, `taxonomy`

Pass only the fields you want to change — omit the rest. The FTS index and `[[...]]` link graph update automatically.

Pass `agent` here too, same as `write_memory` — each revision reports its own agent, so an edit you make is attributed to you even if a different model wrote the original.

---

## Delete

Use `delete_memory` to permanently remove a memory by ID.

---

## Token budgets

| tool           | default budget | purpose                     |
|----------------|----------------|-----------------------------|
| `search`       | ~6 000 tokens  | full content retrieval      |
| `memory_scope` | ~2 000 tokens  | lightweight title browse    |

When `omitted_count > 0`: narrow the query or taxonomy to get a focused slice.

---

## Maintenance

- `rebuild_fts` — rebuilds the FTS5 search index from scratch. Run if `catalog` shows a taxonomy but `search` returns nothing for it.

---

## Error handling

rekam returns structured errors with context:

| code               | meaning                                    | action                                        |
|--------------------|--------------------------------------------|-----------------------------------------------|
| `auth_failed`      | unknown or invalid API key                 | check key configuration                       |
| `write_not_allowed`| identity lacks write permission            | request write access from admin               |
| `missing_fields`   | required fields absent                     | error includes list of missing fields         |
| `missing_taxonomy` | taxonomy not provided                      | taxonomy is required for scope                |
| `invalid_taxonomy` | bad dot-notation or format                 | error includes current catalog                |
| `invalid_field`    | field value is invalid                     | e.g. title cannot be empty                    |
| `no_changes`       | update called with nothing to change       | include at least one field                    |

Errors are part of the contract — self-correct from them.
