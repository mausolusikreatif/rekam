# Rekam visual tests

Screenshot-driven Playwright tests that exercise the real app end-to-end: login,
catalog, reading, editing, revision history, deleted records, teams, taxonomy
guide, and search. Each run produces a self-contained HTML dashboard with
per-step screenshots, a video, and a Playwright trace.

## How it works

The host (Fedora) doesn't run Playwright browsers well, so:

- the **Go server runs on the host** against a **fresh temp data dir**
  (`.rundata/`), which the `serve` bootstrap seeds with an admin
  (`admin@rekam.local` / the value `run.sh` sets `REKAM_ADMIN_PASSWORD` to);
- `seed.sh` **populates the corpus over the HTTP API** (records across branches,
  wiki-links, a record with revisions, a tombstone, a team + second member);
- **Playwright runs inside the official Docker image** (`--network host`), so the
  browser binaries come from the image, not the host.

Nothing touches your real `~/.rekam` data — `REKAM_REGISTRY` is pinned to the
temp dir so the binary never falls back to the developer `.env`.

## Run

    ./tests/visual/run.sh        # or: make visual-test

Requires Docker. Results land in `tests/visual/results/<timestamp>/index.html`.
This always builds fresh, reseeds from scratch, and tears the server down
(deleting its data dir) when it's done — good for a CI-shaped run, not for
poking around by hand.

## Interactive dev instance

To click through the app yourself instead of watching Playwright do it:

    ./tests/visual/dev.sh         # or: make dev
    ./tests/visual/dev.sh --fresh # or: make dev-fresh — wipe and reseed first

Same build + seed as above, but it leaves the server running (Ctrl-C to stop)
and — unlike `run.sh` — doesn't delete its data dir on exit, so the next run
comes back up with the same corpus unless you ask for `--fresh`. Prints the
URL and the seeded admin/member logins on startup. Data lives in
`tests/visual/.devdata/`, gitignored, separate from `.rundata/`.

## Add a test

Drop `tests/NN-name.js` using the `fixtures` (`page`, `baseUrl`, `snap`, `log`)
and the `login` helper. Take `snap('label')` at each meaningful step; the
reporter lays them out per test. Seed any new fixtures you need in `seed.sh`.
