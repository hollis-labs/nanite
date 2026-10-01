package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// postureProbeHoldScript records its argv and environment, then stays up
// like a streaming-stdio Claude.
const postureProbeHoldScript = `#!/bin/sh
printf '%s\n' "$@" > "$NANITE_TEST_PROBE_FILE.argv"
env > "$NANITE_TEST_PROBE_FILE.env"
echo '{"type":"system","subtype":"init","session_id":"posture-probe"}'
sleep 30
`

// postureProbeTurnScript records its argv and environment and ends the turn.
const postureProbeTurnScript = `#!/bin/sh
printf '%s\n' "$@" > "$NANITE_TEST_PROBE_FILE.argv"
env > "$NANITE_TEST_PROBE_FILE.env"
`

// agentkit v0.17.0 made a launch's permission posture go-permission's Mode
// and refuses a provider spelling (acceptEdits, bypassPermissions,
// on-request) in LaunchPlan.Provider.Permission or RuntimeBinding.Permission;
// go-agent-wrapper v0.21.0 maps Config.PermissionPosture onto each runtime's
// flags or environment. Nanite sets neither, so with go-agent-wrapper v0.23.0
// every native runtime still boots and its process carries no posture
// flag or environment: the posture stays where Nanite plants it
// (permissions.defaultMode "acceptEdits" in Claude's settings.json, Codex's
// approval_policy "never" / sandbox_mode "workspace-write" in config.toml;
// bootdir_claude_test.go and bootdir_codex_test.go pin those), and
// developer mode keeps Claude's --dangerously-skip-permissions.
func TestBoot_NativeLaunchCarriesNoPermissionPosture(t *testing.T) {
	for _, tc := range []struct {
		name, provider, cliEnv, script string
		dev                            bool
		mode                           Mode
	}{
		{"claude", "claude", "CLAUDE_CLI_PATH", postureProbeHoldScript, false, ModeLongLived},
		{"claude-dev", "claude", "CLAUDE_CLI_PATH", postureProbeHoldScript, true, ModeLongLived},
		{"codex", "codex", "CODEX_CLI_PATH", postureProbeTurnScript, false, ModeOneShot},
		{"opencode", "opencode", "OPENCODE_CLI_PATH", postureProbeTurnScript, false, ModeOneShot},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			script := filepath.Join(dir, "fake-cli.sh")
			if err := os.WriteFile(script, []byte(tc.script), 0o755); err != nil { //nolint:gosec // an executable test fixture in t.TempDir()
				t.Fatal(err)
			}
			t.Setenv(tc.cliEnv, script)
			probe := filepath.Join(dir, "probe")

			deps, _ := makeBootDeps(t, tc.provider)
			deps.NativeCLIAdapter = nil // the registry's own adapter, as in production
			deps.DeveloperMode = tc.dev
			opts := Options{
				Mode:     tc.mode,
				Provider: tc.provider,
				Workdir:  t.TempDir(),
				Role:     "executor",
				Env:      map[string]string{"NANITE_TEST_PROBE_FILE": probe},
			}
			if tc.mode == ModeOneShot {
				opts.OneShotPrompt = "say hi"
			}
			sess, err := Boot(context.Background(), deps, opts)
			if err != nil {
				t.Fatalf("Boot(%s): %v", tc.name, err)
			}
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = sess.Stop(ctx)
			}()

			argv := strings.Split(strings.TrimSpace(readProbe(t, probe+".argv")), "\n")
			env := readProbe(t, probe+".env")
			skips := 0
			for _, arg := range argv {
				switch {
				case arg == "--permission-mode":
					t.Errorf("argv carries a posture flag: %q", argv)
				case strings.Contains(arg, "sandbox_mode=") || strings.Contains(arg, "approval_policy="):
					t.Errorf("argv overrides Codex's planted posture: %q", argv)
				case arg == "--dangerously-skip-permissions":
					skips++
				}
			}
			if want := map[bool]int{false: 0, true: 1}[tc.dev]; skips != want {
				t.Errorf("--dangerously-skip-permissions appears %d times, want %d; argv %q", skips, want, argv)
			}
			for _, line := range strings.Split(env, "\n") {
				if strings.HasPrefix(line, "OPENCODE_PERMISSION=") {
					t.Errorf("environment carries a posture: %s", line)
				}
			}
		})
	}
}

// readProbe waits for a probe file the fake CLI writes once it is spawned.
func readProbe(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		if b, err := os.ReadFile(path); err == nil && len(b) > 0 { //nolint:gosec // a probe file in t.TempDir()
			return string(b)
		}
		if time.Now().After(deadline) {
			t.Fatalf("the fake CLI never wrote %s", filepath.Base(path))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A thread id from another Boot (a stored provider session id, or a
// checkpoint's) is not preset for native Codex: its thread lives under the
// per-boot CODEX_HOME, so `exec resume <id>` would fail with "no rollout
// found for thread id". The first turn starts a thread.
func TestBoot_CodexIgnoresAThreadIDFromAnotherBoot(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-codex.sh")
	if err := os.WriteFile(script, []byte(postureProbeTurnScript), 0o755); err != nil { //nolint:gosec // an executable test fixture in t.TempDir()
		t.Fatal(err)
	}
	t.Setenv("CODEX_CLI_PATH", script)
	probe := filepath.Join(dir, "probe")

	deps, store := makeBootDeps(t, "codex")
	deps.NativeCLIAdapter = nil
	sess, err := Boot(context.Background(), deps, Options{
		Mode: ModeOneShot, Provider: "codex", Workdir: t.TempDir(), Role: "executor", OneShotPrompt: "say hi",
		ResumeProviderSessionID: "thr-from-an-old-boot",
		Env:                     map[string]string{"NANITE_TEST_PROBE_FILE": probe},
	})
	if err != nil {
		t.Fatalf("Boot: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = sess.Stop(ctx)
	}()
	argv := readProbe(t, probe+".argv")
	if strings.Contains(argv, "resume") || strings.Contains(argv, "thr-from-an-old-boot") {
		t.Errorf("codex's first turn resumed a thread from another boot: %q", argv)
	}
	if id := store.provIDs[sess.ID]; id != "" {
		t.Errorf("a codex thread id was persisted for a later boot: %q", id)
	}
}
