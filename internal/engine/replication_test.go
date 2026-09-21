// Scenarios: spec/replication.md (REPL-05..09, REPL-11)

package engine

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Ucok23/rekam/internal/db"
	"github.com/benbjohnson/litestream"
	"github.com/benbjohnson/litestream/file"
)

// alwaysLoserLeaser simulates every lease already being held by another node —
// exercises the not_primary path without needing a second real Replication to
// contend against (see internal/db/replicate_test.go for real contention
// between two Replications).
type alwaysLoserLeaser struct{ owner string }

func (l *alwaysLoserLeaser) Type() string { return "always-loser" }
func (l *alwaysLoserLeaser) AcquireLease(ctx context.Context) (*litestream.Lease, error) {
	return nil, &litestream.LeaseExistsError{Owner: l.owner, ExpiresAt: time.Now().Add(time.Hour)}
}
func (l *alwaysLoserLeaser) RenewLease(ctx context.Context, lease *litestream.Lease) (*litestream.Lease, error) {
	return nil, litestream.ErrLeaseNotHeld
}
func (l *alwaysLoserLeaser) ReleaseLease(ctx context.Context, lease *litestream.Lease) error {
	return nil
}

// alwaysWinnerLeaser simulates no contention at all — always acquires cleanly.
type alwaysWinnerLeaser struct{ gen int64 }

func (l *alwaysWinnerLeaser) Type() string { return "always-winner" }
func (l *alwaysWinnerLeaser) AcquireLease(ctx context.Context) (*litestream.Lease, error) {
	l.gen++
	return &litestream.Lease{Generation: l.gen, ExpiresAt: time.Now().Add(time.Hour), Owner: "me"}, nil
}
func (l *alwaysWinnerLeaser) RenewLease(ctx context.Context, lease *litestream.Lease) (*litestream.Lease, error) {
	return &litestream.Lease{Generation: lease.Generation, ExpiresAt: time.Now().Add(time.Hour), Owner: "me"}, nil
}
func (l *alwaysWinnerLeaser) ReleaseLease(ctx context.Context, lease *litestream.Lease) error {
	return nil
}

// replicatedTestEngine builds an Engine wired to a real Replication (file
// backend — no network/cloud creds) using leaserFactory for write-lease
// decisions, plus a registry identity ready to use. The registry file itself
// is activated through the same Replication, matching cmd/rekam/main.go's
// wiring, so requireRegistryPrimary's checks are exercised for real too.
func replicatedTestEngine(t *testing.T, leaserFactory db.LeaserFactory) (eng *Engine, apiKey string) {
	t.Helper()
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.sqlite")
	remoteRoot := filepath.Join(dir, "remote")

	reg, err := db.OpenRegistry(regPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reg.Close() })

	// A raw identity created directly against the registry, bypassing engine's
	// requireRegistryPrimary — this test needs a usable key regardless of which
	// leaser scenario it's exercising.
	apiKey = "test-key"
	memPath := filepath.Join(dir, "alice.rekam")
	if _, err := reg.Create("alice", apiKey, memPath, true); err != nil {
		t.Fatal(err)
	}

	repl, err := db.NewReplication(db.ReplicationOptions{
		ReplicaClient: func(logical string) (litestream.ReplicaClient, error) {
			return file.NewReplicaClient(filepath.Join(remoteRoot, logical)), nil
		},
		Leaser:        leaserFactory,
		LeaseTTL:      time.Hour,
		IdleTTL:       time.Hour,
		SweepInterval: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repl.Close() })

	if err := repl.EnsureActive(context.Background(), regPath, regPath); err != nil {
		t.Fatal(err)
	}

	managerOpts := db.ManagerOptions{DefaultOpen: db.ReplicatedOpen(repl)}
	eng = NewWithReplication(reg, 6000, managerOpts, repl, regPath)
	t.Cleanup(eng.Shutdown)
	return eng, apiKey
}

func TestReplicationBlocksTenantWriteWhenNotPrimary(t *testing.T) {
	eng, key := replicatedTestEngine(t, func(logical string) (litestream.Leaser, error) {
		return &alwaysLoserLeaser{owner: "http://other-node:5000"}, nil
	})

	// REPL-06
	_, err := eng.WriteMemory(key, WriteInput{Title: "x", Content: "y", Taxonomy: "t"})
	if err == nil {
		t.Fatal("expected a not_primary error, got nil")
	}
	var ae *ActionableError
	if !errors.As(err, &ae) || ae.Code != "not_primary" {
		t.Fatalf("expected ActionableError{Code: not_primary}, got %#v", err)
	}
	ctx, ok := ae.Context.(map[string]string)
	if !ok || ctx["owner"] != "http://other-node:5000" {
		t.Errorf("expected owner hint in Context, got %#v", ae.Context)
	}
}

func TestReplicationAllowsTenantWriteWhenPrimary(t *testing.T) {
	eng, key := replicatedTestEngine(t, func(logical string) (litestream.Leaser, error) {
		return &alwaysWinnerLeaser{}, nil
	})

	// REPL-07
	mem, err := eng.WriteMemory(key, WriteInput{Title: "x", Content: "y", Taxonomy: "t"})
	if err != nil {
		t.Fatalf("expected write to succeed when primary, got: %v", err)
	}
	if mem == nil || mem.ID == "" {
		t.Error("expected a written memory with an id")
	}
}

func TestReplicationBlocksReadNever(t *testing.T) {
	eng, key := replicatedTestEngine(t, func(logical string) (litestream.Leaser, error) {
		return &alwaysLoserLeaser{owner: "http://other-node:5000"}, nil
	})

	// REPL-08: reads must never be lease-gated — every node serves its local replica.
	if _, err := eng.ListMemories(key, "", 10, 0); err != nil {
		t.Errorf("read should never be blocked by the write lease, got: %v", err)
	}
}

func TestReplicationBlocksRegistryMutationWhenNotPrimary(t *testing.T) {
	eng, _ := replicatedTestEngine(t, func(logical string) (litestream.Leaser, error) {
		return &alwaysLoserLeaser{owner: "http://other-node:5000"}, nil
	})

	dir := t.TempDir()
	// REPL-09
	_, err := eng.CreateUser("bob", "bob@example.com", "hunter22", filepath.Join(dir, "bob.rekam"), true, false)
	if err == nil {
		t.Fatal("expected a not_primary error creating a user, got nil")
	}
	var ae *ActionableError
	if !errors.As(err, &ae) || ae.Code != "not_primary" {
		t.Fatalf("expected ActionableError{Code: not_primary}, got %#v", err)
	}
}

func TestReplicationAllowsRegistryMutationWhenPrimary(t *testing.T) {
	eng, _ := replicatedTestEngine(t, func(logical string) (litestream.Leaser, error) {
		return &alwaysWinnerLeaser{}, nil
	})

	dir := t.TempDir()
	// REPL-09 (allowed-when-primary counterpart)
	id, err := eng.CreateUser("bob", "bob@example.com", "hunter22", filepath.Join(dir, "bob.rekam"), true, false)
	if err != nil {
		t.Fatalf("expected user creation to succeed when primary, got: %v", err)
	}
	if id == nil || id.ID == "" {
		t.Error("expected a created identity with an id")
	}
}

func TestReplicationNilIsANoOp(t *testing.T) {
	// No replication configured at all (nil) — everything behaves exactly as
	// before this feature existed, the default for every non-replicated Engine.
	dir := t.TempDir()
	reg, err := db.OpenRegistry(filepath.Join(dir, "registry.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	key := "k"
	if _, err := reg.Create("alice", key, filepath.Join(dir, "alice.rekam"), true); err != nil {
		t.Fatal(err)
	}
	eng := New(reg, 6000)
	defer eng.Shutdown()

	// REPL-11
	if _, err := eng.WriteMemory(key, WriteInput{Title: "x", Content: "y", Taxonomy: "t"}); err != nil {
		t.Errorf("write should succeed with no replication configured, got: %v", err)
	}
}
