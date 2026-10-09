package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/store"
)

// ConversationLocker fences message admission and manual context operations.
// The lock order is conversation, cognitive admission, then active generation.
type ConversationLocker interface {
	LockConversation(context.Context, string) (func(), error)
}

func (s *chatServiceImpl) LockConversation(ctx context.Context, sessionID string) (func(), error) {
	value, _ := s.conversationGates.LoadOrStore(sessionID, make(chan struct{}, 1))
	gate := value.(chan struct{})
	select {
	case gate <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-gate
			return nil, err
		}
		return func() { <-gate }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type ClearConversationOptions struct {
	KeepHandoff     bool     `json:"keep_handoff"`
	ConfirmCancel   bool     `json:"confirm_cancel"`
	ExpectedTurnIDs []string `json:"expected_turn_ids"`
}

type ClearConfirmationRequired struct{ TurnIDs []string }

func (e *ClearConfirmationRequired) Error() string {
	return "confirm cancellation of active turns before clearing"
}

// ClearSession uses the same host service for HTTP and client slash commands.
func (c *Container) ClearSession(ctx context.Context, sessionID string, opts ClearConversationOptions) (*store.ConversationClear, error) {
	clearer, ok := c.Chat.(interface {
		ClearConversation(context.Context, string, ClearConversationOptions) (*store.ConversationClear, error)
	})
	if !ok {
		return nil, fmt.Errorf("conversation clear unavailable")
	}
	return clearer.ClearConversation(ctx, sessionID, opts)
}

func (s *chatServiceImpl) ClearConversation(ctx context.Context, sessionID string, opts ClearConversationOptions) (*store.ConversationClear, error) {
	writer, ok := s.store.(interface {
		ClearConversation(context.Context, string, bool) (*store.ConversationClear, error)
	})
	if !ok {
		return nil, fmt.Errorf("conversation clear persistence unavailable")
	}
	release, err := s.LockConversation(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	defer release()
	// Check shutdown without holding the global admission mutex while waiting
	// for provider cancellation. Other sessions and shutdown remain responsive.
	s.cognitiveAdmissionMu.Lock()
	closed := s.cognitiveAdmissionClosed
	s.cognitiveAdmissionMu.Unlock()
	if closed {
		return nil, ErrCognitiveAdmissionClosed
	}
	if s.lifecycle != nil {
		ownerCtx, ownerCancel := context.WithCancel(ctx)
		stopOwner := context.AfterFunc(s.lifecycle.Context(), ownerCancel)
		defer func() { stopOwner(); ownerCancel() }()
		ctx = ownerCtx
	}
	if _, err = s.store.GetSession(ctx, sessionID); err != nil {
		return nil, err
	}
	s.activeGenMu.Lock()
	var targets []*inFlightGen
	var ids []string
	for gen := s.activeGen[sessionID]; gen != nil; gen = generationPredecessor(gen) {
		if !generationDone(gen) || !generationResolvedSafe(gen) {
			targets = append(targets, gen)
			ids = append(ids, gen.msgID)
		}
	}
	s.activeGenMu.Unlock()
	expected := slices.Clone(opts.ExpectedTurnIDs)
	slices.Sort(ids)
	slices.Sort(expected)
	if len(ids) > 0 && (!opts.ConfirmCancel || !slices.Equal(ids, expected)) {
		return nil, &ClearConfirmationRequired{TurnIDs: ids}
	}
	// Claim each exact generation. A queued neighbor cannot become an
	// unconfirmed target: no admission can pass the held conversation gate.
	slices.Reverse(targets) // Resolve predecessors before their queued successors.
	for _, gen := range targets {
		s.CancelCognitiveTurn(sessionID, gen.msgID)
	}
	drainCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for _, gen := range targets {
		if !waitClosed(drainCtx, gen.done) || !waitClosed(drainCtx, gen.safeBoundary) {
			return nil, fmt.Errorf("clear: turn %s has not reached a safe boundary: %w", gen.msgID, drainCtx.Err())
		}
		if generationCancelAsked(gen) && !waitClosed(drainCtx, gen.cancelIssued) {
			return nil, fmt.Errorf("clear: cancellation still settling: %w", drainCtx.Err())
		}
		// Done may close just before deferred deregistration. Retire only the
		// captured, safely drained owner; never erase a concurrent replacement.
		s.deregisterGeneration(sessionID, gen)
	}
	// Provider-side context must end too. Stop failure refuses the durable cut;
	// the transcript and handoff are retained for retry, even after cancellation.
	outgoing, hadRuntime := s.runtimeSessions().Load(sessionID)
	if _, err = s.rebootRuntime(ctx, sessionID, true); err != nil {
		return nil, err
	}
	if hadRuntime {
		// Stop acknowledges the provider interrupt; Run may still be draining
		// callbacks. Await that exact owner before clearing its persisted resume
		// identity so a late callback cannot write an old provider thread back.
		stopCtx, stopCancel := context.WithTimeout(ctx, stopRebootGrace)
		waitErr := outgoing.Wait(stopCtx)
		stopCancel()
		if errors.Is(waitErr, context.Canceled) || errors.Is(waitErr, context.DeadlineExceeded) {
			return nil, fmt.Errorf("clear: runtime has not finished draining: %w", waitErr)
		}
	}
	cut, err := writer.ClearConversation(ctx, sessionID, opts.KeepHandoff)
	if err != nil {
		return nil, err
	}
	s.freshBootSessions.Store(sessionID, struct{}{})
	s.activeSessionSlots.Delete(sessionID)
	if s.streams != nil {
		events := make(chan chat.StreamEvent, 1)
		if eventErr := chat.EmitSlotChangedEvent(events, chat.SlotChangedV1{
			Slot: "Conversation", Change: chat.SlotChangeDropped, Reasoning: "/clear requested; transcript retained.",
		}); eventErr == nil {
			s.streams.BroadcastSessionStreamEvent(sessionID, <-events)
		}
	}
	return cut, nil
}

// Narrow optional port keeps legacy test stores useful. Production Store
// always supplies the durable working-history projection.
func listWorkingMessages(ctx context.Context, st SessionReader, sessionID string, limit int) ([]store.Message, error) {
	if reader, ok := st.(interface {
		ListWorkingMessages(context.Context, string, int) ([]store.Message, error)
	}); ok {
		return reader.ListWorkingMessages(ctx, sessionID, limit)
	}
	return st.ListMessages(ctx, sessionID, limit)
}
