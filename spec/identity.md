# Identity: signup, login, sessions, recovery

Browser-facing identity for rekam's `/ui/*` endpoints plus the plain API-key
surface (`GET /me`, `GET /identities`, `DELETE /identities/{id}`). An
identity is a row in the central registry (`internal/db/users.go`) carrying
an email, a PBKDF2 password hash, and the path to its own memory store;
`internal/api/session.go` layers a signed, in-memory browser session on top,
and `internal/engine/users.go` is where signup/login/confirm/reset business
rules live. Team membership within a corpus is a separate concern — see
`spec/teams.md`. OAuth grants minted from a browser session are covered in
the `spec/oauth-*.md` files.

## Editions

Self-service signup is how a hosted service sells accounts, so IDNT-01..05
— `POST /ui/signup` and `GET /ui/confirm` — exist only in the managed
build (`spec/editions.md` EDTN-03). A team build
creates accounts through `/admin/users` instead (`spec/admin.md` ADMN-05);
a solo build has one account and creates it at startup.

Everything else here is core and present in every edition: logging in,
sessions, logging out, forgetting and resetting a password, and `/me`.

## IDNT-01: Signing up creates an unconfirmed account
Given no account exists for an email address, when someone `POST
/ui/signup`s a name (optional), email, and password of at least 8
characters, then the response is `202 Accepted` carrying the new
identity and a `confirm_url`/`confirm_code` — the account exists but
cannot yet log in, and no session cookie is set. Signup is disabled
(`503`) on a server with no configured users directory.

## IDNT-02: Confirming the signup token signs the user in
Given the confirmation code from IDNT-01, when they `GET
/ui/confirm?token=<code>`, then the response is `200` with
`authenticated: true` and the identity, and a session cookie is set —
confirming is itself the first login, no separate `POST /ui/login`
required.

## IDNT-03: A confirmation token is single-use and validated
`GET /ui/confirm` with no token is `400`. A token that is garbage, or
one that has already been consumed by an earlier confirm, is rejected
(`4xx`) and does not confirm the account a second time.

## IDNT-04: Signup rejects invalid or duplicate credentials
`POST /ui/signup` is rejected (`4xx`, no account created) for: an empty
email, a malformed email (no `@`), a password under 8 characters, and an
email already registered to a confirmed account.

## IDNT-05: Email identity is case-insensitive
Signing up as `Alice@Example.COM` and later logging in as
`alice@example.com` reach the same account (`NormalizeEmail` lower-cases
and trims on every write and lookup). A differently-cased duplicate of an
already-registered address is refused at signup, the same as an exact
duplicate would be.

## IDNT-06: Correct credentials sign a confirmed user in
Given a confirmed account, when they `POST /ui/login` with the correct
email and password — as JSON or as a form-encoded body (the login page's
no-JS fallback) — then the response is `200` with `authenticated: true`
and the identity, and a session cookie is set.

## IDNT-07: Login is rejected for an unconfirmed account
Given an account created by signup but never confirmed, when they `POST
/ui/login` with the correct email and password, then the response is
`403 Forbidden` with a `resend` link back to `/ui/confirm`, and no
session cookie is set — confirming is mandatory before the account can be
used, not just before it appears in search.

## IDNT-08: Wrong credentials are rejected and rate-limited
A `POST /ui/login` with the correct email but a wrong password gets
`401 Unauthorized` with no session cookie. Repeated failed attempts from
the same client IP are throttled: after the burst is spent the endpoint
answers `429 Too Many Requests` with a `Retry-After` header instead of
continuing to check passwords, while a different client IP attempting
the correct password is unaffected — the limit is per-IP, not global.

## IDNT-09: The session endpoint reports whether the browser is signed in
`GET /ui/session` with a live session cookie returns `200` with
`authenticated: true` and the identity. With no cookie, or a cookie value
that is forged, stale, or already logged out, it returns `200` with
`authenticated: false` — it never falls back to any identity for an
unrecognized cookie, and a forged cookie also gets `401` when used
against a genuinely protected route like `GET /catalog`.

## IDNT-10: Logging out ends the session server-side
Given a signed-in session, when they `POST /ui/logout`, then the
response is `200` and the browser is told to expire the cookie
(`Max-Age` < 0). Critically, the same cookie value replayed afterward no
longer authenticates anything — the session is dropped from the
server-side store, not just cleared client-side.

## IDNT-11: Forgot-password never reveals whether an account exists
`POST /ui/forgot` with a known email returns `200` with `status: "ok"`,
a `reset_code`, and the account's email. `POST /ui/forgot` with an
unknown email returns the same `200` shape but with an empty
`reset_code` and no echoed email — the response never becomes an
account-enumeration oracle.

## IDNT-12: A valid reset token signs the user in with the new password
Given a reset code from IDNT-11, when they `POST /ui/reset?token=<code>`
with a new password, then the response is `200` with a session cookie
set for that identity (reset doubles as login, like confirm does).
Afterward the new password logs in and the old one no longer does.

## IDNT-13: A reset token is single-use
Replaying an already-used reset token (`POST /ui/reset?token=<code>`
again with a different password) is rejected (`4xx`), and the password
set by the first, legitimate reset keeps working — a reset link found
later in a mailbox or browser history cannot be replayed to seize the
account.

## IDNT-14: Reset rejects invalid tokens and weak passwords
`POST /ui/reset` with no token is `400`. A syntactically-plausible but
unknown token is rejected. A real, unused token combined with a password
under 8 characters is also rejected, and in every rejection case the
account's original password keeps working — a failed reset leaves
nothing changed.

## IDNT-15: A signed-in identity can fetch their own identity
Given a valid API key or session, when they `GET /me`, then the response
is `200` with their id, name, `allow_write`, `created_at`, and the
server's `edition` (`spec/editions.md` EDTN-05). This is also how a caller
discovers their own identity id for use with `DELETE /identities/{id}`.

## IDNT-16: Listing every identity is admin-only
`GET /identities` with an ordinary, non-admin key (even a validly
resolving one, such as a minted OAuth grant) is `401 Unauthorized`. With
an admin credential — the legacy admin Bearer key, or a Bearer/session
whose identity has `is_admin` set — it is `200` with the full roster
(id, name, `allow_write`, `created_at`; no email or API-key hash).

## IDNT-17: An identity may revoke its own key; a stranger may not
`DELETE /identities/{id}` where `{id}` is the caller's own identity
succeeds (`200`) even without admin rights — same-identity scope. A
request bearing an unrelated or bogus key targeting someone else's `id`
is rejected (`401` for an unresolvable key, `403` for a resolvable key
belonging to a different identity). After a successful self-revoke, the
revoked key no longer resolves anywhere (`GET /me` with it is `401`).

## IDNT-18: An admin may revoke any identity's key
An admin credential can `DELETE /identities/{id}` for any identity, not
just its own, and the target's key stops resolving immediately
afterward.

## IDNT-19: An invalid or missing credential is rejected on any protected route
A request bearing an API key that does not resolve to any identity gets
`401 Unauthorized` on a protected route (e.g. `GET /catalog`) — the same
gate `sessionAuth`/`apiKeyFromRequest` apply everywhere behind
`/me`, `/identities`, and the memory/search surface described in
`spec/memory.md` and `spec/search-taxonomy.md`.

## IDNT-20: A browser session survives a server restart
Given a session cookie from a successful login, when the server process
that issued it is replaced by a fresh one pointed at the same registry
(a redeploy, or a second node), then `GET /ui/session` with that same
cookie still returns `200` with `authenticated: true` for the same
identity — sessions are recorded in the registry, not held only in the
process that minted them, so a restart does not silently sign everyone
out.

## IDNT-21: Confirm and reset tokens are emailed when a provider is configured, not returned
Given an `Emailer` is configured (see `internal/email`) and its send
succeeds, when `POST /ui/signup` or `POST /ui/forgot` mints a
confirm/reset token, then the response carries `email_sent: true` and
omits the token (`confirm_code`/`confirm_url` or
`reset_code`/`reset_url`) entirely — the only place it exists is the
email that went to the account's own address, not the API response
anyone who knows that address could otherwise read. Given no `Emailer`
is configured, or one is configured but the send itself fails, then the
response instead carries `email_sent: false` and the token directly,
exactly as before this existed — a provider outage degrades to
manual/UI delivery rather than stranding the caller. `POST /ui/forgot`
for an email with no account never attempts a send (there is no token
to email) and its response is unaffected either way.
