// Package a2a is the A2A (agent-to-agent) service layer. CLI, HTTP, and MCP
// surfaces call into this package so validation, persistence, and subscriber
// fan-out live in exactly one place.
package a2a

import (
	"context"
	"fmt"

	"github.com/hollis-labs/nanite/internal/store"
)

// Service coordinates A2A message validation, persistence, and (future)
// subscriber fan-out. It is safe for concurrent use by multiple goroutines
// since the underlying store and pubsub are each safe for concurrent use.
type Service struct {
	store    *store.Store
	resolver AgentResolver
	pub      *pubsub
}

// NewService constructs a Service backed by the given store and agent
// resolver. The resolver is required by ValidateAgentID to check that
// addresses correspond to real agents (file-based or DB-backed). Callers
// should wire in a resolver that covers all agent sources — typically the
// parent package's service.AgentService — so that validation is uniform
// regardless of where an agent lives.
func NewService(s *store.Store, r AgentResolver) *Service {
	return &Service{
		store:    s,
		resolver: r,
		pub:      newPubsub(),
	}
}

// SendMessage validates both ends of the address tuple, inserts the message
// through the store, and (once Task 7 lands) fans it out to any subscribers
// watching the recipient's inbox. It returns the authoritative row as stored.
func (svc *Service) SendMessage(ctx context.Context, msg *store.A2AMessage) (*store.A2AMessage, error) {
	if msg == nil {
		return nil, fmt.Errorf("message required")
	}
	if err := ValidateAgentID(ctx, svc.resolver, msg.FromAgentID); err != nil {
		return nil, fmt.Errorf("from_agent_id: %w", err)
	}
	if err := ValidateAgentID(ctx, svc.resolver, msg.ToAgentID); err != nil {
		return nil, fmt.Errorf("to_agent_id: %w", err)
	}
	if msg.FromSessionID == "" {
		return nil, fmt.Errorf("from_session_id required")
	}
	if msg.ToSessionID == "" {
		return nil, fmt.Errorf("to_session_id required")
	}
	if msg.Body == "" {
		return nil, fmt.Errorf("body required")
	}

	out, err := svc.store.SendA2AMessage(msg)
	if err != nil {
		return nil, err
	}
	if svc.pub != nil {
		svc.pub.publish(out)
	}
	return out, nil
}

// Inbox returns messages addressed to the (sessionID, agentID) pair. If
// status is non-empty, only messages in that status are returned. Messages
// are ordered by priority DESC, then created_at ASC (see store docs).
func (svc *Service) Inbox(ctx context.Context, sessionID, agentID, status string) ([]store.A2AMessage, error) {
	return svc.store.GetA2AInbox(sessionID, agentID, status)
}

// Thread returns all messages in a thread, chronologically (oldest first).
func (svc *Service) Thread(ctx context.Context, threadID string) ([]store.A2AMessage, error) {
	return svc.store.GetA2AThread(threadID)
}

// Ack marks a message as read. The caller's (sessionID, agentID) is accepted
// for future impersonation checks and logging — MVP is permissive and does
// not require that the caller is the recipient. The agent ID is still
// validated so typos and garbage don't silently succeed.
func (svc *Service) Ack(ctx context.Context, sessionID, agentID, msgID string) error {
	if err := ValidateAgentID(ctx, svc.resolver, agentID); err != nil {
		return fmt.Errorf("agent_id: %w", err)
	}
	return svc.store.AckA2AMessage(msgID)
}

// Resolve marks a message as resolved. Same permissive semantics as Ack:
// validates the caller's agent ID but does not enforce ownership of the
// message.
func (svc *Service) Resolve(ctx context.Context, sessionID, agentID, msgID string) error {
	if err := ValidateAgentID(ctx, svc.resolver, agentID); err != nil {
		return fmt.Errorf("agent_id: %w", err)
	}
	return svc.store.ResolveA2AMessage(msgID)
}

// RecentForSession returns the last N messages touching a session (as
// sender or receiver), regardless of agent. This powers handoff catch-up
// in Task 6, where a newly spawned agent needs a summary of what happened
// in the session before it took over. The default limit is 20 when the
// caller passes 0 or a negative value.
func (svc *Service) RecentForSession(ctx context.Context, sessionID string, limit int) ([]store.A2AMessage, error) {
	if limit <= 0 {
		limit = 20
	}
	return svc.store.GetA2ARecent(sessionID, limit)
}
