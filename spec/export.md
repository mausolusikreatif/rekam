# Export

Bulk and single-record download of a caller's own corpus as Markdown,
so records can be read, backed up, or re-imported outside rekam without
a client that understands the JSON API. `GET /export` streams a zip of
every readable record; `GET /export/{id}` streams one record on its
own. Both render the same Markdown-with-YAML-front-matter format, and
both are governed by the same read scope as every other read (see
spec/identity.md and spec/teams.md for how a bearer's readable taxonomy
is determined) — export is a bulk read, not a separate permission.

## The model

A record's dot-notation taxonomy (e.g. `work.ops`) becomes a nested
folder path (`work/ops/`) inside the archive; its title is slugified
into the filename. Two records that collide on the same folder+slug
both survive — the second gets a short id suffix — so export can never
silently overwrite one record with another. Each file is a self
describing Markdown document: YAML front-matter (`id`, `title`,
`taxonomy`, `format` when non-default, `created_at`, `updated_at`) then
an `#`-titled body. A record whose `format` isn't `markdown` (e.g.
`mermaid`) has its content wrapped in a fenced code block tagged with
that format, so it still renders wherever it's opened and can round-trip
back through ingestion.

## EXPT-01: A caller can export their whole corpus as a zip
Given a signed-in caller with one or more records, when they `GET
/export`, then the response is `200` with `Content-Type:
application/zip` and a `Content-Disposition: attachment;
filename="rekam-export-<date>.zip"`, and the archive contains exactly
one Markdown file per record, at a path formed by exploding the
record's taxonomy into folders and slugifying its title into the
filename (e.g. `work.ops` / "Deploy Runbook" → `work/ops/deploy-runbook.md`).

## EXPT-02: Each exported file carries YAML front-matter and the record body
Given an exported record, its Markdown file opens with a `---`-delimited
front-matter block naming `id`, `title`, and `taxonomy` (plus `format`
when it isn't `markdown`, and `created_at`/`updated_at` when set),
followed by a level-1 heading of the title and the record's content.

## EXPT-03: Records with clashing export paths both survive
Two live records sharing a title can no longer be *created* through the
normal write path (`spec/memory.md` MEM-22 blocks it — wiki-links have
nothing deterministic to resolve to otherwise), but a tenant file can
still hold such a pair from before that constraint existed. Given two
records that share the same taxonomy and title (however they came to),
when the corpus is exported, then both still appear as distinct files
in the archive — the second gets its id's first 8 characters appended
to the filename — and neither's content overwrites the other's. Export
stays defensive about this rather than assuming MEM-22 makes it
impossible for every tenant file it might ever read.

## EXPT-04: Titles with YAML-significant characters are quoted safely
Given a record whose title contains characters that would corrupt YAML
front-matter if left bare (a colon, `#`, quote, or leading/trailing
whitespace), when it is exported, the front-matter `title:` line quotes
the value and escapes embedded quotes and backslashes, so the file
remains valid YAML.

## EXPT-05: Non-markdown records are fenced with their format
Given a record whose `format` is not `markdown` (e.g. `mermaid`), when
it is exported, its front-matter records `format: mermaid` and its body
is wrapped in a code fence tagged with that format (```` ```mermaid ````
… ```` ``` ````), rather than being emitted as plain Markdown.

## EXPT-06: A single record can be exported on its own
Given a record's id, when its owner `GET`s `/export/{id}`, then the
response is `200` with `Content-Type: text/markdown; charset=utf-8` and
`Content-Disposition: attachment; filename="<slug>.md"`, and the body
is that record's Markdown document in the same front-matter format as
the bulk archive. Given an id that does not exist, the response is
`404`, not an empty file.

## EXPT-07: Export only includes what the caller can read
Given a team member whose grant limits them to reading a subset of the
team's taxonomy branches (see spec/teams.md), when they `GET /export`
for that team, then the archive contains only records under their
granted branches — records outside their read scope are absent
entirely, not merely redacted — while the team owner's export of the
same team still contains every record.

## EXPT-08: Export requires authentication
Given a request to `GET /export` or `GET /export/{id}` carrying no
bearer, or an invalid one, then the response is `401` and no archive or
file is produced.
