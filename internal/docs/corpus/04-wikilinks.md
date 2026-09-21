---
title: Wiki-links
taxonomy: docs.model
---

Write `[[The taxonomy]]` in a record body and rekam turns it into an edge to the
record with that title. Links resolve by title, so moving a record between
branches never breaks one.

## Typed links

A bare link says two records are related. A typed link says how:

```
[[depends-on::Release checklist]]
[[supersedes::Blue-green cutover]]
[[contradicts::Hotfix policy]]
```

The relation goes first, separated by `::`. A bare `[[Title]]` is `relates`, and
an unrecognised prefix falls back to `relates` rather than failing the write.

`depends-on` means this record is only correct if that one holds.
`supersedes` marks the older record as stale, and search says so when it
surfaces. `contradicts` records a disagreement you have not resolved yet —
which is worth keeping, because the alternative is an agent confidently citing
one side of it.

## The graph is not decoration

Edges feed ranking. A record many others depend on scores higher for the same
query than an unconnected one, which is [[How search works]] in one sentence.

## Dangling and missing links

A link to a title that does not exist yet is kept, not dropped — it is a note
that the record should be written. The app lists these, along with records that
look related but were never linked, so the graph can be repaired deliberately
instead of rotting quietly.
