package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/permission"
	"github.com/hollis-labs/nanite/internal/store"
)

// CW-20261001-0232, against the real Codex sandbox. A fake CLI cannot show
// what the sandbox does with the roots Nanite plants, so this runs codex exec
// the way Nanite launches it (boot dir as cwd and CODEX_HOME, writable_roots
// from the launch's roots) in a turn that names a protected path, and checks
// the file the model was asked to write there.
//
// It spends real tokens and needs the host's Codex login, so it runs only when
// NANITE_REAL_CODEX=1, and is skipped when codex is not installed.

const realCodexEnv = "NANITE_REAL_CODEX"

// realCodexProbe boots a Codex launch for a turn that names protected/probe.txt
// and asks the real codex to write it, and work/ok.txt as a control. It reports
// whether each file exists afterwards.
func realCodexProbe(t *testing.T, policy permission.MentionPolicy, protectedIsDenied bool) (protectedWritten, workWritten bool) {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory: %v", err)
	}
	// Not under $TMPDIR or /tmp, which codex's workspace-write sandbox makes
	// writable by default and would hide the roots under test.
	cache := filepath.Join(home, ".cache")
	if err := os.MkdirAll(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	base, err := os.MkdirTemp(cache, "nanite-0232-realcodex-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	work := filepath.Join(base, "work")
	protected := filepath.Join(base, "protected")
	for _, d := range []string{work, protected} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if protectedIsDenied {
		policy.Denied = append(policy.Denied, protected)
	}

	const sessionID = "sess-real-codex"
	grants := permission.NewPathGrants()
	grants.SetMentionPolicy(policy)
	// The turn's text names the protected path, as an injected prompt would.
	grants.RegisterFromUserMessage(sessionID, "please write the word hi into "+filepath.Join(protected, "probe.txt"))

	layout, params := composeBootdirParams(&Dependencies{PathGrants: grants},
		Options{Provider: "codex", SessionID: sessionID, Workdir: work},
		&store.AgentProfile{Name: "probe", SystemPrompt: "You are a terse test agent."}, sessionID)
	bootDir, err := layout.Setup(params)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(bootDir) })
	t.Logf("launch roots: %v", params.CLIWritableRoots)

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	prompt := "Run these two shell commands, one after the other, and then report for each whether it succeeded. " +
		"Do not try any other way to write a file. " +
		"1) echo hi > " + filepath.Join(protected, "probe.txt") + "  " +
		"2) echo hi > " + filepath.Join(work, "ok.txt")
	cmd := exec.CommandContext(ctx, "codex", "exec", "--json", "--skip-git-repo-check", "--", prompt) //nolint:gosec // a fixed command with a test-built prompt
	cmd.Dir = bootDir
	cmd.Env = append(os.Environ(), "CODEX_HOME="+bootDir)
	out, runErr := cmd.CombinedOutput()
	if runErr != nil {
		t.Fatalf("codex exec: %v\n%s", runErr, out)
	}

	_, perr := os.Stat(filepath.Join(protected, "probe.txt"))
	_, werr := os.Stat(filepath.Join(work, "ok.txt"))
	if werr != nil {
		t.Fatalf("the control write to the work root did not happen, so codex did not run the probe (%v):\n%s", werr, out)
	}
	return perr == nil, true
}

func TestRealCodex_NamedProtectedPathStaysUnwritable(t *testing.T) {
	if os.Getenv(realCodexEnv) != "1" {
		t.Skipf("set %s=1 to run codex for real (spends tokens)", realCodexEnv)
	}
	if _, err := exec.LookPath("codex"); err != nil {
		t.Skip("codex is not installed")
	}
	home, _ := os.UserHomeDir()
	if _, err := os.Stat(filepath.Join(home, ".codex", "auth.json")); err != nil {
		t.Skip("no host Codex login to link into the boot dir")
	}

	t.Run("default", func(t *testing.T) {
		t.Setenv(PathMentionLaunchRootsEnv, "")
		written, _ := realCodexProbe(t, permission.MentionPolicy{Confine: true}, true)
		if written {
			t.Fatal("codex wrote the protected path a turn named: the mention became a writable root")
		}
	})

	// The positive control: with the old behavior restored and nothing refusing
	// the mention, the same turn does reach the path. If this one fails, the
	// probe cannot see the problem and the default case above proves nothing.
	t.Run("old behavior reaches it", func(t *testing.T) {
		t.Setenv(PathMentionLaunchRootsEnv, "1")
		written, _ := realCodexProbe(t, permission.MentionPolicy{}, false)
		if !written {
			t.Fatal("with the grants folded into the roots the protected path was still unwritable, so the probe is not discriminating")
		}
	})
}
