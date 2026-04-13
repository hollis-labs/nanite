package plugin

import (
	"net/http"
	"sync"
)

// MutablePluginMux is an http.Handler that supports per-plugin route removal.
//
// net/http's *http.ServeMux does not allow pattern removal or re-registration,
// which is fatal for plugin hot-unload. This wrapper tracks routes by plugin
// ID and rebuilds an internal *http.ServeMux whenever a plugin's routes are
// removed. Route lookup on the hot path is a single RLock around the inner
// mux's ServeHTTP, which keeps the common case cheap.
//
// The mux uses Go 1.22 net/http pattern syntax verbatim — callers may pass
// "METHOD /path" and path wildcards like "{id}" and they are honored by the
// inner mux.
type MutablePluginMux struct {
	mu     sync.RWMutex
	routes map[string]routeEntry // pattern → entry
	inner  *http.ServeMux        // rebuilt on RemoveByPlugin
}

type routeEntry struct {
	pattern  string
	handler  http.Handler
	pluginID string
}

// NewMutablePluginMux constructs an empty mutable plugin mux.
func NewMutablePluginMux() *MutablePluginMux {
	return &MutablePluginMux{
		routes: make(map[string]routeEntry),
		inner:  http.NewServeMux(),
	}
}

// Handle registers handler at pattern under the given pluginID. Subsequent
// calls with the same pattern replace the earlier registration (required so a
// plugin reload with the same routes doesn't fail the way bare *http.ServeMux
// would). Replacement rebuilds the inner mux because *http.ServeMux forbids
// re-registering a pattern.
func (m *MutablePluginMux) Handle(pluginID, pattern string, handler http.Handler) {
	m.mu.Lock()
	defer m.mu.Unlock()

	_, existed := m.routes[pattern]
	m.routes[pattern] = routeEntry{pattern: pattern, handler: handler, pluginID: pluginID}

	if existed {
		m.rebuildLocked()
		return
	}
	// First time for this pattern — just register on the current inner.
	m.inner.Handle(pattern, handler)
}

// RemoveByPlugin deletes every route whose pluginID matches id and rebuilds
// the inner mux with the remaining entries. Returns the number of routes
// removed.
func (m *MutablePluginMux) RemoveByPlugin(id string) int {
	m.mu.Lock()
	defer m.mu.Unlock()

	removed := 0
	for pattern, entry := range m.routes {
		if entry.pluginID == id {
			delete(m.routes, pattern)
			removed++
		}
	}
	if removed > 0 {
		m.rebuildLocked()
	}
	return removed
}

// CountByPlugin returns the number of routes owned by id.
func (m *MutablePluginMux) CountByPlugin(id string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	n := 0
	for _, entry := range m.routes {
		if entry.pluginID == id {
			n++
		}
	}
	return n
}

// ServeHTTP forwards to the inner mux. RLock makes concurrent reads cheap;
// writers take the exclusive lock only when registrations change.
func (m *MutablePluginMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.mu.RLock()
	inner := m.inner
	m.mu.RUnlock()
	inner.ServeHTTP(w, r)
}

// rebuildLocked constructs a fresh *http.ServeMux from the current routes
// map. Caller must hold m.mu.
func (m *MutablePluginMux) rebuildLocked() {
	fresh := http.NewServeMux()
	for _, entry := range m.routes {
		fresh.Handle(entry.pattern, entry.handler)
	}
	m.inner = fresh
}
