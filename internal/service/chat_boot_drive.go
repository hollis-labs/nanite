package service

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	agentsessions "github.com/hollis-labs/go-agent-sessions/agentsessions"
	llmtypes "github.com/hollis-labs/go-llm-types"
	ctxpkg "github.com/hollis-labs/nanite/internal/context"
	"github.com/hollis-labs/nanite/internal/fsutil"
	runtimeagent "github.com/hollis-labs/nanite/internal/runtime/agent"
	"github.com/hollis-labs/nanite/internal/runtime/agent/recovery"
	"github.com/hollis-labs/nanite/internal/store"
)

// driveBootSession is the long-lived-PTY counterpart to provider.StreamChat
// for CLI agents. Called from chat_generate.go's per-iteration provider call
// site when chat.IsCLIProvider(providerName) is true.
//
// Lifecycle per call:
//
//  1. Look up the active runtime session for sessionID. Boot if absent.
//  2. If active and any of the System / Agent / Mode / Rules slots changed
//     since the last turn, regenerate CLAUDE.md / agent-context.md in the
//     boot dir and SendInput a re-read instruction so the agent reloads
//     context.
//  3. Compose the per-turn UserContext payload (reminder injections + user
//     message) and SendInput.
//  4. Construct a per-turn turnCh, bind it on the agentEventBridge so the
//     runtime's EventFanout routes events here instead of broadcasting SSE.
//     Spawn a watcher goroutine that unbinds + closes turnCh on ctx cancel.
//
// The chat-harness loop consumes the returned chan via its existing
// for-range-provCh streamLoop. Done / Error events close turnCh via the
// bridge's close-once guard, terminating the streamLoop.
//
// Iteration > 0 is not supported for CLI sessions (claude handles tools
// internally → no tool_use blocks return through provCh → loop exits after
// iter 0). On iter > 0 we log + return an immediately-closed chan so the
// outer harness exits gracefully without crashing the session.
func (s *chatServiceImpl) driveBootSession(
	ctx context.Context,
	sessionID string,
	session *store.Session,
	agent *store.AgentProfile,
	mode *store.AgentMode,
	slotResult *SlotAssemblyResult,
	userContent string,
	iteration int,
) (<-chan llmtypes.StreamEvent, error) {
	if s.agentDeps == nil || s.agentEventBridge == nil {
		return nil, errors.New("driveBootSession: agent runtime not wired (AgentDeps / AgentEventBridge nil)")
	}
	if iteration > 0 {
		// Defensive: CLI agents shouldn't loop beyond the first iteration
		// because tool execution happens inside the agent process. If we
		// ever land here it's most likely a compaction-recovery retry that
		// fired against a CLI session — which is a no-op for the long-lived
		// path. Surface a closed chan so the outer harness exits.
		slog.Warn("driveBootSession: iteration > 0 for CLI session — returning closed chan",
			"session_id", sessionID, "iter", iteration)
		closed := make(chan llmtypes.StreamEvent)
		close(closed)
		return closed, nil
	}

	// 1. Look up the active runtime session.
	var sess *runtimeagent.Session
	if v, ok := s.activeSessions.Load(sessionID); ok {
		if existing, typeOK := v.(*runtimeagent.Session); typeOK {
			sess = existing
		}
	}

	// 2. Boot when absent.
	if sess == nil {
		profileSlug := ""
		if agent != nil {
			profileSlug = agent.Slug
		}
		workdir := bootSessionWorkdir(session)
		role := bootSessionRole(agent, mode)
		booted, err := runtimeagent.Boot(ctx, s.agentDeps, runtimeagent.Options{
			Mode:         runtimeagent.ModeLongLived,
			SessionID:    sessionID,
			AgentProfile: profileSlug,
			Workdir:      workdir,
			Role:         role,
		})
		if err != nil {
			return nil, fmt.Errorf("driveBootSession: boot: %w", err)
		}
		s.activeSessions.Store(sessionID, booted)
		// Stamp the slot hash so the first follow-up turn doesn't trigger
		// a redundant regen.
		s.activeSessionSlots.Store(sessionID, hashSlots(slotResult))
		sess = booted

		// Spawn the session-lifetime Wait observer. When the runtime
		// process terminates, the recovery broker classifies + (when
		// applicable) dispatches a replacement. Uses context.Background
		// so the goroutine outlives the per-turn ctx; the manager's
		// Shutdown drain unblocks Wait at daemon shutdown.
		bootedAt := time.Now()
		bootedSession := booted
		bootedProfile := profileSlug
		bootedWorkdir := workdir
		go s.observeSessionForRecovery(bootedSession, sessionID, bootedProfile, bootedWorkdir, bootedAt)
	} else if s.slotsChangedFor(sessionID, slotResult) {
		// 3. Refresh the boot dir when System / Agent / Mode / Rules
		// slots have shifted. UserContext changes per turn by design and
		// flows via SendInput; we don't regen CLAUDE.md for it.
		if err := s.regenerateBootDirSlots(sess.BootDir, agent, mode); err != nil {
			slog.Warn("driveBootSession: slot regen failed",
				"session_id", sessionID, "err", err)
		} else {
			if err := sess.SendInput([]byte(slotRereadInstruction)); err != nil {
				slog.Warn("driveBootSession: send reread instruction failed",
					"session_id", sessionID, "err", err)
			}
		}
	}

	// 4. Compose + deliver the per-turn payload.
	payload := composeUserPayload(slotResult, userContent)
	if err := sess.SendInput([]byte(payload)); err != nil {
		return nil, fmt.Errorf("driveBootSession: send input: %w", err)
	}

	// 5. Bind the per-turn router. The bridge writes runtime events into
	// turnCh until Done / Error flow through (close-once via the bridge)
	// or until the watcher goroutine below clears the router on ctx cancel.
	turnCh := make(chan llmtypes.StreamEvent, 64)
	s.agentEventBridge.SetPerSessionRouter(sessionID, turnCh)

	go func() {
		<-ctx.Done()
		// Unbind + close. SetPerSessionRouter(nil) is idempotent against
		// the bridge's own close-on-Done path.
		s.agentEventBridge.SetPerSessionRouter(sessionID, nil)
	}()

	return turnCh, nil
}

// composeUserPayload assembles the per-turn payload SendInput delivers to the
// long-lived CLI agent. UserContext slot content (reminders + intent hints)
// leads the user message so the agent processes both as a single turn input.
func composeUserPayload(slotResult *SlotAssemblyResult, userContent string) string {
	var b strings.Builder
	if slotResult != nil && slotResult.Window != nil {
		if slot := slotResult.Window.Slot(ctxpkg.SlotUserContext); slot != nil && slot.Content != "" {
			b.WriteString(slot.Content)
			b.WriteString("\n\n")
		}
	}
	b.WriteString(userContent)
	return b.String()
}

// slotRereadInstruction is the system message SendInput delivers after a
// slot regeneration so the agent reloads CLAUDE.md and agent-context.md.
const slotRereadInstruction = "System: configuration updated. Please reread CLAUDE.md and apply the latest mode/agent context."

// slotsChangedFor returns true when the System / Agent / Mode / Rules slot
// content hash differs from the last hash stamped for sessionID. Updates the
// stamp before returning. UserContext is excluded — it changes per turn by
// design and flows via SendInput, not the boot dir.
func (s *chatServiceImpl) slotsChangedFor(sessionID string, slotResult *SlotAssemblyResult) bool {
	current := hashSlots(slotResult)
	prev, ok := s.activeSessionSlots.Load(sessionID)
	if !ok {
		s.activeSessionSlots.Store(sessionID, current)
		return false
	}
	if prevHash, hashOK := prev.(uint64); hashOK && prevHash == current {
		return false
	}
	s.activeSessionSlots.Store(sessionID, current)
	return true
}

// hashSlots produces a stable FNV-1a hash over the boot-dir-relevant slots.
// Empty slots and nil slotResult both hash to 0, so a missing-then-empty
// transition is not treated as a change.
func hashSlots(slotResult *SlotAssemblyResult) uint64 {
	if slotResult == nil || slotResult.Window == nil {
		return 0
	}
	h := fnv.New64a()
	for _, name := range []string{
		ctxpkg.SlotSystem,
		ctxpkg.SlotAgent,
		ctxpkg.SlotMode,
		ctxpkg.SlotRules,
	} {
		if slot := slotResult.Window.Slot(name); slot != nil {
			_, _ = h.Write([]byte(name))
			_, _ = h.Write([]byte{0})
			_, _ = h.Write([]byte(slot.Content))
			_, _ = h.Write([]byte{0})
		}
	}
	return h.Sum64()
}

// adoptReplacementSession is the recovery-broker replacement hook. The
// broker calls this synchronously from its orchestration loop after a
// successful DispatchRetry so the chat-side activeSessions map binds to
// the freshly booted process — without this, the next user turn would
// not find an entry under sessionID and boot yet another session,
// orphaning the broker's replacement.
//
// The chat composition root installs this via Broker.SetReplacementSessionHook
// once both chatServiceImpl and the broker exist (see container.go).
//
// Also re-arms the per-session Wait observer so the replacement's own
// terminal exits route back through the broker. Without this, the
// replacement would not be observed and a second-tier failure would go
// unrecovered.
func (s *chatServiceImpl) adoptReplacementSession(sessionID string, sess *runtimeagent.Session) {
	if sess == nil {
		return
	}
	s.activeSessions.Store(sessionID, sess)
	// Slot hash + tool-partition state reset is implicit: the prior
	// session's Delete(sessionID) ran before adoptReplacementSession is
	// invoked (see observeSessionForRecovery's call ordering), so the
	// replacement starts with a clean slot/regen window. The boot dir
	// itself is reused — agent.Boot's IsRelaunch=true path skips
	// CreateRuntimeRow + workdir reseed.

	// Re-arm the Wait observer for the replacement. The broker may
	// dispatch additional retries up to its hard cap; without a fresh
	// observer the second terminal exit would not surface to the
	// broker.
	go s.observeSessionForRecovery(sess, sessionID, "", "", time.Now())
}

// observeSessionForRecovery is the Wait-observer goroutine that watches
// a booted runtime session and routes terminal *agentsessions.ExitError
// to the recovery broker via deps.Recovery.OnSessionExit. Spawned per
// session at boot time; exits when the session terminates.
//
// The meta bag carries chat-side context the broker's classifier
// consumes — agent profile, workdir, session age. Future iterations
// extend this to include stderr tail, sandbox state, MCP transport
// health (the BootDir/MCP/Credentials adapter wiring).
func (s *chatServiceImpl) observeSessionForRecovery(sess *runtimeagent.Session, sessionID, agentProfile, workdir string, bootedAt time.Time) {
	if sess == nil || s.agentDeps == nil || s.agentDeps.Recovery == nil {
		return
	}

	// Wait for the session to terminate. Manager.Shutdown unblocks this
	// at daemon shutdown so the goroutine never leaks past process exit.
	err := sess.Wait(context.Background())

	// Extract structured exit info. errors.As walks the chain; nil
	// (clean exit) returns false and we skip the broker hook.
	var xe *agentsessions.ExitError
	if !errors.As(err, &xe) {
		// Clean exit — nothing for the broker to recover.
		s.activeSessions.Delete(sessionID)
		s.activeSessionSlots.Delete(sessionID)
		s.toolPartitionStates.Delete(sessionID)
		// Comma-ok rather than panicking type assert: future
		// RecoveryHooks impls (mocks in tests) may not expose
		// ClearSession; the cleanup is best-effort.
		if broker, ok := s.agentDeps.Recovery.(*recovery.Broker); ok {
			broker.ClearSession(sessionID)
		}
		return
	}

	meta := map[string]any{
		recovery.MetaKeyAgentProfile: agentProfile,
		recovery.MetaKeyWorkdir:      workdir,
		recovery.MetaKeyMode:         "long_lived",
		recovery.MetaKeySessionAge:   time.Since(bootedAt),
	}

	slog.Warn("recovery: session exited with error — invoking broker",
		"session_id", sessionID,
		"cause", xe.Cause,
		"code", xe.Code,
		"signal", xe.Signal)

	// Per-session state cleanup happens BEFORE OnSessionExit. The broker
	// may dispatch a replacement session (DispatchRetry → agent.Boot),
	// at which point it invokes the replacement-session hook installed
	// at container.go and that hook re-stores the new session into
	// activeSessions. Cleaning up after OnSessionExit returns would
	// race-clobber the freshly stored replacement.
	//
	// toolPartitionStates is session-id-keyed too — the replacement
	// session boots fresh, so pruning here mirrors the activeSessions
	// reset.
	s.activeSessions.Delete(sessionID)
	s.activeSessionSlots.Delete(sessionID)
	s.toolPartitionStates.Delete(sessionID)

	s.agentDeps.Recovery.OnSessionExit(sessionID, xe, meta)
}

// regenerateBootDirSlots rewrites the boot dir's CLAUDE.md and
// .sandbox/agent-context.md atomically. Called when slotsChangedFor returns
// true mid-session. Phase 4c.5.
func (s *chatServiceImpl) regenerateBootDirSlots(bootDir string, agent *store.AgentProfile, mode *store.AgentMode) error {
	if bootDir == "" {
		return errors.New("regenerateBootDirSlots: empty bootDir")
	}
	if agent == nil {
		return errors.New("regenerateBootDirSlots: nil agent profile")
	}
	claudePath := filepath.Join(bootDir, "CLAUDE.md")
	if err := fsutil.AtomicWriteFile(claudePath, []byte(runtimeagent.BuildCLAUDEMD(agent.Name, agent.Description)), 0o644); err != nil {
		return fmt.Errorf("regen CLAUDE.md: %w", err)
	}
	contextPath := filepath.Join(bootDir, ".sandbox", "agent-context.md")
	if err := fsutil.AtomicWriteFile(contextPath, []byte(runtimeagent.BuildAgentContext(agent, mode)), 0o644); err != nil {
		return fmt.Errorf("regen agent-context.md: %w", err)
	}
	return nil
}

// bootSessionWorkdir picks the workdir Boot should hand the runtime. The
// chat-harness today binds claude / codex to the per-session sandbox dir
// (cwd = sandbox dir, no project dir threaded). The new boot-dir-based path
// preserves that shape: empty Workdir lets the layout's SpawnWorkdir use the
// boot dir as cwd. Project-repo workdir threading is a follow-up — chat
// sessions don't carry a resolved project path through to this layer today.
func bootSessionWorkdir(session *store.Session) string {
	_ = session
	return ""
}

// bootSessionRole derives a role identifier from the agent profile + mode for
// system-prompt assembly. Returns the empty string when neither the profile
// nor the mode advertises a role-specific slug; the agent package's
// composeSystemPrompt handles empty roles via its null-role default.
func bootSessionRole(agent *store.AgentProfile, mode *store.AgentMode) string {
	if mode != nil && mode.Slug != "" {
		return mode.Slug
	}
	if agent != nil && agent.Slug != "" {
		return agent.Slug
	}
	return ""
}
