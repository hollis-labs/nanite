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
		return nil, fmt.Errorf("%w: message required", ErrValidation)
	}
	if err := ValidateAgentID(ctx, svc.resolver, msg.FromAgentID); err != nil {
		return nil, fmt.Errorf("%w: from_agent_id: %v", ErrValidation, err)
	}
	if err := ValidateAgentID(ctx, svc.resolver, msg.ToAgentID); err != nil {
		return nil, fmt.Errorf("%w: to_agent_id: %v", ErrValidation, err)
	}
	if msg.FromSessionID == "" {
		return nil, fmt.Errorf("%w: from_session_id required", ErrValidation)
	}
	if msg.ToSessionID == "" {
		return nil, fmt.Errorf("%w: to_session_id required", ErrValidation)
	}
	if msg.Body == "" {
		return nil, fmt.Errorf("%w: body required", ErrValidation)
	}

	// DB error: propagate unwrapped so it is classified as internal, not
	// validation, at the API boundary.
	out, err := svc.store.SendA2AMessage(msg)
	if err != nil {
		return nil, err
	}
	if svc.pub != nil {
		svc.pub.publish(out)
	}
	return out, nil
}

// Inbox returns messages addressed to the (sessionID, agentID) pair. The
// caller's (callerSessionID, callerAgentID) is required and must match the
// inbox owner — a caller may only read its own inbox. Returns ErrForbidden
// on mismatch. If status is non-empty, only messages in that status are
// returned. Messages are ordered by priority DESC, then created_at ASC
// (see store docs).
//
// MVP defensive-authz note: the tool-broker (S4a) is the real ACL; this
// check exists so misaddressed MCP/HTTP calls fail loudly rather than
// silently returning someone else's inbox. Real caller-identity-from-ctx
// is a post-MVP upgrade.
func (svc *Service) Inbox(ctx context.Context, sessionID, agentID, status, callerSessionID, callerAgentID string) ([]store.A2AMessage, error) {
	if callerSessionID != sessionID || callerAgentID != agentID {
		return nil, fmt.Errorf("%w: caller does not match inbox owner", ErrForbidden)
	}
	return svc.store.GetA2AInbox(sessionID, agentID, status)
}

// Thread returns all messages in a thread, chronologically (oldest first),
// filtered to only those where the caller is either the sender or the
// recipient. Non-participants see an empty slice — the service does not
// leak "thread exists but you cannot see it" signal.
func (svc *Service) Thread(ctx context.Context, threadID, callerSessionID, callerAgentID string) ([]store.A2AMessage, error) {
	rows, err := svc.store.GetA2AThread(threadID)
	if err != nil {
		return nil, err
	}
	out := make([]store.A2AMessage, 0, len(rows))
	for _, m := range rows {
		if (m.FromSessionID == callerSessionID && m.FromAgentID == callerAgentID) ||
			(m.ToSessionID == callerSessionID && m.ToAgentID == callerAgentID) {
			out = append(out, m)
		}
	}
	return out, nil
}

// Ack marks a message as read. The caller must be the message's intended
// recipient: (sessionID, agentID) is checked against (msg.ToSessionID,
// msg.ToAgentID) and mismatches return ErrForbidden. The agent ID is also
// validated so typos and garbage don't silently succeed.
func (svc *Service) Ack(ctx context.Context, sessionID, agentID, msgID string) error {
	if err := ValidateAgentID(ctx, svc.resolver, agentID); err != nil {
		return fmt.Errorf("%w: agent_id: %v", ErrValidation, err)
	}
	msg, err := svc.store.GetA2AMessage(msgID)
	if err != nil {
		return err
	}
	if msg.ToSessionID != sessionID || msg.ToAgentID != agentID {
		return fmt.Errorf("%w: caller is not message recipient", ErrForbidden)
	}
	return svc.store.AckA2AMessage(msgID)
}

// Resolve marks a message as resolved. Same recipient-ownership check as
// Ack — mismatches return ErrForbidden.
func (svc *Service) Resolve(ctx context.Context, sessionID, agentID, msgID string) error {
	if err := ValidateAgentID(ctx, svc.resolver, agentID); err != nil {
		return fmt.Errorf("%w: agent_id: %v", ErrValidation, err)
	}
	msg, err := svc.store.GetA2AMessage(msgID)
	if err != nil {
		return err
	}
	if msg.ToSessionID != sessionID || msg.ToAgentID != agentID {
		return fmt.Errorf("%w: caller is not message recipient", ErrForbidden)
	}
	return svc.store.ResolveA2AMessage(msgID)
}

// RecentForSession returns the last N messages touching a session (as
// sender or receiver), regardless of agent. This powers handoff catch-up
// in Task 6, where a newly spawned agent needs a summary of what happened
// in the session before it took over. The default limit is 20 when the
// caller passes 0 or a negative value; the absolute cap is enforced at
// the store layer (store.MaxRecentLimit).
func (svc *Service) RecentForSession(ctx context.Context, sessionID string, limit int) ([]store.A2AMessage, error) {
	if limit <= 0 {
		limit = 20
	}
	return svc.store.GetA2ARecent(sessionID, limit)
}

// Close drains all pubsub subscriber channels and clears the subscriber
// map. After Close, publish becomes a no-op (no subscribers); existing
// subscribers see their receive channels close, unblocking any pending
// receive. Safe to call multiple times.
//
// Wired into Container.Shutdown so graceful shutdown of the nanite-agent
// process unblocks any agent still reading from a subscription. Pair with
// F06 ctx propagation: together they close the subscriber-lifecycle gap.
func (svc *Service) Close() error {
	if svc.pub != nil {
		svc.pub.closeAll()
	}
	return nil
}
