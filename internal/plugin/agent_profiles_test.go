package plugin

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

// A real loaded plugin cannot reintroduce mutable profiles or authority.
func TestPhase5AgentProfiles_ImmutableRefusalHasNoInstallEffects(t *testing.T) {
	ctx := context.Background()

	dbPath := filepath.Join(t.TempDir(), "agent-profiles-e2e.db")
	st, err := storetest.New(t, ctx, dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close(context.Background())
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

	// A plugin declaration is not authored immutable content or actor enrollment.
	tables := []string{"agent_profiles", "roles", "consumers", "agent_tools", "agent_known_skills", "agent_actor_bindings", "agent_host_settings"}
	before := make([]int, len(tables))
	for i, table := range tables {
		if queryErr := st.DB.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&before[i]); queryErr != nil {
			t.Fatal(queryErr)
		}
	}
	for range 2 {
		if registrationErr := applyManifestRegistrations(host, manifest, p, pluginDir); !errors.Is(registrationErr, store.ErrImmutableAgentProfile) {
			t.Fatalf("registration error=%v", registrationErr)
		}
		for i, table := range tables {
			var after int
			if queryErr := st.DB.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&after); queryErr != nil {
				t.Fatal(queryErr)
			}
			if after != before[i] {
				t.Fatalf("registration changed %s %d/%d", table, after, before[i])
			}
		}
	}
	if unloadErr := host.UnloadPlugin(pluginID); unloadErr != nil {
		t.Fatal(unloadErr)
	}
	sourceRoot, rootErr := os.OpenRoot(pluginDir)
	if rootErr != nil {
		t.Fatal(rootErr)
	}
	defer sourceRoot.Close()
	data, readErr := sourceRoot.ReadFile("agents/curator.yaml")
	if readErr != nil || string(data) != profileYAML {
		t.Fatalf("source changed %v", readErr)
	}
}

func TestPluginAgentProfilesSkipRetiredSlug(t *testing.T) {
	ctx := context.Background()
	st, err := storetest.New(t, ctx, filepath.Join(t.TempDir(), "retired-plugin-agent.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })
	if _, err := st.DB.ExecContext(ctx, `INSERT INTO retired_agent_profiles
		(id, slug, name, source, plugin_id, class, export_id, digest, actor, reason)
		VALUES ('retired-id', 'retired-plugin-agent', 'Retired', 'plugin', 'demo-retired-plugin', 'process', 'export-id', 'sha256:test', 'test', 'retired')`); err != nil {
		t.Fatalf("insert retired tombstone: %v", err)
	}

	pluginDir := t.TempDir()
	if mkdirErr := os.MkdirAll(filepath.Join(pluginDir, "agents"), 0o750); mkdirErr != nil {
		t.Fatalf("mkdir agents dir: %v", mkdirErr)
	}
	profileYAML := `role:
  slug: retired-plugin-role
  name: Retired Plugin Role
  system_prompt: should not be recreated
agent:
  slug: retired-plugin-agent
  name: Retired Plugin Agent
`
	if err := os.WriteFile(filepath.Join(pluginDir, "agents", "retired.yaml"), []byte(profileYAML), 0o600); err != nil {
		t.Fatalf("write profile file: %v", err)
	}

	const pluginID = "demo-retired-plugin"
	manifest := &PluginManifest{
		SchemaVersion: 1,
		Name:          pluginID,
		ID:            pluginID,
		Version:       "0.1.0",
		Runtime:       "builtin",
		Registers: ManifestRegisters{
			AgentProfiles: []AgentProfileRegistration{{ID: "retired", File: "agents/retired.yaml"}},
		},
	}
	host := NewHost(http.NewServeMux(), NewLogger("retired-agent-profiles"))
	host.SetStore(st)
	if err := applyManifestRegistrations(host, manifest, &fakePlugin{id: pluginID}, pluginDir); err != nil {
		t.Fatalf("applyManifestRegistrations: %v", err)
	}
	if _, err := st.GetAgentBySlug(ctx, "retired-plugin-agent"); err == nil {
		t.Fatal("plugin registration resurrected retired agent")
	}
	if role, err := st.GetRoleBySlug(ctx, "retired-plugin-role"); err != nil || role != nil {
		t.Fatalf("plugin registration created role for suppressed agent: role=%+v err=%v", role, err)
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
	st, err := storetest.New(t, ctx, dbPath)
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
