package service

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/modelsdevtest"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

// TestNewContainer_WiresStoreCollaborators pins that every narrow store
// collaborator transports use is wired, and that the store-backed ones are
// the container's own store.
func TestNewContainer_WiresStoreCollaborators(t *testing.T) {
	root := t.TempDir()
	st, err := storetest.New(t, context.Background(), filepath.Join(root, "nanite.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })

	c, err := NewContainer(ContainerConfig{
		ModelCatalogOptions: modelsdevtest.Options(t),
		Store:               st, Providers: provider.NewRegistry(), WorkingDir: root,
		DisableEmbeddedTesseract: true,
	})
	if err != nil {
		t.Fatalf("NewContainer: %v", err)
	}
	t.Cleanup(c.Shutdown)

	for name, v := range map[string]any{
		"SkillIndex":          c.SkillIndex,
		"SkillUninstallIndex": c.SkillUninstallIndex,
		"SkillGrants":         c.SkillGrants,
		"SkillResolvers":      c.SkillResolvers,
		"AgentImportProfiles": c.AgentImportProfiles,
		"ProviderDefaults":    c.ProviderDefaults,
	} {
		if got, ok := v.(*store.Store); !ok || got != st {
			t.Errorf("%s = %T %v, want the container's store", name, v, v)
		}
	}
	if c.AgentImportSeeder == nil {
		t.Error("AgentImportSeeder is nil")
	}
	if c.CompactionEvents == nil {
		t.Error("CompactionEvents is nil")
	}

	// EnvelopeLookup reaches the store.
	ctx := context.Background()
	sess := &store.Session{Provider: "anthropic", Model: "m", Status: "active"}
	if err := st.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	inst := &store.EnvelopeInstance{SessionID: sess.ID, EnvelopeType: "test", EnvelopeJSON: "{}", EmittedAt: time.Now().UTC()}
	if err := st.CreateEnvelopeInstance(ctx, inst); err != nil {
		t.Fatalf("CreateEnvelopeInstance: %v", err)
	}
	got := c.Sessions.EnvelopeLookup(ctx, []store.Message{{Envelope: `{"id":"` + inst.ID + `"}`}, {Envelope: `{"id":"nope"}`}})
	if len(got) != 1 || got[inst.ID] == nil {
		t.Errorf("EnvelopeLookup = %v, want just %s", got, inst.ID)
	}
}
