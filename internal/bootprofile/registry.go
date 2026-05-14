package bootprofile

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// ProviderIDPrefix is the namespace marker for dropdown / session
// Provider strings that point at a compiled boot-profile LaunchSpec
// rather than a DB-seeded API/CLI provider row.
//
// CW-20260514-0047 chose stable composite IDs (approach (a)) over a
// session-scoped registry-by-short-id (approach (b)) because:
//
//   - The dropdown already round-trips a single string through
//     session.Provider; a composite ID keeps the wire shape unchanged.
//   - No extra DB tables / migrations / TTL semantics. Adding a
//     registry-by-short-id would also force every consumer that wants
//     to print the provider name to consult the registry just to
//     reconstruct the human-meaningful ProfileID.
//   - The encode/decode pair is a pure function and trivially testable
//     for round-trip. The runtime resolution still hits the in-memory
//     Registry (Lookup) — but a missing registry entry then degrades
//     to "ID parses but no spec" rather than "lost the string entirely".
//   - The plugin / catalog-reload story (Reload) is simpler when the
//     ID is self-describing; cache invalidation only has to refresh
//     the LaunchSpec map, not also a separate ID→ProfileID index.
//
// Profile IDs are domain-controlled (we picked them, no user-supplied
// data lands in the encoded string), so we don't bother URL-escaping
// them. The decoder requires a single `:` separator and a non-empty
// profile id; anything else returns ok=false. If a future ticket
// extends the encoding (e.g. launch overrides), do it as another `:`
// suffix and bump the decoder.
const ProviderIDPrefix = "bootprofile:"

// EncodeProviderID returns the stable composite provider id used by
// the dropdown and persisted on session.Provider. Empty input returns
// the empty string so callers can chain through an unset profile id.
func EncodeProviderID(profileID string) string {
	if profileID == "" {
		return ""
	}
	return ProviderIDPrefix + profileID
}

// DecodeProviderID parses an encoded provider id back to its profile
// id. ok=false when the input does not start with ProviderIDPrefix or
// the suffix is empty. The function is tolerant of leading / trailing
// whitespace so a session row migrated from an older format with
// stray space does not silently miss; callers should still feed it
// the FE-supplied value verbatim.
func DecodeProviderID(id string) (profileID string, ok bool) {
	trim := strings.TrimSpace(id)
	if !strings.HasPrefix(trim, ProviderIDPrefix) {
		return "", false
	}
	suffix := strings.TrimSpace(strings.TrimPrefix(trim, ProviderIDPrefix))
	if suffix == "" {
		return "", false
	}
	return suffix, true
}

// IsProviderID reports whether the given string is a boot-profile
// encoded provider id. Convenience for handlers / classifiers that
// only care about the namespace check.
func IsProviderID(id string) bool {
	_, ok := DecodeProviderID(id)
	return ok
}

// Registry caches compiled LaunchSpec values for all boot-profile
// catalog entries and exposes the two surface methods the rest of
// the Nanite codebase consumes:
//
//	List()    — for the provider/model dropdown surface.
//	Lookup(id) — for the chat runtime hookup (CW-20260514-0048).
//
// The registry is constructed once at startup (NewRegistry) from a
// catalog path. Reload re-reads the catalog from disk and atomically
// swaps the cached entries — suitable for the plugin lifecycle wiring
// CW-20260514-0049/0050 will introduce. Until then, an operator can
// trigger a reload by hitting whatever surface a future ticket adds;
// the registry itself is wired to be reload-ready.
//
// A nil / zero Registry is the "no catalog configured" state — both
// public methods are nil-safe (List returns nil, Lookup returns
// ok=false), so callers can construct an API response unconditionally
// and let an unset catalog flow through as "no extra entries".
type Registry struct {
	mu          sync.RWMutex
	catalogPath string
	// specs is the cached compiled LaunchSpec set keyed by ProfileID.
	// Always non-nil after a successful Load; reset to a new map on
	// Reload so readers holding a previous snapshot don't see a
	// partially-rebuilt state.
	specs map[string]*LaunchSpec
	// catalog is the most-recently-loaded *Catalog. Cached so
	// CompileFor (CW-20260514-0048) can re-compile a profile with
	// session-scoped vars WITHOUT re-walking the catalog directory.
	// nil = no catalog configured / load failed. Reload swaps this
	// atomically with the specs map under r.mu.
	catalog *Catalog
}

// NewRegistry constructs a Registry rooted at the given catalog path
// and performs the initial load. An empty path returns a usable but
// empty Registry — the "no catalog configured" branch — so callers
// can wire it unconditionally and let downstream code observe the
// empty List().
//
// Compile errors for individual profiles do NOT abort the load:
// each profile is compiled independently and a failed one is logged
// (via the returned error wrapping the failures) but other profiles
// still land. This matches the CLI/dropdown UX goal of "show the
// profiles that work even when one is misconfigured" — the operator
// fixes the bad YAML and Reloads.
//
// Callers that want strict load semantics can use LoadCatalog +
// CompileFromCatalog directly; the registry's tolerance is scoped
// to the dropdown / runtime entry path.
func NewRegistry(catalogPath string) (*Registry, error) {
	r := &Registry{
		catalogPath: catalogPath,
		specs:       map[string]*LaunchSpec{},
	}
	if err := r.Reload(); err != nil {
		return r, err
	}
	return r, nil
}

// Reload re-reads the catalog from disk and refreshes the cached
// LaunchSpec entries. Safe to call concurrently with List / Lookup —
// the swap is atomic (the new map is built off-lock and assigned
// under the write lock).
//
// An empty catalog path is treated as "no catalog" and clears the
// cache without error. A missing catalog directory is likewise a
// no-op (LoadCatalog already treats it as the empty-catalog branch).
//
// Per-profile compile errors are collected into a single returned
// error so the operator sees all failures at once; the successfully
// compiled profiles are still installed in the cache.
func (r *Registry) Reload() error {
	if r == nil {
		return nil
	}
	cat, err := LoadCatalog(r.catalogPath)
	if err != nil {
		return fmt.Errorf("bootprofile: registry reload: %w", err)
	}
	next := make(map[string]*LaunchSpec, len(cat.Profiles))
	var compileErrs []string
	// Iterate in sorted order so any error message we build is
	// deterministic across runs — easier for operators to diff
	// between reloads.
	ids := make([]string, 0, len(cat.Profiles))
	for id := range cat.Profiles {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		spec, cerr := CompileFromCatalog(cat, id, nil)
		if cerr != nil {
			compileErrs = append(compileErrs, fmt.Sprintf("%s: %v", id, cerr))
			continue
		}
		next[id] = spec
	}
	r.mu.Lock()
	r.specs = next
	r.catalog = cat
	r.mu.Unlock()
	if len(compileErrs) > 0 {
		return fmt.Errorf("bootprofile: registry reload: %d profile(s) failed to compile: %s",
			len(compileErrs), strings.Join(compileErrs, "; "))
	}
	return nil
}

// List returns all cached LaunchSpec entries in ProfileID-sorted
// order. Sorted output keeps the dropdown stable across reloads —
// a profile that doesn't change shouldn't visually move because a
// neighbor was added.
//
// nil receiver returns nil — callers can range over the result
// without nil-checking.
func (r *Registry) List() []*LaunchSpec {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*LaunchSpec, 0, len(r.specs))
	ids := make([]string, 0, len(r.specs))
	for id := range r.specs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		out = append(out, r.specs[id])
	}
	return out
}

// Lookup retrieves a cached LaunchSpec by its dropdown-encoded
// provider id (e.g. "bootprofile:nanite.backend.main") OR by the
// bare profile id. Both forms are accepted so a caller that has the
// ProfileID directly (e.g. test setup) doesn't have to encode it
// just to look up.
//
// ok=false on:
//
//   - nil receiver
//   - empty id
//   - id is not in the cache
//
// The returned spec is the same pointer stored in the cache; callers
// MUST treat it as read-only. Reload allocates a new map / new specs,
// so a previously-returned pointer remains valid even after a reload
// but it represents the pre-reload state.
func (r *Registry) Lookup(id string) (*LaunchSpec, bool) {
	if r == nil {
		return nil, false
	}
	if id == "" {
		return nil, false
	}
	profileID := id
	if decoded, ok := DecodeProviderID(id); ok {
		profileID = decoded
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	spec, ok := r.specs[profileID]
	return spec, ok
}

// CompileFor re-compiles the named profile against the cached
// catalog with caller-supplied session-scoped variables. Distinct
// from Lookup: Lookup returns the registry's cached *LaunchSpec
// (compiled with empty vars at Reload time, suitable for the
// dropdown surface); CompileFor produces a FRESH spec with the
// caller's vars applied so a session boot can substitute
// per-session knobs (e.g. {{session_id}}, {{role}}) into slot
// content WITHOUT mutating the cached spec.
//
// CW-20260514-0048: the chat-runtime hookup uses this at boot
// time. The cached spec stays read-only for the listing surface;
// the chat layer pulls a fresh compile every Boot.
//
// Returns ErrProfileNotFound when the id is unknown to the
// cached catalog. Returns the underlying compile error verbatim
// otherwise — callers may want to surface the slot name / missing
// var name in their own error context.
//
// nil receiver / no catalog configured returns ErrProfileNotFound
// so callers don't have to nil-check separately from the
// not-in-catalog branch.
func (r *Registry) CompileFor(profileID string, vars Vars) (*LaunchSpec, error) {
	if r == nil {
		return nil, ErrProfileNotFound
	}
	if profileID == "" {
		return nil, ErrProfileNotFound
	}
	// Tolerate the encoded provider id form so callers can pass
	// the value coming off session.Provider verbatim.
	if decoded, ok := DecodeProviderID(profileID); ok {
		profileID = decoded
	}
	r.mu.RLock()
	cat := r.catalog
	r.mu.RUnlock()
	if cat == nil {
		return nil, ErrProfileNotFound
	}
	return CompileFromCatalog(cat, profileID, vars)
}

// CatalogPath returns the configured catalog path the registry loads
// from. Useful for logging / debug surfaces; not used in core paths.
func (r *Registry) CatalogPath() string {
	if r == nil {
		return ""
	}
	return r.catalogPath
}

// IsEmpty reports whether the registry has any cached LaunchSpecs.
// Mirrors Catalog.IsEmpty for the "no catalog configured" branch.
func (r *Registry) IsEmpty() bool {
	if r == nil {
		return true
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.specs) == 0
}
