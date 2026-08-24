package chat

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hollis-labs/nanite/internal/store"
)

// tableCardAction mirrors the shape of a single entry in table-card.schema.json's
// $defs/action. Only ID is needed server-side — label/style/confirm are pure
// rendering hints the frontend already consumed before this handler runs.
type tableCardAction struct {
	ID string `json:"id"`
}

// tableCardColumn mirrors a table-card column definition, including its
// optional column-scoped actions.
type tableCardColumn struct {
	Key     string            `json:"key"`
	Actions []tableCardAction `json:"actions,omitempty"`
}

// tableCardPayload mirrors the subset of table-card's data shape this
// handler needs to validate a response against — the columns/rows/actions
// the table was actually EMITTED with, read from the server-persisted
// EnvelopeInstance.EnvelopeJSON, never from the client-supplied response.
type tableCardPayload struct {
	Rows    []map[string]any  `json:"rows"`
	Columns []tableCardColumn `json:"columns"`
	Actions []tableCardAction `json:"actions,omitempty"`
}

// TableCardActionHandler dispatches a row/column action response submitted
// against an interactive table-card envelope (Phase 6 — interactive-table
// row-actions primitive; see docs/engineering/architecture/08-cards.md).
//
// Unlike subagent-spawn-approval (which drives one specific backend side
// effect), table-card is a generic, reusable primitive with no built-in
// business logic of its own. This handler's job is purely defensive and
// generic: confirm the submitted action_id (and, for column-scoped actions,
// column_key) actually names an action the SERVER declared on emit — not
// just whatever a buggy or hostile client happened to POST — resolve
// row_index against the server-persisted row data, and surface the
// validated selection into the transcript so the agent sees exactly what
// was picked on its next turn.
//
// A plugin or backend consumer that wants a real side effect for one
// specific table registers its own ResponseHandler for "table-card" via
// RegisterResponseHandler, which overwrites this default for that process
// (see envelope_handler.go's registration semantics) — table-card keeps
// exactly one active handler per process, same as every other type.
type TableCardActionHandler struct{}

// NewTableCardActionHandler returns a stateless TableCardActionHandler.
func NewTableCardActionHandler() *TableCardActionHandler {
	return &TableCardActionHandler{}
}

func (h *TableCardActionHandler) HandleResponse(_ context.Context, env store.EnvelopeInstance, resp ResponseV1) (HandlerResult, error) {
	actionID, _ := resp.Data["action_id"].(string)
	if actionID == "" {
		return HandlerResult{}, fmt.Errorf("table-card: response data.action_id is required")
	}

	var payload tableCardPayload
	if err := json.Unmarshal([]byte(env.EnvelopeJSON), &payload); err != nil {
		return HandlerResult{}, fmt.Errorf("table-card: parse persisted envelope: %w", err)
	}

	columnKeyRaw, hasColumn := resp.Data["column_key"]
	columnKey, _ := columnKeyRaw.(string)
	hasColumn = hasColumn && columnKey != ""

	declared := payload.Actions
	if hasColumn {
		col, ok := findColumn(payload.Columns, columnKey)
		if !ok {
			return HandlerResult{}, fmt.Errorf("table-card: unknown column_key %q", columnKey)
		}
		declared = col.Actions
	}
	if !actionDeclared(declared, actionID) {
		return HandlerResult{}, fmt.Errorf("table-card: action_id %q is not declared on this table", actionID)
	}

	transcriptData := map[string]any{"action_id": actionID}
	if hasColumn {
		transcriptData["column_key"] = columnKey
	}

	rowSuffix := ""
	if riRaw, ok := resp.Data["row_index"]; ok {
		ri, ok := asInt(riRaw)
		if !ok {
			return HandlerResult{}, fmt.Errorf("table-card: row_index must be a number")
		}
		if ri < 0 || ri >= len(payload.Rows) {
			return HandlerResult{}, fmt.Errorf("table-card: row_index %d out of range (0-%d)", ri, len(payload.Rows)-1)
		}
		transcriptData["row_index"] = ri
		// Row content comes from the SERVER-persisted row, not the
		// client-supplied one, so a tampered client payload can't smuggle
		// altered row data into the transcript.
		transcriptData["row"] = payload.Rows[ri]
		rowSuffix = fmt.Sprintf(" (row %d)", ri)
	}

	return HandlerResult{
		TranscriptData: transcriptData,
		FollowUp:       fmt.Sprintf("Table action %q submitted%s.", actionID, rowSuffix),
	}, nil
}

func findColumn(cols []tableCardColumn, key string) (tableCardColumn, bool) {
	for _, c := range cols {
		if c.Key == key {
			return c, true
		}
	}
	return tableCardColumn{}, false
}

func actionDeclared(actions []tableCardAction, id string) bool {
	for _, a := range actions {
		if a.ID == id {
			return true
		}
	}
	return false
}

// asInt coerces a decoded JSON number (float64 via encoding/json, or an int
// if the caller constructed ResponseV1.Data in Go directly) into an int.
func asInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case json.Number:
		i, err := n.Int64()
		return int(i), err == nil
	default:
		return 0, false
	}
}
