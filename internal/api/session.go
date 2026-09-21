package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/Ucok23/rekam/internal/db"
	"github.com/Ucok23/rekam/internal/email"
)

// sessionCookie is the name of the httpOnly cookie holding a browser session id.
const sessionCookie = "rekam_session"

// sessionTTL bounds how long a browser login stays valid.
const sessionTTL = 7 * 24 * time.Hour

// Sessions are persisted in the registry — see db.Registry.StoreSession and
// Engine.CreateSession/ResolveSession/DeleteSession — not held in an
// in-process map. A plain in-memory store meant every process restart (i.e.
// every deploy) silently logged everyone out, since the browser's cookie
// outlived the server's memory of it; persisting alongside OAuth codes/
// consents in the registry fixes that the same way those were fixed. Only the
// identity id lives server-side; the browser holds nothing but the opaque
// httpOnly cookie. See spec/identity.md IDNT-20.

// sessionAuth translates a valid session cookie into a Bearer Authorization
// header carrying a server-signed session token (which resolves to the session's
// identity). Downstream handlers stay unchanged; a real Bearer header always wins.
func (s *Server) sessionAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			if c, err := r.Cookie(sessionCookie); err == nil {
				if identityID, ok, err := s.eng.ResolveSession(c.Value); err == nil && ok {
					r.Header.Set("Authorization", "Bearer "+s.eng.SessionToken(identityID))
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

// isHTTPS reports whether the request arrived over TLS.
func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

// requestBaseURL builds the absolute origin (scheme://host) a link in an
// email needs — unlike everything else these handlers emit, an email leaves
// the app, so a relative "/ui/confirm?..." path has nowhere to resolve against.
func requestBaseURL(r *http.Request) string {
	scheme := "http"
	if isHTTPS(r) {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// trySend attempts a best-effort email and reports whether it actually went
// out. False covers two different situations identically on purpose: no
// Emailer is configured (email.Noop — the common case today), or one is
// configured but the send itself failed (Resend down, bad key, whatever). In
// both cases the caller's job is the same — fall back to returning the token
// directly in the API response, exactly as every one of these endpoints did
// before this package existed, so a Resend outage degrades to "manual
// delivery" instead of stranding the user.
func (s *Server) trySend(r *http.Request, to, subject, text string) bool {
	if !s.emailer.Configured() {
		return false
	}
	if err := s.emailer.Send(r.Context(), email.Message{To: to, Subject: subject, Text: text}); err != nil {
		return false
	}
	return true
}

// currentBrowserIdentity resolves the browser's authenticated identity from an
// httpOnly session cookie. If no valid session exists it returns nil and a
// redirect target; callers can bounce the user to /ui/login instead.
func (s *Server) currentBrowserIdentity(r *http.Request) (*db.Identity, string) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return nil, "/ui/login"
	}
	identityID, ok, err := s.eng.ResolveSession(c.Value)
	if err != nil || !ok {
		return nil, "/ui/login"
	}
	identity, err := s.eng.IdentityByID(identityID)
	if err != nil {
		return nil, "/ui/login"
	}
	return identity, ""
}

func identityView(id *db.Identity) map[string]any {
	return map[string]any{
		"id":          id.ID,
		"name":        id.Name,
		"email":       id.Email,
		"allow_write": id.AllowWrite,
		"is_admin":    id.IsAdmin,
		"created_at":  id.CreatedAt.Format(time.RFC3339),
	}
}

// setSessionCookie starts a browser session for identityID and writes the cookie.
func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, identityID string) error {
	sid, err := randomHex(24)
	if err != nil {
		return err
	}
	if err := s.eng.CreateSession(sid, identityID, sessionTTL); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    sid,
		Path:     "/",
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(sessionTTL),
		MaxAge:   int(sessionTTL.Seconds()),
	})
	return nil
}

// credentials is the JSON/form body accepted by login and signup.
type credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name"`
}

func readCredentials(r *http.Request) credentials {
	var c credentials
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		json.NewDecoder(r.Body).Decode(&c)
	} else {
		r.ParseForm()
		c.Email = r.FormValue("email")
		c.Password = r.FormValue("password")
		c.Name = r.FormValue("name")
	}
	return c
}

// handleUISignup serves POST /ui/signup — creates an unconfirmed account. The
// caller must follow the returned confirmation link/code before the account can
// be used to log in. Canonical duplicate: POST /signup.
func (s *Server) handleUISignup(w http.ResponseWriter, r *http.Request) {
	if s.usersDir == "" {
		respond(w, http.StatusServiceUnavailable, map[string]string{"error": "signup disabled: no users directory configured"})
		return
	}
	c := readCredentials(r)
	suffix, err := randomHex(12)
	if err != nil {
		respond(w, http.StatusInternalServerError, map[string]string{"error": "server error"})
		return
	}
	memoryPath := filepath.Join(s.usersDir, "user-"+suffix+".rekam")
	identity, token, err := s.eng.CreateUnconfirmed(c.Name, c.Email, c.Password, memoryPath, true)
	if err != nil {
		s.respondErr(w, r, err)
		return
	}
	confirmURL := requestBaseURL(r) + "/ui/confirm?token=" + token
	sent := s.trySend(r, identity.Email, "Confirm your rekam account",
		fmt.Sprintf("Welcome to rekam.\n\nConfirm your account to get started:\n%s\n\nThis link expires in 24 hours.", confirmURL))

	resp := map[string]any{
		"status":     "unconfirmed",
		"email_sent": sent,
		"identity":   identityView(identity),
	}
	if !sent {
		// No provider configured, or the send failed — fall back to the
		// manual-delivery convention every token in this system uses.
		resp["confirm_url"] = "/ui/confirm?token=" + token
		resp["confirm_code"] = token
	}
	respond(w, http.StatusAccepted, resp)
}

// handleUIConfirm serves GET /ui/confirm?token=... — marks the account as
// confirmed and logs the user in by setting the session cookie.
func (s *Server) handleUIConfirm(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if strings.TrimSpace(token) == "" {
		respond(w, http.StatusBadRequest, map[string]string{"error": "token is required"})
		return
	}
	id, confirmed, err := s.eng.ConfirmEmail(token)
	if err != nil {
		s.respondErr(w, r, err)
		return
	}
	if !confirmed {
		respond(w, http.StatusBadRequest, map[string]string{"error": "invalid or expired token"})
		return
	}
	if err := s.setSessionCookie(w, r, id.ID); err != nil {
		respond(w, http.StatusInternalServerError, map[string]string{"error": "server error"})
		return
	}
	respond(w, http.StatusOK, map[string]any{"authenticated": true, "identity": identityView(id)})
}

// handleUILogin serves POST /ui/login — email + password authentication that
// establishes a browser session scoped to that user's own identity/memory.
// Unconfirmed accounts are rejected so they must finish the confirmation flow.
func (s *Server) handleUILogin(w http.ResponseWriter, r *http.Request) {
	c := readCredentials(r)
	identity, err := s.eng.AuthenticatePassword(c.Email, c.Password)
	if err != nil {
		respond(w, http.StatusUnauthorized, map[string]string{"error": "invalid email or password"})
		return
	}
	confirmed, err := s.eng.Confirmed(identity.ID)
	if err != nil {
		respond(w, http.StatusInternalServerError, map[string]string{"error": "server error"})
		return
	}
	if !confirmed {
		respond(w, http.StatusForbidden, map[string]string{
			"error":  "email not confirmed",
			"resend": "/ui/confirm?resend=" + identity.Email,
		})
		return
	}
	if err := s.setSessionCookie(w, r, identity.ID); err != nil {
		respond(w, http.StatusInternalServerError, map[string]string{"error": "server error"})
		return
	}
	respond(w, http.StatusOK, map[string]any{"authenticated": true, "identity": identityView(identity)})
}

// handleUIForgot serves POST /ui/forgot — generates a password-reset token for
// the given email without revealing whether the account exists. The token is
// emailed to that address when an Emailer is configured (see trySend); only
// falls back to returning it directly in the response — the pre-email
// "manual/UI delivery" behavior — when no provider is configured or the send
// itself fails, since otherwise a Resend outage would strand the user with no
// way to recover their account.
func (s *Server) handleUIForgot(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	id, token, err := s.eng.ForgotPassword(req.Email)
	if err != nil {
		s.respondErr(w, r, err)
		return
	}
	if id == nil {
		respond(w, http.StatusOK, map[string]any{
			"status":     "no_account",
			"reset_code": "",
		})
		return
	}
	resetURL := requestBaseURL(r) + "/ui/reset?token=" + token
	sent := s.trySend(r, id.Email, "Reset your rekam password",
		fmt.Sprintf("Someone (hopefully you) requested a password reset for your rekam account.\n\nReset it here:\n%s\n\nThis link expires in 30 minutes. If you didn't request this, you can safely ignore this email.", resetURL))

	resp := map[string]any{
		"status":     "ok",
		"email_sent": sent,
		"email":      id.Email,
	}
	if !sent {
		resp["reset_url"] = "/ui/reset?token=" + token
		resp["reset_code"] = token
	}
	respond(w, http.StatusOK, resp)
}

// handleUIReset serves POST /ui/reset?token=... — validates the reset token
// and sets a new password.
func (s *Server) handleUIReset(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if strings.TrimSpace(token) == "" {
		respond(w, http.StatusBadRequest, map[string]string{"error": "token is required"})
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	id, err := s.eng.ResetPassword(token, req.Password)
	if err != nil {
		s.respondErr(w, r, err)
		return
	}
	if err := s.setSessionCookie(w, r, id.ID); err != nil {
		respond(w, http.StatusInternalServerError, map[string]string{"error": "server error"})
		return
	}
	respond(w, http.StatusOK, map[string]any{"authenticated": true, "identity": identityView(id)})
}

// handleUISession serves GET /ui/session — reports whether the browser has a
// valid session, and if so the identity it maps to.
func (s *Server) handleUISession(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		respond(w, http.StatusOK, map[string]any{"authenticated": false})
		return
	}
	identityID, ok, err := s.eng.ResolveSession(c.Value)
	if err != nil || !ok {
		respond(w, http.StatusOK, map[string]any{"authenticated": false})
		return
	}
	identity, err := s.eng.IdentityByID(identityID)
	if err != nil {
		respond(w, http.StatusOK, map[string]any{"authenticated": false})
		return
	}
	respond(w, http.StatusOK, map[string]any{"authenticated": true, "identity": identityView(identity)})
}

// handleUILogout serves POST /ui/logout — drops the session and clears the cookie.
func (s *Server) handleUILogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.eng.DeleteSession(c.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
	respond(w, http.StatusOK, map[string]any{"ok": true})
}
