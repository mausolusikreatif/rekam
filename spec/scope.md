# Scope: browsing a taxonomy branch, and the read/write model behind it

`GET /scope` is a title-only, token-budgeted browse of one taxonomy
branch — the endpoint an agent calls to see what's already in `work.ops`
before deciding whether to read further or write something new, without
paying to load every record's content. It is deliberately small: see
`internal/api/handlers.go`'s `handleScope` and `internal/engine/engine.go`'s
`ScopeMemories`. It stays its own file (rather than folding into
`spec/admin.md` or `spec/memory.md`) because its enforcement is the same
`db.Scope` mechanism every other read/write path in the system leans on
(`internal/db/scope.go`), and that mechanism deserves scenarios of its
own — `spec/teams.md` covers how a team member comes to be granted a
particular `db.Scope` (its `read`/`write`/`titlesVisible` prefix lists);
this file covers what that scope then does to what `GET /scope` (and, by
the same mechanism, search/catalog/graph/list/backlinks/tombstones)
shows.

## The model

Personal, single-identity rekam always runs with `db.FullScope()` — no
restriction, every scenario below about hidden or title-visible branches
is moot for it. A restricted `db.Scope` (only reachable via team
membership, see `spec/teams.md`) has three prefix lists: `read` (content
visible), `write` (content editable, expected to be a subset of `read`),
and `titlesVisible` (existence and title visible, content is not) — a
branch outside all three is invisible entirely, not merely refused. This
is default-deny: an empty or forgotten scope grants nothing, and
`Scope.clauseFor` renders an empty prefix list as `WHERE 0`, so a
misconfigured restriction fails closed in SQL, not in application logic
that could be bypassed. `GET /scope` is a "browse" (title-level) read,
so it honors `titlesVisible` in addition to `read` — unlike a
content-level read (`GET /memory/{id}`, search), which only honors
`read`.

## SCOP-01: Browsing a taxonomy branch lists its records' titles, not their content
Given a signed-in caller and a taxonomy prefix with one or more records
under it, when they `GET /scope?taxonomy=work.ops`, then the response is
`200` with a `results` array of every matching record's id, title, and
taxonomy, ordered most-recently-updated first — and no record's
`content` field, so browsing a large branch is cheap regardless of how
much any individual record holds.

## SCOP-02: A taxonomy is required
Given a signed-in caller, when they `GET /scope` with no `taxonomy`
query parameter, then the response is `400` — browsing is scoped to a
named branch; there is no "browse everything" mode.

## SCOP-03: The result is trimmed to a token budget, with an honest omitted count
Given a branch with more records than fit in the requested
`token_budget` (default `2000` when omitted, or explicitly set via
`?token_budget=N`), when it is browsed, then the response includes
fewer records than the branch actually holds, plus `omitted_count`
accounting for exactly how many were left out — so a caller can tell "a
tight budget trimmed this" apart from "this branch is genuinely small."
A negative `token_budget` is rejected with `400` rather than silently
treated as unlimited or zero.

## SCOP-04: A title-visible branch (but not readable) shows titles with no content leak
Given a restricted scope where a branch is in `titlesVisible` but not in
`read`, when that branch is browsed via `GET /scope`, then its records'
titles and taxonomy still appear in `results` — existence is disclosed —
while their content is never present, since browsing was already
title-only to begin with. This is `db.Scope.TitleVisible`'s purpose:
the caller can know something exists in `finance.q3` without being able
to read what it says.

## SCOP-05: A fully hidden branch browses as empty, indistinguishable from an empty real branch
Given a restricted scope where a branch is in neither `read` nor
`titlesVisible`, when that branch is browsed, then the response is `200`
with an empty `results` array — the same shape as a branch that
genuinely has no records — never an error or a nonzero count that would
disclose the branch has content the caller can't see.

## SCOP-06: An unrestricted (personal or admin) scope browses everything under the prefix
Given a caller with `db.FullScope()` — the default for personal rekam,
and for any identity with no team membership row — when they browse any
taxonomy prefix, then every record under it appears, with no
`titlesVisible`/`read` distinction in play at all.

## SCOP-07: Browsing requires authentication
Given a request to `GET /scope` with no bearer or an invalid one, then
the response is `401` and no results are returned, matching every other
library read (`spec/memory.md`).

## SCOP-08: The same scope mechanism gates every other read and write surface
`db.Scope`'s `CanRead`/`CanWrite`/`TitleVisible` predicates are the one
enforcement point behind `GET /scope`, `GET /search`, `GET /catalog`,
`GET /memories` (list), `GET /graph`, `GET /suggest-links`,
`GET /admin/edge-health` (see `spec/admin.md` ADMN-17), and writes via
`POST`/`PATCH`/`DELETE /memory`. A hidden branch never leaks through any
of them — including transitively: link-graph traversal (`GET /graph`)
stops at a fully hidden node rather than walking through it to reach an
otherwise-readable one on the other side, and a `[[wiki-link]]` to a
hidden memory's exact title reports unresolved rather than resolved,
closing the obvious "does this title exist" oracle. These cross-surface
guarantees are exercised in `internal/db/scope_enforce_test.go` and
`internal/engine/scope_test.go`, tagged as `spec/teams.md`'s concern
rather than re-tagged here, since they test the scope *assignment* and
*membership* machinery as much as the enforcement.
