package context

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// HandoffEnvelopeSchemaGlass4 identifies a stash payload authored under the
// Glass-4 self-handoff contract (CW-20260502-0015). Discriminates Glass-4
// stashes from the legacy P7 HandoffStashPayload (CW-20260420-0024) — both
// share the handoff_stashes table, payload column.
const HandoffEnvelopeSchemaGlass4 = "glass-4"

// HandoffEnvelope wraps a HandoffPayload with a schema discriminator so the
// reader can distinguish Glass-4 self-authored handoffs from legacy P7 stashes
// in the same handoff_stashes table. Future schema versions add a new const
// and bump Schema; readers should reject unknown schemas rather than guess.
type HandoffEnvelope struct {
	Schema  string         `json:"schema"`
	Payload HandoffPayload `json:"payload"`
}

// ErrHandoffEnvelopeWrongSchema is returned by ParseHandoffEnvelope when the
// payload is well-formed JSON but not a Glass-4 envelope (e.g., a legacy P7
// stash row, or a future schema version this binary doesn't understand).
var ErrHandoffEnvelopeWrongSchema = errors.New("handoff: not a glass-4 envelope")

// MarshalHandoffEnvelope produces the JSON bytes to persist in
// handoff_stashes.payload for a Glass-4 stash. Validates the payload first so
// invalid handoffs never hit the store; the caller doesn't have to call
// ValidateHandoff separately.
func MarshalHandoffEnvelope(payload HandoffPayload) ([]byte, error) {
	if _, err := ValidateHandoff(marshalPayloadForValidation(payload)); err != nil {
		return nil, err
	}
	env := HandoffEnvelope{
		Schema:  HandoffEnvelopeSchemaGlass4,
		Payload: payload,
	}
	return json.Marshal(env)
}

// ParseHandoffEnvelope decodes a stored handoff_stashes.payload row into a
// validated HandoffPayload. Returns ErrHandoffEnvelopeWrongSchema when the
// row is well-formed JSON but not a Glass-4 envelope (legacy P7 rows trip
// this — readers should fall back to the legacy P7 path or skip the row).
// Returns ErrHandoffMalformed when the bytes do not parse as JSON at all,
// so callers can distinguish corruption from a schema mismatch.
func ParseHandoffEnvelope(raw []byte) (*HandoffPayload, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("%w: empty payload", ErrHandoffMalformed)
	}
	var probe struct {
		Schema string `json:"schema"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrHandoffMalformed, err)
	}
	if probe.Schema == "" {
		return nil, ErrHandoffEnvelopeWrongSchema
	}
	if probe.Schema != HandoffEnvelopeSchemaGlass4 {
		return nil, fmt.Errorf("%w: schema=%q", ErrHandoffEnvelopeWrongSchema, probe.Schema)
	}
	var env HandoffEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrHandoffMalformed, err)
	}
	// Round-trip through ValidateHandoff so caps/required-fields are enforced
	// at read time too — protects against payloads written by a future binary
	// with relaxed rules.
	body, err := json.Marshal(env.Payload)
	if err != nil {
		return nil, fmt.Errorf("%w: re-marshal: %w", ErrHandoffMalformed, err)
	}
	return ValidateHandoff(body)
}

// RenderHandoffForSlot formats a validated HandoffPayload as the
// system-prompt-shaped block we ship in SlotHandoff post-compaction. The
// agent reads this as the first thing in its post-compaction context window.
//
// Wording choices matter: "loaded" (not "found" or "available") tells the
// agent the content is authoritative; "use handoff_pointers_expand"
// names the discovery tool with both required args (session_id, cache_key)
// so the agent doesn't have to guess and the call doesn't fail validation.
func RenderHandoffForSlot(p HandoffPayload) string {
	var b strings.Builder
	b.WriteString("Your handoff from before compaction is loaded. ")
	b.WriteString("Last session intent: ")
	b.WriteString(p.SessionIntent)
	b.WriteString(".\n")

	if len(p.RecentDecisions) > 0 {
		b.WriteString("Recent decisions:\n")
		for _, d := range p.RecentDecisions {
			b.WriteString("- ")
			b.WriteString(d)
			b.WriteByte('\n')
		}
	}

	if len(p.ActivePointers) > 0 {
		b.WriteString("Active pointers (use `handoff_pointers_expand({session_id, cache_key})` to retrieve heavy artifacts; both args are required):\n")
		for _, ptr := range p.ActivePointers {
			b.WriteString("- ")
			b.WriteString(ptr.Label)
			b.WriteString(": ")
			b.WriteString(ptr.Purpose)
			if ptr.CacheKey != "" {
				b.WriteString(" [cache_key=")
				b.WriteString(ptr.CacheKey)
				b.WriteString("]")
			}
			b.WriteByte('\n')
		}
	}

	b.WriteString("Next step anchor: ")
	b.WriteString(p.NextStepAnchor)
	return b.String()
}

// marshalPayloadForValidation serializes payload so ValidateHandoff can
// consume it. ValidateHandoff accepts JSON-or-YAML input bytes; we always
// feed it JSON because the in-memory struct is the source of truth.
func marshalPayloadForValidation(p HandoffPayload) []byte {
	data, err := json.Marshal(p)
	if err != nil {
		// HandoffPayload is plain string/slice/struct — marshal cannot fail
		// in practice. Returning empty makes ValidateHandoff produce a clear
		// "empty input" error rather than panicking.
		return nil
	}
	return data
}
