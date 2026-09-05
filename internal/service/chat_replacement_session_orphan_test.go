package service

// TASKS/agent-host-acp/21: adoptReplacementSession must never silently
// overwrite a still-live activeSessions entry. observeSessionForRecovery
// (the real Session.Wait()-driven path) already Deletes its own entry
// BEFORE invoking the broker, so adoptReplacementSession's Store lands in
// an empty slot for that path. notifyRecoveryBrokerForHTTPStreamError
// (chat_http_broker_notify.go) has no such Delete — it reaches the same
// broker.OnSessionExit -> DispatchRetry -> replacement-session-hook chain
// from a chat-harness-level mid-stream error that is entirely independent
// of whether the underlying CLI process is still alive. This file builds
// the clean, single-variable repro task 21 calls for BEFORE any fix: a
// real, live *runtimeagent.Session cached in activeSessions, exactly one
// notifyRecoveryBrokerForHTTPStreamError call (no process exit), and a
// broker configured to actually DispatchRetry a CLI replacement.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/permission"
	"github.com/hollis-labs/nanite/internal/recovery/broker"
	runtimeagent "github.com/hollis-labs/nanite/internal/runtime/agent"
	"github.com/hollis-labs/nanite/internal/store"
)

// bootRealDeps constructs a real runtimeagent.Dependencies — real SQLite
// store, real agentsessions.Manager, fake (no-op) CLI adapter — that
// bootRealLongLivedSession can boot any number of sessions against.
// Shared across every boot within one test (rather than constructing a
// fresh store per boot): store.New's real migration path is measurably
// expensive under `go test -race` (confirmed directly — a single
// newConfigTestStore call costs several real seconds under -race, vs.
// sub-millisecond without it), so sharing one store per test keeps that
// cost from multiplying across this file's two-session-per-test shape.
// Booting two different sessionIDs against the same store/manager is
// safe — Boot doesn't share any other mutable state across sessions.
func bootRealDeps(t *testing.T) *runtimeagent.Dependencies {
	t.Helper()
	st := newConfigTestStore(t)
	deps := &runtimeagent.Dependencies{
		Agents: &fakeAgentProfilesResolver{profile: &store.AgentProfile{
			ID: "agent-1", Slug: "test-agent", DefaultProvider: "codex",
		}},
		Manager:    runtimeagent.NewSessionManager(),
		Store:      &agentRuntimeStore{store: st},
		PathGrants: permission.NewPathGrants(),
		ProviderAdapter: func(name string) provider.CLIAdapter {
			return &fakeCLIAdapter{name: name}
		},
		MCPConfig:      runtimeagent.MCPConfig{}, // empty DBPath disables .mcp.json planting
		WorkspacesRoot: t.TempDir(),
	}
	t.Cleanup(func() { _ = deps.Manager.Shutdown(context.Background()) })
	return deps
}

// bootRealLongLivedSession boots a REAL, live *runtimeagent.Session
// against the "codex" bootdir layout — the exact pattern
// internal/runtime/agent's own TestBoot_LongLived_HappyPath /
// TestSession_StopHappyPath use. ModeLongLived (AutoFireFirstTurn=false)
// against a subprocess-per-turn adapter never actually forks a process at
// Boot time, so this is fast and fully deterministic, while still
// exercising the real wrapper.Wrapper lifecycle: sess.Wait() genuinely
// blocks on the real runDone channel until Stop() (or a crash) closes it.
// That's the load-bearing property this repro needs — a fake/zero-value
// *runtimeagent.Session (nil wr) can't distinguish "stopped" from
// "never was live" the way this real one can.
func bootRealLongLivedSession(t *testing.T, deps *runtimeagent.Dependencies, sessionID string) *runtimeagent.Session {
	t.Helper()
	sess, err := runtimeagent.Boot(context.Background(), deps, runtimeagent.Options{
		Mode:                      runtimeagent.ModeLongLived,
		SessionID:                 sessionID,
		Provider:                  "codex",
		Workdir:                   t.TempDir(),
		ExternalLifecycleObserver: true,
	})
	if err != nil {
		t.Fatalf("bootRealLongLivedSession: Boot: %v", err)
	}
	return sess
}

// fakeAgentBootReturnsFixed is a broker.AgentBoot fake that always
// returns the same pre-built replacement session — stands in for a
// successful DispatchRetry -> agent.Boot call without spinning up a
// second real process.
type fakeAgentBootReturnsFixed struct {
	repl *runtimeagent.Session
}

func (f *fakeAgentBootReturnsFixed) Boot(_ context.Context, _ runtimeagent.Options) (*runtimeagent.Session, error) {
	return f.repl, nil
}

// TestAdoptReplacementSession_MidStreamErrorPath_StopsDisplacedSession is
// the task-21 clean repro AND regression test in one: it exercises the
// REAL production call chain — notifyRecoveryBrokerForHTTPStreamError ->
// broker.OnSessionExit -> Classify (Transient, first attempt) ->
// DispatchRetry (CLI/bootdir path, since provider="codex" has a real
// bootdir Layout) -> the replacement-session hook -> adoptReplacementSession
// — against a real, live old session cached in activeSessions, with no
// underlying process exit anywhere in the chain (mirrors path 2's actual
// trigger: a chat-harness-level mid-stream error, independent of whether
// the CLI process is still alive).
//
// Pre-fix: adoptReplacementSession's bare activeSessions.Store overwrites
// the live old session with the replacement and never calls .Stop() on
// what it displaced — the old session's Wait() never returns, i.e. it is
// orphaned exactly as task 21 describes. This test's assertion
// (oldSess.Wait must return before a short deadline) FAILS pre-fix,
// confirming the bug against real evidence rather than task 07's messier
// dogfeed observation.
//
// Post-fix: the displaced session is stopped before being evicted, so
// Wait() returns promptly.
func TestAdoptReplacementSession_MidStreamErrorPath_StopsDisplacedSession(t *testing.T) {
	const sessionID = "sess-orphan-repro-1"

	deps := bootRealDeps(t)

	oldSess := bootRealLongLivedSession(t, deps, sessionID)
	t.Cleanup(func() { _ = oldSess.Stop(context.Background()) })

	// The replacement must ALSO be real/live — see the analogous note in
	// TestAdoptReplacementSession_ObserveSessionForRecoveryPath_NoDoubleNotify:
	// adoptReplacementSession re-arms a fresh observeSessionForRecovery
	// goroutine for whatever it adopts, and a nil-wr shell's Wait()
	// returns immediately as a "clean exit", racing a bogus
	// activeSessions.Delete against this test's own assertions.
	replacement := bootRealLongLivedSession(t, deps, sessionID+"-replacement")
	replacement.ID = sessionID
	t.Cleanup(func() { _ = replacement.Stop(context.Background()) })

	svc := &chatServiceImpl{}
	svc.runtimeSessions().Store(sessionID, oldSess)

	adopted := make(chan struct{}, 1)
	b := broker.NewBroker(
		broker.Dependencies{AgentBoot: &fakeAgentBootReturnsFixed{repl: replacement}},
		broker.WithReplacementSessionHook(func(id string, sess *runtimeagent.Session) {
			svc.adoptReplacementSession(id, sess)
			adopted <- struct{}{}
		}),
	)
	svc.agentDeps = &runtimeagent.Dependencies{Recovery: b}

	// Path 2 exactly: a chat-harness mid-stream error notifies the broker
	// directly. No Session.Wait()/process-exit anywhere in this call —
	// oldSess is still genuinely alive (its own Wait() is still blocked)
	// the entire time this runs.
	svc.notifyRecoveryBrokerForHTTPStreamError(
		context.Background(), sessionID, "codex", "test-agent",
		errors.New("mid-stream boom"),
	)

	select {
	case <-adopted:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for replacement-session-hook adoption")
	}

	// The replacement must be what's live in activeSessions now — this
	// half of the contract already worked pre-fix; asserting it here
	// pins it against a regression from the fix itself.
	got, ok := svc.runtimeSessions().Load(sessionID)
	if !ok {
		t.Fatal("activeSessions has no entry for sessionID after adoption")
	}
	if got != replacement {
		t.Fatalf("activeSessions holds %+v, want the replacement session %+v", got, replacement)
	}

	// The load-bearing assertion: the displaced OLD session must be
	// stopped, not merely dropped from the map. A live, un-stopped
	// session's Wait() blocks forever (until ctx deadline); a stopped
	// one returns promptly once the wrapper's runDone channel closes.
	waitCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := oldSess.Wait(waitCtx)
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("old session was never stopped — orphaned live process/goroutine (TASKS/agent-host-acp/21)")
	}
}

// TestAdoptReplacementSession_ObserveSessionForRecoveryPath_NoDoubleNotify
// covers the double-notification/clobber race task 21 step 3 calls out:
// once the displaced old session is stopped as part of the fix, its own
// still-running observeSessionForRecovery goroutine must NOT route a
// second OnSessionExit to the broker (which could dispatch an unwanted
// extra retry) and must NOT clobber the replacement already stored by
// adoptReplacementSession.
func TestAdoptReplacementSession_ObserveSessionForRecoveryPath_NoDoubleNotify(t *testing.T) {
	const sessionID = "sess-orphan-repro-2"

	deps := bootRealDeps(t)

	oldSess := bootRealLongLivedSession(t, deps, sessionID)
	// The replacement must ALSO be a real, live session, not a bare
	// &runtimeagent.Session{} shell: adoptReplacementSession re-arms a
	// fresh observeSessionForRecovery goroutine for whatever it adopts,
	// and a nil-wr shell's Wait() returns immediately as a "clean exit",
	// which would make that re-armed observer itself delete the
	// replacement from activeSessions — a test artifact of an
	// under-built fake, not the race this test is actually pinning.
	replacement := bootRealLongLivedSession(t, deps, sessionID+"-replacement")
	replacement.ID = sessionID
	t.Cleanup(func() { _ = replacement.Stop(context.Background()) })

	// A single broker instance, shared by both the mid-stream-error
	// notify call AND whatever oldSess's own Wait-observer goroutine
	// might (incorrectly) route to it later — the real production shape
	// (one broker per chat service). env records every envelope emitted
	// so a second, unwanted OnSessionExit -> classify -> escalate cycle
	// is directly observable as a second envelope.
	env := &recordingEnvelopeSink{}
	b := broker.NewBroker(broker.Dependencies{
		AgentBoot: &fakeAgentBootReturnsFixed{repl: replacement},
		Envelope:  env,
	})

	svc := &chatServiceImpl{agentDeps: &runtimeagent.Dependencies{Recovery: b}}
	svc.runtimeSessions().Store(sessionID, oldSess)
	b.SetReplacementSessionHook(svc.adoptReplacementSession)

	// A second, independent Wait-observer goroutine for oldSess — mirrors
	// the real goroutine driveBootSession spawned when oldSess was first
	// booted (observeSessionForRecovery), which is still running and
	// blocked on oldSess.Wait() at the moment adoptReplacementSession
	// fires.
	observerDone := make(chan struct{})
	go func() {
		defer close(observerDone)
		svc.observeSessionForRecovery(oldSess, sessionID, "", "codex", "", time.Now(), false)
	}()

	// Path 2: the mid-stream chat-loop error notify, exactly as in the
	// first test above.
	svc.notifyRecoveryBrokerForHTTPStreamError(
		context.Background(), sessionID, "codex", "test-agent",
		errors.New("mid-stream boom"),
	)

	// oldSess's own observer must settle (Stop() — dispatched as part of
	// the fix — causes its Wait() to return) without routing a second
	// OnSessionExit to the broker.
	select {
	case <-observerDone:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for oldSess's observeSessionForRecovery to observe the stop")
	}

	// Exactly one envelope: the original transient-retry info-card from
	// the mid-stream-error notify. A second envelope means oldSess's own
	// observer incorrectly treated its Stop-induced exit as a fresh
	// crash and routed a second OnSessionExit to the broker — the
	// double-notification/clobber race task 21 step 3 calls out.
	if got := env.snapshot(); len(got) != 1 {
		t.Fatalf("broker emitted %d envelope(s), want exactly 1 — a second envelope means oldSess's stop-induced exit was misrouted to the broker as a fresh crash (double-notify race)", len(got))
	}

	// The replacement must still be the live activeSessions entry — the
	// old observer's own cleanup must not have clobbered it.
	got, ok := svc.runtimeSessions().Load(sessionID)
	if !ok {
		t.Fatal("activeSessions has no entry for sessionID after both observers settled")
	}
	if got != replacement {
		t.Fatalf("activeSessions holds %+v after oldSess's observer settled, want the replacement %+v (clobber race)", got, replacement)
	}
}

// recordingEnvelopeSink is a broker.EnvelopeSink fake that records every
// emitted envelope so tests can assert on how many recovery-flow cycles
// actually ran.
type recordingEnvelopeSink struct {
	mu   sync.Mutex
	envs []broker.Envelope
}

func (r *recordingEnvelopeSink) Emit(_ string, env broker.Envelope) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.envs = append(r.envs, env)
	return nil
}

func (r *recordingEnvelopeSink) snapshot() []broker.Envelope {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]broker.Envelope, len(r.envs))
	copy(out, r.envs)
	return out
}
