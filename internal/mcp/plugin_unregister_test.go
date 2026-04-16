package mcp

import (
	"testing"
)

// TestManager_RemoveServersByPlugin verifies that servers registered under a
// pluginID can be swept in one call, while servers belonging to other plugins
// (or registered without a pluginID via AddServer) are untouched.
func TestManager_RemoveServersByPlugin(t *testing.T) {
	mgr := NewManager()

	// A regular non-plugin server — RemoveServersByPlugin must not touch it.
	if err := mgr.AddServer("core", stubTransport{}, TierBuiltin); err != nil {
		t.Fatalf("AddServer(core): %v", err)
	}

	// Two servers owned by plugin-a (no real subprocess — use AddServer
	// directly, then poke the reverse map).
	if err := mgr.AddServer("a1", stubTransport{}, TierBuiltin); err != nil {
		t.Fatalf("AddServer(a1): %v", err)
	}
	if err := mgr.AddServer("a2", stubTransport{}, TierBuiltin); err != nil {
		t.Fatalf("AddServer(a2): %v", err)
	}
	mgr.mu.Lock()
	mgr.pluginServers["plugin-a"] = []string{"a1", "a2"}
	mgr.mu.Unlock()

	// One server owned by plugin-b.
	if err := mgr.AddServer("b1", stubTransport{}, TierBuiltin); err != nil {
		t.Fatalf("AddServer(b1): %v", err)
	}
	mgr.mu.Lock()
	mgr.pluginServers["plugin-b"] = []string{"b1"}
	mgr.mu.Unlock()

	// Remove plugin-a's servers.
	if n := mgr.RemoveServersByPlugin("plugin-a"); n != 2 {
		t.Fatalf("RemoveServersByPlugin(plugin-a): got %d want 2", n)
	}

	mgr.mu.RLock()
	_, hasA1 := mgr.servers["a1"]
	_, hasA2 := mgr.servers["a2"]
	_, hasB1 := mgr.servers["b1"]
	_, hasCore := mgr.servers["core"]
	_, stillMapped := mgr.pluginServers["plugin-a"]
	mgr.mu.RUnlock()

	if hasA1 || hasA2 {
		t.Fatalf("plugin-a servers still registered: a1=%v a2=%v", hasA1, hasA2)
	}
	if !hasB1 {
		t.Fatal("plugin-b server b1 was unexpectedly removed")
	}
	if !hasCore {
		t.Fatal("core (non-plugin) server was unexpectedly removed")
	}
	if stillMapped {
		t.Fatal("plugin-a reverse-map entry not cleared")
	}

	// Removing an unknown plugin is a no-op.
	if n := mgr.RemoveServersByPlugin("plugin-z"); n != 0 {
		t.Fatalf("RemoveServersByPlugin(plugin-z): got %d want 0", n)
	}
	// Empty pluginID is a no-op.
	if n := mgr.RemoveServersByPlugin(""); n != 0 {
		t.Fatalf("RemoveServersByPlugin(\"\"): got %d want 0", n)
	}
}
