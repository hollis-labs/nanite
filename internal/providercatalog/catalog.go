// Package providercatalog records the human-readable catalog metadata
// that pairs with each provider registered in the engine
// provider.Registry. CW-20260526-0001.
//
// Before this package, three sources of truth carried provider
// information that had to be kept in sync by hand:
//
//   - cmd/nanite/main.go:initProviders — Registry registration (what
//     chat can resolve and route through).
//   - internal/store/seed.go:seededProviders — DB rows surfaced in the
//     composer's provider/model dropdown.
//   - pkg/models.AllSeeded() — DB models keyed by provider_type.
//
// Adding a new API provider required edits in all three; forgetting
// one made the provider invisible in the dropdown or unroutable in
// chat. The Catalog collapses (1) and (2) into a single registration
// site: initProviders builds both the Registry and the Catalog, and
// the API layer enumerates the Catalog when populating
// /api/providers. The DB providers table stays only as a backing seed
// for model FK integrity.
//
// Models still live in DB / pkg/models — see the Hybrid choice in
// CW-20260526-0001.
package providercatalog

import "sync"

// Entry is a single registry-backed provider's catalog metadata. Name
// matches the registry registration name (e.g. "anthropic"); RowID is
// the stable id the API surfaces and the FE groups by — mirrors the
// pre-hybrid DB row id format so model FKs and persisted
// session.Provider strings keep working without a schema migration.
type Entry struct {
	Name        string
	DisplayName string
	RowID       string
}

// Catalog is a goroutine-safe ordered list of catalog Entries. Order
// reflects registration order, which surfaces in the dropdown.
type Catalog struct {
	mu      sync.RWMutex
	entries []Entry
	byName  map[string]int // name -> index into entries
}

// New returns an empty Catalog.
func New() *Catalog {
	return &Catalog{byName: make(map[string]int)}
}

// Add appends or replaces (by Name) an Entry. Replacement preserves
// the original position so a re-registration doesn't reshuffle the
// dropdown.
func (c *Catalog) Add(e Entry) {
	if c == nil || e.Name == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if idx, ok := c.byName[e.Name]; ok {
		c.entries[idx] = e
		return
	}
	c.byName[e.Name] = len(c.entries)
	c.entries = append(c.entries, e)
}

// List returns a snapshot of the catalog in registration order. Safe
// to call concurrently with Add.
func (c *Catalog) List() []Entry {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]Entry, len(c.entries))
	copy(out, c.entries)
	return out
}

// Get returns the Entry for name and whether it was present.
func (c *Catalog) Get(name string) (Entry, bool) {
	if c == nil {
		return Entry{}, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	idx, ok := c.byName[name]
	if !ok {
		return Entry{}, false
	}
	return c.entries[idx], true
}
