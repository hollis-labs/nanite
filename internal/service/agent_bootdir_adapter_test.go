package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/recovery/broker"
	runtimeagent "github.com/hollis-labs/nanite/internal/runtime/agent"
	"github.com/hollis-labs/nanite/internal/store"
)

// fakeAgentProfilesResolver is the test substitute for
// runtimeagent.AgentProfiles. Returns the configured profile or err.
type fakeAgentProfilesResolver struct {
	profile *store.AgentProfile
	err     error
}

func (f *fakeAgentProfilesResolver) GetOrDefault(string) (*store.AgentProfile, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.profile, nil
}

// newAdapterTestDeps returns a minimal *runtimeagent.Dependencies the
// bootdir adapter exercises. Only Agents + MCPConfig are consulted by
// ResolveBootdirParams + claudeLayout.Populate; the other fields stay
// zero-valued.
func newAdapterTestDeps(profile *store.AgentProfile) *runtimeagent.Dependencies {
	return &runtimeagent.Dependencies{
		Agents:    &fakeAgentProfilesResolver{profile: profile},
		MCPConfig: runtimeagent.MCPConfig{}, // empty DBPath disables .mcp.json
	}
}

// makeClaudeProfile returns a minimal claude profile with a non-empty
// SystemPrompt so the regenerated CLAUDE.md body is detectable in tests.
func makeClaudeProfile() *store.AgentProfile {
	return &store.AgentProfile{
		ID:              "agent-test-claude",
		Name:            "TestClaude",
		Slug:            "test-claude",
		Description:     "Adapter unit-test profile.",
		DefaultProvider: "claude",
		SystemPrompt:    "You are a test agent.",
	}
}

// Bound providers retain their existing artifacts until a supported fenced
// binding transition exists. Recovery must propagate this typed refusal.
func TestBootDirAdapter_BoundRefreshUnavailablePreservesArtifacts(t *testing.T) {
	for _, provider := range []string{"claude", "codex", "opencode"} {
		for _, shape := range []string{"existing", "empty", "missing"} {
			t.Run(provider+"/"+shape, func(t *testing.T) {
				root := t.TempDir()
				bootDir := filepath.Join(root, "bound")
				if shape != "missing" {
					if err := os.Mkdir(bootDir, 0700); err != nil {
						t.Fatal(err)
					}
				}
				if shape == "existing" {
					if err := os.WriteFile(filepath.Join(bootDir, "CLAUDE.md"), []byte("original binding"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				profile := makeClaudeProfile()
				profile.DefaultProvider = provider
				a, err := newAgentBootDirAdapter(newAdapterTestDeps(profile))
				if err != nil {
					t.Fatal(err)
				}
				a.Track("session", bootDir, runtimeagent.Options{Mode: runtimeagent.ModeLongLived, AgentProfile: profile.Slug})
				for _, refresh := range []func(context.Context, string) error{a.Repopulate, a.RegenerateCLAUDEMD} {
					for range 2 {
						refreshErr := refresh(context.Background(), "session")
						if !errors.Is(refreshErr, runtimeagent.ErrArtifactRefreshUnavailable) {
							t.Fatalf("expected typed unavailable: %v", refreshErr)
						}
					}
				}
				entry, ok := a.lookup("session")
				if !ok || entry.bootDir != bootDir {
					t.Fatal("binding changed")
				}
				if shape == "missing" {
					if _, statErr := os.Stat(bootDir); !errors.Is(statErr, os.ErrNotExist) {
						t.Fatal("missing root recreated", statErr)
					}
					return
				}
				entries, err := os.ReadDir(bootDir)
				if err != nil {
					t.Fatal(err)
				}
				if shape == "empty" {
					if len(entries) != 0 {
						t.Fatal("empty bound root mutated", entries)
					}
					return
				}
				confined, openErr := os.OpenRoot(bootDir)
				if openErr != nil {
					t.Fatal(openErr)
				}
				t.Cleanup(func() {
					if closeErr := confined.Close(); closeErr != nil {
						t.Error(closeErr)
					}
				})
				body, err := confined.ReadFile("CLAUDE.md")
				if err != nil || string(body) != "original binding" || len(entries) != 1 {
					t.Fatal("bound artifacts changed", string(body), err)
				}
			})
		}
	}
}

func TestBootDirAdapter_BrokerPropagatesUnavailable(t *testing.T) {
	for _, remediation := range []broker.Remediation{broker.RemediationRepopulateSandbox, broker.RemediationRegenerateCLAUDEMD} {
		a, err := newAgentBootDirAdapter(newAdapterTestDeps(makeClaudeProfile()))
		if err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		a.Track("session", dir, runtimeagent.Options{Mode: runtimeagent.ModeLongLived})
		b := broker.NewBroker(broker.Dependencies{BootDir: a})
		err = b.Remediate(context.Background(), &broker.FailureEvent{SessionID: "session"}, broker.Classification{Class: broker.ClassConfigPermissions, Remediation: remediation})
		if !errors.Is(err, runtimeagent.ErrArtifactRefreshUnavailable) {
			t.Fatalf("broker hid unsupported binding refresh: %v", err)
		}
	}
}

func TestBootDirAdapter_TrackingAndErrors(t *testing.T) {
	a, err := newAgentBootDirAdapter(newAdapterTestDeps(makeClaudeProfile()))
	if err != nil {
		t.Fatal(err)
	}
	a.Track("session", t.TempDir(), runtimeagent.Options{})
	latest := t.TempDir()
	a.Track("session", latest, runtimeagent.Options{})
	entry, ok := a.lookup("session")
	if !ok || entry.bootDir != latest || entry.opts.SessionID != "session" {
		t.Fatal("replacement binding not recorded")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if refreshErr := a.Repopulate(canceled, "session"); !errors.Is(refreshErr, context.Canceled) {
		t.Fatal("cancellation lost", refreshErr)
	}
	a.Untrack("session")
	if refreshErr := a.Repopulate(context.Background(), "session"); refreshErr == nil || errors.Is(refreshErr, runtimeagent.ErrArtifactRefreshUnavailable) {
		t.Fatal("untracked session did not refuse lookup", refreshErr)
	}
	deps := &runtimeagent.Dependencies{Agents: &fakeAgentProfilesResolver{err: errors.New("resolver failed")}}
	failed, err := newAgentBootDirAdapter(deps)
	if err != nil {
		t.Fatal(err)
	}
	failed.Track("session", latest, runtimeagent.Options{})
	if err := failed.RegenerateCLAUDEMD(context.Background(), "session"); err == nil || !strings.Contains(err.Error(), "resolver failed") {
		t.Fatal("resolution error lost", err)
	}
}
