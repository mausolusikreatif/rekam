# Links & graph

Memory content can carry typed `[[wiki-links]]` to other memories by title.
`internal/db/links.go` parses them at write time, `internal/db/memory.go`'s
`syncEdges` resolves them into rows of the `edges` table (one row per
outgoing link, `dst_id` NULL when the target title doesn't exist yet —
"dangling"), `internal/db/graph.go` slices that table into a corpus or
ego-centric graph for the mindmap view, `internal/db/suggest.go` proposes
links the graph is missing, and `internal/db/memory.go`'s `EdgeHealth`
reports adoption/health stats. See `spec/memory.md` for the write/update
paths that trigger link resolution, `spec/teams.md` for the taxonomy-scope
model (`Scope`) that graph and suggestions must respect.

## The model

A link is written as `[[Title]]` or, typed, `[[rel::Title]]` where `rel` is
one of a fixed vocabulary: `relates` (default, also the fallback for an
unrecognized prefix), `depends-on`, `supersedes`, `contradicts`. The bracket
target is normalized (lowercased, whitespace-collapsed, trimmed) and matched
against other memories' titles under the same normalization. Links inside
fenced (` ``` `) or inline (`` ` ``) code spans are not parsed, so
documentation about the syntax itself never creates real edges. Duplicate
`(rel, target)` pairs within one memory collapse to one edge.

Every parsed link becomes an `edges` row (`src_id`, `dst_id` or NULL, `raw`
target text, `rel`), kept in sync on every insert and update. A resolved edge
into a later-deleted memory stays bound to that memory's burned id — a
tombstone reference — rather than reverting to dangling or being silently
re-homed onto a new memory that reclaims the same title.

`depends-on` and `relates` edges count as inbound "authority" for search
ranking and the graph's per-node `Authority` count; `supersedes` additionally
flags the target `Superseded`; `contradicts` carries neither signal. These
ranking effects are documented in full in `spec/search-taxonomy.md`; here
they matter only insofar as they shape `GraphNode.Authority` /
`.Superseded`.

The link graph (`GET /graph`) is either a **corpus graph** (no `center`:
every memory, optionally filtered to a taxonomy prefix, with all resolved
and dangling edges among them) or an **ego graph** (`center` set: an
undirected BFS out to `depth` hops, capped at 4). Dangling links surface as
synthetic "ghost" nodes (`Dangling: true`, id `"dangling:" + normalized
target`) so not-yet-written targets are visible in the mindmap. Deleted
targets surface as leaf "tombstone" nodes (`Deleted: true`) that can be
pointed at but never traversed through. Under a restricted `Scope`, a
title-visible-but-unreadable memory also becomes a leaf (`HiddenContent:
true`, no `Authority`); a fully unreadable memory is erased from the graph
entirely, including as a traversal hop, so it can never bridge two memories
the caller can see.

`GET /suggest-links` proposes memory pairs with strong title/content textual
overlap (via FTS) that have no edge between them yet in either direction —
the inverse of a dangling link.

`GET /admin/edge-health` (despite the `/admin/` path, gated like any other
authenticated route — not `requireAdmin`) reports store-wide edge counts:
total/resolved/dangling, distinct dangling target titles, a dangling
breakdown by relation type, and the top dangling targets each with the
source memories that reference it.

## LINK-01: A bare wiki-link resolves to an existing memory

Given a memory titled "Acme Onboarding" already exists,
when another memory is written with content containing `[[Acme Onboarding]]`,
then the response's `links` array reports that target resolved with relation
`relates`, and an `edges` row is created linking the two memories.

## LINK-02: A typed link is parsed by its relation prefix

When a memory's content contains `[[depends-on::Q3 Launch]]`, or
`[[supersedes::Old Plan]]`, or `[[contradicts::Earlier Note]]`,
then the resulting edge carries that relation (`depends-on`, `supersedes`,
`contradicts`) instead of the default `relates`.

## LINK-03: An unrecognized relation prefix degrades to a plain link

Given content containing `[[needs::Foo]]`, where `needs` is not one of the
four known relation types,
then the whole bracket contents (`needs::foo`) is treated as the link's
literal target text under the default `relates` relation, rather than being
rejected or silently dropped — a stray `::` in a title degrades gracefully.

## LINK-04: Links inside code spans are not parsed

Given content containing `` `[[NotALink]]` `` (inline code) or a fenced
` ``` ` block containing `[[NotALink]]`,
then no edge is created for that bracket text — only `[[Real]]` links
appearing outside code spans in the same content produce edges. This lets
documentation that discusses the `[[...]]` syntax itself avoid fabricating
edges.

## LINK-05: A link to a title that doesn't exist yet is dangling

When a memory links to a title with no matching memory,
then the write still succeeds, `links` reports that target as unresolved,
and the `edges` row is created with a NULL destination — the link is kept,
not discarded, so it can heal automatically later (LINK-06).

## LINK-06: A dangling link heals automatically once its target is written

Given a memory with a dangling link to "Q3 Launch",
when a new memory titled "Q3 Launch" is subsequently written,
then the existing dangling edge's destination resolves to the new memory
without the original memory needing to be re-saved.

## LINK-07: Editing a memory's content re-syncs its outgoing edges

Given a memory that links to "Alpha",
when its content is updated to link to "Beta" instead (dropping the
reference to Alpha),
then the edge to Alpha is removed and a new edge to Beta is created —
`edges` always reflects only the links present in the memory's current
content, never accumulating stale ones from earlier versions.

## LINK-08: Deleting a memory cascades its outgoing edges but preserves inbound ones as a tombstone reference

When a memory with outgoing links is deleted,
then its own `edges` rows (as `src_id`) are removed, but any other memory's
edge pointing at it (as `dst_id`) survives, still bound to the deleted
memory's id — not reverted to a dangling (title-based) link. Consequently,
if a new memory is later written reusing the deleted memory's exact title,
existing inbound edges keep pointing at the original (now-deleted) id and
are not re-homed onto the new memory, whether by re-resolving on read or by
re-saving the linking memory's content unchanged — a reference always means
what it meant when it was written.

## LINK-09: Self-links are excluded from backlinks

Given a memory whose own content contains a `[[link]]` back to its own
title,
then that memory does not appear in its own backlinks list — a
self-reference is parsed and stored as an edge like any other, but self
authority is not counted.

## LINK-10: The corpus graph includes every memory and both resolved and dangling edges

Given `GET /graph` with no `center`,
then the response includes a node for every memory (optionally restricted
to a `taxonomy` prefix and its descendants), an edge for every resolved
`[[link]]` between two included memories, and a synthetic dangling ghost
node (with `dangling: true`) plus edge for every unresolved link target
among them.

## LINK-11: The ego graph expands outward from a center within a depth bound

Given `GET /graph?center=<id>&depth=N` over a chain of memories A→B→C→D
linked in sequence,
then depth 1 includes A and B only, depth 2 additionally includes C but not
D — traversal follows resolved edges in both directions (an inbound link is
discovered the same as an outbound one), and any `depth` above 4 is clamped
to 4 regardless of the requested value.

## LINK-12: Centering on an unreadable or nonexistent memory looks the same

Given `center` names either a memory id that doesn't exist, or one the
caller's scope cannot read,
then `GET /graph` responds `404` in both cases, indistinguishably — a
restricted caller cannot use the graph endpoint to probe which ids exist
outside their scope.

## LINK-13: A fully hidden memory cannot bridge two readable memories in the ego graph

Given two memories the caller can read that both link only through a third
memory the caller cannot read at all (not even its title),
when the caller requests the ego graph centered on one of the readable
pair,
then the hidden memory is entirely absent from the response — not even as a
leaf — and the other readable memory, reachable only by traversing through
the hidden one, is likewise excluded: a hidden node never becomes a bridge
between two visible clusters.

## LINK-14: A title-visible memory appears in the graph as a content-hidden leaf

Given a memory in a branch the caller's scope marks title-visible (existence
disclosed, content hidden) but not readable,
when it is reachable by an edge from an included memory,
then it appears in the graph as a leaf node (`hidden_content: true`) with
its id, title, and taxonomy but with `authority` forced to zero (no ranking
signal that would leak inbound-edge counts) — the edge into it is drawn, but
no edge may be traversed through it to reach further nodes.

## LINK-15: A deleted memory appears in the graph as a non-traversable tombstone leaf

Given a memory that other memories still link to has been deleted,
when the graph includes one of those linking memories,
then the deleted target appears as a leaf node (`deleted: true`, with its
`deleted_at`/`deleted_by`) reachable by the inbound edge, but the graph
never routes through it to reach any other node.

## LINK-16: Suggested links pair memories with unlinked textual overlap

Given two memories about the same unlinked topic (sharing significant title/
content vocabulary) and a third, unrelated memory,
when `GET /suggest-links` is called,
then the top suggestion pairs the two related memories together (not the
unrelated third), each suggestion names both memories and a similarity
score, and pairs already joined by an edge in either direction are never
suggested.

## LINK-17: Suggestions never name a memory outside the caller's readable scope

Given a restricted caller whose scope excludes certain taxonomy branches
entirely,
when `GET /suggest-links` is called,
then no suggestion names a memory from an excluded branch as either the
source or the target of a pair — a suggestion is advice to link two
memories, so both ends must already be things the caller is allowed to see
and read.

## LINK-18: Edge health reports overall adoption and a breakdown of dangling links

Given a store with a mix of resolved and dangling edges, including two
different source memories that both dangle on the same missing title,
when `GET /admin/edge-health` is called,
then the response's `total_edges`/`resolved`/`dangling` counts add up
correctly, `dangling_targets` counts that shared missing title once (not
twice), and `top_dangling` lists it with both referencing source memories
(id + title) attached and ordered by how many memories reference each
missing title.

## LINK-19: Edge health respects the caller's read scope

Given a restricted caller (e.g. a team member whose `read_grants` cover only
a subset of taxonomy branches) and dangling links that reference memories
outside those branches,
when they call `GET /admin/edge-health`,
then the reported counts and `top_dangling` source memories are drawn only
from memories within the caller's own scope — the same content-disclosure
boundary LINK-13/LINK-14/LINK-17 hold everywhere else on the graph surface,
so edge health cannot be used to enumerate or name memories the caller
could not otherwise see.
