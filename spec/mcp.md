# MCP server

The MCP server (`internal/mcp`) exposes rekam's memory store to LLM agents as
a Model Context Protocol surface (spec `2024-11-05`), speaking JSON-RPC 2.0
over three transports. It sits on top of the same `internal/engine.Engine`
the REST API uses — see `spec/memory.md`, `spec/search-taxonomy.md`,
`spec/links-graph.md`, and `spec/identity.md` for the underlying behaviors;
this file covers the protocol surface and tool contracts specifically.
Implementation: `internal/mcp/server.go`, `internal/mcp/types.go`. Tests:
`internal/mcp/server_test.go`, `internal/mcp/transport_test.go`,
`internal/mcp/agent_workflow_test.go`.

## The model

**Transports.** `ServeStdio` runs line-delimited JSON-RPC over stdin/stdout
for local clients (Claude Desktop): one JSON object per line in, one
`Response` per line out, notifications (no `id`) silently ignored. `SSEHandler`
serves remote clients over HTTP on three routes multiplexed by path and
method: `POST /mcp` is the Streamable HTTP transport (2025-03-26) used by
claude.ai and modern clients — each request is a self-contained JSON-RPC
call, re-authenticated every time; `GET /mcp/sse` + `POST /mcp/message` is
the legacy two-part SSE transport — the client opens a long-lived stream,
receives an `event: endpoint` announcement carrying a session-bound POST URL,
and all replies to messages posted there arrive asynchronously down the
stream rather than in the POST's own response body. `OPTIONS` on either path
answers a CORS preflight.

**Authentication.** Every transport requires a bearer API key. Stdio takes it
out of band (`REKAM_API_KEY` env or `--key` flag) and binds it to the whole
process's context. Streamable HTTP reads `Authorization: Bearer <key>` on
every single POST — there is no session that outlives a request, so a
revoked or malformed key is caught immediately even mid-conversation. SSE
reads the bearer once, at the `GET /mcp/sse` handshake, and binds it to a
server-side `session` keyed by a generated `session_id`; subsequent
`POST /mcp/message?session_id=...` calls resolve the key from that session
rather than needing their own `Authorization` header (a fallback path lets a
POST carry its own bearer instead, for direct testing without an open
stream). An `X-Rekam-Team` header, present on either HTTP transport, rebinds
the bearer to a specific team's workspace for that call via
`Engine.BearerForTeam` — see WORKSPACE scenarios below and `spec/identity.md`
for team/workspace semantics generally.

**The tool list.** Fifteen tools, all declared in `buildTools()`, each
carrying a JSON Schema `inputSchema` and `Annotations` (`readOnlyHint` /
`destructiveHint`) that MCP clients use to decide whether to prompt for
confirmation before calling it.

- *Discovery*: `catalog` browses the taxonomy tree (shallow, with counts;
  drill in with `prefix`); it mirrors the `rekam://catalog` resource
  (read-only, cacheable top-level tree) and the `rekam://taxonomy-template`
  resource (the corpus's intended branches, read before filing new records).
- *Read*: `search` (FTS5 full-text, ranked with link authority, scoped by
  `taxonomy`, capped by `token_budget`), `get_memory` (single record by id,
  full content plus `linked_from`), `memory_scope` (id+title listing within a
  taxonomy prefix, no content — browsing rather than searching).
- *Write*: `write_memory` (create; `title`+`taxonomy` required), `update_memory`
  (patch given fields only; optional `base_version` for optimistic
  concurrency), `delete_memory` (permanent; tombstones the title/who/when).
- *History*: `memory_history` (revisions newest-first), `restore_revision`
  (roll back by copying an old version forward as a new one — itself
  recorded, so a bad restore can be restored away from too),
  `deleted_memories` (tombstones for ids/links that no longer resolve).
- *Graph*: `graph` (typed `[[wiki-link]]` traversal — ego graph around
  `center` out to `depth` hops, or the whole corpus/taxonomy without a
  center; see `spec/links-graph.md`), `edge_health` (link total/resolved/
  dangling counts plus which records hold the dangling links), `suggest_links`
  (text-similarity pairs that look related but aren't yet linked).
- *Workspace*: `list_teams` (workspaces this connection can act in, with
  role; see `spec/identity.md`).
- *Maintenance*: `rebuild_fts` (rebuild the search index from scratch;
  destructive-flagged though non-lossy — see MCP-13).

**Tool call behavior.** `tools/call` decodes `{name, arguments}`, looks up
the handler, and invokes it with the request's bound API key. A handler
failure never becomes a JSON-RPC transport error — `handleToolCall` always
returns a normal `result` with `ToolResult{IsError: true, Content: [...]}`,
so the agent reads the reason from tool content and can retry or explain
itself rather than treating it as a broken connection. When the underlying
error is an `engine.ActionableError` (`{code, message, context}` — e.g.
`version_conflict` with the current version in `context`, or `not_a_member`),
that structured payload is serialized as the tool result's text verbatim,
giving the agent a machine-checkable `code` to branch on, not just prose.
Only protocol-level problems — malformed JSON-RPC params, an unknown method,
an unknown tool name, an unreadable resource URI — are real JSON-RPC errors
(`error` field, standard codes from `types.go`: `CodeParse`, `CodeInvalid`,
`CodeNotFound`, `CodeParams`, `CodeInternal`).

**Agent-workflow behaviors.** The `initialize` handshake's `instructions`
field (constant `mcpInstructions`) teaches a fresh agent the retrieval →
write → link → update loop rekam expects, and is dynamically extended with
the caller's skill manifest (`Engine.GetSkillsManifest`) when any skills
exist, so an agent's very first turn can already see what's available to
load. This is the main way the server shapes multi-turn tool-use patterns:
discover via `catalog`/`search`/`memory_scope`, read with `get_memory`, write
with inline `[[Title]]` links, and keep the graph connected with
`suggest_links`/`edge_health` rather than leaving memories as islands.

## MCP-01: Initializing returns the protocol handshake and usage instructions
Given any transport and a valid bearer, when the client sends `initialize`,
then the result carries `protocolVersion: "2024-11-05"`, `serverInfo`, tool
and resource `capabilities`, and a non-empty `instructions` string teaching
the retrieval/write/link workflow.

## MCP-02: The tool list enumerates every tool with a schema
Given a `tools/list` call, then the result's `tools` array is non-empty and
includes (at minimum) `catalog`, `search`, `get_memory`, `write_memory`,
`update_memory`, and `delete_memory`, each with a `name`, `description`, and
JSON Schema `inputSchema`.

## MCP-03: Discovery resources are advertised alongside the tools
Given a `resources/list` call, then the result includes both
`rekam://catalog` (the taxonomy tree) and `rekam://taxonomy-template` (the
corpus's intended filing branches); reading either via `resources/read`
returns its JSON payload as `text/application/json` content, and reading an
unknown URI is a `CodeNotFound` JSON-RPC error.

## MCP-04: `catalog` with no arguments rolls up to top-level branches
Given memories filed under a multi-segment taxonomy path (e.g.
`finance.accounts`), when `catalog` is called with no `prefix`, then the
result's `taxonomy` key lists the top-level branch (`finance`) with a count,
not the full leaf path; passing `prefix: "finance"` drills in and reveals
`finance.accounts`.

## MCP-05: `search` finds a written memory scoped by taxonomy
Given a memory just written into `finance.accounts`, when `search` is called
with `q` matching its content and `taxonomy: "finance"`, then the result's
`results` includes that memory; a call whose `q` matches nothing returns an
empty `results` list, not an error.

## MCP-06: An unknown tool name is a JSON-RPC error, not an in-band one
Given `tools/call` naming a tool not in the registry, then the response
carries a top-level JSON-RPC `error` (protocol-level `CodeNotFound`) — unlike
a tool that runs but fails, which reports in-band per MCP-16.

## MCP-07: A syntactically valid but unrecognized API key fails in-band
Given a bearer that parses as a key but matches no identity, when a tool is
called, then the transport lets the request through (auth is the engine's
job, not the transport's) and the tool result comes back with
`isError: true` — the caller never gets a bare 200 with fabricated data.

## MCP-08: Writing, then recalling in full, then revising is one continuous loop
Given a memory written via `write_memory`, then `get_memory` in a later call
returns its full `content` (not a snippet); `update_memory` with only
`content` set changes the body while `update_memory` with only `title` set
leaves the existing content untouched (a partial patch, not a replace); and
each successful update advances `version`.

## MCP-09: `base_version` makes a concurrent edit an explicit, recoverable conflict
Given two callers who both read the same memory's `version`, when the first
one's `update_memory` lands and the second submits `base_version` set to the
now-stale version, then the second update is refused in-band with
`code: "version_conflict"` (per the `engine.ActionableError` shape) and the
first writer's content is left intact; omitting `base_version` instead opts
into last-writer-wins and always succeeds.

## MCP-10: A bad edit is recoverable via history and restore
Given a memory with a since-corrected edit, `memory_history` lists both
revisions newest-first; `restore_revision` with an earlier version number
copies that revision's fields forward as a new version (append-only — the
bad edit stays in history rather than being erased) and the memory's content
reflects the restored text; calling `restore_revision` without `version` is
an in-band error whose message names the missing field.

## MCP-11: Deletion is terminal but leaves a tombstone
Given a memory deleted via `delete_memory`, then `get_memory` and `search`
both fail to surface it (get_memory in-band errors; search simply omits it),
while `deleted_memories` lists a tombstone naming its title, deleter, and
timestamp; calling `delete_memory` again on the same id is an in-band error,
not a silent success.

## MCP-12: The link graph can be traversed and its gaps found and healed
Given a memory whose content links `[[Postgres]]` (existing) and `[[Redis]]`
(not yet written), then `graph` centered on that memory at `depth: 1`
includes Postgres in the neighborhood; `edge_health` reports exactly one
dangling link and names both the missing target ("redis") and the record
holding the broken link; writing a memory titled "Redis" afterward heals the
link — a subsequent `edge_health` shows zero dangling and both links
resolved, without the source record having been touched.

## MCP-13: Rebuilding the search index is lossless
Given memories already indexed and findable via `search`, when `rebuild_fts`
is called, then every previously-findable memory is still findable
afterward with the same result count — the tool is flagged
`destructiveHint` for client-side confirmation even though it does not lose
or alter any data, only rebuilds derived index state.

## MCP-14: `suggest_links` always answers with a list, even when uncertain
Given two unlinked memories with overlapping text, `suggest_links` returns a
`suggestions` key (present, possibly empty) rather than omitting it or
erroring — a caller can always branch on its presence.

## MCP-15: `memory_scope` browses a taxonomy branch without leaking siblings
Given memories filed under `work.ops` and others under `finance.accounts`,
`memory_scope` with `taxonomy: "work.ops"` lists only the `work.ops` titles
(id+title, no content) and never includes the `finance.accounts` record.

## MCP-16: A tool call that fails reports the reason in-band, never as a transport error
Given any tool invoked with arguments the engine rejects (missing required
field, malformed taxonomy path, unsupported `format`, an id that does not
exist), then `tools/call` still returns a normal JSON-RPC `result` (never a
top-level `error`) with `ToolResult.isError: true` and a non-empty message
the agent can read and act on.

## MCP-17: A read-only identity is refused write tools but keeps read access
Given an identity created without write permission, calling `write_memory`,
`update_memory`, or `delete_memory` is refused in-band for each, while
`catalog` (and other read tools) still succeed — the restriction is per
capability, not a blanket lock on the connection.

## MCP-18: A non-markdown format round-trips unchanged
Given `write_memory` called with `format: "mermaid"` and diagram content,
then the created memory's `format` is `"mermaid"` (not coerced to the
markdown default) and `get_memory` returns the same format and content
verbatim.

## MCP-19: `list_teams` and workspace isolation
Given a connection's bearer belongs to an identity that owns or has joined a
team, `list_teams` names that workspace with the caller's role in it (see
`spec/identity.md`); writing into the workspace (via a team-bound bearer, or
an `X-Rekam-Team`-selected request — MCP-21/22) never appears in that
identity's personal-memory search, and vice versa.

## MCP-20: Notifications are acknowledged without a reply, on every transport
Given a JSON-RPC message with no `id` (a notification, e.g.
`notifications/initialized`), then stdio processes it without emitting a
line, and Streamable HTTP responds `202 Accepted` with an empty body — never
a JSON-RPC response frame, since none was requested.

## MCP-21: Streamable HTTP re-authenticates every request and completes a full session
Given a remote client using only `POST /mcp`, it can complete `initialize`
(response carries an `Mcp-Session-Id` header the client may echo back, though
the server does not require it), `tools/list`, and a `tools/call` write
followed by a `search` that finds it — all as independent, individually
authenticated POSTs. A later POST with no `Authorization` header on the same
logical session is refused with `401`, and one with an unrecognized bearer
is let through to the tool layer and fails in-band (per MCP-07) — the
distinction is missing vs. merely invalid credentials.

## MCP-22: `X-Rekam-Team` scopes a Streamable HTTP request to that workspace
Given a request carrying `X-Rekam-Team: <team id>` alongside a valid bearer,
then `teamBoundKey` rebinds the bearer for that call via
`Engine.BearerForTeam`, and a `write_memory`/`search` pair with the header
set lands in and finds results from that team's corpus; the same `search`
without the header returns the caller's personal memory instead, with no
overlap between the two.

## MCP-23: The legacy SSE transport completes a call across its two-part exchange
Given a client that opens `GET /mcp/sse` with a valid bearer, then it
receives `event: endpoint` followed by a `data:` line carrying a
session-bound `/mcp/message?session_id=...` URL; posting a JSON-RPC request
to that URL gets only a `202 Accepted` acknowledgment on the POST itself,
while the actual JSON-RPC response (matching the request's `id`) arrives
asynchronously as an `event: message` frame on the still-open stream.

## MCP-24: The SSE transport rejects missing credentials and unknown sessions
Given `GET /mcp/sse` with no `Authorization` header, the connection is
refused with `401` before any session is created; given
`POST /mcp/message?session_id=<value that was never issued>`, the request is
refused with `401` rather than falling back to an anonymous or default
session.

## MCP-25: CORS preflight succeeds so browser-based clients can connect at all
Given an `OPTIONS` request to `/mcp`, then the response is `204` with
`Access-Control-Allow-Origin: *` and `Access-Control-Allow-Headers`
permitting both `Authorization` and `X-Rekam-Team` — a browser client that
needs to set either header on the real request must see it allowed here
first, or it never attempts the call.

## MCP-26: Malformed input and unrecognized routes fail predictably
Given a `POST /mcp` body that is not valid JSON, the response is `400` with
a JSON-RPC parse error; given a well-formed request naming a method the
server does not implement, the response is `200` with a JSON-RPC `error`
(`CodeNotFound`); given a request to a path the transport does not serve at
all (e.g. `/mcp/nope`), the response is a plain `404`.
