package agent

import (
	"context"
	"errors"
)

// SendInput delivers a user message into the live runtime. ModeLongLived
// chat sessions call this per turn; ModeOneShot / ModeBackground typically
// call once during Boot via AutoFireFirstTurn.
//
// Body filled in Phase 3 once the SessionsManager wiring is concrete.
func (s *Session) SendInput(payload []byte) error {
	if s == nil || s.deps == nil {
		return errors.New("agent.Session.SendInput: session not initialized")
	}
	return errors.New("agent.Session.SendInput: not yet implemented (skeleton — phase 2)")
}

// Stop terminates the runtime cooperatively (SIGTERM with 5s grace, then
// SIGKILL). On supervised PTY sessions, Stop is non-restart per the v0.6.0
// supervision contract.
func (s *Session) Stop(ctx context.Context) error {
	if s == nil || s.deps == nil {
		return errors.New("agent.Session.Stop: session not initialized")
	}
	return errors.New("agent.Session.Stop: not yet implemented (skeleton — phase 2)")
}

// Wait blocks until the runtime exits or ctx is canceled. Returns the
// underlying ExitError on abnormal termination so callers can branch on
// supervision causes (idle_timeout, restart_exhausted, oom_kill, etc.).
func (s *Session) Wait(ctx context.Context) error {
	if s == nil || s.deps == nil {
		return errors.New("agent.Session.Wait: session not initialized")
	}
	return errors.New("agent.Session.Wait: not yet implemented (skeleton — phase 2)")
}

// Checkpoint requests a session-state snapshot. Persisted to the store
// for ModeResume callers.
func (s *Session) Checkpoint(ctx context.Context) (string, error) {
	if s == nil || s.deps == nil {
		return "", errors.New("agent.Session.Checkpoint: session not initialized")
	}
	return "", errors.New("agent.Session.Checkpoint: not yet implemented (skeleton — phase 2)")
}
