package engine

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Ucok23/rekam/internal/db"
)

// TestLoad1000ActiveUsers exercises the per-tenant Instance lifecycle under a
// realistic multi-tenant load: 1000 distinct tenants (each its own SQLite file)
// driven concurrently, but with the fd budget (MaxOpen) set far below the tenant
// count so the Manager is forced to continuously evict idle handles and reopen
// them on demand. It asserts four things:
//
//  1. Correctness/isolation: every write is read back from the right tenant,
//     even though that tenant's handle is repeatedly closed (with WAL checkpoint)
//     and reopened mid-test. Zero operation errors.
//  2. The fd budget holds: concurrently open handles never exceed the in-flight
//     worker count, and stay near MaxOpen rather than growing toward 1000.
//  3. Eviction actually happened: many opens/closes, proving handles churned
//     instead of accumulating.
//  4. Scale-to-zero at the DB layer: once traffic stops, the reaper drains all
//     handles back to zero.
func TestLoad1000ActiveUsers(t *testing.T) {
	const (
		users      = 1000
		workers    = 48
		maxOpen    = 64
		opsPerUser = 2 // sessions per user → 2000 total write ops
	)

	dir := t.TempDir()
	regFile := filepath.Join(dir, "registry.sqlite")
	reg, err := db.OpenRegistry(regFile)
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()

	keys := make([]string, users)
	for i := range users {
		key := fmt.Sprintf("key-%d", i)
		mem := filepath.Join(dir, fmt.Sprintf("u%04d.memory", i))
		if _, err := reg.Create(fmt.Sprintf("user-%d", i), key, mem, true); err != nil {
			t.Fatalf("create identity %d: %v", i, err)
		}
		keys[i] = key
	}

	// Tight budget + fast reaper so eviction and reopen are continuously exercised.
	eng := NewWithManager(reg, 6000, db.ManagerOptions{
		MaxOpen:       maxOpen,
		IdleTTL:       100 * time.Millisecond,
		SweepInterval: 20 * time.Millisecond,
	})
	defer eng.Shutdown()

	// Build the job list: each user appears opsPerUser times, then shuffled so
	// hot tenants interleave and the working set churns past the budget.
	jobs := make([]int, 0, users*opsPerUser)
	for u := range users {
		for range opsPerUser {
			jobs = append(jobs, u)
		}
	}
	rand.New(rand.NewSource(1)).Shuffle(len(jobs), func(a, b int) {
		jobs[a], jobs[b] = jobs[b], jobs[a]
	})

	var (
		seq         [users]atomic.Int64 // writes issued per user (== expected count)
		opErrors    atomic.Int64
		totalOps    atomic.Int64
		maxOpenSeen atomic.Int64
	)

	// Sampler: continuously record the peak number of open handles.
	stopSampler := make(chan struct{})
	var samplerDone sync.WaitGroup
	samplerDone.Go(func() {
		tick := time.NewTicker(time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stopSampler:
				return
			case <-tick.C:
				if n := int64(eng.Manager().OpenCount()); n > maxOpenSeen.Load() {
					maxOpenSeen.Store(n)
				}
			}
		}
	})

	jobCh := make(chan int, workers*2)
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for u := range jobCh {
				key := keys[u]
				n := seq[u].Add(1)
				_, err := eng.WriteMemory(key, WriteInput{
					Title:    fmt.Sprintf("u%d-mem-%d", u, n),
					Content:  fmt.Sprintf("body for user %d entry %d", u, n),
					Taxonomy: "load.test",
				})
				if err != nil {
					opErrors.Add(1)
					continue
				}
				// Mix in a read so handles stay warm and the read path is covered.
				if _, err := eng.ListMemories(key, "", 100, 0); err != nil {
					opErrors.Add(1)
				}
				totalOps.Add(1)
			}
		})
	}

	start := time.Now()
	for _, u := range jobs {
		jobCh <- u
	}
	close(jobCh)
	wg.Wait()
	elapsed := time.Since(start)

	close(stopSampler)
	samplerDone.Wait()

	// 1. No operation errors.
	if e := opErrors.Load(); e != 0 {
		t.Fatalf("had %d operation errors under load", e)
	}

	// 2. fd budget held: peak open handles stay near MaxOpen, well under the
	//    tenant count. enforceBudget tolerates a bounded transient overshoot —
	//    each in-flight acquirer may pass the budget check before any onOpen — so
	//    the worst case is MaxOpen + workers, never approaching `users`.
	peak := maxOpenSeen.Load()
	if ceiling := int64(maxOpen + workers); peak > ceiling {
		t.Errorf("peak open handles %d exceeded bound MaxOpen+workers=%d", peak, ceiling)
	}
	if peak >= users {
		t.Errorf("open handles reached %d, budget did not bound growth (users=%d)", peak, users)
	}

	// 3. Eviction churned handles: with 1000 tenants and a budget of 32, far more
	//    opens than the budget must have occurred, and closes must have happened.
	opens, closes := eng.Manager().Opens(), eng.Manager().Closes()
	if opens <= int64(maxOpen) {
		t.Errorf("expected many opens from eviction churn, got %d", opens)
	}
	if closes == 0 {
		t.Errorf("expected handle evictions under budget, got 0 closes")
	}

	// 4. Correctness + isolation: every tenant holds exactly its own writes, which
	//    survived repeated close/reopen (WAL checkpoint on close).
	for u := range users {
		want := seq[u].Load()
		res, err := eng.ListMemories(keys[u], "", 1000, 0)
		if err != nil {
			t.Fatalf("verify user %d: %v", u, err)
		}
		if int64(res.Total) != want {
			t.Fatalf("user %d: have %d memories, want %d (data lost across reopen?)", u, res.Total, want)
		}
	}

	// 5. Scale-to-zero at the DB layer: idle past the TTL, the reaper releases all
	//    handles and the -wal/-shm sidecars.
	deadline := time.Now().Add(2 * time.Second)
	for eng.Manager().OpenCount() > 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if n := eng.Manager().OpenCount(); n != 0 {
		t.Fatalf("expected all handles reaped when idle, %d still open", n)
	}
	assertNoWALSidecars(t, dir)

	t.Logf("LOAD RESULT: %d tenants, %d workers, budget=%d | %d write-ops in %s (%.0f ops/s) | peak open=%d | opens=%d closes=%d | all data verified, drained to 0",
		users, workers, maxOpen, totalOps.Load(), elapsed.Round(time.Millisecond),
		float64(totalOps.Load())/elapsed.Seconds(), peak, opens, closes)
}

// assertNoWALSidecars verifies the WAL checkpoint-on-close left no -wal/-shm
// files behind for the now-released tenant DBs. Only tenant *.memory sidecars
// are checked; the registry stays open for the whole test and keeps its own.
func assertNoWALSidecars(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		for _, suffix := range []string{"-wal", "-shm"} {
			base, found := strings.CutSuffix(name, suffix)
			if found && filepath.Ext(base) == ".memory" {
				t.Errorf("leftover WAL sidecar after tenant drain: %s", name)
			}
		}
	}
}
