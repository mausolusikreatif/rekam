package db

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Instance owns the lifecycle of one tenant's *MemoryDB. The heavy SQLite
// handle is opened lazily on first Acquire and released by the Manager's reaper
// once the instance has been idle (no in-flight leases) past the idle TTL. The
// lightweight Instance struct itself stays resident in the Manager map.
type Instance struct {
	path string
	mgr  *Manager
	// open produces the SQLite handle for this path. Defaults to OpenMemory
	// (full memory schema); the file-blob store passes OpenFileStore instead so
	// its bytes live in a separate *.files DB on its own connection.
	open func(string) (*MemoryDB, error)

	mu sync.Mutex // guards the db open/close transition
	db *MemoryDB

	refs     atomic.Int32 // in-flight leases; a handle with refs>0 is never closed
	lastUsed atomic.Int64 // unixnano of the most recent acquire/release
}

// Acquire lazily opens the tenant DB and returns it together with a release
// func. The handle is pinned open (refs>0) until release is called, so the
// reaper can never close a DB out from under an in-flight request. release is
// always non-nil, even on error, so callers may `defer release()` immediately.
func (i *Instance) Acquire() (*MemoryDB, func(), error) {
	i.mu.Lock()
	if i.db == nil {
		// Enforce the fd budget before opening, without holding i.mu (eviction
		// closes *other* instances, which take their own locks).
		i.mu.Unlock()
		i.mgr.enforceBudget()
		i.mu.Lock()
		if i.db == nil { // double-check: another goroutine may have opened it
			mdb, err := i.open(i.path)
			if err != nil {
				i.mu.Unlock()
				return nil, func() {}, err
			}
			i.db = mdb
			i.mgr.onOpen(i)
		}
	}
	mdb := i.db
	i.refs.Add(1)
	i.lastUsed.Store(time.Now().UnixNano())
	i.mu.Unlock()
	return mdb, i.release, nil
}

func (i *Instance) release() {
	i.lastUsed.Store(time.Now().UnixNano())
	i.refs.Add(-1)
}

// closeIfIdle closes the handle when it is open, unreferenced, and (when ttl>0)
// has been idle at least ttl. Returns true if it closed the handle. Holding
// i.mu across the refs check closes the TOCTOU with Acquire, which also bumps
// refs under i.mu.
func (i *Instance) closeIfIdle(ttl time.Duration) bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.db == nil || i.refs.Load() != 0 {
		return false
	}
	if ttl > 0 && time.Since(time.Unix(0, i.lastUsed.Load())) < ttl {
		return false
	}
	i.db.Close()
	i.db = nil
	i.mgr.onClose(i)
	return true
}

// ManagerOptions configures a Manager. Zero values fall back to defaults.
type ManagerOptions struct {
	IdleTTL       time.Duration // close a handle after this long idle (default 2m)
	SweepInterval time.Duration // how often the reaper runs (default 30s)
	MaxOpen       int           // cap on concurrently open handles, 0 = unlimited

	// PathResolver, if set, rewrites the logical path passed to Instance/
	// InstanceWith into the actual path opened on disk. Instances are still
	// keyed and evicted by the *logical* path, so callers and the registry's
	// memory_path column never need to know where the file physically resolves
	// to on the current node. nil = open the path as given.
	PathResolver func(string) string

	// DefaultOpen, if set, replaces OpenMemory as the opener Instance(path)
	// uses (InstanceWith's caller-supplied opener is unaffected). This is the
	// seam distributed replication hooks into — see db.ReplicatedOpen and
	// docs/plan-distributed-tenants.md. nil = OpenMemory, today's behavior.
	DefaultOpen func(string) (*MemoryDB, error)
}

// Manager supervises per-tenant Instances: lazy open, idle release, and an
// optional cap on concurrently open handles (the fd budget). It replaces an
// unbounded handle cache, so process resource use tracks *active* tenants
// rather than every tenant seen since startup.
type Manager struct {
	idleTTL      time.Duration
	maxOpen      int
	pathResolver func(string) string
	defaultOpen  func(string) (*MemoryDB, error)

	mu        sync.Mutex
	instances map[string]*Instance // every tenant ever seen (cheap structs)
	open      map[string]*Instance // subset whose db handle is currently open

	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once

	opens  atomic.Int64
	closes atomic.Int64
}

// NewManager creates a Manager and starts its background reaper.
func NewManager(opts ManagerOptions) *Manager {
	if opts.IdleTTL <= 0 {
		opts.IdleTTL = 2 * time.Minute
	}
	if opts.SweepInterval <= 0 {
		opts.SweepInterval = 30 * time.Second
	}
	m := &Manager{
		idleTTL:      opts.IdleTTL,
		maxOpen:      opts.MaxOpen,
		pathResolver: opts.PathResolver,
		defaultOpen:  opts.DefaultOpen,
		instances:    make(map[string]*Instance),
		open:         make(map[string]*Instance),
		stop:         make(chan struct{}),
		done:         make(chan struct{}),
	}
	go m.reapLoop(opts.SweepInterval)
	return m
}

// Instance returns the (get-or-created) Instance for a path. This is cheap and
// never opens the DB; the handle opens on the first Acquire. Uses OpenMemory,
// or ManagerOptions.DefaultOpen when one was configured.
func (m *Manager) Instance(path string) *Instance {
	open := OpenMemory
	if m.defaultOpen != nil {
		open = m.defaultOpen
	}
	return m.InstanceWith(path, open)
}

// InstanceWith is Instance with a custom opener, letting the same manager (and
// its fd-budget + idle-reaper) manage non-memory stores like the *.files blob
// DB. The opener is fixed on first creation for a given path.
func (m *Manager) InstanceWith(path string, open func(string) (*MemoryDB, error)) *Instance {
	m.mu.Lock()
	defer m.mu.Unlock()
	if i, ok := m.instances[path]; ok {
		return i
	}
	if m.pathResolver != nil {
		resolve, orig := m.pathResolver, open
		open = func(logicalPath string) (*MemoryDB, error) { return orig(resolve(logicalPath)) }
	}
	i := &Instance{path: path, mgr: m, open: open}
	m.instances[path] = i
	return i
}

func (m *Manager) onOpen(i *Instance) {
	m.mu.Lock()
	m.open[i.path] = i
	m.mu.Unlock()
	m.opens.Add(1)
}

func (m *Manager) onClose(i *Instance) {
	m.mu.Lock()
	delete(m.open, i.path)
	m.mu.Unlock()
	m.closes.Add(1)
}

// enforceBudget closes least-recently-used idle handles until the open count is
// back under MaxOpen. If every open handle is busy (refs>0) it gives up rather
// than block, tolerating a temporary overshoot that the next acquire retries.
func (m *Manager) enforceBudget() {
	if m.maxOpen <= 0 {
		return
	}
	for m.OpenCount() >= m.maxOpen {
		victim := m.lruIdleOpen()
		if victim == nil {
			return // all open handles are in use
		}
		if !victim.closeIfIdle(0) {
			// Lost the race (reused or already closed); re-evaluate.
			continue
		}
	}
}

// lruIdleOpen returns the open, unreferenced instance with the oldest lastUsed.
func (m *Manager) lruIdleOpen() *Instance {
	m.mu.Lock()
	candidates := make([]*Instance, 0, len(m.open))
	for _, i := range m.open {
		if i.refs.Load() == 0 {
			candidates = append(candidates, i)
		}
	}
	m.mu.Unlock()
	if len(candidates) == 0 {
		return nil
	}
	sort.Slice(candidates, func(a, b int) bool {
		return candidates[a].lastUsed.Load() < candidates[b].lastUsed.Load()
	})
	return candidates[0]
}

func (m *Manager) reapLoop(interval time.Duration) {
	defer close(m.done)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-t.C:
			m.reap()
		}
	}
}

func (m *Manager) reap() {
	m.mu.Lock()
	snapshot := make([]*Instance, 0, len(m.open))
	for _, i := range m.open {
		snapshot = append(snapshot, i)
	}
	m.mu.Unlock()
	for _, i := range snapshot {
		i.closeIfIdle(m.idleTTL)
	}
}

// Shutdown stops the reaper and closes every open handle. Idempotent.
func (m *Manager) Shutdown() {
	m.stopOnce.Do(func() { close(m.stop) })
	<-m.done
	m.mu.Lock()
	snapshot := make([]*Instance, 0, len(m.open))
	for _, i := range m.open {
		snapshot = append(snapshot, i)
	}
	m.mu.Unlock()
	for _, i := range snapshot {
		i.closeIfIdle(0)
	}
}

// OpenCount is the number of tenant handles currently open.
func (m *Manager) OpenCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.open)
}

// Opens and Closes are cumulative lifecycle counters, useful for observability.
func (m *Manager) Opens() int64  { return m.opens.Load() }
func (m *Manager) Closes() int64 { return m.closes.Load() }
