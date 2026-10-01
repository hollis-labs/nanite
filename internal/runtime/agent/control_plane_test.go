package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/go-agent-wrapper/wrapper"
	"github.com/hollis-labs/go-sandbox/sandbox"
)

func mkdirs(t *testing.T, root string, rel ...string) {
	t.Helper()
	for _, r := range rel {
		if err := os.MkdirAll(filepath.Join(root, r), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// protectedFor protects each control-plane dir whole unless a writable root
// lies inside it; then it protects that dir's other child directories,
// recursively, and never a writable root or a symlink.
func TestControlPlane_ProtectedFor(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mkdirs(t, root,
		"config", "data/backups", "data/skills/vendor", "data/workspaces/default", "data/workspaces/other",
		"state/coordination", "state/worktrees/wt1", "elsewhere", "work")
	if err := os.WriteFile(filepath.Join(root, "data/top.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "elsewhere"), filepath.Join(root, "data/link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "config"), filepath.Join(root, "config-link")); err != nil {
		t.Fatal(err)
	}
	p := func(rel ...string) []string {
		out := make([]string, len(rel))
		for i, r := range rel {
			out[i] = filepath.Join(root, r)
		}
		return out
	}
	cp := ControlPlane{
		// config through a symlink: protected by its real path. A missing
		// dir is skipped. data/skills is nested in data: one entry.
		Dirs:     p("config-link", "state", "data", "missing", "data/skills"),
		Writable: p("data/workspaces/default", "state/worktrees"),
	}
	for _, tc := range []struct {
		name   string
		launch []string
		want   []string
	}{
		{"split around the exceptions", p("work"),
			p("config", "data/backups", "data/skills", "data/workspaces/other", "state/coordination")},
		{"a launch root inside a protected dir splits it too", p("config"),
			p("data/backups", "data/skills", "data/workspaces/other", "state/coordination")},
		{"a launch root not yet created still exempts its parents", p("data/skills/vendor/new"),
			p("config", "data/backups", "data/workspaces/other", "state/coordination")},
		{"a launch root above the protected dirs leaves them protected", []string{root},
			p("config", "data/backups", "data/skills", "data/workspaces/other", "state/coordination")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := cp.protectedFor(tc.launch...)
			if !slices.Equal(got, tc.want) {
				t.Errorf("protectedFor(%q)\n got %q\nwant %q", tc.launch, got, tc.want)
			}
		})
	}
	if got := (ControlPlane{}).protectedFor(p("work")...); got != nil {
		t.Errorf("zero ControlPlane protects %q, want nothing", got)
	}
	if got := (ControlPlane{Dirs: p("config"), Writable: p("config")}).protectedFor(); got != nil {
		t.Errorf("a Dir that is itself writable is protected: %q", got)
	}
}

// controlPlaneProbeScript tries three writes and records which landed:
// into a protected dir, into the writable exception inside it, and into its
// own work dir. hold keeps it up like a streaming-stdio Claude.
func controlPlaneProbeScript(protected, exception, work, probe string, hold bool) string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	for _, w := range []struct{ name, dir string }{{"protected", protected}, {"exception", exception}, {"work", work}} {
		fmt.Fprintf(&b, "if ( echo agent > %q/agent-wrote ) 2>/dev/null; then echo yes > %q.%s; else echo no > %q.%s; fi\n",
			w.dir, probe, w.name, probe, w.name)
	}
	fmt.Fprintf(&b, "echo done > %q.done\n", probe)
	if hold {
		b.WriteString(`echo '{"type":"system","subtype":"init","session_id":"control-plane-probe"}'` + "\nsleep 30\n")
	}
	return b.String()
}

// An agent Nanite launches cannot write Nanite's control-plane directories,
// natively or over ACP, while the directories excepted inside them and its
// own work dir stay writable; Nanite itself still writes them
// (CW-20261001-0143).
func TestBoot_ControlPlaneProtectedFromAgents(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Nanite enables control-plane protection on Linux only (CW-20261001-0189)")
	}
	if caps := sandbox.ResolveBackendCapabilities("", sandbox.BackendAuto); !caps.Supported || !slices.Contains(caps.Capabilities, sandbox.CapWriteProtect) {
		t.Skipf("the %s sandbox backend cannot write-protect paths", caps.Backend)
	}
	for _, tc := range []struct {
		provider, cliEnv string
		mode             Mode
		acp              bool
	}{
		{"claude", "CLAUDE_CLI_PATH", ModeLongLived, false},
		{"codex", "CODEX_CLI_PATH", ModeOneShot, false},
		{"opencode", "OPENCODE_CLI_PATH", ModeOneShot, false},
		{"copilot", "", ModeLongLived, true},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			state, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			protected := filepath.Join(state, "coordination")
			exception := filepath.Join(state, "db")
			mkdirs(t, state, "coordination", "db")
			work, probeDir, bin := t.TempDir(), t.TempDir(), t.TempDir()
			probe := filepath.Join(probeDir, "probe")

			script := controlPlaneProbeScript(protected, exception, work, probe, tc.provider == "claude")
			name := "fake-cli"
			if tc.acp {
				name = "copilot" // found through PATH, the production lookup
			}
			if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil { //nolint:gosec // an executable test fixture in t.TempDir()
				t.Fatal(err)
			}
			if tc.acp {
				t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			} else {
				t.Setenv(tc.cliEnv, filepath.Join(bin, name))
			}

			deps, _ := makeBootDeps(t, tc.provider)
			deps.NativeCLIAdapter = nil
			deps.ControlPlane = ControlPlane{Dirs: []string{state}, Writable: []string{exception}}
			opts := Options{Mode: tc.mode, Provider: tc.provider, Workdir: work, Role: "executor"}
			if tc.mode == ModeOneShot {
				opts.OneShotPrompt = "say hi"
			}
			sess, err := Boot(context.Background(), deps, opts)
			switch {
			case tc.acp && errors.Is(err, wrapper.ErrProtectedPathsUnsupported):
				t.Fatalf("the ACP launch was refused instead of sandboxed: %v", err)
			case tc.acp && err == nil:
				_ = sess.Stop(context.Background())
				t.Fatal("the probe never answers ACP initialize, yet Boot succeeded")
			case !tc.acp && err != nil:
				t.Fatalf("Boot(%s): %v", tc.provider, err)
			case !tc.acp:
				defer func() {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					_ = sess.Stop(ctx)
				}()
			}
			readProbe(t, probe+".done")

			for name, want := range map[string]string{"protected": "no", "exception": "yes", "work": "yes"} {
				if got := strings.TrimSpace(readProbe(t, probe+"."+name)); got != want {
					t.Errorf("agent write into the %s dir landed=%s, want %s", name, got, want)
				}
			}
			if _, err := os.Stat(filepath.Join(protected, "agent-wrote")); err == nil {
				t.Error("the agent's file exists in the protected dir")
			}
			// Nanite's own writes are unaffected: protection binds the
			// dir read-only only inside the agent's sandbox.
			if err := os.WriteFile(filepath.Join(protected, "nanite-wrote"), []byte("nanite"), 0o600); err != nil {
				t.Errorf("Nanite cannot write its own protected dir: %v", err)
			}
		})
	}
}
