# Public docs mirror

rekam's own documentation, published as a rekam corpus and served to
anyone with no account. It is the only surface in the server that answers
an unauthenticated caller, and it exists so a stranger can read the docs
*in the real product UI* — the same catalog, the same record view, the
same wiki-links — which makes it the documentation and the demo at once.

The API lives under `/public/*`; the SPA renders it at `/docs`. The
prefixes differ on purpose: `/docs` and `/docs/<record id>` are the
shareable URLs a reader sees, so the app shell owns them, and an API route
under the same prefix would collide.

Everything here fails closed. The mirror is off unless configured, the
account it reads as cannot write by two independent measures, and the
public route set is a short explicit list rather than a filter over the
authenticated one — widening it takes a deliberate edit.

## Editions

Managed only (`spec/editions.md` EDTN-03). A private self-hosted instance
wants neither an unauthenticated surface nor this project's documentation
served from its own domain, so the handlers are not compiled into the solo
and team builds. Every scenario below therefore describes the managed
build; in the others `/public/*` is `404` because no route exists.

## DOCS-01: The mirror is off until a corpus is configured
A server with no docs corpus configured serves nothing at `/public/*`:
every route is `404`, the same as an unknown path. Configuring it requires
a corpus path; asking to enable it without one is an error at startup, not
a server that silently comes up with the feature half-on.

## DOCS-02: A stranger can read the published corpus
Given a configured mirror, when a caller with no credentials of any kind
— no Authorization header, no session cookie — requests the catalog, the
record list, a single record, or search, then each returns `200` with the
published content. This is the whole point: no signup wall in front of
the documentation.

## DOCS-03: The public surface is read-only, and short
The entire anonymous surface is five GET routes: the catalog, the record
list, a single record, a record's revisions, and search. Writes are not
merely rejected — `POST /public/memory` and every other write shape has no
route at all. There is no code path from `/public/*` to a write.

## DOCS-04: A caller's own credentials cannot steer the mirror
When a request to `/public/*` carries an Authorization header, a session
cookie, an `X-Rekam-Team` header or a `?team=` parameter, then all of it
is discarded and the request reads the docs corpus regardless. A caller
holding a perfectly valid key for an account with far more access gets
exactly what an anonymous caller gets — the public surface is not a
convenience path into someone's own memory, and the substitution is
unconditional rather than conditional on the caller looking anonymous.

## DOCS-05: The corpus is served as a dedicated read-only account
The mirror reads as its own identity, separate from every user and every
team, whose `allow_write` is false. Enabling the mirror against an
identity that has write permission is refused at startup. The account is
recognisable by name in an identity listing, so an operator can see what
it is rather than finding an unexplained extra row.

## DOCS-06: Publishing a docs change is deploying the binary
The documentation is authored as markdown and shipped inside the binary,
so there is no operator publishing step: on every boot the embedded
markdown is synced into the corpus, and what the deploy changed —
inserted, updated, removed — is reported so a deploy says what it
published. Syncing unchanged content twice changes nothing, so a restart
is not an edit.

## DOCS-07: Wiki-links resolve inside the mirror
A `[[wiki-link]]` between two published records resolves for an anonymous
reader the same way it does for a signed-in one, so the docs read as a
connected corpus rather than a pile of pages. Links are the product being
demonstrated; a demo where they dangle would argue against the feature.

## DOCS-08: History is readable; restoring is not
A published record's revision history is readable anonymously, because
the documentation describes version history and a demo that cannot show
the feature it documents is the weaker half. Restoring a revision is a
write and has no public route — nor does the UI offer the control.

## DOCS-09: Responses are publicly cacheable
Responses on the public surface carry a shared-cache directive with a
short lifetime, so a busy documentation page is cheap to serve while an
edit still appears promptly. Nothing here is per-caller, so there is
nothing that must not be shared between readers.

## DOCS-10: Anonymous reads are budgeted per client
Anonymous reads are rate-limited per client IP, well above a human
reader's pace — browsing one page costs several requests — and aimed at
scripted scraping rather than at readers. Exceeding the budget is `429`.
The budget is per IP, so one scraper cannot deny the docs to everyone
else.
