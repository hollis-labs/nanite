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
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/hollis-labs/nanite/internal/store"
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
// NotificationSink receives a message-received hook on every
// successful SendMessage. Implementations bridge messaging events
// into the session SSE stream so subscribed UI clients see an
// incoming message chip / notification. Nil-safe — Service skips the
// sink call when this is unset (e.g. in tests).
type NotificationSink interface {
	NotifyReceived(ctx context.Context, msg *Message)
}

type Service struct {
	store     Store
	db        *sql.DB
	resolver  AgentResolver
	registrar AgentRegistrar  // nil = auto-register disabled
	sink      NotificationSink // nil = no SSE push
	pub       *pubsub
}

// NewService constructs a Service. The Store is used for message CRUD
// and should wrap the same underlying DB as the *sql.DB so that
// handoff transactions and message reads see consistent state. The
// resolver is used by ValidateAgentID when send/subscribe arrives
// with an unknown agent id. The registrar (optional) enables T6
// auto-register-on-first-send — pass nil to disable.
func NewService(s Store, db *sql.DB, r AgentResolver, reg AgentRegistrar) *Service {
	return &Service{
		store:     s,
		db:        db,
		resolver:  r,
		registrar: reg,
		pub:       newPubsub(),
	}
}

// SetNotificationSink wires (or unwires) the T7 message-received
// hook. Separate from NewService so the container can wire the sink
// after StreamManager construction without threading it through
// every caller that doesn't care.
func (svc *Service) SetNotificationSink(s NotificationSink) {
	svc.sink = s
}

// SendMessage validates both ends of the address tuple, persists the
// message via the Store, and fans the row out to any live subscribers
// on the recipient's (session, agent) key.
//
// T6 auto-register: if the caller's FromAgentID is not the user
// sentinel and doesn't resolve AND the Service has a registrar wired
// in, a minimal agent_profiles row is inserted with kind='external'
// (default) or 'cli' (when input.RegisterAs == "cli"). The send then
// proceeds with the freshly-registered ID. Without a registrar the
// unknown id still rejects through ValidateAgentID below.
func (svc *Service) SendMessage(ctx context.Context, input SendInput) (*Message, error) {
	registered, err := svc.maybeAutoRegister(ctx, input.FromAgentID, input.RegisterAs)
	if err != nil {
		return nil, fmt.Errorf("%w: from_agent_id: %v", ErrValidation, err)
	}
	// Skip the from-side ValidateAgentID call when we just auto-
	// registered the id: the resolver may cache negative lookups or
	// (as in tests) be a fixed known-set that doesn't see DB writes.
	// We know the row exists because we just wrote it.
	if !registered {
		if err := ValidateAgentID(ctx, svc.resolver, input.FromAgentID); err != nil {
			return nil, fmt.Errorf("%w: from_agent_id: %v", ErrValidation, err)
		}
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
	if svc.sink != nil {
		// Best-effort notification; don't fail the send if the sink
		// can't deliver (e.g. no active SSE for that session yet).
		svc.sink.NotifyReceived(ctx, out)
	}
	// T8: record send in the session event-log so context broker +
	// replay tooling can reconstruct session history.
	svc.writeSendEvents(ctx, out)
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
func (svc *Service) Inbox(ctx context.Context, sessionID, agentID string, filter InboxFilter, callerSessionID, callerAgentID string) ([]Message, error) {
	if callerSessionID != sessionID || callerAgentID != agentID {
		return nil, fmt.Errorf("%w: caller does not match inbox owner", ErrForbidden)
	}
	return svc.store.Inbox(ctx, sessionID, agentID, filter)
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
	if err := svc.store.Ack(ctx, msgID); err != nil {
		return err
	}
	svc.writeMessageEvent(ctx, sessionID, EventMessageAcked, msg)
	return nil
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
	if err := svc.store.Resolve(ctx, msgID); err != nil {
		return err
	}
	svc.writeMessageEvent(ctx, sessionID, EventMessageResolved, msg)
	return nil
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

// maybeAutoRegister inserts a minimal agent_profiles row for an
// unknown fromAgentID when the Service has a registrar wired.
// Returns (registered=true, nil) when a fresh row was inserted,
// (registered=false, nil) when the id already resolves or auto-
// register is disabled, and (_, err) when the insert fails for a
// reason other than a lost race.
func (svc *Service) maybeAutoRegister(ctx context.Context, fromAgentID, registerAs string) (bool, error) {
	if svc.registrar == nil || fromAgentID == "" || fromAgentID == UserSentinel {
		return false, nil
	}
	if svc.resolver != nil {
		switch _, err := svc.resolver.Get(ctx, fromAgentID); {
		case err == nil:
			return false, nil // already registered
		case errors.Is(err, sql.ErrNoRows):
			// Genuinely not found; fall through to register.
		default:
			// Transient or structural resolver error (DB down,
			// permission denied, etc.) — do NOT auto-register over
			// a real error. Surface it so the caller sees a clear
			// validation failure rather than a silent agent
			// creation.
			return false, fmt.Errorf("resolver: %w", err)
		}
	}
	kind := "external"
	if registerAs == "cli" {
		kind = "cli"
	}
	profile := &store.AgentProfile{
		ID:     fromAgentID,
		Slug:   slugifyAgentID(fromAgentID),
		Name:   fromAgentID,
		Source: "auto",
		Kind:   kind,
	}
	if err := svc.registrar.CreateAgent(profile); err != nil {
		// Race: another request may have registered the same id
		// concurrently (SQLite UNIQUE constraint). Treat as
		// success if the resolver now sees the row.
		if svc.resolver != nil {
			if _, gerr := svc.resolver.Get(ctx, fromAgentID); gerr == nil {
				slog.Info("messaging: auto-register lost race, proceeding",
					"agent_id", fromAgentID, "kind", kind, "err", err)
				return true, nil
			}
		}
		return false, fmt.Errorf("auto-register: %w", err)
	}
	slog.Info("messaging: auto-registered agent on first message_send",
		"agent_id", fromAgentID, "kind", kind)
	return true, nil
}

// slugifyAgentID turns an opaque from_agent_id into a slug suitable
// for the agent_profiles.slug unique index. Conservative: lowercase,
// trim, replace any non-[a-z0-9-] with '-', strip leading/trailing
// dashes. If the cleaned slug is empty, fall back to the raw id —
// CreateAgent will surface the constraint violation clearly if that
// also fails.
func slugifyAgentID(id string) string {
	lower := strings.ToLower(id)
	var b strings.Builder
	for _, r := range lower {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		case r == '_' || r == ' ' || r == '.' || r == '/':
			b.WriteRune('-')
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		return id
	}
	return slug
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
