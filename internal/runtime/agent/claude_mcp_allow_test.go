package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/go-agent-wrapper/adapters"
)

// CW-20261001-0411: a Nanite-launched Claude may call the planted MCP
// server's tools and no others. See claude_mcp_allow.go for why the rule is on
// argv and not in the planted settings.json.

const wantAllowFlag = "--allowedTools=mcp__nanite__*"

var testMCPConfig = MCPConfig{BinaryPath: "/usr/local/bin/nanite", DBPath: "/data/main.db"}

func TestClaudeMCPAllowArgs(t *testing.T) {
	custom := testMCPConfig
	custom.ServerID = "harness"
	for _, tc := range []struct {
		name    string
		runtime runtimes.ID
		bootDir string
		cfg     MCPConfig
		want    []string
	}{
		{"claude with a planted server", runtimes.Claude, "/boot", testMCPConfig, []string{wantAllowFlag}},
		{"the rule follows a configured server id", runtimes.Claude, "/boot", custom, []string{"--allowedTools=mcp__harness__*"}},
		{"no database path: no server is planted, so nothing to allow", runtimes.Claude, "/boot", MCPConfig{}, nil},
		{"ACP claude plants no boot dir", runtimes.Claude, "", testMCPConfig, nil},
		{"codex is not claude", runtimes.Codex, "/boot", testMCPConfig, nil},
		{"opencode is not claude", runtimes.OpenCode, "/boot", testMCPConfig, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := claudeMCPAllowArgs(tc.runtime, tc.bootDir, tc.cfg); !slices.Equal(got, tc.want) {
				t.Errorf("claudeMCPAllowArgs = %q, want %q", got, tc.want)
			}
		})
	}
}

// assertAllowArgv checks a Claude argv against the contract: the one rule,
// spelled as a single `=` entry so it can never swallow what follows, exactly
// once, and before any "--".
func assertAllowArgv(t *testing.T, argv []string) {
	t.Helper()
	var flags []string
	for _, a := range argv {
		if strings.HasPrefix(a, "--allowedTools") || strings.HasPrefix(a, "--allowed-tools") || strings.HasPrefix(a, "--disallowedTools") {
			flags = append(flags, a)
		}
	}
	if !slices.Equal(flags, []string{wantAllowFlag}) {
		t.Errorf("tool permission flags = %q, want exactly %q (argv %q)", flags, wantAllowFlag, argv)
	}
	if sep := slices.Index(argv, "--"); sep >= 0 && slices.Index(argv, wantAllowFlag) > sep {
		t.Errorf("the allow rule comes after \"--\" in %q", argv)
	}
}

// The production selection path, in every argv shape Nanite or go-providers can
// build, carries the rule once, after the strict-MCP flags and before the
// variadic --add-dir, with or without strict MCP.
func TestSelectAdapter_ClaudeArgvCarriesTheMCPAllowRule(t *testing.T) {
	boot := plantedBootDir(t)
	planted := filepath.Join(boot, ".mcp.json")
	deps := func(dev bool) *Dependencies {
		return &Dependencies{DeveloperMode: dev, MCPConfig: testMCPConfig}
	}

	for _, tc := range []struct {
		name   string
		mode   runtimes.Mode
		dev    bool
		strict string
	}{
		{"streaming", runtimes.ModeStreamingStdio, false, ""},
		{"streaming, developer mode", runtimes.ModeStreamingStdio, true, ""},
		{"per turn, whose prompt follows a --", runtimes.ModeSubprocessPerTurn, false, ""},
		{"streaming, strict MCP off", runtimes.ModeStreamingStdio, false, "0"},
		{"per turn, strict MCP off", runtimes.ModeSubprocessPerTurn, false, "0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(StrictMCPEnv, tc.strict)
			sel := RuntimeSelection{Runtime: runtimes.Claude, Mode: tc.mode}
			a, err := selectAdapter(deps(tc.dev), sel, "/work", boot)
			if err != nil {
				t.Fatalf("selectAdapter: %v", err)
			}
			argv := a.(adapters.RuntimeAdapter).CLIAdapter().BuildArgs("do the thing", "", "")
			assertAllowArgv(t, argv)

			allow := slices.Index(argv, wantAllowFlag)
			addDir := slices.Index(argv, "--add-dir")
			if addDir < 0 || argv[addDir+1] != "/work" {
				t.Fatalf("work-root --add-dir missing from %q", argv)
			}
			// The variadic --add-dir list stays after the rule, so nothing joins it.
			if allow > addDir {
				t.Errorf("the allow rule is after --add-dir in %q", argv)
			}
			if tc.strict == "" {
				assertStrictArgv(t, argv, planted)
				if strict := slices.Index(argv, "--strict-mcp-config"); strict > allow {
					t.Errorf("the allow rule is before --strict-mcp-config in %q", argv)
				}
				want := []string{"--mcp-config", planted, "--strict-mcp-config", wantAllowFlag, "--add-dir", "/work"}
				if i := slices.Index(argv, "--mcp-config"); !slices.Equal(argv[i:i+len(want)], want) {
					t.Errorf("extra argv = %q, want it to read %q", argv[i:], want)
				}
			} else if slices.Contains(argv, "--strict-mcp-config") || slices.Contains(argv, "--mcp-config") {
				t.Errorf("kill switch left MCP flags in %q", argv)
			} else if want := []string{wantAllowFlag, "--add-dir", "/work"}; !slices.Equal(argv[allow:allow+len(want)], want) {
				t.Errorf("extra argv with strict MCP off = %q, want it to read %q", argv[allow:], want)
			}

			switch tc.mode {
			case runtimes.ModeSubprocessPerTurn:
				if argv[len(argv)-1] != "do the thing" || argv[len(argv)-2] != "--" {
					t.Errorf("the prompt does not follow a -- at the end of %q", argv)
				}
			case runtimes.ModeStreamingStdio:
				if tc.dev {
					if argv[len(argv)-1] != "--dangerously-skip-permissions" {
						t.Errorf("developer flag is not last in %q", argv)
					}
				} else if argv[len(argv)-2] != "--add-dir" {
					t.Errorf("--add-dir work root is not last in %q", argv)
				}
			}
		})
	}
}

// With no work root the rule is the last extra, and the per-turn prompt after
// the -- is still the prompt.
func TestSelectAdapter_ClaudeAllowRuleWithoutWorkRoot(t *testing.T) {
	boot := plantedBootDir(t)
	t.Setenv(StrictMCPEnv, "0")
	sel := RuntimeSelection{Runtime: runtimes.Claude, Mode: runtimes.ModeSubprocessPerTurn}
	a, err := selectAdapter(&Dependencies{MCPConfig: testMCPConfig}, sel, "", boot)
	if err != nil {
		t.Fatalf("selectAdapter: %v", err)
	}
	argv := a.(adapters.RuntimeAdapter).CLIAdapter().BuildArgs("do the thing", "", "")
	assertAllowArgv(t, argv)
	if argv[len(argv)-1] != "do the thing" || argv[len(argv)-2] != "--" {
		t.Errorf("the prompt does not follow a -- at the end of %q", argv)
	}
}

// A launch with no planted server, and the other runtimes, get no rule.
func TestSelectAdapter_NoMCPAllowRuleWhereNothingIsPlanted(t *testing.T) {
	boot := plantedBootDir(t)
	a, err := selectAdapter(&Dependencies{}, RuntimeSelection{Runtime: runtimes.Claude, Mode: runtimes.ModeStreamingStdio}, "/work", boot)
	if err != nil {
		t.Fatal(err)
	}
	if argv := a.(adapters.RuntimeAdapter).CLIAdapter().BuildArgs("p", "", ""); slices.ContainsFunc(argv, func(s string) bool { return strings.HasPrefix(s, "--allowedTools") }) {
		t.Errorf("no MCP database path, yet argv has an allow rule: %q", argv)
	}
	for _, rt := range []runtimes.ID{runtimes.Codex, runtimes.OpenCode} {
		sel, err := selectRuntime(string(rt), nil)
		if err != nil {
			t.Fatal(err)
		}
		a, err := selectAdapter(&Dependencies{MCPConfig: testMCPConfig}, sel, "/work", boot)
		if err != nil {
			t.Fatalf("%s: %v", rt, err)
		}
		argv := a.(adapters.RuntimeAdapter).CLIAdapter().BuildArgs("do the thing", "", "")
		if slices.ContainsFunc(argv, func(s string) bool { return strings.HasPrefix(s, "--allowedTools") }) {
			t.Errorf("%s argv carries claude's allow rule: %q", rt, argv)
		}
	}
}

// settingsPermissionKeys returns the planted settings.json's top-level keys
// and its permissions keys.
func settingsKeys(t *testing.T, path string) (top, permissions []string) {
	t.Helper()
	raw, err := os.ReadFile(path) //nolint:gosec // a file this test just had planted into a temp dir
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse settings.json: %v\n%s", err, raw)
	}
	var perms map[string]json.RawMessage
	if err := json.Unmarshal(doc["permissions"], &perms); err != nil {
		t.Fatalf("parse permissions: %v\n%s", err, raw)
	}
	for k := range doc {
		top = append(top, k)
	}
	for k := range perms {
		permissions = append(permissions, k)
	}
	slices.Sort(top)
	slices.Sort(permissions)
	return top, permissions
}

// The planted settings.json grants nothing: no allow list (a project allow is
// ignored in an untrusted workspace, so it would only print a warning), no
// extra tools or servers, and the keys it carried before.
func TestClaudeSettingsPlantNoAllowRule(t *testing.T) {
	for _, withDirs := range []bool{false, true} {
		var dirs []string
		if withDirs {
			dirs = []string{t.TempDir()}
		}
		body, err := claudeProviderConfigContent(dirs, nil)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "settings.json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		top, perms := settingsKeys(t, path)
		if !slices.Equal(top, []string{"permissions"}) {
			t.Errorf("settings keys = %v, want only permissions", top)
		}
		want := []string{"defaultMode"}
		if withDirs {
			want = []string{"additionalDirectories", "defaultMode"}
		}
		if !slices.Equal(perms, want) {
			t.Errorf("permissions keys = %v, want %v (no allow list)", perms, want)
		}
	}
}

// Every Claude launch mode, booted end to end against a fake claude, carries
// the rule exactly once, with strict MCP on or off, and plants a settings.json
// that grants nothing. ModeResume is the mid-session re-boot of a recovered
// chat agent.
func TestBoot_ClaudeLaunchCarriesTheMCPAllowRuleInEveryMode(t *testing.T) {
	for _, strict := range []bool{true, false} {
		for _, mode := range []Mode{ModeLongLived, ModeOneShot, ModeResume, ModeSubagent, ModeBackground} {
			name := mode.String()
			if !strict {
				name += "/strict MCP off"
			}
			t.Run(name, func(t *testing.T) {
				t.Setenv(StrictMCPEnv, map[bool]string{true: "", false: "0"}[strict])
				sess, rec := bootFakeClaude(t, mode, strict)

				argv := strings.Split(strings.TrimSpace(string(waitForFile(t, filepath.Join(rec, "argv")))), "\n")
				assertAllowArgv(t, argv)
				if strict != slices.Contains(argv, "--strict-mcp-config") {
					t.Errorf("strict = %v but argv is %q", strict, argv)
				}
				if i, j := slices.Index(argv, wantAllowFlag), slices.Index(argv, "--add-dir"); j >= 0 && i > j {
					t.Errorf("the allow rule is after the variadic --add-dir in %q", argv)
				}

				top, perms := settingsKeys(t, filepath.Join(sess.BootDir, ".claude", "settings.json"))
				if !slices.Equal(top, []string{"permissions"}) || slices.Contains(perms, "allow") {
					t.Errorf("planted settings carry keys %v / permissions %v, want no allow list", top, perms)
				}
			})
		}
	}
}
