package db

import (
	"os"
	"testing"
)

func TestGrantLifecycle(t *testing.T) {
	f, err := os.CreateTemp("", "grant-reg-*.sqlite")
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

	// Mint a grant bound to a memory path with write permission.
	grant, rawKey, err := reg.MintGrant("claude-ai", "oauth:claude-ai", "alice.memory", true)
	if err != nil {
		t.Fatalf("mint grant: %v", err)
	}
	if rawKey == "" {
		t.Fatal("expected a non-empty raw key")
	}

	// The minted key resolves to the bound memory/permissions.
	id, err := reg.Resolve(rawKey)
	if err != nil {
		t.Fatalf("resolve minted key: %v", err)
	}
	if id.MemoryPath != "alice.memory" || !id.AllowWrite {
		t.Fatalf("minted identity mismatch: %+v", id)
	}

	// It shows up in the grant listing as active.
	grants, err := reg.ListGrants()
	if err != nil {
		t.Fatalf("list grants: %v", err)
	}
	if len(grants) != 1 || grants[0].RevokedAt != nil {
		t.Fatalf("expected 1 active grant, got %+v", grants)
	}

	// Revoking stops the key from resolving.
	if err := reg.RevokeGrant(grant.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := reg.Resolve(rawKey); err == nil {
		t.Fatal("expected revoked key to stop resolving")
	}

	// Double-revoke is an error; the grant row remains, now marked revoked.
	if err := reg.RevokeGrant(grant.ID); err == nil {
		t.Fatal("expected error revoking an already-revoked grant")
	}
	grants, _ = reg.ListGrants()
	if len(grants) != 1 || grants[0].RevokedAt == nil {
		t.Fatalf("expected 1 revoked grant, got %+v", grants)
	}
}
