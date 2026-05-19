// Package messaging — envelope_bridge.go
//
// The bridge between Nanite's legacy tuple-addressed Message and the
// portfolio-shared go-messaging Envelope. It lets the divergent legacy
// representation (the `agent_messages` table) interoperate with the
// shared contract during the migration: legacy rows can be projected
// into Envelopes for federated routing, and inbound Envelopes can be
// folded back into the legacy shape for the existing front-ends.
//
// The mapping is lossless. go-messaging's Envelope has no Subject /
// Body / Priority / Status / Type fields — those legacy columns are
// preserved in Envelope.Metadata under the "nanite." prefix and
// recovered by FromEnvelope, so a Message → Envelope → Message
// round-trip reproduces the original.
package messaging

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	messaging "github.com/hollis-labs/go-messaging"
	"github.com/hollis-labs/nanite/internal/messaging/gomsg"
)

// Metadata keys carrying legacy Message columns that have no native
// go-messaging Envelope field. Namespaced so they never collide with a
// caller's own metadata.
const (
	metaType     = "nanite.type"
	metaSubject  = "nanite.subject"
	metaBody     = "nanite.body"
	metaPriority = "nanite.priority"
	metaStatus   = "nanite.status"
	metaMetadata = "nanite.metadata"
)

// ToEnvelope projects a legacy Message onto a go-messaging Envelope
// under the given authority (empty → gomsg.DefaultAuthority). The
// returned Envelope is a faithful translation of an existing row — it
// is not Send-ready (Send assigns its own ID/CreatedAt and rejects the
// preset lifecycle timestamps this function carries over).
func ToEnvelope(m *Message, authority string) (messaging.Envelope, error) {
	if m == nil {
		return messaging.Envelope{}, fmt.Errorf("messaging: ToEnvelope: nil message")
	}

	meta := map[string]string{
		metaType:     m.Type,
		metaSubject:  m.Subject,
		metaBody:     m.Body,
		metaPriority: strconv.Itoa(m.Priority),
		metaStatus:   m.Status,
	}
	if m.Metadata != "" && m.Metadata != "{}" {
		meta[metaMetadata] = m.Metadata
	}

	env := messaging.Envelope{
		ID:          m.ID,
		Kind:        gomsg.ToGoKind(m.Kind),
		Channel:     messaging.Channel(m.Channel),
		From:        gomsg.AgentAddress(authority, m.FromSessionID, m.FromAgentID),
		To:          gomsg.AgentAddress(authority, m.ToSessionID, m.ToAgentID),
		ThreadID:    m.ThreadID,
		InReplyTo:   m.ReplyTo,
		ContentType: "application/json",
		Metadata:    meta,
	}
	if m.PayloadJSON != "" && m.PayloadJSON != "{}" {
		env.Payload = json.RawMessage(m.PayloadJSON)
	}

	if m.CreatedAt != "" {
		t, err := time.Parse(time.RFC3339, m.CreatedAt)
		if err != nil {
			return messaging.Envelope{}, fmt.Errorf("messaging: ToEnvelope: created_at %q: %w", m.CreatedAt, err)
		}
		env.CreatedAt = t.UTC()
	}
	// Legacy has no "delivered" concept; a read message is the closest
	// equivalent of a consumed envelope.
	if m.ReadAt != nil && *m.ReadAt != "" {
		if t, err := time.Parse(time.RFC3339, *m.ReadAt); err == nil {
			ut := t.UTC()
			env.ConsumedAt = &ut
		}
	}
	return env, nil
}

// FromEnvelope folds a go-messaging Envelope back into the legacy
// Message shape. Legacy-only columns are recovered from the "nanite."
// metadata keys written by ToEnvelope; an Envelope minted elsewhere
// (no nanite metadata) still converts, falling back to sensible
// defaults (Type "message", Priority 2, Status "unread").
func FromEnvelope(env messaging.Envelope) (*Message, error) {
	fromSession, fromAgent, err := gomsg.Tuple(env.From)
	if err != nil {
		return nil, fmt.Errorf("messaging: FromEnvelope: from: %w", err)
	}
	toSession, toAgent, err := gomsg.Tuple(env.To)
	if err != nil {
		return nil, fmt.Errorf("messaging: FromEnvelope: to: %w", err)
	}

	m := &Message{
		ID:            env.ID,
		FromSessionID: fromSession,
		FromAgentID:   fromAgent,
		ToSessionID:   toSession,
		ToAgentID:     toAgent,
		ThreadID:      env.ThreadID,
		ReplyTo:       env.InReplyTo,
		Kind:          gomsg.FromGoKind(env.Kind),
		Channel:       string(env.Channel),
		Type:          TypeMessage,
		Metadata:      "{}",
		PayloadJSON:   "{}",
		Priority:      2,
		Status:        StatusUnread,
	}
	if env.Channel == "" {
		m.Channel = ChannelChat
	}
	if len(env.Payload) > 0 {
		m.PayloadJSON = string(env.Payload)
	}
	if !env.CreatedAt.IsZero() {
		m.CreatedAt = env.CreatedAt.UTC().Format(time.RFC3339)
	}

	if v, ok := env.Metadata[metaType]; ok && v != "" {
		m.Type = v
	}
	if v, ok := env.Metadata[metaSubject]; ok {
		m.Subject = v
	}
	if v, ok := env.Metadata[metaBody]; ok {
		m.Body = v
	}
	if v, ok := env.Metadata[metaStatus]; ok && v != "" {
		m.Status = v
	}
	if v, ok := env.Metadata[metaPriority]; ok {
		if p, perr := strconv.Atoi(v); perr == nil {
			m.Priority = p
		}
	}
	if v, ok := env.Metadata[metaMetadata]; ok && v != "" {
		m.Metadata = v
	}
	if env.ConsumedAt != nil {
		ra := env.ConsumedAt.UTC().Format(time.RFC3339)
		m.ReadAt = &ra
	}
	return m, nil
}
