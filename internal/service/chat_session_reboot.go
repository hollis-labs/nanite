package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	runtimeagent "github.com/hollis-labs/nanite/internal/runtime/agent"
)

// stopRebootGrace bounds the cooperative Stop of the outgoing runtime
// process during a reboot. Stop already does SIGTERM-with-grace then
// SIGKILL via the lib supervisor; this is the outer wall-clock cap so a
// wedged process cannot hang the reboot request indefinitely.
const stopRebootGrace = 15 * time.Second

// ErrSessionBusy is returned whenever an operation refuses to run while a
// generateResponse turn is already in flight for the session, rather than
// taking over or interrupting it:
//   - RebootSessionAgent — rebooting under an in-flight turn would yank the
//     runtime out from under an active streamLoop; the caller should retry
//     once the turn settles, or cancel it first via the chat cancel endpoint.
//   - TriggerHarnessTurn (CW-20260520-0001) — a harness-initiated turn must
//     never silently cancel a real user turn; on ErrSessionBusy the caller
//     just skips, relying on the turn-start injection (CW-20260512-0019) to
//     deliver whatever it was reacting to once a turn does run.
var ErrSessionBusy = errors.New("session has an in-flight turn")

// RebootResult reports the outcome of a RebootSessionAgent call.
type RebootResult struct {
	// Rebooted is true when a live runtime agent was found and stopped.
	// false means there was no live agent to stop — the next turn cold-boots
	// a fresh agent regardless, so the caller's intent is already satisfied.
	Rebooted bool `json:"rebooted"`
	// Status is a stable machine-readable token: "rebooted" or
	// "no_active_agent".
	Status string `json:"status"`
}

// RebootSessionAgent tears down one chat session's live runtime agent so
// the next user turn cold-boots a fresh agent. This is the targeted,
// per-session counterpart to restarting nanite-api-service (which wipes
// every session's runtime at once and only lazily on each session's next
// turn). Use it to pick up a freshly deployed binary, a changed boot dir,
// or to recover a single wedged agent without disturbing other sessions.
//
// Design decisions (CW-20260516-0057):
//
//   - Fresh Boot, not ModeResume. The next turn hits driveBootSession's
//     cold-boot branch (ModeLongLived) — a brand-new agent process and a
//     brand-new boot dir from the current binary. ModeResume is the crash-
//     recovery path that preserves provider-side history; a deliberate
//     reboot to pick up new config wants a clean slate. The user-visible DB
//     transcript is untouched; only the agent process is replaced.
//
//   - The boot dir is re-planted unconditionally. Session.Stop removes the
//     old ephemeral boot dir; the next Boot plants a fresh one. This is
//     inherent to the fresh-Boot choice, not a separate flag.
//
//   - In-flight turn: reject. When a generateResponse is registered for the
//     session, return ErrSessionBusy rather than interrupting or queueing.
//     Rejecting keeps the semantics simple and predictable; the operator
//     retries (or cancels the turn first).
//
//   - Lazy re-boot. This call only stops + evicts the current runtime; it
//     does not eagerly Boot a replacement. The next user turn boots fresh
//     via the existing driveBootSession path — the same proven flow as a
//     post-service-restart cold boot, just scoped to one session. Eager
//     re-boot would need per-turn context (slots, agent, mode) the caller
//     does not have here.
//
// The teardown is made safe against the recovery broker by flagging the
// session in rebootingSessions before Stop: observeSessionForRecovery sees
// the flag and treats the resulting process exit as deliberate rather than
// dispatching a crash replacement.
func (s *chatServiceImpl) RebootSessionAgent(ctx context.Context, sessionID string) (RebootResult, error) {
	return s.rebootRuntime(ctx, sessionID, true)
}

// RecoverSession evicts the session's live runtime WITHOUT arming the
// fresh-boot flag, so the next turn cold-boots into auto-recovery (recovery
// pack + provider resume). CW-20260525-0001 Slice 2 — explicit user-triggered
// recovery, distinct from a clean Reboot.
func (s *chatServiceImpl) RecoverSession(ctx context.Context, sessionID string) (RebootResult, error) {
	return s.rebootRuntime(ctx, sessionID, false)
}

// rebootRuntime stops + evicts the session's live runtime so the next turn
// cold-boots. When fresh is true the next boot skips auto-recovery (a clean
// Reboot); when false the next boot recovers (Recover).
func (s *chatServiceImpl) rebootRuntime(ctx context.Context, sessionID string, fresh bool) (RebootResult, error) {
	if sessionID == "" {
		return RebootResult{}, errors.New("rebootRuntime: empty session id")
	}

	// Reject under an in-flight turn — see the in-flight decision above.
	if s.hasActiveGeneration(sessionID) {
		return RebootResult{}, ErrSessionBusy
	}

	v, ok := s.activeSessions.Load(sessionID)
	if !ok {
		// No live runtime agent. The next turn cold-boots fresh anyway, so
		// the reboot intent is already satisfied — report it as a no-op.
		return RebootResult{Rebooted: false, Status: "no_active_agent"}, nil
	}
	sess, _ := v.(*runtimeagent.Session)
	if sess == nil {
		// Defensive: a non-Session value should never be stored here.
		s.activeSessions.CompareAndDelete(sessionID, v)
		return RebootResult{Rebooted: false, Status: "no_active_agent"}, nil
	}

	// Flag the session BEFORE Stop so the Wait-observer
	// (observeSessionForRecovery) — which may wake the instant Stop kills
	// the process, while we are still inside Stop — sees the flag and skips
	// the recovery broker. A SIGTERM/SIGKILL exit can otherwise present as
	// an *agentsessions.ExitError and be misread as a crash.
	s.rebootingSessions.Store(sessionID, struct{}{})

	stopCtx, cancel := context.WithTimeout(ctx, stopRebootGrace)
	defer cancel()
	if err := sess.Stop(stopCtx); err != nil {
		// Stop failed — the process may still be alive. Clear the reboot
		// flag so a later genuine crash of this session is still
		// recoverable, and leave activeSessions intact (a half-stopped
		// session is better left tracked than orphaned).
		s.rebootingSessions.Delete(sessionID)
		return RebootResult{}, fmt.Errorf("RebootSessionAgent: stop runtime: %w", err)
	}

	// Evict per-session runtime state so the next turn hits the cold-boot
	// branch in driveBootSession. CompareAndDelete on activeSessions guards
	// against clobbering a replacement a concurrent turn may have already
	// stored. The observer performs the same eviction idempotently when it
	// wakes; doing it here too makes the reboot synchronous for the caller.
	s.activeSessions.CompareAndDelete(sessionID, sess)
	s.activeSessionSlots.Delete(sessionID)
	s.toolPartitionStates.Delete(sessionID)

	// CW-20260525-0001: a clean reboot must stay fresh — arm the one-shot flag
	// so the next cold-boot skips auto-recovery (pack + provider resume).
	// Recover (fresh=false) leaves it unset so the next turn recovers; a daemon
	// restart never sets it either, so it also recovers.
	if fresh {
		s.freshBootSessions.Store(sessionID, struct{}{})
	}

	slog.Info("reboot: session agent stopped; next turn will cold-boot",
		"session_id", sessionID, "fresh", fresh)
	return RebootResult{Rebooted: true, Status: "rebooted"}, nil
}

// hasActiveGeneration reports whether a generateResponse goroutine is
// currently registered for sessionID. Used by RebootSessionAgent to reject
// a reboot while a turn is streaming.
func (s *chatServiceImpl) hasActiveGeneration(sessionID string) bool {
	s.activeGenMu.Lock()
	defer s.activeGenMu.Unlock()
	cur, ok := s.activeGen[sessionID]
	return ok && cur != nil
}
