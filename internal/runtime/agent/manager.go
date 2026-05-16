package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
)

// SendInput delivers a user message into the live runtime. ModeLongLived
// chat sessions call this per turn; ModeOneShot / ModeSubagent /
// ModeBackground typically deliver via AutoFireFirstTurn during Boot.
//
// CW-20260516-0007: for streaming-stdio sessions (claude long-lived and
// every other Claude mode) the payload is NDJSON-framed as a claude
// "Streaming Input Mode" user message before it reaches the runtime.
// The runtime writes SendInput bytes to the child's stdin verbatim
// (+'\n'); claude parses each line as JSON, so raw text would crash
// its input parser (c207/c208). Non-streaming runtimes (PTY, codex/
// opencode subprocess-per-turn) receive the payload unchanged.
func (s *Session) SendInput(payload []byte) error {
	if s == nil || s.deps == nil || s.deps.SessionsManager == nil {
		return errors.New("agent.Session.SendInput: session not initialized")
	}
	if shouldUseStreamingStdio(s.Provider, s.Mode) {
		framed, err := streamingStdioUserFrame(string(payload))
		if err != nil {
			return fmt.Errorf("agent.Session.SendInput: frame streaming-stdio payload: %w", err)
		}
		payload = framed
	}
	return s.deps.SessionsManager.SendInput(s.ID, payload)
}

// Stop terminates the runtime cooperatively (SIGTERM with grace, then
// SIGKILL via the lib's supervisor). On supervised PTY sessions, Stop is
// non-restart per the v0.6.0 supervision contract. The ephemeral boot dir
// is removed on success.
func (s *Session) Stop(ctx context.Context) error {
	if s == nil || s.deps == nil || s.deps.SessionsManager == nil {
		return errors.New("agent.Session.Stop: session not initialized")
	}
	err := s.deps.SessionsManager.Stop(ctx, s.ID)

	// Path-grant lineage clears even if Stop fails — Boot registered it
	// during launch, so a failed Stop must not leak the lineage entry.
	if s.hadLineage && s.deps.PathGrants != nil {
		s.deps.PathGrants.ClearLineage(s.ID)
		s.hadLineage = false
	}

	if s.BootDir != "" {
		_ = os.RemoveAll(s.BootDir)
	}

	if err != nil {
		return fmt.Errorf("agent.Session.Stop: %w", err)
	}
	return nil
}

// Wait blocks until the runtime exits or ctx is canceled. Returns the
// underlying exit code via the manager's WaitSession.
func (s *Session) Wait(ctx context.Context) error {
	if s == nil || s.deps == nil || s.deps.SessionsManager == nil {
		return errors.New("agent.Session.Wait: session not initialized")
	}
	if _, err := s.deps.SessionsManager.WaitSession(ctx, s.ID); err != nil {
		return err
	}
	return nil
}

// Checkpoint requests a session-state snapshot. The agentsessions Manager
// surface does not expose CheckpointHints directly today; ModeResume relies
// on the persisted RuntimeStore checkpoint payload populated via
// OnSessionID. This method is reserved for explicit checkpoint requests
// once the lib surfaces a Manager-level entry; today it returns the empty
// id without error so callers can no-op.
func (s *Session) Checkpoint(ctx context.Context) (string, error) {
	if s == nil || s.deps == nil || s.deps.SessionsManager == nil {
		return "", errors.New("agent.Session.Checkpoint: session not initialized")
	}
	_ = ctx
	return "", nil
}
