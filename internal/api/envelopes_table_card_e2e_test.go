package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/envelope"
	"github.com/hollis-labs/nanite/internal/store"
)

// TestTableCardRowAction_E2E exercises the full interactive-table row-action
// round trip through the production code path (Phase 6, interactive-table
// row-actions primitive — see docs/engineering/architecture/08-cards.md and
// TASKS/phase-6/04-build-interactive-table-row-actions-primitive.md):
//
//  1. emit  — build the exact data shape an interactive table-card carries
//     (columns/rows/root-level actions), the same shape TableCard.tsx
//     renders and the same shape a real Emit() call (the addressable,
//     EnvelopeInstance-persisting path approval-card/elicitation-prompt
//     already use) would have stored.
//  2. schema-valid — confirm that payload validates against the real
//     released go-envelopes table-card schema,
//     loaded via the same envelope.ValidateData path card_show uses).
//  3. render — this is the payload TableCard.tsx receives as `data` and
//     renders row-action buttons from.
//  4. click — build the exact ResponseV1 wire body TableCard.tsx's
//     onRespond() call POSTs when a row action button is clicked.
//  5. respond — POST /api/envelopes/{id}/respond through the real HTTP
//     mux (no test-only shortcuts).
//  6. backend handles — assert the registered table-card ResponseHandler
//     (wired in container.go, same registration mechanism
//     subagent-spawn-approval uses) validated the action against the
//     server-persisted table, resolved row content server-side, and the
//     transcript message + envelope instance both persisted correctly.
func TestTableCardRowAction_E2E(t *testing.T) {
	envelope.SetupForTesting()

	a, mux := newTestAPI(t)
	sessID := seedSessionForEnvelope(t, a)

	tableData := map[string]any{
		"title": "Open incidents",
		"columns": []any{
			map[string]any{"key": "id", "label": "ID"},
			map[string]any{"key": "summary", "label": "Summary"},
		},
		"rows": []any{
			map[string]any{"id": "INC-1", "summary": "Disk full on db-2"},
			map[string]any{"id": "INC-2", "summary": "Elevated 5xx on api"},
		},
		"actions": []any{
			map[string]any{"id": "acknowledge", "label": "Acknowledge"},
			map[string]any{"id": "escalate", "label": "Escalate", "style": "destructive", "confirm": true, "confirm_message": "Escalate this incident?"},
		},
	}

	// Step 2: the emitted data is real, schema-valid table-card data — not
	// a fixture that happens to be shaped correctly by accident.
	if err := envelope.ValidateData("table-card", tableData); err != nil {
		t.Fatalf("table-card payload with actions failed schema validation: %v", err)
	}

	// Step 1: persist the EnvelopeInstance the way a real interactive-table
	// emitter would (ApprovalEmitterImpl.Emit / ElicitationEmitterImpl —
	// the addressable, id-bearing emit path table-card's own passive
	// card_show path deliberately does not use).
	envJSON, err := json.Marshal(tableData)
	if err != nil {
		t.Fatalf("marshal table data: %v", err)
	}
	inst := &store.EnvelopeInstance{
		SessionID:    sessID,
		EnvelopeType: "table-card",
		EnvelopeJSON: string(envJSON),
	}
	if err := a.Services.Store.CreateEnvelopeInstance(context.Background(), inst); err != nil {
		t.Fatalf("CreateEnvelopeInstance: %v", err)
	}

	// Step 4: the exact wire body TableCard.tsx's onRespond() posts when the
	// user clicks the "Escalate" button on row 1 (INC-2) — action_id +
	// row_index only; no column_key since this is a root-level row action.
	body, err := json.Marshal(chat.ResponseV1{
		V: 1, Kind: "table-card", ID: inst.ID, Status: chat.StatusSubmitted,
		Data: map[string]any{"action_id": "escalate", "row_index": 1},
	})
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}

	// Step 5: respond.
	w := doPost(mux, "/api/envelopes/"+inst.ID+"/respond", body)
	if w.Code != http.StatusOK {
		t.Fatalf("respond: expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	var ack map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &ack); err != nil {
		t.Fatalf("unmarshal ack: %v", err)
	}
	if ack["ok"] != true {
		t.Fatalf("expected ok:true, got %+v", ack)
	}
	if ack["follow_up"] == "" || ack["follow_up"] == nil {
		t.Fatalf("expected a follow_up summarizing the action, got %+v", ack)
	}

	// Step 6: the envelope instance is now claimed/responded...
	got, err := a.Services.Store.GetEnvelopeInstance(context.Background(), inst.ID)
	if err != nil {
		t.Fatalf("GetEnvelopeInstance: %v", err)
	}
	if got.RespondedAt == nil || got.ResponseStatus != "submitted" {
		t.Fatalf("instance not updated: %+v", got)
	}

	// ...and the transcript message the next turn will see carries the
	// SERVER-resolved row content (INC-2), proving the handler read the
	// row from the persisted envelope rather than trusting a (here,
	// absent) client-supplied row payload.
	msgs, err := a.Services.Store.ListMessages(context.Background(), sessID, 10)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 transcript message, got %d", len(msgs))
	}
	if msgs[0].Role != chat.RoleEnvelopeResponse {
		t.Fatalf("role: %q", msgs[0].Role)
	}
	content := msgs[0].Content
	for _, want := range []string{`"action_id":"escalate"`, `"row_index":1`, `INC-2`} {
		if !containsStr2(content, want) {
			t.Fatalf("transcript message missing %q: %q", want, content)
		}
	}

	// A second click against the same envelope must be rejected (409) —
	// exactly the same duplicate-submission guard every other interactive
	// card gets via the generic /respond endpoint.
	w2 := doPost(mux, "/api/envelopes/"+inst.ID+"/respond", body)
	if w2.Code != http.StatusConflict {
		t.Fatalf("expected 409 on duplicate respond, got %d; body: %s", w2.Code, w2.Body.String())
	}
}

// TestTableCardRowAction_E2E_UndeclaredActionRejected proves the handler
// defends against a forged/tampered action_id that was never declared on
// the server-persisted table — the client can only fire an action the
// table actually rendered, not an arbitrary string.
func TestTableCardRowAction_E2E_UndeclaredActionRejected(t *testing.T) {
	envelope.SetupForTesting()

	a, mux := newTestAPI(t)
	sessID := seedSessionForEnvelope(t, a)

	tableData := map[string]any{
		"columns": []any{map[string]any{"key": "id", "label": "ID"}},
		"rows":    []any{map[string]any{"id": "T-1"}},
		"actions": []any{map[string]any{"id": "approve", "label": "Approve"}},
	}
	envJSON, _ := json.Marshal(tableData)
	inst := &store.EnvelopeInstance{SessionID: sessID, EnvelopeType: "table-card", EnvelopeJSON: string(envJSON)}
	if err := a.Services.Store.CreateEnvelopeInstance(context.Background(), inst); err != nil {
		t.Fatalf("CreateEnvelopeInstance: %v", err)
	}

	body, _ := json.Marshal(chat.ResponseV1{
		V: 1, Kind: "table-card", ID: inst.ID, Status: chat.StatusSubmitted,
		Data: map[string]any{"action_id": "delete-everything", "row_index": 0},
	})
	w := doPost(mux, "/api/envelopes/"+inst.ID+"/respond", body)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 (handler rejects undeclared action), got %d; body: %s", w.Code, w.Body.String())
	}
}
