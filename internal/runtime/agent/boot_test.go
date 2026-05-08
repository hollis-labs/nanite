package agent

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	agentsessions "github.com/hollis-labs/go-agent-sessions/agentsessions"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/permission"
	"github.com/hollis-labs/nanite/internal/store"
)

// makeBootDeps wires a Dependencies struct with fakes plus a real
// *agentsessions.Manager. Provider defaults to "codex" so shouldUsePTY=false
// → adapterRuntime, which doesn't spawn anything when AutoFireFirstTurn is
// false (ModeLongLived) or when our fake adapter's BuildArgs hands runner
// nothing to do.
func makeBootDeps(t *testing.T, profileProvider string) (*Dependencies, *fakeRuntimeStore) {
	t.Helper()
	if profileProvider == "" {
		profileProvider = "codex"
	}
	store := newFakeRuntimeStore()
	pg := permission.NewPathGrants()
	profile := storeProfile(profileProvider)
	deps := &Dependencies{
		Agents:          &fakeAgentProfiles{profile: &profile},
		SessionsManager: agentsessions.NewManager(nil),
		Store:           store,
		PathGrants:      pg,
		ProviderAdapter: func(name string) provider.CLIAdapter {
			return &fakeAdapter{name: name}
		},
		MCPConfig:      MCPConfig{}, // empty disables .mcp.json planting
		WorkspacesRoot: t.TempDir(),
	}
	t.Cleanup(func() { _ = deps.SessionsManager.Shutdown(context.Background()) })
	return deps, store
}

// storeProfile is a small helper to avoid repeating profile literals.
func storeProfile(provider string) store.AgentProfile {
	return store.AgentProfile{
		ID:              "profile-1",
		Name:            "Test",
		Slug:            "test",
		Description:     "test",
		DefaultProvider: provider,
	}
}

// TestBoot_LongLived_HappyPath confirms a ModeLongLived spawn (without
// AutoFireFirstTurn) succeeds end-to-end against a real manager + fake
// adapter, persists the runtime row, and returns a Session bound to the
// composition root.
func TestBoot_LongLived_HappyPath(t *testing.T) {
	deps, store := makeBootDeps(t, "codex")

	sess, err := Boot(context.Background(), deps, Options{
		Mode:    ModeLongLived,
		Workdir: t.TempDir(),
		Role:    "executor",
	})
	if err != nil {
		t.Fatalf("Boot: %v", err)
	}
	if sess == nil {
		t.Fatal("Boot returned nil session")
	}
	t.Cleanup(func() { _ = sess.Stop(context.Background()) })

	if sess.ID == "" {
		t.Errorf("session ID is empty")
	}
	if sess.Mode != ModeLongLived {
		t.Errorf("session.Mode = %v, want ModeLongLived", sess.Mode)
	}
	if sess.Provider != "codex" {
		t.Errorf("session.Provider = %q, want codex", sess.Provider)
	}
	if sess.BootDir == "" {
		t.Errorf("session.BootDir is empty")
	}
	if _, err := os.Stat(sess.BootDir); err != nil {
		t.Errorf("boot dir missing: %v", err)
	}
	if sess.WorkspaceDir == "" {
		t.Errorf("session.WorkspaceDir is empty")
	}
	if _, err := os.Stat(sess.WorkspaceDir); err != nil {
		t.Errorf("workspace dir missing: %v", err)
	}

	if len(store.created) != 1 {
		t.Fatalf("CreateRuntimeRow calls = %d, want 1", len(store.created))
	}
	row := store.created[0]
	if row.Mode != "long_lived" {
		t.Errorf("row.Mode = %q, want long_lived", row.Mode)
	}
	if row.State != "launching" {
		t.Errorf("row.State = %q, want launching", row.State)
	}
	if row.Provider != "codex" {
		t.Errorf("row.Provider = %q, want codex", row.Provider)
	}
}

// TestBoot_Subagent_RegistersLineage exercises path-grant lineage
// registration for ModeSubagent.
func TestBoot_Subagent_RegistersLineage(t *testing.T) {
	deps, _ := makeBootDeps(t, "codex")

	parent := "parent-sess-1"
	sess, err := Boot(context.Background(), deps, Options{
		Mode:            ModeSubagent,
		ParentSessionID: parent,
		Workdir:         t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Boot: %v", err)
	}
	t.Cleanup(func() { _ = sess.Stop(context.Background()) })

	if !sess.hadLineage {
		t.Errorf("session.hadLineage = false, want true after ModeSubagent boot")
	}
	// Lineage map isn't read-exposed by PathGrants today; the hadLineage
	// flag + Stop's ClearLineage is the agent-package-visible contract.
}

// TestBoot_Subagent_RequiresParent guards against silent breakage of the
// validation contract.
func TestBoot_Subagent_RequiresParent(t *testing.T) {
	deps, _ := makeBootDeps(t, "codex")

	_, err := Boot(context.Background(), deps, Options{Mode: ModeSubagent})
	if err == nil || !strings.Contains(err.Error(), "ParentSessionID") {
		t.Fatalf("expected ParentSessionID error, got %v", err)
	}
}

// TestBoot_Resume_RequiresCheckpoint exercises the validation guard.
func TestBoot_Resume_RequiresCheckpoint(t *testing.T) {
	deps, _ := makeBootDeps(t, "codex")
	_, err := Boot(context.Background(), deps, Options{Mode: ModeResume})
	if err == nil || !strings.Contains(err.Error(), "ResumeFromCheckpoint") {
		t.Fatalf("expected ResumeFromCheckpoint error, got %v", err)
	}
}

// TestBoot_Resume_LoadsCheckpointPreset verifies ModeResume threads the
// stored provider session id through to StartOptions via the
// SessionIDPreset path.
func TestBoot_Resume_LoadsCheckpointPreset(t *testing.T) {
	deps, store := makeBootDeps(t, "codex")
	store.checkpoint = &RuntimeCheckpoint{
		ID:                "cp-1",
		ProviderSessionID: "claude-sess-original",
	}

	sess, err := Boot(context.Background(), deps, Options{
		Mode:                 ModeResume,
		ResumeFromCheckpoint: "cp-1",
		Workdir:              t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Boot: %v", err)
	}
	t.Cleanup(func() { _ = sess.Stop(context.Background()) })

	// We can't introspect StartOptions directly through the Manager; the
	// row presence + lack of error confirms the preset path executed
	// without falling into the "no checkpoint" failure branch.
	if len(store.created) != 1 {
		t.Errorf("CreateRuntimeRow calls = %d, want 1", len(store.created))
	}
}

// TestBoot_MarksRuntimeFailed_OnCheckpointError ensures the cleanup path
// flips state="failed" + clears the boot dir when checkpoint loading
// fails after the runtime row was persisted.
func TestBoot_MarksRuntimeFailed_OnCheckpointError(t *testing.T) {
	deps, store := makeBootDeps(t, "codex")
	store.checkpoint = nil // forces GetCheckpoint to error

	_, err := Boot(context.Background(), deps, Options{
		Mode:                 ModeResume,
		ResumeFromCheckpoint: "cp-missing",
		Workdir:              t.TempDir(),
	})
	if err == nil {
		t.Fatalf("expected checkpoint error, got nil")
	}
	if !strings.Contains(err.Error(), "checkpoint") {
		t.Errorf("error should mention checkpoint, got %v", err)
	}
	if len(store.created) != 1 {
		t.Fatalf("expected runtime row created before failure, got %d", len(store.created))
	}
	failedID := store.created[0].ID
	if reason, ok := store.failed[failedID]; !ok {
		t.Errorf("MarkRuntimeFailed not called for %q (%v)", failedID, store.failed)
	} else if reason == "" {
		t.Errorf("failure reason is empty")
	}
}

// TestBoot_MissingProviderAdapter returns a clear error.
func TestBoot_MissingProviderAdapter(t *testing.T) {
	deps, _ := makeBootDeps(t, "codex")
	deps.ProviderAdapter = func(string) provider.CLIAdapter { return nil }

	_, err := Boot(context.Background(), deps, Options{
		Mode:    ModeLongLived,
		Workdir: t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "no adapter") {
		t.Fatalf("expected no-adapter error, got %v", err)
	}
}

// TestBoot_RequiresProfile guards the GetOrDefault contract.
func TestBoot_RequiresProfile(t *testing.T) {
	deps, _ := makeBootDeps(t, "codex")
	deps.Agents = &fakeAgentProfiles{err: errors.New("not found")}

	_, err := Boot(context.Background(), deps, Options{
		Mode:    ModeLongLived,
		Workdir: t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "resolve profile") {
		t.Fatalf("expected resolve-profile error, got %v", err)
	}
}
