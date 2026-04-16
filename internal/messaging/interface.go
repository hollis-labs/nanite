// Package messaging defines the nanite messaging subsystem — the
// first-class agent-to-agent and agent-to-user message primitive.
//
// This package replaces the former `internal/store/a2a.go` +
// `internal/service/a2a/*` split. The public Go types, the on-disk
// `agent_messages` table (renamed from `a2a_messages` by migration
// 018; the `agent_` prefix distinguishes from the session-chat
// `messages` table), and the `session_handoffs` table all use a
// consistent "message" vocabulary.
//
// The Store interface is shaped after the Nexus messaging reference
// impl (~/Projects-apps/nexus/messaging) with one adaptation: nanite
// addresses messages by (session_id, agent_id) tuples rather than a
// single agent-id string, so the same agent running in two sessions
// has two distinct inboxes. The method names and signatures otherwise
// mirror Nexus so that a future extraction to a standalone
// `hollis-labs/go-messaging` repo is a move, not a rewrite.
//
// Authorization lives at the service layer (internal/messaging/service),
// not in Store — Store is a pure persistence boundary.
package messaging

import "context"

// Store is the persistence interface for nanite messaging. Implementations
// persist Message rows keyed by id, thread, and the (session, agent)
// address tuples, plus a small set of status columns (unread/read/
// resolved/read_at/resolved_at).
//
// All methods take context.Context first so that cancellations and
// timeouts propagate from the HTTP/MCP boundary into the backing store.
//
// Store has no authorization logic; it trusts its caller to have
// already verified the caller's right to read or mutate the addressed
// row. The service layer is responsible for those checks.
type Store interface {
	// Send persists a new message. Defaults are populated for missing
	// ID (uuid), Type ("message"), Metadata ("{}"), Priority (2),
	// Status ("unread"), ThreadID (message self-thread), and CreatedAt
	// (now). Returns the authoritative persisted row so callers see
	// server-computed values.
	Send(ctx context.Context, input SendInput) (*Message, error)

	// Get returns a single message by ID. Returns an error wrapping
	// ErrNotFound if no such message exists.
	Get(ctx context.Context, msgID string) (*Message, error)

	// Inbox returns messages addressed to (sessionID, agentID),
	// optionally filtered by the fields of InboxFilter (empty string
	// on any filter field = "no constraint on that dimension").
	// Ordered by priority DESC, created_at ASC so high-priority
	// messages surface first while FIFO holds within a priority tier.
	Inbox(ctx context.Context, sessionID, agentID string, filter InboxFilter) ([]Message, error)

	// Thread returns all messages belonging to a thread, ordered
	// chronologically (oldest first). Same-tick inserts are tiebroken
	// by rowid so burst-insert ordering is deterministic.
	Thread(ctx context.Context, threadID string) ([]Message, error)

	// Recent returns up to limit recent messages touching sessionID as
	// either sender or receiver, ordered chronologically (oldest first).
	// limit <= 0 uses the internal default (20); limits above
	// MaxRecentLimit are clamped. Powers catch-up for agents that just
	// spawned or resumed and need the last slice of session traffic.
	Recent(ctx context.Context, sessionID string, limit int) ([]Message, error)

	// Ack marks a message as read. A missing message ID returns an
	// error wrapping ErrNotFound.
	Ack(ctx context.Context, msgID string) error

	// Resolve marks a message as resolved. A missing message ID
	// returns an error wrapping ErrNotFound.
	Resolve(ctx context.Context, msgID string) error

	// UnreadCount returns the count of unread messages addressed to
	// (sessionID, agentID).
	UnreadCount(ctx context.Context, sessionID, agentID string) (int, error)
}
