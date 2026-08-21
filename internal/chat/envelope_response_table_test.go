package chat

import (
	"context"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// tableEnvelopeJSON builds the server-persisted table-card payload
// (columns/rows/actions) a real Emit() call would have stored — the same
// shape the handler must consult instead of trusting the client.
const tableEnvelopeJSON = `{
	"columns": [
		{"key": "id", "label": "ID"},
		{"key": "status", "label": "Status", "actions": [{"id": "resolve", "label": "Resolve"}]}
	],
	"rows": [
		{"id": "T-1", "status": "open"},
		{"id": "T-2", "status": "open"}
	],
	"actions": [{"id": "approve", "label": "Approve"}, {"id": "reject", "label": "Reject"}]
}`

func TestTableCardActionHandler_RowAction(t *testing.T) {
	h := NewTableCardActionHandler()
	env := store.EnvelopeInstance{EnvelopeType: "table-card", EnvelopeJSON: tableEnvelopeJSON}

	res, err := h.HandleResponse(context.Background(), env, ResponseV1{
		V: 1, Kind: "table-card", Status: StatusSubmitted,
		Data: map[string]any{"action_id": "approve", "row_index": float64(1)},
	})
	if err != nil {
		t.Fatalf("HandleResponse: %v", err)
	}
	if res.TranscriptData["action_id"] != "approve" {
		t.Errorf("action_id = %v", res.TranscriptData["action_id"])
	}
	if res.TranscriptData["row_index"] != 1 {
		t.Errorf("row_index = %v", res.TranscriptData["row_index"])
	}
	row, ok := res.TranscriptData["row"].(map[string]any)
	if !ok || row["id"] != "T-2" {
		t.Errorf("row = %v", res.TranscriptData["row"])
	}
	if res.FollowUp == "" {
		t.Error("expected non-empty FollowUp")
	}
}

func TestTableCardActionHandler_ColumnAction(t *testing.T) {
	h := NewTableCardActionHandler()
	env := store.EnvelopeInstance{EnvelopeType: "table-card", EnvelopeJSON: tableEnvelopeJSON}

	res, err := h.HandleResponse(context.Background(), env, ResponseV1{
		V: 1, Kind: "table-card", Status: StatusSubmitted,
		Data: map[string]any{"action_id": "resolve", "row_index": float64(0), "column_key": "status"},
	})
	if err != nil {
		t.Fatalf("HandleResponse: %v", err)
	}
	if res.TranscriptData["column_key"] != "status" {
		t.Errorf("column_key = %v", res.TranscriptData["column_key"])
	}
	if res.TranscriptData["action_id"] != "resolve" {
		t.Errorf("action_id = %v", res.TranscriptData["action_id"])
	}
}

func TestTableCardActionHandler_UndeclaredActionRejected(t *testing.T) {
	h := NewTableCardActionHandler()
	env := store.EnvelopeInstance{EnvelopeType: "table-card", EnvelopeJSON: tableEnvelopeJSON}

	_, err := h.HandleResponse(context.Background(), env, ResponseV1{
		V: 1, Status: StatusSubmitted,
		Data: map[string]any{"action_id": "delete-everything"},
	})
	if err == nil {
		t.Fatal("expected error for undeclared action_id, got nil")
	}
}

func TestTableCardActionHandler_ColumnActionRejectsRootAction(t *testing.T) {
	// "approve" is declared at root level, not on the "status" column —
	// posting it as a column-scoped response must be rejected.
	h := NewTableCardActionHandler()
	env := store.EnvelopeInstance{EnvelopeType: "table-card", EnvelopeJSON: tableEnvelopeJSON}

	_, err := h.HandleResponse(context.Background(), env, ResponseV1{
		V: 1, Status: StatusSubmitted,
		Data: map[string]any{"action_id": "approve", "column_key": "status"},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestTableCardActionHandler_MissingActionID(t *testing.T) {
	h := NewTableCardActionHandler()
	env := store.EnvelopeInstance{EnvelopeType: "table-card", EnvelopeJSON: tableEnvelopeJSON}

	_, err := h.HandleResponse(context.Background(), env, ResponseV1{
		V: 1, Status: StatusSubmitted, Data: map[string]any{},
	})
	if err == nil {
		t.Fatal("expected error for missing action_id, got nil")
	}
}

func TestTableCardActionHandler_RowIndexOutOfRange(t *testing.T) {
	h := NewTableCardActionHandler()
	env := store.EnvelopeInstance{EnvelopeType: "table-card", EnvelopeJSON: tableEnvelopeJSON}

	_, err := h.HandleResponse(context.Background(), env, ResponseV1{
		V: 1, Status: StatusSubmitted,
		Data: map[string]any{"action_id": "approve", "row_index": float64(9)},
	})
	if err == nil {
		t.Fatal("expected error for out-of-range row_index, got nil")
	}
}

func TestTableCardActionHandler_UnknownColumnKey(t *testing.T) {
	h := NewTableCardActionHandler()
	env := store.EnvelopeInstance{EnvelopeType: "table-card", EnvelopeJSON: tableEnvelopeJSON}

	_, err := h.HandleResponse(context.Background(), env, ResponseV1{
		V: 1, Status: StatusSubmitted,
		Data: map[string]any{"action_id": "resolve", "column_key": "nope"},
	})
	if err == nil {
		t.Fatal("expected error for unknown column_key, got nil")
	}
}
