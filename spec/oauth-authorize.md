# OAuth: authorizing a connector and choosing a workspace

This is claude.ai's "connect to rekam" flow: a rekam user is asked to log
in with their own email + password (there is no ambient browser session —
claude.ai opens `/authorize` in a fresh cross-site popup) and, if they
belong to more than one workspace, to pick which one the connection may
read and write. The result is an authorization code minted by
`spec/oauth-token.md`'s `POST /token`, bound to the user's identity and
to at most one workspace.

## The model

The flow is two HTTP steps, not one:

1. `GET /authorize` renders a login form; `POST /authorize` checks the
   email + password. If the identity belongs to zero or one workspace,
   this step finishes immediately with a redirect carrying a code.
2. If the identity belongs to two or more workspaces (personal memory
   always counts as one), step 1 instead renders a workspace picker
   (`POST /authorize/workspace` on submit) rather than finishing. A
   single-use, 10-minute `ticket` — an opaque server-side handle, not the
   user's password — carries the authenticated identity and the OAuth
   request from step 1 into step 2, so the browser never has to resubmit
   credentials.

A grant is pinned to exactly one workspace: it reads and writes that
corpus and no other. To connect a second workspace, the client
authorizes again — there's no way to widen an existing grant. See
`spec/teams.md` for what a workspace and a member's role within it are.

## OAUA-01: An unauthenticated visitor is shown a login form
Given a registered client and a well-formed authorize request, when a
browser with no session hits `GET /authorize`, then it receives an HTML
login form (not a redirect, not an error) asking for a rekam email and
password, and no authorization code is issued yet.

## OAUA-02: A request with nowhere to return is rejected up front
When `GET /authorize` is missing `redirect_uri`, then the response is
`400` before any credential is collected — the page must not present a
password field with no way to send the user back afterward.

## OAUA-03: Client-supplied values can't inject markup into the login form
The login form interpolates `redirect_uri`, `state`, and the other query
parameters as hidden fields the browser is about to submit a password
alongside. Given an authorize request whose `redirect_uri` or `state`
contains HTML/script content, when the form is rendered, then that
content appears escaped, never as live markup — a crafted authorize link
can't inject a script into the page the user types their password into.

## OAUA-04: A wrong password is rejected without issuing a code
When `POST /authorize` is submitted with an email that exists but the
wrong password, then the response is `401`, re-rendering the login form
with an error message and the entered email preserved (not the
password), and no authorization code is issued.

## OAUA-05: An unconfirmed account cannot authorize
Given an identity that signed up but never confirmed its email, when it
submits correct credentials to `POST /authorize`, then the response is
still `401`, with a message telling the user to confirm their email
first — a connector cannot be the first thing that proves an email
address.

## OAUA-06: A single-workspace user skips the picker entirely
Given an identity that belongs to only personal memory (no team
memberships), when they submit correct credentials to `POST /authorize`,
then the response is an immediate `302` redirect to the client's
`redirect_uri` carrying a fresh authorization `code` — no workspace
picker is shown, since there is nothing to choose between.

## OAUA-07: A multi-workspace user is shown a picker naming each one
Given an identity that belongs to personal memory plus at least one team,
when they submit correct credentials to `POST /authorize`, then the
response is `200` with an HTML workspace picker, not a redirect. Personal
memory is offered as "Personal memory"; each team is offered by its own
name and the user's role in it. The page carries a single-use `ticket`
as a hidden field, and no authorization code exists yet.

## OAUA-08: Choosing an offered workspace finishes the flow
Given a valid ticket from OAUA-07, when the user `POST`s
`/authorize/workspace` with a `team_id` that was actually offered, then
the response is a `302` redirect to the client's `redirect_uri` carrying
a fresh authorization code — and the grant that code will mint (see
`spec/oauth-token.md`) is pinned to that workspace.

## OAUA-09: A workspace that was never offered is refused
When `POST /authorize/workspace` is submitted with a `team_id` that
wasn't among the workspaces this ticket's picker actually listed
(tampered form, stale picker, or someone else's team id), then the
response is `400` and no code is issued — even though the ticket proved
the password step already succeeded, it doesn't authorize an arbitrary
workspace. The ticket is consumed by this attempt regardless of the
outcome (see OAUA-11): the user must restart from `POST /authorize` to
try again.

## OAUA-10: A ticket cannot be replayed
Given a ticket that has already been used to finish the flow (OAUA-08) or
to attempt an unoffered workspace (OAUA-09), when it's submitted to
`POST /authorize/workspace` again, then the response is `400` — a
captured or resubmitted ticket cannot mint a second code.

## OAUA-11: An expired ticket is refused like an unknown one
Given a workspace-selection ticket older than its 10-minute lifetime,
when it's submitted to `POST /authorize/workspace`, then the response is
`400`, the same as an unrecognized ticket — a stale picker page left open
in a browser tab cannot be used to authorize later.

## OAUA-12: The original state is carried through to the redirect
Whichever path finishes the flow — immediate (OAUA-06) or after a
workspace choice (OAUA-08) — the client's `redirect_uri` is hit with the
same `state` value the original `GET /authorize` request carried,
unchanged, so the client can match the callback to the request it made.

## OAUA-13: The OAuth surface never leaks a code via Referer
Every response on the `/authorize` surface carries
`Referrer-Policy: no-referrer` — an authorization code embedded in a
redirect URL must not be forwarded to whatever page the browser navigates
to next.

## OAUA-14: A code is only ever delivered to a registered redirect_uri
Given a client registered with a set of `redirect_uris` (see
`spec/oauth-registration.md`), when an authorize request names a
`redirect_uri` that is not one of them — or names a `client_id` that was
never registered, or none at all — then the response is `400` and no code
is issued. The rejection is rendered by rekam; the unregistered
destination is never redirected to, not even to report the error (RFC
6749 §4.1.2.1).

This holds at both ends of the flow: `GET /authorize` refuses before a
password field is ever shown, and the mint point refuses again, so
neither a direct `POST /authorize` nor a workspace ticket carrying a
`redirect_uri` from ten minutes ago can deliver a code somewhere the
client never declared.

PKCE does not substitute for this check. An attacker who initiates the
flow chooses the challenge and therefore holds the verifier, so a
crafted authorize link plus a user who logs in would otherwise yield a
working token scoped to that user's memory.

## OAUA-15: Loopback redirects match on everything but the port
A native app cannot know which loopback port it will be given, so it
registers one and listens on another (RFC 8252 §7.3). Given a client
registered with `http://127.0.0.1:0/callback`, when it authorizes with
`http://127.0.0.1:<any port>/callback`, then the request is accepted.
Scheme, host and path must still match exactly: a different path, a
different host, or `http://localhost` (which resolves through DNS and can
be pointed elsewhere — RFC 8252 §8.3) is refused. Nothing but a literal
`127.0.0.1` or `[::1]` over `http` gets the exemption; every other
registered URI is compared as an exact string, so no prefix, suffix, or
subdomain of a registered URI is a match.

## OAUA-16: The consent screen names where the code will go
The login form and the workspace picker both state the destination of the
authorization code: the host of the `redirect_uri`, or "an application on
this device" for a loopback address. A user asked for their password is
told which party is about to receive access, in terms they can check.

