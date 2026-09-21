#!/bin/bash
# Seed a running rekam with the corpus the landing-page media is shot against.
#
# Deliberately not tests/visual/seed.sh: that corpus exists to exercise edge
# cases (a tombstone, a dangling edge, a second team) and its record bodies are
# one-liners, which look thin in a screenshot. This one files fewer records with
# real bodies — code blocks, a table, a diagram, typed wiki-links — because the
# whole point of the media is to show what a record actually looks like.
#
# It signs up through the real /ui/signup flow rather than adopting the
# bootstrap admin, so the account carries a person's name. The sidebar footer
# renders that name, and "admin" is not what a visitor should see there.
set -euo pipefail

BASE="${1:-http://localhost:3011}"
EMAIL="${REKAM_DEMO_EMAIL:-ada@rekam.local}"
PW="${REKAM_DEMO_PASSWORD:-catalog-of-records}"
NAME="${REKAM_DEMO_NAME:-Ada Lovelace}"
JAR="$(mktemp)"
trap 'rm -f "$JAR"' EXIT

req() { curl -sS -b "$JAR" -c "$JAR" -H "Content-Type: application/json" -X "$1" "$BASE$2" ${3:+-d "$3"}; }

echo "  → signing up as $NAME <$EMAIL>"
SIGNUP=$(curl -fsS -c "$JAR" -H 'Content-Type: application/json' -X POST "$BASE/ui/signup" \
  -d "$(jq -nc --arg e "$EMAIL" --arg p "$PW" --arg n "$NAME" '{email:$e,password:$p,name:$n}')")
CODE=$(jq -r '.confirm_code // empty' <<<"$SIGNUP")
if [ -z "$CODE" ]; then
  echo "  ✗ signup returned no confirm_code — is a mailer configured on this instance?" >&2
  exit 1
fi
curl -fsS -b "$JAR" -c "$JAR" "$BASE/ui/confirm?token=$CODE" >/dev/null
curl -fsS -b "$JAR" "$BASE/me" >/dev/null || { echo "  ✗ session not authenticated" >&2; exit 1; }

# mem <taxonomy> <title>, body on stdin → prints the new id
mem() {
  local tax="$1" title="$2" f id
  f=$(mktemp); cat > "$f"
  id=$(req POST /memory "$(jq -n --arg t "$title" --arg x "$tax" --rawfile c "$f" \
    '{title:$t,taxonomy:$x,content:$c,format:"markdown"}')" | jq -r '.id')
  rm -f "$f"
  echo "$id"
}

echo "  → filing records"

# The hero record. The landing page's copy has always described this one; until
# now the page drew it by hand in markup, which is why it didn't match the app.
mem "work.runbooks" "Deploy runbook" <<'MD' >/dev/null
Ship from `main` only. [[depends-on::Release checklist]] has to pass before
anything leaves CI. This runbook replaces [[supersedes::Blue-green cutover]],
and it still disagrees with [[contradicts::Hotfix policy]] on who may skip the
freeze — worth settling before the next release.

## The sequence

```bash
make release TAG=v1.4.0     # tags, builds, uploads the binary
make deploy ENV=prod        # ships it and restarts the unit
make smoke ENV=prod         # 30s of synthetic traffic before we call it done
```

| Stage   | Owner   | Rollback              |
| ------- | ------- | --------------------- |
| Build   | CI      | re-run the job        |
| Ship    | on-call | previous binary       |
| Migrate | on-call | down migration        |

Back up the registry before any migration step. The restore path is in
[[Release checklist]] and has been tested twice.
MD

mem "work.runbooks" "Release checklist" <<'MD' >/dev/null
Everything that has to be true before a deploy leaves CI.

- [ ] Migrations run clean against a copy of prod
- [ ] Restore from last night's backup verified
- [ ] `make smoke` green on staging
- [ ] On-call named in the channel, and awake

Owned by whoever is on rotation — see [[On-call rotation]].
MD

mem "work.runbooks" "Blue-green cutover" <<'MD' >/dev/null
The old two-environment swap. Kept for the archive; we stopped doing this when
a single binary made the cutover unnecessary, and [[Deploy runbook]] is the
current procedure.

The cost that killed it: two full environments running at once, and a DNS TTL
we could never get below five minutes.
MD

mem "work.runbooks" "Hotfix policy" <<'MD' >/dev/null
A hotfix may skip the freeze window if, and only if, it is a one-line revert
of a change shipped in the last 24 hours.

Anything larger goes through [[Release checklist]] like everything else. This
is the clause that disagrees with [[Deploy runbook]] — the runbook says no
exceptions at all.
MD

mem "work.runbooks" "On-call rotation" <<'MD' >/dev/null
One week each, handover on Monday morning. The person going off rotation writes
the handover note; the person coming on reads it back before the call ends.

Escalation is a person, never a queue.
MD

mem "engineering.notes" "Storage layout" <<'MD' >/dev/null
One SQLite file per identity, plus a registry that maps identities to files.
Nothing shared at the row level, so a tenant is a file you can copy.

```mermaid
flowchart LR
  R[(registry.sqlite)] --> A[(ada.rekam)]
  R --> B[(bob.rekam)]
  R --> T[(acme-research.rekam)]
  A --> FTS[FTS5 index]
  A --> G[link graph]
```

Backups copy whole files, which is why a restore is a file move rather than a
migration. See [[depends-on::Deploy runbook]] for where that sits in a release.
MD

mem "work.meetings" "Q3 planning meeting" <<'MD' >/dev/null
Agenda: teams, versioning, taxonomy templates.

Decided: taxonomy templates ship first, versioning waits for the next quarter.
Follow-ups are tracked against [[Deploy runbook]] and [[Release checklist]].
MD

SYSTEMS=$(mem "learning.books" "Notes on Thinking in Systems" <<'MD'
Stocks, flows, and the delay between them. The delay is the part that bites:
by the time a signal is legible, the stock has already moved.

> A system is never the sum of its parts; it is the product of their
> interactions.

Applies directly to how [[Deploy runbook]] sequences work — the freeze window
exists because the feedback delay on a bad release is longer than the release
itself.
MD
)

mem "personal" "Weekly review ritual" <<'MD' >/dev/null
Every Friday, in this order:

1. Inbox to zero
2. Re-read anything filed under `work.runbooks` that changed this week
3. Plan next week against [[Q3 planning meeting]]

Fifteen minutes, and it is the only meeting with myself that survives.
MD

# One unresolved edge, so the Links view has a dangling link to show.
mem "work.projects" "Open questions" <<'MD' >/dev/null
Unknowns to resolve before the next planning round.

- Whether the freeze window applies to docs-only changes
- Who owns the restore drill — see [[Unwritten spec]], not filed yet
MD

echo "  → enrolling 'Notes on Thinking in Systems' in the review deck"
req POST "/review/$SYSTEMS" >/dev/null

echo "  ✓ demo corpus seeded"
