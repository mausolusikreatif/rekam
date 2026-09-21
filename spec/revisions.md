# Revisions

Version history and restore for a memory. `internal/db/memory.go` snapshots
one `revisions` row per committed version (including the current one, so
history is a complete series rather than an undo log) and offers an
optional compare-and-swap on `Update` so a caller editing a stale copy is
told rather than silently overwritten. `internal/engine/engine.go`'s
`MemoryHistory` and `RestoreRevision` add the read/write-scope checks and
actionable-error shape. See spec/memory.md for the CRUD paths that create
these revisions and for what happens to history on delete.

## REV-01: Creating a memory records its first revision
Given a `POST /memory` that succeeds,
when its history is read back,
then the memory is at `version: 1` and `GET /memory/{id}/revisions` shows
exactly one revision at version 1, attributed to the writer, matching the
memory's title/content/taxonomy/format as written.

## REV-02: Updating a memory bumps its version and snapshots a revision
Given an existing memory,
when it is edited via `PATCH /memory/{id}`,
then its `version` increments by exactly one and a new revision is
recorded at that version, attributed to whoever made the edit — the
overwritten text remains readable in the prior revision, not discarded.

## REV-03: A caller can list a memory's full history
Given a memory with two or more versions,
when they `GET /memory/{id}/revisions`,
then the response is `200` with a `revisions` list ordered newest version
first, one entry per version including the current one.

## REV-04: A caller can fetch one specific version
Given a memory with multiple versions,
when the engine looks up a particular `version` (via `RevisionAt`),
then it gets back exactly that version's title/content/taxonomy/format;
asking for a version number that was never committed is an error.

## REV-05: An update against a stale base version is rejected
Given a memory at version N and an update whose `base_version` names an
earlier version than N,
when the update is applied,
then it is rejected with a version conflict (see spec/memory.md MEM-11)
naming both the version the caller edited against and the version
actually stored — and neither the row nor its revision history advances.

## REV-06: Concurrent updates against the same base — exactly one wins
Given several writers who all read the same base version and race to
update it with a matching `base_version`,
when their updates run concurrently,
then exactly one commits (advancing the version by one and adding one
revision) and every other one is rejected as a version conflict — never
two commits, never zero.

## REV-07: Omitting base_version is last-writer-wins
Given an update with no `base_version` set,
when it is applied even though the memory has since moved past the
version the caller last read,
then it succeeds unconditionally (no conflict), still recording a new
revision — this is what pre-versioning clients and single-user callers
get, and the overwritten text stays recoverable in history either way.

## REV-08: Every revision is attributed to who wrote it
Given a memory created and then edited by different identities,
when its history is read,
then each revision's `author_id` matches the identity that actually
committed it (the create) or made that particular edit (each update).

## REV-09: Restoring a version writes it forward as a new version
Given a memory at version N whose version K < N is a prior state,
when they `POST /memory/{id}/restore/{K}`,
then the response is `200` with the memory's title/content/taxonomy/format
reverted to what version K held, but at version `N+1` — history is
append-only, so a restore is itself a new, auditable revision (attributed
to whoever restored it) rather than a rewind, and the version that was
just replaced (N) is still readable in history.

## REV-10: Restoring a version that was never committed is rejected
Given a `POST /memory/{id}/restore/{version}` naming a version number the
memory never had,
when the request is made,
then the response is `400` or greater (not a silent no-op); a
non-numeric `{version}` path segment is specifically `400`.

## REV-11: History is readable to a viewer; restoring requires write access
Given a caller with a read-only role (e.g. team Viewer) who can read a
memory,
when they `GET /memory/{id}/revisions` (read-only, so allowed) versus
`POST /memory/{id}/restore/{version}` (a write, so it isn't),
then the history read succeeds while the restore is rejected — restoring
is exactly as gated as any other edit to the memory (spec/memory.md
MEM-10, MEM-20).

## REV-12: History and restore on a deleted memory report the tombstone
Given a memory that has since been deleted,
when they `GET /memory/{id}/revisions` or
`POST /memory/{id}/restore/{version}`,
then the response is `410` with `code: "deleted"` and the tombstone,
because deletion purges the revision rows themselves — there is no
history left to show or restore from (spec/memory.md MEM-12).

## REV-13: A pre-versioning memory is backfilled with a seeded first revision
Given a `*.memory` file written before the `version`/`revisions` schema
existed,
when it is opened (migration runs),
then every existing memory gains `version: 1` and exactly one revision
row seeded from its current columns, with `author_id` left empty (the
original writer predates authorship tracking and is genuinely unknown);
a subsequent edit continues the series from version 2, not colliding with
the seed.

## REV-14: Revision routes require authentication
Given a request to `GET /memory/{id}/revisions` or
`POST /memory/{id}/restore/{version}`,
when it carries no bearer token or one that doesn't resolve to an
identity,
then the response is `401`.

## REV-15: Deleting a memory purges its revision history
Given a memory with more than one version,
when it is deleted (spec/memory.md MEM-12),
then `GET /memory/{id}/revisions` no longer has anything to return — the
history is destroyed along with the content, not merely hidden — and only
the tombstone (title, taxonomy, `final_version`) survives as a record of
how much history existed at the time.

## REV-16: A write may report which agent made it
Given a `POST /memory` or `PATCH /memory/{id}` whose body includes an
`agent` field (e.g. `"claude-opus-5"`),
when its history is read back,
then the resulting revision's `agent` matches what was sent. Omitting
`agent` leaves it empty on that revision — not a distinct "unknown"
marker, just the same value a human editing directly in the web UI
already produces, since there is no model to report there. This value is
self-reported by whoever made the call (the same trust model as a git
commit's `Co-Authored-By` trailer, not a cryptographic guarantee) —
`author_id` is who committed the version, `agent` is what they say was
driving it.
