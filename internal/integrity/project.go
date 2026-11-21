package integrity

import (
	"encoding/json"

	"github.com/hollis-labs/nanite/internal/transcript"
)

// Project returns the index a correct projection of entries would hold:
// the conversation's turns, messages and compactions, with the cursor of the
// last entry. It follows the store's projection rule for rule (first write
// of an ID wins, a finish sets the status it carries, interrupt and resume
// apply only from running and interrupted), so a disagreement with the real
// index means the index is wrong, not that the two read records differently.
// Records the projection would refuse are skipped. Notices are not part of
// the transcript, so the result has none.
func Project(conversationID string, entries []transcript.Entry) Index {
	var out Index
	turns := map[string]int{}
	messages := map[string]bool{}
	compactions := map[string]bool{}
	for _, entry := range entries {
		var r record
		if json.Unmarshal(entry.Record, &r) != nil || (conversationID != "" && r.SessionID != conversationID) {
			continue
		}
		switch r.Subtype {
		case subtypeTurnStart, subtypeTurnFinish, subtypeTurnInt, subtypeTurnResume:
			p, err := decodePayload[turnPayload](r.Payload)
			if err != nil {
				continue
			}
			i, known := turns[p.ID]
			switch {
			case r.Subtype == subtypeTurnStart && !known:
				turns[p.ID] = len(out.Turns)
				out.Turns = append(out.Turns, IndexTurn{ID: p.ID, Status: statusRunning, UserMessageID: p.UserMessageID})
			case !known:
			case r.Subtype == subtypeTurnResume && out.Turns[i].Status == statusInterrupted:
				out.Turns[i].Status = statusRunning
			case r.Subtype == subtypeTurnInt && out.Turns[i].Status == statusRunning:
				out.Turns[i].Status = statusInterrupted
			case r.Subtype == subtypeTurnFinish:
				out.Turns[i].Status = p.Status
			}
		case subtypeCompaction:
			p, err := decodePayload[compactionPayload](r.Payload)
			if err != nil || p.ThroughMessageID == "" || p.Summary == "" || compactions[r.UUID] {
				continue
			}
			compactions[r.UUID] = true
			out.Compactions = append(out.Compactions, IndexCompaction{ID: r.UUID, TurnID: p.TurnID, ThroughMessageID: p.ThroughMessageID})
		}
		if r.Type == "user" || r.Type == "assistant" {
			m, err := projected(r)
			if err != nil || messages[m.id] {
				continue
			}
			messages[m.id] = true
			out.Messages = append(out.Messages, IndexMessage{ID: m.id, TurnID: m.turn, Role: m.indexRole()})
		}
	}
	if n := len(entries); n > 0 {
		cursor := entries[n-1].Cursor
		out.Cursor = &cursor
	}
	return out
}
