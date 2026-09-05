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

	agentsessions "github.com/hollis-labs/agentkit/agentsessions"
	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/nanite/internal/chat"
	ctxpkg "github.com/hollis-labs/nanite/internal/context"
	"github.com/hollis-labs/nanite/internal/fsutil"
	"github.com/hollis-labs/nanite/internal/recovery/broker"
	runtimeagent "github.com/hollis-labs/nanite/internal/runtime/agent"
	"github.com/hollis-labs/nanite/internal/store"
)

// staleResumeFastExitWindow is how soon after a --resume cold-boot a runtime
// exit must occur for the recovery path to treat it as a stale provider session
// id (and clear the stored id so the next turn cold-boots without --resume).
// Healthy mid-conversation exits well past this window keep their resume id.
// CW-20260525-0001 Slice 3 follow-up.
const staleResumeFastExitWindow = 5 * time.Second

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
//     context. Independent of that slot-hash gate — TASKS/skills/10:
//     (re)plant this agent's plantable skill set into the boot dir every
//     turn of an already-active session with a real boot dir, so a
//     newly-granted skill appears without a full session restart (skill
//     grants are structurally invisible to the slot hash, since
//     CLI-hosted skill delivery adds nothing to any prompt slot — see
//     PlantAgentSkillFiles's call site below for the full reasoning).
//     Skipped entirely for a session with no boot dir (ACP-protocol
//     sessions — internal/runtime/agent/agent_acp.go — leave
//     Session.BootDir permanently empty by design).
//  3. Construct a per-turn turnCh, bind it on the agentEventBridge so the
//     runtime's EventFanout routes events here instead of broadcasting SSE.
//     Spawn a watcher goroutine that unbinds + closes turnCh on ctx cancel.
//  4. Compose the per-turn UserContext payload (reminder injections + user
//     message) and SendInput it — in a goroutine so the turn's events
//     stream into turnCh while the harness consumes them.
//
// The chat-harness loop consumes the returned chan via its existing
// for-range-provCh streamLoop. Done / Error events close turnCh via the
// bridge's close-once guard, terminating the streamLoop.
//
// CW-20260518-0074: the router MUST be bound before SendInput, and
// SendInput MUST run asynchronously. The subprocess-per-turn adapter
// runtime (codex / opencode `exec` mode) makes SendInput synchronous —
// runner.Run spawns the CLI, drives the whole turn, and fires every
// EventFanout event (deltas + EventDone) before SendInput returns. If the
// router were bound only after SendInput (as Phase 4c.4 did, when only the
// long-lived streaming-stdio claude path existed and SendInput returned
// immediately), every event of a codex turn would be emitted with no
// router bound — fanned out as SSE (so the GUI still renders the reply
// live) but never delivered to turnCh. The harness streamLoop would then
// accumulate nothing and persist an empty `{"v":1,"text":""}` messages
// row, so the reply vanishes on reload. Binding first + sending async
// closes that gap for both runtime shapes.
//
// Iteration > 0 is not supported for CLI sessions (claude handles tools
// internally → no tool_use blocks return through provCh → loop exits after
// iter 0). On iter > 0 we log + return an immediately-closed chan so the
// outer harness exits gracefully without crashing the session.
// Phase 0 item 21 ("Cut Modes, in full") removed this function's `mode
// *store.AgentMode` parameter — Legacy Agent Mode is gone.
func (s *chatServiceImpl) driveBootSession(
	ctx context.Context,
	sessionID string,
	session *store.Session,
	agent *store.AgentProfile,
	slotResult *SlotAssemblyResult,
	userContent string,
	iteration int,
	providerName string,
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
	if existing, ok := s.runtimeSessions().Load(sessionID); ok {
		sess = existing
	}

	// CW-20260525-0001: capture cold-boot BEFORE the boot block reassigns
	// sess. A cold boot with prior persisted history means the host restarted
	// (or the runtime was evicted) — the first post-restart payload gets a
	// recovery pack so the fresh agent resumes from recovered context.
	coldBooted := sess == nil
	// A cold boot auto-recovers UNLESS an intentional reboot armed the
	// one-shot fresh-boot flag (consumed here). Recover() and daemon restarts
	// leave the flag unset, so they recover.
	recoverThisBoot := s.shouldRecoverColdBoot(sessionID, coldBooted)

	// 2. Boot when absent.
	if sess == nil {
		profileSlug := ""
		if agent != nil {
			profileSlug = agent.Slug
		}
		workdir := bootSessionWorkdir(session)
		role := bootSessionRole(agent)
		bootOpts := runtimeagent.Options{
			Mode:         runtimeagent.ModeLongLived,
			SessionID:    sessionID,
			AgentProfile: profileSlug,
			Workdir:      workdir,
			Role:         role,
		}
		applyLegacyCLIProviderToBootOpts(&bootOpts, providerName)
		// Phase 2 item 02 (TASKS/phase-2/02-port-forward-dynamic-resolver.md):
		// resolve this agent's DB-configured cmd/http context resolvers, if
		// any, and fold the output into the boot prompt via
		// Options.DynamicContext. Runs for every agent — not gated behind a
		// bootprofile session — since the whole point of the port is making
		// this a first-class mechanism available to every agent.
		if agent != nil && agent.ID != "" {
			blocks, resolveErr := s.resolveAgentContextForBoot(ctx, agent.ID, workdir)
			if resolveErr != nil {
				return nil, fmt.Errorf("driveBootSession: resolve agent context: %w", resolveErr)
			}
			if len(blocks) > 0 {
				bootOpts.DynamicContext = blocks
				s.activeSessionContextBlocks.Store(sessionID, blocks)
			}
		}
		// CW-20260525-0001 Slice 3: resume the provider's prior session after a
		// host restart. Read the captured provider_session_id BEFORE Boot —
		// CreateRuntimeRow upserts the row and clears the column. When present,
		// Claude resumes its real session (full context); the Slice 1 recovery
		// pack still plants as a safety net in case resume silently no-ops.
		// Skipped for an intentional fresh reboot (recoverThisBoot=false).
		usedResume := false
		if recoverThisBoot && s.store != nil {
			if pid, perr := s.store.AgentRuntimeProviderSessionID(ctx, sessionID); perr != nil {
				slog.Warn("driveBootSession: provider-session lookup failed", "session_id", sessionID, "err", perr)
			} else if pid != "" {
				bootOpts.ResumeProviderSessionID = pid
				usedResume = true
				slog.Info("driveBootSession: resuming provider session after cold boot", "session_id", sessionID)
			}
		}
		booted, err := runtimeagent.Boot(ctx, s.agentDeps, bootOpts)
		if err != nil {
			return nil, fmt.Errorf("driveBootSession: boot: %w", err)
		}
		s.runtimeSessions().Store(sessionID, booted)
		// Track the bootDir + Options on the recovery BootDirOps adapter
		// so a Repopulate / RegenerateCLAUDEMD remediation can rebuild
		// the same SetupParams without us re-encoding them ad-hoc here.
		// nil-safe: chat-service tests / standalone configs without the
		// recovery wiring leave agentBootDirAdapter nil.
		if s.agentBootDirAdapter != nil {
			s.agentBootDirAdapter.Track(sessionID, booted.BootDir, bootOpts)
		}
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
		bootedProvider := bootOpts.Provider
		bootedWorkdir := workdir
		go s.observeSessionForRecovery(bootedSession, sessionID, bootedProfile, bootedProvider, bootedWorkdir, bootedAt, usedResume)
	} else {
		// 3. Refresh the boot dir when System / Agent / Mode / Rules
		// slots have shifted. UserContext changes per turn by design and
		// flows via SendInput; we don't regen CLAUDE.md for it.
		if s.slotsChangedFor(sessionID, slotResult) {
			if err := s.regenerateBootDirSlots(sessionID, sess.BootDir, agent); err != nil {
				slog.Warn("driveBootSession: slot regen failed",
					"session_id", sessionID, "err", err)
			} else {
				if err := sess.SendInput([]byte(slotRereadInstruction)); err != nil {
					slog.Warn("driveBootSession: send reread instruction failed",
						"session_id", sessionID, "err", err)
				}
			}
		}
		// 3a. TASKS/skills/10: (re)plant this agent's plantable skill set
		// into the already-running session's boot dir. Deliberately run
		// on every turn of an active session, independent of
		// slotsChangedFor above — granting/revoking a skill is
		// structurally invisible to the System/Agent/Mode/Rules slot
		// hash (CLI-hosted skill delivery adds nothing to any prompt
		// slot at all, per docs/engineering/architecture/20-skills.md's
		// explicit "no Nanite-specific rendering" instruction), so gating
		// this on the same slot-hash check the CLAUDE.md regen uses would
		// make a newly-granted skill invisible until an unrelated slot
		// happened to change too. The operation itself is cheap (a
		// couple of DB lookups plus a handful of small file writes,
		// typically zero-to-few granted skills per agent) and
		// idempotent/additive (SkillPlantFiles + writePlantedFile), so
		// re-checking every turn is the correct granularity for "a newly
		// granted skill appears without a full session restart."
		//
		// Guarded on sess.BootDir != "" (TASKS/skills/10's Fix-required
		// item 2 / ESCALATIONS.md's 2026-08-22 MEDIUM finding): ACP-
		// protocol sessions (internal/runtime/agent/agent_acp.go's
		// bootACP) leave Session.BootDir permanently empty by design —
		// there is no native-CLI boot dir to plant skill files into at
		// all for that backend. Without this guard,
		// PlantAgentSkillFiles's own "empty bootDir" error fired (and
		// got logged) on every single turn of every ACP-driven agent,
		// forever — real steady-state log noise for a currently-shipped
		// session type. regenerateBootDirSlots above has the identical
		// failure mode on an empty bootDir but is gated behind
		// slotsChangedFor, so it only misfires occasionally; this call
		// runs unconditionally every turn, so it needs its own explicit
		// guard rather than relying on the same incidental tolerance.
		if agent != nil && agent.ID != "" && sess.BootDir != "" {
			if err := runtimeagent.PlantAgentSkillFiles(ctx, s.agentDeps, sess.BootDir, sess.Provider, agent.ID); err != nil {
				slog.Warn("driveBootSession: skill replant failed",
					"session_id", sessionID, "err", err)
			}
		}
	}

	// 4. Bind the per-turn router BEFORE SendInput. The bridge writes
	// runtime events into turnCh until Done / Error flow through
	// (close-once via the bridge) or until the watcher goroutine below
	// clears the router on ctx cancel.
	//
	// CW-20260518-0074: ordering is load-bearing. SendInput is
	// synchronous for the subprocess-per-turn adapter runtime (codex /
	// opencode `exec`): runner.Run spawns the CLI and fires every
	// EventFanout event — including EventDone — before SendInput returns.
	// The router must already be bound or those events route to SSE
	// instead of turnCh and the harness persists an empty assistant row.
	turnCh := make(chan llmtypes.StreamEvent, 64)
	s.agentEventBridge.SetPerSessionRouter(sessionID, turnCh)

	go func() {
		<-ctx.Done()
		// Unbind + close. SetPerSessionRouter(nil) is idempotent against
		// the bridge's own close-on-Done path.
		s.agentEventBridge.SetPerSessionRouter(sessionID, nil)
	}()

	// 5. Compose + deliver the per-turn payload asynchronously. SendInput
	// blocks for the entire turn on the subprocess-per-turn runtime, so
	// running it here would deadlock driveBootSession against its own
	// caller (the harness streamLoop only starts draining turnCh after
	// this function returns). The streaming-stdio runtime returns from
	// SendInput immediately; running it in a goroutine is harmless there.
	//
	// A SendInput failure is surfaced into turnCh as an EventError so the
	// harness streamLoop classifies + persists it on the normal error
	// path; we then clear the router so the channel is closed and the
	// loop terminates (the bridge only auto-closes on a runtime-emitted
	// Done/Error, which never arrives when SendInput itself failed).
	payload := s.composeBootPayload(sessionID, session, agent, sess.BootDir, slotResult, userContent, recoverThisBoot)
	go func() {
		if err := sess.SendInput([]byte(payload)); err != nil {
			slog.Warn("driveBootSession: send input failed",
				"session_id", sessionID, "err", err)
			if v, ok := s.agentEventBridge.routers.Load(sessionID); ok {
				if r, rOK := v.(*sessionRouter); rOK {
					// Non-blocking; drops if the router already closed or
					// its buffer is full. Either way the harness still
					// terminates via the unbind below.
					r.send(llmtypes.StreamEvent{
						Type:  llmtypes.EventError,
						Error: fmt.Sprintf("driveBootSession: send input: %v", err),
					})
				}
			}
			s.agentEventBridge.SetPerSessionRouter(sessionID, nil)
		}
	}()

	return turnCh, nil
}

// resolveAgentContextForBoot loads agentID's enabled
// agent_context_resolvers rows and resolves them through
// runtimeagent.ResolveContextBlocks (Phase 2 item 02,
// TASKS/phase-2/02-port-forward-dynamic-resolver.md), returning the
// slot-name -> resolved-content map that driveBootSession folds into
// Options.DynamicContext.
//
// Returns (nil, nil) when the store isn't wired, agentID is empty, or
// the agent has no resolvers configured — the common case, and the
// pre-existing behavior for every session that predates this
// mechanism.
//
// A resolver-level failure (cmd non-zero exit, HTTP non-2xx, a bad
// timeout string, …) is NOT swallowed: it propagates as a boot error,
// mirroring the pre-port bootprofile.ResolveRequirements behavior — a
// half-resolved boot prompt is worse than a clean stop. An operator
// who wants a flaky resolver to stop blocking an agent's boot should
// disable that row (agent_context_resolvers.enabled) rather than rely
// on silent degradation here.
func (s *chatServiceImpl) resolveAgentContextForBoot(ctx context.Context, agentID, workdir string) (map[string]string, error) {
	if s.store == nil || agentID == "" {
		return nil, nil
	}
	rows, err := s.store.ListEnabledAgentContextResolvers(ctx, agentID)
	if err != nil {
		return nil, fmt.Errorf("list agent_context_resolvers: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	blocks, err := runtimeagent.ResolveContextBlocks(ctx, rows, workdir)
	if err != nil {
		return nil, fmt.Errorf("agent %q: %w", agentID, err)
	}
	return blocks, nil
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
//
// TASKS/agent-host-acp/21: this function's own doc comment used to state
// an invariant — "the prior session's Delete(sessionID) ran before
// adoptReplacementSession is invoked" — that only actually holds for the
// observeSessionForRecovery call path (path 1: it Deletes its own entry
// BEFORE calling the broker, so this function's Store lands in an empty
// slot). notifyRecoveryBrokerForHTTPStreamError (chat_http_broker_notify.go,
// path 2 — a chat-harness-level mid-stream error, entirely independent of
// whether the underlying process actually exited) has no such Delete: a
// live CLI session can still be cached under sessionID when its broker
// call reaches here. A confirmed clean repro (chat_replacement_session_
// orphan_test.go) showed the bare Store this function used to do silently
// overwrote that live session — no .Stop() anywhere — orphaning its real
// subprocess (plus MCP sidecar) and leaving its own
// observeSessionForRecovery goroutine blocked on Wait() forever.
//
// Fixed here (not narrowly in notifyRecoveryBrokerForHTTPStreamError)
// because this is the one seam every replacement-adoption call path
// funnels through — defensive against any future third caller reaching
// the broker without its own pre-clear, not just today's two.
// activeSessions.Swap atomically captures whatever was displaced so
// stopDisplacedSession can tear it down; the common path-1 case (prev is
// absent, or prev == sess on an idempotent re-invoke) is a no-op here,
// identical to the pre-fix behavior for that path.
func (s *chatServiceImpl) adoptReplacementSession(sessionID string, sess *runtimeagent.Session) {
	if sess == nil {
		return
	}
	if prev, hadPrev := s.runtimeSessions().Swap(sessionID, sess); hadPrev {
		if prev != nil && prev != sess {
			s.stopDisplacedSession(sessionID, prev)
		}
	}
	// Slot hash + tool-partition state reset is implicit: the prior
	// session's Delete(sessionID) ran before adoptReplacementSession is
	// invoked (see observeSessionForRecovery's call ordering), so the
	// replacement starts with a clean slot/regen window. The boot dir
	// itself is reused — agent.Boot's IsRelaunch=true path skips
	// CreateRuntimeRow + workdir reseed.

	// Refresh the BootDir adapter's registry entry — the relaunched
	// session has a fresh $TMPDIR-rolled bootDir but reuses the original
	// Options (agent profile, workdir, role). agent.Boot's IsRelaunch=true
	// path doesn't reset the registry, so we re-Track with the new
	// bootDir while preserving the prior entry's Options shape.
	if s.agentBootDirAdapter != nil {
		if prev, ok := s.agentBootDirAdapter.lookup(sessionID); ok {
			s.agentBootDirAdapter.Track(sessionID, sess.BootDir, prev.opts)
		}
	}

	// Re-arm the Wait observer for the replacement. The broker may
	// dispatch additional retries up to its hard cap; without a fresh
	// observer the second terminal exit would not surface to the
	// broker. Replacement boots dispatched by the broker do not carry the
	// caller's resume context, so usedResume=false here — stale-resume
	// clearing only applies to the initial driveBootSession path.
	// CW-20260526-0002: thread the original boot's provider so the next
	// terminal exit's meta bag carries it through to a downstream
	// DispatchRetry. The bootdir adapter is the surviving record of the
	// initial boot's Options; nil-safe lookup returns "" which the broker
	// then degrades on (default provider) — same as the pre-fix shape, so
	// no regression for tests / configs without the adapter wired.
	replacementProvider := ""
	if s.agentBootDirAdapter != nil {
		if entry, ok := s.agentBootDirAdapter.lookup(sessionID); ok {
			replacementProvider = entry.opts.Provider
		}
	}
	go s.observeSessionForRecovery(sess, sessionID, "", replacementProvider, "", time.Now(), false)
}

// stopDisplacedSession cooperatively tears down prev — the session
// adoptReplacementSession (TASKS/agent-host-acp/21) just displaced from
// activeSessions in favor of a broker-dispatched replacement. Runs on its
// own lifecycle-tracked goroutine with a bounded grace period so
// adoptReplacementSession — documented as running synchronously on the
// broker's own orchestration loop — never blocks on prev's cooperative
// SIGTERM-then-SIGKILL escalation.
//
// Flags prev itself (by pointer, in displacedSessions — see chat.go's
// field doc for why this is keyed by object identity rather than
// sessionID) BEFORE calling Stop. prev's own still-running
// observeSessionForRecovery goroutine is blocked on prev.Wait() at this
// point (spawned back when prev was first booted); once Stop causes that
// Wait to return, the flag lets that goroutine recognize the exit as
// deliberate — mirroring RebootSessionAgent's identical
// flag-before-Stop ordering for the reboot path (chat_session_reboot.go)
// — instead of misreading a SIGTERM/SIGKILL exit as a fresh crash and
// routing a second, unwanted OnSessionExit to the broker for a session
// that's already been superseded (the double-notification/clobber race
// task 21 names explicitly).
func (s *chatServiceImpl) stopDisplacedSession(sessionID string, prev *runtimeagent.Session) {
	s.displacedSessions.Store(prev, struct{}{})
	s.goTracked("recovery.stop-displaced-session", func(ownerCtx context.Context) {
		stopCtx, cancel := context.WithTimeout(ownerCtx, stopRebootGrace)
		defer cancel()
		if err := prev.Stop(stopCtx); err != nil {
			slog.Warn("adoptReplacementSession: stop displaced session failed",
				"session_id", sessionID, "err", err)
		}
	})
}

// observeSessionForRecovery is the Wait-observer goroutine that watches
// a booted runtime session and routes terminal *agentsessions.ExitError
// to the recovery broker via deps.Recovery.OnSessionExit. Spawned per
// session at boot time; exits when the session terminates.
//
// The meta bag carries chat-side context the broker's classifier
// consumes — agent profile, provider, workdir, session age. Future
// iterations extend this to include stderr tail, sandbox state, MCP
// transport health (the BootDir/MCP/Credentials adapter wiring).
// CW-20260526-0002: provider is load-bearing — DispatchRetry threads
// ev.Provider into agent.Options so the replacement boots on the same
// runner. An empty provider here would silently re-select the agent
// profile's DefaultProvider, masking provider-specific bugs across
// the retry boundary.
func (s *chatServiceImpl) observeSessionForRecovery(sess *runtimeagent.Session, sessionID, agentProfile, provider, workdir string, bootedAt time.Time, usedResume bool) {
	if sess == nil || s.agentDeps == nil || s.agentDeps.Recovery == nil {
		return
	}

	// Wait for the session to terminate. Manager.Shutdown unblocks this
	// at daemon shutdown so the goroutine never leaks past process exit.
	err := sess.Wait(context.Background())

	// TASKS/agent-host-acp/21: sess was displaced by adoptReplacementSession
	// and stopped on purpose (stopDisplacedSession flagged this exact sess
	// pointer before calling Stop). Treat the exit as deliberate regardless
	// of how Stop surfaced it, exactly like the rebootingSessions check
	// below — but keyed by the sess pointer itself, not sessionID, so a
	// fast-failing REPLACEMENT booted concurrently under the same
	// sessionID can never steal this flag (see chat.go's displacedSessions
	// field doc). CompareAndDelete(sessionID, sess) is what actually
	// closes the double-notification/clobber race: activeSessions already
	// holds the replacement by the time this fires, so the compare fails
	// and the replacement is left untouched — this goroutine simply exits
	// without ever reaching the broker.
	//
	// Deliberately does NOT call broker.ClearSession(sessionID) the way
	// the rebootingSessions branch below does: sessionID's classifier
	// state (attempt count) belongs to the recovery sequence that just
	// dispatched THIS replacement, and stays live for whatever the
	// replacement itself does next — clearing it here would silently
	// reset the broker's hard-cap attempt counter every time a
	// mid-stream-error-triggered replacement supersedes an older
	// session, undermining the "escalate to permanent after N attempts"
	// guard the cap exists for.
	if _, displaced := s.displacedSessions.LoadAndDelete(sess); displaced {
		s.runtimeSessions().CompareAndDelete(sessionID, sess)
		s.activeSessionSlots.Delete(sessionID)
		s.toolPartitionStates.Delete(sessionID)
		slog.Info("recovery: displaced session stopped by adoptReplacementSession — skipping broker",
			"session_id", sessionID)
		return
	}

	// CW-20260516-0057: intentional per-session reboot. RebootSessionAgent
	// stopped this runtime on purpose and flagged it before calling Stop.
	// Treat the exit as deliberate regardless of how Stop surfaced it — a
	// SIGTERM/SIGKILL exit can present as an *agentsessions.ExitError —
	// and do NOT route it to the recovery broker as a crash. The next
	// user turn cold-boots a fresh agent via driveBootSession.
	if _, rebooting := s.rebootingSessions.LoadAndDelete(sessionID); rebooting {
		// CompareAndDelete so a replacement a concurrent turn already
		// stored is not clobbered. The aux maps are session-id keyed and
		// a fresh boot re-stores them, so a plain Delete is safe there.
		s.runtimeSessions().CompareAndDelete(sessionID, sess)
		s.activeSessionSlots.Delete(sessionID)
		s.toolPartitionStates.Delete(sessionID)
		if broker, ok := s.agentDeps.Recovery.(*broker.Broker); ok {
			broker.ClearSession(sessionID)
		}
		slog.Info("recovery: session exited via intentional reboot — skipping broker",
			"session_id", sessionID)
		return
	}

	// Extract structured exit info. errors.As walks the chain; nil
	// (clean exit) returns false and we skip the broker hook.
	var xe *agentsessions.ExitError
	if !errors.As(err, &xe) {
		// Clean exit — nothing for the broker to recover.
		s.runtimeSessions().Delete(sessionID)
		s.activeSessionSlots.Delete(sessionID)
		s.toolPartitionStates.Delete(sessionID)
		// Comma-ok rather than panicking type assert: future
		// RecoveryHooks impls (mocks in tests) may not expose
		// ClearSession; the cleanup is best-effort.
		if broker, ok := s.agentDeps.Recovery.(*broker.Broker); ok {
			broker.ClearSession(sessionID)
		}
		return
	}

	// CW-20260525-0001 Slice 3 follow-up: stale-resume detection. A boot that
	// used --resume and died within a few seconds is overwhelmingly likely to
	// have been rejected by the provider for a stale/unknown session id (the
	// stored provider_session_id outlived its provider-side session). Clear
	// the stale id here so the NEXT user turn cold-boots without --resume —
	// the recovery pack still plants host-side context. The 5s window keeps
	// healthy mid-conversation errors from losing their valid resume id.
	if usedResume && s.store != nil && time.Since(bootedAt) < staleResumeFastExitWindow {
		if clearErr := s.store.SetAgentRuntimeProviderSessionID(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, sessionID, ""); clearErr != nil {
			slog.Warn("recovery: clear stale provider_session_id failed",
				"session_id", sessionID, "err", clearErr)
		} else {
			slog.Warn("recovery: resumed boot died fast — cleared stored provider_session_id so the next turn cold-boots without --resume",
				"session_id", sessionID,
				"age_ms", time.Since(bootedAt).Milliseconds(),
				"cause", xe.Cause,
				"code", xe.Code)
		}
	}

	meta := buildSessionExitMeta(agentProfile, provider, workdir, time.Since(bootedAt))

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
	s.runtimeSessions().Delete(sessionID)
	s.activeSessionSlots.Delete(sessionID)
	s.toolPartitionStates.Delete(sessionID)

	s.agentDeps.Recovery.OnSessionExit(sessionID, xe, meta)
}

// buildSessionExitMeta is the pure meta-bag composer the Wait-observer
// hands to the recovery broker on a terminal exit. Extracted so the
// composition (especially MetaKeyProvider — CW-20260526-0002) is
// exercised by a direct unit test without needing a real
// *runtimeagent.Session to Wait on.
func buildSessionExitMeta(agentProfile, provider, workdir string, sessionAge time.Duration) map[string]any {
	return map[string]any{
		broker.MetaKeyAgentProfile: agentProfile,
		broker.MetaKeyProvider:     provider,
		broker.MetaKeyWorkdir:      workdir,
		broker.MetaKeyMode:         "long_lived",
		broker.MetaKeySessionAge:   sessionAge,
	}
}

// regenerateBootDirSlots rewrites the boot dir's CLAUDE.md and
// .sandbox/agent-context.md atomically. Called when slotsChangedFor returns
// true mid-session. Phase 4c.5.
//
// Phase 0 item 21 ("Cut Modes, in full") removed this function's `mode
// *store.AgentMode` parameter — Legacy Agent Mode is gone.
func (s *chatServiceImpl) regenerateBootDirSlots(sessionID, bootDir string, agent *store.AgentProfile) error {
	if bootDir == "" {
		return errors.New("regenerateBootDirSlots: empty bootDir")
	}
	if agent == nil {
		return errors.New("regenerateBootDirSlots: nil agent profile")
	}
	claudePath := filepath.Join(bootDir, "CLAUDE.md")
	// CW-20260516-0007 round 1: recompute the SAME resolved boot prompt
	// the initial Boot planted, so a mid-session slot refresh doesn't
	// silently thin the session's operating instructions.
	// resolveBootPrompt's inputs: role (from the agent profile), the runtime
	// Mode (ModeLongLived for every chat session — the only caller), and an
	// optional Options.BootPromptOverride (currently unset by any chat-side
	// caller — see runtimeagent.Options's doc comment for the mechanism).
	role := bootSessionRole(agent)
	bootPromptOverride := ""
	// Phase 2 item 02: re-thread the SAME resolved dynamic-context blocks
	// the initial Boot stashed, so a mid-session slot regen doesn't
	// silently drop a resolver's live-fetched data the way a bare
	// re-derive from role/profile/override alone would.
	var dynamicContext map[string]string
	if v, ok := s.activeSessionContextBlocks.Load(sessionID); ok {
		if blocks, blocksOK := v.(map[string]string); blocksOK {
			dynamicContext = blocks
		}
	}
	systemPrompt := runtimeagent.ResolveSystemPrompt(role, agent, runtimeagent.ModeLongLived, bootPromptOverride, dynamicContext)
	if err := fsutil.AtomicWriteFile(claudePath, []byte(runtimeagent.BuildCLAUDEMD(agent.Name, agent.Description, systemPrompt)), 0o644); err != nil {
		return fmt.Errorf("regen CLAUDE.md: %w", err)
	}
	contextPath := filepath.Join(bootDir, ".sandbox", "agent-context.md")
	if err := fsutil.AtomicWriteFile(contextPath, []byte(runtimeagent.BuildAgentContext(agent)), 0o644); err != nil {
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

// applyLegacyCLIProviderToBootOpts threads a resolved CLI alias
// ("pty" / "pty-claude" / "sub-claude" / etc.) into bootOpts.Provider so
// agent.Boot's effectiveProvider has a non-empty adapter name to dispatch
// on. CW-20260515-0005: without this, legacy CLI sessions (the bare
// "Claude CLI" dropdown row, no bootprofile) reached agent.Boot with
// bootOpts.Provider="" AND the file-default agent profile's
// DefaultProvider="" → bootdirLayoutFor("") returned unsupportedLayout →
// boot crashed with `bootdir for provider ""` (c203 reproducer; identical
// surface to c195/c197).
//
// Takes the ALREADY-RESOLVED provider name from resolveProvider's return
// value (chat.go), not the raw session.Provider column. CW-20260812-0001
// investigation: the two can diverge whenever resolution falls through
// past an empty session.Provider to a CLI-shaped candidate elsewhere in
// the chain (a stale user_settings.ProviderFallbackChain entry, for
// instance) — using the raw column here reproduced the exact
// c195/c197/c203 crash class for that case, because the caller had
// already decided "boot CLI" based on the resolved name while this
// helper independently re-derived from a column that was still empty.
//
// chat.IsCLIProvider gates the assignment so HTTP-shaped provider names
// pass through untouched — a resolved HTTP provider should never have
// reached driveBootSession in the first place (the chat-resolve layer
// routes those through the llmcontracts.Provider path).
//
// chat.NormalizeCLIProvider applies the canonical alias table:
// "pty" → "claude", "pty-claude" → "claude", "sub-codex" → "codex",
// etc. The runtime layer duplicates these rules locally as
// normalizeProviderName (for the package-cycle reason documented
// there); this caller routes through chat as the canonical source.
//
// Defensive on nil bootOpts: no-op.
func applyLegacyCLIProviderToBootOpts(bootOpts *runtimeagent.Options, providerName string) {
	if bootOpts == nil {
		return
	}
	if !chat.IsCLIProvider(providerName) {
		return
	}
	bootOpts.Provider = chat.NormalizeCLIProvider(providerName)
}

// bootSessionRole derives a role identifier from the agent profile for
// system-prompt assembly. Returns the empty string when the profile
// advertises no role-specific slug; the agent package's composeSystemPrompt
// handles empty roles via its null-role default.
//
// Phase 0 item 21 ("Cut Modes, in full") removed this function's `mode
// *store.AgentMode` parameter and the mode-slug-wins-over-agent-slug
// preference it implemented — Legacy Agent Mode is gone.
func bootSessionRole(agent *store.AgentProfile) string {
	if agent != nil && agent.Slug != "" {
		return agent.Slug
	}
	return ""
}
