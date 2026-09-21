---
title: How search works
taxonomy: docs.model
---

Search is SQLite full-text search over titles and bodies, re-ranked by the link
graph.

## The two stages

First, FTS5 matches the query and scores by textual relevance. Second, that
score is adjusted by authority: how many records link to each hit, and how. A
record several others declare a `depends-on` against is load-bearing, and ranks
accordingly.

The effect is that the record everything else is built on tends to arrive first,
even when a newer record repeats more of the query's words.

## Narrowing by branch

Search accepts a branch from [[The taxonomy]], and using one is usually better
than a longer query. `taxonomy: work.projects` with two words beats six words
across the whole corpus.

## Superseded records

A record another `supersedes` still appears in results, flagged. Hiding it would
lose the trail of why the current answer replaced the old one — see
[[Wiki-links]] — but the flag keeps an agent from quoting it as current.

## Rebuilding the index

If search results ever look stale after a bulk import, the index can be rebuilt
from the records themselves. Nothing is lost: the FTS table is derived.
