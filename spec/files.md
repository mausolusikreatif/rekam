# File attachments

Upload/fetch of binary blobs (currently images embedded from the editor),
kept out of the JSON `/memory` surface because multipart bodies and raw
bytes need different handling. Blobs live in a tenant's own `*.files`
SQLite, a sibling of its `*.memory`/`*.rekam` file, so binary I/O never
contends with the FTS-triggered memories connection (see
spec/replication.md for how that sibling file is replicated).

## The model

`POST /files` requires write permission the same way `POST /memory`
does (see spec/identity.md), and stores the blob under the caller's own
identity. `GET /files/{identity}/{id}` is deliberately *not* gated by a
bearer token: the (identity, file) UUID pair is itself the capability,
unguessable and sufficient, so an uploaded image can render in a plain
`<img>` tag with no `Authorization` header. Only images are accepted —
`image/png`, `image/jpeg`, `image/gif`, `image/webp` — capped at 2 MiB;
SVG is deliberately excluded because it can carry scripts.

## FILE-01: An authorized caller can upload an image
Given an identity with write permission, when they `POST /files` with a
`multipart/form-data` body whose `file` field carries image bytes and a
`Content-Type` from the allowed image set, then the response is `201`
with the created file's `id` and a `url` of the form
`/files/{identity}/{id}` that can be used to fetch it back.

## FILE-02: A caller can fetch an uploaded file via its capability URL
Given a file uploaded to `/files/{identity}/{id}`, when anyone
`GET`s that URL — with no `Authorization` header at all — then the
response is `200` with the exact bytes that were uploaded, the original
`Content-Type` echoed, and a long, immutable `Cache-Control` (the bytes
behind a given id never change).

## FILE-03: Upload rejects non-image content types
Given a `multipart/form-data` upload whose `file` part's `Content-Type`
is not one of `image/png`, `image/jpeg`, `image/gif`, `image/webp` (for
example `application/pdf`), when it is `POST`ed to `/files`, then the
response is `415` and nothing is stored.

## FILE-04: Fetching an unknown identity or file id is a 404
Given a `GET /files/{identity}/{id}` where either segment does not
correspond to a real identity or a real stored file, then the response
is `404`, indistinguishable from a real file that was fetched with the
wrong id.

## FILE-05: Upload without a `file` field is rejected
Given a `multipart/form-data` body posted to `/files` that has no `file`
part at all, when it is submitted, then the response is `400` and
nothing is stored.

## FILE-06: Upload enforces the 2 MiB size cap
Given a `multipart/form-data` upload whose `file` part exceeds 2 MiB —
whether declared oversized up front or discovered while streaming the
body — when it is `POST`ed to `/files`, then the response is `413` and
nothing is stored.

## FILE-07: Upload requires write permission
Given a caller whose identity has write access revoked (see
spec/identity.md's read-only account scenario), when they `POST
/files`, then the response is `403` and nothing is stored — mirroring
`POST /memory`'s write gate.
