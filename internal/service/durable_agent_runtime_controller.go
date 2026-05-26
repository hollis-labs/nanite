package service

import (
	"context"
	"errors"
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
