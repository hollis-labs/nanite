package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// TestCodexLayout_Setup_FileShape verifies AGENTS.md replaces CLAUDE.md
// and no .claude/ directory is planted.
func TestCodexLayout_Setup_FileShape(t *testing.T) {
	profile := &store.AgentProfile{
		ID:          "codex-agent",
		Name:        "Codex Test",
		Slug:        "codex-test",
		Description: "Codex bootdir verifier",
	}

	bootDir, err := codexLayout{}.Setup(SetupParams{
		SessionID:    "sess-c1",
		RunID:        "r0",
		AgentProfile: profile,
		Mode:         ModeOneShot,
		SystemPrompt: "You are codex test.",
		BootContent:  "# Boot\n",
		MCPConfig:    MCPConfig{BinaryPath: "/bin/nanite", DBPath: "/tmp/db"},
	})
	if err != nil {
		t.Fatalf("codexLayout.Setup: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(bootDir) })

	if !strings.Contains(filepath.Base(bootDir), "nanite-boot-codex-sess-c1-r") {
		t.Errorf("boot dir name %q missing forensic prefix", filepath.Base(bootDir))
	}

	body, err := os.ReadFile(filepath.Join(bootDir, "AGENTS.md"))
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	if !strings.Contains(string(body), "Codex Test") {
		t.Errorf("AGENTS.md missing agent name\n%s", string(body))
	}
	if !strings.Contains(string(body), "You are codex test.") {
		t.Errorf("AGENTS.md missing system prompt body\n%s", string(body))
	}

	if _, err := os.Stat(filepath.Join(bootDir, ".claude")); !os.IsNotExist(err) {
		t.Errorf("codex layout should not plant .claude/ directory")
	}
	if _, err := os.Stat(filepath.Join(bootDir, "CLAUDE.md")); !os.IsNotExist(err) {
		t.Errorf("codex layout should not plant CLAUDE.md")
	}

	// Sandbox + boot.md + .mcp.json common to nanite layouts must exist,
	// plus the provider config files config.toml + auth.json.
	for _, p := range []string{"boot.md", ".sandbox/agent-context.md", ".sandbox/envelope-schema.md", ".mcp.json", "config.toml", "auth.json"} {
		if _, err := os.Stat(filepath.Join(bootDir, p)); err != nil {
			t.Errorf("missing common file %s: %v", p, err)
		}
	}
}

// TestCodexLayout_ConfigTOML_ApprovalPolicy verifies the planted
// config.toml carries approval_policy + sandbox_mode. Without a config.toml
// a headless codex falls back to its interactive approval default and
// blocks forever waiting for an approval no one can give. The content is
// sourced from go-providers' CodexAdapter.BootDirSpec; the headless-safe
// defaults are approval_policy="never" / sandbox_mode="workspace-write".
func TestCodexLayout_ConfigTOML_ApprovalPolicy(t *testing.T) {
	profile := &store.AgentProfile{Name: "codex-cfg", Slug: "codex-cfg"}
	bootDir, err := codexLayout{}.Setup(SetupParams{SessionID: "s-cfg", AgentProfile: profile})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(bootDir) })

	body, err := os.ReadFile(filepath.Join(bootDir, "config.toml"))
	if err != nil {
		t.Fatalf("read config.toml: %v", err)
	}
	got := string(body)
	for _, want := range []string{
		"approval_policy",
		"sandbox_mode",
		`approval_policy = "never"`,
		`sandbox_mode = "workspace-write"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("config.toml missing %q\n--- body ---\n%s", want, got)
		}
	}

	// config.toml carries secret-ish content — go-providers declares mode
	// 0o600 and the codex layout honors it.
	info, err := os.Stat(filepath.Join(bootDir, "config.toml"))
	if err != nil {
		t.Fatalf("stat config.toml: %v", err)
	}
	if perm := info.Mode().Perm(); perm != codexConfigFileMode {
		t.Errorf("config.toml mode = %o, want %o", perm, codexConfigFileMode)
	}
}

// TestCodexLayout_ConfigTOML_WritableRoots pins that SetupParams.CLIWritableRoots
// threads into the planted config.toml as a [sandbox_workspace_write]
// writable_roots table, and that an empty list omits the table entirely
// (CW-20260518-0075).
func TestCodexLayout_ConfigTOML_WritableRoots(t *testing.T) {
	profile := &store.AgentProfile{Name: "codex-wr", Slug: "codex-wr"}

	bootDir, err := codexLayout{}.Setup(SetupParams{
		SessionID:        "s-wr",
		AgentProfile:     profile,
		CLIWritableRoots: []string{"/Users/x/dev", "/tmp/work"},
	})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(bootDir) })

	body, err := os.ReadFile(filepath.Join(bootDir, "config.toml"))
	if err != nil {
		t.Fatalf("read config.toml: %v", err)
	}
	got := string(body)
	for _, want := range []string{
		"[sandbox_workspace_write]",
		`writable_roots = ["/Users/x/dev", "/tmp/work"]`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("config.toml missing %q\n--- body ---\n%s", want, got)
		}
	}

	// Empty CLIWritableRoots → no [sandbox_workspace_write] table.
	bareDir, err := codexLayout{}.Setup(SetupParams{SessionID: "s-wr-bare", AgentProfile: profile})
	if err != nil {
		t.Fatalf("Setup bare: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(bareDir) })
	bareBody, err := os.ReadFile(filepath.Join(bareDir, "config.toml"))
	if err != nil {
		t.Fatalf("read bare config.toml: %v", err)
	}
	if strings.Contains(string(bareBody), "sandbox_workspace_write") {
		t.Errorf("empty CLIWritableRoots must not emit the table\n--- body ---\n%s", bareBody)
	}
}

// TestCodexLayout_AmendEnv_CodexHome verifies AmendEnv sets
// CODEX_HOME=<bootDir>. Codex reads config.toml + auth.json from
// $CODEX_HOME; without this env pointer the planted config.toml is never
// consulted (codex would read ~/.codex/config.toml instead) and the
// approval-policy fix would be inert.
func TestCodexLayout_AmendEnv_CodexHome(t *testing.T) {
	const bootDir = "/tmp/nanite-boot-codex-xyz"
	out := codexLayout{}.AmendEnv(map[string]string{"PATH": "/usr/bin"}, bootDir)
	if out["CODEX_HOME"] != bootDir {
		t.Errorf("CODEX_HOME = %q, want %q", out["CODEX_HOME"], bootDir)
	}
	// Base env pointers must survive the amendment.
	if out["PATH"] != "/usr/bin" {
		t.Errorf("AmendEnv dropped base env: PATH = %q, want /usr/bin", out["PATH"])
	}
	// Empty bootDir is a no-op (defensive path).
	noop := codexLayout{}.AmendEnv(map[string]string{"PATH": "/usr/bin"}, "")
	if noop["CODEX_HOME"] != "" {
		t.Errorf("empty bootDir should not set CODEX_HOME, got %q", noop["CODEX_HOME"])
	}
}

// TestCodexLayout_BootProperties confirms BootMode is empty (subprocess-
// per-turn delivery), SpawnWorkdir is the boot dir.
func TestCodexLayout_BootProperties(t *testing.T) {
	l := codexLayout{}
	if l.BootMode() != "" {
		t.Errorf("codex BootMode = %q, want empty", l.BootMode())
	}
	if got := l.SpawnWorkdir("/tmp/boot", "/proj"); got != "/tmp/boot" {
		t.Errorf("SpawnWorkdir = %q, want /tmp/boot", got)
	}
}

// TestCodexLayout_AuthJSON_CopiesHostAuth pins that the planted auth.json
// carries the host's codex auth (CW-20261001-0021). CODEX_HOME=<bootDir>
// makes codex read auth from the boot dir only, so an empty plant fails
// every launch with 401. go-providers v0.26.0 renders auth.json empty
// unless the caller opts in; codexAuthJSONContent opts in.
//
// CODEX_HOME points at a fixture dir — the source lookup honors it, so the
// test never reads the real ~/.codex/auth.json.
func TestCodexLayout_AuthJSON_CopiesHostAuth(t *testing.T) {
	const fixture = `{"OPENAI_API_KEY":null,"tokens":{"access_token":"fixture-access","refresh_token":"fixture-refresh"}}` + "\n"
	codexHome := t.TempDir()
	if err := os.WriteFile(filepath.Join(codexHome, "auth.json"), []byte(fixture), 0o600); err != nil {
		t.Fatalf("write fixture auth.json: %v", err)
	}
	t.Setenv("CODEX_HOME", codexHome)

	profile := &store.AgentProfile{Name: "codex-auth", Slug: "codex-auth"}
	bootDir, err := codexLayout{}.Setup(SetupParams{SessionID: "s-auth", AgentProfile: profile})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(bootDir) })

	authPath := filepath.Join(bootDir, "auth.json")
	body, err := os.ReadFile(authPath) //nolint:gosec // reads a file this test just planted into a temp boot dir
	if err != nil {
		t.Fatalf("read auth.json: %v", err)
	}
	if string(body) != fixture {
		t.Errorf("planted auth.json = %q, want the host's auth.json %q", body, fixture)
	}

	// auth.json carries OAuth tokens — it must not be world-readable.
	info, err := os.Stat(authPath)
	if err != nil {
		t.Fatalf("stat auth.json: %v", err)
	}
	if perm := info.Mode().Perm(); perm != codexConfigFileMode {
		t.Errorf("auth.json mode = %o, want %o", perm, codexConfigFileMode)
	}
}

// TestCodexLayout_AuthJSON_NotLoggedIn pins the not-logged-in path: no
// host auth.json is not a Setup error. The boot dir still gets an empty
// auth.json and codex reports "Not logged in" at dispatch.
func TestCodexLayout_AuthJSON_NotLoggedIn(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())

	profile := &store.AgentProfile{Name: "codex-noauth", Slug: "codex-noauth"}
	bootDir, err := codexLayout{}.Setup(SetupParams{SessionID: "s-noauth", AgentProfile: profile})
	if err != nil {
		t.Fatalf("Setup with no host auth.json: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(bootDir) })

	body, err := os.ReadFile(filepath.Join(bootDir, "auth.json")) //nolint:gosec // reads a file this test just planted into a temp boot dir
	if err != nil {
		t.Fatalf("read auth.json: %v", err)
	}
	if len(body) != 0 {
		t.Errorf("auth.json = %q, want empty when the host is not logged in", body)
	}
}
