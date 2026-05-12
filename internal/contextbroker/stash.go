// Package contextbroker — slot-content stash.
//
// SP-20260512-0008 W2C (CW-20260512-0110): when a slot's content exceeds
// its budget, the assembly decider substitutes a pointer envelope and
// stashes the full content in the artifact store so the agent can pull
// it back via `dev_read` (passing the pointer's artifact_id).
//
// Atomicity contract (load-bearing per the ticket's sharp edges):
//   - Stash MUST complete BEFORE the envelope ships. A pointer envelope
//     that references a non-existent artifact would be a fabrication.
//   - If the stash write fails, the broker MUST NOT emit a pointer. The
//     decider falls back to shipping the content inline (ActionShip) and
//     logs a warning. This keeps the wire correct at the cost of cache
//     pressure — never the other way around.
//   - Stash writes are content-addressed: same slot + same content →
//     same artifact_id. Cache-stable pointer envelopes when content
//     doesn't change turn-to-turn (e.g. AGENTS.md walk-up).
package contextbroker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

// SlotStasher is the broker's outbound port for persisting oversized slot
// content. Implementations write the content to the artifact store and
// return an opaque artifact_id the pointer envelope embeds. Implementations
// MUST be deterministic on (sessionID, slotName, content): same inputs
// produce the same artifact_id and are idempotent on re-stash. The
// concrete implementation in internal/service uses content-addressed IDs
// (sha256 prefix) so re-runs of an unchanged AGENTS.md walk-up produce a
// stable pointer envelope across turns — preserving the cacheable prefix.
//
// Returning an error signals atomicity failure: the broker reverts to
// shipping the content inline (ActionShip) rather than emitting a
// pointer to a non-existent artifact.
type SlotStasher interface {
	StashSlot(ctx context.Context, req StashRequest) (StashResult, error)
}

// StashRequest carries the inputs the stasher needs to persist a slot's
// content and produce a deterministic artifact ID.
type StashRequest struct {
	SessionID string // owner — required for FK + storage path namespacing
	SlotName  string // e.g. "context", "memory" — informational + ID input
	Content   string // the full slot body (may be 50K+ lines)
	Tokens    int    // pre-computed token estimate (for the pointer envelope)
}

// StashResult is what the stasher returns on success. ArtifactID is the
// opaque ID the pointer envelope embeds. Reused (when present, true)
// signals the artifact already existed for this (session, content) pair —
// telemetry only; the broker treats it identically to a fresh write.
type StashResult struct {
	ArtifactID string
	Reused     bool
}

// ErrStashUnavailable is returned by stasher implementations when the
// stash backend is not configured (e.g. tests that didn't wire the
// artifact store). The decider treats this as a graceful fallback to
// ActionShip — no panic, no pointer to nowhere.
var ErrStashUnavailable = errors.New("contextbroker: slot stasher unavailable")

// nopStasher returns ErrStashUnavailable for every call. Used as the
// decider's default when no stasher is wired, so the decider's logic is
// uniform: it always asks for a stash, and the stasher decides whether
// to provide one. Tests without a stash backend get inline-shipped
// content without code-path branching in the decider.
type nopStasher struct{}

// StashSlot for nopStasher always returns ErrStashUnavailable.
func (nopStasher) StashSlot(_ context.Context, _ StashRequest) (StashResult, error) {
	return StashResult{}, ErrStashUnavailable
}

// NopStasher returns a SlotStasher whose StashSlot always errors with
// ErrStashUnavailable. Wire this when no artifact store is available
// (tests, headless runs). The decider falls back to ActionShip with
// full content rather than emitting a pointer to nowhere.
func NopStasher() SlotStasher { return nopStasher{} }

// DeterministicArtifactID returns the canonical ID format the broker
// embeds in pointer envelopes. Format: `art-stash-<sha256[:16]>` where
// the hash inputs are the session ID, the slot name, and the content
// bytes — keyed in that order with NUL separators so the same content
// landing in different slots produces different IDs (positional cache
// math still works).
//
// Stable across processes — no time, no PID, no UUID inputs. Two runs
// of the same session over the same content produce the same ID, which
// is exactly what the cacheable-prefix math wants (Anthropic's
// cacheable_prefix_tokens treats identical prefix bytes as cache-eligible).
//
// Exposed so the artifact-store-backed stasher and the decider's
// downstream verification can agree on ID shape without re-implementing
// the hash.
func DeterministicArtifactID(sessionID, slotName, content string) string {
	h := sha256.New()
	h.Write([]byte(sessionID))
	h.Write([]byte{0})
	h.Write([]byte(slotName))
	h.Write([]byte{0})
	h.Write([]byte(content))
	sum := h.Sum(nil)
	return "art-stash-" + hex.EncodeToString(sum[:8]) // 16 hex chars
}
