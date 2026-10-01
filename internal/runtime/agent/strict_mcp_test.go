package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/go-agent-wrapper/adapters"
)

// plantedBootDir is a boot dir with a planted .mcp.json, as layout.Setup
// leaves it.
func plantedBootDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".mcp.json"), []byte(`{"mcpServers":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestStrictMCPArgs(t *testing.T) {
	planted := plantedBootDir(t)
	bare := t.TempDir() // no .mcp.json: MCP planting disabled
	want := []string{"--mcp-config", filepath.Join(planted, ".mcp.json"), "--strict-mcp-config"}

	for _, tc := range []struct {
		name    string
		runtime runtimes.ID
		bootDir string
		env     string
		want    []string
	}{
		{"claude with a planted .mcp.json", runtimes.Claude, planted, "", want},
		{"claude with no .mcp.json is still strict: no server rather than the operator's", runtimes.Claude, bare, "", []string{"--strict-mcp-config"}},
		{"ACP claude plants no boot dir", runtimes.Claude, "", "", nil},
		{"codex is not claude", runtimes.Codex, planted, "", nil},
		{"opencode is not claude", runtimes.OpenCode, planted, "", nil},
		{"kill switch 0", runtimes.Claude, planted, "0", nil},
		{"kill switch false", runtimes.Claude, planted, " False ", nil},
		{"1 stays strict", runtimes.Claude, planted, "1", want},
		{"a typo stays strict", runtimes.Claude, planted, "off", want},
		{"empty stays strict", runtimes.Claude, planted, "", want},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(StrictMCPEnv, tc.env)
			if got := strictMCPArgs(tc.runtime, tc.bootDir); !slices.Equal(got, tc.want) {
				t.Errorf("strictMCPArgs = %q, want %q", got, tc.want)
			}
		})
	}
}

// count is how many times arg appears in argv.
func count(argv []string, arg string) int {
	n := 0
	for _, a := range argv {
		if a == arg {
			n++
		}
	}
	return n
}

// assertStrictArgv checks a Claude argv against the contract: both flags
// exactly once, --mcp-config's one value is planted, that value is followed
// by a flag (never a value or positional the variadic flag would swallow),
// and both sit before any "--".
func assertStrictArgv(t *testing.T, argv []string, planted string) {
	t.Helper()
	if n := count(argv, "--strict-mcp-config"); n != 1 {
		t.Errorf("--strict-mcp-config appears %d times in %q, want 1", n, argv)
	}
	if n := count(argv, "--mcp-config"); n != 1 {
		t.Errorf("--mcp-config appears %d times in %q, want 1", n, argv)
		return
	}
	i := slices.Index(argv, "--mcp-config")
	if i+1 >= len(argv) || argv[i+1] != planted {
		t.Errorf("--mcp-config value = %q, want %q (argv %q)", argv[min(i+1, len(argv)-1)], planted, argv)
	}
	if i+2 < len(argv) && !strings.HasPrefix(argv[i+2], "--") {
		t.Errorf("--mcp-config's value is followed by %q, which the variadic flag would take as another config (argv %q)", argv[i+2], argv)
	}
	if sep := slices.Index(argv, "--"); sep >= 0 {
		if slices.Index(argv, "--strict-mcp-config") > sep || i > sep {
			t.Errorf("MCP flags come after \"--\" in %q", argv)
		}
	}
}

// The production selection path for a native Claude launch, in every shape
// Nanite or go-providers can build, carries the strict flags exactly once
// and leaves the stream-json core and the work-root --add-dir as before.
func TestSelectAdapter_ClaudeArgvIsStrict(t *testing.T) {
	boot := plantedBootDir(t)
	planted := filepath.Join(boot, ".mcp.json")

	for _, tc := range []struct {
		name string
		mode runtimes.Mode
		dev  bool
	}{
		{"streaming", runtimes.ModeStreamingStdio, false},
		{"streaming, developer mode", runtimes.ModeStreamingStdio, true},
		{"per turn, whose prompt follows a --", runtimes.ModeSubprocessPerTurn, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(StrictMCPEnv, "")
			sel := RuntimeSelection{Runtime: runtimes.Claude, Mode: tc.mode}
			a, err := selectAdapter(&Dependencies{DeveloperMode: tc.dev}, sel, "/work", boot)
			if err != nil {
				t.Fatalf("selectAdapter: %v", err)
			}
			argv := a.(adapters.RuntimeAdapter).CLIAdapter().BuildArgs("do the thing", "", "")
			assertStrictArgv(t, argv, planted)
			if i := slices.Index(argv, "--add-dir"); i < 0 || argv[i+1] != "/work" {
				t.Errorf("work-root --add-dir missing from %q", argv)
			}
			if tc.mode == runtimes.ModeStreamingStdio {
				core := []string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose"}
				if !slices.Equal(argv[:len(core)], core) {
					t.Errorf("stream-json core changed: %q", argv)
				}
				// The --add-dir list is variadic: it must stay the last
				// flag so nothing joins it.
				if tc.dev {
					if argv[len(argv)-1] != "--dangerously-skip-permissions" {
						t.Errorf("developer flag is not last in %q", argv)
					}
				} else if argv[len(argv)-2] != "--add-dir" {
					t.Errorf("--add-dir work root is not last in %q", argv)
				}
			}

			// The kill switch restores the argv Nanite built before.
			t.Setenv(StrictMCPEnv, "0")
			a, err = selectAdapter(&Dependencies{DeveloperMode: tc.dev}, sel, "/work", boot)
			if err != nil {
				t.Fatalf("selectAdapter (kill switch): %v", err)
			}
			off := a.(adapters.RuntimeAdapter).CLIAdapter().BuildArgs("do the thing", "", "")
			if slices.Contains(off, "--strict-mcp-config") || slices.Contains(off, "--mcp-config") {
				t.Errorf("kill switch left MCP flags in %q", off)
			}
		})
	}
}

// Codex and OpenCode argv are untouched.
func TestSelectAdapter_OtherRuntimesNotStrict(t *testing.T) {
	boot := plantedBootDir(t)
	for _, rt := range []runtimes.ID{runtimes.Codex, runtimes.OpenCode} {
		sel, err := selectRuntime(string(rt), nil)
		if err != nil {
			t.Fatal(err)
		}
		a, err := selectAdapter(&Dependencies{}, sel, "/work", boot)
		if err != nil {
			t.Fatalf("%s: %v", rt, err)
		}
		argv := a.(adapters.RuntimeAdapter).CLIAdapter().BuildArgs("do the thing", "", "")
		if slices.Contains(argv, "--strict-mcp-config") || slices.Contains(argv, "--mcp-config") {
			t.Errorf("%s argv carries claude's MCP flags: %q", rt, argv)
		}
	}
}

func TestWarnIfStrictMCPOff(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	t.Setenv(StrictMCPEnv, "")
	WarnIfStrictMCPOff()
	if buf.Len() != 0 {
		t.Errorf("strict is on, but it logged: %s", buf.String())
	}

	t.Setenv(StrictMCPEnv, "0")
	WarnIfStrictMCPOff()
	out := buf.String()
	if !strings.Contains(out, "level=WARN") || !strings.Contains(out, StrictMCPEnv) {
		t.Errorf("kill switch on logged %q, want a WARN naming %s", out, StrictMCPEnv)
	}
}

// fakeStrictClaudeScript stands in for claude. It records its argv and, when
// strict is "yes", refuses to start unless it was given exactly one
// --strict-mcp-config and exactly one --mcp-config naming a file that
// exists: anything else is the unexpected configuration a real claude would
// merge user-level servers into. A launch that passes is long-lived, and the
// planted file it was given is copied beside the argv.
const fakeStrictClaudeScript = `#!/bin/sh
rec=RECDIR
printf '%s\n' "$@" > "$rec/argv"
if [ STRICT = yes ]; then
  strict=0; cfgs=0; cfg=""; prev=""
  for a in "$@"; do
    [ "$a" = "--strict-mcp-config" ] && strict=$((strict+1))
    if [ "$a" = "--mcp-config" ]; then cfgs=$((cfgs+1)); fi
    [ "$prev" = "--mcp-config" ] && cfg="$a"
    prev="$a"
  done
  if [ "$strict" != 1 ] || [ "$cfgs" != 1 ] || [ ! -f "$cfg" ]; then
    echo "strict=$strict mcp-config=$cfgs file=$cfg" > "$rec/bad"
    exit 3
  fi
  cp "$cfg" "$rec/mcp.json"
fi
echo '{"type":"system","subtype":"init","session_id":"claude-fake-strict-1"}'
sleep 30
`

// bootFakeClaude boots a Claude agent in mode against fakeStrictClaudeScript
// and returns the session and the directory the fake recorded into.
func bootFakeClaude(t *testing.T, mode Mode, strict bool) (*Session, string) {
	t.Helper()
	rec := t.TempDir()
	script := strings.NewReplacer("RECDIR", "'"+rec+"'", "STRICT", map[bool]string{true: "yes", false: "no"}[strict]).Replace(fakeStrictClaudeScript)
	path := filepath.Join(t.TempDir(), "fake-claude.sh")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil { //nolint:gosec // an executable test fixture in t.TempDir()
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CLI_PATH", path)

	deps, rtStore := makeBootDeps(t, "claude")
	deps.NativeCLIAdapter = nil // the registry's own streaming Claude adapter
	deps.MCPConfig = MCPConfig{
		BinaryPath: "/usr/local/bin/nanite",
		DBPath:     "/data/main.db",
		ServerID:   "nanite",
		APIBaseURL: "http://127.0.0.1:8090",
	}
	rtStore.checkpoint = &RuntimeCheckpoint{ID: "cp-1", ProviderSessionID: "claude-sess-original"}
	opts := Options{Mode: mode, Provider: "claude", Workdir: t.TempDir(), Role: "executor"}
	switch mode {
	case ModeSubagent:
		opts.ParentSessionID = "parent-sess-1"
	case ModeResume:
		opts.ResumeFromCheckpoint = "cp-1"
	case ModeLongLived, ModeOneShot, ModeBackground:
		// Nothing beyond the provider and work dir.
	}
	sess, err := Boot(context.Background(), deps, opts)
	if err != nil {
		bad, _ := os.ReadFile(filepath.Join(rec, "bad")) //nolint:gosec // a file in the test's own temp dir
		t.Fatalf("Boot(%s): %v (fake claude saw: %s)", mode, err, bad)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = sess.Stop(ctx)
	})
	return sess, rec
}

// waitForFile waits for path to exist and returns its content.
func waitForFile(t *testing.T, path string) []byte {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil && len(b) > 0 { //nolint:gosec // a file in the test's own temp dir
			return b
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s never appeared", path)
	return nil
}

// Every Claude launch mode, booted end to end against a fake claude, is
// launched strict: the fake refuses to start on anything else, and the file
// it was given is the planted .mcp.json naming Nanite's own server only.
func TestBoot_ClaudeLaunchIsStrictInEveryMode(t *testing.T) {
	for _, mode := range []Mode{ModeLongLived, ModeOneShot, ModeResume, ModeSubagent, ModeBackground} {
		t.Run(mode.String(), func(t *testing.T) {
			t.Setenv(StrictMCPEnv, "")
			sess, rec := bootFakeClaude(t, mode, true)

			argv := strings.Split(strings.TrimSpace(string(waitForFile(t, filepath.Join(rec, "argv")))), "\n")
			planted := filepath.Join(sess.BootDir, ".mcp.json")
			assertStrictArgv(t, argv, planted)

			var doc struct {
				MCPServers map[string]struct {
					Env map[string]string `json:"env"`
				} `json:"mcpServers"`
			}
			if err := json.Unmarshal(waitForFile(t, filepath.Join(rec, "mcp.json")), &doc); err != nil {
				t.Fatalf("the file --mcp-config named is not JSON: %v", err)
			}
			if len(doc.MCPServers) != 1 || doc.MCPServers["nanite"].Env["NANITE_API_URL"] == "" {
				t.Errorf("planted servers = %+v, want only nanite, with NANITE_API_URL", doc.MCPServers)
			}
		})
	}
}

// NANITE_CLAUDE_STRICT_MCP=0 launches Claude as it was before: neither flag.
func TestBoot_ClaudeKillSwitchRestoresUnrestrictedLaunch(t *testing.T) {
	t.Setenv(StrictMCPEnv, "0")
	_, rec := bootFakeClaude(t, ModeLongLived, false)
	argv := strings.Split(strings.TrimSpace(string(waitForFile(t, filepath.Join(rec, "argv")))), "\n")
	if slices.Contains(argv, "--strict-mcp-config") || slices.Contains(argv, "--mcp-config") {
		t.Errorf("kill switch left MCP flags in %q", argv)
	}
}
