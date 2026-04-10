package agent

import (
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// fakeAdapter is a minimal CLIAgentAdapter for testing the registry.
type fakeAdapter struct {
	name     string
	priority int
	synced   *bool
}

func (f *fakeAdapter) Name() string                            { return f.name }
func (f *fakeAdapter) Priority() int                           { return f.priority }
func (f *fakeAdapter) Discover(_ string) ([]Definition, error) { return nil, nil }
func (f *fakeAdapter) PopulateSandbox(_ string, _ store.AgentProfile, _ SandboxContext) error {
	return nil
}
func (f *fakeAdapter) SyncProjectRoot(_ string, _ []store.AgentProfile) error {
	if f.synced != nil {
		*f.synced = true
	}
	return nil
}

func TestSyncAllProjectRootsFiltered_OnlyAllowedRun(t *testing.T) {
	var claudeRan, codexRan, geminiRan, nativeRan bool
	reg := NewAdapterRegistry()
	reg.Register(&fakeAdapter{name: "claude", priority: 60, synced: &claudeRan})
	reg.Register(&fakeAdapter{name: "codex", priority: 70, synced: &codexRan})
	reg.Register(&fakeAdapter{name: "gemini", priority: 80, synced: &geminiRan})
	reg.Register(&fakeAdapter{name: "nanite-native", priority: 10, synced: &nativeRan})

	if err := reg.SyncAllProjectRootsFiltered("/tmp/x", nil, []string{"claude"}); err != nil {
		t.Fatalf("err: %v", err)
	}
	if !claudeRan {
		t.Error("claude should have run")
	}
	if codexRan {
		t.Error("codex should NOT have run")
	}
	if geminiRan {
		t.Error("gemini should NOT have run")
	}
	if !nativeRan {
		t.Error("nanite-native should always run")
	}
}

func TestSyncAllProjectRootsFiltered_EmptyAllowedListStillRunsNative(t *testing.T) {
	var claudeRan, nativeRan bool
	reg := NewAdapterRegistry()
	reg.Register(&fakeAdapter{name: "claude", priority: 60, synced: &claudeRan})
	reg.Register(&fakeAdapter{name: "nanite-native", priority: 10, synced: &nativeRan})

	if err := reg.SyncAllProjectRootsFiltered("/tmp/x", nil, []string{}); err != nil {
		t.Fatalf("err: %v", err)
	}
	if claudeRan {
		t.Error("claude should NOT have run with empty allowed list")
	}
	if !nativeRan {
		t.Error("nanite-native should always run, even with empty allowed list")
	}
}
