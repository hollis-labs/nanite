package plugin

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// TestPhase5AgentProfiles_EndToEnd is Phase 5 item 03's Done-means
// verification (TASKS/phase-5/03-wire-registers-agent-profiles.md): a real
// test plugin declaring registers.agent_profiles[] in the new role/agent
// composition shape, run through the exact same pipeline a genuine on-disk
// plugin uses -- Host.LoadPlugin followed by applyManifestRegistrations
// (mirroring loader.go's LoadDiscovered, not a shortcut around it) -- then
// torn down via the real Host.UnloadPlugin.
//
// No real, currently-installed plugin declares registers.agent_profiles[]
// today: the giphy reference plugin this shape's manifest-level {id, file}
// fields were originally validated against (config_manifest_v1_test.go) was
// cut in full (TASKS/phase-0/15a-cut-giphy.md) before this task landed, and
// no other plugin.yaml in this workspace declares the section (confirmed by
// grep across internal/plugin/builtin/*/plugin.yaml). This test is
// therefore the "test plugin otherwise" branch of this task's Done-means,
// not a migration of a real fixture that never existed on disk.
//
// "Dispatchable" is verified at the composition layer this task owns: the
// constructed agent_profiles row resolves to a real role with a non-empty
// system_prompt (ready for internal/service.ResolveAgentCascade -- the
// actual cascade merge is covered separately by role_cascade_test.go, which
// internal/plugin cannot import without a cycle), and carries real
// agent_tools/agent_known_skills grants of the exact shape
// internal/service/tool.go's filterToolsByAgentTools reads at dispatch
// time. Exercising an actual LLM turn is out of this backend-only task's
// scope.
func TestPhase5AgentProfiles_EndToEnd(t *testing.T) {
	ctx := context.Background()

	dbPath := filepath.Join(t.TempDir(), "agent-profiles-e2e.db")
	st, err := store.New(ctx, dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close(context.

			// A real skill row for agent.skills to grant against.
			Background())
	})

	skill := &store.Skill{Name: "Wiki Classify", Slug: "wiki-classify", Description: "test skill"}
	if err := st.CreateSkill(context.Background(), skill); err != nil {
		t.Fatalf("CreateSkill: %v", err)
	}

	// A real on-disk plugin dir + agent-profile file (new role/agent shape)
	// -- resolvePluginAssetPath requires a real pluginDir, same as a
	// genuinely-installed plugin under pluginsDir.
	pluginDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(pluginDir, "agents"), 0o755); err != nil {
		t.Fatalf("mkdir agents dir: %v", err)
	}
	profileYAML := `role:
  slug: demo-wiki-curator
  name: Demo Wiki Curator
  system_prompt: You are the Demo Wiki Curator. Classify and compile fragments into wiki pages.
  class: process
agent:
  slug: demo-curator
  name: Demo Curator
  description: Phase 5 item 03 end-to-end test agent.
  activation_mode: fresh-per-wake
  runtime_kind: api
  tools:
    - tool_list
  skills:
    - wiki-classify
`
	if err := os.WriteFile(filepath.Join(pluginDir, "agents", "curator.yaml"), []byte(profileYAML), 0o644); err != nil {
		t.Fatalf("write profile file: %v", err)
	}

	const pluginID = "demo-agent-profiles"
	manifest := &PluginManifest{
		SchemaVersion: 1,
		Name:          pluginID,
		ID:            pluginID,
		Version:       "0.1.0",
		Runtime:       "builtin",
		Registers: ManifestRegisters{
			AgentProfiles: []AgentProfileRegistration{
				{ID: "curator", File: "agents/curator.yaml"},
			},
		},
	}

	host := NewHost(http.NewServeMux(), NewLogger("agent-profiles-e2e"))
	host.SetStore(st)

	p := &fakePlugin{id: pluginID}
	if err := host.LoadPlugin(p); err != nil {
		t.Fatalf("LoadPlugin: %v", err)
	}
	if err := applyManifestRegistrations(host, manifest, p, pluginDir); err != nil {
		t.Fatalf("applyManifestRegistrations: %v", err)
	}

	// --- install: agent appears and is dispatchable ---

	agent, err := st.GetAgentBySlug(context.Background(), "demo-curator")
	if err != nil {
		t.Fatalf("GetAgentBySlug: %v", err)
	}
	if agent.PluginID != pluginID {
		t.Errorf("agent.PluginID = %q, want %q", agent.PluginID, pluginID)
	}
	if agent.RoleID == "" {
		t.Fatalf("agent.RoleID not set")
	}
	if agent.ConsumerID == "" {
		t.Fatalf("agent.ConsumerID not set")
	}
	if agent.Status != "active" {
		t.Errorf("agent.Status = %q, want active", agent.Status)
	}
	if agent.ActivationMode != "fresh-per-wake" {
		t.Errorf("agent.ActivationMode = %q, want fresh-per-wake", agent.ActivationMode)
	}

	role, err := st.GetRole(context.Background(), agent.RoleID)
	if err != nil {
		t.Fatalf("GetRole: %v", err)
	}
	if role == nil {
		t.Fatalf("GetRole returned nil for agent.RoleID %q", agent.RoleID)
	}
	if role.SystemPrompt == "" {
		t.Errorf("role.SystemPrompt is empty -- the constructed agent would boot with no persona")
	}
	if agent.SystemPrompt != "" {
		// Confirms the composition defers to the role via the cascade
		// (internal/service.ResolveAgentCascade / applyScalarCascade)
		// rather than duplicating the prompt onto the agent row.
		t.Errorf("agent.SystemPrompt = %q, want empty (deferred to role via cascade)", agent.SystemPrompt)
	}

	consumer, err := st.GetConsumer(context.Background(), agent.ConsumerID)
	if err != nil {
		t.Fatalf("GetConsumer: %v", err)
	}
	if consumer == nil {
		t.Fatalf("GetConsumer returned nil for agent.ConsumerID %q", agent.ConsumerID)
	}
	if consumer.Slug != pluginID {
		t.Errorf("consumer.Slug = %q, want %q (default consumer_slug = plugin id)", consumer.Slug, pluginID)
	}

	grantedTools, err := st.ListAgentToolNames(ctx, agent.ID)
	if err != nil {
		t.Fatalf("ListAgentToolNames: %v", err)
	}
	if !containsString(grantedTools, "tool_list") {
		t.Errorf("granted tools = %v, want tool_list among them", grantedTools)
	}

	grantedSkills, err := st.ListAgentSkills(context.Background(), agent.ID)
	if err != nil {
		t.Fatalf("ListAgentSkills: %v", err)
	}
	foundSkill := false
	for _, sk := range grantedSkills {
		if sk.Slug == "wiki-classify" {
			foundSkill = true
		}
	}
	if !foundSkill {
		t.Errorf("agent skills = %+v, want wiki-classify among them", grantedSkills)
	}

	// --- reload is an upsert, not a duplicate-create (this task's
	// "construct/upsert" instruction) ---

	if err := applyManifestRegistrations(host, manifest, p, pluginDir); err != nil {
		t.Fatalf("applyManifestRegistrations (second load): %v", err)
	}
	agentsAfterReload, err := st.ListAgentsByPluginID(context.Background(), pluginID)
	if err != nil {
		t.Fatalf("ListAgentsByPluginID: %v", err)
	}
	if len(agentsAfterReload) != 1 {
		t.Fatalf("expected exactly 1 plugin-owned agent after reload, got %d: %+v", len(agentsAfterReload), agentsAfterReload)
	}
	if agentsAfterReload[0].ID != agent.ID {
		t.Errorf("reload minted a new agent row (id %q) instead of upserting the existing one (id %q)",
			agentsAfterReload[0].ID, agent.ID)
	}
	if agentsAfterReload[0].Status != "active" {
		// Regression guard: UpdateAgent (unlike CreateAgent) does not
		// default an empty Status, so a reload that forgets to set it on
		// the re-synced row would silently blank agent_profiles.status on
		// the second load, breaking status-based filtering (e.g.
		// ListAgentsFilter) without ever surfacing an error.
		t.Errorf("agent.Status after reload = %q, want active", agentsAfterReload[0].Status)
	}

	// --- uninstall: agent is gone ---

	if err := host.UnloadPlugin(pluginID); err != nil {
		t.Fatalf("UnloadPlugin: %v", err)
	}
	if _, err := st.GetAgentBySlug(context.Background(), "demo-curator"); err == nil {
		t.Errorf("agent %q still resolvable by slug after unload", "demo-curator")
	}
	if remainingAgents, err := st.ListAgentsByPluginID(context.Background(), pluginID); err != nil {
		t.Fatalf("ListAgentsByPluginID after unload: %v", err)
	} else if len(remainingAgents) != 0 {
		t.Errorf("plugin-owned agents survived unload: %+v", remainingAgents)
	}
	if remainingRoles, err := st.ListRolesByPluginID(context.Background(), pluginID); err != nil {
		t.Fatalf("ListRolesByPluginID after unload: %v", err)
	} else if len(remainingRoles) != 0 {
		t.Errorf("plugin-owned roles survived unload: %+v", remainingRoles)
	}
	// Consumer rows are deliberately NOT swept (resolveOrCreateConsumer's
	// doc comment) -- assert it survives, distinguishing "not swept" from
	// "sweep silently failed."
	if c, err := st.GetConsumerBySlug(context.Background(), pluginID); err != nil {
		t.Fatalf("GetConsumerBySlug after unload: %v", err)
	} else if c == nil {
		t.Errorf("consumer %q was unexpectedly swept on unload", pluginID)
	}
}

// TestPhase5AgentProfiles_NoLegacyGrandfathering verifies that a plugin
// agent-profile file NOT written in the new role/agent composition shape
// (e.g. the old flat shape's top-level fields) is rejected with a clear
// error at load time, per this task's explicit "no legacy grandfathering"
// instruction -- not silently coerced into a partial/nonsensical
// composition.
func TestPhase5AgentProfiles_NoLegacyGrandfathering(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "agent-profiles-legacy.db")
	st, err := store.New(ctx, dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })

	pluginDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(pluginDir, "agents"), 0o755); err != nil {
		t.Fatalf("mkdir agents dir: %v", err)
	}
	// The old flat shape: persona/tools/mcp_servers at the top level, no
	// role:/agent: split.
	legacyYAML := `id: giphy-agent
name: Giphy Agent
persona: A gif-searching assistant.
tools:
  - giphy_search
mcp_servers:
  - giphy
`
	if err := os.WriteFile(filepath.Join(pluginDir, "agents", "legacy.yaml"), []byte(legacyYAML), 0o644); err != nil {
		t.Fatalf("write legacy profile file: %v", err)
	}

	manifest := &PluginManifest{
		Name: "legacy-shape-plugin",
		ID:   "legacy-shape-plugin",
		Registers: ManifestRegisters{
			AgentProfiles: []AgentProfileRegistration{
				{ID: "legacy-agent", File: "agents/legacy.yaml"},
			},
		},
	}

	host := NewHost(http.NewServeMux(), NewLogger("agent-profiles-legacy"))
	host.SetStore(st)
	p := &fakePlugin{id: manifest.ID}

	err = applyManifestRegistrations(host, manifest, p, pluginDir)
	if err == nil {
		t.Fatal("applyManifestRegistrations: expected a rejection for the old flat agent-profile shape, got nil error")
	}

	// No partial composition should have been constructed.
	if agents, listErr := st.ListAgentsByPluginID(context.Background(), manifest.ID); listErr != nil {
		t.Fatalf("ListAgentsByPluginID: %v", listErr)
	} else if len(agents) != 0 {
		t.Errorf("a rejected agent-profile file still produced agent rows: %+v", agents)
	}
}

func containsString(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
