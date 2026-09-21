package db

import (
	"os"
	"strings"
	"testing"
)

func tempRegistry(t *testing.T) *Registry {
	t.Helper()
	f, err := os.CreateTemp("", "reg-stats-*.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	t.Cleanup(func() { os.Remove(f.Name()) })
	reg, err := OpenRegistry(f.Name())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { reg.Close() })
	return reg
}

func TestValidateIdentityFields(t *testing.T) {
	reg := tempRegistry(t)

	bad := []struct {
		name, path string
	}{
		{"", "a.memory"},                       // empty name
		{"  ", "a.memory"},                     // whitespace-only name
		{"alice", ""},                          // empty path
		{"alice", "  "},                        // whitespace-only path
		{"al\nice", "a.memory"},                // control char in name
		{"alice", "a\x00.memory"},              // NUL in path
		{strings.Repeat("x", 201), "a.memory"}, // name too long
		{"alice", strings.Repeat("y", 4097)},   // path too long
	}
	for _, c := range bad {
		if _, err := reg.Create(c.name, "k-"+c.name+c.path, c.path, true); err == nil {
			t.Fatalf("Create(%q,%q): expected validation error, got nil", c.name, c.path)
		}
	}

	// A well-formed identity is accepted.
	if _, err := reg.Create("alice", "good-key", "alice.memory", true); err != nil {
		t.Fatalf("Create valid: %v", err)
	}

	// MintGrant is validated too (empty name rejected).
	if _, _, err := reg.MintGrant("client", "", "alice.memory", true); err == nil {
		t.Fatal("MintGrant with empty name: expected error, got nil")
	}
}

func TestRegistryStats(t *testing.T) {
	reg := tempRegistry(t)

	// Two identities sharing one memory path, plus one on a second path.
	if _, err := reg.Create("alice", "key-a", "shared.memory", true); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Create("bob", "key-b", "shared.memory", false); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Create("carol", "key-c", "carol.memory", true); err != nil {
		t.Fatal(err)
	}

	// One grant, then a second that we revoke.
	if _, _, err := reg.MintGrant("client-1", "oauth:client-1", "shared.memory", true); err != nil {
		t.Fatal(err)
	}
	g2, _, err := reg.MintGrant("client-2", "oauth:client-2", "shared.memory", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.RevokeGrant(g2.ID); err != nil {
		t.Fatal(err)
	}

	st, err := reg.Stats()
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	// 3 human identities + 1 surviving minted grant identity (g2's was deleted on revoke).
	if st.Identities != 4 {
		t.Errorf("identities: want 4, got %d", st.Identities)
	}
	if st.Grants != 2 {
		t.Errorf("grants: want 2, got %d", st.Grants)
	}
	if st.ActiveGrants != 1 {
		t.Errorf("active grants: want 1, got %d", st.ActiveGrants)
	}
	// Distinct memory paths: shared.memory, carol.memory.
	if st.MemoryPaths != 2 {
		t.Errorf("memory paths: want 2, got %d", st.MemoryPaths)
	}
}
