package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/go-agent-wrapper/adapters"
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

// go-providers v0.31.0+ projects Claude's --add-dir <project> in every
// mode. Nanite's launch path is not the projection, so its own workRootArgs
// is still the flag's only source: exactly one --add-dir, naming the work
// root (CW-20260930-0113).
func TestSelectNativeAdapter_ClaudeAddDirExactlyOnce(t *testing.T) {
	const workRoot = "/home/x/dev/project"
	for _, tc := range []struct {
		name string
		cli  provider.CLIAdapter
	}{
		{"production", provider.NewClaudeAdapterStreamingStdio()},
		{"developer mode", provider.NewClaudeAdapterDevStreamingStdio()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selected, err := selectNativeAdapter("claude", ModeLongLived, tc.cli, workRoot)
			if err != nil {
				t.Fatalf("selectNativeAdapter: %v", err)
			}
			args := selected.CLIAdapter().BuildArgs("prompt", "system", "session")
			var dirs []string
			for i, a := range args {
				if a == "--add-dir" && i+1 < len(args) {
					dirs = append(dirs, args[i+1])
				}
			}
			if len(dirs) != 1 || dirs[0] != workRoot {
				t.Fatalf("--add-dir values = %q in %#v, want exactly [%q]", dirs, args, workRoot)
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

// CW-20261001-0069: since go-providers v0.34.1 a per-turn argv ends with
// `-- <prompt>`, so untrusted turn text cannot be parsed as a flag. These
// pin the real argv Nanite's selected adapters build, with a turn that is
// itself a dangerous flag: Nanite's own additions never land after the
// `--`, the prompt is the only thing after it, and Claude's --add-dir
// appears exactly once.
func TestSelectNativeAdapter_PromptAfterEndOfOptions(t *testing.T) {
	const (
		workRoot = "/home/x/dev/project"
		turn     = "--dangerously-bypass-approvals-and-sandbox"
	)
	endOfOptions := func(args []string) int {
		for i, a := range args {
			if a == "--" {
				return i
			}
		}
		return -1
	}

	t.Run("codex exec", func(t *testing.T) {
		selected, err := selectNativeAdapter("codex", ModeLongLived, provider.NewCodexAdapter(), workRoot)
		if err != nil {
			t.Fatalf("selectNativeAdapter: %v", err)
		}
		args := selected.CLIAdapter().BuildArgs(turn, "system", "")
		dd := endOfOptions(args)
		if len(args) < 4 || args[0] != "exec" || dd != len(args)-2 || args[len(args)-1] != turn {
			t.Fatalf("codex argv = %q, want exec … -- <turn>", args)
		}
		jsonFlag := -1
		for i, a := range args {
			if a == "--json" {
				jsonFlag = i
			}
		}
		if jsonFlag < 0 || jsonFlag > dd {
			t.Fatalf("codex argv = %q, want --json before --", args)
		}
	})

	t.Run("opencode run", func(t *testing.T) {
		selected, err := selectNativeAdapter("opencode", ModeLongLived, provider.NewOpencodeAdapter(), workRoot)
		if err != nil {
			t.Fatalf("selectNativeAdapter: %v", err)
		}
		args := selected.CLIAdapter().BuildArgs(turn, "", "")
		dd := endOfOptions(args)
		if len(args) < 3 || args[0] != "run" || dd != len(args)-2 || args[len(args)-1] != turn {
			t.Fatalf("opencode argv = %q, want run … -- <turn>", args)
		}
	})

	for _, tc := range []struct {
		name string
		cli  provider.CLIAdapter
	}{
		{"claude streaming", provider.NewClaudeAdapterStreamingStdio()},
		{"claude streaming dev", provider.NewClaudeAdapterDevStreamingStdio()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selected, err := selectNativeAdapter("claude", ModeLongLived, tc.cli, workRoot)
			if err != nil {
				t.Fatalf("selectNativeAdapter: %v", err)
			}
			args := selected.CLIAdapter().BuildArgs(turn, "system", "")
			// Streaming stdio takes its turns on stdin: no prompt, so no
			// `--`, in argv, and --add-dir at the end is a flag.
			if dd := endOfOptions(args); dd >= 0 {
				t.Fatalf("claude streaming argv = %q, want no `--` (turns go over stdin)", args)
			}
			for _, a := range args {
				if a == turn {
					t.Fatalf("claude streaming argv = %q carries the turn text", args)
				}
			}
			if n := len(args); n < 2 || args[n-2] != "--add-dir" || args[n-1] != workRoot {
				t.Fatalf("claude argv = %q, want it to end --add-dir %s", args, workRoot)
			}
		})
	}
}

// The wrapper appends Selection.ExtraArgs after the adapter's whole argv, so
// on a per-turn launch they would follow `-- <prompt>`. Refused.
func TestCheckExtraArgsPlacement(t *testing.T) {
	if err := checkExtraArgsPlacement(adapters.LaunchStreamingStdio, []string{"--add-dir", "/r"}); err != nil {
		t.Fatalf("streaming stdio: %v", err)
	}
	if err := checkExtraArgsPlacement(adapters.LaunchSubprocessPerTurn, nil); err != nil {
		t.Fatalf("per-turn, no additions: %v", err)
	}
	if err := checkExtraArgsPlacement(adapters.LaunchSubprocessPerTurn, []string{"--add-dir", "/r"}); err == nil {
		t.Fatal("per-turn additions accepted; the wrapper would append them after `-- <prompt>`")
	}
}
