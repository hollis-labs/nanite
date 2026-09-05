// Package mailboxadapter connects go-messaging's host-neutral seams to
// Nanite's existing SQLite schema and agent registry.
package mailboxadapter

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	messaging "github.com/hollis-labs/go-messaging/mailbox"
	"github.com/hollis-labs/nanite/internal/a2a"
	"github.com/hollis-labs/nanite/internal/store"
)

// Components is Nanite's host wiring around the reusable mailbox
// service. The mailbox owns transport-neutral message behavior; the session
// event and handoff adapters below own Nanite's schema and policy.
type Components struct {
	Service *messaging.Service
	Events  *SessionEvents
}

// New constructs the shared mailbox against Nanite's
// existing agent_messages table and installs every host-owned seam required by
// the CLI, HTTP, self-tool, and chat-service call paths.
func New(st *store.Store) Components {
	registry := &messagingAgentRegistry{store: st}
	sessionEvents := &SessionEvents{db: st.DB}
	service := messaging.NewService(messaging.NewSQLiteStore(st.DB), registry, registry)
	service.SetEventStore(sessionEvents)
	service.SetHandoffCoordinator(&messagingHandoffCoordinator{db: st.DB})
	return Components{Service: service, Events: sessionEvents}
}

type messagingAgentRegistry struct {
	store *store.Store
}

func (registry *messagingAgentRegistry) AgentExists(ctx context.Context, agentID string) (bool, error) {
	if agentID == a2a.UserSentinel {
		return true, nil
	}
	if registry == nil || registry.store == nil {
		return false, fmt.Errorf("agent registry is not configured")
	}
	_, err := registry.store.GetAgent(ctx, agentID)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, err
}

func (registry *messagingAgentRegistry) RegisterAgent(ctx context.Context, agentID, registerAs string) error {
	if registry == nil || registry.store == nil {
		return fmt.Errorf("agent registry is not configured")
	}
	kind := "external"
	if registerAs == "cli" {
		kind = "cli"
	}
	return registry.store.CreateAgent(ctx, &store.AgentProfile{
		ID:     agentID,
		Slug:   messagingAgentSlug(agentID),
		Name:   agentID,
		Source: "auto",
		Kind:   kind,
	})
}

func messagingAgentSlug(agentID string) string {
	lower := strings.ToLower(agentID)
	var slug strings.Builder
	for _, char := range lower {
		switch {
		case char >= 'a' && char <= 'z', char >= '0' && char <= '9', char == '-':
			slug.WriteRune(char)
		case char == '_' || char == ' ' || char == '.' || char == '/':
			slug.WriteRune('-')
		}
	}
	cleaned := strings.Trim(slug.String(), "-")
	if cleaned == "" {
		return agentID
	}
	return cleaned
}

// SessionEvents implements both the mailbox EventStore seam and Nanite's
// writer shape for provider/compaction/harness lifecycle events.
type SessionEvents struct {
	db *sql.DB
}

func (events *SessionEvents) Append(ctx context.Context, event messaging.SessionEvent) error {
	if events == nil || events.db == nil {
		return fmt.Errorf("session event store is not configured")
	}
	_, err := events.db.ExecContext(ctx,
		`INSERT INTO session_events (id, session_id, event_type, channel, envelope_pointer_json, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		event.ID, event.SessionID, event.EventType, event.Channel, event.EnvelopePointerJSON, event.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("append session event: %w", err)
	}
	return nil
}

func (events *SessionEvents) Recent(ctx context.Context, sessionID string, limit int) ([]messaging.SessionEvent, error) {
	if events == nil || events.db == nil {
		return nil, fmt.Errorf("session event store is not configured")
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	rows, err := events.db.QueryContext(ctx, `
		SELECT id, session_id, event_type, channel, envelope_pointer_json, created_at
		FROM (
			SELECT rowid AS event_rowid, id, session_id, event_type, channel, envelope_pointer_json, created_at
			FROM session_events
			WHERE session_id = ?
			ORDER BY created_at DESC, rowid DESC
			LIMIT ?
		)
		ORDER BY created_at ASC, event_rowid ASC`, sessionID, limit)
	if err != nil {
		return nil, fmt.Errorf("session events: %w", err)
	}
	defer func() { _ = rows.Close() }()

	result := make([]messaging.SessionEvent, 0)
	for rows.Next() {
		var event messaging.SessionEvent
		if err := rows.Scan(&event.ID, &event.SessionID, &event.EventType, &event.Channel, &event.EnvelopePointerJSON, &event.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan session event: %w", err)
		}
		result = append(result, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate session events: %w", err)
	}
	return result, nil
}

func (events *SessionEvents) WriteSessionEvent(ctx context.Context, sessionID, eventType, channel, payloadJSON string) {
	if events == nil || events.db == nil || sessionID == "" || eventType == "" {
		return
	}
	if payloadJSON == "" {
		payloadJSON = "{}"
	}
	if err := events.Append(ctx, messaging.SessionEvent{
		ID:                  uuid.NewString(),
		SessionID:           sessionID,
		EventType:           eventType,
		Channel:             channel,
		EnvelopePointerJSON: payloadJSON,
		CreatedAt:           time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		slog.Warn("messaging: write Nanite session event", "err", err,
			"session_id", sessionID, "event_type", eventType)
	}
}

type messagingHandoffCoordinator struct {
	db *sql.DB
}

func (coordinator *messagingHandoffCoordinator) Request(ctx context.Context, request messaging.HandoffRequest) (string, error) {
	if coordinator == nil || coordinator.db == nil {
		return "", fmt.Errorf("handoff coordinator is not configured")
	}
	switch request.RequestedBy {
	case "departing", "incoming", "user":
	default:
		return "", fmt.Errorf("%w: requested_by must be one of departing|incoming|user", messaging.ErrValidation)
	}
	id := uuid.NewString()
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := coordinator.db.ExecContext(ctx, `
		INSERT INTO session_handoffs (id, session_id, from_agent_id, to_agent_id, requested_by, status, requested_at)
		VALUES (?, ?, ?, ?, ?, 'pending', ?)
	`, id, request.SessionID, nullableMessagingString(request.FromAgentID), request.ToAgentID, request.RequestedBy, now)
	if err != nil {
		return "", fmt.Errorf("insert handoff: %w", err)
	}
	return id, nil
}

func (coordinator *messagingHandoffCoordinator) Approve(ctx context.Context, handoffID string) error {
	if coordinator == nil || coordinator.db == nil {
		return fmt.Errorf("handoff coordinator is not configured")
	}
	tx, err := coordinator.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin handoff transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var sessionID, toAgentID, status string
	err = tx.QueryRowContext(ctx, `
		SELECT session_id, to_agent_id, status FROM session_handoffs WHERE id = ?
	`, handoffID).Scan(&sessionID, &toAgentID, &status)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: handoff %s", messaging.ErrNotFound, handoffID)
		}
		return fmt.Errorf("read handoff: %w", err)
	}
	switch status {
	case "completed":
		return tx.Commit()
	case "rejected":
		return fmt.Errorf("%w: handoff %s is rejected", messaging.ErrValidation, handoffID)
	case "pending", "approved":
	default:
		return fmt.Errorf("handoff %s has unexpected status %q", handoffID, status)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := tx.ExecContext(ctx,
		`UPDATE session_agents SET is_primary = 0 WHERE session_id = ? AND is_primary = 1`, sessionID); err != nil {
		return fmt.Errorf("clear primary: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO session_agents (session_id, agent_id, mode, joined_at, is_primary)
		VALUES (?, ?, 'default', ?, 1)
		ON CONFLICT(session_id, agent_id) DO UPDATE SET is_primary = 1
	`, sessionID, toAgentID, now); err != nil {
		return fmt.Errorf("set new primary: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE session_handoffs
		SET status = 'completed', approved_at = ?, approved_by_user = 1
		WHERE id = ?
	`, now, handoffID); err != nil {
		return fmt.Errorf("update handoff: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE session_handoffs
		SET status = 'rejected', notes = 'superseded by handoff ' || ?
		WHERE session_id = ? AND status = 'pending' AND id != ?
	`, handoffID, sessionID, handoffID); err != nil {
		return fmt.Errorf("reject superseded handoffs: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit handoff: %w", err)
	}
	return nil
}

func (coordinator *messagingHandoffCoordinator) Reject(ctx context.Context, handoffID, reason string) error {
	if coordinator == nil || coordinator.db == nil {
		return fmt.Errorf("handoff coordinator is not configured")
	}
	_, err := coordinator.db.ExecContext(ctx, `
		UPDATE session_handoffs
		SET status = 'rejected', notes = ?
		WHERE id = ? AND status = 'pending'
	`, reason, handoffID)
	if err != nil {
		return fmt.Errorf("reject handoff: %w", err)
	}
	return nil
}

func nullableMessagingString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

var (
	_ messaging.AgentResolver      = (*messagingAgentRegistry)(nil)
	_ messaging.AgentRegistrar     = (*messagingAgentRegistry)(nil)
	_ messaging.EventStore         = (*SessionEvents)(nil)
	_ messaging.HandoffCoordinator = (*messagingHandoffCoordinator)(nil)
)
