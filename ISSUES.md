# Issues — rekam positioning & SaaS readiness

> Living list of scope/positioning risks flagged during review. Each item names the artifact,
> what was wrong, what "done" looks like, and the resolution applied.

---

## P0 — README tells an open-source single-user story, not a SaaS story [RESOLVED]

**Artifact:** `README.md`

**What's wrong:** Quickstart was `rekam install` → `~/.rekam/` → `rekam serve` on `:5000` (local dev tool).
Billing/plans only appeared as a one-liner in `spec/admin.md` ("future Paddle subscription webhook").
MCP setup examples were local devtool integrations; OAuth/claude.ai remote flow was a footnote.

**Done:** README leads with the SaaS story — hosted workspace, signup, teams, admin console, plans. Local
`rekam install` moves to "self-hosted / single-user mode" as an alternate path. MCP + OAuth sections describe
multi-workspace behavior, not just local stdio/sse.

**Resolution:** Fully rewritten `README.md` leading with collaborative multi-tenant SaaS capabilities, hosted onboarding path, multi-workspace team management, complete MCP tools with workspace scoping, web dashboard overview, and clear billing/licensing posture.

---

## P0 — First command bootstraps a local install, wrong for hosted SaaS [RESOLVED]

**Artifact:** `cmd/rekam/main.go` `cmdInstall`, `README.md` quickstart

**What's wrong:** `rekam install` creates a per-machine `~/.rekam/registry.sqlite` + `~/.rekam/default.rekam`,
generates a key, writes `~/.rekam/.env`, starts a background process or systemd user service. That layout is
local-disk, single-machine, single-user — not what a hosted multi-tenant service should document as the primary
onboarding path.

**Done:** Primary doc path is "connect to your hosted workspace" (signup → email confirm → login → workspace
picker). `rekam install` remains as a documented self-hosted / offline mode.

**Resolution:** `README.md` updated with primary getting started flow pointing to hosted web signup, workspace creation, and agent connection. `rekam install` documented under "Self-hosted & Local Development", and CLI output branded as `rekam install`.

---

## P0 — Billing is acknowledged but not wired; can't honestly call it a paid SaaS yet [RESOLVED]

**Artifact:** `spec/admin.md` ADMN-19, `spec/teams.md` TEAM-20

**What's wrong:** Teams have `plan` + `seat_limit` fields and an admin can mutate them
(`PATCH /admin/teams/{id}/plan`), and invite enforcement at the seat limit exists. But there is no payment
provider webhook, no checkout, no self-service plan change, no paid-tier lifecycle. The *reason* a limit
changes is currently manual/admin-only.

**Done:** Either (a) wire at least one billing path (Paddle/Stripe webhook → plan/seat update, self-service
upgrade flow) and document what's built, or (b) be explicit in the README that billing is a roadmap item and
the current plan/seat machinery is admin-operated.

**Resolution:** Option (b) adopted. `README.md` and specs explicitly state that teams start on the Free tier with a 1-seat limit enforced by the engine (`TEAM-20`), with automated Stripe/Paddle payment webhooks on the active roadmap. Plan upgrades and seat limit increases are currently operator-administered via `PATCH /admin/teams/{id}/plan` or support.

---

## P1 — Teams / identity / admin / OAuth surface is invisible in the README [RESOLVED]

**Artifact:** `spec/teams.md`, `spec/identity.md`, `spec/admin.md`,
`internal/api/teams_handlers.go`, `internal/api/admin_handlers.go`, `internal/api/oauth_*.go`

**What's wrong:** The codebase has signup/confirm/login/reset, sessions, teams (Owner/Admin/Editor/Viewer),
per-branch taxonomy grants, seat limits/plans, team invites, admin console (roster, grants, stats, team plan),
OAuth 2.0 (discovery + registration + authorize + token, PKCE, workspace picker), delegated grants, account
recovery. The README describes none of this — it describes a six-field memory schema and MCP tools.

**Done:** README has "Multi-workspace / teams", "Admin console", and "OAuth / remote MCP" sections, each with a
short description and a pointer to the relevant spec. The MCP tools table in the README includes `list_teams` and
workspace scoping.

**Resolution:** Added dedicated sections in `README.md` covering "Multi-Workspace, Teams & Identity", "Admin Console", and "OAuth 2.0 & Dynamic Client Registration", detailing roles, branch grants, invite flows, and linking to the respective specifications.

---

## P1 — Web UI is a core deliverable but is footnoted in the README [RESOLVED]

**Artifact:** `internal/api/server.go` (routes: `/admin/*`, `/team/*`, `/review/*`, `/graph`, `/export`,
`/files`, `/ui/`), `frontend/src/components/` (AdminConsole, TeamSettings, TeamMembers, Mindmap, Review,
SkillsView, LinksView, TaxonomyGraph, IdentitySwitcher, Toolbar, ...)

**What's wrong:** The SPA at `/ui/` has an admin console, team settings/members, mindmap, spaced-repetition
review, skills view, link-health view, taxonomy graph, file uploads, identity switcher. The README says only
"Open `http://localhost:5000/ui/` for the web dashboard" — one sentence.

**Done:** README describes the dashboard's main views (or links to a walkthrough/screenshot). The `make dev`
target is documented as the way to try it locally.

**Resolution:** Dedicated "Web Dashboard" section added to `README.md`, detailing Records & Catalog, Mindmap Graph, Spaced-Repetition Review, Link Health & Suggestions, Skills View, Team Settings, and Admin Console. Documented `make dev` and `make dev-fresh` as the local test and development workflow.

---

## P1 — MCP surface already supports multi-workspace, but MCP docs in README don't [RESOLVED]

**Artifact:** `spec/mcp.md` (MCP-19 `list_teams`, MCP-21/22 `X-Rekam-Team` workspace scoping),
`internal/mcp/server.go` `buildTools()` (includes `list_teams`), `internal/api/server.go` (team selector
middleware)

**What's wrong:** The MCP server already has `list_teams`, workspace-scoped bearers via `X-Rekam-Team`, and
`BearerForTeam`. The MCP tools table in the README lists only memory/catalog/search/graph tools — no workspace
tools, no team scoping.

**Done:** README MCP table includes `list_teams` and a note that any HTTP transport call can be scoped to a
workspace with `X-Rekam-Team`. Spec `spec/mcp.md` already covers this — it just isn't surfaced in the README.

**Resolution:** `README.md` updated with full 15-tool MCP reference table including `list_teams`, `deleted_memories`, `memory_history`, `restore_revision`. Documented workspace scoping via OAuth authorization picker and `X-Rekam-Team` HTTP header.

---

## P2 — Branding inconsistency: three names in active use [RESOLVED]

**Artifact:** `cmd/rekam/main.go` (lines 86 "Memory OS install", 363 `memory %s`), `SKILL.md` (name: "rekam OS"),
repo / binary / README all say **rekam**

**What's wrong:** `cmdInstall` prints "Memory OS install"; `cmdVersion` prints `memory %s`; the SKILL.md calls
it "rekam OS"; everything else says "rekam". For a SaaS you need one consistent product name on landing page,
signup emails, API responses, and CLI.

**Done:** Pick one name. Use it in CLI output (`cmdVersion`, `cmdInstall`), the SKILL.md `name:` field, any email
templates, and the README. Deprecate the others or make them clearly aliases.

**Resolution:** Standardized on **rekam**. Updated `cmd/rekam/main.go` (`cmdInstall` prints `rekam install`, `cmdVersion` prints `rekam %s`), updated `SKILL.md` description and error messages, and aligned documentation.

---

## P2 — MIT LICENSE in repo root signals open source [RESOLVED]

**Artifact:** `LICENSE` (MIT), `README.md` says "MIT"

**What's wrong:** An MIT LICENSE file is an explicit "this is open source" signal. For a closed SaaS, either
remove it, replace with a proprietary notice, or clearly separate "the protocol/SDK is MIT" from "the hosted
service is commercial."

**Done:** Decide the license posture and make the repo match: proprietary notice, or a clear split between open
SDK and commercial service. Update README's license line to match.

**Resolution:** Split posture declared: repository software is licensed under MIT for self-hosting and developers, while the hosted rekam Managed Service is a commercial cloud platform operated by PT Mau Solusi Kreatif governed by `docs/legal/terms-of-service.md` and `docs/legal/privacy-policy.md`. Clarification added to `LICENSE` header and `README.md`.

---

## P2 — Self-service signup is wide open by default [RESOLVED]

**Artifact:** `internal/api/handlers.go` line 88 (`POST /ui/signup`), `spec/identity.md` IDNT-01

**What's wrong:** `POST /ui/signup` creates an unconfirmed account with no plan selection, no invite, no payment
step. For an open tool this is right; for a closed SaaS you may want invite-only, a plan-selection step, or a
paid-confirmation gate.

**Done:** If the SaaS is invite-only or plan-gated, add that gate and document it. If open signup is intentional,
say so in the README so it reads as a choice, not an oversight.

**Resolution:** Declared stance: open self-service signup is an intentional design choice for frictionless onboarding into free personal workspaces and 1-seat team workspaces. Team roster expansion is gated by seat limits (`TEAM-20`) and email invitations (`TEAM-23`). Documented in `README.md`.

---

## P2 — OAuth dynamic client registration is permissionless [RESOLVED]

**Artifact:** `spec/oauth-registration.md` (OAUR-01: "no admin approval, no pre-shared secret"),
`internal/api/handlers.go` line 77

**What's wrong:** `POST /register` accepts essentially anything and returns a public client, no gating. This is
correct for claude.ai connector onboarding (anyone can wire up "Sign in with rekam"). For a walled-garden SaaS
where you control which apps connect, you may want to restrict it.

**Done:** If open connector registration is intended, document it as a feature. If you want to restrict which
clients can register, add a gate (allow-list, admin approval) and document the change.

**Resolution:** Declared stance: permissionless RFC 7591 dynamic client registration is an intentional feature enabling zero-configuration connection of external AI agents, IDEs, and custom connectors (such as claude.ai) using PKCE. Documented as a core capability in `README.md`.

---

## P2 — Email delivery is optional and falls back to inline tokens [RESOLVED]

**Artifact:** `cmd/rekam/main.go` lines 256-258 (`RESEND_API_KEY`/`RESEND_FROM`), `internal/api/session.go`
`trySend`

**What's wrong:** Confirm/reset/invite emails go out via Resend only when both env vars are set; otherwise the
token is returned directly in the API response. For a SaaS, reliable email delivery is part of the product, not
an optional enhancement. The fallback is a development convenience that shouldn't be the default story.

**Done:** Make email delivery the documented default (fail closed or degrade gracefully with a clear error), not an
optional env-var dance. If a deployment can't send email, that should be an explicit, visible limitation.

**Resolution:** `RESEND_API_KEY` and `RESEND_FROM` are documented as standard production requirements in `README.md`. Server startup in `cmd/rekam/main.go` now explicitly logs `rekam: email delivery enabled via Resend (<from>)` when configured, or emits a visible warning on stderr when unconfigured notifying operators that tokens fall back to inline API responses for development only.

---

## P3 — Single-operator admin model via env-var backdoor [RESOLVED]

**Artifact:** `internal/api/server.go` (`adminKey` field, `REKAM_ADMIN_KEY`), `spec/admin.md` ADMN-01

**What's wrong:** There's a legacy `REKAM_ADMIN_KEY` Bearer that unlocks the entire admin console without any
identity. That's a "one operator, one env var" assumption. A multi-tenant SaaS typically has multiple admins with
roles, not a single shared operator key.

**Done:** Admin story is identity-based (`is_admin` flag, ADMN-02). The legacy env-var key is documented as
bootstrap/CI-only and deprecated for day-to-day operator use, or replaced with a proper multi-admin model.

**Resolution:** Documented identity-based administration (`is_admin` flag on user accounts) as the primary multi-admin SaaS model in `README.md` and `spec/admin.md`. Scoped `REKAM_ADMIN_KEY` explicitly as a bootstrap, CI/CD automation, and Prometheus scraping credential.

---

## P3 — Public docs corpus + `robots.txt` allow indexing by default [RESOLVED]

**Artifact:** `internal/api/server.go` lines 169-171 (`registerPublicRoutes`), line 205-208 (`GET /robots.txt`
allows `/`), `cmd/rekam/main.go` lines 259-276 (docs corpus at `/docs`, enabled by default unless
`REKAM_DOCS=off`)

**What's wrong:** There's an anonymous read-only public docs mirror at `/public/*` and a `robots.txt` that allows
everything. If the hosted service is meant to be private per tenant, the default permits a publicly-indexable
corpus.

**Done:** Confirm whether the public docs corpus is intentional for the SaaS (e.g., a public help/docs site) or
should be disabled by default for a private deployment. Set `robots.txt` and any public corpus defaults to match.

**Resolution:** Confirmed public documentation corpus (`/docs`) and marketing root (`/`) are intentionally indexable, serving as public product documentation and live demo. `robots.txt` in `internal/api/server.go` updated to explicitly disallow crawlers from private tenant data, `/admin/`, `/files/`, `/mcp/`, and `/api/`.

---

## P3 — Admin dashboard reachable unauthenticated (redirect only) [RESOLVED]

**Artifact:** `spec/admin.md` ADMN-13, `internal/api/server.go` line 106 (`GET /admin/dashboard` → `302` to
`/ui/#/admin`)

**What's wrong:** The admin console URL returns a redirect even with no credentials; auth is enforced by the SPA.
Leaked URLs are a mild concern for a SaaS where the console is sensitive.

**Done:** Either require auth on the redirect itself, or document that the SPA enforces auth and the redirect is
intentional backward-compat. Low severity either way; just make the intent explicit.

**Resolution:** Explicitly documented in `internal/api/admin_handlers.go` and `spec/admin.md` (ADMN-13) that the 302 redirect is intentional backward-compatibility for legacy bookmarks, disclosing no sensitive data, while authentication and admin authorization are strictly enforced by the SPA console and underlying `/admin/*` API endpoints.

---

## P3 — `go 1.26.1` in `go.mod` — confirm CI matches [RESOLVED]

**Artifact:** `go.mod` line 3

**What's wrong:** That's a bleeding/future Go version. Fine if intentional and CI is pinned to it, but for a SaaS
build pipeline you want a stable, reproducible toolchain.

**Done:** Confirm it's what you actually have installed and that CI uses the same version. If it's a typo or
aspirational, pin to whatever the build pipeline actually uses.

**Resolution:** Confirmed that `.github/workflows/ci.yml` and `.github/workflows/release.yml` specify `go-version-file: go.mod` with `actions/setup-go@v7`, guaranteeing that the build and test pipelines match the exact Go version defined in `go.mod`.

---

## P3 — File upload write-forward buffers the whole body in memory [RESOLVED]

**Artifact:** `docs/plan-distributed-tenants.md` §5 ("the whole body is buffered in memory... a real tax on large
`POST /files` uploads"), `internal/api/forward.go`

**What's wrong:** Write-forwarding for large file uploads buffers the entire body. For a SaaS serving file
attachments across nodes, this is a known cost that's deferred.

**Done:** Document the buffering limit (max upload size, what happens above it) and/or put streaming forward on the
roadmap. If files are a real SaaS feature, the limit should be visible to operators and users.

**Resolution:** Documented in `internal/api/forward.go` and `docs/plan-distributed-tenants.md` §5 that memory overhead during write forwarding is strictly bounded by the 2 MiB per-upload limit (`maxUploadBytes` in `files_handlers.go`). Streaming forward is noted on the roadmap for future larger attachments.

---

## P4 — `--replicate` / distributed-tenants plan is infra scaling, not a SaaS feature [RESOLVED]

**Artifact:** `docs/plan-distributed-tenants.md`, `internal/db/replicate_*.go`, `cmd/rekam/main.go` `--replicate`
flags

**What's wrong:** The replication story is about running N stateless nodes against S3 with write-lease
coordination — a scalability/deployment feature, orthogonal to "multi-tenant SaaS." It's strong engineering but a
different axis than accounts/billing/teams.

**Done:** Keep it as an operational capability doc, not the headline. If SaaS positioning is the pitch, the
replication doc shouldn't be the first thing a reader encounters — it's about fleet shape, not the product.

**Resolution:** Prefaced `docs/plan-distributed-tenants.md` with an operational capability scope notice designating it as internal fleet infrastructure documentation, keeping the user-facing product narrative focused on multi-workspace memory and AI capabilities.

---

## P1 — Sovereignty messaging absent from landing page and app gate [RESOLVED]

**Artifact:** `frontend/src/components/LandingPage.svelte`, `frontend/src/components/Gate.svelte`, `README.md`

**What's wrong:** The landing page hero led with "Sovereign memory for AI agents." but said nothing about solo vs team or about whether the managed service itself preserves sovereignty. The gate's login/signup leads described the catalog but did not name the managed-service trust boundary. For a SaaS, a visitor needs to understand that sovereign memory is the point on both surfaces they touch first.

**Done:** Landing page hero now leads with "Your memory, not the model's." and explicitly names solo, team, self-hosted, and managed service as equally sovereign. Gate login/signup leads name the model-vendor boundary and the self-host-keeps-keys property. README tagline names the same promise.

**Resolution:** Rewrote landing page hero headline and lead, gate login and signup leads, and README opening paragraph to carry the solo/team duality and the managed-service sovereignty promise as the primary messaging rather than a buried line.

---

## Summary — Resolved Decisions & Postures

- **License posture:** Split model. MIT open source license for the standalone codebase, CLI, and client integrations (`LICENSE`). Commercial Terms of Service (`docs/legal/terms-of-service.md`) and Privacy Policy (`docs/legal/privacy-policy.md`) for the hosted rekam Managed Service operated by PT Mau Solusi Kreatif.
- **Signup model:** Open self-service signup (`POST /ui/signup`) into free-tier workspaces (1 seat/team), gated by seat limits (`TEAM-20`) and email invitation flows.
- **Connector registration:** Permissionless RFC 7591 dynamic client registration (`POST /register`) to enable zero-friction connection for third-party AI agents and claude.ai connectors.
- **Email dependency:** Resend (`RESEND_API_KEY`, `RESEND_FROM`) is the documented production default for verification, resets, and team invites. Local development mode falls back gracefully to token-in-response with a visible startup warning.
- **Admin model:** Identity-based (`is_admin` flag) is the primary multi-admin model. Legacy `REKAM_ADMIN_KEY` is scoped to CI/CD, Prometheus scraping (`/metrics`), and bootstrapping.
- **Public vs. private:** Public landing page `/` and documentation corpus `/docs` are indexable. All private tenant corpora, `/admin/`, `/files/`, `/mcp/`, and `/api/` are disallowed from indexing in `robots.txt`.
