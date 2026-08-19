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

// TestBootDirAdapter_Repopulate_Idempotent verifies that calling
// Repopulate twice in a row leaves the bootDir in the same canonical
// shape — every plant call uses fsutil.AtomicWriteFile so the second
// invocation overwrites without error.
func TestBootDirAdapter_Repopulate_Idempotent(t *testing.T) {
	t.Parallel()

	bootDir := t.TempDir()
	deps := newAdapterTestDeps(makeClaudeProfile())

	a, err := newAgentBootDirAdapter(deps)
	if err != nil {
		t.Fatalf("newAgentBootDirAdapter: %v", err)
	}

	const sessID = "sess-repopulate-idempotent"
	a.Track(sessID, bootDir, runtimeagent.Options{
		Mode:         runtimeagent.ModeLongLived,
		AgentProfile: "test-claude",
		SessionID:    sessID,
	})

	if err := a.Repopulate(context.Background(), sessID); err != nil {
		t.Fatalf("Repopulate (1st): %v", err)
	}
	// Sanity-check the canonical claude shape is present.
	for _, rel := range []string{
		"CLAUDE.md",
		"boot.md",
		".sandbox/agent-context.md",
		".sandbox/envelope-schema.md",
		".claude/settings.json",
	} {
		if _, err := os.Stat(filepath.Join(bootDir, rel)); err != nil {
			t.Errorf("Repopulate (1st): expected %s present: %v", rel, err)
		}
	}

	if err := a.Repopulate(context.Background(), sessID); err != nil {
		t.Fatalf("Repopulate (2nd): %v", err)
	}
	// Re-verify after the second repopulate to catch shape regressions.
	for _, rel := range []string{
		"CLAUDE.md",
		".sandbox/agent-context.md",
		".claude/settings.json",
	} {
		if _, err := os.Stat(filepath.Join(bootDir, rel)); err != nil {
			t.Errorf("Repopulate (2nd): expected %s present: %v", rel, err)
		}
	}
}

// TestBootDirAdapter_Repopulate_MissingDir verifies Repopulate succeeds
// against a freshly-created (empty) sandbox dir — the failure mode that
// actually triggers RepopulateSandbox in production is a wiped sandbox
// dir, so this case must work end-to-end.
func TestBootDirAdapter_Repopulate_MissingDir(t *testing.T) {
	t.Parallel()

	bootDir := t.TempDir()
	deps := newAdapterTestDeps(makeClaudeProfile())

	a, err := newAgentBootDirAdapter(deps)
	if err != nil {
		t.Fatalf("newAgentBootDirAdapter: %v", err)
	}
	const sessID = "sess-repopulate-empty"
	a.Track(sessID, bootDir, runtimeagent.Options{
		Mode:         runtimeagent.ModeLongLived,
		AgentProfile: "test-claude",
		SessionID:    sessID,
	})

	// bootDir exists but is empty. Repopulate must converge.
	entries, _ := os.ReadDir(bootDir)
	if len(entries) != 0 {
		t.Fatalf("test setup: bootDir should be empty, got %d entries", len(entries))
	}

	if err := a.Repopulate(context.Background(), sessID); err != nil {
		t.Fatalf("Repopulate: %v", err)
	}
	if _, err := os.Stat(filepath.Join(bootDir, "CLAUDE.md")); err != nil {
		t.Errorf("expected CLAUDE.md present after Repopulate on empty dir: %v", err)
	}
}

// TestBootDirAdapter_Repopulate_PartialDir verifies Repopulate succeeds
// against a sandbox dir that has some but not all files (the
// SandboxState.Missing=true classifier rule fires for any-key-file-missing
// case, not just whole-dir-missing).
func TestBootDirAdapter_Repopulate_PartialDir(t *testing.T) {
	t.Parallel()

	bootDir := t.TempDir()
	// Plant a stale CLAUDE.md to verify Repopulate overwrites it with
	// the canonical body rather than appending.
	stalePath := filepath.Join(bootDir, "CLAUDE.md")
	if err := os.WriteFile(stalePath, []byte("STALE-PLACEHOLDER"), 0o644); err != nil {
		t.Fatalf("seed stale CLAUDE.md: %v", err)
	}

	deps := newAdapterTestDeps(makeClaudeProfile())

	a, err := newAgentBootDirAdapter(deps)
	if err != nil {
		t.Fatalf("newAgentBootDirAdapter: %v", err)
	}
	const sessID = "sess-repopulate-partial"
	a.Track(sessID, bootDir, runtimeagent.Options{
		Mode:         runtimeagent.ModeLongLived,
		AgentProfile: "test-claude",
		SessionID:    sessID,
	})

	if err := a.Repopulate(context.Background(), sessID); err != nil {
		t.Fatalf("Repopulate: %v", err)
	}
	body, err := os.ReadFile(stalePath)
	if err != nil {
		t.Fatalf("read CLAUDE.md: %v", err)
	}
	if strings.Contains(string(body), "STALE-PLACEHOLDER") {
		t.Errorf("expected stale CLAUDE.md to be overwritten, got: %q", string(body))
	}
	if !strings.Contains(string(body), "TestClaude") {
		t.Errorf("expected canonical CLAUDE.md body to contain agent name, got: %q", string(body))
	}
}

// TestBootDirAdapter_RegenerateCLAUDEMD_OnlyClaudeMD verifies that
// RegenerateCLAUDEMD touches only CLAUDE.md and leaves the rest of the
// sandbox dir intact (this is the contract for the watchdog_kill
// remediation path — full-repopulate is overkill when the slot is just
// stale).
func TestBootDirAdapter_RegenerateCLAUDEMD_OnlyClaudeMD(t *testing.T) {
	t.Parallel()

	bootDir := t.TempDir()
	// Pre-populate the sandbox shape and capture mtimes for assertion.
	deps := newAdapterTestDeps(makeClaudeProfile())
	a, err := newAgentBootDirAdapter(deps)
	if err != nil {
		t.Fatalf("newAgentBootDirAdapter: %v", err)
	}
	const sessID = "sess-regen-claudemd"
	a.Track(sessID, bootDir, runtimeagent.Options{
		Mode:         runtimeagent.ModeLongLived,
		AgentProfile: "test-claude",
		SessionID:    sessID,
	})
	if err := a.Repopulate(context.Background(), sessID); err != nil {
		t.Fatalf("seed Repopulate: %v", err)
	}

	// Mark agent-context.md with a sentinel so we can detect rewrite.
	contextPath := filepath.Join(bootDir, ".sandbox", "agent-context.md")
	sentinel := "SENTINEL-AGENT-CONTEXT"
	if err := os.WriteFile(contextPath, []byte(sentinel), 0o644); err != nil {
		t.Fatalf("seed sentinel: %v", err)
	}

	// Stash CLAUDE.md mtime before regen so we can assert it changed.
	claudePath := filepath.Join(bootDir, "CLAUDE.md")
	if err := os.WriteFile(claudePath, []byte("PLACEHOLDER-FOR-MTIME"), 0o644); err != nil {
		t.Fatalf("seed CLAUDE.md placeholder: %v", err)
	}

	if err := a.RegenerateCLAUDEMD(context.Background(), sessID); err != nil {
		t.Fatalf("RegenerateCLAUDEMD: %v", err)
	}

	// CLAUDE.md must be the canonical body, not the placeholder.
	claudeBody, err := os.ReadFile(claudePath)
	if err != nil {
		t.Fatalf("read CLAUDE.md: %v", err)
	}
	if strings.Contains(string(claudeBody), "PLACEHOLDER-FOR-MTIME") {
		t.Errorf("expected CLAUDE.md to be regenerated, got placeholder body")
	}
	if !strings.Contains(string(claudeBody), "TestClaude") {
		t.Errorf("expected regenerated CLAUDE.md to contain agent name")
	}

	// agent-context.md must still carry the sentinel — RegenerateCLAUDEMD
	// must not touch it.
	contextBody, err := os.ReadFile(contextPath)
	if err != nil {
		t.Fatalf("read agent-context.md: %v", err)
	}
	if string(contextBody) != sentinel {
		t.Errorf("expected agent-context.md to retain sentinel, got: %q", string(contextBody))
	}
}

// TestBootDirAdapter_NoEntry verifies that the adapter returns a clear
// error for unknown sessionIDs rather than panicking on nil-map access
// or producing a misleading error elsewhere.
func TestBootDirAdapter_NoEntry(t *testing.T) {
	t.Parallel()

	deps := newAdapterTestDeps(makeClaudeProfile())
	a, err := newAgentBootDirAdapter(deps)
	if err != nil {
		t.Fatalf("newAgentBootDirAdapter: %v", err)
	}

	if err := a.Repopulate(context.Background(), "ghost"); err == nil {
		t.Errorf("Repopulate for unknown sessionID: expected error, got nil")
	} else if !strings.Contains(err.Error(), "no entry") {
		t.Errorf("Repopulate error should mention no-entry, got: %v", err)
	}

	if err := a.RegenerateCLAUDEMD(context.Background(), "ghost"); err == nil {
		t.Errorf("RegenerateCLAUDEMD for unknown sessionID: expected error, got nil")
	}
}

// TestBootDirAdapter_Untrack verifies Untrack removes the registry
// entry — a subsequent Repopulate against the same sessionID must error
// out as no-entry.
func TestBootDirAdapter_Untrack(t *testing.T) {
	t.Parallel()

	bootDir := t.TempDir()
	deps := newAdapterTestDeps(makeClaudeProfile())
	a, err := newAgentBootDirAdapter(deps)
	if err != nil {
		t.Fatalf("newAgentBootDirAdapter: %v", err)
	}

	const sessID = "sess-untrack"
	a.Track(sessID, bootDir, runtimeagent.Options{Mode: runtimeagent.ModeLongLived, AgentProfile: "test-claude"})
	if err := a.Repopulate(context.Background(), sessID); err != nil {
		t.Fatalf("Repopulate (pre-untrack): %v", err)
	}

	a.Untrack(sessID)
	if err := a.Repopulate(context.Background(), sessID); err == nil {
		t.Errorf("Repopulate after Untrack: expected error, got nil")
	}
}

// TestBootDirAdapter_TrackOverwrites verifies that calling Track twice
// for the same sessionID replaces the prior entry — the second
// Repopulate must target the new bootDir.
func TestBootDirAdapter_TrackOverwrites(t *testing.T) {
	t.Parallel()

	dirA := t.TempDir()
	dirB := t.TempDir()
	deps := newAdapterTestDeps(makeClaudeProfile())
	a, err := newAgentBootDirAdapter(deps)
	if err != nil {
		t.Fatalf("newAgentBootDirAdapter: %v", err)
	}

	const sessID = "sess-overwrite"
	a.Track(sessID, dirA, runtimeagent.Options{Mode: runtimeagent.ModeLongLived, AgentProfile: "test-claude"})
	a.Track(sessID, dirB, runtimeagent.Options{Mode: runtimeagent.ModeLongLived, AgentProfile: "test-claude"})

	if err := a.Repopulate(context.Background(), sessID); err != nil {
		t.Fatalf("Repopulate: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dirB, "CLAUDE.md")); err != nil {
		t.Errorf("expected CLAUDE.md in dirB (latest Track), got: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dirA, "CLAUDE.md")); err == nil {
		t.Errorf("expected dirA (overwritten Track) to remain empty")
	}
}

// TestBootDirAdapter_BrokerRemediate_E2E exercises the full broker →
// adapter path: builds a broker.Broker wired with a real
// agentBootDirAdapter, wipes the sandbox dir, asks the broker to
// Remediate(RepopulateSandbox), and asserts the dir is repopulated and
// no "BootDir not wired" error surfaces (the smoke acceptance from the
// boot-prompt).
func TestBootDirAdapter_BrokerRemediate_E2E(t *testing.T) {
	t.Parallel()

	bootDir := t.TempDir()
	deps := newAdapterTestDeps(makeClaudeProfile())

	a, err := newAgentBootDirAdapter(deps)
	if err != nil {
		t.Fatalf("newAgentBootDirAdapter: %v", err)
	}
	const sessID = "sess-broker-e2e"
	a.Track(sessID, bootDir, runtimeagent.Options{
		Mode:         runtimeagent.ModeLongLived,
		AgentProfile: "test-claude",
		SessionID:    sessID,
	})

	// Stand the broker up with the adapter wired in. AgentBoot / Store /
	// Envelope stay nil — Remediate doesn't reach into those for the
	// RepopulateSandbox branch.
	b := broker.NewBroker(broker.Dependencies{
		BootDir: a,
	})

	// Seed the sandbox shape, then wipe it to simulate the
	// SandboxDirState.Missing failure mode.
	if err := a.Repopulate(context.Background(), sessID); err != nil {
		t.Fatalf("seed Repopulate: %v", err)
	}
	if err := os.RemoveAll(bootDir); err != nil {
		t.Fatalf("wipe bootDir: %v", err)
	}
	if err := os.MkdirAll(bootDir, 0o755); err != nil {
		t.Fatalf("recreate empty bootDir: %v", err)
	}

	// Drive Remediate with a Classification carrying RemediationRepopulateSandbox.
	classification := broker.Classification{
		Class:       broker.ClassConfigPermissions,
		Remediation: broker.RemediationRepopulateSandbox,
	}
	ev := &broker.FailureEvent{SessionID: sessID}
	if err := b.Remediate(context.Background(), ev, classification); err != nil {
		t.Fatalf("broker.Remediate: %v (this is the smoke that previously returned 'BootDir not wired')", err)
	}

	// Sandbox dir must be repopulated.
	for _, rel := range []string{
		"CLAUDE.md",
		".sandbox/agent-context.md",
		".sandbox/envelope-schema.md",
	} {
		if _, err := os.Stat(filepath.Join(bootDir, rel)); err != nil {
			t.Errorf("expected %s present after broker remediate: %v", rel, err)
		}
	}
}

// TestBootDirAdapter_BrokerRemediate_RegenerateCLAUDEMD_E2E mirrors the
// repopulate smoke but for the watchdog_kill remediation path —
// RegenerateCLAUDEMD against a populated dir touches only the
// system-prompt slot.
func TestBootDirAdapter_BrokerRemediate_RegenerateCLAUDEMD_E2E(t *testing.T) {
	t.Parallel()

	bootDir := t.TempDir()
	deps := newAdapterTestDeps(makeClaudeProfile())

	a, err := newAgentBootDirAdapter(deps)
	if err != nil {
		t.Fatalf("newAgentBootDirAdapter: %v", err)
	}
	const sessID = "sess-broker-regen-e2e"
	a.Track(sessID, bootDir, runtimeagent.Options{
		Mode:         runtimeagent.ModeLongLived,
		AgentProfile: "test-claude",
		SessionID:    sessID,
	})
	if err := a.Repopulate(context.Background(), sessID); err != nil {
		t.Fatalf("seed Repopulate: %v", err)
	}

	// Mark agent-context.md with a sentinel — RegenerateCLAUDEMD must
	// leave it untouched.
	contextPath := filepath.Join(bootDir, ".sandbox", "agent-context.md")
	sentinel := "SENTINEL-REGEN-E2E"
	if err := os.WriteFile(contextPath, []byte(sentinel), 0o644); err != nil {
		t.Fatalf("seed sentinel: %v", err)
	}

	b := broker.NewBroker(broker.Dependencies{BootDir: a})
	classification := broker.Classification{
		Class:       broker.ClassConfigPermissions,
		Remediation: broker.RemediationRegenerateCLAUDEMD,
	}
	ev := &broker.FailureEvent{SessionID: sessID}
	if err := b.Remediate(context.Background(), ev, classification); err != nil {
		t.Fatalf("broker.Remediate (RegenerateCLAUDEMD): %v", err)
	}

	body, err := os.ReadFile(contextPath)
	if err != nil {
		t.Fatalf("read agent-context.md: %v", err)
	}
	if string(body) != sentinel {
		t.Errorf("expected agent-context.md untouched, got: %q", string(body))
	}
}

// TestBootDirAdapter_ProfileResolverError verifies that an Agents
// resolution failure surfaces from Repopulate as a wrapped error rather
// than a panic — the broker logs + retries on this path so a clean
// error chain is load-bearing.
func TestBootDirAdapter_ProfileResolverError(t *testing.T) {
	t.Parallel()

	bootDir := t.TempDir()
	deps := &runtimeagent.Dependencies{
		Agents: &fakeAgentProfilesResolver{err: errors.New("boom")},
	}
	a, err := newAgentBootDirAdapter(deps)
	if err != nil {
		t.Fatalf("newAgentBootDirAdapter: %v", err)
	}
	const sessID = "sess-resolver-error"
	a.Track(sessID, bootDir, runtimeagent.Options{Mode: runtimeagent.ModeLongLived})
	if err := a.Repopulate(context.Background(), sessID); err == nil {
		t.Errorf("expected Repopulate to surface resolver error")
	} else if !strings.Contains(err.Error(), "boom") {
		t.Errorf("expected wrapped resolver error, got: %v", err)
	}
}
