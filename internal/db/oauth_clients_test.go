package db

import (
	"os"
	"slices"
	"testing"
)

// Scenarios: spec/oauth-registration.md (OAUR-03, OAUR-05)

func TestOAuthClientRegistration(t *testing.T) {
	f, err := os.CreateTemp("", "clients-reg-*.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	defer os.Remove(f.Name())

	reg, err := OpenRegistry(f.Name())
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	// An unknown client is nil, not an error: /authorize has to tell those two
	// apart to answer "register first" instead of "server error".
	got, err := reg.OAuthClient("never-seen")
	if err != nil {
		t.Fatalf("unknown client: %v", err)
	}
	if got != nil {
		t.Fatalf("unknown client should be nil, got %+v", got)
	}

	if err := reg.PutOAuthClient("", []string{"https://x.example/cb"}); err == nil {
		t.Error("a registration with no client_id should be refused")
	}

	// OAUR-03
	uris := []string{"https://claude.ai/api/mcp/auth_callback", "http://127.0.0.1:0/callback"}
	if err := reg.PutOAuthClient("claude-ai", uris); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, err = reg.OAuthClient("claude-ai")
	if err != nil || got == nil {
		t.Fatalf("read back: %v, %+v", err, got)
	}
	if !slices.Equal(got.RedirectURIs, uris) {
		t.Errorf("redirect_uris = %v, want %v", got.RedirectURIs, uris)
	}
	if got.CreatedAt.IsZero() {
		t.Error("created_at should be recorded")
	}

	// OAUR-05: re-registering replaces the list rather than appending to it or
	// being refused — a client may move its callback, and first-write-wins
	// would let anyone squat a client_id.
	if err := reg.PutOAuthClient("claude-ai", []string{"https://claude.ai/v2/cb"}); err != nil {
		t.Fatalf("re-register: %v", err)
	}
	got, _ = reg.OAuthClient("claude-ai")
	if !slices.Equal(got.RedirectURIs, []string{"https://claude.ai/v2/cb"}) {
		t.Errorf("re-registration should replace the URIs, got %v", got.RedirectURIs)
	}

	// The record has to outlive the process: /authorize enforces it on every
	// request, including the first one after a restart.
	if err := reg.Close(); err != nil {
		t.Fatal(err)
	}
	reg2, err := OpenRegistry(f.Name())
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reg2.Close()
	got, err = reg2.OAuthClient("claude-ai")
	if err != nil || got == nil {
		t.Fatalf("after restart: %v, %+v", err, got)
	}
	if !slices.Equal(got.RedirectURIs, []string{"https://claude.ai/v2/cb"}) {
		t.Errorf("after restart, redirect_uris = %v", got.RedirectURIs)
	}
}
