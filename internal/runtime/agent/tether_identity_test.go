package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func TestTetherIdentityFile_NoTetherURN_ReturnsNil(t *testing.T) {
	profile := &store.AgentProfile{ID: "agent-test"}
	if got := tetherIdentityFile(SetupParams{AgentProfile: profile, SessionID: "sess-1"}); got != nil {
		t.Errorf("expected nil when TetherURN is empty, got %q", got)
	}
}

func TestTetherIdentityFile_ManagedButNotYetMinted_ReturnsNil(t *testing.T) {
	profile := &store.AgentProfile{ID: "agent-test", TetherManaged: true, TetherURN: ""}
	if got := tetherIdentityFile(SetupParams{AgentProfile: profile, SessionID: "sess-1"}); got != nil {
		t.Errorf("expected nil when tether_managed is set but no URN has been minted yet, got %q", got)
	}
}

func TestTetherIdentityFile_WithTetherURN_PlantsBothURNs(t *testing.T) {
	profile := &store.AgentProfile{
		ID:        "agent-test",
		TetherURN: "msg://agent/agent-mux/agt_df7bkby66a",
	}
	got := tetherIdentityFile(SetupParams{AgentProfile: profile, SessionID: "sess-1"})
	if got == nil {
		t.Fatal("expected non-nil content when tether_urn is set")
	}
	content := string(got)
	if !strings.Contains(content, "msg://agent/agent-mux/agt_df7bkby66a") {
		t.Errorf("content missing agent URN: %s", content)
	}
	if !strings.Contains(content, "msg://session/nanite/sess-1") {
		t.Errorf("content missing session URN: %s", content)
	}
}

func TestTetherIdentityFile_NoSessionID_OmitsSessionURN(t *testing.T) {
	profile := &store.AgentProfile{
		ID:        "agent-test",
		TetherURN: "msg://agent/agent-mux/agt_df7bkby66a",
	}
	got := tetherIdentityFile(SetupParams{AgentProfile: profile, SessionID: ""})
	if got == nil {
		t.Fatal("expected non-nil content when tether_urn is set, even with empty SessionID")
	}
	if strings.Contains(string(got), "msg://session/nanite/") {
		t.Errorf("did not expect a session URN with empty SessionID: %s", got)
	}
}

// TestClaudeLayout_Setup_PlantsTetherIdentity_WhenRegistered is the
// end-to-end check: a profile with a TetherURN gets the identity file
// planted into the real boot dir; a profile without one does not.
func TestClaudeLayout_Setup_PlantsTetherIdentity_WhenRegistered(t *testing.T) {
	registered := &store.AgentProfile{
		ID:        "agent-registered",
		Name:      "Registered Agent",
		Slug:      "registered-agent",
		TetherURN: "msg://agent/agent-mux/agt_df7bkby66a",
	}
	bootDir, err := claudeLayout{}.Setup(SetupParams{
		SessionID:    "sess-registered",
		AgentProfile: registered,
		SystemPrompt: "You are a test agent.",
		ProjectDir:   t.TempDir(),
	})
	if err != nil {
		t.Fatalf("claudeLayout.Setup: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(bootDir) })

	identityPath := filepath.Join(bootDir, ".sandbox", "tether-identity.md")
	data, err := os.ReadFile(identityPath)
	if err != nil {
		t.Fatalf("expected identity file at %s: %v", identityPath, err)
	}
	if !strings.Contains(string(data), "msg://agent/agent-mux/agt_df7bkby66a") {
		t.Errorf("identity file missing agent URN: %s", data)
	}
	if !strings.Contains(string(data), "msg://session/nanite/sess-registered") {
		t.Errorf("identity file missing session URN: %s", data)
	}
}

func TestClaudeLayout_Setup_OmitsTetherIdentity_WhenNotRegistered(t *testing.T) {
	unregistered := &store.AgentProfile{
		ID:   "agent-unregistered",
		Name: "Unregistered Agent",
		Slug: "unregistered-agent",
	}
	bootDir, err := claudeLayout{}.Setup(SetupParams{
		SessionID:    "sess-unregistered",
		AgentProfile: unregistered,
		SystemPrompt: "You are a test agent.",
		ProjectDir:   t.TempDir(),
	})
	if err != nil {
		t.Fatalf("claudeLayout.Setup: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(bootDir) })

	identityPath := filepath.Join(bootDir, ".sandbox", "tether-identity.md")
	if _, err := os.Stat(identityPath); !os.IsNotExist(err) {
		t.Errorf("expected no identity file for an unregistered profile, stat err = %v", err)
	}
}
