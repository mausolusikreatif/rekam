// Scenarios: spec/replication.md (REPL-01..06, REPL-10, REPL-12)

package db

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/benbjohnson/litestream"
	"github.com/benbjohnson/litestream/file"
)

// fileClientFactory returns a ReplicaClientFactory backed by the local
// filesystem (litestream's own `file` package) — real replication mechanics,
// no network or cloud credentials, exercising restore + ongoing sync for real.
func fileClientFactory(remoteRoot string) ReplicaClientFactory {
	return func(logical string) (litestream.ReplicaClient, error) {
		return file.NewReplicaClient(filepath.Join(remoteRoot, logical)), nil
	}
}

func quietReplicationOpts(remoteRoot string) ReplicationOptions {
	return ReplicationOptions{
		ReplicaClient: fileClientFactory(remoteRoot),
		IdleTTL:       time.Hour,
		SweepInterval: time.Hour,
	}
}

func TestReplicationRestoresFromReplicaOnAnotherNode(t *testing.T) {
	dir := t.TempDir()
	remoteRoot := filepath.Join(dir, "remote")
	nodeALocal := filepath.Join(dir, "node-a", "acme.rekam")
	nodeBLocal := filepath.Join(dir, "node-b", "acme.rekam")
	ctx := context.Background()

	// Node A: write a memory, force a sync so it lands in the shared replica.
	rA, err := NewReplication(quietReplicationOpts(remoteRoot))
	if err != nil {
		t.Fatal(err)
	}
	defer rA.Close()

	if err := rA.EnsureActive(ctx, "acme", nodeALocal); err != nil {
		t.Fatal(err)
	}
	mdb, err := OpenMemory(nodeALocal)
	if err != nil {
		t.Fatal(err)
	}
	// Simulates what ReplicatedOpen sets: with replication active, Close must
	// not force its own checkpoint (see MemoryDB.Close's doc comment) — a
	// TRUNCATE would truncate the WAL before Sync below gets a chance to ship
	// those frames to the replica.
	mdb.externallyCheckpointed = true
	mem := &Memory{Title: "Runbook", Content: "restart the thing", Taxonomy: "ops"}
	if err := mdb.Insert(mem, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := mdb.Close(); err != nil {
		t.Fatal(err)
	}
	if err := rA.Sync(ctx, "acme"); err != nil {
		t.Fatal(err)
	}

	// Node B: never seen this tenant before. EnsureActive must restore it from
	// the shared replica before the caller opens it.
	rB, err := NewReplication(quietReplicationOpts(remoteRoot))
	if err != nil {
		t.Fatal(err)
	}
	defer rB.Close()

	if err := rB.EnsureActive(ctx, "acme", nodeBLocal); err != nil {
		t.Fatal(err)
	}
	mdbB, err := OpenMemory(nodeBLocal)
	if err != nil {
		t.Fatal(err)
	}
	defer mdbB.Close()

	// REPL-01
	got, err := mdbB.Get(mem.ID)
	if err != nil {
		t.Fatalf("record did not survive restore onto node B: %v", err)
	}
	if got.Title != mem.Title || got.Content != mem.Content {
		t.Errorf("restored record mismatch: got %+v, want title=%q content=%q", got, mem.Title, mem.Content)
	}
}

// TestReplicationRequirePrimaryNilLeaserIsAlwaysPrimary covers REPL-05: a
// Replication with object-storage replication configured but no
// LeaserFactory (quietReplicationOpts sets none) must never gate writes —
// RequirePrimary is unconditionally nil, the pre-replication default.
func TestReplicationRequirePrimaryNilLeaserIsAlwaysPrimary(t *testing.T) {
	dir := t.TempDir()
	remoteRoot := filepath.Join(dir, "remote")
	local := filepath.Join(dir, "node-a", "acme.rekam")
	ctx := context.Background()

	r, err := NewReplication(quietReplicationOpts(remoteRoot))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	if err := r.EnsureActive(ctx, "acme", local); err != nil {
		t.Fatal(err)
	}
	// REPL-05
	if err := r.RequirePrimary("acme"); err != nil {
		t.Errorf("RequirePrimary with no LeaserFactory configured should always be nil, got: %v", err)
	}
}

func TestReplicationEnsureActiveIsIdempotentAndBumpsLastUsed(t *testing.T) {
	dir := t.TempDir()
	remoteRoot := filepath.Join(dir, "remote")
	local := filepath.Join(dir, "node-a", "acme.rekam")
	ctx := context.Background()

	r, err := NewReplication(quietReplicationOpts(remoteRoot))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	// REPL-02: "acme" has never been replicated by any node before this — the
	// first EnsureActive below hits restoreIfMissing's no-prior-backup path,
	// which must be a no-op, not an error.
	if err := r.EnsureActive(ctx, "acme", local); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	ts := r.tenants["acme"]
	r.mu.Unlock()
	if ts == nil {
		t.Fatal("tenant not registered after EnsureActive")
	}
	ts.mu.Lock()
	first := ts.lastUsed
	ts.mu.Unlock()

	time.Sleep(5 * time.Millisecond)
	if err := r.EnsureActive(ctx, "acme", local); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	if len(r.tenants) != 1 {
		t.Errorf("expected exactly one tenant entry, got %d", len(r.tenants))
	}
	ts2 := r.tenants["acme"]
	r.mu.Unlock()
	if ts2 != ts {
		t.Error("second EnsureActive should reuse the same tenantState, not create a new one")
	}
	ts2.mu.Lock()
	second := ts2.lastUsed
	ts2.mu.Unlock()
	// REPL-03
	if !second.After(first) {
		t.Error("second EnsureActive should have bumped lastUsed")
	}
}

func TestReplicationIdleSweepDeactivatesTenant(t *testing.T) {
	dir := t.TempDir()
	remoteRoot := filepath.Join(dir, "remote")
	local := filepath.Join(dir, "node-a", "acme.rekam")
	ctx := context.Background()

	r, err := NewReplication(ReplicationOptions{
		ReplicaClient: fileClientFactory(remoteRoot),
		IdleTTL:       20 * time.Millisecond,
		SweepInterval: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	if err := r.EnsureActive(ctx, "acme", local); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		n := len(r.tenants)
		r.mu.Unlock()
		if n == 0 {
			return // REPL-04: deactivated as expected
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("tenant was not deactivated after its idle TTL elapsed")
}

// fakeLeaseState is the state one lease object contends over, shared by every
// fakeLeaser "node" pointing at it in a test.
type fakeLeaseState struct {
	mu     sync.Mutex
	holder string
	expiry time.Time
	gen    int64
}

// fakeLeaser is a deterministic, in-memory litestream.Leaser used to test
// lease contention without real S3 — the interface is exactly what s3.Leaser
// implements, so this exercises Replication's lease-loop logic directly.
type fakeLeaser struct {
	state *fakeLeaseState
	owner string
}

func (l *fakeLeaser) Type() string { return "fake" }

func (l *fakeLeaser) AcquireLease(ctx context.Context) (*litestream.Lease, error) {
	l.state.mu.Lock()
	defer l.state.mu.Unlock()
	if l.state.holder != "" && time.Now().Before(l.state.expiry) && l.state.holder != l.owner {
		return nil, &litestream.LeaseExistsError{Owner: l.state.holder, ExpiresAt: l.state.expiry}
	}
	l.state.gen++
	l.state.holder = l.owner
	l.state.expiry = time.Now().Add(time.Hour)
	return &litestream.Lease{Generation: l.state.gen, ExpiresAt: l.state.expiry, Owner: l.owner}, nil
}

func (l *fakeLeaser) RenewLease(ctx context.Context, lease *litestream.Lease) (*litestream.Lease, error) {
	l.state.mu.Lock()
	defer l.state.mu.Unlock()
	if l.state.holder != l.owner {
		return nil, litestream.ErrLeaseNotHeld
	}
	l.state.expiry = time.Now().Add(time.Hour)
	return &litestream.Lease{Generation: l.state.gen, ExpiresAt: l.state.expiry, Owner: l.owner}, nil
}

func (l *fakeLeaser) ReleaseLease(ctx context.Context, lease *litestream.Lease) error {
	l.state.mu.Lock()
	defer l.state.mu.Unlock()
	if l.state.holder == l.owner {
		l.state.holder = ""
	}
	return nil
}

func TestReplicationRequirePrimary(t *testing.T) {
	dir := t.TempDir()
	remoteRoot := filepath.Join(dir, "remote")
	localA := filepath.Join(dir, "node-a", "acme.rekam")
	localB := filepath.Join(dir, "node-b", "acme.rekam")
	ctx := context.Background()

	shared := &fakeLeaseState{} // one lease both "nodes" below contend over

	newRepl := func(owner string) *Replication {
		r, err := NewReplication(ReplicationOptions{
			ReplicaClient: fileClientFactory(remoteRoot),
			Leaser: func(logical string) (litestream.Leaser, error) {
				return &fakeLeaser{state: shared, owner: owner}, nil
			},
			LeaseTTL:      100 * time.Millisecond,
			IdleTTL:       time.Hour,
			SweepInterval: time.Hour,
		})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}

	rA := newRepl("node-a")
	defer rA.Close()
	rB := newRepl("node-b")
	defer rB.Close()

	if err := rA.EnsureActive(ctx, "acme", localA); err != nil {
		t.Fatal(err)
	}
	if err := rB.EnsureActive(ctx, "acme", localB); err != nil {
		t.Fatal(err)
	}

	// Whichever node's lease loop ran first holds the lease; the other must see
	// ErrNotPrimary with that node's identity as the Owner hint. Don't stop at
	// the first instant exactly one side reports primary=true — the loser's
	// own lease loop may not have run its first attempt() yet, in which case
	// its ownerHint is still its unset zero value ("") rather than a real hint.
	// Wait for the loser to have actually recorded one.
	settled := func(a, b error) (settled bool) {
		if (a == nil) == (b == nil) {
			return false // not exactly one primary yet
		}
		loserErr := a
		if a == nil {
			loserErr = b
		}
		var np *ErrNotPrimary
		return errors.As(loserErr, &np) && np.Owner != ""
	}

	deadline := time.Now().Add(2 * time.Second)
	var aErr, bErr error
	for time.Now().Before(deadline) {
		aErr = rA.RequirePrimary("acme")
		bErr = rB.RequirePrimary("acme")
		if settled(aErr, bErr) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	// REPL-10: exactly one of the two contending nodes is primary at a time.
	if (aErr == nil) == (bErr == nil) {
		t.Fatalf("expected exactly one node to be primary, got aErr=%v bErr=%v", aErr, bErr)
	}

	// REPL-06: the loser's error names the current holder.
	var notPrimary *ErrNotPrimary
	if aErr != nil {
		if !errors.As(aErr, &notPrimary) {
			t.Fatalf("expected *ErrNotPrimary, got %T: %v", aErr, aErr)
		}
		if notPrimary.Owner != "node-b" {
			t.Errorf("owner hint = %q, want node-b", notPrimary.Owner)
		}
	} else {
		if !errors.As(bErr, &notPrimary) {
			t.Fatalf("expected *ErrNotPrimary, got %T: %v", bErr, bErr)
		}
		if notPrimary.Owner != "node-a" {
			t.Errorf("owner hint = %q, want node-a", notPrimary.Owner)
		}
	}
}

func TestReplicatedOpenWiresIntoManager(t *testing.T) {
	dir := t.TempDir()
	remoteRoot := filepath.Join(dir, "remote")
	local := filepath.Join(dir, "node-a", "acme.rekam")

	r, err := NewReplication(quietReplicationOpts(remoteRoot))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	m := NewManager(ManagerOptions{IdleTTL: time.Hour, DefaultOpen: ReplicatedOpen(r)})
	defer m.Shutdown()

	mdb, release, err := m.Instance(local).Acquire()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if mdb == nil {
		t.Fatal("nil handle from successful acquire")
	}

	r.mu.Lock()
	_, active := r.tenants[local]
	r.mu.Unlock()
	// REPL-12
	if !active {
		t.Error("Manager.Instance().Acquire() through ReplicatedOpen should have activated replication for the tenant")
	}
}
