# OAuth: dynamic client registration (RFC 7591)

`POST /register` lets a third-party app — an MCP client, above all —
start using rekam as its "Sign in with..." provider with zero manual
setup: no admin creates a `client_id` for it ahead of time. rekam's
registration endpoint stays permissive about client *metadata* — it
exists to unblock claude.ai's connector flow, not to gatekeep names and
logos — and always hands back a public-client profile.

The one thing it is strict about is `redirect_uris`, because that list is
the whole basis of OAUA-14: `/authorize` will only deliver an
authorization code to a URI the client registered here. A registration is
therefore a durable record, not an echo. See `spec/oauth-authorize.md`
for how the resulting `client_id` is used.

## OAUR-01: A new app can register itself with no human involvement
Given an app that has never talked to rekam before, when it `POST`s
`/register` with a JSON body declaring its `redirect_uris`, then the
response is `201` with a freshly generated `client_id` — no admin
approval, no pre-shared secret, no human in the loop.

## OAUR-02: Every registered client is a public client
Whatever the request asks for, the response always carries
`token_endpoint_auth_method: "none"`, `grant_types: ["authorization_code"]`,
and `response_types: ["code"]`, and never includes a `client_secret` — a
client cannot register itself as confidential, since PKCE (not a secret)
is what binds the authorization code to the client that requested it.

## OAUR-03: Registered redirect_uris are recorded and echoed back
When an app registers with a `redirect_uris` array, the `201` response
echoes that same array back verbatim, so the app can verify what rekam
actually recorded before sending a user through the authorize flow — and
rekam stores it, so `/authorize` can enforce it (OAUA-14). The record
survives a restart, and re-registering the same `client_id` (OAUR-05)
replaces the stored list rather than adding to it: a client may change
where its codes go, and first-registration-wins would let anyone squat a
`client_id` and lock its owner out.

## OAUR-04: Two registrations are two distinct clients
Two separate `POST /register` calls with no `client_id` supplied each get
their own freshly generated `client_id` — never the same value twice.

## OAUR-05: Supplying a client_id keeps it
When a registration request includes its own `client_id`, the response
honors it unchanged rather than minting a new one — this is how a client
that already knows its id (or is retrying) stays on the same identity
without rekam treating the call as an error.

## OAUR-06: A malformed request body is rejected
When `POST /register`'s body is not valid JSON, then the response is
`400` with `error: "invalid_request"`, and no client is registered.

## OAUR-07: A registration with nowhere to send a code is refused
When a registration declares no usable `redirect_uris` — the field is
absent, an empty array, or not an array at all — then the response is
`400` with `error: "invalid_redirect_uri"` and no client is registered.
Such a client could never complete an authorization_code flow, so the
refusal belongs here, where it costs a developer one error message,
rather than at `/authorize`, where a user is already waiting.
