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
//
// Routes through wr.SendInput (go-agent-wrapper's Wrapper.SendInput)
// instead of Dependencies.SessionsManager directly — see agent.go's Boot
// for why Boot no longer registers this session with
// Dependencies.SessionsManager at all.
func (s *Session) SendInput(payload []byte) error {
	if s == nil {
		return errors.New("agent.Session.SendInput: session not initialized")
	}
	// ACP backend (TASKS/agent-host-acp/11): acp.Client.Prompt drives its
	// own ACP-native framing internally -- none of the streaming-stdio
	// NDJSON framing below applies (that's specific to claude's native
	// protocol, an entirely different wire shape from ACP).
	if s.acp != nil {
		return s.acp.SendInput(context.Background(), payload)
	}
	if s.wr == nil {
		return errors.New("agent.Session.SendInput: session not initialized")
	}
	if shouldUseStreamingStdio(s.Provider, s.Mode) {
		framed, err := streamingStdioUserFrame(string(payload))
		if err != nil {
			return fmt.Errorf("agent.Session.SendInput: frame streaming-stdio payload: %w", err)
		}
		payload = framed
	}
	return s.wr.SendInput(context.Background(), payload)
}

// Stop terminates the runtime cooperatively: wr.Stop emits the
// interrupt.requested/acknowledged event pair and calls the underlying
// agentkit session's Stop (stdin close, grace period, then
// SIGTERM/SIGKILL escalation — see go-agent-wrapper's
// adapters.InterruptCapability for the honest per-adapter ceiling on this
// today, carried forward unchanged by this migration). Deliberately does
// NOT touch Session.runCancel — see agent.go's Boot for why canceling
// wr.Run's own ctx from here would race an otherwise-clean stop into an
// avoidable context.Canceled error; wr.Stop's own ctx parameter (bounded
// by the caller) is the correct, sufficient interrupt mechanism on its
// own. The ephemeral boot dir is removed on success, matching
// pre-migration behavior.
func (s *Session) Stop(ctx context.Context) error {
	if s == nil {
		return errors.New("agent.Session.Stop: session not initialized")
	}
	if s.wr == nil && s.acp == nil {
		return errors.New("agent.Session.Stop: session not initialized")
	}

	var err error
	if s.acp != nil {
		// ACP backend (TASKS/agent-host-acp/11): acpSession.Stop
		// Cancel-then-Closes the acp.Client directly -- see its own doc
		// comment for why this mirrors wr.Stop's interrupt-then-terminate
		// shape without touching wr at all.
		err = s.acp.Stop(ctx)
	} else {
		err = s.wr.Stop(ctx)
	}

	// Path-grant lineage clears even if Stop fails — Boot registered it
	// during launch, so a failed Stop must not leak the lineage entry.
	if s.hadLineage && s.deps != nil && s.deps.PathGrants != nil {
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
// error wr.Run's background goroutine (started in Boot) observed —
// wrapper.Wrapper.Run's own Session.Wait call underneath still surfaces a
// *agentsessions.ExitError on an abnormal exit (wrapped, not replaced, by
// Run's "wrapper: session exited with error: %w"), so
// errors.As(err, &exitErr) at every existing internal/recovery/broker call
// site continues to unwrap correctly with zero broker-side changes.
//
// Safe to call from any number of goroutines concurrently — see runDone's
// doc comment on Session for the happens-before argument.
func (s *Session) Wait(ctx context.Context) error {
	if s == nil || s.runDone == nil {
		return errors.New("agent.Session.Wait: session not initialized")
	}
	select {
	case <-s.runDone:
		return s.runErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Checkpoint requests a session-state snapshot. The agentkit/go-agent-
// wrapper surface does not expose CheckpointHints directly today;
// ModeResume relies on the persisted RuntimeStore checkpoint payload
// populated via OnSessionID. This method is reserved for explicit
// checkpoint requests once the lib surfaces one; today it returns the
// empty id without error so callers can no-op — unchanged by this
// migration.
func (s *Session) Checkpoint(ctx context.Context) (string, error) {
	if s == nil || (s.wr == nil && s.acp == nil) {
		return "", errors.New("agent.Session.Checkpoint: session not initialized")
	}
	_ = ctx
	return "", nil
}
