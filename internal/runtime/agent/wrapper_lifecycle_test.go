package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/go-providers/provider/events"
	"github.com/hollis-labs/nanite/internal/permission"
)

// fakeClaudeStreamJSONScript is a POSIX sh script standing in for the real
// claude binary in -p --input-format stream-json --output-format
// stream-json --verbose mode. It ignores stdin/argv (AutoFireFirstTurn's
// NDJSON-framed kickoff is written but never read — the fake doesn't need
// it) and prints exactly one turn's worth of real Claude stream-json
// output, then exits — driving parseClaudeStreamLine's real production
// parser (not a mock) so the events this test asserts on are exactly what
// wrapper.Wrapper.Run's translateStreamEvent/translateProviderEvent would
// see from a genuine claude process.
const fakeClaudeStreamJSONScript = `#!/bin/sh
cat <<'EOF'
{"type":"system","subtype":"init","session_id":"claude-fake-session-1"}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"hello "},{"type":"tool_use","id":"tu_1","name":"Read","input":{"file_path":"/tmp/x"}}]}}
{"type":"result","subtype":"success","is_error":false,"result":"hello world","usage":{"input_tokens":10,"output_tokens":5}}
EOF
`

// TestBoot_WrapperLifecycle_Claude drives one full session lifecycle
// (start -> activity events -> exit) through the post-migration
// wrapper.Wrapper.Run-mediated Boot path against a REAL
// provider.NewClaudeAdapterStreamingStdio() adapter and a real fake-claude
// subprocess (not a mock CLIAdapter, not a mock runtimeevents.Sink) — the
// strongest available non-live-dogfeed proof that:
//
//  1. Session construction genuinely routes through wrapper.Wrapper.Run
//     (a real child process spawns, runs, and exits).
//  2. runtimeEventSink's translation actually reaches
//     Dependencies.EventFanout (agent.delta / turn.completed as
//     EventUsage+EventDone) and Dependencies.TypedEventCallback
//     (agent.tool_use -> events.ToolUse) the same way the pre-migration
//     direct StartOptions wiring did.
//  3. SessionIDPreset/OnSessionID's Config wiring (task 05a's fix) is
//     genuinely used, not silently dropped — RuntimeStore.SetProviderSessionID
//     fires with the fake session id.
//  4. RuntimeStore.UpdateState fires "running" then "done" — the direct
//     replacement for the state transitions agentsessions.Manager's
//     StateSink used to drive automatically (see deps.go's UpdateState
//     doc comment).
//
// Live dogfeed verification against the real claude binary is task 07's
// job, not this one's — this is a fake-binary integration test, per this
// task's own Done-means allowance ("this task's own tests can use
// fakes/mocks per existing convention").
func TestBoot_WrapperLifecycle_Claude(t *testing.T) {
	scriptPath := filepath.Join(t.TempDir(), "fake-claude.sh")
	if err := os.WriteFile(scriptPath, []byte(fakeClaudeStreamJSONScript), 0o755); err != nil {
		t.Fatalf("write fake claude script: %v", err)
	}
	t.Setenv("CLAUDE_CLI_PATH", scriptPath)

	fanoutCh := make(chan llmtypes.StreamEvent, 32)

	var typedMu sync.Mutex
	var typedEvents []events.Event

	store := newFakeRuntimeStore()
	pg := permission.NewPathGrants()
	profile := storeProfile("claude")
	deps := &Dependencies{
		Agents:     &fakeAgentProfiles{profile: &profile},
		Manager:    NewSessionManager(),
		Store:      store,
		PathGrants: pg,
		ProviderAdapter: func(name string) provider.CLIAdapter {
			if name != "claude" {
				return nil
			}
			return provider.NewClaudeAdapterStreamingStdio()
		},
		EventFanout: func(string) chan<- llmtypes.StreamEvent { return fanoutCh },
		TypedEventCallback: func(string) provider.EventsCallback {
			return func(e events.Event) {
				typedMu.Lock()
				typedEvents = append(typedEvents, e)
				typedMu.Unlock()
			}
		},
		WorkspacesRoot: t.TempDir(),
	}
	t.Cleanup(func() { _ = deps.Manager.Shutdown(context.Background()) })

	sess, err := Boot(context.Background(), deps, Options{
		Mode:          ModeOneShot,
		Workdir:       t.TempDir(),
		Role:          "executor",
		OneShotPrompt: "say hi",
	})
	if err != nil {
		t.Fatalf("Boot: %v", err)
	}
	if sess.Provider != "claude" {
		t.Fatalf("session.Provider = %q, want claude", sess.Provider)
	}

	// Boot only returns once wrapper.Wrapper.Run reaches
	// runtimeevents.KindSessionReady — the shared Manager must already
	// expose that wrapper handle before Boot returns.
	if !deps.Manager.IsLive(sess.ID) {
		t.Errorf("deps.Manager.IsLive(%q) = false immediately after Boot, want true", sess.ID)
	}

	waitCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := sess.Wait(waitCtx); err != nil {
		t.Fatalf("Wait: %v (fake script should exit 0)", err)
	}

	// Once Wait returns, wr.Run's background goroutine has fully finished
	// and removed its binding from the shared Manager.
	if deps.Manager.IsLive(sess.ID) {
		t.Errorf("deps.Manager.IsLive(%q) = true after Wait, want false", sess.ID)
	}

	// Drain whatever landed on fanoutCh — Run's translator goroutine only
	// returns (and hence Wait only unblocks) after it has fully drained
	// its own internal fanout channel and pushed every translated event
	// through the Sink synchronously, so every event is already sitting
	// in fanoutCh's buffer by this point.
	var gotDelta, gotUsage, gotDone bool
	var deltaContent string
	var usage *llmtypes.Usage
drain:
	for {
		select {
		case ev := <-fanoutCh:
			switch ev.Type {
			case llmtypes.EventDelta:
				gotDelta = true
				deltaContent = ev.Content
			case llmtypes.EventUsage:
				gotUsage = true
				usage = ev.Usage
			case llmtypes.EventDone:
				gotDone = true
			}
		default:
			break drain
		}
	}
	if !gotDelta {
		t.Errorf("fanout: no EventDelta observed")
	} else if deltaContent != "hello " {
		t.Errorf("fanout EventDelta.Content = %q, want %q", deltaContent, "hello ")
	}
	if !gotUsage {
		t.Errorf("fanout: no EventUsage observed")
	} else if usage == nil || usage.InputTokens != 10 || usage.OutputTokens != 5 {
		t.Errorf("fanout EventUsage = %+v, want InputTokens=10 OutputTokens=5", usage)
	}
	if !gotDone {
		t.Errorf("fanout: no EventDone observed")
	}

	typedMu.Lock()
	defer typedMu.Unlock()
	var gotToolUse bool
	for _, e := range typedEvents {
		if tu, ok := e.(events.ToolUse); ok {
			gotToolUse = true
			if tu.ID != "tu_1" || tu.Name != "Read" {
				t.Errorf("typed ToolUse = %+v, want ID=tu_1 Name=Read", tu)
			}
			if fp, _ := tu.Args["file_path"].(string); fp != "/tmp/x" {
				t.Errorf("typed ToolUse.Args[file_path] = %v, want /tmp/x", tu.Args["file_path"])
			}
		}
	}
	if !gotToolUse {
		t.Errorf("typedCallback: no events.ToolUse observed (tool-use tracking regressed)")
	}

	// SessionIDPreset/OnSessionID wiring (task 05a's fix, genuinely used
	// per this task's own Done-means bar, not just compiled against).
	if got := store.provIDs[sess.ID]; got != "claude-fake-session-1" {
		t.Errorf("store.provIDs[%q] = %q, want claude-fake-session-1", sess.ID, got)
	}

	// UpdateState: the direct replacement for agentsessions.Manager's
	// StateSink-driven transitions (see deps.go's RuntimeStore.UpdateState
	// doc comment) — running once ready, done once the fake script exits
	// cleanly.
	store.mu.Lock()
	states := append([]fakeStateUpdate(nil), store.states...)
	store.mu.Unlock()
	var sawRunning, sawDone bool
	for _, su := range states {
		if su.ID != sess.ID {
			continue
		}
		switch su.State {
		case "running":
			sawRunning = true
		case "done":
			sawDone = true
		}
	}
	if !sawRunning {
		t.Errorf("UpdateState: no %q transition observed for %q (states=%+v)", "running", sess.ID, states)
	}
	if !sawDone {
		t.Errorf("UpdateState: no %q transition observed for %q (states=%+v)", "done", sess.ID, states)
	}
}

// fakeClaudeLongLivedScript stays alive until signaled — a stand-in for a
// long-lived ModeLongLived claude chat session, so TestBoot_WrapperLifecycle_Stop
// can exercise Session.Stop's cooperative-interrupt path (wr.Stop ->
// session.Stop -> stdin close / grace / SIGTERM escalation) against a
// still-running child, not a process that already exited on its own.
const fakeClaudeLongLivedScript = `#!/bin/sh
echo '{"type":"system","subtype":"init","session_id":"claude-fake-longlived-1"}'
sleep 30
`

// TestBoot_WrapperLifecycle_Stop exercises the "stop" half of this task's
// Done-means bar (start -> activity events -> stop/exit) against a
// still-running session: ModeLongLived (no AutoFireFirstTurn — the chat
// harness drives turns explicitly), Session.Stop called against a live
// child process, and Wait/BootDir-cleanup both observed to complete
// promptly rather than hang for the fake script's full 30s sleep.
func TestBoot_WrapperLifecycle_Stop(t *testing.T) {
	scriptPath := filepath.Join(t.TempDir(), "fake-claude-longlived.sh")
	if err := os.WriteFile(scriptPath, []byte(fakeClaudeLongLivedScript), 0o755); err != nil {
		t.Fatalf("write fake claude script: %v", err)
	}
	t.Setenv("CLAUDE_CLI_PATH", scriptPath)

	fanoutCh := make(chan llmtypes.StreamEvent, 32)
	store := newFakeRuntimeStore()
	pg := permission.NewPathGrants()
	profile := storeProfile("claude")
	deps := &Dependencies{
		Agents:     &fakeAgentProfiles{profile: &profile},
		Manager:    NewSessionManager(),
		Store:      store,
		PathGrants: pg,
		ProviderAdapter: func(name string) provider.CLIAdapter {
			if name != "claude" {
				return nil
			}
			return provider.NewClaudeAdapterStreamingStdio()
		},
		EventFanout:    func(string) chan<- llmtypes.StreamEvent { return fanoutCh },
		WorkspacesRoot: t.TempDir(),
	}
	t.Cleanup(func() { _ = deps.Manager.Shutdown(context.Background()) })

	sess, err := Boot(context.Background(), deps, Options{
		Mode:    ModeLongLived,
		Workdir: t.TempDir(),
		Role:    "executor",
	})
	if err != nil {
		t.Fatalf("Boot: %v", err)
	}
	bootDir := sess.BootDir

	if !deps.Manager.IsLive(sess.ID) {
		t.Errorf("deps.Manager.IsLive(%q) = false immediately after Boot, want true", sess.ID)
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopCancel()
	if err := sess.Stop(stopCtx); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// wr.Stop blocks until session.Stop's own grace/escalation sequence
	// completes, so Wait should already be satisfied.
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer waitCancel()
	if err := sess.Wait(waitCtx); err != nil {
		t.Logf("Wait after Stop returned err=%v (acceptable — Stop's SIGTERM/SIGKILL escalation can surface as an *agentsessions.ExitError; only a hang is a failure)", err)
	}

	if _, statErr := os.Stat(bootDir); !os.IsNotExist(statErr) {
		t.Errorf("boot dir %s still exists after Stop", bootDir)
	}
	if deps.Manager.IsLive(sess.ID) {
		t.Errorf("deps.Manager.IsLive(%q) = true after Stop+Wait, want false", sess.ID)
	}
}

// fakeCodexExecScript is a POSIX sh script standing in for the real
// codex binary in `codex exec <prompt> --json` mode. It proves
// wrapper.ChildEnvironment genuinely propagates the composed env — specifically CODEX_HOME, the
// sole redirect to codex's planted, sandboxed config.toml — into the real
// spawned process. Writes CODEX_HOME's observed value to
// $NANITE_TEST_PROBE_FILE (itself only reachable through that explicit
// replacement environment)
// before printing canned codex --json stream output.
const fakeCodexExecScript = `#!/bin/sh
printf '%s' "$CODEX_HOME" > "$NANITE_TEST_PROBE_FILE"
echo '{"type":"item.message","role":"assistant","content":"hello from codex"}'
echo '{"type":"turn.completed","turn_id":"t1"}'
`

// TestBoot_WrapperLifecycle_Codex_EnvParity is this task's own regression
// test for the environment contract now owned by wrapper v0.9.0. Without
// the explicit ChildEnvironment replacement, a
// wrapper.Wrapper.Run-driven Codex session would silently drop
// codexLayout.AmendEnv's CODEX_HOME redirect and fall back to the
// operator's real, global ~/.codex config — a sandbox-restriction bypass.
// This test drives a REAL fake codex subprocess (not a mock) through the
// full Boot path and asserts the value it actually observed for
// $CODEX_HOME matches the session's real boot dir exactly.
func TestBoot_WrapperLifecycle_Codex_EnvParity(t *testing.T) {
	probeFile := filepath.Join(t.TempDir(), "codex_home_seen.txt")
	scriptPath := filepath.Join(t.TempDir(), "fake-codex.sh")
	if err := os.WriteFile(scriptPath, []byte(fakeCodexExecScript), 0o755); err != nil {
		t.Fatalf("write fake codex script: %v", err)
	}
	t.Setenv("CODEX_CLI_PATH", scriptPath)

	fanoutCh := make(chan llmtypes.StreamEvent, 32)
	store := newFakeRuntimeStore()
	pg := permission.NewPathGrants()
	profile := storeProfile("codex")
	deps := &Dependencies{
		Agents:     &fakeAgentProfiles{profile: &profile},
		Manager:    NewSessionManager(),
		Store:      store,
		PathGrants: pg,
		ProviderAdapter: func(name string) provider.CLIAdapter {
			if name != "codex" {
				return nil
			}
			return provider.NewCodexAdapter()
		},
		EventFanout:    func(string) chan<- llmtypes.StreamEvent { return fanoutCh },
		WorkspacesRoot: t.TempDir(),
	}
	t.Cleanup(func() { _ = deps.Manager.Shutdown(context.Background()) })

	sess, err := Boot(context.Background(), deps, Options{
		Mode:          ModeOneShot,
		Workdir:       t.TempDir(),
		Role:          "executor",
		OneShotPrompt: "say hi",
		Env:           map[string]string{"NANITE_TEST_PROBE_FILE": probeFile},
	})
	if err != nil {
		t.Fatalf("Boot: %v", err)
	}
	bootDir := sess.BootDir

	// Codex's runtime kind here (subprocess-per-turn "adapter runtime" —
	// see agentkit/agentsessions/from_adapter.go) is deliberately
	// long-lived at the session level even though the underlying CLI
	// process is spawned fresh per turn: adapterSession.Wait blocks on a
	// done channel ONLY closed by an explicit Stop call, never by a
	// completed turn's process exiting on its own (unlike claude's
	// streaming-stdio session, where the child's exit IS the session's
	// exit). Real ModeOneShot callers of this runtime kind observe turn
	// completion via the EventFanout EventDelta/EventDone signal, then
	// call Session.Stop explicitly — this test does the same rather than
	// asserting on Wait returning unprompted.
	var gotDelta bool
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case ev := <-fanoutCh:
			if ev.Type == llmtypes.EventDelta && ev.Content == "hello from codex" {
				gotDelta = true
			}
		case <-time.After(50 * time.Millisecond):
		}
		if gotDelta {
			break
		}
	}
	if !gotDelta {
		t.Fatalf("fanout: no EventDelta with codex's item.message content observed within 15s")
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()
	if err := sess.Stop(stopCtx); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	waitCtx, waitCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer waitCancel()
	if err := sess.Wait(waitCtx); err != nil {
		t.Fatalf("Wait after Stop: %v", err)
	}

	seen, err := os.ReadFile(probeFile)
	if err != nil {
		t.Fatalf("read probe file (fake codex process never ran, or wrapper child environment is broken): %v", err)
	}
	if string(seen) != bootDir {
		t.Errorf("fake codex process observed CODEX_HOME=%q, want %q (wrapper ChildEnvironment is not propagating codexLayout.AmendEnv's redirect)", string(seen), bootDir)
	}
}

// fakeOpencodeRunScript is a POSIX sh script standing in for the real
// opencode binary in `opencode run --agent <slug> ... "<prompt>"` mode
// (Mode="" — the only mode cmd/nanite's composition root actually wires up,
// under Nanite's product policy: opencode's HTTP+SSE "serve-http"
// Mode is unused today; Nanite drives opencode as a subprocess-per-turn
// "adapter runtime" exactly like Codex). Ignores argv (AutoFireFirstTurn's
// NDJSON-framed kickoff is irrelevant to the fake), writes
// OPENCODE_CONFIG_DIR's observed value to $NANITE_TEST_PROBE_FILE — proving
// wrapper.ChildEnvironment propagates
// opencodeLayout.AmendEnv's redirect exactly as it does for Codex — then
// prints one plain-text line, driving OpencodeAdapter.ParseLine's real
// production parser (each non-empty stdout line -> llmtypes.EventDelta; no
// structured completion event, the bridge synthesizes EventDone on clean
// exit).
const fakeOpencodeRunScript = `#!/bin/sh
printf '%s' "$OPENCODE_CONFIG_DIR" > "$NANITE_TEST_PROBE_FILE"
echo "hello from opencode"
`

// TestBoot_WrapperLifecycle_OpenCode is the regression coverage
// TASKS/agent-host-acp/18 adds for the gap that let a real, 100%-
// reproducible OpenCode crash ship: neither this file's Claude nor Codex
// test (task 06's own new coverage) ever drove OpenCode through a real
// Boot -> wrapper.Wrapper.Run path, and every other test that touches
// opencodeLayout.SpawnWorkdir (bootdir_opencode_test.go) calls it directly
// rather than through Boot.
//
// Options.Workdir is left EMPTY here deliberately — the exact real-world
// shape internal/service/chat_boot_drive.go's bootSessionWorkdir always
// produces for every real chat session today (a documented, intentional
// stub that never threads a resolved project path through). Pre-fix, this
// reproduced the live-dogfeed crash exactly: opencodeLayout.SpawnWorkdir
// returned opts.Workdir ("") verbatim, wrapper.Config.Workdir ended up "",
// and wrapper.Wrapper.Run hard-errored with "wrapper: Config.Workdir is
// required" before ever spawning the fake script. Post-fix,
// opencodeLayout.SpawnWorkdir falls back to bootDir, and this test proves
// the fake process genuinely spawns, streams a real delta, and completes.
func TestBoot_WrapperLifecycle_OpenCode(t *testing.T) {
	probeFile := filepath.Join(t.TempDir(), "opencode_config_dir_seen.txt")
	scriptPath := filepath.Join(t.TempDir(), "fake-opencode.sh")
	if err := os.WriteFile(scriptPath, []byte(fakeOpencodeRunScript), 0o755); err != nil {
		t.Fatalf("write fake opencode script: %v", err)
	}
	t.Setenv("OPENCODE_CLI_PATH", scriptPath)

	fanoutCh := make(chan llmtypes.StreamEvent, 32)
	store := newFakeRuntimeStore()
	pg := permission.NewPathGrants()
	profile := storeProfile("opencode")
	deps := &Dependencies{
		Agents:     &fakeAgentProfiles{profile: &profile},
		Manager:    NewSessionManager(),
		Store:      store,
		PathGrants: pg,
		ProviderAdapter: func(name string) provider.CLIAdapter {
			if name != "opencode" {
				return nil
			}
			return provider.NewOpencodeAdapter()
		},
		EventFanout:    func(string) chan<- llmtypes.StreamEvent { return fanoutCh },
		WorkspacesRoot: t.TempDir(),
	}
	t.Cleanup(func() { _ = deps.Manager.Shutdown(context.Background()) })

	sess, err := Boot(context.Background(), deps, Options{
		Mode: ModeOneShot,
		// Workdir intentionally left empty — see doc comment above.
		Role:          "executor",
		OneShotPrompt: "say hi",
		Env:           map[string]string{"NANITE_TEST_PROBE_FILE": probeFile},
	})
	if err != nil {
		t.Fatalf("Boot: %v (pre-fix this failed with \"wrapper: Config.Workdir is required\" — TASKS/agent-host-acp/18)", err)
	}
	bootDir := sess.BootDir

	// Same subprocess-per-turn "adapter runtime" shape as Codex — see
	// TestBoot_WrapperLifecycle_Codex_EnvParity's comment on why Stop is
	// called explicitly rather than waiting on Wait unprompted.
	var gotDelta bool
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case ev := <-fanoutCh:
			if ev.Type == llmtypes.EventDelta && strings.TrimSpace(ev.Content) == "hello from opencode" {
				gotDelta = true
			}
		case <-time.After(50 * time.Millisecond):
		}
		if gotDelta {
			break
		}
	}
	if !gotDelta {
		t.Fatalf("fanout: no EventDelta with opencode's stdout content observed within 15s")
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()
	if err := sess.Stop(stopCtx); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	waitCtx, waitCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer waitCancel()
	if err := sess.Wait(waitCtx); err != nil {
		t.Fatalf("Wait after Stop: %v", err)
	}

	// spawnWorkdir fell back to bootDir (this task's fix) — confirms both
	// wrapper.Config.Workdir and the persisted runtime row's Workdir
	// reflect the fallback, not an empty string.
	store.mu.Lock()
	var rowWorkdir string
	for _, row := range store.created {
		if row.ID == sess.ID {
			rowWorkdir = row.Workdir
		}
	}
	store.mu.Unlock()
	if rowWorkdir != bootDir {
		t.Errorf("runtime row Workdir = %q, want bootDir %q (SpawnWorkdir fallback)", rowWorkdir, bootDir)
	}

	seen, err := os.ReadFile(probeFile)
	if err != nil {
		t.Fatalf("read probe file (fake opencode process never ran, or wrapper child environment is broken): %v", err)
	}
	if string(seen) != bootDir {
		t.Errorf("fake opencode process observed OPENCODE_CONFIG_DIR=%q, want %q (wrapper ChildEnvironment is not propagating opencodeLayout.AmendEnv's redirect)", string(seen), bootDir)
	}
}
