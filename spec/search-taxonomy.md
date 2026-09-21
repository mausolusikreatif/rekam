# Search & taxonomy

Full-text retrieval and the classification scaffold memories file into.
`internal/db/memory.go`'s `Search` blends FTS5 relevance with backlink
authority and a supersede penalty under a token budget; `Catalog`
aggregates memories by taxonomy path; `internal/engine/engine.go`'s
`Search`/`Catalog`/`ScopeMemories` add scope filtering and budget defaults.
`internal/db/taxonomy_template.go` and `internal/engine/taxonomy_template.go`
hold the corpus's declared taxonomy scaffold — the "what goes here" branches
surfaced to a picker or an agent before any record exists in them. See
spec/memory.md for the `taxonomy` field scenarios write/update themselves
validate against, and spec/links-graph.md for the backlink authority signal
search ranking uses.

## SRCH-01: A caller can search memories by keyword
Given memories whose title or content contains a word,
when they `GET /search?q=<word>`,
then the response is `200` with a `results` list of the matching
memories, ranked by FTS5 relevance blended with backlink authority (more
inbound `relates`/`depends-on` edges rank higher) and a penalty for any
memory another one supersedes (spec/links-graph.md).

## SRCH-02: Search can be scoped to a taxonomy branch
Given matching memories both inside and outside a taxonomy branch,
when they `GET /search?q=<word>&taxonomy=<branch>`,
then only memories in that branch (or a sub-branch of it) appear in
`results`.

## SRCH-03: Search requires a query
Given a `GET /search` request with no `q` parameter (or an empty one),
when the request is made,
then the response is `400` with `code: "missing_query"`.

## SRCH-04: A token budget truncates search results
Given a search that matches more content than a small `token_budget`
allows,
when they `GET /search?q=...&token_budget=N`,
then the response includes as many top-ranked results as fit within `N`
(approximate) tokens, and `omitted_count` reports how many further
matches were left out — so the caller knows to narrow the query rather
than concluding there were no more matches.

## SRCH-05: A negative token budget is rejected
Given a `token_budget` (on `/search` or `/scope`) that is negative or not
an integer,
when the request is made,
then the response is `400`, rather than being coerced to "no budget".

## SRCH-06: The catalog aggregates memories by taxonomy path
Given memories filed across several taxonomy paths,
when they `GET /catalog`,
then the response is `200` with one entry per distinct path actually in
use, each carrying a `count` of memories filed there — a path with zero
memories never appears.

## SRCH-07: The catalog rolls up into a shallow, drillable tree
Given memories at deeper taxonomy paths than the top level (e.g.
`finance.accounts`),
when the catalog is read at the default depth,
then paths are folded to their first segment (e.g. `finance`) with the
child counts summed and `expandable: true`, so the caller sees a shallow
tree and calls again with that path as a prefix to reveal the next level
down (e.g. `finance.accounts`, no longer expandable if it's a leaf).

## SRCH-08: A taxonomy branch can be browsed without content
Given memories filed under a taxonomy branch,
when they `GET /scope?taxonomy=<branch>`,
then the response is `200` with a title-level `results` list (id, title,
taxonomy — no `content`) for that branch and its sub-branches, ordered
most-recently-updated first — a lightweight browse distinct from fetching
each memory's full body.

## SRCH-09: Browsing a branch requires naming one
Given a `GET /scope` request with no `taxonomy` parameter,
when the request is made,
then the response is an error (not the whole corpus) — browsing without a
branch is refused rather than silently unbounded.

## SRCH-10: Browsing respects a token budget
Given a branch with more memories than a small `token_budget` allows,
when they `GET /scope?taxonomy=<branch>&token_budget=N`,
then fewer title-level entries are returned than an unbudgeted call would
give, and `omitted_count` accounts for exactly what was left out.

## SRCH-11: A fresh corpus offers a starter taxonomy template
Given a corpus with no template saved yet,
when they `GET /taxonomy/template`,
then the response is `200` with `source: "default"`, a non-empty
`branches` list (each with a `path` and a "what goes here" `description`),
`kind` reporting `"personal"` or `"team"` depending on the corpus, and
`can_edit` reflecting whether this caller may change it — a personal
corpus gets the personal scaffold (leading with `work`), a team corpus
gets the team scaffold (leading with `product`).

## SRCH-12: A caller can save a custom taxonomy template
Given a caller allowed to edit the template,
when they `PUT /taxonomy/template` with a `branches` list of
`{path, description}` entries,
then the response is `200` with `source: "custom"` and exactly those
branches (in the given order, stamped with `sort`), and a subsequent
`GET /taxonomy/template` serves back the same saved branches rather than
the shipped default.

## SRCH-13: Clearing the taxonomy template reverts to the shipped default
Given a corpus with a saved custom template,
when they `PUT /taxonomy/template` with an empty `branches` list,
then the response reports `source: "default"` again with the full shipped
scaffold for that corpus's kind — clearing is not a one-way door into
"no template at all".

## SRCH-14: Editing the taxonomy template requires a management role
Given a team member without an Owner/Admin-equivalent role (one that
cannot manage members),
when they attempt to save a taxonomy template,
then the request is rejected as forbidden and `GET /taxonomy/template`
reports `can_edit: false` for that caller, even though they may still
read the effective template.

## SRCH-15: Search, catalog, scope, and taxonomy-template routes require authentication
Given a request to `/search`, `/catalog`, `/scope`, or `/taxonomy/template`,
when it carries no bearer token or one that doesn't resolve to an
identity,
then the response is `401`.
