// Package messaging — service.go
//
// Service is the authorization + fan-out wrapper around Store. CLI,
// HTTP, and MCP callers go through Service rather than touching Store
// directly so that validation, auth checks, and subscriber fan-out
// all live in one place.
package messaging

import (
	"context"
	"database/sql"
	"fmt"
)

// Service coordinates messaging validation, persistence, subscriber
// fan-out, and handoff-state mutations. It holds three collaborators:
//
//   - store: the Nexus-shaped messaging Store for message CRUD.
//   - db:    the underlying *sql.DB for cross-table handoff txns that
//            reach into session_handoffs / session_agents — those
//            tables live outside the messaging Store interface.
//   - resolver: looks up agents by ID so SendMessage and Subscribe
//            can reject unknown addresses.
//
// Service is safe for concurrent use since Store, the *sql.DB, and
// the pubsub are each safe for concurrent use.
type Service struct {
	store    Store
	db       *sql.DB
	resolver AgentResolver
	pub      *pubsub
}

// NewService constructs a Service. The Store is used for message CRUD
// and should wrap the same underlying DB as the *sql.DB so that
// handoff transactions and message reads see consistent state. The
// resolver is used by ValidateAgentID when send/subscribe arrives
// with an unknown agent id.
func NewService(s Store, db *sql.DB, r AgentResolver) *Service {
	return &Service{
		store:    s,
		db:       db,
		resolver: r,
		pub:      newPubsub(),
	}
}

// SendMessage validates both ends of the address tuple, persists the
// message via the Store, and fans the row out to any live subscribers
// on the recipient's (session, agent) key.
func (svc *Service) SendMessage(ctx context.Context, input SendInput) (*Message, error) {
	if err := ValidateAgentID(ctx, svc.resolver, input.FromAgentID); err != nil {
		return nil, fmt.Errorf("%w: from_agent_id: %v", ErrValidation, err)
	}
	if err := ValidateAgentID(ctx, svc.resolver, input.ToAgentID); err != nil {
		return nil, fmt.Errorf("%w: to_agent_id: %v", ErrValidation, err)
	}
	if input.FromSessionID == "" {
		return nil, fmt.Errorf("%w: from_session_id required", ErrValidation)
	}
	if input.ToSessionID == "" {
		return nil, fmt.Errorf("%w: to_session_id required", ErrValidation)
	}
	if input.Body == "" {
		return nil, fmt.Errorf("%w: body required", ErrValidation)
	}

	out, err := svc.store.Send(ctx, input)
	if err != nil {
		return nil, err
	}
	if svc.pub != nil {
		svc.pub.publish(out)
	}
	return out, nil
}

// Inbox returns messages addressed to (sessionID, agentID). The
// caller's (callerSessionID, callerAgentID) is required and must
// match the inbox owner — a caller may only read its own inbox.
// Returns an error wrapping ErrForbidden on mismatch.
//
// MVP defensive-authz note: the tool-broker (S4a) is the real ACL;
// this check exists so misaddressed MCP/HTTP calls fail loudly rather
// than silently returning someone else's inbox. Real caller-identity-
// from-ctx is a post-MVP upgrade.
func (svc *Service) Inbox(ctx context.Context, sessionID, agentID, status, callerSessionID, callerAgentID string) ([]Message, error) {
	if callerSessionID != sessionID || callerAgentID != agentID {
		return nil, fmt.Errorf("%w: caller does not match inbox owner", ErrForbidden)
	}
	return svc.store.Inbox(ctx, sessionID, agentID, status)
}

// Thread returns all messages in a thread, chronologically, filtered
// to only those where the caller is either the sender or the
// recipient. Non-participants see an empty slice — the service does
// not leak "thread exists but you cannot see it" signal.
func (svc *Service) Thread(ctx context.Context, threadID, callerSessionID, callerAgentID string) ([]Message, error) {
	rows, err := svc.store.Thread(ctx, threadID)
	if err != nil {
		return nil, err
	}
	out := make([]Message, 0, len(rows))
	for _, m := range rows {
		if (m.FromSessionID == callerSessionID && m.FromAgentID == callerAgentID) ||
			(m.ToSessionID == callerSessionID && m.ToAgentID == callerAgentID) {
			out = append(out, m)
		}
	}
	return out, nil
}

// Ack marks a message as read. The caller must be the message's
// intended recipient: (sessionID, agentID) is checked against the
// persisted (ToSessionID, ToAgentID) and mismatches return an error
// wrapping ErrForbidden.
func (svc *Service) Ack(ctx context.Context, sessionID, agentID, msgID string) error {
	if err := ValidateAgentID(ctx, svc.resolver, agentID); err != nil {
		return fmt.Errorf("%w: agent_id: %v", ErrValidation, err)
	}
	msg, err := svc.store.Get(ctx, msgID)
	if err != nil {
		return err
	}
	if msg.ToSessionID != sessionID || msg.ToAgentID != agentID {
		return fmt.Errorf("%w: caller is not message recipient", ErrForbidden)
	}
	return svc.store.Ack(ctx, msgID)
}

// Resolve marks a message as resolved. Same recipient-ownership check
// as Ack — mismatches return an error wrapping ErrForbidden.
func (svc *Service) Resolve(ctx context.Context, sessionID, agentID, msgID string) error {
	if err := ValidateAgentID(ctx, svc.resolver, agentID); err != nil {
		return fmt.Errorf("%w: agent_id: %v", ErrValidation, err)
	}
	msg, err := svc.store.Get(ctx, msgID)
	if err != nil {
		return err
	}
	if msg.ToSessionID != sessionID || msg.ToAgentID != agentID {
		return fmt.Errorf("%w: caller is not message recipient", ErrForbidden)
	}
	return svc.store.Resolve(ctx, msgID)
}

// RecentForSession returns the last N messages touching a session
// (as sender or receiver), regardless of agent. Powers handoff
// catch-up, where a newly spawned agent needs a summary of what
// happened in the session before it took over. Default and max
// bounds are enforced by Store.Recent.
func (svc *Service) RecentForSession(ctx context.Context, sessionID string, limit int) ([]Message, error) {
	return svc.store.Recent(ctx, sessionID, limit)
}

// UnreadCount returns the count of unread messages for (sessionID,
// agentID). Thin pass-through — adding a caller-match defensive check
// here would pair with Inbox, but the unread count does not leak
// content, and the existing HTTP handler does not have caller
// identity plumbed yet. Revisit when caller-identity-from-ctx lands.
func (svc *Service) UnreadCount(ctx context.Context, sessionID, agentID string) (int, error) {
	return svc.store.UnreadCount(ctx, sessionID, agentID)
}

// Close drains all pubsub subscriber channels and clears the
// subscriber map. After Close, publish becomes a no-op (no
// subscribers); existing subscribers see their receive channels
// close, unblocking any pending receive. Safe to call multiple times.
//
// Wired into Container.Shutdown so graceful shutdown of the nanite-
// agent process unblocks any agent still reading from a subscription.
func (svc *Service) Close() error {
	if svc.pub != nil {
		svc.pub.closeAll()
	}
	return nil
}
