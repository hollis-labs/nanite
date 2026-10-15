package agent

import (
	"slices"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/substrate/harness/adapters"
	"github.com/hollis-labs/substrate/llm-core/contracts/runtimes"
)

func TestAddDirArgsWorkRootFirstDedupedClaudeOnly(t *testing.T) {
	got := addDirArgs("claude", "/work", []string{"/extra", "/work", "", "/other"})
	want := []string{"--add-dir", "/work", "/extra", "/other"}
	if !slices.Equal(got, want) {
		t.Fatalf("addDirArgs = %q, want %q", got, want)
	}
	for _, p := range []string{"codex", "opencode"} {
		if got := addDirArgs(p, "/work", []string{"/extra"}); got != nil {
			t.Errorf("%s: addDirArgs = %q, want none", p, got)
		}
	}
	if got := addDirArgs("claude", "", nil); got != nil {
		t.Errorf("no roots: addDirArgs = %q, want none", got)
	}
}

func TestModelArgsFollowTheVerifiedFlagTable(t *testing.T) {
	for _, p := range []string{"claude", "codex", "opencode", "agy", "sub-codex", "pty-claude"} {
		if got := modelArgs(p, "m"); !slices.Equal(got, []string{"--model", "m"}) {
			t.Errorf("%s: %q, want --model m", p, got)
		}
		if !ModelFlagVerified(p) {
			t.Errorf("%s: ModelFlagVerified = false", p)
		}
		if got := modelArgs(p, ""); got != nil {
			t.Errorf("%s: empty model gave %q", p, got)
		}
	}
	// ACP-only runtimes take their model through the protocol, not a flag.
	for _, p := range []string{"copilot", "pi", "nonsense"} {
		if got := modelArgs(p, "m"); got != nil || ModelFlagVerified(p) {
			t.Errorf("%s: %q verified=%v, want no flag", p, got, ModelFlagVerified(p))
		}
	}
}

// Each runtime's model lands in its real argv ahead of the prompt, so the
// prompt is never read as the flag's value or the flag as part of the prompt.
func TestSelectAdapterPlacesTheModelInEachRuntimesArgv(t *testing.T) {
	for _, tc := range []struct {
		runtime runtimes.ID
		mode    runtimes.Mode
		prompt  string
	}{
		{runtimes.Claude, runtimes.ModeStreamingStdio, ""},
		{runtimes.Codex, runtimes.ModeSubprocessPerTurn, "PROMPT"},
		{runtimes.OpenCode, runtimes.ModeSubprocessPerTurn, "PROMPT"},
		{runtimes.Antigravity, runtimes.ModeSubprocessPerTurn, "PROMPT"},
	} {
		t.Run(string(tc.runtime), func(t *testing.T) {
			a, err := selectAdapter(&Dependencies{}, RuntimeSelection{Runtime: tc.runtime, Mode: tc.mode}, "", "", launchExtra{Model: "the-model"})
			if err != nil {
				t.Fatal(err)
			}
			argv := a.(adapters.RuntimeAdapter).CLIAdapter().BuildArgs("PROMPT", "", "")
			i := slices.Index(argv, "--model")
			if i < 0 || i+1 >= len(argv) || argv[i+1] != "the-model" {
				t.Fatalf("--model the-model missing from %q", argv)
			}
			if tc.prompt != "" {
				for j, a := range argv {
					if strings.Contains(a, tc.prompt) && j < i {
						t.Errorf("prompt at %d precedes --model at %d in %q", j, i, argv)
					}
				}
			}
		})
	}
}

func TestAddDirArgsAgyRepeatsTheFlag(t *testing.T) {
	got := addDirArgs("agy", "/work", []string{"/extra", "/work"})
	want := []string{"--add-dir", "/work", "--add-dir", "/extra"}
	if !slices.Equal(got, want) {
		t.Fatalf("addDirArgs(agy) = %q, want %q", got, want)
	}
}

// The profile's model and directories reach the Claude launch argv, with the
// variadic --add-dir list last so nothing joins it.
func TestSelectAdapterPassesModelAndHostDirectoriesToClaude(t *testing.T) {
	work, extra := t.TempDir(), t.TempDir()
	sel := RuntimeSelection{Runtime: runtimes.Claude, Mode: runtimes.ModeStreamingStdio}
	a, err := selectAdapter(&Dependencies{}, sel, work, "", launchExtra{Model: "haiku", AddDirs: []string{extra}})
	if err != nil {
		t.Fatal(err)
	}
	argv := a.(adapters.RuntimeAdapter).CLIAdapter().BuildArgs("hi", "", "")
	i := slices.Index(argv, "--model")
	if i < 0 || argv[i+1] != "haiku" {
		t.Fatalf("--model haiku missing from %q", argv)
	}
	j := slices.Index(argv, "--add-dir")
	if j < 0 || j < i || !slices.Equal(argv[j:], []string{"--add-dir", work, extra}) {
		t.Fatalf("--add-dir list wrong or not last in %q", argv)
	}
}

func TestHostDirectoriesJoinWritableRoots(t *testing.T) {
	work, host := t.TempDir(), t.TempDir()
	prof := &store.AgentProfile{Directories: `["` + host + `","/does/not/exist"]`}
	got := effectiveCLIWritableRoots(nil, "", work, profileDirectories(prof)...)
	if !slices.Equal(got, []string{work, host}) {
		t.Fatalf("roots = %q, want work root then the existing host directory", got)
	}
	if got := profileDirectories(&store.AgentProfile{Directories: "not json"}); got != nil {
		t.Errorf("malformed directories granted %q", got)
	}
}

// agy's permission bypass follows the permission engine's mode, like Claude's
// developer variant: only yolo adds it.
func TestAgyBypassFlagFollowsYoloMode(t *testing.T) {
	sel := RuntimeSelection{Runtime: runtimes.Antigravity, Mode: runtimes.ModeSubprocessPerTurn}
	argvFor := func(d *Dependencies) []string {
		a, err := selectAdapter(d, sel, "", "")
		if err != nil {
			t.Fatal(err)
		}
		return a.(adapters.RuntimeAdapter).CLIAdapter().BuildArgs("hi", "", "")
	}
	if slices.Contains(argvFor(&Dependencies{}), "--dangerously-skip-permissions") {
		t.Error("default mode launched agy with the bypass flag")
	}
	if !slices.Contains(argvFor(withYolo(&Dependencies{}, true)), "--dangerously-skip-permissions") {
		t.Error("yolo mode did not launch agy with the bypass flag")
	}
	// Other runtimes never get agy's flag.
	a, err := selectAdapter(withYolo(&Dependencies{}, true), RuntimeSelection{Runtime: runtimes.Codex, Mode: runtimes.ModeSubprocessPerTurn}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(a.(adapters.RuntimeAdapter).CLIAdapter().BuildArgs("hi", "", ""), "--dangerously-skip-permissions") {
		t.Error("codex launched with agy's bypass flag")
	}
}
