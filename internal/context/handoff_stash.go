package context

import "context"

// HandoffStashPayload is the structured pre-compaction state blob. Fields are
// enumerated in D4 (P7, CW-20260420-0024). P8 CompactionContract
// (CW-20260420-0027) owns schema definition — do not add/remove top-level
// fields without an ADR coordinated with P8.
type HandoffStashPayload struct {
	DecisionsLocked []string `json:"decisions_locked"`
	OpenQuestions   []string `json:"open_questions"`
	ActiveFileRefs  []string `json:"active_file_refs"`
	ActiveTicketIDs []string `json:"active_ticket_ids"`
	ShouldReread    []string `json:"should_reread"`
}

// StashWriter persists a HandoffStash record. Implemented by the service layer
// (wrapping the store) so this package remains free of store imports.
type StashWriter interface {
	WriteHandoffStash(ctx context.Context, sessionID, stashID string, payload HandoffStashPayload) error
}

// BuildPayloadFromScratchpad extracts HandoffStashPayload fields from a
// scratchpad snapshot using well-known string-slice keys. Missing or
// wrongly-typed keys produce nil slices — the stash is always written.
func BuildPayloadFromScratchpad(scratchpad map[string]any) HandoffStashPayload {
	return HandoffStashPayload{
		DecisionsLocked: stringsFromScratchpad(scratchpad, "decisions_locked"),
		OpenQuestions:   stringsFromScratchpad(scratchpad, "open_questions"),
		ActiveFileRefs:  stringsFromScratchpad(scratchpad, "active_file_refs"),
		ActiveTicketIDs: stringsFromScratchpad(scratchpad, "active_ticket_ids"),
		ShouldReread:    stringsFromScratchpad(scratchpad, "should_reread"),
	}
}

func stringsFromScratchpad(scratchpad map[string]any, key string) []string {
	v, ok := scratchpad[key]
	if !ok {
		return nil
	}
	raw, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
