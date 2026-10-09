package plugin

import (
	"database/sql"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

func TestProfileRetirementProtectedPluginReloadCannotResurrect(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "retired-plugin.db")
	st, err := storetest.New(t, ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(ctx) })
	keep := &store.AgentProfile{Name: "Keep exact identity", Slug: "general-chat", Source: "user", SystemPrompt: "fixture chat"}
	if checkErr := st.CreateAgent(ctx, keep); checkErr != nil {
		t.Fatal(checkErr)
	}
	pluginDir := t.TempDir()
	file := filepath.Join(pluginDir, "profile.yaml")
	profile := `role:
  slug: retired-plugin-role
  name: Retired plugin role
  system_prompt: Plugin fixture
agent:
  slug: retired-plugin-agent
  name: Retired plugin agent
`
	if checkErr := os.WriteFile(file, []byte(profile), 0600); checkErr != nil {
		t.Fatal(checkErr)
	}
	const pluginID = "retirement-fixture"
	manifest := &PluginManifest{SchemaVersion: 1, ID: pluginID, Name: pluginID, Version: "0.1.0", Runtime: "builtin", Registers: ManifestRegisters{AgentProfiles: []AgentProfileRegistration{{ID: "retired", File: "profile.yaml"}}}}
	host := NewHost(http.NewServeMux(), NewLogger(pluginID))
	host.SetStore(st)
	p := &fakePlugin{id: pluginID}
	if checkErr := host.LoadPlugin(p); checkErr != nil {
		t.Fatal(checkErr)
	}
	if checkErr := applyManifestRegistrations(host, manifest, p, pluginDir); checkErr != nil {
		t.Fatal(checkErr)
	}
	agent, err := st.GetAgentBySlug(ctx, "retired-plugin-agent")
	if err != nil {
		t.Fatal(err)
	}
	authority := store.ProtectedRetirementAuthority{Actor: "fixture-owner", Reason: "Retire plugin class", KeepProfileID: keep.ID, KeepRevision: keep.Revision}
	snapshot, err := st.ExportProtectedProfileRetirement(ctx, agent.ID, authority)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := snapshot.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if checkErr := st.RetireExportedProtectedProfile(ctx, agent.ID, "fixture-export", digest, authority); checkErr != nil {
		t.Fatal(checkErr)
	}
	if checkErr := host.UnloadPlugin(pluginID); checkErr != nil {
		t.Fatal(checkErr)
	}
	if checkErr := st.Close(ctx); checkErr != nil {
		t.Fatal(checkErr)
	}
	st, err = store.New(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	// The reloaded declaration tries new secondary rows as well as the old slug.
	profile = `role:
  slug: suppressed-new-role
  name: Suppressed role
  system_prompt: Must never be admitted
agent:
  slug: retired-plugin-agent
  name: Changed plugin agent
  consumer_slug: suppressed-new-consumer
`
	if checkErr := os.WriteFile(file, []byte(profile), 0600); checkErr != nil {
		t.Fatal(checkErr)
	}
	host = NewHost(http.NewServeMux(), NewLogger(pluginID))
	host.SetStore(st)
	p = &fakePlugin{id: pluginID}
	if checkErr := host.LoadPlugin(p); checkErr != nil {
		t.Fatal(checkErr)
	}
	if checkErr := applyManifestRegistrations(host, manifest, p, pluginDir); checkErr != nil {
		t.Fatal(checkErr)
	}
	if _, checkErr := st.GetAgentBySlug(ctx, agent.Slug); !errors.Is(checkErr, sql.ErrNoRows) {
		t.Fatal("plugin reload resurrected profile", checkErr)
	}
	role, err := st.GetRoleBySlug(ctx, "suppressed-new-role")
	if err != nil || role != nil {
		t.Fatal("suppression allowed secondary role effects", err)
	}
	consumer, err := st.GetConsumerBySlug(ctx, "suppressed-new-consumer")
	if err != nil || consumer != nil {
		t.Fatal("suppression allowed consumer effects", err)
	}
	retained, err := st.GetAgent(ctx, keep.ID)
	if err != nil || retained.Revision != keep.Revision {
		t.Fatal("reload changed retained logical identity", err)
	}
}
