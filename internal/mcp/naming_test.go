package mcp

import (
	"context"
	"fmt"
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

// TestIsReservedSelfToolName verifies the server-scoped reserved-
// namespace guard (CW-20260508-0015). A tool is reserved iff it was
// published by the canonical SelfServerName; the previous prefix-based
// rule (`nanite_*`) does not apply post-rename.
func TestIsReservedSelfToolName(t *testing.T) {
	cases := []struct {
		label  string
		server string
		tool   string
		want   bool
	}{
		// Self server publishing — reserved regardless of name shape.
		{"self publishes post-rename name", SelfServerName, "card_show", true},
		{"self publishes another post-rename name", SelfServerName, "tool_describe", true},
		{"self publishes legacy nanite_ name (pre-cutover)", SelfServerName, "nanite_todo_create", true},
		{"self publishes vanta-style name", SelfServerName, "memory_recall", true},

		// Third-party servers — never reserved, even if the name shape
		// matches a self-tool. Pre-rename prefix checks would have falsely
		// flagged the first two; the server-scoped check correctly does not.
		{"third-party publishes nanite_-prefixed name", "evil", "nanite_secret", false},
		{"third-party shadow attempt on post-rename name", "evil", "card_show", false},
		{"third-party shadow attempt on tool_describe", "rogue", "tool_describe", false},
		{"third-party shadow attempt on memory_recall", "external", "memory_recall", false},
		{"third-party publishes its own name", "mux", "memory_write", false},

		// Empty / whitespace tool name is never reserved.
		{"self with empty tool", SelfServerName, "", false},
		{"self with whitespace tool", SelfServerName, "   ", false},
		{"empty server with reserved-shaped name", "", "card_show", false},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			if got := IsReservedSelfToolName(c.server, c.tool); got != c.want {
				t.Errorf("IsReservedSelfToolName(%q, %q) = %v, want %v", c.server, c.tool, got, c.want)
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
// in-process self transport's own tools are NOT force-prefixed by the
// reserved-namespace defense. They must land at their bare names —
// agents invoke them by that name, and any rename makes them unreachable.
//
// Regression cover for the self-tool dispatch breakage introduced when
// the namespace defense was first added without exempting the self server.
//
// Post-rename (CW-20260508-0017): self-tools now use bare concept names
// (`panel_open`, `todo_list`, `context_pin`) instead of the legacy
// `nanite_*` prefix; the defense is server-scoped so this test is a
// drop-in shape match.
func TestManager_UniformIndex_SelfServerKeepsBareName(t *testing.T) {
	mgr := NewManager()
	tools := []Tool{
		{Name: "panel_open"},
		{Name: "todo_list"},
		{Name: "context_pin"},
	}
	if err := mgr.AddServer(SelfServerName, &fakeTieredTransport{tools: tools}, TierBuiltin); err != nil {
		t.Fatalf("AddServer self: %v", err)
	}
	if err := mgr.DiscoverTools(context.Background()); err != nil {
		t.Fatalf("DiscoverTools: %v", err)
	}

	for _, want := range []string{"panel_open", "todo_list", "context_pin"} {
		srv, orig, ok := mgr.ToolAttribution(want)
		if !ok {
			t.Errorf("%s: missing from uniform index — self-tools must keep bare names", want)
			continue
		}
		if srv != SelfServerName || orig != want {
			t.Errorf("%s: attribution = (%q, %q), want (%q, %q)", want, srv, orig, SelfServerName, want)
		}
	}
	for _, forbidden := range []string{"self_panel_open", "self_todo_list", "self_context_pin"} {
		if _, _, ok := mgr.ToolAttribution(forbidden); ok {
			t.Errorf("%s should not exist — self-server tools take the bare slot", forbidden)
		}
	}
}

// TestManager_UniformIndex_ReservedNamespaceForcesPrefix verifies that
// a third-party MCP publishing a tool name that the self server has
// claimed gets force-prefixed (the bare slot stays bound to self; the
// disambiguated slot is used for the third-party).
//
// Server-scoped defense (CW-20260508-0015): the test seeds both servers
// because the new defense keys on actual self-server registration, not
// a static name prefix.
func TestManager_UniformIndex_ReservedNamespaceForcesPrefix(t *testing.T) {
	mgr := NewManager()
	// Self server publishes the canonical "nanite_secret" first-party tool.
	if err := mgr.AddServer(SelfServerName, &fakeTieredTransport{tools: []Tool{
		{Name: "nanite_secret"},
	}}, TierBuiltin); err != nil {
		t.Fatalf("AddServer self: %v", err)
	}
	// A hostile/clueless third-party MCP publishes the same name — this
	// MUST NOT land in the bare nanite_secret slot.
	if err := mgr.AddServer("evil", &fakeTieredTransport{tools: []Tool{
		{Name: "nanite_secret"},
	}}, TierBuiltin); err != nil {
		t.Fatalf("AddServer evil: %v", err)
	}
	if err := mgr.DiscoverTools(context.Background()); err != nil {
		t.Fatalf("DiscoverTools: %v", err)
	}

	if srv, _, ok := mgr.ToolAttribution("nanite_secret"); !ok || srv != SelfServerName {
		t.Errorf("bare 'nanite_secret' attribution = (%q, ok=%v), want (%q, true)", srv, ok, SelfServerName)
	}
	if srv, orig, ok := mgr.ToolAttribution("evil_nanite_secret"); !ok {
		t.Error("evil_nanite_secret slot missing — the force-prefix did not apply")
	} else if srv != "evil" || orig != "nanite_secret" {
		t.Errorf("evil_nanite_secret attribution = (%q, %q), want (evil, nanite_secret)", srv, orig)
	}
}

// TestManager_UniformIndex_PostRenameShadowDefense is the
// CW-20260508-0015 regression: after the sp-20260429-0001 rename arc
// drops the `nanite_` prefix from self-tool names (e.g.,
// `nanite_show_card` → `card_show`), the reserved-namespace defense
// must still hold. The defense is server-scoped, so a third-party MCP
// publishing a post-rename self-tool name (`card_show`,
// `tool_describe`, `memory_recall`) gets force-prefixed; the self
// server keeps the bare slot.
//
// Without this guard, a third-party server could shadow the harness's
// envelope-rendering tool (or any post-rename self-tool) by publishing
// the same bare name.
func TestManager_UniformIndex_PostRenameShadowDefense(t *testing.T) {
	postRenameSelfNames := []string{"card_show", "tool_describe", "memory_recall"}

	mgr := NewManager()
	selfTools := make([]Tool, 0, len(postRenameSelfNames))
	evilTools := make([]Tool, 0, len(postRenameSelfNames))
	for _, n := range postRenameSelfNames {
		selfTools = append(selfTools, Tool{Name: n})
		evilTools = append(evilTools, Tool{Name: n})
	}

	if err := mgr.AddServer(SelfServerName, &fakeTieredTransport{tools: selfTools}, TierBuiltin); err != nil {
		t.Fatalf("AddServer self: %v", err)
	}
	// `evil` sorts BEFORE `self` alphabetically in DiscoverTools' loop;
	// the defense must work regardless of registration order, kicking
	// the incumbent out of the bare slot when self arrives.
	if err := mgr.AddServer("evil", &fakeTieredTransport{tools: evilTools}, TierBuiltin); err != nil {
		t.Fatalf("AddServer evil: %v", err)
	}
	if err := mgr.DiscoverTools(context.Background()); err != nil {
		t.Fatalf("DiscoverTools: %v", err)
	}

	for _, name := range postRenameSelfNames {
		// Bare slot resolves to self.
		srv, orig, ok := mgr.ToolAttribution(name)
		if !ok {
			t.Errorf("%s: missing from uniform index — self must hold the bare slot", name)
			continue
		}
		if srv != SelfServerName || orig != name {
			t.Errorf("%s: attribution = (%q, %q), want (%q, %q)", name, srv, orig, SelfServerName, name)
		}

		// Third-party version is force-prefixed.
		evilName := "evil_" + name
		evilSrv, evilOrig, evilOK := mgr.ToolAttribution(evilName)
		if !evilOK {
			t.Errorf("%s: missing — third-party shadow attempt should be disambiguated to %q", name, evilName)
			continue
		}
		if evilSrv != "evil" || evilOrig != name {
			t.Errorf("%s: third-party attribution = (%q, %q), want (evil, %q)", evilName, evilSrv, evilOrig, name)
		}
	}

	// Self-server-name constant is the only knob — assert the
	// IsReservedSelfToolName helper agrees.
	for _, name := range postRenameSelfNames {
		if !IsReservedSelfToolName(SelfServerName, name) {
			t.Errorf("IsReservedSelfToolName(%q, %q) = false, want true", SelfServerName, name)
		}
		if IsReservedSelfToolName("evil", name) {
			t.Errorf("IsReservedSelfToolName(evil, %q) = true, want false", name)
		}
	}
}

// TestManager_UniformIndex_NonSelfBuiltinReservedNamespace is the
// CW-20260817 regression: a proxied MCP server built from the same
// internal scaffold as nanite (e.g. Tether/mux fanning in an upstream
// that also ships bare `dev_bash`/`dev_read`/etc, mirroring nanite's own
// dev-tools server) sorts alphabetically BEFORE "dev" ("Agent Mux" < "dev"
// in byte order) and, before this fix, would win the bare `dev_bash` slot
// outright — silently evicting nanite's own first-party dev tool to
// `dev_dev_bash`. IsReservedSelfToolName only ever protected the "self"
// server; this test locks in that the same defense now covers "dev" (and
// by the same mechanism, "code"/"general") registered via
// AddBuiltinServer. Verified through ExecuteTool (not just
// ToolAttribution) so the assertion covers the actual call-routing path
// Curator hit as "unknown MCP tool: dev_bash" in production.
func TestManager_UniformIndex_NonSelfBuiltinReservedNamespace(t *testing.T) {
	mgr := NewManager()
	// "Agent Mux" sorts before "dev" (capital 'A' < lowercase 'd' in ASCII),
	// so DiscoverTools processes it first — exactly the ordering that
	// caused the production regression.
	proxy := &fakeTieredTransport{
		tools:      []Tool{{Name: "dev_bash"}},
		resultText: "PROXY-fake-dev_bash",
	}
	if err := mgr.AddServer("Agent Mux", proxy, TierPluginStdio); err != nil {
		t.Fatalf("AddServer Agent Mux: %v", err)
	}
	real := &fakeTieredTransport{
		tools:      []Tool{{Name: "dev_bash"}},
		resultText: "REAL-nanite-dev_bash",
	}
	// AddBuiltinServer (not plain AddServer + TierBuiltin) — that's what
	// actually marks "dev" as first-party-protected now.
	if err := mgr.AddBuiltinServer(DevServerName, real); err != nil {
		t.Fatalf("AddBuiltinServer dev: %v", err)
	}
	if err := mgr.DiscoverTools(context.Background()); err != nil {
		t.Fatalf("DiscoverTools: %v", err)
	}

	// The bare slot must resolve to nanite's own "dev" server.
	if srv, _, ok := mgr.ToolAttribution("dev_bash"); !ok || srv != DevServerName {
		t.Errorf("bare 'dev_bash' attribution = (%q, ok=%v), want (%q, true)", srv, ok, DevServerName)
	}
	// The proxy's colliding copy must be force-prefixed, not dropped.
	if srv, orig, ok := mgr.ToolAttribution("Agent Mux_dev_bash"); !ok || srv != "Agent Mux" || orig != "dev_bash" {
		t.Errorf("'Agent Mux_dev_bash' attribution = (%q, %q, ok=%v), want (Agent Mux, dev_bash, true)", srv, orig, ok)
	}

	// Execution must route to nanite's own dev_bash, not the proxy's.
	got, err := mgr.ExecuteTool(context.Background(), "dev_bash", nil)
	if err != nil {
		t.Fatalf("ExecuteTool(dev_bash): %v", err)
	}
	if got != "REAL-nanite-dev_bash" {
		t.Errorf("ExecuteTool(dev_bash) = %q, want REAL-nanite-dev_bash (routed to the proxy instead of the builtin)", got)
	}
}

// TestManager_UniformIndex_RenameSurvivesSliceGrowth is the CW-20260817
// regression for the listing/broker-registration side of a collision
// rename (as opposed to the execution-routing side covered by
// TestManager_UniformIndex_NonSelfBuiltinReservedNamespace above). m.tools
// used to be a []toolEntry VALUE slice: assignUniformNameLocked's
// collision path mutates an incumbent entry in place through the
// *toolEntry pointer stashed in uniformIndex at insertion time
// (`existing.uniformName = incumbentDisambig`). If a later DiscoverTools
// append triggers that slice to reallocate its backing array — which,
// with hundreds of tools, is close to guaranteed for any entry touched
// early in the loop — the mutation lands on the orphaned pre-growth
// array, not the one m.tools now points to, so GetAllToolsUnfiltered
// (and the broker registration loop, which iterates the same slice)
// would report a duplicate, un-renamed "dev_bash" name instead of the
// correct disambiguated "collider_dev_bash". This registers dozens of
// filler tools after the colliding pair specifically to force at least
// one reallocation past the collision point, then asserts the listing
// view agrees with ToolAttribution/ExecuteTool.
func TestManager_UniformIndex_RenameSurvivesSliceGrowth(t *testing.T) {
	mgr := NewManager()
	if err := mgr.AddServer("collider", &fakeTieredTransport{
		tools: []Tool{{Name: "dev_bash"}},
	}, TierPluginStdio); err != nil {
		t.Fatalf("AddServer collider: %v", err)
	}
	// AddBuiltinServer — plain AddServer(..., TierBuiltin) would no longer
	// grant "dev" first-party protection (see
	// TestManager_UniformIndex_TierAloneDoesNotGrantFirstPartyStatus).
	if err := mgr.AddBuiltinServer(DevServerName, &fakeTieredTransport{
		tools: []Tool{{Name: "dev_bash"}},
	}); err != nil {
		t.Fatalf("AddBuiltinServer dev: %v", err)
	}
	// "filler" sorts after "dev" and "collider" — its many tools are
	// appended AFTER the colliding pair is resolved, forcing m.tools past
	// several capacity-doubling reallocations.
	fillerTools := make([]Tool, 0, 200)
	for i := 0; i < 200; i++ {
		fillerTools = append(fillerTools, Tool{Name: fmt.Sprintf("filler_tool_%03d", i)})
	}
	if err := mgr.AddServer("filler", &fakeTieredTransport{tools: fillerTools}, TierPluginStdio); err != nil {
		t.Fatalf("AddServer filler: %v", err)
	}
	if err := mgr.DiscoverTools(context.Background()); err != nil {
		t.Fatalf("DiscoverTools: %v", err)
	}

	names := make(map[string]int, 210)
	for _, def := range mgr.GetAllToolsUnfiltered() {
		names[def.Name]++
	}

	if n := names["dev_bash"]; n != 1 {
		t.Errorf(`listing shows %d entries named "dev_bash", want exactly 1 (a stale un-renamed duplicate means the collision rename was lost on slice growth)`, n)
	}
	if n := names["collider_dev_bash"]; n != 1 {
		t.Errorf(`listing shows %d entries named "collider_dev_bash", want exactly 1 (the disambiguated rename never reached the live listing view)`, n)
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

// TestManager_AddBuiltinServer covers the registration path itself: it
// must behave exactly like AddServer(name, transport, TierBuiltin) (same
// tier, same duplicate-name error propagation) while ALSO marking the
// server first-party. The first-party effect is exercised end-to-end by
// TestManager_UniformIndex_FifthBuiltinServerResistsEviction below; this
// test just covers the plumbing (tier + error propagation) directly.
func TestManager_AddBuiltinServer(t *testing.T) {
	mgr := NewManager()
	ft := &fakeTieredTransport{}
	if err := mgr.AddBuiltinServer("audio", ft); err != nil {
		t.Fatalf("AddBuiltinServer: %v", err)
	}

	var got *ServerInfo
	for _, info := range mgr.ListServers() {
		if info.Name == "audio" {
			infoCopy := info
			got = &infoCopy
		}
	}
	if got == nil {
		t.Fatal("audio server not found in ListServers after AddBuiltinServer")
	}
	if got.TrustTier != string(TierBuiltin) {
		t.Errorf("TrustTier = %q, want %q", got.TrustTier, TierBuiltin)
	}

	// Duplicate registration propagates AddServer's "already registered"
	// error — AddBuiltinServer must not swallow it or double-register.
	if err := mgr.AddBuiltinServer("audio", ft); err == nil {
		t.Error("expected error registering duplicate builtin server, got nil")
	}
}

// TestManager_UniformIndex_FifthBuiltinServerResistsEviction is the
// hardening's core regression test (07-harden-builtin-server-check):
// it proves the reserved-namespace defense covers a FIFTH first-party
// builtin — "widget", never part of the old hardcoded
// self/dev/code/general switch — registered through AddBuiltinServer,
// not by adding a new name to any list in naming.go. Same collision
// shape as TestManager_UniformIndex_NonSelfBuiltinReservedNamespace
// ("Agent Mux" sorts alphabetically before the builtin and grabs the bare
// slot first), but for a server that was never hardcoded anywhere, which
// is exactly what "adding a fifth first-party builtin requires touching
// exactly one call site" needs to demonstrate.
func TestManager_UniformIndex_FifthBuiltinServerResistsEviction(t *testing.T) {
	mgr := NewManager()
	// "AAA Proxy" sorts before "widget" (capital 'A' < lowercase 'w' in
	// ASCII), so DiscoverTools processes it first.
	proxy := &fakeTieredTransport{
		tools:      []Tool{{Name: "widget_render"}},
		resultText: "PROXY-fake-widget_render",
	}
	if err := mgr.AddServer("AAA Proxy", proxy, TierPluginStdio); err != nil {
		t.Fatalf("AddServer AAA Proxy: %v", err)
	}
	real := &fakeTieredTransport{
		tools:      []Tool{{Name: "widget_render"}},
		resultText: "REAL-nanite-widget_render",
	}
	// "widget" was never one of the original four hardcoded names — this
	// is the whole point of the test. Registered via AddBuiltinServer,
	// the same way a real fifth builtin would be added to main.go.
	if err := mgr.AddBuiltinServer("widget", real); err != nil {
		t.Fatalf("AddBuiltinServer widget: %v", err)
	}
	if err := mgr.DiscoverTools(context.Background()); err != nil {
		t.Fatalf("DiscoverTools: %v", err)
	}

	// The bare slot must resolve to the fifth builtin, not the proxy that
	// registered (and sorted) first.
	if srv, _, ok := mgr.ToolAttribution("widget_render"); !ok || srv != "widget" {
		t.Errorf("bare 'widget_render' attribution = (%q, ok=%v), want (widget, true)", srv, ok)
	}
	// The proxy's colliding copy must be force-prefixed, not dropped.
	if srv, orig, ok := mgr.ToolAttribution("AAA Proxy_widget_render"); !ok || srv != "AAA Proxy" || orig != "widget_render" {
		t.Errorf("'AAA Proxy_widget_render' attribution = (%q, %q, ok=%v), want (AAA Proxy, widget_render, true)", srv, orig, ok)
	}

	// Execution must route to the fifth builtin, not the proxy.
	got, err := mgr.ExecuteTool(context.Background(), "widget_render", nil)
	if err != nil {
		t.Fatalf("ExecuteTool(widget_render): %v", err)
	}
	if got != "REAL-nanite-widget_render" {
		t.Errorf("ExecuteTool(widget_render) = %q, want REAL-nanite-widget_render (routed to the proxy instead of the fifth builtin)", got)
	}
}

// TestManager_UniformIndex_TierAloneDoesNotGrantFirstPartyStatus locks in
// the doc-comment constraint on isFirstPartyBuiltinServerLocked: a
// test-only (or otherwise arbitrary/hostile) server registered at
// TierBuiltin through the ORDINARY AddServer path — not AddBuiltinServer
// — must NOT be treated as first-party. If tier alone conferred
// first-party protection, "zzz-fixture" below would force-evict
// "aaa-fixture" from the bare uniform-name slot outright, the same way a
// real builtin evicts a third-party proxy. Instead, since neither went
// through AddBuiltinServer, ordinary collision-disambiguation must apply
// to both — proving tier isn't the signal the defense uses.
func TestManager_UniformIndex_TierAloneDoesNotGrantFirstPartyStatus(t *testing.T) {
	mgr := NewManager()
	if err := mgr.AddServer("aaa-fixture", &fakeTieredTransport{
		tools: []Tool{{Name: "widget_render"}},
	}, TierBuiltin); err != nil {
		t.Fatalf("AddServer aaa-fixture: %v", err)
	}
	if err := mgr.AddServer("zzz-fixture", &fakeTieredTransport{
		tools: []Tool{{Name: "widget_render"}},
	}, TierBuiltin); err != nil {
		t.Fatalf("AddServer zzz-fixture: %v", err)
	}
	if err := mgr.DiscoverTools(context.Background()); err != nil {
		t.Fatalf("DiscoverTools: %v", err)
	}

	// Neither server went through AddBuiltinServer, so the bare slot must
	// NOT survive for either — plain collision-disambiguation renames
	// BOTH incumbents, same as any two unrelated third-party servers.
	if srv, _, ok := mgr.ToolAttribution("widget_render"); ok {
		t.Errorf("bare 'widget_render' still resolves to server %q — TierBuiltin alone granted first-party protection", srv)
	}
	if srv, _, ok := mgr.ToolAttribution("aaa-fixture_widget_render"); !ok || srv != "aaa-fixture" {
		t.Errorf("aaa-fixture_widget_render attribution = (%q, ok=%v), want (aaa-fixture, true)", srv, ok)
	}
	if srv, _, ok := mgr.ToolAttribution("zzz-fixture_widget_render"); !ok || srv != "zzz-fixture" {
		t.Errorf("zzz-fixture_widget_render attribution = (%q, ok=%v), want (zzz-fixture, true)", srv, ok)
	}
}
