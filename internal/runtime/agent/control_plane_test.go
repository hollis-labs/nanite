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

// probeTarget is one more directory a probe script tries to write.
type probeTarget struct{ name, dir string }

// controlPlaneProbeScript tries three writes and records which landed:
// into a protected dir, into the writable exception inside it, and into its
// own work dir, and into each extra target. hold keeps it up like a
// streaming-stdio Claude.
func controlPlaneProbeScript(protected, exception, work, probe string, hold bool, extra ...probeTarget) string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	targets := append([]probeTarget{{"protected", protected}, {"exception", exception}, {"work", work}}, extra...)
	for _, w := range targets {
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
// natively or over ACP, while the directory excepted inside them (the
// worktree root, in production) and its own work dir stay writable; Nanite
// itself still writes them (CW-20261001-0143). The main database's directory
// is not an exception: it sits in the protected data dir with its main.db,
// and an agent can neither create files there nor change the database
// (CW-20261001-0188). Codex is confined by its own sandbox instead; see
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
			exception := filepath.Join(state, "worktrees")
			dbDir := filepath.Join(state, "workspaces", "default")
			mkdirs(t, state, "coordination", "worktrees", "workspaces/default")
			dbFile := filepath.Join(dbDir, "main.db")
			if werr := os.WriteFile(dbFile, []byte("nanite-database"), 0o600); werr != nil {
				t.Fatal(werr)
			}
			work, probeDir, bin := t.TempDir(), t.TempDir(), t.TempDir()
			probe := filepath.Join(probeDir, "probe")

			// The probe also appends to main.db itself, the write that
			// `nanite mcp` used to make, and tries a sibling the way a
			// SQLite -wal or -shm file would be created.
			script := controlPlaneProbeScript(protected, exception, work, probe, tc.provider == "claude",
				probeTarget{"database", dbDir})
			script = strings.Replace(script, fmt.Sprintf("echo done > %q.done\n", probe),
				fmt.Sprintf("if ( echo agent >> %q ) 2>/dev/null; then echo yes > %q.dbfile; else echo no > %q.dbfile; fi\n", dbFile, probe, probe)+
					fmt.Sprintf("echo done > %q.done\n", probe), 1)
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
			// The protected dir is also offered as a root, configured
			// (dev_tools_allowed_paths) and named in a user message (a path
			// grant): neither may un-protect it.
			deps.CLIWritableRoots = []string{protected}
			grantNamed(t, deps, "sess-"+tc.provider, filepath.Join(protected, "note"))
			opts := Options{Mode: tc.mode, Provider: tc.provider, Workdir: work, Role: "executor", SessionID: "sess-" + tc.provider}
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

			for name, want := range map[string]string{"protected": "no", "exception": "yes", "work": "yes", "database": "no", "dbfile": "no"} {
				if got := strings.TrimSpace(readProbe(t, probe+"."+name)); got != want {
					t.Errorf("agent write into the %s dir landed=%s, want %s", name, got, want)
				}
			}
			if _, err := os.Stat(filepath.Join(protected, "agent-wrote")); err == nil {
				t.Error("the agent's file exists in the protected dir")
			}
			if _, err := os.Stat(filepath.Join(dbDir, "agent-wrote")); err == nil {
				t.Error("the agent created a file in the database's directory")
			}
			if got, err := os.ReadFile(dbFile); err != nil || string(got) != "nanite-database" { //nolint:gosec // a file this test created in t.TempDir()
				t.Errorf("main.db after the agent ran = %q (%v), want it unchanged", got, err)
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

// A root offered to an agent must not give it the control plane
// (CW-20261001-0143): one equal to or inside a protected directory is
// dropped, however it is spelled; one containing a protected directory is
// split around it (Codex) or kept whole (split=false); the rest are kept as
// given, in order.
func TestRootsOutsideProtected(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mkdirs(t, root, "allowed/state/coordination/sub", "allowed/ok", "allowed/deep/x", "other", "elsewhere")
	if err := os.Symlink(filepath.Join(root, "allowed/state/coordination"), filepath.Join(root, "other/link-in")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "elsewhere"), filepath.Join(root, "other/link-out")); err != nil {
		t.Fatal(err)
	}
	p := func(rel string) string { return filepath.Join(root, rel) }
	protected := []string{p("allowed/state")}

	for _, tc := range []struct {
		name  string
		roots []string
		split bool
		want  []string
	}{
		{"equal to a protected dir", []string{p("allowed/state"), p("other")}, true, []string{p("other")}},
		{"equal to a protected dir, kept whole otherwise", []string{p("allowed/state"), p("other")}, false, []string{p("other")}},
		{"inside a protected dir", []string{p("allowed/state/coordination"), p("other")}, true, []string{p("other")}},
		{"deep inside a protected dir", []string{p("allowed/state/coordination/sub")}, false, nil},
		{"inside one, and not created yet", []string{p("allowed/state/coordination/new/deeper"), p("missing")}, false, []string{p("missing")}},
		{"a symlink into a protected dir", []string{p("other/link-in")}, false, nil},
		{"a symlink out of the way", []string{p("other/link-out")}, true, []string{p("other/link-out")}},
		{"spelled with .. and a trailing slash", []string{p("other/../allowed/state/") + "/", p("allowed/ok/../ok")}, false, []string{p("allowed/ok/../ok")}},
		{"containing one, split", []string{p("allowed")}, true, []string{p("allowed/deep"), p("allowed/ok")}},
		{"containing one, kept whole", []string{p("allowed"), p("other")}, false, []string{p("allowed"), p("other")}},
		{"unrelated roots keep their order", []string{p("other"), p("missing"), p("elsewhere")}, true, []string{p("other"), p("missing"), p("elsewhere")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := rootsOutsideProtected(tc.roots, protected, tc.split)
			if !slices.Equal(got, tc.want) {
				t.Errorf("rootsOutsideProtected(%q, split=%v)\n got %q\nwant %q", tc.roots, tc.split, got, tc.want)
			}
		})
	}
	roots := []string{p("allowed"), p("allowed/state"), p("other")}
	if got := rootsOutsideProtected(roots, nil, true); !slices.Equal(got, roots) {
		t.Errorf("with nothing protected the roots changed: %q", got)
	}
}

// What a user's message does to the launch's roots: naming a control-plane
// path grants it and its parent, so a message that mentions
// <state>/inner/note grants <state>/inner, and one that mentions
// <state>/note grants <state> itself (CW-20261001-0143). Neither may reach
// the planted roots, for Claude or Codex, nor may a configured
// dev_tools_allowed_paths root that is the control plane.
func TestComposeBootdirParams_RootsKeepOutOfControlPlane(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	allowed, state, ok, work := codexLayoutFixture(t, base)
	inner := filepath.Join(state, "inner")
	for _, tc := range []struct {
		provider string
		want     []string
	}{
		{"claude", []string{work, allowed, ok}}, // kept whole: Claude's mount protection backs it
		{"codex", []string{work, ok}},           // split: Codex's own sandbox enforces its roots
	} {
		t.Run(tc.provider, func(t *testing.T) {
			deps, _ := makeBootDeps(t, tc.provider)
			deps.ControlPlane = ControlPlane{Dirs: []string{state}}
			deps.CLIWritableRoots = []string{allowed, state, inner, ok}
			grantNamed(t, deps, "sess-roots", filepath.Join(state, "note"), filepath.Join(inner, "note"), filepath.Join(ok, "note"))
			profile := storeProfile(tc.provider)
			_, params := composeBootdirParams(deps, Options{Provider: tc.provider, Workdir: work, SessionID: "sess-roots"}, &profile, "sess-roots")
			if !slices.Equal(params.CLIWritableRoots, tc.want) {
				t.Errorf("planted roots\n got %q\nwant %q", params.CLIWritableRoots, tc.want)
			}
		})
	}
}

// grantNamed registers paths the way chat does for a user message that
// mentions them, and checks the control-plane grants really exist, so a test
// built on them cannot pass by exercising nothing.
func grantNamed(t *testing.T, deps *Dependencies, sessionID string, paths ...string) {
	t.Helper()
	deps.PathGrants.RegisterFromUserMessage(sessionID, "please write "+strings.Join(paths, " "))
	grants := deps.PathGrants.ListGrants(sessionID)
	for _, want := range paths {
		if !slices.Contains(grants, want) {
			t.Fatalf("naming %s did not grant it: grants = %q", want, grants)
		}
	}
}

// codexLayoutFixture lays out a control plane inside a configured writable
// root: base/allowed (dev_tools_allowed_paths) holds base/allowed/state
// (protected) and base/allowed/ok, beside the work root base/work.
func codexLayoutFixture(t *testing.T, base string) (allowed, state, ok, work string) {
	t.Helper()
	mkdirs(t, base, "allowed/state/inner", "allowed/ok", "work")
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
	inner := filepath.Join(state, "inner")
	script := controlPlaneProbeScript(state, ok, work, probe, false)
	if werr := os.WriteFile(filepath.Join(bin, "codex"), []byte(script), 0o755); werr != nil { //nolint:gosec // an executable test fixture in t.TempDir()
		t.Fatal(werr)
	}
	t.Setenv("CODEX_CLI_PATH", filepath.Join(bin, "codex"))

	deps, _ := makeBootDeps(t, "codex")
	deps.NativeCLIAdapter = nil
	deps.ControlPlane = ControlPlane{Dirs: []string{state}}
	// The control plane is offered every way a root can arrive: inside a
	// configured root, as one, inside one, and named in a user message.
	deps.CLIWritableRoots = []string{allowed, state, inner}
	grantNamed(t, deps, "sess-codex", filepath.Join(state, "note"), filepath.Join(inner, "note"))
	sess, err := Boot(context.Background(), deps, Options{Mode: ModeOneShot, Provider: "codex", Workdir: work, Role: "executor", OneShotPrompt: "say hi", SessionID: "sess-codex"})
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
	for _, unwanted := range []string{strconv.Quote(allowed), strconv.Quote(state), strconv.Quote(inner)} {
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

	innerDir := filepath.Join(state, "inner")
	inner := controlPlaneProbeScript(state, ok, work, probe, false, probeTarget{"inner", innerDir})
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
	// The protected dir is also a root every way one can arrive: configured,
	// inside a configured root, and named in a user message (a path grant,
	// which grants the named path's parent too). The writes must still fail.
	deps.CLIWritableRoots = []string{allowed, state, innerDir}
	grantNamed(t, deps, "sess-real-codex", filepath.Join(state, "note"), filepath.Join(innerDir, "note"))
	sess, err := Boot(context.Background(), deps, Options{
		Mode: ModeOneShot, Provider: "codex", Workdir: work, Role: "executor", OneShotPrompt: "say hi",
		Env: map[string]string{"TMPDIR": agentTmp}, SessionID: "sess-real-codex",
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
	for name, want := range map[string]string{"protected": "no", "inner": "no", "exception": "yes", "work": "yes"} {
		if got := strings.TrimSpace(readProbe(t, probe+"."+name)); got != want {
			t.Errorf("codex write into the %s dir landed=%s, want %s", name, got, want)
		}
	}
	for _, f := range []string{filepath.Join(state, "agent-wrote"), filepath.Join(innerDir, "agent-wrote")} {
		if _, err := os.Stat(f); err == nil {
			t.Errorf("codex's file exists in the protected tree: %s", f)
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
