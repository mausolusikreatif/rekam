# Specs

These describe expected behavior from the point of view of whoever is
outside the server — a browser, an app, a script, an MCP client, an AI agent.
They are written to state what should be true; a test under `internal/*`
(or `internal/mcp`) verifies the running server actually does that.

**Rule: never derive a spec from reading the implementation.** If a scenario
here turns out not to match the code, that's either a bug to fix or a
behavior change to negotiate — not a reason to edit the spec to match what
the code does. Specs only change when the intended behavior changes.

Each scenario has a stable ID (e.g. `MEM-04`, `OAUT-14`). Tests reference the
ID in a comment (`// MEM-04`) so a failing test points back to the exact
sentence it's checking, and so a spec change makes it easy to find what
needs re-verifying. A file's own tests carry a `// Scenarios: spec/x.md
(PREFIX-01..NN)` header comment naming everything it covers.

- [memory.md](memory.md) — `MEM` — core CRUD for a memory (title, content, taxonomy)
- [revisions.md](revisions.md) — `REV` — version history and restore
- [search-taxonomy.md](search-taxonomy.md) — `SRCH` — full-text search, catalog, taxonomy templates
- [links-graph.md](links-graph.md) — `LINK` — typed `[[wiki-links]]`, the link graph, dangling-link healing, suggested links
- [review.md](review.md) — `RVW` — spaced-repetition (SM-2) review deck
- [files.md](files.md) — `FILE` — uploading and fetching file attachments
- [export.md](export.md) — `EXPT` — bulk/single-record export as Markdown
- [identity.md](identity.md) — `IDNT` — signup, login, sessions, password recovery
- [teams.md](teams.md) — `TEAM` — shared workspaces, membership, roles
- [oauth-discovery.md](oauth-discovery.md) — `OAUD` — `.well-known` OAuth metadata
- [oauth-registration.md](oauth-registration.md) — `OAUR` — RFC 7591 dynamic client registration
- [oauth-authorize.md](oauth-authorize.md) — `OAUA` — the "connect to rekam" login + workspace-picker flow
- [oauth-token.md](oauth-token.md) — `OAUT` — exchanging a code (+ PKCE) for an access token
- [admin.md](admin.md) — `ADMN` — the admin console: account roster, grants, stats
- [docs.md](docs.md) — `DOCS` — the anonymous read-only documentation mirror
- [scope.md](scope.md) — `SCOP` — browsing a taxonomy branch, and the read/write scope model
- [replication.md](replication.md) — `REPL` — Litestream object-storage backup and write-lease coordination
- [mcp.md](mcp.md) — `MCP` — the MCP server's tools, transports, and agent-facing behavior
- [editions.md](editions.md) — `EDTN` — which of the above surfaces each of the three binaries actually serves

Some scenarios only apply to some editions. `editions.md` is the contract
for which; the specs whose surface is split — `admin.md`, `teams.md`,
`identity.md` — each open with an "Editions" note saying which of their own
scenarios belong to which build. A scenario with no such note is core, and
holds everywhere.

## Known gaps

Scenarios below describe *intended* behavior, not what's currently
implemented — the code has a bug or missing enforcement relative to the
rest of the system's conventions. Flagged during the spec-writing pass
rather than encoded as correct:

- **`replication.md`'s GAPs section** — `internal/db/replicate_s3.go` (S3/R2/
  MinIO wiring) had zero test coverage before this pass beyond one new
  bucket-required check; `cmd/rekam/main.go`'s replication flag wiring and
  mid-flight replica-sync failure handling are untested (would need a fake
  `ReplicaClient` or `cmd`-level test infra).
