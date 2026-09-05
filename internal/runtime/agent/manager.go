package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	agentsessions "github.com/hollis-labs/agentkit/agentsessions"
	"github.com/hollis-labs/go-agent-wrapper/acp"
	"github.com/hollis-labs/go-agent-wrapper/wrapper"
)

// ErrTurnCancelUnsupported is the wrapper-owned native-runtime ceiling. It is
// re-exported at Nanite's runtime boundary so service code can treat the
// honest unsupported result as expected without importing wrapper directly.
var ErrTurnCancelUnsupported = wrapper.ErrTurnCancelUnsupported

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
// Routes through go-agent-wrapper for both native and ACP protocols.
func (s *Session) SendInput(payload []byte) error {
	if s == nil {
		return errors.New("agent.Session.SendInput: session not initialized")
	}
	if s.wr == nil {
		return errors.New("agent.Session.SendInput: session not initialized")
	}
	if !s.isACP && shouldUseStreamingStdio(s.Provider, s.Mode) {
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
	if s.wr == nil {
		return errors.New("agent.Session.Stop: session not initialized")
	}

	err := s.wr.Stop(ctx)

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
// error wr.Run's background goroutine (started in Boot) observed. Native
// wrappers retain agentkit's *agentsessions.ExitError. ACP wrappers return
// the richer *acp.LifecycleError; Boot joins a compatibility ExitError at
// this Nanite-owned recovery/UI translation boundary so the existing broker
// can still classify abnormal exits without taking lifecycle ownership back
// from Wrapper.
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

// recoveryCompatibleWrapperError preserves wrapper's normalized ACP error
// while adding the legacy shape Nanite's recovery broker consumes. Code -1
// retains the pre-v0.9 ACP contract: the protocol exposes a classified
// lifecycle outcome but not a portable child exit code or signal.
func recoveryCompatibleWrapperError(err error, isACP bool) error {
	if err == nil || !isACP {
		return err
	}
	var lifecycleErr *acp.LifecycleError
	if !errors.As(err, &lifecycleErr) {
		return err
	}
	var exitErr *agentsessions.ExitError
	if errors.As(err, &exitErr) {
		return err
	}
	return errors.Join(err, &agentsessions.ExitError{Code: -1})
}

// Checkpoint requests a session-state snapshot. The agentkit/go-agent-
// wrapper surface does not expose CheckpointHints directly today;
// ModeResume relies on the persisted RuntimeStore checkpoint payload
// populated via OnSessionID. This method is reserved for explicit
// checkpoint requests once the lib surfaces one; today it returns the
// empty id without error so callers can no-op — unchanged by this
// migration.
func (s *Session) Checkpoint(ctx context.Context) (string, error) {
	if s == nil || s.wr == nil {
		return "", errors.New("agent.Session.Checkpoint: session not initialized")
	}
	_ = ctx
	return "", nil
}

// CancelTurn requests turn-scoped cancellation through Wrapper. ACP adapters
// support it; native adapters return wrapper.ErrTurnCancelUnsupported.
func (s *Session) CancelTurn(ctx context.Context) error {
	if s == nil || s.wr == nil {
		return errors.New("agent.Session.CancelTurn: session not initialized")
	}
	return s.wr.CancelTurn(ctx)
}

// WaitTurnTerminal waits until an ACP turn is no longer processing. Native
// wrappers cannot expose that boundary today and return
// ErrTurnCancelUnsupported; callers that require takeover safety must stop
// and cold-boot that exact native session instead.
func (s *Session) WaitTurnTerminal(ctx context.Context) error {
	if s == nil || s.wr == nil {
		return errors.New("agent.Session.WaitTurnTerminal: session not initialized")
	}
	if !s.isACP {
		return ErrTurnCancelUnsupported
	}
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		snapshot, ok := s.wr.ACPSnapshot()
		if !ok || snapshot.State != acp.StateProcessing {
			return nil
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// WaitTurnCancelable waits until an admitted SendInput has either reached
// ACP Processing (where CancelTurn is meaningful) or returned without ever
// becoming cancelable. Closing sendReturned is owned by the exact SendInput
// invocation. Native adapters have no Processing snapshot; callers stop the
// exact wrapper and still wait sendReturned before declaring takeover safe.
func (s *Session) WaitTurnCancelable(ctx context.Context, sendReturned <-chan struct{}) (bool, error) {
	if s == nil || s.wr == nil {
		return false, errors.New("agent.Session.WaitTurnCancelable: session not initialized")
	}
	if !s.isACP {
		return true, nil
	}
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		snapshot, ok := s.wr.ACPSnapshot()
		if ok && snapshot.State == acp.StateProcessing {
			return true, nil
		}
		select {
		case <-sendReturned:
			return false, nil
		case <-ticker.C:
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
}

// ProviderSessionID returns wrapper's current provider-assigned identity.
func (s *Session) ProviderSessionID() string {
	if s == nil || s.wr == nil {
		return ""
	}
	return s.wr.ProviderSessionID()
}

func (s *Session) isLive() bool {
	if s == nil || s.wr == nil || s.runDone == nil {
		return false
	}
	if s.isACP {
		if snapshot, ok := s.wr.ACPSnapshot(); ok {
			return snapshot.Live
		}
	}
	select {
	case <-s.runDone:
		return false
	default:
		return true
	}
}

// cancelBoot aborts Wrapper.Run only at the manager's launch/adoption
// shutdown boundary. It is intentionally separate from Stop so ordinary
// cooperative session termination retains Wrapper's clean-exit semantics.
func (s *Session) cancelBoot() {
	if s != nil && s.runCancel != nil {
		s.runCancel()
	}
}
