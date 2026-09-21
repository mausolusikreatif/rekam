package db

import (
	"os"
	"testing"
	"time"
)

func TestRegistry(t *testing.T) {
	f, err := os.CreateTemp("", "registry-*.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	defer os.Remove(f.Name())

	reg, err := OpenRegistry(f.Name())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer reg.Close()

	rawKey := "test-key-alice"
	alice, err := reg.Create("alice", rawKey, "alice.memory", true)
	if err != nil {
		t.Fatalf("create alice: %v", err)
	}

	bob, err := reg.Create("bob", "test-key-bob", "bob.memory", false)
	if err != nil {
		t.Fatalf("create bob: %v", err)
	}
	if bob.AllowWrite != false {
		t.Error("expected allow_write=false for bob")
	}

	got, err := reg.Resolve(rawKey)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.Name != "alice" {
		t.Errorf("name: want alice, got %s", got.Name)
	}
	if got.MemoryPath != "alice.memory" {
		t.Errorf("memory_path: want alice.memory, got %s", got.MemoryPath)
	}

	_, err = reg.Resolve("nope")
	if err == nil {
		t.Error("expected error for unknown key")
	}

	ids, err := reg.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 {
		t.Errorf("want 2 identities, got %d", len(ids))
	}

	if err := reg.Delete(bob.ID); err != nil {
		t.Fatal(err)
	}
	ids, _ = reg.List()
	if len(ids) != 1 {
		t.Errorf("want 1 after delete, got %d", len(ids))
	}

	_ = alice
	if err := reg.Delete("does-not-exist"); err == nil {
		t.Error("expected error deleting non-existent id")
	}
}

func TestOAuthCodeRoundTripAndSingleUse(t *testing.T) {
	f, err := os.CreateTemp("", "registry-oauth-*.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	defer os.Remove(f.Name())

	reg, err := OpenRegistry(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()

	entry := OAuthCodeEntry{
		CodeChallenge: "abc123",
		ClientID:      "claude-ai",
		IdentityID:    "identity-1",
		TeamID:        "team-1",
		Expiry:        time.Now().Add(10 * time.Minute),
	}
	if err := reg.StoreOAuthCode("code-1", entry); err != nil {
		t.Fatal(err)
	}

	got, ok, err := reg.TakeOAuthCode("code-1")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected code-1 to be found")
	}
	if got.CodeChallenge != entry.CodeChallenge || got.ClientID != entry.ClientID ||
		got.IdentityID != entry.IdentityID || got.TeamID != entry.TeamID {
		t.Errorf("round-tripped entry = %+v, want %+v", got, entry)
	}

	// Single-use: a second take of the same code finds nothing.
	if _, ok, err := reg.TakeOAuthCode("code-1"); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Error("code-1 was redeemable a second time")
	}

	// An expired code is consumed but reported not found.
	if err := reg.StoreOAuthCode("code-2", OAuthCodeEntry{
		ClientID: "claude-ai",
		Expiry:   time.Now().Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := reg.TakeOAuthCode("code-2"); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Error("expired code-2 should not be takeable")
	}

	// Unknown code.
	if _, ok, err := reg.TakeOAuthCode("never-existed"); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Error("unknown code should not be found")
	}
}

func TestConsentTicketRoundTripAndSingleUse(t *testing.T) {
	f, err := os.CreateTemp("", "registry-consent-*.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	defer os.Remove(f.Name())

	reg, err := OpenRegistry(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()

	entry := ConsentTicketEntry{
		IdentityID:          "identity-1",
		Teams:               []string{"team-1", "team-2"},
		RedirectURI:         "https://claude.ai/callback",
		State:               "xyz",
		CodeChallenge:       "abc123",
		CodeChallengeMethod: "S256",
		ClientID:            "claude-ai",
		Expiry:              time.Now().Add(10 * time.Minute),
	}
	if err := reg.StoreConsentTicket("ticket-1", entry); err != nil {
		t.Fatal(err)
	}

	got, ok, err := reg.TakeConsentTicket("ticket-1")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected ticket-1 to be found")
	}
	if got.IdentityID != entry.IdentityID || got.RedirectURI != entry.RedirectURI ||
		got.State != entry.State || got.CodeChallenge != entry.CodeChallenge ||
		got.CodeChallengeMethod != entry.CodeChallengeMethod || got.ClientID != entry.ClientID ||
		len(got.Teams) != 2 || got.Teams[0] != "team-1" || got.Teams[1] != "team-2" {
		t.Errorf("round-tripped entry = %+v, want %+v", got, entry)
	}

	if _, ok, err := reg.TakeConsentTicket("ticket-1"); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Error("ticket-1 was redeemable a second time")
	}
}

func TestInviteRoundTrip(t *testing.T) {
	f, err := os.CreateTemp("", "registry-invite-*.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	defer os.Remove(f.Name())

	reg, err := OpenRegistry(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()

	token, err := reg.StoreInvite("team-1", "dave@example.com", RoleEditor, []string{"work"}, nil, "alice-id", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if token == "" {
		t.Fatal("expected a non-empty token")
	}

	inv, ok, err := reg.ResolveInvite(token)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected the invite to resolve")
	}
	if inv.TeamID != "team-1" || inv.Email != "dave@example.com" || inv.Role != RoleEditor ||
		len(inv.ReadGrants) != 1 || inv.ReadGrants[0] != "work" || inv.InvitedBy != "alice-id" {
		t.Errorf("round-tripped invite = %+v, want team-1/dave@example.com/editor/[work]/alice-id", inv)
	}

	// Unlike TakeOAuthCode, resolving does not consume — the same token
	// resolves again until DeleteInvite is called.
	if _, ok, err := reg.ResolveInvite(token); err != nil {
		t.Fatal(err)
	} else if !ok {
		t.Error("a resolve should not consume the invite")
	}

	if err := reg.DeleteInvite(token); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := reg.ResolveInvite(token); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Error("invite should be gone after DeleteInvite")
	}

	// An expired invite resolves as not-found.
	expired, err := reg.StoreInvite("team-1", "eve@example.com", RoleViewer, nil, nil, "alice-id", -time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := reg.ResolveInvite(expired); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Error("expired invite should not resolve")
	}

	// Unknown token.
	if _, ok, err := reg.ResolveInvite("never-existed"); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Error("unknown token should not resolve")
	}
}

func TestHashKey(t *testing.T) {
	h1 := HashKey("secret")
	h2 := HashKey("secret")
	if h1 != h2 {
		t.Error("same input should produce same hash")
	}
	h3 := HashKey("other")
	if h1 == h3 {
		t.Error("different inputs should not collide")
	}
}
