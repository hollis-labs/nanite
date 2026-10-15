package agent

import (
	"slices"
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

func TestModelArgsOnlyForVerifiedFlag(t *testing.T) {
	if got := modelArgs("claude", "haiku"); !slices.Equal(got, []string{"--model", "haiku"}) {
		t.Fatalf("claude: %q", got)
	}
	if got := modelArgs("claude", ""); got != nil {
		t.Errorf("empty model: %q", got)
	}
	for _, p := range []string{"codex", "opencode", "copilot"} {
		if got := modelArgs(p, "m"); got != nil {
			t.Errorf("%s: %q, want no flag", p, got)
		}
		if ModelFlagVerified(p) {
			t.Errorf("%s: ModelFlagVerified = true", p)
		}
	}
	if !ModelFlagVerified("claude") {
		t.Error("claude: ModelFlagVerified = false")
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
