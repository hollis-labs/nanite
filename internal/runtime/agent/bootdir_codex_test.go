package agent

import (
	"errors"
	"io/fs"
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
	// plus config.toml and the auth.json link. Lstat: under TestMain's
	// empty CODEX_HOME the auth.json link dangles by design.
	for _, p := range []string{"boot.md", ".sandbox/agent-context.md", ".sandbox/envelope-schema.md", ".mcp.json", "config.toml", "auth.json"} {
		if _, err := os.Lstat(filepath.Join(bootDir, p)); err != nil {
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

// hostCodexLogin points CODEX_HOME at a fresh fixture dir, optionally
// holding a 0600 auth.json, and returns the host auth.json path. No test
// touches the real ~/.codex.
func hostCodexLogin(t *testing.T, loggedIn bool, content string) string {
	t.Helper()
	codexHome := t.TempDir()
	hostAuth := filepath.Join(codexHome, "auth.json")
	if loggedIn {
		if err := os.WriteFile(hostAuth, []byte(content), 0o600); err != nil {
			t.Fatalf("write fixture auth.json: %v", err)
		}
	}
	t.Setenv("CODEX_HOME", codexHome)
	return hostAuth
}

func setupCodexBootDir(t *testing.T, session string) string {
	t.Helper()
	profile := &store.AgentProfile{Name: "codex-auth", Slug: "codex-auth"}
	bootDir, err := codexLayout{}.Setup(SetupParams{SessionID: session, AgentProfile: profile})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(bootDir) })
	return bootDir
}

// assertAuthLink fails unless <bootDir>/auth.json is a symlink to target.
func assertAuthLink(t *testing.T, bootDir, target string) {
	t.Helper()
	planted := filepath.Join(bootDir, "auth.json")
	info, err := os.Lstat(planted)
	if err != nil {
		t.Fatalf("lstat auth.json: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("auth.json mode = %v, want a symlink to the host login", info.Mode())
	}
	if got, err := os.Readlink(planted); err != nil || got != target {
		t.Fatalf("auth.json -> %q (err %v), want %q", got, err, target)
	}
}

// TestCodexLayout_AuthJSON_LinksHostLogin pins CW-20261001-0027: the
// planted auth.json is a symlink to the host's login, so codex reads the
// host's credentials, and a token refresh — which codex writes in place
// with truncate, through the link — lands in the host's file rather than
// in a boot-dir copy that dies with the session. The refresh below
// mirrors codex's FileAuthStorage::save (open truncate+write, no rename).
func TestCodexLayout_AuthJSON_LinksHostLogin(t *testing.T) {
	const fixture = `{"tokens":{"access_token":"fixture-access","refresh_token":"fixture-refresh-1"}}`
	hostAuth := hostCodexLogin(t, true, fixture)
	bootDir := setupCodexBootDir(t, "s-auth")
	assertAuthLink(t, bootDir, hostAuth)

	planted := filepath.Join(bootDir, "auth.json")
	if body, err := os.ReadFile(planted); err != nil || string(body) != fixture { //nolint:gosec // reads a link this test just planted into a temp boot dir
		t.Fatalf("auth.json reads %q (err %v), want the host login %q", body, err, fixture)
	}

	// A refresh inside the session, written the way codex writes it.
	const refreshed = `{"tokens":{"access_token":"fixture-access-2","refresh_token":"fixture-refresh-2"}}`
	f, err := os.OpenFile(planted, os.O_WRONLY|os.O_TRUNC|os.O_CREATE, 0o600) //nolint:gosec // writes through a link this test planted to its own fixture
	if err != nil {
		t.Fatalf("open auth.json for the refresh: %v", err)
	}
	if _, err = f.WriteString(refreshed); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	if body, _ := os.ReadFile(hostAuth); string(body) != refreshed { //nolint:gosec // reads this test's own fixture
		t.Errorf("host auth.json = %q after the refresh, want %q", body, refreshed)
	}
	assertAuthLink(t, bootDir, hostAuth)
	info, err := os.Stat(hostAuth)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("host auth.json mode = %o after the refresh, want the user's 0600", perm)
	}

	// A re-plant (crash-recovery Populate) keeps the link and never writes
	// through it: auth.json is outside the materialize engine's tree.
	if _, err := (codexLayout{}).Populate(bootDir, SetupParams{SessionID: "s-auth", AgentProfile: &store.AgentProfile{Name: "codex-auth", Slug: "codex-auth"}}); err != nil {
		t.Fatalf("re-Populate: %v", err)
	}
	assertAuthLink(t, bootDir, hostAuth)
	if body, _ := os.ReadFile(hostAuth); string(body) != refreshed { //nolint:gosec // reads this test's own fixture
		t.Errorf("host auth.json = %q after a re-plant, want it untouched: %q", body, refreshed)
	}
}

// TestCodexLayout_AuthJSON_NotLoggedIn pins the not-logged-in decision: the
// link is planted anyway and dangles, which reads exactly like a missing
// file (codex reports "Not logged in" at dispatch), and Setup does not
// fail. A login on the host afterwards is picked up through the same link.
func TestCodexLayout_AuthJSON_NotLoggedIn(t *testing.T) {
	hostAuth := hostCodexLogin(t, false, "")
	bootDir := setupCodexBootDir(t, "s-noauth")
	assertAuthLink(t, bootDir, hostAuth)

	planted := filepath.Join(bootDir, "auth.json")
	if _, err := os.Stat(planted); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("stat through the link = %v, want not-exist (codex: not logged in)", err)
	}

	const login = `{"OPENAI_API_KEY":"fixture-key"}`
	if err := os.WriteFile(hostAuth, []byte(login), 0o600); err != nil {
		t.Fatal(err)
	}
	if body, err := os.ReadFile(planted); err != nil || string(body) != login { //nolint:gosec // reads a link this test just planted into a temp boot dir
		t.Fatalf("auth.json reads %q (err %v) after the host login, want %q", body, err, login)
	}
}

// A boot dir planted before CW-20261001-0027 holds a snapshot copy. A
// re-plant replaces it with the link, so no credential copy survives.
func TestCodexLayout_AuthJSON_ReplacesSnapshotCopy(t *testing.T) {
	hostAuth := hostCodexLogin(t, true, `{"OPENAI_API_KEY":"fixture-key"}`)
	bootDir := setupCodexBootDir(t, "s-copy")
	planted := filepath.Join(bootDir, "auth.json")
	if err := os.Remove(planted); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(planted, []byte(`{"OPENAI_API_KEY":"stale-copy"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := linkCodexHostAuth(bootDir); err != nil {
		t.Fatalf("linkCodexHostAuth: %v", err)
	}
	assertAuthLink(t, bootDir, hostAuth)
}

// A relative CODEX_HOME resolves against Nanite's working directory, not
// against the boot dir the link lives in.
func TestCodexHostAuthPath_AbsoluteForRelativeCodexHome(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("CODEX_HOME", "rel-codex-home")
	want := filepath.Join(dir, "rel-codex-home", "auth.json")
	if got := codexHostAuthPath(); got != want {
		t.Fatalf("codexHostAuthPath() = %q, want %q", got, want)
	}
}
