package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Ucok23/rekam/internal/email"
)

// Scenarios: spec/identity.md (IDNT-21)
//
// Password-reset mail, which every edition sends. Signup mail is managed-only
// (signup_managed_test.go) and invite mail comes with teams
// (email_invite_team_test.go).

// fakeEmailer is an in-memory email.Emailer for tests: configured/fail are
// set up front to model the two things trySend branches on, and sent records
// what actually went out so a test can assert on To/Subject/Text.
type fakeEmailer struct {
	configured bool
	fail       bool
	sent       []email.Message
}

func (f *fakeEmailer) Configured() bool { return f.configured }

func (f *fakeEmailer) Send(_ context.Context, msg email.Message) error {
	if f.fail {
		return errors.New("fake send failure")
	}
	f.sent = append(f.sent, msg)
	return nil
}

// Forgot-password: same contract as signup — sent means no token in the
// response, this being the more security-sensitive of the two (a reset token
// left in the response is a full account takeover for anyone who knows the
// victim's email, not just an inconvenience).
func TestForgotPasswordEmailsResetLinkWhenConfigured(t *testing.T) {
	srv, _, _, usersDir, cleanup := oauthServer(t)
	defer cleanup()
	newConfirmedUser(t, srv, usersDir, "alice@example.com", "old-password-1")
	fe := &fakeEmailer{configured: true}
	srv.SetEmailer(fe)

	rr := postJSON(t, srv, "POST", "/ui/forgot", `{"email":"alice@example.com"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("forgot: want 200, got %d: %s", rr.Code, rr.Body)
	}
	var resp struct {
		EmailSent bool   `json:"email_sent"`
		ResetCode string `json:"reset_code"`
		ResetURL  string `json:"reset_url"`
	}
	decodeJSON(t, rr, &resp)
	if !resp.EmailSent {
		t.Error("email_sent = false, want true")
	}
	if resp.ResetCode != "" || resp.ResetURL != "" {
		t.Errorf("reset token leaked into the response (code=%q url=%q) even though the email was sent", resp.ResetCode, resp.ResetURL)
	}
	if len(fe.sent) != 1 || fe.sent[0].To != "alice@example.com" {
		t.Fatalf("emailer sent = %+v, want one message to alice@example.com", fe.sent)
	}
	if !strings.Contains(fe.sent[0].Text, "/ui/reset?token=") {
		t.Errorf("email text = %q, want a reset link", fe.sent[0].Text)
	}
}

func TestForgotPasswordFallsBackToResponseTokenWhenSendFails(t *testing.T) {
	srv, _, _, usersDir, cleanup := oauthServer(t)
	defer cleanup()
	newConfirmedUser(t, srv, usersDir, "bob@example.com", "old-password-1")
	fe := &fakeEmailer{configured: true, fail: true}
	srv.SetEmailer(fe)

	rr := postJSON(t, srv, "POST", "/ui/forgot", `{"email":"bob@example.com"}`)
	var resp struct {
		EmailSent bool   `json:"email_sent"`
		ResetCode string `json:"reset_code"`
	}
	decodeJSON(t, rr, &resp)
	if resp.EmailSent {
		t.Error("email_sent = true, want false (the fake send failed)")
	}
	if resp.ResetCode == "" {
		t.Error("reset_code missing from the response even though the send failed — the user would be locked out")
	}
}

// Unknown email: the no-account branch is untouched by any of this — there is
// no token to email in the first place, and it must keep looking the same as
// the real-account branch either way (see handleUIForgot's doc comment).
func TestForgotPasswordUnknownEmailNeverAttemptsToSend(t *testing.T) {
	srv, _, _, _, cleanup := oauthServer(t)
	defer cleanup()
	fe := &fakeEmailer{configured: true}
	srv.SetEmailer(fe)

	rr := postJSON(t, srv, "POST", "/ui/forgot", `{"email":"nobody@example.com"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("forgot: want 200, got %d: %s", rr.Code, rr.Body)
	}
	if len(fe.sent) != 0 {
		t.Errorf("emailer got %d sends for an unknown email, want 0", len(fe.sent))
	}
}
