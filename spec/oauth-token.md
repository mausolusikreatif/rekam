# OAuth: exchanging a code for a token

`POST /token` redeems the authorization code minted by
`spec/oauth-authorize.md`'s flow for a freshly minted, independently
revocable API key ("grant") — not a copy of any static key. The grant is
*delegated*: it authenticates as the rekam identity who completed
`/authorize`, scoped to at most the one workspace chosen there, at
exactly that user's role. See `spec/teams.md` for roles and membership,
and `spec/admin.md` for how an admin lists and revokes grants.

## OAUT-01: A valid code with the matching PKCE verifier yields a token
Given an authorization code just issued, tied to the `code_challenge`
from that authorize request, when the client `POST`s `/token` with
`grant_type=authorization_code`, the `code`, and the matching
`code_verifier`, then the response is `200` with `access_token`,
`token_type: "bearer"`, the grant's `id`, and the `identity_id` it
belongs to, and the response carries `Cache-Control: no-store`.

## OAUT-02: The wrong PKCE verifier is rejected
Given a code issued against a `code_challenge`, when the client `POST`s
`/token` with a `code_verifier` that does not hash to that challenge,
then the response is `400` with `error: "invalid_grant"`, and no token is
issued.

## OAUT-03: A code can only be redeemed once
Given a code that has already been successfully exchanged for a token,
when it's submitted to `/token` a second time (even with the correct
verifier), then the response is `400` (`invalid_grant`) and no new token
is issued — replaying a captured code must not work.

## OAUT-04: An unknown or expired code is rejected
When `/token` is called with a `code` that was never issued, or one whose
10-minute lifetime has passed, then the response is `400`
(`invalid_grant`), and no token is issued.

## OAUT-05: The minted token authenticates ordinary API calls
The `access_token` from OAUT-01 works exactly like any other bearer API
key against rekam's regular endpoints (e.g. `GET /catalog`) — a connector
never has to do anything OAuth-specific to use the memory it was granted.

## OAUT-06: The token is pinned to the workspace chosen during authorize
Given a code minted after the user picked a team on the workspace picker
(`spec/oauth-authorize.md` OAUA-08), the token from exchanging it reads
and writes only that team's corpus: it can find that team's memories and
cannot find memories filed under the user's personal store or any other
team, and `GET /teams` (or its equivalent) reports that team as the
active workspace.

## OAUT-07: A grant with no chosen workspace stays on personal memory
Given a code minted through the single-workspace shortcut (OAUA-06, no
team chosen), the resulting token reads and writes the user's personal
memory only, and cannot see any team's corpus — it can still enumerate
the user's teams (with personal memory marked active), which is how an
agent learns a second workspace exists and that it would need its own
connection to reach it.

## OAUT-08: The token carries the user's own role, not elevated access
A token minted for a member with a restricted role (e.g. viewer, or an
editor limited to specific taxonomy branches) can only do what that role
permits in that workspace — it cannot write where the underlying identity
couldn't, and cannot manage team membership unless the identity's own
role allows it. A delegated grant is a window onto the user's existing
permissions, never a way to gain more.

## OAUT-09: A token cannot be pinned to a workspace the user isn't in
Minting a grant pinned to a team the identity is not a member of fails —
whether the team exists and they've simply never joined it, or the team
id doesn't exist at all. (`spec/oauth-authorize.md` OAUA-09 already
blocks this at the picker; this is the deeper check the mint itself makes,
so a tampered request can't get a token even if it slipped past the
picker.)

## OAUT-10: Revoking a grant invalidates its token immediately
Given a token minted through this flow, when an admin revokes the
underlying grant (`spec/admin.md`), then that token stops authenticating
any further request — but every other credential (the identity's own
session or API key, other grants) keeps working; revoking one connector
cannot lock its owner out.

## OAUT-11: Revoking a delegated grant never deletes the person behind it
Revoking a grant removes only that grant's key. The rekam identity it was
delegated for still exists, still resolves, and its own key keeps
working — unlike the legacy, pre-delegation grant model where a grant
*owned* a hidden identity and revoking it deleted that identity (see
OAUT-12).

## OAUT-12: A pre-delegation grant keeps working exactly as before
A grant minted before per-user delegation existed (bound to a fixed
memory path rather than to a resolvable identity + workspace pin) keeps
reading and writing that same path after this feature shipped, and
revoking it still removes its own hidden identity as it always did — the
newer, identity-delegated grant model is additive, not a breaking
migration.

## OAUT-13: The admin grant listing reports each grant's pinned workspace
`GET /admin/grants` reports, for every grant, which workspace (if any)
it's pinned to and that workspace's current name — a grant pinned to
personal memory reports no team; a grant pinned to a team reports that
team's name. If the pinned team is later deleted, the grant still reports
that team id but no name, rather than silently falling back to personal
memory or resolving to some other team — that distinction is how an
admin tells "points at personal memory" apart from "points at something
that no longer exists."

## OAUT-14: PKCE is mandatory for every code exchange
An authorize request that never supplies a `code_challenge` (or supplies
a `code_challenge_method` other than `S256`) cannot produce a redeemable
code — this server advertises `S256` as the only supported PKCE method
(`spec/oauth-discovery.md` OAUD-04), and the whole point of PKCE is
binding the code to the client that requested it regardless of whether
that client can hold a secret. Enforced at three points: `GET /authorize`
rejects a missing/wrong-method challenge before the login form even
renders; `finishAuthorize` (the sole place a code is minted, reached from
every consent path) refuses to mint one without a valid challenge; and
`POST /token` refuses to redeem any code whose stored entry lacks one.
