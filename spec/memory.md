# Memory

Core CRUD for the record type rekam is built around — a memory is a
title + content + `taxonomy` (dot-notation classification path) plus a
`format` discriminator. `internal/db/memory.go` owns the SQLite schema and
low-level queries; `internal/engine/engine.go`'s `WriteMemory`,
`UpdateMemory`, `DeleteMemory`, `GetMemory`, `ListMemories` add validation,
scope enforcement, and the actionable-error shape the HTTP layer serves
verbatim. Deletion is permanent and leaves a tombstone rather than a
recoverable trash can — see spec/revisions.md for version history and
restore, and spec/search-taxonomy.md for `/search`, `/catalog`, and the
taxonomy template that `taxonomy` values are expected to file into.

## MEM-01: A caller can create a memory
Given an access token with write permission,
when they `POST /memory` with a JSON body carrying `title`, `taxonomy`,
and optionally `content` and `format`,
then the response is `201` with the created memory, including a
server-assigned `id`, `created_at`/`updated_at`, and `version: 1`.

## MEM-02: Creating a memory requires a title and a taxonomy
Given a `POST /memory` body missing `title`, `taxonomy`, or both,
when the request is made,
then the response is `400` with `code: "missing_fields"` and a
`missing_fields` list naming exactly what was left out.

## MEM-03: A memory's taxonomy must be dot-notation
Given a `POST /memory` (or `PATCH .../taxonomy`) body whose `taxonomy` does
not match `^[a-zA-Z0-9_]+(\.[a-zA-Z0-9_]+)*$` (e.g. contains a space or
punctuation),
when the request is made,
then the response is `400` with `code: "invalid_taxonomy"`, carrying the
current catalog in `context` so the caller can see valid branches instead
of guessing.

## MEM-04: A memory's format is restricted to a closed set
Given a `POST /memory` or `PATCH /memory/{id}` body with a `format` outside
`markdown`, `mermaid`, `code`, `table` (case-insensitive; empty defaults to
`markdown`),
when the request is made,
then the response is `400` with `code: "invalid_format"` and the accepted
list in `context.allowed_formats`; a recognized format is normalized to
lowercase and stored as given.

## MEM-05: A caller can fetch a memory by id
Given a memory the caller can read,
when they `GET /memory/{id}`,
then the response is `200` with the full memory, including `backlinks`
(other memories linking to it — see spec/links-graph.md) and `in_review`
(whether it's enrolled in the spaced-repetition deck — see spec/review.md).

## MEM-06: Fetching an unknown id is a plain 404
Given an id that was never used,
when they `GET /memory/{id}`,
then the response is `404` — distinct from a deleted id's `410` (MEM-12),
so the two cases stay tellable apart.

## MEM-07: A partial update changes only the fields given
Given an existing memory and a `PATCH /memory/{id}` body naming a subset of
`title`, `content`, `taxonomy`, `format`,
when the request is made,
then the response is `200` with those fields changed and every omitted
field left exactly as it was.

## MEM-08: A memory's title cannot be updated to empty
Given a `PATCH /memory/{id}` body with `title` set to `""` or
all-whitespace,
when the request is made,
then the response is `400` with `code: "invalid_field"` and the memory is
unchanged.

## MEM-09: An update naming no fields is rejected
Given a `PATCH /memory/{id}` body with none of `title`, `content`,
`taxonomy`, `format` set,
when the request is made,
then the response is `400` with `code: "no_changes"`.

## MEM-10: Moving a memory requires write access to the destination branch
Given a caller with write access to a memory's current taxonomy but not to
the taxonomy named in a `PATCH .../taxonomy` update,
when the request is made,
then the response is `403` with `code: "taxonomy_forbidden"` and the
memory stays in its original branch.

## MEM-11: A stale update is rejected as a version conflict
Given a memory at version N and a `PATCH /memory/{id}` body carrying
`base_version` set to an older version than the one currently stored,
when the request is made,
then the response is `409` with `code: "version_conflict"` and
`context.current_version` set to the real current version, so the caller
can re-read and retry without a second round trip; omitting
`base_version` entirely skips the check (last-writer-wins). See
spec/revisions.md REV-05..REV-07 for the underlying compare-and-swap.

## MEM-12: Deleting a memory is permanent and reserves its id
Given a memory the caller can write,
when they `DELETE /memory/{id}`,
then the response is `200`; the memory's content and revision history are
gone, but the id is burned forever — a subsequent `GET /memory/{id}`
returns `410` (not `404`) with `code: "deleted"` and a `tombstone` naming
the title, taxonomy, deleter, and deletion time.

## MEM-13: Deleting an already-deleted memory reports the existing tombstone
Given an id that was already deleted,
when they `DELETE /memory/{id}` again,
then the response is `410` with the original tombstone — deleting a burned
address is not treated as a no-op success, and no second tombstone is
created.

## MEM-14: A caller can review what was deleted
Given one or more memories the caller has deleted,
when they `GET /deleted`,
then the response is `200` with a `deleted` list of tombstones — title,
taxonomy, `deleted_by`, `deleted_at`, and `final_version` — newest first.

## MEM-15: The deleted list can be scoped to a taxonomy branch
Given tombstones across more than one taxonomy branch,
when they `GET /deleted?taxonomy=work`,
then the response includes only tombstones whose taxonomy is `work` or a
sub-branch of it (`work.*`).

## MEM-16: Listing memories is paginated with an accurate total
Given more memories than one page,
when they `GET /memories?limit=N&offset=M`,
then the response is `200` with up to `N` memories starting at offset `M`
and a `total` that counts the whole matching set, not just the page — so a
client can page through every record exactly once and render "showing X
of total".

## MEM-17: Listing can be narrowed to a taxonomy branch
Given memories across sibling branches,
when they `GET /memories?taxonomy=work`,
then the response includes memories in `work` and any `work.*`
sub-branch, and excludes every sibling branch (e.g. `finance.*`).

## MEM-18: An empty or unknown branch lists as empty, not an error
Given a taxonomy prefix with no memories filed under it,
when they `GET /memories?taxonomy=nonexistent`,
then the response is `200` with an empty `memories` list — not a `404`.

## MEM-19: Listing rejects invalid paging parameters
Given a `limit` or `offset` that is negative or not an integer,
when they `GET /memories?limit=...` or `?offset=...`,
then the response is `400`, rather than the parameter being silently
coerced to some default.

## MEM-20: Every memory route requires authentication
Given a request to any `/memory`, `/memory/{id}`, `/memories`, or
`/deleted` route,
when it carries no bearer token or one that doesn't resolve to an
identity,
then the response is `401` — anonymous callers get nothing, not even an
empty list.

## MEM-21: A tenant file has a storage ceiling, independent of any plan
Given a tenant file (personal or team, any plan) already at or over a
fixed size on disk,
when a `POST /memory` or `PATCH /memory/{id}` would grow it further,
then the response is rejected with `storage_limit`, naming the limit
and the current size. This is an abuse backstop protecting the disk
from one runaway account, not a billing feature — the ceiling is
generous relative to ordinary use and applies uniformly regardless of
plan. Disk usage counts the tenant's WAL/SHM sidecar files alongside
its main file, since WAL mode can leave a recent write sitting in
`-wal` before checkpointing.

## MEM-22: A title cannot collide with another live memory's title
Given a memory already titled "Alpha" (any taxonomy — the check is
tenant-wide, matching how `[[Alpha]]` actually resolves, not scoped to
one branch), when a caller `POST /memory`s a new memory titled "Alpha",
or `PATCH /memory/{id}` renames a different memory to "Alpha", then the
write is rejected with `title_conflict` naming the colliding title and
the id of the memory that already holds it — nothing is written. The
comparison is case- and whitespace-insensitive, the same normalization
`syncEdges` uses to resolve `[[wiki-links]]`, so this check and that
resolution never disagree about what counts as the same title. Renaming
a memory to its own current title (no-op) is not a conflict. A deleted
memory's burned title does not block reuse — only a *live* memory's
title collides.

## MEM-23: Deleting a memory with inbound links is refused unless forced
Given a memory that one or more other live memories link to (any
`rel`), when a caller `DELETE /memory/{id}` without `?force=true`, then
the response is rejected with `has_inbound_links`, naming the count —
deleting would otherwise leave every one of those links pointing at a
burned id with no recovery, exactly the outcome `supersedes` exists to
avoid (see `spec/links-graph.md`'s model section). `DELETE
/memory/{id}?force=true` deletes anyway, identically to MEM-12. A
memory with no inbound links deletes normally with no flag needed.

## MEM-24: Media a record links to is fetched by the reader, not stored
A record's content may name an image or a clip by URL. rekam stores the
record, not the bytes: the fetch happens in the reader's browser, from
whoever hosts the file, when they open the record. Three consequences,
all of them things a reader must be able to tell apart from a bug.

A pasted image or clip URL alone on its own line renders as the media,
the same way a bare YouTube or Vimeo URL renders as a player — that is
how people write, and `![alt](url)` keeps working for anyone who
prefers it. A URL that someone gave link text on purpose stays a link;
so does a URL whose path names no media type, which is what a Google
Drive or Dropbox *share* link is — those serve a web page, and turning
one into an `<img>` would produce a permanently broken image.

An externally-hosted image that fails to load is replaced by a card
naming the host it was on, a way to open it, and the fact that rekam
links to it rather than storing it. The browser's broken-image icon
reads as rekam failing; for a share link it is not even a failure, it
is the correct final state.

Nothing externally hosted may leak the reader's context to its host:
external images carry `referrerpolicy="no-referrer"`, so opening a
record does not tell that server which page the reader was on.
Uploaded attachments (`spec/files.md`) are served from rekam's own
origin and are not treated as external.

The corollary worth stating plainly, because it is the part a user has
to know: **a linked image can be visible to the person who wrote the
record and invisible to a teammate**, since access is decided by the
host against the reader's own account. rekam cannot see that, cannot
fix it, and does not pretend the record is self-contained.
