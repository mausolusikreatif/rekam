package db

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/benbjohnson/litestream"
)

// ErrNotPrimary means this node does not currently hold the write lease for a
// tenant — another node does (or no node does yet). Owner, when non-empty, is
// that lease's current holder. Callers should set a LeaserFactory's lease Owner
// to this node's own reachable base URL (see s3.Leaser.Owner), so a losing
// AcquireLease/RenewLease directly yields a usable forwarding target — no
// separate routing table needed. See docs/plan-distributed-tenants.md §5-6.
type ErrNotPrimary struct {
	Tenant string
	Owner  string
}

func (e *ErrNotPrimary) Error() string {
	if e.Owner != "" {
		return fmt.Sprintf("not primary for %q (owned by %s)", e.Tenant, e.Owner)
	}
	return fmt.Sprintf("not primary for %q (no current owner)", e.Tenant)
}

// ReplicaClientFactory builds a fresh litestream.ReplicaClient scoped to one
// tenant's logical path (e.g. an S3 client whose Path is namespaced under that
// tenant). A new client is built per tenant — litestream ReplicaClient values
// are not meant to be shared across databases.
type ReplicaClientFactory func(logical string) (litestream.ReplicaClient, error)

// LeaserFactory builds a fresh litestream.Leaser scoped to one tenant. nil
// disables write-lease gating entirely: every node that has a tenant's file
// open may write to it, same as rekam's behavior before this package existed.
// Only meaningful once more than one node can reach the same tenant's data —
// see docs/plan-distributed-tenants.md.
type LeaserFactory func(logical string) (litestream.Leaser, error)

// ReplicationOptions configures a Replication.
type ReplicationOptions struct {
	ReplicaClient ReplicaClientFactory // required
	Leaser        LeaserFactory        // optional, see LeaserFactory

	LeaseTTL      time.Duration // default 30s
	IdleTTL       time.Duration // default 30m — see Replication's doc comment
	SweepInterval time.Duration // default 1m

	CompactionLevels litestream.CompactionLevels // default: L0 + L1@10s, per litestream's own example
	Logger           *slog.Logger
}

func (o *ReplicationOptions) setDefaults() {
	if o.LeaseTTL <= 0 {
		o.LeaseTTL = 30 * time.Second
	}
	if o.IdleTTL <= 0 {
		o.IdleTTL = 30 * time.Minute
	}
	if o.SweepInterval <= 0 {
		o.SweepInterval = time.Minute
	}
	if o.CompactionLevels == nil {
		o.CompactionLevels = litestream.CompactionLevels{
			{Level: 0},
			{Level: 1, Interval: 10 * time.Second},
		} // element type is *litestream.CompactionLevel; composite elision fills it in
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
}

// Replication manages the *replication* lifecycle for every tenant this node
// has touched: continuous WAL-to-object-storage shipping (via an embedded
// litestream.Store/DB/Replica per tenant) and, when a LeaserFactory is
// configured, a background loop that holds this node's claim to be the
// current write-primary for that tenant.
//
// This is deliberately decoupled from Manager's connection lifecycle.
// Manager evicts idle *sql.DB handles aggressively by design — that's the
// whole point proven at 1000-tenant density by
// internal/engine/load_test.go's TestLoad1000ActiveUsers. Litestream's Store
// is comparatively heavy (its own SQLite connection, monitor goroutines,
// checkpoint executor, compaction) and its own docs warn the app's DB
// connections must close before litestream's does — tying the two lifecycles
// together would mean standing up and tearing down that machinery on every
// Manager reopen, which fights the churn Manager exists for. So a tenant's
// replication starts on first touch and stays warm on its own, much longer
// IdleTTL, independent of how many times Manager itself opens and closes the
// underlying SQLite handle in between. See docs/plan-distributed-tenants.md.
type Replication struct {
	opts  ReplicationOptions
	store *litestream.Store

	mu      sync.Mutex
	tenants map[string]*tenantState

	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
}

type tenantState struct {
	logical string
	ldb     *litestream.DB
	leaser  litestream.Leaser
	cancel  context.CancelFunc // stops this tenant's lease loop

	mu        sync.Mutex
	lease     *litestream.Lease
	isPrimary bool
	ownerHint string
	lastUsed  time.Time
}

// NewReplication creates a Replication and opens its underlying litestream
// Store. Call Close on process shutdown.
func NewReplication(opts ReplicationOptions) (*Replication, error) {
	if opts.ReplicaClient == nil {
		return nil, fmt.Errorf("replication: ReplicaClient factory is required")
	}
	opts.setDefaults()

	store := litestream.NewStore(nil, opts.CompactionLevels)
	store.Logger = opts.Logger
	if err := store.Open(context.Background()); err != nil {
		return nil, fmt.Errorf("replication: open store: %w", err)
	}

	r := &Replication{
		opts:    opts,
		store:   store,
		tenants: make(map[string]*tenantState),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
	go r.sweepLoop()
	return r, nil
}

// EnsureActive makes sure logical's replication is running against localPath:
// restoring from the replica first if localPath doesn't exist locally yet,
// then registering it with the store for ongoing WAL replication, then (if a
// LeaserFactory is configured) starting its lease-acquisition loop. Idempotent
// — a tenant already active just has its idle clock reset.
func (r *Replication) EnsureActive(ctx context.Context, logical, localPath string) error {
	r.mu.Lock()
	if ts, ok := r.tenants[logical]; ok {
		r.mu.Unlock()
		ts.mu.Lock()
		ts.lastUsed = time.Now()
		ts.mu.Unlock()
		return nil
	}
	r.mu.Unlock()

	// A node picking up a tenant it has never served before may not have this
	// tenant's parent directory yet — unlike rekam's own install/signup flows,
	// which mkdir it once on the node that first created the tenant. Any node
	// must be able to cold-start it, so create it defensively here.
	if err := os.MkdirAll(filepath.Dir(localPath), 0700); err != nil {
		return fmt.Errorf("replication: create directory for %s: %w", logical, err)
	}

	if err := r.restoreIfMissing(ctx, logical, localPath); err != nil {
		return fmt.Errorf("replication: restore %s: %w", logical, err)
	}

	client, err := r.opts.ReplicaClient(logical)
	if err != nil {
		return fmt.Errorf("replication: build replica client for %s: %w", logical, err)
	}
	if err := client.Init(ctx); err != nil {
		return fmt.Errorf("replication: init replica client for %s: %w", logical, err)
	}

	ldb := litestream.NewDB(localPath)
	replica := litestream.NewReplicaWithClient(ldb, client)
	ldb.Replica = replica

	if err := r.store.RegisterDB(ldb); err != nil {
		return fmt.Errorf("replication: register %s: %w", logical, err)
	}

	ts := &tenantState{logical: logical, ldb: ldb, lastUsed: time.Now()}

	if r.opts.Leaser != nil {
		leaser, err := r.opts.Leaser(logical)
		if err != nil {
			_ = r.store.UnregisterDB(context.Background(), localPath)
			return fmt.Errorf("replication: build leaser for %s: %w", logical, err)
		}
		ts.leaser = leaser
		// Run the first attempt synchronously: without this, EnsureActive
		// could return before the background loop's first attempt runs at
		// all, leaving isPrimary/ownerHint at their unset zero values — a
		// caller that calls RequirePrimary right after EnsureActive (exactly
		// what resolveTenant and requireRegistryPrimary do) would then see a
		// false "not primary, no owner known" instead of the real outcome.
		r.attemptLease(ctx, ts)
		leaseCtx, cancel := context.WithCancel(context.Background())
		ts.cancel = cancel
		go r.leaseRenewalLoop(leaseCtx, ts)
	}

	r.mu.Lock()
	// Another goroutine may have raced us in (EnsureActive doesn't lock across
	// the I/O above, deliberately, so one slow tenant setup can't stall every
	// other tenant's requests). Keep whichever landed first; tear down ours.
	if existing, ok := r.tenants[logical]; ok {
		r.mu.Unlock()
		if ts.cancel != nil {
			ts.cancel()
		}
		_ = r.store.UnregisterDB(context.Background(), localPath)
		existing.mu.Lock()
		existing.lastUsed = time.Now()
		existing.mu.Unlock()
		return nil
	}
	r.tenants[logical] = ts
	r.mu.Unlock()
	return nil
}

// restoreIfMissing hydrates localPath from the replica if it doesn't already
// exist on disk. A tenant with no backup yet (brand new) is not an error —
// OpenMemory creates a fresh file as it always has.
func (r *Replication) restoreIfMissing(ctx context.Context, logical, localPath string) error {
	if _, err := os.Stat(localPath); err == nil {
		return nil // already present locally
	} else if !os.IsNotExist(err) {
		return err
	}

	client, err := r.opts.ReplicaClient(logical)
	if err != nil {
		return fmt.Errorf("build replica client: %w", err)
	}
	if err := client.Init(ctx); err != nil {
		return fmt.Errorf("init replica client: %w", err)
	}
	restoreReplica := litestream.NewReplicaWithClient(nil, client)
	opt := litestream.NewRestoreOptions()
	opt.OutputPath = localPath
	if err := restoreReplica.Restore(ctx, opt); err != nil {
		if errors.Is(err, litestream.ErrTxNotAvailable) || errors.Is(err, litestream.ErrNoSnapshots) {
			return nil // no backup yet — fine, a fresh tenant
		}
		return err
	}
	return nil
}

// attemptLease tries once to acquire (or renew) ts's write lease, updating
// isPrimary/ownerHint for RequirePrimary to read. Called synchronously once
// from EnsureActive (so a caller checking RequirePrimary right after
// EnsureActive sees a real outcome, not an unset zero value) and then
// repeatedly from leaseRenewalLoop.
func (r *Replication) attemptLease(ctx context.Context, ts *tenantState) {
	ts.mu.Lock()
	lease := ts.lease
	ts.mu.Unlock()

	var newLease *litestream.Lease
	var err error
	if lease != nil {
		newLease, err = ts.leaser.RenewLease(ctx, lease)
	}
	if lease == nil || err != nil {
		newLease, err = ts.leaser.AcquireLease(ctx)
	}

	ts.mu.Lock()
	defer ts.mu.Unlock()
	if err != nil {
		ts.isPrimary = false
		ts.lease = nil
		var existsErr *litestream.LeaseExistsError
		if errors.As(err, &existsErr) {
			ts.ownerHint = existsErr.Owner
		}
		return
	}
	ts.lease = newLease
	ts.isPrimary = true
	ts.ownerHint = ""
}

// leaseRenewalLoop repeatedly calls attemptLease on a timer until cancelled.
// It never gives up — a node that loses the lease keeps retrying, so it
// reclaims write eligibility automatically once the current holder's lease
// expires or is released, with no restart needed.
func (r *Replication) leaseRenewalLoop(ctx context.Context, ts *tenantState) {
	interval := r.opts.LeaseTTL / 2
	if interval <= 0 {
		interval = 15 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.attemptLease(ctx, ts)
		}
	}
}

// RequirePrimary returns nil if this node currently holds logical's write
// lease (or no LeaserFactory is configured at all, preserving today's
// single-writer-per-process behavior), and an *ErrNotPrimary otherwise.
// logical must already be active (see EnsureActive) — an inactive tenant is a
// programming error in the caller, not a lease failure.
func (r *Replication) RequirePrimary(logical string) error {
	if r.opts.Leaser == nil {
		return nil
	}
	r.mu.Lock()
	ts, ok := r.tenants[logical]
	r.mu.Unlock()
	if !ok {
		return fmt.Errorf("replication: %s is not active (EnsureActive must run first)", logical)
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if !ts.isPrimary {
		return &ErrNotPrimary{Tenant: logical, Owner: ts.ownerHint}
	}
	return nil
}

// Sync forces an immediate replication sync for an already-active tenant,
// rather than waiting for the DB's normal MonitorInterval. Mainly useful for
// tests and for a graceful-drain path before taking a node out of rotation.
func (r *Replication) Sync(ctx context.Context, logical string) error {
	r.mu.Lock()
	ts, ok := r.tenants[logical]
	r.mu.Unlock()
	if !ok {
		return fmt.Errorf("replication: %s is not active", logical)
	}
	// DB.Sync only writes local LTX files from the WAL; the actual push to the
	// replica client is the Replica's own job (it normally runs on its own
	// SyncInterval timer), so both must be driven for a caller to be sure the
	// write actually reached the replica.
	if err := ts.ldb.Sync(ctx); err != nil {
		return fmt.Errorf("sync db: %w", err)
	}
	if err := ts.ldb.Replica.Sync(ctx); err != nil {
		return fmt.Errorf("sync replica: %w", err)
	}
	return nil
}

// sweepLoop periodically deactivates tenants idle past IdleTTL.
func (r *Replication) sweepLoop() {
	defer close(r.done)
	t := time.NewTicker(r.opts.SweepInterval)
	defer t.Stop()
	for {
		select {
		case <-r.stop:
			return
		case <-t.C:
			r.sweep()
		}
	}
}

func (r *Replication) sweep() {
	cutoff := time.Now().Add(-r.opts.IdleTTL)
	r.mu.Lock()
	var stale []string
	for logical, ts := range r.tenants {
		ts.mu.Lock()
		idle := ts.lastUsed.Before(cutoff)
		ts.mu.Unlock()
		if idle {
			stale = append(stale, logical)
		}
	}
	r.mu.Unlock()
	for _, logical := range stale {
		r.deactivate(logical)
	}
}

// deactivate stops a tenant's lease loop, releases its lease if held, and
// unregisters it from the store (stopping WAL replication for it). A later
// EnsureActive starts it fresh, restoring from the replica as needed.
func (r *Replication) deactivate(logical string) {
	r.mu.Lock()
	ts, ok := r.tenants[logical]
	if ok {
		delete(r.tenants, logical)
	}
	r.mu.Unlock()
	if !ok {
		return
	}

	if ts.cancel != nil {
		ts.cancel()
	}
	if ts.leaser != nil {
		ts.mu.Lock()
		lease := ts.lease
		ts.mu.Unlock()
		if lease != nil {
			_ = ts.leaser.ReleaseLease(context.Background(), lease)
		}
	}
	_ = r.store.UnregisterDB(context.Background(), ts.ldb.Path())
}

// Close deactivates every tenant and closes the underlying store. Safe to call
// once at process shutdown.
func (r *Replication) Close() error {
	r.stopOnce.Do(func() { close(r.stop) })
	<-r.done

	r.mu.Lock()
	logicals := make([]string, 0, len(r.tenants))
	for logical := range r.tenants {
		logicals = append(logicals, logical)
	}
	r.mu.Unlock()
	for _, logical := range logicals {
		r.deactivate(logical)
	}
	return r.store.Close(context.Background())
}

// ReplicatedOpen returns a Manager opener (see ManagerOptions.DefaultOpen)
// that ensures a tenant's replication is active before opening it — restoring
// it from the replica first if it isn't present on this node yet. Write-lease
// gating (RequirePrimary) is a separate, explicit check callers make before a
// mutating operation, not part of open — every node may always *read* its
// local (possibly slightly stale) replica.
func ReplicatedOpen(r *Replication) func(string) (*MemoryDB, error) {
	return ReplicatedOpenWith(r, OpenMemory)
}

// ReplicatedOpenWith is ReplicatedOpen generalized to any Manager opener — the
// seam that also lets the *.files blob store (opened via db.OpenFileStore at
// InstanceWith call sites, bypassing ManagerOptions.DefaultOpen) get the same
// replication treatment as the main memory file. Each distinct path (a
// tenant's "foo.rekam" and its sibling "foo.files") gets its own independent
// tenantState in the Replication — same mechanism, no special-casing needed.
func ReplicatedOpenWith(r *Replication, open func(string) (*MemoryDB, error)) func(string) (*MemoryDB, error) {
	return func(path string) (*MemoryDB, error) {
		if err := r.EnsureActive(context.Background(), path, path); err != nil {
			return nil, err
		}
		mdb, err := open(path)
		if err != nil {
			return nil, err
		}
		// litestream owns checkpoint timing for this file now; see
		// MemoryDB.Close's doc comment for why Close must not also checkpoint.
		mdb.externallyCheckpointed = true
		return mdb, nil
	}
}
