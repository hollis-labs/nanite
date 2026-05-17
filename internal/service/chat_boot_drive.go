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

	"github.com/hollis-labs/go-agent-launch/agentlaunch"
	agentsessions "github.com/hollis-labs/go-agent-sessions/agentsessions"
	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/nanite/internal/bootprofile"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/launchplan"
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
		bootOpts := runtimeagent.Options{
			Mode:         runtimeagent.ModeLongLived,
			SessionID:    sessionID,
			AgentProfile: profileSlug,
			Workdir:      workdir,
			Role:         role,
		}
		applyLegacyCLIProviderToBootOpts(&bootOpts, session)
		// CW-20260514-0048 / S5 Phase F: when this session is backed by a
		// compiled boot-profile LaunchSpec (provider was a
		// "bootprofile:<id>" id that chat_generate.go decoded + stashed),
		// resolve the launch through the SHARED plan-assembly seam and
		// project the validated agentlaunch.LaunchPlan onto bootOpts —
		// the SAME launchplan.Build the standalone launcher uses. The
		// runtime binding resolves registry-primary with an explicit,
		// observable file/spec fallback (D1 + §4.1). See
		// applyLaunchSpecAsPlanToBootOpts for the projection + fallback.
		if launchSpec := s.launchSpecFor(sessionID); launchSpec != nil {
			s.applyLaunchSpecAsPlanToBootOpts(&bootOpts, launchSpec)
		}
		booted, err := runtimeagent.Boot(ctx, s.agentDeps, bootOpts)
		if err != nil {
			return nil, fmt.Errorf("driveBootSession: boot: %w", err)
		}
		s.activeSessions.Store(sessionID, booted)
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
		bootedWorkdir := workdir
		go s.observeSessionForRecovery(bootedSession, sessionID, bootedProfile, bootedWorkdir, bootedAt)
	} else if s.slotsChangedFor(sessionID, slotResult) {
		// 3. Refresh the boot dir when System / Agent / Mode / Rules
		// slots have shifted. UserContext changes per turn by design and
		// flows via SendInput; we don't regen CLAUDE.md for it.
		if err := s.regenerateBootDirSlots(sessionID, sess.BootDir, agent, mode); err != nil {
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
		s.activeSessions.CompareAndDelete(sessionID, sess)
		s.activeSessionSlots.Delete(sessionID)
		s.toolPartitionStates.Delete(sessionID)
		if broker, ok := s.agentDeps.Recovery.(*recovery.Broker); ok {
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
func (s *chatServiceImpl) regenerateBootDirSlots(sessionID, bootDir string, agent *store.AgentProfile, mode *store.AgentMode) error {
	if bootDir == "" {
		return errors.New("regenerateBootDirSlots: empty bootDir")
	}
	if agent == nil {
		return errors.New("regenerateBootDirSlots: nil agent profile")
	}
	claudePath := filepath.Join(bootDir, "CLAUDE.md")
	// CW-20260516-0007 round 1: recompute the SAME resolved boot prompt
	// the initial Boot planted, so a mid-session slot refresh doesn't
	// silently thin a bootprofile session's operating instructions.
	// resolveBootPrompt's inputs: role (from agent/mode), the runtime
	// Mode (ModeLongLived for every chat session — the only caller),
	// and a bootprofile LaunchSpec's BootPrompt as the override. The
	// LaunchSpec is the per-session stash keyed by sessionID; nil for
	// non-bootprofile sessions, where ResolveSystemPrompt falls back to
	// the role/mode-composed prompt.
	role := bootSessionRole(agent, mode)
	bootPromptOverride := ""
	if ls := s.launchSpecFor(sessionID); ls != nil {
		bootPromptOverride = ls.BootPrompt
	}
	systemPrompt := runtimeagent.ResolveSystemPrompt(role, agent, runtimeagent.ModeLongLived, bootPromptOverride)
	if err := fsutil.AtomicWriteFile(claudePath, []byte(runtimeagent.BuildCLAUDEMD(agent.Name, agent.Description, systemPrompt)), 0o644); err != nil {
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

// applyLegacyCLIProviderToBootOpts threads a legacy CLI alias from
// session.Provider ("pty" / "pty-claude" / "sub-claude" / etc.) into
// bootOpts.Provider so agent.Boot's effectiveProvider has a non-empty
// adapter name to dispatch on. CW-20260515-0005: without this, legacy
// CLI sessions (the bare "Claude CLI" dropdown row, no bootprofile)
// reached agent.Boot with bootOpts.Provider="" AND the file-default
// agent profile's DefaultProvider="" → bootdirLayoutFor("") returned
// unsupportedLayout → boot crashed with `bootdir for provider ""`
// (c203 reproducer; identical surface to c195/c197).
//
// chat.IsCLIProvider gates the assignment so bootprofile-shaped
// session providers ("bootprofile:<id>") and HTTP shapes pass
// through untouched — applyLaunchSpecToBootOpts stays the source of
// truth for bootprofile sessions, and a session with a real HTTP
// provider should never have reached driveBootSession in the first
// place (the chat-resolve layer routes those through the
// llmcontracts.Provider path).
//
// chat.NormalizeCLIProvider applies the canonical alias table:
// "pty" → "claude", "pty-claude" → "claude", "sub-codex" → "codex",
// etc. The runtime layer duplicates these rules locally as
// normalizeProviderName (for the package-cycle reason documented
// there); this caller routes through chat as the canonical source.
//
// Defensive on nil session and nil bootOpts: both no-op. The caller
// (driveBootSession) passes session through without a pre-validating
// nil guard — matching the surrounding pattern of bootSessionWorkdir
// and applyLaunchSpecToBootOpts, where each helper owns its own nil
// handling. Keeping the guards local also makes the helper safe to
// test in isolation without setup boilerplate.
func applyLegacyCLIProviderToBootOpts(bootOpts *runtimeagent.Options, session *store.Session) {
	if bootOpts == nil || session == nil {
		return
	}
	if !chat.IsCLIProvider(session.Provider) {
		return
	}
	bootOpts.Provider = chat.NormalizeCLIProvider(session.Provider)
}

// applyLaunchSpecToBootOpts overlays a compiled bootprofile.LaunchSpec
// onto the agent.Options that driveBootSession passes to runtimeagent.Boot.
// CW-20260514-0048: factored out of the inline boot setup so the merge
// semantics are unit-testable without booting a real runtime.
//
// Precedence rules (pinned in tests under chat_boot_drive_test.go):
//
//	Workdir          — caller-supplied bootOpts.Workdir wins (chat
//	                    layer may have a session-scoped override);
//	                    falls through to spec.Workdir when empty.
//	                    Today bootSessionWorkdir returns "" so the
//	                    spec value lands; if a future ticket puts a
//	                    real workdir on store.Session, that wins.
//	Env              — spec values overlay bootOpts.Env (spec wins on
//	                    key collision; pre-existing caller-supplied
//	                    keys persist for keys the spec doesn't touch).
//	                    Inside agent.Boot, composeEnv then layers:
//	                    host < profile < Options.Env, so spec values
//	                    beat the inherited host env.
//	ExtraArgs        — spec.Args are APPENDED to bootOpts.ExtraArgs
//	                    so any caller-side argv (none today) is
//	                    preserved. The runtime splices ExtraArgs
//	                    after adapter.BuildArgs.
//	BootPrompt       — spec.BootPrompt overrides the role-derived
//	                    composeSystemPrompt output via
//	                    Options.BootPromptOverride. Empty
//	                    spec.BootPrompt leaves the legacy
//	                    behavior intact (composeSystemPrompt fires).
//	Provider         — caller-supplied bootOpts.Provider wins (no
//	                    caller sets it today, but the precedence
//	                    mirrors Workdir so a future explicit
//	                    override remains the most specific signal);
//	                    spec.Provider (the bare adapter name, e.g.
//	                    "claude") fills the empty case. The override
//	                    propagates through agent.Boot via
//	                    effectiveProvider, replacing the agent
//	                    profile's DefaultProvider. CW-20260514-0053
//	                    fix: the file-default agent profile has
//	                    DefaultProvider="" so without this override
//	                    agent.Boot would dispatch bootdirLayoutFor("")
//	                    and crash with the c197 "bootdir for provider
//	                    \"\" is not yet implemented" error.
//
// CW-20260514-0048 scope: ModeResume / ResumeFromCheckpoint are
// explicitly NOT touched here — normal boot-profile launches start
// fresh. Crash recovery's resume path (CW-20260514-0049) constructs
// its own Options and bypasses this helper.
func applyLaunchSpecToBootOpts(bootOpts *runtimeagent.Options, spec *bootprofile.LaunchSpec) {
	if bootOpts == nil || spec == nil {
		return
	}
	if spec.Workdir != "" && bootOpts.Workdir == "" {
		bootOpts.Workdir = spec.Workdir
	}
	if len(spec.Env) > 0 {
		if bootOpts.Env == nil {
			bootOpts.Env = make(map[string]string, len(spec.Env))
		}
		for k, v := range spec.Env {
			bootOpts.Env[k] = v
		}
	}
	if len(spec.Args) > 0 {
		bootOpts.ExtraArgs = append(bootOpts.ExtraArgs, spec.Args...)
	}
	if spec.BootPrompt != "" {
		bootOpts.BootPromptOverride = spec.BootPrompt
	}
	if spec.Provider != "" && bootOpts.Provider == "" {
		bootOpts.Provider = spec.Provider
	}
}

// applyLaunchSpecAsPlanToBootOpts is the S5 Phase F chat-side launch
// seam. It routes a compiled bootprofile.LaunchSpec through the SHARED
// plan-assembly path (launchplan.Build → agentlaunch.PlanFromLaunch)
// instead of overlaying the spec directly — so a GUI chat boot-profile
// session and a standalone `nanite launch` resolve their runtime binding
// and assemble their plan identically.
//
// # Why route through a LaunchPlan at all
//
// Phase C flipped the standalone launcher to registry-primary launch
// resolution; the chat path never assembled a LaunchPlan. Phase F
// converges them: launchplan.Build resolves the runner→RuntimeBinding
// registry-primary through the shared agentregistry.Registry, with an
// explicit, observable fallback to the spec/profile default (D1 + §4.1),
// and runs agentlaunch.PlanFromLaunch — which Validate()s the plan.
//
// # No behavior change for the user (locked decision §4.1)
//
// The composer's provider/model selection stays authoritative. It is
// spec.Provider — the `runner` id. Registry resolution RESOLVES that
// runner; it never substitutes a different provider. When the registry
// has a binding for the runner it carries the same provider; when it has
// none or is down, launchplan.Build's fallback is RuntimeBindingForSpec
// (carrying exactly spec.Provider). Either way the projected
// bootOpts.Provider equals the composer/profile selection. The plan path
// therefore produces a byte-identical boot to the pre-Phase-F
// applyLaunchSpecToBootOpts overlay — the user's UI pick always wins.
//
// # Degradation (D1 — chat must work offline / registry-down)
//
// launchplan.Build never hard-fails on a down/empty registry — the
// DegradingRegistrar + cache-miss handling inside ResolveRuntimeBinding
// degrades to the file/spec fallback. The ONLY way Build returns an
// error here is a genuine fault (ambiguous registry match, malformed
// source file) or a structurally invalid spec. On any error we log it
// and fall back to the proven spec-direct overlay
// (applyLaunchSpecToBootOpts) so the chat session still boots — the
// registry is a side service, never a launch-path dependency.
//
// nil-safe on bootOpts / spec: both no-op (matches the surrounding
// helper pattern).
func (s *chatServiceImpl) applyLaunchSpecAsPlanToBootOpts(bootOpts *runtimeagent.Options, spec *bootprofile.LaunchSpec) {
	if bootOpts == nil || spec == nil {
		return
	}

	plan, err := launchplan.Build(spec, s.agentRegistry, slog.Default())
	if err != nil {
		// A genuine registry fault or a structurally invalid spec. D1:
		// never block the launch — degrade to the spec-direct overlay,
		// observably. The user still gets their composer-selected
		// provider/model/boot prompt.
		slog.Warn("driveBootSession: launch-plan assembly failed; degrading to spec-direct overlay",
			"profile_id", spec.ProfileID, "provider", spec.Provider, "err", err)
		applyLaunchSpecToBootOpts(bootOpts, spec)
		return
	}

	applyLaunchPlanToBootOpts(bootOpts, plan, spec)
}

// applyLaunchPlanToBootOpts projects a validated agentlaunch.LaunchPlan
// onto the runtimeagent.Options driveBootSession hands to agent.Boot.
//
// The plan is the registry-resolved, PlanFromLaunch-assembled,
// Validate()-clean projection of the compiled spec. spec is still passed
// because three boot-relevant fields are NOT carried as load-bearing
// data on the LaunchPlan and stay spec-sourced — they were never
// registry-resolvable, and projecting them from the spec keeps the chat
// boot byte-identical to the pre-Phase-F applyLaunchSpecToBootOpts
// overlay (the no-behavior-change guarantee):
//
//	Env       — go-agent-launch's LaunchPlan does not carry a process
//	            env map on the base plan (it lands on ProviderSpec.Env /
//	            InjectionSpec.Env post-Prepare); the compiled
//	            bootprofile.LaunchSpec.Env stays the source of truth.
//	BootPrompt— PlanFromLaunch maps the rendered boot body onto
//	            BootProfile.Inline.BootContent. Nanite's runtime expects
//	            the durable system/persona prompt on
//	            Options.BootPromptOverride; spec.BootPrompt IS that
//	            rendered body (it is what launchplan.Build fed into
//	            RenderResult.Body), so spec.BootPrompt is used verbatim —
//	            identical bytes to plan.BootProfile.Inline.BootContent.
//	ExtraArgs — spec.Args is the profile-authored argv. PlanFromLaunch
//	            does map a RuntimeBinding's Args onto Provider.Flags, but
//	            a registry-hit binding (which does NOT carry the
//	            profile's argv) would otherwise DROP spec.Args — a
//	            behavior change. spec.Args is therefore appended directly,
//	            exactly as the legacy overlay did. A future ticket that
//	            wants registry-contributed argv extends this deliberately.
//
// The PLAN is authoritative for the registry-resolved fields:
//
//	Provider  — plan.Provider.ID is the runtime binding's Provider, the
//	            registry-primary resolution of the composer/profile
//	            runner. Caller-supplied bootOpts.Provider still wins when
//	            set (precedence mirrors applyLaunchSpecToBootOpts; no chat
//	            caller sets it today).
//	Workdir   — plan.Workspace.Workdir is PlanFromLaunch's resolved work
//	            dir (sourced from spec.Workdir). Caller-supplied
//	            bootOpts.Workdir wins when set.
func applyLaunchPlanToBootOpts(bootOpts *runtimeagent.Options, plan agentlaunch.LaunchPlan, spec *bootprofile.LaunchSpec) {
	if bootOpts == nil {
		return
	}

	if plan.Workspace.Workdir != "" && bootOpts.Workdir == "" {
		bootOpts.Workdir = plan.Workspace.Workdir
	}
	if plan.Provider.ID != "" && bootOpts.Provider == "" {
		bootOpts.Provider = plan.Provider.ID
	}

	// Env + Args + BootPrompt are not registry-resolvable and stay
	// spec-sourced — keeps the chat boot byte-identical to the legacy
	// applyLaunchSpecToBootOpts overlay.
	if spec != nil {
		if len(spec.Env) > 0 {
			if bootOpts.Env == nil {
				bootOpts.Env = make(map[string]string, len(spec.Env))
			}
			for k, v := range spec.Env {
				bootOpts.Env[k] = v
			}
		}
		if len(spec.Args) > 0 {
			bootOpts.ExtraArgs = append(bootOpts.ExtraArgs, spec.Args...)
		}
		if spec.BootPrompt != "" {
			bootOpts.BootPromptOverride = spec.BootPrompt
		}
	}
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
