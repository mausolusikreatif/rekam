---
title: The taxonomy
taxonomy: docs.model
---

Every record lives at one dot-separated path: `work.projects.rekam`,
`finance.accounts`, `docs.model`. The path is the record's classification, the
way a call number places a book on a shelf.

## Why a path and not tags

A path forces one decision — where does this belong — and that decision is what
lets an agent load a useful slice of memory without reading everything. Asking
for `work.projects` returns that whole branch and nothing else.

Tags let a record belong everywhere, which in practice means nowhere gets
narrowed. When a record genuinely relates to two areas, file it in one and
connect it to the other with [[Wiki-links]].

## Choosing a path

Branch by area of life or work first, then by subject. Keep it shallow until
depth earns itself: `work.projects` is fine until it holds forty records, and
then `work.projects.rekam` is obvious.

A corpus can carry a taxonomy template — a list of branches with a note on what
belongs in each — so an agent files a new record where you would have filed it
rather than inventing a parallel tree.

## Moving records

Changing a record's path is an ordinary edit. Links do not break, because
[[Wiki-links]] resolve by title, not by location.
