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

	"github.com/hollis-labs/nanite/internal/lifecycle"
	"github.com/hollis-labs/nanite/internal/safego"
	"github.com/hollis-labs/nanite/internal/store"
)

// Service coordinates messaging validation, persistence, subscriber
// fan-out, and handoff-state mutations. It holds three collaborators:
//
//   - store: the Nexus-shaped messaging Store for message CRUD.
//   - db:    the underlying *sql.DB for cross-table handoff txns that
//     reach into session_handoffs / session_agents — those
//     tables live outside the messaging Store interface.
//   - resolver: looks up agents by ID so SendMessage and Subscribe
//     can reject unknown addresses.
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

// WakeReactor receives a live-wake hook on every successful SendMessage
// whose Kind is eligible (see the SendMessage call site for the
// KindSubagentResult exclusion). Implementations resolve the recipient
// session's message-wake policy and, when eligible, invoke the same
// live-wake machinery SendAgentMessage/TriggerHarnessTurn already use
// (CW-20260816-0065) so the recipient session generates a new turn
// instead of relying on the recipient polling message_inbox/
// message_thread.
//
// Implemented by internal/service (chatServiceImpl owns the in-flight-
// generation registry and session/agent resolution this needs, neither
// of which internal/messaging has visibility into). Defined here — not
// in internal/service — to avoid an import cycle: internal/service
// constructs messaging.Service, not the reverse (same reasoning as
// subagent.CompletionReactor). Wired via SetWakeReactor; nil is
// permitted (reaction is a no-op when no reactor is set, same contract
// as NotificationSink).
type WakeReactor interface {
	ReactToMessage(ctx context.Context, msg *Message)
}

type Service struct {
	store     Store
	db        *sql.DB
	resolver  AgentResolver
	registrar AgentRegistrar     // nil = auto-register disabled
	sink      NotificationSink   // nil = no SSE push
	wake      WakeReactor        // nil = no live-wake side effect (CW-20260816-0065)
	lifecycle *lifecycle.Manager // nil = fall back to untracked safego.Go (see SetLifecycleManager)
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

// SetWakeReactor wires (or unwires) the CW-20260816-0065 live-wake hook.
// Separate from NewService so the container can wire it after
// chatServiceImpl construction (the reactor needs the fully-built chat
// service to reach its in-flight-generation registry) without threading
// it through every caller that doesn't care — mirrors SetNotificationSink
// and subagent.Service.SetCompletionReactor.
func (svc *Service) SetWakeReactor(r WakeReactor) {
	svc.wake = r
}

// SetLifecycleManager wires (or unwires) the tracked-goroutine Manager
// used to spawn the wake-reactor side effect in SendMessage. Separate
// from NewService for the same reason as SetWakeReactor/
// SetNotificationSink: the container builds messaging.Service before
// the chat service (whose *lifecycle.Manager this typically reuses —
// see internal/service/container.go) exists. When unset, SendMessage
// falls back to an untracked safego.Go spawn (still panic-safe, just
// not drained on Shutdown) so tests and standalone Service construction
// keep working without wiring a manager.
func (svc *Service) SetLifecycleManager(m *lifecycle.Manager) {
	svc.lifecycle = m
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
		return nil, fmt.Errorf("%w: from_agent_id: %w", ErrValidation, err)
	}
	// Skip the from-side ValidateAgentID call when we just auto-
	// registered the id: the resolver may cache negative lookups or
	// (as in tests) be a fixed known-set that doesn't see DB writes.
	// We know the row exists because we just wrote it.
	if !registered {
		if err := ValidateAgentID(ctx, svc.resolver, input.FromAgentID); err != nil {
			return nil, fmt.Errorf("%w: from_agent_id: %w", ErrValidation, err)
		}
	}
	if err := ValidateAgentID(ctx, svc.resolver, input.ToAgentID); err != nil {
		return nil, fmt.Errorf("%w: to_agent_id: %w", ErrValidation, err)
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

	// CW-20260816-0065: react to the send by optionally triggering a live
	// turn on the recipient session instead of leaving delivery to a
	// poll. Skipped for Kind=KindSubagentResult — that specific kind
	// already has its own dedicated wake path (subagent.CompletionReactor,
	// invoked directly by internal/subagent/service.go right after its own
	// SendMessage call, with its own busy-check and a completion-specific
	// summarizing prompt fed by the kind=subagent_result turn-start
	// injection). Reacting here too would double-trigger the same
	// completion event through two different synthetic prompts racing
	// registerGenerationIfIdle. Fire-and-forget in its own goroutine
	// (mirrors internal/subagent/service.go's "subagent.completion-
	// reactor" goroutine) with a background context so a slow/misbehaving
	// reactor never blocks the SendMessage caller (self-tool call, HTTP
	// handler, or background-job poster) and outlives a cancelled request
	// ctx.
	//
	// msgCopy takes a shallow copy of *out before handing it to the
	// goroutine: out is also returned to SendMessage's own caller, so
	// without the copy the goroutine and the caller would share the same
	// *Message pointer — a data race if the caller mutates/reuses it
	// after SendMessage returns. A shallow copy is sufficient: the two
	// *string fields (ReadAt/ResolvedAt) are set by separate post-send
	// code paths (Ack/Resolve), not something the original caller races
	// on here.
	//
	// Spawn goes through svc.lifecycle (a *lifecycle.Manager, tracked and
	// drained on Shutdown) when wired; falls back to untracked-but-still-
	// panic-safe safego.Go otherwise (e.g. tests that construct Service
	// directly without SetLifecycleManager).
	if svc.wake != nil && out.Kind != KindSubagentResult {
		msgCopy := *out
		if svc.lifecycle != nil {
			svc.lifecycle.Go("wake-reactor", func(ctx context.Context) {
				svc.wake.ReactToMessage(ctx, &msgCopy)
			})
		} else {
			safego.Go(context.Background(), "messaging.wake-reactor", func() {
				svc.wake.ReactToMessage(context.Background(), &msgCopy)
			})
		}
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
		return fmt.Errorf("%w: agent_id: %w", ErrValidation, err)
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
		return fmt.Errorf("%w: agent_id: %w", ErrValidation, err)
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
// agentID). When a CallerIdentity is carried on ctx (set by the HTTP
// caller-identity middleware — see internal/messaging/caller.go and
// internal/server/caller_identity.go), the caller tuple must match
// the inbox owner; mismatches return ErrForbidden to pair with the
// Inbox / Thread / Ack / Resolve caller-match pattern. When no
// identity is plumbed, the check falls open — matches the pre-G-6.3
// MVP trust-the-query behavior for MCP and CLI callers that have not
// yet been wired through ctx.
func (svc *Service) UnreadCount(ctx context.Context, sessionID, agentID string) (int, error) {
	if caller, ok := CallerFromCtx(ctx); ok {
		if caller.SessionID != sessionID || caller.AgentID != agentID {
			return 0, fmt.Errorf("%w: caller does not match inbox owner", ErrForbidden)
		}
	}
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
	if err := svc.registrar.CreateAgent(ctx, profile); err != nil {
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
