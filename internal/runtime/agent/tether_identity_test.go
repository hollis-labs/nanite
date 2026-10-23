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
	// #nosec G304 -- identityPath is an exact path beneath the t.TempDir-derived bootDir.
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

// The bridge delivers to the durable actor's address, so the identity file
// must name that one and not the profile's separately registered address.
func TestTetherIdentityFile_PrefersTheBoundActorURN(t *testing.T) {
	profile := &store.AgentProfile{ID: "p", TetherURN: "msg://agent/agent-mux/agt_profile"}
	got := string(tetherIdentityFile(SetupParams{
		AgentProfile: profile, SessionID: "s", ActorURN: "msg://agent/nanite/agt_actor",
	}))
	if !strings.Contains(got, "Agent URN: `msg://agent/nanite/agt_actor`") {
		t.Fatalf("actor URN not planted as the Agent URN:\n%s", got)
	}
	if strings.Contains(got, "Agent URN: `msg://agent/agent-mux/agt_profile`") {
		t.Fatalf("profile URN planted as the Agent URN:\n%s", got)
	}
	if !strings.Contains(got, "as `from`") || !strings.Contains(got, "in_reply_to") {
		t.Fatalf("missing send guidance:\n%s", got)
	}
}

func TestTetherIdentityFile_BoundActorWithoutProfileURN(t *testing.T) {
	got := tetherIdentityFile(SetupParams{AgentProfile: &store.AgentProfile{ID: "p"}, ActorURN: "msg://agent/nanite/agt_actor"})
	if got == nil || !strings.Contains(string(got), "msg://agent/nanite/agt_actor") {
		t.Fatalf("a bound actor with no profile URN got %q", got)
	}
}

func TestComposeBootdirParams_ResolvesActorURNFromDependencies(t *testing.T) {
	var gotSession, gotProfile string
	deps := &Dependencies{ActorURN: func(sessionID, profileID string) string {
		gotSession, gotProfile = sessionID, profileID
		return "msg://agent/nanite/agt_actor"
	}}
	profile := &store.AgentProfile{ID: "prof-1"}
	_, params := composeBootdirParams(deps, Options{Provider: "claude", Workdir: t.TempDir(), SessionID: "sess-9"}, profile, "sess-9")
	if params.ActorURN != "msg://agent/nanite/agt_actor" || gotSession != "sess-9" || gotProfile != "prof-1" {
		t.Fatalf("ActorURN=%q looked up (%q,%q)", params.ActorURN, gotSession, gotProfile)
	}
	// No profile, no resolver: nothing is looked up or planted.
	if _, p := composeBootdirParams(deps, Options{Provider: "claude", Workdir: t.TempDir()}, nil, "s"); p.ActorURN != "" {
		t.Fatalf("no profile: ActorURN=%q", p.ActorURN)
	}
	if _, p := composeBootdirParams(&Dependencies{}, Options{Provider: "claude", Workdir: t.TempDir()}, profile, "s"); p.ActorURN != "" {
		t.Fatalf("no resolver: ActorURN=%q", p.ActorURN)
	}
}

// The observe-mode caveat is planted whichever address the file names: the
// bound actor's or the profile's own.
func TestTetherIdentityFile_AlwaysSaysFromIsNotControlled(t *testing.T) {
	for name, params := range map[string]SetupParams{
		"bound actor":      {AgentProfile: &store.AgentProfile{ID: "p"}, ActorURN: "msg://agent/nanite/agt_actor", SessionID: "s"},
		"profile fallback": {AgentProfile: &store.AgentProfile{ID: "p", TetherURN: "msg://agent/agent-mux/agt_p"}, SessionID: "s"},
	} {
		got := string(tetherIdentityFile(params))
		if !strings.Contains(got, "guidance, not a control") || !strings.Contains(got, "only observes") {
			t.Errorf("%s: no observe-mode caveat:\n%s", name, got)
		}
	}
}
