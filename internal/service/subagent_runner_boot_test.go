package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/go-providers/provider"
	runtimeagent "github.com/hollis-labs/nanite/internal/runtime/agent"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/subagent"
)

// --- drainBootSession unit tests ---

func TestDrainBootSession_DeltasConcatenateIntoSummary(t *testing.T) {
	ch := make(chan llmtypes.StreamEvent, 8)
	ch <- llmtypes.StreamEvent{Type: llmtypes.EventDelta, Content: "Hello "}
	ch <- llmtypes.StreamEvent{Type: llmtypes.EventDelta, Content: "world"}
	ch <- llmtypes.StreamEvent{Type: llmtypes.EventDone}
	close(ch)

	summary, envelope, err := drainBootSession(ch)
	if err != nil {
		t.Fatalf("drainBootSession: %v", err)
	}
	if summary != "Hello world" {
		t.Errorf("summary = %q, want %q", summary, "Hello world")
	}
	if envelope != "{}" {
		t.Errorf("envelope = %q, want \"{}\"", envelope)
	}
}

func TestDrainBootSession_DoneTerminatesEarly(t *testing.T) {
	ch := make(chan llmtypes.StreamEvent, 8)
	ch <- llmtypes.StreamEvent{Type: llmtypes.EventDelta, Content: "first"}
	ch <- llmtypes.StreamEvent{Type: llmtypes.EventDone}
	ch <- llmtypes.StreamEvent{Type: llmtypes.EventDelta, Content: "after-done should not be read"}
	close(ch)

	summary, _, err := drainBootSession(ch)
	if err != nil {
		t.Fatalf("drainBootSession: %v", err)
	}
	if summary != "first" {
		t.Errorf("summary = %q, want only events before Done", summary)
	}
}

func TestDrainBootSession_ErrorEventTerminates(t *testing.T) {
	ch := make(chan llmtypes.StreamEvent, 8)
	ch <- llmtypes.StreamEvent{Type: llmtypes.EventDelta, Content: "partial"}
	ch <- llmtypes.StreamEvent{Type: llmtypes.EventError, Error: "runtime exploded"}
	close(ch)

	_, _, err := drainBootSession(ch)
	if err == nil {
		t.Fatal("expected error from drainBootSession, got nil")
	}
	if !errors.Is(err, errBootStreamFailure) {
		t.Errorf("error = %v, want wrapped errBootStreamFailure", err)
	}
	if !strings.Contains(err.Error(), "runtime exploded") {
		t.Errorf("error = %v, want preserved message", err)
	}
}

func TestDrainBootSession_ChannelCloseWithoutDoneIsCleanDrain(t *testing.T) {
	ch := make(chan llmtypes.StreamEvent, 4)
	ch <- llmtypes.StreamEvent{Type: llmtypes.EventDelta, Content: "partial"}
	close(ch)

	summary, _, err := drainBootSession(ch)
	if err != nil {
		t.Fatalf("drainBootSession: %v", err)
	}
	if summary != "partial" {
		t.Errorf("summary = %q", summary)
	}
}

func TestDrainBootSession_IgnoresOtherEventTypes(t *testing.T) {
	ch := make(chan llmtypes.StreamEvent, 8)
	ch <- llmtypes.StreamEvent{Type: llmtypes.EventToolUse}
	ch <- llmtypes.StreamEvent{Type: llmtypes.EventUsage}
	ch <- llmtypes.StreamEvent{Type: llmtypes.EventSessionID}
	ch <- llmtypes.StreamEvent{Type: llmtypes.EventDelta, Content: "real text"}
	ch <- llmtypes.StreamEvent{Type: llmtypes.EventDone}
	close(ch)

	summary, _, err := drainBootSession(ch)
	if err != nil {
		t.Fatalf("drainBootSession: %v", err)
	}
	if summary != "real text" {
		t.Errorf("summary = %q, want only delta content", summary)
	}
}

// --- BootRunner branch tests ---

// fakeBridge captures the per-session router binding so tests can drive
// runtime events without spinning up a real agentEventBridge.
type fakeBridge struct {
	mu      sync.Mutex
	bound   map[string]chan llmtypes.StreamEvent
	unbound []string
}

func newFakeBridge() *fakeBridge {
	return &fakeBridge{bound: make(map[string]chan llmtypes.StreamEvent)}
}

func (b *fakeBridge) SetPerSessionRouter(sessionID string, ch chan llmtypes.StreamEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if ch == nil {
		delete(b.bound, sessionID)
		b.unbound = append(b.unbound, sessionID)
		return
	}
	b.bound[sessionID] = ch
}

func (b *fakeBridge) chanFor(sessionID string) chan llmtypes.StreamEvent {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.bound[sessionID]
}

// fakeLegacyRunner records delegation calls so HTTP-provider branch tests
// can assert the fallback path was taken.
type fakeLegacyRunner struct {
	called int
	last   *subagent.Run
	result *subagent.Result
	err    error
}

func (f *fakeLegacyRunner) Run(_ context.Context, run *subagent.Run) (*subagent.Result, error) {
	f.called++
	f.last = run
	return f.result, f.err
}

func TestBootRunner_NilDeps(t *testing.T) {
	r := &BootRunner{}
	_, err := r.Run(context.Background(), &subagent.Run{Role: "x", ParentSessionID: "p", Prompt: "p"})
	if err == nil {
		t.Fatal("expected error on nil deps")
	}
}

func TestBootRunner_NilAgentsResolver(t *testing.T) {
	r := &BootRunner{deps: &runtimeagent.Dependencies{}}
	_, err := r.Run(context.Background(), &subagent.Run{Role: "x", ParentSessionID: "p", Prompt: "p"})
	if err == nil {
		t.Fatal("expected error on nil agents resolver")
	}
}

// TestBootRunner_ResolveRoleFails — when neither the requested slug NOR
// the `worker` fallback exists, resolveRole surfaces errRoleResolveFailed
// with both slugs in the message. CW-20260512-0002 (a): see also the
// fallback-happy-path test below.
func TestBootRunner_ResolveRoleFails(t *testing.T) {
	r := &BootRunner{
		deps:   &runtimeagent.Dependencies{},
		agents: &stubAgentReaderForRunner{agents: map[string]*store.AgentProfile{}},
	}
	_, err := r.Run(context.Background(), &subagent.Run{Role: "missing", ParentSessionID: "p", Prompt: "p"})
	if err == nil {
		t.Fatal("expected role-resolve error when both slug and fallback missing")
	}
	if !errors.Is(err, errRoleResolveFailed) {
		t.Errorf("error = %v, want wrapped errRoleResolveFailed", err)
	}
}

// TestBootRunner_ResolveRoleFallsBackToWorker verifies the BootRunner
// path also picks up the worker fallback. CW-20260512-0002 subtodo (a).
func TestBootRunner_ResolveRoleFallsBackToWorker(t *testing.T) {
	worker := &store.AgentProfile{ID: "ag-worker", Slug: "worker", DefaultProvider: "anthropic"}
	r := &BootRunner{
		deps:   &runtimeagent.Dependencies{},
		agents: &stubAgentReaderForRunner{agents: map[string]*store.AgentProfile{"worker": worker}},
	}
	agent, err := r.resolveRole("nanite-planner")
	if err != nil {
		t.Fatalf("resolveRole(\"nanite-planner\"): expected fallback, got %v", err)
	}
	if agent == nil || agent.Slug != "worker" {
		t.Errorf("resolveRole(\"nanite-planner\") returned slug=%q, want \"worker\"", agentSlugOrEmpty(agent))
	}
}

// TestBootRunner_HTTPProvider_DelegatesToLegacy verifies that an agent
// profile whose DefaultProvider has no CLI adapter routes to the legacy
// ChatRunner instead of attempting a Boot.
func TestBootRunner_HTTPProvider_DelegatesToLegacy(t *testing.T) {
	legacyResult := &subagent.Result{Summary: "from legacy", ResultJSON: `{"src":"legacy"}`}
	legacy := &fakeLegacyRunner{result: legacyResult}

	deps := &runtimeagent.Dependencies{
		ProviderAdapter: func(name string) provider.CLIAdapter { return nil }, // HTTP-only
	}
	r := &BootRunner{
		deps: deps,
		agents: &stubAgentReaderForRunner{agents: map[string]*store.AgentProfile{
			"role-http": {ID: "ag-http", DefaultProvider: "anthropic"},
		}},
		legacy: legacy,
	}

	got, err := r.Run(context.Background(), &subagent.Run{
		ID: "run-http", Role: "role-http", ParentSessionID: "p", Prompt: "go",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got != legacyResult {
		t.Errorf("result = %+v, want delegated legacy result", got)
	}
	if legacy.called != 1 {
		t.Errorf("legacy.called = %d, want 1", legacy.called)
	}
	if legacy.last == nil || legacy.last.Role != "role-http" {
		t.Errorf("legacy received unexpected run = %+v", legacy.last)
	}
}

// TestBootRunner_HTTPProvider_NoLegacyErrors verifies that when no legacy
// fallback is configured AND the provider has no CLI adapter, Run returns
// a clear configuration error instead of attempting a Boot.
func TestBootRunner_HTTPProvider_NoLegacyErrors(t *testing.T) {
	deps := &runtimeagent.Dependencies{
		ProviderAdapter: func(name string) provider.CLIAdapter { return nil },
	}
	r := &BootRunner{
		deps: deps,
		agents: &stubAgentReaderForRunner{agents: map[string]*store.AgentProfile{
			"role-http": {ID: "ag-http", DefaultProvider: "anthropic"},
		}},
		// legacy intentionally nil
	}
	_, err := r.Run(context.Background(), &subagent.Run{
		ID: "run-x", Role: "role-http", ParentSessionID: "p", Prompt: "p",
	})
	if err == nil {
		t.Fatal("expected error: no CLI adapter and no legacy fallback")
	}
	if !strings.Contains(err.Error(), "no CLI adapter") {
		t.Errorf("error = %v, want CLI-adapter wrap", err)
	}
}

// fakeCLIAdapter is a minimal provider.CLIAdapter for the canBoot branch.
type fakeCLIAdapter struct {
	name string
}

func (f *fakeCLIAdapter) Name() string                                       { return f.name }
func (f *fakeCLIAdapter) BuildArgs(_, _, _ string) []string                  { return nil }
func (f *fakeCLIAdapter) ParseLine(_ []byte) ([]llmtypes.StreamEvent, error) { return nil, nil }
func (f *fakeCLIAdapter) Detect() (string, bool)                             { return "", true }

// TestBootRunner_CLIProvider_BootsAndDrains exercises the happy path:
// canBoot true → createChildSession → persistChild → SetPerSessionRouter →
// stub booter returns a fake Session → test pushes EventDelta + EventDone
// onto the bound chan → drainBootSession returns the assembled summary.
func TestBootRunner_CLIProvider_BootsAndDrains(t *testing.T) {
	bridge := newFakeBridge()
	st := &recordingSessionStore{
		parents: map[string]*store.Session{
			"sess-parent": {ID: "sess-parent"},
		},
	}

	bootCalled := 0
	var capturedOpts runtimeagent.Options
	booter := func(_ context.Context, _ *runtimeagent.Dependencies, opts runtimeagent.Options) (*runtimeagent.Session, error) {
		bootCalled++
		capturedOpts = opts
		// Drive the per-session router asynchronously: the runtime would
		// emit deltas + Done; we simulate that here so drainBootSession
		// returns cleanly.
		go func() {
			ch := bridge.chanFor(opts.SessionID)
			if ch == nil {
				return
			}
			ch <- llmtypes.StreamEvent{Type: llmtypes.EventDelta, Content: "Project X has "}
			ch <- llmtypes.StreamEvent{Type: llmtypes.EventDelta, Content: "3 open tasks."}
			ch <- llmtypes.StreamEvent{Type: llmtypes.EventDone}
		}()
		// Return a Session shell. deps left nil → Stop returns
		// "session not initialized" which BootRunner swallows in defer.
		return &runtimeagent.Session{ID: opts.SessionID}, nil
	}

	deps := &runtimeagent.Dependencies{
		ProviderAdapter: func(name string) provider.CLIAdapter {
			if name == "claude" {
				return &fakeCLIAdapter{name: "claude"}
			}
			return nil
		},
	}

	r := &BootRunner{
		deps:   deps,
		bridge: bridge,
		agents: &stubAgentReaderForRunner{agents: map[string]*store.AgentProfile{
			"role-cli": {ID: "ag-cli", Slug: "role-cli", DefaultProvider: "claude", DefaultModel: "claude-sonnet-4-6"},
		}},
		store:     st,
		booter:    booter,
		persistFn: func(_ context.Context, _, _ string) error { return nil },
	}

	run := &subagent.Run{
		ID: "run-cli", Role: "role-cli", ParentSessionID: "sess-parent", Prompt: "summarize project x",
	}
	result, err := r.Run(context.Background(), run)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if bootCalled != 1 {
		t.Errorf("booter called %d times, want 1", bootCalled)
	}
	if result.Summary != "Project X has 3 open tasks." {
		t.Errorf("Summary = %q", result.Summary)
	}
	if result.ResultJSON != "{}" {
		t.Errorf("ResultJSON = %q, want \"{}\"", result.ResultJSON)
	}
	if run.ChildSessionID == "" {
		t.Error("ChildSessionID not set on run")
	}

	// Boot was called with ModeSubagent + parent + prompt threading.
	if capturedOpts.Mode != runtimeagent.ModeSubagent {
		t.Errorf("Boot Mode = %v, want ModeSubagent", capturedOpts.Mode)
	}
	if capturedOpts.ParentSessionID != "sess-parent" {
		t.Errorf("Boot ParentSessionID = %q", capturedOpts.ParentSessionID)
	}
	if capturedOpts.SessionID != run.ChildSessionID {
		t.Errorf("Boot SessionID = %q, want childID %q", capturedOpts.SessionID, run.ChildSessionID)
	}
	if capturedOpts.OneShotPrompt != run.Prompt {
		t.Errorf("Boot OneShotPrompt = %q, want %q", capturedOpts.OneShotPrompt, run.Prompt)
	}
	if capturedOpts.Role != run.Role {
		t.Errorf("Boot Role = %q, want %q", capturedOpts.Role, run.Role)
	}
}

// TestBootRunner_RunProviderOverride_ThreadsToBootOptions pins the
// CW-20260514-0053 round-1 Copilot finding: a subagent.Run with a
// per-spawn Provider override (e.g. "claude") whose agent profile
// declares an HTTP DefaultProvider must reach agent.Boot with
// opts.Provider populated. Pre-fix the runner used the override to
// gate canBoot but passed an empty Provider to runtimeagent.Boot, so
// agent.Boot fell back to profile.DefaultProvider (the wrong adapter).
//
// Mismatch shape:
//
//	agent.DefaultProvider = "anthropic"   (HTTP — no CLI adapter)
//	run.Provider          = "claude"      (CLI override)
//
// canBoot's effectiveProvider returns "claude" → adapter exists → boot
// path engages. The test asserts runBoot threads the same "claude" into
// runtimeagent.Options.Provider so agent.Boot dispatches the claude
// layout instead of "anthropic" (which has no CLI adapter and would
// surface as "no adapter registered for provider \"anthropic\"").
func TestBootRunner_RunProviderOverride_ThreadsToBootOptions(t *testing.T) {
	bridge := newFakeBridge()
	st := &recordingSessionStore{
		parents: map[string]*store.Session{
			"sess-parent": {ID: "sess-parent"},
		},
	}

	var capturedOpts runtimeagent.Options
	booter := func(_ context.Context, _ *runtimeagent.Dependencies, opts runtimeagent.Options) (*runtimeagent.Session, error) {
		capturedOpts = opts
		go func() {
			if ch := bridge.chanFor(opts.SessionID); ch != nil {
				ch <- llmtypes.StreamEvent{Type: llmtypes.EventDone}
			}
		}()
		return &runtimeagent.Session{ID: opts.SessionID}, nil
	}

	deps := &runtimeagent.Dependencies{
		ProviderAdapter: func(name string) provider.CLIAdapter {
			// Composition root would strip the "pty-" prefix; the test
			// stub matches the bare adapter name directly. canBoot's
			// effectiveProvider returns "claude" for this run, so this
			// is what the lookup sees.
			if name == "claude" {
				return &fakeCLIAdapter{name: "claude"}
			}
			return nil
		},
	}

	r := &BootRunner{
		deps:   deps,
		bridge: bridge,
		agents: &stubAgentReaderForRunner{agents: map[string]*store.AgentProfile{
			// Profile declares an HTTP default that has NO CLI adapter.
			// Without the fix, agent.Boot would fall back to this and
			// fail to dispatch a CLI layout.
			"role-mixed": {ID: "ag-mixed", Slug: "role-mixed", DefaultProvider: "anthropic"},
		}},
		store:     st,
		booter:    booter,
		persistFn: func(_ context.Context, _, _ string) error { return nil },
	}

	run := &subagent.Run{
		ID:              "run-mixed",
		Role:            "role-mixed",
		ParentSessionID: "sess-parent",
		Prompt:          "go",
		Provider:        "claude", // per-spawn CLI override
	}
	if _, err := r.Run(context.Background(), run); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if capturedOpts.Provider != "claude" {
		t.Fatalf("Options.Provider = %q, want %q (CW-20260514-0053 round-1: run.Provider must reach agent.Boot)",
			capturedOpts.Provider, "claude")
	}
}

// TestBootRunner_NoProviderOverride_LeavesOptionsProviderToProfileFallback
// is the no-regression case: when run.Provider is empty, the BootRunner
// still passes the agent profile's DefaultProvider into Options.Provider
// (via effectiveProvider's profile fallback). agent.Boot's
// effectiveProvider then sees opts.Provider == profile.DefaultProvider,
// which is the same name the legacy dispatch used — so the behavior is
// indistinguishable from pre-CW-20260514-0053 for non-override spawns.
func TestBootRunner_NoProviderOverride_LeavesOptionsProviderToProfileFallback(t *testing.T) {
	bridge := newFakeBridge()
	st := &recordingSessionStore{
		parents: map[string]*store.Session{
			"sess-parent": {ID: "sess-parent"},
		},
	}

	var capturedOpts runtimeagent.Options
	booter := func(_ context.Context, _ *runtimeagent.Dependencies, opts runtimeagent.Options) (*runtimeagent.Session, error) {
		capturedOpts = opts
		go func() {
			if ch := bridge.chanFor(opts.SessionID); ch != nil {
				ch <- llmtypes.StreamEvent{Type: llmtypes.EventDone}
			}
		}()
		return &runtimeagent.Session{ID: opts.SessionID}, nil
	}

	deps := &runtimeagent.Dependencies{
		ProviderAdapter: func(name string) provider.CLIAdapter {
			if name == "claude" {
				return &fakeCLIAdapter{name: "claude"}
			}
			return nil
		},
	}

	r := &BootRunner{
		deps:   deps,
		bridge: bridge,
		agents: &stubAgentReaderForRunner{agents: map[string]*store.AgentProfile{
			"role-cli": {ID: "ag-cli", Slug: "role-cli", DefaultProvider: "claude"},
		}},
		store:     st,
		booter:    booter,
		persistFn: func(_ context.Context, _, _ string) error { return nil },
	}

	run := &subagent.Run{
		ID:              "run-default",
		Role:            "role-cli",
		ParentSessionID: "sess-parent",
		Prompt:          "go",
		// Provider intentionally empty — use the profile default.
	}
	if _, err := r.Run(context.Background(), run); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if capturedOpts.Provider != "claude" {
		t.Fatalf("Options.Provider = %q, want %q (no-override case must still populate from profile)",
			capturedOpts.Provider, "claude")
	}
}

// TestBootRunner_BootError_UnbindsRouter verifies that a Boot failure
// releases the per-session router so a stale chan doesn't sit on the
// bridge after the spawn aborts.
func TestBootRunner_BootError_UnbindsRouter(t *testing.T) {
	bridge := newFakeBridge()
	st := &recordingSessionStore{
		parents: map[string]*store.Session{
			"sess-p": {ID: "sess-p"},
		},
	}
	bootErr := errors.New("boot failed deliberately")
	booter := func(_ context.Context, _ *runtimeagent.Dependencies, _ runtimeagent.Options) (*runtimeagent.Session, error) {
		return nil, bootErr
	}

	deps := &runtimeagent.Dependencies{
		ProviderAdapter: func(name string) provider.CLIAdapter { return &fakeCLIAdapter{name: name} },
	}
	r := &BootRunner{
		deps:   deps,
		bridge: bridge,
		agents: &stubAgentReaderForRunner{agents: map[string]*store.AgentProfile{
			"role-cli": {ID: "ag-cli", Slug: "role-cli", DefaultProvider: "claude"},
		}},
		store:     st,
		booter:    booter,
		persistFn: func(_ context.Context, _, _ string) error { return nil },
	}

	_, err := r.Run(context.Background(), &subagent.Run{
		ID: "run-bad", Role: "role-cli", ParentSessionID: "sess-p", Prompt: "p",
	})
	if err == nil {
		t.Fatal("expected boot error")
	}
	if !errors.Is(err, bootErr) {
		t.Errorf("error = %v, want wrapped boot error", err)
	}
	if len(bridge.unbound) != 1 {
		t.Errorf("router unbinds = %v, want exactly one cleanup", bridge.unbound)
	}
}

// TestBootRunner_CLIProvider_EmptySummaryFallback verifies that the
// per-Role placeholder summary lands when the runtime emits Done with no
// preceding deltas.
func TestBootRunner_CLIProvider_EmptySummaryFallback(t *testing.T) {
	bridge := newFakeBridge()
	st := &recordingSessionStore{
		parents: map[string]*store.Session{
			"sess-p": {ID: "sess-p"},
		},
	}
	booter := func(_ context.Context, _ *runtimeagent.Dependencies, opts runtimeagent.Options) (*runtimeagent.Session, error) {
		go func() {
			ch := bridge.chanFor(opts.SessionID)
			if ch != nil {
				ch <- llmtypes.StreamEvent{Type: llmtypes.EventDone}
			}
		}()
		return &runtimeagent.Session{ID: opts.SessionID}, nil
	}
	deps := &runtimeagent.Dependencies{
		ProviderAdapter: func(name string) provider.CLIAdapter { return &fakeCLIAdapter{name: name} },
	}
	r := &BootRunner{
		deps:   deps,
		bridge: bridge,
		agents: &stubAgentReaderForRunner{agents: map[string]*store.AgentProfile{
			"role-cli": {ID: "ag-cli", Slug: "role-cli", DefaultProvider: "claude"},
		}},
		store:     st,
		booter:    booter,
		persistFn: func(_ context.Context, _, _ string) error { return nil },
	}
	got, err := r.Run(context.Background(), &subagent.Run{
		ID: "run-empty", Role: "role-cli", ParentSessionID: "sess-p", Prompt: "p",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(got.Summary, "completed without text response") {
		t.Errorf("Summary = %q, want fallback wording", got.Summary)
	}
}

// TestBootRunner_PersistFailure_AbortsBeforeBoot verifies the persist-
// child-id step gates Boot — a failure here must not leave a Boot'd
// process running with no DB linkage.
func TestBootRunner_PersistFailure_AbortsBeforeBoot(t *testing.T) {
	st := &recordingSessionStore{
		parents: map[string]*store.Session{
			"sess-p": {ID: "sess-p"},
		},
	}
	bootCalled := false
	booter := func(_ context.Context, _ *runtimeagent.Dependencies, _ runtimeagent.Options) (*runtimeagent.Session, error) {
		bootCalled = true
		return nil, nil
	}
	deps := &runtimeagent.Dependencies{
		ProviderAdapter: func(name string) provider.CLIAdapter { return &fakeCLIAdapter{name: name} },
	}
	r := &BootRunner{
		deps:   deps,
		bridge: newFakeBridge(),
		agents: &stubAgentReaderForRunner{agents: map[string]*store.AgentProfile{
			"role-cli": {ID: "ag-cli", Slug: "role-cli", DefaultProvider: "claude"},
		}},
		store:     st,
		booter:    booter,
		persistFn: func(_ context.Context, _, _ string) error { return errors.New("persist db down") },
	}
	_, err := r.Run(context.Background(), &subagent.Run{
		ID: "run-x", Role: "role-cli", ParentSessionID: "sess-p", Prompt: "p",
	})
	if err == nil {
		t.Fatal("expected persist error")
	}
	if bootCalled {
		t.Error("booter should not run when persist fails")
	}
}

// TestBootRunner_ProviderOverride_RoutesToCLIPath confirms a per-spawn
// run.Provider override (e.g. caller forces pty-claude) flows through
// ProviderAdapter resolution and reaches the Boot path even when the
// agent profile's DefaultProvider would resolve elsewhere.
func TestBootRunner_ProviderOverride_RoutesToCLIPath(t *testing.T) {
	bridge := newFakeBridge()
	st := &recordingSessionStore{
		parents: map[string]*store.Session{
			"sess-p": {ID: "sess-p"},
		},
	}
	bootCalled := 0
	booter := func(_ context.Context, _ *runtimeagent.Dependencies, opts runtimeagent.Options) (*runtimeagent.Session, error) {
		bootCalled++
		go func() {
			ch := bridge.chanFor(opts.SessionID)
			if ch != nil {
				ch <- llmtypes.StreamEvent{Type: llmtypes.EventDone}
			}
		}()
		return &runtimeagent.Session{ID: opts.SessionID}, nil
	}
	deps := &runtimeagent.Dependencies{
		ProviderAdapter: func(name string) provider.CLIAdapter {
			if name == "pty-claude" {
				return &fakeCLIAdapter{name: name}
			}
			return nil
		},
	}
	legacy := &fakeLegacyRunner{}
	r := &BootRunner{
		deps:   deps,
		bridge: bridge,
		agents: &stubAgentReaderForRunner{agents: map[string]*store.AgentProfile{
			"role-mixed": {ID: "ag-m", Slug: "role-mixed", DefaultProvider: "anthropic"},
		}},
		store:     st,
		booter:    booter,
		persistFn: func(_ context.Context, _, _ string) error { return nil },
		legacy:    legacy,
	}
	_, err := r.Run(context.Background(), &subagent.Run{
		ID: "run-o", Role: "role-mixed", ParentSessionID: "sess-p", Prompt: "p",
		Provider: "pty-claude",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if bootCalled != 1 {
		t.Errorf("booter called %d times, want 1 (override should select CLI path)", bootCalled)
	}
	if legacy.called != 0 {
		t.Errorf("legacy.called = %d, want 0 (override should NOT delegate)", legacy.called)
	}
}
