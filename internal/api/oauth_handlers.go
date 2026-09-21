package api

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"html/template"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/Ucok23/rekam/internal/db"
)

// The in-flight /authorize -> /token state (the minted code, and the
// workspace-picker consent ticket) is persisted in the registry — see
// db.OAuthCodeEntry/db.ConsentTicketEntry and the Engine passthroughs used
// below — rather than held in a process-local map, so the flow survives a
// restart and works correctly across more than one rekam instance.

// randomHex returns n random bytes hex-encoded.
func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// handleOAuthMeta serves GET /.well-known/oauth-authorization-server
func (s *Server) handleOAuthMeta(w http.ResponseWriter, r *http.Request) {
	scheme := "https"
	if r.Header.Get("X-Forwarded-Proto") == "http" {
		scheme = "http"
	}
	base := scheme + "://" + r.Host
	respond(w, http.StatusOK, map[string]any{
		"issuer":                           base,
		"authorization_endpoint":           base + "/authorize",
		"token_endpoint":                   base + "/token",
		"registration_endpoint":            base + "/register",
		"response_types_supported":         []string{"code"},
		"grant_types_supported":            []string{"authorization_code"},
		"code_challenge_methods_supported": []string{"S256"},
	})
}

// authorizeView holds the fields carried through the consent form.
type authorizeView struct {
	RedirectURI         string
	State               string
	CodeChallenge       string
	CodeChallengeMethod string
	ClientID            string
	Email               string // repopulated on the form after a failed submit
	Error               string

	// Destination is where the authorization code will be sent, filled in by
	// the renderers. The consent screen used to say only "this client",
	// which told the user nothing they could check.
	Destination string

	// Second step only: the workspace picker.
	Ticket     string
	Workspaces []workspaceChoice
}

// workspaceChoice is one selectable corpus on the consent form.
type workspaceChoice struct {
	ID   string // "" for personal memory
	Name string
	Role string
}

// authorizeForm is a deliberately minimal HTML consent page. Each user logs in
// with their own rekam email + password, so the minted grant is bound to their
// own memory store. html/template escapes every interpolated value, so the OAuth
// params (which arrive from the client) can't inject markup into the hidden inputs.
var authorizeForm = template.Must(template.New("authorize").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>rekam — authorize</title>
</head>
<body style="font-family: system-ui, -apple-system, sans-serif; max-width: 360px; margin: 72px auto; padding: 0 16px; color: #222;">
<h1 style="font-size: 20px; margin: 0 0 8px;">Authorize rekam</h1>
<p style="color:#555; font-size: 13.5px; line-height: 1.5; margin: 0 0 20px;">
Log in with your rekam account to grant this client access to your memory store.
</p>
<p style="color:#555; font-size: 13.5px; line-height: 1.5; margin: 0 0 20px;">
Access will be handed to <strong style="color:#222;">{{.Destination}}</strong>. If you don't recognise it, close this page.
</p>
{{if .Error}}<p style="color:#b00020; font-size: 13.5px; margin: 0 0 14px;">{{.Error}}</p>{{end}}
<form method="POST" action="/authorize">
  <input type="hidden" name="redirect_uri" value="{{.RedirectURI}}">
  <input type="hidden" name="state" value="{{.State}}">
  <input type="hidden" name="code_challenge" value="{{.CodeChallenge}}">
  <input type="hidden" name="code_challenge_method" value="{{.CodeChallengeMethod}}">
  <input type="hidden" name="client_id" value="{{.ClientID}}">
  <label for="email" style="display:block; font-size:13px; margin-bottom:6px; color:#444;">Email</label>
  <input id="email" type="email" name="email" autocomplete="username" value="{{.Email}}" autofocus
         style="width:100%; padding:9px; font-size:14px; box-sizing:border-box; border:1px solid #ccc; border-radius:6px;">
  <label for="pw" style="display:block; font-size:13px; margin:12px 0 6px; color:#444;">Password</label>
  <input id="pw" type="password" name="password" autocomplete="current-password"
         style="width:100%; padding:9px; font-size:14px; box-sizing:border-box; border:1px solid #ccc; border-radius:6px;">
  <button type="submit"
          style="margin-top:14px; width:100%; padding:9px 16px; font-size:14px; border:0; border-radius:6px; background:#1a1a1a; color:#fff; cursor:pointer;">
    Log in &amp; authorize
  </button>
</form>
</body>
</html>`))

// workspaceForm is the second consent step, shown only when the user has more
// than one workspace. The grant is pinned to whatever they pick here: an MCP
// connection reads and writes that corpus and no other, so a client connected for
// one team cannot reach another. Adding a second workspace means authorizing a
// second connector.
var workspaceForm = template.Must(template.New("workspace").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>rekam — choose workspace</title>
</head>
<body style="font-family: system-ui, -apple-system, sans-serif; max-width: 380px; margin: 72px auto; padding: 0 16px; color: #222;">
<h1 style="font-size: 20px; margin: 0 0 8px;">Choose a workspace</h1>
<p style="color:#555; font-size: 13.5px; line-height: 1.5; margin: 0 0 20px;">
This connection will read and write only the workspace you pick, and will be handed to <strong style="color:#222;">{{.Destination}}</strong>. To connect another one, authorize again.
</p>
{{if .Error}}<p style="color:#b00020; font-size: 13.5px; margin: 0 0 14px;">{{.Error}}</p>{{end}}
<form method="POST" action="/authorize/workspace">
  <input type="hidden" name="ticket" value="{{.Ticket}}">
  {{range $i, $w := .Workspaces}}
  <label style="display:flex; align-items:center; gap:10px; padding:11px 12px; margin-bottom:8px; border:1px solid #ccc; border-radius:6px; cursor:pointer;">
    <input type="radio" name="team_id" value="{{$w.ID}}"{{if eq $i 0}} checked{{end}}>
    <span>
      <span style="display:block; font-size:14px;">{{$w.Name}}</span>
      <span style="display:block; font-size:11.5px; color:#777; text-transform:uppercase; letter-spacing:.06em;">{{$w.Role}}</span>
    </span>
  </label>
  {{end}}
  <button type="submit"
          style="margin-top:14px; width:100%; padding:9px 16px; font-size:14px; border:0; border-radius:6px; background:#1a1a1a; color:#fff; cursor:pointer;">
    Authorize
  </button>
</form>
</body>
</html>`))

func renderWorkspaceForm(w http.ResponseWriter, v authorizeView, status int) {
	v.Destination = redirectDestination(v.RedirectURI)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = workspaceForm.Execute(w, v)
}

func renderAuthorizeForm(w http.ResponseWriter, v authorizeView, status int) {
	v.Destination = redirectDestination(v.RedirectURI)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = authorizeForm.Execute(w, v)
}

// redirectDestination describes, in a few words a person can act on, where the
// authorization code is about to go. The host is the part worth checking, so
// it leads; a loopback address means the app asking is running on the same
// machine as the browser, which is worth saying plainly rather than showing
// "127.0.0.1" and leaving the reader to work it out.
func redirectDestination(redirectURI string) string {
	u, err := url.Parse(redirectURI)
	if err != nil || u.Host == "" {
		return "an unnamed destination"
	}
	if isLoopback(u) {
		return "an application on this device"
	}
	return u.Hostname()
}

// validPKCE reports whether an authorize request's PKCE parameters are usable:
// a non-empty challenge with the sole method discovery advertises (OAUD-04).
// Discovery advertising S256 as the only supported method is a promise this
// server has to keep everywhere a code gets minted, not just where it's
// convenient — see OAUT-14.
func validPKCE(challenge, method string) bool {
	return challenge != "" && method == "S256"
}

// checkRedirectURI reports whether a code may be delivered to this URI for
// this client, returning a message suitable for a 400 when it may not.
//
// OAUA-14. Until this existed, /authorize sent the code wherever the request
// asked: a crafted link plus a user who logs in handed the attacker a working
// token for that user's memory. PKCE does not help here — an attacker who
// starts the flow picks the challenge and so holds the verifier.
//
// The error is always rendered on this server. Redirecting to report a bad
// redirect_uri would defeat the check, so an unregistered destination never
// receives anything, not even an error (RFC 6749 §4.1.2.1).
func (s *Server) checkRedirectURI(clientID, redirectURI string) string {
	if clientID == "" {
		return "client_id required"
	}
	client, err := s.eng.OAuthClient(clientID)
	if err != nil {
		return "server error"
	}
	// Unknown client: registration is open (RFC 7591) and cheap, so the
	// honest fix is to register, which also records where codes may go.
	// Accepting unknown clients instead would leave the hole wide open,
	// since an attacker can always invent a client_id.
	if client == nil {
		return "unknown client_id — register at /register first"
	}
	if !redirectURIAllowed(client.RedirectURIs, redirectURI) {
		return "redirect_uri is not registered for this client"
	}
	return ""
}

// handleAuthorizeForm serves GET /authorize — the HTML consent form.
// claude.ai opens this in a browser popup, so a plain HTML form works without
// any client-side change.
func (s *Server) handleAuthorizeForm(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("redirect_uri") == "" {
		http.Error(w, "redirect_uri required", http.StatusBadRequest)
		return
	}
	// OAUT-14: reject before even asking for a password — a client that never
	// supplies a code_challenge must not be able to walk a user through the
	// rest of the flow only to fail at the last step.
	if !validPKCE(q.Get("code_challenge"), q.Get("code_challenge_method")) {
		http.Error(w, "code_challenge with code_challenge_method=S256 is required", http.StatusBadRequest)
		return
	}
	// OAUA-14: for the same reason — a user must never be shown a password
	// prompt for a request whose code could not legitimately be delivered.
	if msg := s.checkRedirectURI(q.Get("client_id"), q.Get("redirect_uri")); msg != "" {
		http.Error(w, msg, http.StatusBadRequest)
		return
	}
	// Always render the email + password login form. claude.ai opens /authorize
	// in a fresh cross-site popup with no cookie, so we authenticate the user
	// here rather than relying on an existing browser session.
	renderAuthorizeForm(w, authorizeView{
		RedirectURI:         q.Get("redirect_uri"),
		State:               q.Get("state"),
		CodeChallenge:       q.Get("code_challenge"),
		CodeChallengeMethod: q.Get("code_challenge_method"),
		ClientID:            q.Get("client_id"),
	}, http.StatusOK)
}

// handleAuthorizeSubmit serves POST /authorize — authenticates the user's rekam
// email + password and redirects back to the client with an authorization code
// bound to that user's identity, so the minted token scopes to their own store.
func (s *Server) handleAuthorizeSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	// OAUA-14 before any credential is touched: finishAuthorize would refuse
	// this anyway, but checking here keeps /authorize from doubling as a
	// password oracle for a client that could never receive a code.
	if msg := s.checkRedirectURI(r.FormValue("client_id"), r.FormValue("redirect_uri")); msg != "" {
		http.Error(w, msg, http.StatusBadRequest)
		return
	}

	// Re-render the consent form with an error, preserving the OAuth params and
	// the entered email so the user can retry.
	fail := func(msg string) {
		renderAuthorizeForm(w, authorizeView{
			RedirectURI:         r.FormValue("redirect_uri"),
			State:               r.FormValue("state"),
			CodeChallenge:       r.FormValue("code_challenge"),
			CodeChallengeMethod: r.FormValue("code_challenge_method"),
			ClientID:            r.FormValue("client_id"),
			Email:               r.FormValue("email"),
			Error:               msg,
		}, http.StatusUnauthorized)
	}

	identity, err := s.eng.AuthenticatePassword(r.FormValue("email"), r.FormValue("password"))
	if err != nil {
		fail("Incorrect email or password. Try again.")
		return
	}
	confirmed, err := s.eng.Confirmed(identity.ID)
	if err != nil {
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	if !confirmed {
		fail("Confirm your email address first, then authorize.")
		return
	}
	view := authorizeView{
		RedirectURI:         r.FormValue("redirect_uri"),
		State:               r.FormValue("state"),
		CodeChallenge:       r.FormValue("code_challenge"),
		CodeChallengeMethod: r.FormValue("code_challenge_method"),
		ClientID:            r.FormValue("client_id"),
	}

	// Which workspaces can this person act in? A grant is pinned to exactly one,
	// so when there is a choice to make, make it here rather than defaulting to
	// personal memory and leaving the user no way to reach their teams.
	choices := s.workspacesFor(identity.ID)
	if len(choices) < 2 {
		s.finishAuthorize(w, r, view, identity.ID, "")
		return
	}

	ticket, err := randomHex(16)
	if err != nil {
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	ids := make([]string, 0, len(choices))
	for _, c := range choices {
		ids = append(ids, c.ID)
	}
	err = s.eng.StoreConsentTicket(ticket, db.ConsentTicketEntry{
		IdentityID:          identity.ID,
		Teams:               ids,
		RedirectURI:         view.RedirectURI,
		State:               view.State,
		CodeChallenge:       view.CodeChallenge,
		CodeChallengeMethod: view.CodeChallengeMethod,
		ClientID:            view.ClientID,
		Expiry:              time.Now().Add(10 * time.Minute),
	})
	if err != nil {
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	view.Ticket = ticket
	view.Workspaces = choices
	renderWorkspaceForm(w, view, http.StatusOK)
}

// handleAuthorizeWorkspace serves POST /authorize/workspace — the second consent
// step. The ticket proves the password was already accepted; it is single-use, so
// a replayed form cannot mint a second code.
func (s *Server) handleAuthorizeWorkspace(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	entry, ok, err := s.eng.TakeConsentTicket(r.FormValue("ticket"))
	if err != nil {
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "this authorization expired — start again", http.StatusBadRequest)
		return
	}
	// Only a workspace we actually offered. Membership is enforced again when the
	// grant is minted; this just keeps a tampered form from getting that far.
	team := r.FormValue("team_id")
	if !slices.Contains(entry.Teams, team) {
		http.Error(w, "unknown workspace", http.StatusBadRequest)
		return
	}
	view := authorizeView{
		RedirectURI:         entry.RedirectURI,
		State:               entry.State,
		CodeChallenge:       entry.CodeChallenge,
		CodeChallengeMethod: entry.CodeChallengeMethod,
		ClientID:            entry.ClientID,
	}
	s.finishAuthorize(w, r, view, entry.IdentityID, team)
}

// workspacesFor lists the corpora an identity may authorize against, personal
// first. A failure to enumerate teams degrades to personal-only rather than
// blocking the login.
func (s *Server) workspacesFor(identityID string) []workspaceChoice {
	teams, err := s.eng.MyTeams(s.eng.SessionTokenForTeam(identityID, ""))
	if err != nil {
		return nil
	}
	out := make([]workspaceChoice, 0, len(teams))
	for _, t := range teams {
		name := t.Name
		if t.Home {
			name = "Personal memory"
		}
		out = append(out, workspaceChoice{ID: t.ID, Name: name, Role: string(t.Role)})
	}
	return out
}

// finishAuthorize mints the authorization code and hands control back to the
// client. The chosen workspace rides on the code so the token exchange can pin
// the grant to it.
func (s *Server) finishAuthorize(w http.ResponseWriter, r *http.Request, v authorizeView, identityID, teamID string) {
	// OAUT-14: the actual mint point, reached from every path (direct POST
	// /authorize, or via the workspace-picker ticket) — a code must never be
	// issued without a valid PKCE challenge attached, regardless of whether an
	// earlier step already checked. This is the enforcement of last resort.
	if !validPKCE(v.CodeChallenge, v.CodeChallengeMethod) {
		http.Error(w, "code_challenge with code_challenge_method=S256 is required", http.StatusBadRequest)
		return
	}
	// OAUA-14, same enforcement-of-last-resort argument: the destination is
	// re-checked at the mint point, so no path (direct POST /authorize, or a
	// consent ticket carrying a redirect_uri stored ten minutes ago) can
	// deliver a code somewhere the client never registered.
	if msg := s.checkRedirectURI(v.ClientID, v.RedirectURI); msg != "" {
		http.Error(w, msg, http.StatusBadRequest)
		return
	}
	code, err := randomHex(16)
	if err != nil {
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	if err := s.eng.StoreOAuthCode(code, db.OAuthCodeEntry{
		CodeChallenge: v.CodeChallenge,
		ClientID:      v.ClientID,
		IdentityID:    identityID,
		TeamID:        teamID,
		Expiry:        time.Now().Add(10 * time.Minute),
	}); err != nil {
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}

	u, err := url.Parse(v.RedirectURI)
	if err != nil {
		http.Error(w, "invalid redirect_uri", http.StatusBadRequest)
		return
	}
	q := u.Query()
	q.Set("code", code)
	if v.State != "" {
		q.Set("state", v.State)
	}
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

// handleOAuthToken serves POST /token — exchanges an auth code for a freshly
// minted, revocable per-grant API key bound to the user who authorized it, so
// the token scopes to that user's own memory store.
func (s *Server) handleOAuthToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		respond(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}

	code := r.FormValue("code")
	codeVerifier := r.FormValue("code_verifier")

	entry, ok, err := s.eng.TakeOAuthCode(code)
	if err != nil {
		respond(w, http.StatusInternalServerError, map[string]string{
			"error":             "server_error",
			"error_description": err.Error(),
		})
		return
	}
	if !ok {
		respond(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
		return
	}

	// OAUT-14: PKCE is mandatory, not opportunistic — finishAuthorize should
	// never mint a code without a challenge, but this is the redemption side's
	// own check so a code somehow missing one (a future path that forgets the
	// earlier gate) still can't be exchanged for a token.
	if entry.CodeChallenge == "" {
		respond(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
		return
	}
	h := sha256.Sum256([]byte(codeVerifier))
	got := base64.RawURLEncoding.EncodeToString(h[:])
	if subtle.ConstantTimeCompare([]byte(got), []byte(entry.CodeChallenge)) != 1 {
		respond(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
		return
	}

	rawKey, grantID, err := s.eng.MintGrantByID(entry.IdentityID, entry.ClientID, entry.TeamID)
	if err != nil {
		respond(w, http.StatusBadRequest, map[string]string{
			"error":             "invalid_grant",
			"error_description": err.Error(),
		})
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	respond(w, http.StatusOK, map[string]any{
		"id":           grantID,
		"identity_id":  entry.IdentityID,
		"access_token": rawKey,
		"token_type":   "bearer",
	})
}

// handleOAuthRegister serves POST /register — dynamic client registration (RFC 7591).
// Accepts any client and echoes back a client_id so claude.ai can proceed.
func (s *Server) handleOAuthRegister(w http.ResponseWriter, r *http.Request) {
	var req map[string]any
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}

	clientID, _ := req["client_id"].(string)
	if clientID == "" {
		b := make([]byte, 8)
		rand.Read(b)
		clientID = hex.EncodeToString(b)
	}

	// OAUR-07: the registration is the only record of where this client is
	// allowed to receive a code, so it has to be kept. A client that declares
	// no redirect_uris could never complete an authorization_code flow, and
	// accepting the registration would only produce a confusing rejection
	// later at /authorize — refuse it here, where the error can say why.
	uris := stringList(req["redirect_uris"])
	if len(uris) == 0 {
		respond(w, http.StatusBadRequest, map[string]string{
			"error":             "invalid_redirect_uri",
			"error_description": "redirect_uris must list at least one URI",
		})
		return
	}
	if err := s.eng.PutOAuthClient(clientID, uris); err != nil {
		respond(w, http.StatusInternalServerError, map[string]string{"error": "server_error"})
		return
	}

	respond(w, http.StatusCreated, map[string]any{
		"client_id":                  clientID,
		"redirect_uris":              uris,
		"grant_types":                []string{"authorization_code"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	})
}

// stringList coerces a decoded JSON value into a list of non-empty strings,
// dropping anything that isn't one. Registration bodies come from arbitrary
// clients, so a wrong-typed field must not panic the handler.
func stringList(v any) []string {
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		if s, ok := it.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

// requireAdmin reports whether the request is authorized for the admin surface.
// Two credentials are accepted: the legacy Bearer admin key (for CLI/curl), or
// an authenticated identity (Bearer key or browser session) whose is_admin flag
// is set.
func (s *Server) requireAdmin(r *http.Request) bool {
	bearer := apiKeyFromRequest(r)
	if s.adminKey != "" &&
		subtle.ConstantTimeCompare([]byte(bearer), []byte(s.adminKey)) == 1 {
		return true
	}
	if id, err := s.eng.GetCurrentIdentity(bearer); err == nil && id.IsAdmin {
		return true
	}
	return false
}

// handleListGrants serves GET /admin/grants — lists OAuth grants. Admin only.
func (s *Server) handleListGrants(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(r) {
		respond(w, http.StatusUnauthorized, map[string]string{"error": "admin key required"})
		return
	}
	grants, err := s.eng.ListGrants()
	if err != nil {
		respond(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if grants == nil {
		grants = []*db.Grant{}
	}
	respond(w, http.StatusOK, map[string]any{"grants": grants})
}

// handleRevokeGrant serves DELETE /admin/grants/{id} — revokes a grant. Admin only.
func (s *Server) handleRevokeGrant(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(r) {
		respond(w, http.StatusUnauthorized, map[string]string{"error": "admin key required"})
		return
	}
	id := r.PathValue("id")
	if err := s.eng.RevokeGrant(id); err != nil {
		respond(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	respond(w, http.StatusOK, map[string]string{"revoked": id})
}
