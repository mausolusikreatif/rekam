# release/version.json

The update manifest, published to `https://rekam.net/version.json` as part of
the marketing site (`frontend/vite.marketing.config.js` emits it into
`dist/marketing/`). It is the only channel that reaches a self-hosted instance
— see `internal/updatecheck`, and issue #35 for why it exists.

## What a server does with it

Once a day, and once at startup, a server fetches this file, looks up its own
edition, and compares locally. It sends nothing: no version, no edition, no
identifying headers. It never updates anything either — replacing a binary is
the operator's decision, and an existing binary keeps working indefinitely
regardless of entitlement.

## Editing it

One object per edition (`solo`, `team`, `managed`, `desktop`), each with:

| field | meaning |
|---|---|
| `latest` | the newest release, as a `vX.Y.Z` tag |
| `min_supported` | versions below this are called out as known-bad; `""` for none |
| `security` | `true` when `latest` fixes a vulnerability — this is the field the feature exists for, and it turns the operator's notice from a passing mention into a warning |
| `notes` | one short clause appended to the message; keep it actionable |

**An edition with no entry says nothing.** That is deliberate: silence is the
right answer for an edition that has not shipped yet, and better than a claim
that turns out to be wrong. Add the key when the first release goes out.

`security` and `notes` are editorial — nothing can derive them from a tag —
which is why this file is hand-edited and reviewed rather than generated. The
release workflow checks that a tag being built appears here, so a release
cannot ship without the manifest knowing about it.

Bump this **in the commit that gets tagged**, not after: the tag is what the
workflow validates against.
