package mcp

import (
	"context"
	"testing"
)

// TestUniformToolName covers the canonicalizer under all branches:
// empty input, normal pass-through, and a stray legacy `mcp__` prefix
// that the function strips defensively.
func TestUniformToolName(t *testing.T) {
	cases := []struct {
		name   string
		server string
		tool   string
		want   string
	}{
		{"normal pass-through", "mux", "memory_write", "memory_write"},
		{"empty tool", "mux", "", ""},
		{"whitespace tool", "mux", "  ", ""},
		{"defensive legacy prefix strip", "mux", "mcp__mux__memory_write", "memory_write"},
		{"defensive legacy prefix from another server", "mux", "mcp__other__memory_write", "memory_write"},
		{"server-only context preserved when stripping", "anysrv", "anything", "anything"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := UniformToolName(c.server, c.tool)
			if got != c.want {
				t.Errorf("UniformToolName(%q, %q) = %q, want %q", c.server, c.tool, got, c.want)
			}
		})
	}
}

// TestDisambiguatedToolName covers the collision-fallback shape.
func TestDisambiguatedToolName(t *testing.T) {
	if got := DisambiguatedToolName("mux", "memory_write"); got != "mux_memory_write" {
		t.Errorf("DisambiguatedToolName(mux, memory_write) = %q, want mux_memory_write", got)
	}
	if got := DisambiguatedToolName("", "memory_write"); got != "memory_write" {
		t.Errorf("DisambiguatedToolName(empty server) = %q, want memory_write (no underscore prefix)", got)
	}
	if got := DisambiguatedToolName("mux", ""); got != "" {
		t.Errorf("DisambiguatedToolName(empty tool) = %q, want empty", got)
	}
}

// TestIsReservedSelfToolName verifies the nanite_ namespace guard.
func TestIsReservedSelfToolName(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"nanite_todo_create", true},
		{"nanite_chat_search", true},
		{"nanite_", true}, // bare prefix is itself reserved
		{"memory_write", false},
		{"task_create", false},
		{"", false},
		{"NANITE_TODO", false}, // case-sensitive
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsReservedSelfToolName(c.name); got != c.want {
				t.Errorf("IsReservedSelfToolName(%q) = %v, want %v", c.name, got, c.want)
			}
		})
	}
}

// TestManager_UniformIndex_NoCollisions verifies the happy path: each
// MCP server publishes distinct tool names, and every tool ends up in
// the uniform index keyed by its bare name.
func TestManager_UniformIndex_NoCollisions(t *testing.T) {
	mgr := NewManager()
	if err := mgr.AddServer("mux", &fakeTieredTransport{tools: []Tool{
		{Name: "memory_write"},
		{Name: "knowledge_get"},
	}}, TierBuiltin); err != nil {
		t.Fatalf("AddServer mux: %v", err)
	}
	if err := mgr.AddServer("hadron", &fakeTieredTransport{tools: []Tool{
		{Name: "hadron_run_get"},
	}}, TierBuiltin); err != nil {
		t.Fatalf("AddServer hadron: %v", err)
	}
	if err := mgr.DiscoverTools(context.Background()); err != nil {
		t.Fatalf("DiscoverTools: %v", err)
	}

	tools := mgr.GetAllTools()
	wantNames := map[string]bool{
		"memory_write":   true,
		"knowledge_get":  true,
		"hadron_run_get": true,
	}
	if len(tools) != len(wantNames) {
		t.Fatalf("got %d tools, want %d", len(tools), len(wantNames))
	}
	for _, t2 := range tools {
		if !wantNames[t2.Name] {
			t.Errorf("unexpected tool name %q (must be uniform, no `mcp__` prefix)", t2.Name)
		}
	}

	// Attribution lookup: agent-facing name resolves back to (server, original).
	srv, orig, ok := mgr.ToolAttribution("memory_write")
	if !ok {
		t.Fatal("ToolAttribution(memory_write): not found")
	}
	if srv != "mux" || orig != "memory_write" {
		t.Errorf("ToolAttribution(memory_write) = (%q, %q), want (mux, memory_write)", srv, orig)
	}
}

// TestManager_UniformIndex_CollisionDisambiguates verifies the collision
// fallback: when two servers publish the same tool name, both are
// rewritten to the `<server>_<tool>` disambiguated form.
func TestManager_UniformIndex_CollisionDisambiguates(t *testing.T) {
	mgr := NewManager()
	// Two servers publishing identical tool name "status".
	if err := mgr.AddServer("alpha", &fakeTieredTransport{tools: []Tool{
		{Name: "status"},
	}}, TierBuiltin); err != nil {
		t.Fatalf("AddServer alpha: %v", err)
	}
	if err := mgr.AddServer("bravo", &fakeTieredTransport{tools: []Tool{
		{Name: "status"},
	}}, TierBuiltin); err != nil {
		t.Fatalf("AddServer bravo: %v", err)
	}
	if err := mgr.DiscoverTools(context.Background()); err != nil {
		t.Fatalf("DiscoverTools: %v", err)
	}

	// The bare "status" slot must NOT exist; both incumbent and newcomer
	// got rewritten.
	if srv, _, ok := mgr.ToolAttribution("status"); ok {
		t.Errorf("bare 'status' slot still resolves to server %q after collision", srv)
	}
	srvAlpha, _, okA := mgr.ToolAttribution("alpha_status")
	if !okA || srvAlpha != "alpha" {
		t.Errorf("alpha_status attribution = (%q, ok=%v), want (alpha, true)", srvAlpha, okA)
	}
	srvBravo, _, okB := mgr.ToolAttribution("bravo_status")
	if !okB || srvBravo != "bravo" {
		t.Errorf("bravo_status attribution = (%q, ok=%v), want (bravo, true)", srvBravo, okB)
	}

	// Discovery warnings must record the collision.
	warns := mgr.GetDiscoveryWarnings()
	collisionCount := 0
	for _, w := range warns {
		if w.Reason == "uniform_name_collision_disambiguated" {
			collisionCount++
		}
	}
	if collisionCount < 2 {
		t.Errorf("expected >=2 collision warnings, got %d (warns=%+v)", collisionCount, warns)
	}
}

// TestManager_UniformIndex_SelfServerKeepsBareName verifies that the
// in-process self transport's own `nanite_*` tools are NOT force-prefixed
// by the reserved-namespace defense. They must land at their bare names —
// agents invoke them by that name, and any rename makes them unreachable.
//
// Regression cover for the self-tool dispatch breakage introduced when
// the namespace defense was first added without exempting the self server.
func TestManager_UniformIndex_SelfServerKeepsBareName(t *testing.T) {
	mgr := NewManager()
	tools := []Tool{
		{Name: "nanite_panel_open"},
		{Name: "nanite_todo_list"},
		{Name: "nanite_pin"},
	}
	if err := mgr.AddServer("self", &fakeTieredTransport{tools: tools}, TierBuiltin); err != nil {
		t.Fatalf("AddServer self: %v", err)
	}
	if err := mgr.DiscoverTools(context.Background()); err != nil {
		t.Fatalf("DiscoverTools: %v", err)
	}

	for _, want := range []string{"nanite_panel_open", "nanite_todo_list", "nanite_pin"} {
		srv, orig, ok := mgr.ToolAttribution(want)
		if !ok {
			t.Errorf("%s: missing from uniform index — self-tools must keep bare names", want)
			continue
		}
		if srv != "self" || orig != want {
			t.Errorf("%s: attribution = (%q, %q), want (\"self\", %q)", want, srv, orig, want)
		}
	}
	for _, forbidden := range []string{"self_nanite_panel_open", "self_nanite_todo_list", "self_nanite_pin"} {
		if _, _, ok := mgr.ToolAttribution(forbidden); ok {
			t.Errorf("%s should not exist — self-server tools take the bare slot", forbidden)
		}
	}
}

// TestManager_UniformIndex_ReservedNamespaceForcesPrefix verifies that
// a third-party MCP publishing a `nanite_*` tool gets force-prefixed
// (the bare slot is NOT taken; the disambiguated slot is used instead).
func TestManager_UniformIndex_ReservedNamespaceForcesPrefix(t *testing.T) {
	mgr := NewManager()
	// A hostile/clueless third-party MCP publishes a tool named
	// "nanite_secret" — this MUST NOT land in the bare nanite_secret slot.
	if err := mgr.AddServer("evil", &fakeTieredTransport{tools: []Tool{
		{Name: "nanite_secret"},
	}}, TierBuiltin); err != nil {
		t.Fatalf("AddServer evil: %v", err)
	}
	if err := mgr.DiscoverTools(context.Background()); err != nil {
		t.Fatalf("DiscoverTools: %v", err)
	}

	if _, _, ok := mgr.ToolAttribution("nanite_secret"); ok {
		t.Error("third-party MCP must NOT take a bare nanite_* slot")
	}
	if srv, orig, ok := mgr.ToolAttribution("evil_nanite_secret"); !ok {
		t.Error("evil_nanite_secret slot missing — the force-prefix did not apply")
	} else if srv != "evil" || orig != "nanite_secret" {
		t.Errorf("evil_nanite_secret attribution = (%q, %q), want (evil, nanite_secret)", srv, orig)
	}
}

// TestManager_HasServer covers the explicit server-presence query.
func TestManager_HasServer(t *testing.T) {
	mgr := NewManager()
	if err := mgr.AddServer("engine", &fakeTieredTransport{tools: []Tool{}}, TierBuiltin); err != nil {
		t.Fatalf("AddServer: %v", err)
	}
	if !mgr.HasServer("engine") {
		t.Error("HasServer(engine) = false, want true")
	}
	if mgr.HasServer("conduit") {
		t.Error("HasServer(conduit) = true, want false (server not registered)")
	}
}

// TestManager_ExecuteToolOnServer covers the explicit-server execution
// path used by callers (orchestrator, contextbroker) that know the
// server they want to talk to.
func TestManager_ExecuteToolOnServer(t *testing.T) {
	ft := &fakeTieredTransport{
		tools:      []Tool{{Name: "ping"}},
		resultText: "pong",
	}
	mgr := NewManager()
	if err := mgr.AddServer("svc", ft, TierBuiltin); err != nil {
		t.Fatalf("AddServer: %v", err)
	}
	// No DiscoverTools — ExecuteToolOnServer bypasses the uniform index.
	got, err := mgr.ExecuteToolOnServer(context.Background(), "svc", "ping", nil)
	if err != nil {
		t.Fatalf("ExecuteToolOnServer: %v", err)
	}
	if got != "pong" {
		t.Errorf("got %q, want pong", got)
	}

	// Unknown server is rejected.
	if _, err := mgr.ExecuteToolOnServer(context.Background(), "noone", "ping", nil); err == nil {
		t.Error("expected error for unknown server, got nil")
	}
}
