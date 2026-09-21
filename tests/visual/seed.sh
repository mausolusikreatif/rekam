#!/bin/bash
# Seed a running rekam instance with a deterministic corpus, over the real HTTP
# API as the bootstrapped admin. Everything the visual suite needs is created
# here: memories across taxonomy branches, wiki-links, a record with revision
# history, a deleted record (tombstone), a team with a second member, and a few
# team records. Idempotency is not a goal — this runs against a fresh instance.
set -euo pipefail

BASE="${1:-http://localhost:3010}"
EMAIL="${REKAM_ADMIN_EMAIL:-admin@rekam.local}"
PW="${REKAM_ADMIN_PASSWORD:-rekam-visual-test}"
JAR="$(mktemp)"
trap 'rm -f "$JAR"' EXIT

j() { jq -nc "$@"; }
# Authenticated request as the admin session (cookie jar).
req() { curl -sS -b "$JAR" -c "$JAR" -H "Content-Type: application/json" -X "$1" "$BASE$2" ${3:+-d "$3"}; }
# Same, but acting inside a team (selector header).
treq() { curl -sS -b "$JAR" -c "$JAR" -H "Content-Type: application/json" -H "X-Rekam-Team: $2" -X "$1" "$BASE$3" ${4:+-d "$4"}; }
# req/treq with -f: abort on a non-2xx instead of silently proceeding as if it
# succeeded — for a step whose silent failure would corrupt the seed in a way
# nothing downstream would notice (an invite that silently didn't happen looks,
# to the rest of this script, exactly like one that did — this exact gap once
# left a seeded team with only its owner, no second member, until the next
# thing that actually checked the roster caught it).
reqCheck() { curl -fsS -b "$JAR" -c "$JAR" -H "Content-Type: application/json" -X "$1" "$BASE$2" ${3:+-d "$3"} || { echo "  ✗ $1 $2 failed"; exit 1; }; }
treqCheck() { curl -fsS -b "$JAR" -c "$JAR" -H "Content-Type: application/json" -H "X-Rekam-Team: $2" -X "$1" "$BASE$3" ${4:+-d "$4"} || { echo "  ✗ $1 $3 (team $2) failed"; exit 1; }; }

echo "  → logging in as $EMAIL"
# --fail makes a non-2xx (e.g. bad creds because the admin bootstrap didn't run)
# abort the seed under `set -e`, instead of silently proceeding unauthenticated.
curl -fsS -c "$JAR" -H "Content-Type: application/json" -X POST "$BASE/ui/login" \
  -d "$(j --arg e "$EMAIL" --arg p "$PW" '{email:$e,password:$p}')" >/dev/null \
  || { echo "  ✗ login failed — is the admin bootstrapped? check the server log"; exit 1; }

# Sanity-check the session is real before filing anything.
curl -fsS -b "$JAR" "$BASE/me" >/dev/null || { echo "  ✗ session not authenticated"; exit 1; }

# mem <taxonomy> <title> <content> → prints the new id
mem() { req POST /memory "$(j --arg t "$2" --arg x "$1" --arg c "$3" '{title:$t,taxonomy:$x,content:$c,format:"markdown"}')" | jq -r '.id'; }
tmem() { treq POST "$1" /memory "$(j --arg t "$3" --arg x "$2" --arg c "$4" '{title:$t,taxonomy:$x,content:$c,format:"markdown"}')" | jq -r '.id'; }

echo "  → filing personal records"
ROADMAP=$(mem "work.projects" "Rekam roadmap" "Milestones for the memory OS. Related: [[Team onboarding guide]] and [[Q3 planning meeting]].")
mem "work.meetings" "Q3 planning meeting" "Agenda: teams, versioning, taxonomy templates. Owner follow-ups tracked in [[Rekam roadmap]]." >/dev/null
mem "work.projects" "Team onboarding guide" "How a new teammate gets productive in the first week. Depends-on [[Rekam roadmap]]." >/dev/null
mem "personal" "Weekly review ritual" "Every Friday: inbox to zero, review [[Rekam roadmap]], plan next week." >/dev/null
mem "finance" "2026 subscriptions audit" "Cancelled three tools, consolidated two. Annual saving tracked here." >/dev/null

# A record whose media lives somewhere else. rekam stores records, not blobs,
# so this exercises the whole externally-hosted path in one place: a bare image
# URL, a bare clip URL, a markdown image, a share link that can never render
# inline, and a deliberately worded link that must stay a link. The hosts are
# .invalid (RFC 2606) so the failure is deterministic and offline — the test
# depends on these NOT resolving.
mem "work.projects" "Screens from the launch review" \
"Pasted straight from the thread.

https://example.invalid/dashboard.png

The clip of the regression:

https://example.invalid/regression.mp4

![annotated](https://example.invalid/annotated.webp)

Full deck (Drive): https://drive.google.com/file/d/1AbCdEfGhIjKlMnOpQrStUv/view?usp=sharing

See [the original diagram](https://example.invalid/diagram.png) for context." >/dev/null
SYSTEMS=$(mem "learning.books" "Notes on Thinking in Systems" "Stocks, flows, feedback loops. Applies to how [[Rekam roadmap]] sequences work.")
mem "health" "Marathon training block" "16-week base build. Long run Sundays, tempo Wednesdays." >/dev/null
CONTACTS=$(mem "people" "Contacts — design team" "Placeholder roster; superseded by the shared team directory.")
# A record with an unresolved wiki-link, so the Links view has a dangling edge.
mem "work.projects" "Open questions" "Unknowns to resolve. See [[Unwritten spec]] (not filed yet)." >/dev/null

echo "  → building revision history on 'Rekam roadmap'"
req PATCH "/memory/$ROADMAP" "$(j '{content:"Milestones v2 — shipped teams and per-tenant roles. See [[Team onboarding guide]]."}')" >/dev/null
req PATCH "/memory/$ROADMAP" "$(j '{content:"Milestones v3 — taxonomy templates landed. See [[Team onboarding guide]] and [[Notes on Thinking in Systems]]."}')" >/dev/null

echo "  → enrolling 'Notes on Thinking in Systems' in the review deck"
req POST "/review/$SYSTEMS" >/dev/null

echo "  → deleting 'Contacts — design team' (tombstone)"
req DELETE "/memory/$CONTACTS" >/dev/null

echo "  → creating team 'Acme Research' + second member"
TEAM=$(req POST /teams "$(j '{name:"Acme Research"}')" | jq -r '.id')
# New teams default to the free plan (seat_limit 1 — the owner alone); raise
# it before inviting anyone, or the invite below is refused with seat_limit.
reqCheck PATCH "/admin/teams/$TEAM/plan" "$(j '{plan:"team",seat_limit:5}')" >/dev/null
BOB=$(req POST /admin/users "$(j '{email:"bob@rekam.local",password:"password123",name:"Bob Rivera"}')" | jq -r '.user.id')
treqCheck PUT "$TEAM" "/team/members/$BOB" "$(j '{role:"editor",read_grants:["product","engineering"],title_grants:[]}')" >/dev/null

echo "  → filing team records into 'Acme Research'"
tmem "$TEAM" "product.specs" "Onboarding flow spec" "The invite-by-email flow and first-run taxonomy guide." >/dev/null
tmem "$TEAM" "engineering.runbooks" "Deploy runbook" "make deploy: build, ship binary, restart service. Back up first." >/dev/null

echo "  ✓ seed complete (team=$TEAM, records filed, 1 tombstone, revisions on Rekam roadmap)"
