package agent

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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

// TestOpencodeLayout_AmendEnv injects OPENCODE_CONFIG_DIR, and moves
// XDG_CONFIG_HOME into the boot dir so the operator's own opencode config is
// not loaded next to it (CW-20261001-0239). XDG_DATA_HOME, where opencode keeps
// its auth, is left alone.
func TestOpencodeLayout_AmendEnv(t *testing.T) {
	t.Setenv(OpenCodeIsolateEnv, "")
	base := map[string]string{"PATH": "/usr/bin"}
	amended := opencodeLayout{}.AmendEnv(base, "/tmp/boot")
	if amended["OPENCODE_CONFIG_DIR"] != "/tmp/boot" {
		t.Errorf("AmendEnv missing OPENCODE_CONFIG_DIR=/tmp/boot, got %v", amended)
	}
	if want := "/tmp/boot/xdg-config"; amended["XDG_CONFIG_HOME"] != want {
		t.Errorf("XDG_CONFIG_HOME = %q, want %q", amended["XDG_CONFIG_HOME"], want)
	}
	if v, set := amended["XDG_DATA_HOME"]; set {
		t.Errorf("AmendEnv set XDG_DATA_HOME=%q; auth lives there and must stay where it is", v)
	}
	if amended["PATH"] != "/usr/bin" {
		t.Errorf("AmendEnv lost base PATH, got %v", amended)
	}
	if base["OPENCODE_CONFIG_DIR"] != "" || base["XDG_CONFIG_HOME"] != "" {
		t.Errorf("AmendEnv leaked into base map: %v", base)
	}
}

// NANITE_OPENCODE_ISOLATE_CONFIG=0 (or false) goes back to loading the
// operator's opencode config; any other value, a typo included, stays
// isolated, and the startup warning appears only when it is off.
func TestOpencodeLayout_AmendEnv_KillSwitch(t *testing.T) {
	for _, tc := range []struct {
		env     string
		isolate bool
	}{{"", true}, {"1", true}, {"off", true}, {"0", false}, {" False ", false}} {
		t.Setenv(OpenCodeIsolateEnv, tc.env)
		amended := opencodeLayout{}.AmendEnv(map[string]string{}, "/tmp/boot")
		if _, set := amended["XDG_CONFIG_HOME"]; set != tc.isolate {
			t.Errorf("%s=%q: XDG_CONFIG_HOME set = %v, want %v", OpenCodeIsolateEnv, tc.env, set, tc.isolate)
		}
		if amended["OPENCODE_CONFIG_DIR"] != "/tmp/boot" {
			t.Errorf("%s=%q: OPENCODE_CONFIG_DIR lost: %v", OpenCodeIsolateEnv, tc.env, amended)
		}
	}

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	t.Setenv(OpenCodeIsolateEnv, "")
	WarnIfOpenCodeConfigNotIsolated()
	if buf.Len() != 0 {
		t.Errorf("isolation is on, but it logged: %s", buf.String())
	}
	t.Setenv(OpenCodeIsolateEnv, "0")
	WarnIfOpenCodeConfigNotIsolated()
	if out := buf.String(); !strings.Contains(out, "level=WARN") || !strings.Contains(out, OpenCodeIsolateEnv) {
		t.Errorf("kill switch on logged %q, want a WARN naming %s", out, OpenCodeIsolateEnv)
	}
}

// The real opencode, with a user-level MCP server in a fake HOME's
// ~/.config/opencode: it loads it with OPENCODE_CONFIG_DIR alone (the
// behavior before CW-20261001-0239), and with the env the layout now builds
// it loads none, while the agent the layout planted is still there. The
// resolved config comes from `opencode debug config`, which starts no model
// and no MCP server. Skipped where opencode is not installed.
func TestOpencodeLayout_RealOpencodeDropsUserLevelMCP(t *testing.T) {
	bin, err := exec.LookPath("opencode")
	if err != nil {
		t.Skip("opencode is not installed")
	}
	home := t.TempDir()
	userCfg := filepath.Join(home, ".config", "opencode")
	if mkErr := os.MkdirAll(userCfg, 0o750); mkErr != nil {
		t.Fatal(mkErr)
	}
	userJSON := `{"mcp":{"user-level-leak":{"type":"local","command":["true"],"enabled":true}}}`
	if wErr := os.WriteFile(filepath.Join(userCfg, "opencode.json"), []byte(userJSON), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	bootDir, err := opencodeLayout{}.Setup(SetupParams{
		SessionID:    "sess-real",
		RunID:        "r1",
		AgentProfile: &store.AgentProfile{ID: "oc-1", Name: "Real", Slug: "real-agent", Description: "d"},
		Mode:         ModeOneShot,
		BootContent:  "# Boot\n",
		ProjectDir:   t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(bootDir) })

	resolved := func(env map[string]string) (mcp []string, hasAgent bool) {
		cmd := exec.Command(bin, "debug", "config") // #nosec G204 -- opencode found on PATH, fixed arguments
		cmd.Dir = t.TempDir()
		cmd.Stdin = nil
		cmd.Env = []string{
			"PATH=" + os.Getenv("PATH"), "HOME=" + home,
			"XDG_DATA_HOME=" + filepath.Join(home, ".local", "share"),
			"XDG_STATE_HOME=" + filepath.Join(home, ".local", "state"),
			"XDG_CACHE_HOME=" + filepath.Join(home, ".cache"),
		}
		for k, v := range env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("opencode debug config: %v", err)
		}
		var cfg struct {
			MCP   map[string]json.RawMessage `json:"mcp"`
			Agent map[string]json.RawMessage `json:"agent"`
		}
		if err := json.Unmarshal(out, &cfg); err != nil {
			t.Fatalf("opencode debug config is not JSON: %v\n%s", err, out)
		}
		for name := range cfg.MCP {
			mcp = append(mcp, name)
		}
		_, hasAgent = cfg.Agent["real-agent"]
		return mcp, hasAgent
	}

	// Before: OPENCODE_CONFIG_DIR alone, the user's config home untouched.
	before, hasAgent := resolved(map[string]string{"OPENCODE_CONFIG_DIR": bootDir})
	if !slices.Equal(before, []string{"user-level-leak"}) || !hasAgent {
		t.Fatalf("control: with OPENCODE_CONFIG_DIR alone mcp = %v, planted agent = %v; want the user-level server and the agent", before, hasAgent)
	}

	t.Setenv(OpenCodeIsolateEnv, "")
	after, hasAgent := resolved(opencodeLayout{}.AmendEnv(map[string]string{}, bootDir))
	if len(after) != 0 {
		t.Errorf("with the layout's env mcp servers = %v, want none", after)
	}
	if !hasAgent {
		t.Error("the planted agent is gone with the layout's env")
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
