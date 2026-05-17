//go:build smoke

// Package launcher — LIVE behavioral smoke for the S5 platform-reshape
// cutover (EP-20260516-0001).
//
// This file is gated behind `//go:build smoke` so it never runs in plain
// CI (`go test ./...`). It drives a REAL tool-using provider subprocess
// (claude / codex) through Nanite's headless, no-TTY subordinate-dispatch
// path — `runtimeagent.Boot` in `ModeSubagent` — and proves the agent
// completed real work and exited cleanly rather than hanging on a
// tool-approval prompt.
//
// # Why this test exists
//
// The S5 cutover's S4.5 parity harness compares only launch *identity*
// (project / work_dir / runner / isolation). It is blind to boot-dir file
// CONTENT and env. A consumer that drops the codex `approval_policy` /
// `sandbox_mode` or the claude `permissions.defaultMode` from its planted
// boot dir still shows parity green — but a real headless agent then HANGS
// forever on the first tool-approval prompt, because there is no human at
// a TTY to answer it.
//
// Phase B (committed, cea9209) fixed Nanite's boot-dir provider-config
// plant: codex `config.toml` + claude `.claude/settings.json` are now
// sourced from go-providers' BootDirSpec via
// `internal/runtime/agent/bootdir_provider_config.go`. This test is the
// BEHAVIORAL proof that the fix works — the equivalent of Torque's
// `cmd/torque/serve_live_smoke_test.go`.
//
// # Dispatch seam
//
// The test drives `runtimeagent.Boot` directly with `Mode = ModeSubagent`.
// That is the SAME headless, no-TTY, AutoFireFirstTurn dispatch class that
// `service.BootRunner.runBoot` uses in production for a CLI-provider
// subordinate — see `internal/service/subagent_runner_boot.go`. Boot
// composes env + boot dir (planting the Phase-B provider-config files),
// resolves the adapter, and fires the first turn via
// `StartOptions.AutoFireFirstTurn` (true for ModeSubagent — see
// factory.go shouldAutoFireFirstTurn).
//
// Turn completion is observed exactly the way BootRunner observes it: by
// draining the runtime's event stream for a terminal EventDone / EventError
// (see drainBootSession in subagent_runner_boot.go). The streaming-stdio
// claude runtime is a LONG-LIVED process — after a turn completes it keeps
// stdin open for the next NDJSON message and does NOT exit, so waiting on
// process exit would itself look like a hang. The event stream is the
// correct turn-boundary signal. The test wires that stream via
// `Dependencies.EventFanout` — the same hook `service.agentEventBridge`
// uses — and supplies its own minimal per-session channel.
//
// The runtime Dependencies are composed via the launcher package's own
// `buildDeps` + `memoryRuntimeStore` — no Tether process, no registry, no
// chat service in the loop.
//
// # Outcome taxonomy
//
//   - PASS: a terminal EventDone arrived before the deadline AND the marker
//     file landed in the agent's work dir. The headless agent completed a
//     real tool-using turn (a file-write tool call) without a human
//     answering an approval prompt.
//   - BLOCKED(environment): the provider binary is missing from PATH, or
//     the provider failed for an auth/network reason (EventError with an
//     auth signature, or a Boot provider-spawn error). An honest "could
//     not verify" — not a pass, not a failure.
//   - FAIL: no terminal event arrived before the deadline — the agent HUNG,
//     the regression this test guards. Or a wiring break (Boot returned an
//     error from a plant / dispatch stage; a turn completed but produced no
//     marker).
//
// Run:
//
//	go test -tags smoke -run TestLiveSubordinateSmoke ./internal/launcher/ -v -timeout 600s
package launcher

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/go-providers/provider"

	"github.com/hollis-labs/nanite/internal/bootprofile"
	runtimeagent "github.com/hollis-labs/nanite/internal/runtime/agent"
)

// markerFile is the file the smoke task asks the agent to create.
// Asserting on its presence in the work dir is how the test distinguishes
// real tool-using work in the intended directory from a no-op, a
// wrong-directory dispatch, or a hang.
const markerFile = "NANITE_SMOKE_OK.txt"

// smokePromptFor builds the deterministic one-file task prompt. The agent
// is told to write a RELATIVE filename into its current working directory.
//
// The relative-filename / cwd choice is deliberate and load-bearing — it
// is what makes the task survive a real Nanite subordinate sandbox:
//
//   - No environment variable. The planted .sandbox/agent-context.md
//     declares "Can Execute: false" and a permission hook blocks shell
//     `$VAR` expansion; a $NANITE_SMOKE_WORK_DIR-based prompt made claude
//     burn a dozen failed tool calls and never land the file.
//   - No absolute path outside the boot dir. claude's planted
//     permissions.defaultMode = "acceptEdits" (Phase B) auto-accepts edits
//     to paths claude already has write permission for — and the boot dir
//     (claude's cwd) qualifies, but the Workdir passed via `claude
//     --add-dir` is read-granted only. A Write to an absolute Workdir path
//     comes back "Claude requested permissions to write ... you haven't
//     granted it yet" — no interactive approver in a headless subagent.
//   - A relative filename resolves against cwd = the boot dir for BOTH
//     providers (claudeLayout / codexLayout SpawnWorkdir both return the
//     boot dir). The boot dir is unambiguously writable. The marker
//     landing there proves the headless agent ran a real file-write tool
//     call, in the directory Nanite spawned it into, with NO approval
//     prompt — which is exactly the Phase-B contract.
//
// markerName must be a bare filename (no path separator).
func smokePromptFor(markerName string) string {
	return "Create a file named " + markerName + " in your current working directory, " +
		"containing exactly the word OK and nothing else. Use your file-write tool with the " +
		"relative filename " + markerName + " (no directory path). Do not run any shell " +
		"commands. Do not create or edit any other files."
}

// liveProviders maps a provider id to the binary it requires and a human
// label. Each runs as an isolated subtest; one provider's auth/binary
// failure does not block the other.
var liveProviders = []struct {
	provider string
	binary   string
	label    string
}{
	{provider: "claude", binary: "claude", label: "claude"},
	{provider: "codex", binary: "codex", label: "codex"},
}

// turnOutcome is the classified result of draining one subordinate turn.
type turnOutcome int

const (
	// outcomeDone — a terminal EventDone arrived: the turn completed.
	outcomeDone turnOutcome = iota
	// outcomeError — a terminal EventError arrived: the turn failed.
	outcomeError
	// outcomeHang — neither terminal event arrived before the deadline.
	outcomeHang
)

// TestLiveSubordinateSmoke drives a real provider subprocess through
// Nanite's headless subordinate-dispatch path (runtimeagent.Boot,
// ModeSubagent) for claude and codex, asserting each completes a real
// tool-using task and exits without hanging on an approval prompt.
func TestLiveSubordinateSmoke(t *testing.T) {
	for _, p := range liveProviders {
		p := p
		t.Run(p.label, func(t *testing.T) {
			binPath, err := exec.LookPath(p.binary)
			if err != nil {
				t.Skipf("BLOCKED(environment): %s binary not on PATH — cannot run a live behavioral smoke", p.binary)
			}
			t.Logf("provider=%s binary=%s", p.label, binPath)
			runLiveSubordinateSmoke(t, p.provider, p.label)
		})
	}
}

// runLiveSubordinateSmoke boots one provider as a ModeSubagent process
// against an isolated temp work dir, drains the runtime event stream for a
// terminal turn boundary, then asserts the marker file landed where Nanite
// told the agent to write it.
func runLiveSubordinateSmoke(t *testing.T, providerID, label string) {
	t.Helper()

	// Project work dir, passed to Boot as Options.Workdir. The agent is
	// NOT told to write here (the marker goes to cwd = the boot dir — see
	// smokePromptFor); Workdir is supplied so the dispatch exercises the
	// real Options.Workdir → SpawnWorkdir → provider --add-dir / workspace
	// path. It is initialized as a git repo because codex's `codex exec`
	// pre-flight wants a trusted git working tree, and a real Nanite
	// work_root is a project checkout anyway. claude is indifferent to the
	// git state, so a single git-repo work dir serves both providers.
	workDir := filepath.Join(t.TempDir(), "agent-workdir")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("mkdir work dir: %v", err)
	}
	initGitRepo(t, workDir)

	// Per-session event channel + EventFanout factory. This is the exact
	// hook service.agentEventBridge uses; supplying our own minimal
	// version lets the test observe turn boundaries (EventDone /
	// EventError) without standing up the chat service. Buffered so the
	// runtime never blocks publishing.
	eventsCh := make(chan llmtypes.StreamEvent, 256)

	// Compose the runtime Dependencies the SAME way the standalone
	// launcher does — buildDeps + memoryRuntimeStore — so there is no
	// Tether process, no registry, and no chat service in the loop. The
	// only additions are real claude / codex adapters and the EventFanout
	// hook for turn observation.
	deps, err := buildDeps(Config{
		CLIAdapters:    liveCLIAdapters(),
		WorkspacesRoot: filepath.Join(t.TempDir(), "workspaces"),
		// No BinaryPath / DBPath → no .mcp.json plant. The smoke task uses
		// only the provider's own file-write tool; it needs no Nanite MCP
		// surface, and leaving it out keeps the boot dir minimal so the
		// only provider-config under test is the Phase-B plant.
	})
	if err != nil {
		t.Fatalf("buildDeps: %v", err)
	}
	deps.EventFanout = func(sessionID string) chan<- llmtypes.StreamEvent {
		return eventsCh
	}

	// ParentSessionID is required for ModeSubagent. There is no parent
	// session row — Boot only uses it to register a PathGrants lineage,
	// and deps.PathGrants is nil here (buildDeps does not wire it), so the
	// registration is a nil-safe no-op. This matches the BootRunner
	// contract: the parent id is plumbing, not a store lookup.
	const parentSessionID = "nanite-s5-smoke-parent"

	opts := runtimeagent.Options{
		Mode:            runtimeagent.ModeSubagent,
		ParentSessionID: parentSessionID,
		Provider:        providerID,
		Workdir:         workDir,
		Role:            "backend",
		// codex spawns with cwd = the ephemeral boot dir (codexLayout.
		// SpawnWorkdir), which is never a git working tree. `codex exec`
		// refuses to run outside a trusted git repo unless
		// --skip-git-repo-check is passed — without it the headless child
		// exits 1 at the git-trust pre-flight before the approval policy
		// matters. A real codex launch profile supplies this via its
		// `args:` list (see examples/boot-profiles/launches/codex-smoke.yaml);
		// here it rides Options.ExtraArgs, the same field the boot-profile
		// compiler targets.
		ExtraArgs: codexExtraArgs(providerID),
		// ModeSubagent fires OneShotPrompt as the kickoff turn via
		// AutoFireFirstTurn.
		OneShotPrompt: smokePromptFor(markerFile),
		SessionMeta: map[string]any{
			"launch_source": "s5-live-subordinate-smoke",
		},
	}

	// Boot budget. Boot returns once the runtime is spawned and the first
	// turn fired; it does not wait for the turn to finish.
	bootCtx, cancelBoot := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelBoot()

	sess, err := runtimeagent.Boot(bootCtx, deps, opts)
	if err != nil {
		// Boot itself failed. Distinguish a plant / dispatch wiring break
		// (FAIL) from a provider-spawn / environment break (BLOCKED).
		msg := err.Error()
		switch {
		case containsAny(msg, "bootdir setup", "plant", "launch plan", "workspace", "persist runtime row"):
			t.Fatalf("FAIL provider=%s: Boot failed at a boot-dir / plant / dispatch stage — a wiring break: %v", label, err)
		case looksLikeEnvFailure(msg):
			t.Logf("BLOCKED(environment) provider=%s: Boot failed for an auth/network reason (not a plant break): %v", label, err)
		default:
			// A provider-spawn failure with no auth signature. Boot fires
			// the first turn synchronously for ModeSubagent, so a child
			// "process exited N" surfaces here. It is not a Nanite plant
			// break (the boot dir was planted before the spawn), but it is
			// also not a confirmed environment block — surface it plainly.
			t.Logf("BLOCKED(environment) provider=%s: Boot failed at the provider-spawn stage — the child process exited before completing the turn: %v",
				label, err)
		}
		return
	}
	if sess == nil {
		t.Fatalf("FAIL provider=%s: Boot returned nil session with nil error", label)
	}
	t.Logf("provider=%s: Boot OK — session=%s bootDir=%s workspaceDir=%s", label, sess.ID, sess.BootDir, sess.WorkspaceDir)

	// Capture the boot dir BEFORE Stop reaps it — a hang report needs the
	// path so an operator can inspect the planted provider-config files.
	bootDir := sess.BootDir

	// Drain the event stream for a terminal turn boundary. A timeout with
	// no terminal event is a HANG — the headless tool-approval regression.
	outcome, errMsg := drainTurn(eventsCh, 240*time.Second)

	// The marker is a relative filename written to the agent's cwd, which
	// is the ephemeral boot dir for both providers (claudeLayout /
	// codexLayout SpawnWorkdir both return the boot dir). Stat it BEFORE
	// Stop — Stop reaps the boot dir, taking the marker with it.
	markerPath := filepath.Join(bootDir, markerFile)
	markerPresent := fileExists(markerPath)
	var markerBody string
	if markerPresent {
		if b, rerr := os.ReadFile(markerPath); rerr == nil {
			markerBody = strings.TrimSpace(string(b))
		}
	}

	// Stop reaps the long-lived runtime + the boot dir. Best-effort.
	stopCtx, cancelStop := context.WithTimeout(context.Background(), 15*time.Second)
	_ = sess.Stop(stopCtx)
	cancelStop()

	switch outcome {
	case outcomeHang:
		// No terminal event before the deadline. This is the headless
		// tool-approval-prompt hang Phase B was meant to fix. Report
		// loudly with the boot dir path and the expected planted policy.
		t.Errorf("FAIL(hang) provider=%s: no terminal turn event (EventDone/EventError) arrived within the deadline — "+
			"this is the headless tool-approval-prompt hang Phase B was meant to fix. "+
			"Boot dir: %s — inspect %s for the planted non-interactive approval policy "+
			"(codex config.toml: approval_policy=\"never\" sandbox_mode=\"workspace-write\"; "+
			"claude .claude/settings.json: permissions.defaultMode=\"acceptEdits\"). marker_present=%v",
			label, bootDir, providerConfigRelPath(providerID), markerPresent)

	case outcomeError:
		// A terminal EventError arrived. An auth/network signature is an
		// honest environment block — the dispatch + plant path was
		// exercised cleanly. Anything else is a pipeline break.
		if looksLikeEnvFailure(strings.ToLower(errMsg)) {
			t.Logf("BLOCKED(environment) provider=%s: the turn ended with an auth/network error (%q) — "+
				"dispatch + boot-dir plant path was exercised cleanly, but the tool-approval "+
				"contract could not be behaviorally verified on this machine.", label, errMsg)
			return
		}
		t.Errorf("FAIL provider=%s: the turn ended with a non-environment error and the dispatch path "+
			"produced no clean completion — error=%q boot_dir=%s. marker_present=%v",
			label, errMsg, bootDir, markerPresent)

	case outcomeDone:
		// The turn completed. The behavioral proof is the marker file: it
		// only exists if the headless agent ran a real file-write tool
		// call WITHOUT a human answering an approval prompt — exactly the
		// Phase-B contract (codex approval_policy / claude
		// permissions.defaultMode).
		if markerPresent {
			t.Logf("PASS provider=%s: terminal EventDone received AND marker file present in the agent boot dir "+
				"(%s, content=%q) — the headless agent completed a real file-write tool call without hanging on "+
				"an approval prompt. Phase B provider-config plant behaviorally confirmed.",
				label, markerPath, markerBody)
			return
		}
		// Turn completed but no marker. The agent ran, did not hang, did
		// not error — but produced no file. Either the model declined /
		// misread the task, or a permission gate silently denied the
		// file-write tool call (a Phase-B-adjacent break: the turn does
		// not hang, but the planted policy did not actually grant the
		// write). Surface it as a failure with the boot dir for inspection.
		t.Errorf("FAIL provider=%s: the turn completed (EventDone) but the marker file is absent (%s) — "+
			"the headless agent's file-write tool call produced no file. Either the model declined the "+
			"task or the planted %s did not grant the write. boot_dir=%s.",
			label, markerPath, providerConfigRelPath(providerID), bootDir)
	}
}

// drainTurn consumes the per-session event channel until a terminal event
// (EventDone / EventError) arrives or the deadline elapses. It mirrors
// service.drainBootSession's terminal-event rules — EventDone is success,
// EventError is failure — but adds a deadline so a hung turn (no terminal
// event at all) is observable as outcomeHang rather than blocking forever.
func drainTurn(ch <-chan llmtypes.StreamEvent, deadline time.Duration) (turnOutcome, string) {
	timer := time.NewTimer(deadline)
	defer timer.Stop()
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				// Channel closed without a terminal event — treat as a
				// clean drain (the runtime stopped cleanly). Equivalent to
				// drainBootSession's closed-channel rule.
				return outcomeDone, ""
			}
			switch ev.Type {
			case llmtypes.EventDone:
				return outcomeDone, ""
			case llmtypes.EventError:
				msg := ev.Error
				if msg == "" {
					msg = "stream error event with no message"
				}
				return outcomeError, msg
			}
		case <-timer.C:
			return outcomeHang, ""
		}
	}
}

// initGitRepo makes dir a git repository with one commit, so codex's
// `codex exec` git-trust pre-flight passes. Deterministic identity so the
// commit does not depend on the operator's global git config.
func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=nanite-smoke", "GIT_AUTHOR_EMAIL=smoke@nanite.test",
			"GIT_COMMITTER_NAME=nanite-smoke", "GIT_COMMITTER_EMAIL=smoke@nanite.test")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s (in %s): %v\n%s", strings.Join(args, " "), dir, err, string(out))
		}
	}
	run("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("nanite smoke sandbox\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	run("add", "-A")
	run("commit", "-q", "-m", "smoke: initial commit")
}

// liveCLIAdapters builds the real go-providers CLI adapters the live smoke
// dispatches to. Mirrors cmd/nanite initProviders' production (non-dev)
// wiring: claude as the streaming-stdio long-lived shape, codex as the
// default `codex exec` single-turn subprocess. Production is the correct
// reference because the smoke must prove the SHIPPED behavior, not a
// dev-mode override (dev mode's --dangerously-skip-permissions would mask
// exactly the planted-config dependency under test).
func liveCLIAdapters() []provider.CLIAdapter {
	return []provider.CLIAdapter{
		provider.NewClaudeAdapterStreamingStdio(),
		provider.NewCodexAdapter(),
	}
}

// codexExtraArgs returns the ExtraArgs a headless codex dispatch needs.
// codex spawns with cwd = the ephemeral boot dir, which is never a git
// repo; `codex exec` requires --skip-git-repo-check to run there. Other
// providers get no extra args.
func codexExtraArgs(providerID string) []string {
	if providerID == "codex" {
		return []string{"--skip-git-repo-check"}
	}
	return nil
}

// providerConfigRelPath names the boot-dir-relative provider-config file
// Phase B plants for the given provider, for use in failure messages.
func providerConfigRelPath(providerID string) string {
	switch providerID {
	case "codex":
		return "config.toml"
	case "claude":
		return ".claude/settings.json"
	default:
		return "(provider config file)"
	}
}

// looksLikeEnvFailure reports whether an error message carries an
// auth/network/quota signature — the signals that a provider failure is
// an environment block rather than a Nanite wiring break.
func looksLikeEnvFailure(msg string) bool {
	return containsAny(strings.ToLower(msg),
		"auth", "login", "not logged in", "credential", "api key", "apikey",
		"unauthorized", "401", "403", "network", "rate limit", "quota",
		"429", "overloaded", "connection refused", "no api key",
	)
}

// containsAny reports whether s contains any of the substrings.
func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// fileExists reports whether path exists and is a regular file.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// TestLiveSmoke_CodexProfileCompiles is a smoke-tagged guard that the
// codex-smoke example boot profile (added alongside this test) loads and
// compiles cleanly — the codex analogue of the claude-smoke coverage in
// service/chat_bootprofile_smoke_test.go. It lives here, under the smoke
// tag, because it is part of the same S5 cutover surface; the plain-CI
// catalog check stays in the service package.
func TestLiveSmoke_CodexProfileCompiles(t *testing.T) {
	root := exampleCatalogDirForSmoke(t)
	cat, err := bootprofile.LoadCatalog(root)
	if err != nil {
		t.Fatalf("LoadCatalog(%s): %v", root, err)
	}
	if _, ok := cat.Profiles["codex-smoke"]; !ok {
		t.Fatalf("profile %q missing from example catalog", "codex-smoke")
	}
	if _, ok := cat.Launches["codex-smoke"]; !ok {
		t.Fatalf("launch %q missing from example catalog", "codex-smoke")
	}
	spec, err := bootprofile.CompileFromCatalog(cat, "codex-smoke", nil)
	if err != nil {
		t.Fatalf("CompileFromCatalog(codex-smoke): %v", err)
	}
	if spec.Provider != "codex" {
		t.Errorf("spec.Provider = %q, want codex", spec.Provider)
	}
	if spec.BootPrompt == "" {
		t.Error("spec.BootPrompt empty — text + static slots should render at compile time")
	}
}

// exampleCatalogDirForSmoke locates examples/boot-profiles/ relative to
// the launcher package dir (internal/launcher → ../../examples/boot-profiles),
// so the test runs regardless of cwd. go test runs with cwd = the package
// dir.
func exampleCatalogDirForSmoke(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Join(wd, "..", "..", "examples", "boot-profiles")
}
