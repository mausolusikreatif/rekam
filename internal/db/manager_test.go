package db

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func tenantPath(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(t.TempDir(), name+".memory")
}

func TestManagerLazyOpenAndReuse(t *testing.T) {
	m := NewManager(ManagerOptions{IdleTTL: time.Hour})
	defer m.Shutdown()

	if got := m.OpenCount(); got != 0 {
		t.Fatalf("nothing acquired yet, open=%d", got)
	}
	inst := m.Instance(tenantPath(t, "a"))
	if got := m.OpenCount(); got != 0 {
		t.Fatalf("Instance() must not open the DB, open=%d", got)
	}

	mdb1, rel1, err := inst.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	if m.OpenCount() != 1 {
		t.Fatalf("acquire should open one handle, open=%d", m.OpenCount())
	}
	mdb2, rel2, err := inst.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	if mdb1 != mdb2 {
		t.Error("concurrent acquires of one tenant must share the handle")
	}
	if m.OpenCount() != 1 {
		t.Fatalf("two leases, still one handle, open=%d", m.OpenCount())
	}
	rel1()
	rel2()
}

func TestManagerReapsIdleHandle(t *testing.T) {
	m := NewManager(ManagerOptions{IdleTTL: 20 * time.Millisecond, SweepInterval: 5 * time.Millisecond})
	defer m.Shutdown()

	_, rel, err := m.Instance(tenantPath(t, "a")).Acquire()
	if err != nil {
		t.Fatal(err)
	}
	rel()

	if !waitFor(func() bool { return m.OpenCount() == 0 }, time.Second) {
		t.Fatalf("idle handle was not reaped, open=%d", m.OpenCount())
	}
	if m.Closes() == 0 {
		t.Error("expected a close to be recorded")
	}
}

func TestManagerRefcountBlocksReap(t *testing.T) {
	m := NewManager(ManagerOptions{IdleTTL: 10 * time.Millisecond, SweepInterval: 5 * time.Millisecond})
	defer m.Shutdown()

	_, rel, err := m.Instance(tenantPath(t, "a")).Acquire()
	if err != nil {
		t.Fatal(err)
	}

	// Hold the lease well past the idle TTL; the reaper must not close it.
	time.Sleep(60 * time.Millisecond)
	if m.OpenCount() != 1 {
		t.Fatalf("held lease must keep the handle open, open=%d", m.OpenCount())
	}
	rel()
	if !waitFor(func() bool { return m.OpenCount() == 0 }, time.Second) {
		t.Fatalf("handle not reaped after release, open=%d", m.OpenCount())
	}
}

func TestManagerEnforcesBudget(t *testing.T) {
	const maxOpen = 4
	// Long TTL so only the budget (not the reaper) drives eviction.
	m := NewManager(ManagerOptions{MaxOpen: maxOpen, IdleTTL: time.Hour, SweepInterval: time.Hour})
	defer m.Shutdown()

	// Touch many tenants serially, releasing each before the next.
	for i := range 50 {
		_, rel, err := m.Instance(tenantPath(t, string(rune('a'+i%26))+"-"+itoa(i))).Acquire()
		if err != nil {
			t.Fatal(err)
		}
		rel()
		if got := m.OpenCount(); got > maxOpen {
			t.Fatalf("open handles %d exceeded budget %d at step %d", got, maxOpen, i)
		}
	}
	if m.Closes() == 0 {
		t.Error("expected budget eviction to close handles")
	}
}

func TestManagerShutdownDrains(t *testing.T) {
	m := NewManager(ManagerOptions{IdleTTL: time.Hour})

	var rels []func()
	for i := range 5 {
		_, rel, err := m.Instance(tenantPath(t, "t"+itoa(i))).Acquire()
		if err != nil {
			t.Fatal(err)
		}
		rels = append(rels, rel)
	}
	for _, r := range rels {
		r()
	}
	if m.OpenCount() != 5 {
		t.Fatalf("expected 5 open before shutdown, got %d", m.OpenCount())
	}
	m.Shutdown()
	if m.OpenCount() != 0 {
		t.Fatalf("shutdown must close all handles, open=%d", m.OpenCount())
	}
	m.Shutdown() // idempotent: must not panic
}

func TestManagerConcurrentAcquireRelease(t *testing.T) {
	m := NewManager(ManagerOptions{MaxOpen: 8, IdleTTL: 5 * time.Millisecond, SweepInterval: 2 * time.Millisecond})
	defer m.Shutdown()

	paths := make([]string, 20)
	for i := range paths {
		paths[i] = tenantPath(t, "p"+itoa(i))
	}

	var wg sync.WaitGroup
	for w := range 16 {
		wg.Go(func() {
			for i := range 200 {
				inst := m.Instance(paths[(w+i)%len(paths)])
				mdb, rel, err := inst.Acquire()
				if err != nil {
					t.Errorf("acquire: %v", err)
					return
				}
				if mdb == nil {
					t.Error("nil handle from successful acquire")
				}
				rel()
			}
		})
	}
	wg.Wait()
}

// TestManagerPathResolverRewritesInstance confirms PathResolver (the LiteFS
// wiring seam) resolves the logical path passed to Instance() into the actual
// on-disk path opened, while Instance identity/eviction stay keyed by the
// unresolved logical path — see LiteFSPathResolver's doc comment.
func TestManagerPathResolverRewritesInstance(t *testing.T) {
	root := t.TempDir()
	resolved := filepath.Join(root, "resolved.memory")

	m := NewManager(ManagerOptions{
		IdleTTL:      time.Hour,
		PathResolver: func(string) string { return resolved },
	})
	defer m.Shutdown()

	logical := "some/logical/tenant/path.memory" // never created directly
	mdb, rel, err := m.Instance(logical).Acquire()
	if err != nil {
		t.Fatal(err)
	}
	defer rel()
	if mdb == nil {
		t.Fatal("nil handle from successful acquire")
	}
	if _, err := os.Stat(resolved); err != nil {
		t.Errorf("expected the resolved path to be created on disk: %v", err)
	}
	if _, err := os.Stat(logical); err == nil {
		t.Error("the unresolved logical path must not exist on disk")
	}
	// Re-acquiring the same logical path must reuse the same Instance (keyed by
	// the logical path, not the resolved one).
	first := m.Instance(logical)
	second := m.Instance(logical)
	if first != second {
		t.Error("Instance(logical) should be stable across calls")
	}
}

// TestManagerPathResolverRewritesInstanceWith is the InstanceWith counterpart —
// the file-store call site (engine.filesStore) passes its own opener, and the
// resolver must wrap that opener too, not just the OpenMemory default.
func TestManagerPathResolverRewritesInstanceWith(t *testing.T) {
	root := t.TempDir()
	resolved := filepath.Join(root, "resolved.files")

	var openedWith string
	m := NewManager(ManagerOptions{
		IdleTTL:      time.Hour,
		PathResolver: func(logical string) string { return resolved },
	})
	defer m.Shutdown()

	fakeOpen := func(p string) (*MemoryDB, error) {
		openedWith = p
		return OpenFileStore(p)
	}
	_, rel, err := m.InstanceWith("logical.files", fakeOpen).Acquire()
	if err != nil {
		t.Fatal(err)
	}
	defer rel()
	if openedWith != resolved {
		t.Errorf("opener called with %q, want resolved path %q", openedWith, resolved)
	}
}

// TestManagerAcquireDuringSlowOpen guards Instance.Acquire against a slow (e.g.
// LiteFS cold-hydration) open: a second concurrent Acquire on the same Instance
// must wait for the in-flight open rather than racing a second one, and both
// callers must end up with the same handle. This is a regression test for
// behavior manager.go already relies on (i.mu held across the open), not new
// logic — see LiteFSPathResolver / docs/plan-distributed-tenants.md.
func TestManagerAcquireDuringSlowOpen(t *testing.T) {
	m := NewManager(ManagerOptions{IdleTTL: time.Hour})
	defer m.Shutdown()

	path := tenantPath(t, "slow")
	started := make(chan struct{})
	release := make(chan struct{})
	var opens int
	var mu sync.Mutex
	slowOpen := func(p string) (*MemoryDB, error) {
		mu.Lock()
		opens++
		mu.Unlock()
		close(started)
		<-release
		return OpenMemory(p)
	}

	inst := m.InstanceWith(path, slowOpen)

	var wg sync.WaitGroup
	results := make([]*MemoryDB, 2)
	errs := make([]error, 2)
	for i := range 2 {
		wg.Go(func() {
			mdb, rel, err := inst.Acquire()
			results[i], errs[i] = mdb, err
			if err == nil {
				defer rel()
			}
		})
	}
	<-started
	time.Sleep(20 * time.Millisecond) // give the second Acquire a chance to (wrongly) race in
	close(release)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("acquire %d: %v", i, err)
		}
	}
	if results[0] != results[1] {
		t.Error("concurrent acquires during a slow open must converge on one handle")
	}
	mu.Lock()
	defer mu.Unlock()
	if opens != 1 {
		t.Errorf("slow open should run exactly once, ran %d times", opens)
	}
}

func waitFor(cond func() bool, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return cond()
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
