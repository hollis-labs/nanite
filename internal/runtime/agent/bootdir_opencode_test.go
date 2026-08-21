package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// TestOpencodeLayout_Setup_FileShape verifies the agents/<slug>.md +
// agents.json + opencode.json triplet plus the shared sandbox content.
func TestOpencodeLayout_Setup_FileShape(t *testing.T) {
	profile := &store.AgentProfile{
		ID:          "oc-1",
		Name:        "Opencode Test",
		Slug:        "opencode-test",
		Description: "Opencode bootdir verifier",
	}

	bootDir, err := opencodeLayout{}.Setup(SetupParams{
		SessionID:    "sess-oc",
		RunID:        "r1",
		AgentProfile: profile,
		Mode:         ModeOneShot,
		SystemPrompt: "Opencode behaviors.",
		BootContent:  "# Boot\n",
		ProjectDir:   t.TempDir(),
	})
	if err != nil {
		t.Fatalf("opencodeLayout.Setup: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(bootDir) })

	if !strings.Contains(filepath.Base(bootDir), "nanite-boot-opencode-sess-oc-r") {
		t.Errorf("boot dir name %q missing forensic prefix", filepath.Base(bootDir))
	}

	// agents/opencode-test.md
	agentMDPath := filepath.Join(bootDir, "agents", "opencode-test.md")
	body, err := os.ReadFile(agentMDPath)
	if err != nil {
		t.Fatalf("read agents/opencode-test.md: %v", err)
	}
	if !strings.Contains(string(body), "Opencode Test") {
		t.Errorf("agents/<slug>.md missing agent name\n%s", string(body))
	}
	if !strings.Contains(string(body), "Opencode behaviors.") {
		t.Errorf("agents/<slug>.md missing system prompt\n%s", string(body))
	}

	// agents.json shape
	agentsJSONBody, err := os.ReadFile(filepath.Join(bootDir, "agents.json"))
	if err != nil {
		t.Fatalf("read agents.json: %v", err)
	}
	var agentsJSON struct {
		Agents []struct {
			Name             string `json:"name"`
			Description      string `json:"description"`
			InstructionsFile string `json:"instructions_file"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(agentsJSONBody, &agentsJSON); err != nil {
		t.Fatalf("parse agents.json: %v", err)
	}
	if len(agentsJSON.Agents) != 1 {
		t.Fatalf("agents.json: want 1 agent, got %d", len(agentsJSON.Agents))
	}
	got := agentsJSON.Agents[0]
	if got.Name != "opencode-test" || got.InstructionsFile != "./agents/opencode-test.md" {
		t.Errorf("agents.json entry mismatch: %+v", got)
	}

	// opencode.json shape
	opencodeJSONBody, err := os.ReadFile(filepath.Join(bootDir, "opencode.json"))
	if err != nil {
		t.Fatalf("read opencode.json: %v", err)
	}
	var opencodeJSON map[string]map[string]map[string]string
	if err := json.Unmarshal(opencodeJSONBody, &opencodeJSON); err != nil {
		t.Fatalf("parse opencode.json: %v", err)
	}
	if got := opencodeJSON["agent"]["opencode-test"]["prompt"]; got != "{file:./agents/opencode-test.md}" {
		t.Errorf("opencode.json prompt mismatch: %q", got)
	}

	// Common sandbox content + boot.md.
	for _, p := range []string{"boot.md", ".sandbox/agent-context.md", ".sandbox/envelope-schema.md"} {
		if _, err := os.Stat(filepath.Join(bootDir, p)); err != nil {
			t.Errorf("missing %s: %v", p, err)
		}
	}
}

// TestOpencodeLayout_AmendEnv injects OPENCODE_CONFIG_DIR.
func TestOpencodeLayout_AmendEnv(t *testing.T) {
	base := map[string]string{"PATH": "/usr/bin"}
	amended := opencodeLayout{}.AmendEnv(base, "/tmp/boot")
	if amended["OPENCODE_CONFIG_DIR"] != "/tmp/boot" {
		t.Errorf("AmendEnv missing OPENCODE_CONFIG_DIR=/tmp/boot, got %v", amended)
	}
	if amended["PATH"] != "/usr/bin" {
		t.Errorf("AmendEnv lost base PATH, got %v", amended)
	}
	if base["OPENCODE_CONFIG_DIR"] != "" {
		t.Errorf("AmendEnv leaked into base map: %v", base)
	}
}

// TestOpencodeLayout_SpawnWorkdir returns project dir, not boot dir, when a
// project dir is given.
func TestOpencodeLayout_SpawnWorkdir(t *testing.T) {
	if got := (opencodeLayout{}).SpawnWorkdir("/tmp/boot", "/proj"); got != "/proj" {
		t.Errorf("SpawnWorkdir = %q, want /proj", got)
	}
}

// TestOpencodeLayout_SpawnWorkdir_EmptyProjectDirFallsBackToBootDir is the
// regression pin for TASKS/agent-host-acp/18: real chat sessions always call
// SpawnWorkdir with an empty projectDir (bootSessionWorkdir's documented
// stub, internal/service/chat_boot_drive.go), and the caller feeds the
// result straight into wrapper.Config.Workdir, which wrapper.Wrapper.Run
// hard-requires to be non-empty. Pre-fix this returned "" verbatim, which
// crashed every real OpenCode CLI session on its first turn.
func TestOpencodeLayout_SpawnWorkdir_EmptyProjectDirFallsBackToBootDir(t *testing.T) {
	if got := (opencodeLayout{}).SpawnWorkdir("/tmp/boot", ""); got != "/tmp/boot" {
		t.Errorf("SpawnWorkdir(bootDir, \"\") = %q, want /tmp/boot (bootDir fallback)", got)
	}
}

// TestAgentSlug_Fallback ensures agentSlug picks Slug over Name and
// falls back to "agent" for nil profiles.
func TestAgentSlug_Fallback(t *testing.T) {
	cases := []struct {
		profile *store.AgentProfile
		want    string
	}{
		{nil, "agent"},
		{&store.AgentProfile{Name: "Spaces In Name"}, "spaces-in-name"},
		{&store.AgentProfile{Slug: "preferred", Name: "Other"}, "preferred"},
		{&store.AgentProfile{Slug: "", Name: ""}, "agent"},
	}
	for _, c := range cases {
		got := agentSlug(SetupParams{AgentProfile: c.profile})
		if got != c.want {
			t.Errorf("agentSlug(%+v) = %q, want %q", c.profile, got, c.want)
		}
	}
}
