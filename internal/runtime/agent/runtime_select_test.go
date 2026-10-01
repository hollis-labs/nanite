package agent

import (
	"testing"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/go-agent-wrapper/adapters"
	"github.com/hollis-labs/go-agent-wrapper/launch"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/store"
)

// selectNativeAdapter drives the production selection path (selectRuntime
// + selectAdapter) with cli as the host adapter through the
// NativeCLIAdapter seam, and returns the native RuntimeAdapter. The tests
// that predate launch.Select keep their shape through it.
func selectNativeAdapter(providerName string, _ Mode, cli provider.CLIAdapter, workRoot string) (adapters.RuntimeAdapter, error) {
	sel, err := selectRuntime(providerName, nil)
	if err != nil {
		return nil, err
	}
	deps := &Dependencies{NativeCLIAdapter: func(runtimes.ID) provider.CLIAdapter { return cli }}
	a, err := selectAdapter(deps, sel, workRoot)
	if err != nil {
		return nil, err
	}
	return a.(adapters.RuntimeAdapter), nil
}

// The launch decision: explicit native modes for the runtimes Nanite drives
// natively (Codex per turn, never the registry's jsonrpc-stdio default),
// ACP when the profile asks for it, and the registry default otherwise.
func TestSelectRuntime(t *testing.T) {
	acp := &store.AgentProfile{Protocol: "acp"}
	acpTCP := &store.AgentProfile{Protocol: "acp", Transport: "tcp"}
	for _, tc := range []struct {
		provider string
		profile  *store.AgentProfile
		want     RuntimeSelection
	}{
		{"claude", nil, RuntimeSelection{runtimes.Claude, runtimes.ModeStreamingStdio}},
		{"pty-claude", nil, RuntimeSelection{runtimes.Claude, runtimes.ModeStreamingStdio}},
		{"claude-code", nil, RuntimeSelection{runtimes.Claude, runtimes.ModeStreamingStdio}},
		{"codex", nil, RuntimeSelection{runtimes.Codex, runtimes.ModeSubprocessPerTurn}},
		{"sub-codex", nil, RuntimeSelection{runtimes.Codex, runtimes.ModeSubprocessPerTurn}},
		{"opencode", nil, RuntimeSelection{runtimes.OpenCode, runtimes.ModeSubprocessPerTurn}},
		{"copilot", nil, RuntimeSelection{runtimes.Copilot, runtimes.ModeACPStdio}},
		{"pi", nil, RuntimeSelection{runtimes.Pi, runtimes.ModeACPStdio}},
		{"agy", nil, RuntimeSelection{runtimes.Antigravity, runtimes.ModeSubprocessPerTurn}},
		{"claude", acp, RuntimeSelection{runtimes.Claude, runtimes.ModeACPStdio}},
		{"copilot", acpTCP, RuntimeSelection{runtimes.Copilot, runtimes.ModeACPTCP}},
		// The stdio-only bridges ignore a tcp transport, as before.
		{"claude", acpTCP, RuntimeSelection{runtimes.Claude, runtimes.ModeACPStdio}},
	} {
		got, err := selectRuntime(tc.provider, tc.profile)
		if err != nil {
			t.Errorf("selectRuntime(%q): %v", tc.provider, err)
			continue
		}
		if got != tc.want {
			t.Errorf("selectRuntime(%q, %+v) = %+v, want %+v", tc.provider, tc.profile, got, tc.want)
		}
	}
	if _, err := selectRuntime("nonsense-provider", nil); err == nil {
		t.Error("selectRuntime(nonsense-provider): want an error")
	}
}

// Chat routing and the subagent runner ask CanLaunch. Copilot and Pi are
// routable now; Antigravity has no Nanite boot-dir layout yet.
func TestCanLaunch(t *testing.T) {
	for name, want := range map[string]bool{
		"claude": true, "pty-claude": true, "codex": true, "opencode": true,
		"copilot": true, "pi": true,
		"agy": false, "nonsense-provider": false, "": false,
	} {
		if got := CanLaunch(name); got != want {
			t.Errorf("CanLaunch(%q) = %v, want %v", name, got, want)
		}
	}
}

// With no injected adapter, selectAdapter builds the registry's own
// go-providers adapter in Nanite's mode; DeveloperMode turns on Claude's
// --dangerously-skip-permissions.
func TestSelectAdapter_RegistryNative(t *testing.T) {
	for _, tc := range []struct {
		provider string
		dev      bool
		wantFlag string
	}{
		{"claude", false, "--input-format"},
		{"claude", true, "--dangerously-skip-permissions"},
		{"codex", false, "exec"},
		{"opencode", false, "run"},
	} {
		sel, err := selectRuntime(tc.provider, nil)
		if err != nil {
			t.Fatal(err)
		}
		a, err := selectAdapter(&Dependencies{DeveloperMode: tc.dev}, sel, "")
		if err != nil {
			t.Fatalf("selectAdapter(%s): %v", tc.provider, err)
		}
		ra, ok := a.(adapters.RuntimeAdapter)
		if !ok {
			t.Fatalf("%s: %T is not a native RuntimeAdapter", tc.provider, a)
		}
		args := ra.CLIAdapter().BuildArgs("hi", "", "")
		found := false
		for _, arg := range args {
			found = found || arg == tc.wantFlag
		}
		if !found {
			t.Errorf("%s (dev=%v) argv = %q, want %s", tc.provider, tc.dev, args, tc.wantFlag)
		}
		if !tc.dev {
			for _, arg := range args {
				if arg == "--dangerously-skip-permissions" {
					t.Errorf("%s argv = %q carries the developer flag without DeveloperMode", tc.provider, args)
				}
			}
		}
	}
}

// Moved here from cmd/nanite's TestInitProviders_ClaudeAdapterIsStreamingStdioShape
// when the composition root stopped building adapters (CW-20260930-0113).
// It pins the c200/c202 regression chain: Nanite's Claude is the
// streaming-stdio shape (`-p --input-format stream-json --output-format
// stream-json --verbose`), never print mode's empty `-p ""` nor the PTY
// TUI's bare argv, and developer mode adds --dangerously-skip-permissions
// exactly once.
func TestSelectAdapter_ClaudeIsStreamingStdioShape(t *testing.T) {
	sel, err := selectRuntime("claude", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, dev := range []bool{false, true} {
		a, err := selectAdapter(&Dependencies{DeveloperMode: dev}, sel, "")
		if err != nil {
			t.Fatalf("selectAdapter(dev=%v): %v", dev, err)
		}
		args := a.(adapters.RuntimeAdapter).CLIAdapter().BuildArgs("", "", "")
		flag := func(name string) (string, bool) {
			for i, arg := range args {
				if arg == name {
					if i+1 < len(args) {
						return args[i+1], true
					}
					return "", true
				}
			}
			return "", false
		}
		for _, required := range []string{"-p", "--verbose"} {
			if _, ok := flag(required); !ok {
				t.Errorf("dev=%v: claude argv %q missing %s", dev, args, required)
			}
		}
		for _, kv := range []string{"--input-format", "--output-format"} {
			if v, _ := flag(kv); v != "stream-json" {
				t.Errorf("dev=%v: %s = %q, want stream-json; argv %q", dev, kv, v, args)
			}
		}
		skips := 0
		for _, arg := range args {
			if arg == "--dangerously-skip-permissions" {
				skips++
			}
		}
		if want := map[bool]int{false: 0, true: 1}[dev]; skips != want {
			t.Errorf("dev=%v: --dangerously-skip-permissions appears %d times, want %d; argv %q", dev, skips, want, args)
		}
	}
}

// go-agent-wrapper v0.15.0 sets Selection.ExtraArgs on the adapter's own
// ExtraArgs field, which go-providers places at the convention's extra
// slot: before codex exec's --json and before opencode run's `-- <prompt>`.
// That is why Nanite no longer refuses argv additions on a per-turn launch
// (the v0.13 wrapper appended them after the prompt).
func TestSelectAdapter_ExtraArgsPrecedePrompt(t *testing.T) {
	const turn = "--dangerously-bypass-approvals-and-sandbox"
	for _, id := range []runtimes.ID{runtimes.Codex, runtimes.OpenCode} {
		a, err := launch.Select(launch.Selection{Runtime: string(id), Mode: runtimes.ModeSubprocessPerTurn, ExtraArgs: []string{"--nanite-extra", "x"}})
		if err != nil {
			t.Fatalf("launch.Select(%s): %v", id, err)
		}
		args := a.(adapters.RuntimeAdapter).CLIAdapter().BuildArgs(turn, "", "")
		extra, dd := -1, -1
		for i, arg := range args {
			switch arg {
			case "--nanite-extra":
				extra = i
			case "--":
				dd = i
			}
		}
		if extra < 0 || dd < 0 || extra > dd || args[len(args)-1] != turn {
			t.Errorf("%s argv = %q, want the extra args before `--` and the turn last", id, args)
		}
	}
}
