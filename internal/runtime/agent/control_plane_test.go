package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/agent-contracts-leaf/runtimes"
	"github.com/hollis-labs/go-agent-wrapper/wrapper"
	"github.com/hollis-labs/go-sandbox/sandbox"
)

func mkdirs(t *testing.T, root string, rel ...string) {
	t.Helper()
	for _, r := range rel {
		if err := os.MkdirAll(filepath.Join(root, r), 0o750); err != nil {
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
// (CW-20261001-0143). Codex is confined by its own sandbox instead; see
// TestBoot_CodexRunsUnderItsOwnSandbox and
// TestBoot_RealCodexSandboxConfinesControlPlane.
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
			if werr := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); werr != nil { //nolint:gosec // an executable test fixture in t.TempDir()
				t.Fatal(werr)
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

func TestProtectionEnabled(t *testing.T) {
	for v, want := range map[string]bool{"": true, "1": true, "on": true, "0": false, "false": false, " OFF ": false, "no": false} {
		t.Setenv(ProtectEnv, v)
		if got := ProtectionEnabled(); got != want {
			t.Errorf("%s=%q: ProtectionEnabled() = %v, want %v", ProtectEnv, v, got, want)
		}
	}
}

func TestCodexSandboxesItself(t *testing.T) {
	for _, tc := range []struct {
		sel  RuntimeSelection
		want bool
	}{
		{RuntimeSelection{runtimes.Codex, runtimes.ModeSubprocessPerTurn}, true},
		{RuntimeSelection{runtimes.Codex, runtimes.ModeACPStdio}, false},
		{RuntimeSelection{runtimes.Claude, runtimes.ModeStreamingStdio}, false},
		{RuntimeSelection{runtimes.OpenCode, runtimes.ModeSubprocessPerTurn}, false},
	} {
		if got := codexSandboxesItself(tc.sel); got != tc.want {
			t.Errorf("codexSandboxesItself(%s %s) = %v, want %v", tc.sel.Runtime, tc.sel.Mode, got, tc.want)
		}
	}
}

// A writable root that contains a protected directory is narrowed to its
// other child directories; one that does not, or does not exist yet, is
// kept.
func TestWritableRootsAround(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mkdirs(t, root, "allowed/state/coordination", "allowed/ok", "allowed/deep/x", "other")
	p := func(rel string) string { return filepath.Join(root, rel) }
	roots := []string{p("allowed"), p("other"), p("missing")}
	got := writableRootsAround(roots, []string{p("allowed/state")})
	want := []string{p("allowed/deep"), p("allowed/ok"), p("missing"), p("other")}
	if !slices.Equal(got, want) {
		t.Errorf("writableRootsAround\n got %q\nwant %q", got, want)
	}
	if got := writableRootsAround(roots, nil); !slices.Equal(got, roots) {
		t.Errorf("with nothing protected the roots changed: %q", got)
	}
}

// codexLayoutFixture lays out a control plane inside a configured writable
// root: base/allowed (dev_tools_allowed_paths) holds base/allowed/state
// (protected) and base/allowed/ok, beside the work root base/work.
func codexLayoutFixture(t *testing.T, base string) (allowed, state, ok, work string) {
	t.Helper()
	mkdirs(t, base, "allowed/state", "allowed/ok", "work")
	if err := os.WriteFile(filepath.Join(base, "work", "README.md"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(base, "allowed"), filepath.Join(base, "allowed", "state"),
		filepath.Join(base, "allowed", "ok"), filepath.Join(base, "work")
}

// Nanite does not wrap a native codex launch in its control-plane sandbox:
// codex's own bwrap cannot nest inside it. Codex's own sandbox confines it
// instead, so the planted writable_roots must leave the control plane out,
// even when a configured root contains it.
func TestBoot_CodexRunsUnderItsOwnSandbox(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	allowed, state, ok, work := codexLayoutFixture(t, base)
	probeDir, bin := t.TempDir(), t.TempDir()
	probe := filepath.Join(probeDir, "probe")
	script := controlPlaneProbeScript(state, ok, work, probe, false)
	if werr := os.WriteFile(filepath.Join(bin, "codex"), []byte(script), 0o755); werr != nil { //nolint:gosec // an executable test fixture in t.TempDir()
		t.Fatal(werr)
	}
	t.Setenv("CODEX_CLI_PATH", filepath.Join(bin, "codex"))

	deps, _ := makeBootDeps(t, "codex")
	deps.NativeCLIAdapter = nil
	deps.ControlPlane = ControlPlane{Dirs: []string{state}}
	deps.CLIWritableRoots = []string{allowed}
	sess, err := Boot(context.Background(), deps, Options{Mode: ModeOneShot, Provider: "codex", Workdir: work, Role: "executor", OneShotPrompt: "say hi"})
	if err != nil {
		t.Fatalf("Boot(codex): %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = sess.Stop(ctx)
	}()
	readProbe(t, probe+".done")
	// The fake does not sandbox itself, so with no Nanite sandbox around it
	// the write lands: proof that Nanite did not wrap codex.
	if got := strings.TrimSpace(readProbe(t, probe+".protected")); got != "yes" {
		t.Errorf("a fake codex could not write the control plane (landed=%s): Nanite wrapped codex, whose own sandbox cannot nest there", got)
	}

	b, err := os.ReadFile(filepath.Join(sess.BootDir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := string(b)
	for _, want := range []string{`sandbox_mode = "workspace-write"`, strconv.Quote(ok), strconv.Quote(work)} {
		if !strings.Contains(cfg, want) {
			t.Errorf("planted config.toml lacks %s:\n%s", want, cfg)
		}
	}
	for _, unwanted := range []string{strconv.Quote(allowed), strconv.Quote(state)} {
		if strings.Contains(cfg, unwanted) {
			t.Errorf("planted writable_roots include %s, which holds or is the control plane:\n%s", unwanted, cfg)
		}
	}
}

// The real codex binary, launched by Nanite, runs its own sandbox and that
// sandbox keeps the control plane read-only. CODEX_CLI_PATH points at a shim
// that runs `codex sandbox` (codex's command sandbox, no model call) with the
// planted sandbox_mode and CODEX_HOME, exactly as `codex exec` would run a
// command. If Nanite wrapped codex in its own bwrap, codex's sandbox could
// not start: "bwrap: No permissions to create a new namespace".
func TestBoot_RealCodexSandboxConfinesControlPlane(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("codex's bwrap sandbox is Linux's")
	}
	codexBin, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("no codex on PATH")
	}
	// Outside /tmp and the agent's $TMPDIR, which codex's workspace-write
	// sandbox always leaves writable.
	base, err := os.MkdirTemp("/var/tmp", "nanite-codex-e2e-")
	if err != nil {
		t.Skipf("no /var/tmp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	if base, err = filepath.EvalSymlinks(base); err != nil {
		t.Fatal(err)
	}
	allowed, state, ok, work := codexLayoutFixture(t, base)
	agentTmp, bin := t.TempDir(), t.TempDir()
	probe := filepath.Join(agentTmp, "probe")

	inner := controlPlaneProbeScript(state, ok, work, probe, false)
	inner = strings.TrimPrefix(inner, "#!/bin/sh\n")
	inner = fmt.Sprintf("cat %q > %q.read\n", filepath.Join(work, "README.md"), probe) + inner
	shim := fmt.Sprintf("#!/bin/sh\nmode=$(sed -n 's/^sandbox_mode = \"\\(.*\\)\"$/\\1/p' \"$CODEX_HOME/config.toml\")\n"+
		"%q sandbox -c \"sandbox_mode=\\\"$mode\\\"\" -- sh -c %s > %q.out 2>&1\necho $? > %q.exit\n",
		codexBin, shellQuote(inner), probe, probe)
	if werr := os.WriteFile(filepath.Join(bin, "codex-shim"), []byte(shim), 0o755); werr != nil { //nolint:gosec // an executable test fixture in t.TempDir()
		t.Fatal(werr)
	}
	t.Setenv("CODEX_CLI_PATH", filepath.Join(bin, "codex-shim"))

	deps, _ := makeBootDeps(t, "codex")
	deps.NativeCLIAdapter = nil
	deps.ControlPlane = ControlPlane{Dirs: []string{state}}
	deps.CLIWritableRoots = []string{allowed}
	sess, err := Boot(context.Background(), deps, Options{
		Mode: ModeOneShot, Provider: "codex", Workdir: work, Role: "executor", OneShotPrompt: "say hi",
		Env: map[string]string{"TMPDIR": agentTmp},
	})
	if err != nil {
		t.Fatalf("Boot(codex): %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = sess.Stop(ctx)
	}()
	if exit := strings.TrimSpace(readProbe(t, probe+".exit")); exit != "0" {
		out, _ := os.ReadFile(probe + ".out") //nolint:gosec // a probe file in t.TempDir()
		t.Fatalf("codex's sandbox exited %s under Nanite:\n%s", exit, out)
	}
	if got := strings.TrimSpace(readProbe(t, probe+".read")); got != "hello" {
		t.Errorf("codex read README.md as %q, want hello", got)
	}
	for name, want := range map[string]string{"protected": "no", "exception": "yes", "work": "yes"} {
		if got := strings.TrimSpace(readProbe(t, probe+"."+name)); got != want {
			t.Errorf("codex write into the %s dir landed=%s, want %s", name, got, want)
		}
	}
	if err := os.WriteFile(filepath.Join(state, "nanite-wrote"), []byte("nanite"), 0o600); err != nil {
		t.Errorf("Nanite cannot write its own protected dir: %v", err)
	}
}

// shellQuote single-quotes s for sh.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
