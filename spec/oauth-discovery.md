# OAuth: discovery

A remote MCP client (claude.ai above all) must find rekam's OAuth endpoints
without any human hardcoding a URL — they're discoverable from one
well-known document. See `spec/oauth-registration.md`, `spec/oauth-authorize.md`,
and `spec/oauth-token.md` for what each advertised endpoint actually does.

## OAUD-01: A client can find every endpoint from one well-known URL
When a client `GET`s `/.well-known/oauth-authorization-server`, then it
receives `issuer`, `authorization_endpoint`, `token_endpoint`, and
`registration_endpoint`, each an absolute URL rooted at the host the
client actually reached — not a hardcoded localhost or a value from
server config — so a tunnelled or proxied deployment advertises its
public name.

## OAUD-02: Discovery follows the forwarded scheme
Given a deployment behind a plain-HTTP local proxy, when the request
carries `X-Forwarded-Proto: http`, then every endpoint in the discovery
document is rooted at `http://`, not `https://` — a client is never
handed a scheme it can't actually reach. Otherwise the scheme defaults to
`https`.

## OAUD-03: Discovery requires no credential
`GET /.well-known/oauth-authorization-server` succeeds with no
`Authorization` header and no session cookie — it has to, since a client
reads it before it has any credential at all.

## OAUD-04: The document advertises exactly the flow rekam supports
The discovery document lists `response_types_supported: ["code"]`,
`grant_types_supported: ["authorization_code"]`, and
`code_challenge_methods_supported: ["S256"]` — the authorization-code +
PKCE flow, and nothing else (no implicit grant, no plain PKCE). It carries
no `scopes_supported` and no `jwks_uri`: a minted grant is not a JWT to
verify locally, it's an opaque per-connector API key rekam resolves
itself on every request, and access is not scoped by an OAuth `scope`
parameter — it's whatever workspace the user picks during consent (see
`spec/oauth-authorize.md`) at their own role there.
