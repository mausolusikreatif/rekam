---
title: Authorship and history
taxonomy: docs.agents
---

Every write is signed and every version is kept. Two questions always have an
answer: who put this here, and what did it say before.

## An agent writes as you, not as itself

An agent has no account of its own. It writes under an identity you granted it,
and each version records both — the person accountable for the change, and the
tool that made it.

That distinction is the whole point. "The AI decided this" is never an
explanation, because a person authorized every write an agent was able to make.

## What a record's history looks like

Editing a record does not overwrite it. rekam snapshots the new version and
keeps the old text readable, so a record at version 3 has three complete
versions behind it, newest first:

| Version | Author | Written by | When | Change |
| --- | --- | --- | --- | --- |
| 3 | Rina | web | 12 Mar | Added the rollback step |
| 2 | Rina | claude | 8 Mar | Filled in the migration checklist |
| 1 | Ucok | claude | 2 Mar | First draft |

Version 2 was written by an agent; the account answerable for it is still
Rina's. Any of those versions can be read in full, and restoring one copies it
forward as a new version rather than erasing what came after — the history stays
a complete series, never an undo log.

These docs have histories of their own. Open **History** on any record here and
you can read its earlier versions in full — they are records like any other, and
each time one changes, what it said before stays where it was.

If two people edit the same record at once, the second edit is refused rather
than silently overwriting the first.

## Read and write scope

An identity can be read-only, or limited to certain branches of
[[The taxonomy]]. A read-only agent can consult the corpus and quote it but
cannot change anything — which is exactly what is serving you this page. These
docs are a rekam corpus published read-only: you can read every record and every
past version of it, and change none of them.

## Deleting

A delete leaves a tombstone naming what was removed, by whom, and when. A record
that disappears is still accounted for, which matters most when the thing that
removed it was an agent. See [[Bringing records back]] for the other half of
keeping decisions reachable.
