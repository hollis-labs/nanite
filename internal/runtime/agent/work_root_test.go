package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/store"
)

// CW-20261001-0020: a native CLI agent's cwd stays its boot dir, so the
// session's work root (Options.Workdir) has to reach it as an extra
// directory. These pin each carrier: claude's argv and settings, codex's
// config.toml, and the system prompt and boot.md that name the folder.

func TestSelectNativeAdapter_ClaudeArgvCarriesWorkRoot(t *testing.T) {
	const workRoot = "/home/x/dev/project"
	for _, tc := range []struct {
		name string
		cli  provider.CLIAdapter
	}{
		{"production", provider.NewClaudeAdapterStreamingStdio()},
		{"developer mode", provider.NewClaudeAdapterDevStreamingStdio()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selected, err := selectNativeAdapter("pty-claude", ModeLongLived, tc.cli, workRoot)
			if err != nil {
				t.Fatalf("selectNativeAdapter: %v", err)
			}
			got := selected.CLIAdapter().BuildArgs("", "", "")
			want := append(tc.cli.BuildArgs("", "", ""), "--add-dir", workRoot)
			if !equalStrings(got, want) {
				t.Fatalf("claude argv = %#v, want %#v", got, want)
			}
		})
	}
}

func TestSelectNativeAdapter_WorkRootAddsNoArgvForCodexOrEmptyRoot(t *testing.T) {
	codex := provider.NewCodexAdapter()
	selected, err := selectNativeAdapter("codex", ModeLongLived, codex, "/home/x/dev/project")
	if err != nil {
		t.Fatalf("selectNativeAdapter codex: %v", err)
	}
	if got, want := selected.CLIAdapter().BuildArgs("p", "s", ""), codex.BuildArgs("p", "s", ""); !equalStrings(got, want) {
		t.Fatalf("codex argv = %#v, want unchanged %#v (codex reaches the root through writable_roots)", got, want)
	}

	claude := provider.NewClaudeAdapterStreamingStdio()
	selected, err = selectNativeAdapter("claude", ModeLongLived, claude, "")
	if err != nil {
		t.Fatalf("selectNativeAdapter claude: %v", err)
	}
	if got, want := selected.CLIAdapter().BuildArgs("", "", ""), claude.BuildArgs("", "", ""); !equalStrings(got, want) {
		t.Fatalf("claude argv with no work root = %#v, want unchanged %#v", got, want)
	}
}

func TestBootdir_ClaudePlantsWorkRoot(t *testing.T) {
	workRoot := t.TempDir()
	extra := t.TempDir()
	sessID := "sess-claude-work-root"
	_, params := composeBootdirParams(&Dependencies{CLIWritableRoots: []string{extra}},
		Options{Provider: "claude", SessionID: sessID, Workdir: workRoot},
		&store.AgentProfile{Name: "worker", SystemPrompt: "base prompt"}, sessID)

	bootDir, err := claudeLayout{}.Setup(params)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(bootDir) })

	if got := (claudeLayout{}).SpawnWorkdir(bootDir, workRoot); got != bootDir {
		t.Fatalf("SpawnWorkdir = %q, want the boot dir %q (the planted CLAUDE.md loads from cwd)", got, bootDir)
	}

	raw, err := os.ReadFile(filepath.Join(bootDir, ".claude", "settings.json")) //nolint:gosec // reads a file this test just planted into a temp dir
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	var settings struct {
		Permissions struct {
			AdditionalDirectories []string `json:"additionalDirectories"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatalf("parse settings.json: %v\n%s", err, raw)
	}
	if got, want := settings.Permissions.AdditionalDirectories, []string{workRoot, extra}; !equalStrings(got, want) {
		t.Fatalf("additionalDirectories = %v, want %v", got, want)
	}

	assertFileNamesWorkRoot(t, filepath.Join(bootDir, "CLAUDE.md"), "## Project folder", workRoot)
	assertFileNamesWorkRoot(t, filepath.Join(bootDir, "boot.md"), "**Project folder:** "+workRoot, workRoot)
}

func TestBootdir_CodexPlantsWorkRoot(t *testing.T) {
	workRoot := t.TempDir()
	sessID := "sess-codex-work-root"
	_, params := composeBootdirParams(nil,
		Options{Provider: "codex", SessionID: sessID, Workdir: workRoot},
		&store.AgentProfile{Name: "worker", SystemPrompt: "base prompt"}, sessID)

	bootDir, err := codexLayout{}.Setup(params)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(bootDir) })

	raw, err := os.ReadFile(filepath.Join(bootDir, "config.toml")) //nolint:gosec // reads a file this test just planted into a temp dir
	if err != nil {
		t.Fatalf("read config.toml: %v", err)
	}
	if want := `writable_roots = ["` + workRoot + `"]`; !strings.Contains(string(raw), want) {
		t.Fatalf("config.toml missing %q\n--- body ---\n%s", want, raw)
	}
	assertFileNamesWorkRoot(t, filepath.Join(bootDir, "AGENTS.md"), "## Project folder", workRoot)
}

func TestResolveSystemPrompt_NamesWorkRootOnlyWhenSet(t *testing.T) {
	profile := &store.AgentProfile{SystemPrompt: "base prompt"}
	without := ResolveSystemPrompt("", profile, ModeLongLived, "", nil, "")
	if strings.Contains(without, "## Project folder") {
		t.Fatalf("prompt with no work root names a project folder:\n%s", without)
	}
	with := ResolveSystemPrompt("", profile, ModeLongLived, "", nil, "/home/x/dev/project")
	section := strings.Index(with, "## Project folder")
	if section < 0 || !strings.Contains(with[section:], "/home/x/dev/project") {
		t.Fatalf("prompt does not name the work root:\n%s", with)
	}
	if base := strings.Index(with, "base prompt"); base < 0 || base > section {
		t.Fatalf("project folder section should follow the base prompt:\n%s", with)
	}
	if got := resolveBootPrompt(profile, Options{Mode: ModeLongLived, Workdir: "/home/x/dev/project"}); got != with {
		t.Fatalf("resolveBootPrompt and ResolveSystemPrompt disagree:\n%s\n---\n%s", got, with)
	}
}

func assertFileNamesWorkRoot(t *testing.T, path, marker, workRoot string) {
	t.Helper()
	body, err := os.ReadFile(path) //nolint:gosec // reads a file this test just planted into a temp dir
	if err != nil {
		t.Fatalf("read %s: %v", filepath.Base(path), err)
	}
	if !strings.Contains(string(body), marker) || !strings.Contains(string(body), workRoot) {
		t.Fatalf("%s does not name the work root %q (marker %q):\n%s", filepath.Base(path), workRoot, marker, body)
	}
}
