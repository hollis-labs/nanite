package service

import (
	"context"
	"errors"

	"github.com/hollis-labs/nanite/internal/dispatcher"
)

type chatDurableAgentRuntimeController struct {
	chat ChatService
}

func NewChatDurableAgentRuntimeController(chat ChatService) DurableAgentRuntimeController {
	return chatDurableAgentRuntimeController{chat: chat}
}

func (c chatDurableAgentRuntimeController) StopSession(ctx context.Context, sessionID string) error {
	if c.chat == nil {
		return nil
	}
	_ = c.chat.CancelActiveGeneration(sessionID)
	_, err := c.chat.RebootSessionAgent(ctx, sessionID)
	if errors.Is(err, ErrSessionBusy) {
		return nil
	}
	return err
}

func (c chatDurableAgentRuntimeController) RebootSession(ctx context.Context, sessionID string) error {
	if c.chat == nil {
		return nil
	}
	_, err := c.chat.RebootSessionAgent(ctx, sessionID)
	return err
}

func (c chatDurableAgentRuntimeController) RecoverSession(ctx context.Context, sessionID string) error {
	if c.chat == nil {
		return nil
	}
	// An in-flight turn means the session is live and streaming — there is
	// nothing to recover, so treat ErrSessionBusy as success (best-effort).
	_, err := c.chat.RecoverSession(ctx, sessionID)
	if errors.Is(err, ErrSessionBusy) {
		return nil
	}
	return err
}

func (c chatDurableAgentRuntimeController) CancelSession(_ context.Context, sessionID string) error {
	if c.chat == nil {
		return nil
	}
	c.chat.CancelActiveGeneration(sessionID)
	return nil
}

// SendMessage delivers a durable agent's scheduled wake prompt as a real
// user turn on sessionID. It routes through the same ChatService.HandleMessage
// path real end-user chat messages use (internal/api/harness_v1.go,
// internal/api/messages.go) — but a durable-agent wake is background work
// by definition, not a user typing into chat, so it must not be tagged
// dispatcher.CallerChat like those two callers.
//
// Stamping dispatcher.CallerBackground onto ctx here (rather than
// hardcoding a CallerType parameter into HandleMessage's signature) reuses
// the ambient-ctx convention already established in chat_generate.go
// (read via dispatcher.CallerTypeFromContext) instead of inventing a
// second one. HandleMessage prefers this ctx-carried value when present
// and valid, falling back to dispatcher.CallerChat for the two real
// HTTP-handler callers, which stamp nothing.
func (c chatDurableAgentRuntimeController) SendMessage(ctx context.Context, sessionID, content string) error {
	if c.chat == nil {
		return nil
	}
	ctx = dispatcher.WithCallerType(ctx, dispatcher.CallerBackground)
	_, err := c.chat.HandleMessage(ctx, sessionID, content)
	return err
}
