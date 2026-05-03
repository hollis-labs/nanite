package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/hollis-labs/nanite/internal/chat"
	ctxpkg "github.com/hollis-labs/nanite/internal/context"
	"github.com/hollis-labs/nanite/internal/store"
)

// WriteGlass4Handoff validates and persists a self-authored Glass-4 handoff
// for the given session. Returns the stash_id (used as the cache_key the
// agent can reference in its `active_pointers` follow-ups, and what
// post-compaction inject reads back).
//
// Validates via ctxpkg.ValidateHandoff before any write — corrupted handoff
// is worse than no handoff because the post-compaction inject treats the
// payload as authoritative. ErrHandoff* sentinels propagate unchanged so
// callers can branch on cause.
func WriteGlass4Handoff(s HandoffStashStore, sessionID string, payload ctxpkg.HandoffPayload) (string, error) {
	if sessionID == "" {
		return "", fmt.Errorf("write glass-4 handoff: session_id is required")
	}
	envelopeBytes, err := ctxpkg.MarshalHandoffEnvelope(payload)
	if err != nil {
		return "", err
	}
	stashID := uuid.New().String()
	if err := s.UpsertHandoffStash(store.HandoffStash{
		ID:        stashID,
		SessionID: sessionID,
		Payload:   string(envelopeBytes),
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		return "", fmt.Errorf("write glass-4 handoff: %w", err)
	}
	return stashID, nil
}

// ReadLatestGlass4Handoff returns the most recent Glass-4 handoff for a
// session, or (nil, "", nil) when none exists (legacy P7 row, NULL session,
// or the stash genuinely is missing). On unexpected DB / parse errors the
// error is returned so the caller can decide whether to log and continue.
//
// "Latest stash row exists but isn't a Glass-4 envelope" is NOT an error —
// it means the session never wrote a Glass-4 handoff (or P7 wrote later,
// which Glass-4's compaction-path discipline prevents but legacy data may
// reflect). The caller treats this as "no handoff to inject" and skips.
func ReadLatestGlass4Handoff(s HandoffStashStore, sessionID string) (*ctxpkg.HandoffPayload, string, error) {
	if sessionID == "" {
		return nil, "", nil
	}
	row, err := s.GetLatestStashForSession(sessionID)
	if err != nil {
		if errors.Is(err, store.ErrHandoffStashNotFound) {
			return nil, "", nil
		}
		return nil, "", fmt.Errorf("read glass-4 handoff: %w", err)
	}
	payload, err := ctxpkg.ParseHandoffEnvelope([]byte(row.Payload))
	if err != nil {
		if errors.Is(err, ctxpkg.ErrHandoffEnvelopeWrongSchema) {
			// Legacy P7 row (or future schema). Not a Glass-4 handoff.
			return nil, "", nil
		}
		return nil, "", fmt.Errorf("read glass-4 handoff: parse: %w", err)
	}
	return payload, row.ID, nil
}

// InjectGlass4HandoffSlot reads the latest Glass-4 handoff for a session,
// renders it for SlotHandoff, and populates the slot on the assembled
// window (AutoInject=true so the LLM cannot strip it). When ch is non-nil
// and a handoff was injected, a `handoff_loaded` SSE event is emitted.
//
// No-ops with no error when:
//   - sessionID is empty
//   - no Glass-4 stash exists for the session
//   - the assembly window does not have a SlotHandoff (defensive)
//
// Returns (true, nil) on successful inject; (false, nil) on no-op.
// Glass-4 (CW-20260502-0015).
func InjectGlass4HandoffSlot(
	store HandoffStashStore,
	window *ctxpkg.ContextWindow,
	sessionID string,
	ch chan chat.StreamEvent,
) (bool, error) {
	if window == nil || sessionID == "" {
		return false, nil
	}
	payload, stashID, err := ReadLatestGlass4Handoff(store, sessionID)
	if err != nil {
		return false, err
	}
	if payload == nil {
		return false, nil
	}

	rendered := ctxpkg.RenderHandoffForSlot(*payload)
	window.SetContent(ctxpkg.SlotHandoff, rendered)
	// AutoInject=true — Glass-3 contract: harness writes this slot, LLM cannot strip.
	window.SetFlags(ctxpkg.SlotHandoff, ctxpkg.SlotFlags{AutoInject: true})

	slog.Info("handoff: post-compaction inject",
		"session_id", sessionID,
		"stash_id", stashID,
		"recent_decisions", len(payload.RecentDecisions),
		"active_pointers", len(payload.ActivePointers),
	)

	if ch != nil {
		emitHandoffLoaded(ch, stashID, *payload)
	}
	return true, nil
}

// emitHandoffLoaded sends a handoff_loaded SSE event. Best-effort — a full
// stream channel just means the FE missed the event; the slot itself is
// already populated.
func emitHandoffLoaded(ch chan chat.StreamEvent, stashID string, p ctxpkg.HandoffPayload) {
	type pointerSummary struct {
		Label   string `json:"label"`
		Purpose string `json:"purpose"`
	}
	body := struct {
		CacheKey       string           `json:"cache_key"`
		SessionIntent  string           `json:"session_intent"`
		NextStepAnchor string           `json:"next_step_anchor"`
		Pointers       []pointerSummary `json:"pointers,omitempty"`
	}{
		CacheKey:       stashID,
		SessionIntent:  p.SessionIntent,
		NextStepAnchor: p.NextStepAnchor,
	}
	for _, ptr := range p.ActivePointers {
		body.Pointers = append(body.Pointers, pointerSummary{Label: ptr.Label, Purpose: ptr.Purpose})
	}
	data, err := json.Marshal(body)
	if err != nil {
		slog.Warn("handoff_loaded: marshal failed (event not emitted)", "err", err)
		return
	}
	select {
	case ch <- chat.StreamEvent{Type: "handoff_loaded", Data: string(data)}:
	default:
		slog.Warn("handoff_loaded: stream channel full, event dropped")
	}
}

// IsLongRunning reports whether session.Intent indicates a long-running
// session. Used as the gate for both the proactive Glass-4 stash hook and
// the post-compaction inject — per-turn / ephemeral / unclassified
// sessions never carry a Glass-4 handoff.
func IsLongRunning(sess *store.Session) bool {
	if sess == nil || sess.Intent == nil {
		return false
	}
	return *sess.Intent == store.SessionIntentLongRunning
}
