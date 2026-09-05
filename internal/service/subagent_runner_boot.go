package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/nanite/internal/permission"
	runtimeagent "github.com/hollis-labs/nanite/internal/runtime/agent"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/subagent"
)

// errBootStreamFailure is the sentinel returned by drainBootSession when the
// per-session router emits an EventError.
var errBootStreamFailure = errors.New("subagent: boot session emitted error event")

// drainBootSession consumes a chan llmtypes.StreamEvent (the bridge's
// per-session router output for a Boot'd subagent) and assembles a
// summary string + envelope payload for subagent.Result. Pure logic so the
// rules can be unit-tested without spawning a runtime.
//
// Rules:
//   - EventDelta: append Content to the summary builder.
//   - EventThinking: ignored (thinking blocks are observability, not output).
//   - EventDone: terminal; return cleanly.
//   - EventError: terminal; return errBootStreamFailure wrapping ev.Error.
//   - everything else (EventToolUse, EventUsage, EventSessionID): ignored.
//
// Returns summary, envelope (always valid JSON; "{}" today since the
// long-lived PTY surface does not yet emit a structured envelope through
// llmtypes.StreamEvent), and a non-nil error if the stream emitted an
// error event. Channel close without EventDone is treated as a clean drain.
func drainBootSession(ch <-chan llmtypes.StreamEvent) (summary string, envelope string, err error) {
	var sb strings.Builder
	envelope = "{}"

	for ev := range ch {
		switch ev.Type {
		case llmtypes.EventDelta:
			sb.WriteString(ev.Content)
		case llmtypes.EventDone:
			return sb.String(), envelope, nil
		case llmtypes.EventError:
			msg := ev.Error
			if msg == "" {
				msg = "stream error event with no message"
			}
			return sb.String(), envelope, errors.Join(errBootStreamFailure, errors.New(msg))
		}
	}
	return sb.String(), envelope, nil
}

// bootEventBridge is the narrow surface BootRunner needs from
// agentEventBridge so tests can stub the per-session router without a real
// bridge. Satisfied structurally by *agentEventBridge.
type bootEventBridge interface {
	SetPerSessionRouter(sessionID string, ch chan llmtypes.StreamEvent)
	PrepareRuntimeTurnOwner(sessionID string, ch chan llmtypes.StreamEvent) bool
}

// agentBooter is the narrow surface BootRunner needs to spawn a Boot'd
// subagent. Satisfied structurally by a function pointer to
// runtimeagent.Boot; production wiring uses runtimeagent.Boot directly,
// tests inject a stub that returns a fake *runtimeagent.Session.
type agentBooter func(ctx context.Context, deps *runtimeagent.Dependencies, opts runtimeagent.Options) (*runtimeagent.Session, error)

// legacyHTTPRunner is the narrow surface BootRunner uses to delegate
// HTTP-provider subagents through the existing chat-harness drive. Satisfied
// structurally by *ChatRunner.
type legacyHTTPRunner interface {
	Run(ctx context.Context, run *subagent.Run) (*subagent.Result, error)
}

// BootRunner implements subagent.Runner by spawning the child as a Boot'd
// process (ModeSubagent) for CLI providers, or delegating to the legacy
// chat-harness ChatRunner for HTTP-provider profiles. Lives in package
// service for unexported access to chatServiceImpl + agentEventBridge.
//
// Architectural alignment (locked session-3, see
// session-progress-phase-4c1-and-bridge.md): spawned agents are Boot'd
// processes routed through the messaging service, not chat-harness child
// sessions. BootRunner.Run returns a *subagent.Result; subagent.Service
// then delivers that Result to the parent via messaging.SendMessage
// (channel=inbox for async modes, channel=chat for sync/api). No
// per-event forwarding here — recommended option A from the implementer
// prompt's Phase 4a.4.
type BootRunner struct {
	deps       *runtimeagent.Dependencies
	bridge     bootEventBridge
	agents     agentSlugResolver
	store      sessionStoreForRunner
	db         *sql.DB
	pathGrants *permission.PathGrants
	legacy     legacyHTTPRunner

	// Test-only override hooks. Production wiring leaves these nil.
	booter    agentBooter
	persistFn func(ctx context.Context, runID, childID string) error
}

// NewBootRunner constructs a BootRunner. deps is the composed agent
// Dependencies (shares the agentsessions.Manager + bridge with the chat
// service). bridge is the agentEventBridge so per-session routers can be
// bound for the spawned subagent. legacy is the HTTP-provider fallback
// runner (today: *ChatRunner); pass nil only if HTTP subagents are
// out-of-scope for the caller (returns an error in that case). All other
// deps are required.
func NewBootRunner(
	deps *runtimeagent.Dependencies,
	bridge bootEventBridge,
	agents agentSlugResolver,
	st sessionStoreForRunner,
	db *sql.DB,
	pathGrants *permission.PathGrants,
	legacy legacyHTTPRunner,
) *BootRunner {
	return &BootRunner{
		deps:       deps,
		bridge:     bridge,
		agents:     agents,
		store:      st,
		db:         db,
		pathGrants: pathGrants,
		legacy:     legacy,
	}
}

// Run spawns the subagent. CLI providers (claude/codex/opencode etc., as
// resolved via deps.ProviderAdapter) Boot a fresh ModeSubagent process
// and stream events back through the bridge's per-session router; HTTP
// providers (anthropic/openai/etc.) delegate to the legacy ChatRunner.
//
// Branch decision is structural: a CLI adapter resolved by the deps
// ProviderAdapter resolver is the signal we can Boot; otherwise the
// child session has no spawnable runtime and must use the chat-harness
// drive against an HTTP provider.
func (r *BootRunner) Run(ctx context.Context, run *subagent.Run) (*subagent.Result, error) {
	if r.deps == nil {
		return nil, errors.New("subagent BootRunner: nil deps")
	}
	if r.agents == nil {
		return nil, errors.New("subagent BootRunner: nil agents resolver")
	}

	agent, err := r.resolveRole(run.Role)
	if err != nil {
		return nil, err
	}

	if !r.canBoot(agent, run) {
		if r.legacy == nil {
			return nil, fmt.Errorf("subagent BootRunner: no CLI adapter for provider %q and no legacy fallback configured", r.effectiveProvider(agent, run))
		}
		return r.legacy.Run(ctx, run)
	}

	return r.runBoot(ctx, run, agent)
}

// resolveRole looks up the role slug. Mirrors ChatRunner.resolveRole —
// unknown slugs fall back to the `worker` profile with a slog.Warn; other
// errors wrap with errRoleResolveFailed. See resolveRoleWithFallback.
func (r *BootRunner) resolveRole(slug string) (*store.AgentProfile, error) {
	return resolveRoleWithFallback(r.agents, slug, "BootRunner")
}

// canBoot reports whether the resolved agent's effective provider has a
// CLI adapter registered with deps.ProviderAdapter. The composition root
// strips the legacy "pty-"/"sub-" registry prefixes inside ProviderAdapter
// so callers can pass a session-side provider name verbatim.
func (r *BootRunner) canBoot(agent *store.AgentProfile, run *subagent.Run) bool {
	if r.deps == nil || r.deps.ProviderAdapter == nil {
		return false
	}
	return r.deps.ProviderAdapter(r.effectiveProvider(agent, run)) != nil
}

// effectiveProvider resolves the per-spawn provider override, then the
// agent profile default. Mirrors ChatRunner.createChildSession's resolution
// for the spawn-time decision (without the parent-inherit fallback —
// the boot path doesn't need to defer to parent provider since the
// adapter resolver is the authoritative signal).
func (r *BootRunner) effectiveProvider(agent *store.AgentProfile, run *subagent.Run) string {
	if run != nil && run.Provider != "" {
		return run.Provider
	}
	if agent != nil {
		return agent.DefaultProvider
	}
	return ""
}

// runBoot is the CLI-provider path. Creates a child session row (kept for
// transcript storage continuity per architectural alignment #4),
// persists the child id back onto subagent_runs, binds a per-session
// router so runtime events stream into our drain channel, then Boots
// ModeSubagent. PathGrants lineage is registered by Boot itself when
// Mode==ModeSubagent, so the manual stamping the legacy ChatRunner does
// is gone here.
func (r *BootRunner) runBoot(ctx context.Context, run *subagent.Run, agent *store.AgentProfile) (*subagent.Result, error) {
	childID, err := r.createChildSession(ctx, run, agent)
	if err != nil {
		return nil, err
	}
	if err := r.persistChild(ctx, run.ID, childID); err != nil {
		return nil, err
	}
	run.ChildSessionID = childID

	eventsCh := make(chan llmtypes.StreamEvent, 64)
	if r.bridge != nil {
		// ModeSubagent auto-fires inside Wrapper.Run, before Boot returns its
		// Session. Hand the exact router through the RuntimeEventSink factory so
		// the first TurnStarted cannot outrun owner registration.
		if !r.bridge.PrepareRuntimeTurnOwner(childID, eventsCh) {
			close(eventsCh)
			return nil, errors.New("subagent BootRunner: prepare runtime turn owner")
		}
	} else {
		// No bridge wired — the drain would block forever waiting for
		// EventDone. Synthesize a closed channel up-front so the drain
		// returns immediately with the empty-summary fallback. Tests
		// that don't exercise event routing can leave bridge nil.
		close(eventsCh)
	}

	// CW-20260514-0053: thread the same effective provider that canBoot
	// used into Options.Provider so agent.Boot's bootdir + adapter
	// dispatch sees the per-spawn override (run.Provider). Without this
	// the runtime falls back to profile.DefaultProvider — and when the
	// profile carries an HTTP default like "anthropic" while run.Provider
	// names a CLI adapter like "claude", agent.Boot would either fail
	// outright or launch the wrong adapter.
	sess, bootErr := r.boot(ctx, runtimeagent.Options{
		Mode:            runtimeagent.ModeSubagent,
		ParentSessionID: run.ParentSessionID,
		AgentProfile:    agent.Slug,
		SessionID:       childID,
		Workdir:         "",
		Role:            run.Role,
		OneShotPrompt:   run.Prompt,
		Provider:        r.effectiveProvider(agent, run),
	})
	if bootErr != nil {
		if r.bridge != nil {
			r.bridge.SetPerSessionRouter(childID, nil)
		}
		return nil, fmt.Errorf("subagent BootRunner: boot subagent: %w", bootErr)
	}
	defer func() {
		if sess != nil {
			_ = sess.Stop(context.Background())
		}
	}()

	summary, envelope, drainErr := drainBootSession(eventsCh)
	if drainErr != nil {
		return nil, drainErr
	}
	if summary == "" {
		summary = fmt.Sprintf("subagent %s completed without text response", run.Role)
	}
	return &subagent.Result{Summary: summary, ResultJSON: envelope}, nil
}

// boot delegates to the test override or runtimeagent.Boot. Allows tests
// to inject a stub that exercises the wiring without a real spawn.
func (r *BootRunner) boot(ctx context.Context, opts runtimeagent.Options) (*runtimeagent.Session, error) {
	if r.booter != nil {
		return r.booter(ctx, r.deps, opts)
	}
	return runtimeagent.Boot(ctx, r.deps, opts)
}

// persistChild delegates to the test override or the real
// persistChildSessionID. Mirrors ChatRunner.persistChild.
func (r *BootRunner) persistChild(ctx context.Context, runID, childID string) error {
	if r.persistFn != nil {
		return r.persistFn(ctx, runID, childID)
	}
	return r.persistChildSessionID(ctx, runID, childID)
}

// persistChildSessionID writes child_session_id back onto subagent_runs.
// Same shape as ChatRunner.persistChildSessionID; kept here so BootRunner
// is self-contained and the legacy runner can be deleted independently.
func (r *BootRunner) persistChildSessionID(ctx context.Context, runID, childID string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE subagent_runs SET child_session_id = ? WHERE id = ?`,
		childID, runID,
	)
	if err != nil {
		return fmt.Errorf("persist child_session_id: %w", err)
	}
	return nil
}

// createChildSession creates the child store.Session row + binds it to
// the resolved agent. Mirrors ChatRunner.createChildSession's provider
// resolution (run.Provider → agent.DefaultProvider → parent.Provider).
//
// Per architectural alignment #4: the child session row is kept (still
// a store.Session for transcript storage / FE drawer continuity) but
// nothing populates it from chat.generateResponse anymore — the typed
// events surface through the bridge instead.
func (r *BootRunner) createChildSession(ctx context.Context, run *subagent.Run, agent *store.AgentProfile) (string, error) {
	parent, err := r.store.GetSession(ctx, run.ParentSessionID)
	if err != nil {
		return "", fmt.Errorf("get parent session: %w", err)
	}
	prov := run.Provider
	if prov == "" {
		prov = agent.DefaultProvider
	}
	if prov == "" {
		prov = parent.Provider
	}
	childID := uuid.New().String()
	if err := r.store.CreateSession(ctx, &store.Session{
		ID:       childID,
		Provider: prov,
		Model:    agent.DefaultModel,
		Title:    fmt.Sprintf("subagent: %s — %s", run.Role, truncatePrompt(run.Prompt, 60)),
	}); err != nil {
		return "", fmt.Errorf("create child session: %w", err)
	}
	if err := r.store.EnsureSessionAgent(ctx, childID, agent.ID, "default", true); err != nil {
		return "", fmt.Errorf("bind child session to agent: %w", err)
	}
	return childID, nil
}
